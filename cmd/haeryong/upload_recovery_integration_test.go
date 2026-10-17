package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"testing"

	"haeryong.local/haeryong/internal/adapters/client"
	importer "haeryong.local/haeryong/internal/application/import"
	"haeryong.local/haeryong/internal/application/service"
)

func TestActualCLIOtherOwnerDiscoversAndResumesArbitraryConfirmedRanges(t *testing.T) {
	ctx := context.Background()
	s := selection(t)
	initProfile(t, s)
	daemon := startDaemon(t, s, availablePort(t))
	first, firstToken := bootstrapOwnerCLI(t, s, daemon.id.Endpoint, "original upload device")
	second, secondToken := bootstrapOwnerCLI(t, s, daemon.id.Endpoint, "recovery owner device")
	api, e := client.New(daemon.id.Endpoint, s.Profile, client.Credential{Endpoint: daemon.id.Endpoint, Profile: s.Profile, LibraryID: first.LibraryID, Token: firstToken})
	if e != nil {
		t.Fatal(e)
	}
	identity, e := api.Identity(ctx)
	if e != nil {
		t.Fatal(e)
	}
	target, _ := json.Marshal(map[string]any{"project_id": identity.UnfiledProjectID, "document_id": nil})
	u, _ := service.NewID()
	created, e := api.Create(ctx, u, importer.CreateRequest{Purpose: "result", InputMode: "file", SelectionName: "original.html", Target: target})
	if e != nil {
		t.Fatal(e)
	}
	body := bytes.Repeat([]byte("exact arbitrary-range input\n"), 14000)
	size := int64(len(body))
	// These confirmed segments deliberately leave both a prefix and interior
	// gaps and share no fixed-size chunk boundary with the recovering client.
	for _, interval := range [][2]int64{{17, 1003}, {5009, 17011}, {size - 127, size}} {
		segment := body[interval[0]:interval[1]]
		h := sha256.Sum256(segment)
		if _, e = api.Put(ctx, created.ID, importer.Chunk{Path: "_input", Kind: "file", Offset: interval[0], Hash: hex.EncodeToString(h[:]), Length: &size}, bytes.NewReader(segment)); e != nil {
			t.Fatal(e)
		}
	}
	if _, e = api.JSON(ctx, "DELETE", "/api/v1/sessions/"+first.ID, "", nil, nil); e != nil {
		t.Fatal(e)
	}
	var mu sync.Mutex
	var offsets []int64
	destination, _ := url.Parse(daemon.id.Endpoint)
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "PUT" {
			n, _ := strconv.ParseInt(r.Header.Get("Haeryong-Upload-Offset"), 10, 64)
			mu.Lock()
			offsets = append(offsets, n)
			mu.Unlock()
		}
		out := r.Clone(r.Context())
		out.URL.Scheme = destination.Scheme
		out.URL.Host = destination.Host
		out.Host = destination.Host
		out.RequestURI = ""
		response, err := http.DefaultTransport.RoundTrip(out)
		if err != nil {
			http.Error(w, "fixture transport unavailable", 502)
			return
		}
		defer response.Body.Close()
		for k, values := range response.Header {
			for _, v := range values {
				w.Header().Add(k, v)
			}
		}
		w.WriteHeader(response.StatusCode)
		io.Copy(w, response.Body)
	}))
	defer proxy.Close()
	root, flags := callerProfile(t, s, second, secondToken, proxy.URL)
	discovered := cli(t, 0, append([]string{"uploads", "list", "--unadopted"}, flags...)...)
	if !bytes.Contains(discovered, []byte(created.ID)) {
		t.Fatal("owner with no caller journal cannot discover retained upload")
	}
	cli(t, 0, append([]string{"uploads", "show", created.ID}, flags...)...)
	input := filepath.Join(t.TempDir(), "renamed-local-file.html")
	changed := bytes.Clone(body)
	changed[18] ^= 1
	if e = os.WriteFile(input, changed, 0600); e != nil {
		t.Fatal(e)
	}
	// A mismatching confirmed range must be rejected before any missing byte is
	// sent. This first caller's retained snapshot remains its original intent.
	badID, _ := service.NewID()
	cli(t, 4, append([]string{"uploads", "resume", created.ID, "--input", input, "--operation-id", badID}, flags...)...)
	mu.Lock()
	wrote := len(offsets)
	mu.Unlock()
	if wrote != 0 {
		t.Fatal("changed confirmed bytes caused new upload writes", wrote)
	}
	if e = os.WriteFile(input, body, 0600); e != nil {
		t.Fatal(e)
	}
	completed := cli(t, 0, append([]string{"uploads", "resume", created.ID, "--input", input}, flags...)...)
	var result struct {
		Data struct {
			Operation string `json:"operation_id"`
		} `json:"data"`
	}
	if e = json.Unmarshal(completed, &result); e != nil || result.Data.Operation == "" {
		t.Fatal("completion identity missing", e)
	}
	journal := readCallerJournal(t, root, result.Data.Operation)
	if journal.Result.UploadID != created.ID || journal.Result.Snapshot.SelectionName != "original.html" || !journal.Result.Checkpoint.Complete {
		t.Fatal("resume changed original binding")
	}
	mu.Lock()
	actual := append([]int64(nil), offsets...)
	mu.Unlock()
	if len(actual) != 4 || actual[0] != 0 || actual[1] != 1003 || actual[2] != 17011 || actual[3] != size-127 {
		t.Fatal("resume retransmitted confirmed ranges or skipped gaps", actual)
	}
	secondAPI, e := client.New(daemon.id.Endpoint, s.Profile, client.Credential{Endpoint: daemon.id.Endpoint, Profile: s.Profile, LibraryID: second.LibraryID, Token: secondToken})
	if e != nil {
		t.Fatal(e)
	}
	i, _ := service.NewID()
	request, _ := json.Marshal(map[string]any{"upload_id": created.ID, "project_id": identity.UnfiledProjectID, "document_id": nil, "base_version_id": nil})
	response, e := secondAPI.Import(ctx, i, request)
	if e != nil {
		t.Fatal(e)
	}
	var job struct {
		ID string `json:"job_id"`
	}
	if e = json.Unmarshal(response.Body, &job); e != nil {
		t.Fatal(e)
	}
	_, version := waitPublishedCLI(t, daemon.id.Endpoint, secondToken, job.ID)
	file, e := secondAPI.Call(ctx, "GET", "/api/v1/versions/"+version+"/file?path=original.html", "", nil, nil)
	if e != nil {
		t.Fatal(e)
	}
	defer file.Body.Close()
	got, e := io.ReadAll(file.Body)
	if e != nil || !bytes.Equal(got, body) {
		t.Fatal("resumed upload publication lost exact bytes", e)
	}
}
