package readerfacade

import (
	"encoding/json"
	"net/url"
	"strconv"
)

func (s *ReaderService) annotationOperation(op string, payload json.RawMessage, auth authority) (any, *wireError) {
	if op == "annotationBodyStream" || op == "annotationContextStream" {
		var in struct {
			AnnotationID string `json:"annotation_id"`
			Revision     int64  `json:"revision"`
		}
		if e := decodeReferencePayload(payload, &in); e != nil {
			return nil, e
		}
		if in.AnnotationID == "" || in.Revision < 1 {
			return nil, &wireError{Status: 400, Code: "NATIVE_PAYLOAD_INVALID"}
		}
		kind := "body"
		if op == "annotationContextStream" {
			kind = "context"
		}
		epoch, e := s.currentEpoch(auth)
		if e != nil {
			return nil, e
		}
		r := s.requestRaw("GET", "/api/v1/annotations/"+url.PathEscape(in.AnnotationID)+"/"+kind, url.Values{"revision": {strconv.FormatInt(in.Revision, 10)}}, nil, auth, true)
		if r.werr != nil {
			return nil, r.werr
		}
		if r.status != 200 {
			_, e := typedResult(r.status, r.body)
			r.close()
			return nil, e
		}
		tag, size, media := r.header.Get("ETag"), r.header.Get("Content-Length"), r.header.Get("Content-Type")
		if tag == "" || size == "" || media == "" {
			r.close()
			return nil, &wireError{Status: 0, Code: "UNCERTAIN_RESPONSE"}
		}
		id := randomID()
		if id == "" {
			r.close()
			return nil, networkError()
		}
		s.mu.Lock()
		s.streams[id] = &readerStream{id: id, body: r.response, cancel: r.cancel, auth: auth, annotationID: in.AnnotationID, epoch: epoch}
		s.mu.Unlock()
		return map[string]any{"stream_id": id, "etag": tag, "byte_size": size, "media_type": media}, nil
	}
	if op == "annotationList" {
		var in struct {
			VersionID string `json:"version_id"`
			Cursor    string `json:"cursor"`
		}
		if e := decodeReferencePayload(payload, &in); e != nil {
			return nil, e
		}
		return plain(s.request("GET", "/api/v1/versions/"+url.PathEscape(in.VersionID)+"/annotations", collection(map[string]string{"cursor": in.Cursor}), nil, nil, auth))
	}
	if op == "annotationGet" || op == "annotationHistory" {
		var in struct {
			AnnotationID string `json:"annotation_id"`
			Cursor       string `json:"cursor"`
		}
		if e := decodeReferencePayload(payload, &in); e != nil {
			return nil, e
		}
		path := "/api/v1/annotations/" + url.PathEscape(in.AnnotationID)
		if op == "annotationHistory" {
			path += "/history"
		}
		return plain(s.request("GET", path, collection(map[string]string{"cursor": in.Cursor}), nil, nil, auth))
	}
	if op == "revisitGet" {
		var in struct {
			DocumentID string `json:"document_id"`
		}
		if e := decodeReferencePayload(payload, &in); e != nil {
			return nil, e
		}
		return plain(s.request("GET", "/api/v1/documents/"+url.PathEscape(in.DocumentID)+"/revisit", nil, nil, nil, auth))
	}
	if op == "revisitPut" {
		var in struct {
			DocumentID   string `json:"document_id"`
			Flag         *bool  `json:"flag"`
			OperationID  string `json:"operation_id"`
			Precondition struct {
				IfMatch *string `json:"if_match"`
				Create  *bool   `json:"create"`
			} `json:"precondition"`
		}
		if e := decodeReferencePayload(payload, &in); e != nil {
			return nil, e
		}
		if in.Flag == nil {
			return nil, &wireError{Status: 400, Code: "NATIVE_PAYLOAD_INVALID"}
		}
		headers := map[string]string{"Idempotency-Key": in.OperationID}
		if in.Precondition.IfMatch != nil && in.Precondition.Create == nil {
			headers["If-Match"] = *in.Precondition.IfMatch
		} else if in.Precondition.IfMatch == nil && in.Precondition.Create != nil && *in.Precondition.Create {
			headers["If-None-Match"] = "*"
		} else {
			return nil, &wireError{Status: 400, Code: "NATIVE_PAYLOAD_INVALID"}
		}
		return decision(s.request("PUT", "/api/v1/documents/"+url.PathEscape(in.DocumentID)+"/revisit", nil, headers, jsonBody(map[string]bool{"flag": *in.Flag}), auth))
	}
	if op == "annotationCreate" {
		var in struct {
			VersionID   string          `json:"version_id"`
			Body        json.RawMessage `json:"body"`
			OperationID string          `json:"operation_id"`
		}
		if e := decodeReferencePayload(payload, &in); e != nil {
			return nil, e
		}
		return decision(s.request("POST", "/api/v1/versions/"+url.PathEscape(in.VersionID)+"/annotations", nil, map[string]string{"Idempotency-Key": in.OperationID}, in.Body, auth))
	}
	if op == "annotationDelete" {
		var in struct {
			AnnotationID string `json:"annotation_id"`
			ETag         string `json:"etag"`
			OperationID  string `json:"operation_id"`
		}
		if e := decodeReferencePayload(payload, &in); e != nil {
			return nil, e
		}
		return decision(s.request("DELETE", "/api/v1/annotations/"+url.PathEscape(in.AnnotationID), nil, map[string]string{"Idempotency-Key": in.OperationID, "If-Match": in.ETag}, nil, auth))
	}
	var in struct {
		AnnotationID string          `json:"annotation_id"`
		Body         json.RawMessage `json:"body"`
		ETag         string          `json:"etag"`
		OperationID  string          `json:"operation_id"`
	}
	if e := decodeReferencePayload(payload, &in); e != nil {
		return nil, e
	}
	return decision(s.request("PATCH", "/api/v1/annotations/"+url.PathEscape(in.AnnotationID), nil, map[string]string{"Idempotency-Key": in.OperationID, "If-Match": in.ETag}, in.Body, auth))
}
