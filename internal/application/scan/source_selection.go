package scan

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"haeryong.local/haeryong/internal/application/service"
	"haeryong.local/haeryong/internal/application/source"
	app "haeryong.local/haeryong/internal/application/storage"
	"haeryong.local/haeryong/internal/domain"
)

type VersionSourceDecision struct {
	SelectedMode         string
	SelectionSource      string
	TargetSetID          string
	Selection            domain.ScanSourceSelector
	SelectionJSON        json.RawMessage
	Targets              []domain.ScanSourceTarget
	TargetsJSON          json.RawMessage
	RetainedObjectHashes []string
}

func extractVersionSourceIntent(v domain.Version) *string {
	if len(v.Metadata) == 0 {
		return nil
	}
	var meta struct {
		SourceIntent *string `json:"source_intent"`
	}
	if json.Unmarshal(v.Metadata, &meta) != nil || meta.SourceIntent == nil {
		return nil
	}
	val := strings.TrimSpace(*meta.SourceIntent)
	if val != "moving_target" && val != "historical" {
		return nil
	}
	return &val
}

func projectDefaultSourceMode(cfg domain.Configuration, projectID string) (string, bool) {
	var parsed struct {
		Projects map[string]struct {
			SourceModeDefault *string `json:"source_mode_default"`
		} `json:"projects"`
	}
	if json.Unmarshal(cfg.Value, &parsed) != nil {
		return "", false
	}
	if p, ok := parsed.Projects[projectID]; ok && p.SourceModeDefault != nil {
		m := strings.TrimSpace(*p.SourceModeDefault)
		if m == "pinned" || m == "current" {
			return m, true
		}
	}
	return "", false
}

func allSourceCaptureReads(tx app.WriteTx, captureID string) ([]domain.SourceCaptureRead, error) {
	var all []domain.SourceCaptureRead
	after := ""
	for {
		batch, err := tx.SourceCaptureReads(captureID, after, 256)
		if err != nil {
			return nil, err
		}
		if len(batch) == 0 {
			break
		}
		for _, r := range batch {
			after = r.ID
			all = append(all, r)
		}
	}
	return all, nil
}

func validateSelectorFields(sel domain.ScanSourceSelector, reqMode, reqTS string) error {
	mode := strings.TrimSpace(sel.Mode)
	if mode != "" && mode != "pinned" && mode != "current" {
		return domain.ErrInvalidTarget
	}
	if reqMode != "" && mode != "" && reqMode != mode {
		return domain.ErrInvalidTarget
	}
	rev := strings.TrimSpace(sel.Revision)
	branch := strings.TrimSpace(sel.Branch)
	snap := strings.TrimSpace(sel.SnapshotID)
	ts := strings.TrimSpace(sel.TargetSetID)
	if rev != "" && branch != "" {
		return domain.ErrInvalidTarget
	}
	if mode == "pinned" && branch != "" {
		return domain.ErrInvalidTarget
	}
	if ts != "" && (rev != "" || branch != "" || snap != "") {
		return domain.ErrInvalidTarget
	}
	if reqTS != "" && ts != "" && reqTS != ts {
		return domain.ErrInvalidTarget
	}
	if sel.SourceID != "" && len(sel.SourceIDs) > 0 {
		found := false
		for _, id := range sel.SourceIDs {
			if strings.TrimSpace(id) == strings.TrimSpace(sel.SourceID) {
				found = true
				break
			}
		}
		if !found {
			return domain.ErrInvalidTarget
		}
	}
	return nil
}

func resolveFallbackSourceMode(cfg domain.Configuration, projectID string, sourceIntent *string, explicitMode string) (mode, selSource string) {
	if explicitMode != "" {
		return explicitMode, "request_override"
	}
	if sourceIntent != nil && *sourceIntent == "historical" {
		return "pinned", "historical_intent"
	}
	if defMode, ok := projectDefaultSourceMode(cfg, projectID); ok {
		return defMode, "project_default"
	}
	return "pinned", "default_pinned"
}

// ResolveVersionSourceMode implements the canonical EE-P01 source family
// precedence before target capture:
//  1. Explicit target_set_id binds that exact committed capture and its selection;
//  2. Explicit source_mode ('pinned' | 'current') wins;
//  3. Historical source_intent uses 'pinned';
//  4. Remembered projects[id].source_mode_default, defaulting to 'pinned' (moving_target does not force 'current').
func ResolveVersionSourceMode(cfg domain.Configuration, projectID string, sourceIntent *string, req domain.ScanRequest) (mode, selSource, targetSetID string, matchedSelector *domain.ScanSourceSelector, err error) {
	reqMode := strings.TrimSpace(req.SourceMode)
	if reqMode != "" && reqMode != "pinned" && reqMode != "current" {
		return "", "", "", nil, domain.ErrInvalidTarget
	}
	reqTS := strings.TrimSpace(req.TargetSetID)
	explicitTS := reqTS
	for i := range req.Sources {
		sel := &req.Sources[i]
		if err := validateSelectorFields(*sel, reqMode, reqTS); err != nil {
			return "", "", "", nil, err
		}
		if sel.ProjectID == "" || sel.ProjectID == projectID {
			if matchedSelector == nil {
				matchedSelector = sel
			}
			if ts := strings.TrimSpace(sel.TargetSetID); ts != "" {
				if explicitTS != "" && explicitTS != ts {
					return "", "", "", nil, domain.ErrInvalidTarget
				}
				explicitTS = ts
			}
		}
	}
	if explicitTS != "" {
		return "", "target_set", explicitTS, matchedSelector, nil
	}
	explicitMode := reqMode
	if explicitMode == "" && matchedSelector != nil && matchedSelector.Mode != "" {
		allSameMode := true
		firstMode := ""
		for i := range req.Sources {
			sel := &req.Sources[i]
			if sel.ProjectID != "" && sel.ProjectID != projectID {
				continue
			}
			m := strings.TrimSpace(sel.Mode)
			if m == "" {
				allSameMode = false
				break
			}
			if firstMode == "" {
				firstMode = m
			} else if firstMode != m {
				allSameMode = false
				break
			}
		}
		if allSameMode && firstMode != "" {
			explicitMode = firstMode
		}
	}
	m, src := resolveFallbackSourceMode(cfg, projectID, sourceIntent, explicitMode)
	return m, src, "", matchedSelector, nil
}

type preparedVersionSources struct {
	VersionID            string
	ProjectID            string
	ContentSetID         string
	Config               domain.Configuration
	RequestSourceMode    string
	SourceIntent         *string
	SelectedMode         string
	SelectionSource      string
	TargetSetID          string
	MatchedSelector      *domain.ScanSourceSelector
	ProjectWideSelector  *domain.ScanSourceSelector
	PerSourceSelectors   map[string]domain.ScanSourceSelector
	DeclaredSources      []domain.Source
	Mappings             []source.Mapping
	MappingErr           error
	RetainedAssets       map[string]string
	RetainedReads        map[string]string
	CurrentRetainedReads map[string]string
	RetainedReadHashes   map[string][]string
	BoundTargets         []domain.ScanSourceTarget
	BoundSelection       *domain.ScanSourceSelector
	BoundTargetSet       *domain.TargetSet
	BoundObjectHashes    []string
	OnProbe              func(projectID, versionID, sourceID, op string)
}

type readPathDigest struct {
	ReadID string
	Path   string
	Hash   string
}

