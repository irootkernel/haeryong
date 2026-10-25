package foundation

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"reflect"
	"strings"

	"haeryong.local/haeryong/internal/application/referencecodec"
	"haeryong.local/haeryong/internal/domain"
)

// Check the declared nested grammar before decoding loses field presence or
// duplicate keys. RawMessage is deliberately opaque until its family is known.
func decodeCopyValue(raw []byte, out any) error {
	if e := copyShape(raw, reflect.TypeOf(out).Elem()); e != nil {
		return e
	}
	return json.Unmarshal(raw, out)
}

var rawMessageType = reflect.TypeOf(json.RawMessage{})

func copyShape(raw []byte, t reflect.Type) error {
	raw = bytes.TrimSpace(raw)
	if t == rawMessageType {
		return nil
	}
	if t.Kind() == reflect.Pointer {
		if bytes.Equal(raw, []byte("null")) {
			return nil
		}
		return copyShape(raw, t.Elem())
	}
	if bytes.Equal(raw, []byte("null")) {
		return domain.ErrContentIntegrity
	}
	if t.Kind() != reflect.Struct {
		return nil
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	open, e := d.Token()
	if e != nil || open != json.Delim('{') {
		return domain.ErrContentIntegrity
	}
	fields := map[string]reflect.StructField{}
	required := map[string]bool{}
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		tag := strings.Split(f.Tag.Get("json"), ",")
		name := tag[0]
		if name == "-" {
			continue
		}
		if name == "" {
			name = f.Name
		}
		fields[name] = f
		if !strings.Contains(f.Tag.Get("json"), ",omitempty") {
			required[name] = true
		}
	}
	seen := map[string]bool{}
	for d.More() {
		key, e := d.Token()
		if e != nil {
			return e
		}
		name, ok := key.(string)
		f, known := fields[name]
		if !ok || !known || seen[name] {
			return domain.ErrContentIntegrity
		}
		seen[name] = true
		var value json.RawMessage
		if e = d.Decode(&value); e != nil {
			return e
		}
		if e = copyShape(value, f.Type); e != nil {
			return e
		}
	}
	if _, e = d.Token(); e != nil {
		return e
	}
	if _, e = d.Token(); e != io.EOF {
		return domain.ErrContentIntegrity
	}
	for name := range required {
		if !seen[name] {
			return domain.ErrContentIntegrity
		}
	}
	return nil
}

type exportRecord struct {
	Kind    string          `json:"kind"`
	Owner   string          `json:"owner_id,omitempty"`
	Version string          `json:"version_id,omitempty"`
	Value   json.RawMessage `json:"value"`
}
type exportContext struct {
	ID      string  `json:"context_ref"`
	Version string  `json:"version_id"`
	Page    *string `json:"page_path"`
	Media   string  `json:"media_type"`
	Hash    string  `json:"sha256"`
	Size    int64   `json:"bytes"`
}
type noteIdentity struct{ owner, document, id string }
type noteRevisions struct{ values map[int64]domain.Annotation }
type exportBindings struct {
	ctx          context.Context
	owner        string
	notes        map[noteIdentity]*noteRevisions
	contexts     map[string]exportContext
	required     map[exportLink]bool
	dependencies map[exportLink]string
	values       map[string]json.RawMessage
}

