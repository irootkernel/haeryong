// Personal reader state: three independent owner-scoped resource families
// with full resource-bound opaque validators. The validator embeds kind,
// principal, version, exact page and value digest, so no page-A validator can
// validate page B or the version pointer at any revision. PUT compares the
// submitted If-Match as an opaque string against the currently stored
// validator inside the same serialized write transaction; a revision is never
// parsed out of an ETag.
package query

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	app "haeryong.local/haeryong/internal/application/storage"
	"haeryong.local/haeryong/internal/domain"
	"io"
	"net/url"
	"path"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

type PersonalKind string

const (
	ReaderPreference PersonalKind = "reader-preference"
	ReadingPosition  PersonalKind = "reading-position"
	PagePosition     PersonalKind = "page-position"
)

func (k PersonalKind) valid() bool {
	return k == ReaderPreference || k == ReadingPosition || k == PagePosition
}
func (k PersonalKind) resource() string {
	switch k {
	case ReaderPreference:
		return "reader_preference"
	case ReadingPosition:
		return "reading_position"
	default:
		return "page_position"
	}
}

// Transition reports an authorized lifecycle conflict for a personal write.
type Transition struct{ Lifecycle string }

func (e *Transition) Error() string { return "lifecycle blocks write: " + e.Lifecycle }

// PersonalView is the selected wire shape for all three resource families.
// Absent state returns the family's default value with explicit null
// revision/etag keys and the create precondition; a stored resource returns
// its real validator. A reading-position response always carries
// page_position_url: the once-encoded canonical page link, or explicit null
// for a whole-version pointer (never an omitted key).
type PersonalView struct {
	Exists               bool            `json:"exists"`
	Value                json.RawMessage `json:"value"`
	Revision             *int64          `json:"revision"`
	ETag                 *string         `json:"etag"`
	RequiredPrecondition string          `json:"required_precondition,omitempty"`
	PagePositionURL      *string         `json:"page_position_url"`
}

func absentPersonalValue(kind PersonalKind) json.RawMessage {
	switch kind {
	case ReaderPreference:
		return json.RawMessage(`{"default_page":null}`)
	case ReadingPosition:
		return json.RawMessage(`{"page_path":null,"page_query":"","page_fragment":"","opened":false,"first_opened_at":null,"last_opened_at":null}`)
	default:
		return json.RawMessage(`{"page_query":"","page_fragment":"","position":{}}`)
	}
}

// pointerPageLink extracts the stored pointer's page for the selected
// page-position link; null page_path is an explicit null link.
func pointerPageLink(version string, stored json.RawMessage) *string {
	var value struct {
		PagePath *string `json:"page_path"`
	}
	if json.Unmarshal(stored, &value) != nil || value.PagePath == nil {
		return nil
	}
	link := "/api/v1/versions/" + version + "/page-position?page=" + url.QueryEscape(*value.PagePath)
	return &link
}

func personalETag(kind, id string, revision int64, value []byte) string {
	h := sha256.New()
	h.Write([]byte(kind))
	h.Write([]byte{0})
	h.Write([]byte(id))
	h.Write([]byte{0})
	h.Write([]byte(strconv.FormatInt(revision, 10)))
	h.Write([]byte{0})
	valueDigest := sha256.Sum256(value)
	h.Write(valueDigest[:])
	return "\"sha256-" + hex.EncodeToString(h.Sum(nil)) + "\""
}

func personalID(principal, version, page string) string {
	id := principal + "/" + version
	if page != "" {
		id += "/" + page
	}
	return id
}

func personalTarget(version string, kind PersonalKind, page string) string {
	target := "/api/v1/versions/" + version + "/" + string(kind)
	if kind == PagePosition {
		// The page path is percent-encoded exactly once, mirroring the wire
		// route; literal '&', '?', '#', Unicode and percent characters stay
		// inside the single encoded value for authority and receipt identity.
		target += "?page=" + url.QueryEscape(page)
	}
	return target
}