func compositeReadDigestsSHA(items []readPathDigest) string {
	if len(items) == 0 {
		return ""
	}
	if len(items) == 1 {
		return items[0].Hash
	}
	sorted := append([]readPathDigest(nil), items...)
	sort.Slice(sorted, func(i, j int) bool {
		if sorted[i].Path != sorted[j].Path {
			return sorted[i].Path < sorted[j].Path
		}
		if sorted[i].Hash != sorted[j].Hash {
			return sorted[i].Hash < sorted[j].Hash
		}
		return sorted[i].ReadID < sorted[j].ReadID
	})
	var buf bytes.Buffer
	for _, it := range sorted {
		buf.WriteString(it.Path)
		buf.WriteByte(0)
		buf.WriteString(it.Hash)
		buf.WriteByte('\n')
	}
	sum := sha256.Sum256(buf.Bytes())
	return hex.EncodeToString(sum[:])
}

func prepareVersionSourceInput(tx app.WriteTx, actor domain.Session, cfg domain.Configuration, projectID string, v domain.Version, req domain.ScanRequest, mayReadSource bool) (preparedVersionSources, error) {
	if actor.Scope.Kind != "owner" {
		if _, _, err := app.CheckSourceVersion(tx, actor, v.ID); err != nil {
			mayReadSource = false
		}
	}
	intent := extractVersionSourceIntent(v)
	mode, selSource, explicitTS, matchedSel, err := ResolveVersionSourceMode(cfg, projectID, intent, req)
	if err != nil {
		return preparedVersionSources{}, err
	}
	declared, err := tx.VersionSources(v.ID)
	if err != nil {
		return preparedVersionSources{}, err
	}
	sort.Slice(declared, func(i, j int) bool { return declared[i].ID < declared[j].ID })

	var mappings []source.Mapping
	mapErr := domain.ErrDenied
	if mayReadSource {
		mappings, mapErr = source.Mappings(tx, projectID)
	}

	declaredByID := map[string]bool{}
	allowed := map[string]bool{}
	for _, ds := range declared {
		declaredByID[ds.ID] = true
		allowed[ds.ID] = true
	}
	mappingByID := map[string]source.Mapping{}
	for _, m := range mappings {
		allowed[m.SourceID] = true
		mappingByID[m.SourceID] = m
	}

	reqMode := strings.TrimSpace(req.SourceMode)
	reqTS := strings.TrimSpace(req.TargetSetID)

	// When a version was imported without compulsory author source metadata (len(declared)==0)
	// and the caller explicitly requests source evaluation (via source_mode or sources[]),
	// or when sources[] explicitly selects a configured project mapping not yet in declared,
	// synthesize the configured project source mapping into declared so explicit source selection works
	// without compulsory import metadata.
	if explicitTS == "" && len(mappings) > 0 {
		for _, sel := range req.Sources {
			if sel.ProjectID != "" && sel.ProjectID != projectID {
				continue
			}
			for _, sid := range normalizeIDSlice(sel.SourceID, sel.SourceIDs) {
				if m, ok := mappingByID[sid]; ok && !declaredByID[sid] {
					meta, _ := json.Marshal(map[string]any{
						"kind":           m.Kind,
						"repo_id":        m.RepoID,
						"retention_mode": m.RetentionMode,
					})
					declared = append(declared, domain.Source{
						VersionID: v.ID,
						ID:        m.SourceID,
						Metadata:  meta,
					})
					declaredByID[sid] = true
				}
			}
		}
		if len(declared) == 0 && (reqMode != "" || len(req.Sources) > 0) {
			for _, m := range mappings {
				if !declaredByID[m.SourceID] {
					meta, _ := json.Marshal(map[string]any{
						"kind":           m.Kind,
						"repo_id":        m.RepoID,
						"retention_mode": m.RetentionMode,
					})
					declared = append(declared, domain.Source{
						VersionID: v.ID,
						ID:        m.SourceID,
						Metadata:  meta,
					})
					declaredByID[m.SourceID] = true
				}
			}
		}
		sort.Slice(declared, func(i, j int) bool { return declared[i].ID < declared[j].ID })
	}

	var projectWideSel *domain.ScanSourceSelector
	perSourceSels := map[string]domain.ScanSourceSelector{}
	projectSelectors := []domain.ScanSourceSelector{}

	for i := range req.Sources {
		sel := req.Sources[i]
		if err := validateSelectorFields(sel, reqMode, reqTS); err != nil {
			return preparedVersionSources{}, err
		}
		if sel.ProjectID != "" && sel.ProjectID != projectID {
			continue
		}
		projectSelectors = append(projectSelectors, sel)
		sids := normalizeIDSlice(sel.SourceID, sel.SourceIDs)
		rev := strings.TrimSpace(sel.Revision)
		branch := strings.TrimSpace(sel.Branch)
		snap := strings.TrimSpace(sel.SnapshotID)
		if len(sids) == 0 {
			if projectWideSel != nil || len(perSourceSels) > 0 {
				return preparedVersionSources{}, domain.ErrInvalidTarget
			}
			if (rev != "" || branch != "" || snap != "") && len(declared) == 0 && explicitTS == "" {
				return preparedVersionSources{}, domain.ErrInvalidTarget
			}
			if (rev != "" || snap != "") && len(declared) > 1 && explicitTS == "" {
				return preparedVersionSources{}, domain.ErrInvalidTarget
			}
			selCopy := sel
			projectWideSel = &selCopy
		} else {
			if projectWideSel != nil {
				return preparedVersionSources{}, domain.ErrInvalidTarget
			}
			if len(sids) > 1 && (rev != "" || snap != "") {
				return preparedVersionSources{}, domain.ErrInvalidTarget
			}
			for _, sid := range sids {
				if !allowed[sid] && explicitTS == "" {
					return preparedVersionSources{}, domain.ErrInvalidTarget
				}
				if _, dup := perSourceSels[sid]; dup {
					return preparedVersionSources{}, domain.ErrInvalidTarget
				}
				perSourceSels[sid] = sel
			}
		}
	}

	retainedAssets := map[string]string{}
	for _, ds := range declared {
		var sm struct {
			AssetPath     string `json:"asset_path"`
			RetentionMode string `json:"retention_mode"`
			BytesSHA      string `json:"bytes_sha256"`
			SnapshotSHA   string `json:"snapshot_sha256"`
		}
		if json.Unmarshal(ds.Metadata, &sm) == nil && sm.AssetPath != "" && sm.RetentionMode != "reference_only" {
			if entry, e := tx.QueryEntry(v.ContentSetID, sm.AssetPath); e == nil && entry.Kind == "file" && entry.Hash != "" {
				if obj, oe := tx.Object(entry.Hash); oe == nil && obj.State == "ready" {
					expected := sm.BytesSHA
					if expected == "" {
						expected = sm.SnapshotSHA
					}
					if expected == "" || expected == entry.Hash {
						retainedAssets[ds.ID] = entry.Hash
					}
				}
			}
		}
	}

	retainedReads := map[string]string{}
	currentRetainedReads := map[string]string{}
	retainedReadHashes := map[string][]string{}
	if mayReadSource {
		if caps, e := tx.SourceCaptures(projectID); e == nil {
			for _, capTS := range caps {
				sc, ok, se := tx.SourceCaptureByTargetSet(capTS.ID)
				if se != nil || !ok || sc.State != "completed" {
					continue
				}
				if app.CheckCaptureActor(tx, actor, sc) != nil {
					continue
				}
				var scSel struct {
					VersionID  string `json:"version_id"`
					SourceMode string `json:"source_mode"`
				}
				_ = json.Unmarshal(sc.Selection, &scSel)
				if scSel.VersionID != "" && scSel.VersionID != v.ID {
					continue
				}
				reads, re := allSourceCaptureReads(tx, sc.ID)
				if re != nil {
					continue
				}
				bySource := map[string][]readPathDigest{}
				sourceAllReady := map[string]bool{}
				sourceHashes := map[string][]string{}
				for _, r := range reads {
					if _, seen := sourceAllReady[r.SourceID]; !seen {
						sourceAllReady[r.SourceID] = true
					}
					if r.RetentionMode != "selected_snapshot" || r.Availability != "available" || r.Hash == "" {
						sourceAllReady[r.SourceID] = false
						continue
					}
					obj, oe := tx.Object(r.Hash)
					if oe != nil || obj.State != "ready" {
						sourceAllReady[r.SourceID] = false
						continue
					}
					var ident struct {
						Kind          string `json:"kind"`
						RepoID        string `json:"repo_id"`
						ExactRevision string `json:"exact_revision"`
						SnapshotSHA   string `json:"snapshot_sha256"`
					}
					_ = json.Unmarshal(r.SourceJSON, &ident)
					var loc struct {
						Path string `json:"path"`
					}
					_ = json.Unmarshal(r.LocationJSON, &loc)
					bySource[r.SourceID] = append(bySource[r.SourceID], readPathDigest{
						ReadID: r.ID,
						Path:   loc.Path,
						Hash:   r.Hash,
					})
					sourceHashes[r.SourceID] = append(sourceHashes[r.SourceID], r.Hash)
					retainedReads[r.SourceID+":"+r.Hash] = r.Hash
					retainedReadHashes[r.SourceID+":"+r.Hash] = []string{r.Hash}
					if scSel.SourceMode == "current" {
						currentRetainedReads[r.SourceID+":"+r.Hash] = r.Hash
					}
					if ident.SnapshotSHA != "" {
						retainedReads[r.SourceID+":"+ident.SnapshotSHA] = r.Hash
						retainedReads["sha:"+ident.SnapshotSHA] = r.Hash
						retainedReadHashes[r.SourceID+":"+ident.SnapshotSHA] = []string{r.Hash}
						if scSel.SourceMode == "current" {
							currentRetainedReads[r.SourceID+":"+ident.SnapshotSHA] = r.Hash
						}
					}
					if ident.ExactRevision != "" {
						retainedReads[r.SourceID+":"+ident.ExactRevision] = r.Hash
						retainedReadHashes[r.SourceID+":"+ident.ExactRevision] = []string{r.Hash}
						if ident.RepoID != "" {
							retainedReads["repo:"+ident.RepoID+":"+ident.ExactRevision] = r.Hash
							retainedReadHashes["repo:"+ident.RepoID+":"+ident.ExactRevision] = []string{r.Hash}
						}
					}
				}
				for sid, digests := range bySource {
					if !sourceAllReady[sid] || len(digests) == 0 {
						continue
					}
					compSHA := compositeReadDigestsSHA(digests)
					retainedReads[sid+":"+compSHA] = compSHA
					retainedReads["sha:"+compSHA] = compSHA
					retainedReadHashes[sid+":"+compSHA] = sourceHashes[sid]
					if scSel.SourceMode == "current" {
						currentRetainedReads[sid+":"+compSHA] = compSHA
					}
				}
			}
		}
		if vcaps, e := tx.VerificationCaptures(v.ID, projectID); e == nil {
			for _, vts := range vcaps {
				verifs, ve := tx.VerificationsByTargetSet(v.ID, vts.ID)
				if ve != nil {
					continue
				}
				for _, vf := range verifs {
					if (vf.Outcome == "captured" || vf.Outcome == "verified") && vf.BytesSHA != "" {
						if obj, oe := tx.Object(vf.BytesSHA); oe == nil && obj.State == "ready" {
							retainedReads["sha:"+vf.BytesSHA] = vf.BytesSHA
							retainedReadHashes["sha:"+vf.BytesSHA] = []string{vf.BytesSHA}
							if ev, ee := tx.QueryEvidence(v.ID, vf.EvidenceID); ee == nil && ev.SourceID != "" {
								retainedReads[ev.SourceID+":"+vf.BytesSHA] = vf.BytesSHA
								retainedReadHashes[ev.SourceID+":"+vf.BytesSHA] = []string{vf.BytesSHA}
							}
						}
					}
				}
			}
		}
	}

	var boundTS *domain.TargetSet
	var boundTargets []domain.ScanSourceTarget
	var boundSel *domain.ScanSourceSelector
	var boundObjHashes []string
	if explicitTS != "" {
		ts, tsMode, sel, targets, objHashes, e := loadCommittedTargetSet(tx, actor, projectID, v, declared, mappings, mapErr, explicitTS, req, projectSelectors)
		if e != nil {
			return preparedVersionSources{}, e
		}
		mode = tsMode
		boundTS = &ts
		boundTargets = targets
		boundSel = &sel
		boundObjHashes = objHashes
	}

	return preparedVersionSources{
		VersionID:            v.ID,
		ProjectID:            projectID,
		ContentSetID:         v.ContentSetID,
		Config:               cfg,
		RequestSourceMode:    reqMode,
		SourceIntent:         intent,
		SelectedMode:         mode,
		SelectionSource:      selSource,
		TargetSetID:          explicitTS,
		MatchedSelector:      matchedSel,
		ProjectWideSelector:  projectWideSel,
		PerSourceSelectors:   perSourceSels,
		DeclaredSources:      declared,
		Mappings:             mappings,
		MappingErr:           mapErr,
		RetainedAssets:       retainedAssets,
		RetainedReads:        retainedReads,
		CurrentRetainedReads: currentRetainedReads,
		RetainedReadHashes:   retainedReadHashes,
		BoundTargets:         boundTargets,
		BoundSelection:       boundSel,
		BoundTargetSet:       boundTS,
		BoundObjectHashes:    boundObjHashes,
	}, nil
}

