package source

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"haeryong.local/haeryong/internal/application/service"
	"io"
	"sort"

	app "haeryong.local/haeryong/internal/application/storage"
	"haeryong.local/haeryong/internal/domain"
	sec "haeryong.local/haeryong/internal/security/session"
)

func (s *Service) captureAccess(ctx context.Context, tx app.WriteTx, token, id string, metadata bool) (domain.SourceCapture, error) {
	actor, e := s.Auth.AuthenticateWrite(ctx, tx, token)
	if e != nil {
		return domain.SourceCapture{}, e
	}
	c, e := tx.SourceCapture(id)
	if e != nil {
		return c, e
	}
	if e = app.CheckCaptureActor(tx, actor, c); e != nil {
		return c, e
	}
	if metadata {
		for after := ""; ; {
			reads, err := tx.SourceCaptureReads(c.ID, after, 64)
			if err != nil {
				return c, err
			}
			if len(reads) == 0 {
				break
			}
			for _, read := range reads {
				parent, err := app.SourceReadParent(c, read)
				if err != nil {
					return c, err
				}
				if parent != "" {
					if err = app.CheckSourceReadContext(tx, actor, parent, c.ProjectID); err != nil {
						return c, err
					}
				}
				after = read.ID
			}
		}

	}
	var input CaptureRequest
	if e = json.Unmarshal(c.Selection, &input); e != nil {
		return c, e
	}
	// A scoped reader cannot use a known history id as a source-existence oracle
	// after a selected source was revoked. Owner history remains inspectable.
	if actor.Scope.Kind != "owner" {
		for _, sel := range input.Selections {
			if _, e = app.ManagedSourcePolicy(tx, c.ProjectID, sel.SourceID, ""); e != nil {
				return c, domain.ErrNotFound
			}
		}
	}
	return c, nil
}
func (s *Service) CaptureView(ctx context.Context, token, id, after string) (map[string]any, error) {
	var out map[string]any
	e := s.Store.Write(ctx, func(tx app.WriteTx) error {
		c, e := s.captureAccess(ctx, tx, token, id, true)
		if e != nil {
			return e
		}
		rows, e := tx.SourceCaptureReads(id, after, 65)
		if e != nil {
			return e
		}
		next := ""
		if len(rows) > 64 {
			next = rows[63].ID
			rows = rows[:64]
		}
		for i := range rows {
			rows[i] = projectCaptureRead(tx, c, rows[i])
		}
		out = captureProjection(c)
		out["items"] = rows
		if next != "" {
			out["next_cursor"] = next
		} else {
			out["next_cursor"] = nil
		}
		return nil
	})
	return out, e
}

// Metadata projection performs no object/source read. Historical capture facts
// remain separate from the current principal's permitted retained-byte view.
func projectCaptureRead(tx app.WriteTx, c domain.SourceCapture, row domain.SourceCaptureRead) domain.SourceCaptureRead {
	row.CaptureAvailability, row.Reason = row.CaptureFacts()
	row.ReadState = "not_read_by_view"
	row.BytesOrigin = ""
	if _, e := app.ManagedSourcePolicy(tx, c.ProjectID, row.SourceID, ""); e != nil {
		row.Availability, row.Reason = "denied", "source_denied"
		row.Hash, row.SourceJSON, row.LocationJSON = "", nil, nil
		return row
	}
	if c.State != "completed" {
		if row.Availability == "available" {
			row.Availability = "unavailable"
		}
		if row.Reason == "" {
			row.Reason = "capture_not_completed"
		}
		return row
	}
	if row.RetentionMode == "reference_only" {
		row.Availability, row.Reason = "unavailable", "not_retained"
	} else if row.Availability == "available" {
		row.BytesOrigin = "retained_snapshot"
	}
	return row
}

// ReadCaptured serves an authorized retained snapshot or explicitly rereads
// and verifies the same exact approved source. A reference-only identity never
// grants incidental object retention or silently substitutes changed bytes.
// checkedSourceReader reuses current source/session authority before each
// caller read; a delivered chunk cannot be recalled after later revocation.
type checkedSourceReader struct {
	io.ReadSeeker
	check func() error
}

func (r checkedSourceReader) Read(p []byte) (int, error) {
	if e := r.check(); e != nil {
		return 0, e
	}
	return r.ReadSeeker.Read(p)
}
func (r checkedSourceReader) Seek(n int64, w int) (int64, error) {
	if e := r.check(); e != nil {
		return 0, e
	}
	return r.ReadSeeker.Seek(n, w)
}