func newExportBindings(ctx context.Context) *exportBindings {
	return &exportBindings{ctx: ctx, notes: map[noteIdentity]*noteRevisions{}, contexts: map[string]exportContext{}, required: map[exportLink]bool{}, dependencies: map[exportLink]string{}, values: map[string]json.RawMessage{}}
}
func (b *exportBindings) finish(payload *exportView, selected map[string]domain.CopyVersion) error {
	used := map[string]bool{}
	owners := map[string]noteIdentity{}
	for identity, group := range b.notes {
		if old, ok := owners[identity.id]; ok && old != identity {
			return domain.ErrContentIntegrity
		}
		owners[identity.id] = identity
		qualified := false
		var original domain.AnnotationAnchor
		var created string
		var first bool
		for _, n := range group.values {
			if !first {
				original = n.OriginalAnchor
				created = n.CreatedAt
				first = true
			} else if !reflect.DeepEqual(original, n.OriginalAnchor) || n.CreatedAt != created {
				return domain.ErrContentIntegrity
			}
			for _, anchor := range []domain.AnnotationAnchor{n.Anchor, n.OriginalAnchor} {
				if v, ok := selected[anchor.VersionID]; ok {
					if v.DocumentID != identity.document || !exportReaderPage(payload, v, anchor.PagePath) {
						return domain.ErrContentIntegrity
					}
					qualified = true
				}
			}
			if n.ContextRef != nil {
				used[*n.ContextRef] = true
				c, ok := b.contexts[*n.ContextRef]
				if !ok || c.Version != n.Anchor.VersionID || !reflect.DeepEqual(c.Page, n.Anchor.PagePath) {
					return domain.ErrContentIntegrity
				}
			}
		}
		if !qualified {
			return domain.ErrContentIntegrity
		}
	}
	for link, state := range b.dependencies {
		v, included := selected[link.version]
		if link.kind == "reference" && included && v.DocumentID != link.document {
			// An immutable authored pair may be unresolved even when its version
			// belongs to another selected document. It is not a Note invariant.
			if state != "unavailable" {
				return domain.ErrContentIntegrity
			}
			continue
		}
		if (state == "included") != included || included && v.DocumentID != link.document {
			return domain.ErrContentIntegrity
		}
	}

	for link := range b.required {
		if _, ok := b.dependencies[link]; !ok {
			return domain.ErrContentIntegrity
		}
	}

	for id := range b.contexts {
		if !used[id] {
			return domain.ErrContentIntegrity
		}
	}
	return nil
}
func validExportAnchor(a domain.AnnotationAnchor) bool {
	return copyUUID.MatchString(a.VersionID) && domain.ValidReferencePath(a.PagePath) && (a.RegionID == nil || a.PagePath != nil && *a.RegionID != "") && (a.PagePath != nil || a.PageQuery == "" && a.PageFragment == "")
}
func (b *exportBindings) record(payload, view *exportView, selected domain.CopyVersion, row exportRecord) error {
	if row.Kind == "context" {
		if row.Owner != "" || row.Version != "" {
			return domain.ErrContentIntegrity
		}
		var c exportContext
		if e := decodeCopyValue(row.Value, &c); e != nil {
			return e
		}
		if !copyUUID.MatchString(c.ID) || !copyUUID.MatchString(c.Version) || !domain.ValidReferencePath(c.Page) || c.Media == "" {
			return domain.ErrContentIntegrity
		}
		if old, ok := b.contexts[c.ID]; ok && !reflect.DeepEqual(old, c) {
			return domain.ErrContentIntegrity
		}
		b.contexts[c.ID] = c
		return verifyMetadataObject(b.ctx, view, *selected.MetadataRoot, domain.Object{Hash: c.Hash, Size: c.Size})
	}
	if !copyUUID.MatchString(row.Owner) || row.Version != selected.VersionID {
		return domain.ErrContentIntegrity
	}
	if b.owner != "" && b.owner != row.Owner {
		return domain.ErrContentIntegrity
	}
	b.owner = row.Owner
	switch row.Kind {
	case "notes":
		var n domain.Annotation
		if e := decodeCopyValue(row.Value, &n); e != nil {
			return e
		}
		if !copyUUID.MatchString(n.ID) || n.DocumentID != selected.DocumentID || n.Revision < 1 || !validExportAnchor(n.Anchor) || !validExportAnchor(n.OriginalAnchor) {
			return domain.ErrContentIntegrity
		}
		if !copyUUID.MatchString(n.ActorSession) || !copyUUID.MatchString(n.OperationID) {
			return domain.ErrContentIntegrity
		}
		switch n.Action {
		case "create", "edit", "reattach", "delete":
		default:
			return domain.ErrContentIntegrity
		}
		if (n.Action == "delete") != (n.DeletedAt != nil) {
			return domain.ErrContentIntegrity
		}
		if n.DeletedAt != nil {
			if n.Body != nil || n.BodyObject != nil || n.ContextRef != nil || n.SelectedText != nil {
				return domain.ErrContentIntegrity
			}
		} else if (n.Body == nil) == (n.BodyObject == nil) {
			return domain.ErrContentIntegrity
		}
		if n.BodyObject != nil {
			if e := verifyMetadataObject(b.ctx, view, *selected.MetadataRoot, *n.BodyObject); e != nil {
				return e
			}
		}
		id := noteIdentity{row.Owner, n.DocumentID, n.ID}
		g := b.notes[id]
		if g == nil {
			g = &noteRevisions{map[int64]domain.Annotation{}}
			b.notes[id] = g
		}
		if old, ok := g.values[n.Revision]; ok && !reflect.DeepEqual(old, n) {
			return domain.ErrContentIntegrity
		}
		g.values[n.Revision] = n
		for _, a := range []domain.AnnotationAnchor{n.Anchor, n.OriginalAnchor} {
			b.required[newExportLink("note_anchor", selected.VersionID, n.DocumentID, a.VersionID, a.PagePath, a.RegionID)] = true
		}
	case "review":
		var fields map[string]json.RawMessage
		if json.Unmarshal(row.Value, &fields) != nil {
			return domain.ErrContentIntegrity
		}
		if _, event := fields["event_id"]; event {
			var v struct {
				ID        string `json:"event_id"`
				Owner     string `json:"owner_id"`
				Version   string `json:"version_id"`
				Action    string `json:"action"`
				Old       string `json:"old_state"`
				New       string `json:"new_state"`
				Operation string `json:"operation_id"`
				Created   string `json:"created_at"`
			}
			if e := decodeCopyValue(row.Value, &v); e != nil {
				return e
			}
			if v.Owner != row.Owner || v.Version != row.Version || !copyUUID.MatchString(v.ID) || !copyUUID.MatchString(v.Operation) || !validExportReviewEvent(v.Action, v.Old, v.New) {
				return domain.ErrContentIntegrity
			}
		} else {
			var v struct {
				Owner    string  `json:"owner_id"`
				Version  string  `json:"version_id"`
				State    string  `json:"state"`
				Reviewed *string `json:"reviewed_at"`
				Revision int64   `json:"revision"`
				Sequence int64   `json:"event_sequence"`
			}
			if e := decodeCopyValue(row.Value, &v); e != nil {
				return e
			}
			if v.Owner != row.Owner || v.Version != row.Version || v.Revision < 1 || (!validExportReviewState(v.State)) || (v.State == "reviewed") != (v.Reviewed != nil) || v.Sequence < 1 {
				return domain.ErrContentIntegrity
			}
		}
	case "revisit":
		var v struct {
			Owner    string `json:"owner_id"`
			Document string `json:"document_id"`
			Flag     bool   `json:"flag"`
			Revision int64  `json:"revision"`
		}
		if e := decodeCopyValue(row.Value, &v); e != nil {
			return e
		}
		if v.Owner != row.Owner || v.Document != selected.DocumentID || v.Revision < 1 {
			return domain.ErrContentIntegrity
		}
	case "reader":
		var v struct {
			Kind     string          `json:"kind"`
			ID       string          `json:"resource_id"`
			Revision int64           `json:"revision"`
			Value    json.RawMessage `json:"value"`
		}
		if e := decodeCopyValue(row.Value, &v); e != nil {
			return e
		}
		prefix := row.Owner + "/" + row.Version
		if v.Revision < 1 {
			return domain.ErrContentIntegrity
		}
		switch v.Kind {
		case "reader_preference":
			var x struct {
				Page *string `json:"default_page"`
			}
			if e := decodeCopyValue(v.Value, &x); e != nil {
				return e
			}
			if v.ID != prefix || !domain.ValidReferencePath(x.Page) || !exportReaderPage(payload, selected, x.Page) {
				return domain.ErrContentIntegrity
			}
		case "reading_position":
			var x struct {
				Page     *string `json:"page_path"`
				Query    string  `json:"page_query"`
				Fragment string  `json:"page_fragment"`
				Opened   bool    `json:"opened"`
				First    string  `json:"first_opened_at"`
				Last     string  `json:"last_opened_at"`
			}
			if e := decodeCopyValue(v.Value, &x); e != nil {
				return e
			}
			if v.ID != prefix || !domain.ValidReferencePath(x.Page) || !exportReaderPage(payload, selected, x.Page) {
				return domain.ErrContentIntegrity
			}
		case "page_position":
			var x struct {
				Query    string         `json:"page_query"`
				Fragment string         `json:"page_fragment"`
				Position map[string]any `json:"position"`
			}
			if e := decodeCopyValue(v.Value, &x); e != nil {
				return e
			}
			page := strings.TrimPrefix(v.ID, prefix+"/")
			if !strings.HasPrefix(v.ID, prefix+"/") || domain.ValidatePayloadPath(page) != nil || !exportReaderPage(payload, selected, &page) {
				return domain.ErrContentIntegrity
			}
		default:
			return domain.ErrContentIntegrity
		}
	case "references":
		var v struct {
			Version  string          `json:"from_version_id"`
			ID       string          `json:"metadata_reference_id"`
			Authored json.RawMessage `json:"authored"`
		}
		if e := decodeCopyValue(row.Value, &v); e != nil {
			return e
		}
		r, e := referencecodec.DecodeReference(v.Authored)
		if e != nil {
			return e
		}
		if v.Version != row.Version || v.ID != r.ID {
			return domain.ErrContentIntegrity
		}
		b.required[newExportLink("reference", selected.VersionID, r.TargetDocumentID, r.TargetVersionID, r.TargetPagePath, r.TargetRegionID)] = true
	default:
		return domain.ErrContentIntegrity
	}
	// Repeated portable identities may repeat identical content, never disagree.
	var identity string
	switch row.Kind {
	case "review":
		var v map[string]any
		_ = json.Unmarshal(row.Value, &v)
		if id, ok := v["event_id"].(string); ok {
			identity = "event/" + id
		} else {
			identity = "review/" + row.Owner + "/" + row.Version
		}
	case "reader":
		var v struct {
			Kind string `json:"kind"`
			ID   string `json:"resource_id"`
		}
		_ = json.Unmarshal(row.Value, &v)
		identity = "reader/" + v.Kind + "/" + v.ID
	case "revisit":
		identity = "revisit/" + row.Owner + "/" + selected.DocumentID
	case "references":
		var v struct {
			ID string `json:"metadata_reference_id"`
		}
		_ = json.Unmarshal(row.Value, &v)
		identity = "reference/" + row.Version + "/" + v.ID
	}
	if identity != "" {
		return b.value(identity, row.Value)
	}
	return nil
}

