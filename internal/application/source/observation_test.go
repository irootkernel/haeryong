package source_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"haeryong.local/haeryong/internal/adapters/foundation"
	"haeryong.local/haeryong/internal/adapters/gitreader"
	appauth "haeryong.local/haeryong/internal/application/auth"
	appconfig "haeryong.local/haeryong/internal/application/configuration"
	"haeryong.local/haeryong/internal/application/query"
	"haeryong.local/haeryong/internal/application/service"
	appsource "haeryong.local/haeryong/internal/application/source"
	app "haeryong.local/haeryong/internal/application/storage"
	"haeryong.local/haeryong/internal/domain"
)

// observationClock steps every read so run stamps order deterministically.
type observationClock struct {
	now  time.Time
	step time.Duration
}

func (c *observationClock) Now() time.Time {
	stamp := c.now
	c.now = c.now.Add(c.step)
	return stamp
}

// obsRepo is one disposable synthetic Git repository under TMPDIR with real
// hashes (fixtures.md rule).
type obsRepo struct {
	root string
}

func (r *obsRepo) git(t GinkgoTInterface, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = r.root
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=fixture", "GIT_AUTHOR_EMAIL=fixture@example.invalid", "GIT_COMMITTER_NAME=fixture", "GIT_COMMITTER_EMAIL=fixture@example.invalid")
	var out strings.Builder
	cmd.Stdout = &out
	cmd.Stderr = &out
	ExpectWithOffset(1, cmd.Run()).To(Succeed(), "git %v: %s", args, out.String())
	return strings.TrimSpace(out.String())
}

func newObsRepo(t GinkgoTInterface, files map[string]string) *obsRepo {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		Skip("git binary unavailable")
	}
	repo := &obsRepo{root: t.TempDir()}
	repo.git(t, "init", "--initial-branch=main")
	repo.write(t, files)
	repo.git(t, "add", "-A")
	repo.git(t, "commit", "-m", "base")
	return repo
}

func (r *obsRepo) write(t GinkgoTInterface, files map[string]string) {
	t.Helper()
	for path, content := range files {
		ExpectWithOffset(1, os.MkdirAll(filepath.Dir(filepath.Join(r.root, path)), 0o755)).To(Succeed())
		ExpectWithOffset(1, os.WriteFile(filepath.Join(r.root, path), []byte(content), 0o644)).To(Succeed())
	}
}

func (r *obsRepo) commit(t GinkgoTInterface, files map[string]string) string {
	t.Helper()
	r.write(t, files)
	r.git(t, "add", "-A")
	r.git(t, "commit", "-m", "advance")
	return r.head(t)
}

func (r *obsRepo) head(t GinkgoTInterface) string {
	t.Helper()
	return r.git(t, "rev-parse", "HEAD")
}

func (r *obsRepo) reset(t GinkgoTInterface, revision string) {
	t.Helper()
	r.git(t, "reset", "--hard", revision)
}

func (r *obsRepo) breakRoot(t GinkgoTInterface) {
	t.Helper()
	ExpectWithOffset(1, os.RemoveAll(r.root)).To(Succeed())
}

const (
	repoAID = "77777777-7777-4777-8777-777777777777"
	repoBID = "88888888-8888-4888-8888-888888888888"
)

// observationFixture reuses the verification fixture's store/auth/token with
// the real git reader, a stepping clock and the query/configuration composers.
type observationFixture struct {
	vFixture
	clock    *observationClock
	repoA    *obsRepo
	repoB    *obsRepo
	baseA    string
	baseB    string
	queries  *query.Service
	statuses *appconfig.Service
}

// observationOptions varies the observed version's layout.
type observationOptions struct {
	noPages  bool
	manifest string
	layouts  []observationLayout
}

// observationLayout adds authored rows before publication; prepare runs
// first, outside the ledger transaction (repository commits).
type observationLayout struct {
	prepare func(f *observationFixture)
	rows    func(f *observationFixture, tx app.WriteTx) error
}

// withLayout adds a layout variant to the observed version.
func withLayout(prepare func(f *observationFixture), rows func(f *observationFixture, tx app.WriteTx) error) observationOption {
	return func(o *observationOptions) {
		o.layouts = append(o.layouts, observationLayout{prepare: prepare, rows: rows})
	}
}

type observationOption func(*observationOptions)

// withoutPages publishes the version without HTML pages (one whole-version
// unit, plan §7.6).
func withoutPages() observationOption {
	return func(o *observationOptions) { o.noPages = true }
}

// withManifest stores the given normalized manifest for the version.
func withManifest(raw string) observationOption {
	return func(o *observationOptions) { o.manifest = raw }
}

func newObservationFixture(t GinkgoTInterface, mode string, options ...observationOption) *observationFixture {
	GinkgoHelper()
	return newObservationBase(t, mode, *newVerificationFixture(t), options...)
}

func newObservationBase(t GinkgoTInterface, mode string, base vFixture, options ...observationOption) *observationFixture {
	GinkgoHelper()
	opts := observationOptions{manifest: `{"schema_version":"1.0","version":{"source_intent":null}}`}
	for _, option := range options {
		option(&opts)
	}
	f := &observationFixture{}
	f.vFixture = base
	if mode == "" {
		mode = "pinned"
	}
	f.repoA = newObsRepo(t, map[string]string{"a.txt": "alpha\n"})
	f.repoB = newObsRepo(t, map[string]string{"b.txt": "beta\n"})
	f.baseA, f.baseB = f.repoA.head(t), f.repoB.head(t)
	f.clock = &observationClock{now: time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC), step: 10 * time.Millisecond}
	f.core = appsource.New(f.store, f.auth, f.store, gitreader.New())
	f.core.Now = f.clock.Now
	f.queries = query.New(f.store, f.auth, f.store)
	f.statuses = appconfig.New(f.store, f.auth, nil)
	f.buildObservedVersion(t, mode, opts)
	return f
}

