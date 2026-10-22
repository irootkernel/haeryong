package importer_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	importer "haeryong.local/haeryong/internal/application/import"
	"haeryong.local/haeryong/internal/application/query"
	"haeryong.local/haeryong/internal/application/search"
	app "haeryong.local/haeryong/internal/application/storage"
	"haeryong.local/haeryong/internal/domain"
	sec "haeryong.local/haeryong/internal/security/session"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func corpusImport(f *fixture, name, marker string) (string, string, string) {
	t := GinkgoT()
	target := encodedTest(map[string]any{"project_id": f.project, "document_id": nil})
	created, e := f.core.Create(ctx, f.token, key(t), importer.CreateRequest{Purpose: "result", InputMode: "directory", SelectionName: name, Target: target})
	Expect(e).NotTo(HaveOccurred())
	var creation struct {
		ID string `json:"upload_id"`
	}
	Expect(json.Unmarshal(created.Body, &creation)).To(Succeed())
	upload := creation.ID
	root := filepath.Join("../../../spikes/knowledge-value/samples", name)
	var cp domain.Checkpoint
	count := 0
	paths := map[string][]string{
		"mulgae": {"assets/recovery.js", "assets/style.css", "recovery/compose.html", "recovery/inventory.html"},
		"gaori":  {"assets/gaori/illustrations.js", "assets/style.css", "gaori/lifecycle.html", "gaori/outcomes.html"},
	}[name]
	for _, relative := range paths {
		data, e := os.ReadFile(filepath.Join(root, filepath.FromSlash(relative)))
		Expect(e).NotTo(HaveOccurred())
		if marker != "" && strings.HasSuffix(relative, ".html") {
			data = append(data, []byte("<p>"+marker+"</p>")...)
		}
		hash := sha256.Sum256(data)
		length := int64(len(data))
		ack, e := f.core.Put(ctx, f.token, upload, importer.Chunk{Path: filepath.ToSlash(relative), Kind: "file", Hash: hex.EncodeToString(hash[:]), Length: &length, Final: true}, bytes.NewReader(data))
		if e != nil {
			Fail(e.Error())
		}
		cp = ack.Checkpoint
		count++
	}
	Expect(count).To(Equal(4))
	cp.Generation++
	cp.Complete = true
	accepted, e := f.core.Complete(ctx, f.token, upload, key(t), cp)
	Expect(e).NotTo(HaveOccurred())
	Expect(f.core.Process(ctx, decoded(t, accepted)["job_id"])).To(Succeed())
	doc, version := f.imported(t, upload, "")
	var index domain.SearchIndex
	Expect(f.store.Write(ctx, func(tx app.WriteTx) error { var e error; index, e = tx.SearchIndex(version); return e })).To(Succeed())
	return doc, version, index.JobID
}
func corpusFinish(f *fixture, job string) {
	w := search.NewWorker(f.store, f.store)
	for turns := 0; turns < 100; turns++ {
		j, e := f.store.Job(ctx, job)
		Expect(e).NotTo(HaveOccurred())
		if j.State == "completed" {
			return
		}
		Expect(w.Process(ctx, job)).To(Succeed())
	}
	Fail("sample index did not finish")
}
func corpusToken(f *fixture, scope domain.CredentialScope) string {
	token, e := sec.Secret()
	Expect(e).NotTo(HaveOccurred())
	id := key(GinkgoT())
	Expect(f.store.Write(ctx, func(tx app.WriteTx) error {
		state, e := tx.AuthState()
		if e != nil {
			return e
		}
		if e = tx.PutWorkflowPrincipal(domain.WorkflowPrincipal{ID: id, BindingID: id, Active: true, Scope: scope, Revision: 1}); e != nil {
			return e
		}
		config, e := tx.Configuration()
		if e != nil {
			return e
		}
		permissions := map[string]int64{}
		for _, p := range scope.ProjectIDs {
			x, e := tx.Scope(p)
			if e != nil {
				return e
			}
			permissions[p] = x.Project.PermissionsRevision
		}
		return tx.PutScopedCredential(domain.ScopedCredential{ID: id, PrincipalID: id, TokenHash: sec.Verifier("workflow", token), AuthEpoch: state.AuthEpoch, Scope: scope, Permissions: permissions, ConfigRevision: config.Revision, CreatedAt: time.Now().UTC().Truncate(time.Millisecond), Revision: 1})
	})).To(Succeed())
	return token
}

