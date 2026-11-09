package storage

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"reflect"
	"strings"

	"haeryong.local/haeryong/internal/domain"
)

const (
	maxNominationCardJSONBytes  = 8192
	maxNominationFrameBytes     = 144 * 1024
	nominationFixedFrameReserve = 8192
)

func encodedJSONTextBytes(raw []byte) int {
	encoded, _ := json.Marshal(string(raw))
	return len(encoded)
}

func encodedJSONFragmentBytes(raw []byte) int {
	return encodedJSONTextBytes(raw) - 2
}

// GuardProvider binds detached external work to its captured authority. A device
// disconnect is deliberately absent: accepted work belongs to its principal.
func GuardProvider(tx WriteTx, a domain.ProviderAttempt) error {
	j, e := tx.Job(a.JobID)
	if e != nil {
		return e
	}
	if !domain.ProviderJob(j.Kind) || j.State != "running" || j.CancelRequested || j.AttemptGeneration != providerApplyGeneration(a) {
		return domain.ErrStaleWorker
	}
	return guardProviderAuthority(tx, j, a)
}

// GuardProviderContinuation validates only an explicit queued saved-result
// reservation. Actual callbacks still require GuardProvider's running fence.
func GuardProviderContinuation(tx WriteTx, a domain.ProviderAttempt) error {
	j, e := tx.Job(a.JobID)
	if e != nil {
		return e
	}
	if !domain.ProviderJob(j.Kind) || j.State != "queued" || j.CancelRequested || a.State != "result_saved" || a.ApplyGeneration != j.AttemptGeneration+1 {
		return domain.ErrStaleWorker
	}
	return guardProviderAuthority(tx, j, a)
}

func guardProviderAuthority(tx WriteTx, j domain.Job, a domain.ProviderAttempt) error {
	l, e := tx.Library()
	if e != nil {
		return e
	}
	if l.RuntimeGeneration != a.Runtime {
		return domain.ErrReconciliation
	}
	h, e := tx.RecoveryHold()
	if e != nil {
		return e
	}
	if h != nil && h.State != "reviewed" {
		return domain.ErrReconciliation
	}
	var scope domain.ProviderScope
	if json.Unmarshal(j.Scope, &scope) != nil || scope.PrincipalID == "" || scope.TargetSetID != a.TargetSetID || scope.RequestSHA != a.RequestSHA {
		return domain.ErrDenied
	}
	state, e := tx.AuthState()
	if e != nil {
		return e
	}
	if state.AuthEpoch != scope.AuthEpoch {
		return domain.ErrDenied
	}
	if scope.PrincipalID != state.OwnerID {
		p, e := tx.WorkflowPrincipal(scope.PrincipalID)
		if e != nil {
			return e
		}
		if !p.Active {
			return domain.ErrDenied
		}
		switch j.Kind {
		case "generation":
			if scope.Purpose != "body" {
				return domain.ErrDenied
			}
			if e = AuthorizeGeneration(tx, scope); e != nil {
				return e
			}
		case "maintenance":
			if scope.Purpose != "body" {
				return domain.ErrDenied
			}
			st := a.State
			if j.State == "reconciling" {
				st = "reconciling"
			}
			if e = AuthorizeMaintenance(tx, j, scope, a.ID, st); e != nil {
				return e
			}
		default:
			return domain.ErrDenied
		}
	}
	if scope.Invocation != nil && scope.Invocation.Generation != nil {
		if scope.PrincipalID == state.OwnerID {
			if e = AuthorizeGeneration(tx, scope); e != nil {
				return e
			}
		}
		g, e := tx.GenerationIntent(j.ID)
		if e != nil {
			return e
		}
		if !g.AdmissionReady {
			return domain.ErrReconciliation
		}
		d, e := tx.Draft(g.DraftID)
		if e != nil {
			return e
		}
		if d.State != "preparing" && d.State != "ready" {
			return domain.ErrStaleWorker
		}
	}
	if scope.Invocation != nil && scope.Invocation.Question != nil {
		if j.Kind != "question" {
			return domain.ErrInvalidTarget
		}
		if e = AuthorizeQuestion(tx, scope); e != nil {
			return e
		}
		q, e := tx.Question(scope.Invocation.Question.QuestionID)
		if e != nil {
			return e
		}
		if q.JobID != j.ID || !q.AdmissionReady {
			return domain.ErrReconciliation
		}
	}
	if scope.Invocation != nil && scope.Invocation.Maintenance != nil {
		if j.Kind != "maintenance" {
			return domain.ErrInvalidTarget
		}
		st := a.State
		if j.State == "reconciling" {
			st = "reconciling"
		}
		if e = AuthorizeMaintenance(tx, j, scope, a.ID, st); e != nil {
			return e
		}
		m, e := tx.MaintenanceIntent(j.ID)
		if e != nil {
			return e
		}
		if !m.AdmissionReady {
			return domain.ErrReconciliation
		}
	}
	if e = AuthorizeTarget(tx, scope.Target); e != nil {
		return e
	}
	if a.TargetSetID != "" {
		target, e := tx.TargetSetByID(a.TargetSetID)
		if e != nil {
			return e
		}
		project, e := TargetProject(tx, scope.Target)
		if e != nil {
			return e
		}
		if project == "" || target.ProjectID != project {
			return domain.ErrDenied
		}
		if _, ok := j.Permissions[project]; !ok {
			return domain.ErrDenied
		}
	}
	if e = tx.CheckProviderPolicy(a); e != nil {
		return e
	}
	for id, rev := range j.Permissions {
		p, e := tx.Scope(id)
		if e != nil {
			return e
		}
		if p.Project.PermissionsRevision != rev {
			return domain.ErrDenied
		}
	}
	return nil
}

