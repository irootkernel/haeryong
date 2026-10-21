package query_test

import (
	"encoding/json"
	"errors"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	appconfig "haeryong.local/haeryong/internal/application/configuration"
	"haeryong.local/haeryong/internal/application/query"
	app "haeryong.local/haeryong/internal/application/storage"
	"haeryong.local/haeryong/internal/domain"
	sec "haeryong.local/haeryong/internal/security/session"
)

// assessmentsView reuses the query fixture's store/auth/token with a version
// that cites one mapped git source, one page and one declared region.
type assessmentsView struct {
	fixture   *fixture
	versionID string
	statuses  *appconfig.Service
}

func newAssessmentsView(t GinkgoTInterface) *assessmentsView {
	GinkgoHelper()
	f := newFixture(t)
	version := key(t)
	set := key(t)
	digest := hash("coverage body\n")
	revision := documentRevision(t, f)
	Expect(f.store.Write(ctx, func(tx app.WriteTx) error {
		if e := tx.CreateContentSet(set, digest); e != nil {
			return e
		}
		if e := tx.SealContentSet(set); e != nil {
			return e
		}
		if e := tx.PutVersion(domain.Version{ID: version, DocumentID: f.document, BaseVersionID: f.version, ContentSetID: set, Title: "Assessed", ViewerProfile: "html_js_v1", SourceBasis: "evidence_recorded", PayloadSHA: digest, ManifestSHA: digest}); e != nil {
			return e
		}
		if e := tx.PutSource(domain.Source{VersionID: version, ID: "src", Metadata: json.RawMessage(`{"id":"src","kind":"git","retention_mode":"reference_only","repo_id":"77777777-7777-4777-8777-777777777777","object_format":"sha1","exact_revision":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}`)}); e != nil {
			return e
		}
		if e := tx.PutEvidence(domain.Evidence{VersionID: version, ID: "ev", SourceID: "src", Metadata: json.RawMessage(`{"id":"ev","source_id":"src","capture_status":"reference_only","path":"a.txt"}`)}); e != nil {
			return e
		}
		if e := tx.PutPage(domain.Page{VersionID: version, Path: "pages/first.html"}); e != nil {
			return e
		}
		if e := tx.PutRegion(domain.Region{VersionID: version, PagePath: "pages/first.html", ID: "R1"}); e != nil {
			return e
		}
		if e := tx.LinkRegionEvidence(version, "pages/first.html", "R1", "ev"); e != nil {
			return e
		}
		return tx.PublishVersion(version, revision)
	})).To(Succeed())
	Expect(f.store.Write(ctx, func(tx app.WriteTx) error {
		current, e := tx.Configuration()
		if e != nil {
			return e
		}
		var config map[string]any
		Expect(json.Unmarshal(current.Value, &config)).NotTo(HaveOccurred())
		config["projects"] = map[string]any{f.project: map[string]any{"sources": []any{map[string]any{"source_id": "git-1", "kind": "git", "root_path": "/unused", "repo_id": "77777777-7777-4777-8777-777777777777", "retention_mode": "reference_only", "provider_ids": []string{}}}}}
		raw, e := json.Marshal(config)
		if e != nil {
			return e
		}
		return tx.SetConfiguration(domain.Configuration{Revision: current.Revision, Value: raw, Actor: "fixture"}, current.Revision)
	})).To(Succeed())
	return &assessmentsView{fixture: f, versionID: version, statuses: appconfig.New(f.store, f.auth, nil)}
}

// seedRun persists one committed observation run with its rows and an
// optional coverage set, entirely through the storage port. Stamps step
// monotonically so latest-row selection is deterministic.
var seedClock int64 = 0

