package readerfacade

import (
	"bytes"
	"encoding/json"
	"io"
	"net/url"
	"regexp"
	"strconv"

	generation "haeryong.local/haeryong/internal/application/generation"
	importer "haeryong.local/haeryong/internal/application/import"
	"haeryong.local/haeryong/internal/application/scan"
	"haeryong.local/haeryong/internal/domain"
)

var generationID = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
var generationJobETag = regexp.MustCompile(`^"job-[0-9a-f-]{36}-[0-9a-f]{64}"$`)
var generationETag = regexp.MustCompile(`^"draft-[0-9a-f]{64}"$`)

func generationInvalid() *wireError { return &wireError{Status: 400, Code: "NATIVE_PAYLOAD_INVALID"} }

// This family uses a closed duplicate-aware outer shape. Nested generation
// request parsing is shared with HTTP; it never becomes a generic URL proxy.
func decodeGenerationPayload(raw json.RawMessage, out any, required ...string) *wireError {
	d := json.NewDecoder(bytes.NewReader(raw))
	token, e := d.Token()
	if e != nil || token != json.Delim('{') {
		return generationInvalid()
	}
	fields := map[string]json.RawMessage{}
	for d.More() {
		token, e = d.Token()
		if e != nil {
			return generationInvalid()
		}
		key, ok := token.(string)
		if !ok {
			return generationInvalid()
		}
		if _, exists := fields[key]; exists {
			return generationInvalid()
		}
		var value json.RawMessage
		if d.Decode(&value) != nil || bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return generationInvalid()
		}
		fields[key] = value
	}
	if token, e = d.Token(); e != nil || token != json.Delim('}') {
		return generationInvalid()
	}
	var extra any
	if d.Decode(&extra) != io.EOF {
		return generationInvalid()
	}
	for _, key := range required {
		if _, ok := fields[key]; !ok {
			return generationInvalid()
		}
	}
	d = json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if d.Decode(out) != nil {
		return generationInvalid()
	}
	return nil
}
func (s *ReaderService) generationOperation(operation string, payload json.RawMessage, auth authority) (any, *wireError) {
	switch operation {
	case "jobResume":
		var in struct {
			ID         string `json:"job_id"`
			Decision   string `json:"decision"`
			Generation int64  `json:"expected_attempt_generation"`
			ETag       string `json:"etag"`
			Key        string `json:"operation_id"`
		}
		if e := decodeGenerationPayload(payload, &in, "job_id", "decision", "expected_attempt_generation", "etag", "operation_id"); e != nil {
			return nil, e
		}
		if !generationID.MatchString(in.ID) || !generationID.MatchString(in.Key) || in.Generation < 1 || (in.Decision != "reconcile_only" && in.Decision != "continue" && in.Decision != "retry") || !generationJobETag.MatchString(in.ETag) {
			return nil, generationInvalid()
		}
		return decision(s.request("POST", "/api/v1/jobs/"+url.PathEscape(in.ID)+"/resume", nil, map[string]string{"Idempotency-Key": in.Key, "If-Match": in.ETag}, jsonBody(map[string]any{"decision": in.Decision, "expected_attempt_generation": in.Generation}), auth))

	case "generationStart":
		var in struct {
			Request     json.RawMessage `json:"request"`
			OperationID string          `json:"operation_id"`
		}
		if e := decodeGenerationPayload(payload, &in, "request", "operation_id"); e != nil {
			return nil, e
		}
		request, e := generation.DecodeRequest(bytes.NewReader(in.Request))
		if e != nil || !generationID.MatchString(in.OperationID) || !generationID.MatchString(request.ProjectID) || request.DocumentID != nil && !generationID.MatchString(*request.DocumentID) {
			return nil, generationInvalid()
		}
		result := s.request("POST", "/api/v1/generations", nil, map[string]string{"Idempotency-Key": in.OperationID}, in.Request, auth)
		if result.werr == nil {
			s.receipt(operation, map[string]any{"status": result.status})
		}
		return decision(result)
	case "draftList":
		var in struct {
			Project string `json:"project_id"`
			Cursor  string `json:"cursor"`
		}
		if e := decodeGenerationPayload(payload, &in); e != nil {
			return nil, e
		}
		if in.Project != "" && !generationID.MatchString(in.Project) {
			return nil, generationInvalid()
		}
		return plain(s.request("GET", "/api/v1/drafts", collection(map[string]string{"project_id": in.Project, "cursor": in.Cursor}), nil, nil, auth))
	case "draftGet", "draftHistory":
		// Get has no cursor field; each operation has its own exact shape.
		var id, cursor string
		if operation == "draftGet" {
			var in struct {
				ID string `json:"draft_id"`
			}
			if e := decodeGenerationPayload(payload, &in, "draft_id"); e != nil {
				return nil, e
			}
			id = in.ID
		} else {
			var in struct {
				ID     string `json:"draft_id"`
				Cursor string `json:"cursor"`
			}
			if e := decodeGenerationPayload(payload, &in, "draft_id"); e != nil {
				return nil, e
			}
			id, cursor = in.ID, in.Cursor
		}
		if !generationID.MatchString(id) {
			return nil, generationInvalid()
		}
		path := "/api/v1/drafts/" + url.PathEscape(id)
		if operation == "draftHistory" {
			path += "/history"
		}
		return plain(s.request("GET", path, collection(map[string]string{"cursor": cursor}), nil, nil, auth))
	case "draftPatch":
		var in struct {
			ID          string          `json:"draft_id"`
			Body        json.RawMessage `json:"body"`
			ETag        string          `json:"etag"`
			OperationID string          `json:"operation_id"`
		}
		if e := decodeGenerationPayload(payload, &in, "draft_id", "body", "etag", "operation_id"); e != nil {
			return nil, e
		}
		var body struct {
			Action   string          `json:"action"`
			Upload   *string         `json:"upload_id"`
			Metadata json.RawMessage `json:"metadata_ref"`
		}
		if e := decodeGenerationPayload(in.Body, &body, "action"); e != nil {
			return nil, e
		}
		if body.Action == "reject" {
			if body.Upload != nil || body.Metadata != nil {
				return nil, generationInvalid()
			}
		} else if body.Action == "replace" {
			if body.Upload == nil || !generationID.MatchString(*body.Upload) {
				return nil, generationInvalid()
			}
		} else {
			return nil, generationInvalid()
		}
		if body.Metadata != nil {
			var ref importer.ObjectRef
			if e := decodeGenerationPayload(body.Metadata, &ref, "object_id", "bytes_sha256", "byte_size", "media_type"); e != nil {
				return nil, e
			}
		}
		if !generationID.MatchString(in.ID) || !generationID.MatchString(in.OperationID) || !generationETag.MatchString(in.ETag) {
			return nil, generationInvalid()
		}
		result := s.request("PATCH", "/api/v1/drafts/"+url.PathEscape(in.ID), nil, map[string]string{"Idempotency-Key": in.OperationID, "If-Match": in.ETag}, in.Body, auth)
		return decision(result)
	case "draftPublish":
		var in struct {
			ID          string `json:"draft_id"`
			ETag        string `json:"etag"`
			OperationID string `json:"operation_id"`
		}
		if e := decodeGenerationPayload(payload, &in, "draft_id", "etag", "operation_id"); e != nil {
			return nil, e
		}
		if !generationID.MatchString(in.ID) || !generationID.MatchString(in.OperationID) || !generationETag.MatchString(in.ETag) {
			return nil, generationInvalid()
		}
		result := s.request("POST", "/api/v1/drafts/"+url.PathEscape(in.ID)+"/publish", nil, map[string]string{"Idempotency-Key": in.OperationID, "If-Match": in.ETag}, jsonBody(map[string]any{}), auth)
		return decision(result)
	case "scanStart":
		var in struct {
			Request     json.RawMessage `json:"request"`
			OperationID string          `json:"operation_id"`
		}
		if e := decodeGenerationPayload(payload, &in, "request", "operation_id"); e != nil {
			return nil, e
		}
		var req domain.ScanRequest
		if e := decodeGenerationPayload(in.Request, &req); e != nil || !generationID.MatchString(in.OperationID) {
			return nil, generationInvalid()
		}
		result := s.request("POST", "/api/v1/scans", nil, map[string]string{"Idempotency-Key": in.OperationID}, in.Request, auth)
		if result.werr == nil {
			s.receipt(operation, map[string]any{"status": result.status})
		}
		return decision(result)
	case "jobFindingsList":
		var in struct {
			JobID  string  `json:"job_id"`
			Kind   *string `json:"kind"`
			Status *string `json:"status"`
			Cursor *string `json:"cursor"`
			Limit  *int    `json:"limit"`
		}
		if e := decodeGenerationPayload(payload, &in, "job_id"); e != nil {
			return nil, e
		}
		if !generationID.MatchString(in.JobID) ||
			(in.Kind != nil && (*in.Kind == "" || !scan.ValidFindingKind(*in.Kind))) ||
			(in.Status != nil && (*in.Status == "" || !scan.ValidFindingStatus(*in.Status))) ||
			(in.Cursor != nil && *in.Cursor == "") ||
			(in.Limit != nil && (*in.Limit < 1 || *in.Limit > 200)) {
			return nil, generationInvalid()
		}
		params := map[string]string{}
		if in.Kind != nil {
			params["kind"] = *in.Kind
		}
		if in.Status != nil {
			params["status"] = *in.Status
		}
		if in.Cursor != nil {
			params["cursor"] = *in.Cursor
		}
		if in.Limit != nil {
			params["limit"] = strconv.Itoa(*in.Limit)
		}
		return plain(s.request("GET", "/api/v1/jobs/"+url.PathEscape(in.JobID)+"/findings", collection(params), nil, nil, auth))
	}
	return nil, generationInvalid()
}