type corpusObservation struct {
	name, version string
	before, after query.SearchPage
}
type corpusFixture struct {
	f                                      *fixture
	allowed, deniedProject, scoped, denied string
	versions                               []string
	documents                              map[string]string
	byName                                 map[string]string
	observations                           []corpusObservation
	q                                      *query.Service
}

func corpusSetup(exhaustive bool) *corpusFixture {
	f := newFixture(GinkgoT())
	s := &corpusFixture{f: f, allowed: f.project, deniedProject: key(GinkgoT()), documents: map[string]string{}, byName: map[string]string{}}
	Expect(f.store.Write(ctx, func(tx app.WriteTx) error {
		return tx.CreateProject(domain.Project{ID: s.deniedProject, Name: "SC19 denied"})
	})).To(Succeed())
	for _, name := range []string{"mulgae", "gaori"} {
		f.project = s.allowed
		if !exhaustive && name == "gaori" {
			f.project = s.deniedProject
		}
		doc, version, job := corpusImport(f, name, "")
		s.documents[name] = doc
		s.byName[name] = version
		s.versions = append(s.versions, version)
		worker := search.NewWorker(f.store, f.store)
		partial := false
		for turns := 0; turns < 30; turns++ {
			Expect(worker.Process(ctx, job)).To(Succeed())
			j, e := f.store.Job(ctx, job)
			Expect(e).NotTo(HaveOccurred())
			var progress struct {
				Done  string `json:"pages_done"`
				Total string `json:"pages_total"`
			}
			Expect(json.Unmarshal(j.Result, &progress)).To(Succeed())
			if progress.Done == "1" && progress.Total == "2" {
				partial = true
				break
			}
		}
		Expect(partial).To(BeTrue())
		before, e := query.New(f.store, f.auth, nil).Search(ctx, f.token, query.SearchOptions{Text: "sc19-no-match-unique", Options: query.Options{Project: f.project}})
		Expect(e).NotTo(HaveOccurred())
		f.reopen(GinkgoT())
		corpusFinish(f, job)
		after, e := query.New(f.store, f.auth, nil).Search(ctx, f.token, query.SearchOptions{Text: "sc19-no-match-unique", Options: query.Options{Project: f.project}})
		Expect(e).NotTo(HaveOccurred())
		s.observations = append(s.observations, corpusObservation{name, version, before, after})
	}
	f.project = s.allowed
	readable := s.versions
	if !exhaustive {
		readable = []string{s.byName["mulgae"]}
	}
	s.scoped = corpusToken(f, domain.CredentialScope{Kind: "workflow", Actions: []string{"version.read"}, ProjectIDs: []string{s.allowed}, VersionIDs: readable})
	s.denied = corpusToken(f, domain.CredentialScope{Kind: "workflow", Actions: []string{"job.read"}, ProjectIDs: []string{s.allowed}, VersionIDs: readable})
	s.q = query.New(f.store, f.auth, nil)
	return s
}

