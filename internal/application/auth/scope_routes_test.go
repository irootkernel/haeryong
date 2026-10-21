package auth

import (
	"context"
	"fmt"
	"path/filepath"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"haeryong.local/haeryong/internal/adapters/foundation"
	"haeryong.local/haeryong/internal/application/service"
	app "haeryong.local/haeryong/internal/application/storage"
	"haeryong.local/haeryong/internal/domain"
	sec "haeryong.local/haeryong/internal/security/session"
)

var _ = Describe("scoped route authorization for source operations", Label("persistence"), func() {
	t := GinkgoT()
	var store *foundation.Store
	var auth *Service
	var token string
	var project, version string

	BeforeEach(func() {
		selection := service.Selection{Profile: "test", DataDir: filepath.Join(t.TempDir(), "library")}
		_, e := service.Initialize(selection)
		Expect(e).NotTo(HaveOccurred())
		w, e := service.OpenWriter(selection)
		Expect(e).NotTo(HaveOccurred())
		DeferCleanup(func() { w.Close() })
		store = foundation.New(nil)
		state, closeStore, e := store.Prepare(context.Background(), w.Profile)
		Expect(e).NotTo(HaveOccurred())
		Expect(state.Ready).To(BeTrue())
		DeferCleanup(func() { _ = closeStore() })
		Expect(w.MarkStorageReady(nil)).To(Succeed())
		auth = New(store)
		pairing, e := auth.CreatePairing(context.Background(), "http://127.0.0.1:9270", "owner", "client")
		Expect(e).NotTo(HaveOccurred())
		op, _ := service.NewID()
		_, e = auth.BootstrapDecide(context.Background(), domain.PairingDecision{ID: pairing.ID, Action: "approve", Code: pairing.Code, Origin: pairing.Origin, ETag: pairing.ETag, OperationID: op})
		Expect(e).NotTo(HaveOccurred())
		redeemed, e := auth.Poll(context.Background(), pairing.ID, pairing.PollingSecret, pairing.Origin)
		Expect(e).NotTo(HaveOccurred())
		token = redeemed.Token
		project, version = "21111111-1111-4111-8111-111111111111", "23131313-1313-4131-8131-131313131313"
		Expect(store.Write(context.Background(), func(tx app.WriteTx) error {
			if e := tx.CreateProject(domain.Project{ID: project, Name: "P"}); e != nil {
				return e
			}
			if e := tx.CreateDocument(domain.Document{ID: "22121212-1212-4121-8121-121212121212", ProjectID: project, Title: "Doc"}); e != nil {
				return e
			}
			if e := tx.CreateContentSet("24141414-1414-4141-8141-141414141414", "0000000000000000000000000000000000000000000000000000000000000000"); e != nil {
				return e
			}
			if e := tx.SealContentSet("24141414-1414-4141-8141-141414141414"); e != nil {
				return e
			}
			if e := tx.PutVersion(domain.Version{ID: version, DocumentID: "22121212-1212-4121-8121-121212121212", ContentSetID: "24141414-1414-4141-8141-141414141414", Title: "Doc", ViewerProfile: "html_js_v1", SourceBasis: "unverified", PayloadSHA: "0000000000000000000000000000000000000000000000000000000000000000", ManifestSHA: "0000000000000000000000000000000000000000000000000000000000000000"}); e != nil {
				return e
			}
			return tx.PublishVersion(version, 1)
		})).To(Succeed())
	})

	scopedCount := 0
	scopedSession := func(actions []string, projects, versions []string) func() domain.Session {
		return func() domain.Session {
			scopedCount++
			secret, e := sec.Secret()
			ExpectWithOffset(1, e).NotTo(HaveOccurred())
			principal := fmt.Sprintf("c0000000-0000-4000-8000-%012d", scopedCount)
			scope := domain.CredentialScope{Kind: "workflow", Actions: actions, ProjectIDs: projects, VersionIDs: versions}
			ExpectWithOffset(1, store.Write(context.Background(), func(tx app.WriteTx) error {
				state, e := tx.AuthState()
				if e != nil {
					return e
				}
				if e := tx.PutWorkflowPrincipal(domain.WorkflowPrincipal{ID: principal, BindingID: principal, Active: true, Scope: scope, Revision: 1}); e != nil {
					return e
				}
				p, e := tx.Scope(project)
				if e != nil {
					return e
				}
				return tx.PutScopedCredential(domain.ScopedCredential{ID: principal, PrincipalID: principal, TokenHash: sec.Verifier("workflow", secret), AuthEpoch: state.AuthEpoch, Scope: scope, Permissions: map[string]int64{project: p.Project.PermissionsRevision}, ConfigRevision: p.ConfigRevision, CreatedAt: time.Now().UTC().Truncate(time.Millisecond), Revision: 1})
			})).To(Succeed())
			session, e := auth.Authenticate(context.Background(), secret)
			ExpectWithOffset(1, e).NotTo(HaveOccurred())
			return session
		}
	}

	authorize := func(record domain.Session, method, target string) error {
		var err error
		ExpectWithOffset(1, store.Write(context.Background(), func(tx app.WriteTx) error {
			err = authorizeScope(tx, record, domain.Request{Method: method, Target: target})
			return nil
		})).To(Succeed())
		return err
	}

	It("allows an in-scope verification on both route forms and denies missing actions (C3)", func() {
		record := scopedSession([]string{"version.read", "source.read"}, []string{project}, []string{version})()
		queryForm := "/api/v1/versions/" + version + "/source-evidence-verifications?evidence_id=ev"
		Expect(authorize(record, "POST", queryForm)).To(Succeed())
		pathForm := "/api/v1/versions/" + version + "/source-evidence/ev/verifications"
		Expect(authorize(record, "POST", pathForm)).To(Succeed())
		noSource := scopedSession([]string{"version.read"}, []string{project}, []string{version})()
		Expect(authorize(noSource, "POST", queryForm)).To(MatchError(domain.ErrDenied))
	})

	It("answers nondisclosing 404 for out-of-scope versions (C3)", func() {
		record := scopedSession([]string{"version.read", "source.read"}, []string{project}, []string{version})()
		foreign := "/api/v1/versions/99999999-9999-4999-8999-999999999999/source-evidence-verifications?evidence_id=ev"
		err := authorize(record, "POST", foreign)
		Expect(err).To(SatisfyAny(MatchError(domain.ErrNotFound), MatchError(domain.ErrDenied)))
	})

	It("keeps project registration and source observations owner-only (C3)", func() {
		record := scopedSession([]string{"version.read", "source.read", "project.read"}, []string{project}, []string{version})()
		Expect(authorize(record, "POST", "/api/v1/projects")).To(MatchError(domain.ErrDenied))
		Expect(authorize(record, "POST", "/api/v1/versions/"+version+"/source-observations")).To(MatchError(domain.ErrDenied))
	})

	It("allows project sources reads for project.read with the anchor (C3)", func() {
		record := scopedSession([]string{"project.read"}, []string{project}, nil)()
		Expect(authorize(record, "GET", "/api/v1/projects/"+project+"/sources")).To(Succeed())
		err := authorize(record, "GET", "/api/v1/projects/99999999-9999-4999-8999-999999999999/sources")
		Expect(err).To(SatisfyAny(MatchError(domain.ErrNotFound), MatchError(domain.ErrDenied)))
	})

	It("lets the owner bypass the scoped route rules", func() {
		session, e := auth.Authenticate(context.Background(), token)
		Expect(e).NotTo(HaveOccurred())
		Expect(authorize(session, "POST", "/api/v1/projects")).To(Succeed())
	})
})
