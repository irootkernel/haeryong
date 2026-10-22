package sqlite

import (
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	app "haeryong.local/haeryong/internal/application/storage"
	"haeryong.local/haeryong/internal/domain"
	_ "modernc.org/sqlite"
)

func nowStamp() string { return time.Now().UTC().Format(time.RFC3339Nano) }

var _ = Describe("schema7 source records", Label("persistence"), func() {
	t := GinkgoT()
	It("migrates to revision 7 and validates the new tables", func() {
		_, w := profile(t)
		s := openStore(t, w, nil)
		health, e := s.Health(ctx)
		Expect(e).NotTo(HaveOccurred())
		Expect(health.Library.SchemaRevision).To(Equal(SchemaRevision))
		// validateTables ran during Prepare; a restart keeps accepting the rows.
		Expect(s.RegisterProjectRow(ctx, domain.Project{ID: "11111111-1111-4111-8111-111111111111", Name: "Registered"})).To(Succeed())
		row, e := s.QueryProjectRow(ctx, "11111111-1111-4111-8111-111111111111")
		Expect(e).NotTo(HaveOccurred())
		Expect(row.Name).To(Equal("Registered"))
		Expect(row.UpdatedAt).NotTo(BeEmpty())
		var events []domain.Event
		Expect(s.Write(ctx, func(tx app.WriteTx) error {
			var e error
			events, e = tx.Events("", 0, 8)
			return e
		})).To(Succeed())
		Expect(events).NotTo(BeEmpty())
		Expect(events[len(events)-1].Kind).To(Equal("project.updated"))
	})

	It("persists captures, reads and verifications across restart including null unavailable fields", func() {
		_, w := profile(t)
		s := openStore(t, w, nil)
		project := "22222222-2222-4222-8222-222222222222"
		Expect(s.RegisterProjectRow(ctx, domain.Project{ID: project, Name: "P"})).To(Succeed())
		var captured, read, verification, unavailable string
		Expect(s.Write(ctx, func(tx app.WriteTx) error {
			captured, read, verification, unavailable = "33333333-3333-4333-8333-333333333333", "44444444-4444-4444-8444-444444444444", "55555555-5555-4555-8555-555555555555", "66666666-6666-4666-8666-666666666666"
			stamp := nowStamp()
			if e := tx.PutTargetSet(domain.TargetSet{ID: captured, ProjectID: project, RequestedSelection: []byte(`{"mode":"pinned"}`), TargetsJSON: []byte(`{"kind":"git","exact_revision":"abc"}`), CapturedAt: stamp}); e != nil {
				return e
			}
			if e := tx.PutCapturedRead(domain.CapturedRead{ID: read, TargetSetID: captured, Basis: "project_source", SourceJSON: []byte(`{}`), LocationJSON: []byte(`{"path":"a.go"}`), BytesSHA: "aa", ConfigRevision: 1, PermissionsRevision: 1, CreatedAt: stamp}); e != nil {
				return e
			}
			if e := tx.PutVerification(domain.CitationVerification{ID: verification, PrincipalID: "owner", VersionID: "v", EvidenceID: "e", ReadID: read, TargetSetID: captured, SourceJSON: []byte(`{}`), LocationJSON: []byte(`{"path":"a.go"}`), BytesSHA: "aa", Outcome: "matched", ConfigRevision: 1, PermissionsRevision: 1, CheckedAt: stamp, VerifiedAt: stamp, OperationID: "77777777-7777-4777-8777-777777777777"}); e != nil {
				return e
			}
			// An unavailable check records no read/target/bytes (SN1).
			return tx.PutVerification(domain.CitationVerification{ID: unavailable, PrincipalID: "owner", VersionID: "v", EvidenceID: "e", SourceJSON: []byte(`{}`), LocationJSON: []byte(`{"path":"a.go"}`), Outcome: "unavailable", Reason: "source_not_registered", ConfigRevision: 1, PermissionsRevision: 1, CheckedAt: stamp, OperationID: "88888888-8888-4888-8888-888888888888"})
		})).To(Succeed())
		// Restart.
		s2 := openStore(t, w, nil)
		var rows []domain.CitationVerification
		Expect(s2.Write(ctx, func(tx app.WriteTx) error {
			var e error
			rows, e = tx.Verifications("v", "e", "", "", 10)
			return e
		})).To(Succeed())
		Expect(rows).To(HaveLen(2))
		Expect(rows[0].ID).To(Equal(verification))
		Expect(rows[0].VerifiedAt).NotTo(BeEmpty())
		Expect(rows[1].ID).To(Equal(unavailable))
		Expect(rows[1].ReadID).To(BeEmpty())
		Expect(rows[1].TargetSetID).To(BeEmpty())
		Expect(rows[1].BytesSHA).To(BeEmpty())
		Expect(rows[1].Reason).To(Equal("source_not_registered"))
		var latest domain.CitationVerification
		Expect(s2.Write(ctx, func(tx app.WriteTx) error {
			var e error
			latest, e = tx.LatestVerification("v", "e")
			return e
		})).To(Succeed())
		Expect(latest.ID).To(Equal(unavailable), "newest checked_at wins")
		captures := []domain.TargetSet{}
		Expect(s2.Write(ctx, func(tx app.WriteTx) error {
			var e error
			captures, e = tx.SourceCaptures(project)
			return e
		})).To(Succeed())
		Expect(captures).To(HaveLen(1))
		var found bool
		var stored domain.CitationVerification
		Expect(s2.Write(ctx, func(tx app.WriteTx) error {
			var e error
			stored, found, e = tx.VerificationByOperation("owner", "77777777-7777-4777-8777-777777777777")
			return e
		})).To(Succeed())
		Expect(found).To(BeTrue())
		Expect(stored.ID).To(Equal(verification))
	})

	It("enforces the sentinel denominator on required units", func() {
		_, w := profile(t)
		s := openStore(t, w, nil)
		stamp := nowStamp()
		run := "aaaaaaa1-0000-4000-8000-000000000001"
		Expect(s.Write(ctx, func(tx app.WriteTx) error {
			if e := tx.PutObservationRun(domain.ObservationRun{ID: run, VersionID: "v", PrincipalID: "owner", OperationID: "aaaaaaa2-0000-4000-8000-000000000002", RequestedSelection: []byte(`{"mode":"current"}`), ScopeJSON: []byte(`{"kind":"version"}`), CreatedAt: stamp}); e != nil {
				return e
			}
			if e := tx.PutTargetSet(domain.TargetSet{ID: "99999999-9999-4999-8999-999999999999", ProjectID: "p", RequestedSelection: []byte(`{}`), TargetsJSON: []byte(`{}`), CapturedAt: stamp}); e != nil {
				return e
			}
			if e := putCoverageSet(tx, "cs1", "v", "99999999-9999-4999-8999-999999999999", run, `{"kind":"version"}`, "page", stamp); e != nil {
				return e
			}
			if e := putRequiredUnit(tx, "cs1", "page", "pages/a.html", "", "no_cited_evidence", "99999999-9999-4999-8999-999999999999"); e != nil {
				return e
			}
			// A version unit must keep both anchors empty; duplicates are
			// rejected by the primary key.
			if e := putRequiredUnit(tx, "cs1", "version", "", "", "x", "99999999-9999-4999-8999-999999999999"); e != nil {
				return e
			}
			return putRequiredUnit(tx, "cs1", "page", "pages/a.html", "", "duplicate", "99999999-9999-4999-8999-999999999999")
		})).NotTo(Succeed())
		// The duplicate rejection rolls back the whole single transaction:
		// the denominator stays all-or-nothing.
		var count int
		Expect(s.db.QueryRow("SELECT count(*) FROM required_units").Scan(&count)).To(Succeed())
		Expect(count).To(Equal(0))
	})

	It("persists observation runs, all-unavailable sets and null target units", func() {
		_, w := profile(t)
		s := openStore(t, w, nil)
		stamp := nowStamp()
		run := "bbbbbb01-0000-4000-8000-000000000001"
		Expect(s.Write(ctx, func(tx app.WriteTx) error {
			// An all-unavailable run persists its attempted scope with a
			// null target (r4.2 rule 2) and per-unit unavailable reasons.
			if e := tx.PutObservationRun(domain.ObservationRun{ID: run, VersionID: "v", PrincipalID: "owner", OperationID: "bbbbbb02-0000-4000-8000-000000000002", RequestedSelection: []byte(`{"project_id":"p","mode":"current","source_ids":["git-1"]}`), ScopeJSON: []byte(`{"kind":"version","declared":true}`), CreatedAt: stamp}); e != nil {
				return e
			}
			if e := tx.PutSourceObservation(domain.SourceObservation{ID: "bbbbbb03-0000-4000-8000-000000000003", RunID: run, VersionID: "v", SourceID: "git-1", Availability: "unavailable", Classification: "unavailable", Reason: "missing_source", ConfigRevision: 1, PermissionsRevision: 1, ObservedAt: stamp, OperationID: "bbbbbb02-0000-4000-8000-000000000002"}); e != nil {
				return e
			}
			if e := putCoverageSet(tx, "cs2", "v", "", run, `{"kind":"version","declared":true}`, "page", stamp); e != nil {
				return e
			}
			return putRequiredUnit(tx, "cs2", "page", "pages/a.html", "", "missing_source", "")
		})).To(Succeed())
		// Restart keeps the run, its null-target set and the null unit
		// target; one run per (principal, operation) is enforced.
		s2 := openStore(t, w, nil)
		var targetSet, unitTarget sql.NullString
		Expect(s2.db.QueryRow("SELECT target_set_id FROM observation_runs WHERE run_id=?", run).Scan(&targetSet)).To(Succeed())
		Expect(targetSet.Valid).To(BeFalse(), "an all-unavailable run carries no target")
		Expect(s2.db.QueryRow("SELECT target_set_id FROM required_units WHERE coverage_set_id='cs2'").Scan(&unitTarget)).To(Succeed())
		Expect(unitTarget.Valid).To(BeFalse(), "a unit of a null-target set carries a null target")
		Expect(s2.Write(ctx, func(tx app.WriteTx) error {
			return tx.PutObservationRun(domain.ObservationRun{ID: "bbbbbb04-0000-4000-8000-000000000004", VersionID: "v", PrincipalID: "owner", OperationID: "bbbbbb02-0000-4000-8000-000000000002", RequestedSelection: []byte(`{}`), ScopeJSON: []byte(`{}`), CreatedAt: stamp})
		})).NotTo(Succeed(), "one run per principal and operation (S16)")
	})

	It("rejects coverage sets without a target outside the observation origin", func() {
		_, w := profile(t)
		s := openStore(t, w, nil)
		// origin 'x' with a null target violates
		// CHECK(target_set_id IS NOT NULL OR origin='source_observation');
		// a source_observation origin without a run violates
		// CHECK(origin<>'source_observation' OR run_id IS NOT NULL) (r4.4 3c).
		_, e := s.db.Exec("INSERT INTO coverage_sets VALUES(?,?,?,?,?,?,?,'x')", "cs3", "v", nil, nil, "{}", "version", nowStamp())
		Expect(e).NotTo(BeNil())
		_, e = s.db.Exec("INSERT INTO coverage_sets VALUES(?,?,?,?,?,?,?,'source_observation')", "cs4", "v", nil, nil, "{}", "version", nowStamp())
		Expect(e).NotTo(BeNil())
		var count int
		Expect(s.db.QueryRow("SELECT count(*) FROM coverage_sets").Scan(&count)).To(Succeed())
		Expect(count).To(Equal(0))
	})

	It("sweeps open check intents and releases intent-owned pins", func() {
		_, w := profile(t)
		s := openStore(t, w, nil)
		stamp := nowStamp()
		Expect(s.Write(ctx, func(tx app.WriteTx) error {
			return tx.PutCheckIntent(domain.CheckIntent{ID: "intent-1", OperationID: "aaaaaaaa-0000-4000-8000-000000000001", PrincipalID: "owner", Kind: "verification", CanonicalTarget: []byte(`{}`), ConfigRevision: 1, PermissionsRevision: 1, State: "open", CreatedAt: stamp})
		})).To(Succeed())
		Expect(s.Write(ctx, func(tx app.WriteTx) error {
			if e := tx.PutDurableObject(domain.Object{Hash: "ff00ff00ff00ff00ff00ff00ff00ff00ff00ff00ff00ff00ff00ff00ff00ff00", Size: 1, State: "ready"}); e != nil {
				return e
			}
			return tx.AcquirePin(domain.Pin{ID: "pin-1", Kind: domain.CheckIntentPinOwner, OwnerID: "intent-1", Hash: "ff00ff00ff00ff00ff00ff00ff00ff00ff00ff00ff00ff00ff00ff00ff00ff00"})
		})).To(Succeed())
		var open []domain.CheckIntent
		Expect(s.Write(ctx, func(tx app.WriteTx) error {
			var e error
			open, e = tx.OpenCheckIntents()
			return e
		})).To(Succeed())
		Expect(open).To(HaveLen(1))
		Expect(s.Write(ctx, func(tx app.WriteTx) error {
			return tx.ReleaseCheckIntentPins("intent-1")
		})).To(Succeed())
		Expect(s.Write(ctx, func(tx app.WriteTx) error {
			return tx.ResolveCheckIntent("intent-1", "failed", "interrupted_before_commit", stamp)
		})).To(Succeed())
		var pins int
		Expect(s.db.QueryRow("SELECT count(*) FROM object_pins WHERE owner_kind='check_intent'").Scan(&pins)).To(Succeed())
		Expect(pins).To(Equal(0))
		var state, reason string
		Expect(s.db.QueryRow("SELECT state,failure_reason FROM check_intents WHERE intent_id='intent-1'").Scan(&state, &reason)).To(Succeed())
		Expect(state).To(Equal("failed"))
		Expect(reason).To(Equal("interrupted_before_commit"))
	})

	It("keeps one check intent per principal and operation (S16)", func() {
		_, w := profile(t)
		s := openStore(t, w, nil)
		stamp := nowStamp()
		intent := domain.CheckIntent{ID: "intent-1", OperationID: "aaaaaaaa-0000-4000-8000-000000000002", PrincipalID: "owner", Kind: "verification", CanonicalTarget: []byte(`{}`), ConfigRevision: 1, PermissionsRevision: 1, State: "open", CreatedAt: stamp}
		Expect(s.Write(ctx, func(tx app.WriteTx) error { return tx.PutCheckIntent(intent) })).To(Succeed())
		// A second intent id for the same principal and operation is rejected.
		second := intent
		second.ID = "intent-2"
		Expect(s.Write(ctx, func(tx app.WriteTx) error { return tx.PutCheckIntent(second) })).NotTo(Succeed())
		// Another principal may use the same operation id.
		other := intent
		other.ID, other.PrincipalID = "intent-3", "workflow"
		Expect(s.Write(ctx, func(tx app.WriteTx) error { return tx.PutCheckIntent(other) })).To(Succeed())
		var count int
		Expect(s.db.QueryRow("SELECT count(*) FROM check_intents WHERE operation_id=?", intent.OperationID).Scan(&count)).To(Succeed())
		Expect(count).To(Equal(2))
	})
})

