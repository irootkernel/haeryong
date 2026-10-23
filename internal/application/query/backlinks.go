package query

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"haeryong.local/haeryong/internal/application/references"
	"haeryong.local/haeryong/internal/application/source"
	app "haeryong.local/haeryong/internal/application/storage"
	"haeryong.local/haeryong/internal/domain"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"unicode/utf8"
)

type BacklinkOptions struct {
	Options
	ReferenceOptions
}
type LinkScope struct {
	VersionID string `json:"version_id"`
	ReferenceContext
}
type LinkEndpoint struct {
	DocumentID string `json:"document_id"`
	VersionID  string `json:"version_id"`
	ReferenceContext
}
type Backlink struct {
	EdgeID      string       `json:"edge_id"`
	ReferenceID string       `json:"reference_id"`
	From        LinkEndpoint `json:"from"`
	Target      LinkEndpoint `json:"target"`
	Relation    *string      `json:"relation"`
	Label       *string      `json:"label"`
	ReaderURL   string       `json:"reader_url"`
}
type BacklinkCounts struct {
	EdgeCount string `json:"edge_count"`
	Documents string `json:"referring_document_count"`
}
type OutgoingCounts struct {
	EdgeCount string `json:"edge_count"`
	Documents string `json:"referenced_document_count"`
}
type BacklinkPage struct {
	Scope      LinkScope      `json:"scope"`
	Items      []Backlink     `json:"items"`
	NextCursor *string        `json:"next_cursor"`
	Counts     BacklinkCounts `json:"counts"`
}
type backlinkPosition struct {
	Page, Region             *string
	SourceVersion, Reference string
}
type backlinkCursor struct {
	Binding string
	After   backlinkPosition
}

func edgePosition(k domain.EdgeKey) backlinkPosition {
	return backlinkPosition{k.Page, k.Region, k.SourceVersion, k.Reference}
}
func compareAnchor(a, b *string) int {
	if a == nil {
		if b == nil {
			return 0
		}
		return -1
	}
	if b == nil {
		return 1
	}
	return strings.Compare(*a, *b)
}
func positionAfter(a, b backlinkPosition) bool {
	if c := compareAnchor(a.Page, b.Page); c != 0 {
		return c > 0
	}
	if c := compareAnchor(a.Region, b.Region); c != 0 {
		return c > 0
	}
	if a.SourceVersion != b.SourceVersion {
		return a.SourceVersion > b.SourceVersion
	}
	return a.Reference > b.Reference
}
func validateLinkScope(o ReferenceOptions) error {
	if !domain.ValidReferencePath(o.Page) {
		return &ReferenceInvalid{"/page_path", "invalid_path"}
	}
	if o.Region != nil && (o.Page == nil || *o.Region == "" || !utf8.ValidString(*o.Region)) {
		return &ReferenceInvalid{"/region_id", "region_requires_page"}
	}
	return nil
}
func (s *Service) linkBinding(ctx context.Context, token, version string, scope ReferenceOptions, limit int) (string, error) {
	var binding string
	err := s.referenceRead(ctx, token, version, func(tx app.WriteTx, record domain.Session, e source.Eligibility) error {
		l, err := tx.Library()
		if err != nil {
			return err
		}
		p, err := tx.Scope(e.ProjectID)
		if err != nil {
			return err
		}
		var revision int64
		if record.Scope.Kind != "owner" {
			principal, err := tx.WorkflowPrincipal(record.PrincipalID)
			if err != nil {
				return err
			}
			revision = principal.Revision
		}
		binding = validator([]any{l.ID, l.RuntimeGeneration, record.PrincipalID, record.Scope, revision, p.Project.PermissionsRevision, version, e.Version.ManifestSHA, scope, limit, 1})
		return nil
	})
	return binding, err
}

