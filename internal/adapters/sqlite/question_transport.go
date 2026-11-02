package sqlite

import (
	"encoding/json"
	"reflect"

	"haeryong.local/haeryong/internal/domain"
)

func (t *writeTx) validateQuestionTransport(a domain.ProviderAttempt, transport domain.ProviderTransport) error {
	job, e := t.Job(a.JobID)
	if e != nil {
		return e
	}
	var scope domain.ProviderScope
	if json.Unmarshal(job.Scope, &scope) != nil {
		return domain.ErrInvalidTarget
	}
	if scope.Invocation == nil || scope.Invocation.Question == nil {
		if transport.Question != nil {
			return domain.ErrInvalidTarget
		}
		return nil
	}
	input := scope.Invocation.Question
	actual := transport.Question
	if job.Kind != "question" || transport.Sandbox != "read-only" || transport.Generation != nil || actual == nil || scope.Invocation.PromptRef == nil || actual.QuestionID != input.QuestionID || actual.Text != *scope.Invocation.PromptRef || actual.SourceMode != input.SourceMode || !reflect.DeepEqual(actual.Context, input.Context) || !reflect.DeepEqual(actual.Explanation, input.Explanation) {
		return domain.ErrInvalidTarget
	}
	if actual.PromptInputAcknowledged || actual.ContextInputAcknowledged || actual.ExplanationInputAcknowledged {
		if transport.AcceptedAt == "" || len(transport.FrameSHA) != 64 || !actual.PromptInputAcknowledged || !actual.ExplanationInputAcknowledged || actual.ContextInputAcknowledged != (actual.Context != nil) {
			return domain.ErrInvalidTarget
		}
	}
	return nil
}

func questionTransportAdvance(old, next *domain.QuestionTransport) bool {
	if old == nil || next == nil {
		return old == nil && next == nil
	}
	if old.PromptInputAcknowledged && !next.PromptInputAcknowledged || old.ContextInputAcknowledged && !next.ContextInputAcknowledged || old.ExplanationInputAcknowledged && !next.ExplanationInputAcknowledged {
		return false
	}
	a, b := *old, *next
	a.PromptInputAcknowledged, b.PromptInputAcknowledged = false, false
	a.ContextInputAcknowledged, b.ContextInputAcknowledged = false, false
	a.ExplanationInputAcknowledged, b.ExplanationInputAcknowledged = false, false
	return reflect.DeepEqual(a, b)
}