// buildObservedVersion publishes a version citing two git sources with two
// pages, one anchored region per page, and configures both mappings plus the
// project's source_mode_default.
func (f *observationFixture) buildObservedVersion(t GinkgoTInterface, mode string, opts observationOptions) {
	t.Helper()
	ctx := context.Background()
	f.project, f.document, f.version = "21111111-1111-4111-8111-111111111121", "22121212-1212-4121-8121-121212121222", "23131313-1313-4131-8131-131313131333"
	// Stage the normalized manifest object before the ledger transaction
	// (the store writer is not reentrant).
	manifest, e := f.store.Put(ctx, strings.NewReader(opts.manifest), func(tx app.WriteTx, object domain.Object) error { return nil })
	Expect(e).NotTo(HaveOccurred())
	for _, layout := range opts.layouts {
		if layout.prepare != nil {
			layout.prepare(f)
		}
	}
	sourceA := `{"id":"srcA","kind":"git","retention_mode":"reference_only","repo_id":"` + repoAID + `","object_format":"sha1","exact_revision":"` + f.baseA + `"}`
	sourceB := `{"id":"srcB","kind":"git","retention_mode":"reference_only","repo_id":"` + repoBID + `","object_format":"sha1","exact_revision":"` + f.baseB + `"}`
	Expect(f.store.Write(ctx, func(tx app.WriteTx) error {
		if e := tx.CreateProject(domain.Project{ID: f.project, Name: "Observed"}); e != nil {
			return e
		}
		if e := tx.CreateDocument(domain.Document{ID: f.document, ProjectID: f.project, Title: "Observed"}); e != nil {
			return e
		}
		if e := tx.CreateContentSet("24141414-1414-4141-8141-141414141444", vsha(nil)); e != nil {
			return e
		}
		if e := tx.SealContentSet("24141414-1414-4141-8141-141414141444"); e != nil {
			return e
		}
		if e := tx.PutVersion(domain.Version{ID: f.version, DocumentID: f.document, ContentSetID: "24141414-1414-4141-8141-141414141444", Title: "Observed", ViewerProfile: "html_js_v1", SourceBasis: "evidence_recorded", PayloadSHA: vsha(nil), ManifestSHA: manifest.Hash}); e != nil {
			return e
		}
		for id, meta := range map[string]string{"srcA": sourceA, "srcB": sourceB} {
			if e := tx.PutSource(domain.Source{VersionID: f.version, ID: id, Metadata: json.RawMessage(meta)}); e != nil {
				return e
			}
		}
		if e := tx.PutEvidence(domain.Evidence{VersionID: f.version, ID: "evA", SourceID: "srcA", Metadata: json.RawMessage(`{"id":"evA","source_id":"srcA","capture_status":"reference_only","path":"a.txt"}`)}); e != nil {
			return e
		}
		if e := tx.PutEvidence(domain.Evidence{VersionID: f.version, ID: "evB", SourceID: "srcB", Metadata: json.RawMessage(`{"id":"evB","source_id":"srcB","capture_status":"reference_only","path":"b.txt"}`)}); e != nil {
			return e
		}
		if opts.noPages {
			return f.publishWithLayouts(tx, opts)
		}
		for _, page := range []string{"pages/first.html", "pages/second.html"} {
			if e := tx.PutPage(domain.Page{VersionID: f.version, Path: page}); e != nil {
				return e
			}
		}
		if e := tx.PutRegion(domain.Region{VersionID: f.version, PagePath: "pages/first.html", ID: "R1"}); e != nil {
			return e
		}
		if e := tx.PutRegion(domain.Region{VersionID: f.version, PagePath: "pages/second.html", ID: "R2"}); e != nil {
			return e
		}
		if e := tx.LinkRegionEvidence(f.version, "pages/first.html", "R1", "evA"); e != nil {
			return e
		}
		if e := tx.LinkRegionEvidence(f.version, "pages/second.html", "R2", "evB"); e != nil {
			return e
		}
		return f.publishWithLayouts(tx, opts)
	})).To(Succeed())
	f.configureObservation(t, mode, true)
}

// publishWithLayouts adds the layout rows, then publishes the version.
func (f *observationFixture) publishWithLayouts(tx app.WriteTx, opts observationOptions) error {
	for _, layout := range opts.layouts {
		if layout.rows != nil {
			if e := layout.rows(f, tx); e != nil {
				return e
			}
		}
	}
	return tx.PublishVersion(f.version, 1)
}

// configureObservation installs both mappings and the mode default; gitB
// controls whether the second mapping exists.
func (f *observationFixture) configureObservation(t GinkgoTInterface, mode string, gitB bool) {
	t.Helper()
	mappings := []map[string]any{obsMapping("git-1", f.repoA.root, repoAID)}
	if gitB {
		mappings = append(mappings, obsMapping("git-2", f.repoB.root, repoBID))
	}
	f.configureMappings(t, mode, mappings...)
}

// obsMapping is one configured git source mapping.
func obsMapping(id, root, repo string) map[string]any {
	return map[string]any{"source_id": id, "kind": "git", "root_path": root, "repo_id": repo, "retention_mode": "reference_only", "provider_ids": []string{}}
}

// configureMappings installs exactly the given mappings and the mode default.
func (f *observationFixture) configureMappings(t GinkgoTInterface, mode string, mappings ...map[string]any) {
	t.Helper()
	sources := []any{}
	for _, mapping := range mappings {
		sources = append(sources, mapping)
	}
	Expect(f.store.Write(context.Background(), func(tx app.WriteTx) error {
		current, e := tx.Configuration()
		if e != nil {
			return e
		}
		var config map[string]any
		Expect(json.Unmarshal(current.Value, &config)).NotTo(HaveOccurred())
		config["projects"] = map[string]any{f.project: map[string]any{"source_mode_default": mode, "sources": sources}}
		raw, e := json.Marshal(config)
		if e != nil {
			return e
		}
		return tx.SetConfiguration(domain.Configuration{Revision: current.Revision, Value: raw, Actor: "fixture"}, current.Revision)
	})).To(Succeed())
}

func (f *observationFixture) observe(key, body string) (domain.Response, error) {
	raw := []byte(body)
	if body == "" {
		raw = []byte(`{}`)
	}
	return f.core.Observe(context.Background(), f.token, f.version, key, raw)
}

