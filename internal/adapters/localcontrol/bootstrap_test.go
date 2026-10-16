package localcontrol

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"haeryong.local/haeryong/internal/adapters/foundation"
	appauth "haeryong.local/haeryong/internal/application/auth"
	"haeryong.local/haeryong/internal/application/service"
)

func TestBootstrapHMACBindsExactDecisionIntent(t *testing.T) {
	selection := service.Selection{Profile: "bootstrap", DataDir: filepath.Join(t.TempDir(), "library")}
	if _, e := service.Initialize(selection); e != nil {
		t.Fatal(e)
	}
	writer, e := service.OpenWriter(selection)
	if e != nil {
		t.Fatal(e)
	}
	defer writer.Close()
	if e := writer.PrepareStorageBootstrap(nil); e != nil {
		t.Fatal(e)
	}
	store := foundation.New(nil)
	_, closeStore, e := store.Prepare(context.Background(), writer.Profile)
	if e != nil {
		t.Fatal(e)
	}
	defer closeStore()
	core := appauth.New(store)
	p, e := core.CreatePairing(context.Background(), "http://127.0.0.1:9270", "exact", "reliable-client")
	if e != nil {
		t.Fatal(e)
	}
	id := service.Identity{InstanceID: "test-instance"}
	endpoint := &HTTP{Auth: core}
	mux := http.NewServeMux()
	endpoint.mountBootstrap(mux, writer.Profile, id)
	op, _ := service.NewID()
	path := pairingsPath + "/" + p.ID + "/approve"
	body, _ := json.Marshal(map[string]string{"code": p.Code, "origin": p.Origin})
	nonce := strings.Repeat("ab", 32)
	proof := bootstrapProof(writer.Profile.ControlKey, nonce, id.InstanceID, "POST", path, op, p.ETag, body)
	for _, change := range []string{"body", "path", "operation", "etag", "lifecycle-proof", "instance"} {
		actualPath, actualBody, actualOp, actualETag, actualProof := path, body, op, p.ETag, proof
		switch change {
		case "body":
			actualBody = append(append([]byte{}, body...), byte(' '))
		case "path":
			actualPath = strings.TrimSuffix(path, "approve") + "reject"
		case "operation":
			actualOp, _ = service.NewID()
		case "etag":
			actualETag = "\"another\""
		case "lifecycle-proof":
			actualProof = requestProof(writer.Profile.ControlKey, nonce, "POST", path, id.InstanceID)
		case "instance":
			actualProof = bootstrapProof(writer.Profile.ControlKey, nonce, "previous-instance", "POST", path, op, p.ETag, body)
		}
		r := httptest.NewRequest("POST", actualPath, bytes.NewReader(actualBody))
		r.Header.Set("X-Haeryong-Nonce", nonce)
		r.Header.Set("X-Haeryong-Proof", actualProof)
		r.Header.Set("Idempotency-Key", actualOp)
		r.Header.Set("If-Match", actualETag)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		if w.Code != 401 {
			t.Fatalf("%s intent substitution HTTP %d", change, w.Code)
		}
	}
	r := httptest.NewRequest("POST", path, bytes.NewReader(body))
	r.Header.Set("X-Haeryong-Nonce", nonce)
	r.Header.Set("X-Haeryong-Proof", proof)
	r.Header.Set("Idempotency-Key", op)
	r.Header.Set("If-Match", p.ETag)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatalf("exact intent HTTP %d", w.Code)
	}
	if w.Header().Get("X-Haeryong-Proof") != bootstrapResponseProof(writer.Profile.ControlKey, nonce, w.Code, w.Body.Bytes()) {
		t.Fatal("response not proved")
	}
}
