package readerfacade

import (
	"encoding/json"
	"haeryong.local/haeryong/internal/domain"
	"strconv"
)

func (s *ReaderService) managementOperation(operation string, payload json.RawMessage, auth authority) (any, *wireError) {
	if operation == "jobsList" {
		var in struct{}
		if e := decodeGenerationPayload(payload, &in); e != nil {
			return nil, e
		}
		return plain(s.request("GET", "/api/v1/jobs", nil, nil, nil, auth))
	}
	var in struct {
		DocumentID *string `json:"document_id"`
		VersionID  *string `json:"version_id"`
		PagePath   *string `json:"page_path"`
		RegionID   *string `json:"region_id"`
		Cursor     *string `json:"cursor"`
		Limit      *int    `json:"limit"`
	}
	if e := decodeGenerationPayload(payload, &in); e != nil {
		return nil, e
	}
	for _, id := range []*string{in.DocumentID, in.VersionID} {
		if id != nil && !generationID.MatchString(*id) {
			return nil, generationInvalid()
		}
	}
	if in.PagePath != nil && (in.VersionID == nil || !domain.ValidReferencePath(in.PagePath)) || in.RegionID != nil && (in.PagePath == nil || *in.RegionID == "") || in.Cursor != nil && *in.Cursor == "" || in.Limit != nil && (*in.Limit < 1 || *in.Limit > 200) {
		return nil, generationInvalid()
	}
	params := map[string]string{}
	for k, v := range map[string]*string{"document_id": in.DocumentID, "version_id": in.VersionID, "page_path": in.PagePath, "region_id": in.RegionID, "cursor": in.Cursor} {
		if v != nil {
			params[k] = *v
		}
	}
	if in.Limit != nil {
		params["limit"] = strconv.Itoa(*in.Limit)
	}
	path := "/api/v1/topic-assignments"
	if operation == "versionRelated" {
		if in.VersionID == nil || in.DocumentID != nil {
			return nil, generationInvalid()
		}
		path = "/api/v1/versions/" + *in.VersionID + "/related"
		delete(params, "version_id")
	}
	return plain(s.request("GET", path, collection(params), nil, nil, auth))
}
