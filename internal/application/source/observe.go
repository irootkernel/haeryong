package source

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strings"

	app "haeryong.local/haeryong/internal/application/storage"
	"haeryong.local/haeryong/internal/domain"
	sec "haeryong.local/haeryong/internal/security/session"
)

// Observation scope kinds (plan §3): region requires page.
const (
	scopeVersion = "version"
	scopePage    = "page"
	scopeRegion  = "region"
)

// observationScope is the validated requested scope of one run. Declared is
// false when a region scope names a region the page does not declare: the
// run still records the real source facts and the region_not_declared reason,
// and fabricates no unit (plan §7.6).
type observationScope struct {
	Kind     string
	PagePath string
	RegionID string
	Declared bool
	Reason   string
}

// view is the observation scope record — stored on the run and its coverage
// set and returned in the 201: the anchor fields (null unless the scope names
// them) plus declared and reason. It is distinct from the frozen three-field
// applicable.scope.
func (s observationScope) view() map[string]any {
	out := map[string]any{"kind": s.Kind, "page_path": nil, "region_id": nil, "declared": s.Declared, "reason": nil}
	if s.Kind != scopeVersion {
		out["page_path"] = s.PagePath
	}
	if s.Kind == scopeRegion {
		out["region_id"] = s.RegionID
	}
	if s.Reason != "" {
		out["reason"] = s.Reason
	}
	return out
}

// decodeObservationScope reads a run's recorded scope copy back.
func decodeObservationScope(raw []byte) observationScope {
	var stored struct {
		Kind     string `json:"kind"`
		PagePath string `json:"page_path"`
		RegionID string `json:"region_id"`
		Declared bool   `json:"declared"`
		Reason   string `json:"reason"`
	}
	_ = json.Unmarshal(raw, &stored)
	return observationScope{Kind: stored.Kind, PagePath: stored.PagePath, RegionID: stored.RegionID, Declared: stored.Declared, Reason: stored.Reason}
}

// evidencePlan is one cited evidence item of an observed source with its own
// declared base revision (W3b L5).
type evidencePlan struct {
	evidenceID string
	path       string
	base       string
}

// sourcePlan is one requested configuration source with its cited evidence.
type sourcePlan struct {
	sourceID string
	rootPath string
	repoID   string
	bases    []string
	evidence []evidencePlan
}

// observationPlan is the reserve-stage plan: the effective requested source
// set (plan §7.1 step 2) and the validated scope.
type observationPlan struct {
	projectID string
	versionID string
	sources   []sourcePlan
	scope     observationScope
	selection []byte
}

// sourcePlanByID looks up one plan source by configuration id.
func (p *observationPlan) sourcePlanByID(id string) (sourcePlan, bool) {
	for _, s := range p.sources {
		if s.sourceID == id {
			return s, true
		}
	}
	return sourcePlan{}, false
}

// observationReservation carries the staged protocol's durable identity.
type observationReservation struct {
	intentID       string
	operation      string
	runID          string
	targetSetID    string
	persistedAt    string
	principalID    string
	versionID      string
	plan           observationPlan
	configRevision int64
	permissions    int64
}

// Observe runs the staged owner-only observation protocol (plan §7.3 as
// amended): reserve → resolve HEAD once per source and persist the run's
// single target set → read/diff outside the lock → commit. The response is a
// durable receipt result; an identical concurrent request receives the stable
// 202 pending envelope, and a crash resolves through the restart sweep.
func (s *Service) Observe(ctx context.Context, token, versionID, key string, rawBody []byte) (domain.Response, error) {
	if !uuidPattern.MatchString(key) {
		return domain.Response{}, sec.ErrInvalid
	}
	res, response, done, err := s.reserveObservation(ctx, token, versionID, key, rawBody)
	if done || err != nil {
		return response, err
	}
	bound := s.CheckBound
	if bound <= 0 {
		bound = DefaultCheckBound
	}
	checkCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), bound)
	defer cancel()
	if err := s.fault("after_reservation"); err != nil {
		response, err := s.settleObservationFailed(checkCtx, res, 503, "SERVICE_RECOVERING", "injected_after_reservation")
		return response, err
	}
	resolved, resolveErr := s.resolveObservation(checkCtx, res)
	if resolveErr != nil {
		return s.settleStageError(checkCtx, res, resolveErr)
	}
	stamp, err := s.persistObservationTarget(checkCtx, res, resolved)
	if err != nil {
		if errors.Is(err, errFaultInjected) {
			response, err := s.settleObservationFailed(context.WithoutCancel(checkCtx), res, 503, "SERVICE_RECOVERING", "injected_before_observation_target_persist")
			return response, err
		}
		return s.settleStageError(checkCtx, res, err)
	}
	res.persistedAt = stamp
	diffed, diffErr := s.diffObservation(checkCtx, res, resolved)
	if diffErr != nil {
		return s.settleStageError(checkCtx, res, diffErr)
	}
	var commitErr error
	response, commitErr = s.commitObservation(checkCtx, token, key, res, resolved, diffed)
	if commitErr != nil && ctx.Err() != nil {
		return domain.Response{}, ctx.Err()
	}
	return response, commitErr
}

