package client

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestCompletePartialRecoversRenameCollisionAfterCurrentHead(t *testing.T) {
	for _, deny := range []bool{false, true} {
		t.Run(fmt.Sprint("current_auth_denied=", deny), func(t *testing.T) {
			body := strings.Repeat("verified-original-", 4096)
			hash := sha256.Sum256([]byte(body))
			header := "sha-256=:" + base64.StdEncoding.EncodeToString(hash[:]) + ":"
			destination := filepath.Join(t.TempDir(), "result.html")
			var first atomic.Bool
			var head atomic.Bool
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if first.CompareAndSwap(false, true) {
					if e := os.WriteFile(destination, []byte("unrelated destination"), 0600); e != nil {
						t.Error(e)
					}
				} else if deny {
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(401)
					fmt.Fprint(w, `{"error":{"code":"SESSION_REVOKED"}}`)
					return
				}
				if r.Method == "HEAD" {
					head.Store(true)
				}
				w.Header().Set("Content-Type", "application/octet-stream")
				w.Header().Set("ETag", "\"fixed-immutable\"")
				w.Header().Set("Repr-Digest", header)
				w.Header().Set("Haeryong-Resolved-Path", "first.html")
				http.ServeContent(w, r, "first.html", time.Time{}, strings.NewReader(body))
			}))
			defer server.Close()
			api, e := New(server.URL, "fixture", testCredential(t, server.URL))
			if e != nil {
				t.Fatal(e)
			}
			if _, _, e = api.Download(context.Background(), "version-fixture", "first.html", destination, false); e == nil {
				t.Fatal("real no-replace collision did not fail")
			}
			original, e := os.ReadFile(destination)
			if e != nil || string(original) != "unrelated destination" {
				t.Fatal("collision overwrote unrelated file", e)
			}
			partials, e := filepath.Glob(filepath.Join(filepath.Dir(destination), "*.partial"))
			if e != nil || len(partials) != 1 {
				t.Fatal("complete verified partial was not retained", e)
			}
			info, e := os.Stat(partials[0])
			if e != nil || info.Size() != int64(len(body)) {
				t.Fatal("counterexample is not a full partial", e)
			}
			if e = os.Remove(destination); e != nil {
				t.Fatal(e)
			}
			result, _, e := api.Download(context.Background(), "version-fixture", "first.html", destination, false)
			if deny {
				if e == nil {
					t.Fatal("revoked current authority published local partial")
				}
				if _, e = os.Stat(destination); !os.IsNotExist(e) {
					t.Fatal("unauthorized partial published")
				}
				return
			}
			if e != nil {
				t.Fatal("complete partial could not recover after actual rename collision", e)
			}
			actual, e := os.ReadFile(destination)
			if e != nil || string(actual) != body || !result.Resumed || !head.Load() {
				t.Fatal("recovered output/current HEAD proof missing", e)
			}
		})
	}
}
