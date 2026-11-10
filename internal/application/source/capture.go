package source

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	appauth "haeryong.local/haeryong/internal/application/auth"
	"io"
	"os"
	"syscall"
	"time"

	"haeryong.local/haeryong/internal/application/service"
	app "haeryong.local/haeryong/internal/application/storage"
	"haeryong.local/haeryong/internal/domain"
)

type CapturePath struct {
	Path   string `json:"path"`
	ReadID string `json:"read_id,omitempty"`
}
type CaptureSelection struct {
	SourceID    string        `json:"source_id"`
	Revision    string        `json:"revision,omitempty"`
	WorkingCopy bool          `json:"working_copy,omitempty"`
	Paths       []CapturePath `json:"paths"`
}
type CaptureRequest struct {
	SourceIntent string             `json:"source_intent,omitempty"`
	VersionID    string             `json:"version_id,omitempty"`
	SourceMode   string             `json:"source_mode,omitempty"`
	ProviderID   string             `json:"provider_id,omitempty"`
	Selections   []CaptureSelection `json:"selections"`
}

// RetainedReadIDs is an internal ownership projection of the exact effective
// mapping at reservation. Ignored IDs never become lifecycle dependencies.
func RetainedReadIDs(input CaptureRequest, mappings map[string]domain.ManagedSourceMapping) []string {
	ids := []string{}
	seen := map[string]bool{}
	if input.SourceMode != "pinned" {
		return ids
	}
	for _, selection := range input.Selections {
		mapping := mappings[selection.SourceID]
		if mapping.Kind != "files" && !selection.WorkingCopy {
			continue
		}
		for _, path := range selection.Paths {
			if path.ReadID != "" && !seen[path.ReadID] {
				ids = append(ids, path.ReadID)
				seen[path.ReadID] = true
			}
		}
	}
	return ids
}

type CaptureBytes interface {
	Bytes
	Put(context.Context, io.Reader, func(app.WriteTx, domain.Object) error) (domain.Object, error)
}
type FileReader interface {
	Open(context.Context, string, string) (io.ReadCloser, error)
}

// CaptureAdmission is a trusted internal owner seam. Callers atomically create
// their authorized job and attach its exact read-set here, before source pins
// are released. No public job_id or caller digest supplies admission authority.
type CaptureAdmission func(app.WriteTx, domain.SourceCapture) error

func (s *Service) capturePrincipal(tx app.WriteTx, actor domain.Session, project, version string) error {
	if _, e := tx.Scope(project); e != nil {
		return e
	}
	if actor.Scope.Kind == "owner" && version == "" {
		return nil
	}
	if version == "" {
		return domain.ErrNotFound
	}
	el, e := Check(tx, actor, version)
	if e != nil {
		return e
	}
	if el.ProjectID != project {
		return domain.ErrNotFound
	}
	if !el.MayReadSource() {
		return domain.ErrDenied
	}
	return nil
}

// Capture persists the exact selected bytes, never provider delivery. Reads and
// fsyncs run outside the writer; current policy is rechecked before publication.
func (s *Service) Capture(ctx context.Context, token, key, project string, input CaptureRequest) (domain.Response, error) {
	return s.CaptureAndAdmit(ctx, token, key, project, input, nil)
}
func (s *Service) CaptureAndAdmit(ctx context.Context, token, key, project string, input CaptureRequest, admit CaptureAdmission) (domain.Response, error) {
	return s.captureAndAdmit(ctx, token, key, project, input, admit, "", "")
}

// CaptureGenerationAndAdmit reads only an already accepted generation's frozen
// selection. Current binding/epoch/policy remain required; bearer expiry alone
// does not cancel this local preparation. The private preparation receipt has
// its own operation key and is not an additional HTTP endpoint.
func (s *Service) CaptureGenerationAndAdmit(ctx context.Context, jobID string, admit CaptureAdmission) (domain.Response, error) {
	var g domain.GenerationIntent
	e := s.Store.Write(ctx, func(tx app.WriteTx) error { var e error; g, e = tx.GenerationIntent(jobID); return e })
	if e != nil {
		return domain.Response{}, e
	}
	var input CaptureRequest
	if json.Unmarshal(g.SourceSelection, &input) != nil || admit == nil {
		return domain.Response{}, domain.ErrInvalidTarget
	}
	return s.captureAndAdmit(ctx, "", g.CaptureOperationID, g.ProjectID, input, admit, jobID, "generation")
}

// CaptureQuestionAndAdmit retains the accepted question's exact version context
// throughout local preparation, independently of its old bearer/render channel.
func (s *Service) CaptureQuestionAndAdmit(ctx context.Context, jobID string, admit CaptureAdmission) (domain.Response, error) {
	var q domain.Question
	e := s.Store.Write(ctx, func(tx app.WriteTx) error { var e error; q, e = tx.QuestionByJob(jobID); return e })
	if e != nil {
		return domain.Response{}, e
	}
	var input CaptureRequest
	if json.Unmarshal(q.SourceSelection, &input) != nil || input.VersionID != q.VersionID || admit == nil {
		return domain.Response{}, domain.ErrInvalidTarget
	}
	return s.captureAndAdmit(ctx, "", q.CaptureOperationID, q.ProjectID, input, admit, jobID, "question")
}

