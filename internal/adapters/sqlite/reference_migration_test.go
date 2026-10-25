package sqlite

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"haeryong.local/haeryong/internal/adapters/contentstore"
	"haeryong.local/haeryong/internal/application/service"
	app "haeryong.local/haeryong/internal/application/storage"
	"haeryong.local/haeryong/internal/domain"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Construct revision 8 from its original migration bytes, never by downgrading
// the live revision-9 schema. This test-only fixture owns its database/profile.
func referenceSchema8(w *service.LockedProfile) ([]byte, string) {
	GinkgoHelper()
	path := filepath.Join(w.Profile.DataDir, "storage", "library.sqlite")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0600)
	Expect(err).NotTo(HaveOccurred())
	Expect(f.Close()).To(Succeed())
	uri := url.URL{Scheme: "file", Path: path}
	db, err := sql.Open("sqlite", uri.String())
	Expect(err).NotTo(HaveOccurred())
	db.SetMaxOpenConns(1)
	_, err = db.Exec(`PRAGMA foreign_keys=ON;PRAGMA journal_mode=WAL;PRAGMA synchronous=FULL;`)
	Expect(err).NotTo(HaveOccurred())
	tx, err := db.Begin()
	Expect(err).NotTo(HaveOccurred())
	for i, migration := range migrations[:8] {
		_, err = tx.Exec(migration)
		Expect(err).NotTo(HaveOccurred())
		_, err = tx.Exec(`INSERT INTO schema_migrations VALUES(?,?,?)`, i+1, digest([]byte(migration)), nowStamp())
		Expect(err).NotTo(HaveOccurred())
	}
	_, err = tx.Exec(`INSERT INTO libraries VALUES(1,?,?,8,?,?,?,?)`, w.Profile.LibraryID, w.Profile.ProfileID, refSource, refTarget, refJob, refUnknown)
	Expect(err).NotTo(HaveOccurred())
	_, err = tx.Exec(`INSERT INTO projects(project_id,name,lifecycle,revision,permissions_revision) VALUES(?,'Unfiled','active',1,1)`, refUnknown)
	Expect(err).NotTo(HaveOccurred())
	_, err = tx.Exec(`INSERT INTO config_revisions VALUES(1,?,'bootstrap',?)`, app.DefaultConfiguration(), nowStamp())
	Expect(err).NotTo(HaveOccurred())
	_, err = tx.Exec(`INSERT INTO auth_owner VALUES(1,?)`, refDocument)
	Expect(err).NotTo(HaveOccurred())
	_, err = tx.Exec(`PRAGMA user_version=8`)
	Expect(err).NotTo(HaveOccurred())
	Expect(tx.Commit()).To(Succeed())
	s := &Store{db: db, profile: w.Profile}
	manifest, _ := json.Marshal(map[string]any{"schema_version": "1.0", "viewer_profile": "html_js_v1", "source_basis": "unverified", "document": map[string]any{"id": refDocument, "project_id": refUnknown, "title": "Pinned schema8"}, "version": map[string]any{"id": refSource, "base_version_id": nil, "source_intent": nil, "created_at": nowStamp()}, "content": map[string]any{"default_page": nil, "payload_sha256": app.EmptyInventorySHA256, "inventory": "/api/v1/versions/" + refSource + "/files"}, "references": []any{map[string]any{"id": "legacy ref", "document_id": refTargetDocument, "version_id": refTarget, "page_path": nil, "region_id": nil, "resolution": "unresolved"}}})
	objects, err := contentstore.New(filepath.Join(w.Profile.DataDir, "storage"))
	Expect(err).NotTo(HaveOccurred())
	staged, err := objects.Stage(ctx, bytes.NewReader(manifest))
	Expect(err).NotTo(HaveOccurred())
	_, err = objects.Finalize(ctx, staged, func(o contentstore.Object, publish func() error) error {
		return s.Write(ctx, func(tx app.WriteTx) error {
			if err := publish(); err != nil {
				return err
			}
			if err := tx.PutDurableObject(domain.Object{Hash: o.Hash, Size: o.Size, State: "ready"}); err != nil {
				return err
			}
			return tx.Adopt(domain.Reference{Kind: "version_manifest", ID: refSource, Hash: o.Hash}, "")
		})
	})
	Expect(err).NotTo(HaveOccurred())
	Expect(objects.Close()).To(Succeed())
	Expect(s.Write(ctx, func(tx app.WriteTx) error {
		if err := tx.CreateDocument(domain.Document{ID: refDocument, ProjectID: refUnknown, Title: "Pinned schema8"}); err != nil {
			return err
		}
		if err := tx.CreateContentSet("legacy-set", app.EmptyInventorySHA256); err != nil {
			return err
		}
		if err := tx.SealContentSet("legacy-set"); err != nil {
			return err
		}
		if err := tx.PutVersion(domain.Version{ID: refSource, DocumentID: refDocument, ContentSetID: "legacy-set", ManifestSHA: staged.Object.Hash, PayloadSHA: app.EmptyInventorySHA256, ViewerProfile: "html_js_v1", SourceBasis: "unverified", Metadata: manifest}); err != nil {
			return err
		}
		return tx.PublishVersion(refSource, 1)
	})).To(Succeed())
	Expect(s.validateTables(ctx, 8)).To(Succeed())
	Expect(s.Close()).To(Succeed())
	Expect(w.MarkStorageReady(nil)).To(Succeed())
	return manifest, staged.Object.Hash
}

