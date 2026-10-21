package source_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"sync/atomic"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"haeryong.local/haeryong/internal/application/query"
	appsource "haeryong.local/haeryong/internal/application/source"
	app "haeryong.local/haeryong/internal/application/storage"
	"haeryong.local/haeryong/internal/domain"
	sec "haeryong.local/haeryong/internal/security/session"
)

// mustObserve runs one observation and requires its 201.
func mustObserve(t GinkgoTInterface, f *observationFixture, key, body string) map[string]any {
	t.Helper()
	response, e := f.observe(key, body)
	ExpectWithOffset(1, e).NotTo(HaveOccurred())
	ExpectWithOffset(1, response.Status).To(Equal(201), string(response.Body))
	return outcome(t, response)
}

// errorReason reads details.reason of a stored error envelope.
func errorReason(t GinkgoTInterface, response domain.Response) string {
	t.Helper()
	var body struct {
		Error struct {
			Details map[string]any `json:"details"`
		} `json:"error"`
	}
	ExpectWithOffset(1, json.Unmarshal(response.Body, &body)).To(Succeed())
	reason, _ := body.Error.Details["reason"].(string)
	return reason
}

// observationState reads the receipt state, the intent state and the
// committed per-source rows of one observation key.
type observationState struct {
	receipt     string
	intent      string
	rows        int
	runExists   bool
	runID       string
	runCaptured string
	runScope    map[string]any
}

func readObservationState(t GinkgoTInterface, f *observationFixture, key string) observationState {
	t.Helper()
	return readObservationStateAs(t, f, ownerPrincipal(&f.vFixture), key)
}

func readObservationStateAs(t GinkgoTInterface, f *observationFixture, owner, key string) observationState {
	t.Helper()
	out := observationState{}
	ExpectWithOffset(1, f.store.Write(context.Background(), func(tx app.WriteTx) error {
		receipt, e := tx.Receipt(domain.OperationKey{PrincipalID: owner, OperationID: key})
		if e == nil {
			out.receipt = receipt.State
		} else if !errors.Is(e, domain.ErrNotFound) {
			return e
		}
		intent, e := tx.CheckIntentByOperation(owner, key)
		if e == nil {
			out.intent = intent.State
		} else if !errors.Is(e, domain.ErrNotFound) {
			return e
		}
		rows, e := tx.ObservationsByOperation(owner, key)
		if e != nil {
			return e
		}
		out.rows = len(rows)
		run, exists, e := tx.ObservationRunForOperation(owner, key)
		out.runExists, out.runID, out.runCaptured = exists, run.ID, run.TargetSetID
		if exists {
			if e := json.Unmarshal(run.ScopeJSON, &out.runScope); e != nil {
				return e
			}
		}
		return e
	})).To(Succeed())
	return out
}

// failingStore fails one storage method of its writer transactions with an
// ordinary storage error while armed (W3b L8).
type failingStore struct {
	app.Store
	method string
	armed  *atomic.Bool
}

func newFailingStore(store app.Store, method string) failingStore {
	return failingStore{Store: store, method: method, armed: &atomic.Bool{}}
}

func (s failingStore) Write(ctx context.Context, fn func(app.WriteTx) error) error {
	return s.Store.Write(ctx, func(tx app.WriteTx) error { return fn(failingTx{WriteTx: tx, store: s}) })
}

type failingTx struct {
	app.WriteTx
	store failingStore
}

func (t failingTx) fails(method string) bool {
	return t.store.method == method && t.store.armed.Load()
}

func (t failingTx) VersionPages(id string) ([]domain.Page, error) {
	if t.fails("VersionPages") {
		return nil, errStorageFailure
	}
	return t.WriteTx.VersionPages(id)
}

func (t failingTx) Configuration() (domain.Configuration, error) {
	if t.fails("Configuration") {
		return domain.Configuration{}, errStorageFailure
	}
	return t.WriteTx.Configuration()
}

func (t failingTx) CoverageSetByID(id string) (domain.CoverageSet, error) {
	if t.fails("CoverageSetByID") {
		return domain.CoverageSet{}, errStorageFailure
	}
	return t.WriteTx.CoverageSetByID(id)
}