func (b *exportBindings) value(identity string, value json.RawMessage) error {
	if old, ok := b.values[identity]; ok {
		var a, c any
		left, right := json.NewDecoder(bytes.NewReader(old)), json.NewDecoder(bytes.NewReader(value))
		left.UseNumber()
		right.UseNumber()
		if left.Decode(&a) != nil || right.Decode(&c) != nil {
			return domain.ErrContentIntegrity
		}
		if !reflect.DeepEqual(a, c) {
			return domain.ErrContentIntegrity
		}
	} else {
		b.values[identity] = value
	}
	return nil
}

func validExportReviewState(s string) bool {
	return s == "unreviewed" || s == "reviewing" || s == "reviewed"
}
func validExportReviewEvent(action, old, next string) bool {
	if !validExportReviewState(old) || !validExportReviewState(next) || old == next {
		return false
	}
	switch action {
	case "start":
		return old == "unreviewed" && next == "reviewing"
	case "complete":
		return next == "reviewed"
	case "reopen":
		return old == "reviewed" && next == "reviewing"
	case "clear":
		return next == "unreviewed"
	}
	return false
}

type exportLink struct{ kind, bucket, document, version, page, region string }

func newExportLink(kind, bucket, document, version string, page, region *string) exportLink {
	l := exportLink{kind: kind, bucket: bucket, document: document, version: version}
	if page != nil {
		l.page = *page
	}
	if region != nil {
		l.region = *region
	}
	return l
}

func exportReaderPage(payload *exportView, v domain.CopyVersion, page *string) bool {
	if page == nil {
		return true
	}
	entry, ok := payload.payloadEntry(v.PayloadRoot, *page)
	return ok && entry.kind == "file" && domain.IsHTMLPath(*page)
}
