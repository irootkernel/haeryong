package query

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/url"
	"reflect"
	"runtime"
	"strings"
	"time"
	"unicode/utf8"

	"haeryong.local/haeryong/internal/application/references"
	"haeryong.local/haeryong/internal/application/source"
	app "haeryong.local/haeryong/internal/application/storage"
	"haeryong.local/haeryong/internal/domain"
)

const ReferenceTimeout = 30 * time.Second

type ReferenceInvalid struct{ Pointer, Reason string }

func (e *ReferenceInvalid) Error() string { return e.Pointer + ": " + e.Reason }

type ReferenceUnavailable struct{ Cause error }

func (e *ReferenceUnavailable) Error() string { return "references unavailable" }
func (e *ReferenceUnavailable) Unwrap() error { return e.Cause }

type GoneTarget struct {
	DocumentID string `json:"document_id"`
	VersionID  string `json:"version_id"`
}
type TargetGone struct{ Target GoneTarget }

func (e *TargetGone) Error() string { return "target gone" }

type ReferenceContext struct {
	PagePath *string `json:"page_path"`
	RegionID *string `json:"region_id"`
}
type ExactTarget struct {
	DocumentID    string  `json:"document_id"`
	VersionID     string  `json:"version_id"`
	PagePath      *string `json:"page_path"`
	RegionID      *string `json:"region_id"`
	RequestedPath *string `json:"requested_path"`
	ResolvedPath  *string `json:"resolved_path"`
	PageQuery     string  `json:"page_query"`
	PageFragment  string  `json:"page_fragment"`
}
type ReferenceView struct {
	ID         string           `json:"id"`
	From       ReferenceContext `json:"from"`
	Relation   *string          `json:"relation"`
	Label      *string          `json:"label"`
	Resolution string           `json:"resolution"`
	Reason     *string          `json:"reason"`
	EdgeID     *string          `json:"edge_id"`
	Target     any              `json:"target"`
	ReaderURL  *string          `json:"reader_url"`
}
type ReferenceMaterialization struct {
	State  string  `json:"state"`
	Reason *string `json:"reason"`
}
type ReferenceList struct {
	Items           []ReferenceView          `json:"items"`
	NextCursor      *string                  `json:"next_cursor"`
	Materialization ReferenceMaterialization `json:"materialization"`
}
type ReferenceOptions struct{ Page, Region *string }
type referenceCursor struct{ Binding, After string }
type referenceSnapshot struct {
	binding         string
	materialization ReferenceMaterialization
}

var errReferenceChanged = errors.New("reference snapshot changed")

