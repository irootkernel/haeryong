package importer

import (
	"bufio"
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	app "haeryong.local/haeryong/internal/application/storage"
	"haeryong.local/haeryong/internal/domain"
	"io"
	"math/big"
	"regexp"
	"strings"
)

//go:embed metadata-schema.json
var metadataSchema []byte
var metadataDefinitions map[string]map[string]any

func init() {
	if err := json.Unmarshal(metadataSchema, &metadataDefinitions); err != nil {
		panic(err)
	}
}

type MetadataError struct{ Pointer string }

func (e *MetadataError) Error() string { return "PACKAGE_INVALID at " + e.Pointer }
func (e *MetadataError) Unwrap() error { return ErrPackageInvalid }
func invalidMeta(path string) error    { return &MetadataError{Pointer: path} }
func whitespace(r *bufio.Reader) error {
	for {
		b, e := r.Peek(1)
		if e == io.EOF {
			return nil
		}
		if e != nil {
			return e
		}
		if !strings.ContainsRune(" \t\r\n", rune(b[0])) {
			return nil
		}
		_, _ = r.ReadByte()
	}
}
func expectJSON(r *bufio.Reader, want byte) error {
	if e := whitespace(r); e != nil {
		return e
	}
	b, e := r.ReadByte()
	if e != nil || b != want {
		return ErrPackageInvalid
	}
	return nil
}
func scalarJSON(r *bufio.Reader) (json.RawMessage, error) {
	var b bytes.Buffer
	if e := CopyJSONValue(r, &b); e != nil {
		return nil, e
	}
	return b.Bytes(), nil
}
func objectJSON(r *bufio.Reader, field func(string, *bufio.Reader) error) error {
	if e := expectJSON(r, '{'); e != nil {
		return e
	}
	seen := map[string]bool{}
	if e := whitespace(r); e != nil {
		return e
	}
	peek, e := r.Peek(1)
	if e != nil {
		return ErrPackageInvalid
	}
	if peek[0] == '}' {
		_, _ = r.ReadByte()
		return nil
	}
	for {
		raw, e := scalarJSON(r)
		if e != nil {
			return e
		}
		var key string
		if json.Unmarshal(raw, &key) != nil || seen[key] {
			return ErrPackageInvalid
		}
		seen[key] = true
		if e := expectJSON(r, ':'); e != nil {
			return e
		}
		if e := field(key, r); e != nil {
			return e
		}
		if e := whitespace(r); e != nil {
			return e
		}
		b, e := r.ReadByte()
		if e != nil {
			return ErrPackageInvalid
		}
		if b == '}' {
			return nil
		}
		if b != ',' {
			return ErrPackageInvalid
		}
		if e := whitespace(r); e != nil {
			return e
		}
	}
}
func validateShape(value any, schema map[string]any) bool {
	if ref, ok := schema["$ref"].(string); ok {
		return validateShape(value, metadataDefinitions[strings.TrimPrefix(ref, "#/$defs/")])
	}
	if choices, ok := schema["anyOf"].([]any); ok {
		for _, c := range choices {
			if validateShape(value, c.(map[string]any)) {
				return true
			}
		}
		return false
	}
	if values, ok := schema["enum"].([]any); ok {
		for _, v := range values {
			if fmt.Sprint(v) == fmt.Sprint(value) && fmt.Sprintf("%T", v) == fmt.Sprintf("%T", value) {
				return true
			}
		}
		return false
	}
	kind, _ := schema["type"].(string)
	switch kind {
	case "null":
		return value == nil
	case "string":
		s, ok := value.(string)
		if !ok {
			return false
		}
		if n, ok := schema["minLength"].(float64); ok && len([]rune(s)) < int(n) {
			return false
		}
		if pattern, ok := schema["pattern"].(string); ok {
			match, e := regexp.MatchString(pattern, s)
			if e != nil || !match {
				return false
			}
		}
		if schema["format"] == "uuid" && !idPattern.MatchString(s) {
			return false
		}
		if schema["format"] == "relative-path" && ValidatePath(s) != nil {
			return false
		}
		return true
	case "integer":
		n, ok := value.(json.Number)
		if !ok {
			return false
		}
		integer, ok := new(big.Int).SetString(string(n), 10)
		if !ok {
			return false
		}
		if minimum, ok := schema["minimum"].(float64); ok && integer.Cmp(big.NewInt(int64(minimum))) < 0 {
			return false
		}
		return true
	case "object":
		v, ok := value.(map[string]any)
		if !ok {
			return false
		}
		props, _ := schema["properties"].(map[string]any)
		for key, x := range v {
			shape, ok := props[key]
			if !ok {
				return false
			}
			if !validateShape(x, shape.(map[string]any)) {
				return false
			}
		}
		if required, ok := schema["required"].([]any); ok {
			for _, key := range required {
				if _, ok := v[key.(string)]; !ok {
					return false
				}
			}
		}
		return true
	case "array":
		v, ok := value.([]any)
		if !ok {
			return false
		}
		for _, x := range v {
			if !validateShape(x, schema["items"].(map[string]any)) {
				return false
			}
		}
		return true
	}
	return false
}
func decodeMeta(raw []byte, definition string) (map[string]any, error) {
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	value, e := strictMetaValue(d)
	if e != nil || !validateShape(value, metadataDefinitions[definition]) {
		return nil, ErrPackageInvalid
	}

	obj, ok := value.(map[string]any)
	if !ok {
		return nil, ErrPackageInvalid
	}
	return obj, nil
}
func strictMetaValue(d *json.Decoder) (any, error) {
	token, e := d.Token()
	if e != nil {
		return nil, e
	}
	delimiter, ok := token.(json.Delim)
	if !ok {
		return token, nil
	}
	switch delimiter {
	case '{':
		out := map[string]any{}
		for d.More() {
			key, e := d.Token()
			if e != nil {
				return nil, e
			}
			name, ok := key.(string)
			if !ok {
				return nil, ErrPackageInvalid
			}
			if _, ok := out[name]; ok {
				return nil, ErrPackageInvalid
			}
			value, e := strictMetaValue(d)
			if e != nil {
				return nil, e
			}
			out[name] = value
		}
		if end, e := d.Token(); e != nil || end != json.Delim('}') {
			return nil, ErrPackageInvalid
		}
		return out, nil
	case '[':
		out := []any{}
		for d.More() {
			value, e := strictMetaValue(d)
			if e != nil {
				return nil, e
			}
			out = append(out, value)
		}
		if end, e := d.Token(); e != nil || end != json.Delim(']') {
			return nil, ErrPackageInvalid
		}
		return out, nil
	}
	return nil, ErrPackageInvalid
}
func (s *Service) indexMetadata(ctx context.Context, j domain.Job, i domain.ImportIntent, v *domain.Version) error {
	var obj domain.Object
	e := s.Store.Write(ctx, func(tx app.WriteTx) error {
		if e := s.jobFence(tx, j); e != nil {
			return e
		}
		index, e := tx.MetadataIndex(j.ID)
		if e != nil {
			return e
		}
		if !index.Complete || index.JobID != j.ID || index.Hash != i.MetadataHash || index.ContentSetID != i.ContentSetID {
			return domain.ErrTransition
		}
		obj, e = tx.Object(i.MetadataHash)
		return e
	})
	if e != nil {
		return e
	}
	return s.Bytes.Read(ctx, obj, domain.Pin{Kind: "job", OwnerID: j.ID}, func(reader io.ReadSeeker) error { return s.parseMetadata(ctx, bufio.NewReader(reader), i, v, nil) })
}
func (s *Service) parseMetadata(ctx context.Context, r *bufio.Reader, i domain.ImportIntent, v *domain.Version, sink func(string, string, []byte) error) error {
	err := objectJSON(r, func(key string, r *bufio.Reader) error {
		switch key {
		case "schema_version":
			raw, e := scalarJSON(r)
			if e != nil || string(bytes.Trim(raw, " \t\r\n")) != `"1.0"` {
				return invalidMeta("/schema_version")
			}
		case "document", "version", "content":
			shape := metadataDefinitions["metadata"]["properties"].(map[string]any)[key].(map[string]any)
			properties := shape["properties"].(map[string]any)
			return objectJSON(r, func(field string, r *bufio.Reader) error {
				rule, ok := properties[field]
				if !ok {
					return invalidMeta("/" + key + "/" + field)
				}
				if key == "document" && field == "question" {
					if e := whitespace(r); e != nil {
						return e
					}
					b, e := r.Peek(1)
					if e != nil || b[0] != '"' && b[0] != 'n' {
						return invalidMeta("/document/question")
					}
					return CopyJSONValue(r, io.Discard)
				}
				if key == "content" && field == "external_dependencies" {
					return metadataArray(r, func(raw []byte) error { _, e := decodeMeta(raw, "dependency"); return e })
				}
				raw, e := scalarJSON(r)
				if e != nil {
					return e
				}
				var value any
				d := json.NewDecoder(bytes.NewReader(raw))
				d.UseNumber()
				if d.Decode(&value) != nil || !validateShape(value, rule.(map[string]any)) {
					return invalidMeta("/" + key + "/" + field)
				}
				str, _ := value.(string)
				switch key + "/" + field {
				case "document/id":
					if i.DocumentID != "" && str != i.DocumentID {
						return invalidMeta("/document/id")
					}
				case "document/project_id":
					if i.ProjectID != "" && str != i.ProjectID {
						return invalidMeta("/document/project_id")
					}
				case "document/title":
					v.Title = str
				case "version/id":
					if i.VersionID != "" && str != i.VersionID {
						return invalidMeta("/version/id")
					}
				case "version/base_version_id":
					if i.DocumentID != "" && str != i.BaseVersionID {
						return invalidMeta("/version/base_version_id")
					}
				case "content/default_page":
					if value != nil {
						v.DefaultPage = &str
					}
				}
				return nil
			})
		case "pages", "regions", "sources", "evidence", "references":
			definition := map[string]string{"pages": "page", "regions": "region", "sources": "source", "evidence": "evidence", "references": "reference"}[key]
			return metadataArray(r, func(raw []byte) error {
				item, e := decodeMeta(raw, definition)
				if e != nil {
					return invalidMeta("/" + key)
				}
				id, _ := item["id"].(string)
				path, _ := item["page_path"].(string)
				if key == "pages" {
					id = path
				}
				if key == "regions" {
					id = path + "\x00" + id
				}
				if key == "evidence" {
					v.SourceBasis = "evidence_recorded"
				}
				if e := validateMetadataItem(key, item); e != nil {
					return invalidMeta("/" + key)
				}
				if sink == nil {
					return nil
				}
				if e := sink(key, id, raw); e != nil {
					if errors.Is(e, domain.ErrConflict) {
						return invalidMeta("/" + key)
					}
					return e
				}
				return nil
			})
		case "generation":
			raw, e := scalarJSON(r)
			if e != nil {
				return e
			}
			_, e = decodeMeta(raw, "generation")
			return e
		default:
			return invalidMeta("/" + key)
		}
		return nil
	})
	if err != nil {
		return err
	}
	if e := whitespace(r); e != nil {
		return e
	}
	if _, e := r.Peek(1); e != io.EOF {
		return ErrPackageInvalid
	}
	return nil
}
func metadataArray(r *bufio.Reader, item func([]byte) error) error {
	if e := expectJSON(r, '['); e != nil {
		return e
	}
	if e := whitespace(r); e != nil {
		return e
	}
	p, e := r.Peek(1)
	if e != nil {
		return ErrPackageInvalid
	}
	if p[0] == ']' {
		_, _ = r.ReadByte()
		return nil
	}
	for {
		raw, e := scalarJSON(r)
		if e != nil {
			return e
		}
		if e := item(raw); e != nil {
			return e
		}
		if e := whitespace(r); e != nil {
			return e
		}
		b, e := r.ReadByte()
		if e != nil {
			return ErrPackageInvalid
		}
		if b == ']' {
			return nil
		}
		if b != ',' {
			return ErrPackageInvalid
		}
	}
}

