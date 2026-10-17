package v1

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	app "haeryong.local/haeryong/internal/application/storage"
	"os"
	"path/filepath"
)

var _ = It("TestActualHTTPStoredCorruptionWithholdsFileAndMetadataAndReleasesPins", Label("persistence"), func() {
	t := GinkgoT()

	f := newFixture(t)
	u := f.create()
	payload := []byte("<html>original immutable bytes</html>")
	f.put(u, "original.html", "file", payload)
	v := f.publish(u, map[string]any{"document": map[string]any{"title": "Stored integrity fixture"}})
	sum := sha256.Sum256(payload)
	base := "/api/v1/versions/" + v.ID
	cases := []struct {
		name, hash, endpoint string
		sizeMismatch         bool
	}{{"file", hex.EncodeToString(sum[:]), base + "/file?path=original.html", false}, {"normalized_metadata", v.ManifestSHA, base + "/metadata?kind=normalized", true}, {"supplied_metadata", v.MetadataHash, base + "/metadata?kind=supplied", false}}
	// An ordinary staging carrier references these same exact hashes, making all
	// active transient read pins inspectable through the existing typed ledger.
	carrier := f.create()
	for _, test := range cases {
		objectPath := filepath.Join(f.writer.Profile.DataDir, "storage", "objects", test.hash)
		original, err := os.ReadFile(objectPath)
		Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })

		f.put(carrier, test.name+".bin", "file", original)
		func() {
			By(test.name)
			corrupt := bytes.Repeat([]byte("!"), len(original))
			if test.sizeMismatch {
				corrupt = original[:len(original)/2]
			}
			{
				err := os.WriteFile(objectPath, corrupt, 0600)
				Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })
			}

			defer func() {
				{
					err := os.WriteFile(objectPath, original, 0600)
					Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })
				}

			}()
			status, headers, data := f.request("GET", test.endpoint, nil, nil)
			want(t, status, 503, data)
			var response struct {
				Data  json.RawMessage `json:"data"`
				Error struct {
					Code string `json:"code"`
				} `json:"error"`
			}
			{
				err := json.Unmarshal(data, &response)
				Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })
			}
			Expect(response.Error.Code != "CONTENT_INTEGRITY_ERROR").To(BeFalse(), func() string {
				return fmt.Sprintf("stored verified-object failure code=%s want CONTENT_INTEGRITY_ERROR", response.Error.Code)
			})
			Expect(len(response.Data) != 0 || bytes.Contains(data, payload) || headers.Get("Content-Digest") != "" || headers.Get("Repr-Digest") != "").To(BeFalse(), func() string { return fmt.Sprint("failed verification exposed a successful payload or digest") })
			{

				err := f.store.Write(context.Background(), func(tx app.WriteTx) error {
					pins, err := tx.UploadPins(carrier, "", 256)
					if err != nil {
						return err
					}
					for _, pin := range pins {
						if pin.Hash == test.hash && pin.Kind == "read" {
							Fail(fmt.Sprint("failed stored-object verification leaked transient read pin"))
						}
					}
					return nil
				})
				Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })
			}

		}()
	}
})