func providerApplyGeneration(a domain.ProviderAttempt) int64 {
	if a.State == "result_saved" && a.ApplyGeneration > 0 {
		return a.ApplyGeneration
	}
	return a.Generation
}

// ProviderSavedObjects enumerates the exact captured inputs and complete result
// whose actual bytes the coordinator verifies before consuming a continuation.
// Metadata readiness alone does not establish byte availability or integrity.
func ProviderSavedObjects(tx WriteTx, a domain.ProviderAttempt) ([]domain.Object, error) {
	if a.State != "result_saved" || a.ResultHash == "" {
		return nil, domain.ErrReconciliation
	}
	j, e := tx.Job(a.JobID)
	if e != nil {
		return nil, e
	}
	var scope domain.ProviderScope
	if json.Unmarshal(j.Scope, &scope) != nil {
		return nil, domain.ErrInvalidTarget
	}
	inputs, e := tx.ProviderInputs(a.ID)
	if e != nil {
		return nil, e
	}
	if len(inputs) != len(scope.ReadIDs) {
		return nil, domain.ErrReconciliation
	}
	byRead := map[string]domain.ProviderInput{}
	for _, input := range inputs {
		byRead[input.ReadID] = input
	}
	objects := make([]domain.Object, 0, len(inputs)+1)
	for _, id := range scope.ReadIDs {
		input, ok := byRead[id]
		if !ok {
			return nil, domain.ErrReconciliation
		}
		read, e := tx.CapturedRead(id)
		if e != nil {
			return nil, e
		}
		expectedTargetSet, _, _, ok := domain.MaintenanceReadTarget(scope, a.TargetSetID, "", id)
		if !ok || read.TargetSetID != expectedTargetSet || read.BytesSHA != input.Hash {
			return nil, domain.ErrReconciliation
		}
		object, e := tx.Object(input.Hash)
		if e != nil {
			return nil, e
		}
		if object.State != "ready" {
			return nil, domain.ErrReconciliation
		}
		objects = append(objects, object)
	}
	owned, e := ProviderInputObjects(tx, scope)
	if e != nil {
		return nil, e
	}
	objects = append(objects, owned...)
	result, e := tx.Object(a.ResultHash)
	if e != nil {
		return nil, e
	}
	if result.State != "ready" {
		return nil, domain.ErrReconciliation
	}
	return append(objects, result), nil
}

// GuardProviderReconciliation is used only after the storage adapter verifies
// the current executing reconciliation intent. It grants no dispatch authority.
func GuardProviderReconciliation(tx WriteTx, j domain.Job, a domain.ProviderAttempt) error {
	return guardProviderAuthority(tx, j, a)
}

