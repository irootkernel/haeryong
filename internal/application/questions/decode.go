package questions

import (
	"bytes"
	"encoding/json"
	"io"
	"unicode/utf8"

	"haeryong.local/haeryong/internal/domain"
	sec "haeryong.local/haeryong/internal/security/session"
)

// decodeObject rejects duplicate keys before maps lose their identity. These
// contentful inputs have no authored-byte quota; the complete JSON is retained.
func decodeObject(r io.Reader, target any) (map[string]any, error) {
	raw, e := io.ReadAll(r)
	if e != nil {
		return nil, e
	}
	if !utf8.Valid(raw) {
		return nil, sec.ErrInvalid
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	v, e := jsonValue(d)
	if e != nil {
		return nil, e
	}
	if _, e = d.Token(); e != io.EOF {
		return nil, sec.ErrInvalid
	}
	fields, ok := v.(map[string]any)
	if !ok {
		return nil, sec.ErrInvalid
	}
	d = json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if d.Decode(target) != nil {
		return nil, sec.ErrInvalid
	}
	return fields, nil
}

func jsonValue(d *json.Decoder) (any, error) {
	t, e := d.Token()
	if e != nil {
		return nil, sec.ErrInvalid
	}
	switch t {
	case json.Delim('{'):
		m := map[string]any{}
		for d.More() {
			key, e := d.Token()
			if e != nil {
				return nil, sec.ErrInvalid
			}
			name, ok := key.(string)
			if !ok {
				return nil, sec.ErrInvalid
			}
			if _, exists := m[name]; exists {
				return nil, sec.ErrInvalid
			}
			m[name], e = jsonValue(d)
			if e != nil {
				return nil, e
			}
		}
		if end, e := d.Token(); e != nil || end != json.Delim('}') {
			return nil, sec.ErrInvalid
		}
		return m, nil
	case json.Delim('['):
		a := []any{}
		for d.More() {
			v, e := jsonValue(d)
			if e != nil {
				return nil, e
			}
			a = append(a, v)
		}
		if end, e := d.Token(); e != nil || end != json.Delim(']') {
			return nil, sec.ErrInvalid
		}
		return a, nil
	}
	if _, delimiter := t.(json.Delim); delimiter {
		return nil, sec.ErrInvalid
	}
	return t, nil
}

func DecodeRequest(r io.Reader) (Request, error) {
	return decodeRequest(r, false)
}

// CLI whole-version defaults fill only omitted nullable anchors. The public
// HTTP request keeps requiring its explicit captured tuple.
func DecodeCLIRequest(r io.Reader) (Request, error) { return decodeRequest(r, true) }
func decodeRequest(r io.Reader, defaults bool) (Request, error) {
	var input Request
	fields, e := decodeObject(r, &input)
	if e != nil {
		return input, e
	}
	for _, name := range []string{"page_path", "region_id"} {
		if _, ok := fields[name]; !ok && !defaults {
			return input, sec.ErrInvalid
		}
	}
	for name, value := range fields {
		if value == nil && name != "page_path" && name != "region_id" {
			return input, sec.ErrInvalid
		}
	}
	_, text := fields["question"]
	_, ref := fields["body_ref"]
	if text == ref {
		return input, sec.ErrInvalid
	}
	_, input.SourcesPresent = fields["source_selection"]
	return input, nil
}

// MarshalRequest preserves omitted source selection, so an explicit mode can
// still resolve the version's exact default targets once at admission.
func MarshalRequest(input Request) ([]byte, error) {
	type wire Request
	raw, e := json.Marshal(wire(input))
	if e != nil {
		return nil, e
	}
	var fields map[string]json.RawMessage
	if e = json.Unmarshal(raw, &fields); e != nil {
		return nil, e
	}
	if !input.SourcesPresent {
		delete(fields, "source_selection")
	}
	return json.Marshal(fields)
}

func decodeAnswer(r io.Reader) (domain.AnswerEnvelope, error) {
	var output domain.AnswerEnvelope
	fields, e := decodeObject(r, &output)
	if e != nil {
		return output, e
	}
	for _, name := range []string{"state", "body", "citations", "explanation_references", "limitations"} {
		if value, exists := fields[name]; !exists || value == nil {
			return output, sec.ErrInvalid
		}
	}
	// Nullable union members must be present as explicit null or their typed
	// value. Missing fields do not acquire an observation/verification meaning.
	for collection, names := range map[string][]string{"citations": {"basis", "read_id", "verification_id", "input_origin", "target_set_id", "source", "location", "bytes_sha256"}, "explanation_references": {"version_id", "page_path", "region_id", "bytes_sha256"}, "limitations": {"code", "message"}} {
		rows, ok := fields[collection].([]any)
		if !ok {
			return output, sec.ErrInvalid
		}
		for _, row := range rows {
			object, ok := row.(map[string]any)
			if !ok {
				return output, sec.ErrInvalid
			}
			for _, name := range names {
				if _, exists := object[name]; !exists {
					return output, sec.ErrInvalid
				}
			}
		}
	}
	for _, citation := range output.Citations {
		if citation.Source == nil || citation.Location == nil {
			return output, sec.ErrInvalid
		}
	}
	return output, nil
}
