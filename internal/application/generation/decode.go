package generation

import (
	"bufio"
	"bytes"
	"encoding/json"
	"haeryong.local/haeryong/internal/security/session"
	"io"
	"unicode/utf8"
)

// Generation has explicit nullable document/base and reference page/region
// fields. Keep that closed shape local instead of relaxing unrelated APIs.
func DecodeRequest(r io.Reader) (Request, error) {
	return decodeRequest(r, nil)
}

// DecodeRequestWithTarget supplies trusted remembered CLI association only for
// omitted targets. Explicit file fields keep their own meaning.
func DecodeRequestWithTarget(r io.Reader, project string, document *string) (Request, error) {
	return decodeRequest(r, map[string]any{"project_id": project, "document_id": document})
}

func decodeRequest(r io.Reader, defaults map[string]any) (Request, error) {
	var input Request
	d := json.NewDecoder(&requestTextReader{reader: bufio.NewReader(r)})
	d.UseNumber()
	raw, e := requestJSONValue(d)
	if e != nil {
		return input, e
	}
	var extra any
	if d.Decode(&extra) != io.EOF {
		return input, session.ErrInvalid
	}
	fields, ok := raw.(map[string]any)
	if !ok {
		return input, session.ErrInvalid
	}
	for key, value := range defaults {
		if _, present := fields[key]; !present {
			fields[key] = value
		}
	}
	for _, key := range []string{"project_id", "document_id", "intent", "source_selection"} {
		if _, ok := fields[key]; !ok {
			return input, session.ErrInvalid
		}
	}
	for key, v := range fields {
		if v == nil && key != "document_id" && key != "base_version_id" {
			return input, session.ErrInvalid
		}
	}
	_, prompt := fields["prompt"]
	_, ref := fields["body_ref"]
	if prompt == ref {
		return input, session.ErrInvalid
	}
	if values, ok := fields["explanation_references"]; ok {
		refs, ok := values.([]any)
		if !ok {
			return input, session.ErrInvalid
		}
		for _, value := range refs {
			r, ok := value.(map[string]any)
			if !ok || len(r) != 4 {
				return input, session.ErrInvalid
			}
			for _, key := range []string{"version_id", "page_path", "region_id", "bytes_sha256"} {
				if _, ok := r[key]; !ok {
					return input, session.ErrInvalid
				}
			}
		}
	}
	data, e := json.Marshal(fields)
	if e != nil {
		return input, e
	}
	strict := json.NewDecoder(bytes.NewReader(data))
	strict.DisallowUnknownFields()
	if strict.Decode(&input) != nil {
		return input, session.ErrInvalid
	}
	input.DocumentPresent = true
	_, input.BasePresent = fields["base_version_id"]
	return input, nil
}
func requestJSONValue(d *json.Decoder) (any, error) {
	token, e := d.Token()
	if e != nil {
		return nil, session.ErrInvalid
	}
	switch token {
	case json.Delim('{'):
		fields := map[string]any{}
		for d.More() {
			key, e := d.Token()
			if e != nil {
				return nil, session.ErrInvalid
			}
			name, ok := key.(string)
			if !ok {
				return nil, session.ErrInvalid
			}
			if _, exists := fields[name]; exists {
				return nil, session.ErrInvalid
			}
			value, e := requestJSONValue(d)
			if e != nil {
				return nil, e
			}
			fields[name] = value
		}
		if token, e = d.Token(); e != nil || token != json.Delim('}') {
			return nil, session.ErrInvalid
		}
		return fields, nil
	case json.Delim('['):
		values := []any{}
		for d.More() {
			value, e := requestJSONValue(d)
			if e != nil {
				return nil, e
			}
			values = append(values, value)
		}
		if token, e = d.Token(); e != nil || token != json.Delim(']') {
			return nil, session.ErrInvalid
		}
		return values, nil
	}
	if _, ok := token.(json.Delim); ok {
		return nil, session.ErrInvalid
	}
	return token, nil
}

// JSON escape syntax remains encoding/json's responsibility; this reader only
// rejects malformed raw UTF-8 without a total authored-content limit.
type requestTextReader struct {
	reader  *bufio.Reader
	pending []byte
}

func (r *requestTextReader) Read(p []byte) (int, error) {
	n := 0
	for n < len(p) {
		if len(r.pending) > 0 {
			count := copy(p[n:], r.pending)
			n += count
			r.pending = r.pending[count:]
			continue
		}
		value, size, e := r.reader.ReadRune()
		if e != nil {
			return n, e
		}
		if value == utf8.RuneError && size == 1 {
			return n, session.ErrInvalid
		}
		r.pending = utf8.AppendRune(r.pending[:0], value)
	}
	return n, nil
}