func (t failingTx) ObservationRunByID(id string) (domain.ObservationRun, error) {
	if t.fails("ObservationRunByID") {
		return domain.ObservationRun{}, errStorageFailure
	}
	return t.WriteTx.ObservationRunByID(id)
}

func (t failingTx) VersionCapture(versionID, targetSetID string) (domain.TargetSet, error) {
	if t.fails("VersionCapture") {
		return domain.TargetSet{}, errStorageFailure
	}
	return t.WriteTx.VersionCapture(versionID, targetSetID)
}

func (t failingTx) VersionRegion(versionID, pagePath, regionID string) (domain.Region, error) {
	if t.fails("VersionRegion") {
		return domain.Region{}, errStorageFailure
	}
	return t.WriteTx.VersionRegion(versionID, pagePath, regionID)
}

// gitFailingReader keeps the real reader but fails one stage with a genuine
// git failure (S12).
type gitFailingReader struct {
	inner appsource.Reader
	at    string
}

func (r *gitFailingReader) ResolveRevision(ctx context.Context, root, revision string) error {
	return r.inner.ResolveRevision(ctx, root, revision)
}

func (r *gitFailingReader) ResolveHead(ctx context.Context, root string) (string, error) {
	if r.at == "resolve" {
		return "", appsource.ErrGitUnavailable
	}
	return r.inner.ResolveHead(ctx, root)
}

func (r *gitFailingReader) ChangedPaths(ctx context.Context, root, base, tip string, cited []string) ([]string, error) {
	if r.at == "diff" {
		return nil, appsource.ErrGitUnavailable
	}
	return r.inner.ChangedPaths(ctx, root, base, tip, cited)
}

func (r *gitFailingReader) ReadBlob(ctx context.Context, root, revision, path string) (io.ReadCloser, error) {
	return r.inner.ReadBlob(ctx, root, revision, path)
}

// observeParkedAt runs one observation, parks its reader at the stage, runs
// during() while it is parked and returns the settled response.
func observeParkedAt(t GinkgoTInterface, f *observationFixture, key, stage string, during func()) domain.Response {
	t.Helper()
	parked, release := make(chan struct{}), make(chan struct{})
	original := f.core.Reader
	f.core.Reader = &parkingReader{inner: original, at: stage, park: func() { close(parked); <-release }}
	defer func() { f.core.Reader = original }()
	done := make(chan struct{})
	var response domain.Response
	var err error
	go func() {
		defer GinkgoRecover()
		defer close(done)
		response, err = f.core.Observe(context.Background(), f.token, f.version, key, []byte(`{}`))
	}()
	<-parked
	during()
	close(release)
	<-done
	ExpectWithOffset(1, err).NotTo(HaveOccurred())
	return response
}

