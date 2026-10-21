package main

import (
	"context"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"
	"time"
	"unicode/utf8"

	client "haeryong.local/haeryong/internal/adapters/client"
	"haeryong.local/haeryong/internal/adapters/clientstate"
	importer "haeryong.local/haeryong/internal/application/import"
	"haeryong.local/haeryong/internal/application/service"
	appsource "haeryong.local/haeryong/internal/application/source"
	"haeryong.local/haeryong/internal/application/transfer"
	"haeryong.local/haeryong/internal/domain"
)

// sourceCommand implements the TASK-004-01 CLI family: projects register,
// sources approve/status and evidence show/verify. The observation trigger
// (sources observe) arrives with Milestone 3.
func sourceCommand(ctx context.Context, opt options, api *client.Client, state *clientstate.Store, emit func(any) error) error {
	args := opt.command
	switch {
	case args[0] == "projects" && len(args) == 2 && args[1] == "register":
		name := opt.flags["name"]
		if strings.TrimSpace(name) == "" {
			return invalid("projects register --name NAME [--operation-id ID]")
		}
		return mutationCommand(ctx, opt, api, state, emit, "POST", "/api/v1/projects", map[string]any{"name": name})
	case args[0] == "sources" && len(args) == 2 && args[1] == "status":
		project := opt.flags["project"]
		if project == "" {
			return invalid("sources status --project ID")
		}
		var data json.RawMessage
		if _, e := api.JSON(ctx, "GET", "/api/v1/projects/"+project+"/sources", "", nil, &data); e != nil {
			return productError(e)
		}
		return emit(data)
	case args[0] == "sources" && len(args) == 2 && args[1] == "approve":
		return sourcesApprove(ctx, opt, api, state, emit)
	case args[0] == "sources" && len(args) == 2 && args[1] == "observe":
		return sourcesObserve(ctx, opt, api, state, emit)
	case args[0] == "assessments" && len(args) == 2 && args[1] == "list":
		return assessmentsList(ctx, opt, api, emit)
	case args[0] == "evidence" && len(args) == 2 && args[1] == "show":
		version, evidence := opt.flags["version"], opt.flags["evidence"]
		if version == "" || evidence == "" {
			return invalid("evidence show --version ID --evidence ID")
		}
		var data json.RawMessage
		if _, e := api.JSON(ctx, "GET", "/api/v1/versions/"+version+"/source-evidence?evidence_id="+url.QueryEscape(evidence), "", nil, &data); e != nil {
			return productError(e)
		}
		return emit(data)
	case args[0] == "evidence" && len(args) == 2 && args[1] == "verify":
		return evidenceVerify(ctx, opt, api, state, emit)
	}
	return invalid("use projects register | sources approve/status/observe | evidence show/verify | assessments list")
}

// sourcesObserve runs the owner-only observation POST. An omitted
// --operation-id is recovered from the pending-command journal like
// evidence verify (E1), so an interrupted rerun resumes the same run; an
// explicit key replays identically even after completion.
func sourcesObserve(ctx context.Context, opt options, api *client.Client, state *clientstate.Store, emit func(any) error) error {
	version := opt.flags["version"]
	if version == "" {
		return invalid("sources observe --version ID [--page PATH | --region ID --page PATH] [--operation-id ID]")
	}
	page, region := opt.flags["page"], opt.flags["region"]
	if region != "" && page == "" {
		return invalid("--region requires --page")
	}
	body := map[string]any{}
	scope := map[string]any{}
	if page != "" {
		scope["kind"] = "page"
		scope["page_path"] = page
	}
	if region != "" {
		scope["kind"] = "region"
		scope["region_id"] = region
	}
	if len(scope) > 0 {
		body["scope"] = scope
	}
	route := "/api/v1/versions/" + version + "/source-observations"
	key := opt.flags["operation-id"]
	var release func() error
	if key == "" {
		var e error
		if key, release, e = automaticKey(ctx, api, state, clientstate.CommandIntent{Method: "POST", Target: route}); e != nil {
			return e
		}
	} else if !operationID.MatchString(key) {
		return invalid("canonical operation ID required")
	}
	return dispatchCommand(ctx, api, state, emit, key, "POST", route, body, release)
}

