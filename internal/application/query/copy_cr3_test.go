package query_test

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"haeryong.local/haeryong/internal/application/references"
	"haeryong.local/haeryong/internal/application/search"
	"haeryong.local/haeryong/internal/application/work"
	"os"
	"path/filepath"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"haeryong.local/haeryong/internal/adapters/foundation"
	"haeryong.local/haeryong/internal/application/query"
	"haeryong.local/haeryong/internal/application/recovery"
	"haeryong.local/haeryong/internal/domain"
)

var _ = DescribeTable("CR3 G5 effective selected source digest binds verified payload", func(mode string) {
	f := newFixture(GinkgoT())
	source := map[string]any{"id": "src", "kind": "snapshot", "retention_mode": "selected_snapshot", "asset_path": "hello.html", "snapshot_sha256": hash("payload")}
	if mode != "fallback" {
		source["bytes_sha256"] = hash("payload")
		// An explicit byte hash takes precedence over snapshot identity.
		source["snapshot_sha256"] = hash("different snapshot identity")
	}
	raw, _ := json.Marshal(map[string]any{"schema_version": "1.0", "sources": []any{source}})
	f.correctionVersion(GinkgoT(), map[string]string{"hello.html": "payload"}, "alias", "hello.html", string(raw))
	destination := filepath.Join(GinkgoT().TempDir(), "export")
	a := correctionExport(f, destination, "directory")
	Expect(copyTurns(f, a.JobID, 300).State).To(Equal("completed"))
	_, e := foundation.VerifyExport(ctx, destination, "")
	Expect(e).NotTo(HaveOccurred())
	if mode == "precedence" {
		return
	}
	cr2Rewrite(destination, f.version, "dependencies", func(r map[string]any) bool {
		if r["kind"] != "source" {
			return false
		}
		field := "bytes_sha256"
		if mode == "fallback" {
			field = "snapshot_sha256"
		}
		r["source"].(map[string]any)[field] = hash("wrong bytes")
		return true
	})
	_, e = foundation.VerifyExport(ctx, destination, "")
	Expect(e).To(HaveOccurred())
}, Entry("explicit mismatch", "explicit"), Entry("fallback mismatch", "fallback"), Entry("explicit precedence positive", "precedence"), Label("persistence"))

var _ = DescribeTable("CR3 G5 selected Note pages join exact payload authority", func(which, page string) {
	f := newFixture(GinkgoT())
	f.correctionVersion(GinkgoT(), map[string]string{"hello.html": "<html>note</html>", "asset.txt": "ordinary payload", "dir/child.html": "child"}, "alias.html", "hello.html")
	original := "hello.html"
	note, e := noteMutation(f, key(GinkgoT()), "POST", f.version, "", noteBody(&original, nil, "first"))
	Expect(e).NotTo(HaveOccurred())
	_, e = noteMutation(f, key(GinkgoT()), "PATCH", note.AnnotationID, note.ETag, encodedNote(map[string]any{"action": "reattach", "anchor": domain.AnnotationAnchor{VersionID: f.version, PagePath: &original}, "selected_text": nil, "context": nil, "context_ref": nil}))
	Expect(e).NotTo(HaveOccurred())
	destination := filepath.Join(GinkgoT().TempDir(), "export")
	a := correctionExport(f, destination, "directory")
	Expect(copyTurns(f, a.JobID, 300).State).To(Equal("completed"))
	cr2Rewrite(destination, f.version, "records", func(r map[string]any) bool {
		v := r["value"].(map[string]any)
		if r["kind"] == "context" && which == "history" {
			v["page_path"] = page
			return true
		}
		if r["kind"] != "notes" {
			return false
		}
		if which == "history" && v["revision"].(float64) != 1 || which == "current" && v["revision"].(float64) != 2 {
			return false
		}
		field := "anchor"
		if which == "original" {
			field = "original_anchor"
		}
		v[field].(map[string]any)["page_path"] = page
		return true
	})
	cr3AddNoteDependency(destination, f.version, page)

	_, e = foundation.VerifyExport(ctx, destination, "")
	Expect(e).To(HaveOccurred())
}, Entry("current missing", "current", "missing.html"), Entry("original missing", "original", "missing.html"), Entry("retained history missing", "history", "missing.html"), Entry("ordinary non HTML", "current", "asset.txt"), Entry("directory", "current", "dir"), Entry("noncanonical symlink", "current", "alias.html"), Label("persistence"))