func (s *Service) ReadCaptured(ctx context.Context, token, readID string, consume func(domain.SourceCaptureRead, io.ReadSeeker) error) (result error) {
	var row domain.SourceCaptureRead
	var object domain.Object
	var mapping domain.ManagedSourceMapping
	var permissions int64
	e := s.Store.Write(ctx, func(tx app.WriteTx) error {
		var e error
		row, e = tx.SourceCaptureRead(readID)
		if e != nil {
			return e
		}
		c, e := s.captureAccess(ctx, tx, token, row.CaptureID, false)
		if e != nil {
			return e
		}
		actor, e := s.Auth.AuthenticateWrite(ctx, tx, token)
		if e != nil {
			return e
		}
		if e = app.CheckSourceReadAuthority(tx, actor, row.ID, c.ProjectID, ""); e != nil {
			if errors.Is(e, domain.ErrDenied) || errors.Is(e, domain.ErrNotFound) {
				return domain.ErrNotFound
			}
			return e
		}
		mapping, e = app.ManagedSourcePolicy(tx, c.ProjectID, row.SourceID, "")
		if e != nil {
			if errors.Is(e, domain.ErrDenied) || errors.Is(e, domain.ErrNotFound) {
				return domain.ErrNotFound
			}
			return e
		}
		p, e := tx.Scope(c.ProjectID)
		if e != nil {
			return e
		}
		permissions = p.Project.PermissionsRevision
		if c.State != "completed" || row.Availability != "available" && !(row.RetentionMode == "reference_only" && row.Reason == "reference_only_uncopied") {
			return ErrObjectMissing
		}
		if row.RetentionMode == "selected_snapshot" {
			object, e = tx.Object(row.Hash)
			if e != nil {
				return e
			}
			if object.State != "ready" || object.Size != row.Size {
				return domain.ErrDeleting
			}
		}
		return nil
	})
	if e != nil {
		return e
	}
	check := func() error {
		return s.Store.Write(ctx, func(tx app.WriteTx) error {
			c, e := s.captureAccess(ctx, tx, token, row.CaptureID, false)
			if e != nil {
				return e
			}
			actor, e := s.Auth.AuthenticateWrite(ctx, tx, token)
			if e != nil {
				return e
			}
			if e = app.CheckSourceReadAuthority(tx, actor, row.ID, c.ProjectID, ""); e != nil {
				if errors.Is(e, domain.ErrDenied) || errors.Is(e, domain.ErrNotFound) {
					return domain.ErrNotFound
				}
				return e
			}
			current, e := app.ManagedSourcePolicy(tx, c.ProjectID, row.SourceID, "")
			if e != nil {
				return e
			}
			p, e := tx.Scope(c.ProjectID)
			if e != nil {
				return e
			}
			if p.Project.PermissionsRevision != permissions || current.RootPath != mapping.RootPath || current.Kind != mapping.Kind || current.RepoID != mapping.RepoID {
				return domain.ErrPrecondition
			}
			return nil
		})
	}
	if row.RetentionMode == "reference_only" {
		// Re-read the same exact target, verify its digest, then serve those newly
		// captured immutable bytes. Incidental existing objects grant no retention.
		var identity struct {
			Kind     string `json:"kind"`
			RepoID   string `json:"repo_id"`
			Revision string `json:"exact_revision"`
		}
		var location struct {
			Path string `json:"path"`
		}
		if json.Unmarshal(row.SourceJSON, &identity) != nil || json.Unmarshal(row.LocationJSON, &location) != nil {
			return domain.ErrInvalidTarget
		}
		if policy, ok := s.Files.(interface {
			CheckLocation(context.Context, string, string) error
		}); ok {
			if e = policy.CheckLocation(ctx, mapping.RootPath, location.Path); e != nil {
				return e
			}
		}
		var stream io.ReadCloser
		if identity.Kind == "git" {
			if mapping.Kind != "git" || mapping.RepoID != identity.RepoID {
				return domain.ErrDenied
			}
			if e = s.Reader.ResolveRevision(ctx, mapping.RootPath, identity.Revision); e == nil {
				stream, e = s.Reader.ReadBlob(ctx, mapping.RootPath, identity.Revision, location.Path)
			}
		} else if s.Files != nil {
			stream, e = s.Files.Open(ctx, mapping.RootPath, location.Path)
		} else {
			e = ErrInvalidRoot
		}
		if e != nil {
			return e
		}
		bytes, ok := s.Bytes.(CaptureBytes)
		if !ok {
			stream.Close()
			return ErrObjectMissing
		}
		pinID, e := service.NewID()
		if e != nil {
			stream.Close()
			return e
		}
		object, e = bytes.Put(ctx, stream, func(tx app.WriteTx, o domain.Object) error {
			if o.Hash != row.Hash || o.Size != row.Size {
				return ErrSourceChanged
			}
			return tx.AcquirePin(domain.Pin{ID: pinID, Kind: "source_capture_read", OwnerID: readID, Hash: o.Hash})
		})
		closeErr := stream.Close()
		if e != nil {
			return e
		}
		defer func() {
			result = errors.Join(result, s.Store.Write(context.Background(), func(tx app.WriteTx) error { return tx.ReleasePin(pinID) }))
		}()
		if closeErr != nil {
			return closeErr
		}
		row.BytesOrigin = "approved_source"
	} else {
		row.BytesOrigin = "retained_snapshot"
	}
	return s.Bytes.Read(ctx, object, domain.Pin{Kind: "source_capture_read", OwnerID: readID}, func(reader io.ReadSeeker) error {
		if e := check(); e != nil {
			return e
		}
		row.ReadState = "verified_read"
		return consume(row, checkedSourceReader{reader, check})
	})
}