// settleStageError settles a failed post-reservation stage terminally (S1,
// W3b L3): a typed stage failure keeps its class, a bound expiry is
// recovering, and any other error — a storage failure included — becomes
// 503 SERVICE_RECOVERING stage_failure. A failed settlement surfaces both
// errors and leaves the intent to the restart sweep.
func (s *Service) settleStageError(ctx context.Context, res observationReservation, cause error) (domain.Response, error) {
	settle := context.WithoutCancel(ctx)
	var typed *observationError
	if errors.As(cause, &typed) {
		return s.settleObservationFailed(settle, res, typed.status, typed.code, typed.reason)
	}
	if ctx.Err() != nil {
		return s.settleObservationFailed(settle, res, 503, "SERVICE_RECOVERING", "check_bound_exceeded")
	}
	settled, settleErr := s.settleObservationFailedQuiet(settle, res, cause)
	if settleErr != nil {
		return domain.Response{}, errors.Join(cause, settleErr)
	}
	return settled, nil
}

// reserveObservation is stage 1: authenticate (owner-only, O1), normalize the
// body from the request alone, replay an existing receipt before any
// mapping-dependent planning (W3b L2), then plan, reserve the receipt and
// persist the observation intent. A planning failure leaves no receipt.
func (s *Service) reserveObservation(ctx context.Context, token, versionID, key string, rawBody []byte) (res observationReservation, response domain.Response, done bool, err error) {
	target := "/api/v1/versions/" + versionID + "/source-observations"
	err = s.Store.Write(ctx, func(tx app.WriteTx) error {
		actor, e := s.Auth.AuthenticateWrite(ctx, tx, token)
		if e != nil {
			return e
		}
		if actor.Scope.Kind != "owner" {
			return domain.ErrDenied
		}
		el, e := Check(tx, actor, versionID)
		if e != nil {
			return e
		}
		requested, scope, normalized, e := normalizeObservationRequest(rawBody)
		if e != nil {
			return e
		}
		request := domain.Request{Key: domain.OperationKey{PrincipalID: actor.PrincipalID, OperationID: key}, Method: "POST", Target: target, Body: normalized}
		if e := s.Auth.Authority(token).AuthorizeWrite(ctx, tx, request); e != nil {
			return e
		}
		if _, e := tx.Receipt(request.Key); e == nil {
			// An existing receipt replays (or conflicts) under current
			// authority before the current mappings are consulted.
			receipt, _, e := tx.ReserveOperation(request, nil)
			if e != nil {
				return e
			}
			response, done, e = replayObservation(receipt, key)
			return e
		} else if !errors.Is(e, domain.ErrNotFound) {
			return e
		}
		plan, failure, e := s.planObservation(tx, el, requested, scope)
		if e != nil {
			return e
		}
		if failure != nil {
			response, done = *failure, true
			return nil
		}
		receipt, existing, e := tx.ReserveOperation(request, nil)
		if e != nil {
			return e
		}
		if existing {
			response, done, e = replayObservation(receipt, key)
			return e
		}
		state, e := tx.Scope(el.ProjectID)
		if e != nil {
			return e
		}
		now := s.now()
		res = observationReservation{
			intentID:       mustID(),
			operation:      key,
			runID:          mustID(),
			targetSetID:    mustID(),
			principalID:    actor.PrincipalID,
			versionID:      versionID,
			plan:           plan,
			configRevision: state.ConfigRevision,
			permissions:    state.Project.PermissionsRevision,
		}
		intent := domain.CheckIntent{ID: res.intentID, OperationID: key, PrincipalID: actor.PrincipalID, Kind: "observation", CanonicalTarget: encoded(map[string]string{"route": target, "version_id": versionID}), ValidatedBody: normalized, ConfigRevision: res.configRevision, PermissionsRevision: res.permissions, State: "open", CreatedAt: now}
		return tx.PutCheckIntent(intent)
	})
	return res, response, done, err
}

// replayObservation answers an existing receipt: a terminal one replays its
// original response, an accepted one the stable 202 pending envelope.
func replayObservation(receipt domain.Receipt, key string) (domain.Response, bool, error) {
	switch receipt.State {
	case "completed", "failed":
		return *receipt.Response, true, nil
	case "reconciling":
		return domain.Response{}, false, domain.ErrReconciliation
	}
	return PendingResponse(key), true, nil
}