func (v *assessmentsView) seedRun(t GinkgoTInterface, runID, operation, target, scope string, sourceRows []domain.SourceObservation, units []domain.RequiredUnit) string {
	t.Helper()
	seedClock++
	stamp := time.Date(2026, 10, 1, 0, 0, int(seedClock), 0, time.UTC).Format("2006-01-02T15:04:05.000Z07:00")
	setID := ""
	Expect(v.fixture.store.Write(ctx, func(tx app.WriteTx) error {
		if target != "" {
			if e := tx.PutTargetSet(domain.TargetSet{ID: target, ProjectID: v.fixture.project, RequestedSelection: []byte(`{"project_id":"` + v.fixture.project + `","mode":"current","source_ids":["git-1"]}`), TargetsJSON: []byte(`{"sources":[]}`), CapturedAt: stamp}); e != nil {
				return e
			}
		}
		if e := tx.PutObservationRun(domain.ObservationRun{ID: runID, VersionID: v.versionID, PrincipalID: "owner", OperationID: operation, RequestedSelection: []byte(`{"project_id":"` + v.fixture.project + `","mode":"current","source_ids":["git-1"]}`), ScopeJSON: []byte(scope), TargetSetID: target, CreatedAt: stamp, Committed: true}); e != nil {
			return e
		}
		for i := range sourceRows {
			row := sourceRows[i]
			row.RunID, row.VersionID, row.OperationID, row.ObservedAt = runID, v.versionID, operation, stamp
			if target != "" {
				row.TargetSetID = target
			}
			if e := tx.PutSourceObservation(row); e != nil {
				return e
			}
		}
		if len(units) > 0 {
			setID = runID + "-set"
			unitKind := "page"
			if len(units) == 1 && units[0].Kind == "version" {
				unitKind = "version"
			}
			if e := tx.PutCoverageSet(domain.CoverageSet{ID: setID, VersionID: v.versionID, TargetSetID: target, RunID: runID, ScopeJSON: []byte(scope), UnitKind: unitKind, CapturedAt: stamp, Origin: "source_observation"}); e != nil {
				return e
			}
			for _, unit := range units {
				unit.CoverageSetID, unit.TargetSetID = setID, target
				if e := tx.PutRequiredUnit(unit); e != nil {
					return e
				}
			}
		}
		return nil
	})).To(Succeed())
	return setID
}

func (v *assessmentsView) applicable(t GinkgoTInterface, q query.AssessmentQuery) (map[string]any, error) {
	t.Helper()
	out, e := v.fixture.queries.Assessments(ctx, v.fixture.token, v.versionID, q)
	if e != nil {
		return nil, e
	}
	raw, _ := json.Marshal(out)
	var view struct {
		Applicable map[string]any `json:"applicable"`
	}
	ExpectWithOffset(1, json.Unmarshal(raw, &view)).NotTo(HaveOccurred())
	return view.Applicable, nil
}

// seedScopedReader issues a scoped credential anchored to this fixture.
func (v *assessmentsView) seedScopedReader(t GinkgoTInterface, projectID string) string {
	t.Helper()
	secret, e := sec.Secret()
	ExpectWithOffset(1, e).NotTo(HaveOccurred())
	principal := key(t)
	scope := domain.CredentialScope{Kind: "workflow", Actions: []string{"version.read"}, ProjectIDs: []string{projectID}, VersionIDs: []string{v.versionID}}
	ExpectWithOffset(1, v.fixture.store.Write(ctx, func(tx app.WriteTx) error {
		state, e := tx.AuthState()
		if e != nil {
			return e
		}
		if e := tx.PutWorkflowPrincipal(domain.WorkflowPrincipal{ID: principal, BindingID: principal, Active: true, Scope: scope, Revision: 1}); e != nil {
			return e
		}
		p, e := tx.Scope(projectID)
		if e != nil {
			return e
		}
		return tx.PutScopedCredential(domain.ScopedCredential{ID: principal, PrincipalID: principal, TokenHash: sec.Verifier("workflow", secret), AuthEpoch: state.AuthEpoch, Scope: scope, Permissions: map[string]int64{projectID: p.Project.PermissionsRevision}, ConfigRevision: p.ConfigRevision, CreatedAt: time.Now().UTC().Truncate(time.Millisecond), Revision: 1})
	})).To(Succeed())
	return secret
}