// CompareCaptures compares only already captured exact identity/digests. It
// starts no Git/filesystem/provider operation and gives no semantic verdict.
func (s *Service) CompareCaptures(ctx context.Context, token, left, right, cursor string) (map[string]any, error) {
	var key [4]string
	if cursor != "" {
		raw, e := base64.RawURLEncoding.DecodeString(cursor)
		if e != nil || json.Unmarshal(raw, &key) != nil || key[0] != left || key[1] != right {
			return nil, domain.ErrInvalidTarget
		}
	}
	changes := []domain.SourceCaptureDelta{}
	var next any
	e := s.Store.Write(ctx, func(tx app.WriteTx) error {
		a, e := s.captureAccess(ctx, tx, token, left, true)
		if e != nil {
			return e
		}
		b, e := s.captureAccess(ctx, tx, token, right, true)
		if e != nil {
			return e
		}
		if a.ProjectID != b.ProjectID {
			return domain.ErrNotFound
		}
		changes, e = tx.SourceCaptureDiff(left, right, key[2], key[3], 65)
		if e != nil {
			return e
		}
		if len(changes) > 64 {
			last := changes[63]
			raw, _ := json.Marshal([4]string{left, right, last.SourceID, string(last.Location)})
			next = base64.RawURLEncoding.EncodeToString(raw)
			changes = changes[:64]
		}
		for i := range changes {
			d := &changes[i]
			if _, e = app.ManagedSourcePolicy(tx, a.ProjectID, d.SourceID, ""); e != nil {
				return domain.ErrNotFound
			}
			if d.Before != nil {
				row := projectCaptureRead(tx, a, *d.Before)
				d.Before = &row
			}
			if d.After != nil {
				row := projectCaptureRead(tx, b, *d.After)
				d.After = &row
			}
		}
		return nil
	})
	if e != nil {
		return nil, e
	}
	return map[string]any{"left_capture_id": left, "right_capture_id": right, "changes": changes, "next_cursor": next, "semantic_verdict": nil}, nil
}

// ValidateObservedRead rejects source-only captures and post-hoc checks as input
// evidence. It checks the producing attempt, exact target/read/hash and actual
// persisted first observation; it never creates observations itself.
func ValidateObservedRead(tx app.WriteTx, attemptID, readID string) error {
	a, e := tx.ProviderAttempt(attemptID)
	if e != nil {
		return e
	}
	r, e := tx.CapturedRead(readID)
	if e != nil {
		return e
	}
	if r.TargetSetID != a.TargetSetID {
		return domain.ErrDenied
	}
	inputs, e := tx.ProviderInputs(attemptID)
	if e != nil {
		return e
	}
	for _, input := range inputs {
		if input.ReadID == readID && input.Hash == r.BytesSHA && input.ObservedAt != nil && (input.ObservedOrigin == "daemon_supplied" || input.ObservedOrigin == "native_observed") {
			return nil
		}
	}
	return domain.ErrReconciliation
}

type retainedReadChoice struct {
	domain.SourceCaptureRead
	CapturedAt string `json:"captured_at"`
}

