package query_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	importer "haeryong.local/haeryong/internal/application/import"
	"haeryong.local/haeryong/internal/application/work"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"haeryong.local/haeryong/internal/adapters/foundation"
	config "haeryong.local/haeryong/internal/application/configuration"
	"haeryong.local/haeryong/internal/application/recovery"
	app "haeryong.local/haeryong/internal/application/storage"
	"haeryong.local/haeryong/internal/domain"
)

// Recompute the containing checksum so rejection proves semantic verification.
func cr2Rewrite(destination, version, name string, change func(map[string]any) bool) {
	GinkgoHelper()
	indexPath := filepath.Join(destination, "export-index.json")
	raw, e := os.ReadFile(indexPath)
	Expect(e).NotTo(HaveOccurred())
	var index map[string]any
	Expect(json.Unmarshal(raw, &index)).To(Succeed())
	var selected map[string]any
	for _, v := range index["versions"].([]any) {
		x := v.(map[string]any)
		if x["version_id"] == version {
			selected = x
		}
	}
	Expect(selected).NotTo(BeNil())
	records := filepath.Join(destination, selected["metadata_root"].(string), name+".jsonl")
	raw, e = os.ReadFile(records)
	Expect(e).NotTo(HaveOccurred())
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	changed := false
	for i, line := range lines {
		var row map[string]any
		Expect(json.Unmarshal([]byte(line), &row)).To(Succeed())
		if change(row) {
			b, _ := json.Marshal(row)
			lines[i] = string(b)
			changed = true
		}
	}
	Expect(changed).To(BeTrue())
	raw = []byte(strings.Join(lines, "\n") + "\n")
	Expect(os.WriteFile(records, raw, 0600)).To(Succeed())
	sum := sha256.Sum256(raw)
	selected[name+"_sha256"] = hex.EncodeToString(sum[:])
	raw, _ = json.Marshal(index)
	Expect(os.WriteFile(indexPath, raw, 0600)).To(Succeed())
}

var _ = DescribeTable("CR2 G5 Review declared semantic corpus", func(mode string) {
	f := newFixture(GinkgoT())
	_, e := reviewPut(f, key(GinkgoT()), "complete", "", true)
	Expect(e).NotTo(HaveOccurred())
	destination := filepath.Join(GinkgoT().TempDir(), "export")
	a := correctionExport(f, destination, "directory")
	Expect(copyTurns(f, a.JobID, 300).State).To(Equal("completed"))
	cr2Rewrite(destination, f.version, "records", func(r map[string]any) bool {
		if r["kind"] != "review" {
			return false
		}
		v := r["value"].(map[string]any)
		_, event := v["event_id"]
		if event {
			switch mode {
			case "action":
				v["action"] = "unknown"
			case "old":
				v["old_state"] = "unknown"
			case "new":
				v["new_state"] = "unknown"
			case "transition":
				v["action"] = "reopen"
				v["old_state"] = "unreviewed"
				v["new_state"] = "reviewing"
			case "noop":
				v["old_state"] = "reviewed"
			default:
				return false
			}
		} else {
			switch mode {
			case "missing_stamp":
				v["reviewed_at"] = nil
			case "extra_stamp":
				v["state"] = "reviewing"
			default:
				return false
			}
		}
		return true
	})
	_, e = foundation.VerifyExport(ctx, destination, "")
	Expect(e).To(HaveOccurred(), mode)
}, Entry("action enum", "action"), Entry("old enum", "old"), Entry("new enum", "new"), Entry("effective transition", "transition"), Entry("no no-op event", "noop"), Entry("reviewed stamp", "missing_stamp"), Entry("other state stamp", "extra_stamp"), Label("persistence"))

