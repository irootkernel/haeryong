package foundation_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"path/filepath"

	"fmt"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
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

func newFixture(t GinkgoTInterface) *fixture {
	GinkgoHelper()
	f := &fixture{ctx: context.Background(), selection: service.Selection{Profile: "foundation", DataDir: filepath.Join(t.TempDir(), "library")}}
	{
		_, err := service.Initialize(f.selection)
		Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })
	}

	f.reopen(t)
	DeferCleanup(func() { f.close(t) })
	p, err := f.auth.CreatePairing(f.ctx, "http://127.0.0.1:9270", "synthetic foundation fixture", "test")
	Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })
	{

		_, err = f.auth.BootstrapDecide(f.ctx, domain.PairingDecision{ID: p.ID, Code: p.Code, Origin: p.Origin, ETag: p.ETag, OperationID: key(t), Action: "approve"})
		Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })
	}

	poll, err := f.auth.Poll(f.ctx, p.ID, p.PollingSecret, p.Origin)
	Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })

	f.token = poll.Token
	return f
}

func (f *fixture) close(t GinkgoTInterface) {
	GinkgoHelper()
	if f.closeDB != nil {
		{
			err := f.closeDB()
			Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })
		}

		f.closeDB = nil
	}
	if f.writer != nil {
		{
			err := f.writer.Close()
			Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })
		}

		f.writer = nil
	}
}

func (f *fixture) reopen(t GinkgoTInterface) {
	GinkgoHelper()
	f.close(t)
	var err error
	f.writer, err = service.OpenWriter(f.selection)
	Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })
	{

		err = f.writer.PrepareStorageBootstrap(nil)
		Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })
	}

	f.store = foundation.New(nil)
	_, f.closeDB, err = f.store.Prepare(f.ctx, f.writer.Profile)
	Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })
	{

		err = f.writer.MarkStorageReady(nil)
		Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })
	}

	f.auth = auth.New(f.store)
	f.core = importer.New(f.store, f.auth, f.store, staging.New())
	health, err := f.store.Health(f.ctx)
	Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })

	f.project = health.Library.UnfiledProjectID
}

func key(t GinkgoTInterface) string {
	GinkgoHelper()
	id, err := service.NewID()
	Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })

	return id
}

func responseID(t GinkgoTInterface, response domain.Response, field string) string {
	GinkgoHelper()
	var data map[string]json.RawMessage
	var value string
	{
		err := json.Unmarshal(response.Body, &data)
		Expect(err != nil || json.Unmarshal(data[field], &value) != nil || value == "").To(BeFalse(), func() string { return fmt.Sprintf("missing %s in typed response", field) })
	}

	return value
}

func digest(data []byte) string {
	hash := sha256.Sum256(data)
	return hex.EncodeToString(hash[:])
}

func (f *fixture) create(t GinkgoTInterface) string {
	GinkgoHelper()
	target, _ := json.Marshal(map[string]any{"project_id": f.project, "document_id": nil})
	r, err := f.core.Create(f.ctx, f.token, key(t), importer.CreateRequest{Purpose: "result", InputMode: "directory", SelectionName: "foundation tree", Target: target})
	Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })

	return responseID(t, r, "upload_id")
}

func (f *fixture) put(t GinkgoTInterface, upload string, data []byte) domain.Checkpoint {
	GinkgoHelper()
	size := int64(len(data))
	ack, err := f.core.Put(f.ctx, f.token, upload, importer.Chunk{Path: "nested/한글.html", Kind: "file", Hash: digest(data), Length: &size, Final: true}, bytes.NewReader(data))
	Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })

	return ack.Checkpoint
}

func (f *fixture) complete(t GinkgoTInterface, upload string, checkpoint domain.Checkpoint, operation string) string {
	GinkgoHelper()
	checkpoint.Generation++
	checkpoint.Complete = true
	r, err := f.core.Complete(f.ctx, f.token, upload, operation, checkpoint)
	Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })

	return responseID(t, r, "job_id")
}
