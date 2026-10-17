package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"haeryong.local/haeryong/internal/application/service"
	"haeryong.local/haeryong/internal/application/transfer"
)

// The proxy forwards real authenticated requests to the production daemon and
// closes one socket after its response has committed. It supplies no authority.
func lostResponse(t *testing.T, endpoint, phase string) (*httptest.Server, *atomic.Int64, *atomic.Bool) {
	t.Helper()
	target, e := url.Parse(endpoint)
	if e != nil {
		t.Fatal(e)
	}
	count := new(atomic.Int64)
	blocked := new(atomic.Bool)
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		match := r.Method == "POST" && ((phase == "create" || phase == "metadata_create") && r.URL.Path == "/api/v1/uploads" || (phase == "complete" || phase == "metadata_complete") && strings.HasSuffix(r.URL.Path, "/complete") || phase == "final" && r.URL.Path == "/api/v1/imports")

		if match && blocked.Load() {
			http.Error(w, "fixture response unavailable", 503)
			return
		}
		out := r.Clone(r.Context())
		out.URL.Scheme = target.Scheme
		out.URL.Host = target.Host
		out.Host = target.Host
		out.RequestURI = ""
		response, e := http.DefaultTransport.RoundTrip(out)
		if e != nil {
			http.Error(w, "fixture transport unavailable", 502)
			return
		}
		defer response.Body.Close()
		dropAt := int64(1)
		if strings.HasPrefix(phase, "metadata_") {
			dropAt = 2
		}
		if match && count.Add(1) == dropAt {
			blocked.Store(true)
			io.Copy(io.Discard, response.Body)
			connection, _, e := w.(http.Hijacker).Hijack()
			if e != nil {
				t.Error(e)
				return
			}
			connection.Close()
			return
		}
		for key, values := range response.Header {
			for _, value := range values {
				w.Header().Add(key, value)
			}
		}
		w.WriteHeader(response.StatusCode)
		io.Copy(w, response.Body)
	}))
	t.Cleanup(proxy.Close)
	return proxy, count, blocked
}
func readCallerJournal(t *testing.T, root, id string) transfer.Journal {
	t.Helper()
	b, e := os.ReadFile(filepath.Join(root, "journals", id+".json"))
	if e != nil {
		t.Fatal(e)
	}
	var j transfer.Journal
	if e = json.Unmarshal(b, &j); e != nil {
		t.Fatal(e)
	}
	return j
}

func TestActualCLIResponseLossAtCreateCompleteAndFinalDispatch(t *testing.T) {
	for _, phase := range []string{"create", "complete", "metadata_create", "metadata_complete", "final"} {
		t.Run(phase, func(t *testing.T) {
			s := selection(t)
			initProfile(t, s)
			daemon := startDaemon(t, s, availablePort(t))
			session, token := bootstrapOwnerCLI(t, s, daemon.id.Endpoint, "phase-loss fixture")
			proxy, lost, blocked := lostResponse(t, daemon.id.Endpoint, phase)
			root, flags := callerProfile(t, s, session, token, proxy.URL)
			input := filepath.Join(t.TempDir(), "complete-tree")
			if e := os.Mkdir(input, 0700); e != nil {
				t.Fatal(e)
			}
			for _, name := range []string{"first.html", "second.data"} {
				if e := os.WriteFile(filepath.Join(input, name), []byte("original:"+name), 0600); e != nil {
					t.Fatal(e)
				}
			}
			metadata := filepath.Join(t.TempDir(), "metadata.json")
			if e := os.WriteFile(metadata, []byte(`{"document":{"title":"Captured original title"}}`), 0600); e != nil {
				t.Fatal(e)
			}
			id, _ := service.NewID()
			args := append([]string{"import", input, "--metadata", metadata, "--operation-id", id}, flags...)
			cli(t, 5, args...)
			blocked.Store(false)
			before := readCallerJournal(t, root, id)
			wantLost := int64(1)
			if strings.HasPrefix(phase, "metadata_") {
				wantLost = 2
			}
			if lost.Load() != wantLost || before.Result.Snapshot.ID == "" || before.Metadata == nil || before.Metadata.Snapshot.ID == "" {
				t.Fatal("dispatch preceded durable complete input capture", phase)
			}
			if before.OperationID == before.Result.CreateKey || before.OperationID == before.Result.CompleteKey || before.Result.CreateKey == before.Result.CompleteKey {
				t.Fatal("composite IDs aliased")
			}
			if e := os.RemoveAll(input); e != nil {
				t.Fatal(e)
			}
			if e := os.Remove(metadata); e != nil {
				t.Fatal(e)
			}
			out := cli(t, 0, args...)
			var response struct {
				Data struct {
					Document string `json:"document_id"`
					Version  string `json:"version_id"`
					Result   struct {
						Job string `json:"job_id"`
					} `json:"result"`
				}
			}
			if e := json.Unmarshal(out, &response); e != nil {
				t.Fatal(e)
			}
			after := readCallerJournal(t, root, id)
			if after.Result.CreateKey != before.Result.CreateKey || after.Result.CompleteKey != before.Result.CompleteKey || after.Result.Snapshot != before.Result.Snapshot || after.Metadata.Snapshot != before.Metadata.Snapshot || after.Metadata.CreateKey != before.Metadata.CreateKey || after.Metadata.CompleteKey != before.Metadata.CompleteKey || after.Selection != before.Selection || (after.BaseVersionID != nil || before.BaseVersionID != nil) {
				t.Fatal("retry replaced original input/keys")
			}
			status, version := authRequest(t, daemon.id.Endpoint, "GET", "/api/v1/versions/"+response.Data.Version, token, nil)
			if status != 200 || !bytes.Contains(version, []byte("Captured original title")) {
				t.Fatal("original selected metadata was not preserved", status)
			}
			status, versions := authRequest(t, daemon.id.Endpoint, "GET", "/api/v1/documents/"+response.Data.Document+"/versions", token, nil)
			if status != 200 {
				t.Fatal(status)
			}
			var list struct {
				Items []json.RawMessage `json:"items"`
			}
			if e := json.Unmarshal(versions, &list); e != nil || len(list.Items) != 1 {
				t.Fatal("response-loss retry published twice", e)
			}
		})
	}
}