var _ = Describe("assessments selection", Label("persistence"), func() {
	t := GinkgoT()

	// §7.1 step 4: a version with evidence but no runs composes the
	// no-capture applicable with nulls and the requested scope's kind.
	It("composes the no-capture applicable for a never-observed version", func() {
		view := newAssessmentsView(t)
		applicable, e := view.applicable(t, query.AssessmentQuery{})
		Expect(e).NotTo(HaveOccurred())
		Expect(applicable).NotTo(BeNil())
		Expect(applicable["target_set_id"]).To(BeNil())
		Expect(applicable["captured_at"]).To(BeNil())
		Expect(applicable["status"]).To(Equal("not_assessed"))
		Expect(applicable["reassessment_required"]).To(BeFalse())
		coverage := applicable["coverage"].(map[string]any)
		Expect(coverage["coverage_set_id"]).To(BeNil())
		Expect(coverage["unit_kind"]).To(Equal("version"))
		Expect(coverage["eligible"]).To(Equal(float64(0)))
		Expect(coverage["units_url"]).To(BeNil())
	})

	// r4.4 rule 2: the pinned family selects verification-created captures
	// with coverage nulls and never consults an observation run.
	It("selects a verification-created first capture for the pinned family", func() {
		view := newAssessmentsView(t)
		stamp := time.Now().UTC().Truncate(time.Millisecond).Format("2006-01-02T15:04:05.000Z07:00")
		Expect(view.fixture.store.Write(ctx, func(tx app.WriteTx) error {
			if e := tx.PutTargetSet(domain.TargetSet{ID: "52000000-0000-4000-8000-0000000000t1", ProjectID: view.fixture.project, RequestedSelection: []byte(`{"project_id":"` + view.fixture.project + `","mode":"pinned","source_ids":["git-1"],"revision":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}`), TargetsJSON: []byte(`{"sources":[]}`), CapturedAt: stamp}); e != nil {
				return e
			}
			return tx.PutVerification(domain.CitationVerification{ID: "52000000-0000-4000-8000-0000000000v1", PrincipalID: "owner", VersionID: view.versionID, EvidenceID: "ev", TargetSetID: "52000000-0000-4000-8000-0000000000t1", SourceJSON: []byte(`{}`), LocationJSON: []byte(`{}`), Outcome: "captured", ConfigRevision: 1, PermissionsRevision: 1, CheckedAt: stamp, VerifiedAt: stamp, OperationID: "52000000-0000-4000-8000-0000000000o1"})
		})).To(Succeed())
		applicable, e := view.applicable(t, query.AssessmentQuery{})
		Expect(e).NotTo(HaveOccurred())
		Expect(applicable["target_set_id"]).To(Equal("52000000-0000-4000-8000-0000000000t1"))
		Expect(applicable["status"]).To(Equal("not_assessed"))
		Expect(applicable["reassessment_required"]).To(BeFalse())
		coverage := applicable["coverage"].(map[string]any)
		Expect(coverage["coverage_set_id"]).To(BeNil(), "no coverage-producing owner exists for the pinned family")
		Expect(coverage["eligible"]).To(Equal(float64(0)))
		// The explicit request selects that exact capture (r4.4 rule 2).
		explicit, e := view.applicable(t, query.AssessmentQuery{TargetSetID: "52000000-0000-4000-8000-0000000000t1"})
		Expect(e).NotTo(HaveOccurred())
		Expect(explicit["target_set_id"]).To(Equal("52000000-0000-4000-8000-0000000000t1"))
	})

	// §7.1 step 1: a foreign explicit target stays 404 with the named
	// detail; an unknown page scope and a mismatched explicit scope yield
	// applicable null.
	It("answers 404 for foreign targets and null for unprojectable scopes", func() {
		view := newAssessmentsView(t)
		_, e := view.applicable(t, query.AssessmentQuery{TargetSetID: "53000000-0000-4000-8000-0000000000ff"})
		Expect(e).To(MatchError(domain.ErrNotFound))
		var missing *query.Missing
		Expect(errors.As(e, &missing)).To(BeTrue())
		Expect(missing.Details).To(HaveKeyWithValue("target_set_id", "53000000-0000-4000-8000-0000000000ff"))
		unknownPage, e := view.applicable(t, query.AssessmentQuery{ScopeKind: "page", PagePath: "nope.html"})
		Expect(e).NotTo(HaveOccurred())
		Expect(unknownPage).To(BeNil())
		// A page-scope run exists; a region-filtered explicit request on its
		// capture mismatches (step 6b).
		run := "53000000-0000-4000-8000-000000000001"
		target := "53000000-0000-4000-8000-0000000000t1"
		view.seedRun(t, run, "53000000-0000-4000-8000-0000000000o1", target, `{"kind":"page","page_path":"pages/first.html","declared":true}`, []domain.SourceObservation{{ID: run + "r1", SourceID: "git-1", Availability: "available", Classification: "unchanged", ConfigRevision: 1, PermissionsRevision: 1, BaseRevision: "aaaa", ObservedRevision: "bbbb"}}, []domain.RequiredUnit{{Kind: "page", PagePath: "pages/first.html"}})
		mismatch, e := view.applicable(t, query.AssessmentQuery{TargetSetID: target, ScopeKind: "region", PagePath: "pages/first.html", RegionID: "R1"})
		Expect(e).NotTo(HaveOccurred())
		Expect(mismatch).To(BeNil(), "an explicit target never substitutes into another scope")
	})

	// The coverage endpoint keys by coverage_set_id: stored units only; a
	// foreign set or a foreign attempt stays 404.
	It("serves exactly the stored units of one coverage set", func() {
		view := newAssessmentsView(t)
		run := "54000000-0000-4000-8000-000000000001"
		target := "54000000-0000-4000-8000-0000000000t1"
		setID := view.seedRun(t, run, "54000000-0000-4000-8000-0000000000o1", target, `{"kind":"version","declared":true}`, []domain.SourceObservation{{ID: run + "r1", SourceID: "git-1", Availability: "available", Classification: "unchanged", ConfigRevision: 1, PermissionsRevision: 1, BaseRevision: "aaaa", ObservedRevision: "bbbb"}}, []domain.RequiredUnit{{Kind: "page", PagePath: "pages/first.html", Reason: "no_cited_evidence"}})
		list, e := view.fixture.queries.AssessmentCoverage(ctx, view.fixture.token, view.versionID, setID, "", query.Options{})
		Expect(e).NotTo(HaveOccurred())
		Expect(list.Items).To(HaveLen(1))
		Expect(list.Items[0]["page_path"]).To(Equal("pages/first.html"))
		Expect(list.Items[0]["outcome"]).To(Equal("not_checked"))
		Expect(list.Items[0]["reason"]).To(Equal("no_cited_evidence"))
		Expect(list.Items[0]["target_set_id"]).To(Equal(target))
		_, e = view.fixture.queries.AssessmentCoverage(ctx, view.fixture.token, view.versionID, "54000000-0000-4000-8000-0000000000ff", "", query.Options{})
		Expect(e).To(MatchError(domain.ErrNotFound))
		_, e = view.fixture.queries.AssessmentCoverage(ctx, view.fixture.token, view.versionID, setID, "54000000-0000-4000-8000-0000000000ff", query.Options{})
		Expect(e).To(MatchError(domain.ErrNotFound))
	})

	// The /status source section derives from configuration plus recorded
	// observations only: each configured source's latest row decides, and
	// the strongest present reason wins across sources (note 2).
	It("composes the status source section with the reason precedence", func() {
		view := newAssessmentsView(t)
		// Configure a second source so cross-source precedence is
		// observable; nothing cites it.
		Expect(view.fixture.store.Write(ctx, func(tx app.WriteTx) error {
			current, e := tx.Configuration()
			if e != nil {
				return e
			}
			var config map[string]any
			Expect(json.Unmarshal(current.Value, &config)).NotTo(HaveOccurred())
			config["projects"].(map[string]any)[view.fixture.project].(map[string]any)["sources"] = []any{
				map[string]any{"source_id": "git-1", "kind": "git", "root_path": "/unused", "repo_id": "77777777-7777-4777-8777-777777777777", "retention_mode": "reference_only", "provider_ids": []string{}},
				map[string]any{"source_id": "git-2", "kind": "git", "root_path": "/unused2", "repo_id": "88888888-8888-4888-8888-888888888888", "retention_mode": "reference_only", "provider_ids": []string{}},
			}
			raw, e := json.Marshal(config)
			if e != nil {
				return e
			}
			return tx.SetConfiguration(domain.Configuration{Revision: current.Revision, Value: raw, Actor: "fixture"}, current.Revision)
		})).To(Succeed())
		summary, e := view.statuses.SourceSummary(ctx)
		Expect(e).NotTo(HaveOccurred())
		Expect(summary["available"]).To(BeFalse())
		Expect(summary["reason"]).To(Equal("not_observed"))
		Expect(summary["configured_source_count"]).To(Equal(2))
		Expect(summary["last_observed_at"]).To(BeNil())
		seed := func(source, reason, availability string) {
			view.seedRun(t, key(t), key(t), "", `{"kind":"version","declared":true}`, []domain.SourceObservation{{ID: key(t), SourceID: source, Availability: availability, Classification: "unavailable", Reason: reason, ConfigRevision: 1, PermissionsRevision: 1}}, nil)
		}
		seed("git-1", "missing_source", "unavailable")
		seed("git-1", "source_not_registered", "unavailable")
		summary, e = view.statuses.SourceSummary(ctx)
		Expect(e).NotTo(HaveOccurred())
		Expect(summary["reason"]).To(Equal("source_not_registered"), "one source's latest row decides its own reason")
		// A weaker latest reason on another source loses to the stronger
		// present one.
		seed("git-2", "missing_source", "unavailable")
		summary, e = view.statuses.SourceSummary(ctx)
		Expect(e).NotTo(HaveOccurred())
		Expect(summary["reason"]).To(Equal("source_not_registered"), "the strongest present reason wins across sources")
		// Every configured source's latest observation available flips the
		// section healthy.
		view.seedRun(t, key(t), key(t), key(t), `{"kind":"version","declared":true}`, []domain.SourceObservation{
			{ID: key(t), SourceID: "git-1", Availability: "available", Classification: "unchanged", ConfigRevision: 1, PermissionsRevision: 1, BaseRevision: "aaaa", ObservedRevision: "bbbb"},
			{ID: key(t), SourceID: "git-2", Availability: "available", Classification: "unchanged", ConfigRevision: 1, PermissionsRevision: 1, BaseRevision: "aaaa", ObservedRevision: "bbbb"},
		}, nil)
		summary, e = view.statuses.SourceSummary(ctx)
		Expect(e).NotTo(HaveOccurred())
		Expect(summary["available"]).To(BeTrue())
		Expect(summary["reason"]).To(BeNil())
		Expect(summary["last_observed_at"]).NotTo(BeNil())
	})

	// F10 canary: a scoped principal of another project receives the
	// nondisclosing 404; an in-scope principal reads the summary.
	It("hides the assessments surface from out-of-scope principals", func() {
		view := newAssessmentsView(t)
		var other string
		Expect(view.fixture.store.Write(ctx, func(tx app.WriteTx) error {
			id := key(t)
			other = id
			return tx.CreateProject(domain.Project{ID: id, Name: "Other"})
		})).To(Succeed())
		scoped := view.seedScopedReader(t, view.fixture.project)
		out, e := view.fixture.queries.Assessments(ctx, scoped, view.versionID, query.AssessmentQuery{})
		Expect(e).NotTo(HaveOccurred())
		Expect(out["applicable"]).NotTo(BeNil())
		foreign := view.seedScopedReader(t, other)
		_, e = view.fixture.queries.Assessments(ctx, foreign, view.versionID, query.AssessmentQuery{})
		Expect(e).To(MatchError(domain.ErrNotFound), "no summary, count or cursor leaks")
		_, e = view.fixture.queries.AssessmentCoverage(ctx, foreign, view.versionID, "54000000-0000-4000-8000-0000000000ff", "", query.Options{})
		Expect(e).To(MatchError(domain.ErrNotFound))
	})
})