// CaptureMaintenanceAndAdmit executes each planned source capture step for a
// paused maintenance child job, pinning every captured read across multi-step
// captures before releasing transient source_capture pins, and atomically admits
// the maintenance job to queued once all steps complete.
func (s *Service) CaptureMaintenanceAndAdmit(ctx context.Context, jobID string, identity func(string, json.RawMessage, domain.ProviderInvocation, []domain.CapturedRead) (string, error)) error {
	if identity == nil {
		return errors.Join(domain.ErrInvalidTarget, s.pauseMaintenanceCapture(context.WithoutCancel(ctx), jobID, "source_capture_unavailable", nil))
	}
	var count int
	var done bool
	if e := s.Store.Write(ctx, func(tx app.WriteTx) error {
		j, e := tx.Job(jobID)
		if e != nil {
			return e
		}
		if j.CancelRequested {
			done = true
			if err := tx.ReleaseOwnerPins("job_input", j.ID); err != nil {
				return err
			}
			return tx.ConfirmStop(j.ID, j.AttemptGeneration)
		}
		if j.Kind != "maintenance" || j.State != "paused" || j.Error != "source_capture_pending" {
			done = true
			return nil
		}
		intent, e := tx.MaintenanceIntent(jobID)
		if e != nil {
			return e
		}
		if intent.AdmissionReady {
			done = true
			return nil
		}
		count = len(intent.Captures)
		return nil
	}); e != nil || done {
		return e
	}
	for idx := 0; idx < count; idx++ {
		var step domain.MaintenanceCaptureStep
		var skip bool
		if e := s.Store.Write(ctx, func(tx app.WriteTx) error {
			j, e := tx.Job(jobID)
			if e != nil {
				return e
			}
			if j.CancelRequested {
				done = true
				if err := tx.ReleaseOwnerPins("job_input", j.ID); err != nil {
					return err
				}
				return tx.ConfirmStop(j.ID, j.AttemptGeneration)
			}
			if j.Kind != "maintenance" || j.State != "paused" || j.Error != "source_capture_pending" {
				done = true
				return nil
			}
			intent, e := tx.MaintenanceIntent(jobID)
			if e != nil {
				return e
			}
			if intent.AdmissionReady || idx >= len(intent.Captures) {
				done = true
				return nil
			}
			step = intent.Captures[idx]
			if step.Finished {
				skip = true
			}
			return nil
		}); e != nil || done {
			return e
		}
		if skip {
			continue
		}
		var input CaptureRequest
		if json.Unmarshal(step.SourceSelection, &input) != nil || step.ProjectID == "" || step.VersionID == "" || input.VersionID == "" || input.VersionID != step.VersionID {
			lim := []domain.MaintenanceLimitation{{
				Kind:            "source_unavailable",
				TargetVersionID: step.VersionID,
				Reason:          "incomplete_source_capture",
			}}
			return errors.Join(domain.ErrInvalidTarget, s.pauseMaintenanceCapture(context.WithoutCancel(ctx), jobID, "source_capture_unavailable", lim))
		}
		_, captureErr := s.captureAndAdmit(ctx, "", step.OperationID, step.ProjectID, input, func(tx app.WriteTx, c domain.SourceCapture) error {
			j, e := tx.Job(jobID)
			if e != nil {
				return e
			}
			curIntent, e := tx.MaintenanceIntent(jobID)
			if e != nil {
				return e
			}
			if idx >= len(curIntent.Captures) {
				return domain.ErrInvalidTarget
			}
			var scope domain.ProviderScope
			if json.Unmarshal(j.Scope, &scope) != nil {
				return domain.ErrInvalidTarget
			}
			if e = app.AuthorizeMaintenance(tx, j, scope, "", ""); e != nil {
				return e
			}
			if c.ProjectID != step.ProjectID || c.PrincipalID != scope.PrincipalID || c.ConfigRevision != j.ConfigRevision || c.PermissionsRevision != j.Permissions[c.ProjectID] {
				return domain.ErrPrecondition
			}
			readIDs := []string{}
			after := ""
			for {
				rows, e := tx.SourceCaptureReads(c.ID, after, 64)
				if e != nil {
					return e
				}
				if len(rows) == 0 {
					break
				}
				for _, r := range rows {
					if r.Availability != "available" {
						return domain.ErrReconciliation
					}
					read, e := tx.CapturedRead(r.ID)
					if e != nil {
						return e
					}
					if e = tx.AcquirePin(domain.Pin{ID: "job-input:" + j.ID + ":" + r.ID, Kind: "job_input", OwnerID: j.ID, Hash: read.BytesSHA}); e != nil {
						return e
					}
					readIDs = append(readIDs, r.ID)
					after = r.ID
				}
			}
			curStep := curIntent.Captures[idx]
			curStep.CaptureID = c.ID
			curStep.TargetSetID = c.TargetSetID
			curStep.ReadIDs = readIDs
			curStep.State = c.State
			curStep.Reason = c.Reason
			curStep.Finished = true
			curIntent.Captures[idx] = curStep
			return tx.UpdateMaintenanceIntent(curIntent)
		}, jobID, "maintenance")
		if captureErr != nil {
			if errors.Is(captureErr, domain.ErrStaleWorker) {
				return nil
			}
			return errors.Join(captureErr, s.pauseMaintenanceCapture(context.WithoutCancel(ctx), jobID, "source_capture_unavailable", nil))
		}
		var stepFinished bool
		if e := s.Store.Write(context.WithoutCancel(ctx), func(tx app.WriteTx) error {
			curIntent, e := tx.MaintenanceIntent(jobID)
			if e != nil {
				return e
			}
			if idx < len(curIntent.Captures) {
				stepFinished = curIntent.Captures[idx].Finished && curIntent.Captures[idx].State == "completed"
			}
			return nil
		}); e != nil {
			return e
		}
		if !stepFinished {
			lim := []domain.MaintenanceLimitation{{
				Kind:            "source_unavailable",
				TargetVersionID: step.VersionID,
				Reason:          "incomplete_source_capture",
			}}
			return s.pauseMaintenanceCapture(context.WithoutCancel(ctx), jobID, "source_capture_unavailable", lim)
		}
	}
	var failLims []domain.MaintenanceLimitation
	admitErr := s.Store.Write(context.WithoutCancel(ctx), func(tx app.WriteTx) error {
		j, e := tx.Job(jobID)
		if e != nil {
			return e
		}
		if j.CancelRequested {
			if err := tx.ReleaseOwnerPins("job_input", j.ID); err != nil {
				return err
			}
			return tx.ConfirmStop(j.ID, j.AttemptGeneration)
		}
		if j.State != "paused" || j.Error != "source_capture_pending" {
			return nil
		}
		curIntent, e := tx.MaintenanceIntent(jobID)
		if e != nil {
			return e
		}
		if curIntent.AdmissionReady {
			return nil
		}
		var scope domain.ProviderScope
		if json.Unmarshal(j.Scope, &scope) != nil || scope.Invocation == nil || scope.Invocation.Maintenance == nil {
			return domain.ErrInvalidTarget
		}
		if e = app.AuthorizeMaintenance(tx, j, scope, "", ""); e != nil {
			return e
		}
		allReads := []domain.CapturedRead{}
		allReadIDs := []string{}
		bindings := []domain.MaintenanceReadBinding{}
		seenReadID := map[string]bool{}
		targetSetsByVersion := map[string]string{}
		distinctTargetSets := map[string]bool{}
		for _, b := range curIntent.ReadBindings {
			if b.ReadID == "" || seenReadID[b.ReadID] {
				continue
			}
			read, e := tx.CapturedRead(b.ReadID)
			if e != nil {
				return e
			}
			seenReadID[b.ReadID] = true
			allReads = append(allReads, read)
			allReadIDs = append(allReadIDs, b.ReadID)
			bindings = append(bindings, b)
			if b.TargetSetID != "" {
				distinctTargetSets[b.TargetSetID] = true
			}
		}
		for _, step := range curIntent.Captures {
			if !step.Finished || step.State != "completed" || step.TargetSetID == "" || step.ProjectID == "" || step.VersionID == "" {
				failLims = []domain.MaintenanceLimitation{{
					Kind:            "source_unavailable",
					TargetVersionID: step.VersionID,
					Reason:          "incomplete_source_capture",
				}}
				return domain.ErrReconciliation
			}
			targetSetsByVersion[step.VersionID] = step.TargetSetID
			distinctTargetSets[step.TargetSetID] = true
			for _, readID := range step.ReadIDs {
				if seenReadID[readID] {
					continue
				}
				read, e := tx.CapturedRead(readID)
				if e != nil {
					return e
				}
				seenReadID[readID] = true
				allReads = append(allReads, read)
				allReadIDs = append(allReadIDs, readID)
				bindings = append(bindings, domain.MaintenanceReadBinding{
					ReadID:      readID,
					ProjectID:   step.ProjectID,
					VersionID:   step.VersionID,
					TargetSetID: step.TargetSetID,
				})
			}
		}
		scope.Invocation.Maintenance.ReadBindings = bindings
		for i := range scope.Invocation.Maintenance.Explanations {
			verID := scope.Invocation.Maintenance.Explanations[i].VersionID
			if ts, ok := targetSetsByVersion[verID]; ok {
				scope.Invocation.Maintenance.Explanations[i].TargetSetID = ts
			}
		}
		scope.ReadIDs = allReadIDs
		if len(distinctTargetSets) == 1 {
			for ts := range distinctTargetSets {
				scope.TargetSetID = ts
			}
		} else {
			scope.TargetSetID = ""
		}
		if curIntent.Mode == domain.MaintenanceModeFreshness {
			if len(distinctTargetSets) != 1 {
				return domain.ErrInvalidTarget
			}
			if e = tx.BindFreshnessCoverage(&curIntent, scope.TargetSetID); e != nil {
				return e
			}
			scope.Invocation.Maintenance.CoverageSetID = curIntent.CoverageSetID
			if e = tx.UpdateMaintenanceIntent(curIntent); e != nil {
				return e
			}
		}
		scope.RequestSHA, e = identity(scope.PrincipalID, scope.Target, *scope.Invocation, allReads)
		if e != nil {
			return e
		}
		j.Scope = encoded(scope)
		curIntent.SourceReadIDs = allReadIDs
		curIntent.ReadBindings = bindings
		if e = tx.AdmitMaintenanceCapture(j, curIntent); e != nil {
			return e
		}
		if e = tx.AttachSourceInputs(j.ID, scope.ReadIDs); e != nil {
			return e
		}
		if curIntent.Mode == domain.MaintenanceModeFreshness {
			// Hold the shared exact capture for the scan's remaining fixed
			// units. These are job pins, released by ordinary scan settlement.
			for _, read := range allReads {
				if e = tx.AcquirePin(domain.Pin{ID: "job-source:" + curIntent.ScanJobID + ":" + read.ID, Kind: "job", OwnerID: curIntent.ScanJobID, Hash: read.BytesSHA}); e != nil {
					return e
				}
			}
		}
		return nil
	})
	if admitErr != nil {
		if errors.Is(admitErr, domain.ErrStaleWorker) {
			return nil
		}
		return errors.Join(admitErr, s.pauseMaintenanceCapture(context.WithoutCancel(ctx), jobID, "source_capture_unavailable", failLims))
	}
	return nil
}