func TestActualCLIRetainedManifestPrefixCannotPublishAndOriginalRecovers(t *testing.T) {
	s := selection(t)
	initProfile(t, s)
	daemon := startDaemon(t, s, availablePort(t))
	session, token := bootstrapOwnerCLI(t, s, daemon.id.Endpoint, "manifest-integrity fixture")
	proxy, _, blocked := lostResponse(t, daemon.id.Endpoint, "create")
	root, flags := callerProfile(t, s, session, token, proxy.URL)
	input := filepath.Join(t.TempDir(), "selected")
	if e := os.Mkdir(input, 0700); e != nil {
		t.Fatal(e)
	}
	for _, name := range []string{"a.html", "b.html"} {
		if e := os.WriteFile(filepath.Join(input, name), []byte(name), 0600); e != nil {
			t.Fatal(e)
		}
	}
	id, _ := service.NewID()
	args := append([]string{"import", input, "--operation-id", id}, flags...)
	cli(t, 5, args...)
	blocked.Store(false)
	before := readCallerJournal(t, root, id)
	manifest := filepath.Join(root, "snapshots", before.Result.Snapshot.ID, "entries.jsonl")
	original, e := os.ReadFile(manifest)
	if e != nil {
		t.Fatal(e)
	}
	cut := bytes.IndexByte(original, '\n') + 1
	if cut < 1 || cut == len(original) {
		t.Fatal("fixture lacks two entries")
	}
	if e = os.WriteFile(manifest, original[:cut], 0600); e != nil {
		t.Fatal(e)
	}
	if e = os.RemoveAll(input); e != nil {
		t.Fatal(e)
	}
	cli(t, 4, args...)
	after := readCallerJournal(t, root, id)
	if after.Response != nil || after.Phase == "final_dispatch" {
		t.Fatal("prefix reached completion/publication")
	}
	status, _ := authRequest(t, daemon.id.Endpoint, "GET", "/api/v1/operations/"+id, token, nil)
	if status != 404 {
		t.Fatal("prefix created final operation", status)
	}
	if e = os.WriteFile(manifest, original, 0600); e != nil {
		t.Fatal(e)
	}
	cli(t, 0, args...)
	final := readCallerJournal(t, root, id)
	if final.Result.CreateKey != before.Result.CreateKey || final.Result.CompleteKey != before.Result.CompleteKey || final.Response == nil {
		t.Fatal("recovery replaced captured intent")
	}
}
