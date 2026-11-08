package sqlite

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	"net/url"
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"haeryong.local/haeryong/internal/adapters/contentstore"
	appauth "haeryong.local/haeryong/internal/application/auth"
	"haeryong.local/haeryong/internal/application/query"
	app "haeryong.local/haeryong/internal/application/storage"
	"haeryong.local/haeryong/internal/domain"
)

// Test-only reverse construction, never a live product downgrade or an old binary.
// Remove migration 10's dependent personal tables before constructing pre-search
// schemas, including fixtures that subsequently remove the owner or versions.
const dropSearchSchema = dropProviderSchema + `DROP TABLE IF EXISTS copy_versions;DROP TABLE IF EXISTS copy_sets;DROP TABLE IF EXISTS recovery_holds;DROP TABLE IF EXISTS purge_candidates;DROP TABLE IF EXISTS purge_intents;DROP TABLE IF EXISTS purged_versions;DROP TABLE IF EXISTS lifecycle_plan_items;DROP TABLE IF EXISTS lifecycle_plans;DROP INDEX IF EXISTS lifecycle_trash;ALTER TABLE documents DROP COLUMN previous_lifecycle;ALTER TABLE documents DROP COLUMN trashed_at;ALTER TABLE documents DROP COLUMN trash_generation;DROP TABLE IF EXISTS annotation_history;DROP TABLE IF EXISTS annotations;DROP TABLE IF EXISTS revisit_flags;DROP TABLE IF EXISTS review_states;DROP TABLE IF EXISTS review_events;` + dropReferenceSchema + `DROP TRIGGER search_chunks_insert;DROP TRIGGER search_chunks_update;DROP TRIGGER search_chunks_delete;DROP TABLE search_words;DROP TABLE search_grams;DROP TABLE search_chunks;DROP TABLE search_units;DROP TABLE search_indexes;DROP TABLE derived_work;DROP TABLE page_anchors;`

func searchSeed(s *Store) (string, error) {
	h, err := s.Health(ctx)
	if err != nil {
		return "", err
	}
	v := "search-version"
	err = s.Write(ctx, func(tx app.WriteTx) error {
		if err := tx.CreateDocument(domain.Document{ID: "search-document", ProjectID: h.Library.UnfiledProjectID, Title: "Search"}); err != nil {
			return err
		}
		if err := tx.CreateContentSet("search-set", digest([]byte("inventory"))); err != nil {
			return err
		}
		if err := tx.SealContentSet("search-set"); err != nil {
			return err
		}
		for i, id := range []string{v, "search-version-2"} {
			base := ""
			if i > 0 {
				base = v
			}
			if err := tx.PutVersion(domain.Version{ID: id, BaseVersionID: base, DocumentID: "search-document", ContentSetID: "search-set", Title: "Search", ViewerProfile: "html_js_v1", SourceBasis: "unverified"}); err != nil {
				return err
			}
			for _, path := range []string{"pages/é.html", "pages/café.html", "pages/cafe\u0301.html"} {
				if err := tx.PutPage(domain.Page{VersionID: id, Path: path, Metadata: []byte(`{"label":"Supplied"}`)}); err != nil {
					return err
				}
				if err := tx.PutRegion(domain.Region{VersionID: id, PagePath: path, ID: "retry", Metadata: []byte(`{"label":"Supplied region"}`)}); err != nil {
					return err
				}
			}
			if err := tx.PutSource(domain.Source{VersionID: id, ID: "git-source", Metadata: []byte(`{"kind":"git","repo_id":"logical"}`)}); err != nil {
				return err
			}
			if err := tx.PublishVersion(id, int64(i+1)); err != nil {
				return err
			}
		}
		return nil
	})
	return v, err
}

