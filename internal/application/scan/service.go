package scan

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"haeryong.local/haeryong/internal/application/auth"
	"haeryong.local/haeryong/internal/application/search"
	"haeryong.local/haeryong/internal/application/service"
	app "haeryong.local/haeryong/internal/application/storage"
	"haeryong.local/haeryong/internal/domain"
	sec "haeryong.local/haeryong/internal/security/session"
)

type Service struct {
	Store         app.Store
	Auth          *auth.Service
	Bytes         search.ObjectReader
	Wake          func()
	Now           func() time.Time
	OnSourceProbe func(projectID, versionID, sourceID, op string)
}

func New(store app.Store, a *auth.Service, bytes search.ObjectReader, wake func()) *Service {
	if bytes == nil {
		if b, ok := store.(search.ObjectReader); ok {
			bytes = b
		}
	}
	return &Service{
		Store: store,
		Auth:  a,
		Bytes: bytes,
		Wake:  wake,
		Now:   time.Now,
	}
}

type preparedDocEntry struct {
	Ordinal         int
	ProjectID       string
	DocumentID      string
	Lifecycle       string
	LatestVersionID string
	Version         *domain.Version
	PageCount       int
	RegionCount     int
	EntryCount      int
	Regionless      bool
	MetadataLess    bool
	ExclusionReason string
	PreparedSource  *preparedVersionSources
}

type admissionSnapshot struct {
	LibraryID            string
	AuthEpoch            string
	ConfigRevision       int64
	PermissionsRevisions map[string]int64
	RequestedScope       domain.ScanRequestedScope
	EffectivePolicy      domain.ScanPolicySnapshot
	Entries              []preparedDocEntry
}

var errAdmissionStateChanged = errors.New("admission state changed between preparation and commit")

