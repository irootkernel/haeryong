package auth

import (
	"errors"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	app "haeryong.local/haeryong/internal/application/storage"
	"haeryong.local/haeryong/internal/domain"
)

type questionEventTx struct {
	app.WriteTx
	question              domain.Question
	failure, scopeFailure error
}

func (t questionEventTx) Question(string) (domain.Question, error) { return t.question, t.failure }
func (t questionEventTx) Version(string) (domain.Version, error) {
	return domain.Version{ID: "version", DocumentID: "document"}, nil
}
func (t questionEventTx) Document(string) (domain.Document, error) {
	return domain.Document{ID: "document", ProjectID: "project", Lifecycle: "active"}, nil
}
func (t questionEventTx) Scope(string) (domain.ScopeState, error) {
	return domain.ScopeState{}, t.scopeFailure
}

var _ = It("T5QAUTH01 question event projection masks foreign ownership missing records and current denied context without suppressing storage failures", Label("unit"), func() {
	event := domain.Event{StreamEpoch: "epoch", Sequence: 37, ResourceKind: "question", ResourceID: "question", Kind: "answer.saved", Body: []byte(`{"answer_id":"private"}`)}
	owner := domain.Session{PrincipalID: "owner", Scope: domain.CredentialScope{Kind: "owner"}}
	tx := questionEventTx{question: domain.Question{OwnerID: "owner", VersionID: "version"}}
	visible, e := visibleQuestionEvent(tx, owner, event)
	Expect(e).NotTo(HaveOccurred())
	Expect(visible).To(Equal(event))
	for _, test := range []struct {
		actor domain.Session
		tx    questionEventTx
	}{{domain.Session{PrincipalID: "scoped", Scope: domain.CredentialScope{Kind: "workflow"}}, tx}, {domain.Session{PrincipalID: "other", Scope: domain.CredentialScope{Kind: "owner"}}, tx}, {owner, questionEventTx{failure: domain.ErrNotFound}}, {owner, questionEventTx{question: tx.question, scopeFailure: domain.ErrDenied}}} {
		masked, e := visibleQuestionEvent(test.tx, test.actor, event)
		Expect(e).NotTo(HaveOccurred())
		Expect(masked).To(Equal(domain.Event{StreamEpoch: "epoch", Sequence: 37, ResourceKind: "library", Kind: "library.invalidated"}))
	}
	fault := errors.New("controlled storage failure")
	_, e = visibleQuestionEvent(questionEventTx{failure: fault}, owner, event)
	Expect(e).To(MatchError(fault))
})