// FTS shadow bytes include startup merge cookies. Preserve authoritative rows
// and virtual query results, rather than treating engine cookies as author data.
func referencePriorRows(db *sql.DB) map[string]string {
	GinkgoHelper()
	names, err := db.Query(`SELECT name FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%' AND name NOT GLOB 'search_words_*' AND name NOT GLOB 'search_grams_*' AND name NOT IN ('reference_materializations','authored_references','authored_edges','review_states','review_events','annotations','annotation_history','revisit_flags','lifecycle_plans','lifecycle_plan_items','purge_intents','purged_versions','purge_candidates') ORDER BY name`)
	Expect(err).NotTo(HaveOccurred())
	var tables []string
	for names.Next() {
		var name string
		Expect(names.Scan(&name)).To(Succeed())
		tables = append(tables, name)
	}
	Expect(names.Err()).NotTo(HaveOccurred())
	names.Close()
	out := map[string]string{}
	for _, name := range tables {
		where := ""
		if name == "schema_migrations" {
			where = " WHERE revision<=8"
		}
		columns := "*"
		if name == "documents" {
			// Compare every original schema7/8 column; revision 12 defaults are checked separately.
			columns = "document_id,project_id,title,lifecycle,latest_version_id,revision,updated_at"
		}
		rows, err := db.Query(`SELECT ` + columns + ` FROM "` + name + `"` + where)
		Expect(err).NotTo(HaveOccurred())
		cols, err := rows.Columns()
		Expect(err).NotTo(HaveOccurred())
		values := []string{}
		for rows.Next() {
			row := make([]any, len(cols))
			ptr := make([]any, len(cols))
			for i := range row {
				ptr[i] = &row[i]
			}
			Expect(rows.Scan(ptr...)).To(Succeed())
			for i, v := range row {
				if b, ok := v.([]byte); ok {
					row[i] = string(b)
				}
				if name == "libraries" && cols[i] == "schema_revision" {
					row[i] = int64(8)
				}
			}
			raw, err := json.Marshal(row)
			Expect(err).NotTo(HaveOccurred())
			values = append(values, string(raw))
		}
		Expect(rows.Err()).NotTo(HaveOccurred())
		rows.Close()
		sort.Strings(values)
		out[name] = strings.Join(values, "\n")
	}
	return out
}

