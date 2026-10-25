package query_test

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"haeryong.local/haeryong/internal/adapters/content/staging"
	"haeryong.local/haeryong/internal/adapters/foundation"
	auth "haeryong.local/haeryong/internal/application/auth"
	importer "haeryong.local/haeryong/internal/application/import"
	"haeryong.local/haeryong/internal/application/query"
	"haeryong.local/haeryong/internal/application/service"
	"haeryong.local/haeryong/internal/domain"
	_ "modernc.org/sqlite"
)

var ctx = context.Background()

type fixture struct {
	store              *foundation.Store
	core               *importer.Service
	auth               *auth.Service
	queries            *query.Service
	token, project     string
	document, version  string
	dbPath, profileDir string
}

func newFixture(t GinkgoTInterface) *fixture {
	GinkgoHelper()
	selection := service.Selection{Profile: "test", DataDir: filepath.Join(t.TempDir(), "library")}
	_, e := service.Initialize(selection)
	Expect(e != nil).To(BeFalse())
	w, e := service.OpenWriter(selection)
	Expect(e != nil).To(BeFalse())
	DeferCleanup(func() { w.Close() })
	Expect(w.PrepareStorageBootstrap(nil) != nil).To(BeFalse())
	f := &fixture{dbPath: filepath.Join(selection.DataDir, "storage", "library.sqlite"), profileDir: selection.DataDir}
	f.store = foundation.New(nil)
	_, closeStore, e := f.store.Prepare(ctx, w.Profile)
	Expect(e != nil).To(BeFalse())
	DeferCleanup(func() { closeStore() })
	Expect(w.MarkStorageReady(nil) != nil).To(BeFalse())
	f.auth = auth.New(f.store)
	f.core = importer.New(f.store, f.auth, f.store, staging.New())
	f.queries = query.New(f.store, f.auth, f.store)
	h, e := f.store.Health(ctx)
	Expect(e != nil).To(BeFalse())
	f.project = h.Library.UnfiledProjectID
	p, e := f.auth.CreatePairing(ctx, "http://127.0.0.1:9270", "personal fixture", "test")
	Expect(e != nil).To(BeFalse())
	_, e = f.auth.BootstrapDecide(ctx, domain.PairingDecision{ID: p.ID, Code: p.Code, Origin: p.Origin, ETag: p.ETag, OperationID: key(t), Action: "approve"})
	Expect(e != nil).To(BeFalse())
	poll, e := f.auth.Poll(ctx, p.ID, p.PollingSecret, p.Origin)
	Expect(e != nil).To(BeFalse())
	f.token = poll.Token
	// Publish a version with an index page, a nested page, an NFC-equivalent
	// name and an in-version alias so canonical identity is observable.
	f.publishVersion(t, map[string]string{
		"hello.html":        "<html>main</html>",
		"pages/nested.html": "<html>nested</html>",
		"café.html":         "<html>unicode</html>",
	}, "alias.html", "hello.html")
	return f
}

func key(t GinkgoTInterface) string {
	id, e := service.NewID()
	Expect(e != nil).To(BeFalse())
	return id
}