// assessmentsList reads the assessments summary, or every captured unit of
// one coverage set with --coverage-set, following the cursor to the end
// (cli.md: the cursor required-unit view). A failed page fails the command;
// no partial set is emitted.
func assessmentsList(ctx context.Context, opt options, api *client.Client, emit func(any) error) error {
	version := opt.flags["version"]
	if version == "" {
		return invalid("assessments list --version ID [--coverage-set ID]")
	}
	var data json.RawMessage
	if set := opt.flags["coverage-set"]; set != "" {
		units := []json.RawMessage{}
		cursor := ""
		for {
			values := url.Values{"coverage_set_id": {set}}
			if cursor != "" {
				values.Set("cursor", cursor)
			}
			var page struct {
				Items []json.RawMessage `json:"items"`
				Next  *string           `json:"next_cursor"`
			}
			if _, e := api.JSON(ctx, "GET", "/api/v1/versions/"+version+"/assessments/coverage?"+values.Encode(), "", nil, &page); e != nil {
				return productError(e)
			}
			units = append(units, page.Items...)
			if page.Next == nil {
				return emit(map[string]any{"items": units, "next_cursor": nil})
			}
			cursor = *page.Next
		}
	}
	if _, e := api.JSON(ctx, "GET", "/api/v1/versions/"+version+"/assessments", "", nil, &data); e != nil {
		return productError(e)
	}
	return emit(data)
}

// evidenceVerify runs the imported-evidence check. The verification key is
// fixed before staging (a referenced expectation upload binds to this exact
// consumer operation) and an omitted --operation-id is recovered from the
// pending-command journal, so an interrupted rerun resumes the same
// operation (E1).
func evidenceVerify(ctx context.Context, opt options, api *client.Client, state *clientstate.Store, emit func(any) error) error {
	version, evidence := opt.flags["version"], opt.flags["evidence"]
	if version == "" || evidence == "" {
		return invalid("evidence verify --version ID --evidence ID [--expected-sha256 H | --expected-excerpt-file PATH] [--operation-id ID]")
	}
	if opt.flags["expected-sha256"] != "" && opt.flags["expected-excerpt-file"] != "" {
		return invalid("supply at most one expectation form")
	}
	route := "/api/v1/versions/" + version + "/source-evidence-verifications?evidence_id=" + url.QueryEscape(evidence)
	key := opt.flags["operation-id"]
	var release func() error
	if key == "" {
		// E1: the generated key is persisted as this command's pending
		// operation before any staging, so a rerun without --operation-id
		// recovers it instead of generating a second operation.
		var e error
		if key, release, e = automaticKey(ctx, api, state, clientstate.CommandIntent{Method: "POST", Target: route}); e != nil {
			return e
		}
	} else if !operationID.MatchString(key) {
		return invalid("canonical operation ID required")
	}
	body := map[string]any{}
	if digest := opt.flags["expected-sha256"]; digest != "" {
		body["expected_bytes_sha256"] = digest
	}
	if path := opt.flags["expected-excerpt-file"]; path != "" {
		raw, e := os.ReadFile(path)
		if e != nil {
			return e
		}
		// S24: both excerpt forms are UTF-8; binary targets use the digest
		// form. Reject a non-UTF-8 file with the actionable alternative.
		if !utf8.Valid(raw) {
			return invalid("--expected-excerpt-file must be valid UTF-8 text; use --expected-sha256 for binary targets")
		}
		if int64(len(raw)) <= int64(appsource.MaxInlineExcerpt) {
			body["expected_excerpt"] = string(raw)
		} else {
			// C4: large expectations travel through the approved
			// purpose=body reference bound to this exact verification.
			uploadID, e := stageExpectation(ctx, api, state, version, evidence, key, raw)
			if e != nil {
				return productError(e)
			}
			body["expected_excerpt_ref"] = map[string]string{"upload_id": uploadID}
		}
	}
	// D2/E2: keep the final consumer request on the staging journal once,
	// immutably — a rerun with changed semantic inputs for the same key is
	// rejected instead of silently overwriting the stored request.
	if journal, e := state.Load(key); e == nil && journal.Purpose == "expectation" {
		raw, e := json.Marshal(body)
		if e != nil {
			return e
		}
		if len(journal.FinalBody) == 0 {
			journal.FinalBody = raw
			if e := state.Save(journal); e != nil {
				return e
			}
		} else if !bytesEqualJSON(journal.FinalBody, raw) {
			return productError(transfer.ErrIntent)
		}
	}
	return dispatchCommand(ctx, api, state, emit, key, "POST", route, body, release)
}