// RetainedReads exposes metadata for a normal pinned-files chooser, not a source
// read or a provider observation. The existing completed target-set inventory is
// the index; no new table, implicit capture, pin or current-file fallback exists.
func (s *Service) RetainedReads(ctx context.Context, token, project, sourceID, cursor string) (map[string]any, error) {
	if !uuidPattern.MatchString(project) || sourceID == "" {
		return nil, sec.ErrInvalid
	}
	var out map[string]any
	err := s.Store.Write(ctx, func(tx app.WriteTx) error {
		actor, e := s.Auth.AuthenticateWrite(ctx, tx, token)
		if e != nil {
			return e
		}
		if e = generationCapturePrincipal(tx, actor, project); e != nil {
			return e
		}
		if _, e = app.ManagedSourcePolicy(tx, project, sourceID, ""); e != nil {
			return e
		}
		library, e := tx.Library()
		if e != nil {
			return e
		}
		scope, e := tx.Scope(project)
		if e != nil {
			return e
		}
		configuration, e := tx.Configuration()
		if e != nil {
			return e
		}
		principalRevision := int64(0)
		if actor.Scope.Kind != "owner" {
			p, e := tx.WorkflowPrincipal(actor.PrincipalID)
			if e != nil {
				return e
			}
			principalRevision = p.Revision
		}
		binding := fmt.Sprintf("%x", sha256.Sum256(encoded([]any{library.ID, library.RuntimeGeneration, library.AuthEpoch, actor.PrincipalID, actor.Scope, principalRevision, project, scope.Project.PermissionsRevision, configuration.Revision, sourceID})))
		c := struct {
			Binding string `json:"binding"`
			After   string `json:"after,omitempty"`
		}{Binding: binding}
		if cursor != "" {
			raw, e := base64.RawURLEncoding.DecodeString(cursor)
			if e != nil {
				return sec.ErrInvalid
			}
			d := json.NewDecoder(bytes.NewReader(raw))
			d.DisallowUnknownFields()
			var extra any
			if d.Decode(&c) != nil || d.Decode(&extra) != io.EOF || c.Binding != binding || !uuidPattern.MatchString(c.After) {
				return sec.ErrInvalid
			}
			canonical, _ := json.Marshal(c)
			if base64.RawURLEncoding.EncodeToString(canonical) != cursor {
				return sec.ErrInvalid
			}
		}
		targets, e := tx.SourceCaptures(project)
		if e != nil {
			return e
		}
		seen := map[string]bool{}
		items := map[string]retainedReadChoice{}
		ids := []string{}
		for _, target := range targets {
			var saved struct {
				Sources []struct {
					SourceID string `json:"source_id"`
					ReadID   string `json:"read_id"`
				} `json:"sources"`
			}
			if e = json.Unmarshal(target.TargetsJSON, &saved); e != nil {
				return e
			}
			for _, entry := range saved.Sources {
				if entry.SourceID != sourceID || entry.ReadID == "" || entry.ReadID <= c.After || seen[entry.ReadID] {
					continue
				}
				seen[entry.ReadID] = true
				row, e := tx.SourceCaptureRead(entry.ReadID)
				if e != nil {
					return e
				}
				capture, e := s.captureAccess(ctx, tx, token, row.CaptureID, false)
				if e == nil {
					e = app.CheckSourceReadContext(tx, actor, row.ID, capture.ProjectID)
				}
				if errors.Is(e, domain.ErrNotFound) || errors.Is(e, domain.ErrDenied) {
					continue
				}
				if e != nil {
					return e
				}
				if capture.ProjectID != project || capture.TargetSetID != target.ID || capture.State != "completed" || row.SourceID != sourceID {
					return domain.ErrInvalidTarget
				}
				ids = append(ids, row.ID)
				items[row.ID] = retainedReadChoice{SourceCaptureRead: projectCaptureRead(tx, capture, row), CapturedAt: capture.CapturedAt}
			}
		}
		sort.Strings(ids)
		rows := []retainedReadChoice{}
		var next *string
		if len(ids) > 64 {
			c.After = ids[63]
			raw, _ := json.Marshal(c)
			value := base64.RawURLEncoding.EncodeToString(raw)
			next = &value
			ids = ids[:64]
		}
		for _, id := range ids {
			rows = append(rows, items[id])
		}
		out = map[string]any{"items": rows, "next_cursor": next}
		return nil
	})
	return out, err
}