// copyField reuses the one verified seekable delivery handle; omitted fields
// remain omitted, and arbitrarily long authored values stream directly.
func copyField(reader io.ReadSeeker, path []string, out io.Writer) (bool, error) {
	if _, e := reader.Seek(0, io.SeekStart); e != nil {
		return false, e
	}
	found := false
	var walk func(*bufio.Reader, []string) error
	walk = func(r *bufio.Reader, path []string) error {
		return objectJSON(r, func(key string, r *bufio.Reader) error {
			if key != path[0] {
				return CopyJSONValue(r, io.Discard)
			}
			if len(path) == 1 {
				found = true
				return CopyJSONValue(r, out)
			}
			return walk(r, path[1:])
		})
	}
	e := walk(bufio.NewReader(reader), path)
	return found, e
}
func writeGenerated(out io.Writer, i domain.ImportIntent, v domain.Version, metadata io.ReadSeeker) error {
	w := bufio.NewWriter(out)
	write := func(text string) error { _, e := io.WriteString(w, text); return e }
	field := func(key string, value any) error {
		if e := write(key + ":"); e != nil {
			return e
		}
		_, e := w.Write(encoded(value))
		return e
	}
	if e := write(`{"schema_version":"1.0","viewer_profile":"html_js_v1","source_basis":` + string(encoded(v.SourceBasis)) + `,"document":{`); e != nil {
		return e
	}
	if e := field(`"id"`, v.DocumentID); e != nil {
		return e
	}
	if e := field(`,"project_id"`, i.ProjectID); e != nil {
		return e
	}
	if e := field(`,"title"`, v.Title); e != nil {
		return e
	}
	if metadata != nil {
		var exists bool
		var e error
		exists, e = copyField(metadata, []string{"document", "question"}, io.Discard)
		if e != nil {
			return e
		}
		if exists {
			if e := write(`,"question":`); e != nil {
				return e
			}
			if _, e := copyField(metadata, []string{"document", "question"}, w); e != nil {
				return e
			}
		}
	}
	if e := write(`},"version":{`); e != nil {
		return e
	}
	if e := field(`"id"`, v.ID); e != nil {
		return e
	}
	if e := field(`,"base_version_id"`, strptr(v.BaseVersionID)); e != nil {
		return e
	}
	if e := field(`,"created_at"`, i.CreatedAt); e != nil {
		return e
	}
	if e := write(`,"source_intent":`); e != nil {
		return e
	}
	if metadata != nil {
		found, e := copyField(metadata, []string{"version", "source_intent"}, w)
		if e != nil {
			return e
		}
		if !found {
			if e := write("null"); e != nil {
				return e
			}
		}
	} else {
		if e := write("null"); e != nil {
			return e
		}
	}
	if e := write(`},"content":{`); e != nil {
		return e
	}
	if e := field(`"default_page"`, v.DefaultPage); e != nil {
		return e
	}
	if e := field(`,"inventory"`, "/api/v1/versions/"+v.ID+"/files"); e != nil {
		return e
	}
	if e := field(`,"payload_sha256"`, v.PayloadSHA); e != nil {
		return e
	}
	if metadata != nil {
		found, e := copyField(metadata, []string{"content", "external_dependencies"}, io.Discard)
		if e != nil {
			return e
		}
		if found {
			if e := write(`,"external_dependencies":`); e != nil {
				return e
			}
			if _, e := copyField(metadata, []string{"content", "external_dependencies"}, w); e != nil {
				return e
			}
		}
	}
	if e := write(`}`); e != nil {
		return e
	}
	if metadata != nil {
		for _, key := range []string{"pages", "regions", "sources", "evidence", "references", "generation"} {
			found, e := copyField(metadata, []string{key}, io.Discard)
			if e != nil {
				return e
			}
			if found {
				if e := write("," + string(encoded(key)) + ":"); e != nil {
					return e
				}
				if _, e := copyField(metadata, []string{key}, w); e != nil {
					return e
				}
			}
		}
	}
	if e := write(`}`); e != nil {
		return e
	}
	return w.Flush()
}
func (s *Service) generated(ctx context.Context, j domain.Job, i domain.ImportIntent, v domain.Version) (domain.Object, error) {
	reader, writer := io.Pipe()
	done := make(chan error, 1)
	go func() {
		var e error
		if i.MetadataHash == "" {
			e = writeGenerated(writer, i, v, nil)
		} else {
			var object domain.Object
			e = s.Store.Write(ctx, func(tx app.WriteTx) error { var e error; object, e = tx.Object(i.MetadataHash); return e })
			if e == nil {
				e = s.Bytes.Read(ctx, object, domain.Pin{Kind: "job", OwnerID: j.ID}, func(r io.ReadSeeker) error { return writeGenerated(writer, i, v, r) })
			}
		}
		_ = writer.CloseWithError(e)
		done <- e
		close(done)
	}()
	object, e := s.Bytes.Put(ctx, &fencedReader{Reader: reader, ctx: ctx, service: s, job: j}, func(tx app.WriteTx, o domain.Object) error {
		if e := s.jobFence(tx, j); e != nil {
			return e
		}
		return tx.AcquirePin(domain.Pin{ID: "manifest:" + j.ID, Kind: "job", OwnerID: j.ID, Hash: o.Hash})
	})
	_ = reader.Close()
	streamErr := <-done
	if e != nil {
		return domain.Object{}, e
	}
	if streamErr != nil {
		return domain.Object{}, streamErr
	}
	return object, nil
}

