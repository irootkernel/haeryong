package imports

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"syscall"

	"fmt"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"haeryong.local/haeryong/internal/domain"
)

var _ = It("TestActualHTTPInputFromDifferentMountedFilesystem", Label("persistence"), func() {
	t := GinkgoT()

	f := newHTTPFixture(t)
	var destination syscall.Stat_t
	{
		err := syscall.Stat(f.selection.DataDir, &destination)
		Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })
	}

	var sourcePath string
	var source syscall.Stat_t
	for _, path := range []string{"/System/Library/CoreServices/SystemVersion.plist", "/etc/hosts"} {
		if syscall.Stat(path, &source) == nil && source.Dev != destination.Dev {
			sourcePath = path
			break
		}
	}
	if sourcePath == "" {
		Fail(fmt.Sprint("no existing public input on a different mounted filesystem observed; EXDEV fault proof remains separate"))
	}
	input, err := os.Open(sourcePath)
	Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })

	defer input.Close()
	hash := sha256.New()
	size, err := io.CopyBuffer(hash, input, make([]byte, 64<<10))
	Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })

	digest := hex.EncodeToString(hash.Sum(nil))
	{
		_, err = input.Seek(0, io.SeekStart)
		Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })
	}

	id := f.upload("file", filepath.Base(sourcePath))
	status, _, data := f.request("PUT", "/api/v1/uploads/"+id+"/entries?path=_input&kind=file", input, map[string]string{"Content-Type": "application/octet-stream", "Haeryong-Upload-Offset": "0", "Haeryong-Upload-Chunk-SHA256": digest, "Haeryong-Upload-Final": "true"})
	requireStatus(t, status, 200, data)
	f.seal(id)
	version := f.publish(id, operation(t))
	f.restart()
	entries, err := f.store.Entries(context.Background(), version.ContentSetID, "", 64)
	Expect(err != nil || len(entries) != 1 || entries[0].Path != filepath.Base(sourcePath) || entries[0].Kind != "file").To(BeFalse(), func() string { return fmt.Sprint("cross-volume selected file path/type changed") })

	actual := sha256.New()
	var read int64
	err = f.store.Read(context.Background(), domain.Object{Hash: entries[0].Hash, Size: entries[0].Size, State: "ready"}, domain.Pin{Kind: "active_read", OwnerID: "cross-volume-proof"}, func(r io.ReadSeeker) error {
		var err error
		read, err = io.CopyBuffer(actual, r, make([]byte, 64<<10))
		return err
	})
	Expect(err != nil || read != size || hex.EncodeToString(actual.Sum(nil)) != digest).To(BeFalse(), func() string { return fmt.Sprint("cross-volume input bytes changed") })

	if path := os.Getenv("HAERYONG_TASK004_CROSSVOLUME_REPORT"); path != "" {
		report := map[string]any{"fixture": "actual HTTP client streamed existing public OS file from observed distinct mounted filesystem", "source_path": sourcePath, "source_device": uint64(source.Dev), "destination_device": uint64(destination.Dev), "same_device": false, "byte_size": size, "sha256": digest, "restart_path_type_bytes_equal": true}
		data, err := json.MarshalIndent(report, "", "  ")
		Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })

		file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })

		_, err = file.Write(append(data, '\n'))
		closeErr := file.Close()
		Expect(err != nil || closeErr != nil).To(BeFalse(), func() string { return fmt.Sprint("cross-volume evidence write failed") })

	}
})
