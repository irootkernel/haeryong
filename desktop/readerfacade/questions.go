package readerfacade

import (
	"bytes"
	"encoding/json"
	"haeryong.local/haeryong/internal/application/questions"
	"net/url"
	"strconv"
)

// Closed owner Ask/history operations reuse the Go-held credential and the
// existing per-pull attachment/daemon/record guard. No generic route is exposed.
func (s *ReaderService) questionOperation(op string, payload json.RawMessage, auth authority) (any, *wireError) {
	if op == "questionStart" {
		var in struct {
			Version string          `json:"version_id"`
			Request json.RawMessage `json:"request"`
			Key     string          `json:"operation_id"`
		}
		if e := decodeGenerationPayload(payload, &in, "version_id", "request", "operation_id"); e != nil {
			return nil, e
		}
		if !generationID.MatchString(in.Version) || !generationID.MatchString(in.Key) {
			return nil, generationInvalid()
		}
		if _, e := questions.DecodeRequest(bytes.NewReader(in.Request)); e != nil {
			return nil, generationInvalid()
		}
		result := s.request("POST", "/api/v1/versions/"+url.PathEscape(in.Version)+"/questions", nil, map[string]string{"Idempotency-Key": in.Key}, in.Request, auth)
		if result.werr == nil {
			s.receipt(op, map[string]any{"status": result.status})
		}
		return decision(result)
	}
	var id, cursor string
	citationIndex := -1
	switch op {
	case "questionList":
		var in struct {
			ID     string `json:"version_id"`
			Cursor string `json:"cursor"`
		}
		if e := decodeGenerationPayload(payload, &in, "version_id"); e != nil {
			return nil, e
		}
		id, cursor = in.ID, in.Cursor
		if !generationID.MatchString(id) {
			return nil, generationInvalid()
		}
		return plain(s.request("GET", "/api/v1/versions/"+url.PathEscape(id)+"/questions", collection(map[string]string{"cursor": cursor}), nil, nil, auth))
	case "questionAnswers":
		var in struct {
			ID     string `json:"question_id"`
			Cursor string `json:"cursor"`
		}
		if e := decodeGenerationPayload(payload, &in, "question_id"); e != nil {
			return nil, e
		}
		id, cursor = in.ID, in.Cursor
		if !generationID.MatchString(id) {
			return nil, generationInvalid()
		}
		return plain(s.request("GET", "/api/v1/questions/"+url.PathEscape(id)+"/answers", collection(map[string]string{"cursor": cursor}), nil, nil, auth))
	case "questionGet", "questionTextStream", "questionContextStream":
		var in struct {
			ID string `json:"question_id"`
		}
		if e := decodeGenerationPayload(payload, &in, "question_id"); e != nil {
			return nil, e
		}
		id = in.ID
	case "answerGet", "answerBodyStream":
		var in struct {
			ID string `json:"answer_id"`
		}
		if e := decodeGenerationPayload(payload, &in, "answer_id"); e != nil {
			return nil, e
		}
		id = in.ID
	case "answerCitationStream":
		var in struct {
			ID    string `json:"answer_id"`
			Index *int   `json:"citation_index"`
		}
		if e := decodeGenerationPayload(payload, &in, "answer_id", "citation_index"); e != nil {
			return nil, e
		}
		if in.Index == nil || *in.Index < 0 {
			return nil, generationInvalid()
		}
		id, citationIndex = in.ID, *in.Index
	default:
		return nil, generationInvalid()
	}
	if !generationID.MatchString(id) {
		return nil, generationInvalid()
	}
	path := "/api/v1/questions/" + url.PathEscape(id)
	if op == "answerGet" || op == "answerBodyStream" || op == "answerCitationStream" {
		path = "/api/v1/answers/" + url.PathEscape(id)
	}
	if op == "questionGet" || op == "answerGet" {
		return plain(s.request("GET", path, nil, nil, nil, auth))
	}
	endpoint := path + "/text"
	if op == "answerBodyStream" {
		endpoint = path + "/body"
	}
	if op == "questionContextStream" {
		endpoint = path + "/context"
	}
	if op == "answerCitationStream" {
		endpoint = path + "/citations/" + strconv.Itoa(citationIndex) + "/source"
	}
	epoch, e := s.currentEpoch(auth)
	if e != nil {
		return nil, e
	}
	r := s.requestRaw("GET", endpoint, nil, nil, auth, true)
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
	streamID := randomID()
	if streamID == "" {
		r.close()
		return nil, networkError()
	}
	stream := &readerStream{id: streamID, body: r.response, cancel: r.cancel, auth: auth, epoch: epoch}
	if op == "answerCitationStream" {
		stream.citationPath = endpoint
	} else if op == "answerBodyStream" {
		stream.answerID = id
	} else {
		stream.questionID = id
	}
	s.mu.Lock()
	s.streams[streamID] = stream
	s.mu.Unlock()
	return map[string]any{"stream_id": streamID, "etag": tag, "byte_size": size, "media_type": media}, nil
}
