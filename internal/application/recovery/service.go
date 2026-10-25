// Package recovery owns host-local copy requests over the existing job/receipt
// foundation. The storage adapter owns snapshot and filesystem operations.
package recovery

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"haeryong.local/haeryong/internal/security/session"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync/atomic"
	"time"

	"haeryong.local/haeryong/internal/application/auth"
	"haeryong.local/haeryong/internal/application/service"
	app "haeryong.local/haeryong/internal/application/storage"
	"haeryong.local/haeryong/internal/domain"
)

type Bytes interface {
	CopyDestination(string) (string, error)
	AbandonCopy(domain.CopySet) error
	ProcessCopy(context.Context, string) error
	BackupStatus(context.Context) (domain.BackupStatus, error)
}
type Service struct {
	Store  app.Store
	Auth   *auth.Service
	Bytes  Bytes
	active atomic.Pointer[copyPhase]
}

func New(s app.Store, a *auth.Service, b Bytes) *Service {
	return &Service{Store: s, Auth: a, Bytes: b}
}

var copyID = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

var ErrRecordUnavailable = errors.New("FEATURE_NOT_READY")

func Validate(r *domain.CopyRequest) error {
	for _, id := range []string{r.ID, r.VersionID, r.DocumentID, r.ProjectID, r.Runtime} {
		if id != "" && !copyID.MatchString(id) {
			return domain.ErrInvalidTarget
		}
	}
	if r.Action != "export.plan.show" && r.Cursor != "" {
		return domain.ErrInvalidTarget
	}
	if r.Action != "backup.abandon" && r.Confirmed {
		return domain.ErrInvalidTarget
	}
	sort.Strings(r.Records)
	for i := 1; i < len(r.Records); i++ {
		if r.Records[i] == r.Records[i-1] {
			return domain.ErrInvalidTarget
		}
	}
	if r.Action == "export.create" && r.ID == "" {
		r.Action = "export.plan"
		e := Validate(r)
		r.Action = "export.create"
		return e
	}
	switch r.Action {
	case "backup.create":
		if r.Destination == "" || r.ID != "" || r.Format != "" || r.VersionID != "" || r.DocumentID != "" || r.ProjectID != "" || len(r.Records) > 0 || r.RetainedSources || r.MetadataDestination != "" || r.Versions != "" || r.Lifecycle != "" {
			return domain.ErrInvalidTarget
		}
	case "export.plan":
		n := 0
		for _, id := range []string{r.VersionID, r.DocumentID, r.ProjectID} {
			if id != "" {
				n++
			}
		}
		if n != 1 || r.Destination == "" || r.ID != "" || (r.Format != "directory" && r.Format != "zip") {
			return domain.ErrInvalidTarget
		}
		if r.VersionID != "" && r.Versions != "" {
			return domain.ErrInvalidTarget
		}
		if r.Versions == "" && r.VersionID == "" {
			r.Versions = "latest"
		}
		if r.VersionID == "" && r.Versions != "latest" && r.Versions != "all" {
			return domain.ErrInvalidTarget
		}
		if r.Lifecycle == "" {
			r.Lifecycle = "active"
		}
		switch r.Lifecycle {
		case "active", "archived", "trash", "all":
		default:
			return domain.ErrInvalidTarget
		}
		for _, kind := range r.Records {
			switch kind {
			case "review", "revisit", "reader", "notes", "references":
			case "decisions":
				return ErrRecordUnavailable
			default:
				return domain.ErrInvalidTarget
			}
		}
	case "backup.resume", "backup.abandon", "export.create", "export.plan.show":
		if r.ID == "" || r.Destination != "" || r.VersionID != "" || r.DocumentID != "" || r.ProjectID != "" || r.Format != "" || len(r.Records) > 0 || r.RetainedSources || r.MetadataDestination != "" || r.Versions != "" || r.Lifecycle != "" {
			return domain.ErrInvalidTarget
		}
		if r.Action == "backup.abandon" && !r.Confirmed {
			return domain.ErrInvalidTarget
		}
	default:
		return domain.ErrInvalidTarget
	}
	return nil
}