var _ = It("CR2 G5 selected Note anchors cannot select another document with matching context", Label("persistence"), func() {
	f := newFixture(GinkgoT())
	first := f.version
	page := "hello.html"
	_, e := noteMutation(f, key(GinkgoT()), "POST", first, "", noteBody(&page, nil, "note"))
	Expect(e).NotTo(HaveOccurred())
	f.publishVersion(GinkgoT(), map[string]string{"hello.html": "other document"}, "alias", "hello.html")
	second := f.version
	destination := filepath.Join(GinkgoT().TempDir(), "export")
	response, e := recovery.New(f.store, f.auth, f.store).Submit(ctx, f.token, key(GinkgoT()), domain.CopyRequest{Action: "export.create", ProjectID: f.project, Versions: "all", Format: "directory", Destination: destination, Records: []string{"notes"}})
	Expect(e).NotTo(HaveOccurred())
	var a domain.CopyResult
	Expect(json.Unmarshal(response.Body, &a)).To(Succeed())
	Expect(copyTurns(f, a.JobID, 300).State).To(Equal("completed"))
	cr2Rewrite(destination, first, "records", func(r map[string]any) bool {
		v := r["value"].(map[string]any)
		switch r["kind"] {
		case "notes":
			v["anchor"].(map[string]any)["version_id"] = second
			v["original_anchor"].(map[string]any)["version_id"] = second
			return true
		case "context":
			v["version_id"] = second
			return true
		}
		return false
	})
	cr2Rewrite(destination, first, "dependencies", func(r map[string]any) bool {
		if r["kind"] == "note_anchor" {
			r["version_id"] = second
			return true
		}
		return false
	})
	_, e = foundation.VerifyExport(ctx, destination, "")
	Expect(e).To(HaveOccurred())
})

var _ = DescribeTable("CR2 G7 stale cancelled selection cannot mutate retry or abandoned set", func(kind, mode string) {
	f := newFixture(GinkgoT())
	core := recovery.New(f.store, f.auth, f.store)
	request := domain.CopyRequest{Action: "backup.create", Destination: filepath.Join(GinkgoT().TempDir(), "copy")}
	if kind == "export" {
		request.Action = "export.create"
		request.VersionID = f.version
		request.Format = "directory"
	}
	operation := key(GinkgoT())
	r, e := core.Submit(ctx, f.token, operation, request)
	Expect(e).NotTo(HaveOccurred())
	var first domain.CopyResult
	Expect(json.Unmarshal(r.Body, &first)).To(Succeed())
	// The dispatcher legitimately selected J1 before the subsequent transactions.
	Expect(f.store.Write(ctx, func(tx app.WriteTx) error {
		jobs, e := tx.WorkJobs(64)
		Expect(jobs).To(ContainElement(HaveField("ID", first.JobID)))
		return e
	})).To(Succeed())
	Expect(f.store.Write(ctx, func(tx app.WriteTx) error { _, e := tx.CancelJob(first.JobID); return e })).To(Succeed())
	action := "backup.resume"
	if kind == "export" {
		action = "export.create"
	}
	if mode == "abandon" {
		action = "backup.abandon"
	}
	r, e = core.Submit(ctx, f.token, key(GinkgoT()), domain.CopyRequest{Action: action, ID: first.ID, Confirmed: mode == "abandon"})
	Expect(e).NotTo(HaveOccurred())
	var before domain.CopySet
	var j domain.Job
	Expect(f.store.Write(ctx, func(tx app.WriteTx) error {
		var e error
		before, e = tx.CopySet(first.ID)
		if e == nil {
			j, e = tx.Job(before.JobID)
		}
		return e
	})).To(Succeed())
	for _, process := range []func() error{func() error { return core.Process(ctx, first.JobID) }, func() error { return f.store.ProcessCopy(ctx, first.JobID) }} {
		_ = process()
		Expect(core.StopCopies()).To(Succeed())
		var after domain.CopySet
		Expect(f.store.Write(ctx, func(tx app.WriteTx) error { var e error; after, e = tx.CopySet(first.ID); return e })).To(Succeed())
		Expect(after).To(Equal(before))
		current, e := f.store.Job(ctx, before.JobID)
		Expect(e).NotTo(HaveOccurred())
		Expect(current).To(Equal(j))
	}
	replay, e := core.Submit(ctx, f.token, operation, request)
	Expect(e).NotTo(HaveOccurred())
	Expect(replay.Body).To(MatchJSON(responseBody(first)))
	if mode == "retry" {
		Expect(copyTurns(f, before.JobID, 300).State).To(Equal("completed"))
		_ = f.store.ProcessCopy(ctx, first.JobID)
		var after domain.CopySet
		Expect(f.store.Write(ctx, func(tx app.WriteTx) error { var e error; after, e = tx.CopySet(first.ID); return e })).To(Succeed())
		Expect(after.Stage).To(Equal("complete"))
	}
}, Entry("backup retry", "backup", "retry"), Entry("export retry", "export", "retry"), Entry("backup abandon", "backup", "abandon"), Entry("export abandon", "export", "abandon"), Label("persistence"))

