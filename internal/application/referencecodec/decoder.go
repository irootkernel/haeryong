// Package referencecodec decodes immutable reference metadata without auth,
// storage or service dependencies. Import and derived repositories share it.
package referencecodec

import (
	"bytes"
	"encoding/json"
	"fmt"
	"haeryong.local/haeryong/internal/domain"
	"io"
	"regexp"
	"unicode/utf8"
)

type Error struct{ Pointer string }

func (e *Error) Error() string  { return fmt.Sprintf("invalid reference at %s", e.Pointer) }
func invalid(path string) error { return &Error{Pointer: path} }

var serviceID = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
var referenceFields = map[string]bool{"id": true, "document_id": true, "version_id": true, "page_path": true, "region_id": true, "resolution": true, "label": true, "from": true, "relation": true, "page_query": true, "page_fragment": true}
var fromFields = map[string]bool{"page_path": true, "region_id": true}

func object(value any, fields map[string]bool, path string) (map[string]any, error) {
	m, ok := value.(map[string]any)
	if !ok {
		return nil, invalid(path)
	}
	for key := range m {
		if !fields[key] {
			return nil, invalid(path + "/" + key)
		}
	}
	return m, nil
}
func text(m map[string]any, key, path string, required, nullable, nonempty bool) (*string, error) {
	v, present := m[key]
	if !present {
		if required {
			return nil, invalid(path + "/" + key)
		}
		return nil, nil
	}
	if v == nil && nullable {
		return nil, nil
	}
	s, ok := v.(string)
	if !ok || (nonempty && s == "") {
		return nil, invalid(path + "/" + key)
	}
	return &s, nil
}
func pathText(m map[string]any, key, path string, nullable bool) (*string, error) {
	p, err := text(m, key, path, true, nullable, true)
	if err != nil {
		return nil, err
	}
	if !domain.ValidReferencePath(p) {
		return nil, invalid(path + "/" + key)
	}
	return p, nil
}
func DecodeReference(raw []byte) (domain.AuthoredReference, error) {
	var r domain.AuthoredReference
	if !utf8.Valid(raw) {
		return r, invalid("/references")
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	value, err := strictValue(d)
	if err != nil {
		return r, invalid("/references")
	}
	if _, err = d.Token(); err != io.EOF {
		return r, invalid("/references")
	}
	m, err := object(value, referenceFields, "/references")
	if err != nil {
		return r, err
	}
	for _, field := range []struct {
		key string
		out *string
	}{{"id", &r.ID}, {"document_id", &r.TargetDocumentID}, {"version_id", &r.TargetVersionID}} {
		p, err := text(m, field.key, "/references", true, false, true)
		if err != nil {
			return r, err
		}
		*field.out = *p
		if field.key != "id" && !serviceID.MatchString(*p) {
			return r, invalid("/references/" + field.key)
		}
	}
	r.TargetPagePath, err = pathText(m, "page_path", "/references", true)
	if err != nil {
		return r, err
	}
	r.TargetRegionID, err = text(m, "region_id", "/references", true, true, true)
	if err != nil {
		return r, err
	}
	if r.TargetRegionID != nil && r.TargetPagePath == nil {
		return r, invalid("/references/page_path")
	}
	if from, exists := m["from"]; exists {
		context, err := object(from, fromFields, "/references/from")
		if err != nil {
			return r, err
		}
		r.FromPagePath, err = pathText(context, "page_path", "/references/from", false)
		if err != nil {
			return r, err
		}
		r.FromRegionID, err = text(context, "region_id", "/references/from", true, true, true)
		if err != nil {
			return r, err
		}
	}
	r.Label, err = text(m, "label", "/references", false, false, false)
	if err != nil {
		return r, err
	}
	r.Relation, err = text(m, "relation", "/references", false, false, false)
	if err != nil {
		return r, err
	}
	for _, field := range []struct {
		key string
		out *string
	}{{"page_query", &r.PageQuery}, {"page_fragment", &r.PageFragment}} {
		p, err := text(m, field.key, "/references", false, false, false)
		if err != nil {
			return r, err
		}
		if p != nil {
			*field.out = *p
		}
	}
	if p, err := text(m, "resolution", "/references", false, false, false); err != nil {
		return r, err
	} else if p != nil {
		switch *p {
		case "resolved", "unresolved", "unavailable", "tombstoned":
		default:
			return r, invalid("/references/resolution")
		}
	}
	r.AuthoredJSON = bytes.Clone(raw)
	return r, nil
}

// JSON decoding must reject duplicate keys at every object depth before losing
// their identity in a map. Preserve invalid array values for field diagnostics.
func strictValue(d *json.Decoder) (any, error) {
	token, err := d.Token()
	if err != nil {
		return nil, err
	}
	delim, container := token.(json.Delim)
	if !container {
		return token, nil
	}
	if delim == '[' {
		values := []any{}
		for d.More() {
			value, err := strictValue(d)
			if err != nil {
				return nil, err
			}
			values = append(values, value)
		}
		end, err := d.Token()
		if err != nil || end != json.Delim(']') {
			return nil, invalid("/references")
		}
		return values, nil
	}
	if delim != '{' {
		return nil, invalid("/references")
	}
	obj := map[string]any{}
	for d.More() {
		key, err := d.Token()
		if err != nil {
			return nil, err
		}
		name, ok := key.(string)
		if !ok {
			return nil, invalid("/references")
		}
		if _, exists := obj[name]; exists {
			return nil, invalid("/references")
		}
		value, err := strictValue(d)
		if err != nil {
			return nil, err
		}
		obj[name] = value
	}
	end, err := d.Token()
	if err != nil || end != json.Delim('}') {
		return nil, invalid("/references")
	}
	return obj, nil
}