// The second project is part of SC10's recovery fixture, not a new product path.
func seedSearchSecondProject(s *Store) error {
	return s.Write(ctx, func(tx app.WriteTx) error {
		if err := tx.CreateProject(domain.Project{ID: "search-second-project", Name: "Second project"}); err != nil {
			return err
		}
		for _, document := range []string{"search-second-document-a", "search-second-document-b"} {
			if err := tx.CreateDocument(domain.Document{ID: document, ProjectID: "search-second-project", Title: document}); err != nil {
				return err
			}
			set := document + "-set"
			if err := tx.CreateContentSet(set, digest([]byte(document))); err != nil {
				return err
			}
			if err := tx.SealContentSet(set); err != nil {
				return err
			}
			base := ""
			for i, suffix := range []string{"-v1", "-v2"} {
				version := document + suffix
				if err := tx.PutVersion(domain.Version{ID: version, DocumentID: document, BaseVersionID: base, ContentSetID: set, Title: version, ViewerProfile: "html_js_v1", SourceBasis: "unverified"}); err != nil {
					return err
				}
				for _, path := range []string{"second/one.html", "second/two.html"} {
					if err := tx.PutPage(domain.Page{VersionID: version, Path: path, Metadata: []byte(`{"label":"Second project page"}`)}); err != nil {
						return err
					}
				}
				if err := tx.PublishVersion(version, int64(i+1)); err != nil {
					return err
				}
				base = version
			}
		}
		return nil
	})
}

// Assert the independent project's denominator on every before/after/snapshot
// read. The row-value comparison below additionally preserves every field.
func assertSearchTwoProjects(db *sql.DB) {
	GinkgoHelper()
	for _, row := range []struct {
		query string
		count int
	}{
		{`SELECT count(*) FROM projects`, 2},
		{`SELECT count(*) FROM projects WHERE project_id='search-second-project' AND name='Second project'`, 1},
		{`SELECT count(*) FROM documents WHERE project_id='search-second-project'`, 2},
		{`SELECT count(*) FROM document_versions v JOIN documents d USING(document_id) WHERE d.project_id='search-second-project'`, 4},
		{`SELECT count(*) FROM version_pages p JOIN document_versions v USING(version_id) JOIN documents d USING(document_id) WHERE d.project_id='search-second-project'`, 8},
	} {
		var count int
		Expect(db.QueryRow(row.query).Scan(&count)).To(Succeed())
		Expect(count).To(Equal(row.count), "SC10 E4 second-project denominator: "+row.query)
	}
}

// Capture all prior-schema row values, not just row counts. The only permitted
// changes are later migration history and the library schema revision; identities,
// sessions, receipts, evidence, coverage, source7 and pins compare byte-for-byte.
// Later additive tables have no schema7 rows; their declarations are checked by
// startup and newly created Review tables are asserted empty after the upgrade.
func searchPriorRows(db *sql.DB) map[string]string {
	assertEmptyProviderMigrationTables(db)
	GinkgoHelper()
	assertSearchTwoProjects(db)
	names, err := db.Query(`SELECT name FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%' AND name NOT LIKE 'search_%' AND name NOT IN ('derived_work','page_anchors','reference_materializations','authored_references','authored_edges','review_states','review_events','annotations','annotation_history','revisit_flags','lifecycle_plans','lifecycle_plan_items','purge_intents','purged_versions','purge_candidates','copy_sets','copy_versions','recovery_holds','provider_transport','provider_attempts','provider_inputs','provider_retry_authorizations','provider_linked_retry_authorizations','provider_orphans','provider_reconcile_intents','source_captures','source_capture_reads','job_inputs','generation_intents','drafts','draft_history','workflow_selections','questions','answers','scan_runs','scan_captured_versions','job_items','findings') ORDER BY name`)
	Expect(err).NotTo(HaveOccurred())
	var tables []string
	for names.Next() {
		var name string
		Expect(names.Scan(&name)).To(Succeed())
		tables = append(tables, name)
	}
	Expect(names.Err()).NotTo(HaveOccurred())
	names.Close()
	result := map[string]string{}
	for _, name := range tables {
		where := ""
		if name == "schema_migrations" {
			where = " WHERE revision<=7"
		}
		columns := "*"
		if name == "documents" {
			// Compare every original schema7/8 column; revision 12 defaults are checked separately.
			columns = "document_id,project_id,title,lifecycle,latest_version_id,revision,updated_at"
		}
		rows, err := db.Query(`SELECT ` + columns + ` FROM "` + name + `"` + where + ` ORDER BY rowid`)
		Expect(err).NotTo(HaveOccurred())
		cols, err := rows.Columns()
		Expect(err).NotTo(HaveOccurred())
		var values [][]any
		for rows.Next() {
			row := make([]any, len(cols))
			ptr := make([]any, len(cols))
			for i := range row {
				ptr[i] = &row[i]
			}
			Expect(rows.Scan(ptr...)).To(Succeed())
			for i := range row {
				if b, ok := row[i].([]byte); ok {
					row[i] = string(b)
				}
				if name == "libraries" && cols[i] == "schema_revision" {
					row[i] = int64(7)
				}
			}
			values = append(values, row)
		}
		Expect(rows.Err()).NotTo(HaveOccurred())
		rows.Close()
		raw, err := json.Marshal(values)
		Expect(err).NotTo(HaveOccurred())
		result[name] = string(raw)
	}
	return result
}