var _ = It("CR2 G7 actual access configuration change drains held export into durable failure retaining pins", Label("persistence"), func() {
	f := newFixture(GinkgoT())
	core := recovery.New(f.store, f.auth, f.store)
	destination := filepath.Join(GinkgoT().TempDir(), "copy")
	a := correctionExport(f, destination, "directory")
	// Advance the finite turns until the frozen set owns real pins.
	for i := 0; i < 300; i++ {
		var set domain.CopySet
		Expect(f.store.Write(ctx, func(tx app.WriteTx) error { var e error; set, e = tx.CopySet(a.ID); return e })).To(Succeed())
		if !set.Capturing && set.Stage == "copying" {
			break
		}
		Expect(f.store.ProcessCopy(ctx, a.JobID)).To(Succeed())
	}
	entered, release := make(chan struct{}), make(chan struct{})
	var held, unblock sync.Once
	f.store.CopyFault = func(at string) error {
		if at == "export_final_transfer" {
			held.Do(func() { close(entered); <-release })
		}
		return nil
	}
	runCtx, cancel := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() {
		done <- (work.Dispatcher{Store: f.store, Copies: core, Imports: f.core}).Run(runCtx)
		close(done)
	}()
	DeferCleanup(func() { cancel(); unblock.Do(func() { close(release) }); Expect(<-done).To(Succeed()) })
	Eventually(entered, 5*time.Second).Should(BeClosed())
	independent := correctionPendingSeal(f)
	Eventually(func() string { j, _ := f.store.Job(ctx, independent); return j.State }, 5*time.Second).Should(Equal("completed"))
	configuration, e := f.store.Configuration(ctx)
	Expect(e).NotTo(HaveOccurred())
	authority := config.New(f.store, f.auth, cr2Paths{})
	r, e := authority.Plan(ctx, f.token, key(GinkgoT()), config.PlanRequest{Scope: config.Scope{Kind: "project", ProjectID: f.project}, ExpectedRevision: configuration.Revision, Changes: map[string]any{"output_destination": GinkgoT().TempDir()}})
	Expect(e).NotTo(HaveOccurred())
	var plan struct {
		ID string `json:"plan_id"`
	}
	Expect(json.Unmarshal(r.Body, &plan)).To(Succeed())
	_, e = authority.Commit(ctx, f.token, plan.ID, "", config.CommitRequest{ExpectedRevision: configuration.Revision, Confirmed: true})
	Expect(e).NotTo(HaveOccurred())
	unblock.Do(func() { close(release) })
	Eventually(func() string {
		j, _ := f.store.Job(ctx, a.JobID)
		return j.State
	}, 5*time.Second).Should(Equal("failed"))
	var set domain.CopySet
	Expect(f.store.Write(ctx, func(tx app.WriteTx) error { var e error; set, e = tx.CopySet(a.ID); return e })).To(Succeed())
	Expect(set.Stage).To(Equal("failed"))
	Expect(copyPinCount(f, a.ID)).To(BeNumerically(">", 0))
	_, e = os.Stat(destination)
	Expect(os.IsNotExist(e)).To(BeTrue())
	Expect(f.store.Write(ctx, func(tx app.WriteTx) error {
		jobs, e := tx.WorkJobs(64)
		Expect(jobs).NotTo(ContainElement(HaveField("ID", a.JobID)))
		return e
	})).To(Succeed())
})

