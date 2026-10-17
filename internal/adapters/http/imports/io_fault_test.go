package imports

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"syscall"

	"fmt"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"haeryong.local/haeryong/internal/adapters/contentstore"
	app "haeryong.local/haeryong/internal/application/storage"
	"haeryong.local/haeryong/internal/domain"
)

var _ = It("TestActualHTTPResourceFaultNeverAcknowledgesUndurableRange", Label("persistence"), func() {
	t := GinkgoT()

	for _, test := range []struct {
		name     string
		boundary contentstore.Boundary
		err      error
		code     string
	}{{"disk-full", contentstore.Write, syscall.ENOSPC, "RESOURCE_EXHAUSTED"}, {"file-sync-io", contentstore.FileSync, syscall.EIO, "FILE_ACCESS_ERROR"}} {
		func() {
			By(test.name)
			f := newHTTPFixture(t)
			id := f.upload("directory", "injected I/O failure")
			original := f.store.Objects
			injected, err := contentstore.New(filepath.Join(f.writer.Profile.DataDir, "storage"), contentstore.WithFault(func(boundary contentstore.Boundary) error {
				if boundary == test.boundary {
					return test.err
				}
				return nil
			}))
			Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })

			f.store.Objects = injected
			defer func() { f.store.Objects = original; injected.Close() }()
			status, _, data := f.put(id, "file", "file", []byte("never acknowledged"), 0, true)
			requireStatus(t, status, 503, data)
			var failure struct {
				Error struct {
					Code      string `json:"code"`
					Retryable bool   `json:"retryable"`
				} `json:"error"`
			}
			Expect(json.Unmarshal(data, &failure) != nil || failure.Error.Code != test.code || !failure.Error.Retryable).To(BeFalse(), func() string { return fmt.Sprint("actual resource failure diagnosis lost") })
			{

				detail := f.detail(id)
				Expect(detail.State != "open" || detail.Checkpoint.AcceptedBytes != "0").To(BeFalse(), func() string { return fmt.Sprint("undurable bytes acknowledged") })
			}

			hash := sha256.Sum256([]byte("never acknowledged"))
			err = f.store.Write(context.Background(), func(tx app.WriteTx) error { _, err := tx.Object(hex.EncodeToString(hash[:])); return err })
			Expect(!errors.Is(err, domain.ErrNotFound)).To(BeFalse(), func() string { return fmt.Sprint("failed flush created READY object") })

		}()
	}
})

type interruptedBody struct{ done bool }

func (r *interruptedBody) Read(p []byte) (int, error) {
	if r.done {
		return 0, io.ErrUnexpectedEOF
	}
	r.done = true
	return copy(p, "prefix"), nil
}

var _ = It("TestActualHandlerInterruptedBodyReportsRecoverableTransfer", Label("persistence"), func() {
	t := GinkgoT()

	f := newHTTPFixture(t)
	id := f.upload("directory", "interrupted body")
	request := httptest.NewRequest("PUT", f.server.URL+"/api/v1/uploads/"+id+"/entries?path=file&kind=file", &interruptedBody{})
	request.Header.Set("Origin", f.server.URL)
	request.Header.Set("Authorization", "Bearer "+f.token)
	request.Header.Set("Content-Type", "application/octet-stream")
	request.Header.Set("Haeryong-Upload-Offset", "0")
	request.Header.Set("Haeryong-Upload-Chunk-SHA256", strings.Repeat("0", 64))
	request.Header.Set("Haeryong-Upload-Final", "true")
	recorder := httptest.NewRecorder()
	f.server.Config.Handler.ServeHTTP(recorder, request)
	response := recorder.Result()
	defer response.Body.Close()
	data, err := io.ReadAll(response.Body)
	Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })

	requireStatus(t, response.StatusCode, 503, data)
	var failure struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	Expect(json.Unmarshal(data, &failure) != nil || failure.Error.Code != "TRANSFER_INTERRUPTED").To(BeFalse(), func() string { return fmt.Sprint("interrupted input was reported as complete/outage") })
	Expect(f.detail(id).Checkpoint.AcceptedBytes != "0").To(BeFalse(), func() string { return fmt.Sprint("interrupted range acknowledged") })

})