var _ = Describe("RF06 authentic schema8 upgrade", Label("persistence"), func() {
	It("preserves all prior rows and reference-bearing manifest recovery bytes through rollback, upgrade and restart (RV-RECOVERY)", func() {
		_, w := profile(GinkgoT())
		manifest, hash := referenceSchema8(w)
		old := readDB(GinkgoT(), w.Profile)
		prior := referencePriorRows(old)
		Expect(prior["document_versions"]).To(ContainSubstring(hash))
		old.Close()
		injected := errors.New("reference migration fault")
		for _, boundary := range []Boundary{BeforeSnapshotObjectSync, BeforeMigrationCommit} {
			up := New(func(b Boundary) error {
				if b == boundary {
					return injected
				}
				return nil
			})
			_, _, err := up.Prepare(ctx, w.Profile)
			Expect(errors.Is(err, injected)).To(BeTrue())
			Expect(up.db).To(BeNil())
			old = readDB(GinkgoT(), w.Profile)
			Expect(referencePriorRows(old)).To(Equal(prior))
			var rev, count int
			Expect(old.QueryRow(`PRAGMA user_version`).Scan(&rev)).To(Succeed())
			Expect(rev).To(Equal(8))
			Expect(old.QueryRow(`SELECT count(*) FROM sqlite_master WHERE name='authored_references'`).Scan(&count)).To(Succeed())
			Expect(count).To(BeZero())
			old.Close()
		}
		ready := openStore(GinkgoT(), w, nil)
		Expect(referencePriorRows(ready.db)).To(Equal(prior))
		Expect(ready.validateTables(ctx, SchemaRevision)).To(Succeed())
		var nonDefaultLifecycle int
		Expect(ready.db.QueryRow(`SELECT count(*) FROM documents WHERE previous_lifecycle IS NOT NULL OR trashed_at IS NOT NULL OR trash_generation<>0`).Scan(&nonDefaultLifecycle)).To(Succeed())
		Expect(nonDefaultLifecycle).To(BeZero(), "revision 12 adds only the declared empty/default lifecycle state")
		for _, table := range []string{"reference_materializations", "authored_references", "authored_edges", "lifecycle_plans", "lifecycle_plan_items", "purge_candidates", "purge_intents", "purged_versions"} {
			var n int
			Expect(ready.db.QueryRow(`SELECT count(*) FROM ` + table).Scan(&n)).To(Succeed())
			Expect(n).To(BeZero(), "DDL never backfills manifests")
		}
		snapshots, err := filepath.Glob(filepath.Join(w.Profile.DataDir, "storage", "pre-migration-*.sqlite"))
		Expect(err).NotTo(HaveOccurred())
		Expect(snapshots).To(HaveLen(3))
		complete := 0
		for _, path := range snapshots {
			marker, err := os.ReadFile(path + ".json")
			if os.IsNotExist(err) {
				continue
			}
			Expect(err).NotTo(HaveOccurred())
			complete++
			var record map[string]any
			Expect(json.Unmarshal(marker, &record)).To(Succeed())
			Expect(record["complete"]).To(Equal(true))
			Expect(record["schema_revision"]).To(Equal(float64(8)))
			raw, err := os.ReadFile(path)
			Expect(err).NotTo(HaveOccurred())
			Expect(record["sha256"]).To(Equal(digest(raw)))
			inventory, err := os.ReadFile(path + ".objects.jsonl")
			Expect(err).NotTo(HaveOccurred())
			Expect(record["objects_manifest_sha256"]).To(Equal(digest(inventory)))
			Expect(record["object_count"]).To(Equal(float64(1)))
			recovered, err := os.ReadFile(filepath.Join(path+".objects", hash))
			Expect(err).NotTo(HaveOccurred())
			Expect(recovered).To(Equal(manifest))
			Expect(digest(recovered)).To(Equal(hash))
			uri := url.URL{Scheme: "file", Path: path}
			q := uri.Query()
			q.Set("mode", "ro")
			uri.RawQuery = q.Encode()
			snapshot, err := sql.Open("sqlite", uri.String())
			Expect(err).NotTo(HaveOccurred())
			Expect(referencePriorRows(snapshot)).To(Equal(prior))
			Expect(snapshot.Close()).To(Succeed())
		}
		Expect(complete).To(Equal(2), "failed object sync has no complete marker")
		Expect(ready.Close()).To(Succeed())
		ready = openStore(GinkgoT(), w, nil)
		Expect(referencePriorRows(ready.db)).To(Equal(prior))
		after, err := filepath.Glob(filepath.Join(w.Profile.DataDir, "storage", "pre-migration-*.sqlite"))
		Expect(err).NotTo(HaveOccurred())
		Expect(after).To(HaveLen(3), "restart does not remigrate")
	})
})