// seedForeignRun persists one committed observation run of the given version
// and selection with its target, row and coverage set, through the storage
// port.
func (v *assessmentsView) seedForeignRun(t GinkgoTInterface, versionID, sources string) (run, target, set string) {
	t.Helper()
	run, target, set = key(t), key(t), key(t)
	stamp := time.Date(2026, 10, 2, 0, 0, 0, 0, time.UTC).Format("2006-01-02T15:04:05.000Z07:00")
	selection := []byte(`{"project_id":"` + v.fixture.project + `","mode":"current","source_ids":` + sources + `}`)
	Expect(v.fixture.store.Write(ctx, func(tx app.WriteTx) error {
		if e := tx.PutTargetSet(domain.TargetSet{ID: target, ProjectID: v.fixture.project, RequestedSelection: selection, TargetsJSON: []byte(`{"sources":[]}`), CapturedAt: stamp}); e != nil {
			return e
		}
		if e := tx.PutObservationRun(domain.ObservationRun{ID: run, VersionID: versionID, PrincipalID: "owner", OperationID: key(t), RequestedSelection: selection, ScopeJSON: []byte(`{"kind":"version","declared":true}`), TargetSetID: target, CreatedAt: stamp}); e != nil {
			return e
		}
		if e := tx.PutSourceObservation(domain.SourceObservation{ID: key(t), RunID: run, VersionID: versionID, TargetSetID: target, SourceID: "git-1", Availability: "available", Classification: "unchanged", ConfigRevision: 1, PermissionsRevision: 1, ObservedAt: stamp, OperationID: key(t)}); e != nil {
			return e
		}
		return tx.PutCoverageSet(domain.CoverageSet{ID: set, VersionID: versionID, TargetSetID: target, RunID: run, ScopeJSON: []byte(`{"kind":"version","declared":true}`), UnitKind: "page", CapturedAt: stamp, Origin: "source_observation"})
	})).To(Succeed())
	return run, target, set
}