type metadataHints struct {
	Title, DocumentID, ProjectID, VersionID, BaseVersionID *string
	BasePresent                                            bool
}

func (s *Service) metadataHints(ctx context.Context, token, key string, obj domain.Object) (metadataHints, error) {
	var hints metadataHints
	e := s.Auth.Guard(ctx, token, func() error {
		return s.Bytes.Read(ctx, obj, domain.Pin{Kind: "import_prepare", OwnerID: key}, func(reader io.ReadSeeker) error {
			var v domain.Version
			if e := s.parseMetadata(ctx, bufio.NewReader(reader), domain.ImportIntent{}, &v, nil); e != nil {
				return e
			}
			for _, item := range []struct {
				path    []string
				value   **string
				present *bool
			}{{[]string{"document", "title"}, &hints.Title, nil}, {[]string{"document", "id"}, &hints.DocumentID, nil}, {[]string{"document", "project_id"}, &hints.ProjectID, nil}, {[]string{"version", "id"}, &hints.VersionID, nil}, {[]string{"version", "base_version_id"}, &hints.BaseVersionID, &hints.BasePresent}} {
				var raw bytes.Buffer
				found, e := copyField(reader, item.path, &raw)
				if e != nil {
					return e
				}
				if item.present != nil {
					*item.present = found
				}
				if found && json.Unmarshal(raw.Bytes(), item.value) != nil {
					return ErrPackageInvalid
				}
			}
			return nil
		})
	})
	return hints, e
}