func (f *observationFixture) applicable(t GinkgoTInterface, q query.AssessmentQuery) map[string]any {
	t.Helper()
	out, e := f.queries.Assessments(context.Background(), f.token, f.version, q)
	ExpectWithOffset(1, e).NotTo(HaveOccurred(), fmt.Sprint(e))
	raw, _ := json.Marshal(out)
	var view struct {
		Items      []map[string]any `json:"items"`
		Applicable map[string]any   `json:"applicable"`
	}
	ExpectWithOffset(1, json.Unmarshal(raw, &view)).NotTo(HaveOccurred())
	ExpectWithOffset(1, view.Items).To(BeEmpty(), "EPIC-004 writes no assessment rows")
	return view.Applicable
}

func (f *observationFixture) coverage(t GinkgoTInterface, setID, attempt string) []map[string]any {
	t.Helper()
	list, e := f.queries.AssessmentCoverage(context.Background(), f.token, f.version, setID, attempt, query.Options{})
	ExpectWithOffset(1, e).NotTo(HaveOccurred(), fmt.Sprint(e))
	return list.Items
}

func mapString(value any) string {
	text, _ := value.(string)
	return text
}

var _ = Describe("observation runs", Label("persistence"), func() {
	t := GinkgoT()

	// X15/F06: a direct cited-path change sets target-scoped
	// reassessment_required under the current-selection summary and an
	// explicit target request, while the pinned summary of the same version
	// never absorbs the HEAD observation.
	It("sets target-scoped reassessment for a direct change and isolates families", func() {
		f := newObservationFixture(t, "current")
		f.repoA.commit(t, map[string]string{"a.txt": "alpha changed\n"})
		key := "31000000-0000-4000-8000-000000000001"
		response, e := f.observe(key, "")
		Expect(e).NotTo(HaveOccurred())
		Expect(response.Status).To(Equal(201))
		run := outcome(t, response)
		Expect(run["run_id"]).NotTo(BeEmpty())
		Expect(run["target_set_id"]).NotTo(BeNil())
		target := mapString(run["target_set_id"])
		capturedAt := mapString(run["captured_at"])
		sources := run["sources"].([]any)
		Expect(sources).To(HaveLen(2))
		first := sources[0].(map[string]any)
		Expect(first["classification"]).To(Equal("direct"))
		Expect(first["changed_paths"]).To(ContainElement("a.txt"))
		Expect(first["reassessment_required"]).To(BeTrue())
		// The current-selection summary composes this run's capture.
		applicable := f.applicable(t, query.AssessmentQuery{})
		Expect(applicable["target_set_id"]).To(Equal(target))
		Expect(applicable["captured_at"]).To(Equal(capturedAt))
		Expect(applicable["status"]).To(Equal("reassessment_needed"))
		Expect(applicable["reassessment_required"]).To(BeTrue())
		coverage := applicable["coverage"].(map[string]any)
		Expect(coverage["unit_kind"]).To(Equal("page"))
		Expect(coverage["eligible"]).To(Equal(float64(2)))
		// An explicit target_set_id request selects exactly this capture.
		explicit := f.applicable(t, query.AssessmentQuery{TargetSetID: target})
		Expect(explicit["reassessment_required"]).To(BeTrue())
		Expect(explicit["status"]).To(Equal("reassessment_needed"))
		// Region-scoped visibility: the trigger is anchored at R1.
		regionScoped := f.applicable(t, query.AssessmentQuery{ScopeKind: "region", PagePath: "pages/first.html", RegionID: "R1"})
		Expect(regionScoped["reassessment_required"]).To(BeTrue())
		otherRegion := f.applicable(t, query.AssessmentQuery{ScopeKind: "region", PagePath: "pages/second.html", RegionID: "R2"})
		Expect(otherRegion["reassessment_required"]).To(BeFalse(), "a region summary never counts another region's trigger")
		// The pinned summary of the same version stays isolated (F06).
		f.configureObservation(t, "pinned", true)
		pinned := f.applicable(t, query.AssessmentQuery{})
		Expect(pinned["target_set_id"]).To(BeNil())
		Expect(pinned["reassessment_required"]).To(BeFalse())
		Expect(pinned["coverage"].(map[string]any)["coverage_set_id"]).To(BeNil(), "a pinned summary never absorbs HEAD coverage")
	})

	// F04/F05: an unrelated (or dependency-scope) commit is indirect —
	// disclosed, never a trigger, never an outdated verdict.
	It("discloses an unexamined dependency scope without ever claiming outdated", func() {
		f := newObservationFixture(t, "current")
		f.repoA.commit(t, map[string]string{"ui/only.css": "body{}\n"})
		response, e := f.observe("31000000-0000-4000-8000-000000000002", "")
		Expect(e).NotTo(HaveOccurred())
		run := outcome(t, response)
		sources := run["sources"].([]any)
		first := sources[0].(map[string]any)
		Expect(first["classification"]).To(Equal("indirect"))
		Expect(first["changed_paths"]).To(HaveLen(0), "no cited path changed")
		Expect(first["dependency_scope"]).To(ContainElement("ui/only.css"))
		Expect(first["reassessment_required"]).To(BeFalse())
		applicable := f.applicable(t, query.AssessmentQuery{})
		Expect(applicable["status"]).To(Equal("not_assessed"))
		Expect(applicable["reassessment_required"]).To(BeFalse())
		Expect(mapString(applicable["status"])).NotTo(ContainSubstring("outdated"))
		coverage := applicable["coverage"].(map[string]any)
		setID := mapString(coverage["coverage_set_id"])
		Expect(setID).NotTo(BeEmpty())
		units := f.coverage(t, setID, "")
		reasons := map[string]string{}
		for _, unit := range units {
			reasons[mapString(unit["page_path"])] = mapString(unit["reason"])
		}
		Expect(reasons["pages/first.html"]).To(Equal("unexamined_dependency_scope"))
		Expect(reasons["pages/second.html"]).To(Equal(""), "an inspected unchanged page keeps a null reason")
		var triggers int
		Expect(f.store.Write(context.Background(), func(tx app.WriteTx) error {
			rows, e := tx.TriggersByTargetSet(f.version, mapString(run["target_set_id"]))
			triggers = len(rows)
			return e
		})).To(Succeed())
		Expect(triggers).To(Equal(0), "F04/F05 never create a trigger")
	})

	// F07 + r4.2 rule 2: a first all-unavailable run persists the attempted
	// scope without fabricating a target; the read path exposes the recorded
	// per-unit unavailable reasons with applicable nulls and
	// unable_to_verify.
	It("records an all-unavailable first run without fabricating a target", func() {
		f := newObservationFixture(t, "current")
		f.repoA.breakRoot(t)
		f.repoB.breakRoot(t)
		response, e := f.observe("31000000-0000-4000-8000-000000000003", "")
		Expect(e).NotTo(HaveOccurred())
		run := outcome(t, response)
		Expect(run["target_set_id"]).To(BeNil())
		Expect(run["captured_at"]).To(BeNil())
		Expect(run["coverage_set_id"]).NotTo(BeNil(), "the attempted scope persists")
		applicable := f.applicable(t, query.AssessmentQuery{})
		Expect(applicable["target_set_id"]).To(BeNil())
		Expect(applicable["captured_at"]).To(BeNil())
		Expect(applicable["status"]).To(Equal("unable_to_verify"))
		coverage := applicable["coverage"].(map[string]any)
		Expect(coverage["eligible"]).To(Equal(float64(2)))
		setID := mapString(coverage["coverage_set_id"])
		Expect(setID).NotTo(BeEmpty())
		for _, unit := range f.coverage(t, setID, "") {
			Expect(unit["reason"]).To(Equal("missing_source"), "every unit carries the unavailable reason")
			Expect(unit["target_set_id"]).To(BeNil())
		}
		// /status and project sources derive from recorded observations only.
		summary, e := f.statuses.SourceSummary(context.Background())
		Expect(e).NotTo(HaveOccurred())
		Expect(summary["available"]).To(BeFalse())
		Expect(summary["reason"]).To(Equal("missing_source"))
		Expect(summary["configured_source_count"]).To(Equal(2))
		Expect(summary["last_observed_at"]).NotTo(BeNil())
		list, e := f.queries.ProjectSources(context.Background(), f.token, f.project, query.Options{})
		Expect(e).NotTo(HaveOccurred())
		Expect(list.Items).To(HaveLen(2))
		for _, item := range list.Items {
			Expect(item["last_availability"]).To(Equal("unavailable"))
			Expect(item["last_observation_reason"]).To(Equal("missing_source"))
			Expect(item["last_observed_revision"]).To(BeNil(), "no source was captured")
			Expect(item["last_observed_at"]).NotTo(BeNil())
		}
	})

	// r4.2 rule 5(a): a multi-source partial run keeps one set with
	// per-source entries and per-source unit attribution.
	It("attributes unit reasons per source on a partial run", func() {
		f := newObservationFixture(t, "current")
		f.repoB.breakRoot(t)
		response, e := f.observe("31000000-0000-4000-8000-000000000004", "")
		Expect(e).NotTo(HaveOccurred())
		run := outcome(t, response)
		Expect(run["target_set_id"]).NotTo(BeNil(), "a partial run that captured a target keeps its set")
		applicable := f.applicable(t, query.AssessmentQuery{})
		Expect(applicable["status"]).To(Equal("unable_to_verify"), "the attempt is availability-blocked (note 3)")
		setID := mapString(applicable["coverage"].(map[string]any)["coverage_set_id"])
		reasons := map[string]string{}
		for _, unit := range f.coverage(t, setID, "") {
			reasons[mapString(unit["page_path"])] = mapString(unit["reason"])
		}
		Expect(reasons["pages/second.html"]).To(Equal("missing_source"), "the failed source's page carries its reason")
		Expect(reasons["pages/first.html"]).To(Equal(""), "no cross-source attribution to the healthy page")
		// A partial run's non-null target never makes a failed source
		// captured (note 4).
		list, e := f.queries.ProjectSources(context.Background(), f.token, f.project, query.Options{})
		Expect(e).NotTo(HaveOccurred())
		bySource := map[string]map[string]any{}
		for _, item := range list.Items {
			bySource[item["source_id"].(string)] = item
		}
		Expect(bySource["git-1"]["last_observed_revision"]).NotTo(BeNil())
		Expect(bySource["git-2"]["last_observed_revision"]).To(BeNil())
		Expect(bySource["git-2"]["last_availability"]).To(Equal("unavailable"))
	})

	// r4.2 rule 5(c) + r4.4 rule 1 test + r4.5 rule 1(a): a capture run R1
	// with a direct-change trigger followed by an all-unavailable run R2 on
	// the same selection keeps R1's target, coverage and triggers while the
	// status turns unable_to_verify and R2's reasons surface through the
	// attempt-attributed units_url (Supervisor note 1).
	It("composes the latest attempt with the latest valid capture (R1 then R2)", func() {
		f := newObservationFixture(t, "current")
		f.repoA.commit(t, map[string]string{"a.txt": "alpha changed\n"})
		first, e := f.observe("31000000-0000-4000-8000-000000000005", "")
		Expect(e).NotTo(HaveOccurred())
		run1 := outcome(t, first)
		target1 := mapString(run1["target_set_id"])
		f.repoA.breakRoot(t)
		f.repoB.breakRoot(t)
		second, e := f.observe("31000000-0000-4000-8000-000000000006", "")
		Expect(e).NotTo(HaveOccurred())
		run2 := outcome(t, second)
		Expect(run2["target_set_id"]).To(BeNil())
		applicable := f.applicable(t, query.AssessmentQuery{})
		Expect(applicable["status"]).To(Equal("unable_to_verify"))
		Expect(applicable["reassessment_required"]).To(BeTrue(), "C is still T1 (r4.5 a)")
		Expect(applicable["target_set_id"]).To(Equal(target1))
		coverage := applicable["coverage"].(map[string]any)
		Expect(coverage["coverage_set_id"]).To(Equal(run1["coverage_set_id"]))
		unitsURL := mapString(coverage["units_url"])
		Expect(unitsURL).To(ContainSubstring("attempt_run_id="+mapString(run2["run_id"])), "note 1: the units_url names the attempt")
		// The plain endpoint keeps R1's stored reasons; the attempt-attributed
		// endpoint overlays R2's availability reasons.
		setID := mapString(coverage["coverage_set_id"])
		plain := f.coverage(t, setID, "")
		Expect(plain[0]["reason"]).To(Equal("direct_source_change"), "stored reasons stay as captured")
		attempt := mapString(run2["run_id"])
		overlaid := f.coverage(t, setID, attempt)
		for _, unit := range overlaid {
			Expect(unit["reason"]).To(Equal("missing_source"), "R2's reasons attribute through the units_url")
		}
		// W3b L7: the attribution follows the links the runs recorded, so a
		// later mapping change never re-attributes the same units_url.
		f.configureMappings(t, "current")
		for _, unit := range f.coverage(t, setID, attempt) {
			Expect(unit["reason"]).To(Equal("missing_source"), "attribution never depends on the current mappings")
		}
		// An unknown attempt id fails nondisclosing with the named detail.
		_, e = f.queries.AssessmentCoverage(context.Background(), f.token, f.version, setID, "00000000-0000-4000-8000-0000000000ff", query.Options{})
		Expect(e).To(MatchError(domain.ErrNotFound))
	})

	// r4.5 rule 1(b): a later capture without a direct change supersedes the
	// selection; the old target's explicit request still shows its triggers.
	It("supersedes selection with an unchanged capture while the old target keeps its triggers", func() {
		f := newObservationFixture(t, "current")
		f.repoA.commit(t, map[string]string{"a.txt": "alpha changed\n"})
		first, e := f.observe("31000000-0000-4000-8000-000000000007", "")
		Expect(e).NotTo(HaveOccurred())
		target1 := mapString(outcome(t, first)["target_set_id"])
		// Move HEAD back to the pinned base: the next observation is a
		// normal unchanged capture.
		f.repoA.reset(t, f.baseA)
		second, e := f.observe("31000000-0000-4000-8000-000000000008", "")
		Expect(e).NotTo(HaveOccurred())
		run2 := outcome(t, second)
		target2 := mapString(run2["target_set_id"])
		Expect(target2).NotTo(Equal(target1))
		applicable := f.applicable(t, query.AssessmentQuery{})
		Expect(applicable["target_set_id"]).To(Equal(target2))
		Expect(applicable["reassessment_required"]).To(BeFalse(), "C is T2 without a direct change")
		// The historical trigger row stays visible through T1's own summary.
		explicit := f.applicable(t, query.AssessmentQuery{TargetSetID: target1})
		Expect(explicit["reassessment_required"]).To(BeTrue())
		Expect(explicit["status"]).To(Equal("reassessment_needed"))
		// W3b L18: each target composes its own coverage set and units.
		set1 := mapString(outcome(t, first)["coverage_set_id"])
		set2 := mapString(run2["coverage_set_id"])
		Expect(explicit["coverage"].(map[string]any)["coverage_set_id"]).To(Equal(set1))
		Expect(applicable["coverage"].(map[string]any)["coverage_set_id"]).To(Equal(set2))
		Expect(unitReasons(f.coverage(t, set1, ""))).To(Equal(map[string]string{"pages/first.html": "direct_source_change", "pages/second.html": ""}))
		Expect(unitReasons(f.coverage(t, set2, ""))).To(Equal(map[string]string{"pages/first.html": "", "pages/second.html": ""}))
		for _, unit := range f.coverage(t, set1, "") {
			Expect(unit["target_set_id"]).To(Equal(target1))
		}
		for _, unit := range f.coverage(t, set2, "") {
			Expect(unit["target_set_id"]).To(Equal(target2))
		}
		var triggers int
		Expect(f.store.Write(context.Background(), func(tx app.WriteTx) error {
			rows, e := tx.TriggersByTargetSet(f.version, target1)
			triggers = len(rows)
			return e
		})).To(Succeed())
		Expect(triggers).To(Equal(1), "the durable trigger row is never cleared")
		// The default T2 summary stays undisturbed after the explicit request.
		again := f.applicable(t, query.AssessmentQuery{})
		Expect(again["target_set_id"]).To(Equal(target2))
		Expect(again["reassessment_required"]).To(BeFalse())
	})

	// r4.3: two all-unavailable runs on the same version and scope with
	// different source subsets and overlapping timestamps stay attributable
	// to exactly their run identity.
	It("keeps overlapping all-unavailable runs attributable per run", func() {
		f := newObservationFixture(t, "current")
		f.repoA.breakRoot(t)
		f.repoB.breakRoot(t)
		one, e := f.observe("31000000-0000-4000-8000-000000000009", `{"source_ids":["git-1"]}`)
		Expect(e).NotTo(HaveOccurred())
		two, e := f.observe("31000000-0000-4000-8000-00000000000a", `{"source_ids":["git-2"]}`)
		Expect(e).NotTo(HaveOccurred())
		run1, run2 := outcome(t, one), outcome(t, two)
		Expect(run1["coverage_set_id"]).NotTo(Equal(run2["coverage_set_id"]))
		var rows1, rows2 []domain.SourceObservation
		Expect(f.store.Write(context.Background(), func(tx app.WriteTx) error {
			var e error
			rows1, e = tx.ObservationsByRun(mapString(run1["run_id"]))
			if e != nil {
				return e
			}
			rows2, e = tx.ObservationsByRun(mapString(run2["run_id"]))
			return e
		})).To(Succeed())
		Expect(rows1).To(HaveLen(1))
		Expect(rows1[0].SourceID).To(Equal("git-1"))
		Expect(rows2).To(HaveLen(1))
		Expect(rows2[0].SourceID).To(Equal("git-2"))
		// Requesting git-1's selection never surfaces git-2's units.
		git1, e := f.queries.Assessments(context.Background(), f.token, f.version, query.AssessmentQuery{})
		Expect(e).NotTo(HaveOccurred())
		raw, _ := json.Marshal(git1)
		var view struct {
			Applicable map[string]any `json:"applicable"`
		}
		Expect(json.Unmarshal(raw, &view)).NotTo(HaveOccurred())
		coverage := view.Applicable["coverage"].(map[string]any)
		Expect(coverage["coverage_set_id"]).To(BeNil(), "the default [git-1,git-2] selection has no matching run")
		// W3b L13: each run's own selection composes exactly its own units;
		// switching the configured mappings selects A-only or B-only.
		f.configureMappings(t, "current", obsMapping("git-1", f.repoA.root, repoAID))
		gitOne := f.applicable(t, query.AssessmentQuery{})
		Expect(gitOne["target_set_id"]).To(BeNil())
		Expect(gitOne["coverage"].(map[string]any)["coverage_set_id"]).To(Equal(run1["coverage_set_id"]))
		Expect(unitReasons(f.coverage(t, mapString(run1["coverage_set_id"]), ""))).To(Equal(map[string]string{"pages/first.html": "missing_source", "pages/second.html": "no_cited_evidence"}))
		f.configureMappings(t, "current", obsMapping("git-2", f.repoB.root, repoBID))
		gitTwo := f.applicable(t, query.AssessmentQuery{})
		Expect(gitTwo["coverage"].(map[string]any)["coverage_set_id"]).To(Equal(run2["coverage_set_id"]))
		Expect(unitReasons(f.coverage(t, mapString(run2["coverage_set_id"]), ""))).To(Equal(map[string]string{"pages/first.html": "no_cited_evidence", "pages/second.html": "missing_source"}))
		var captures []domain.TargetSet
		Expect(f.store.Write(context.Background(), func(tx app.WriteTx) error {
			var e error
			captures, e = tx.SourceCaptures(f.project)
			return e
		})).To(Succeed())
		Expect(captures).To(BeEmpty(), "no run fabricates a target set")
	})

	// Restart stability: triggers, coverage sets, captured_at and target
	// identity survive a close/reopen; a later observation never rewrites
	// the earlier capture.
	It("keeps captures and triggers stable across restart", func() {
		f := newObservationReopenFixture(t, "current")
		f.repoA.commit(t, map[string]string{"a.txt": "alpha changed\n"})
		first, e := f.observe("31000000-0000-4000-8000-00000000000b", "")
		Expect(e).NotTo(HaveOccurred())
		run1 := outcome(t, first)
		target1, captured1, set1 := mapString(run1["target_set_id"]), mapString(run1["captured_at"]), mapString(run1["coverage_set_id"])
		units := f.coverage(t, set1, "")
		triggers := f.triggers(t, target1)
		// W3b L13: close and reopen the profile like a daemon restart.
		f.restartObservation(t)
		applicable := f.applicable(t, query.AssessmentQuery{})
		Expect(applicable["target_set_id"]).To(Equal(target1))
		Expect(applicable["captured_at"]).To(Equal(captured1))
		Expect(applicable["reassessment_required"]).To(BeTrue())
		Expect(applicable["coverage"].(map[string]any)["coverage_set_id"]).To(Equal(set1))
		Expect(f.coverage(t, set1, "")).To(Equal(units), "the captured units survive the restart")
		Expect(f.triggers(t, target1)).To(Equal(triggers), "the trigger rows survive the restart")
		// A later unchanged observation supersedes selection without
		// rewriting the earlier capture.
		f.repoA.reset(t, f.baseA)
		second, e := f.observe("31000000-0000-4000-8000-00000000000c", "")
		Expect(e).NotTo(HaveOccurred())
		Expect(mapString(outcome(t, second)["target_set_id"])).NotTo(Equal(target1))
		explicit := f.applicable(t, query.AssessmentQuery{TargetSetID: target1})
		Expect(explicit["captured_at"]).To(Equal(captured1), "the earlier capture is immutable")
		Expect(explicit["reassessment_required"]).To(BeTrue())
	})

	// Receipts: an identical replay returns the stored 201 byte-for-byte; a
	// concurrent identical request receives the stable 202 envelope.
	It("replays receipts and answers concurrent identical requests with 202", func() {
		f := newObservationFixture(t, "current")
		key := "31000000-0000-4000-8000-00000000000d"
		first, e := f.observe(key, "")
		Expect(e).NotTo(HaveOccurred())
		replay, e := f.observe(key, "")
		Expect(e).NotTo(HaveOccurred())
		Expect(replay.Status).To(Equal(201))
		Expect(replay.Body).To(Equal(first.Body), "the replay equals the stored 201")
		// Park the resolution so the second identical request meets an
		// accepted receipt.
		parked, release := make(chan struct{}), make(chan struct{})
		original := f.core.Reader
		f.core.Reader = &parkingReader{inner: original, at: "resolve", park: func() { close(parked); <-release }}
		done := make(chan struct{})
		var pending domain.Response
		go func() {
			defer GinkgoRecover()
			defer close(done)
			var e error
			pending, e = f.core.Observe(context.Background(), f.token, f.version, "31000000-0000-4000-8000-00000000000e", []byte(`{}`))
			Expect(e).NotTo(HaveOccurred())
		}()
		<-parked
		concurrent, e := f.core.Observe(context.Background(), f.token, f.version, "31000000-0000-4000-8000-00000000000e", []byte(`{}`))
		Expect(e).NotTo(HaveOccurred())
		Expect(concurrent.Status).To(Equal(202), "the stable pending envelope")
		var body map[string]any
		Expect(json.Unmarshal(concurrent.Body, &body)).NotTo(HaveOccurred())
		Expect(body["state"]).To(Equal("accepted"))
		close(release)
		<-done
		Expect(pending.Status).To(Equal(201))
		f.core.Reader = original
	})

	// O1: the observation POST is owner-only; the results stay
	// scoped-readable.
	It("rejects scoped principals on the observation POST", func() {
		f := newObservationFixture(t, "current")
		scoped := seedScopedSourceReader(t, &f.vFixture)
		_, e := f.core.Observe(context.Background(), scoped, f.version, "31000000-0000-4000-8000-00000000000f", []byte(`{}`))
		Expect(e).To(MatchError(domain.ErrDenied))
	})

	// Page and region scopes capture single-unit denominators; an undeclared
	// region records the fact and fabricates no unit.
	It("captures page and region scopes without fabricating anchors", func() {
		f := newObservationFixture(t, "current")
		f.repoA.commit(t, map[string]string{"a.txt": "alpha changed\n"})
		page, e := f.observe("31000000-0000-4000-8000-000000000010", `{"scope":{"kind":"page","page_path":"pages/first.html"}}`)
		Expect(e).NotTo(HaveOccurred())
		run := outcome(t, page)
		units := f.coverage(t, mapString(run["coverage_set_id"]), "")
		Expect(units).To(HaveLen(1))
		Expect(units[0]["kind"]).To(Equal("page"))
		Expect(units[0]["page_path"]).To(Equal("pages/first.html"))
		Expect(units[0]["reason"]).To(Equal("direct_source_change"))
		region, e := f.observe("31000000-0000-4000-8000-000000000011", `{"scope":{"kind":"region","page_path":"pages/first.html","region_id":"R1"}}`)
		Expect(e).NotTo(HaveOccurred())
		runRegion := outcome(t, region)
		units = f.coverage(t, mapString(runRegion["coverage_set_id"]), "")
		Expect(units).To(HaveLen(1))
		Expect(units[0]["kind"]).To(Equal("region"))
		Expect(units[0]["region_id"]).To(Equal("R1"))
		Expect(units[0]["reason"]).To(Equal("direct_source_change"))
		// An undeclared region fabricates nothing.
		undeclared, e := f.observe("31000000-0000-4000-8000-000000000012", `{"scope":{"kind":"region","page_path":"pages/first.html","region_id":"missing"}}`)
		Expect(e).NotTo(HaveOccurred())
		runU := outcome(t, undeclared)
		Expect(runU["coverage_set_id"]).To(BeNil())
		scopeEcho := runU["scope"].(map[string]any)
		Expect(scopeEcho["region_id"]).To(Equal("missing"))
		_, e = f.queries.Assessments(context.Background(), f.token, f.version, query.AssessmentQuery{ScopeKind: "region", PagePath: "pages/first.html", RegionID: "missing"})
		Expect(e).NotTo(HaveOccurred())
		applicable := f.applicable(t, query.AssessmentQuery{ScopeKind: "region", PagePath: "pages/first.html", RegionID: "missing"})
		Expect(applicable).To(BeNil(), "a scope that cannot project yields applicable null")
	})

	// Events: direct and indirect changes emit source.changed through the
	// transactional outbox; availability flips emit
	// source.availability_changed; FilterEvent masks them for scoped
	// principals.
	It("emits masked outbox events for changes and availability flips", func() {
		f := newObservationFixture(t, "current")
		f.repoA.commit(t, map[string]string{"a.txt": "alpha changed\n"})
		_, e := f.observe("31000000-0000-4000-8000-000000000013", "")
		Expect(e).NotTo(HaveOccurred())
		var events []domain.Event
		scoped := seedScopedSourceReader(t, &f.vFixture)
		Expect(f.store.Write(context.Background(), func(tx app.WriteTx) error {
			var e error
			events, e = tx.Events("", 0, 50)
			return e
		})).To(Succeed())
		kinds := map[string]int{}
		for _, event := range events {
			kinds[event.Kind]++
			if event.Kind == "source.changed" || event.Kind == "source.availability_changed" {
				// The owner sees the event unmasked.
				filtered, e := f.auth.FilterEvent(context.Background(), f.token, event)
				Expect(e).NotTo(HaveOccurred())
				Expect(filtered.Kind).To(Equal(event.Kind))
				// A scoped principal has no source-read route, so the event
				// is masked (no leak).
				masked, e := f.auth.FilterEvent(context.Background(), scoped, event)
				Expect(e).NotTo(HaveOccurred())
				Expect(masked.Kind).To(Equal("library.invalidated"))
			}
		}
		Expect(kinds["source.changed"]).To(Equal(1), "the direct change emits source.changed")
		f.repoA.breakRoot(t)
		f.repoB.breakRoot(t)
		_, e = f.observe("31000000-0000-4000-8000-000000000014", "")
		Expect(e).NotTo(HaveOccurred())
		Expect(f.store.Write(context.Background(), func(tx app.WriteTx) error {
			var e error
			events, e = tx.Events("", 0, 50)
			return e
		})).To(Succeed())
		flips := 0
		for _, event := range events {
			if event.Kind == "source.availability_changed" {
				flips++
			}
		}
		Expect(flips).To(Equal(2), "available to unavailable flips emit availability_changed")
	})
})

