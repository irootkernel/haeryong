package v1

import (
	"context"
	"encoding/json"
	"fmt"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	httpauth "haeryong.local/haeryong/internal/adapters/http/auth"
	httpimports "haeryong.local/haeryong/internal/adapters/http/imports"
	"haeryong.local/haeryong/internal/application/query"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
)

var _ = It("Lifecycle real trash interrupts an active saved context stream without deleting adoption", Label("persistence"), func() {
	f := newFixture(GinkgoT())
	u := f.create()
	f.put(u, "hello.html", "file", []byte("<p>stream lifecycle</p>"))
	v := f.publish(u, nil)
	body, _ := json.Marshal(map[string]any{"page_path": "hello.html", "region_id": nil, "page_query": "", "page_fragment": "", "body": "note", "body_ref": nil, "selected_text": nil, "context": strings.Repeat("private 🌊", 40000), "context_ref": nil})
	response, e := f.query.AnnotationMutate(context.Background(), f.token, id(GinkgoT()), "POST", v.ID, "", body)
	Expect(e).NotTo(HaveOccurred())
	var note query.AnnotationView
	Expect(json.Unmarshal(response.Body, &note)).To(Succeed())
	d, e := f.store.Document(context.Background(), v.DocumentID)
	Expect(e).NotTo(HaveOccurred())
	response, e = f.query.LifecyclePlanCreate(context.Background(), f.token, "", d.ID, []byte(fmt.Sprintf(`{"operation":"trash","expected_revision":%d}`, d.Revision)))
	Expect(e).NotTo(HaveOccurred())
	var p struct {
		ID       string `json:"plan_id"`
		Revision int64  `json:"expected_revision"`
	}
	Expect(json.Unmarshal(response.Body, &p)).To(Succeed())
	server := httptest.NewUnstartedServer(nil)
	origin := "http://" + server.Listener.Addr().String()
	handler, e := httpauth.New(f.auth, httpauth.Policy{Hosts: []string{server.Listener.Addr().String()}, Origins: []string{origin}}, routes{New(f.query, nil, nil), httpimports.New(f.imports)})
	Expect(e).NotTo(HaveOccurred())
	trashed := make(chan error, 1)
	server.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handler.ServeHTTP(&packageRevokingWriter{ResponseWriter: w, afterFirstWrite: func() {
			_, e := f.query.LifecycleCommit(context.Background(), f.token, p.ID, "", []byte(fmt.Sprintf(`{"plan_id":%q,"expected_revision":%d,"confirmed":true}`, p.ID, p.Revision)))
			trashed <- e
		}}, r)
	})
	server.Start()
	DeferCleanup(server.Close)
	request, e := http.NewRequest("GET", origin+"/api/v1/annotations/"+note.AnnotationID+"/context?revision=1", nil)
	Expect(e).NotTo(HaveOccurred())
	request.Header.Set("Authorization", "Bearer "+f.token)
	request.Header.Set("Origin", origin)
	result, e := http.DefaultClient.Do(request)
	Expect(e).NotTo(HaveOccurred())
	read, e := io.ReadAll(result.Body)
	result.Body.Close()
	Expect(<-trashed).To(Succeed())
	Expect(result.StatusCode).To(Equal(200))
	Expect(len(read)).To(BeNumerically(">", 0))
	Expect(len(read)).To(BeNumerically("<", len(strings.Repeat("private 🌊", 40000))))
	Expect(e).To(HaveOccurred())
})