var _ = Describe("SC10 schema8 recovery and postings", Label("persistence"), func() {
	It("preserves every populated schema7 row in two projects and verified objects through sync/commit rollback, retry and restart (SC10 E4; reversal: seedSearchSecondProject/recoverySnapshot/transaction rollback)", func() {
		for _, fault := range []Boundary{BeforeSnapshotSync, BeforeSnapshotObjectCopy, BeforeSnapshotObjectSync, BeforeMigrationCommit} {
			By(string(fault))
			t := GinkgoT()
			s, auth, w, _ := authFixture(t)
			_, owner := pairOwner(t, auth, "search migration session")
			version, err := searchSeed(s)
			Expect(err).NotTo(HaveOccurred())
			Expect(seedSearchSecondProject(s)).To(Succeed())
			objects, err := contentstore.New(filepath.Join(w.Profile.DataDir, "storage"))
			Expect(err).NotTo(HaveOccurred())
			payload := []byte("immutable pinned search migration bytes")
			staged, err := objects.Stage(ctx, bytes.NewReader(payload))
			Expect(err).NotTo(HaveOccurred())
			_, err = objects.Finalize(ctx, staged, func(o contentstore.Object, publish func() error) error {
				return s.Write(ctx, func(tx app.WriteTx) error {
					if err := publish(); err != nil {
						return err
					}
					if err := tx.PutDurableObject(domain.Object{Hash: o.Hash, Size: o.Size, State: "ready"}); err != nil {
						return err
					}
					return tx.AcquirePin(domain.Pin{ID: "search-pin", Kind: "backup", OwnerID: "search-migration", Hash: o.Hash})
				})
			})
			Expect(err).NotTo(HaveOccurred())
			Expect(objects.Close()).To(Succeed())
			stamp := nowStamp()
			health, err := s.Health(ctx)
			Expect(err).NotTo(HaveOccurred())
			Expect(s.Write(ctx, func(tx app.WriteTx) error {
				if err := tx.PutTargetSet(domain.TargetSet{ID: "search-target", ProjectID: health.Library.UnfiledProjectID, RequestedSelection: []byte(`{}`), TargetsJSON: []byte(`{}`), CapturedAt: stamp}); err != nil {
					return err
				}
				if err := tx.PutCapturedRead(domain.CapturedRead{ID: "search-read", TargetSetID: "search-target", Basis: "project_source", SourceJSON: []byte(`{}`), LocationJSON: []byte(`{}`), BytesSHA: staged.Object.Hash, ConfigRevision: 1, PermissionsRevision: 1, CreatedAt: stamp}); err != nil {
					return err
				}
				if err := tx.PutVerification(domain.CitationVerification{ID: "search-verification", PrincipalID: "owner", VersionID: version, EvidenceID: "e", ReadID: "search-read", TargetSetID: "search-target", SourceJSON: []byte(`{}`), LocationJSON: []byte(`{}`), Outcome: "matched", ConfigRevision: 1, PermissionsRevision: 1, CheckedAt: stamp, VerifiedAt: stamp, OperationID: "search-verification-operation"}); err != nil {
					return err
				}
				if err := tx.PutObservationRun(domain.ObservationRun{ID: "search-observation", VersionID: version, PrincipalID: "owner", OperationID: "search-observation-operation", RequestedSelection: []byte(`{}`), ScopeJSON: []byte(`{}`), TargetSetID: "search-target", CreatedAt: stamp}); err != nil {
					return err
				}
				if err := tx.PutSourceObservation(domain.SourceObservation{ID: "search-observation-item", RunID: "search-observation", VersionID: version, TargetSetID: "search-target", SourceID: "git-source", Availability: "available", Classification: "direct", ConfigRevision: 1, PermissionsRevision: 1, ObservedAt: stamp, OperationID: "search-observation-operation"}); err != nil {
					return err
				}
				if err := putCoverageSet(tx, "search-coverage", version, "search-target", "search-observation", `{}`, "page", stamp); err != nil {
					return err
				}
				return putRequiredUnit(tx, "search-coverage", "page", "pages/é.html", "", "declared", "search-target")
			})).To(Succeed())
			_, err = s.db.Exec(`INSERT INTO object_references VALUES('version','search-version',?)`, staged.Object.Hash)
			Expect(err).NotTo(HaveOccurred())
			_, err = s.db.Exec(`INSERT INTO operation_receipts VALUES('owner','search-receipt','fingerprint','{}','{}','completed',200,'{}')`)
			Expect(err).NotTo(HaveOccurred())
			_, err = s.db.Exec(dropSearchSchema + `DELETE FROM schema_migrations WHERE revision>7;UPDATE libraries SET schema_revision=7;PRAGMA user_version=7`)
			Expect(err).NotTo(HaveOccurred())
			prior := searchPriorRows(s.db)
			Expect(prior["device_sessions"]).NotTo(Equal("null"))
			Expect(s.Close()).To(Succeed())
			injected := errors.New("search migration fault")
			up := New(func(b Boundary) error {
				if b == fault {
					return injected
				}
				return nil
			})
			_, _, err = up.Prepare(ctx, w.Profile)
			Expect(errors.Is(err, injected)).To(BeTrue())
			Expect(up.db).To(BeNil())
			old := readDB(t, w.Profile)
			Expect(searchPriorRows(old)).To(Equal(prior))
			var rev int
			Expect(old.QueryRow("PRAGMA user_version").Scan(&rev)).To(Succeed())
			Expect(rev).To(Equal(7))
			var count int
			Expect(old.QueryRow("SELECT count(*) FROM sqlite_master WHERE name='search_units'").Scan(&count)).To(Succeed())
			Expect(count).To(Equal(0))
			old.Close()
			snapshots, err := filepath.Glob(filepath.Join(w.Profile.DataDir, "storage", "pre-migration-*.sqlite"))
			Expect(err).NotTo(HaveOccurred())
			Expect(snapshots).To(HaveLen(1))
			_, markerErr := os.Stat(snapshots[0] + ".json")
			if fault == BeforeMigrationCommit {
				Expect(markerErr).NotTo(HaveOccurred())
				checkSearchSnapshot(snapshots[0], prior, payload, staged.Object.Hash)
			} else {
				Expect(os.IsNotExist(markerErr)).To(BeTrue(), "no falsely complete recovery marker")
			}
			ready := openStore(t, w, nil)
			Expect(searchPriorRows(ready.db)).To(Equal(prior))
			Expect(ready.validateTables(ctx, SchemaRevision)).To(Succeed())
			var nonDefaultLifecycle int
			Expect(ready.db.QueryRow(`SELECT count(*) FROM documents WHERE previous_lifecycle IS NOT NULL OR trashed_at IS NOT NULL OR trash_generation<>0`).Scan(&nonDefaultLifecycle)).To(Succeed())
			Expect(nonDefaultLifecycle).To(BeZero(), "revision 12 adds only the declared empty/default lifecycle state")
			for _, table := range []string{"annotation_history", "annotations", "revisit_flags", "review_states", "review_events", "lifecycle_plans", "lifecycle_plan_items", "purge_candidates", "purge_intents", "purged_versions", "provider_transport", "provider_attempts", "provider_inputs", "provider_retry_authorizations", "provider_linked_retry_authorizations", "provider_orphans", "provider_reconcile_intents", "source_captures", "source_capture_reads", "job_inputs"} {
				Expect(ready.db.QueryRow(`SELECT count(*) FROM ` + table).Scan(&count)).To(Succeed())
				Expect(count).To(BeZero(), "additive migrations create empty personal and lifecycle plan tables")
			}
			Expect(ready.db.QueryRow("SELECT count(*) FROM schema_migrations WHERE revision=8").Scan(&count)).To(Succeed())
			Expect(count).To(Equal(1))
			snapshots, err = filepath.Glob(filepath.Join(w.Profile.DataDir, "storage", "pre-migration-*.sqlite"))
			Expect(err).NotTo(HaveOccurred())
			Expect(snapshots).To(HaveLen(2))
			for _, path := range snapshots {
				if _, err := os.Stat(path + ".json"); err == nil {
					checkSearchSnapshot(path, prior, payload, staged.Object.Hash)
				}
			}

			// M4 ranked extension on the populated, fault/retry-upgraded schema7 fixture.
			Expect(ready.Write(ctx, func(tx app.WriteTx) error {
				if e := tx.PutSearchIndex(domain.SearchIndex{VersionID: version, State: "complete", DiscoveryComplete: true, Revision: 1, ExtractorRevision: domain.SearchExtractorRevision}); e != nil {
					return e
				}
				unit, e := tx.PutSearchUnit(domain.SearchUnit{VersionID: version, Kind: "page", PagePath: "pages/é.html", Title: "Migration exact page", State: "ready", BodyState: "complete", AnchorState: "not_applicable"})
				if e != nil {
					return e
				}
				return tx.PutSearchChunk(domain.SearchChunk{UnitID: unit, Part: "body", Body: "migrationrankliteral", DisplayBody: "migrationRankLiteral"})
			})).To(Succeed())
			options := query.SearchOptions{Options: query.Options{Kind: "page"}, Versions: "all", Text: "migrationRankLiteral"}
			ranked, e := query.New(ready, appauth.New(ready), nil).Search(ctx, owner.Token, options)
			Expect(e).NotTo(HaveOccurred())
			Expect(ranked.Items).To(HaveLen(1))
			Expect(ranked.Items[0].VersionID).To(Equal(version))
			Expect(*ranked.Items[0].PagePath).To(Equal("pages/é.html"))
			Expect(ready.Close()).To(Succeed())
			again := openStore(t, w, nil)
			Expect(searchPriorRows(again.db)).To(Equal(prior))
			restarted, e := query.New(again, appauth.New(again), nil).Search(ctx, owner.Token, options)
			Expect(e).NotTo(HaveOccurred())
			Expect(restarted.Items).To(Equal(ranked.Items))
			Expect(again.Close()).To(Succeed())
			after, _ := filepath.Glob(filepath.Join(w.Profile.DataDir, "storage", "pre-migration-*.sqlite"))
			Expect(after).To(HaveLen(2), "restart does not migrate twice")
		}
	})
	It("maintains both external-content postings on insert/update/delete and reopens populated FTS (reversal: search_chunks triggers)", func() {
		t := GinkgoT()
		_, w := profile(t)
		s := openStore(t, w, nil)
		version, err := searchSeed(s)
		Expect(err).NotTo(HaveOccurred())
		var unit int64
		Expect(s.Write(ctx, func(tx app.WriteTx) error {
			var e error
			unit, e = tx.PutSearchUnit(domain.SearchUnit{VersionID: version, Kind: "page", PagePath: "pages/é.html", State: "ready", BodyState: "complete", AnchorState: "not_applicable"})
			if e != nil {
				return e
			}
			return tx.PutSearchChunk(domain.SearchChunk{UnitID: unit, Part: "body", Body: "uniqueoriginal", DisplayBody: "UniqueOriginal"})
		})).To(Succeed())
		posting := func(term string, want int) {
			for _, table := range []string{"search_words", "search_grams"} {
				var n int
				Expect(s.db.QueryRow("SELECT count(*) FROM "+table+" WHERE "+table+" MATCH ?", term).Scan(&n)).To(Succeed())
				Expect(n).To(Equal(want))
				_, e := s.db.Exec("INSERT INTO " + table + "(" + table + ",rank) VALUES('integrity-check',1)")
				Expect(e).NotTo(HaveOccurred())
			}
		}
		posting("uniqueoriginal", 1)
		Expect(s.Close()).To(Succeed())
		s = openStore(t, w, nil)
		posting("uniqueoriginal", 1)
		_, err = s.db.Exec("UPDATE search_chunks SET body='uniquereplacement'")
		Expect(err).NotTo(HaveOccurred())
		posting("uniqueoriginal", 0)
		posting("uniquereplacement", 1)
		_, err = s.db.Exec("DELETE FROM search_units WHERE unit_id=?", unit)
		Expect(err).NotTo(HaveOccurred())
		posting("uniquereplacement", 0)
		var integrity string
		Expect(s.db.QueryRow("PRAGMA integrity_check").Scan(&integrity)).To(Succeed())
		Expect(integrity).To(Equal("ok"))
		rows, err := s.db.Query("PRAGMA foreign_key_check")
		Expect(err).NotTo(HaveOccurred())
		Expect(rows.Next()).To(BeFalse())
		Expect(rows.Err()).NotTo(HaveOccurred())
		rows.Close()
	})
	It("rejects unknown/missing/forged FTS inventory and declarations (reversal: validateTables exact shadow/type/declaration checks)", func() {
		for _, tamper := range []string{
			"CREATE VIRTUAL TABLE unknown_fts USING fts5(body)",
			"CREATE TABLE search_words_content(value TEXT)",
			"CREATE TABLE search_words_arbitrary(value TEXT)",
			"DROP TABLE search_words_docsize",
			"DROP TABLE search_words_config;CREATE TABLE search_words_config(k PRIMARY KEY,v) WITHOUT ROWID",
			"DROP TRIGGER search_chunks_insert",
			"DROP TABLE search_grams;CREATE VIRTUAL TABLE search_grams USING fts5(title,path,terms,body,content='search_chunks',content_rowid='chunk_id',tokenize='unicode61')",
		} {
			By(tamper)
			t := GinkgoT()
			_, w := profile(t)
			s := openStore(t, w, nil)
			Expect(s.validateTables(ctx, SchemaRevision)).To(Succeed(), "pinned engine has exactly data/idx/docsize/config shadows")
			_, err := s.db.Exec(tamper)
			Expect(err).NotTo(HaveOccurred())
			Expect(s.validateTables(ctx, SchemaRevision)).NotTo(Succeed())
			Expect(s.Close()).To(Succeed())
			failed := New(nil)
			_, _, err = failed.Prepare(ctx, w.Profile)
			Expect(err).To(HaveOccurred())
			Expect(failed.db).To(BeNil())
		}
	})
})