// bytesEqualJSON compares two request bodies semantically (object key order
// is not part of the request identity).
func bytesEqualJSON(a, b []byte) bool {
	var av, bv any
	if json.Unmarshal(a, &av) != nil || json.Unmarshal(b, &bv) != nil {
		return string(a) == string(b)
	}
	aj, _ := json.Marshal(av)
	bj, _ := json.Marshal(bv)
	return string(aj) == string(bj)
}

// mutationCommand dispatches one K request under --operation-id, or under an
// automatic pending key recovered on rerun, and emits its result.
func mutationCommand(ctx context.Context, opt options, api *client.Client, state *clientstate.Store, emit func(any) error, method, target string, body any) error {
	key := opt.flags["operation-id"]
	var release func() error
	if key == "" {
		var e error
		if key, release, e = automaticKey(ctx, api, state, clientstate.CommandIntent{Method: method, Target: target}); e != nil {
			return e
		}
	}
	return dispatchCommand(ctx, api, state, emit, key, method, target, body, release)
}

// automaticKey recovers or records this command's pending operation key
// before any dispatch (E1); release removes that pending index.
func automaticKey(ctx context.Context, api *client.Client, state *clientstate.Store, intent clientstate.CommandIntent) (string, func() error, error) {
	identity, e := api.Identity(ctx)
	if e != nil {
		return "", nil, productError(e)
	}
	return pendingKey(ctx, state, identity, intent)
}

// pendingKey is automaticKey for a caller that already read its identity.
func pendingKey(ctx context.Context, state *clientstate.Store, identity transfer.Identity, intent clientstate.CommandIntent) (string, func() error, error) {
	generated, e := service.NewID()
	if e != nil {
		return "", nil, e
	}
	key, e := state.UseCommandPending(ctx, identity, intent, generated)
	if e != nil {
		return "", nil, productError(e)
	}
	return key, func() error { return state.FinishCommandPending(context.Background(), identity, intent, key) }, nil
}

// dispatchCommand sends one K request under key and emits its result. A
// non-nil release (an automatic key) runs only after the result or a
// definitive failure has been emitted; an uncertain outcome keeps the key so
// a rerun resumes the same operation (E1).
func dispatchCommand(ctx context.Context, api *client.Client, state *clientstate.Store, emit func(any) error, key, method, target string, body any, release func() error) error {
	response, id, e := mutation(ctx, api, state, key, method, target, body)
	if e != nil {
		return afterDefinitive(recordedError(productError(e), response, id, nil), id, release)
	}
	if e = emit(map[string]any{"operation_id": id, "result": json.RawMessage(response.Body)}); e != nil {
		return e
	}
	if release != nil {
		return release()
	}
	return nil
}