var _ = Describe("observation receipts and planning", Label("persistence"), func() {
	t := GinkgoT()

	// W3b L2: an identical completed key replays its stored 201 under current
	// authority before the current mappings are consulted; new work plans
	// against them and leaves no receipt when planning fails.
	It("replays an identical completed key after its mappings were removed", func() {
		f := newObservationFixture(t, "current")
		key := "33000000-0000-4000-8000-000000000001"
		first, e := f.observe(key, `{"source_ids":["git-2","git-1"]}`)
		Expect(e).NotTo(HaveOccurred())
		Expect(first.Status).To(Equal(201))
		f.configureMappings(t, "current")
		replay, e := f.observe(key, `{"source_ids":["git-1","git-2"]}`)
		Expect(e).NotTo(HaveOccurred())
		Expect(replay.Status).To(Equal(201))
		Expect(replay.Body).To(Equal(first.Body), "the stored 201 replays byte for byte")
		fresh := "33000000-0000-4000-8000-000000000002"
		_, e = f.observe(fresh, "")
		Expect(e).To(MatchError(sec.ErrInvalid), "new work plans against the current mappings")
		Expect(readObservationState(t, f, fresh).receipt).To(BeEmpty(), "a planning failure leaves no receipt")
		f.configureObservation(t, "current", true)
		// With the mappings restored, request validation alone decides:
		// duplicate and empty-string ids stay invalid; an empty array is the
		// same request as its omission.
		_, e = f.observe("33000000-0000-4000-8000-000000000003", `{"source_ids":["git-1","git-1"]}`)
		Expect(e).To(MatchError(sec.ErrInvalid))
		_, e = f.observe("33000000-0000-4000-8000-000000000004", `{"source_ids":[""]}`)
		Expect(e).To(MatchError(sec.ErrInvalid))
		// W3c 1: the body is one JSON object and the nested scope is closed.
		for i, body := range []string{`null`, `{"scope":{"kind":"version","unexpected":1}}`, `{"scope":{"kind":"page","page_path":"pages/first.html","region_id":null}}`, `{"scope":null}`, `{"scope":"version"}`} {
			key := "33000000-0000-4000-8000-00000000006" + string(rune('1'+i))
			_, e = f.observe(key, body)
			Expect(e).To(MatchError(sec.ErrInvalid), body)
			Expect(readObservationState(t, f, key).receipt).To(BeEmpty(), body)
		}
		omitted := "33000000-0000-4000-8000-000000000005"
		mustObserve(t, f, omitted, "")
		same, e := f.observe(omitted, `{"source_ids":[]}`)
		Expect(e).NotTo(HaveOccurred())
		Expect(same.Status).To(Equal(201), "an empty source_ids array replays the omitted form")
	})

	// W3b L4(c): a cited repo_id mapped by two configured sources fails closed
	// before reservation; the default search composes the no-capture form
	// while the explicit target and the plain coverage endpoint keep their
	// exact stored records.
	It("fails closed on an ambiguous mapping and keeps stored records readable", func() {
		f := newObservationFixture(t, "current")
		f.repoA.commit(t, map[string]string{"a.txt": "alpha changed\n"})
		run := mustObserve(t, f, "33000000-0000-4000-8000-000000000011", "")
		target, setID := mapString(run["target_set_id"]), mapString(run["coverage_set_id"])
		f.configureMappings(t, "current", obsMapping("git-1", f.repoA.root, repoAID), obsMapping("git-2", f.repoB.root, repoBID), obsMapping("git-9", f.repoA.root, repoAID))
		key := "33000000-0000-4000-8000-000000000012"
		response, e := f.observe(key, "")
		Expect(e).NotTo(HaveOccurred())
		Expect(response.Status).To(Equal(409))
		Expect(errorCode(t, response)).To(Equal("SOURCE_UNAVAILABLE"))
		Expect(errorReason(t, response)).To(Equal("mapping_ambiguous"))
		state := readObservationState(t, f, key)
		Expect(state.receipt).To(BeEmpty(), "no receipt before reservation")
		Expect(state.runExists).To(BeFalse(), "neither duplicate was observed")
		applicable := f.applicable(t, query.AssessmentQuery{})
		Expect(applicable["target_set_id"]).To(BeNil(), "the default search never picks one of the duplicates")
		Expect(applicable["status"]).To(Equal("not_assessed"))
		explicit := f.applicable(t, query.AssessmentQuery{TargetSetID: target})
		Expect(explicit["target_set_id"]).To(Equal(target))
		Expect(explicit["reassessment_required"]).To(BeTrue())
		Expect(f.coverage(t, setID, "")).To(HaveLen(2), "the plain coverage endpoint keeps the stored units")
	})

	// W3b L15: a page scope must name an HTML page of the immutable version.
	It("rejects a scope whose page is not part of the version before any receipt", func() {
		f := newObservationFixture(t, "current")
		key := "33000000-0000-4000-8000-000000000021"
		response, e := f.observe(key, `{"scope":{"kind":"page","page_path":"pages/none.html"}}`)
		Expect(e).NotTo(HaveOccurred())
		Expect(response.Status).To(Equal(400))
		Expect(errorCode(t, response)).To(Equal("INVALID_REQUEST"))
		var body struct {
			Error struct {
				Details map[string]any `json:"details"`
			} `json:"error"`
		}
		Expect(json.Unmarshal(response.Body, &body)).To(Succeed())
		Expect(body.Error.Details).To(HaveKeyWithValue("page_path", "pages/none.html"))
		Expect(readObservationState(t, f, key).receipt).To(BeEmpty())
		region, e := f.observe("33000000-0000-4000-8000-000000000022", `{"scope":{"kind":"region","page_path":"pages/none.html","region_id":"R1"}}`)
		Expect(e).NotTo(HaveOccurred())
		Expect(region.Status).To(Equal(400), "a region scope on an unknown page is the same request error")
	})
})