// normalizeObservationRequest validates the closed body from the request
// alone: an empty-string or duplicate source id is invalid, an empty
// source_ids array is the same request as its omission, and a valid array is
// sorted for the receipt fingerprint (W3b L2).
func normalizeObservationRequest(raw []byte) ([]string, observationScope, []byte, error) {
	scope := observationScope{Kind: scopeVersion, Declared: true}
	if len(raw) == 0 {
		return nil, scope, nil, sec.ErrInvalid
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil || fields == nil {
		// A body is one JSON object; a top-level null is no request.
		return nil, scope, nil, sec.ErrInvalid
	}
	for name, value := range fields {
		if name != "source_ids" && name != "scope" {
			return nil, scope, nil, sec.ErrInvalid
		}
		if strings.TrimSpace(string(value)) == "null" {
			return nil, scope, nil, sec.ErrInvalid
		}
	}
	requested := []string{}
	if rawIDs := fields["source_ids"]; rawIDs != nil {
		if json.Unmarshal(rawIDs, &requested) != nil {
			return nil, scope, nil, sec.ErrInvalid
		}
		seen := map[string]bool{}
		for _, id := range requested {
			if id == "" || seen[id] {
				return nil, scope, nil, sec.ErrInvalid
			}
			seen[id] = true
		}
	}
	if rawScope := fields["scope"]; rawScope != nil {
		// The nested scope is a closed object too (W3c 1).
		var scopeFields map[string]json.RawMessage
		if json.Unmarshal(rawScope, &scopeFields) != nil || scopeFields == nil {
			return nil, scope, nil, sec.ErrInvalid
		}
		for name, value := range scopeFields {
			if name != "kind" && name != "page_path" && name != "region_id" {
				return nil, scope, nil, sec.ErrInvalid
			}
			if strings.TrimSpace(string(value)) == "null" {
				return nil, scope, nil, sec.ErrInvalid
			}
		}
		var body struct {
			Kind     string `json:"kind"`
			PagePath string `json:"page_path"`
			RegionID string `json:"region_id"`
		}
		if json.Unmarshal(rawScope, &body) != nil {
			return nil, scope, nil, sec.ErrInvalid
		}
		switch body.Kind {
		case scopeVersion:
			if body.PagePath != "" || body.RegionID != "" {
				return nil, scope, nil, sec.ErrInvalid
			}
		case scopePage:
			if body.PagePath == "" || body.RegionID != "" {
				return nil, scope, nil, sec.ErrInvalid
			}
			scope.PagePath = body.PagePath
		case scopeRegion:
			if body.PagePath == "" || body.RegionID == "" {
				return nil, scope, nil, sec.ErrInvalid
			}
			scope.PagePath, scope.RegionID = body.PagePath, body.RegionID
		default:
			return nil, scope, nil, sec.ErrInvalid
		}
		scope.Kind = body.Kind
	}
	sorted := append([]string{}, requested...)
	sort.Strings(sorted)
	normalized := map[string]any{"scope": map[string]any{"kind": scope.Kind}}
	if scope.Kind != scopeVersion {
		normalized["scope"].(map[string]any)["page_path"] = scope.PagePath
	}
	if scope.Kind == scopeRegion {
		normalized["scope"].(map[string]any)["region_id"] = scope.RegionID
	}
	if len(sorted) > 0 {
		normalized["source_ids"] = sorted
	}
	return requested, scope, encoded(normalized), nil
}

// planObservation derives the effective requested source set (plan §7.1 step
// 2, shared with the read selection) and projects the scope onto the
// immutable version. A typed failure response (mapping ambiguity, unknown
// page) is returned without a receipt; storage errors propagate.
func (s *Service) planObservation(tx app.WriteTx, el Eligibility, requested []string, scope observationScope) (observationPlan, *domain.Response, error) {
	sources, err := observableSources(tx, el)
	if errors.Is(err, ErrMappingAmbiguous) {
		// W3b L4(c): a cited repo_id mapped by several configured sources
		// fails closed before any receipt.
		failed := errorEnvelope(409, "SOURCE_UNAVAILABLE", "mapping_ambiguous")
		return observationPlan{}, &failed, nil
	}
	if err != nil {
		return observationPlan{}, nil, err
	}
	plan := observationPlan{projectID: el.ProjectID, versionID: el.Version.ID, sources: sources, scope: scope}
	if len(requested) > 0 {
		subset := make([]sourcePlan, 0, len(requested))
		for _, id := range requested {
			candidate, ok := plan.sourcePlanByID(id)
			if !ok {
				return observationPlan{}, nil, sec.ErrInvalid
			}
			subset = append(subset, candidate)
		}
		plan.sources = subset
	}
	if len(plan.sources) == 0 {
		return observationPlan{}, nil, sec.ErrInvalid
	}
	// A page or region scope must name an HTML page of the immutable version
	// (W3b L15); an undeclared region records region_not_declared and
	// fabricates no unit (plan §7.6).
	if scope.Kind != scopeVersion {
		pages, err := tx.VersionPages(el.Version.ID)
		if err != nil {
			return observationPlan{}, nil, err
		}
		found := false
		for _, page := range pages {
			if page.Path == scope.PagePath {
				found = true
			}
		}
		if !found {
			failed := invalidPageEnvelope(scope.PagePath)
			return observationPlan{}, &failed, nil
		}
	}
	if scope.Kind == scopeRegion {
		if _, err := tx.VersionRegion(el.Version.ID, scope.PagePath, scope.RegionID); err != nil {
			if !errors.Is(err, domain.ErrNotFound) {
				return observationPlan{}, nil, err
			}
			scope.Declared, scope.Reason = false, "region_not_declared"
		}
	}
	plan.scope = scope
	ids := make([]string, 0, len(plan.sources))
	for _, item := range plan.sources {
		ids = append(ids, item.sourceID)
	}
	sort.Strings(ids)
	plan.selection = encoded(map[string]any{"project_id": plan.projectID, "mode": "current", "source_ids": ids})
	return plan, nil, nil
}

// invalidPageEnvelope rejects a scope whose page_path names no HTML page of
// the version, before any receipt.
func invalidPageEnvelope(page string) domain.Response {
	return domain.Response{Status: 400, Body: encoded(map[string]any{"error": map[string]any{"code": "INVALID_REQUEST", "message": "source observation scope names no page of this version", "retryable": false, "details": map[string]any{"page_path": page}}, "request_id": requestStamp()})}
}

// EffectiveSourceIDs is the effective requested source set of a version's
// current-family selection, shared by the observation POST and the
// assessments read (W3b L4(a)): the configured git sources cited by the
// version's evidence, sorted. A cited repo_id mapped by more than one source
// returns ErrMappingAmbiguous.
func EffectiveSourceIDs(tx app.WriteTx, el Eligibility) ([]string, error) {
	sources, err := observableSources(tx, el)
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(sources))
	for _, item := range sources {
		ids = append(ids, item.sourceID)
	}
	sort.Strings(ids)
	return ids, nil
}

