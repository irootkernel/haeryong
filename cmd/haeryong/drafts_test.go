package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"haeryong.local/haeryong/internal/adapters/client"
	"haeryong.local/haeryong/internal/adapters/clientstate"
	importer "haeryong.local/haeryong/internal/application/import"
	"haeryong.local/haeryong/internal/application/service"
)

var _ = It("GCLI05 draft CLI retains whole replacement, exact validators and physical lost-response replay without reopening removed input or reexecuting generation", Label("persistence"), func() {
	t := GinkgoT()
	ctx := context.Background()
	s := selection(t)
	initProfile(t, s)
	var dropped atomic.Bool
	var patches atomic.Int32
	daemon := startGenerationCLIApplication(t, s, false, func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method == "PATCH" && strings.HasPrefix(r.URL.Path, "/api/v1/drafts/") {
				patches.Add(1)
			}
			if r.Method == "PATCH" && strings.HasPrefix(r.URL.Path, "/api/v1/drafts/") && dropped.CompareAndSwap(false, true) {
				// Serve through the actual HTTP/application/store boundary, commit first,
				// then close the actual client TCP connection before writing its response.
				captured := httptest.NewRecorder()
				next.ServeHTTP(captured, r)
				Expect(captured.Code).To(Equal(200))
				conn, _, e := w.(http.Hijacker).Hijack()
				Expect(e).NotTo(HaveOccurred())
				Expect(conn.Close()).To(Succeed())
				return
			}
			next.ServeHTTP(w, r)
		})
	})
	owner, token := bootstrapOwnerCLI(t, s, daemon.id.Endpoint, "draft CLI owner")
	root, flags := callerProfile(t, s, owner, token, daemon.id.Endpoint)
	flags = append(flags, "--data-dir", s.DataDir)
	api, e := client.New(daemon.id.Endpoint, s.Profile, client.Credential{Endpoint: daemon.id.Endpoint, Profile: s.Profile, LibraryID: owner.LibraryID, Token: token})
	Expect(e).NotTo(HaveOccurred())
	identity, e := api.Identity(ctx)
	Expect(e).NotTo(HaveOccurred())
	input := filepath.Join(t.TempDir(), "request.json")
	generationCLIInput(input, identity.UnfiledProjectID)
	generated := cli(t, 0, append([]string{"generate", "--request-file", input}, flags...)...)
	var accepted struct {
		Data struct {
			Result struct {
				DraftID string `json:"draft_id"`
			} `json:"result"`
		}
	}
	Expect(json.Unmarshal(generated, &accepted)).To(Succeed())
	id := accepted.Data.Result.DraftID
	Expect(id).NotTo(BeEmpty())
	cli(t, 0, append([]string{"drafts", "list"}, flags...)...)
	cli(t, 0, append([]string{"drafts", "show", id}, flags...)...)
	var before importer.DraftView
	_, e = api.JSON(ctx, "GET", "/api/v1/drafts/"+id, "", nil, &before)
	Expect(e).NotTo(HaveOccurred())
	tree := filepath.Join(t.TempDir(), "replacement")
	Expect(os.MkdirAll(filepath.Join(tree, "empty"), 0700)).To(Succeed())
	Expect(os.MkdirAll(filepath.Join(tree, "nested"), 0700)).To(Succeed())
	Expect(os.WriteFile(filepath.Join(tree, "read.html"), []byte(`<a href="nested/next.html">next</a><script src="behavior.js"></script>`), 0600)).To(Succeed())
	Expect(os.WriteFile(filepath.Join(tree, "nested/next.html"), []byte("<h1>replacement</h1>"), 0600)).To(Succeed())
	Expect(os.WriteFile(filepath.Join(tree, "behavior.js"), []byte("document.body.dataset.ready='yes'"), 0600)).To(Succeed())
	Expect(os.WriteFile(filepath.Join(tree, "attachment.bin"), []byte{0, 255, 1, 3}, 0600)).To(Succeed())
	Expect(os.Symlink("nested/next.html", filepath.Join(tree, "alias.html"))).To(Succeed())
	key, _ := service.NewID()
	cli(t, 0, append([]string{"drafts", "replace", id, "--input", tree, "--operation-id", key}, flags...)...)
	Expect(dropped.Load()).To(BeTrue())
	var after importer.DraftView
	_, e = api.JSON(ctx, "GET", "/api/v1/drafts/"+id, "", nil, &after)
	Expect(e).NotTo(HaveOccurred())
	Expect(after.Revision).To(Equal(before.Revision + 1))
	Expect(after.PayloadSHA).NotTo(Equal(before.PayloadSHA))
	state, e := clientstate.New(root)
	Expect(e).NotTo(HaveOccurred())
	journal, e := state.Load(key)
	Expect(e).NotTo(HaveOccurred())
	Expect(journal.Result.UploadID).NotTo(BeEmpty())
	Expect(journal.Preconditions["If-Match"]).To(Equal(before.ETag))
	beforeReplay := patches.Load()
	Expect(beforeReplay).To(BeNumerically(">=", 1))
	Expect(os.RemoveAll(tree)).To(Succeed())
	cli(t, 0, append([]string{"drafts", "replace", id, "--input", tree, "--operation-id", key}, flags...)...)
	var replay importer.DraftView
	_, e = api.JSON(ctx, "GET", "/api/v1/drafts/"+id, "", nil, &replay)
	Expect(e).NotTo(HaveOccurred())
	Expect(replay).To(Equal(after))
	Expect(patches.Load()).To(Equal(beforeReplay))
	cli(t, 0, append([]string{"drafts", "history", id}, flags...)...)
	cli(t, 4, append([]string{"drafts", "publish", id, "--if-match", before.ETag}, flags...)...)
	publish, _ := service.NewID()
	cli(t, 0, append([]string{"drafts", "publish", id, "--operation-id", publish}, flags...)...)
	cli(t, 0, append([]string{"drafts", "publish", id, "--operation-id", publish}, flags...)...)
	var published importer.DraftView
	_, e = api.JSON(ctx, "GET", "/api/v1/drafts/"+id, "", nil, &published)
	Expect(e).NotTo(HaveOccurred())
	Expect(published.State).To(Equal("published"))
	Expect(published.PublishedVersionID).NotTo(BeNil())
	var files struct{ Items []struct{ Path, Kind string } }
	_, e = api.JSON(ctx, "GET", "/api/v1/versions/"+*published.PublishedVersionID+"/files", "", nil, &files)
	Expect(e).NotTo(HaveOccurred())
	paths := map[string]string{}
	for _, file := range files.Items {
		paths[file.Path] = file.Kind
	}
	Expect(paths).To(HaveKeyWithValue("empty", "directory"))
	Expect(paths).To(HaveKeyWithValue("alias.html", "symlink"))
	Expect(paths).To(HaveKeyWithValue("behavior.js", "file"))
	Expect(paths).To(HaveKeyWithValue("attachment.bin", "file"))
	Expect(daemon.driver.calls.Load()).To(Equal(int32(1)))
	cli(t, 0, append([]string{"service", "stop"}, profileArgs(s)...)...)
	daemon.wait(t)
})