// strictObject decodes one closed JSON object with duplicate-key detection.
func strictObject(body []byte) (map[string]json.RawMessage, error) {
	decoder := json.NewDecoder(bytes.NewReader(body))
	open, err := decoder.Token()
	if err != nil || open != json.Delim('{') {
		return nil, &Error{"invalid_body"}
	}
	out := map[string]json.RawMessage{}
	for decoder.More() {
		keyToken, err := decoder.Token()
		if err != nil {
			return nil, &Error{"invalid_body"}
		}
		key, ok := keyToken.(string)
		if !ok {
			return nil, &Error{"invalid_body"}
		}
		if _, exists := out[key]; exists {
			return nil, &Error{"invalid_body"}
		}
		var raw json.RawMessage
		if err := decoder.Decode(&raw); err != nil {
			return nil, &Error{"invalid_body"}
		}
		out[key] = raw
	}
	if _, err := decoder.Token(); err != nil {
		return nil, &Error{"invalid_body"}
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return nil, &Error{"invalid_body"}
	}
	return out, nil
}

func strictString(raw json.RawMessage, nullable bool) (*string, error) {
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		if nullable {
			return nil, nil
		}
		return nil, &Error{"invalid_body"}
	}
	var value string
	if json.Unmarshal(raw, &value) != nil || !utf8.ValidString(value) {
		return nil, &Error{"invalid_body"}
	}
	return &value, nil
}

func strictBool(raw json.RawMessage) (bool, error) {
	// opened is typed control data: JSON null is not a boolean and must not
	// decode as a silent false.
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return false, &Error{"invalid_body"}
	}
	var value bool
	if json.Unmarshal(raw, &value) != nil {
		return false, &Error{"invalid_body"}
	}
	return value, nil
}

// PersonalInput is the closed client intent for one personal save. Page is the
// exact route page for page-position resources.
type PersonalInput struct {
	Kind         PersonalKind
	Page         string
	DefaultPage  *string
	PagePath     *string
	PageQuery    string
	PageFragment string
	Opened       bool
	Position     json.RawMessage
}

func decodePersonal(kind PersonalKind, body []byte) (PersonalInput, error) {
	fields, err := strictObject(body)
	if err != nil {
		return PersonalInput{}, err
	}
	out := PersonalInput{Kind: kind}
	switch kind {
	case ReaderPreference:
		if len(fields) != 1 {
			return PersonalInput{}, &Error{"invalid_body"}
		}
		raw, ok := fields["default_page"]
		if !ok {
			return PersonalInput{}, &Error{"invalid_body"}
		}
		if out.DefaultPage, err = strictString(raw, true); err != nil {
			return PersonalInput{}, err
		}
	case ReadingPosition:
		if len(fields) != 4 {
			return PersonalInput{}, &Error{"invalid_body"}
		}
		if out.PagePath, err = nullableField(fields, "page_path", true); err != nil {
			return PersonalInput{}, err
		}
		if out.PageQuery, err = requiredString(fields, "page_query"); err != nil {
			return PersonalInput{}, err
		}
		if out.PageFragment, err = requiredString(fields, "page_fragment"); err != nil {
			return PersonalInput{}, err
		}
		opened, ok := fields["opened"]
		if !ok {
			return PersonalInput{}, &Error{"invalid_body"}
		}
		if out.Opened, err = strictBool(opened); err != nil {
			return PersonalInput{}, err
		}
	case PagePosition:
		if len(fields) != 3 {
			return PersonalInput{}, &Error{"invalid_body"}
		}
		if out.PageQuery, err = requiredString(fields, "page_query"); err != nil {
			return PersonalInput{}, err
		}
		if out.PageFragment, err = requiredString(fields, "page_fragment"); err != nil {
			return PersonalInput{}, err
		}
		position, ok := fields["position"]
		if !ok || len(position) == 0 || position[0] != '{' {
			return PersonalInput{}, &Error{"invalid_body"}
		}
		if !json.Valid(position) {
			return PersonalInput{}, &Error{"invalid_body"}
		}
		out.Position = position
	}
	return out, nil
}