func generationScopeAllows(scope domain.CredentialScope, project string) bool {
	action, anchor := false, false
	for _, v := range scope.Actions {
		action = action || v == "generation.request"
	}
	for _, v := range scope.ProjectIDs {
		anchor = anchor || v == project
	}
	return scope.Kind == "workflow" && action && anchor && project != ""
}

// AuthorizeGeneration checks only the already captured job-bound generation
// action. It cannot issue a credential or grant direct import/publication.
func AuthorizeGeneration(tx WriteTx, scope domain.ProviderScope) error {
	state, e := tx.AuthState()
	if e != nil {
		return e
	}
	if scope.AuthEpoch != state.AuthEpoch {
		return domain.ErrDenied
	}
	if scope.PrincipalID == state.OwnerID {
		return generationContextAuthority(tx, scope, domain.CredentialScope{Kind: "owner"})
	}
	current, e := tx.WorkflowPrincipal(scope.PrincipalID)
	if e != nil {
		return e
	}
	if !current.Active {
		return domain.ErrDenied
	}
	project, e := TargetProject(tx, scope.Target)
	if e != nil {
		return e
	}
	if !generationScopeAllows(scope.CredentialScope, project) || !generationScopeAllows(current.Scope, project) {
		return domain.ErrDenied
	}
	if len(scope.ReadIDs) > 0 {
		has := func(s domain.CredentialScope) bool {
			for _, a := range s.Actions {
				if a == "source.read" {
					return true
				}
			}
			return false
		}
		if !has(scope.CredentialScope) || !has(current.Scope) {
			return domain.ErrDenied
		}
	}
	if e = generationContextAuthority(tx, scope, current.Scope); e != nil {
		return e
	}
	return AuthorizeTarget(tx, scope.Target)
}

func maintenanceScopeAllows(scope domain.CredentialScope, projects map[string]bool, needSourceRead bool) bool {
	if scope.Kind != "workflow" || len(projects) == 0 {
		return false
	}
	hasAction := func(want string) bool {
		for _, a := range scope.Actions {
			if a == want {
				return true
			}
		}
		return false
	}
	if !hasAction("scan.request") || !hasAction("version.read") {
		return false
	}
	if needSourceRead && !hasAction("source.read") {
		return false
	}
	allowedProjects := map[string]bool{}
	for _, pid := range scope.ProjectIDs {
		allowedProjects[pid] = true
	}
	for pid := range projects {
		if pid == "" || !allowedProjects[pid] {
			return false
		}
	}
	return true
}