var _ = It("T4R3CLI04 each frozen draft action acknowledges definitive412 only after both outputs and preserves exact K body ETag", Label("integration"), func() {
	for _, verb := range []string{"reject", "replace", "publish"} {
		peer, state, j, args := r3CompositeCaller(GinkgoT(), verb, "answer", 412, "PRECONDITION_FAILED")
		Expect(run(context.Background(), args, c2RejectedOutput{}, io.Discard)).To(Equal(4))
		Expect(pendingOperations(GinkgoT(), state.Root)).To(Equal([]string{j.OperationID}))
		Expect(run(context.Background(), args, io.Discard, c2RejectedOutput{})).To(Equal(4))
		Expect(pendingOperations(GinkgoT(), state.Root)).To(Equal([]string{j.OperationID}))
		Expect(r3ErrorOperation(GinkgoT(), cli(GinkgoT(), 4, args...), 412)).To(Equal(j.OperationID))
		Expect(pendingOperations(GinkgoT(), state.Root)).To(BeEmpty())
		retained, e := state.Load(j.OperationID)
		Expect(e).NotTo(HaveOccurred())
		Expect(retained.FinalBody).To(Equal(j.FinalBody))
		Expect(retained.Preconditions).To(Equal(j.Preconditions))
		requests, _ := r3WireSnapshot(peer)
		Expect(requests).To(HaveLen(3))
		Expect(requests[1]).To(Equal(requests[0]))
		Expect(requests[2]).To(Equal(requests[0]))
	}
})
var _ = It("T4R3CLI05 draft early failed receipt acknowledges without new write while uncertain runtime and explicit operations retain identity", Label("integration"), func() {
	peer, state, j, args := r3CompositeCaller(GinkgoT(), "publish", "recorded", 503, "SERVICE_RECOVERING")
	Expect(r3ErrorOperation(GinkgoT(), cli(GinkgoT(), 5, args...), 503)).To(Equal(j.OperationID))
	Expect(pendingOperations(GinkgoT(), state.Root)).To(BeEmpty())
	retained, e := state.Load(j.OperationID)
	Expect(e).NotTo(HaveOccurred())
	Expect(retained.Response.Status).To(Equal(503))
	requests, _ := r3WireSnapshot(peer)
	Expect(requests).To(BeEmpty())
	for _, row := range []struct {
		mode   string
		status int
		code   string
		exit   int
	}{{"answer", 401, "SESSION_EXPIRED", 3}, {"answer", 403, "FORBIDDEN", 3}, {"answer", 503, "SERVICE_RECOVERING", 5}, {"lost", 0, "", 5}, {"decode", 0, "", 5}, {"runtime", 0, "", 6}} {
		peer, state, j, args = r3CompositeCaller(GinkgoT(), "reject", row.mode, row.status, row.code)
		if row.mode == "runtime" {
			peer.mu.Lock()
			peer.runtime = "changed-runtime"
			peer.mu.Unlock()
		}
		Expect(r3ErrorOperation(GinkgoT(), cli(GinkgoT(), row.exit, args...), -1)).To(Equal(j.OperationID))
		Expect(pendingOperations(GinkgoT(), state.Root)).To(Equal([]string{j.OperationID}))
		retained, e = state.Load(j.OperationID)
		Expect(e).NotTo(HaveOccurred())
		Expect(retained.Preconditions).To(Equal(j.Preconditions))
		Expect(retained.FinalBody).To(Equal(j.FinalBody))
		requests, _ = r3WireSnapshot(peer)
		if row.mode == "runtime" {
			Expect(requests).To(BeEmpty())
		}
	}
	_, state, j, args = r3CompositeCaller(GinkgoT(), "publish", "answer", 412, "PRECONDITION_FAILED")
	args = append(args, "--operation-id", j.OperationID)
	cli(GinkgoT(), 4, args...)
	Expect(pendingOperations(GinkgoT(), state.Root)).To(Equal([]string{j.OperationID}))
})