func nullableField(fields map[string]json.RawMessage, name string, nullable bool) (*string, error) {
	raw, ok := fields[name]
	if !ok {
		return nil, &Error{"invalid_body"}
	}
	return strictString(raw, nullable)
}

func requiredString(fields map[string]json.RawMessage, name string) (string, error) {
	value, err := nullableField(fields, name, false)
	if err != nil {
		return "", err
	}
	return *value, nil
}

// clientValue is the fingerprint-relevant canonical client intent; server
// timestamps and route identity are added when the stored value is built.
func (in PersonalInput) clientValue() []byte {
	switch in.Kind {
	case ReaderPreference:
		return encodeJSON(map[string]any{"default_page": nullableString(in.DefaultPage)})
	case ReadingPosition:
		return encodeJSON(map[string]any{"page_path": nullableString(in.PagePath), "page_query": in.PageQuery, "page_fragment": in.PageFragment, "opened": in.Opened})
	default:
		return encodeJSON(map[string]any{"page_query": in.PageQuery, "page_fragment": in.PageFragment, "position": json.RawMessage(in.Position)})
	}
}

func nullableString(value *string) any {
	if value == nil {
		return nil
	}
	return *value
}

func encodeJSON(value any) []byte {
	out, _ := json.Marshal(value)
	return out
}

// resolvePageTx resolves a page path inside the caller's transaction to its
// CANONICAL stored identity: the exact or unique NFC-equivalent stored entry,
// following only in-version symlinks with cycle detection, requiring a regular
// HTML file. Aliases and canonical-equivalent spellings therefore share one
// per-page resource identity, per the viewer page-identity contract.
func (s *Service) resolvePageTx(ctx context.Context, tx app.WriteTx, version, contentSet, name string) (string, error) {
	entry, err := s.lookupTx(ctx, tx, version, contentSet, name)
	if err != nil {
		return "", err
	}
	seen := map[string]bool{}
	for entry.Kind == "symlink" {
		if seen[entry.Path] || strings.HasPrefix(entry.LinkText, "/") || !utf8.ValidString(entry.LinkText) || strings.ContainsRune(entry.LinkText, 0) {
			return "", &Missing{Reason: "unresolved_link"}
		}
		seen[entry.Path] = true
		target := path.Clean(path.Join(path.Dir(entry.Path), entry.LinkText))
		if target == ".." || strings.HasPrefix(target, "../") {
			return "", &Missing{Reason: "unresolved_link"}
		}
		entry, err = s.lookupTx(ctx, tx, version, contentSet, target)
		if err != nil {
			var invalid *Error
			if errors.Is(err, domain.ErrNotFound) || errors.As(err, &invalid) && invalid.Reason == "invalid_path" {
				return "", &Missing{Reason: "unresolved_link"}
			}
			return "", err
		}
	}
	if entry.Kind != "file" || !isHTML(entry.Path) {
		return "", &Error{"page_is_not_html"}
	}
	return entry.Path, nil
}