func (s *Service) pauseMaintenanceCapture(ctx context.Context, jobID, reason string, lims []domain.MaintenanceLimitation) error {
	return s.Store.Write(ctx, func(tx app.WriteTx) error {
		j, e := tx.Job(jobID)
		if e != nil {
			return e
		}
		if e := tx.ReleaseOwnerPins("job_input", j.ID); e != nil {
			return e
		}
		if j.CancelRequested {
			return tx.ConfirmStop(j.ID, j.AttemptGeneration)
		}
		if j.State != "paused" || j.Error != "source_capture_pending" {
			return nil
		}
		return tx.PauseMaintenanceCapture(jobID, reason, lims)
	})
}

func maintenanceCapturePrincipal(tx app.WriteTx, actor domain.Session, project, version string) error {
	if _, e := tx.Scope(project); e != nil {
		return e
	}
	if version != "" {
		v, e := tx.PublishedVersion(version)
		if e != nil {
			return e
		}
		d, e := tx.Document(v.DocumentID)
		if e != nil {
			return e
		}
		if d.ProjectID != project || d.Lifecycle == "trash" || d.Lifecycle == "purged" {
			return domain.ErrNotFound
		}
		if actor.Scope.Kind != "owner" {
			if _, _, e := app.CheckSourceVersion(tx, actor, version); e != nil {
				return e
			}
		}
	}
	if actor.Scope.Kind == "owner" {
		return nil
	}
	has := func(values []string, want string) bool {
		for _, v := range values {
			if v == want {
				return true
			}
		}
		return false
	}
	if !has(actor.Scope.ProjectIDs, project) {
		return domain.ErrNotFound
	}
	if !has(actor.Scope.Actions, "scan.request") || !has(actor.Scope.Actions, "source.read") {
		return domain.ErrDenied
	}
	return nil
}
func generationCapturePrincipal(tx app.WriteTx, actor domain.Session, project string) error {
	if _, e := tx.Scope(project); e != nil {
		return e
	}
	if actor.Scope.Kind == "owner" {
		return nil
	}
	has := func(values []string, want string) bool {
		for _, v := range values {
			if v == want {
				return true
			}
		}
		return false
	}
	if !has(actor.Scope.ProjectIDs, project) {
		return domain.ErrNotFound
	}
	if !has(actor.Scope.Actions, "generation.request") || !has(actor.Scope.Actions, "source.read") {
		return domain.ErrDenied
	}
	return nil
}
func (s *Service) captureAndAdmit(ctx context.Context, token, key, project string, input CaptureRequest, admit CaptureAdmission, acceptedJob, acceptedKind string) (domain.Response, error) {
	requestedSelection := encoded(input)
	authenticate := func(ctx context.Context, tx app.WriteTx) (domain.Session, error) {
		if acceptedJob == "" {
			return s.Auth.AuthenticateWrite(ctx, tx, token)
		}
		j, e := tx.Job(acceptedJob)
		if e != nil {
			return domain.Session{}, e
		}
		if j.Kind != acceptedKind || j.State != "paused" || j.Error != "source_capture_pending" || j.CancelRequested {
			return domain.Session{}, domain.ErrStaleWorker
		}
		switch acceptedKind {
		case "generation":
			g, e := tx.GenerationIntent(j.ID)
			if e != nil {
				return domain.Session{}, e
			}
			if g.AdmissionReady || g.ProjectID != project || g.CaptureOperationID != key || !bytes.Equal(g.SourceSelection, requestedSelection) {
				return domain.Session{}, domain.ErrStaleWorker
			}
		case "question":
			q, e := tx.QuestionByJob(j.ID)
			if e != nil {
				return domain.Session{}, e
			}
			if q.AdmissionReady || q.ProjectID != project || q.VersionID != input.VersionID || q.CaptureOperationID != key || !bytes.Equal(q.SourceSelection, requestedSelection) {
				return domain.Session{}, domain.ErrStaleWorker
			}
		case "maintenance":
			m, e := tx.MaintenanceIntent(j.ID)
			if e != nil {
				return domain.Session{}, e
			}
			if m.AdmissionReady {
				return domain.Session{}, domain.ErrStaleWorker
			}
			matched := false
			for _, step := range m.Captures {
				if !step.Finished && step.ProjectID == project && step.OperationID == key && bytes.Equal(step.SourceSelection, requestedSelection) {
					matched = true
					break
				}
			}
			if !matched {
				return domain.Session{}, domain.ErrStaleWorker
			}
		default:
			return domain.Session{}, domain.ErrInvalidTarget
		}
		var scope domain.ProviderScope
		if json.Unmarshal(j.Scope, &scope) != nil || scope.Purpose != "body" {
			return domain.Session{}, domain.ErrInvalidTarget
		}
		if e = appauth.AuthorizeAccepted(tx, scope.PrincipalID, scope.AuthEpoch, "body", scope.Target, scope.CredentialScope); e != nil {
			return domain.Session{}, e
		}
		if acceptedKind == "question" {
			if e = app.AuthorizeQuestion(tx, scope); e != nil {
				return domain.Session{}, e
			}
		}
		if acceptedKind == "maintenance" {
			if e = app.AuthorizeMaintenance(tx, j, scope, "", ""); e != nil {
				return domain.Session{}, e
			}
		}
		captured := scope.CredentialScope
		if captured.Kind != "owner" {
			p, e := tx.WorkflowPrincipal(scope.PrincipalID)
			if e != nil {
				return domain.Session{}, e
			}
			captured = appauth.RestrictScope(captured, p.Scope)
		}
		return domain.Session{PrincipalID: scope.PrincipalID, AuthEpoch: scope.AuthEpoch, Scope: captured}, nil
	}
	principal := func(tx app.WriteTx, actor domain.Session) error {
		if acceptedKind == "generation" {
			return generationCapturePrincipal(tx, actor, project)
		}
		if acceptedKind == "maintenance" {
			return maintenanceCapturePrincipal(tx, actor, project, input.VersionID)
		}
		return s.capturePrincipal(tx, actor, project, input.VersionID)
	}
	var actor domain.Session
	var e error
	if acceptedJob == "" {
		actor, e = s.Auth.Authenticate(ctx, token)
	} else {
		e = s.Store.Write(ctx, func(tx app.WriteTx) error { var e error; actor, e = authenticate(ctx, tx); return e })
	}
	if e != nil {
		return domain.Response{}, e
	}
	if key == "" || len(input.Selections) == 0 || input.SourceIntent != "" && input.SourceIntent != "historical" && input.SourceIntent != "moving_target" {
		return domain.Response{}, domain.ErrInvalidTarget
	}
	seenSources := map[string]bool{}
	for _, selection := range input.Selections {
		if seenSources[selection.SourceID] {
			return domain.Response{}, domain.ErrInvalidTarget
		}
		seenSources[selection.SourceID] = true
		if selection.SourceID == "" || len(selection.Paths) == 0 {
			return domain.Response{}, domain.ErrInvalidTarget
		}
		seen := map[string]bool{}
		for _, p := range selection.Paths {
			if p.Path == "" || seen[p.Path] {
				return domain.Response{}, domain.ErrInvalidTarget
			}
			seen[p.Path] = true
		}
	}
	body := encoded(input)
	route := "/source-captures"
	if acceptedJob != "" {
		route = "/" + acceptedKind + "-source-captures"
	}
	request := domain.Request{Key: domain.OperationKey{PrincipalID: actor.PrincipalID, OperationID: key}, Method: "POST", Target: "/api/v1/projects/" + project + route, Body: body}
	var capture domain.SourceCapture
	var response domain.Response
	done := false
	maps := map[string]domain.ManagedSourceMapping{}
	prepared := map[string]string{}
	e = s.Store.Write(ctx, func(tx app.WriteTx) error {
		current, e := authenticate(ctx, tx)
		if e != nil {
			return e
		}
		if e = principal(tx, current); e != nil {
			return e
		}
		cfg, e := tx.Configuration()
		if e != nil {
			return e
		}
		scope, e := tx.Scope(project)
		if e != nil {
			return e
		}
		for _, selection := range input.Selections {
			m, e := app.ManagedSourcePolicy(tx, project, selection.SourceID, input.ProviderID)
			if e != nil {
				return e
			}
			maps[m.SourceID] = m
		}
		if acceptedJob == "" {
			if e = appauth.AuthorizeFreshWorkflowMutation(tx, current, request); e != nil {
				return e
			}
		}
		receipt, exists, e := tx.ReserveOperation(request, func() ([]byte, error) {
			id, e := service.NewID()
			if e != nil {
				return nil, e
			}
			target, e := service.NewID()
			return encoded(map[string]string{"capture_id": id, "target_set_id": target}), e
		})
		if e != nil {
			return e
		}
		if exists {
			done = true
			if receipt.State == "completed" || receipt.State == "failed" {
				response = *receipt.Response
				return nil
			}
			if receipt.State == "reconciling" {
				return domain.ErrReconciliation
			}
			response = PendingResponse(key)
			return nil
		}
		var ids map[string]string
		if e = json.Unmarshal(receipt.Normalization, &ids); e != nil {
			return e
		}
		if input.SourceMode == "" && input.SourceIntent == "historical" {
			input.SourceMode = "pinned"
		}
		if input.SourceMode == "" {
			var value struct {
				Projects map[string]struct {
					Mode string `json:"source_mode_default"`
				} `json:"projects"`
			}
			if e = json.Unmarshal(cfg.Value, &value); e != nil {
				return e
			}
			input.SourceMode = value.Projects[project].Mode
			if input.SourceMode == "" {
				input.SourceMode = "pinned"
			}
		}
		if input.SourceMode != "pinned" && input.SourceMode != "current" {
			return domain.ErrInvalidTarget
		}
		var savedSelection map[string]json.RawMessage
		if e = json.Unmarshal(encoded(input), &savedSelection); e != nil {
			return e
		}
		savedSelection["_retained_read_ids"] = encoded(RetainedReadIDs(input, maps))
		capture = domain.SourceCapture{ID: ids["capture_id"], PrincipalID: current.PrincipalID, ProjectID: project, TargetSetID: ids["target_set_id"], State: "capturing", Selection: encoded(savedSelection), ConfigRevision: cfg.Revision, PermissionsRevision: scope.Project.PermissionsRevision, CapturedAt: s.now(), OperationID: key}
		if e = tx.CreateSourceCapture(capture); e != nil {
			return e
		}
		for _, selection := range input.Selections {
			for _, path := range selection.Paths {
				id, e := service.NewID()
				if e != nil {
					return e
				}
				prepared[selection.SourceID+"\x00"+path.Path] = id
				if e = tx.PutSourceCaptureRead(domain.SourceCaptureRead{ID: id, CaptureID: capture.ID, SourceID: selection.SourceID, SourceJSON: encoded(map[string]any{}), LocationJSON: encoded(map[string]any{"path": path.Path}), RetentionMode: maps[selection.SourceID].RetentionMode, Availability: "unavailable", Reason: "not_read"}); e != nil {
					return e
				}
			}
		}
		return nil
	})
	if e != nil || done {
		return response, e
	}
	// Accepted work survives a lost caller acknowledgement. An explicit bound
	// remains available, but ordinary progressing selections have no total quota.
	turn, cancelCapture := context.WithCancel(context.WithoutCancel(ctx))
	defer cancelCapture()
	if s.Lifetime != nil {
		stop := context.AfterFunc(s.Lifetime, cancelCapture)
		defer stop()
		if s.Lifetime.Err() != nil {
			cancelCapture()
		}
	}
	if s.CheckBound > 0 {
		var cancel context.CancelFunc
		turn, cancel = context.WithTimeout(turn, s.CheckBound)
		defer cancel()
	}
	bytes, ok := s.Bytes.(CaptureBytes)
	if !ok {
		return s.failCapture(turn, capture, ErrObjectMissing)
	}
	guard := func(tx app.WriteTx) error {
		current, e := authenticate(turn, tx)
		if e != nil {
			return e
		}
		if e = principal(tx, current); e != nil {
			return e
		}
		cfg, e := tx.Configuration()
		if e != nil {
			return e
		}
		scope, e := tx.Scope(project)
		if e != nil {
			return e
		}
		if cfg.Revision != capture.ConfigRevision || scope.Project.PermissionsRevision != capture.PermissionsRevision {
			return domain.ErrPrecondition
		}
		for _, selection := range input.Selections {
			if _, e = app.ManagedSourcePolicy(tx, project, selection.SourceID, input.ProviderID); e != nil {
				return e
			}
		}
		return nil
	}

	targets := []json.RawMessage{}
	complete := true
	for _, selection := range input.Selections {
		if e = s.Store.Write(turn, guard); e != nil {
			return s.failCapture(turn, capture, e)
		}
		mapping := maps[selection.SourceID]
		revision := selection.Revision
		var resolveErr error
		if mapping.Kind == "git" && !selection.WorkingCopy {
			if input.SourceMode == "current" {
				if selection.Revision != "" && s.Reader.ResolveRevision(turn, mapping.RootPath, selection.Revision) == nil {
					revision = selection.Revision
				} else if resolver, ok := s.Reader.(interface {
					ResolveRef(context.Context, string, string) (string, error)
				}); ok {
					revision, resolveErr = resolver.ResolveRef(turn, mapping.RootPath, selection.Revision)
				} else if selection.Revision == "" {
					revision, resolveErr = s.Reader.ResolveHead(turn, mapping.RootPath)
				} else {
					resolveErr = ErrObjectMissing
				}
			} else {
				resolveErr = s.Reader.ResolveRevision(turn, mapping.RootPath, revision)
			}
		}
		for _, path := range selection.Paths {
			if e = s.Store.Write(turn, guard); e != nil {
				return s.failCapture(turn, capture, e)
			}
			readID := prepared[selection.SourceID+"\x00"+path.Path]
			row := domain.SourceCaptureRead{ID: readID, CaptureID: capture.ID, SourceID: selection.SourceID, LocationJSON: encoded(map[string]any{"path": path.Path}), RetentionMode: mapping.RetentionMode, Availability: "available"}
			identity := map[string]any{"kind": "snapshot"}
			if mapping.Kind == "git" && !selection.WorkingCopy {
				format := "sha1"
				if len(revision) == 64 {
					format = "sha256"
				}
				identity = map[string]any{"kind": "git", "repo_id": mapping.RepoID, "object_format": format, "exact_revision": revision}
			}
			row.SourceJSON = encoded(identity)
			var stream io.ReadCloser
			readErr := resolveErr
			if policy, ok := s.Files.(interface {
				CheckLocation(context.Context, string, string) error
			}); ok && readErr == nil {
				readErr = policy.CheckLocation(turn, mapping.RootPath, path.Path)
			}
			if readErr == nil {
				switch {
				case input.SourceMode == "pinned" && (mapping.Kind == "files" || selection.WorkingCopy):
					pinnedGuard := func(tx app.WriteTx) error {
						if err := guard(tx); err != nil {
							return err
						}
						current, err := authenticate(turn, tx)
						if err != nil {
							return err
						}
						return app.CheckSourceReadAuthority(tx, current, path.ReadID, project, input.ProviderID)
					}
					readPinned := func(consume func(io.Reader) error) error {
						if acceptedJob != "" {
							return s.readPinnedAccepted(turn, project, mapping, path, pinnedGuard, consume)
						}
						return s.readPinnedCapture(turn, token, project, mapping, path, consume)
					}
					readErr = readPinned(func(reader io.Reader) error {
						_, e = bytes.Put(turn, reader, func(tx app.WriteTx, o domain.Object) error {
							if e := pinnedGuard(tx); e != nil {
								return e
							}
							row.Hash, row.Size = o.Hash, o.Size
							identity["snapshot_sha256"] = o.Hash
							row.SourceJSON = encoded(identity)
							return tx.PutSourceCaptureRead(row)
						})
						return e
					})
				default:
					if mapping.Kind == "git" && !selection.WorkingCopy {
						stream, readErr = s.Reader.ReadBlob(turn, mapping.RootPath, revision, path.Path)
					} else if s.Files != nil {
						stream, readErr = s.Files.Open(turn, mapping.RootPath, path.Path)
					} else {
						readErr = ErrInvalidRoot
					}
					if readErr == nil {
						_, readErr = bytes.Put(turn, stream, func(tx app.WriteTx, o domain.Object) error {
							if e := guard(tx); e != nil {
								return e
							}
							row.Hash, row.Size = o.Hash, o.Size
							if identity["kind"] == "snapshot" {
								identity["snapshot_sha256"] = o.Hash
								row.SourceJSON = encoded(identity)
							}
							return tx.PutSourceCaptureRead(row)
						})
						readErr = errors.Join(readErr, stream.Close())
					}
				}
			}
			if readErr != nil {
				if _, code := settleClass(readErr); code != "" || errors.Is(readErr, domain.ErrPrecondition) || errors.Is(readErr, context.Canceled) || errors.Is(readErr, context.DeadlineExceeded) || errors.Is(readErr, syscall.ENOSPC) || errors.Is(readErr, syscall.EDQUOT) {
					return s.failCapture(turn, capture, readErr)
				}
				complete = false
				row.Hash = ""
				row.Size = 0
				row.Availability = "unavailable"
				row.Reason = "source_read_failed"
				if errors.Is(readErr, ErrInvalidLocation) {
					row.Reason = "location_invalid"
				}
				if errors.Is(readErr, ErrSourceChanged) {
					row.Reason = "source_changed"
				}
				if errors.Is(readErr, os.ErrPermission) {
					row.Reason = "source_unreadable"
				}
				if errors.Is(readErr, os.ErrNotExist) || errors.Is(readErr, ErrInvalidRoot) || errors.Is(readErr, ErrObjectMissing) {
					row.Reason = "missing_source"
				}
				if errors.Is(readErr, ErrSourceDenied) || errors.Is(readErr, domain.ErrDenied) {
					row.Availability = "denied"
					row.Reason = "source_denied"
				}
				// A failed immutable Put may have left no row; terminal metadata is kept
				// for every requested unit instead of reducing the selected denominator.
				if e = s.Store.Write(turn, func(tx app.WriteTx) error { return tx.PutSourceCaptureRead(row) }); e != nil {
					return s.failCapture(turn, capture, e)
				}
			} else {
				entry := map[string]any{"source_id": row.SourceID, "identity": identity, "read_id": row.ID}
				targets = append(targets, encoded(entry))
			}
		}
	}
	if s.Fault != nil {
		if e = s.Fault("capture_before_commit"); e != nil {
			return s.failCapture(turn, capture, e)
		}
	}
	e = s.Store.Write(turn, func(tx app.WriteTx) error {
		current, e := authenticate(turn, tx)
		if e != nil {
			return e
		}
		if e = principal(tx, current); e != nil {
			return e
		}
		cfg, e := tx.Configuration()
		if e != nil {
			return e
		}
		scope, e := tx.Scope(project)
		if e != nil {
			return e
		}
		if cfg.Revision != capture.ConfigRevision || scope.Project.PermissionsRevision != capture.PermissionsRevision {
			return domain.ErrPrecondition
		}
		for _, selection := range input.Selections {
			mapping, err := app.ManagedSourcePolicy(tx, project, selection.SourceID, input.ProviderID)
			if err != nil {
				return err
			}
			if input.SourceMode == "pinned" && (mapping.Kind == "files" || selection.WorkingCopy) {
				for _, path := range selection.Paths {
					if err = app.CheckSourceReadAuthority(tx, current, path.ReadID, project, input.ProviderID); err != nil {
						return err
					}
				}
			}
		}
		state, reason := "failed", "incomplete_source_capture"
		if complete {
			state, reason = "completed", ""
			if e = tx.PutTargetSet(domain.TargetSet{ID: capture.TargetSetID, ProjectID: project, RequestedSelection: capture.Selection, TargetsJSON: encoded(map[string]any{"sources": targets}), CapturedAt: capture.CapturedAt}); e != nil {
				return e
			}
		}
		if e = tx.FinishSourceCapture(capture.ID, state, reason); e != nil {
			return e
		}
		capture.State, capture.Reason = state, reason
		if complete && admit != nil {
			if e = admit(tx, capture); e != nil {
				return e
			}
		}
		if e = tx.ReleaseOwnerPins("source_capture", capture.ID); e != nil {
			return e
		}
		response = domain.Response{Status: 201, Body: encoded(captureProjection(capture))}
		return tx.CompleteOperation(request.Key, response)
	})
	if e != nil {
		return s.failCapture(context.WithoutCancel(turn), capture, e)
	}
	return response, nil
}
func captureProjection(c domain.SourceCapture) map[string]any {
	// Private derived ownership is not a new public author request field.
	var publicSelection map[string]json.RawMessage
	_ = json.Unmarshal(c.Selection, &publicSelection)
	delete(publicSelection, "_retained_read_ids")
	out := map[string]any{"capture_id": c.ID, "project_id": c.ProjectID, "state": c.State, "requested_selection": json.RawMessage(encoded(publicSelection)), "captured_at": nil, "target_set_id": nil, "complete": c.State == "completed", "reason": c.Reason, "reads_url": "/api/v1/source-captures/" + c.ID + "/reads"}
	if c.State == "completed" {
		out["captured_at"] = c.CapturedAt
		out["target_set_id"] = c.TargetSetID
	}
	return out
}
func (s *Service) failCapture(ctx context.Context, c domain.SourceCapture, cause error) (domain.Response, error) {
	status, code, reason := 503, "SERVICE_RECOVERING", "stage_failure"
	if errors.Is(cause, context.Canceled) {
		reason = "interrupted_before_commit"
	}
	if errors.Is(cause, context.DeadlineExceeded) {
		reason = "check_bound_exceeded"
	}
	if errors.Is(cause, syscall.ENOSPC) || errors.Is(cause, syscall.EDQUOT) {
		code, reason = "RESOURCE_EXHAUSTED", "resource_exhausted"
	}
	if errors.Is(cause, domain.ErrPrecondition) {
		status, code, reason = 412, "PRECONDITION_FAILED", "config_changed"
	} else if actualStatus, actualCode := settleClass(cause); actualCode != "" {
		status, code, reason = actualStatus, actualCode, settleReason(actualCode)
	}
	response := errorEnvelope(status, code, reason)
	cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	defer cancel()
	e := s.Store.Write(cleanup, func(tx app.WriteTx) error {
		current, e := tx.SourceCapture(c.ID)
		if e != nil {
			return e
		}
		if current.State != "capturing" {
			receipt, e := tx.Receipt(domain.OperationKey{PrincipalID: c.PrincipalID, OperationID: c.OperationID})
			if e != nil {
				return e
			}
			if receipt.Response == nil {
				return domain.ErrReconciliation
			}
			response = *receipt.Response
			return nil
		}
		if e = tx.FinishSourceCapture(c.ID, "failed", reason); e != nil {
			return e
		}
		return tx.CompleteOperation(domain.OperationKey{PrincipalID: c.PrincipalID, OperationID: c.OperationID}, response)
	})
	if e != nil {
		return domain.Response{}, errors.Join(cause, e)
	}
	return response, nil
}
func (s *Service) readPinnedCapture(ctx context.Context, token, project string, mapping domain.ManagedSourceMapping, path CapturePath, consume func(io.Reader) error) error {
	if path.ReadID == "" {
		return ErrObjectMissing
	}
	// A retained read cannot acquire another project's provider policy merely
	// because both projects use the same logical source ID and relative path.
	if e := s.Store.Write(ctx, func(tx app.WriteTx) error {
		row, e := tx.SourceCaptureRead(path.ReadID)
		if e != nil {
			return e
		}
		capture, e := s.captureAccess(ctx, tx, token, row.CaptureID, false)
		if e != nil {
			return e
		}
		if capture.ProjectID != project {
			return domain.ErrDenied
		}
		if row.SourceID != mapping.SourceID || string(row.LocationJSON) != string(encoded(map[string]any{"path": path.Path})) {
			return domain.ErrDenied
		}
		return nil
	}); e != nil {
		return e
	}
	return s.ReadCaptured(ctx, token, path.ReadID, func(row domain.SourceCaptureRead, reader io.ReadSeeker) error {
		if row.SourceID != mapping.SourceID || string(row.LocationJSON) != string(encoded(map[string]any{"path": path.Path})) {
			return domain.ErrDenied
		}
		return consume(reader)
	})
}

