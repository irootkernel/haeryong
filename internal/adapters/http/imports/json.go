package imports

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"

	importer "haeryong.local/haeryong/internal/application/import"
	"haeryong.local/haeryong/internal/security/session"
)

// importInput keeps exact submitted metadata bytes in a private temporary file.
// It is transport preparation only: the byte-store copies and syncs those bytes
// on the destination filesystem before any durable adoption or publication.
func importInput(body io.Reader, validation bool) (input importer.ImportRequest, cleanup func(), err error) {
	var metadata *os.File
	cleanup = func() {
		if metadata != nil {
			metadata.Close()
			os.Remove(metadata.Name())
		}
	}
	defer func() {
		if err != nil {
			cleanup()
		}
	}()
	r := bufio.NewReader(body)
	if err = expect(r, '{'); err != nil {
		return
	}
	seen := map[string]bool{}
	for {
		var next byte
		next, err = nonspace(r)
		if err != nil {
			return
		}
		if next == '}' {
			break
		}
		if next != '"' {
			err = session.ErrInvalid
			return
		}
		if err = r.UnreadByte(); err != nil {
			return
		}
		var keyBytes bytes.Buffer
		if err = copyValue(r, &keyBytes); err != nil {
			return
		}
		var key string
		if err = json.Unmarshal(keyBytes.Bytes(), &key); err != nil {
			err = session.ErrInvalid
			return
		}
		if seen[key] {
			err = session.ErrInvalid
			return
		}
		seen[key] = true
		if err = expect(r, ':'); err != nil {
			return
		}
		if key == "metadata" {
			metadata, err = os.CreateTemp("", "haeryong-request-metadata-*")
			if err != nil {
				return
			}
			if err = copyValue(r, metadata); err != nil {
				return
			}
			if _, err = metadata.Seek(0, io.SeekStart); err != nil {
				return
			}
			input.Metadata = metadata
		} else {
			var raw bytes.Buffer
			switch key {
			case "upload_id", "document_id", "project_id", "version_id", "base_version_id", "metadata_ref":
			default:
				err = session.ErrInvalid
				return
			}
			if validation && key != "upload_id" && key != "metadata_ref" {
				err = session.ErrInvalid
				return
			}
			if err = copyValue(r, &raw); err != nil {
				return
			}
			data := raw.Bytes()
			if key == "metadata_ref" {
				if bytes.Equal(data, []byte("null")) {
					err = session.ErrInvalid
					return
				}
				var reference importer.ObjectRef
				if err = closedJSON(bytes.NewReader(data), &reference, []string{"object_id", "bytes_sha256", "byte_size", "media_type"}); err != nil {
					err = session.ErrInvalid
					return
				}
				input.MetadataRef = &reference
			} else {
				var value string
				null := bytes.Equal(data, []byte("null"))
				if !null {
					if err = json.Unmarshal(data, &value); err != nil {
						err = session.ErrInvalid
						return
					}
				}
				switch key {
				case "upload_id":
					input.UploadID = value
				case "document_id":
					input.DocumentPresent = true
					if !null {
						input.DocumentID = &value
					}
				case "project_id":
					input.ProjectID = value
				case "version_id":
					input.VersionID = value
				case "base_version_id":
					input.BasePresent = true
					if !null {
						input.BaseVersionID = &value
					}
				}
				if null && key != "document_id" && key != "base_version_id" {
					err = session.ErrInvalid
					return
				}
			}
		}
		next, err = nonspace(r)
		if err != nil {
			return
		}
		if next == '}' {
			break
		}
		if next != ',' {
			err = session.ErrInvalid
			return
		}
		next, err = nonspace(r)
		if err != nil {
			return
		}
		if next == '}' {
			err = session.ErrInvalid
			return
		}
		if err = r.UnreadByte(); err != nil {
			return
		}
	}
	if input.UploadID == "" || (!validation && !input.DocumentPresent) || (input.Metadata != nil && seen["metadata_ref"]) {
		err = session.ErrInvalid
		return
	}
	if _, extra := nonspace(r); extra != io.EOF {
		err = session.ErrInvalid
		return
	}
	return
}

func nonspace(r *bufio.Reader) (byte, error) {
	for {
		b, err := r.ReadByte()
		if err != nil {
			return b, err
		}
		if b != ' ' && b != '\t' && b != '\r' && b != '\n' {
			return b, nil
		}
	}
}
func expect(r *bufio.Reader, want byte) error {
	b, err := nonspace(r)
	if err != nil || b != want {
		return session.ErrInvalid
	}
	return nil
}

// copyValue excludes transport whitespace before the value, preserving every
// byte inside it through the application's shared streaming JSON validator.
func copyValue(r *bufio.Reader, output io.Writer) error {
	if _, err := nonspace(r); err != nil {
		return session.ErrInvalid
	}
	if err := r.UnreadByte(); err != nil {
		return err
	}
	err := importer.CopyJSONValue(r, output)
	if errors.Is(err, importer.ErrPackageInvalid) {
		return errors.Join(session.ErrInvalid, err)
	}
	return err
}

func closedJSON(body io.Reader, out any, required []string) error {
	decoder := json.NewDecoder(body)
	token, err := decoder.Token()
	if err != nil || token != json.Delim('{') {
		return session.ErrInvalid
	}
	fields := map[string]json.RawMessage{}
	for decoder.More() {
		token, err := decoder.Token()
		if err != nil {
			return session.ErrInvalid
		}
		key, ok := token.(string)
		if !ok {
			return session.ErrInvalid
		}
		if _, exists := fields[key]; exists {
			return session.ErrInvalid
		}
		var value json.RawMessage
		if decoder.Decode(&value) != nil || bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return session.ErrInvalid
		}
		if err := importer.CopyJSONValue(bufio.NewReader(bytes.NewReader(value)), io.Discard); err != nil {
			return session.ErrInvalid
		}
		fields[key] = value
	}
	if _, err := decoder.Token(); err != nil {
		return session.ErrInvalid
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		return session.ErrInvalid
	}
	for _, key := range required {
		if _, ok := fields[key]; !ok {
			return session.ErrInvalid
		}
	}
	data, err := json.Marshal(fields)
	if err != nil {
		return err
	}
	strict := json.NewDecoder(bytes.NewReader(data))
	strict.DisallowUnknownFields()
	if err := strict.Decode(out); err != nil {
		return errors.Join(session.ErrInvalid, err)
	}
	if input, ok := out.(*importer.CreateRequest); ok && input.SelectionName == "" {
		if _, supplied := fields["selection_name"]; supplied {
			return session.ErrInvalid
		}
	}
	return nil
}