func loadCommittedTargetSet(
	tx app.WriteTx,
	actor domain.Session,
	projectID string,
	v domain.Version,
	declared []domain.Source,
	mappings []source.Mapping,
	mapErr error,
	explicitTS string,
	req domain.ScanRequest,
	projectSelectors []domain.ScanSourceSelector,
) (domain.TargetSet, string, domain.ScanSourceSelector, []domain.ScanSourceTarget, []string, error) {
	ts, err := tx.TargetSetByID(explicitTS)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return domain.TargetSet{}, "", domain.ScanSourceSelector{}, nil, nil, domain.ErrInvalidTarget
		}
		return domain.TargetSet{}, "", domain.ScanSourceSelector{}, nil, nil, err
	}
	if ts.ProjectID != projectID {
		return domain.TargetSet{}, "", domain.ScanSourceSelector{}, nil, nil, domain.ErrInvalidTarget
	}

	declaredMetaByID := map[string]struct {
		ExactRevision string `json:"exact_revision"`
		SnapshotSHA   string `json:"snapshot_sha256"`
		BytesSHA      string `json:"bytes_sha256"`
		AssetPath     string `json:"asset_path"`
	}{}
	for _, ds := range declared {
		var sm struct {
			ExactRevision string `json:"exact_revision"`
			SnapshotSHA   string `json:"snapshot_sha256"`
			BytesSHA      string `json:"bytes_sha256"`
			AssetPath     string `json:"asset_path"`
		}
		if json.Unmarshal(ds.Metadata, &sm) == nil {
			declaredMetaByID[ds.ID] = sm
		}
	}

	checkModeCompat := func(tsMode string) error {
		if req.SourceMode != "" && req.SourceMode != tsMode {
			return domain.ErrInvalidTarget
		}
		for _, sel := range projectSelectors {
			if sel.Mode != "" && sel.Mode != tsMode {
				return domain.ErrInvalidTarget
			}
			if sel.TargetSetID != "" && strings.TrimSpace(sel.TargetSetID) != explicitTS {
				return domain.ErrInvalidTarget
			}
			if sel.Branch != "" {
				return domain.ErrInvalidTarget
			}
		}
		return nil
	}
	checkTargetsCompat := func(targets []domain.ScanSourceTarget) error {
		byID := map[string]domain.ScanSourceTarget{}
		for _, t := range targets {
			byID[t.SourceID] = t
		}
		for _, sel := range projectSelectors {
			sids := normalizeIDSlice(sel.SourceID, sel.SourceIDs)
			if len(sids) == 0 && len(targets) == 1 {
				t := targets[0]
				if sel.Revision != "" && sel.Revision != t.ResolvedRevision {
					return domain.ErrInvalidTarget
				}
				if sel.SnapshotID != "" && sel.SnapshotID != t.SnapshotSHA256 {
					return domain.ErrInvalidTarget
				}
			}
			for _, sid := range sids {
				t, ok := byID[sid]
				if !ok {
					return domain.ErrInvalidTarget
				}
				if sel.Revision != "" && sel.Revision != t.ResolvedRevision {
					return domain.ErrInvalidTarget
				}
				if sel.SnapshotID != "" && sel.SnapshotID != t.SnapshotSHA256 {
					return domain.ErrInvalidTarget
				}
			}
		}
		return nil
	}

	// Family 1: Managed Source Capture (source_captures + source_capture_reads)
	if sc, ok, err := tx.SourceCaptureByTargetSet(explicitTS); err != nil {
		return domain.TargetSet{}, "", domain.ScanSourceSelector{}, nil, nil, err
	} else if ok {
		if sc.State != "completed" || sc.ProjectID != projectID {
			return domain.TargetSet{}, "", domain.ScanSourceSelector{}, nil, nil, domain.ErrInvalidTarget
		}
		if err := app.CheckCaptureActor(tx, actor, sc); err != nil {
			return domain.TargetSet{}, "", domain.ScanSourceSelector{}, nil, nil, domain.ErrInvalidTarget
		}
		var scSel struct {
			VersionID  string `json:"version_id"`
			SourceMode string `json:"source_mode"`
			Selections []struct {
				SourceID string `json:"source_id"`
				Revision string `json:"revision"`
			} `json:"selections"`
		}
		if json.Unmarshal(sc.Selection, &scSel) != nil {
			return domain.TargetSet{}, "", domain.ScanSourceSelector{}, nil, nil, domain.ErrInvalidTarget
		}
		if scSel.VersionID != "" && scSel.VersionID != v.ID {
			return domain.TargetSet{}, "", domain.ScanSourceSelector{}, nil, nil, domain.ErrInvalidTarget
		}
		tsMode := scSel.SourceMode
		if tsMode != "pinned" && tsMode != "current" {
			tsMode = "pinned"
		}
		if err := checkModeCompat(tsMode); err != nil {
			return domain.TargetSet{}, "", domain.ScanSourceSelector{}, nil, nil, err
		}
		reads, err := allSourceCaptureReads(tx, sc.ID)
		if err != nil {
			return domain.TargetSet{}, "", domain.ScanSourceSelector{}, nil, nil, err
		}
		readByID := map[string]domain.SourceCaptureRead{}
		for _, r := range reads {
			readByID[r.ID] = r
		}
		type tsSourceItem struct {
			SourceID string `json:"source_id"`
			ReadID   string `json:"read_id"`
			Identity struct {
				Kind          string `json:"kind"`
				RepoID        string `json:"repo_id"`
				ObjectFormat  string `json:"object_format"`
				ExactRevision string `json:"exact_revision"`
				SnapshotSHA   string `json:"snapshot_sha256"`
			} `json:"identity"`
		}
		var tsEnv struct {
			Sources []tsSourceItem `json:"sources"`
		}
		if json.Unmarshal(ts.TargetsJSON, &tsEnv) != nil {
			return domain.TargetSet{}, "", domain.ScanSourceSelector{}, nil, nil, domain.ErrInvalidTarget
		}
		sourceIDs := []string{}
		seenSource := map[string]bool{}
		itemsBySource := map[string][]tsSourceItem{}
		for _, item := range tsEnv.Sources {
			if item.SourceID == "" {
				continue
			}
			if !seenSource[item.SourceID] {
				seenSource[item.SourceID] = true
				sourceIDs = append(sourceIDs, item.SourceID)
			}
			itemsBySource[item.SourceID] = append(itemsBySource[item.SourceID], item)
		}
		targets := []domain.ScanSourceTarget{}
		var objectHashes []string
		seenObjHash := map[string]bool{}
		for _, sid := range sourceIDs {
			group := itemsBySource[sid]
			if len(group) == 0 {
				continue
			}
			first := group[0]
			kind := first.Identity.Kind
			if kind == "snapshot" || kind == "" {
				if first.Identity.ExactRevision != "" {
					kind = "git"
				} else {
					kind = "files"
				}
			}
			exactRev := first.Identity.ExactRevision
			allSelectedSnapshotReady := true
			anyReferenceOnly := false
			anyDenied := errors.Is(mapErr, domain.ErrDenied)
			firstUnavailableReason := ""
			var digests []readPathDigest
			for _, item := range group {
				if item.Identity.ExactRevision != "" && exactRev == "" {
					exactRev = item.Identity.ExactRevision
				}
				r, found := readByID[item.ReadID]
				if !found {
					allSelectedSnapshotReady = false
					if firstUnavailableReason == "" {
						firstUnavailableReason = "missing_source"
					}
					continue
				}
				if r.Availability == "denied" {
					anyDenied = true
					allSelectedSnapshotReady = false
					continue
				}
				var loc struct {
					Path string `json:"path"`
				}
				_ = json.Unmarshal(r.LocationJSON, &loc)
				readHash := r.Hash
				if readHash == "" {
					readHash = item.Identity.SnapshotSHA
				}
				if readHash != "" {
					digests = append(digests, readPathDigest{
						ReadID: item.ReadID,
						Path:   loc.Path,
						Hash:   readHash,
					})
				}
				if r.RetentionMode == "selected_snapshot" && r.Availability == "available" && r.Hash != "" {
					if obj, oe := tx.Object(r.Hash); oe == nil && obj.State == "ready" {
						if !seenObjHash[r.Hash] {
							seenObjHash[r.Hash] = true
							objectHashes = append(objectHashes, r.Hash)
						}
					} else {
						allSelectedSnapshotReady = false
						if firstUnavailableReason == "" {
							firstUnavailableReason = "missing_retained_bytes"
						}
					}
				} else if r.RetentionMode == "reference_only" {
					allSelectedSnapshotReady = false
					anyReferenceOnly = true
					if firstUnavailableReason == "" {
						firstUnavailableReason = "missing_retained_bytes"
					}
				} else {
					allSelectedSnapshotReady = false
					if firstUnavailableReason == "" {
						if r.Reason != "" {
							firstUnavailableReason = r.Reason
						} else {
							firstUnavailableReason = "missing_source"
						}
					}
				}
			}
			snapSHA := compositeReadDigestsSHA(digests)
			if snapSHA == "" && len(group) == 1 {
				snapSHA = first.Identity.SnapshotSHA
			}
			sel := domain.ScanSourceSelector{
				ProjectID:   projectID,
				SourceID:    sid,
				SourceIDs:   []string{sid},
				Mode:        tsMode,
				Revision:    exactRev,
				SnapshotID:  snapSHA,
				TargetSetID: ts.ID,
			}
			dm := declaredMetaByID[sid]
			st := domain.ScanSourceTarget{
				ProjectID:          projectID,
				SourceID:           sid,
				Kind:               kind,
				Mode:               tsMode,
				SelectionSource:    "target_set",
				ResolvedRevision:   exactRev,
				SnapshotSHA256:     snapSHA,
				AssetPath:          dm.AssetPath,
				Availability:       "unavailable",
				Reason:             "missing_source",
				CapturedAt:         ts.CapturedAt,
				RequestedSelection: sel,
			}
			if anyDenied {
				st.Availability = "denied"
				st.Reason = "source_denied"
				st.BytesOrigin = ""
			} else if allSelectedSnapshotReady && len(digests) == len(group) {
				st.Availability = "available"
				st.Reason = ""
				st.BytesOrigin = "retained_snapshot"
				if dm.ExactRevision != "" && exactRev != "" && dm.ExactRevision != exactRev {
					st.ReassessmentNeeded = true
				}
				declaredSnap := dm.SnapshotSHA
				if declaredSnap == "" {
					declaredSnap = dm.BytesSHA
				}
				if declaredSnap != "" && snapSHA != "" && declaredSnap != snapSHA {
					st.ReassessmentNeeded = true
				}
			} else if kind == "git" && exactRev != "" && anyReferenceOnly && firstUnavailableReason == "missing_retained_bytes" {
				// Reference-only git capture retains exact commit identity; actual live checkout is verified in Phase 2.
				st.Availability = "available"
				st.Reason = ""
				st.BytesOrigin = "approved_source"
				if dm.ExactRevision != "" && dm.ExactRevision != exactRev {
					st.ReassessmentNeeded = true
				}
			} else if firstUnavailableReason != "" {
				st.Availability = "unavailable"
				st.Reason = firstUnavailableReason
				st.BytesOrigin = ""
			}
			targets = append(targets, st)
		}
		if err := checkTargetsCompat(targets); err != nil {
			return domain.TargetSet{}, "", domain.ScanSourceSelector{}, nil, nil, err
		}
		sort.Strings(sourceIDs)
		topSel := domain.ScanSourceSelector{
			ProjectID:   projectID,
			SourceIDs:   sourceIDs,
			Mode:        tsMode,
			TargetSetID: ts.ID,
		}
		if len(targets) == 1 {
			topSel.SourceID = targets[0].SourceID
			topSel.Revision = targets[0].ResolvedRevision
			topSel.SnapshotID = targets[0].SnapshotSHA256
		}
		return ts, tsMode, topSel, targets, objectHashes, nil
	}

	// Family 2: Committed Source Observation Run (observation_runs + source_observations)
	if obsRun, ok, err := tx.ObservationRunByTargetSet(explicitTS); err != nil {
		return domain.TargetSet{}, "", domain.ScanSourceSelector{}, nil, nil, err
	} else if ok {
		if !obsRun.Committed || obsRun.VersionID != v.ID || actor.Scope.Kind != "owner" {
			return domain.TargetSet{}, "", domain.ScanSourceSelector{}, nil, nil, domain.ErrInvalidTarget
		}
		tsMode := "current"
		if err := checkModeCompat(tsMode); err != nil {
			return domain.TargetSet{}, "", domain.ScanSourceSelector{}, nil, nil, err
		}
		obsRows, err := tx.ObservationsByRun(obsRun.ID)
		if err != nil {
			return domain.TargetSet{}, "", domain.ScanSourceSelector{}, nil, nil, err
		}
		sourceIDs := make([]string, 0, len(obsRows))
		targets := make([]domain.ScanSourceTarget, 0, len(obsRows))
		for _, o := range obsRows {
			sourceIDs = append(sourceIDs, o.SourceID)
			sel := domain.ScanSourceSelector{
				ProjectID:   projectID,
				SourceID:    o.SourceID,
				SourceIDs:   []string{o.SourceID},
				Mode:        tsMode,
				Revision:    o.ObservedRevision,
				TargetSetID: ts.ID,
			}
			avail := "unavailable"
			if o.Availability == "available" || o.Availability == "captured" {
				avail = "available"
			} else if o.Availability == "denied" {
				avail = "denied"
			}
			if errors.Is(mapErr, domain.ErrDenied) {
				avail = "denied"
			}
			reason := o.Reason
			if avail == "denied" && reason == "" {
				reason = "source_denied"
			}
			st := domain.ScanSourceTarget{
				ProjectID:          projectID,
				SourceID:           o.SourceID,
				Kind:               "git",
				Mode:               tsMode,
				SelectionSource:    "target_set",
				ResolvedRevision:   o.ObservedRevision,
				BytesOrigin:        "approved_source",
				Availability:       avail,
				Reason:             reason,
				ReassessmentNeeded: o.Classification == "direct" || o.Classification == "indirect",
				CapturedAt:         ts.CapturedAt,
				RequestedSelection: sel,
			}
			if avail != "available" {
				st.BytesOrigin = ""
			}
			targets = append(targets, st)
		}
		if err := checkTargetsCompat(targets); err != nil {
			return domain.TargetSet{}, "", domain.ScanSourceSelector{}, nil, nil, err
		}
		sort.Strings(sourceIDs)
		topSel := domain.ScanSourceSelector{
			ProjectID:   projectID,
			SourceIDs:   sourceIDs,
			Mode:        tsMode,
			TargetSetID: ts.ID,
		}
		return ts, tsMode, topSel, targets, nil, nil
	}

	// Family 3: Exact Citation Verification (citation_verifications)
	if verifs, err := tx.VerificationsByTargetSet(v.ID, explicitTS); err != nil {
		return domain.TargetSet{}, "", domain.ScanSourceSelector{}, nil, nil, err
	} else if len(verifs) > 0 {
		if _, err := tx.VersionCapture(v.ID, explicitTS); err != nil {
			return domain.TargetSet{}, "", domain.ScanSourceSelector{}, nil, nil, domain.ErrInvalidTarget
		}
		tsMode := "pinned"
		if err := checkModeCompat(tsMode); err != nil {
			return domain.TargetSet{}, "", domain.ScanSourceSelector{}, nil, nil, err
		}
		var tsEnv struct {
			Sources []struct {
				SourceID      *string `json:"source_id"`
				Kind          string  `json:"kind"`
				RepoID        string  `json:"repo_id"`
				ExactRevision string  `json:"exact_revision"`
				SnapshotSHA   string  `json:"snapshot_sha256"`
			} `json:"sources"`
		}
		_ = json.Unmarshal(ts.TargetsJSON, &tsEnv)
		vf := verifs[len(verifs)-1]
		sid := ""
		if len(tsEnv.Sources) > 0 && tsEnv.Sources[0].SourceID != nil {
			sid = *tsEnv.Sources[0].SourceID
		}
		if sid == "" && len(declared) > 0 {
			sid = declared[0].ID
		}
		kind := "git"
		exactRev := ""
		snapSHA := ""
		if len(tsEnv.Sources) > 0 {
			if tsEnv.Sources[0].Kind == "snapshot" {
				kind = "files"
			}
			exactRev = tsEnv.Sources[0].ExactRevision
			snapSHA = tsEnv.Sources[0].SnapshotSHA
		}
		sel := domain.ScanSourceSelector{
			ProjectID:   projectID,
			SourceID:    sid,
			SourceIDs:   []string{sid},
			Mode:        tsMode,
			Revision:    exactRev,
			SnapshotID:  snapSHA,
			TargetSetID: ts.ID,
		}
		avail := "unavailable"
		origin := ""
		reason := vf.Reason
		var objHashes []string
		if errors.Is(mapErr, domain.ErrDenied) {
			avail = "denied"
			reason = "source_denied"
		} else if vf.Outcome == "captured" || vf.Outcome == "verified" {
			if vf.BytesSHA != "" {
				if obj, oe := tx.Object(vf.BytesSHA); oe == nil && obj.State == "ready" {
					avail = "available"
					origin = "retained_snapshot"
					reason = ""
					objHashes = append(objHashes, vf.BytesSHA)
					if snapSHA == "" {
						snapSHA = vf.BytesSHA
					}
				}
			}
			if avail != "available" && kind == "git" && exactRev != "" {
				avail = "available"
				origin = "approved_source"
				reason = ""
			}
		}
		st := domain.ScanSourceTarget{
			ProjectID:          projectID,
			SourceID:           sid,
			Kind:               kind,
			Mode:               tsMode,
			SelectionSource:    "target_set",
			ResolvedRevision:   exactRev,
			SnapshotSHA256:     snapSHA,
			BytesOrigin:        origin,
			Availability:       avail,
			Reason:             reason,
			CapturedAt:         ts.CapturedAt,
			RequestedSelection: sel,
		}
		targets := []domain.ScanSourceTarget{st}
		if err := checkTargetsCompat(targets); err != nil {
			return domain.TargetSet{}, "", domain.ScanSourceSelector{}, nil, nil, err
		}
		return ts, tsMode, sel, targets, objHashes, nil
	}

	// Family 4: Previously committed Scan TargetSet
	if committed, err := tx.ScanTargetSetCommitted(projectID, v.ID, explicitTS); err != nil {
		return domain.TargetSet{}, "", domain.ScanSourceSelector{}, nil, nil, err
	} else if committed {
		var sel domain.ScanSourceSelector
		_ = json.Unmarshal(ts.RequestedSelection, &sel)
		tsMode := sel.Mode
		if tsMode != "pinned" && tsMode != "current" {
			tsMode = "pinned"
		}
		if err := checkModeCompat(tsMode); err != nil {
			return domain.TargetSet{}, "", domain.ScanSourceSelector{}, nil, nil, err
		}
		sel.ProjectID = projectID
		sel.Mode = tsMode
		sel.TargetSetID = ts.ID
		var env struct {
			Sources []domain.ScanSourceTarget `json:"sources"`
		}
		var targets []domain.ScanSourceTarget
		if json.Unmarshal(ts.TargetsJSON, &env) == nil && env.Sources != nil {
			targets = env.Sources
		} else {
			_ = json.Unmarshal(ts.TargetsJSON, &targets)
		}
		var objHashes []string
		for i := range targets {
			targets[i].ProjectID = projectID
			if targets[i].Mode == "" {
				targets[i].Mode = tsMode
			}
			targets[i].SelectionSource = "target_set"
			if targets[i].RequestedSelection.ProjectID == "" {
				targets[i].RequestedSelection = sel
			}
			if errors.Is(mapErr, domain.ErrDenied) {
				targets[i].Availability = "denied"
				targets[i].Reason = "source_denied"
				targets[i].BytesOrigin = ""
			} else if targets[i].Availability == "available" && targets[i].BytesOrigin == "retained_snapshot" && targets[i].SnapshotSHA256 != "" {
				if obj, oe := tx.Object(targets[i].SnapshotSHA256); oe == nil && obj.State == "ready" {
					objHashes = append(objHashes, targets[i].SnapshotSHA256)
				}
			}
		}
		if err := checkTargetsCompat(targets); err != nil {
			return domain.TargetSet{}, "", domain.ScanSourceSelector{}, nil, nil, err
		}
		return ts, tsMode, sel, targets, objHashes, nil
	}

	return domain.TargetSet{}, "", domain.ScanSourceSelector{}, nil, nil, domain.ErrInvalidTarget
}