// observableSources links the version's cited git evidence to the project's
// configured git mappings by exact repo_id through LinkGit (plan §7.5): an
// unregistered repo is outside the effective set, an ambiguous one fails
// closed. Each evidence item keeps its own declared base (W3b L5).
func observableSources(tx app.WriteTx, el Eligibility) ([]sourcePlan, error) {
	mappings, err := Mappings(tx, el.ProjectID)
	if err != nil {
		return nil, err
	}
	rows, err := tx.VersionSources(el.Version.ID)
	if err != nil {
		return nil, err
	}
	evidenceRows, err := tx.VersionEvidence(el.Version.ID)
	if err != nil {
		return nil, err
	}
	type localSource struct {
		kind, repoID, revision string
	}
	locals := map[string]localSource{}
	for _, row := range rows {
		var identity struct {
			Kind          string `json:"kind"`
			RepoID        string `json:"repo_id"`
			ExactRevision string `json:"exact_revision"`
		}
		if json.Unmarshal(row.Metadata, &identity) != nil {
			return nil, sec.ErrInvalid
		}
		locals[row.ID] = localSource{kind: identity.Kind, repoID: identity.RepoID, revision: identity.ExactRevision}
	}
	bySource := map[string]*sourcePlan{}
	for _, row := range evidenceRows {
		local, ok := locals[row.SourceID]
		if !ok || local.kind != "git" {
			continue
		}
		var meta struct {
			Path string `json:"path"`
		}
		if json.Unmarshal(row.Metadata, &meta) != nil || meta.Path == "" {
			return nil, sec.ErrInvalid
		}
		mapping, err := LinkGit(mappings, local.repoID)
		if errors.Is(err, ErrMappingAmbiguous) {
			return nil, err
		}
		if err != nil {
			// Not registered: outside the effective requested set.
			continue
		}
		plan := bySource[mapping.SourceID]
		if plan == nil {
			plan = &sourcePlan{sourceID: mapping.SourceID, rootPath: mapping.RootPath, repoID: mapping.RepoID}
			bySource[mapping.SourceID] = plan
		}
		plan.evidence = append(plan.evidence, evidencePlan{evidenceID: row.ID, path: meta.Path, base: local.revision})
		plan.bases = append(plan.bases, local.revision)
	}
	out := make([]sourcePlan, 0, len(bySource))
	for _, plan := range bySource {
		plan.bases = uniqueSorted(plan.bases)
		out = append(out, *plan)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].sourceID < out[j].sourceID })
	return out, nil
}

func uniqueSorted(values []string) []string {
	sort.Strings(values)
	out := values[:0]
	previous := ""
	for i, value := range values {
		if i == 0 || value != previous {
			out = append(out, value)
		}
		previous = value
	}
	return out
}

// resolvedSource is one source's single HEAD resolution result.
type resolvedSource struct {
	plan     sourcePlan
	revision string
	reason   string
}

func (r resolvedSource) available() bool { return r.reason == "" }

// resolveObservation resolves each mapped source's current target exactly
// once, outside the DB lock (plan §7.3 stage 2), and proves every declared
// base exists in the checkout. Only absent/invalid repositories and absent
// bases classify as missing_source (W3b L10, registry ~L143); every other
// error — a git failure or an I/O error on the checkout — settles typed and
// never becomes a recorded source fact (S12).
func (s *Service) resolveObservation(ctx context.Context, res observationReservation) ([]resolvedSource, error) {
	out := make([]resolvedSource, 0, len(res.plan.sources))
	for _, plan := range res.plan.sources {
		revision, err := s.Reader.ResolveHead(ctx, plan.rootPath)
		if err != nil {
			if ctx.Err() != nil {
				return out, ctx.Err()
			}
			if errors.Is(err, ErrInvalidRoot) || errors.Is(err, ErrObjectMissing) {
				out = append(out, resolvedSource{plan: plan, reason: "missing_source"})
				continue
			}
			return out, readFailure(err)
		}
		reason := ""
		for _, base := range plan.bases {
			err := s.Reader.ResolveRevision(ctx, plan.rootPath, base)
			if err == nil {
				continue
			}
			if ctx.Err() != nil {
				return out, ctx.Err()
			}
			if !errors.Is(err, ErrInvalidRoot) && !errors.Is(err, ErrObjectMissing) {
				return out, readFailure(err)
			}
			reason = "missing_source"
			break
		}
		if reason != "" {
			out = append(out, resolvedSource{plan: plan, reason: reason})
			continue
		}
		out = append(out, resolvedSource{plan: plan, revision: revision})
	}
	return out, nil
}

// readFailure settles a genuine read failure: the git-binary reason only for
// a git failure, every other I/O error kind-neutral (G14, W3d).
func readFailure(err error) *observationError {
	reason := "source_read_failed"
	if errors.Is(err, ErrGitUnavailable) {
		reason = "git_unavailable"
	}
	return &observationError{status: 503, code: "FILE_ACCESS_ERROR", reason: reason, cause: err}
}

// observationError carries a typed settlement for stage failures.
type observationError struct {
	status int
	code   string
	reason string
	cause  error
}