func corpusSpecs(exhaustive bool) {
	var s *corpusFixture
	BeforeAll(func() { s = corpusSetup(exhaustive) })
	It("SC19 deterministic checkpoint interruption reports partial separately from zero then completes both sample trees", func() {
		Expect(s.observations).To(HaveLen(2))
		observations := []any{}
		for _, row := range s.observations {
			Expect(row.before.Items).To(BeEmpty())
			Expect(row.before.Index.State).To(BeElementOf("pending", "partial"))
			Expect(row.before.Index.HTMLPending).NotTo(Equal("0"))
			Expect(row.after.Items).To(BeEmpty())
			Expect(row.after.Index.State).To(Equal("complete"))
			Expect(row.after.Index.HTMLPending).To(Equal("0"))
			observations = append(observations, map[string]any{"sample": row.name, "version": row.version, "partial": row.before.Index, "complete": row.after.Index})
		}
		AddReportEntry("SC19_partial", ReportEntryVisibilityFailureOrVerbose, observations)
	})
	if exhaustive {
		It("SC19 named-project owner-default scoped and denied variants exclude F10 and trash sample canaries including cursor and events", func() { corpusMatrix(s) })
	} else {
		It("SC19 lean actual sample preserves scoped cursor and masks F10 trash bytes counts snippets and events", func() { corpusLean(s) })
	}
}

var _ = Describe("SC19 real sample seeded variants", Ordered, Label("persistence"), func() { corpusSpecs(false) })

// The manual corpus driver owns this exhaustive matrix; canonical layers select neither this label nor its specs.
var _ = Describe("SC19 real sample exhaustive variants", Ordered, Label("manual_sc19"), func() { corpusSpecs(true) })