// parkingReader parks one stage of the reader port so a crash underneath the
// observation protocol is reproducible.
type parkingReader struct {
	inner appsource.Reader
	at    string
	park  func()
}

func (p *parkingReader) ResolveRevision(ctx context.Context, root, revision string) error {
	return p.inner.ResolveRevision(ctx, root, revision)
}

func (p *parkingReader) ResolveHead(ctx context.Context, root string) (string, error) {
	if p.at == "resolve" {
		park := p.park
		p.park = nil
		if park != nil {
			park()
		}
	}
	return p.inner.ResolveHead(ctx, root)
}

func (p *parkingReader) ChangedPaths(ctx context.Context, root, base, tip string, cited []string) ([]string, error) {
	if p.at == "diff" {
		park := p.park
		p.park = nil
		if park != nil {
			park()
		}
	}
	return p.inner.ChangedPaths(ctx, root, base, tip, cited)
}

func (p *parkingReader) ReadBlob(ctx context.Context, root, revision, path string) (io.ReadCloser, error) {
	return p.inner.ReadBlob(ctx, root, revision, path)
}

// crashObservationDuring runs one observation until its reader parks, closes
// the store underneath it like a process crash and joins the crashed run.
func crashObservationDuring(f *observationReopenFixture, key string, parked <-chan struct{}, release chan<- struct{}) {
	GinkgoHelper()
	done := make(chan struct{})
	go func() {
		defer GinkgoRecover()
		defer close(done)
		_, _ = f.core.Observe(context.Background(), f.token, f.version, key, []byte(`{}`))
	}()
	<-parked
	Expect(f.closeStore()).To(Succeed())
	close(release)
	<-done
}

