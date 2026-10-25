package query_test

import (
	"database/sql"
	"encoding/json"
	"fmt"
	app "haeryong.local/haeryong/internal/application/storage"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"haeryong.local/haeryong/internal/adapters/foundation"
	"haeryong.local/haeryong/internal/application/recovery"
	"haeryong.local/haeryong/internal/domain"
)

var _ = It("Copies scoped export plan and directory ZIP inventories preserve distinct version roots", Label("persistence"), func() {
	f := newFixture(GinkgoT())
	first := f.version
	f.publishVersion(GinkgoT(), map[string]string{"hello.html": "<html>different bytes</html>"}, "alias.html", "hello.html")
	second := f.version
	for _, format := range []string{"directory", "zip"} {
		destination := filepath.Join(GinkgoT().TempDir(), "output")
		core := recovery.New(f.store, f.auth, f.store)
		response, e := core.Submit(ctx, f.token, key(GinkgoT()), domain.CopyRequest{Action: "export.plan", ProjectID: f.project, Versions: "all", Format: format, Destination: destination, Records: []string{"review", "notes", "references"}})
		Expect(e).NotTo(HaveOccurred())
		var planned domain.CopyResult
		Expect(json.Unmarshal(response.Body, &planned)).To(Succeed())
		response, e = core.Submit(ctx, f.token, key(GinkgoT()), domain.CopyRequest{Action: "export.create", ID: planned.PlanID})
		Expect(e).NotTo(HaveOccurred())
		var accepted domain.CopyResult
		Expect(json.Unmarshal(response.Body, &accepted)).To(Succeed())
		Expect(copyTurns(f, accepted.JobID, 300).State).To(Equal("completed"))
		verified, e := foundation.VerifyExport(ctx, destination, "")
		Expect(e).NotTo(HaveOccurred())
		Expect(verified["versions"]).To(Equal(2))
		Expect(first).NotTo(Equal(second))
	}
})

var _ = It("Copies scoped export finalization failures retry the same frozen plan and explicit abandon releases only its output", Label("persistence"), func() {
	f := newFixture(GinkgoT())
	core := recovery.New(f.store, f.auth, f.store)
	for _, phase := range []string{"export_publish", "export_after_rename", "abandon"} {
		response, e := core.Submit(ctx, f.token, key(GinkgoT()), domain.CopyRequest{Action: "export.plan", VersionID: f.version, Format: "directory", Destination: filepath.Join(GinkgoT().TempDir(), "output")})
		Expect(e).NotTo(HaveOccurred())
		var plan domain.CopyResult
		Expect(json.Unmarshal(response.Body, &plan)).To(Succeed())
		response, e = core.Submit(ctx, f.token, key(GinkgoT()), domain.CopyRequest{Action: "export.create", ID: plan.PlanID})
		Expect(e).NotTo(HaveOccurred())
		var first domain.CopyResult
		Expect(json.Unmarshal(response.Body, &first)).To(Succeed())
		f.store.CopyFault = func(at string) error {
			if at == phase || phase == "abandon" && at == "export_publish" {
				return syscall.ENOSPC
			}
			return nil
		}
		var failure error
		for n := 0; n < 300; n++ {
			failure = f.store.ProcessCopy(ctx, first.JobID)
			if failure != nil {
				break
			}
		}
		Expect(failure).To(MatchError(syscall.ENOSPC))
		f.store.CopyFault = nil
		var set domain.CopySet
		Expect(f.store.Write(ctx, func(tx app.WriteTx) error { var e error; set, e = tx.CopySet(plan.ID); return e })).To(Succeed())
		if phase == "abandon" {
			_, e = core.Submit(ctx, f.token, key(GinkgoT()), domain.CopyRequest{Action: "backup.abandon", ID: set.ID, Confirmed: true})
			Expect(e).NotTo(HaveOccurred())
			_, e = os.Stat(set.Directory)
			Expect(os.IsNotExist(e)).To(BeTrue())
			continue
		}
		response, e = core.Submit(ctx, f.token, key(GinkgoT()), domain.CopyRequest{Action: "export.create", ID: plan.PlanID})
		Expect(e).NotTo(HaveOccurred())
		var retry domain.CopyResult
		Expect(json.Unmarshal(response.Body, &retry)).To(Succeed())
		Expect(retry.ID).To(Equal(first.ID))
		Expect(retry.JobID).NotTo(Equal(first.JobID))
		Expect(copyTurns(f, retry.JobID, 300).State).To(Equal("completed"))
	}
})