// Resolve a real disposable output destination without host configuration.
type cr2Paths struct{}

func (cr2Paths) Canonical(p string, _ bool) (string, error) { return filepath.EvalSymlinks(p) }

var _ = It("CR2 G5 declared dependency membership and document relationships use the entire index", Label("persistence"), func() {
	for _, mode := range []string{"selected_wrong_document", "selected_excluded", "outside_included"} {
		f := newFixture(GinkgoT())
		version := f.version
		page := "hello.html"
		_, e := noteMutation(f, key(GinkgoT()), "POST", version, "", noteBody(&page, nil, "note"))
		Expect(e).NotTo(HaveOccurred())
		destination := filepath.Join(GinkgoT().TempDir(), "export")
		a := correctionExport(f, destination, "directory")
		Expect(copyTurns(f, a.JobID, 300).State).To(Equal("completed"))
		cr2Rewrite(destination, version, "dependencies", func(r map[string]any) bool {
			if r["kind"] != "note_anchor" {
				return false
			}
			switch mode {
			case "selected_wrong_document":
				r["document_id"] = key(GinkgoT())
			case "selected_excluded":
				r["state"] = "excluded_by_scope"
			case "outside_included":
				r["version_id"] = key(GinkgoT())
			}
			return true
		})
		_, e = foundation.VerifyExport(ctx, destination, "")
		Expect(e).To(HaveOccurred(), mode)
	}
})

var _ = It("CR2 G5 legitimate Review effective transitions and all current states verify without weakening history", Label("persistence"), func() {
	f := newFixture(GinkgoT())
	tag := ""
	for i, action := range []string{"start", "complete", "reopen", "clear", "complete", "clear"} {
		v, e := reviewPut(f, key(GinkgoT()), action, tag, i == 0)
		Expect(e).NotTo(HaveOccurred())
		Expect(v.ETag).NotTo(BeNil())
		tag = *v.ETag
		destination := filepath.Join(GinkgoT().TempDir(), "export")
		a := correctionExport(f, destination, "directory")
		Expect(copyTurns(f, a.JobID, 300).State).To(Equal("completed"))
		_, e = foundation.VerifyExport(ctx, destination, "")
		Expect(e).NotTo(HaveOccurred(), action)
	}
})

var _ = DescribeTable("CR2 G5 source dependency preserves its declared local identity and payload state", func(mode string) {
	f := newFixture(GinkgoT())
	meta := `{"schema_version":"1.0","sources":[{"id":"src","kind":"snapshot","retention_mode":"reference_only","snapshot_sha256":"` + hash("source snapshot") + `","asset_path":"hello.html"}]}`
	if mode == "revision_case" {
		meta = `{"schema_version":"1.0","sources":[{"id":"src","kind":"git","retention_mode":"reference_only","repo_id":"77777777-7777-4777-8777-777777777777","object_format":"sha1","exact_revision":"` + strings.Repeat("a", 40) + `"}]}`
	}
	f.correctionVersion(GinkgoT(), map[string]string{"hello.html": "source payload"}, "alias", "hello.html", meta)
	destination := filepath.Join(GinkgoT().TempDir(), "export")
	a := correctionExport(f, destination, "directory")
	Expect(copyTurns(f, a.JobID, 300).State).To(Equal("completed"))
	cr2Rewrite(destination, f.version, "dependencies", func(r map[string]any) bool {
		if r["kind"] != "source" {
			return false
		}
		switch mode {
		case "id":
			r["source"].(map[string]any)["id"] = "other"
		case "kind":
			r["source"].(map[string]any)["kind"] = "unknown"
		case "state":
			r["state"] = "included"
		case "snapshot_case":
			v := r["source"].(map[string]any)
			v["snapshot_sha256"] = strings.ToUpper(v["snapshot_sha256"].(string))
		case "revision_case":
			v := r["source"].(map[string]any)
			v["exact_revision"] = strings.ToUpper(v["exact_revision"].(string))
		}
		return true
	})
	_, e := foundation.VerifyExport(ctx, destination, "")
	Expect(e).To(HaveOccurred(), mode)
}, Entry("inner local identity", "id"), Entry("declared kind", "kind"), Entry("reference only state", "state"), Entry("uppercase snapshot hash", "snapshot_case"), Entry("uppercase Git revision", "revision_case"), Label("persistence"))

