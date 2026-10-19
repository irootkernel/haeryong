package main

import (
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// The exact-host and supplied-origin guards run BEFORE any static or SPA
// serving, every reply (including the early rejects) is no-store, and no
// CORS header is ever emitted.
func TestCompanionStaticHandlerHostOriginGuards(t *testing.T) {
	dist := t.TempDir()
	if err := os.WriteFile(filepath.Join(dist, "index.html"), []byte("<html>entry</html>"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dist, "assets"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dist, "assets", "app.js"), []byte("console.log(1)"), 0600); err != nil {
		t.Fatal(err)
	}
	const address = "127.0.0.1:9271"
	const origin = "http://127.0.0.1:9271"
	handler := companionStaticHandler(dist, address, origin)

	serve := func(host, suppliedOrigin, path string) *httptest.ResponseRecorder {
		request := httptest.NewRequest("GET", origin+path, nil)
		request.Host = host
		if suppliedOrigin != "" {
			request.Header.Set("Origin", suppliedOrigin)
		}
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, request)
		return recorder
	}

	exact := serve(address, "", "/assets/app.js")
	if exact.Code != 200 || exact.Body.String() != "console.log(1)" {
		t.Fatalf("exact host must serve the asset, got %d %q", exact.Code, exact.Body.String())
	}
	if exact.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("every reply must carry no-store")
	}

	sameOrigin := serve(address, origin, "/assets/app.js")
	if sameOrigin.Code != 200 {
		t.Fatalf("the exact supplied origin must be served, got %d", sameOrigin.Code)
	}

	foreignOrigin := serve(address, "http://evil.example", "/assets/app.js")
	if foreignOrigin.Code != 403 {
		t.Fatalf("a foreign supplied origin must be refused before serving, got %d", foreignOrigin.Code)
	}
	if foreignOrigin.Body.String() == "console.log(1)" {
		t.Fatal("no trusted byte may be delivered to a foreign origin request")
	}
	if foreignOrigin.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("the origin reject reply must also carry no-store")
	}

	wrongHost := serve("localhost:9271", "", "/assets/app.js")
	if wrongHost.Code != 404 {
		t.Fatalf("an inexact host must be refused before serving, got %d", wrongHost.Code)
	}
	if wrongHost.Body.String() == "console.log(1)" {
		t.Fatal("no trusted byte may be delivered to an inexact host request")
	}

	for _, path := range []string{"/", "/documents/some-route", "/documents/some-route/"} {
		spa := serve(address, "", path)
		if spa.Code != 200 || !strings.Contains(spa.Body.String(), "entry") {
			t.Fatalf("SPA fallback at %s must serve the compiled entry document, got %d %q", path, spa.Code, spa.Body.String())
		}
	}
	spa := serve(address, "", "/documents/some-route")

	for _, recorder := range []*httptest.ResponseRecorder{exact, sameOrigin, foreignOrigin, wrongHost, spa} {
		for key := range recorder.Header() {
			if strings.HasPrefix(strings.ToLower(key), "access-control-") {
				t.Fatalf("no CORS header may ever be emitted, saw %s", key)
			}
		}
	}
}

// The pinned SDK reflects every exported method of its bound receiver.
// Embedding the facade would silently expose its harness helpers too.
func TestCompanionProductionBindingSurface(t *testing.T) {
	typ := reflect.TypeFor[*companionBindings]()
	if typ.NumMethod() != 1 || typ.Method(0).Name != "Invoke" {
		var names []string
		for index := 0; index < typ.NumMethod(); index++ {
			names = append(names, typ.Method(index).Name)
		}
		t.Fatalf("production reflected binding surface must be only Invoke, got %v", names)
	}
}