var _ = It("Copies retained verification expectation objects are opt-in while ordinary payload source bytes stay unconditional", Label("persistence"), func() {
	f := newFixture(GinkgoT())
	a, e := f.auth.Authenticate(ctx, f.token)
	Expect(e).NotTo(HaveOccurred())
	staged, e := f.store.Objects.Stage(ctx, strings.NewReader("separately retained expected source excerpt"))
	Expect(e).NotTo(HaveOccurred())
	pin := "expected-excerpt-fixture"
	object, e := f.store.Publish(ctx, staged, domain.Pin{ID: pin, Kind: "recovery", OwnerID: "copy-fixture"})
	Expect(e).NotTo(HaveOccurred())
	verification := key(GinkgoT())
	Expect(f.store.Write(ctx, func(tx app.WriteTx) error {
		scope, e := tx.Scope(f.project)
		if e != nil {
			return e
		}
		if e = tx.PutVerification(domain.CitationVerification{ID: verification, PrincipalID: a.PrincipalID, VersionID: f.version, EvidenceID: "explicitly-synthetic-typed-expectation-fixture", SourceJSON: []byte(`{}`), LocationJSON: []byte(`{}`), Outcome: "unavailable", ExpectedExcerptRef: object.Hash, ConfigRevision: scope.ConfigRevision, PermissionsRevision: scope.Project.PermissionsRevision, CheckedAt: time.Now().UTC().Format(time.RFC3339Nano), Reason: "synthetic_fixture_not_a_source_read", OperationID: key(GinkgoT())}); e != nil {
			return e
		}
		return tx.Adopt(domain.Reference{Kind: "verification_excerpt", ID: verification, Hash: object.Hash}, pin)
	})).To(Succeed())
	for _, inline := range [][]byte{[]byte{}, []byte("inline retained expectation")} {
		Expect(f.store.Write(ctx, func(tx app.WriteTx) error {
			return tx.PutVerification(domain.CitationVerification{ID: key(GinkgoT()), PrincipalID: a.PrincipalID, VersionID: f.version, EvidenceID: "synthetic-inline-expectation", SourceJSON: []byte(`{}`), LocationJSON: []byte(`{}`), Outcome: "unavailable", ExpectedExcerpt: inline, ConfigRevision: 1, PermissionsRevision: 1, CheckedAt: time.Now().UTC().Format(time.RFC3339Nano), Reason: "synthetic_fixture_not_a_source_read", OperationID: key(GinkgoT())})
		})).To(Succeed())
	}
	for _, include := range []bool{false, true} {
		destination := filepath.Join(GinkgoT().TempDir(), "output")
		core := recovery.New(f.store, f.auth, f.store)
		response, e := core.Submit(ctx, f.token, key(GinkgoT()), domain.CopyRequest{Action: "export.create", VersionID: f.version, Format: "directory", Destination: destination, Records: []string{"references"}, RetainedSources: include})
		Expect(e).NotTo(HaveOccurred())
		var accepted domain.CopyResult
		Expect(json.Unmarshal(response.Body, &accepted)).To(Succeed())
		Expect(copyTurns(f, accepted.JobID, 300).State).To(Equal("completed"))
		_, e = foundation.VerifyExport(ctx, destination, "")
		Expect(e).NotTo(HaveOccurred())
		path := filepath.Join(destination, "metadata", "versions", f.version, "objects", object.Hash)
		_, e = os.Stat(path)
		if include {
			Expect(e).NotTo(HaveOccurred())
			Expect(os.WriteFile(path, []byte("corrupt opted excerpt"), 0600)).To(Succeed())
			_, e = foundation.VerifyExport(ctx, destination, "")
			Expect(e).To(HaveOccurred())
		} else {
			Expect(os.IsNotExist(e)).To(BeTrue())
		}
	}
})