func (e *observationError) Error() string { return e.reason + ": " + e.cause.Error() }
func (e *observationError) Unwrap() error { return e.cause }

// persistObservationTarget is the observation acceptance point (T2): the
// resolved targets and the run row persist in one short writer transaction
// immediately after resolution; nothing re-resolves afterwards. It returns
// the acceptance stamp shared by the run and its capture.
func (s *Service) persistObservationTarget(ctx context.Context, res observationReservation, resolved []resolvedSource) (string, error) {
	entries := []any{}
	anyResolved := false
	for _, item := range resolved {
		if item.available() {
			anyResolved = true
			entries = append(entries, map[string]any{"source_id": item.plan.sourceID, "status": "captured", "revision": item.revision})
			continue
		}
		entries = append(entries, map[string]any{"source_id": item.plan.sourceID, "status": "unavailable", "reason": item.reason})
	}
	stamp := s.now()
	err := s.Store.Write(ctx, func(tx app.WriteTx) error {
		if err := s.fault("before_observation_target_persist"); err != nil {
			return err
		}
		captured := ""
		if anyResolved {
			if err := tx.PutTargetSet(domain.TargetSet{ID: res.targetSetID, ProjectID: res.plan.projectID, RequestedSelection: res.plan.selection, TargetsJSON: encoded(map[string]any{"sources": entries}), CapturedAt: stamp}); err != nil {
				return err
			}
			captured = res.targetSetID
		}
		return tx.PutObservationRun(domain.ObservationRun{ID: res.runID, VersionID: res.versionID, PrincipalID: res.principalID, OperationID: res.operation, RequestedSelection: res.plan.selection, ScopeJSON: encoded(res.plan.scope.view()), TargetSetID: captured, CreatedAt: stamp})
	})
	return stamp, err
}

// diffedSource is one source's read/diff result: the evidence items whose
// own cited path differs between their own base and HEAD, the source's
// dependency scope (every changed path over its distinct bases) and whether
// the repository advanced.
type diffedSource struct {
	resolved       resolvedSource
	changed        map[string]bool
	changedPaths   []string
	dependency     []string
	advanced       bool
	classification string
}

// diffObservation compares each resolved source's HEAD against every cited
// evidence item's own declared base, outside the DB lock (W3b L5): a changed
// cited path is direct for exactly the items that cite it at that base; an
// advanced repo with unchanged cited paths is indirect; the same revision is
// unchanged (plan §7.6).
func (s *Service) diffObservation(ctx context.Context, res observationReservation, resolved []resolvedSource) ([]diffedSource, error) {
	out := make([]diffedSource, 0, len(resolved))
	for _, item := range resolved {
		if !item.available() {
			out = append(out, diffedSource{resolved: item, changed: map[string]bool{}, classification: "unavailable"})
			continue
		}
		result := diffedSource{resolved: item, changed: map[string]bool{}}
		changedAt := map[string]map[string]bool{}
		full := []string{}
		for _, base := range item.plan.bases {
			if base != item.revision {
				result.advanced = true
			}
			paths, err := s.Reader.ChangedPaths(ctx, item.plan.rootPath, base, item.revision, nil)
			if err != nil {
				if ctx.Err() != nil {
					return out, ctx.Err()
				}
				return out, readFailure(err)
			}
			set := map[string]bool{}
			for _, path := range paths {
				set[path] = true
			}
			changedAt[base] = set
			full = append(full, paths...)
		}
		result.dependency = uniqueSorted(full)
		paths := []string{}
		for _, evidence := range item.plan.evidence {
			if changedAt[evidence.base][evidence.path] {
				result.changed[evidence.evidenceID] = true
				paths = append(paths, evidence.path)
			}
		}
		result.changedPaths = uniqueSorted(paths)
		switch {
		case len(result.changed) > 0:
			result.classification = "direct"
		case result.advanced || len(result.dependency) > 0:
			result.classification = "indirect"
		default:
			result.classification = "unchanged"
		}
		out = append(out, result)
	}
	return out, nil
}

// availability reasons ranked strongest first (plan §7.6).
var availabilityReasonRank = map[string]int{
	"source_not_registered":  5,
	"missing_source":         4,
	"missing_retained_bytes": 3,
	"retained_bytes_corrupt": 2,
	"source_denied":          1,
}

// strongerAvailability reports the strongest availability-level reason.
func strongerAvailability(a, b string) string {
	if a == "" {
		return b
	}
	if b == "" {
		return a
	}
	if availabilityReasonRank[a] >= availabilityReasonRank[b] {
		return a
	}
	return b
}