func AuthorizeMaintenance(tx WriteTx, j domain.Job, scope domain.ProviderScope, attemptID, attemptState string) error {
	state, e := tx.AuthState()
	if e != nil {
		return e
	}
	if scope.AuthEpoch != state.AuthEpoch || scope.ProviderID != "codex" {
		return domain.ErrDenied
	}
	if j.Kind != "maintenance" || j.ParentID == "" || scope.Invocation == nil || scope.Invocation.Maintenance == nil || scope.Invocation.Generation != nil || scope.Invocation.Question != nil || len(scope.Invocation.Maintenance.Explanations) == 0 {
		return domain.ErrInvalidTarget
	}
	m, e := tx.MaintenanceIntent(j.ID)
	if e != nil {
		return e
	}
	if m.Purged {
		return domain.ErrDenied
	}
	if m.ScanJobID != j.ParentID || m.ItemID == "" || m.ItemID != scope.Invocation.Maintenance.ItemID {
		return domain.ErrInvalidTarget
	}
	if m.Mode != scope.Invocation.Maintenance.Mode || (m.Mode != "" && m.Mode != domain.MaintenanceModeSemanticNomination) {
		return domain.ErrInvalidTarget
	}
	if attemptID != "" && !m.AdmissionReady {
		return domain.ErrReconciliation
	}
	parent, e := tx.Job(j.ParentID)
	if e != nil {
		return e
	}
	if parent.Kind != "scan" {
		return domain.ErrStaleWorker
	}
	if m.Mode == domain.MaintenanceModeSemanticNomination {
		if m.BatchID == "" || m.ItemID != m.BatchID || scope.Invocation.Maintenance.BatchID != m.BatchID {
			return domain.ErrInvalidTarget
		}
		if len(scope.ReadIDs) > 0 || len(m.Captures) > 0 || len(m.SourceReadIDs) > 0 || len(m.ReadBindings) > 0 || len(scope.Invocation.Maintenance.ReadBindings) > 0 {
			return domain.ErrInvalidTarget
		}
		nCards := len(m.Cards)
		if nCards < 1 || nCards > 64 || len(m.BatchItemIDs) != nCards || len(scope.Invocation.Maintenance.BatchItemIDs) != nCards || len(scope.Invocation.Maintenance.Explanations) != nCards {
			return domain.ErrInvalidTarget
		}
		if m.CardPayloadDigest == "" || scope.Invocation.Maintenance.CardPayloadDigest != m.CardPayloadDigest {
			return domain.ErrInvalidTarget
		}
		var promptEnv struct {
			Consumer string                            `json:"consumer"`
			Cards    []domain.MaintenanceProjectedCard `json:"cards"`
		}
		promptDec := json.NewDecoder(strings.NewReader(scope.Invocation.Prompt))
		promptDec.DisallowUnknownFields()
		if promptDec.Decode(&promptEnv) != nil || promptEnv.Consumer != "managed-maintenance-nomination" || len(promptEnv.Cards) != nCards {
			return domain.ErrInvalidTarget
		}
		var trailing any
		if promptDec.Decode(&trailing) != io.EOF {
			return domain.ErrInvalidTarget
		}
		canonicalPromptJSON, pErr := json.Marshal(promptEnv)
		if pErr != nil || string(canonicalPromptJSON) != scope.Invocation.Prompt {
			return domain.ErrInvalidTarget
		}
		if len(canonicalPromptJSON)+nominationFixedFrameReserve > maxNominationFrameBytes ||
			encodedJSONTextBytes(canonicalPromptJSON)+nominationFixedFrameReserve > maxNominationFrameBytes {
			return domain.ErrInvalidTarget
		}
		canonicalCardsJSON, mErr := json.Marshal(promptEnv.Cards)
		if mErr != nil {
			return domain.ErrInvalidTarget
		}
		sum := sha256.Sum256(canonicalCardsJSON)
		if "sha256:"+hex.EncodeToString(sum[:]) != m.CardPayloadDigest {
			return domain.ErrInvalidTarget
		}
		if attemptState == "reconciling" {
			if parent.State != "running" && parent.State != "queued" && parent.State != "interrupted" && parent.State != "paused" && parent.State != "reconciling" && parent.State != "cancelling" {
				return domain.ErrStaleWorker
			}
		} else {
			if parent.CancelRequested || (parent.State != "running" && parent.State != "queued") {
				return domain.ErrStaleWorker
			}
		}
		seenItems := make(map[string]bool, nCards)
		seenUnits := make(map[string]bool, nCards)
		for i, card := range m.Cards {
			wantUID := fmt.Sprintf("u%02d", i+1)
			if card.ItemID == "" || card.UnitID != wantUID || seenItems[card.ItemID] || seenUnits[card.UnitID] {
				return domain.ErrInvalidTarget
			}
			seenItems[card.ItemID] = true
			seenUnits[card.UnitID] = true
			if m.BatchItemIDs[i] != card.ItemID || scope.Invocation.Maintenance.BatchItemIDs[i] != card.ItemID {
				return domain.ErrInvalidTarget
			}
			expectedProjected := domain.MaintenanceProjectedCard{
				UnitID:              card.UnitID,
				UnitKind:            card.UnitKind,
				ProjectID:           card.ProjectID,
				VersionID:           card.VersionID,
				Title:               card.Title,
				Headings:            card.Headings,
				DeclaredTopics:      card.DeclaredTopics,
				SourcePaths:         card.SourcePaths,
				BodyExcerpt:         card.BodyExcerpt,
				ExcerptCoverage:     card.ExcerptCoverage,
				TotalBodyBytes:      card.TotalBodyBytes,
				AuthoredRefUnitIDs:  card.AuthoredRefUnitIDs,
				SharedSourceUnitIDs: card.SharedSourceUnitIDs,
			}
			pCard := promptEnv.Cards[i]
			if !reflect.DeepEqual(pCard, expectedProjected) {
				return domain.ErrInvalidTarget
			}
			rawCardBytes, cErr := json.Marshal(pCard)
			if cErr != nil || len(rawCardBytes) > maxNominationCardJSONBytes || encodedJSONFragmentBytes(rawCardBytes) > maxNominationCardJSONBytes {
				return domain.ErrInvalidTarget
			}
			exp := scope.Invocation.Maintenance.Explanations[i]
			expPage := ""
			if exp.PagePath != nil {
				expPage = *exp.PagePath
			}
			expReg := ""
			if exp.RegionID != nil {
				expReg = *exp.RegionID
			}
			if exp.Role != "card" || exp.UnitID != card.UnitID || exp.ItemID != card.ItemID || exp.UnitKind != card.UnitKind || exp.ProjectID != card.ProjectID || exp.DocumentID != card.DocumentID || exp.VersionID != card.VersionID || expPage != card.PagePath || expReg != card.RegionID || exp.ObjectHash != card.ObjectHash || exp.Size != card.ByteSize {
				return domain.ErrInvalidTarget
			}
			if attemptState != "reconciling" {
				item, e := tx.JobItem(parent.ID, card.ItemID)
				if e != nil {
					return domain.ErrStaleWorker
				}
				if item.ProjectID != card.ProjectID || item.DocumentID != card.DocumentID || item.VersionID != card.VersionID || item.PagePath != card.PagePath || item.RegionID != card.RegionID || item.UnitKind != card.UnitKind {
					return domain.ErrInvalidTarget
				}
				if item.Stage != "inspect" || (item.State != "running" && item.State != "pending") {
					return domain.ErrStaleWorker
				}
				if item.State == "running" && parent.State == "running" && item.AttemptGeneration != parent.AttemptGeneration {
					return domain.ErrStaleWorker
				}
			}
		}
	} else if attemptState == "reconciling" {
		if parent.State != "running" && parent.State != "queued" && parent.State != "interrupted" && parent.State != "paused" && parent.State != "reconciling" && parent.State != "cancelling" {
			return domain.ErrStaleWorker
		}
	} else {
		if parent.CancelRequested || (parent.State != "running" && parent.State != "queued") {
			return domain.ErrStaleWorker
		}
		item, e := tx.JobItem(parent.ID, m.ItemID)
		if e != nil {
			return domain.ErrStaleWorker
		}
		if item.Stage != "inspect" || (item.State != "running" && item.State != "pending") {
			return domain.ErrStaleWorker
		}
		if item.State == "running" && parent.State == "running" && item.AttemptGeneration != parent.AttemptGeneration {
			return domain.ErrStaleWorker
		}
	}
	run, e := tx.ScanRun(j.ParentID)
	if e != nil {
		return e
	}
	cfg, e := tx.Configuration()
	if e != nil {
		return e
	}
	var cfgParsed struct {
		Maintenance struct {
			MaxProviderAttemptsPerRun int `json:"max_provider_attempts_per_run"`
		} `json:"maintenance"`
		Projects map[string]struct {
			ProviderID  *string `json:"provider_id"`
			Maintenance *struct {
				MaxProviderAttemptsPerRun *int `json:"max_provider_attempts_per_run"`
			} `json:"maintenance"`
		} `json:"projects"`
	}
	if json.Unmarshal(cfg.Value, &cfgParsed) != nil {
		return domain.ErrDenied
	}
	maxAttempts := run.EffectivePolicy.MaxProviderAttemptsPerRun
	if cfgParsed.Maintenance.MaxProviderAttemptsPerRun > 0 && (maxAttempts <= 0 || cfgParsed.Maintenance.MaxProviderAttemptsPerRun < maxAttempts) {
		maxAttempts = cfgParsed.Maintenance.MaxProviderAttemptsPerRun
	}
	for _, pid := range run.RequestedScope.ProjectIDs {
		if p, ok := cfgParsed.Projects[pid]; ok && p.Maintenance != nil && p.Maintenance.MaxProviderAttemptsPerRun != nil && *p.Maintenance.MaxProviderAttemptsPerRun > 0 && (maxAttempts <= 0 || *p.Maintenance.MaxProviderAttemptsPerRun < maxAttempts) {
			maxAttempts = *p.Maintenance.MaxProviderAttemptsPerRun
		}
	}
	if (attemptState == "" || attemptState == "prepared") && maxAttempts > 0 {
		count, e := tx.ScanProviderAttemptCount(j.ParentID, attemptID)
		if e != nil {
			return e
		}
		if count >= maxAttempts {
			return domain.ErrDenied
		}
	}
	rosterVersions := map[string]bool{}
	afterOrd := 0
	for {
		cvs, e := tx.ScanCapturedVersions(parent.ID, afterOrd, 256)
		if e != nil {
			return e
		}
		if len(cvs) == 0 {
			break
		}
		for _, cv := range cvs {
			if cv.VersionOrdinal > afterOrd {
				afterOrd = cv.VersionOrdinal
			} else {
				afterOrd++
			}
			if cv.VersionID != "" && cv.ExclusionReason == "" && cv.EnumState != "purged" {
				rosterVersions[cv.VersionID] = true
			}
		}
		if len(cvs) < 256 {
			break
		}
	}
	projects := map[string]bool{}
	focalProject, e := TargetProject(tx, scope.Target)
	if e != nil {
		return e
	}
	projects[focalProject] = true
	for i, exp := range scope.Invocation.Maintenance.Explanations {
		if exp.ProjectID == "" || exp.DocumentID == "" || exp.VersionID == "" {
			return domain.ErrInvalidTarget
		}
		if i == 0 && exp.ProjectID != focalProject {
			return domain.ErrInvalidTarget
		}
		if len(rosterVersions) > 0 && !rosterVersions[exp.VersionID] {
			return domain.ErrDenied
		}
		projects[exp.ProjectID] = true
		pCfg, ok := cfgParsed.Projects[exp.ProjectID]
		if !ok || pCfg.ProviderID == nil || *pCfg.ProviderID != scope.ProviderID {
			return domain.ErrDenied
		}
		pScope, e := tx.Scope(exp.ProjectID)
		if e != nil {
			return e
		}
		if rev, ok := j.Permissions[exp.ProjectID]; !ok || rev != pScope.Project.PermissionsRevision {
			return domain.ErrPrecondition
		}
		if rev, ok := run.PermissionsRevisions[exp.ProjectID]; ok && rev != pScope.Project.PermissionsRevision {
			return domain.ErrPrecondition
		}
		doc, e := tx.Document(exp.DocumentID)
		if e != nil {
			return e
		}
		if doc.ProjectID != exp.ProjectID || doc.Lifecycle == "trash" || doc.Lifecycle == "purged" {
			return domain.ErrDenied
		}
		ver, e := tx.PublishedVersion(exp.VersionID)
		if e != nil {
			return e
		}
		if ver.DocumentID != exp.DocumentID {
			return domain.ErrDenied
		}
		obj, e := tx.Object(exp.ObjectHash)
		if e != nil {
			return e
		}
		if obj.State != "ready" || obj.Size != exp.Size {
			return domain.ErrContentIntegrity
		}
		if exp.TargetSetID != "" {
			ts, e := tx.TargetSetByID(exp.TargetSetID)
			if e != nil {
				return e
			}
			if ts.ProjectID != exp.ProjectID {
				return domain.ErrDenied
			}
		}
	}
	if scope.PrincipalID != state.OwnerID {
		current, e := tx.WorkflowPrincipal(scope.PrincipalID)
		if e != nil {
			return e
		}
		if !current.Active {
			return domain.ErrDenied
		}
		needSourceRead := len(scope.ReadIDs) > 0 || len(m.Captures) > 0
		effective := domain.RestrictCredentialScope(scope.CredentialScope, current.Scope)
		if run.AuthKind == "workflow" && run.PrincipalID == scope.PrincipalID {
			effective = domain.RestrictCredentialScope(run.CapturedScope, effective)
		}
		if !maintenanceScopeAllows(effective, projects, needSourceRead) {
			return domain.ErrDenied
		}
		actor := domain.Session{PrincipalID: scope.PrincipalID, AuthEpoch: scope.AuthEpoch, Scope: effective}
		for _, exp := range scope.Invocation.Maintenance.Explanations {
			if _, _, e := CheckSourceVersion(tx, actor, exp.VersionID); e != nil {
				return domain.ErrDenied
			}
		}
	}
	return AuthorizeTarget(tx, scope.Target)
}