var _ = It("T4R3CLI06 normal concurrent draft mutation refuses the frozen CLI validator then a new automatic command captures current state", Label("persistence"), func() {
	t := GinkgoT()
	ctx := context.Background()
	s := selection(t)
	initProfile(t, s)
	var interleaved atomic.Bool
	daemon := startGenerationCLIApplication(t, s, false, func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method == "PATCH" && strings.HasPrefix(r.URL.Path, "/api/v1/drafts/") && interleaved.CompareAndSwap(false, true) {
				// A second ordinary authenticated command wins before the original request.
				other := r.Clone(r.Context())
				other.Header = r.Header.Clone()
				key, _ := service.NewID()
				other.Header.Set("Idempotency-Key", key)
				body := `{"action":"reject"}`
				other.Body = io.NopCloser(strings.NewReader(body))
				other.ContentLength = int64(len(body))
				rec := httptest.NewRecorder()
				next.ServeHTTP(rec, other)
				Expect(rec.Code).To(Equal(200))
			}
			next.ServeHTTP(w, r)
		})
	})
	owner, token := bootstrapOwnerCLI(t, s, daemon.id.Endpoint, "concurrent draft owner")
	root, flags := callerProfile(t, s, owner, token, daemon.id.Endpoint)
	flags = append(flags, "--data-dir", s.DataDir)
	api, e := client.New(daemon.id.Endpoint, s.Profile, client.Credential{Endpoint: daemon.id.Endpoint, Profile: s.Profile, LibraryID: owner.LibraryID, Token: token})
	Expect(e).NotTo(HaveOccurred())
	identity, e := api.Identity(ctx)
	Expect(e).NotTo(HaveOccurred())
	input := filepath.Join(t.TempDir(), "request.json")
	generationCLIInput(input, identity.UnfiledProjectID)
	generated := cli(t, 0, append([]string{"generate", "--request-file", input}, flags...)...)
	var result struct {
		Data struct {
			Result struct {
				Draft string `json:"draft_id"`
			} `json:"result"`
		} `json:"data"`
	}
	Expect(json.Unmarshal(generated, &result)).To(Succeed())
	id := result.Data.Result.Draft
	Expect(id).NotTo(BeEmpty())
	args := append([]string{"drafts", "reject", id}, flags...)
	first := r3ErrorOperation(t, cli(t, 4, args...), 412)
	Expect(interleaved.Load()).To(BeTrue())
	Expect(pendingOperations(t, root)).NotTo(ContainElement(first))
	state, e := clientstate.New(root)
	Expect(e).NotTo(HaveOccurred())
	original, e := state.Load(first)
	Expect(e).NotTo(HaveOccurred())
	var current importer.DraftView
	_, e = api.JSON(ctx, "GET", "/api/v1/drafts/"+id, "", nil, &current)
	Expect(e).NotTo(HaveOccurred())
	Expect(current.State).To(Equal("rejected"))
	Expect(current.ETag).NotTo(Equal(original.Preconditions["If-Match"]))
	second := r3ErrorOperation(t, cli(t, 4, args...), 409)
	Expect(second).NotTo(Equal(first))
	fresh, e := state.Load(second)
	Expect(e).NotTo(HaveOccurred())
	Expect(fresh.Preconditions["If-Match"]).To(Equal(current.ETag))
	Expect(original.FinalBody).To(Equal(fresh.FinalBody))
	Expect(daemon.driver.calls.Load()).To(Equal(int32(1)))
	cli(t, 0, append([]string{"service", "stop"}, profileArgs(s)...)...)
	daemon.wait(t)
})