// afterDefinitive attaches release to a definitive failure, which main runs
// only after the failure has been emitted. Definitive means the outcome is
// known: a recorded failure (a terminal receipt's replay), or a 4xx rejection
// other than an authority denial or a reconciliation demand. A 5xx answer
// may leave an accepted receipt behind; like a denial, a reconciliation
// demand, a transport loss or a local error it keeps the automatic key until
// a later recovery invocation can read the original outcome
// (docs/contracts/cli.md).
func afterDefinitive(err error, operation string, release func() error) error {
	if release == nil {
		return err
	}
	var recorded *recordedCommandError
	if errors.As(err, &recorded) {
		recorded.acknowledge = release
		return err
	}
	var answered *client.Error
	if !errors.As(err, &answered) || answered.Status < 400 || answered.Status >= 500 || answered.Status == 401 || answered.Status == 403 || answered.Code == "OPERATION_RECONCILIATION_REQUIRED" || answered.Code == "PROVIDER_OUTCOME_UNKNOWN" {
		return err
	}
	return &recordedCommandError{cause: err, operation: operation, status: answered.Status, acknowledge: release}
}

// sourcesApprove registers or updates one project source through the normal
// project-scoped configuration plan/commit flow. The plan request is the
// preparation step: its automatic key is a pending command identified by the
// project and source, and a journaled plan request is reused — after the
// explicit flags are compared with it, like config plan — before any
// configuration read or new plan, so a rerun resumes the same plan and
// commit. --operation-id binds the final commit and derives the plan key
// from it, so even a rerun after completion recovers, compares and replays
// the original request; otherwise the commit uses the plan's natural
// identity, its plan id. The pending key is released only after the result
// or a definitive failure has been emitted; an uncertain outcome keeps it.
// --plan-id commits a printed preview with its original expected revision. The submitted sources array is the full merged set, so
// approving a second source never drops the first (S5).
func sourcesApprove(ctx context.Context, opt options, api *client.Client, state *clientstate.Store, emit func(any) error) error {
	project := opt.flags["project"]
	if opt.flags["plan-id"] != "" {
		return commitPreviewedApprove(ctx, opt, api, state, emit, project)
	}
	if project == "" || opt.flags["source-id"] == "" || opt.flags["kind"] == "" || opt.flags["root-path"] == "" {
		return invalid("sources approve --project ID --source-id ID --kind git|files --root-path PATH [--repo-id ID] [--retention-mode MODE] [--provider-ids a,b] [--confirmed] [--operation-id ID], or sources approve --plan-id ID --confirmed [--operation-id ID]")
	}
	if opt.flags["kind"] != "git" && opt.flags["kind"] != "files" {
		return invalid("kind must be git or files")
	}
	if opt.flags["kind"] == "git" && opt.flags["repo-id"] == "" {
		return invalid("git sources require --repo-id")
	}
	if mode := opt.flags["retention-mode"]; mode != "" && mode != "reference_only" && mode != "selected_snapshot" {
		return invalid("retention mode must be reference_only or selected_snapshot")
	}
	if key := opt.flags["operation-id"]; key != "" && !operationID.MatchString(key) {
		return invalid("canonical operation ID required")
	}
	identity, e := api.Identity(ctx)
	if e != nil {
		return productError(e)
	}
	planIntent := clientstate.CommandIntent{Method: "POST", Target: "/api/v1/configuration/plans", ProjectID: project, SourceID: opt.flags["source-id"]}
	var planKey string
	var release func() error
	if commitKey := opt.flags["operation-id"]; commitKey != "" {
		planKey = derivedKey("haeryong/sources-approve-plan/" + commitKey)
	} else if planKey, release, e = pendingKey(ctx, state, identity, planIntent); e != nil {
		return e
	}
	done := func() error {
		if release == nil {
			return nil
		}
		return release()
	}
	var planBody json.RawMessage
	saved, e := state.Load(planKey)
	switch {
	case e == nil:
		if saved.Identity.LibraryID != identity.LibraryID || saved.Identity.PrincipalID != identity.PrincipalID || saved.Method != planIntent.Method || saved.Target != planIntent.Target {
			return productError(transfer.ErrIntent)
		}
		if e := sameApproveRequest(saved.FinalBody, project, opt); e != nil {
			return productError(e)
		}
		planBody = saved.FinalBody
	case errors.Is(e, domain.ErrNotFound):
		// S5: read the current project sources and build the full array.
		var view struct {
			ConfigRevision int64 `json:"config_revision"`
			Configuration  struct {
				Projects map[string]struct {
					Sources []map[string]any `json:"sources"`
				} `json:"projects"`
			} `json:"configuration"`
		}
		if _, e := api.JSON(ctx, "GET", "/api/v1/configuration", "", nil, &view); e != nil {
			return productError(e)
		}
		merged := mergeSourceEntry(view.Configuration.Projects[project].Sources, opt, buildSourceEntry(opt))
		planBody, e = json.Marshal(map[string]any{"scope": map[string]any{"kind": "project", "project_id": project}, "expected_config_revision": view.ConfigRevision, "changes": map[string]any{"sources": merged}})
		if e != nil {
			return e
		}
	default:
		return e
	}
	var request approvePlanRequest
	if e := json.Unmarshal(planBody, &request); e != nil {
		return productError(transfer.ErrIntent)
	}
	response, _, e := mutation(ctx, api, state, planKey, "POST", "/api/v1/configuration/plans", planBody)
	if e != nil {
		return afterDefinitive(recordedError(productError(e), response, planKey, nil), planKey, release)
	}
	var plan struct {
		PlanID string `json:"plan_id"`
	}
	if json.Unmarshal(response.Body, &plan) != nil || plan.PlanID == "" {
		if e := emit(map[string]any{"operation_id": planKey, "plan": json.RawMessage(response.Body)}); e != nil {
			return e
		}
		return done()
	}
	if opt.flags["confirmed"] != "true" {
		// Record the preview before printing it so --plan-id commits it
		// later with its original expected revision.
		if e := savePreviewLink(state, previewLink{PlanID: plan.PlanID, Project: project, ExpectedRevision: request.ExpectedRevision, PlanOperation: planKey}, identity); e != nil {
			return e
		}
		if e := emit(map[string]any{"operation_id": planKey, "plan_id": plan.PlanID, "plan": json.RawMessage(response.Body), "hint": "commit with --confirmed --plan-id " + plan.PlanID + " [--operation-id ID]"}); e != nil {
			return e
		}
		// Preview-only ends at the printed plan (S20 refinement).
		return done()
	}
	return commitApprove(ctx, opt, api, state, emit, plan.PlanID, request.ExpectedRevision, release)
}