func cr2ChangeAccess(f *fixture) {
	GinkgoHelper()
	configuration, e := f.store.Configuration(ctx)
	Expect(e).NotTo(HaveOccurred())
	authority := config.New(f.store, f.auth, cr2Paths{})
	r, e := authority.Plan(ctx, f.token, key(GinkgoT()), config.PlanRequest{Scope: config.Scope{Kind: "project", ProjectID: f.project}, ExpectedRevision: configuration.Revision, Changes: map[string]any{"output_destination": GinkgoT().TempDir()}})
	Expect(e).NotTo(HaveOccurred())
	var plan struct {
		ID string `json:"plan_id"`
	}
	Expect(json.Unmarshal(r.Body, &plan)).To(Succeed())
	_, e = authority.Commit(ctx, f.token, plan.ID, "", config.CommitRequest{ExpectedRevision: configuration.Revision, Confirmed: true})
	Expect(e).NotTo(HaveOccurred())
}

var _ = DescribeTable("CR2 G7 revoked authority stops both admission and worker-first paths with cancellation precedence", func(mode string) {
	f := newFixture(GinkgoT())
	core := recovery.New(f.store, f.auth, f.store)
	destination := filepath.Join(GinkgoT().TempDir(), "export")
	a := correctionExport(f, destination, "directory")
	if mode == "admission" {
		cr2ChangeAccess(f)
		Expect(core.Process(ctx, a.JobID)).To(Succeed())
		j, e := f.store.Job(ctx, a.JobID)
		Expect(e).NotTo(HaveOccurred())
		Expect(j.State).To(Equal("failed"))
		Expect(j.Error).To(Equal("copy_authority_changed"))
		Expect(core.StopCopies()).To(Succeed())
		return
	}
	entered, release := make(chan struct{}), make(chan struct{})
	var held, unblock sync.Once
	f.store.CopyFault = func(at string) error {
		if at == "export_final_transfer" {
			held.Do(func() { close(entered); <-release })
		}
		return nil
	}
	DeferCleanup(func() { unblock.Do(func() { close(release) }); _ = core.StopCopies() })
	Eventually(func() bool {
		Expect(core.PollCopy(ctx)).To(Succeed())
		Expect(core.Process(ctx, a.JobID)).To(Succeed())
		select {
		case <-entered:
			return true
		default:
			return false
		}
	}, 5*time.Second).Should(BeTrue())
	cr2ChangeAccess(f)
	expected := "failed"
	if mode == "cancel" {
		Expect(f.store.Write(ctx, func(tx app.WriteTx) error { _, e := tx.CancelJob(a.JobID); return e })).To(Succeed())
		expected = "cancelled"
	}
	unblock.Do(func() { close(release) })
	if mode == "worker" {
		Eventually(func() string { j, _ := f.store.Job(ctx, a.JobID); return j.State }, 5*time.Second).Should(Equal(expected))
	}
	Eventually(func() string {
		Expect(core.PollCopy(ctx)).To(Succeed())
		j, _ := f.store.Job(ctx, a.JobID)
		return j.State
	}, 5*time.Second).Should(Equal(expected))
	Expect(copyPinCount(f, a.ID)).To(BeNumerically(">", 0))
	_, e := os.Stat(destination)
	Expect(os.IsNotExist(e)).To(BeTrue())
	var set domain.CopySet
	Expect(f.store.Write(ctx, func(tx app.WriteTx) error { var e error; set, e = tx.CopySet(a.ID); return e })).To(Succeed())
	if mode == "cancel" {
		Expect(set.Stage).To(Equal("interrupted"))
	} else {
		Expect(set.Stage).To(Equal("failed"))
	}
}, Entry("preclaim admission", "admission"), Entry("worker sees denial before polling", "worker"), Entry("cancel intent wins over failure", "cancel"), Label("persistence"))