func validateMetadataItem(kind string, item map[string]any) error {
	switch kind {
	case "sources":
		if item["kind"] == "git" {
			format, _ := item["object_format"].(string)
			revision, _ := item["exact_revision"].(string)
			_, repo := item["repo_id"]
			_, snapshot := item["snapshot_sha256"]
			length := 40
			if format == "sha256" {
				length = 64
			}
			if !repo || format == "" || snapshot || len(revision) != length {
				return ErrPackageInvalid
			}
			for _, b := range revision {
				if !(b >= '0' && b <= '9' || b >= 'a' && b <= 'f') {
					return ErrPackageInvalid
				}
			}
		} else {
			if _, ok := item["snapshot_sha256"]; !ok {
				return ErrPackageInvalid
			}
			for _, key := range []string{"repo_id", "object_format", "exact_revision"} {
				if _, ok := item[key]; ok {
					return ErrPackageInvalid
				}
			}
		}
		if item["retention_mode"] == "selected_snapshot" {
			if _, ok := item["asset_path"]; !ok {
				return ErrPackageInvalid
			}
			if _, bytes := item["bytes_sha256"]; !bytes {
				if _, snapshot := item["snapshot_sha256"]; !snapshot {
					return ErrPackageInvalid
				}
			}
		}
	case "evidence":
		start, a := item["line_start"]
		end, b := item["line_end"]
		if a != b {
			return ErrPackageInvalid
		}
		if a {
			left, _ := new(big.Int).SetString(string(start.(json.Number)), 10)
			right, _ := new(big.Int).SetString(string(end.(json.Number)), 10)
			if left.Cmp(right) > 0 {
				return ErrPackageInvalid
			}
		}
	case "references":
		if item["region_id"] != nil && item["page_path"] == nil {
			return ErrPackageInvalid
		}
	}
	return nil
}
