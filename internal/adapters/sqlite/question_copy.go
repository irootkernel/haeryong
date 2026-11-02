package sqlite

import (
	"context"
	"encoding/json"
	"haeryong.local/haeryong/internal/domain"
)

// Explicit personal-record export preserves exact adopted text/display bytes,
// never the job invocation, credentials or an unpublished prepared answer.
func CopyQuestionObjects(ctx context.Context, path, owner string, raw json.RawMessage, consume func(domain.Object) error) ([]json.RawMessage, error) {
	var record struct {
		Record string          `json:"record"`
		Value  json.RawMessage `json:"value"`
	}
	if e := json.Unmarshal(raw, &record); e != nil {
		return nil, e
	}
	refs := []domain.ProviderText{}
	contexts := []json.RawMessage{}
	switch record.Record {
	case "question":
		var q domain.Question
		if e := json.Unmarshal(record.Value, &q); e != nil {
			return nil, e
		}
		if q.OwnerID != owner || q.PreparedAnswer != nil {
			return nil, domain.ErrInvalidTarget
		}
		refs = append(refs, q.Text, domain.ProviderText{Hash: q.Explanation.ObjectHash, Size: q.Explanation.Size})
		if q.Context != nil {
			refs = append(refs, domain.ProviderText{Hash: q.Context.Hash, Size: q.Context.Size})
			value, _ := json.Marshal(map[string]any{"context_ref": q.Context.ID, "version_id": q.VersionID, "page_path": q.PagePath, "media_type": q.Context.MediaType, "sha256": q.Context.Hash, "bytes": q.Context.Size})
			contexts = append(contexts, value)
		}
	case "answer":
		var a domain.QuestionAnswer
		if e := json.Unmarshal(record.Value, &a); e != nil {
			return nil, e
		}
		refs = append(refs, a.Body)
	default:
		return nil, domain.ErrInvalidTarget
	}
	for _, ref := range refs {
		if e := consume(domain.Object{Hash: ref.Hash, Size: ref.Size, State: "ready"}); e != nil {
			return nil, e
		}
	}
	return contexts, nil
}
