package authingress

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestConnectShellDoesNotReplacePrivateAuthorization(t *testing.T) {
	s := fixture(t)
	h := browserHandler(s, random(), map[string]bool{"127.0.0.1:9270": true}, map[string]bool{"http://127.0.0.1:9270": true}, []byte("Connect shell"))
	for _, c := range []struct {
		host, path string
		status     int
	}{{"127.0.0.1:9270", "/", 200}, {"other.test", "/", 403}, {"127.0.0.1:9270", "/private/data", 401}} {
		r := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:9270"+c.path, nil)
		r.Host = c.host
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != c.status {
			t.Fatal(c, w.Code)
		}
	}
}