// commitObservation is stage 4: recheck current authority, then persist the
// per-source rows, triggers, coverage set and units, emit the outbox events,
// complete the receipt and resolve the intent atomically.
func (s *Service) commitObservation(ctx context.Context, token, key string, res observationReservation, resolved []resolvedSource, diffed []diffedSource) (response domain.Response, err error) {
	observedAt := s.now()
	policySettled := false
	err = s.Store.Write(ctx, func(tx app.WriteTx) error {
		if err := s.fault("before_observation_commit"); err != nil {
			return err
		}
		actor, e := s.Auth.AuthenticateWrite(ctx, tx, token)
		if e != nil {
			return e
		}
		if actor.PrincipalID != res.principalID || actor.Scope.Kind != "owner" {
			return domain.ErrDenied
		}
		el, e := Check(tx, actor, res.versionID)
		if e != nil {
			return e
		}
		if el.ProjectID != res.plan.projectID {
			return domain.ErrNotFound
		}
		state, e := tx.Scope(el.ProjectID)
		if e != nil {
			return e
		}
		policyReason := ""
		if state.Project.PermissionsRevision != res.permissions {
			policyReason = "permissions_changed"
		} else if state.ConfigRevision != res.configRevision {
			policyReason = "config_changed"
		}
		if policyReason != "" {
			failed := errorEnvelope(412, "PRECONDITION_FAILED", policyReason)
			response = failed
			if e := s.failObservationWithin(tx, res, 412, "PRECONDITION_FAILED", policyReason, observedAt, failed); e != nil {
				return e
			}
			policySettled = true
			return nil
		}
		statuses, e := tx.LatestObservationBySource(el.ProjectID)
		if e != nil {
			return e
		}
		previous := map[string]domain.SourceStatus{}
		for _, status := range statuses {
			previous[status.SourceID] = status
		}
		captured := res.plan.hasCapture(resolved)
		// Per-source observation rows record the observed repo_id, the link
		// the coverage overlay attributes units through (W3b L7).
		rows := make([]domain.SourceObservation, 0, len(diffed))
		for _, item := range diffed {
			row := domain.SourceObservation{ID: mustID(), RunID: res.runID, VersionID: res.versionID, SourceID: item.resolved.plan.sourceID, RepoID: item.resolved.plan.repoID, BaseRevision: strings.Join(item.resolved.plan.bases, ","), ObservedRevision: item.resolved.revision, Availability: "available", Classification: item.classification, ConfigRevision: res.configRevision, PermissionsRevision: res.permissions, ObservedAt: observedAt, OperationID: key}
			if captured {
				row.TargetSetID = res.targetSetID
			}
			switch {
			case !item.resolved.available():
				row.Availability, row.Classification, row.Reason = "unavailable", "unavailable", item.resolved.reason
			case item.classification == "direct":
				row.ChangedPathsJSON = encoded(item.changedPaths)
			case item.classification == "indirect":
				row.DependencyScopeJSON = encoded(item.dependency)
			}
			if e := tx.PutSourceObservation(row); e != nil {
				return e
			}
			rows = append(rows, row)
		}
		// Triggers and units share the run's captured target (plan §7.2/§7.6).
		triggers, coverageSetID, e := s.persistCoverage(tx, res, captured, diffed, rows, observedAt)
		if e != nil {
			return e
		}
		// Transactional-outbox events; FilterEvent masks them for scoped
		// principals (there is no per-source read route).
		for _, row := range rows {
			if row.Classification == "direct" || row.Classification == "indirect" {
				if e := tx.AppendEvent("source", row.SourceID, "source.changed", res.permissions, encoded(map[string]any{"version_id": res.versionID, "run_id": res.runID, "classification": row.Classification})); e != nil {
					return e
				}
			}
			if prev, ok := previous[row.SourceID]; ok && prev.LastAvailability != row.Availability {
				if e := tx.AppendEvent("source", row.SourceID, "source.availability_changed", res.permissions, encoded(map[string]any{"version_id": res.versionID, "run_id": res.runID, "availability": row.Availability})); e != nil {
					return e
				}
			}
		}
		if e := tx.ResolveCheckIntent(res.intentID, "completed", "", observedAt); e != nil {
			return e
		}
		if e := tx.ReleaseCheckIntentPins(res.intentID); e != nil {
			return e
		}
		run := domain.ObservationRun{ID: res.runID, VersionID: res.versionID, ScopeJSON: encoded(res.plan.scope.view()), CreatedAt: res.persistedAt}
		if captured {
			run.TargetSetID = res.targetSetID
		}
		response = domain.Response{Status: 201, Body: encoded(observationView(key, run, coverageSetID, rows, triggeredSources(triggers)))}
		return tx.CompleteOperation(domain.OperationKey{PrincipalID: res.principalID, OperationID: key}, response)
	})
	if err == nil && policySettled {
		return response, nil
	}
	if err != nil {
		if errors.Is(err, errFaultInjected) {
			response, err := s.settleObservationFailed(context.WithoutCancel(ctx), res, 503, "SERVICE_RECOVERING", "injected_before_observation_commit")
			return response, err
		}
		if ctx.Err() != nil {
			response, err := s.settleObservationFailed(context.WithoutCancel(ctx), res, 503, "SERVICE_RECOVERING", "check_bound_exceeded")
			return response, err
		}
		if status, code := settleClass(err); code != "" {
			response, err := s.settleObservationFailed(context.WithoutCancel(ctx), res, status, code, settleReason(code))
			return response, err
		}
		return s.settleStageError(ctx, res, err)
	}
	return response, nil
}

// observationView is the observation 201 shared by the commit and the
// restart sweep (W3b L12): the run's capture stamp is both captured_at (when
// it captured a target) and observed_at, and the scope is the observation
// scope view.
func observationView(key string, run domain.ObservationRun, coverageSetID string, rows []domain.SourceObservation, triggered map[string]bool) map[string]any {
	view := map[string]any{"operation_id": key, "run_id": run.ID, "target_set_id": nil, "captured_at": nil, "observed_at": run.CreatedAt, "scope": decodeObservationScope(run.ScopeJSON).view(), "coverage_set_id": nil}
	if run.TargetSetID != "" {
		view["target_set_id"] = run.TargetSetID
		view["captured_at"] = run.CreatedAt
	}
	if coverageSetID != "" {
		view["coverage_set_id"] = coverageSetID
	}
	view["sources"] = observationSourcesView(rows, triggered)
	return view
}