func normalizeIDSlice(single string, multi []string) []string {
	seen := map[string]bool{}
	var out []string
	add := func(v string) {
		v = strings.TrimSpace(v)
		if v != "" && !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	add(single)
	for _, m := range multi {
		add(m)
	}
	return out
}

func (s *Service) StartScan(ctx context.Context, token, key string, rawBody []byte, req domain.ScanRequest) (domain.Response, error) {
	if strings.TrimSpace(req.FindingID) != "" || req.ExpectedFindingRevision != nil {
		return domain.Response{}, domain.ErrFeatureNotReady
	}
	actor, err := s.Auth.Authenticate(ctx, token)
	if err != nil {
		return domain.Response{}, err
	}
	if req.Scope != "" && req.Scope != "selected_projects" && req.Scope != "all_allowed_projects" {
		return domain.Response{}, domain.ErrInvalidTarget
	}
	normDepth, err := NormalizeDepth(req.Depth)
	if err != nil {
		return domain.Response{}, err
	}
	if req.SourceMode != "" && req.SourceMode != "pinned" && req.SourceMode != "current" {
		return domain.Response{}, domain.ErrInvalidTarget
	}
	pagePath := strings.TrimSpace(req.PagePath)
	if pagePath == "" {
		pagePath = strings.TrimSpace(req.PathPrefix)
	}
	if pagePath != "" && !domain.ValidReferencePath(&pagePath) {
		return domain.Response{}, domain.ErrInvalidTarget
	}
	regionID := strings.TrimSpace(req.RegionID)
	if regionID != "" && (pagePath == "" || !utf8.ValidString(regionID)) {
		return domain.Response{}, domain.ErrInvalidTarget
	}
	for _, sel := range req.Sources {
		if sel.Mode != "" && sel.Mode != "pinned" && sel.Mode != "current" {
			return domain.Response{}, domain.ErrInvalidTarget
		}
	}
	if len(rawBody) == 0 {
		rawBody = []byte("{}")
	}
	opReq := domain.Request{
		Key:    domain.OperationKey{PrincipalID: actor.PrincipalID, OperationID: key},
		Method: "POST",
		Target: "/api/v1/scans",
		Body:   rawBody,
	}
	if resp, found, err := app.Replay(ctx, s.Store, s.Auth.Authority(token), opReq); err != nil || found {
		return resp, err
	}

	var resp domain.Response
	var woke bool
	for attempt := 0; attempt < 3; attempt++ {
		nowStr := s.Now().UTC().Format(time.RFC3339Nano)
		var snap admissionSnapshot
		if err := s.Store.Write(ctx, func(tx app.WriteTx) error {
			var e error
			snap, e = s.prepareAdmissionSnapshot(ctx, tx, token, actor, opReq, req, normDepth, pagePath, regionID)
			return e
		}); err != nil {
			return domain.Response{}, err
		}

		// The immutable normalized manifest is authoritative for source intent.
		// Older imports did not project it into the small version summary.
		// Read explanation metadata outside the writer before choosing a family.
		if err := s.resolveAdmissionIntents(ctx, token, req, &snap); err != nil {
			return domain.Response{}, err
		}
		// Phase 2: Resolve source targets once before dispatch outside the SQLite lock.
		decisions := map[string]VersionSourceDecision{}
		newTargetSets := map[string]*domain.TargetSet{}
		for i := range snap.Entries {
			entry := &snap.Entries[i]
			if entry.PreparedSource == nil {
				continue
			}
			dec, newTS, err := resolvePreparedVersionSources(ctx, *entry.PreparedSource, nowStr)
			if err != nil {
				return domain.Response{}, err
			}
			decisions[entry.Version.ID] = dec
			if newTS != nil {
				newTargetSets[entry.Version.ID] = newTS
			}
		}

		// Phase 3: Atomic coalescing check, target-set persistence, and job/receipt commit.
		// Captured revisions/permissions/authority are strictly compared, never overwritten.
		var normPayload struct {
			ScopeHash   string   `json:"scope_hash"`
			ProjectIDs  []string `json:"project_ids"`
			DocumentIDs []string `json:"document_ids"`
		}
		resp, err = app.ExecuteNormalized(ctx, s.Store, s.Auth.Authority(token), opReq, func(tx app.WriteTx) ([]byte, error) {
			current, e := s.Auth.AuthenticateWrite(ctx, tx, token)
			if e != nil {
				return nil, e
			}
			if current.PrincipalID != actor.PrincipalID {
				return nil, domain.ErrDenied
			}
			if e := verifyAdmissionUnchanged(tx, current, snap); e != nil {
				return nil, e
			}
			roster, _, _ := buildCapturedRoster(snap, decisions, nowStr)
			scopeHash := ComputeAuthorizedScopeHash(
				current,
				snap.LibraryID,
				snap.RequestedScope,
				roster,
				snap.ConfigRevision,
				snap.PermissionsRevisions,
				snap.EffectivePolicy,
			)
			docIDs := make([]string, 0, len(roster))
			for _, r := range roster {
				if r.DocumentID != "" && r.ExclusionReason == "" {
					docIDs = append(docIDs, r.DocumentID)
				}
			}
			normPayload.ScopeHash = scopeHash
			normPayload.ProjectIDs = snap.RequestedScope.ProjectIDs
			normPayload.DocumentIDs = docIDs
			return json.Marshal(normPayload)
		}, func(tx app.WriteTx, r domain.Receipt) (domain.Response, error) {
			current, e := s.Auth.AuthenticateWrite(ctx, tx, token)
			if e != nil {
				return domain.Response{}, e
			}
			if e := verifyAdmissionUnchanged(tx, current, snap); e != nil {
				return domain.Response{}, e
			}
			roster, denom, primaryTS := buildCapturedRoster(snap, decisions, nowStr)
			scopeHash := ComputeAuthorizedScopeHash(
				current,
				snap.LibraryID,
				snap.RequestedScope,
				roster,
				snap.ConfigRevision,
				snap.PermissionsRevisions,
				snap.EffectivePolicy,
			)

			// Coalescing check against active queued/running/interrupted scans with identical authorized scope_hash.
			if activeJob, found, e := tx.ActiveCoalescedScan(scopeHash); e != nil {
				return domain.Response{}, e
			} else if found && activeJob.ID != "" {
				if activeRun, re := tx.ScanRun(activeJob.ID); re == nil && activeRun.PrincipalID == current.PrincipalID && activeRun.AuthKind == current.Scope.Kind {
					activeTS := primaryTS
					if activeRun.TargetSetID != "" {
						activeTS = activeRun.TargetSetID
					}
					body, _ := json.Marshal(map[string]any{
						"operation_id":  key,
						"job_id":        activeJob.ID,
						"job_url":       "/api/v1/jobs/" + activeJob.ID,
						"coalesced":     true,
						"target_set_id": nullableStr(activeTS),
					})
					woke = true
					return domain.Response{Status: 202, Body: body}, nil
				}
			}

			jobID, e := service.NewID()
			if e != nil {
				return domain.Response{}, e
			}
			activeDocIDs := make([]string, 0, len(roster))
			activeVerIDs := make([]string, 0, len(roster))
			for i := range roster {
				roster[i].JobID = jobID
				if roster[i].ExclusionReason == "" {
					if roster[i].DocumentID != "" {
						activeDocIDs = append(activeDocIDs, roster[i].DocumentID)
					}
					if roster[i].VersionID != "" {
						activeVerIDs = append(activeVerIDs, roster[i].VersionID)
					}
				}
				if ts := newTargetSets[roster[i].VersionID]; ts != nil {
					if err := tx.PutTargetSet(*ts); err != nil {
						return domain.Response{}, err
					}
				}
			}

			singleDoc := ""
			if len(snap.RequestedScope.DocumentIDs) == 1 {
				singleDoc = snap.RequestedScope.DocumentIDs[0]
			}
			scopeMeta, _ := json.Marshal(map[string]any{
				"principal_id":        current.PrincipalID,
				"auth_epoch":          snap.AuthEpoch,
				"auth_kind":           current.Scope.Kind,
				"credential_scope":    current.Scope,
				"project_ids":         snap.RequestedScope.ProjectIDs,
				"document_id":         singleDoc,
				"document_ids":        snap.RequestedScope.DocumentIDs,
				"active_document_ids": activeDocIDs,
				"active_version_ids":  activeVerIDs,
				"version_id":          snap.RequestedScope.VersionID,
				"path_prefix":         snap.RequestedScope.PathPrefix,
				"region_id":           snap.RequestedScope.RegionID,
				"depth":               snap.EffectivePolicy.Depth,
				"target_set_id":       primaryTS,
				"scope_hash":          scopeHash,
			})
			job := domain.Job{
				ID:             jobID,
				Kind:           "scan",
				Scope:          scopeMeta,
				ConfigRevision: snap.ConfigRevision,
				Permissions:    snap.PermissionsRevisions,
			}
			if err := tx.CreateJob(job); err != nil {
				return domain.Response{}, err
			}
			pinnedIDs := map[string]bool{}
			for i := range roster {
				if roster[i].VersionID == "" {
					continue
				}
				dec := decisions[roster[i].VersionID]
				for _, objHash := range dec.RetainedObjectHashes {
					if objHash == "" {
						continue
					}
					pinID := "scan:" + jobID + ":" + roster[i].VersionID + ":" + objHash
					if pinnedIDs[pinID] {
						continue
					}
					if obj, oe := tx.Object(objHash); oe == nil && obj.State == "ready" {
						if err := tx.AcquirePin(domain.Pin{ID: pinID, Kind: "job", OwnerID: jobID, Hash: objHash}); err != nil {
							return domain.Response{}, err
						}
						pinnedIDs[pinID] = true
					}
				}
			}

			initialReport := initialScanReport(jobID, primaryTS, snap.ConfigRevision, snap.PermissionsRevisions, snap.RequestedScope, snap.EffectivePolicy, denom, roster)
			run := domain.ScanRun{
				JobID:                jobID,
				PrincipalID:          current.PrincipalID,
				AuthKind:             current.Scope.Kind,
				CapturedScope:        current.Scope,
				ScopeHash:            scopeHash,
				TargetSetID:          primaryTS,
				RequestedScope:       snap.RequestedScope,
				EffectivePolicy:      snap.EffectivePolicy,
				ConfigRevision:       snap.ConfigRevision,
				PermissionsRevisions: snap.PermissionsRevisions,
				Stage:                "inventory",
				EnumVersionOrdinal:   0,
				EnumAfterPath:        "",
				EnumAfterRegion:      "",
				EnumerationComplete:  false,
				NextItemOrdinal:      1,
				ElapsedMs:            0,
				Denominator:          denom,
				Report:               initialReport,
				CapturedAt:           nowStr,
				UpdatedAt:            nowStr,
			}
			if err := tx.CreateScanRun(run, roster); err != nil {
				return domain.Response{}, err
			}
			if run.EffectivePolicy.AutoApply.FreshnessAssessment != "disabled" {
				for _, cv := range roster {
					if cv.VersionID != "" && cv.TargetSetID != "" && cv.ExclusionReason == "" {
						if _, _, err := freshnessMembership(tx, run, cv); err != nil {
							return domain.Response{}, err
						}
					}
				}
			}
			body, _ := json.Marshal(map[string]any{
				"operation_id":  key,
				"job_id":        jobID,
				"job_url":       "/api/v1/jobs/" + jobID,
				"coalesced":     false,
				"target_set_id": nullableStr(primaryTS),
			})
			woke = true
			return domain.Response{Status: 202, Body: body}, nil
		})
		if errors.Is(err, errAdmissionStateChanged) {
			if attempt < 2 {
				continue
			}
			return domain.Response{}, domain.ErrPrecondition
		}
		break
	}
	if err == nil && woke && s.Wake != nil {
		s.Wake()
	}
	return resp, err
}

func verifyAdmissionUnchanged(tx app.WriteTx, current domain.Session, snap admissionSnapshot) error {
	authState, err := tx.AuthState()
	if err != nil {
		return err
	}
	if authState.AuthEpoch != snap.AuthEpoch {
		return domain.ErrDenied
	}
	cfg, err := tx.Configuration()
	if err != nil {
		return err
	}
	if cfg.Revision != snap.ConfigRevision {
		return errAdmissionStateChanged
	}
	for _, pid := range snap.RequestedScope.ProjectIDs {
		ps, err := tx.Scope(pid)
		if err != nil {
			return err
		}
		if ps.Project.PermissionsRevision != snap.PermissionsRevisions[pid] {
			return errAdmissionStateChanged
		}
	}
	for _, entry := range snap.Entries {
		d, err := tx.Document(entry.DocumentID)
		if err != nil {
			return errAdmissionStateChanged
		}
		if d.Lifecycle != entry.Lifecycle || d.LatestVersionID != entry.LatestVersionID {
			return errAdmissionStateChanged
		}
		if entry.Version != nil {
			v, err := tx.PublishedVersion(entry.Version.ID)
			if err != nil || v.ManifestSHA != entry.Version.ManifestSHA || v.MetadataHash != entry.Version.MetadataHash {
				return errAdmissionStateChanged
			}
		}
	}
	return nil
}

func (s *Service) prepareAdmissionSnapshot(
	ctx context.Context,
	tx app.WriteTx,
	token string,
	actor domain.Session,
	opReq domain.Request,
	req domain.ScanRequest,
	normDepth, pagePath, regionID string,
) (admissionSnapshot, error) {
	current, err := s.Auth.AuthenticateWrite(ctx, tx, token)
	if err != nil {
		return admissionSnapshot{}, err
	}
	if current.PrincipalID != actor.PrincipalID {
		return admissionSnapshot{}, domain.ErrDenied
	}
	if err := s.Auth.Authority(token).AuthorizeWrite(ctx, tx, opReq); err != nil {
		return admissionSnapshot{}, err
	}
	authState, err := tx.AuthState()
	if err != nil {
		return admissionSnapshot{}, err
	}
	lib, err := tx.Library()
	if err != nil {
		return admissionSnapshot{}, err
	}
	cfg, err := tx.Configuration()
	if err != nil {
		return admissionSnapshot{}, err
	}

	explicitProjects := normalizeIDSlice(req.ProjectID, req.ProjectIDs)
	explicitDocs := normalizeIDSlice(req.DocumentID, req.DocumentIDs)
	versionID := strings.TrimSpace(req.VersionID)

	var targetVersion *domain.Version
	if versionID != "" {
		v, e := tx.PublishedVersion(versionID)
		if e != nil {
			return admissionSnapshot{}, e
		}
		d, e := tx.Document(v.DocumentID)
		if e != nil {
			return admissionSnapshot{}, e
		}
		if current.Scope.Kind != "owner" {
			if _, _, e := app.CheckSourceVersion(tx, current, v.ID); e != nil {
				return admissionSnapshot{}, e
			}
		}
		if len(explicitDocs) > 0 && (len(explicitDocs) != 1 || explicitDocs[0] != d.ID) {
			return admissionSnapshot{}, domain.ErrInvalidTarget
		}
		if len(explicitProjects) > 0 && (len(explicitProjects) != 1 || explicitProjects[0] != d.ProjectID) {
			return admissionSnapshot{}, domain.ErrInvalidTarget
		}
		explicitDocs = []string{d.ID}
		explicitProjects = []string{d.ProjectID}
		targetVersion = &v
	}

	explicitDocMap := map[string]domain.Document{}
	if len(explicitDocs) > 0 {
		docProjects := map[string]bool{}
		for _, docID := range explicitDocs {
			d, e := tx.Document(docID)
			if e != nil {
				return admissionSnapshot{}, e
			}
			if current.Scope.Kind != "owner" && versionID == "" {
				if d.Lifecycle == "trash" || d.Lifecycle == "purged" || d.LatestVersionID == "" {
					return admissionSnapshot{}, domain.ErrNotFound
				}
				if _, _, e := app.CheckSourceVersion(tx, current, d.LatestVersionID); e != nil {
					return admissionSnapshot{}, e
				}
			}
			explicitDocMap[docID] = d
			docProjects[d.ProjectID] = true
		}
		if len(explicitProjects) > 0 {
			allowedProj := map[string]bool{}
			for _, pid := range explicitProjects {
				allowedProj[pid] = true
			}
			for pid := range docProjects {
				if !allowedProj[pid] {
					return admissionSnapshot{}, domain.ErrInvalidTarget
				}
			}
		} else {
			for pid := range docProjects {
				explicitProjects = append(explicitProjects, pid)
			}
		}
	}

	var parsedMaint struct {
		Maintenance struct {
			Scope      string   `json:"scope"`
			ProjectIDs []string `json:"project_ids"`
		} `json:"maintenance"`
	}
	_ = json.Unmarshal(cfg.Value, &parsedMaint)

	scopeMode := req.Scope
	if scopeMode == "" {
		if len(explicitProjects) > 0 {
			scopeMode = "selected_projects"
		} else if parsedMaint.Maintenance.Scope == "all_allowed_projects" {
			scopeMode = "all_allowed_projects"
		} else {
			scopeMode = "selected_projects"
		}
	}
	if scopeMode == "selected_projects" && len(explicitProjects) == 0 && current.Scope.Kind == "owner" {
		explicitProjects = normalizeIDSlice("", parsedMaint.Maintenance.ProjectIDs)
	}

	var targetProjects []string
	if current.Scope.Kind != "owner" {
		if len(explicitProjects) == 0 {
			if current.MutationProjectID != "" {
				targetProjects = []string{current.MutationProjectID}
			} else {
				targetProjects = append([]string(nil), current.Scope.ProjectIDs...)
			}
		} else {
			targetProjects = explicitProjects
		}
		allowed := map[string]bool{}
		for _, pid := range current.Scope.ProjectIDs {
			allowed[pid] = true
		}
		for _, pid := range targetProjects {
			if !allowed[pid] {
				return admissionSnapshot{}, domain.ErrDenied
			}
			if current.MutationProjectID != "" && pid != current.MutationProjectID {
				return admissionSnapshot{}, domain.ErrDenied
			}
		}
	} else {
		if scopeMode != "all_allowed_projects" {
			targetProjects = explicitProjects
		} else if len(explicitProjects) > 0 && (len(explicitDocs) > 0 || versionID != "" || req.ProjectID != "" || len(req.ProjectIDs) > 0) {
			targetProjects = explicitProjects
		} else {
			afterUpdated, afterID := "", ""
			for {
				projs, e := tx.QueryProjects(current.Scope, afterUpdated, afterID, 256)
				if e != nil {
					return admissionSnapshot{}, e
				}
				if len(projs) == 0 {
					break
				}
				for _, p := range projs {
					afterUpdated = p.UpdatedAt
					afterID = p.ID
					targetProjects = append(targetProjects, p.ID)
				}
			}
		}
	}
	sort.Strings(targetProjects)
	if len(targetProjects) == 0 {
		return admissionSnapshot{}, domain.ErrInvalidTarget
	}

	inScopeProject := make(map[string]bool, len(targetProjects))
	for _, pid := range targetProjects {
		inScopeProject[pid] = true
	}
	reqMode := strings.TrimSpace(req.SourceMode)
	reqTS := strings.TrimSpace(req.TargetSetID)
	for _, sel := range req.Sources {
		if err := validateSelectorFields(sel, reqMode, reqTS); err != nil {
			return admissionSnapshot{}, err
		}
		pid := strings.TrimSpace(sel.ProjectID)
		if pid != "" && !inScopeProject[pid] {
			return admissionSnapshot{}, domain.ErrInvalidTarget
		}
		if pid == "" && len(targetProjects) > 1 {
			if sel.SourceID != "" || len(sel.SourceIDs) > 0 || sel.Revision != "" || sel.Branch != "" || sel.SnapshotID != "" || sel.TargetSetID != "" {
				return admissionSnapshot{}, domain.ErrInvalidTarget
			}
		}
	}

	permissionsRevisions := make(map[string]int64, len(targetProjects))
	for _, pid := range targetProjects {
		ps, e := tx.Scope(pid)
		if e != nil {
			return admissionSnapshot{}, e
		}
		permissionsRevisions[pid] = ps.Project.PermissionsRevision
	}

	effectivePolicy, err := ResolveEffectivePolicy(cfg, targetProjects, normDepth)
	if err != nil {
		return admissionSnapshot{}, err
	}

	mayReadSource := current.Scope.Kind == "owner"
	if !mayReadSource {
		for _, a := range current.Scope.Actions {
			if a == "source.read" {
				mayReadSource = true
				break
			}
		}
	}

	var entries []preparedDocEntry
	var ordinal int
	sort.Strings(explicitDocs)

	for _, pid := range targetProjects {
		var docs []domain.Document
		if len(explicitDocs) > 0 {
			for _, docID := range explicitDocs {
				d := explicitDocMap[docID]
				if d.ProjectID == pid {
					docs = append(docs, d)
				}
			}
		} else {
			allDocs, e := tx.ProjectDocumentsAll(pid)
			if e != nil {
				return admissionSnapshot{}, e
			}
			for _, d := range allDocs {
				if d.Lifecycle == "purged" {
					continue
				}
				docs = append(docs, d)
			}
		}

		for _, d := range docs {
			ordinal++
			if d.Lifecycle == "trash" {
				entries = append(entries, preparedDocEntry{
					Ordinal:         ordinal,
					ProjectID:       pid,
					DocumentID:      d.ID,
					Lifecycle:       d.Lifecycle,
					LatestVersionID: d.LatestVersionID,
					ExclusionReason: "document_trashed",
				})
				continue
			}
			if d.Lifecycle == "purged" {
				entries = append(entries, preparedDocEntry{
					Ordinal:         ordinal,
					ProjectID:       pid,
					DocumentID:      d.ID,
					Lifecycle:       d.Lifecycle,
					LatestVersionID: d.LatestVersionID,
					ExclusionReason: "document_purged",
				})
				continue
			}

			var v domain.Version
			if targetVersion != nil && targetVersion.DocumentID == d.ID {
				v = *targetVersion
			} else if d.LatestVersionID != "" {
				pv, e := tx.PublishedVersion(d.LatestVersionID)
				if e != nil {
					return admissionSnapshot{}, e
				}
				v = pv
			} else {
				entries = append(entries, preparedDocEntry{
					Ordinal:         ordinal,
					ProjectID:       pid,
					DocumentID:      d.ID,
					Lifecycle:       d.Lifecycle,
					LatestVersionID: d.LatestVersionID,
					ExclusionReason: "no_published_version",
				})
				continue
			}

			if current.Scope.Kind != "owner" {
				if _, _, e := app.CheckSourceVersion(tx, current, v.ID); e != nil {
					if versionID != "" || len(explicitDocs) > 0 {
						return admissionSnapshot{}, e
					}
					entries = append(entries, preparedDocEntry{
						Ordinal:         ordinal,
						ProjectID:       pid,
						DocumentID:      d.ID,
						Lifecycle:       d.Lifecycle,
						LatestVersionID: d.LatestVersionID,
						ExclusionReason: "authority_denied",
					})
					continue
				}
			}

			pages, regions, entryCount, _, e := tx.CountVersionInventory(v.ID, v.ContentSetID, pagePath, regionID)
			if e != nil {
				return admissionSnapshot{}, e
			}
			regionless := regions == 0
			metadataLess := v.MetadataHash == "" && len(v.Metadata) == 0
			prepSrc, e := prepareVersionSourceInput(tx, current, cfg, pid, v, req, mayReadSource)
			if e != nil {
				return admissionSnapshot{}, e
			}
			prepSrc.OnProbe = s.OnSourceProbe
			vCopy := v
			entries = append(entries, preparedDocEntry{
				Ordinal:         ordinal,
				ProjectID:       pid,
				DocumentID:      d.ID,
				Lifecycle:       d.Lifecycle,
				LatestVersionID: d.LatestVersionID,
				Version:         &vCopy,
				PageCount:       pages,
				RegionCount:     regions,
				EntryCount:      entryCount,
				Regionless:      regionless,
				MetadataLess:    metadataLess,
				PreparedSource:  &prepSrc,
			})
		}
	}

	if len(req.Sources) > 0 {
		projectHasEligible := map[string]bool{}
		projectDeclared := map[string]map[string]bool{}
		for _, entry := range entries {
			if entry.PreparedSource == nil {
				continue
			}
			projectHasEligible[entry.ProjectID] = true
			if projectDeclared[entry.ProjectID] == nil {
				projectDeclared[entry.ProjectID] = map[string]bool{}
			}
			for _, ds := range entry.PreparedSource.DeclaredSources {
				projectDeclared[entry.ProjectID][ds.ID] = true
			}
			for _, bt := range entry.PreparedSource.BoundTargets {
				projectDeclared[entry.ProjectID][bt.SourceID] = true
			}
		}
		for _, sel := range req.Sources {
			pid := strings.TrimSpace(sel.ProjectID)
			if pid == "" && len(targetProjects) == 1 {
				pid = targetProjects[0]
			}
			if pid != "" {
				if !projectHasEligible[pid] {
					return admissionSnapshot{}, domain.ErrInvalidTarget
				}
				sids := normalizeIDSlice(sel.SourceID, sel.SourceIDs)
				for _, sid := range sids {
					if !projectDeclared[pid][sid] {
						return admissionSnapshot{}, domain.ErrInvalidTarget
					}
				}
			}
		}
	}

	reqScope := domain.ScanRequestedScope{
		Scope:       scopeMode,
		ProjectIDs:  targetProjects,
		DocumentIDs: explicitDocs,
		VersionID:   versionID,
		PathPrefix:  pagePath,
		PagePath:    pagePath,
		RegionID:    regionID,
		Depth:       effectivePolicy.Depth,
		SourceMode:  req.SourceMode,
		TargetSetID: strings.TrimSpace(req.TargetSetID),
		Sources:     req.Sources,
	}
	if len(explicitDocs) == 1 {
		reqScope.DocumentID = explicitDocs[0]
	}

	return admissionSnapshot{
		LibraryID:            lib.ID,
		AuthEpoch:            authState.AuthEpoch,
		ConfigRevision:       cfg.Revision,
		PermissionsRevisions: permissionsRevisions,
		RequestedScope:       reqScope,
		EffectivePolicy:      effectivePolicy,
		Entries:              entries,
	}, nil
}

func buildCapturedRoster(snap admissionSnapshot, decisions map[string]VersionSourceDecision, capturedAt string) ([]domain.ScanCapturedVersion, domain.ScanDenominator, string) {
	roster := make([]domain.ScanCapturedVersion, 0, len(snap.Entries))
	denom := domain.ScanDenominator{
		Projects:  len(snap.RequestedScope.ProjectIDs),
		Documents: len(snap.Entries),
	}
	primaryTS := ""
	for _, entry := range snap.Entries {
		if entry.ExclusionReason != "" || entry.Version == nil {
			roster = append(roster, domain.ScanCapturedVersion{
				VersionOrdinal:  entry.Ordinal,
				ProjectID:       entry.ProjectID,
				DocumentID:      entry.DocumentID,
				SelectedMode:    "pinned",
				SelectionSource: "default_pinned",
				SelectionJSON:   json.RawMessage(`{}`),
				TargetsJSON:     json.RawMessage(`{"sources":[]}`),
				ExclusionReason: entry.ExclusionReason,
				UnitCount:       1,
				EnumState:       "pending",
				CapturedAt:      capturedAt,
			})
			denom.ExcludedDocuments++
			denom.TotalUnits += 1
			continue
		}
		v := entry.Version
		dec := decisions[v.ID]
		if primaryTS == "" && dec.TargetSetID != "" {
			primaryTS = dec.TargetSetID
		}
		srcCount := len(dec.Targets)
		sourceUnits := srcCount
		if sourceUnits == 0 {
			sourceUnits = 1
		}
		unitCount := 1 + entry.PageCount + entry.RegionCount + entry.EntryCount + sourceUnits + 1
		denom.Versions++
		denom.Pages += entry.PageCount
		denom.Regions += entry.RegionCount
		denom.Entries += entry.EntryCount
		denom.Sources += srcCount
		denom.Indexes++
		denom.TotalUnits += unitCount

		roster = append(roster, domain.ScanCapturedVersion{
			VersionOrdinal:  entry.Ordinal,
			ProjectID:       entry.ProjectID,
			DocumentID:      entry.DocumentID,
			VersionID:       v.ID,
			ContentSetID:    v.ContentSetID,
			ManifestSHA256:  v.ManifestSHA,
			MetadataSHA256:  v.MetadataHash,
			SourceBasis:     v.SourceBasis,
			SourceIntent:    entry.PreparedSource.SourceIntent,
			SelectedMode:    dec.SelectedMode,
			SelectionSource: dec.SelectionSource,
			TargetSetID:     dec.TargetSetID,
			SelectionJSON:   dec.SelectionJSON,
			TargetsJSON:     dec.TargetsJSON,
			Targets:         dec.Targets,
			Regionless:      entry.Regionless,
			MetadataLess:    entry.MetadataLess,
			PageCount:       entry.PageCount,
			RegionCount:     entry.RegionCount,
			EntryCount:      entry.EntryCount,
			SourceCount:     srcCount,
			UnitCount:       unitCount,
			EnumState:       "pending",
			CapturedAt:      capturedAt,
		})
	}
	return roster, denom, primaryTS
}

func initialScanReport(
	jobID, targetSetID string,
	cfgRev int64,
	perms map[string]int64,
	reqScope domain.ScanRequestedScope,
	policy domain.ScanPolicySnapshot,
	denom domain.ScanDenominator,
	roster []domain.ScanCapturedVersion,
) domain.ScanReport {
	var regionless, metadataLess, excluded int
	var allTargets []domain.ScanSourceTarget
	for _, cv := range roster {
		if cv.ExclusionReason != "" {
			excluded++
			continue
		}
		if cv.Regionless {
			regionless++
		}
		if cv.MetadataLess {
			metadataLess++
		}
		allTargets = append(allTargets, cv.Targets...)
	}
	scopeCopy := reqScope
	policyCopy := policy
	return domain.ScanReport{
		JobID:                jobID,
		Stage:                "inventory",
		EnumerationComplete:  false,
		BudgetExhausted:      false,
		TargetSetID:          targetSetID,
		ConfigRevision:       cfgRev,
		PermissionsRevisions: perms,
		RequestedScope:       &scopeCopy,
		EffectivePolicy:      &policyCopy,
		SourceTargets:        allTargets,
		Captured:             denom,
		Structural: domain.ScanStructuralCounts{
			Excluded:             excluded,
			RegionlessVersions:   regionless,
			MetadataLessVersions: metadataLess,
		},
		Semantic: domain.ScanSemanticCounts{
			Assessed: 0,
			Complete: false,
			Status:   "not_assessed",
			Reason:   "semantic_assessment_not_run",
		},
		Findings: domain.ScanFindingSummary{},
	}
}

type findingCursor struct {
	Binding   string `json:"binding"`
	CreatedAt string `json:"created_at"`
	FindingID string `json:"finding_id"`
}

func ValidFindingKind(k string) bool {
	switch k {
	case "backlink_index_repair", "related_explanation", "topic_assignment", "freshness_assessment", "new_version_proposal", "new_document_proposal":
		return true
	default:
		return false
	}
}

func ValidFindingStatus(s string) bool {
	switch s {
	case "proposed", "applied", "rejected", "stale", "blocked", "undone":
		return true
	default:
		return false
	}
}

func (s *Service) ListJobFindings(ctx context.Context, token string, filter domain.FindingFilter) (domain.FindingPage, error) {
	out := domain.FindingPage{Items: []domain.Finding{}}
	if filter.Kind != "" && !ValidFindingKind(filter.Kind) {
		return out, domain.ErrInvalidTarget
	}
	if filter.Status != "" && !ValidFindingStatus(filter.Status) {
		return out, domain.ErrInvalidTarget
	}
	limit := filter.Limit
	if limit == 0 {
		limit = 50
	}
	if limit < 1 || limit > 200 {
		return out, domain.ErrInvalidTarget
	}

	err := s.Store.Write(ctx, func(tx app.WriteTx) error {
		actor, e := s.Auth.AuthenticateWrite(ctx, tx, token)
		if e != nil {
			return e
		}
		if actor.Scope.Kind != "owner" {
			return domain.ErrDenied
		}
		j, e := tx.Job(filter.JobID)
		if e != nil {
			return e
		}
		if j.Kind != "scan" {
			return domain.ErrNotFound
		}
		var scanScope struct {
			PrincipalID string   `json:"principal_id"`
			ProjectIDs  []string `json:"project_ids"`
			DocumentID  string   `json:"document_id"`
			DocumentIDs []string `json:"document_ids"`
			VersionID   string   `json:"version_id"`
		}
		if json.Unmarshal(j.Scope, &scanScope) != nil || scanScope.PrincipalID == "" {
			return domain.ErrNotFound
		}
		for _, pid := range scanScope.ProjectIDs {
			if _, e := tx.Scope(pid); e != nil {
				return e
			}
		}
		if scanScope.VersionID != "" {
			v, e := tx.Version(scanScope.VersionID)
			if e != nil {
				return e
			}
			d, e := tx.Document(v.DocumentID)
			if e != nil {
				return e
			}
			if d.Lifecycle == "trash" || d.Lifecycle == "purged" {
				return domain.ErrNotFound
			}
		}
		singleDoc := scanScope.DocumentID
		if singleDoc == "" && len(scanScope.DocumentIDs) == 1 {
			singleDoc = scanScope.DocumentIDs[0]
		}
		if singleDoc != "" {
			d, e := tx.Document(singleDoc)
			if e != nil {
				return e
			}
			if d.Lifecycle == "trash" || d.Lifecycle == "purged" {
				return domain.ErrNotFound
			}
		}
		lib, e := tx.Library()
		if e != nil {
			return e
		}
		bindingBytes, _ := json.Marshal([]any{lib.RuntimeGeneration, "job_findings", j.ID, filter.Kind, filter.Status, actor.PrincipalID, limit})
		binding := base64.RawURLEncoding.EncodeToString(bindingBytes)

		var afterCreated, afterID string
		if filter.Cursor != "" {
			raw, e := base64.RawURLEncoding.DecodeString(filter.Cursor)
			if e != nil {
				return sec.ErrInvalid
			}
			var c findingCursor
			dec := json.NewDecoder(strings.NewReader(string(raw)))
			dec.DisallowUnknownFields()
			if dec.Decode(&c) != nil || c.Binding != binding || c.CreatedAt == "" || c.FindingID == "" {
				return sec.ErrInvalid
			}
			afterCreated = c.CreatedAt
			afterID = c.FindingID
		}

		f := filter
		f.Limit = limit + 1
		if f.Limit < 32 {
			f.Limit = 32
		}
		curAfterCreated := afterCreated
		curAfterID := afterID
		for {
			rows, e := tx.JobFindings(f, curAfterCreated, curAfterID)
			if e != nil {
				return e
			}
			if len(rows) == 0 {
				break
			}
			reachedPageLimit := false
			for _, item := range rows {
				curAfterCreated = item.CreatedAt
				curAfterID = item.ID
				if !visibleFindingForActor(tx, actor, item) {
					continue
				}
				if len(out.Items) == limit {
					last := out.Items[len(out.Items)-1]
					nextRaw, _ := json.Marshal(findingCursor{
						Binding:   binding,
						CreatedAt: last.CreatedAt,
						FindingID: last.ID,
					})
					nextStr := base64.RawURLEncoding.EncodeToString(nextRaw)
					out.NextCursor = &nextStr
					reachedPageLimit = true
					break
				}
				// Legacy missing basis is a read projection, never a history rewrite.
				if item.Kind == "topic_assignment" {
					var proposal map[string]any
					if json.Unmarshal(item.Proposal, &proposal) == nil && proposal != nil {
						basis, _ := proposal["evidence_basis"].(string)
						proposal["evidence_basis"] = domain.TopicEvidenceBasis(basis)
						item.Proposal, _ = json.Marshal(proposal)
					}
				}
				out.Items = append(out.Items, item)
			}
			if reachedPageLimit || len(rows) < f.Limit {
				break
			}
		}
		out.ETag = domain.FindingPageETag(j.ID, j.Revision, filter.Kind, filter.Status, out.Items, out.NextCursor)
		return nil
	})
	return out, err
}

func checkVersionVisibleForFinding(tx app.WriteTx, actor domain.Session, verID string) bool {
	if verID == "" {
		return true
	}
	if tomb, err := tx.TombstonedVersion(verID); err != nil || tomb {
		return false
	}
	v, err := tx.PublishedVersion(verID)
	if err != nil {
		return false
	}
	doc, err := tx.Document(v.DocumentID)
	if err != nil || doc.Lifecycle == "trash" || doc.Lifecycle == "purged" {
		return false
	}
	if pScope, err := tx.Scope(doc.ProjectID); err != nil || pScope.Project.Lifecycle == "trash" || pScope.Project.Lifecycle == "purged" {
		return false
	}
	if actor.Scope.Kind != "" && actor.Scope.Kind != "owner" {
		if _, _, err := app.CheckSourceVersion(tx, actor, verID); err != nil {
			return false
		}
	}
	return true
}

func visibleFindingForActor(tx app.WriteTx, actor domain.Session, f domain.Finding) bool {
	if f.Reason == "document_purged" {
		return false
	}
	if pScope, err := tx.Scope(f.ProjectID); err != nil || pScope.Project.Lifecycle == "trash" || pScope.Project.Lifecycle == "purged" {
		return false
	}
	if f.DocumentID != nil && *f.DocumentID != "" {
		doc, err := tx.Document(*f.DocumentID)
		if err != nil || doc.Lifecycle == "trash" || doc.Lifecycle == "purged" {
			return false
		}
	}
	if f.VersionID != nil && *f.VersionID != "" {
		if !checkVersionVisibleForFinding(tx, actor, *f.VersionID) {
			return false
		}
	}
	var prop struct {
		Purged           bool   `json:"purged"`
		TargetProjectID  string `json:"target_project_id"`
		TargetDocumentID string `json:"target_document_id"`
		TargetVersionID  string `json:"target_version_id"`
		OriginUnit       *struct {
			ProjectID  string `json:"project_id"`
			DocumentID string `json:"document_id"`
			VersionID  string `json:"version_id"`
		} `json:"origin_unit"`
	}
	if len(f.Proposal) > 0 && json.Unmarshal(f.Proposal, &prop) == nil {
		if prop.Purged {
			return false
		}
		if prop.TargetProjectID != "" {
			if pScope, err := tx.Scope(prop.TargetProjectID); err != nil || pScope.Project.Lifecycle == "trash" || pScope.Project.Lifecycle == "purged" {
				return false
			}
		}
		if prop.TargetDocumentID != "" {
			doc, err := tx.Document(prop.TargetDocumentID)
			if err != nil || doc.Lifecycle == "trash" || doc.Lifecycle == "purged" {
				return false
			}
		}
		if prop.TargetVersionID != "" {
			if !checkVersionVisibleForFinding(tx, actor, prop.TargetVersionID) {
				return false
			}
		}
		if prop.OriginUnit != nil {
			if prop.OriginUnit.ProjectID != "" {
				if pScope, err := tx.Scope(prop.OriginUnit.ProjectID); err != nil || pScope.Project.Lifecycle == "trash" || pScope.Project.Lifecycle == "purged" {
					return false
				}
			}
			if prop.OriginUnit.DocumentID != "" {
				doc, err := tx.Document(prop.OriginUnit.DocumentID)
				if err != nil || doc.Lifecycle == "trash" || doc.Lifecycle == "purged" {
					return false
				}
			}
			if prop.OriginUnit.VersionID != "" {
				if !checkVersionVisibleForFinding(tx, actor, prop.OriginUnit.VersionID) {
					return false
				}
			}
		}
	}
	if len(f.Evidence) > 0 {
		var evArr []struct {
			VersionID string `json:"version_id"`
		}
		if json.Unmarshal(f.Evidence, &evArr) == nil {
			for _, ev := range evArr {
				if ev.VersionID != "" && !checkVersionVisibleForFinding(tx, actor, ev.VersionID) {
					return false
				}
			}
		} else {
			var evObj struct {
				VersionID string `json:"version_id"`
			}
			if json.Unmarshal(f.Evidence, &evObj) == nil && evObj.VersionID != "" {
				if !checkVersionVisibleForFinding(tx, actor, evObj.VersionID) {
					return false
				}
			}
		}
	}
	return true
}

func nullableStr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
