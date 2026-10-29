package source_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"sync/atomic"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	sourcefiles "haeryong.local/haeryong/internal/adapters/source"
	appsource "haeryong.local/haeryong/internal/application/source"
	app "haeryong.local/haeryong/internal/application/storage"
	"haeryong.local/haeryong/internal/domain"
)

// Counters surround the actual source/object adapters. Metadata requests must
// not cross these ports, including when the approved checkout is gone.
type r1HTTPFiles struct {
	sourcefiles.Files
	opens, locations atomic.Int64
}

func (r *r1HTTPFiles) Open(ctx context.Context, root, path string) (io.ReadCloser, error) {
	r.opens.Add(1)
	return r.Files.Open(ctx, root, path)
}

func (r *r1HTTPFiles) CheckLocation(ctx context.Context, root, path string) error {
	r.locations.Add(1)
	return r.Files.CheckLocation(ctx, root, path)
}

type r1HTTPGit struct {
	appsource.Reader
	calls atomic.Int64
}

func (r *r1HTTPGit) ResolveRevision(ctx context.Context, root, revision string) error {
	r.calls.Add(1)
	return r.Reader.ResolveRevision(ctx, root, revision)
}

func (r *r1HTTPGit) ResolveHead(ctx context.Context, root string) (string, error) {
	r.calls.Add(1)
	return r.Reader.ResolveHead(ctx, root)
}

func (r *r1HTTPGit) ChangedPaths(ctx context.Context, root, base, tip string, cited []string) ([]string, error) {
	r.calls.Add(1)
	return r.Reader.ChangedPaths(ctx, root, base, tip, cited)
}

func (r *r1HTTPGit) ReadBlob(ctx context.Context, root, revision, path string) (io.ReadCloser, error) {
	r.calls.Add(1)
	return r.Reader.ReadBlob(ctx, root, revision, path)
}

type r1HTTPBytes struct {
	appsource.CaptureBytes
	reads, puts atomic.Int64
}

func (r *r1HTTPBytes) Read(ctx context.Context, object domain.Object, pin domain.Pin, consume func(io.ReadSeeker) error) error {
	r.reads.Add(1)
	return r.CaptureBytes.Read(ctx, object, pin, consume)
}

func (r *r1HTTPBytes) Put(ctx context.Context, input io.Reader, commit func(app.WriteTx, domain.Object) error) (domain.Object, error) {
	r.puts.Add(1)
	return r.CaptureBytes.Put(ctx, input, commit)
}

type r1HTTPView struct {
	ID        string                     `json:"capture_id"`
	State     string                     `json:"state"`
	Complete  bool                       `json:"complete"`
	Target    *string                    `json:"target_set_id"`
	Captured  *string                    `json:"captured_at"`
	Reason    string                     `json:"reason"`
	Selection appsource.CaptureRequest   `json:"requested_selection"`
	Items     []domain.SourceCaptureRead `json:"items"`
	Next      *string                    `json:"next_cursor"`
}

