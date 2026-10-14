package authingress

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"net/http"
)

func browserHandler(s *store, bootstrap string, hosts, origins map[string]bool, page []byte) http.Handler {
	base := handler(s, bootstrap, hosts, origins)
	_, rest, _ := bytes.Cut(page, []byte("<script>"))
	script, _, _ := bytes.Cut(rest, []byte("</script>"))
	digest := sha256.Sum256(script)
	policy := "default-src 'none'; script-src 'sha256-" + base64.StdEncoding.EncodeToString(digest[:]) + "'; connect-src 'self'; img-src 'self' data:; frame-src 'self'; object-src 'none'; base-uri 'none'; frame-ancestors 'none'"
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		// A synthetic opaque child for the auth boundary check, not the directory renderer.
		if r.URL.Path == "/probe-child" && r.Method == "GET" && hosts[r.Host] {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.Header().Set("Cache-Control", "no-store")
			w.Header().Set("Referrer-Policy", "no-referrer")
			w.Header().Set("Content-Security-Policy", "sandbox allow-scripts; default-src 'none'; script-src 'unsafe-inline'; connect-src http: https:; frame-ancestors 'self'; base-uri 'none'; object-src 'none'")
			w.Write([]byte(`<!doctype html><title>Opaque authentication boundary fixture</title><body>Not the production renderer<script>
window.fixtureBoundary={};
for(const [name,read] of Object.entries({hostDOM:()=>parent.document.body.textContent,hostStorage:()=>parent.localStorage.getItem('haeryong.auth-ingress.v1'),hostScript:()=>parent.eval('bearer'),ownStorage:()=>localStorage.length})){
 try{read();window.fixtureBoundary[name]=true;}catch{window.fixtureBoundary[name]=false;}
}
for(const path of ['/pair/decision','/devices/revoke'])fetch(path,{method:'POST',mode:'no-cors',headers:{'Content-Type':'text/plain'},body:'{}'}).catch(()=>{});
</script>`))
			return
		}
		if (r.URL.Path == "/" || r.URL.Path == "/connect" || r.URL.Path == "/reader") && r.Method == "GET" && hosts[r.Host] {
			w.Header().Set("Content-Security-Policy", policy)
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.Header().Set("Cache-Control", "no-store")
			w.Header().Set("Referrer-Policy", "no-referrer")
			w.Write(page)
			return
		}
		base.ServeHTTP(w, r)
	})
}