func (f *fixture) publishVersion(t GinkgoTInterface, files map[string]string, alias, target string, metadata ...string) {
	GinkgoHelper()
	doc := (*string)(nil)
	targetJSON, _ := json.Marshal(map[string]any{"project_id": f.project, "document_id": doc})
	r, e := f.core.Create(ctx, f.token, key(t), importer.CreateRequest{Purpose: "result", InputMode: "directory", Target: targetJSON})
	Expect(e != nil).To(BeFalse())
	var created struct {
		ID string `json:"upload_id"`
	}
	_ = json.Unmarshal(r.Body, &created)
	var ack importer.ChunkResult
	for path, content := range files {
		length := int64(len(content))
		ack, e = f.core.Put(ctx, f.token, created.ID, importer.Chunk{Path: path, Kind: "file", Hash: hash(content), Length: &length, Final: true}, strings.NewReader(content))
		Expect(e != nil).To(BeFalse())
	}
	ack, e = f.core.Put(ctx, f.token, created.ID, importer.Chunk{Path: alias, Kind: "symlink", LinkTarget: target}, strings.NewReader(`{"link_target":"`+target+`"}`))
	Expect(e != nil).To(BeFalse())
	cp := ack.Checkpoint
	cp.Generation++
	cp.Complete = true
	r, e = f.core.Complete(ctx, f.token, created.ID, key(t), cp)
	Expect(e != nil).To(BeFalse())
	var envelope struct {
		JobID string `json:"job_id"`
	}
	_ = json.Unmarshal(r.Body, &envelope)
	Expect(f.core.Process(ctx, envelope.JobID) != nil).To(BeFalse())
	version := key(t)
	input := importer.ImportRequest{UploadID: created.ID, DocumentPresent: true, VersionID: version}
	if len(metadata) > 0 {
		input.Metadata = strings.NewReader(metadata[0])
	}
	importResponse, e := f.core.Import(ctx, f.token, key(t), input)
	Expect(e != nil).To(BeFalse())
	var imported struct {
		JobID string `json:"job_id"`
	}
	_ = json.Unmarshal(importResponse.Body, &imported)
	Expect(f.core.Process(ctx, imported.JobID) != nil).To(BeFalse())
	j, e := f.store.Job(ctx, imported.JobID)
	Expect(e != nil).To(BeFalse())
	var result struct {
		DocumentID string `json:"document_id"`
		VersionID  string `json:"version_id"`
	}
	Expect(json.Unmarshal(j.Result, &result) != nil).To(BeFalse())
	f.document, f.version = result.DocumentID, result.VersionID
}