var _ = It("R1HTTP2 metadata GET HEAD and both diff sides preserve retained reference incomplete and current-policy facts without new reads", Label("persistence"), func() {
	f := newHTTPFixture(GinkgoT())
	ctx := context.Background()
	health, e := f.store.Health(ctx)
	Expect(e).NotTo(HaveOccurred())
	project := health.Library.UnfiledProjectID
	root := GinkgoT().TempDir()
	files := &r1HTTPFiles{}
	git := &r1HTTPGit{Reader: f.core.Reader}
	objects := &r1HTTPBytes{CaptureBytes: f.store}
	f.core.Files, f.core.Reader, f.core.Bytes = files, git, objects
	counts := func() [5]int64 {
		return [5]int64{files.opens.Load(), files.locations.Load(), git.calls.Load(), objects.reads.Load(), objects.puts.Load()}
	}
	configure := func(retention string, present bool) {
		Expect(f.store.Write(ctx, func(tx app.WriteTx) error {
			cfg, err := tx.Configuration()
			if err != nil {
				return err
			}
			var value map[string]any
			if err = json.Unmarshal(cfg.Value, &value); err != nil {
				return err
			}
			sources := []domain.ManagedSourceMapping{}
			if present {
				sources = append(sources, domain.ManagedSourceMapping{SourceID: "r1-http-source", Kind: "files", RootPath: root, RetentionMode: retention, ProviderIDs: []string{}})
			}
			value["projects"] = map[string]any{project: map[string]any{"sources": sources}}
			cfg.Value, err = json.Marshal(value)
			if err != nil {
				return err
			}
			return tx.SetConfiguration(cfg, cfg.Revision)
		})).To(Succeed())
	}
	// An actual loopback HTTP server supplies real HEAD semantics (the network
	// response has no body), while reusing the authenticated production handler.
	server := httptest.NewServer(f.handler)
	DeferCleanup(server.Close)
	get := func(method, path string) (int, []byte, http.Header) {
		request, err := http.NewRequest(method, server.URL+path, nil)
		Expect(err).NotTo(HaveOccurred())
		request.Header.Set("Authorization", "Bearer "+f.token)
		response, err := server.Client().Do(request)
		Expect(err).NotTo(HaveOccurred())
		body, err := io.ReadAll(response.Body)
		Expect(response.Body.Close()).To(Succeed())
		Expect(err).NotTo(HaveOccurred())
		return response.StatusCode, body, response.Header
	}
	metadata := func(method, path string, expected int) []byte {
		before := counts()
		status, body, headers := get(method, path)
		Expect(status).To(Equal(expected), string(body))
		Expect(counts()).To(Equal(before), "metadata performed source/Git/object I/O")
		if method == http.MethodHead {
			Expect(body).To(BeEmpty())
		}
		Expect(headers.Get("Content-Type")).To(ContainSubstring("application/json"))
		return body
	}
	decodeView := func(body []byte) r1HTTPView {
		var envelope struct {
			Data r1HTTPView `json:"data"`
		}
		Expect(json.Unmarshal(body, &envelope)).To(Succeed())
		return envelope.Data
	}
	post := func(key string, request appsource.CaptureRequest) r1HTTPView {
		raw, err := json.Marshal(request)
		Expect(err).NotTo(HaveOccurred())
		response := f.post("/api/v1/projects/"+project+"/source-captures", f.token, key, string(raw))
		Expect(response.Code).To(Equal(http.StatusCreated), response.Body.String())
		return decodeView(response.Body.Bytes())
	}
	inspect := func(id string, selection appsource.CaptureRequest) r1HTTPView {
		var result r1HTTPView
		seen := map[string]bool{}
		path := "/api/v1/source-captures/" + id
		for {
			page := decodeView(metadata(http.MethodGet, path, http.StatusOK))
			metadata(http.MethodHead, path, http.StatusOK)
			Expect(page.ID).To(Equal(id))
			Expect(page.Selection).To(Equal(selection), "inspection dropped or reinterpreted a selected unit")
			if result.ID == "" {
				result = page
				result.Items = nil
			} else {
				Expect(page.State).To(Equal(result.State))
				Expect(page.Target).To(Equal(result.Target))
				Expect(page.Captured).To(Equal(result.Captured))
			}
			Expect(page.Items).NotTo(BeEmpty())
			for _, row := range page.Items {
				Expect(seen[row.ID]).To(BeFalse(), "cursor repeated a captured unit")
				seen[row.ID] = true
				result.Items = append(result.Items, row)
			}
			if page.Next == nil {
				break
			}
			Expect(*page.Next).NotTo(BeEmpty())
			path = "/api/v1/source-captures/" + id + "?cursor=" + url.QueryEscape(*page.Next)
		}
		Expect(result.Items).To(HaveLen(len(selection.Selections[0].Paths)))
		return result
	}
	location := func(raw json.RawMessage) string {
		var value struct {
			Path string `json:"path"`
		}
		Expect(json.Unmarshal(raw, &value)).To(Succeed())
		Expect(value.Path).NotTo(BeEmpty())
		return value.Path
	}
	byPath := func(rows []domain.SourceCaptureRead) map[string]domain.SourceCaptureRead {
		out := map[string]domain.SourceCaptureRead{}
		for _, row := range rows {
			path := location(row.LocationJSON)
			_, exists := out[path]
			Expect(exists).To(BeFalse())
			out[path] = row
		}
		return out
	}
	compare := func(left, right r1HTTPView) {
		before, after := byPath(left.Items), byPath(right.Items)
		union := map[string]bool{}
		for path := range before {
			union[path] = true
		}
		for path := range after {
			union[path] = true
		}
		seen := map[string]bool{}
		path := "/api/v1/source-captures/" + left.ID + "/diff?other=" + right.ID
		for {
			body := metadata(http.MethodGet, path, http.StatusOK)
			metadata(http.MethodHead, path, http.StatusOK)
			var envelope struct {
				Data struct {
					Left     string                      `json:"left_capture_id"`
					Right    string                      `json:"right_capture_id"`
					Changes  []domain.SourceCaptureDelta `json:"changes"`
					Next     *string                     `json:"next_cursor"`
					Semantic any                         `json:"semantic_verdict"`
				} `json:"data"`
			}
			Expect(json.Unmarshal(body, &envelope)).To(Succeed())
			Expect(envelope.Data.Left).To(Equal(left.ID))
			Expect(envelope.Data.Right).To(Equal(right.ID))
			Expect(envelope.Data.Semantic).To(BeNil())
			Expect(envelope.Data.Changes).NotTo(BeEmpty())
			for _, delta := range envelope.Data.Changes {
				key := location(delta.Location)
				Expect(seen[key]).To(BeFalse())
				seen[key] = true
				Expect(delta.SourceID).To(Equal("r1-http-source"))
				if expected, exists := before[key]; exists {
					Expect(delta.Before).NotTo(BeNil())
					Expect(*delta.Before).To(Equal(expected), "left diff projection differs from the HTTP view")
				} else {
					Expect(delta.Before).To(BeNil())
				}
				if expected, exists := after[key]; exists {
					Expect(delta.After).NotTo(BeNil())
					Expect(*delta.After).To(Equal(expected), "right diff projection differs from the HTTP view")
				} else {
					Expect(delta.After).To(BeNil())
				}
			}
			if envelope.Data.Next == nil {
				break
			}
			path = "/api/v1/source-captures/" + left.ID + "/diff?other=" + right.ID + "&cursor=" + url.QueryEscape(*envelope.Data.Next)
		}
		Expect(seen).To(Equal(union))
	}
	request := appsource.CaptureRequest{SourceMode: "current", Selections: []appsource.CaptureSelection{{SourceID: "r1-http-source"}}}
	for n := 0; n < 65; n++ {
		path := fmt.Sprintf("unit-%02d.unknown", n)
		request.Selections[0].Paths = append(request.Selections[0].Paths, appsource.CapturePath{Path: path})
		Expect(os.WriteFile(filepath.Join(root, path), []byte("retained original "+path), 0600)).To(Succeed())
	}
	configure("selected_snapshot", true)
	retained := post(sourceKey(), request)
	retained = inspect(retained.ID, request)
	Expect(retained.Complete).To(BeTrue())
	Expect(retained.Target).NotTo(BeNil())
	Expect(retained.Captured).NotTo(BeNil())
	for _, row := range retained.Items {
		Expect(row.Availability).To(Equal("available"))
		Expect(row.CaptureAvailability).To(Equal("available"))
		Expect(row.BytesOrigin).To(Equal("retained_snapshot"))
		Expect(row.ReadState).To(Equal("not_read_by_view"))
		Expect(row.Reason).To(BeEmpty())
	}
	// Positive control: the real read endpoint verifies and delivers known
	// retained bytes; metadata-only expectations cannot pass by denying all I/O.
	firstPath := request.Selections[0].Paths[0].Path
	first := byPath(retained.Items)[firstPath]
	status, body, _ := get(http.MethodGet, "/api/v1/source-reads/"+first.ID)
	Expect(status).To(Equal(http.StatusOK))
	Expect(body).To(Equal([]byte("retained original " + firstPath)))
	Expect(first.Hash).To(Equal(shaOf(body)))
	Expect(objects.reads.Load()).To(BeNumerically(">", 0))
	for _, path := range request.Selections[0].Paths {
		Expect(os.WriteFile(filepath.Join(root, path.Path), []byte("later input "+path.Path), 0600)).To(Succeed())
	}
	configure("reference_only", true)
	reference := post(sourceKey(), request)
	reference = inspect(reference.ID, request)
	Expect(reference.Complete).To(BeTrue())
	for _, row := range reference.Items {
		Expect(row.CaptureAvailability).To(Equal("available"))
		Expect(row.Availability).To(Equal("unavailable"))
		Expect(row.BytesOrigin).To(BeEmpty())
		Expect(row.Reason).To(Equal("not_retained"))
		Expect(row.ReadState).To(Equal("not_read_by_view"))
		Expect(row.Hash).To(Equal(shaOf([]byte("later input " + location(row.LocationJSON)))))
	}
	for _, path := range request.Selections[0].Paths {
		Expect(os.WriteFile(filepath.Join(root, path.Path), []byte("partial input "+path.Path), 0600)).To(Succeed())
	}
	configure("selected_snapshot", true)
	partialRequest := request
	partialRequest.Selections = append([]appsource.CaptureSelection(nil), request.Selections...)
	partialRequest.Selections[0].Paths = append(append([]appsource.CapturePath(nil), request.Selections[0].Paths...), appsource.CapturePath{Path: "missing.unknown"})
	partial := post(sourceKey(), partialRequest)
	partial = inspect(partial.ID, partialRequest)
	Expect(partial.Complete).To(BeFalse())
	Expect(partial.State).To(Equal("failed"))
	Expect(partial.Reason).To(Equal("incomplete_source_capture"))
	Expect(partial.Target).To(BeNil())
	Expect(partial.Captured).To(BeNil())
	for _, row := range partial.Items {
		Expect(row.Availability).To(Equal("unavailable"))
		Expect(row.BytesOrigin).To(BeEmpty())
		Expect(row.ReadState).To(Equal("not_read_by_view"))
		if location(row.LocationJSON) == "missing.unknown" {
			Expect(row.CaptureAvailability).To(Equal("unavailable"))
			Expect(row.Reason).To(Equal("missing_source"))
			Expect(row.Hash).To(BeEmpty())
		} else {
			Expect(row.CaptureAvailability).To(Equal("available"))
			Expect(row.Reason).To(Equal("capture_not_completed"))
			Expect(row.Hash).To(Equal(shaOf([]byte("partial input " + location(row.LocationJSON)))))
		}
	}
	// Read a genuinely capturing request at the existing detached stage hook,
	// without manufacturing a lifecycle row or introducing a worker/goroutine.
	pendingRequest := appsource.CaptureRequest{SourceMode: "current", Selections: []appsource.CaptureSelection{{SourceID: "r1-http-source", Paths: []appsource.CapturePath{{Path: firstPath}}}}}
	pendingKey := sourceKey()
	observedPending := false
	f.core.Fault = func(stage string) error {
		if stage != "capture_before_commit" {
			return nil
		}
		actor, err := f.auth.Authenticate(ctx, f.token)
		Expect(err).NotTo(HaveOccurred())
		receipt, err := f.store.Receipt(ctx, domain.OperationKey{PrincipalID: actor.PrincipalID, OperationID: pendingKey})
		Expect(err).NotTo(HaveOccurred())
		var ids map[string]string
		Expect(json.Unmarshal(receipt.Normalization, &ids)).To(Succeed())
		pending := inspect(ids["capture_id"], pendingRequest)
		Expect(pending.State).To(Equal("capturing"))
		Expect(pending.Target).To(BeNil())
		Expect(pending.Captured).To(BeNil())
		Expect(pending.Items).To(HaveLen(1))
		Expect(pending.Items[0].Availability).To(Equal("unavailable"))
		Expect(pending.Items[0].CaptureAvailability).To(Equal("available"))
		Expect(pending.Items[0].BytesOrigin).To(BeEmpty())
		Expect(pending.Items[0].Reason).To(Equal("capture_not_completed"))
		compare(pending, retained)
		compare(retained, pending)
		observedPending = true
		return nil
	}
	finished := post(pendingKey, pendingRequest)
	f.core.Fault = nil
	Expect(observedPending).To(BeTrue())
	Expect(finished.Complete).To(BeTrue())
	// The absent original checkout must not cause metadata to run another read
	// or silently upgrade reference-only and failed capture facts.
	Expect(os.RemoveAll(root)).To(Succeed())
	baseline := counts()
	retained = inspect(retained.ID, request)
	reference = inspect(reference.ID, request)
	partial = inspect(partial.ID, partialRequest)
	compare(retained, reference)
	compare(reference, retained)
	compare(reference, partial)
	compare(partial, reference)
	Expect(counts()).To(Equal(baseline))
	configure("selected_snapshot", false)
	for _, capture := range []r1HTTPView{retained, reference, partial} {
		denied := inspect(capture.ID, capture.Selection)
		Expect(denied.State).To(Equal(capture.State))
		Expect(denied.Complete).To(Equal(capture.Complete))
		before := map[string]domain.SourceCaptureRead{}
		for _, row := range capture.Items {
			before[row.ID] = row
		}
		for _, row := range denied.Items {
			Expect(row.Availability).To(Equal("denied"))
			Expect(row.CaptureAvailability).To(Equal(before[row.ID].CaptureAvailability))
			Expect(row.Reason).To(Equal("source_denied"))
			Expect(row.BytesOrigin).To(BeEmpty())
			Expect(row.Hash).To(BeEmpty())
			Expect([]byte(row.SourceJSON)).To(Or(BeEmpty(), Equal([]byte("null"))))
			Expect([]byte(row.LocationJSON)).To(Or(BeEmpty(), Equal([]byte("null"))))
		}
	}
	for _, pair := range [][2]string{{retained.ID, reference.ID}, {reference.ID, retained.ID}, {reference.ID, partial.ID}, {partial.ID, reference.ID}} {
		path := "/api/v1/source-captures/" + pair[0] + "/diff?other=" + pair[1]
		body := metadata(http.MethodGet, path, http.StatusNotFound)
		Expect(string(body)).NotTo(ContainSubstring(first.Hash))
		Expect(string(body)).NotTo(ContainSubstring(firstPath))
		metadata(http.MethodHead, path, http.StatusNotFound)
	}
	Expect(counts()).To(Equal(baseline))
	Expect(files.opens.Load()).To(BeNumerically(">", 0))
	Expect(files.locations.Load()).To(BeNumerically(">", 0))
	Expect(f.store.Write(ctx, func(tx app.WriteTx) error {
		jobs, err := tx.Jobs()
		if err == nil {
			Expect(jobs).To(BeEmpty(), "metadata or captures scheduled provider work")
		}
		return err
	})).To(Succeed())
})