// commitPreviewedApprove commits a printed preview (--plan-id) with the
// plan's original expected revision, never the current one.
func commitPreviewedApprove(ctx context.Context, opt options, api *client.Client, state *clientstate.Store, emit func(any) error, project string) error {
	if opt.flags["confirmed"] != "true" {
		return invalid("--plan-id requires --confirmed")
	}
	if key := opt.flags["operation-id"]; key != "" && !operationID.MatchString(key) {
		return invalid("canonical operation ID required")
	}
	link, e := loadPreviewLink(state, opt.flags["plan-id"])
	if e != nil {
		return productError(e)
	}
	if project != "" && project != link.Project {
		return productError(transfer.ErrIntent)
	}
	return commitApprove(ctx, opt, api, state, emit, link.PlanID, link.ExpectedRevision, nil)
}

// commitApprove dispatches (or replays) the plan commit and emits its result.
// Without --operation-id the commit key is the plan id, the plan's natural
// commit identity, so a rerun reaches the same single committed outcome. A
// definitive failure runs release (the preparation key) after the failure has
// been emitted; an uncertain one keeps it.
func commitApprove(ctx context.Context, opt options, api *client.Client, state *clientstate.Store, emit func(any) error, planID string, expected int64, release func() error) error {
	key := opt.flags["operation-id"]
	if key == "" {
		key = planID
	}
	commit, commitID, e := mutation(ctx, api, state, key, "POST", "/api/v1/configuration/plans/"+planID+"/commit", map[string]any{"expected_config_revision": expected, "confirmed": true})
	if e != nil {
		return afterDefinitive(recordedError(productError(e), commit, commitID, nil), commitID, release)
	}
	if e := emit(map[string]any{"plan_id": planID, "operation_id": commitID, "result": json.RawMessage(commit.Body)}); e != nil {
		return e
	}
	if release != nil {
		return release()
	}
	return nil
}