func resolvePreparedVersionSources(ctx context.Context, prep preparedVersionSources, now string) (VersionSourceDecision, *domain.TargetSet, error) {
	if prep.BoundTargetSet != nil {
		ts := *prep.BoundTargetSet
		sel := *prep.BoundSelection
		targets := append([]domain.ScanSourceTarget(nil), prep.BoundTargets...)
		// Re-verify any git target in BoundTargets whose BytesOrigin is approved_source (not retained_snapshot)
		for i := range targets {
			t := &targets[i]
			if t.Availability == "available" && t.BytesOrigin == "approved_source" && t.Kind == "git" && t.ResolvedRevision != "" {
				m, err := findSourceMapping(prep.Mappings, t.SourceID, "git", "")
				if err != nil {
					t.Availability = "unavailable"
					t.Reason = err.Error()
					t.BytesOrigin = ""
				} else {
					if prep.OnProbe != nil {
						prep.OnProbe(prep.ProjectID, prep.VersionID, t.SourceID, "git_commit_exists")
					}
					if !gitCommitExists(ctx, m.RootPath, t.ResolvedRevision) {
						t.Availability = "unavailable"
						t.Reason = "revision_unavailable"
						t.BytesOrigin = ""
					}
				}
			}
		}
		selBytes, _ := json.Marshal(sel)
		targetBytes, _ := json.Marshal(map[string]any{"sources": targets})
		return VersionSourceDecision{
			SelectedMode:         prep.SelectedMode,
			SelectionSource:      "target_set",
			TargetSetID:          ts.ID,
			Selection:            sel,
			SelectionJSON:        selBytes,
			Targets:              targets,
			TargetsJSON:          targetBytes,
			RetainedObjectHashes: prep.BoundObjectHashes,
		}, nil, nil
	}

	sourceIDs := make([]string, 0, len(prep.DeclaredSources))
	for _, ds := range prep.DeclaredSources {
		sourceIDs = append(sourceIDs, ds.ID)
	}
	normSel := domain.ScanSourceSelector{
		ProjectID: prep.ProjectID,
		SourceIDs: sourceIDs,
		Mode:      prep.SelectedMode,
	}
	if prep.ProjectWideSelector != nil {
		normSel.Revision = strings.TrimSpace(prep.ProjectWideSelector.Revision)
		normSel.Branch = strings.TrimSpace(prep.ProjectWideSelector.Branch)
		normSel.SnapshotID = strings.TrimSpace(prep.ProjectWideSelector.SnapshotID)
	} else if len(prep.PerSourceSelectors) == 1 && len(prep.DeclaredSources) == 1 {
		for sid, s := range prep.PerSourceSelectors {
			normSel.SourceID = sid
			normSel.Revision = strings.TrimSpace(s.Revision)
			normSel.Branch = strings.TrimSpace(s.Branch)
			normSel.SnapshotID = strings.TrimSpace(s.SnapshotID)
		}
	}

	targets := make([]domain.ScanSourceTarget, 0, len(prep.DeclaredSources))
	var retainedHashes []string
	seenHash := map[string]bool{}
	addHash := func(h string) {
		if h != "" && !seenHash[h] {
			seenHash[h] = true
			retainedHashes = append(retainedHashes, h)
		}
	}

	for _, ds := range prep.DeclaredSources {
		var sm struct {
			Kind          string `json:"kind"`
			RepoID        string `json:"repo_id"`
			ExactRevision string `json:"exact_revision"`
			SnapshotSHA   string `json:"snapshot_sha256"`
			BytesSHA      string `json:"bytes_sha256"`
			AssetPath     string `json:"asset_path"`
			RetentionMode string `json:"retention_mode"`
		}
		_ = json.Unmarshal(ds.Metadata, &sm)
		if sm.Kind == "" || sm.Kind == "snapshot" {
			if sm.RepoID != "" || sm.ExactRevision != "" {
				sm.Kind = "git"
			} else {
				sm.Kind = "files"
			}
		}

		var dsSelector *domain.ScanSourceSelector
		if sel, ok := prep.PerSourceSelectors[ds.ID]; ok {
			selCopy := sel
			dsSelector = &selCopy
		} else if prep.ProjectWideSelector != nil {
			dsSelector = prep.ProjectWideSelector
		}

		explicitSrcMode := prep.RequestSourceMode
		if explicitSrcMode == "" && dsSelector != nil && dsSelector.Mode != "" {
			explicitSrcMode = strings.TrimSpace(dsSelector.Mode)
		}
		dsMode, dsSelSource := resolveFallbackSourceMode(prep.Config, prep.ProjectID, prep.SourceIntent, explicitSrcMode)

		itemSel := domain.ScanSourceSelector{
			ProjectID: prep.ProjectID,
			SourceID:  ds.ID,
			SourceIDs: []string{ds.ID},
			Mode:      dsMode,
		}
		explicitRev := ""
		explicitBranch := ""
		explicitSnap := ""
		if dsSelector != nil {
			explicitRev = strings.TrimSpace(dsSelector.Revision)
			explicitBranch = strings.TrimSpace(dsSelector.Branch)
			explicitSnap = strings.TrimSpace(dsSelector.SnapshotID)
			itemSel.Revision = explicitRev
			itemSel.Branch = explicitBranch
			itemSel.SnapshotID = explicitSnap
		}
		if sm.Kind == "files" && (explicitRev != "" || explicitBranch != "") {
			return VersionSourceDecision{}, nil, domain.ErrInvalidTarget
		}
		if sm.Kind == "git" && explicitSnap != "" {
			return VersionSourceDecision{}, nil, domain.ErrInvalidTarget
		}
		if dsMode == "pinned" && itemSel.Branch != "" {
			return VersionSourceDecision{}, nil, domain.ErrInvalidTarget
		}
		if dsMode == "pinned" && sm.Kind == "git" && explicitRev != "" && sm.ExactRevision != "" && explicitRev != sm.ExactRevision {
			if prep.RetainedReads[ds.ID+":"+explicitRev] == "" && (sm.RepoID == "" || prep.RetainedReads["repo:"+sm.RepoID+":"+explicitRev] == "") {
				return VersionSourceDecision{}, nil, domain.ErrInvalidTarget
			}
		}
		if dsMode == "pinned" && sm.Kind == "files" && explicitSnap != "" {
			matchesDeclared := (sm.SnapshotSHA != "" && explicitSnap == sm.SnapshotSHA) ||
				(sm.BytesSHA != "" && explicitSnap == sm.BytesSHA) ||
				(prep.RetainedAssets[ds.ID] != "" && explicitSnap == prep.RetainedAssets[ds.ID])
			matchesRetained := prep.RetainedReads[ds.ID+":"+explicitSnap] != "" || prep.RetainedReads["sha:"+explicitSnap] != ""
			if !matchesDeclared && !matchesRetained {
				return VersionSourceDecision{}, nil, domain.ErrInvalidTarget
			}
		}
		if dsMode == "current" && sm.Kind == "files" && explicitSnap != "" {
			if prep.CurrentRetainedReads[ds.ID+":"+explicitSnap] == "" && prep.RetainedReads[ds.ID+":"+explicitSnap] == "" {
				return VersionSourceDecision{}, nil, domain.ErrInvalidTarget
			}
		}

		if dsMode == "pinned" && itemSel.Revision == "" && sm.ExactRevision != "" {
			itemSel.Revision = sm.ExactRevision
		}
		if dsMode == "pinned" && itemSel.SnapshotID == "" {
			if sm.SnapshotSHA != "" {
				itemSel.SnapshotID = sm.SnapshotSHA
			} else if sm.BytesSHA != "" {
				itemSel.SnapshotID = sm.BytesSHA
			}
		}
		target := domain.ScanSourceTarget{
			ProjectID:          prep.ProjectID,
			SourceID:           ds.ID,
			Kind:               sm.Kind,
			Mode:               dsMode,
			SelectionSource:    dsSelSource,
			AssetPath:          sm.AssetPath,
			CapturedAt:         now,
			RequestedSelection: itemSel,
		}
		if errors.Is(prep.MappingErr, domain.ErrDenied) {
			target.Availability = "denied"
			target.Reason = "source_denied"
			targets = append(targets, target)
			continue
		}
		if dsMode == "pinned" {
			resolvePinnedTarget(ctx, prep, ds.ID, sm.Kind, sm.RepoID, sm.ExactRevision, sm.SnapshotSHA, sm.BytesSHA, itemSel.Revision, itemSel.SnapshotID, &target)
		} else {
			resolveCurrentTarget(ctx, prep, ds.ID, sm.Kind, sm.RepoID, sm.ExactRevision, sm.SnapshotSHA, sm.BytesSHA, itemSel.Revision, itemSel.Branch, explicitSnap, &target)
		}
		if target.Availability == "available" && target.BytesOrigin == "retained_snapshot" {
			if h := prep.RetainedAssets[ds.ID]; h != "" {
				addHash(h)
			}
			for _, h := range prep.RetainedReadHashes[ds.ID+":"+target.SnapshotSHA256] {
				addHash(h)
			}
			for _, h := range prep.RetainedReadHashes["sha:"+target.SnapshotSHA256] {
				addHash(h)
			}
		}
		targets = append(targets, target)
	}

	tsID, err := service.NewID()
	if err != nil {
		return VersionSourceDecision{}, nil, err
	}
	normSel.TargetSetID = tsID
	for i := range targets {
		targets[i].RequestedSelection.TargetSetID = tsID
	}
	selBytes, _ := json.Marshal(normSel)
	targetBytes, _ := json.Marshal(map[string]any{"sources": targets})
	newTS := &domain.TargetSet{
		ID:                 tsID,
		ProjectID:          prep.ProjectID,
		RequestedSelection: selBytes,
		TargetsJSON:        targetBytes,
		CapturedAt:         now,
	}
	return VersionSourceDecision{
		SelectedMode:         prep.SelectedMode,
		SelectionSource:      prep.SelectionSource,
		TargetSetID:          tsID,
		Selection:            normSel,
		SelectionJSON:        selBytes,
		Targets:              targets,
		TargetsJSON:          targetBytes,
		RetainedObjectHashes: retainedHashes,
	}, newTS, nil
}

