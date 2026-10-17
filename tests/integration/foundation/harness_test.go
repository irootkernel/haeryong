package foundation_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"path/filepath"
	"testing"

	"haeryong.local/haeryong/internal/adapters/content/staging"
	"haeryong.local/haeryong/internal/adapters/foundation"
	auth "haeryong.local/haeryong/internal/application/auth"
	importer "haeryong.local/haeryong/internal/application/import"
	"haeryong.local/haeryong/internal/application/service"
	"haeryong.local/haeryong/internal/domain"
)

type fixture struct {
	ctx       context.Context
	selection service.Selection
	writer    *service.LockedProfile
	store     *foundation.Store
	auth      *auth.Service
	core      *importer.Service
	token     string
	project   string
	closeDB   func() error
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	f := &fixture{ctx: context.Background(), selection: service.Selection{Profile: "foundation", DataDir: filepath.Join(t.TempDir(), "library")}}
	if _, err := service.Initialize(f.selection); err != nil {
		t.Fatal(err)
	}
	f.reopen(t)
	t.Cleanup(func() { f.close(t) })
	p, err := f.auth.CreatePairing(f.ctx, "http://127.0.0.1:9270", "synthetic foundation fixture", "test")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.auth.BootstrapDecide(f.ctx, domain.PairingDecision{ID: p.ID, Code: p.Code, Origin: p.Origin, ETag: p.ETag, OperationID: key(t), Action: "approve"}); err != nil {
		t.Fatal(err)
	}
	poll, err := f.auth.Poll(f.ctx, p.ID, p.PollingSecret, p.Origin)
	if err != nil {
		t.Fatal(err)
	}
	f.token = poll.Token
	return f
}

func (f *fixture) close(t *testing.T) {
	t.Helper()
	if f.closeDB != nil {
		if err := f.closeDB(); err != nil {
			t.Error(err)
		}
		f.closeDB = nil
	}
	if f.writer != nil {
		if err := f.writer.Close(); err != nil {
			t.Error(err)
		}
		f.writer = nil
	}
}

func (f *fixture) reopen(t *testing.T) {
	t.Helper()
	f.close(t)
	var err error
	f.writer, err = service.OpenWriter(f.selection)
	if err != nil {
		t.Fatal(err)
	}
	if err = f.writer.PrepareStorageBootstrap(nil); err != nil {
		t.Fatal(err)
	}
	f.store = foundation.New(nil)
	_, f.closeDB, err = f.store.Prepare(f.ctx, f.writer.Profile)
	if err != nil {
		t.Fatal(err)
	}
	if err = f.writer.MarkStorageReady(nil); err != nil {
		t.Fatal(err)
	}
	f.auth = auth.New(f.store)
	f.core = importer.New(f.store, f.auth, f.store, staging.New())
	health, err := f.store.Health(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	f.project = health.Library.UnfiledProjectID
}

func key(t *testing.T) string {
	t.Helper()
	id, err := service.NewID()
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func responseID(t *testing.T, response domain.Response, field string) string {
	t.Helper()
	var data map[string]json.RawMessage
	var value string
	if err := json.Unmarshal(response.Body, &data); err != nil || json.Unmarshal(data[field], &value) != nil || value == "" {
		t.Fatalf("missing %s in typed response", field)
	}
	return value
}

func digest(data []byte) string {
	hash := sha256.Sum256(data)
	return hex.EncodeToString(hash[:])
}

func (f *fixture) create(t *testing.T) string {
	t.Helper()
	target, _ := json.Marshal(map[string]any{"project_id": f.project, "document_id": nil})
	r, err := f.core.Create(f.ctx, f.token, key(t), importer.CreateRequest{Purpose: "result", InputMode: "directory", SelectionName: "foundation tree", Target: target})
	if err != nil {
		t.Fatal(err)
	}
	return responseID(t, r, "upload_id")
}

func (f *fixture) put(t *testing.T, upload string, data []byte) domain.Checkpoint {
	t.Helper()
	size := int64(len(data))
	ack, err := f.core.Put(f.ctx, f.token, upload, importer.Chunk{Path: "nested/한글.html", Kind: "file", Hash: digest(data), Length: &size, Final: true}, bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	return ack.Checkpoint
}

func (f *fixture) complete(t *testing.T, upload string, checkpoint domain.Checkpoint, operation string) string {
	t.Helper()
	checkpoint.Generation++
	checkpoint.Complete = true
	r, err := f.core.Complete(f.ctx, f.token, upload, operation, checkpoint)
	if err != nil {
		t.Fatal(err)
	}
	return responseID(t, r, "job_id")
}