func triggeredSources(triggers []domain.ReassessmentTrigger) map[string]bool {
	out := map[string]bool{}
	for _, trigger := range triggers {
		out[trigger.SourceID] = true
	}
	return out
}

// hasCapture reports whether at least one requested source resolved (r4.2
// rule 2): only then does the run carry a target set.
func (p observationPlan) hasCapture(resolved []resolvedSource) bool {
	for _, item := range resolved {
		if item.available() {
			return true
		}
	}
	return false
}

// persistCoverage persists the run's direct-change triggers for every
// captured direct change whatever the requested scope (W3b L15), then — only
// for a scope that projects onto the version — the coverage set and its
// required units, all bound to the run's captured target.
func (s *Service) persistCoverage(tx app.WriteTx, res observationReservation, captured bool, diffed []diffedSource, rows []domain.SourceObservation, observedAt string) ([]domain.ReassessmentTrigger, string, error) {
	anchors, err := tx.VersionRegionEvidence(res.versionID)
	if err != nil {
		return nil, "", err
	}
	anchorsOf := map[string][]domain.RegionEvidence{}
	for _, anchor := range anchors {
		anchorsOf[anchor.EvidenceID] = append(anchorsOf[anchor.EvidenceID], anchor)
	}
	targetSet := ""
	if captured {
		targetSet = res.targetSetID
	}
	rowBySource := map[string]domain.SourceObservation{}
	for _, row := range rows {
		rowBySource[row.SourceID] = row
	}
	// Triggers: one per (source, region anchor) of the changed evidence items
	// and one version-anchored trigger per source for changed evidence
	// without region anchors (plan §7.2).
	type anchorKey struct{ page, region string }
	triggers := []domain.ReassessmentTrigger{}
	for _, item := range diffed {
		if item.classification != "direct" {
			continue
		}
		sourceID := item.resolved.plan.sourceID
		byAnchor := map[anchorKey][]string{}
		versionPaths := []string{}
		for _, evidence := range item.resolved.plan.evidence {
			if !item.changed[evidence.evidenceID] {
				continue
			}
			itemAnchors := anchorsOf[evidence.evidenceID]
			if len(itemAnchors) == 0 {
				versionPaths = append(versionPaths, evidence.path)
				continue
			}
			for _, anchor := range itemAnchors {
				key := anchorKey{page: anchor.PagePath, region: anchor.RegionID}
				byAnchor[key] = append(byAnchor[key], evidence.path)
			}
		}
		keys := make([]anchorKey, 0, len(byAnchor))
		for key := range byAnchor {
			keys = append(keys, key)
		}
		sort.Slice(keys, func(i, j int) bool {
			if keys[i].page != keys[j].page {
				return keys[i].page < keys[j].page
			}
			return keys[i].region < keys[j].region
		})
		for _, key := range keys {
			triggers = append(triggers, domain.ReassessmentTrigger{ID: mustID(), RunID: res.runID, VersionID: res.versionID, TargetSetID: targetSet, SourceID: sourceID, AnchorKind: scopeRegion, PagePath: key.page, RegionID: key.region, Reason: "direct_source_change", ChangedPathsJSON: encoded(uniqueSorted(byAnchor[key])), ObservationID: rowBySource[sourceID].ID, ObservedAt: observedAt, CreatedAt: observedAt})
		}
		if len(versionPaths) > 0 {
			triggers = append(triggers, domain.ReassessmentTrigger{ID: mustID(), RunID: res.runID, VersionID: res.versionID, TargetSetID: targetSet, SourceID: sourceID, AnchorKind: scopeVersion, Reason: "direct_source_change", ChangedPathsJSON: encoded(uniqueSorted(versionPaths)), ObservationID: rowBySource[sourceID].ID, ObservedAt: observedAt, CreatedAt: observedAt})
		}
	}
	for _, trigger := range triggers {
		if err := tx.PutReassessmentTrigger(trigger); err != nil {
			return nil, "", err
		}
	}
	scope := res.plan.scope
	if !scope.Declared {
		// An undeclared region records the fact and fabricates no unit.
		return triggers, "", nil
	}
	// Unit composition (plan §7.6); every unit's reason comes from its own
	// cited evidence (W3b L6).
	unitKind := scope.Kind
	units := []domain.RequiredUnit{}
	add := func(kind, page, region string, evidence []unitEvidence) {
		units = append(units, domain.RequiredUnit{Kind: kind, PagePath: page, RegionID: region, Reason: unitReason(evidence), TargetSetID: targetSet})
	}
	cited := citedEvidence(diffed)
	switch scope.Kind {
	case scopeVersion:
		pages, err := tx.VersionPages(res.versionID)
		if err != nil {
			return nil, "", err
		}
		if len(pages) == 0 {
			add(scopeVersion, "", "", cited)
			break
		}
		unitKind = scopePage
		for _, page := range pages {
			add(scopePage, page.Path, "", anchoredEvidence(cited, anchorsOf, page.Path, ""))
		}
	case scopePage:
		add(scopePage, scope.PagePath, "", anchoredEvidence(cited, anchorsOf, scope.PagePath, ""))
	case scopeRegion:
		add(scopeRegion, scope.PagePath, scope.RegionID, anchoredEvidence(cited, anchorsOf, scope.PagePath, scope.RegionID))
	}
	coverageSetID := mustID()
	set := domain.CoverageSet{ID: coverageSetID, VersionID: res.versionID, TargetSetID: targetSet, RunID: res.runID, ScopeJSON: encoded(scope.view()), UnitKind: unitKind, CapturedAt: observedAt, Origin: "source_observation"}
	if err := tx.PutCoverageSet(set); err != nil {
		return nil, "", err
	}
	for i := range units {
		units[i].CoverageSetID = coverageSetID
		if err := tx.PutRequiredUnit(units[i]); err != nil {
			return nil, "", err
		}
	}
	return triggers, coverageSetID, nil
}