// pinnedCapture seeds one verification-created capture of this version.
func (v *assessmentsView) pinnedCapture(t GinkgoTInterface, revision, capturedAt string) string {
	t.Helper()
	target := key(t)
	Expect(v.fixture.store.Write(ctx, func(tx app.WriteTx) error {
		if e := tx.PutTargetSet(domain.TargetSet{ID: target, ProjectID: v.fixture.project, RequestedSelection: []byte(`{"project_id":"` + v.fixture.project + `","mode":"pinned","source_ids":["git-1"],"revision":"` + revision + `"}`), TargetsJSON: []byte(`{"sources":[]}`), CapturedAt: capturedAt}); e != nil {
			return e
		}
		return tx.PutVerification(domain.CitationVerification{ID: key(t), PrincipalID: "owner", VersionID: v.versionID, EvidenceID: "ev", TargetSetID: target, SourceJSON: []byte(`{}`), LocationJSON: []byte(`{}`), Outcome: "captured", ConfigRevision: 1, PermissionsRevision: 1, CheckedAt: capturedAt, VerifiedAt: capturedAt, OperationID: key(t)})
	})).To(Succeed())
	return target
}

var _ = Describe("assessments validation", Label("persistence"), func() {
	t := GinkgoT()

	// W3b L11: the page always carries next_cursor and checks its cursor and
	// limit like every other list.
	It("carries next_cursor and validates the cursor like the other lists", func() {
		view := newAssessmentsView(t)
		out, e := view.fixture.queries.Assessments(ctx, view.fixture.token, view.versionID, query.AssessmentQuery{})
		Expect(e).NotTo(HaveOccurred())
		Expect(out).To(HaveKey("next_cursor"))
		Expect(out["next_cursor"]).To(BeNil())
		var invalid *query.Error
		_, e = view.fixture.queries.Assessments(ctx, view.fixture.token, view.versionID, query.AssessmentQuery{Options: query.Options{Cursor: "not-a-cursor"}})
		Expect(errors.As(e, &invalid)).To(BeTrue(), "a malformed cursor fails typed")
		Expect(invalid.Reason).To(Equal("cursor_mismatch"))
		run := key(t)
		setID := view.seedRun(t, run, key(t), key(t), `{"kind":"version","declared":true}`, []domain.SourceObservation{{ID: key(t), SourceID: "git-1", Availability: "available", Classification: "unchanged", ConfigRevision: 1, PermissionsRevision: 1, BaseRevision: "aaaa", ObservedRevision: "bbbb"}}, []domain.RequiredUnit{{Kind: "page", PagePath: "pages/a.html"}, {Kind: "page", PagePath: "pages/b.html"}})
		page, e := view.fixture.queries.AssessmentCoverage(ctx, view.fixture.token, view.versionID, setID, "", query.Options{Limit: 1})
		Expect(e).NotTo(HaveOccurred())
		Expect(page.NextCursor).NotTo(BeNil())
		_, e = view.fixture.queries.Assessments(ctx, view.fixture.token, view.versionID, query.AssessmentQuery{Options: query.Options{Cursor: *page.NextCursor}})
		Expect(errors.As(e, &invalid)).To(BeTrue(), "another list's cursor is foreign here")
		Expect(invalid.Reason).To(Equal("cursor_mismatch"))
		_, e = view.fixture.queries.Assessments(ctx, view.fixture.token, view.versionID, query.AssessmentQuery{Options: query.Options{Limit: 500}})
		Expect(errors.As(e, &invalid)).To(BeTrue())
		Expect(invalid.Reason).To(Equal("invalid_page_size"))
	})

	// W3b L4(b): among verification captures only the exact pinned selection
	// competes; the latest of them wins.
	It("selects the latest capture of exactly the version's pinned selection", func() {
		view := newAssessmentsView(t)
		exact := strings.Repeat("a", 40)
		older := view.pinnedCapture(t, exact, "2026-10-01T00:00:00.000Z")
		newer := view.pinnedCapture(t, exact, "2026-10-01T00:00:01.000Z")
		view.pinnedCapture(t, strings.Repeat("b", 40), "2026-10-01T00:00:02.000Z")
		applicable, e := view.applicable(t, query.AssessmentQuery{})
		Expect(e).NotTo(HaveOccurred())
		Expect(applicable["target_set_id"]).NotTo(Equal(older))
		Expect(applicable["target_set_id"]).To(Equal(newer), "a later capture of another revision never competes")
	})

	// W3b L19: real ids of another version or another selection stay 404 with
	// their named detail.
	It("answers 404 for real attempts, targets and coverage sets of another version or selection", func() {
		view := newAssessmentsView(t)
		_, _, setID := view.seedForeignRun(t, view.versionID, `["git-1"]`)
		otherVersionRun, otherTarget, otherSet := view.seedForeignRun(t, view.fixture.version, `["git-1"]`)
		otherSelectionRun, _, _ := view.seedForeignRun(t, view.versionID, `["git-2"]`)
		for _, attempt := range []string{otherVersionRun, otherSelectionRun} {
			_, e := view.fixture.queries.AssessmentCoverage(ctx, view.fixture.token, view.versionID, setID, attempt, query.Options{})
			Expect(e).To(MatchError(domain.ErrNotFound))
			var missing *query.Missing
			Expect(errors.As(e, &missing)).To(BeTrue())
			Expect(missing.Details).To(HaveKeyWithValue("attempt_run_id", attempt))
		}
		_, e := view.applicable(t, query.AssessmentQuery{TargetSetID: otherTarget})
		Expect(e).To(MatchError(domain.ErrNotFound), "another version's capture")
		_, e = view.fixture.queries.AssessmentCoverage(ctx, view.fixture.token, view.versionID, otherSet, "", query.Options{})
		Expect(e).To(MatchError(domain.ErrNotFound), "another version's coverage set")
		var missing *query.Missing
		Expect(errors.As(e, &missing)).To(BeTrue())
		Expect(missing.Details).To(HaveKeyWithValue("coverage_set_id", otherSet))
	})

	// W3b L21: each configured source's latest row decides its reason, also
	// when it is weaker than an earlier one.
	It("lets the latest row decide a source's reason even when it is weaker", func() {
		view := newAssessmentsView(t)
		seed := func(reason string) {
			view.seedRun(t, key(t), key(t), "", `{"kind":"version","declared":true}`, []domain.SourceObservation{{ID: key(t), SourceID: "git-1", Availability: "unavailable", Classification: "unavailable", Reason: reason, ConfigRevision: 1, PermissionsRevision: 1}}, nil)
		}
		seed("source_not_registered")
		seed("missing_source")
		summary, e := view.statuses.SourceSummary(ctx)
		Expect(e).NotTo(HaveOccurred())
		Expect(summary["reason"]).To(Equal("missing_source"), "the latest row decides")
	})

	// W3b L19 (F10): a principal of another project learns nothing — no
	// summary, units, cursor continuation or source list, and no canary in
	// any error.
	It("discloses nothing of the version to a principal of another project", func() {
		view := newAssessmentsView(t)
		const canary = "pages/canary-f10-7f3a.html"
		run := key(t)
		setID := view.seedRun(t, run, key(t), key(t), `{"kind":"version","declared":true}`, []domain.SourceObservation{{ID: key(t), SourceID: "git-1", Availability: "available", Classification: "unchanged", ConfigRevision: 1, PermissionsRevision: 1, BaseRevision: "aaaa", ObservedRevision: "bbbb"}}, []domain.RequiredUnit{{Kind: "page", PagePath: canary, Reason: "no_cited_evidence"}, {Kind: "page", PagePath: "pages/first.html"}})
		page, e := view.fixture.queries.AssessmentCoverage(ctx, view.fixture.token, view.versionID, setID, "", query.Options{Limit: 1})
		Expect(e).NotTo(HaveOccurred())
		Expect(page.NextCursor).NotTo(BeNil())
		var other string
		Expect(view.fixture.store.Write(ctx, func(tx app.WriteTx) error {
			other = key(t)
			return tx.CreateProject(domain.Project{ID: other, Name: "Other"})
		})).To(Succeed())
		foreign := view.seedScopedReader(t, other)
		calls := map[string]func() error{
			"assessments": func() error {
				_, e := view.fixture.queries.Assessments(ctx, foreign, view.versionID, query.AssessmentQuery{})
				return e
			},
			"coverage": func() error {
				_, e := view.fixture.queries.AssessmentCoverage(ctx, foreign, view.versionID, setID, "", query.Options{})
				return e
			},
			"coverage cursor": func() error {
				_, e := view.fixture.queries.AssessmentCoverage(ctx, foreign, view.versionID, setID, "", query.Options{Limit: 1, Cursor: *page.NextCursor})
				return e
			},
			"attempt": func() error {
				_, e := view.fixture.queries.AssessmentCoverage(ctx, foreign, view.versionID, setID, run, query.Options{})
				return e
			},
			"project sources": func() error {
				_, e := view.fixture.queries.ProjectSources(ctx, foreign, view.fixture.project, query.Options{})
				return e
			},
		}
		for name, call := range calls {
			e := call()
			Expect(e).To(HaveOccurred(), name)
			Expect(errors.Is(e, domain.ErrNotFound) || errors.Is(e, domain.ErrDenied)).To(BeTrue(), name+": %v", e)
			Expect(e.Error()).NotTo(ContainSubstring("canary"), name)
		}
	})
})