// Every bounded transaction authenticates and checks the referring version.
// Neither a structural edge nor a previous read supplies authority.
func (s *Service) referenceRead(ctx context.Context, token, version string, fn func(app.WriteTx, domain.Session, source.Eligibility) error) error {
	return s.Store.Write(ctx, func(tx app.WriteTx) error {
		record, err := s.Auth.AuthenticateWrite(ctx, tx, token)
		if err != nil {
			return err
		}
		if !searchID.MatchString(version) {
			return &ReferenceInvalid{"/source_version", "invalid_uuid"}
		}
		eligible, err := source.Check(tx, record, version)
		if err != nil {
			return err
		}
		return fn(tx, record, eligible)
	})
}
func referenceState(m domain.ReferenceMaterialization, v domain.Version) ReferenceMaterialization {
	if m.ManifestSHA == v.ManifestSHA && m.ReferenceRevision == domain.ReferenceRevision {
		switch m.State {
		case "ready":
			return ReferenceMaterialization{State: "ready"}
		case "failed":
			return ReferenceMaterialization{State: "failed", Reason: nullable("materialization_failed")}
		}
	}
	return ReferenceMaterialization{State: "pending", Reason: nullable("materialization_pending")}
}
func referenceBinding(tx app.WriteTx, record domain.Session, eligible source.Eligibility, limit int) (referenceSnapshot, error) {
	l, err := tx.Library()
	if err != nil {
		return referenceSnapshot{}, err
	}
	m, err := tx.ReferenceMaterialization(eligible.Version.ID)
	if err != nil && !errors.Is(err, domain.ErrNotFound) {
		return referenceSnapshot{}, err
	}
	state := referenceState(m, eligible.Version)
	revision := int64(0)
	if state.State == "ready" {
		revision = m.Revision
	}
	principalRevision := int64(0)
	if record.Scope.Kind != "owner" {
		p, err := tx.WorkflowPrincipal(record.PrincipalID)
		if err != nil {
			return referenceSnapshot{}, err
		}
		principalRevision = p.Revision
	}
	scope, err := tx.Scope(eligible.ProjectID)
	if err != nil {
		return referenceSnapshot{}, err
	}
	// Deliberately source-only: no edge, target, job, index or outbox generation.
	b := validator([]any{l.ID, l.RuntimeGeneration, record.PrincipalID, record.Scope, principalRevision, scope.Project.PermissionsRevision, eligible.Version.ID, eligible.Version.ManifestSHA, domain.ReferenceRevision, state.State, revision, limit})
	return referenceSnapshot{b, state}, nil
}
func referenceError(ctx context.Context, err error) error {
	if ctx.Err() != nil {
		return &ReferenceUnavailable{ctx.Err()}
	}
	if err == nil {
		return nil
	}
	var invalid *ReferenceInvalid
	var gone *TargetGone
	if errors.As(err, &invalid) || errors.As(err, &gone) || errors.Is(err, domain.ErrNotFound) || errors.Is(err, domain.ErrDenied) {
		return err
	}
	// Authentication errors keep their ordinary mapping at the adapter.
	return &ReferenceUnavailable{err}
}
func (s *Service) References(ctx context.Context, token, version string, o Options) (ReferenceList, error) {
	ctx, cancel := context.WithTimeout(ctx, ReferenceTimeout)
	defer cancel()
	for {
		out, err := s.referencesAttempt(ctx, token, version, o)
		if errors.Is(err, errReferenceChanged) && ctx.Err() == nil {
			runtime.Gosched()
			continue
		}
		return out, referenceError(ctx, err)
	}
}
func (s *Service) referencesAttempt(ctx context.Context, token, version string, o Options) (ReferenceList, error) {
	out := ReferenceList{Items: []ReferenceView{}}
	limit := o.Limit
	if limit == 0 {
		limit = 50
	}
	var snap referenceSnapshot
	err := s.referenceRead(ctx, token, version, func(tx app.WriteTx, record domain.Session, e source.Eligibility) error {
		if limit < 1 || limit > 200 {
			return &ReferenceInvalid{"/limit", "invalid_page_size"}
		}
		var err error
		snap, err = referenceBinding(tx, record, e, limit)
		return err
	})
	if err != nil {
		return out, err
	}
	out.Materialization = snap.materialization
	c := referenceCursor{Binding: snap.binding}
	if o.Cursor != "" {
		raw, err := base64.RawURLEncoding.DecodeString(o.Cursor)
		d := json.NewDecoder(strings.NewReader(string(raw)))
		d.DisallowUnknownFields()
		if err != nil || d.Decode(&c) != nil || c.Binding != snap.binding || c.After == "" || !utf8.ValidString(c.After) {
			return out, &ReferenceInvalid{"/cursor", "cursor_mismatch"}
		}
		encoded, _ := json.Marshal(c)
		if base64.RawURLEncoding.EncodeToString(encoded) != o.Cursor {
			return out, &ReferenceInvalid{"/cursor", "cursor_mismatch"}
		}
	}
	after := c.After
	checks := []referenceProjection{}
	for out.Materialization.State == "ready" && len(out.Items) <= limit {
		n := limit + 1 - len(out.Items)
		if n > 64 {
			n = 64
		}
		var rows []domain.AuthoredReference
		err = s.referenceRead(ctx, token, version, func(tx app.WriteTx, record domain.Session, e source.Eligibility) error {
			current, err := referenceBinding(tx, record, e, limit)
			if err != nil {
				return err
			}
			if current.binding != snap.binding {
				return errReferenceChanged
			}
			rows, err = tx.AuthoredReferences(version, after, n)
			return err
		})
		if err != nil {
			return out, err
		}
		start := time.Now()
		for _, r := range rows {
			if len(out.Items) == limit {
				c.After = out.Items[len(out.Items)-1].ID
				b, _ := json.Marshal(c)
				v := base64.RawURLEncoding.EncodeToString(b)
				out.NextCursor = &v
				break
			}
			projected, err := s.projectReference(ctx, token, r, ReferenceOptions{})
			if err != nil {
				return out, err
			}
			out.Items = append(out.Items, projected.view)
			checks = append(checks, projected)
			after = r.ID
			if time.Since(start) >= 8*time.Millisecond {
				runtime.Gosched()
				start = time.Now()
			}
		}
		if out.NextCursor != nil || len(rows) < n {
			break
		}
		runtime.Gosched()
	}
	// Rejoin all detached inventory/anchor work under current authority, one row
	// per short transaction. Nothing in a read path starts or repairs maintenance.
	for _, p := range checks {
		if err = s.referenceProjectionCurrent(ctx, token, version, p); err != nil {
			return out, err
		}
	}
	err = s.referenceRead(ctx, token, version, func(tx app.WriteTx, record domain.Session, e source.Eligibility) error {
		current, err := referenceBinding(tx, record, e, limit)
		if err != nil {
			return err
		}
		if current.binding != snap.binding {
			return errReferenceChanged
		}
		return ctx.Err()
	})
	return out, err
}
func (s *Service) ResolveReference(ctx context.Context, token, version, id string, o ReferenceOptions) (ReferenceView, error) {
	ctx, cancel := context.WithTimeout(ctx, ReferenceTimeout)
	defer cancel()
	for {
		var r domain.AuthoredReference
		err := s.referenceRead(ctx, token, version, func(tx app.WriteTx, _ domain.Session, _ source.Eligibility) error {
			if id == "" || !utf8.ValidString(id) {
				return &ReferenceInvalid{"/reference_id", "reference_id_required"}
			}
			if !domain.ValidReferencePath(o.Page) {
				return &ReferenceInvalid{"/page_path", "invalid_path"}
			}
			if o.Region != nil && (o.Page == nil || *o.Region == "" || !utf8.ValidString(*o.Region)) {
				return &ReferenceInvalid{"/region_id", "region_requires_page"}
			}
			var err error
			r, err = tx.AuthoredReference(version, id)
			return err
		})
		var p referenceProjection
		if err == nil {
			p, err = s.projectReference(ctx, token, r, o)
		}
		if err == nil {
			err = s.referenceProjectionCurrent(ctx, token, version, p)
		}
		if errors.Is(err, errReferenceChanged) && ctx.Err() == nil {
			runtime.Gosched()
			continue
		}
		if err == nil {
			switch p.view.Resolution {
			case "unavailable":
				err = domain.ErrNotFound
			case "tombstoned":
				err = &TargetGone{p.view.Target.(GoneTarget)}
			}
			if p.view.Reason != nil && *p.view.Reason == "ambiguous_path" {
				err = &ReferenceInvalid{"/page_path", "ambiguous_path"}
			}
		}
		return p.view, referenceError(ctx, err)
	}
}