// A retained selected snapshot is read under the accepted job's current
// authority. Reference-only/missing bytes are unavailable; there is no fallback
// to a different path, project or current working copy for a pinned snapshot.
func (s *Service) readPinnedAccepted(ctx context.Context, project string, mapping domain.ManagedSourceMapping, path CapturePath, guard func(app.WriteTx) error, consume func(io.Reader) error) error {
	var object domain.Object
	e := s.Store.Write(ctx, func(tx app.WriteTx) error {
		if e := guard(tx); e != nil {
			return e
		}
		row, e := tx.SourceCaptureRead(path.ReadID)
		if e != nil {
			return e
		}
		capture, e := tx.SourceCapture(row.CaptureID)
		if e != nil {
			return e
		}
		if capture.ProjectID != project || capture.State != "completed" || row.SourceID != mapping.SourceID || !bytes.Equal(row.LocationJSON, encoded(map[string]any{"path": path.Path})) {
			return domain.ErrDenied
		}
		if row.RetentionMode != "selected_snapshot" || row.Availability != "available" || row.Hash == "" {
			return ErrObjectMissing
		}
		object, e = tx.Object(row.Hash)
		return e
	})
	if e != nil {
		return e
	}
	return s.Bytes.Read(ctx, object, domain.Pin{Kind: "source_capture_read", OwnerID: path.ReadID}, func(r io.ReadSeeker) error {
		return consume(checkedSourceReader{ReadSeeker: r, check: func() error { return s.Store.Write(ctx, guard) }})
	})
}