var _ = DescribeTable("CR3 G5 stored reader pages remain canonical HTML", func(kind string) {
	f := newFixture(GinkgoT())
	f.correctionVersion(GinkgoT(), map[string]string{"hello.html": "<html>reader</html>", "asset.txt": "ordinary payload"}, "alias", "hello.html")
	_, e := f.queries.PersonalPut(ctx, f.token, key(GinkgoT()), f.version, query.ReaderPreference, "", "", true, []byte(`{"default_page":"hello.html"}`))
	Expect(e).NotTo(HaveOccurred())
	destination := filepath.Join(GinkgoT().TempDir(), "export")
	r, e := recovery.New(f.store, f.auth, f.store).Submit(ctx, f.token, key(GinkgoT()), domain.CopyRequest{Action: "export.create", VersionID: f.version, Format: "directory", Destination: destination, Records: []string{"reader"}})
	Expect(e).NotTo(HaveOccurred())
	var a domain.CopyResult
	Expect(json.Unmarshal(r.Body, &a)).To(Succeed())
	Expect(copyTurns(f, a.JobID, 300).State).To(Equal("completed"))
	cr2Rewrite(destination, f.version, "records", func(r map[string]any) bool {
		if r["kind"] != "reader" {
			return false
		}
		v := r["value"].(map[string]any)
		if v["kind"] != "reader_preference" {
			return false
		}
		v["value"].(map[string]any)["default_page"] = kind
		return true
	})
	_, e = foundation.VerifyExport(ctx, destination, "")
	Expect(e).To(HaveOccurred())
}, Entry("non HTML file", "asset.txt"), Entry("symlink", "alias"), Label("persistence"))