var _ = Describe("observation settlements", Label("persistence"), func() {
	t := GinkgoT()

	// W3b L3: a storage failure of the stage-2 target persist settles the
	// receipt terminally; the first response equals the replay.
	It("settles a stage-2 storage failure as a durable 503", func() {
		f := newObservationFixture(t, "current")
		armed := &armedStore{Store: f.store}
		f.core.Store = armed
		f.core.Fault = func(stage string) error {
			if stage == "after_reservation" {
				armed.arm()
			}
			return nil
		}
		key := "34000000-0000-4000-8000-000000000001"
		first, e := f.observe(key, "")
		Expect(e).NotTo(HaveOccurred())
		Expect(first.Status).To(Equal(503))
		Expect(errorCode(t, first)).To(Equal("SERVICE_RECOVERING"))
		Expect(errorReason(t, first)).To(Equal("stage_failure"))
		f.core.Fault = nil
		replay, e := f.observe(key, "")
		Expect(e).NotTo(HaveOccurred())
		Expect(replay.Body).To(Equal(first.Body), "the first response equals the replay")
		state := readObservationState(t, f, key)
		Expect(state.receipt).To(Equal("failed"))
		Expect(state.intent).To(Equal("failed"))
		Expect(state.runExists).To(BeFalse(), "the stage-2 transaction rolled back")
		var captures []domain.TargetSet
		Expect(f.store.Write(context.Background(), func(tx app.WriteTx) error {
			var e error
			captures, e = tx.SourceCaptures(f.project)
			return e
		})).To(Succeed())
		Expect(captures).To(BeEmpty(), "no target set persisted")
	})

	// W3b L17: the policy recheck at commit settles 412 typed, with no rows;
	// the stored 412 replays after the configuration settles again.
	It("settles 412 when configuration or permissions advance mid-check", func() {
		f := newObservationFixture(t, "current")
		configKey := "34000000-0000-4000-8000-000000000011"
		response := observeParkedAt(t, f, configKey, "diff", func() { bumpConfig(t, &f.vFixture) })
		Expect(response.Status).To(Equal(412))
		Expect(errorCode(t, response)).To(Equal("PRECONDITION_FAILED"))
		Expect(errorReason(t, response)).To(Equal("config_changed"))
		state := readObservationState(t, f, configKey)
		Expect(state.rows).To(Equal(0))
		Expect(state.intent).To(Equal("failed"))
		Expect(state.receipt).To(Equal("failed"))
		replay, e := f.observe(configKey, "")
		Expect(e).NotTo(HaveOccurred())
		Expect(replay.Body).To(Equal(response.Body), "the stored 412 replays")
		permissionsKey := "34000000-0000-4000-8000-000000000012"
		response = observeParkedAt(t, f, permissionsKey, "diff", func() {
			Expect(f.store.Write(context.Background(), func(tx app.WriteTx) error {
				state, e := tx.Scope(f.project)
				if e != nil {
					return e
				}
				return tx.ApplyProjectConfiguration(f.project, "", "", true, state.Project.PermissionsRevision)
			})).To(Succeed())
		})
		Expect(response.Status).To(Equal(412))
		Expect(errorReason(t, response)).To(Equal("permissions_changed"))
		state = readObservationState(t, f, permissionsKey)
		Expect(state.rows).To(Equal(0))
		Expect(state.intent).To(Equal("failed"))
		Expect(state.receipt).To(Equal("failed"))
	})

	// W3b L17: the commit-stage authority recheck settles typed with no rows.
	// A principal that stops being the owner cannot occur for an owner token,
	// so the 403 branch has no reachable observation case.
	It("settles 401 and 404 when authority is lost mid-check", func() {
		f := newObservationFixture(t, "current")
		session, e := f.auth.Authenticate(context.Background(), f.token)
		Expect(e).NotTo(HaveOccurred())
		owner := session.PrincipalID
		revoked := "34000000-0000-4000-8000-000000000021"
		response := observeParkedAt(t, f, revoked, "diff", func() {
			_, re := f.auth.Revoke(context.Background(), f.token, session.ID)
			Expect(re).NotTo(HaveOccurred())
		})
		Expect(response.Status).To(Equal(401))
		Expect(errorCode(t, response)).To(Equal("SESSION_REVOKED"))
		state := readObservationStateAs(t, f, owner, revoked)
		Expect(state.rows).To(Equal(0))
		Expect(state.intent).To(Equal("failed"))
		Expect(state.receipt).To(Equal("failed"))

		g := newObservationFixture(t, "current")
		trashed := "34000000-0000-4000-8000-000000000022"
		response = observeParkedAt(t, g, trashed, "diff", func() {
			Expect(g.store.Write(context.Background(), func(tx app.WriteTx) error {
				return tx.TestTrashDocument(g.document)
			})).To(Succeed())
		})
		Expect(response.Status).To(Equal(404))
		Expect(errorCode(t, response)).To(Equal("NOT_FOUND"))
		Expect(errorReason(t, response)).To(Equal("target_no_longer_visible"))
		state = readObservationState(t, g, trashed)
		Expect(state.rows).To(Equal(0))
		Expect(state.intent).To(Equal("failed"))
		Expect(state.receipt).To(Equal("failed"))
	})

	// W3b L17: injected faults at each staged boundary settle 503
	// SERVICE_RECOVERING with the intent failed and no rows.
	It("settles injected faults at every staged boundary", func() {
		f := newObservationFixture(t, "current")
		for i, stage := range []string{"after_reservation", "before_observation_target_persist", "before_observation_commit"} {
			stage := stage
			f.core.Fault = func(at string) error {
				if at == stage {
					return errors.New("injected")
				}
				return nil
			}
			key := "34000000-0000-4000-8000-00000000003" + string(rune('1'+i))
			response, e := f.observe(key, "")
			Expect(e).NotTo(HaveOccurred())
			Expect(response.Status).To(Equal(503), stage)
			Expect(errorCode(t, response)).To(Equal("SERVICE_RECOVERING"), stage)
			Expect(errorReason(t, response)).To(Equal("injected_"+stage), stage)
			state := readObservationState(t, f, key)
			Expect(state.intent).To(Equal("failed"), stage)
			Expect(state.receipt).To(Equal("failed"), stage)
			Expect(state.rows).To(Equal(0), stage)
		}
		f.core.Fault = nil
	})

	// W3b L17 / S12: a genuine git failure at resolve or at diff settles 503
	// FILE_ACCESS_ERROR git_unavailable and never becomes a recorded source
	// fact.
	It("settles a genuine git failure without recording a source fact", func() {
		f := newObservationFixture(t, "current")
		original := f.core.Reader
		for i, stage := range []string{"resolve", "diff"} {
			f.core.Reader = &gitFailingReader{inner: original, at: stage}
			key := "34000000-0000-4000-8000-00000000004" + string(rune('1'+i))
			response, e := f.observe(key, "")
			Expect(e).NotTo(HaveOccurred())
			Expect(response.Status).To(Equal(503), stage)
			Expect(errorCode(t, response)).To(Equal("FILE_ACCESS_ERROR"), stage)
			Expect(errorReason(t, response)).To(Equal("git_unavailable"), stage)
			state := readObservationState(t, f, key)
			Expect(state.rows).To(Equal(0), stage)
			Expect(state.intent).To(Equal("failed"), stage)
			Expect(state.receipt).To(Equal("failed"), stage)
		}
		f.core.Reader = original
		list, e := f.queries.ProjectSources(context.Background(), f.token, f.project, query.Options{})
		Expect(e).NotTo(HaveOccurred())
		for _, item := range list.Items {
			Expect(item["last_availability"]).To(BeNil(), "no git failure became a recorded source fact")
		}
	})
})