// approvePlanRequest is the journaled plan request of one approval.
type approvePlanRequest struct {
	Scope struct {
		ProjectID string `json:"project_id"`
	} `json:"scope"`
	ExpectedRevision int64 `json:"expected_config_revision"`
	Changes          struct {
		Sources []map[string]any `json:"sources"`
	} `json:"changes"`
}

// sameApproveRequest compares the explicit flags of a rerun with the
// journaled plan request; an omitted optional flag keeps the journaled value.
func sameApproveRequest(raw []byte, project string, opt options) error {
	var request approvePlanRequest
	if json.Unmarshal(raw, &request) != nil || request.Scope.ProjectID != project {
		return transfer.ErrIntent
	}
	for _, entry := range request.Changes.Sources {
		if entry["source_id"] != opt.flags["source-id"] {
			continue
		}
		wanted := buildSourceEntry(opt)
		if entry["kind"] != wanted["kind"] || entry["root_path"] != wanted["root_path"] || entry["repo_id"] != wanted["repo_id"] {
			return transfer.ErrIntent
		}
		if mode := opt.flags["retention-mode"]; mode != "" && entry["retention_mode"] != mode {
			return transfer.ErrIntent
		}
		if opt.flags["provider-ids"] != "" && !sameProviders(entry["provider_ids"], providerFlags(opt)) {
			return transfer.ErrIntent
		}
		return nil
	}
	return transfer.ErrIntent
}

func sameProviders(stored any, wanted []any) bool {
	list, ok := stored.([]any)
	if !ok || len(list) != len(wanted) {
		return false
	}
	for i := range list {
		if list[i] != wanted[i] {
			return false
		}
	}
	return true
}

// buildSourceEntry turns the explicit flags into a source entry (S21: repo_id
// is nullable — files sources send null).
func buildSourceEntry(opt options) map[string]any {
	entry := map[string]any{"source_id": opt.flags["source-id"], "kind": opt.flags["kind"], "root_path": opt.flags["root-path"]}
	if opt.flags["kind"] == "git" {
		entry["repo_id"] = opt.flags["repo-id"]
	} else {
		entry["repo_id"] = nil
	}
	return entry
}

// mergeSourceEntry builds the full submitted array: replace the entry with
// the same source_id keeping unspecified fields, else append (S5).
func mergeSourceEntry(current []map[string]any, opt options, entry map[string]any) []any {
	retention := "reference_only"
	if mode := opt.flags["retention-mode"]; mode != "" {
		retention = mode
	}
	providers := providerFlags(opt)
	merged := make([]any, 0, len(current)+1)
	replaced := false
	for _, existing := range current {
		if existing["source_id"] == opt.flags["source-id"] {
			updated := map[string]any{}
			for k, v := range existing {
				updated[k] = v
			}
			updated["source_id"] = entry["source_id"]
			updated["kind"] = entry["kind"]
			updated["root_path"] = entry["root_path"]
			updated["repo_id"] = entry["repo_id"]
			if opt.flags["retention-mode"] != "" {
				updated["retention_mode"] = retention
			}
			if opt.flags["provider-ids"] != "" {
				updated["provider_ids"] = providers
			}
			merged = append(merged, updated)
			replaced = true
			continue
		}
		merged = append(merged, existing)
	}
	if !replaced {
		merged = append(merged, map[string]any{"source_id": entry["source_id"], "kind": entry["kind"], "root_path": entry["root_path"], "repo_id": entry["repo_id"], "retention_mode": retention, "provider_ids": providers})
	}
	return merged
}

