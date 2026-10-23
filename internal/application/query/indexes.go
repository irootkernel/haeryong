package query

import (
	"context"
	"encoding/json"
	"errors"
	"haeryong.local/haeryong/internal/application/references"
	"haeryong.local/haeryong/internal/application/source"
	app "haeryong.local/haeryong/internal/application/storage"
	"haeryong.local/haeryong/internal/domain"
	"io"
	"reflect"
	"runtime"
	"strconv"
)

type IndexValidationCounts struct {
	Checked    string `json:"checked"`
	Missing    string `json:"missing"`
	Unexpected string `json:"unexpected"`
	Mismatched string `json:"mismatched"`
}
type IndexValidation struct {
	VersionID string                `json:"version_id"`
	Kind      string                `json:"kind"`
	State     string                `json:"state"`
	Counts    IndexValidationCounts `json:"counts"`
	Reasons   []string              `json:"reasons"`
}

func (s *Service) indexRead(ctx context.Context, token, version string, fn func(app.WriteTx, source.Eligibility) error) error {
	return s.referenceRead(ctx, token, version, func(tx app.WriteTx, record domain.Session, e source.Eligibility) error {
		if record.Scope.Kind != "owner" {
			return domain.ErrDenied
		}
		return fn(tx, e)
	})
}
func (s *Service) RebuildVersionIndexes(ctx context.Context, token, version, key, kind string) (domain.Response, error) {
	if !oneOf(kind, "search", "references", "all") {
		return domain.Response{}, &ReferenceInvalid{"/kind", "invalid_index_kind"}
	}
	record, err := s.Auth.Authenticate(ctx, token)
	if err != nil {
		return domain.Response{}, err
	}
	if record.Scope.Kind != "owner" {
		return domain.Response{}, domain.ErrDenied
	}
	body, _ := json.Marshal(map[string]string{"kind": kind})
	request := domain.Request{Key: domain.OperationKey{PrincipalID: record.PrincipalID, OperationID: key}, Method: "POST", Target: "/api/v1/versions/" + version + "/indexes/rebuild", Body: body}
	return app.Execute(ctx, s.Store, s.Auth.Authority(token), request, nil, func(tx app.WriteTx, _ domain.Receipt) (domain.Response, error) {
		current, err := s.Auth.AuthenticateWrite(ctx, tx, token)
		if err != nil {
			return domain.Response{}, err
		}
		if current.Scope.Kind != "owner" {
			return domain.Response{}, domain.ErrDenied
		}
		if _, err = source.Check(tx, current, version); err != nil {
			return domain.Response{}, err
		}
		ids, err := tx.RebuildIndexes(version, kind)
		if err != nil {
			return domain.Response{}, err
		}
		raw, _ := json.Marshal(map[string]any{"version_id": version, "kind": kind, "job_ids": ids, "operation_id": key})
		return domain.Response{Status: 202, Body: raw}, nil
	})
}
func (s *Service) ValidateVersionIndexes(ctx context.Context, token, version, kind string) (out IndexValidation, err error) {
	ctx, cancel := context.WithTimeout(ctx, ReferenceTimeout)
	defer cancel()
	out = IndexValidation{VersionID: version, Kind: kind, State: "consistent", Reasons: []string{}}
	if !oneOf(kind, "search", "references") {
		return out, &ReferenceInvalid{"/kind", "invalid_index_kind"}
	}
	var v domain.Version
	var definition string
	err = s.indexRead(ctx, token, version, func(tx app.WriteTx, e source.Eligibility) error {
		v = e.Version
		var err error
		definition, err = tx.ReferenceIndexDefinition()
		return err
	})
	if err != nil {
		return out, referenceError(ctx, err)
	}
	var checked, missing, unexpected, mismatched int64
	incomplete := false
	finish := func() (IndexValidation, error) {
		out.Counts = IndexValidationCounts{strconv.FormatInt(checked, 10), strconv.FormatInt(missing, 10), strconv.FormatInt(unexpected, 10), strconv.FormatInt(mismatched, 10)}
		if incomplete {
			out.State = "incomplete"
		}
		if missing+unexpected+mismatched > 0 || definition != "" {
			out.State = "inconsistent"
		}
		return out, referenceError(ctx, ctx.Err())
	}
	if definition != "" {
		out.Reasons = append(out.Reasons, definition)
		return finish()
	}
	if kind == "search" {
		var index domain.SearchIndex
		err = s.indexRead(ctx, token, version, func(tx app.WriteTx, _ source.Eligibility) error {
			var err error
			index, err = tx.SearchIndex(version)
			return err
		})
		if errors.Is(err, domain.ErrNotFound) {
			missing++
			out.Reasons = append(out.Reasons, "search_index_missing")
			return finish()
		}
		if err != nil {
			return out, referenceError(ctx, err)
		}
		var ownership domain.DerivedWork
		var job domain.Job
		err = s.indexRead(ctx, token, version, func(tx app.WriteTx, _ source.Eligibility) error {
			var e error
			ownership, e = tx.DerivedWork(index.JobID)
			if e != nil {
				return e
			}
			job, e = tx.Job(index.JobID)
			return e
		})
		if errors.Is(err, domain.ErrNotFound) {
			mismatched++
			out.Reasons = append(out.Reasons, "search_owner_missing")
		} else if err != nil {
			return out, referenceError(ctx, err)
		} else {
			if ownership.VersionID != version || ownership.WorkKind != "search" || ownership.ExtractorRevision != index.ExtractorRevision || job.Kind != "derived_index" {
				mismatched++
			}
			if job.State != "completed" {
				incomplete = true
			}
		}
		checked++
		if index.ExtractorRevision != domain.SearchExtractorRevision || !index.DiscoveryComplete || index.State != "complete" {
			incomplete = true
			out.Reasons = append(out.Reasons, "search_index_incomplete")
		}
		if incomplete {
			return finish()
		}
		audit, auditErr := s.auditSearchIndex(ctx, token, v)
		if auditErr != nil {
			return out, referenceError(ctx, auditErr)
		}
		checked += audit.checked
		missing += audit.missing
		unexpected += audit.unexpected
		mismatched += audit.mismatched
		incomplete = audit.incomplete

		err = s.indexRead(ctx, token, version, func(tx app.WriteTx, _ source.Eligibility) error {
			current, e := tx.SearchIndex(version)
			if e != nil {
				return e
			}
			if current != index {
				incomplete = true
				out.Reasons = append(out.Reasons, "validation_changed")
			}
			return nil
		})
		if err != nil {
			return out, referenceError(ctx, err)
		}
		if missing+unexpected+mismatched > 0 {
			out.Reasons = append(out.Reasons, "search_index_divergence")
		}
		return finish()
	}
	var m domain.ReferenceMaterialization
	err = s.indexRead(ctx, token, version, func(tx app.WriteTx, _ source.Eligibility) error {
		var err error
		m, err = tx.ReferenceMaterialization(version)
		return err
	})
	if errors.Is(err, domain.ErrNotFound) {
		incomplete = true
		out.Reasons = append(out.Reasons, "materialization_pending")
	} else if err != nil {
		return out, referenceError(ctx, err)
	} else if referenceState(m, v).State != "ready" {
		incomplete = true
		out.Reasons = append(out.Reasons, "materialization_incomplete")
	}
	var object domain.Object
	err = s.indexRead(ctx, token, version, func(tx app.WriteTx, _ source.Eligibility) error {
		var err error
		object, err = tx.Object(v.ManifestSHA)
		return err
	})
	if err != nil {
		return out, referenceError(ctx, err)
	}
	expected := map[string]bool{}
	err = s.Bytes.Read(ctx, object, domain.Pin{Kind: "active_read", OwnerID: version}, func(reader io.ReadSeeker) error {
		return references.WalkDeclarations(ctx, reader, version, func(prepared app.PreparedReference) error {
			r := prepared.Columns()
			expected[r.ID] = true
			return s.indexRead(ctx, token, version, func(tx app.WriteTx, _ source.Eligibility) error {
				actual, err := tx.AuthoredReference(version, r.ID)
				checked++
				if errors.Is(err, domain.ErrNotFound) {
					missing++
					return nil
				}
				if err != nil {
					return err
				}
				raw := string(actual.AuthoredJSON)
				actual.AuthoredJSON = nil
				if !reflect.DeepEqual(actual, r) || raw != prepared.JSON() {
					mismatched++
				}
				return nil
			})
		})
	})
	if err != nil {
		return out, referenceError(ctx, err)
	}
	after := ""
	for {
		var rows []domain.AuthoredReference
		err = s.indexRead(ctx, token, version, func(tx app.WriteTx, _ source.Eligibility) error {
			var e error
			rows, e = tx.AuthoredReferences(version, after, 64)
			return e
		})
		if err != nil {
			return out, referenceError(ctx, err)
		}
		if len(rows) == 0 {
			break
		}
		for _, r := range rows {
			after = r.ID
			if !expected[r.ID] {
				unexpected++
				continue
			}
			p, e := s.projectReference(ctx, token, r, ReferenceOptions{})
			if e != nil {
				return out, referenceError(ctx, e)
			}
			if p.view.Resolution == "unresolved" && p.view.Reason != nil {
				if *p.view.Reason == "anchor_pending" {
					incomplete = true
					if !oneOf("anchor_prerequisite_pending", out.Reasons...) {
						out.Reasons = append(out.Reasons, "anchor_prerequisite_pending")
					}
				}
				if *p.view.Reason == "resolution_pending" {
					missing++
				}
				if *p.view.Reason == "anchor_drift" && p.edge != nil {
					mismatched++
				}
			}
		}
		runtime.Gosched()
	}
	// Canonical table scan is physically bounded BEFORE selecting this target.
	var rowID int64
	for {
		var keys []domain.EdgeKey
		err = s.indexRead(ctx, token, version, func(tx app.WriteTx, _ source.Eligibility) error {
			var e error
			keys, e = tx.CanonicalEdgeKeys(rowID, 64)
			return e
		})
		if err != nil {
			return out, referenceError(ctx, err)
		}
		if len(keys) == 0 {
			break
		}
		for _, k := range keys {
			rowID = k.RowID
			if k.TargetVersion != version {
				continue
			}
			_, document, e := s.projectedEdge(ctx, token, version, k)
			if e != nil {
				return out, referenceError(ctx, e)
			}
			if document == "" {
				continue
			}
			checked++
			var found bool
			err = s.indexRead(ctx, token, version, func(tx app.WriteTx, _ source.Eligibility) error {
				var e error
				found, e = tx.HasIndexedEdge(k)
				return e
			})
			if err != nil {
				return out, referenceError(ctx, err)
			}
			if !found {
				missing++
			}
		}
		runtime.Gosched()
	}
	var position *domain.EdgeKey
	for {
		var keys []domain.EdgeKey
		err = s.indexRead(ctx, token, version, func(tx app.WriteTx, _ source.Eligibility) error {
			var e error
			keys, e = tx.IncomingEdgeKeys(version, nil, nil, position, 64)
			return e
		})
		if err != nil {
			return out, referenceError(ctx, err)
		}
		if len(keys) == 0 {
			break
		}
		for _, k := range keys {
			position = &k
			_, document, e := s.projectedEdge(ctx, token, version, k)
			if errors.Is(e, domain.ErrNotFound) || errors.Is(e, domain.ErrContentIntegrity) {
				mismatched++
				continue
			}
			if e != nil {
				return out, referenceError(ctx, e)
			}
			if document != "" {
				checked++
			}
		}
		runtime.Gosched()
	}
	err = s.indexRead(ctx, token, version, func(tx app.WriteTx, _ source.Eligibility) error {
		current, e := tx.ReferenceMaterialization(version)
		if errors.Is(e, domain.ErrNotFound) && m.JobID == "" {
			return nil
		}
		if e != nil {
			return e
		}
		if current != m {
			incomplete = true
			out.Reasons = append(out.Reasons, "validation_changed")
		}
		return nil
	})
	if err != nil {
		return out, referenceError(ctx, err)
	}
	if missing+unexpected+mismatched > 0 {
		out.Reasons = append(out.Reasons, "reference_index_divergence")
	}
	return finish()
}