// resolvePinnedTarget resolves the package-recorded exact Git revision /
// snapshot / retained selection. It NEVER resolves HEAD as its selected target,
// and available current bytes never replace unavailable pinned bytes.
func resolvePinnedTarget(ctx context.Context, prep preparedVersionSources, sourceID, kind, repoID, declaredRev, declaredSnapSHA, declaredBytesSHA, exactRev, snapshotSHA string, target *domain.ScanSourceTarget) {
	if hash := prep.RetainedAssets[sourceID]; hash != "" {
		revMatches := exactRev == "" || exactRev == declaredRev
		snapMatches := snapshotSHA == "" || snapshotSHA == hash || snapshotSHA == declaredSnapSHA || snapshotSHA == declaredBytesSHA
		if revMatches && snapMatches {
			target.Availability = "available"
			target.BytesOrigin = "retained_snapshot"
			target.ResolvedRevision = exactRev
			if snapshotSHA != "" {
				target.SnapshotSHA256 = snapshotSHA
			} else {
				target.SnapshotSHA256 = hash
			}
			return
		}
	}
	if snapshotSHA != "" {
		if hash := prep.RetainedReads[sourceID+":"+snapshotSHA]; hash != "" {
			target.Availability = "available"
			target.BytesOrigin = "retained_snapshot"
			target.SnapshotSHA256 = hash
			return
		}
		if hash := prep.RetainedReads["sha:"+snapshotSHA]; hash != "" {
			target.Availability = "available"
			target.BytesOrigin = "retained_snapshot"
			target.SnapshotSHA256 = hash
			return
		}
	}
	if exactRev != "" {
		if hash := prep.RetainedReads[sourceID+":"+exactRev]; hash != "" {
			target.Availability = "available"
			target.BytesOrigin = "retained_snapshot"
			target.ResolvedRevision = exactRev
			target.SnapshotSHA256 = hash
			return
		}
		if repoID != "" {
			if hash := prep.RetainedReads["repo:"+repoID+":"+exactRev]; hash != "" {
				target.Availability = "available"
				target.BytesOrigin = "retained_snapshot"
				target.ResolvedRevision = exactRev
				target.SnapshotSHA256 = hash
				return
			}
		}
	}
	if kind == "git" {
		if exactRev == "" {
			target.Availability = "unavailable"
			target.Reason = "pinned_revision_missing"
			return
		}
		m, err := findSourceMapping(prep.Mappings, sourceID, "git", repoID)
		if err != nil {
			target.Availability = "unavailable"
			target.Reason = err.Error()
			return
		}
		if prep.OnProbe != nil {
			prep.OnProbe(prep.ProjectID, prep.VersionID, sourceID, "git_commit_exists")
		}
		if !gitCommitExists(ctx, m.RootPath, exactRev) {
			target.Availability = "unavailable"
			target.Reason = "revision_unavailable"
			return
		}
		target.Availability = "available"
		target.BytesOrigin = "approved_source"
		target.ResolvedRevision = exactRev
		return
	}
	// Snapshot / files source in pinned mode requires retained bytes; live
	// mutable working-copy files never silently substitute for a missing pinned snapshot.
	_, err := findSourceMapping(prep.Mappings, sourceID, "files", "")
	if err != nil {
		target.Availability = "unavailable"
		target.Reason = err.Error()
		return
	}
	target.Availability = "unavailable"
	target.Reason = "missing_retained_bytes"
}