var _ = DescribeTable("CR3 G5 authored wrong-document reference remains exact and unavailable", func(mode, format string) {
	f := newFixture(GinkgoT())
	target := f.version
	wrong := key(GinkgoT())
	expected := "unavailable"
	if mode == "matching" || mode == "matching_outside" || mode == "matching_purged" {
		wrong = f.document
		expected = "included"
	}
	if mode == "matching_outside" {
		expected = "excluded_by_scope"
	}
	if mode == "unknown" {
		target = key(GinkgoT())
	}
	if mode == "purged" || mode == "matching_purged" {
		cr3MaterializeReferences(f, target)
		_, accepted := purgeAccept(f)
		abandonPurgeUploads(f)
		clock := time.Now()
		Expect(purgeTurns(f, &work.Purger{Store: f.store, Bytes: f.store, Now: func() time.Time { clock = clock.Add(61 * time.Second); return clock }}, accepted.JobID, 300).State).To(Equal("completed"))
		if mode == "matching_purged" {
			expected = "tombstoned"
		}
	}
	declaration := map[string]any{"id": "wrong-target", "document_id": wrong, "version_id": target, "page_path": "missing.html", "region_id": "unproved", "resolution": "resolved", "from": map[string]any{"page_path": "unproved-source.html", "region_id": nil}}
	metadata, _ := json.Marshal(map[string]any{"schema_version": "1.0", "references": []any{declaration}})
	f.publishVersion(GinkgoT(), map[string]string{"hello.html": "<html>authored reference</html>"}, "alias", "hello.html", string(metadata))
	source := f.version
	cr3MaterializeReferences(f, source)
	destination := filepath.Join(GinkgoT().TempDir(), "export")
	request := domain.CopyRequest{Action: "export.create", ProjectID: f.project, Versions: "all", Format: format, Destination: destination, Records: []string{"references"}}
	if mode == "outside" || mode == "matching_outside" {
		request.ProjectID = ""
		request.Versions = ""
		request.VersionID = source
	}
	r, e := recovery.New(f.store, f.auth, f.store).Submit(ctx, f.token, key(GinkgoT()), request)
	Expect(e).NotTo(HaveOccurred())
	var a domain.CopyResult
	Expect(json.Unmarshal(r.Body, &a)).To(Succeed())
	Expect(copyTurns(f, a.JobID, 300).State).To(Equal("completed"))
	_, e = foundation.VerifyExport(ctx, destination, "")
	Expect(e).NotTo(HaveOccurred())
	if format == "zip" {
		return
	}
	raw, e := os.ReadFile(filepath.Join(destination, "metadata", "versions", source, "dependencies.jsonl"))
	Expect(e).NotTo(HaveOccurred())
	Expect(string(raw)).To(ContainSubstring(`"state":"` + expected + `"`))
	raw, e = os.ReadFile(filepath.Join(destination, "metadata", "versions", source, "records.jsonl"))
	Expect(e).NotTo(HaveOccurred())
	var record map[string]any
	Expect(json.Unmarshal([]byte(strings.TrimSpace(string(raw))), &record)).To(Succeed())
	Expect(record["value"].(map[string]any)["authored"]).To(Equal(declaration))
	var exact struct {
		Value struct {
			Authored json.RawMessage `json:"authored"`
		} `json:"value"`
	}
	Expect(json.Unmarshal([]byte(strings.TrimSpace(string(raw))), &exact)).To(Succeed())
	authored, _ := json.Marshal(declaration)
	Expect(string(exact.Value.Authored)).To(Equal(string(authored)))
	if mode != "selected" {
		return
	}
	cr2Rewrite(destination, source, "dependencies", func(r map[string]any) bool { r["state"] = "included"; return true })
	_, e = foundation.VerifyExport(ctx, destination, "")
	Expect(e).To(HaveOccurred())
}, Entry("wrong selected directory", "selected", "directory"), Entry("wrong selected ZIP", "selected", "zip"), Entry("wrong excluded tuple", "outside", "directory"), Entry("wrong purged tuple", "purged", "directory"), Entry("unknown tuple", "unknown", "directory"), Entry("exact selected unresolved page", "matching", "directory"), Entry("exact excluded tuple", "matching_outside", "directory"), Entry("exact purged tuple", "matching_purged", "directory"), Label("persistence"))

func cr3AddNoteDependency(destination, version, page string, target ...string) {
	GinkgoHelper()
	indexPath := filepath.Join(destination, "export-index.json")
	raw, e := os.ReadFile(indexPath)
	Expect(e).NotTo(HaveOccurred())
	var index map[string]any
	Expect(json.Unmarshal(raw, &index)).To(Succeed())
	for _, item := range index["versions"].([]any) {
		v := item.(map[string]any)
		if v["version_id"] != version {
			continue
		}
		file := filepath.Join(destination, v["metadata_root"].(string), "dependencies.jsonl")
		raw, e = os.ReadFile(file)
		Expect(e).NotTo(HaveOccurred())
		for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
			var row map[string]any
			Expect(json.Unmarshal([]byte(line), &row)).To(Succeed())
			if row["kind"] != "note_anchor" || len(target) > 0 && row["version_id"] != target[0] {
				continue
			}
			row["page_path"] = page
			extra, _ := json.Marshal(row)
			raw = append(raw, append(extra, '\n')...)
			break
		}
		Expect(os.WriteFile(file, raw, 0600)).To(Succeed())
		sum := sha256.Sum256(raw)
		v["dependencies_sha256"] = hex.EncodeToString(sum[:])
	}
	raw, _ = json.Marshal(index)
	Expect(os.WriteFile(indexPath, raw, 0600)).To(Succeed())
}