func hash(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

func (f *fixture) put(t GinkgoTInterface, kind query.PersonalKind, page string, etag query.PersonalView, create bool, body string) (query.PersonalView, error) {
	etagValue := ""
	if etag.ETag != nil {
		etagValue = *etag.ETag
	}
	response, e := f.queries.PersonalPut(ctx, f.token, key(t), f.version, kind, page, etagValue, create, []byte(body))
	if e != nil {
		return query.PersonalView{}, e
	}
	var view query.PersonalView
	_ = json.Unmarshal(response.Body, &view)
	return view, nil
}

var _ = It("TestPersonalResourcesIndependentValidatorsAndCanonicalPages", Label("persistence"), func() {
	t := GinkgoT()
	f := newFixture(t)

	// Create the three families; validators stay fully distinct.
	pref, e := f.put(t, query.ReaderPreference, "", query.PersonalView{}, true, `{"default_page":"hello.html"}`)
	Expect(e != nil).To(BeFalse(), func() string { return fmt.Sprint(e) })
	pointer, e := f.put(t, query.ReadingPosition, "", query.PersonalView{}, true, `{"page_path":"pages/nested.html","page_query":"step=2","page_fragment":"f","opened":true}`)
	Expect(e != nil).To(BeFalse(), func() string { return fmt.Sprint(e) })
	pageA, e := f.put(t, query.PagePosition, "hello.html", query.PersonalView{}, true, `{"page_query":"step=2","page_fragment":"f","position":{"top":120}}`)
	Expect(e != nil).To(BeFalse(), func() string { return fmt.Sprint(e) })
	pageB, e := f.put(t, query.PagePosition, "pages/nested.html", query.PersonalView{}, true, `{"page_query":"","page_fragment":"","position":{"top":0}}`)
	Expect(e != nil).To(BeFalse(), func() string { return fmt.Sprint(e) })
	Expect(pageA.Revision == nil || pageB.Revision == nil || *pageA.Revision != *pageB.Revision).To(BeFalse(), func() string { return "same first revision expected" })
	// Same revision across pages still cannot cross-validate.
	_, e = f.put(t, query.PagePosition, "pages/nested.html", pageA, false, `{"page_query":"","page_fragment":"","position":{"top":5}}`)
	Expect(errors.Is(e, domain.ErrPrecondition)).To(BeTrue(), func() string { return fmt.Sprint("page A etag validated page B: ", e) })
	_, e = f.put(t, query.ReadingPosition, "", pageA, false, `{"page_path":null,"page_query":"","page_fragment":"","opened":true}`)
	Expect(errors.Is(e, domain.ErrPrecondition)).To(BeTrue(), func() string { return fmt.Sprint("page etag validated pointer: ", e) })
	_, e = f.put(t, query.ReaderPreference, "", pointer, false, `{"default_page":null}`)
	Expect(errors.Is(e, domain.ErrPrecondition)).To(BeTrue(), func() string { return fmt.Sprint("pointer etag validated preference: ", e) })

	// Alias and NFC-equivalent spellings address the SAME canonical resource.
	_, e = f.put(t, query.PagePosition, "alias.html", query.PersonalView{}, true, `{"page_query":"a=1","page_fragment":"","position":{"top":9}}`)
	Expect(errors.Is(e, domain.ErrPrecondition)).To(BeTrue(), func() string { return fmt.Sprint("alias create on existing canonical resource: ", e) })
	viaNFD, e := f.put(t, query.PagePosition, "cafe\u0301.html", query.PersonalView{}, true, `{"page_query":"u=1","page_fragment":"","position":{"top":7}}`)
	Expect(e != nil).To(BeFalse(), func() string { return fmt.Sprint("NFD page spelling rejected: ", e) })
	viaNFC, e := f.queries.PersonalGet(ctx, f.token, f.version, query.PagePosition, "café.html")
	Expect(e != nil).To(BeFalse(), func() string { return fmt.Sprint(e) })
	Expect(viaNFC.Exists && viaNFC.ETag != nil && viaNFD.ETag != nil && *viaNFC.ETag == *viaNFD.ETag).To(BeTrue(), func() string { return "NFC/NFD spellings diverged" })

	// Update with the exact opaque validator; stale and create-on-exist fail.
	updated, e := f.put(t, query.PagePosition, "hello.html", pageA, false, `{"page_query":"step=3","page_fragment":"f","position":{"top":240}}`)
	Expect(e != nil).To(BeFalse(), func() string { return fmt.Sprint(e) })
	Expect(updated.Revision == nil || *updated.Revision <= *pageA.Revision).To(BeFalse())
	_, e = f.put(t, query.PagePosition, "hello.html", pageA, false, `{"page_query":"x","page_fragment":"","position":{}}`)
	Expect(errors.Is(e, domain.ErrPrecondition)).To(BeTrue(), func() string { return fmt.Sprint("stale etag accepted: ", e) })
	_, e = f.put(t, query.ReaderPreference, "", query.PersonalView{}, true, `{"default_page":null}`)
	Expect(errors.Is(e, domain.ErrPrecondition)).To(BeTrue(), func() string { return fmt.Sprint("create-on-exist accepted: ", e) })
	_, e = f.put(t, query.ReadingPosition, "", query.PersonalView{ETag: strPtr("")}, false, `{"page_path":null,"page_query":"","page_fragment":"","opened":false}`)
	Expect(errors.Is(e, domain.ErrPrecondition)).To(BeTrue(), func() string { return fmt.Sprint("update-on-absent accepted: ", e) })

	// Typed control bodies reject null booleans, wrong shapes and duplicates.
	_, e = f.put(t, query.ReadingPosition, "", query.PersonalView{}, true, `{"page_path":null,"page_query":"","page_fragment":"","opened":null}`)
	var invalid *query.Error
	Expect(errors.As(e, &invalid) && invalid.Reason == "invalid_body").To(BeTrue(), func() string { return fmt.Sprint("null opened accepted: ", e) })
	_, e = f.put(t, query.PagePosition, "hello.html", query.PersonalView{}, true, `{"page_query":"","page_fragment":"","position":[1]}`)
	Expect(errors.As(e, &invalid)).To(BeTrue(), func() string { return fmt.Sprint("array position accepted: ", e) })
	_, e = f.put(t, query.ReaderPreference, "", query.PersonalView{}, true, `{"default_page":"missing.html"}`)
	Expect(errors.Is(e, domain.ErrNotFound)).To(BeTrue(), func() string { return fmt.Sprint("unknown preference page accepted: ", e) })
	_, e = f.put(t, query.PagePosition, "assets/app.js", query.PersonalView{}, true, `{"page_query":"","page_fragment":"","position":{}}`)
	var missing *query.Missing
	Expect(errors.Is(e, domain.ErrNotFound) || errors.As(e, &missing)).To(BeTrue(), func() string { return fmt.Sprint("non-HTML page accepted: ", e) })

	// Absent GET returns the create-precondition shape; unknown versions 404.
	other, e := f.queries.PersonalGet(ctx, f.token, key(t), query.ReaderPreference, "")
	Expect(errors.Is(e, domain.ErrNotFound)).To(BeTrue(), func() string { return fmt.Sprint(e) })
	_ = other

	// Replay keeps one receipt per exact intent; changed intent conflicts.
	operation := key(t)
	first, e := f.queries.PersonalPut(ctx, f.token, operation, f.version, query.ReaderPreference, "", *pref.ETag, false, []byte(`{"default_page":"pages/nested.html"}`))
	Expect(e != nil).To(BeFalse(), func() string { return fmt.Sprint(e) })
	replay, e := f.queries.PersonalPut(ctx, f.token, operation, f.version, query.ReaderPreference, "", *pref.ETag, false, []byte(`{"default_page":"pages/nested.html"}`))
	Expect(e != nil).To(BeFalse(), func() string { return fmt.Sprint(e) })
	Expect(string(replay.Body) != string(first.Body)).To(BeFalse(), func() string { return "replay body differs" })
	_, e = f.queries.PersonalPut(ctx, f.token, operation, f.version, query.ReaderPreference, "", *pref.ETag, false, []byte(`{"default_page":"hello.html"}`))
	Expect(errors.Is(e, domain.ErrConflict)).To(BeTrue(), func() string { return fmt.Sprint("changed intent replayed: ", e) })

	// Trash blocks writes with an explicit lifecycle transition.
	f.execSQL("UPDATE documents SET lifecycle='trash' WHERE document_id='" + f.document + "'")
	_, e = f.put(t, query.ReadingPosition, "", query.PersonalView{}, true, `{"page_path":null,"page_query":"","page_fragment":"","opened":true}`)
	var lifecycle *query.Transition
	Expect(errors.As(e, &lifecycle) && lifecycle.Lifecycle == "trash").To(BeTrue(), func() string { return fmt.Sprint("trash transition: ", e) })
	// A saved PUT request must not exempt later GETs or replays from current
	// document visibility, even when its original owner is still authenticated.
	_, e = f.queries.PersonalGet(ctx, f.token, f.version, query.ReaderPreference, "")
	Expect(errors.Is(e, domain.ErrNotFound)).To(BeTrue(), func() string { return fmt.Sprint("trash personal read disclosed: ", e) })
	_, e = f.auth.Operation(ctx, f.token, operation)
	Expect(errors.Is(e, domain.ErrNotFound)).To(BeTrue(), func() string { return fmt.Sprint("trash personal receipt disclosed: ", e) })
	_, e = f.queries.PersonalPut(ctx, f.token, operation, f.version, query.ReaderPreference, "", *pref.ETag, false, []byte(`{"default_page":"pages/nested.html"}`))
	Expect(errors.Is(e, domain.ErrNotFound)).To(BeTrue(), func() string { return fmt.Sprint("trash personal replay disclosed: ", e) })
})

func (f *fixture) execSQL(statement string) {
	db, e := sqlOpenRW(f.dbPath)
	Expect(e != nil).To(BeFalse())
	defer db.Close()
	_, e = db.ExecContext(context.Background(), statement)
	Expect(e != nil).To(BeFalse(), func() string { return fmt.Sprint(statement, ": ", e) })
}

func sqlOpenRW(path string) (*sql.DB, error) { return sql.Open("sqlite", "file:"+path+"?mode=rw") }

// C2 regressions: registry absent-wire defaults with explicit null
// revision/etag keys, and the pointer's selected page-position link.
var _ = It("TestPersonalAbsentWireDefaultsAndPointerLink", Label("persistence"), func() {
	t := GinkgoT()
	f := newFixture(t)
	view, e := f.queries.PersonalGet(ctx, f.token, f.version, query.ReaderPreference, "")
	Expect(e != nil).To(BeFalse(), func() string { return fmt.Sprint(e) })
	encoded, _ := json.Marshal(view)
	Expect(string(encoded)).To(MatchRegexp(`"exists":false`), func() string { return string(encoded) })
	Expect(string(encoded)).To(ContainSubstring(`"default_page":null`), func() string { return string(encoded) })
	Expect(string(encoded)).To(ContainSubstring(`"revision":null`), func() string { return string(encoded) })
	Expect(string(encoded)).To(ContainSubstring(`"etag":null`), func() string { return string(encoded) })
	Expect(string(encoded)).To(ContainSubstring(`"required_precondition":"if_none_match_star"`), func() string { return string(encoded) })
	pointer, e := f.queries.PersonalGet(ctx, f.token, f.version, query.ReadingPosition, "")
	Expect(e != nil).To(BeFalse(), func() string { return fmt.Sprint(e) })
	pointerJSON, _ := json.Marshal(pointer)
	Expect(string(pointerJSON)).To(ContainSubstring(`"page_path":null,"page_query":"","page_fragment":"","opened":false,"first_opened_at":null,"last_opened_at":null`), func() string { return string(pointerJSON) })
	Expect(string(pointerJSON)).To(ContainSubstring(`"page_position_url":null`), func() string { return string(pointerJSON) })
	// A page-bound pointer carries the once-encoded canonical page link.
	created, e := f.put(t, query.ReadingPosition, "", query.PersonalView{}, true, `{"page_path":"pages/nested.html","page_query":"s=1","page_fragment":"f","opened":true}`)
	Expect(e != nil).To(BeFalse(), func() string { return fmt.Sprint(e) })
	createdJSON, _ := json.Marshal(created)
	Expect(string(createdJSON)).To(ContainSubstring(`"page_position_url":"/api/v1/versions/`+f.version+`/page-position?page=pages%2Fnested.html"`), func() string { return string(createdJSON) })
	fresh, e := f.queries.PersonalGet(ctx, f.token, f.version, query.ReadingPosition, "")
	Expect(e != nil).To(BeFalse(), func() string { return fmt.Sprint(e) })
	freshJSON, _ := json.Marshal(fresh)
	Expect(string(freshJSON)).To(ContainSubstring(`"page_position_url":"/api/v1/versions/`+f.version+`/page-position?page=pages%2Fnested.html"`), func() string { return string(freshJSON) })
	// Whole-version updates keep the explicit null link, never an omitted key.
	whole, e := f.put(t, query.ReadingPosition, "", created, false, `{"page_path":null,"page_query":"","page_fragment":"","opened":true}`)
	Expect(e != nil).To(BeFalse(), func() string { return fmt.Sprint(e) })
	wholeJSON, _ := json.Marshal(whole)
	Expect(string(wholeJSON)).To(ContainSubstring(`"page_position_url":null`), func() string { return string(wholeJSON) })
})

func strPtr(v string) *string { return &v }

// C4: storing each family's exact default is a real durable write: exists
// with a real revision/etag, no create precondition, and a second save under
// the returned validator advances the revision. The null-page pointer keeps
// an explicit null page-position link.
var _ = It("TestPersonalStoredDefaultsRoundTripWithRealValidators", Label("persistence"), func() {
	t := GinkgoT()
	f := newFixture(t)

	prefDefault := `{"default_page":null}`
	storedPref, e := f.put(t, query.ReaderPreference, "", query.PersonalView{}, true, prefDefault)
	Expect(e != nil).To(BeFalse(), func() string { return fmt.Sprint(e) })
	Expect(storedPref.Exists && storedPref.Revision != nil && *storedPref.Revision >= 1 && storedPref.ETag != nil && *storedPref.ETag != "").To(BeTrue(), func() string { return "stored preference default lacks real validators" })
	Expect(storedPref.RequiredPrecondition != "").To(BeFalse(), func() string { return "stored state carries a create precondition" })
	Expect(storedPref.PagePositionURL != nil).To(BeFalse(), func() string { return "preference links no page-position resource" })
	again, e := f.put(t, query.ReaderPreference, "", storedPref, false, prefDefault)
	Expect(e != nil).To(BeFalse(), func() string { return fmt.Sprint(e) })
	Expect(again.Revision == nil || *again.Revision <= *storedPref.Revision).To(BeFalse(), func() string { return "default re-save did not advance the revision" })

	pointerDefault := `{"page_path":null,"page_query":"","page_fragment":"","opened":false}`
	storedPointer, e := f.put(t, query.ReadingPosition, "", query.PersonalView{}, true, pointerDefault)
	Expect(e != nil).To(BeFalse(), func() string { return fmt.Sprint(e) })
	Expect(storedPointer.Exists && storedPointer.Revision != nil && storedPointer.ETag != nil && *storedPointer.ETag != "").To(BeTrue(), func() string { return "stored pointer default lacks real validators" })
	Expect(storedPointer.RequiredPrecondition != "").To(BeFalse(), func() string { return "stored pointer carries a create precondition" })
	Expect(storedPointer.PagePositionURL != nil).To(BeFalse(), func() string { return "null-page pointer must keep an explicit null link" })

	pageDefault := `{"page_query":"","page_fragment":"","position":{}}`
	storedPage, e := f.put(t, query.PagePosition, "hello.html", query.PersonalView{}, true, pageDefault)
	Expect(e != nil).To(BeFalse(), func() string { return fmt.Sprint(e) })
	Expect(storedPage.Exists && storedPage.Revision != nil && storedPage.ETag != nil && *storedPage.ETag != "").To(BeTrue(), func() string { return "stored page default lacks real validators" })
	Expect(storedPage.RequiredPrecondition != "").To(BeFalse(), func() string { return "stored page state carries a create precondition" })
	Expect(storedPage.PagePositionURL != nil).To(BeFalse(), func() string { return "page-position links no resource" })

	// F02: PersonalGet returns the complete stored state for every family.
	// Every GET is placed IMMEDIATELY after the PUT it verifies so the
	// comparison is exact: full value bytes, exact revision, and the SAME
	// opaque ETag the PUT returned. Pointer GET comes before the withPage
	// update; preference GET compares against the SECOND PUT (`again`).
	gotPref, e := f.queries.PersonalGet(ctx, f.token, f.version, query.ReaderPreference, "")
	Expect(e != nil).To(BeFalse(), func() string { return fmt.Sprint("GET preference: ", e) })
	Expect(gotPref.Exists).To(BeTrue(), "GET preference must exist")
	Expect(gotPref.Revision).NotTo(BeNil(), "GET preference revision present")
	Expect(*gotPref.Revision).To(Equal(*again.Revision), "GET preference revision equals PUT revision")
	Expect(gotPref.ETag).NotTo(BeNil(), "GET preference etag present")
	Expect(*gotPref.ETag).To(Equal(*again.ETag), "GET preference etag equals PUT etag")
	Expect(gotPref.RequiredPrecondition).To(Equal(""), "GET preference must not carry create precondition")
	Expect(gotPref.PagePositionURL).To(BeNil(), "GET preference links nothing")
	Expect(string(gotPref.Value)).To(Equal(`{"default_page":null}`), "GET preference value exact")

	gotPointer, e := f.queries.PersonalGet(ctx, f.token, f.version, query.ReadingPosition, "")
	Expect(e != nil).To(BeFalse(), func() string { return fmt.Sprint("GET pointer: ", e) })
	Expect(gotPointer.Exists).To(BeTrue(), "GET pointer must exist")
	Expect(gotPointer.Revision).NotTo(BeNil(), "GET pointer revision present")
	Expect(*gotPointer.Revision).To(Equal(*storedPointer.Revision), "GET pointer revision equals PUT revision")
	Expect(gotPointer.ETag).NotTo(BeNil(), "GET pointer etag present")
	Expect(*gotPointer.ETag).To(Equal(*storedPointer.ETag), "GET pointer etag equals PUT etag")
	Expect(gotPointer.RequiredPrecondition).To(Equal(""), "GET pointer no create precondition")
	Expect(gotPointer.PagePositionURL).To(BeNil(), "GET pointer null link before withPage update")
	Expect(string(gotPointer.Value)).To(Equal(string(storedPointer.Value)), "GET pointer value equals PUT value exactly")

	gotPage, e := f.queries.PersonalGet(ctx, f.token, f.version, query.PagePosition, "hello.html")
	Expect(e != nil).To(BeFalse(), func() string { return fmt.Sprint("GET page-position: ", e) })
	Expect(gotPage.Exists).To(BeTrue(), "GET page-position must exist")
	Expect(gotPage.Revision).NotTo(BeNil(), "GET page-position revision present")
	Expect(*gotPage.Revision).To(Equal(*storedPage.Revision), "GET page-position revision equals PUT revision")
	Expect(gotPage.ETag).NotTo(BeNil(), "GET page-position etag present")
	Expect(*gotPage.ETag).To(Equal(*storedPage.ETag), "GET page-position etag equals PUT etag")
	Expect(gotPage.RequiredPrecondition).To(Equal(""), "GET page-position no create precondition")
	Expect(gotPage.PagePositionURL).To(BeNil(), "GET page-position links nothing")
	Expect(string(gotPage.Value)).To(Equal(string(storedPage.Value)), "GET page-position value equals PUT value exactly")

	// A stored pointer WITH a page links that page's position resource.
	withPage, e := f.put(t, query.ReadingPosition, "", storedPointer, false, `{"page_path":"hello.html","page_query":"","page_fragment":"","opened":true}`)
	Expect(e != nil).To(BeFalse(), func() string { return fmt.Sprint(e) })
	Expect(withPage.PagePositionURL == nil || *withPage.PagePositionURL == "").To(BeFalse(), func() string { return "stored pointer with a page lost its page-position link" })

	// Stored pointer with page: GET returns the withPage PUT's exact state.
	gotWithPage, e := f.queries.PersonalGet(ctx, f.token, f.version, query.ReadingPosition, "")
	Expect(e != nil).To(BeFalse(), func() string { return fmt.Sprint("GET with-page pointer: ", e) })
	Expect(gotWithPage.Exists).To(BeTrue(), "GET with-page pointer must exist")
	Expect(gotWithPage.Revision).NotTo(BeNil(), "GET with-page revision present")
	Expect(*gotWithPage.Revision).To(Equal(*withPage.Revision), "GET with-page revision equals PUT revision")
	Expect(gotWithPage.ETag).NotTo(BeNil(), "GET with-page etag present")
	Expect(*gotWithPage.ETag).To(Equal(*withPage.ETag), "GET with-page etag equals PUT etag")
	Expect(gotWithPage.PagePositionURL).NotTo(BeNil(), "GET with-page pointer has a link")
	Expect(*gotWithPage.PagePositionURL).To(ContainSubstring("/page-position?page="), "GET with-page link format")
	Expect(string(gotWithPage.Value)).To(Equal(string(withPage.Value)), "GET with-page value equals PUT value exactly")
})