// observationReopenFixture rebuilds the observation fixture over one profile
// so the store can be closed and reopened (real restart sweeps, S11).
type observationReopenFixture struct {
	observationFixture
	selection  service.Selection
	closeStore func() error
}

func newObservationReopenFixture(t GinkgoTInterface, mode string) *observationReopenFixture {
	GinkgoHelper()
	rf := newReopenFixture(t)
	f := newObservationBase(t, mode, rf.vFixture)
	return &observationReopenFixture{observationFixture: *f, selection: rf.selection, closeStore: rf.closeStore}
}

// restartObservation closes and reopens the profile and runs the startup
// sweep like the daemon wiring (S11 pattern).
func (f *observationReopenFixture) restartObservation(t GinkgoTInterface) {
	t.Helper()
	Expect(f.closeStore()).To(Succeed())
	profile, e := service.Load(f.selection)
	Expect(e).NotTo(HaveOccurred())
	store := foundation.New(nil)
	state, closeStore, e := store.Prepare(context.Background(), profile)
	Expect(e).NotTo(HaveOccurred())
	Expect(state.Ready).To(BeTrue())
	f.closeStore = closeStore
	DeferCleanup(func() { _ = closeStore() })
	f.store = store
	f.auth = appauth.New(store)
	f.core = appsource.New(store, f.auth, store, gitreader.New())
	f.core.Now = f.clock.Now
	f.queries = query.New(store, f.auth, store)
	f.statuses = appconfig.New(store, f.auth, nil)
	Expect(f.core.Recover(context.Background())).To(Succeed())
}