var _ = Describe("observation storage errors", Label("persistence"), func() {
	t := GinkgoT()

	// W3b L8: before reservation a storage error propagates with no receipt;
	// after reservation it settles durable 503 without recording an
	// undeclared fact or a denominator.
	It("propagates a page lookup failure before and after reservation", func() {
		f := newObservationFixture(t, "current")
		failing := newFailingStore(f.store, "VersionPages")
		f.core.Store = failing
		failing.armed.Store(true)
		key := "35000000-0000-4000-8000-000000000001"
		_, e := f.observe(key, `{"scope":{"kind":"page","page_path":"pages/first.html"}}`)
		Expect(e).To(MatchError(errStorageFailure), "planning propagates the storage error")
		state := readObservationState(t, f, key)
		Expect(state.receipt).To(BeEmpty(), "no receipt")
		Expect(state.intent).To(BeEmpty(), "no intent")
		failing.armed.Store(false)
		f.core.Fault = func(stage string) error {
			if stage == "before_observation_commit" {
				failing.armed.Store(true)
			}
			return nil
		}
		committed := "35000000-0000-4000-8000-000000000002"
		response, e := f.observe(committed, "")
		Expect(e).NotTo(HaveOccurred())
		Expect(response.Status).To(Equal(503))
		Expect(errorReason(t, response)).To(Equal("stage_failure"))
		failing.armed.Store(false)
		f.core.Fault = nil
		state = readObservationState(t, f, committed)
		Expect(state.rows).To(Equal(0), "no per-source rows and no denominator")
		Expect(state.receipt).To(Equal("failed"))
		Expect(state.intent).To(Equal("failed"))
	})

	It("propagates a configuration failure while planning", func() {
		f := newObservationFixture(t, "current")
		failing := newFailingStore(f.store, "Configuration")
		f.core.Store = failing
		failing.armed.Store(true)
		key := "35000000-0000-4000-8000-000000000011"
		_, e := f.observe(key, "")
		Expect(e).To(MatchError(errStorageFailure))
		failing.armed.Store(false)
		state := readObservationState(t, f, key)
		Expect(state.receipt).To(BeEmpty())
		Expect(state.intent).To(BeEmpty())
	})

	// W3b L8: the assessments read returns storage errors instead of a null
	// applicable, the pinned default or a 404.
	It("returns storage errors from the assessments reads", func() {
		f := newObservationFixture(t, "current")
		run := mustObserve(t, f, "35000000-0000-4000-8000-000000000021", "")
		setID, target := mapString(run["coverage_set_id"]), mapString(run["target_set_id"])
		for _, method := range []string{"VersionPages", "Configuration", "VersionRegion", "CoverageSetByID", "ObservationRunByID", "VersionCapture"} {
			failing := newFailingStore(f.store, method)
			queries := query.New(failing, f.auth, f.store)
			failing.armed.Store(true)
			var e error
			switch method {
			case "VersionPages":
				_, e = queries.Assessments(context.Background(), f.token, f.version, query.AssessmentQuery{ScopeKind: "page", PagePath: "pages/first.html"})
			case "Configuration":
				_, e = queries.Assessments(context.Background(), f.token, f.version, query.AssessmentQuery{})
			case "VersionRegion":
				_, e = queries.Assessments(context.Background(), f.token, f.version, query.AssessmentQuery{ScopeKind: "region", PagePath: "pages/first.html", RegionID: "R1"})
			case "CoverageSetByID":
				_, e = queries.AssessmentCoverage(context.Background(), f.token, f.version, setID, "", query.Options{})
			case "ObservationRunByID":
				_, e = queries.AssessmentCoverage(context.Background(), f.token, f.version, setID, mapString(run["run_id"]), query.Options{})
			case "VersionCapture":
				_, e = queries.Assessments(context.Background(), f.token, f.version, query.AssessmentQuery{TargetSetID: target})
			}
			Expect(e).To(MatchError(errStorageFailure), method)
			failing.armed.Store(false)
		}
	})
})
