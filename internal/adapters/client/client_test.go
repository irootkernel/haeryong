package client

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"haeryong.local/haeryong/internal/application/transfer"
	sec "haeryong.local/haeryong/internal/security/session"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

func TestPendingKResponsePollsOnlyLocalIdentityAndKeepsOther202Kinds(t *testing.T) {
	const key = "12345678-1234-1234-1234-123456789abc"
	var forwarded atomic.Int64
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { forwarded.Add(1); w.WriteHeader(204) }))
	defer other.Close()
	for _, kind := range []string{"pending", "job_creation", "natural_pending", "foreign_url", "different_id"} {
		t.Run(kind, func(t *testing.T) {
			var polls atomic.Int64
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if r.Method == "GET" {
					if r.URL.Path != "/api/v1/operations/"+key {
						t.Error("poll changed original operation target", r.URL.Path)
					}
					polls.Add(1)
					fmt.Fprint(w, `{"data":{"state":"completed","original_status":202,"result_metadata":{"job_id":"original-job"}}}`)
					return
				}
				w.WriteHeader(202)
				if kind == "job_creation" {
					fmt.Fprint(w, `{"data":{"job_id":"original-job"}}`)
					return
				}
				id, path := key, "/api/v1/operations/"+key
				if kind == "foreign_url" {
					path = other.URL + path
				}
				if kind == "different_id" {
					id = "87654321-1234-1234-1234-123456789abc"
				}
				json.NewEncoder(w).Encode(map[string]any{"data": map[string]string{"state": "accepted", "operation_id": id, "operation_url": path}})
			}))
			defer server.Close()
			api, e := New(server.URL, "fixture", testCredential(t, server.URL))
			if e != nil {
				t.Fatal(e)
			}
			selectedKey := key
			if kind == "natural_pending" {
				selectedKey = ""
			}
			var body map[string]any
			response, e := api.JSON(context.Background(), "POST", "/api/v1/fixture", selectedKey, map[string]any{}, &body)
			switch kind {
			case "foreign_url", "different_id":
				if !errors.Is(e, transfer.ErrReconcile) || polls.Load() != 0 {
					t.Fatal("invalid pending binding was followed", e, polls.Load())
				}
			case "pending":
				if e != nil || response.Status != 202 || body["job_id"] != "original-job" || polls.Load() != 1 {
					t.Fatal("pending reservation did not return original job creation", e, body, polls.Load())
				}
			case "job_creation":
				if e != nil || body["job_id"] != "original-job" || polls.Load() != 0 {
					t.Fatal("committed job creation was polled as a reservation", e)
				}
			case "natural_pending":
				if e != nil || body["state"] != "accepted" || polls.Load() != 0 {
					t.Fatal("non-K natural pending was changed", e)
				}
			}
		})
	}
	if forwarded.Load() != 0 {
		t.Fatal("server operation_url received request/credential")
	}
}

func testCredential(t *testing.T, endpoint string) Credential {
	t.Helper()
	token, e := sec.Secret()
	if e != nil {
		t.Fatal(e)
	}
	return Credential{Endpoint: endpoint, Profile: "fixture", LibraryID: "labelled-library", Token: token}
}
func TestCredentialOriginProfileBindingAndRedirectNeverForward(t *testing.T) {
	var forwarded atomic.Int64
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			forwarded.Add(1)
		}
		w.WriteHeader(204)
	}))
	defer other.Close()
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, other.URL+"/api/v1/private", 302) }))
	defer source.Close()
	credential := testCredential(t, source.URL)
	api, e := New(source.URL, "fixture", credential)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = api.Call(context.Background(), "GET", "/api/v1/private", "", nil, nil); e == nil {
		t.Fatal("redirect followed")
	}
	if forwarded.Load() != 0 {
		t.Fatal("bearer reached redirect target")
	}
	if _, e = New(other.URL, "fixture", credential); e == nil {
		t.Fatal("endpoint change widened credential")
	}
	if _, e = New(source.URL, "other-profile", credential); e == nil {
		t.Fatal("profile binding ignored")
	}
	path := filepath.Join(t.TempDir(), "selected.json")
	b, _ := json.Marshal(credential)
	if e = os.WriteFile(path, b, 0600); e != nil {
		t.Fatal(e)
	}
	if _, e = LoadCredential(path); e != nil {
		t.Fatal(e)
	}
	link := path + ".link"
	if e = os.Symlink(path, link); e != nil {
		t.Fatal(e)
	}
	if _, e = LoadCredential(link); e == nil {
		t.Fatal("symlink credential accepted")
	}
	if e = os.Chmod(path, 0644); e != nil {
		t.Fatal(e)
	}
	if _, e = LoadCredential(path); e == nil {
		t.Fatal("public credential accepted")
	}
}
func TestVerifiedDownloadResumesExactRangeAfterInterruptedBody(t *testing.T) {
	content := []byte(strings.Repeat("exact-stream-", 10000))
	hash := sha256.Sum256(content)
	representation := "sha-256=:" + base64.StdEncoding.EncodeToString(hash[:]) + ":"
	var calls atomic.Int64
	var ranged atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		call := calls.Add(1)
		if call == 1 {
			connection, buffer, e := w.(http.Hijacker).Hijack()
			if e != nil {
				t.Error(e)
				return
			}
			defer connection.Close()
			fmt.Fprintf(buffer, "HTTP/1.1 200 OK\r\nContent-Type: application/octet-stream\r\nContent-Length: %d\r\nETag: \"immutable\"\r\nRepr-Digest: %s\r\nHaeryong-Resolved-Path: first.html\r\n\r\n", len(content), representation)
			buffer.Write(content[:4000])
			buffer.Flush()
			return
		}
		if r.Header.Get("Range") != "bytes=4000-" || r.Header.Get("If-Range") != "\"immutable\"" {
			t.Errorf("lost stable range validator")
		}
		ranged.Store(true)
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("ETag", "\"immutable\"")
		w.Header().Set("Repr-Digest", representation)
		w.Header().Set("Haeryong-Resolved-Path", "first.html")
		w.Header().Set("Content-Range", fmt.Sprintf("bytes 4000-%d/%d", len(content)-1, len(content)))
		w.Header().Set("Content-Length", fmt.Sprint(len(content)-4000))
		w.WriteHeader(206)
		w.Write(content[4000:])
	}))
	defer server.Close()
	api, e := New(server.URL, "fixture", testCredential(t, server.URL))
	if e != nil {
		t.Fatal(e)
	}
	target := filepath.Join(t.TempDir(), "first.html")
	if _, _, e = api.Download(context.Background(), "version-fixture", "first.html", target, false); e == nil {
		t.Fatal("interruption reported complete")
	}
	if _, e = os.Stat(target); !os.IsNotExist(e) {
		t.Fatal("partial output published")
	}
	result, _, e := api.Download(context.Background(), "version-fixture", "first.html", target, false)
	if e != nil {
		t.Fatal(e)
	}
	actual, e := os.ReadFile(target)
	if e != nil || !strings.EqualFold(result.SHA256, fmt.Sprintf("%x", hash)) || string(actual) != string(content) || !result.Resumed || !ranged.Load() {
		t.Fatal("resumed output differed", e)
	}
	if _, _, e = api.Download(context.Background(), "version-fixture", "first.html", target, false); e == nil {
		t.Fatal("existing destination overwritten")
	}
}