var _ = DescribeTable("CR2 G5 whole-version navigation and deleted Note null coupling", func(mode string) {
	f := newFixture(GinkgoT())
	note, e := noteMutation(f, key(GinkgoT()), "POST", f.version, "", encodedNote(map[string]any{"page_path": nil, "region_id": nil, "page_query": "", "page_fragment": "", "body": "retained history", "body_ref": nil, "selected_text": "saved text", "context": map[string]any{"whole": true}, "context_ref": nil}))
	Expect(e).NotTo(HaveOccurred())
	if strings.HasPrefix(mode, "deleted_") {
		_, e = noteMutation(f, key(GinkgoT()), "DELETE", note.AnnotationID, note.ETag, nil)
		Expect(e).NotTo(HaveOccurred())
	}
	destination := filepath.Join(GinkgoT().TempDir(), "export")
	a := correctionExport(f, destination, "directory")
	Expect(copyTurns(f, a.JobID, 300).State).To(Equal("completed"))
	// Whole-version and complete permitted deletion history are legitimate.
	_, e = foundation.VerifyExport(ctx, destination, "")
	Expect(e).NotTo(HaveOccurred())
	raw, e := os.ReadFile(filepath.Join(destination, "metadata", "versions", f.version, "records.jsonl"))
	Expect(e).NotTo(HaveOccurred())
	var contextID string
	var object domain.Object
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		var row map[string]any
		Expect(json.Unmarshal([]byte(line), &row)).To(Succeed())
		if row["kind"] == "context" {
			v := row["value"].(map[string]any)
			contextID = v["context_ref"].(string)
			object = domain.Object{Hash: v["sha256"].(string), Size: int64(v["bytes"].(float64)), State: "ready"}
		}
	}
	Expect(contextID).NotTo(BeEmpty())
	cr2Rewrite(destination, f.version, "records", func(row map[string]any) bool {
		if row["kind"] != "notes" {
			return false
		}
		v := row["value"].(map[string]any)
		if strings.HasPrefix(mode, "deleted_") && v["deleted_at"] == nil {
			return false
		}
		switch mode {
		case "current_query":
			v["anchor"].(map[string]any)["page_query"] = "unexpected=1"
		case "original_fragment":
			v["original_anchor"].(map[string]any)["page_fragment"] = "unexpected"
		case "deleted_body":
			v["body"] = "revived"
		case "deleted_object":
			v["body_object"] = object
		case "deleted_context":
			v["context_ref"] = contextID
		case "deleted_selected":
			v["selected_text"] = "revived"
		case "live_null_body":
			v["body"] = nil
		case "live_action":
			v["action"] = "unknown"
		case "live_actor":
			v["actor_session"] = "invalid"
		case "live_operation":
			v["operation_id"] = "invalid"
		case "live_delete_action":
			v["action"] = "delete"
		}
		return true
	})
	_, e = foundation.VerifyExport(ctx, destination, "")
	Expect(e).To(HaveOccurred(), mode)
}, Entry("whole current query", "current_query"), Entry("whole original fragment", "original_fragment"), Entry("tombstone body", "deleted_body"), Entry("tombstone body object", "deleted_object"), Entry("tombstone saved context", "deleted_context"), Entry("tombstone selected text", "deleted_selected"), Entry("live body required", "live_null_body"), Entry("declared action", "live_action"), Entry("declared actor identity", "live_actor"), Entry("declared operation identity", "live_operation"), Entry("delete action is tombstone", "live_delete_action"), Label("persistence"))