func cr3MaterializeReferences(f *fixture, version string) {
	GinkgoHelper()
	db, e := sql.Open("sqlite", f.dbPath)
	Expect(e).NotTo(HaveOccurred())
	rows, e := db.Query(`SELECT job_id,work_kind FROM derived_work WHERE version_id=? ORDER BY CASE work_kind WHEN 'search' THEN 0 ELSE 1 END`, version)
	Expect(e).NotTo(HaveOccurred())
	type work struct{ id, kind string }
	var items []work
	for rows.Next() {
		var w work
		Expect(rows.Scan(&w.id, &w.kind)).To(Succeed())
		items = append(items, w)
	}
	Expect(rows.Err()).NotTo(HaveOccurred())
	Expect(rows.Close()).To(Succeed())
	Expect(db.Close()).To(Succeed())
	Expect(items).NotTo(BeEmpty())
	for _, w := range items {
		for i := 0; i < 100; i++ {
			j, e := f.store.Job(ctx, w.id)
			Expect(e).NotTo(HaveOccurred())
			if j.State == "completed" {
				break
			}
			Expect(j.State).NotTo(Or(Equal("failed"), Equal("cancelled")))
			if w.kind == "search" {
				Expect(search.NewWorker(f.store, f.store).Process(ctx, w.id)).To(Succeed())
			} else {
				Expect(references.NewWorker(f.store, f.store).Process(ctx, w.id)).To(Succeed())
			}
		}
		j, e := f.store.Job(ctx, w.id)
		Expect(e).NotTo(HaveOccurred())
		Expect(j.State).To(Equal("completed"))
	}
}

var _ = It("CR3 G5 forward selected Note pages use their target version root", Label("persistence"), func() {
	f := newFixture(GinkgoT())
	first := f.version
	page := "hello.html"
	note, e := noteMutation(f, key(GinkgoT()), "POST", first, "", noteBody(&page, nil, "first"))
	Expect(e).NotTo(HaveOccurred())
	f.correctionVersion(GinkgoT(), map[string]string{"b.html": "<html>second</html>"}, "alias", "b.html")
	second := f.version
	other := "b.html"
	_, e = noteMutation(f, key(GinkgoT()), "PATCH", note.AnnotationID, note.ETag, encodedNote(map[string]any{"action": "reattach", "anchor": domain.AnnotationAnchor{VersionID: second, PagePath: &other}, "selected_text": nil, "context": nil, "context_ref": nil}))
	Expect(e).NotTo(HaveOccurred())
	destination := filepath.Join(GinkgoT().TempDir(), "export")
	response, e := recovery.New(f.store, f.auth, f.store).Submit(ctx, f.token, key(GinkgoT()), domain.CopyRequest{Action: "export.create", ProjectID: f.project, Versions: "all", Format: "directory", Destination: destination, Records: []string{"notes"}})
	Expect(e).NotTo(HaveOccurred())
	var a domain.CopyResult
	Expect(json.Unmarshal(response.Body, &a)).To(Succeed())
	Expect(copyTurns(f, a.JobID, 300).State).To(Equal("completed"))
	_, e = foundation.VerifyExport(ctx, destination, "")
	Expect(e).NotTo(HaveOccurred())
	for _, bucket := range []string{first, second} {
		cr2Rewrite(destination, bucket, "records", func(r map[string]any) bool {
			if r["kind"] != "notes" {
				return false
			}
			anchor := r["value"].(map[string]any)["anchor"].(map[string]any)
			if anchor["version_id"] != second {
				return false
			}
			anchor["page_path"] = page
			return true
		})
		cr3AddNoteDependency(destination, bucket, page, second)
	}
	_, e = foundation.VerifyExport(ctx, destination, "")
	Expect(e).To(HaveOccurred())
})