// providerFlags parses --provider-ids into the submitted list.
func providerFlags(opt options) []any {
	providers := []any{}
	if list := opt.flags["provider-ids"]; list != "" {
		for _, value := range strings.Split(list, ",") {
			if strings.TrimSpace(value) != "" {
				providers = append(providers, strings.TrimSpace(value))
			}
		}
	}
	return providers
}

// previewLink records a printed preview so --plan-id can commit it with its
// original expected revision; it is replay data and is never removed by a
// commit.
type previewLink struct {
	PlanID           string `json:"plan_id"`
	Project          string `json:"project_id"`
	ExpectedRevision int64  `json:"expected_config_revision"`
	PlanOperation    string `json:"plan_operation_id"`
}

// previewLinkJournalKey derives the preview record's journal key from the
// plan id: the plan id itself is the commit journal's key.
func previewLinkJournalKey(planID string) string {
	return derivedKey("haeryong/sources-approve-preview/" + planID)
}

// derivedKey derives a stable canonical UUID from a name (RFC 4122 shape,
// name-based like v5), so the client-state store and the Idempotency-Key
// syntax accept it without new third-party code.
func derivedKey(name string) string {
	sum := sha1.Sum([]byte(name))
	b := sum[:16]
	b[6] = b[6]&0x0f | 0x50 // version 5
	b[8] = b[8]&0x3f | 0x80 // RFC 4122 variant
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

func savePreviewLink(state *clientstate.Store, link previewLink, identity transfer.Identity) error {
	raw, e := json.Marshal(link)
	if e != nil {
		return e
	}
	return state.Save(transfer.Journal{OperationID: previewLinkJournalKey(link.PlanID), Purpose: "sources-approve-preview", Identity: identity, Method: "POST", Target: "/api/v1/configuration/plans/" + link.PlanID + "/commit", FinalBody: raw})
}

func loadPreviewLink(state *clientstate.Store, planID string) (previewLink, error) {
	if !operationID.MatchString(planID) {
		return previewLink{}, invalid("canonical plan ID required")
	}
	journal, e := state.Load(previewLinkJournalKey(planID))
	if errors.Is(e, domain.ErrNotFound) {
		return previewLink{}, invalid("no recorded preview for plan " + planID + "; run sources approve without --confirmed first")
	}
	if e != nil {
		return previewLink{}, e
	}
	var link previewLink
	if json.Unmarshal(journal.FinalBody, &link) != nil || link.PlanID != planID || link.ExpectedRevision < 1 {
		return previewLink{}, transfer.ErrIntent
	}
	return link, nil
}

const sealPollInterval = 100 * time.Millisecond

// pollUploadUntilSealed waits for an expectation upload to seal like the
// transfer seal wait (S23/G9): no total time quota, so a slow seal keeps
// waiting until sealed or cancelled; a recorded error, abandonment or a
// stopped (open) upload ends the wait as a typed class-5 failure.
func pollUploadUntilSealed(ctx context.Context, fetch func() (importer.UploadDetail, error), interval time.Duration) error {
	for {
		detail, err := fetch()
		if err != nil {
			return err
		}
		switch {
		case detail.State == "sealed":
			return nil
		case detail.State == "abandoned":
			return commandError{5, fmt.Errorf("expectation upload abandoned")}
		case detail.Error != nil && *detail.Error != "":
			return commandError{5, fmt.Errorf("expectation seal failed: %s", *detail.Error)}
		case detail.State == "open":
			return commandError{5, fmt.Errorf("expectation upload stopped; retained input requires explicit resume")}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(interval):
		}
	}
}

// stageExpectation uploads one large expectation through the approved
// purpose=body flow bound to this exact verification operation (C4/D1/D2):
// create, one whole-object chunk, complete (generation+1), await the seal by
// polling the upload itself (S23: job polling needs job.read that a
// version.read+source.read principal does not hold). The caller-profile
// journal records the final consumer key, the distinct create and seal keys,
// the upload id and its checkpoint before each dispatch; an interrupted
// rerun recovers and reuses them instead of creating a new upload.
func stageExpectation(ctx context.Context, api *client.Client, state *clientstate.Store, versionID, evidenceID, operationKey string, raw []byte) (string, error) {
	if operationKey == "" {
		generated, err := service.NewID()
		if err != nil {
			return "", err
		}
		operationKey = generated
	}
	identity, err := api.Identity(ctx)
	if err != nil {
		return "", err
	}
	route := "/api/v1/versions/" + versionID + "/source-evidence-verifications?evidence_id=" + url.QueryEscape(evidenceID)
	// G1(b): the expectation input's fingerprint (SHA-256 and length) is
	// part of the staging journal from its first write; a recovered staging
	// continues only for the same input, completed or not.
	inputSum := sha256.Sum256(raw)
	fingerprint := fmt.Sprintf("%s:%d", hex.EncodeToString(inputSum[:]), len(raw))
	journal, loadErr := state.Load(operationKey)
	if loadErr == nil {
		if journal.Purpose != "expectation" || journal.OperationID != operationKey || journal.Identity.LibraryID != identity.LibraryID || journal.Identity.PrincipalID != identity.PrincipalID || journal.Method != "POST" || journal.Target != route || journal.Result.Snapshot.Identity != fingerprint {
			return "", transfer.ErrIntent
		}
	} else if errors.Is(loadErr, domain.ErrNotFound) {
		createKey, err := service.NewID()
		if err != nil {
			return "", err
		}
		sealKey, err := service.NewID()
		if err != nil {
			return "", err
		}
		journal = transfer.Journal{OperationID: operationKey, Purpose: "expectation", Identity: identity, Method: "POST", Target: route, Result: transfer.Preparation{CreateKey: createKey, CompleteKey: sealKey, Snapshot: transfer.Snapshot{Identity: fingerprint}, Phase: "capturing"}}
		if err := state.Save(journal); err != nil {
			return "", err
		}
	} else {
		return "", loadErr
	}
	if journal.Result.UploadID == "" {
		target, _ := json.Marshal(map[string]string{"operation_id": operationKey, "action": "evidence_verification", "version_id": versionID, "evidence_id": evidenceID})
		created, err := api.Create(ctx, journal.Result.CreateKey, importer.CreateRequest{Purpose: "body", InputMode: "object", SelectionName: "expected-excerpt", Target: target})
		if err != nil {
			return "", err
		}
		journal.Result.UploadID = created.ID
		journal.Result.Phase = "transferring"
		if err := state.Save(journal); err != nil {
			return "", err
		}
	}
	if !journal.Result.Checkpoint.Complete {
		length := int64(len(raw))
		chunk, err := api.Put(ctx, journal.Result.UploadID, importer.Chunk{Path: "_input", Kind: "file", Hash: hex.EncodeToString(inputSum[:]), Length: &length, Final: true}, strings.NewReader(string(raw)))
		if err != nil {
			return "", err
		}
		// D1: the server requires the successor generation at completion.
		cp := chunk.Checkpoint
		cp.Generation++
		cp.Complete = true
		journal.Result.Checkpoint = cp
		journal.Result.Phase = "complete_dispatch"
		if err := state.Save(journal); err != nil {
			return "", err
		}
	}
	detail, err := api.Upload(ctx, journal.Result.UploadID)
	if err != nil {
		return "", err
	}
	if detail.State != "sealed" {
		response, err := api.Complete(ctx, journal.Result.UploadID, journal.Result.CompleteKey, journal.Result.Checkpoint)
		if err != nil {
			return "", err
		}
		_ = response
		// S23/G9: poll the upload itself until sealed; surface failed
		// seals, recorded errors and a stopped open upload distinctly.
		if err := pollUploadUntilSealed(ctx, func() (importer.UploadDetail, error) { return api.Upload(ctx, journal.Result.UploadID) }, sealPollInterval); err != nil {
			return "", err
		}
	}
	journal.Result.Phase = "sealed"
	if err := state.Save(journal); err != nil {
		return "", err
	}
	return journal.Result.UploadID, nil
}
