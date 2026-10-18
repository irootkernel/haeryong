package importer

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	sec "haeryong.local/haeryong/internal/security/session"
	"io"
	"unicode/utf8"
)

// ParseContextBinding decodes the fresh-proof binding header value: RawURL
// base64 of one UTF-8 JSON object carrying exactly version_id, page_path and
// render_instance_id, with JSON null as the only absent form of the optional
// tuple members (the sealed-upload target normalization family). Omitted or
// unknown fields, duplicate keys, non-string members, invalid identifiers
// and unsafe page paths are rejected; the literal string "null" stays a page
// path, never the null form, and no Unicode re-normalization is applied.
func ParseContextBinding(value string) (ContextQuery, error) {
	q := ContextQuery{}
	raw, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil || len(raw) == 0 || !utf8.Valid(raw) {
		return q, sec.ErrInvalid
	}
	// Canonical-alphabet check: Go's decoder skips CR/LF and accepts
	// non-canonical trailing bits, so only an exact encode round-trip admits
	// the value. This keeps the header form strict without any size quota.
	if base64.RawURLEncoding.EncodeToString(raw) != value {
		return q, sec.ErrInvalid
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	token, err := decoder.Token()
	if err != nil || token != json.Delim('{') {
		return q, sec.ErrInvalid
	}
	seen := map[string]bool{}
	for decoder.More() {
		token, err := decoder.Token()
		if err != nil {
			return q, sec.ErrInvalid
		}
		name, ok := token.(string)
		if !ok || seen[name] {
			return q, sec.ErrInvalid
		}
		seen[name] = true
		var member json.RawMessage
		if err := decoder.Decode(&member); err != nil {
			return q, sec.ErrInvalid
		}
		switch name {
		case "version_id":
			var version string
			if string(member) == "null" || json.Unmarshal(member, &version) != nil || !idPattern.MatchString(version) {
				return q, sec.ErrInvalid
			}
			q.VersionID = version
		case "page_path":
			if string(member) == "null" {
				continue
			}
			var page string
			if json.Unmarshal(member, &page) != nil || page == "" || ValidatePath(page) != nil {
				return q, sec.ErrInvalid
			}
			q.PagePath = &page
		case "render_instance_id":
			if string(member) == "null" {
				continue
			}
			var render string
			if json.Unmarshal(member, &render) != nil || !idPattern.MatchString(render) {
				return q, sec.ErrInvalid
			}
			q.RenderInstanceID = &render
		default:
			return q, sec.ErrInvalid
		}
	}
	if token, err := decoder.Token(); err != nil || token != json.Delim('}') || len(seen) != 3 {
		return q, sec.ErrInvalid
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return q, sec.ErrInvalid
	}
	return q, nil
}