var _ = DescribeTable("CR2 G5 valid source descriptor variants retain distinct dependency meanings", func(mode string) {
	f := newFixture(GinkgoT())
	source := map[string]any{"id": "src", "kind": "snapshot", "retention_mode": "reference_only", "snapshot_sha256": hash("source snapshot"), "asset_path": "hello.html"}
	expected := "external_reference"
	if strings.HasPrefix(mode, "git") {
		source["kind"] = "git"
		delete(source, "snapshot_sha256")
		source["repo_id"] = key(GinkgoT())
		source["object_format"] = "sha1"
		source["exact_revision"] = strings.Repeat("a", 40)
	}
	if strings.HasSuffix(mode, "included") {
		source["retention_mode"] = "selected_snapshot"
		source["bytes_sha256"] = hash("source payload")
		expected = "included"
	}
	if mode == "snapshot_missing" {
		source["retention_mode"] = "selected_snapshot"
		source["asset_path"] = "missing.bin"
		expected = "unavailable"
	}
	raw, _ := json.Marshal(map[string]any{"schema_version": "1.0", "sources": []any{source}})
	if mode == "snapshot_missing" {
		// The original package contract requires inventoried selected-snapshot
		// assets. This is an admission refusal, not a valid producer export.
		seal := correctionPendingSeal(f)
		Expect(f.core.Process(ctx, seal)).To(Succeed())
		sealed, e := f.store.Job(ctx, seal)
		Expect(e).NotTo(HaveOccurred())
		r, e := f.core.Import(ctx, f.token, key(GinkgoT()), importer.ImportRequest{UploadID: sealed.UploadID, DocumentPresent: true, VersionID: key(GinkgoT()), Metadata: strings.NewReader(string(raw))})
		if e == nil {
			var accepted struct {
				JobID string `json:"job_id"`
			}
			Expect(json.Unmarshal(r.Body, &accepted)).To(Succeed())
			e = f.core.Process(ctx, accepted.JobID)
		}
		Expect(errors.Is(e, importer.ErrPackageInvalid)).To(BeTrue())
		return
	}
	f.correctionVersion(GinkgoT(), map[string]string{"hello.html": "source payload"}, "alias", "hello.html", string(raw))
	destination := filepath.Join(GinkgoT().TempDir(), "export")
	a := correctionExport(f, destination, "directory")
	Expect(copyTurns(f, a.JobID, 300).State).To(Equal("completed"))
	_, e := foundation.VerifyExport(ctx, destination, "")
	Expect(e).NotTo(HaveOccurred())
	raw, e = os.ReadFile(filepath.Join(destination, "metadata", "versions", f.version, "dependencies.jsonl"))
	Expect(e).NotTo(HaveOccurred())
	found := false
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		var row map[string]any
		Expect(json.Unmarshal([]byte(line), &row)).To(Succeed())
		if row["kind"] == "source" {
			found = true
			Expect(row["state"]).To(Equal(expected))
		}
	}
	Expect(found).To(BeTrue())
}, Entry("reference snapshot", "snapshot_reference"), Entry("reference Git", "git_reference"), Entry("included snapshot", "snapshot_included"), Entry("included Git", "git_included"), Entry("missing selected snapshot asset refuses import", "snapshot_missing"), Label("persistence"))
