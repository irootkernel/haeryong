package storage

import (
	"encoding/json"
	"reflect"

	"haeryong.local/haeryong/internal/domain"
)

// QuestionTx owns saved Q&A; provider jobs and attempts retain execution,
// cancellation and original-operation recovery authority.
type QuestionTx interface {
	CreateQuestion(domain.Question) error
	Question(string) (domain.Question, error)
	QuestionByJob(string) (domain.Question, error)
	Questions(owner, version, afterCreated, afterID string, limit int) ([]domain.Question, error)
	AdmitQuestion(domain.Job) error
	QuestionCaptureUnavailable(string, []domain.QuestionLimitation) error
	PrepareQuestionAnswer(string, domain.QuestionAnswer) error
	PutQuestionAnswer(domain.QuestionAnswer) error
	QuestionAnswer(string) (domain.QuestionAnswer, error)
	QuestionAnswerForAttempt(string, string) (domain.QuestionAnswer, error)
	QuestionAnswers(question, afterCreated, afterID string, limit int) ([]domain.QuestionAnswer, error)
	LatestCompletedAnswer(string) (*domain.QuestionAnswer, error)
	Verification(string) (domain.CitationVerification, error)
}

// AuthorizeQuestion checks accepted owner authority and exact saved input. It
// does not depend on the submitting bearer or render channel remaining alive.
func AuthorizeQuestion(tx WriteTx, scope domain.ProviderScope) error {
	state, e := tx.AuthState()
	if e != nil {
		return e
	}
	if scope.PrincipalID != state.OwnerID || scope.AuthEpoch != state.AuthEpoch || scope.CredentialScope.Kind != "owner" || scope.Purpose != "body" || scope.Invocation == nil || scope.Invocation.Question == nil || scope.Invocation.Generation != nil {
		return domain.ErrDenied
	}
	inv := scope.Invocation
	input := inv.Question
	q, e := tx.Question(input.QuestionID)
	if e != nil {
		return e
	}
	if q.OwnerID != scope.PrincipalID || input.VersionID != q.VersionID || input.SourceMode != q.SourceMode || !reflect.DeepEqual(input.PagePath, q.PagePath) || !reflect.DeepEqual(input.RegionID, q.RegionID) || input.PageQuery != q.PageQuery || input.PageFragment != q.PageFragment || !reflect.DeepEqual(input.Context, q.Context) || !reflect.DeepEqual(input.Explanation, q.Explanation) || inv.PromptRef == nil || *inv.PromptRef != q.Text || inv.Prompt != "" {
		return domain.ErrDenied
	}
	target, _ := json.Marshal(map[string]any{"operation_id": q.OperationID, "action": "question", "version_id": q.VersionID, "page_path": q.PagePath})
	actual, e := NormalizeTarget("body", scope.Target)
	if e != nil {
		return e
	}
	expected, e := NormalizeTarget("body", target)
	if e != nil || string(actual) != string(expected) {
		return domain.ErrInvalidTarget
	}
	if e = AuthorizeTarget(tx, target); e != nil {
		return e
	}
	ref, e := ResolveQuestionReference(tx, scope.CredentialScope, q.Explanation.ExplanationReference)
	if e != nil {
		return e
	}
	if !reflect.DeepEqual(ref, q.Explanation) {
		return domain.ErrContentIntegrity
	}
	if q.Context != nil {
		context, e := tx.Context(q.Context.ID)
		if e != nil {
			return e
		}
		if context.PrincipalID != q.OwnerID || context.VersionID != q.VersionID || !reflect.DeepEqual(context.PagePath, q.PagePath) || !reflect.DeepEqual(context.RenderInstanceID, q.Context.RenderInstanceID) || context.Object.Hash != q.Context.Hash || context.Object.Size != q.Context.Size || context.MediaType != q.Context.MediaType {
			return domain.ErrDenied
		}
	}
	return nil
}

// ResolveQuestionReference preserves a captured page-local runtime anchor even
// when the author supplied no optional region metadata. Page/hash/owner checks
// remain the same immutable explanation checks used by Generation; Generation's
// declared-region contract is unchanged.
func ResolveQuestionReference(tx WriteTx, scope domain.CredentialScope, ref domain.ExplanationReference) (domain.GenerationReference, error) {
	if ref.RegionID != nil && (ref.PagePath == nil || *ref.RegionID == "") {
		return domain.GenerationReference{}, domain.ErrInvalidTarget
	}
	pageRef := ref
	pageRef.RegionID = nil
	out, e := ResolveGenerationReference(tx, scope, pageRef)
	if e != nil {
		return out, e
	}
	out.ExplanationReference = ref
	return out, nil
}