func corpusMatrix(s *corpusFixture) {
	f, allowed, deniedProject, scoped, denied, q := s.f, s.allowed, s.deniedProject, s.scoped, s.denied, s.q
	options := query.SearchOptions{Text: "source", Options: query.Options{Project: allowed, Kind: "region", Limit: 2}}
	baseline := map[string]query.SearchPage{}
	queries := []string{"source"}
	worksheet, e := os.ReadFile("../../../docs/testing/search-relevance-worksheet.md")
	Expect(e).NotTo(HaveOccurred())
	table := strings.Split(strings.Split(string(worksheet), "### 9.3 Agent-predeclared real sample queries")[1], "Additional R checks:")[0]
	for _, line := range strings.Split(table, "\n") {
		if strings.HasPrefix(line, "| RC") {
			queries = append(queries, strings.TrimSpace(strings.Split(line, "|")[2]))
		}
	}
	Expect(queries).To(HaveLen(15))
	for _, reader := range []struct{ name, token string }{{"owner-default-named-project", f.token}, {"scoped", scoped}} {
		for _, text := range queries {
			o := options
			o.Text = text
			page, e := q.Search(ctx, reader.token, o)
			Expect(e).NotTo(HaveOccurred())
			baseline[reader.name+"|"+text] = page
		}
	}
	var epoch string
	var head int64
	Expect(f.store.Write(ctx, func(tx app.WriteTx) error { var e error; epoch, head, e = tx.EventHead(); return e })).To(Succeed())
	hiddenVersions := []string{}
	trashDocs := []string{}
	trashVersions := map[string]bool{}
	for _, name := range []string{"mulgae", "gaori"} {
		f.project = deniedProject
		_, v, j := corpusImport(f, name, "F10PrivateCanary")
		corpusFinish(f, j)
		hiddenVersions = append(hiddenVersions, v)
		f.project = allowed
		doc, v, j := corpusImport(f, name, "TrashPrivateCanary")
		corpusFinish(f, j)
		hiddenVersions = append(hiddenVersions, v)
		trashDocs = append(trashDocs, doc)
		trashVersions[v] = true
	}
	for _, control := range []struct{ project, term string }{{deniedProject, "F10PrivateCanary"}, {allowed, "TrashPrivateCanary"}} {
		positive, e := q.Search(ctx, f.token, query.SearchOptions{Text: control.term, Options: query.Options{Project: control.project}})
		Expect(e).NotTo(HaveOccurred())
		Expect(positive.Items).NotTo(BeEmpty())
	}
	Expect(f.store.Write(ctx, func(tx app.WriteTx) error {
		for _, doc := range trashDocs {
			if e := tx.TestTrashDocument(doc); e != nil {
				return e
			}
		}
		return nil
	})).To(Succeed())
	for _, reader := range []struct{ name, token string }{{"owner-default-named-project", f.token}, {"scoped", scoped}} {
		for _, text := range queries {
			o := options
			o.Text = text
			page, e := q.Search(ctx, reader.token, o)
			Expect(e).NotTo(HaveOccurred())
			Expect(page.TotalMatches).To(Equal(baseline[reader.name+"|"+text].TotalMatches))
			Expect(page.Index).To(Equal(baseline[reader.name+"|"+text].Index))
			Expect(page.Items).To(Equal(baseline[reader.name+"|"+text].Items))
			Expect(page.NextCursor).To(Equal(baseline[reader.name+"|"+text].NextCursor))
			raw, _ := json.Marshal(page)
			for _, v := range hiddenVersions {
				Expect(string(raw)).NotTo(ContainSubstring(v))
			}
			Expect(string(raw)).NotTo(ContainSubstring("PrivateCanary"))
			if page.NextCursor != nil {
				decoded, e := base64.RawURLEncoding.DecodeString(*page.NextCursor)
				Expect(e).NotTo(HaveOccurred())
				for _, v := range hiddenVersions {
					Expect(string(decoded)).NotTo(ContainSubstring(v))
				}
			}
		}
		for _, term := range []string{"F10PrivateCanary", "TrashPrivateCanary"} {
			hidden, e := q.Search(ctx, reader.token, query.SearchOptions{Text: term, Options: query.Options{Project: allowed}})
			Expect(e).NotTo(HaveOccurred())
			Expect(hidden.Items).To(BeEmpty())
			Expect(hidden.TotalMatches).To(Equal("0"))
		}
	}
	_, e = q.Search(ctx, denied, options)
	Expect(e).To(HaveOccurred())
	events := []domain.Event{}
	after := head
	for {
		page, e := f.store.Events(ctx, epoch, after, 64)
		Expect(e).NotTo(HaveOccurred())
		if len(page) == 0 {
			break
		}
		events = append(events, page...)
		after = page[len(page)-1].Sequence
	}
	seenCanaries := map[string]bool{}
	checked := 0
	for _, event := range events {
		for _, version := range hiddenVersions {
			if event.ResourceID == version {
				seenCanaries[version] = true
				readers := []string{scoped, denied}
				if trashVersions[version] {
					readers = append(readers, f.token)
				}
				for _, reader := range readers {
					visible, e := f.auth.FilterEvent(ctx, reader, event)
					Expect(e).NotTo(HaveOccurred())
					Expect(visible.ResourceID).To(BeEmpty())
					Expect(visible.Body).To(BeEmpty())
					Expect(visible.Kind).To(Equal("library.invalidated"))
					checked++
				}
			}
		}
	}
	Expect(checked).To(BeNumerically(">", 0))
	Expect(seenCanaries).To(HaveLen(len(hiddenVersions)))
	AddReportEntry("SC19_canaries", ReportEntryVisibilityFailureOrVerbose, map[string]any{"variants": 3, "sample_copies": 4, "queries_per_readable_variant": len(queries), "event_checks": checked, "scope": "owner default active/latest in named project; F10 project denied to workflows, outside owner named-project selection; lifecycle and issuance seeded"})
}