var _ = It("Copies recovered publication sync failures keep exact ownership for backup and separate-metadata directory ZIP exports", Label("persistence"), func() {
	for _, format := range []string{"backup", "directory", "zip"} {
		f := newFixture(GinkgoT())
		core := recovery.New(f.store, f.auth, f.store)
		destination := filepath.Join(GinkgoT().TempDir(), "output")
		request := domain.CopyRequest{Action: "backup.create", Destination: destination}
		phase := "after_rename"
		if format != "backup" {
			request = domain.CopyRequest{Action: "export.create", VersionID: f.version, Format: format, Destination: destination, MetadataDestination: filepath.Join(GinkgoT().TempDir(), "metadata"), Records: []string{"notes", "review"}}
			phase = "export_after_rename"
		}
		response, e := core.Submit(ctx, f.token, key(GinkgoT()), request)
		Expect(e).NotTo(HaveOccurred())
		var accepted domain.CopyResult
		Expect(json.Unmarshal(response.Body, &accepted)).To(Succeed())
		runFailure := func(job, at string) {
			fired := false
			f.store.CopyFault = func(p string) error {
				if p == at {
					fired = true
					return syscall.ENOSPC
				}
				return nil
			}
			var err error
			for n := 0; n < 300; n++ {
				err = f.store.ProcessCopy(ctx, job)
				if err != nil {
					break
				}
			}
			Expect(fired).To(BeTrue(), at)
			Expect(err).To(MatchError(syscall.ENOSPC))
			f.store.CopyFault = nil
			Expect(f.store.Write(ctx, func(tx app.WriteTx) error {
				set, e := tx.CopySet(accepted.ID)
				if e != nil {
					return e
				}
				Expect(set.Stage).To(Equal("failed"))
				j, e := tx.Job(job)
				if e != nil {
					return e
				}
				Expect(j.State).To(Equal("failed"))
				Expect(copyPinCount(f, accepted.ID)).To(BeNumerically(">", 0))
				return nil
			})).To(Succeed())
		}
		retry := func() domain.CopyResult {
			r := domain.CopyRequest{Action: "backup.resume", ID: accepted.ID}
			if format != "backup" {
				r.Action = "export.create"
			}
			response, e := core.Submit(ctx, f.token, key(GinkgoT()), r)
			Expect(e).NotTo(HaveOccurred())
			var next domain.CopyResult
			Expect(json.Unmarshal(response.Body, &next)).To(Succeed())
			Expect(next.ID).To(Equal(accepted.ID))
			return next
		}
		runFailure(accepted.JobID, phase)
		syncPhases := []string{"backup_parent_sync", "backup_parent_sync", "backup_destination_parent_sync"}
		if format != "backup" {
			syncPhases = []string{"export_metadata_parent_sync", "export_parent_sync", "export_source_parent_sync"}
		}
		for _, at := range syncPhases {
			next := retry()
			runFailure(next.JobID, at)
		}
		next := retry()
		Expect(copyTurns(f, next.JobID, 300).State).To(Equal("completed"))
		Expect(f.store.Write(ctx, func(tx app.WriteTx) error { Expect(copyPinCount(f, accepted.ID)).To(Equal(0)); return nil })).To(Succeed())
		if format == "backup" {
			_, e = foundation.VerifyBackup(ctx, filepath.Join(destination, accepted.ID))
		} else {
			_, e = foundation.VerifyExport(ctx, destination, filepath.Join(request.MetadataDestination, "export-index.json"))
		}
		Expect(e).NotTo(HaveOccurred())
	}
})

var _ = It("Copies export index and anchored record mutation corpus rejects structural and identity drift", Label("persistence"), func() {
	f := newFixture(GinkgoT())
	page := "hello.html"
	_, e := noteMutation(f, key(GinkgoT()), "POST", f.version, "", noteBody(&page, nil, "selected exact note"))
	Expect(e).NotTo(HaveOccurred())
	core := recovery.New(f.store, f.auth, f.store)
	destination := filepath.Join(GinkgoT().TempDir(), "export")
	response, e := core.Submit(ctx, f.token, key(GinkgoT()), domain.CopyRequest{Action: "export.create", VersionID: f.version, Format: "directory", Destination: destination, Records: []string{"notes", "review", "reader", "revisit", "references"}})
	Expect(e).NotTo(HaveOccurred())
	var accepted domain.CopyResult
	Expect(json.Unmarshal(response.Body, &accepted)).To(Succeed())
	Expect(copyTurns(f, accepted.JobID, 300).State).To(Equal("completed"))
	index := filepath.Join(destination, "export-index.json")
	original, e := os.ReadFile(index)
	Expect(e).NotTo(HaveOccurred())
	var decoded map[string]any
	Expect(json.Unmarshal(original, &decoded)).To(Succeed())
	mutations := []func() []byte{
		func() []byte { return append(append([]byte{}, original...), []byte(" {}")...) },
		func() []byte { return []byte(strings.Replace(string(original), "{", `{"format_version":1,`, 1)) },
		func() []byte { return []byte(strings.Replace(string(original), "{", `{"unknown":null,`, 1)) },
	}
	for _, field := range []string{"version_id", "document_id", "payload_root", "payload_sha256", "manifest_sha256", "records_sha256", "dependencies_sha256", "inventory_path"} {
		field := field
		mutations = append(mutations, func() []byte {
			var m map[string]any
			Expect(json.Unmarshal(original, &m)).To(Succeed())
			v := m["versions"].([]any)[0].(map[string]any)
			value := "invalid"
			if field == "document_id" || field == "version_id" {
				value = "11111111-1111-4111-8111-111111111111"
			}
			v[field] = value
			raw, e := json.Marshal(m)
			Expect(e).NotTo(HaveOccurred())
			return raw
		})
	}
	for n, mutate := range mutations {
		Expect(os.WriteFile(index, mutate(), 0600)).To(Succeed())
		_, e = foundation.VerifyExport(ctx, destination, "")
		Expect(e).To(HaveOccurred(), n)
	}
	Expect(os.WriteFile(index, original, 0600)).To(Succeed())
	for _, name := range []string{"payload/unselected.bin"} {
		path := filepath.Join(destination, name)
		Expect(os.WriteFile(path, []byte("extra"), 0600)).To(Succeed())
		_, e = foundation.VerifyExport(ctx, destination, "")
		Expect(e).To(HaveOccurred())
		Expect(os.Remove(path)).To(Succeed())
	}
	_, e = foundation.VerifyExport(ctx, destination, "")
	Expect(e).NotTo(HaveOccurred())
})

