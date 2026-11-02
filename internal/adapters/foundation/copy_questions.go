package foundation

import (
	"encoding/json"
	"reflect"

	"haeryong.local/haeryong/internal/domain"
)

// Question export is an explicit personal-record family, not a provider job
// or a claim that copied citations are newly verified. The same independent
// object/anchor binding verification used by other records covers its bytes.
func (b *exportBindings) questionRecord(payload, view *exportView, selected domain.CopyVersion, row exportRecord) error {
	var record struct {
		Record string          `json:"record"`
		Value  json.RawMessage `json:"value"`
	}
	if e := decodeCopyValue(row.Value, &record); e != nil {
		return e
	}
	switch record.Record {
	case "question":
		var fields map[string]json.RawMessage
		if json.Unmarshal(record.Value, &fields) != nil {
			return domain.ErrContentIntegrity
		}
		if _, exists := fields["PreparedAnswer"]; exists {
			return domain.ErrContentIntegrity
		}
		// The producer deliberately omits the unpublished prepared candidate.
		// Restore that absent optional value only for closed grammar validation.
		fields["PreparedAnswer"] = json.RawMessage(`null`)
		raw, e := json.Marshal(fields)
		if e != nil {
			return e
		}
		var q domain.Question
		if e = decodeCopyValue(raw, &q); e != nil {
			return e
		}
		anchor := domain.AnnotationAnchor{VersionID: q.VersionID, PagePath: q.PagePath, RegionID: q.RegionID, PageQuery: q.PageQuery, PageFragment: q.PageFragment}
		if !copyUUID.MatchString(q.ID) || q.OwnerID != row.Owner || q.DocumentID != selected.DocumentID || q.VersionID != selected.VersionID || !copyUUID.MatchString(q.ProjectID) || !copyUUID.MatchString(q.JobID) || !copyUUID.MatchString(q.OperationID) || !copyUUID.MatchString(q.CaptureOperationID) || len(q.RequestHash) != 64 || q.CreatedAt == "" || !validExportAnchor(anchor) || !exportReaderPage(payload, selected, q.PagePath) {
			return domain.ErrContentIntegrity
		}
		if q.SourceMode != "pinned" && q.SourceMode != "current" || q.Explanation.VersionID != q.VersionID || !reflect.DeepEqual(q.Explanation.PagePath, q.PagePath) || !reflect.DeepEqual(q.Explanation.RegionID, q.RegionID) {
			return domain.ErrContentIntegrity
		}
		if old, ok := b.questions[q.ID]; ok && !reflect.DeepEqual(old, q) {
			return domain.ErrContentIntegrity
		}
		b.questions[q.ID] = q
		for _, object := range []domain.ProviderText{q.Text, {Hash: q.Explanation.ObjectHash, Size: q.Explanation.Size}} {
			if e = verifyMetadataObject(b.ctx, view, *selected.MetadataRoot, domain.Object{Hash: object.Hash, Size: object.Size}); e != nil {
				return e
			}
		}
	case "answer":
		var answer domain.QuestionAnswer
		if e := decodeCopyValue(record.Value, &answer); e != nil {
			return e
		}
		if !copyUUID.MatchString(answer.ID) || !copyUUID.MatchString(answer.QuestionID) || !copyUUID.MatchString(answer.AttemptID) || answer.CreatedAt == "" || answer.State != "final" && answer.State != "partial" && answer.State != "error" {
			return domain.ErrContentIntegrity
		}
		if old, ok := b.answers[answer.ID]; ok && !reflect.DeepEqual(old, answer) {
			return domain.ErrContentIntegrity
		}
		b.answers[answer.ID] = answer
		return verifyMetadataObject(b.ctx, view, *selected.MetadataRoot, domain.Object{Hash: answer.Body.Hash, Size: answer.Body.Size})
	default:
		return domain.ErrContentIntegrity
	}
	return nil
}

func (b *exportBindings) finishQuestions(payload *exportView, selected map[string]domain.CopyVersion, used map[string]bool) error {
	for _, q := range b.questions {
		version, ok := selected[q.VersionID]
		if !ok || version.DocumentID != q.DocumentID || !exportReaderPage(payload, version, q.PagePath) {
			return domain.ErrContentIntegrity
		}
		if q.Context != nil {
			context, ok := b.contexts[q.Context.ID]
			if !ok || context.Version != q.VersionID || !reflect.DeepEqual(context.Page, q.PagePath) || context.Hash != q.Context.Hash || context.Size != q.Context.Size || context.Media != q.Context.MediaType {
				return domain.ErrContentIntegrity
			}
			used[q.Context.ID] = true
		}
	}
	for _, answer := range b.answers {
		if _, ok := b.questions[answer.QuestionID]; !ok {
			return domain.ErrContentIntegrity
		}
	}
	return nil
}
