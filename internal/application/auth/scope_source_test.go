package auth_test

import (
	"encoding/json"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	appauth "haeryong.local/haeryong/internal/application/auth"
	"haeryong.local/haeryong/internal/domain"
)

// The TASK-004-01 scoped-authority rules: the byte-affecting verification
// action needs both version.read and source.read with exact anchors; the
// sealed expectation transfer follows the same anchors (plan §4/T4).
var _ = Describe("scoped source authority", Label("unit"), func() {
	bodyTarget := func(action, operation, version, evidence string) []byte {
		raw, _ := json.Marshal(map[string]string{"action": action, "operation_id": operation, "version_id": version, "evidence_id": evidence})
		return raw
	}
	base := domain.CredentialScope{Kind: "workflow", ProjectIDs: []string{"p1"}, VersionIDs: []string{"v1"}}

	It("allows the evidence_verification body action with both required actions and anchors", func() {
		scope := base
		scope.Actions = []string{"version.read", "source.read"}
		Expect(appauth.AuthorizeUploadScope(scope, "body", bodyTarget("evidence_verification", "00000000-0000-4000-8000-000000000001", "v1", "e1"))).To(Succeed())
	})

	It("denies the action without source.read or version.read", func() {
		onlyRead := base
		onlyRead.Actions = []string{"version.read"}
		Expect(appauth.AuthorizeUploadScope(onlyRead, "body", bodyTarget("evidence_verification", "00000000-0000-4000-8000-000000000001", "v1", "e1"))).To(MatchError(domain.ErrDenied))
		onlySource := base
		onlySource.Actions = []string{"source.read"}
		Expect(appauth.AuthorizeUploadScope(onlySource, "body", bodyTarget("evidence_verification", "00000000-0000-4000-8000-000000000001", "v1", "e1"))).To(MatchError(domain.ErrDenied))
	})

	It("denies the action outside the exact version anchor", func() {
		scope := base
		scope.Actions = []string{"version.read", "source.read"}
		Expect(appauth.AuthorizeUploadScope(scope, "body", bodyTarget("evidence_verification", "00000000-0000-4000-8000-000000000001", "v2", "e1"))).To(MatchError(domain.ErrDenied))
	})

	It("keeps other body actions generation-bound", func() {
		scope := base
		scope.Actions = []string{"version.read", "source.read"}
		raw, _ := json.Marshal(map[string]string{"action": "generation", "operation_id": "00000000-0000-4000-8000-000000000001", "project_id": "p1", "document_id": "d1"})
		Expect(appauth.AuthorizeUploadScope(scope, "body", raw)).To(MatchError(domain.ErrDenied))
	})
})