var _ = Describe("populated schema-6 upgrade to schema 7", Label("persistence"), func() {
	t := GinkgoT()
	It("preserves populated rows through the upgrade, restart and recovery snapshot (C15)", func() {
		_, w := profile(t)
		s := openStore(t, w, nil)
		project := "31111111-1111-4111-8111-111111111111"
		document := "32121212-1212-4121-8121-121212121212"
		version := "33131313-1313-4131-8131-131313131313"
		set := "34141414-1414-4141-8141-141414141414"
		Expect(s.Write(ctx, func(tx app.WriteTx) error {
			if e := tx.CreateProject(domain.Project{ID: project, Name: "Populated"}); e != nil {
				return e
			}
			if e := tx.CreateDocument(domain.Document{ID: document, ProjectID: project, Title: "Doc"}); e != nil {
				return e
			}
			if e := tx.CreateContentSet(set, "0000000000000000000000000000000000000000000000000000000000000000"); e != nil {
				return e
			}
			if e := tx.SealContentSet(set); e != nil {
				return e
			}
			if e := tx.PutVersion(domain.Version{ID: version, DocumentID: document, ContentSetID: set, Title: "Doc", ViewerProfile: "html_js_v1", SourceBasis: "unverified", PayloadSHA: "0000000000000000000000000000000000000000000000000000000000000000", ManifestSHA: "0000000000000000000000000000000000000000000000000000000000000000"}); e != nil {
				return e
			}
			return tx.PublishVersion(version, 1)
		})).To(Succeed())
		// Revert to a schema-6 shape with populated rows retained.
		Expect(s.Close()).To(Succeed())
		{
			db := readDB(t, w.Profile)
			_, e := db.Exec(dropSearchSchema + `DROP INDEX verifications_by_evidence;DROP INDEX observations_by_source;DROP INDEX observations_by_time;DROP INDEX observations_by_run;DROP INDEX triggers_by_target;DROP INDEX check_intents_state;DROP INDEX observation_runs_by_operation;DROP INDEX observation_runs_by_version;DROP INDEX coverage_sets_by_run;DROP INDEX coverage_sets_by_version;DROP TABLE assessment_units;DROP TABLE assessments;DROP TABLE required_units;DROP TABLE coverage_sets;DROP TABLE check_intents;DROP TABLE reassessment_triggers;DROP TABLE source_observations;DROP TABLE observation_runs;DROP TABLE citation_verifications;DROP TABLE captured_reads;DROP TABLE captured_target_sets;DELETE FROM schema_migrations WHERE revision>6;UPDATE libraries SET schema_revision=6;PRAGMA user_version=6`)
			Expect(e).NotTo(HaveOccurred())
			db.Close()
		}
		// A migration fault leaves the prior library untouched and a
		// verifiable recovery snapshot behind (D7).
		injected := errors.New("schema7 fault")
		faulted := New(func(b Boundary) error {
			if b == BeforeMigrationCommit {
				return injected
			}
			return nil
		})
		_, _, e := faulted.Prepare(ctx, w.Profile)
		Expect(errors.Is(e, injected)).To(BeTrue())
		Expect(faulted.db).To(BeNil(), "a failed migration closes the adapter")
		prior := readDB(t, w.Profile)
		var revision int
		Expect(prior.QueryRow("PRAGMA user_version").Scan(&revision)).To(Succeed())
		Expect(revision).To(Equal(6), "the failed migration advanced nothing")
		var snapProject string
		Expect(prior.QueryRow("SELECT name FROM projects WHERE project_id=?", project).Scan(&snapProject)).To(Succeed())
		Expect(snapProject).To(Equal("Populated"), "the snapshot retains the populated rows")
		prior.Close()
		sets, e := filepath.Glob(filepath.Join(w.Profile.DataDir, "storage", "pre-migration-*.sqlite"))
		Expect(e).NotTo(HaveOccurred())
		Expect(sets).To(HaveLen(1), "exactly one coherent schema-6→7 recovery snapshot exists")
		_, e = os.Stat(sets[0] + ".json")
		Expect(e).NotTo(HaveOccurred(), "the recovery set carries its completion manifest")
		// E3: open the recovery snapshot itself — not the live database —
		// and verify schema 6 and the preserved rows.
		snapCopy := filepath.Join(t.TempDir(), "snapshot.sqlite")
		Expect(copyFile(sets[0], snapCopy)).To(Succeed())
		snap, e := sql.Open("sqlite", snapCopy)
		Expect(e).NotTo(HaveOccurred())
		var snapSchema int
		Expect(snap.QueryRow("SELECT schema_revision FROM libraries").Scan(&snapSchema)).To(Succeed())
		Expect(snapSchema).To(Equal(6), "the snapshot is the prior schema-6 library")
		var snapApplied int
		Expect(snap.QueryRow("SELECT count(*) FROM schema_migrations WHERE revision>6").Scan(&snapApplied)).To(Succeed())
		Expect(snapApplied).To(Equal(0))
		var snapRevision int
		Expect(snap.QueryRow("PRAGMA user_version").Scan(&snapRevision)).To(Succeed())
		Expect(snapRevision).To(Equal(6), "the snapshot's file-level schema marker is 6")
		var snapshotProject string
		Expect(snap.QueryRow("SELECT name FROM projects WHERE project_id=?", project).Scan(&snapshotProject)).To(Succeed())
		Expect(snapshotProject).To(Equal("Populated"))
		var snapVersion int
		Expect(snap.QueryRow("SELECT count(*) FROM document_versions WHERE version_id=?", version).Scan(&snapVersion)).To(Succeed())
		Expect(snapVersion).To(Equal(1), "the snapshot keeps the published version row")
		Expect(snap.Close()).To(Succeed())
		// The clean upgrade migrates to 7 and keeps every populated row.
		upgraded := openStore(t, w, nil)
		health, e := upgraded.Health(ctx)
		Expect(e).NotTo(HaveOccurred())
		Expect(health.Library.SchemaRevision).To(Equal(SchemaRevision))
		row, e := upgraded.QueryProjectRow(ctx, project)
		Expect(e).NotTo(HaveOccurred())
		Expect(row.Name).To(Equal("Populated"))
		var versions int
		Expect(upgraded.db.QueryRow("SELECT count(*) FROM document_versions WHERE version_id=?", version).Scan(&versions)).To(Succeed())
		Expect(versions).To(Equal(1))
		// A restart keeps the upgraded library readable.
		Expect(upgraded.Close()).To(Succeed())
		restarted := openStore(t, w, nil)
		_, e = restarted.QueryProjectRow(ctx, project)
		Expect(e).NotTo(HaveOccurred())
	})
})

func copyFile(from, to string) error {
	raw, err := os.ReadFile(from)
	if err != nil {
		return err
	}
	return os.WriteFile(to, raw, 0600)
}
