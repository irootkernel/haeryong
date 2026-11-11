package readerfacade

import (
	"encoding/json"
	"net/url"
	"strings"
)

func (s *ReaderService) findingDecisionOperation(operation string, payload json.RawMessage, auth authority) (any, *wireError) {
	var in struct {
		FindingID    string `json:"finding_id"`
		OverlayID    string `json:"overlay_id"`
		AssignmentID string `json:"assignment_id"`
		Decision     string `json:"decision"`
		Pinned       *bool  `json:"pinned"`
		ETag         string `json:"etag"`
		OperationID  string `json:"operation_id"`
	}
	if e := decodeGenerationPayload(payload, &in); e != nil {
		return nil, e
	}
	if !generationID.MatchString(in.OperationID) || in.ETag == "" || !strings.HasPrefix(in.ETag, "\"") || strings.Contains(in.ETag, ",") {
		return nil, generationInvalid()
	}
	method, path := "POST", ""
	body := map[string]any{}
	switch operation {
	case "findingDecision":
		if in.FindingID == "" || in.OverlayID != "" || in.AssignmentID != "" || in.Pinned != nil || (in.Decision != "approve" && in.Decision != "reject") {
			return nil, generationInvalid()
		}
		path = "/api/v1/findings/" + url.PathEscape(in.FindingID) + "/decisions"
		body["decision"] = in.Decision
	case "overlayUndo":
		if in.OverlayID == "" || in.FindingID != "" || in.AssignmentID != "" || in.Pinned != nil || in.Decision != "" {
			return nil, generationInvalid()
		}
		path = "/api/v1/overlays/" + url.PathEscape(in.OverlayID) + "/undo"
	case "topicAssignmentSet":
		if in.AssignmentID == "" || in.FindingID != "" || in.OverlayID != "" || in.Pinned == nil || (in.Decision != "accepted" && in.Decision != "rejected") || *in.Pinned && in.Decision == "rejected" {
			return nil, generationInvalid()
		}
		method = "PUT"
		path = "/api/v1/topic-assignments/" + url.PathEscape(in.AssignmentID)
		body["decision"] = in.Decision
		body["pinned"] = *in.Pinned
	default:
		return nil, generationInvalid()
	}
	return decision(s.request(method, path, nil, map[string]string{"Idempotency-Key": in.OperationID, "If-Match": in.ETag}, jsonBody(body), auth))
}