func (s *Service) Submit(ctx context.Context, token, operation string, r domain.CopyRequest) (domain.Response, error) {
	if r.Action != "export.plan.show" && !copyID.MatchString(operation) {
		return domain.Response{}, domain.ErrInvalidTarget
	}
	if e := Validate(&r); e != nil {
		return domain.Response{}, e
	}
	record, e := s.Auth.Authenticate(ctx, token)
	if e != nil {
		return domain.Response{}, e
	}
	if record.Scope.Kind != "owner" {
		return domain.Response{}, domain.ErrDenied
	}
	if r.Runtime != "" {
		health, e := s.Store.Health(ctx)
		if e != nil {
			return domain.Response{}, e
		}
		if health.Library.RuntimeGeneration != r.Runtime {
			return domain.Response{}, domain.ErrReconciliation
		}
	}
	if r.Destination != "" {
		r.Destination, e = filepath.Abs(r.Destination)
		if e != nil {
			return domain.Response{}, e
		}
	}
	if r.MetadataDestination != "" {
		r.MetadataDestination, e = filepath.Abs(r.MetadataDestination)
		if e != nil {
			return domain.Response{}, e
		}
	}
	if r.Action == "export.plan.show" {
		var v domain.CopyPlanPage
		e = s.Store.Write(ctx, func(tx app.WriteTx) error {
			a, e := s.Auth.AuthenticateWrite(ctx, tx, token)
			if e != nil {
				return e
			}
			if a.Scope.Kind != "owner" {
				return domain.ErrDenied
			}
			set, e := tx.CopySet(r.ID)
			if e != nil {
				return e
			}
			if set.OwnerID != a.PrincipalID {
				return domain.ErrDenied
			}
			v = domain.CopyPlanPage{ID: set.ID, PlanID: set.ID, State: set.Stage, Digest: set.PlanDigest, Items: []domain.CopySelection{}}
			members, e := tx.CopyVersions(set.ID, r.Cursor, 64)
			for _, m := range members {
				v.Items = append(v.Items, domain.CopySelection{DocumentID: m.DocumentID, VersionID: m.VersionID, PayloadSHA: m.PayloadSHA, ManifestSHA: m.ManifestSHA, MetadataSHA: m.MetadataSHA})
			}
			if len(v.Items) == 64 {
				last := v.Items[len(v.Items)-1].VersionID
				v.NextCursor = &last
			}
			return e
		})
		raw, _ := json.Marshal(v)
		return domain.Response{Status: 200, Body: raw}, e
	}
	// Reuse the original lexical binding when another spelling resolves to
	// the same physical destination. Exact replay still works if it is unmounted.
	existing, lookup := s.Store.Receipt(ctx, domain.OperationKey{PrincipalID: record.PrincipalID, OperationID: operation})
	if lookup == nil {
		var original, bound domain.CopyRequest
		if json.Unmarshal(existing.Original.Body, &original) != nil || json.Unmarshal(existing.Normalization, &bound) != nil {
			return domain.Response{}, domain.ErrConflict
		}
		equivalent := func(incoming, captured, physical string) string {
			if incoming == captured {
				return incoming
			}
			if incoming != "" {
				resolved, e := s.Bytes.CopyDestination(incoming)
				if e == nil && resolved == physical {
					return captured
				}
			}
			return incoming
		}
		r.Destination = equivalent(r.Destination, original.Destination, bound.Destination)
		r.MetadataDestination = equivalent(r.MetadataDestination, original.MetadataDestination, bound.MetadataDestination)
	} else if !errors.Is(lookup, domain.ErrNotFound) {
		return domain.Response{}, lookup
	}
	body, _ := json.Marshal(r)
	req := domain.Request{Key: domain.OperationKey{PrincipalID: record.PrincipalID, OperationID: operation}, Method: "POST", Target: "/local/v1/copy-requests", Body: body}
	response, executeErr := app.ExecuteNormalized(ctx, s.Store, s.Auth.Authority(token), req, func(tx app.WriteTx) ([]byte, error) {
		bound := r
		if bound.Destination != "" {
			value, e := s.Bytes.CopyDestination(bound.Destination)
			if e != nil {
				return nil, e
			}
			bound.Destination = value
		}
		if bound.MetadataDestination != "" {
			value, e := s.Bytes.CopyDestination(bound.MetadataDestination)
			if e != nil {
				return nil, e
			}
			bound.MetadataDestination = value
		}
		if bound.MetadataDestination != "" && (bound.MetadataDestination == bound.Destination || strings.HasPrefix(bound.MetadataDestination, bound.Destination+string(filepath.Separator)) || strings.HasPrefix(bound.Destination, bound.MetadataDestination+string(filepath.Separator))) {
			return nil, domain.ErrInvalidTarget
		}
		return json.Marshal(bound)
	}, func(tx app.WriteTx, receipt domain.Receipt) (domain.Response, error) {
		var bound domain.CopyRequest
		if e := json.Unmarshal(receipt.Normalization, &bound); e != nil {
			return domain.Response{}, e
		}
		r = bound
		lib, e := tx.Library()
		if e != nil {
			return domain.Response{}, e
		}
		set := domain.CopySet{}
		if r.ID != "" {
			set, e = tx.CopySet(r.ID)
			if e != nil {
				return domain.Response{}, e
			}
			if phase := s.active.Load(); phase != nil && phase.set.ID == set.ID {
				return domain.Response{}, domain.ErrTransition
			}
			restoredAbandon := r.Action == "backup.abandon" && (set.Kind == "backup" || set.Kind == "export") && set.Stage == "restored_paused"
			if set.OwnerID != record.PrincipalID || set.LibraryID != lib.ID || set.Runtime != lib.RuntimeGeneration && !restoredAbandon {
				return domain.Response{}, domain.ErrReconciliation
			}
		}
		switch r.Action {
		case "backup.create", "export.plan", "export.create":
			if r.Action == "export.create" && r.ID != "" {
				if set.Kind != "export" || (set.Stage != "planned" && set.Stage != "failed" && set.Stage != "interrupted") {
					return domain.Response{}, domain.ErrTransition
				}
				if set.Stage != "planned" {
					set.Error = ""
					set.CompletedAt = ""
					if set.Capturing || set.Snapshot == "" {
						set.Stage = "queued"
					} else if set.PublicationReady {
						set.Stage = "publishing"
					} else {
						set.Stage = "copying"
					}
				}
				break
			}
			id, e := service.NewID()
			if e != nil {
				return domain.Response{}, e
			}
			set = domain.CopySet{ID: id, OwnerID: record.PrincipalID, LibraryID: lib.ID, Runtime: lib.RuntimeGeneration, Destination: r.Destination, Request: r, CreatedAt: time.Now().UTC().Format(time.RFC3339Nano), Stage: "queued", Kind: "backup"}
			if strings.HasPrefix(r.Action, "export.") {
				set.Kind = "export"
				set.Stage = "planned"
			}
			if e = tx.PutCopySet(set); e != nil {
				return domain.Response{}, e
			}
			if set.Kind == "export" {
				project := r.ProjectID
				if r.VersionID != "" {
					v, e := tx.Version(r.VersionID)
					if e != nil {
						return domain.Response{}, e
					}
					d, e := tx.Document(v.DocumentID)
					if e != nil {
						return domain.Response{}, e
					}
					project = d.ProjectID
				} else if r.DocumentID != "" {
					d, e := tx.Document(r.DocumentID)
					if e != nil {
						return domain.Response{}, e
					}
					project = d.ProjectID
				}
				scope, e := tx.Scope(project)
				if e != nil {
					return domain.Response{}, e
				}
				set.Permissions = map[string]int64{project: scope.Project.PermissionsRevision}
				if e = tx.CaptureCopyVersions(set.ID, r); e != nil {
					return domain.Response{}, e
				}
				sum := sha256.Sum256(append(body, []byte(set.ID)...))
				set.PlanDigest = hex.EncodeToString(sum[:])
				if e = tx.PutCopySet(set); e != nil {
					return domain.Response{}, e
				}
				if r.Action == "export.plan" {
					raw, _ := json.Marshal(domain.CopyResult{OperationID: operation, ID: set.ID, PlanID: set.ID, State: set.Stage, Digest: set.PlanDigest})
					return domain.Response{Status: 201, Body: raw}, nil
				}
			}
		case "backup.resume":
			if set.Kind != "backup" || (set.Stage != "failed" && set.Stage != "interrupted") {
				return domain.Response{}, domain.ErrTransition
			}
			set.Error = ""
			set.CompletedAt = ""
			if set.Snapshot != "" && !set.Capturing {
				set.Stage = "copying"
			} else {
				set.Stage = "queued"
			}
		case "backup.abandon":
			if set.Kind != "backup" && set.Kind != "export" {
				return domain.Response{}, domain.ErrInvalidTarget
			}
			if set.Stage == "restored_paused" {
				hold, e := tx.RecoveryHold()
				if e != nil {
					return domain.Response{}, e
				}
				if hold == nil || hold.State != "reviewed" {
					return domain.Response{}, domain.ErrReconciliation
				}
				j, e := tx.CancelJob(set.JobID)
				if e != nil {
					return domain.Response{}, e
				}
				if j.State != "cancelled" {
					if e = tx.ConfirmStop(j.ID, j.AttemptGeneration); e != nil {
						return domain.Response{}, e
					}
				}
				set.Directory = ""
				set.Snapshot = ""
				set.Runtime = lib.RuntimeGeneration
			}
			if set.Stage == "complete" || set.Stage == "planned" {
				return domain.Response{}, domain.ErrTransition
			}
			if set.JobID != "" {
				job, e := tx.Job(set.JobID)
				if e != nil {
					return domain.Response{}, e
				}
				if job.State != "cancelled" && job.State != "failed" {
					return domain.Response{}, domain.ErrTransition
				}
			}
			set.Stage = "abandoned"
			set.Capturing = false
			if e = tx.ReleaseOwnerPins("backup", set.ID); e != nil {
				return domain.Response{}, e
			}
			if e = tx.PutCopySet(set); e != nil {
				return domain.Response{}, e
			}
			raw, _ := json.Marshal(map[string]string{"operation_id": operation, "set_id": set.ID, "kind": set.Kind, "state": "abandoned"})
			return domain.Response{Status: 200, Body: raw}, nil
		}
		id, e := service.NewID()
		if e != nil {
			return domain.Response{}, e
		}
		scope, _ := json.Marshal(map[string]string{"principal_id": set.OwnerID, "copy_set_id": set.ID})
		if e = tx.CreateJob(domain.Job{ID: id, Kind: set.Kind, Scope: scope, Permissions: set.Permissions, ParentID: set.JobID}); e != nil {
			return domain.Response{}, e
		}
		set.JobID = id
		set.AttemptAt = time.Now().UTC().Format(time.RFC3339Nano)
		if set.Stage == "planned" {
			set.Stage = "queued"
		}
		if e = tx.PutCopySet(set); e != nil {
			return domain.Response{}, e
		}
		raw, _ := json.Marshal(domain.CopyResult{OperationID: operation, ID: set.ID, JobID: id, State: "queued"})
		return domain.Response{Status: 202, Body: raw}, nil
	})
	if executeErr == nil && r.Action == "backup.abandon" {
		var set domain.CopySet
		executeErr = s.Store.Write(ctx, func(tx app.WriteTx) error { var e error; set, e = tx.CopySet(r.ID); return e })
		if executeErr == nil && set.Stage == "abandoned" {
			executeErr = s.Bytes.AbandonCopy(set)
		}
	}
	return response, executeErr

}

func ErrorCode(e error) string {
	switch {
	case errors.Is(e, ErrRecordUnavailable):
		return "FEATURE_NOT_READY"
	case errors.Is(e, domain.ErrInvalidTarget):
		return "INVALID_REQUEST"
	case errors.Is(e, domain.ErrDenied):
		return "FORBIDDEN"
	case errors.Is(e, domain.ErrConflict):
		return "OPERATION_CONFLICT"
	case errors.Is(e, domain.ErrReconciliation):
		return "OPERATION_RECONCILIATION_REQUIRED"
	case errors.Is(e, domain.ErrTransition):
		return "INVALID_TRANSITION"
	case errors.Is(e, domain.ErrNotFound):
		return "NOT_FOUND"
	}
	for _, known := range []error{session.ErrRequired, session.ErrRevoked, session.ErrExpired, session.ErrInvalid, session.ErrOrigin, session.ErrMissing, session.ErrLimited} {
		if errors.Is(e, known) {
			return known.Error()
		}
	}
	return "SERVICE_RECOVERING"
}