// unitEvidence is one cited evidence item of an observed source with its
// run outcome, the input of a unit's §7.6 reason.
type unitEvidence struct {
	evidenceID  string
	unavailable string
	changed     bool
	advanced    bool
}

// citedEvidence lists every cited evidence item of the run's sources; an
// item advanced when HEAD differs from its own declared base (W3b L5).
func citedEvidence(diffed []diffedSource) []unitEvidence {
	out := []unitEvidence{}
	for _, item := range diffed {
		for _, evidence := range item.resolved.plan.evidence {
			out = append(out, unitEvidence{evidenceID: evidence.evidenceID, unavailable: item.resolved.reason, changed: item.changed[evidence.evidenceID], advanced: item.resolved.available() && evidence.base != item.resolved.revision})
		}
	}
	return out
}

// anchoredEvidence keeps the evidence anchored on a page, or on one exact
// region when regionID is set.
func anchoredEvidence(cited []unitEvidence, anchorsOf map[string][]domain.RegionEvidence, page, regionID string) []unitEvidence {
	out := []unitEvidence{}
	for _, evidence := range cited {
		for _, anchor := range anchorsOf[evidence.evidenceID] {
			if anchor.PagePath == page && (regionID == "" || anchor.RegionID == regionID) {
				out = append(out, evidence)
				break
			}
		}
	}
	return out
}

// unitReason derives one unit's §7.6 reason from its own cited evidence,
// strongest first: an availability-level reason of a cited source that is
// unavailable in the run, a direct change of one of its items, an advanced
// cited source whose cited paths are unchanged, no cited evidence, else null.
func unitReason(evidence []unitEvidence) string {
	if len(evidence) == 0 {
		return "no_cited_evidence"
	}
	reason := ""
	for _, item := range evidence {
		if item.unavailable != "" {
			reason = strongerAvailability(reason, item.unavailable)
		}
	}
	if reason != "" {
		return reason
	}
	for _, item := range evidence {
		if item.changed {
			return "direct_source_change"
		}
	}
	for _, item := range evidence {
		if item.advanced {
			return "unexamined_dependency_scope"
		}
	}
	return ""
}

// observationSourcesView projects the per-source response rows ordered by
// source id, so the commit and the restart sweep return the same array
// whatever the requested order (W3c 3).
func observationSourcesView(rows []domain.SourceObservation, triggered map[string]bool) []map[string]any {
	ordered := append([]domain.SourceObservation{}, rows...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].SourceID < ordered[j].SourceID })
	out := make([]map[string]any, 0, len(ordered))
	for _, row := range ordered {
		item := map[string]any{"source_id": row.SourceID, "observation_id": row.ID, "availability": row.Availability, "classification": row.Classification, "reassessment_required": triggered[row.SourceID]}
		if row.Reason != "" {
			item["reason"] = row.Reason
		} else {
			item["reason"] = nil
		}
		if len(row.ChangedPathsJSON) > 0 {
			item["changed_paths"] = json.RawMessage(row.ChangedPathsJSON)
		} else {
			item["changed_paths"] = []string{}
		}
		if len(row.DependencyScopeJSON) > 0 {
			item["dependency_scope"] = json.RawMessage(row.DependencyScopeJSON)
		} else {
			item["dependency_scope"] = []string{}
		}
		out = append(out, item)
	}
	return out
}

// failObservationWithin settles a failed observation inside the caller's
// transaction.
func (s *Service) failObservationWithin(tx app.WriteTx, res observationReservation, status int, code, reason, now string, failed domain.Response) error {
	if err := tx.ReleaseCheckIntentPins(res.intentID); err != nil {
		return err
	}
	if err := tx.ResolveCheckIntent(res.intentID, "failed", reason, now); err != nil {
		return err
	}
	return tx.CompleteOperation(domain.OperationKey{PrincipalID: res.principalID, OperationID: res.operation}, failed)
}

func (s *Service) settleObservationFailedQuiet(ctx context.Context, res observationReservation, cause error) (domain.Response, error) {
	return s.settleObservationFailed(ctx, res, 503, "SERVICE_RECOVERING", "stage_failure")
}

// settleObservationFailed settles a failed observation in its own writer
// transaction (S1/S2): the receipt completes as failed, the intent resolves
// and any intent-owned pin is released. A run row already persisted by stage
// 2 stays behind uncommitted and is never a selection candidate.
func (s *Service) settleObservationFailed(ctx context.Context, res observationReservation, status int, code, reason string) (domain.Response, error) {
	failed := errorEnvelope(status, code, reason)
	if err := s.Store.Write(ctx, func(tx app.WriteTx) error {
		return s.failObservationWithin(tx, res, status, code, reason, s.now(), failed)
	}); err != nil {
		return domain.Response{}, err
	}
	return failed, nil
}