var _ = Describe("observation restart sweeps", Label("persistence"), func() {
	t := GinkgoT()

	// A crash between reservation and the target persist leaves no run row;
	// the sweep settles the receipt deterministically.
	It("settles a crash after reservation with no run row", func() {
		f := newObservationReopenFixture(t, "current")
		f.repoA.commit(t, map[string]string{"a.txt": "alpha changed\n"})
		key := "32000000-0000-4000-8000-000000000001"
		parked, release := make(chan struct{}), make(chan struct{})
		original := f.core.Reader
		f.core.Reader = &parkingReader{inner: original, at: "resolve", park: func() { close(parked); <-release }}
		crashObservationDuring(f, key, parked, release)
		f.restartObservation(t)
		receipt, e := f.auth.Operation(context.Background(), f.token, key)
		Expect(e).NotTo(HaveOccurred())
		Expect(receipt.State).To(Equal("failed"))
		Expect(string(receipt.Response.Body)).To(ContainSubstring("interrupted_before_commit"))
		owner := ownerPrincipal(&f.vFixture)
		var runExists bool
		Expect(f.store.Write(context.Background(), func(tx app.WriteTx) error {
			_, exists, e := tx.ObservationRunForOperation(owner, key)
			runExists = exists
			return e
		})).To(Succeed())
		Expect(runExists).To(BeFalse(), "no run row was persisted yet")
	})

	// A crash between the target persist and the commit leaves a dangling
	// run row and capture; the sweep fails the receipt and the dangling
	// capture is never a selection candidate (r4.1 rule 4).
	It("excludes a dangling stage-2 capture from selection after a crash", func() {
		f := newObservationReopenFixture(t, "current")
		f.repoA.commit(t, map[string]string{"a.txt": "alpha changed\n"})
		key := "32000000-0000-4000-8000-000000000002"
		parked, release := make(chan struct{}), make(chan struct{})
		original := f.core.Reader
		f.core.Reader = &parkingReader{inner: original, at: "diff", park: func() { close(parked); <-release }}
		crashObservationDuring(f, key, parked, release)
		f.restartObservation(t)
		receipt, e := f.auth.Operation(context.Background(), f.token, key)
		Expect(e).NotTo(HaveOccurred())
		Expect(receipt.State).To(Equal("failed"))
		Expect(string(receipt.Response.Body)).To(ContainSubstring("interrupted_before_commit"))
		owner := ownerPrincipal(&f.vFixture)
		var run domain.ObservationRun
		var runExists bool
		Expect(f.store.Write(context.Background(), func(tx app.WriteTx) error {
			var e error
			run, runExists, e = tx.ObservationRunForOperation(owner, key)
			return e
		})).To(Succeed())
		Expect(runExists).To(BeTrue(), "the stage-2 run row stays (never deleted)")
		Expect(run.Committed).To(BeFalse(), "no per-source rows ever committed")
		var targets int
		Expect(f.store.Write(context.Background(), func(tx app.WriteTx) error {
			_, e := tx.TargetSetByID(run.TargetSetID)
			if e == nil {
				targets = 1
			}
			return nil
		})).To(Succeed())
		Expect(targets).To(Equal(1), "the dangling reservation capture stays (r4.1 rule 4)")
		applicable := f.applicable(t, query.AssessmentQuery{})
		Expect(applicable["target_set_id"]).To(BeNil(), "the dangling capture never composes a summary")
		Expect(applicable["status"]).To(Equal("not_assessed"))
	})

	// A crash between the commit and the intent resolution recomposes the
	// recorded 201 from the durable rows.
	It("completes a committed observation from its rows after restart", func() {
		f := newObservationReopenFixture(t, "current")
		f.repoA.commit(t, map[string]string{"a.txt": "alpha changed\n"})
		key := "32000000-0000-4000-8000-000000000003"
		response, e := f.observe(key, "")
		Expect(e).NotTo(HaveOccurred())
		Expect(response.Status).To(Equal(201))
		owner := ownerPrincipal(&f.vFixture)
		Expect(f.store.Write(context.Background(), func(tx app.WriteTx) error {
			return tx.TestReopenIntent(key, owner)
		})).To(Succeed())
		f.restartObservation(t)
		receipt, e := f.auth.Operation(context.Background(), f.token, key)
		Expect(e).NotTo(HaveOccurred())
		Expect(receipt.State).To(Equal("completed"))
		Expect(receipt.Response.Status).To(Equal(201))
		var intent domain.CheckIntent
		Expect(f.store.Write(context.Background(), func(tx app.WriteTx) error {
			var e error
			intent, e = tx.CheckIntentByOperation(owner, key)
			return e
		})).To(Succeed())
		Expect(intent.State).To(Equal("completed"), "the sweep resolves the reopened intent from the terminal receipt")
		Expect(receipt.Response.Body).To(Equal(response.Body), "the terminal receipt keeps the original 201")
	})
})