func resolveCurrentTarget(ctx context.Context, prep preparedVersionSources, sourceID, kind, repoID, declaredRev, declaredSnapSHA, declaredBytesSHA, overrideRev, overrideBranch, explicitSnap string, target *domain.ScanSourceTarget) {
	if kind == "git" {
		m, err := findSourceMapping(prep.Mappings, sourceID, "git", repoID)
		if err != nil {
			target.Availability = "unavailable"
			target.Reason = err.Error()
			return
		}
		ref := "HEAD"
		if overrideRev != "" {
			ref = overrideRev
		} else if overrideBranch != "" {
			ref = overrideBranch
		}
		if prep.OnProbe != nil {
			prep.OnProbe(prep.ProjectID, prep.VersionID, sourceID, "git_resolve_ref")
		}
		resolved, err := gitResolveRef(ctx, m.RootPath, ref)
		if err != nil {
			target.Availability = "unavailable"
			target.Reason = "revision_unavailable"
			return
		}
		target.Availability = "available"
		target.BytesOrigin = "approved_source"
		target.ResolvedRevision = resolved
		if declaredRev != "" && declaredRev != resolved {
			target.ReassessmentNeeded = true
		}
		return
	}
	m, err := findSourceMapping(prep.Mappings, sourceID, "files", "")
	if err != nil {
		target.Availability = "unavailable"
		target.Reason = err.Error()
		return
	}
	info, statErr := os.Stat(m.RootPath)
	if statErr != nil || !info.IsDir() {
		target.Availability = "unavailable"
		target.Reason = "missing_source"
		return
	}
	if explicitSnap != "" {
		if hash := prep.CurrentRetainedReads[sourceID+":"+explicitSnap]; hash != "" {
			target.Availability = "available"
			target.BytesOrigin = "retained_snapshot"
			target.SnapshotSHA256 = hash
			target.RequestedSelection.SnapshotID = hash
			if (declaredSnapSHA != "" && declaredSnapSHA != hash) || (declaredBytesSHA != "" && declaredBytesSHA != hash) {
				target.ReassessmentNeeded = true
			}
			return
		}
		if hash := prep.RetainedReads[sourceID+":"+explicitSnap]; hash != "" {
			target.Availability = "available"
			target.BytesOrigin = "retained_snapshot"
			target.SnapshotSHA256 = hash
			target.RequestedSelection.SnapshotID = hash
			if (declaredSnapSHA != "" && declaredSnapSHA != hash) || (declaredBytesSHA != "" && declaredBytesSHA != hash) {
				target.ReassessmentNeeded = true
			}
			return
		}
	}
	// ScanSourceSelector supplies no selected file paths; implicit current files
	// without an applicable exact authorized capture fails closed with
	// source_capture_required and no fabricated snapshot or bytes_origin.
	target.Availability = "unavailable"
	target.Reason = "source_capture_required"
	target.BytesOrigin = ""
	target.SnapshotSHA256 = ""
}

