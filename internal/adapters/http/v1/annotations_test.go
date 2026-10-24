package v1

import (
	"context"
	"encoding/json"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	httpauth "haeryong.local/haeryong/internal/adapters/http/auth"
	httpimports "haeryong.local/haeryong/internal/adapters/http/imports"
	"haeryong.local/haeryong/internal/application/query"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"time"
)

var _ = It("Annotations B-1 current-context stream stops on owner revocation", Label("persistence"), func() {
	f := newFixture(GinkgoT())
	u := f.create()
	f.put(u, "hello.html", "file", []byte("<h1>Note context</h1>"))
	v := f.publish(u, nil)
	body, _ := json.Marshal(map[string]any{"page_path": "hello.html", "region_id": nil, "page_query": "", "page_fragment": "", "body": "note", "body_ref": nil, "selected_text": nil, "context": strings.Repeat("context 🌊", 40000), "context_ref": nil})
	response, e := f.query.AnnotationMutate(context.Background(), f.token, id(GinkgoT()), "POST", v.ID, "", body)
	Expect(e).NotTo(HaveOccurred())
	var note query.AnnotationView
	Expect(json.Unmarshal(response.Body, &note)).To(Succeed())
	actor, e := f.auth.Authenticate(context.Background(), f.token)
	Expect(e).NotTo(HaveOccurred())
	server := httptest.NewUnstartedServer(nil)
	origin := "http://" + server.Listener.Addr().String()
	handler, e := httpauth.New(f.auth, httpauth.Policy{Hosts: []string{server.Listener.Addr().String()}, Origins: []string{origin}}, routes{New(f.query, nil, nil), httpimports.New(f.imports)})
	Expect(e).NotTo(HaveOccurred())
	revoked := make(chan error, 1)
	server.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handler.ServeHTTP(&packageRevokingWriter{ResponseWriter: w, afterFirstWrite: func() { _, e := f.auth.Revoke(context.Background(), f.token, actor.ID); revoked <- e }}, r)
	})
	server.Start()
	DeferCleanup(server.Close)
	request, e := http.NewRequest("GET", origin+"/api/v1/annotations/"+note.AnnotationID+"/context?revision=1", nil)
	Expect(e).NotTo(HaveOccurred())
	request.Header.Set("Authorization", "Bearer "+f.token)
	request.Header.Set("Origin", origin)
	result, e := (&http.Client{Timeout: 30 * time.Second}).Do(request)
	Expect(e).NotTo(HaveOccurred())
	read, err := io.ReadAll(result.Body)
	result.Body.Close()
	Expect(<-revoked).To(Succeed())
	Expect(result.StatusCode).To(Equal(200))
	Expect(len(read)).To(BeNumerically(">", 0))
	Expect(len(read)).To(BeNumerically("<", len(strings.Repeat("context 🌊", 40000))))
	Expect(err).To(HaveOccurred())
})