func checkSearchSnapshot(path string, prior map[string]string, payload []byte, hash string) {
	GinkgoHelper()
	raw, err := os.ReadFile(path)
	Expect(err).NotTo(HaveOccurred())
	marker, err := os.ReadFile(path + ".json")
	Expect(err).NotTo(HaveOccurred())
	var m map[string]any
	Expect(json.Unmarshal(marker, &m)).To(Succeed())
	Expect(m["complete"]).To(Equal(true))
	Expect(m["schema_revision"]).To(Equal(float64(7)))
	Expect(m["sha256"]).To(Equal(digest(raw)))
	manifest, err := os.ReadFile(path + ".objects.jsonl")
	Expect(err).NotTo(HaveOccurred())
	Expect(m["objects_manifest_sha256"]).To(Equal(digest(manifest)))
	Expect(m["object_count"]).To(Equal(float64(1)))
	object, err := os.ReadFile(filepath.Join(path+".objects", hash))
	Expect(err).NotTo(HaveOccurred())
	Expect(object).To(Equal(payload))
	Expect(digest(object)).To(Equal(hash))
	uri := url.URL{Scheme: "file", Path: path}
	query := uri.Query()
	query.Set("mode", "ro")
	uri.RawQuery = query.Encode()
	db, err := sql.Open("sqlite", uri.String())
	Expect(err).NotTo(HaveOccurred())
	defer db.Close()
	actual := searchPriorRows(db)
	Expect(actual).To(HaveLen(len(prior)))
	for name, want := range prior {
		Expect(actual[name]).To(Equal(want), "snapshot table "+name)
	}
	var revision int
	Expect(db.QueryRow("PRAGMA user_version").Scan(&revision)).To(Succeed())
	Expect(revision).To(Equal(7))
	var check string
	Expect(db.QueryRow("PRAGMA integrity_check").Scan(&check)).To(Succeed())
	Expect(check).To(Equal("ok"))
}