var _ = It("T4R3CLI01 frozen generation definitive refusals acknowledge only after both outputs and retain original journal", Label("integration"), func() {
	for _, status := range []int{400, 422} {
		peer, state, j, args := r3CompositeCaller(GinkgoT(), "generation", "answer", status, "INVALID_REQUEST")
		Expect(run(context.Background(), args, c2RejectedOutput{}, io.Discard)).To(Equal(2))
		Expect(pendingOperations(GinkgoT(), state.Root)).To(Equal([]string{j.OperationID}))
		Expect(run(context.Background(), args, io.Discard, c2RejectedOutput{})).To(Equal(2))
		Expect(pendingOperations(GinkgoT(), state.Root)).To(Equal([]string{j.OperationID}))
		out := cli(GinkgoT(), 2, args...)
		Expect(r3ErrorOperation(GinkgoT(), out, status)).To(Equal(j.OperationID))
		Expect(pendingOperations(GinkgoT(), state.Root)).To(BeEmpty())
		retained, e := state.Load(j.OperationID)
		Expect(e).NotTo(HaveOccurred())
		Expect(retained.FinalBody).To(Equal(j.FinalBody))
		Expect(retained.Identity).To(Equal(j.Identity))
		requests, _ := r3WireSnapshot(peer)
		Expect(requests).To(HaveLen(3))
		Expect(requests[1]).To(Equal(requests[0]))
		Expect(requests[2]).To(Equal(requests[0]))
		// A corrected new invocation rereads the same path only after that ACK.
		out = cli(GinkgoT(), 2, args...)
		Expect(r3ErrorOperation(GinkgoT(), out, status)).NotTo(Equal(j.OperationID))
		Expect(pendingOperations(GinkgoT(), state.Root)).To(BeEmpty())
	}
})

