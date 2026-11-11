package scans

import (
	"bytes"
	"encoding/json"
	"haeryong.local/haeryong/internal/security/session"
	"io"
)

func decodeDecision(raw []byte, out any) error {
	d := json.NewDecoder(bytes.NewReader(raw))
	token, e := d.Token()
	if e != nil || token != json.Delim('{') {
		return session.ErrInvalid
	}
	seen := map[string]bool{}
	for d.More() {
		k, e := d.Token()
		if e != nil {
			return session.ErrInvalid
		}
		name, ok := k.(string)
		if !ok || seen[name] {
			return session.ErrInvalid
		}
		seen[name] = true
		var v json.RawMessage
		if d.Decode(&v) != nil {
			return session.ErrInvalid
		}
	}
	if _, e = d.Token(); e != nil {
		return session.ErrInvalid
	}
	var extra any
	if d.Decode(&extra) != io.EOF {
		return session.ErrInvalid
	}
	d = json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if d.Decode(out) != nil {
		return session.ErrInvalid
	}
	return nil
}