var _ = It("Copies selector lifecycle and paged frozen membership classes keep exact version identities", Label("persistence"), func() {
	for _, total := range []int{1, 63, 64, 65} {
		f := newFixture(GinkgoT())
		db, e := sql.Open("sqlite", f.dbPath)
		Expect(e).NotTo(HaveOccurred())
		// Labelled synthetic published-row cursor fixture. Real later-publication
		// identity is separately exercised through public import by the E2E driver.
		columns, e := db.Query(`PRAGMA table_info(document_versions)`)
		Expect(e).NotTo(HaveOccurred())
		names := []string{}
		for columns.Next() {
			var n, notnull, pk int
			var name, kind string
			var def any
			Expect(columns.Scan(&n, &name, &kind, &notnull, &def, &pk)).To(Succeed())
			names = append(names, name)
		}
		Expect(columns.Close()).To(Succeed())
		expressions := append([]string{}, names...)
		for n, name := range names {
			if name == "version_id" || name == "sequence" {
				expressions[n] = "?"
			}
		}
		expected := map[string]bool{f.version: true}
		latest := f.version
		for n := 2; n <= total; n++ {
			latest = key(GinkgoT())
			expected[latest] = true
			_, e = db.Exec(`INSERT INTO document_versions(`+strings.Join(names, ",")+`) SELECT `+strings.Join(expressions, ",")+` FROM document_versions WHERE version_id=?`, latest, n, f.version)
			Expect(e).NotTo(HaveOccurred())
		}
		_, e = db.Exec(`UPDATE documents SET latest_version_id=? WHERE document_id=?`, latest, f.document)
		Expect(e).NotTo(HaveOccurred())
		Expect(db.Close()).To(Succeed())
		core := recovery.New(f.store, f.auth, f.store)
		for _, state := range []string{"active", "archived", "trash"} {
			if state == "archived" {
				lifecycleCommit(f, lifecyclePlan(f, "archive"))
			}
			if state == "trash" {
				lifecycleCommit(f, lifecyclePlan(f, "trash"))
			}
			for _, filter := range []string{"", "active", "archived", "trash", "all"} {
				for _, selector := range []string{"version", "document", "project"} {
					for _, mode := range []string{"latest", "all"} {
						if selector == "version" && mode == "all" {
							continue
						}
						r := domain.CopyRequest{Action: "export.plan", Format: "zip", Destination: filepath.Join(GinkgoT().TempDir(), "output"), Lifecycle: filter}
						switch selector {
						case "version":
							r.VersionID = f.version
						case "document":
							r.DocumentID = f.document
							r.Versions = mode
						case "project":
							r.ProjectID = f.project
							r.Versions = mode
						}
						response, e := core.Submit(ctx, f.token, key(GinkgoT()), r)
						effective := filter
						if effective == "" {
							effective = "active"
						}
						if effective != "all" && effective != state {
							Expect(e).To(HaveOccurred())
							continue
						}
						Expect(e).NotTo(HaveOccurred())
						var plan domain.CopyResult
						Expect(json.Unmarshal(response.Body, &plan)).To(Succeed())
						found := map[string]bool{}
						cursor := ""
						for {
							response, e = core.Submit(ctx, f.token, "", domain.CopyRequest{Action: "export.plan.show", ID: plan.PlanID, Cursor: cursor})
							Expect(e).NotTo(HaveOccurred())
							var page domain.CopyPlanPage
							Expect(json.Unmarshal(response.Body, &page)).To(Succeed())
							for _, v := range page.Items {
								Expect(found[v.VersionID]).To(BeFalse())
								found[v.VersionID] = true
							}
							if page.NextCursor == nil {
								break
							}
							cursor = *page.NextCursor
						}
						want := expected
						if selector == "version" {
							want = map[string]bool{f.version: true}
						} else if mode == "latest" {
							want = map[string]bool{latest: true}
						}
						Expect(found).To(Equal(want), fmt.Sprint(total, state, filter, selector, mode))
					}
				}
			}
		}
	}
})
