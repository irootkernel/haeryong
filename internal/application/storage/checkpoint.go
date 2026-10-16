package storage

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"regexp"

	"haeryong.local/haeryong/internal/domain"
)

var digestPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)
var decimalPattern = regexp.MustCompile(`^(0|[1-9][0-9]*)$`)

func ValidateCheckpoint(c domain.Checkpoint) error {
	if c.Generation < 1 || !digestPattern.MatchString(c.InventorySHA256) || !decimalPattern.MatchString(c.AcceptedBytes) {
		return errors.New("invalid four-field upload checkpoint")
	}
	return nil
}
func ParseCheckpoint(data []byte) (domain.Checkpoint, error) {
	var c domain.Checkpoint
	var fields map[string]json.RawMessage
	var err error
	fields, err = closedObject(data)
	if err != nil {
		return c, err
	}
	if len(fields) != 4 {
		return c, errors.New("checkpoint requires exactly four fields")
	}
	for _, key := range []string{"generation", "inventory_sha256", "accepted_bytes", "complete_input"} {
		if _, ok := fields[key]; !ok || bytes.Equal(fields[key], []byte("null")) {
			return c, errors.New("missing/null checkpoint field")
		}
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&c); err != nil {
		return c, err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return c, errors.New("trailing checkpoint data")
	}
	return c, ValidateCheckpoint(c)
}

func closedObject(data []byte) (map[string]json.RawMessage, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	token, err := decoder.Token()
	if err != nil || token != json.Delim('{') {
		return nil, errors.New("JSON object required")
	}
	fields := map[string]json.RawMessage{}
	for decoder.More() {
		token, err := decoder.Token()
		if err != nil {
			return nil, err
		}
		key, ok := token.(string)
		if !ok {
			return nil, errors.New("object key required")
		}
		if _, exists := fields[key]; exists {
			return nil, errors.New("duplicate object field")
		}
		var value json.RawMessage
		if err := decoder.Decode(&value); err != nil {
			return nil, err
		}
		fields[key] = value
	}
	if _, err := decoder.Token(); err != nil {
		return nil, err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return nil, errors.New("trailing JSON data")
	}
	return fields, nil
}

const EmptyInventorySHA256 = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