func corpusLean(s *corpusFixture) {
	f, q := s.f, s.q
	hidden := s.byName["gaori"]
	marker := "await_run" // An unchanged, Gaori-only literal in the real checked-in tree.
	options := query.SearchOptions{Text: "source", Options: query.Options{Kind: "region", Limit: 2}}
	readers := []struct{ name, token, project string }{{"owner-default-named-project", f.token, s.allowed}, {"scoped", s.scoped, ""}}
	baseline := map[string]query.SearchPage{}
	for _, reader := range readers {
		o := options
		o.Project = reader.project
		page, e := q.Search(ctx, reader.token, o)
		Expect(e).NotTo(HaveOccurred())
		Expect(page.NextCursor).NotTo(BeNil())
		Expect(page.TotalMatches).NotTo(Equal("0"))
		baseline[reader.name] = page
	}
	positive, e := q.Search(ctx, f.token, query.SearchOptions{Text: marker, Options: query.Options{Kind: "region"}})
	Expect(e).NotTo(HaveOccurred())
	Expect(positive.Items).NotTo(BeEmpty())
	Expect(positive.Items[0].VersionID).To(Equal(hidden))
	checkAbsent := func(token string) {
		page, e := q.Search(ctx, token, query.SearchOptions{Text: marker, Options: query.Options{Kind: "region"}})
		Expect(e).NotTo(HaveOccurred())
		Expect(page.Items).To(BeEmpty())
		Expect(page.TotalMatches).To(Equal("0"))
		Expect(page.NextCursor).To(BeNil())
		raw, _ := json.Marshal(page)
		Expect(string(raw)).NotTo(ContainSubstring(hidden))
		// The response echoes the caller's query; only returned content is protected.
		items, _ := json.Marshal(page.Items)
		Expect(string(items)).NotTo(ContainSubstring(marker))
	}
	checkAbsent(s.scoped) // F10 project denied, without a project filter masking the authorization check.
	Expect(f.store.Write(ctx, func(tx app.WriteTx) error { return tx.TestTrashDocument(s.documents["gaori"]) })).To(Succeed())
	checkAbsent(f.token) // The same actual indexed sample is now hidden by default active lifecycle.
	checkAbsent(s.scoped)
	for _, reader := range readers {
		o := options
		o.Project = reader.project
		page, e := q.Search(ctx, reader.token, o)
		Expect(e).NotTo(HaveOccurred())
		Expect(page.TotalMatches).To(Equal(baseline[reader.name].TotalMatches))
		Expect(page.Index).To(Equal(baseline[reader.name].Index))
		Expect(page.Items).To(Equal(baseline[reader.name].Items))
		Expect(page.NextCursor).To(Equal(baseline[reader.name].NextCursor))
		raw, _ := json.Marshal(page)
		Expect(string(raw)).NotTo(ContainSubstring(hidden))
		Expect(string(raw)).NotTo(ContainSubstring(marker))
		payload, e := base64.RawURLEncoding.DecodeString(*page.NextCursor)
		Expect(e).NotTo(HaveOccurred())
		Expect(string(payload)).NotTo(ContainSubstring(hidden))
	}
	_, e = q.Search(ctx, s.denied, options)
	Expect(e).To(HaveOccurred())
	var epoch string
	Expect(f.store.Write(ctx, func(tx app.WriteTx) error { var e error; epoch, _, e = tx.EventHead(); return e })).To(Succeed())
	checked := 0
	var after int64
	for {
		events, e := f.store.Events(ctx, epoch, after, 64)
		Expect(e).NotTo(HaveOccurred())
		if len(events) == 0 {
			break
		}
		after = events[len(events)-1].Sequence
		for _, event := range events {
			if event.ResourceID != hidden {
				continue
			}
			for _, token := range []string{f.token, s.scoped, s.denied} {
				visible, e := f.auth.FilterEvent(ctx, token, event)
				Expect(e).NotTo(HaveOccurred())
				Expect(visible.ResourceID).To(BeEmpty())
				Expect(visible.Body).To(BeEmpty())
				Expect(visible.Kind).To(Equal("library.invalidated"))
				checked++
			}
		}
	}
	Expect(checked).To(BeNumerically(">", 0))
	AddReportEntry("SC19_lean", ReportEntryVisibilityFailureOrVerbose, map[string]any{"imports": 2, "variants": 3, "event_checks": checked, "scope": "actual Gaori sample as F10 then trash; full four-copy/15-query matrix is manual_sc19"})
}