// PersonalGet reads one personal resource; absent state uses the explicit
// exists/create-precondition shape.
func (s *Service) PersonalGet(ctx context.Context, token, version string, kind PersonalKind, page string) (PersonalView, error) {
	if !kind.valid() {
		return PersonalView{}, &Error{"invalid_kind"}
	}
	if kind == PagePosition && page == "" {
		return PersonalView{}, &Error{"page_required"}
	}
	var out PersonalView
	err := s.read(ctx, token, personalTarget(version, kind, page), func(tx app.WriteTx) error {
		actor, err := s.Auth.AuthenticateWrite(ctx, tx, token)
		if err != nil {
			return err
		}
		// Personal reader state is owner-only; scoped credentials are denied
		// even with version read anchors.
		if actor.Scope.Kind != "owner" {
			return domain.ErrDenied
		}
		v, err := visibleVersion(tx, version)
		if err != nil {
			return err
		}
		if kind == PagePosition {
			// Canonicalize the requested page so aliases, NFC-equivalent
			// spellings and symlink names address one resource; an unknown
			// or invalid page fails exactly like the stored-path contract.
			page, err = s.resolvePageTx(ctx, tx, version, v.ContentSetID, page)
			if err != nil {
				return err
			}
		}
		stored, err := tx.ResourceRow(kind.resource(), personalID(actor.PrincipalID, version, page))
		if errors.Is(err, domain.ErrNotFound) {
			out = PersonalView{Exists: false, Value: absentPersonalValue(kind), RequiredPrecondition: "if_none_match_star"}
			return nil
		}
		if err != nil {
			return err
		}
		etagValue := personalETag(string(kind), personalID(actor.PrincipalID, version, page), stored.Revision, stored.Value)
		revision := stored.Revision
		view := PersonalView{Exists: true, Value: json.RawMessage(stored.Value), Revision: &revision, ETag: &etagValue}
		if kind == ReadingPosition {
			view.PagePositionURL = pointerPageLink(version, stored.Value)
		}
		out = view
		return nil
	})
	return out, err
}

// PersonalPut performs one durable K personal save. create selects the
// If-None-Match:* create precondition; otherwise etag is the exact opaque
// validator previously returned to this client.
func (s *Service) PersonalPut(ctx context.Context, token, key, version string, kind PersonalKind, page, etag string, create bool, body []byte) (domain.Response, error) {
	if !kind.valid() {
		return domain.Response{}, &Error{"invalid_kind"}
	}
	if kind == PagePosition && page == "" {
		return domain.Response{}, &Error{"page_required"}
	}
	input, err := decodePersonal(kind, body)
	if err != nil {
		return domain.Response{}, err
	}
	if kind == PagePosition {
		input.Page = page
	}
	actor, err := s.Auth.Authenticate(ctx, token)
	if err != nil {
		return domain.Response{}, err
	}
	if err := validOperationKey(key); err != nil {
		return domain.Response{}, err
	}
	request := domain.Request{
		Key:           domain.OperationKey{PrincipalID: actor.PrincipalID, OperationID: key},
		Method:        "PUT",
		Target:        personalTarget(version, kind, page),
		Body:          input.clientValue(),
		Preconditions: map[string]string{},
	}
	if create {
		request.Preconditions["if-none-match"] = "*"
	} else {
		request.Preconditions["if-match"] = etag
	}
	return app.Execute(ctx, s.Store, s.Auth.Authority(token), request, nil, func(tx app.WriteTx, receipt domain.Receipt) (domain.Response, error) {
		writer, err := s.Auth.AuthenticateWrite(ctx, tx, token)
		if err != nil {
			return domain.Response{}, err
		}
		if writer.Scope.Kind != "owner" {
			return domain.Response{}, domain.ErrDenied
		}
		// Resolve without the published-visibility filter first so an
		// authorized writer on a trashed document sees the explicit lifecycle
		// transition instead of a nondisclosing 404.
		v, err := tx.Version(version)
		if err != nil {
			return domain.Response{}, err
		}
		document, err := tx.Document(v.DocumentID)
		if err != nil {
			return domain.Response{}, err
		}
		if document.Lifecycle == "trash" || document.Lifecycle == "purged" {
			return domain.Response{}, &Transition{document.Lifecycle}
		}
		// Canonicalize every stored page reference to the resolved stored
		// entry so aliases cannot fork per-page resources.
		if kind == PagePosition {
			canonical, err := s.canonicalRefTx(ctx, tx, version, v.ContentSetID, &input.Page, true)
			if err != nil {
				return domain.Response{}, err
			}
			input.Page = *canonical
		} else {
			// A whole-version pointer keeps a null page; only a present page
			// reference is resolved to its canonical stored identity.
			if input.PagePath, err = s.canonicalRefTx(ctx, tx, version, v.ContentSetID, input.PagePath, false); err != nil {
				return domain.Response{}, err
			}
			if kind == ReaderPreference {
				// A null preference clears the override; a present page must resolve.
				if input.DefaultPage, err = s.canonicalRefTx(ctx, tx, version, v.ContentSetID, input.DefaultPage, false); err != nil {
					return domain.Response{}, err
				}
			}
		}
		id := personalID(writer.PrincipalID, version, "")
		if kind == PagePosition {
			id = personalID(writer.PrincipalID, version, input.Page)
		}
		stored, err := tx.ResourceRow(kind.resource(), id)
		expected := int64(0)
		if create {
			if err == nil {
				return domain.Response{}, domain.ErrPrecondition
			}
			if !errors.Is(err, domain.ErrNotFound) {
				return domain.Response{}, err
			}
		} else {
			if errors.Is(err, domain.ErrNotFound) {
				return domain.Response{}, domain.ErrPrecondition
			}
			if err != nil {
				return domain.Response{}, err
			}
			if personalETag(string(kind), id, stored.Revision, stored.Value) != etag {
				return domain.Response{}, domain.ErrPrecondition
			}
			expected = stored.Revision
		}
		value, err := s.storedValue(kind, input, stored, create)
		if err != nil {
			return domain.Response{}, err
		}
		mutated, err := tx.MutateResource(domain.Resource{Kind: kind.resource(), ID: id, Value: value}, expected, "resource.invalidated")
		if err != nil {
			return domain.Response{}, err
		}
		etagValue := personalETag(string(kind), id, mutated.Revision, mutated.Value)
		revision := mutated.Revision
		view := PersonalView{Exists: true, Value: json.RawMessage(mutated.Value), Revision: &revision, ETag: &etagValue}
		if kind == ReadingPosition {
			view.PagePositionURL = pointerPageLink(version, mutated.Value)
		}
		return domain.Response{Status: 200, Body: encodeJSON(view), Headers: map[string]string{"ETag": etagValue}}, nil
	})
}

