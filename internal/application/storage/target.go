package storage

import (
	"bytes"
	"encoding/json"
	"errors"
)

// NormalizeTarget retains exact nullable identities. Only result.draft_id has
// an omitted-field default. It does not infer project/document associations.
func NormalizeTarget(purpose string, data []byte) ([]byte, error) {
	fields, err := closedObject(data)
	if err != nil {
		return nil, err
	}
	keys := []string{}
	nullable := map[string]bool{}
	action := ""
	if raw, ok := fields["action"]; ok {
		if err := json.Unmarshal(raw, &action); err != nil {
			return nil, err
		}
	}
	switch purpose {
	case "result":
		keys = []string{"project_id", "document_id", "draft_id"}
		nullable["document_id"] = true
		nullable["draft_id"] = true
		if _, ok := fields["draft_id"]; !ok {
			fields["draft_id"] = json.RawMessage("null")
		}
	case "context":
		keys = []string{"version_id", "page_path", "render_instance_id"}
		nullable["page_path"] = true
		nullable["render_instance_id"] = true
	case "body":
		switch action {
		case "question", "annotation_create":
			keys = []string{"operation_id", "action", "version_id", "page_path"}
			nullable["page_path"] = true
		case "annotation_edit":
			keys = []string{"operation_id", "action", "annotation_id"}
		case "generation":
			keys = []string{"operation_id", "action", "project_id", "document_id"}
			nullable["document_id"] = true
		case "evidence_verification":
			keys = []string{"operation_id", "action", "version_id", "evidence_id"}
		default:
			return nil, errors.New("unknown body target action")
		}
	case "metadata":
		switch action {
		case "import":
			keys = []string{"operation_id", "action", "project_id", "document_id"}
			nullable["document_id"] = true
		case "draft_replace":
			keys = []string{"operation_id", "action", "draft_id"}
		default:
			return nil, errors.New("unknown metadata target action")
		}
	default:
		return nil, errors.New("unknown upload purpose")
	}
	if len(fields) != len(keys) {
		return nil, errors.New("upload target requires its exact closed field set")
	}
	for _, key := range keys {
		raw, ok := fields[key]
		if !ok {
			return nil, errors.New("missing explicit upload target field")
		}
		if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			if !nullable[key] {
				return nil, errors.New("unexpected null target field")
			}
			continue
		}
		var value string
		if err := json.Unmarshal(raw, &value); err != nil || value == "" {
			return nil, errors.New("target field requires a nonempty string")
		}
		if key != "action" && key != "page_path" && key != "evidence_id" && !operationID.MatchString(value) {
			return nil, errors.New("target service identity requires canonical UUID")
		}
	}
	return json.Marshal(fields)
}