func findSourceMapping(mappings []source.Mapping, sourceID, kind, repoID string) (source.Mapping, error) {
	if kind == "git" && repoID != "" {
		m, err := source.LinkGit(mappings, repoID)
		if err == nil {
			return m, nil
		}
		if errors.Is(err, source.ErrMappingAmbiguous) {
			return source.Mapping{}, err
		}
	}
	for _, m := range mappings {
		if m.SourceID == sourceID && (kind == "" || m.Kind == kind || (kind == "files" && m.Kind == "files")) {
			if m.RootPath == "" {
				return source.Mapping{}, errors.New("missing_source")
			}
			return m, nil
		}
	}
	return source.Mapping{}, errors.New("source_not_registered")
}

func gitCommitExists(ctx context.Context, rootPath, rev string) bool {
	if rootPath == "" || rev == "" {
		return false
	}
	if info, err := os.Stat(rootPath); err != nil || !info.IsDir() {
		return false
	}
	cctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(cctx, "git", "-C", filepath.Clean(rootPath), "cat-file", "-e", rev+"^{commit}")
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	return cmd.Run() == nil
}

func gitResolveRef(ctx context.Context, rootPath, ref string) (string, error) {
	if rootPath == "" {
		return "", errors.New("missing_source")
	}
	if info, err := os.Stat(rootPath); err != nil || !info.IsDir() {
		return "", errors.New("missing_source")
	}
	cctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(cctx, "git", "-C", filepath.Clean(rootPath), "rev-parse", "--verify", ref+"^{commit}")
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	var out bytes.Buffer
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		return "", err
	}
	resolved := strings.TrimSpace(out.String())
	if len(resolved) != 40 && len(resolved) != 64 {
		return "", errors.New("invalid_revision")
	}
	return resolved, nil
}