var _ = It("T4R3CLI02 frozen generation terminal receipt acknowledges while auth uncertain runtime and explicit identities remain pending", Label("integration"), func() {
	peer, state, j, args := r3CompositeCaller(GinkgoT(), "generation", "recorded", 503, "SERVICE_RECOVERING")
	out := cli(GinkgoT(), 5, args...)
	Expect(r3ErrorOperation(GinkgoT(), out, 503)).To(Equal(j.OperationID))
	Expect(pendingOperations(GinkgoT(), state.Root)).To(BeEmpty())
	retained, e := state.Load(j.OperationID)
	Expect(e).NotTo(HaveOccurred())
	Expect(retained.Phase).To(Equal("failed"))
	Expect(retained.Response.Status).To(Equal(503))
	requests, _ := r3WireSnapshot(peer)
	Expect(requests).To(BeEmpty())
	for _, row := range []struct {
		mode   string
		status int
		code   string
		exit   int
	}{{"answer", 401, "SESSION_EXPIRED", 3}, {"answer", 403, "FORBIDDEN", 3}, {"answer", 503, "SERVICE_RECOVERING", 5}, {"answer", 409, "OPERATION_RECONCILIATION_REQUIRED", 6}, {"lost", 0, "", 5}, {"decode", 0, "", 5}, {"runtime", 0, "", 6}} {
		peer, state, j, args = r3CompositeCaller(GinkgoT(), "generation", row.mode, row.status, row.code)
		if row.mode == "runtime" {
			peer.mu.Lock()
			peer.runtime = "changed-runtime"
			peer.mu.Unlock()
		}
		Expect(r3ErrorOperation(GinkgoT(), cli(GinkgoT(), row.exit, args...), -1)).To(Equal(j.OperationID))
		Expect(pendingOperations(GinkgoT(), state.Root)).To(Equal([]string{j.OperationID}))
		retained, e = state.Load(j.OperationID)
		Expect(e).NotTo(HaveOccurred())
		Expect(retained.FinalBody).To(Equal(j.FinalBody))
		requests, _ = r3WireSnapshot(peer)
		if row.mode == "runtime" {
			Expect(requests).To(BeEmpty())
		}
	}
	// Explicit replay must not ACK the automatic index, even for its same saved K.
	_, state, j, args = r3CompositeCaller(GinkgoT(), "generation", "answer", 422, "INVALID_REQUEST")
	args = append(args, "--operation-id", j.OperationID)
	cli(GinkgoT(), 2, args...)
	Expect(pendingOperations(GinkgoT(), state.Root)).To(Equal([]string{j.OperationID}))
})

var _ = It("T4R3CLI07 generation and draft failed-receipt persistence refusal cannot acknowledge an automatic index", Label("integration"), func() {
	for _, kind := range []string{"generation", "publish"} {
		peer, state, j, args := r3CompositeCaller(GinkgoT(), kind, "recorded", 503, "SERVICE_RECOVERING")
		originalPath := filepath.Join(state.Root, "journals", j.OperationID+".json")
		original, e := os.ReadFile(originalPath)
		Expect(e).NotTo(HaveOccurred())
		directory := filepath.Join(state.Root, "journals")
		peer.mu.Lock()
		peer.beforeReceipt = func() { Expect(os.Chmod(directory, 0500)).To(Succeed()) }
		peer.mu.Unlock()
		// The current caller has already loaded its journal when the real GET returns.
		code := run(context.Background(), args, io.Discard, io.Discard)
		Expect(os.Chmod(directory, 0700)).To(Succeed())
		Expect(code).To(Equal(5))
		Expect(pendingOperations(GinkgoT(), state.Root)).To(Equal([]string{j.OperationID}))
		retained, e := os.ReadFile(originalPath)
		Expect(e).NotTo(HaveOccurred())
		Expect(retained).To(Equal(original))
		Expect(r3ErrorOperation(GinkgoT(), cli(GinkgoT(), 5, args...), 503)).To(Equal(j.OperationID))
		Expect(pendingOperations(GinkgoT(), state.Root)).To(BeEmpty())
		requests, _ := r3WireSnapshot(peer)
		Expect(requests).To(BeEmpty())
	}
})

var _ = It("T4R3CLI08 composite saved-result and fresh-validator reconciliation use exit6 without acknowledgement or new writes", Label("integration"), func() {
	peer, state, j, args := r3CompositeCaller(GinkgoT(), "generation", "accepted", 0, "")
	Expect(r3ErrorOperation(GinkgoT(), cli(GinkgoT(), 6, args...), -1)).To(Equal(j.OperationID))
	Expect(pendingOperations(GinkgoT(), state.Root)).To(Equal([]string{j.OperationID}))
	requests, _ := r3WireSnapshot(peer)
	Expect(requests).To(BeEmpty())
	peer, state, j, args = r3CompositeCaller(GinkgoT(), "publish", "invalid-validator", 0, "")
	// A fresh capture reads the normal current resource before preparing its journal.
	Expect(os.Remove(filepath.Join(state.Root, "journals", j.OperationID+".json"))).To(Succeed())
	Expect(r3ErrorOperation(GinkgoT(), cli(GinkgoT(), 6, args...), -1)).To(Equal(j.OperationID))
	Expect(pendingOperations(GinkgoT(), state.Root)).To(Equal([]string{j.OperationID}))
	requests, _ = r3WireSnapshot(peer)
	Expect(requests).To(BeEmpty())
})