type referenceProjection struct {
	view                  ReferenceView
	declaration           domain.AuthoredReference
	source, target, bound *references.EndpointSnapshot
	edge                  *domain.AuthoredEdge
}

func includes(values []string, v string) bool {
	for _, x := range values {
		if x == v {
			return true
		}
	}
	return false
}

// D4: a retained, consistent edge, a retained purged document, and CURRENT
// intersected target authority. This does not inspect purged files or titles.
func historicalReference(tx app.WriteTx, record domain.Session, r domain.AuthoredReference, edge *domain.AuthoredEdge) (bool, error) {
	if edge == nil || edge.TargetDocumentID != r.TargetDocumentID || edge.TargetVersionID != r.TargetVersionID || edge.FromVersionID != r.FromVersionID || edge.ReferenceID != r.ID {
		return false, nil
	}
	d, err := tx.ReferenceLifecycle(r.TargetDocumentID)
	if errors.Is(err, domain.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if d.Lifecycle != "purged" || d.ProjectID != edge.TargetProjectID {
		return false, nil
	}
	if record.Scope.Kind == "owner" {
		return true, nil
	}
	return includes(record.Scope.Actions, "version.read") && includes(record.Scope.ProjectIDs, d.ProjectID) && includes(record.Scope.VersionIDs, r.TargetVersionID), nil
}
func (s *Service) projectReference(ctx context.Context, token string, r domain.AuthoredReference, o ReferenceOptions) (p referenceProjection, err error) {
	p.declaration = r
	p.view = ReferenceView{ID: r.ID, From: ReferenceContext{r.FromPagePath, r.FromRegionID}, Relation: r.Relation, Resolution: "unavailable", Reason: nullable("target_unavailable")}
	if o.Page != nil {
		err = s.referenceRead(ctx, token, r.FromVersionID, func(tx app.WriteTx, _ domain.Session, _ source.Eligibility) error {
			edge, e := tx.AuthoredEdge(r.FromVersionID, r.ID)
			if e == nil {
				p.edge = &edge
				return nil
			}
			if errors.Is(e, domain.ErrNotFound) {
				return nil
			}
			return e
		})
		if err != nil {
			return p, err
		}
		var reason string
		p.source, p.bound, reason, err = s.boundReference(ctx, r, o)
		if err != nil {
			return p, err
		}
		if reason != "" {
			p.view.Resolution = "unresolved"
			p.view.Reason = &reason
			return p, nil
		}
	}
	authorized := false
	err = s.referenceRead(ctx, token, r.FromVersionID, func(tx app.WriteTx, record domain.Session, _ source.Eligibility) error {
		edge, e := tx.AuthoredEdge(r.FromVersionID, r.ID)
		if e == nil {
			p.edge = &edge
		} else if !errors.Is(e, domain.ErrNotFound) {
			return e
		}
		lifecycle, e := tx.ReferenceLifecycle(r.TargetDocumentID)
		if errors.Is(e, domain.ErrNotFound) {
			return nil
		}
		if e != nil {
			return e
		}
		if lifecycle.Lifecycle == "purged" {
			gone, e := historicalReference(tx, record, r, p.edge)
			if e != nil {
				return e
			}
			if gone {
				p.view.Resolution = "tombstoned"
				p.view.Reason = nil
				p.view.Target = GoneTarget{r.TargetDocumentID, r.TargetVersionID}
			}
			return nil
		}
		if lifecycle.Lifecycle == "trash" {
			return nil
		}
		eligible, e := source.Check(tx, record, r.TargetVersionID)
		if e != nil {
			if errors.Is(e, domain.ErrNotFound) || errors.Is(e, domain.ErrDenied) {
				return nil
			}
			return e
		}
		if eligible.Version.DocumentID != r.TargetDocumentID {
			return nil
		}
		authorized = true
		return nil
	})
	if err != nil || !authorized {
		return p, err
	}
	p.view.Label = r.Label
	p.view.Resolution = "unresolved"
	target, err := references.Inspect(ctx, s.Store, r.TargetVersionID, r.TargetPagePath, r.TargetRegionID)
	if err != nil {
		return p, err
	}
	p.target = &target
	exact := ExactTarget{r.TargetDocumentID, r.TargetVersionID, r.TargetPagePath, r.TargetRegionID, r.TargetPagePath, target.Page, r.PageQuery, r.PageFragment}
	p.view.Target = exact
	if target.Page != nil || r.TargetPagePath == nil {
		p.view.ReaderURL = referenceReaderURL(exact, false)
	}
	src, err := references.Inspect(ctx, s.Store, r.FromVersionID, r.FromPagePath, r.FromRegionID)
	if err != nil {
		return p, err
	}
	p.source = &src
	reason := src.Reason
	if reason == "" {
		reason = target.Reason
	}
	if reason == "" && p.edge != nil {
		e := p.edge
		hash := func(p *string, s string) bool { return (p == nil && s == "") || (p != nil && *p == s) }
		if !reflect.DeepEqual(e.FromPagePath, src.Page) || !reflect.DeepEqual(e.TargetPagePath, target.Page) || e.AnchorRevision != domain.SearchExtractorRevision || !hash(e.FromPageSHA, src.Anchor.PageHash) || !hash(e.FromAnchorSHA, src.Anchor.AnchorHash) || !hash(e.TargetPageSHA, target.Anchor.PageHash) || !hash(e.TargetAnchorSHA, target.Anchor.AnchorHash) {
			reason = "anchor_drift"
		} else {
			p.view.Resolution = "resolved"
			p.view.Reason = nil
			p.view.EdgeID = &e.ID
			p.view.ReaderURL = referenceReaderURL(exact, true)
			return p, nil
		}
	}
	if reason == "" {
		reason = "resolution_pending"
	}
	p.view.Reason = &reason
	return p, nil
}
func referenceReaderURL(t ExactTarget, region bool) *string {
	q := url.Values{}
	if t.ResolvedPath != nil {
		q.Set("page", *t.ResolvedPath)
	}
	if t.PageQuery != "" {
		q.Set("page_query", t.PageQuery)
	}
	if t.PageFragment != "" {
		q.Set("page_fragment", t.PageFragment)
	}
	value := "/documents/" + t.DocumentID + "/versions/" + t.VersionID
	if len(q) > 0 {
		value += "?" + q.Encode()
	}
	if region && t.RegionID != nil {
		value += "#region=" + url.QueryEscape(*t.RegionID)
	}
	return &value
}
func (s *Service) referenceProjectionCurrent(ctx context.Context, token, version string, p referenceProjection) error {
	return s.referenceRead(ctx, token, version, func(tx app.WriteTx, record domain.Session, _ source.Eligibility) error {
		r, err := tx.AuthoredReference(version, p.declaration.ID)
		if err != nil {
			return err
		}
		if !reflect.DeepEqual(r, p.declaration) {
			return errReferenceChanged
		}
		if p.view.Resolution == "tombstoned" {
			gone, err := historicalReference(tx, record, r, p.edge)
			if err != nil {
				return err
			}
			if !gone {
				return errReferenceChanged
			}
		}
		if p.target != nil {
			if _, err := source.Check(tx, record, r.TargetVersionID); err != nil {
				if errors.Is(err, domain.ErrNotFound) || errors.Is(err, domain.ErrDenied) {
					return errReferenceChanged
				}
				return err
			}
		}
		for _, e := range []*references.EndpointSnapshot{p.source, p.target, p.bound} {
			if e != nil {
				if err := e.Current(tx); err != nil {
					if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
						return err
					}
					return errReferenceChanged
				}
			}
		}
		edge, err := tx.AuthoredEdge(version, r.ID)
		if errors.Is(err, domain.ErrNotFound) {
			if p.edge != nil {
				return errReferenceChanged
			}
		} else if err != nil {
			return err
		} else if p.edge == nil || !reflect.DeepEqual(edge, *p.edge) {
			return errReferenceChanged
		}
		return ctx.Err()
	})
}