// A physical edge never grants authority. Check both endpoints before fetching
// its declaration label, then reuse the pinned-reference live projection.
func (s *Service) projectedEdge(ctx context.Context, token, selected string, k domain.EdgeKey) (referenceProjection, string, error) {
	var r domain.AuthoredReference
	var document string
	admitted := false
	err := s.referenceRead(ctx, token, selected, func(tx app.WriteTx, record domain.Session, _ source.Eligibility) error {
		e, err := source.Check(tx, record, k.SourceVersion)
		if errors.Is(err, domain.ErrNotFound) || errors.Is(err, domain.ErrDenied) {
			return nil
		}
		if err != nil {
			return err
		}
		if _, err = source.Check(tx, record, k.TargetVersion); errors.Is(err, domain.ErrNotFound) || errors.Is(err, domain.ErrDenied) {
			return nil
		} else if err != nil {
			return err
		}
		base, err := tx.CanonicalEdgeKey(k.RowID)
		if err != nil {
			return err
		}
		// A damaged covering key can name a different target. Establish the
		// actual base row's endpoints before exposing a mismatch as well.
		for _, endpoint := range []string{base.SourceVersion, base.TargetVersion} {
			if _, err = source.Check(tx, record, endpoint); errors.Is(err, domain.ErrNotFound) || errors.Is(err, domain.ErrDenied) {
				return nil
			} else if err != nil {
				return err
			}
		}
		if !reflect.DeepEqual(base, k) {
			return domain.ErrContentIntegrity
		}
		r, err = tx.AuthoredReference(k.SourceVersion, k.Reference)
		document = e.Document.ID
		admitted = err == nil
		return err
	})
	if err != nil || !admitted {
		return referenceProjection{}, "", err
	}
	p, err := s.projectReference(ctx, token, r, ReferenceOptions{})
	if err != nil {
		return p, "", err
	}
	if p.view.Resolution != "resolved" {
		return referenceProjection{}, "", nil
	}
	if err = s.referenceProjectionCurrent(ctx, token, k.SourceVersion, p); err != nil {
		return p, "", err
	}
	return p, document, nil
}
func (s *Service) scanBacklinks(ctx context.Context, token, version string, o BacklinkOptions, after *backlinkPosition, collect bool) (BacklinkPage, string, []backlinkPosition, error) {
	out := BacklinkPage{Scope: LinkScope{version, ReferenceContext{o.Page, o.Region}}, Items: []Backlink{}}
	base, err := s.linkBinding(ctx, token, version, o.ReferenceOptions, o.Limit)
	if err != nil {
		return out, "", nil, err
	}
	hash := sha256.New()
	hash.Write([]byte(base))
	docs := map[string]struct{}{}
	var count int64
	var pos *domain.EdgeKey
	positions := []backlinkPosition{}
	seenAfter := after == nil
	for {
		var keys []domain.EdgeKey
		err = s.referenceRead(ctx, token, version, func(tx app.WriteTx, _ domain.Session, _ source.Eligibility) error {
			var e error
			keys, e = tx.IncomingEdgeKeys(version, o.Page, o.Region, pos, 64)
			return e
		})
		if err != nil {
			return out, "", nil, err
		}
		if len(keys) == 0 {
			break
		}
		for _, k := range keys {
			p, document, e := s.projectedEdge(ctx, token, version, k)
			if e != nil {
				return out, "", nil, e
			}
			pos = &k
			if document == "" {
				continue
			}
			target := p.view.Target.(ExactTarget)
			fromPage := p.view.From.PagePath
			if p.source != nil {
				fromPage = p.source.Page
			}
			route := referenceReaderURL(ExactTarget{DocumentID: document, VersionID: k.SourceVersion, ResolvedPath: fromPage, RegionID: p.view.From.RegionID}, true)
			row := Backlink{*p.view.EdgeID, k.Reference, LinkEndpoint{document, k.SourceVersion, ReferenceContext{fromPage, p.view.From.RegionID}}, LinkEndpoint{target.DocumentID, target.VersionID, ReferenceContext{target.ResolvedPath, target.RegionID}}, p.view.Relation, p.view.Label, *route}
			raw, _ := json.Marshal(row)
			hash.Write(raw)
			count++
			docs[document] = struct{}{}
			public := edgePosition(k)
			if after != nil && reflect.DeepEqual(public, *after) {
				seenAfter = true
			}
			if collect && (after == nil || positionAfter(public, *after)) && len(out.Items) <= o.Limit {
				out.Items = append(out.Items, row)
				positions = append(positions, public)
			}
		}
		runtime.Gosched()
	}
	if !seenAfter {
		return out, "", nil, &ReferenceInvalid{"/cursor", "cursor_mismatch"}
	}
	current, err := s.linkBinding(ctx, token, version, o.ReferenceOptions, o.Limit)
	if err != nil {
		return out, "", nil, err
	}
	if base != current {
		return out, "", nil, errReferenceChanged
	}
	out.Counts = BacklinkCounts{strconv.FormatInt(count, 10), strconv.Itoa(len(docs))}
	raw, _ := json.Marshal(out.Counts)
	hash.Write(raw)
	return out, hex.EncodeToString(hash.Sum(nil)), positions, nil
}
func (s *Service) Backlinks(ctx context.Context, token, version string, o BacklinkOptions) (BacklinkPage, error) {
	ctx, cancel := context.WithTimeout(ctx, ReferenceTimeout)
	defer cancel()
	if err := validateLinkScope(o.ReferenceOptions); err != nil {
		return BacklinkPage{}, err
	}
	if o.Limit == 0 {
		o.Limit = 50
	}
	if o.Limit < 1 || o.Limit > 200 {
		return BacklinkPage{}, &ReferenceInvalid{"/limit", "invalid_page_size"}
	}
	if err := s.referenceRead(ctx, token, version, func(app.WriteTx, domain.Session, source.Eligibility) error { return nil }); err != nil {
		return BacklinkPage{}, referenceError(ctx, err)
	}
	if o.Page != nil {
		snapshot, err := references.Inspect(ctx, s.Store, version, o.Page, o.Region)
		if err != nil {
			return BacklinkPage{}, referenceError(ctx, err)
		}
		if snapshot.Page != nil {
			o.Page = snapshot.Page
		}
	}
	var cursor backlinkCursor
	var after *backlinkPosition
	if o.Cursor != "" {
		raw, e := base64.RawURLEncoding.DecodeString(o.Cursor)
		d := json.NewDecoder(strings.NewReader(string(raw)))
		d.DisallowUnknownFields()
		if e != nil || d.Decode(&cursor) != nil || cursor.After.SourceVersion == "" || cursor.After.Reference == "" {
			return BacklinkPage{}, &ReferenceInvalid{"/cursor", "cursor_mismatch"}
		}
		encoded, _ := json.Marshal(cursor)
		if base64.RawURLEncoding.EncodeToString(encoded) != o.Cursor {
			return BacklinkPage{}, &ReferenceInvalid{"/cursor", "cursor_mismatch"}
		}
		after = &cursor.After
	}
	for ctx.Err() == nil {
		out, binding, positions, err := s.scanBacklinks(ctx, token, version, o, after, true)
		if errors.Is(err, errReferenceChanged) {
			continue
		}
		if err != nil {
			return out, referenceError(ctx, err)
		}
		_, confirm, _, err := s.scanBacklinks(ctx, token, version, o, nil, false)
		if errors.Is(err, errReferenceChanged) || err == nil && binding != confirm {
			continue
		}
		if err != nil {
			return out, referenceError(ctx, err)
		}
		if o.Cursor != "" && cursor.Binding != binding {
			return out, &ReferenceInvalid{"/cursor", "cursor_mismatch"}
		}
		if len(out.Items) > o.Limit {
			out.Items = out.Items[:o.Limit]
			raw, _ := json.Marshal(backlinkCursor{binding, positions[o.Limit-1]})
			v := base64.RawURLEncoding.EncodeToString(raw)
			out.NextCursor = &v
		}
		return out, referenceError(ctx, ctx.Err())
	}
	return BacklinkPage{}, referenceError(ctx, ctx.Err())
}
func (s *Service) outgoingCounts(ctx context.Context, token, version string) (OutgoingCounts, string, error) {
	docs := map[string]struct{}{}
	var count int64
	after := ""
	hash := sha256.New()
	for {
		var keys []domain.EdgeKey
		err := s.referenceRead(ctx, token, version, func(tx app.WriteTx, _ domain.Session, _ source.Eligibility) error {
			var err error
			keys, err = tx.OutgoingEdgeKeys(version, after, 64)
			return err
		})
		if err != nil {
			return OutgoingCounts{}, "", err
		}
		if len(keys) == 0 {
			break
		}
		for _, k := range keys {
			p, document, err := s.projectedEdge(ctx, token, version, k)
			if err != nil {
				return OutgoingCounts{}, "", err
			}
			after = k.Reference
			if document == "" {
				continue
			}
			target := p.view.Target.(ExactTarget)
			count++
			docs[target.DocumentID] = struct{}{}
			raw, _ := json.Marshal(p.view)
			hash.Write(raw)
		}
		runtime.Gosched()
	}
	return OutgoingCounts{strconv.FormatInt(count, 10), strconv.Itoa(len(docs))}, hex.EncodeToString(hash.Sum(nil)), nil
}
