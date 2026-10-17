package v1

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	app "haeryong.local/haeryong/internal/application/storage"
	"os"
	"path/filepath"
	"testing"
)

func TestActualHTTPStoredCorruptionWithholdsFileAndMetadataAndReleasesPins(t *testing.T) {
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
		if err != nil {
			t.Fatal(err)
		}
		f.put(carrier, test.name+".bin", "file", original)
		t.Run(test.name, func(t *testing.T) {
			corrupt := bytes.Repeat([]byte("!"), len(original))
			if test.sizeMismatch {
				corrupt = original[:len(original)/2]
			}
			if err := os.WriteFile(objectPath, corrupt, 0600); err != nil {
				t.Fatal(err)
			}
			defer func() {
				if err := os.WriteFile(objectPath, original, 0600); err != nil {
					t.Error(err)
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
			if err := json.Unmarshal(data, &response); err != nil {
				t.Fatal(err)
			}
			if response.Error.Code != "CONTENT_INTEGRITY_ERROR" {
				t.Errorf("stored verified-object failure code=%s want CONTENT_INTEGRITY_ERROR", response.Error.Code)
			}
			if len(response.Data) != 0 || bytes.Contains(data, payload) || headers.Get("Content-Digest") != "" || headers.Get("Repr-Digest") != "" {
				t.Error("failed verification exposed a successful payload or digest")
			}
			if err := f.store.Write(context.Background(), func(tx app.WriteTx) error {
				pins, err := tx.UploadPins(carrier, "", 256)
				if err != nil {
					return err
				}
				for _, pin := range pins {
					if pin.Hash == test.hash && pin.Kind == "read" {
						t.Error("failed stored-object verification leaked transient read pin")
					}
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
		})
	}
}
