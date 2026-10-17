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

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"time"
)

var _ = It("TestCompletePartialRecoversRenameCollisionAfterCurrentHead", Label("integration"), func() {
	t := GinkgoT()

	for _, deny := range []bool{false, true} {
		func() {
			By(fmt.Sprint("current_auth_denied=", deny))
			body := strings.Repeat("verified-original-", 4096)
			hash := sha256.Sum256([]byte(body))
			header := "sha-256=:" + base64.StdEncoding.EncodeToString(hash[:]) + ":"
			destination := filepath.Join(t.TempDir(), "result.html")
			var first atomic.Bool
			var head atomic.Bool
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if first.CompareAndSwap(false, true) {
					{
						e := os.WriteFile(destination, []byte("unrelated destination"), 0600)
						Expect(e != nil).To(BeFalse(), func() string { return fmt.Sprint(e) })
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
			Expect(e != nil).To(BeFalse(), func() string { return fmt.Sprint(e) })
			{

				_, _, e = api.Download(context.Background(), "version-fixture", "first.html", destination, false)
				Expect(e == nil).To(BeFalse(), func() string { return fmt.Sprint("real no-replace collision did not fail") })
			}

			original, e := os.ReadFile(destination)
			Expect(e != nil || string(original) != "unrelated destination").To(BeFalse(), func() string { return fmt.Sprint("collision overwrote unrelated file", e) })

			partials, e := filepath.Glob(filepath.Join(filepath.Dir(destination), "*.partial"))
			Expect(e != nil || len(partials) != 1).To(BeFalse(), func() string { return fmt.Sprint("complete verified partial was not retained", e) })

			info, e := os.Stat(partials[0])
			Expect(e != nil || info.Size() != int64(len(body))).To(BeFalse(), func() string { return fmt.Sprint("counterexample is not a full partial", e) })
			{

				e = os.Remove(destination)
				Expect(e != nil).To(BeFalse(), func() string { return fmt.Sprint(e) })
			}

			result, _, e := api.Download(context.Background(), "version-fixture", "first.html", destination, false)
			if deny {
				Expect(e == nil).To(BeFalse(), func() string { return fmt.Sprint("revoked current authority published local partial") })
				{

					_, e = os.Stat(destination)
					Expect(!os.IsNotExist(e)).To(BeFalse(), func() string { return fmt.Sprint("unauthorized partial published") })
				}

				return
			}
			Expect(e != nil).To(BeFalse(), func() string {
				return fmt.Sprint("complete partial could not recover after actual rename collision", e)
			})

			actual, e := os.ReadFile(destination)
			Expect(e != nil || string(actual) != body || !result.Resumed || !head.Load()).To(BeFalse(), func() string { return fmt.Sprint("recovered output/current HEAD proof missing", e) })

		}()
	}
})