// canonicalRefTx resolves an optional or required page reference to its
// canonical stored path. A required reference must resolve; an optional nil
// stays nil (whole-version/null binding).
func (s *Service) canonicalRefTx(ctx context.Context, tx app.WriteTx, version, contentSet string, value *string, required bool) (*string, error) {
	if value == nil {
		if required {
			return nil, domain.ErrNotFound
		}
		return nil, nil
	}
	canonical, err := s.resolvePageTx(ctx, tx, version, contentSet, *value)
	if err != nil {
		return nil, err
	}
	return &canonical, nil
}

// storedValue builds the durable value; reading-position timestamps are
// service-assigned with the first observation preserved across updates.
func (s *Service) storedValue(kind PersonalKind, input PersonalInput, current domain.Resource, created bool) ([]byte, error) {
	if kind != ReadingPosition {
		return input.clientValue(), nil
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	first := now
	if !created {
		var existing struct {
			FirstOpenedAt string `json:"first_opened_at"`
		}
		if json.Unmarshal(current.Value, &existing) == nil && existing.FirstOpenedAt != "" {
			first = existing.FirstOpenedAt
		}
	}
	return encodeJSON(map[string]any{"page_path": nullableString(input.PagePath), "page_query": input.PageQuery, "page_fragment": input.PageFragment, "opened": input.Opened, "first_opened_at": first, "last_opened_at": now}), nil
}

func validOperationKey(key string) error {
	if key == "" {
		return &Error{"operation_key_required"}
	}
	// Canonical UUID shape matches the shared operation-identity contract.
	for i, r := range key {
		switch i {
		case 8, 13, 18, 23:
			if r != '-' {
				return &Error{"operation_key_required"}
			}
		default:
			if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
				return &Error{"operation_key_required"}
			}
		}
	}
	if len(key) != 36 {
		return &Error{"operation_key_required"}
	}
	return nil
}
