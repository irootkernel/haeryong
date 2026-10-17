package foundation_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"io/fs"
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	importer "haeryong.local/haeryong/internal/application/import"
	app "haeryong.local/haeryong/internal/application/storage"
	"haeryong.local/haeryong/internal/domain"

	// Reuse the public native-generated G-VALUE corpus, not the unapproved R5 UI
	// sample state. This checks bytes/paths and restart, not new human approval or
	// semantic usefulness. Synthetic corruption belongs to separate regressions.
	"fmt"
	. "github.com/onsi/gomega"
)

var _ = It("TestExistingNativeGeneratedResultDirectoriesPublishAndReopenExactly", Label("persistence"), func() {
	t := GinkgoT()

	f := newFixture(t)
	type publishedTree struct {
		version  string
		entries  map[string]domain.Entry
		fileSize int64
	}
	trees := map[string]publishedTree{}
	for _, name := range []string{"gaori", "mulgae"} {
		project := key(t)
		{
			err := f.store.Write(f.ctx, func(tx app.WriteTx) error {
				return tx.CreateProject(domain.Project{ID: project, Name: "public native corpus " + name, Lifecycle: "active", Revision: 1, PermissionsRevision: 1})
			})
			Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })
		}

		f.project = project
		upload := f.create(t)
		source := filepath.Join("../../../spikes/knowledge-value/samples", name)
		tree := publishedTree{version: key(t), entries: map[string]domain.Entry{}}
		var checkpoint domain.Checkpoint
		err := filepath.WalkDir(source, func(path string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil || path == source {
				return walkErr
			}
			relative, err := filepath.Rel(source, path)
			if err != nil {
				return err
			}
			relative = filepath.ToSlash(relative)
			input := importer.Chunk{Path: relative, Kind: "directory", Final: true}
			expected := domain.Entry{Path: relative, Kind: "directory"}
			var data []byte
			if !entry.IsDir() {
				Expect(!entry.Type().IsRegular()).To(BeFalse(), func() string { return fmt.Sprint("approved corpus changed to a nonregular fixture entry") })

				data, err = os.ReadFile(path)
				if err != nil {
					return err
				}
				length := int64(len(data))
				input.Kind, input.Hash, input.Length = "file", digest(data), &length
				expected.Kind, expected.Hash, expected.Size = "file", input.Hash, length
				tree.fileSize += length
			}
			ack, err := f.core.Put(f.ctx, f.token, upload, input, bytes.NewReader(data))
			if err != nil {
				return err
			}
			checkpoint = ack.Checkpoint
			tree.entries[relative] = expected
			return nil
		})
		Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })

		seal := f.complete(t, upload, checkpoint, key(t))
		{
			err := f.core.Process(f.ctx, seal)
			Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })
		}

		accepted, err := f.core.Import(f.ctx, f.token, key(t), importer.ImportRequest{UploadID: upload, DocumentPresent: true, VersionID: tree.version})
		Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })
		{

			err := f.core.Process(f.ctx, responseID(t, accepted, "job_id"))
			Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })
		}

		trees[name] = tree
	}
	f.reopen(t)
	report := map[string]any{}
	for name, tree := range trees {
		version, err := f.store.Version(f.ctx, tree.version)
		Expect(err != nil || version.Sequence != 1 || version.ManifestSHA == "").To(BeFalse(), func() string { return fmt.Sprint("real generated version did not reopen with immutable identity") })

		var cursor string
		seen, files := 0, 0
		for {
			entries, err := f.store.Entries(f.ctx, version.ContentSetID, cursor, 2)
			Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })

			if len(entries) == 0 {
				break
			}
			for _, actual := range entries {
				expected, exists := tree.entries[actual.Path]
				Expect(!exists || actual.Kind != expected.Kind || actual.Size != expected.Size || actual.Hash != expected.Hash).To(BeFalse(), func() string { return fmt.Sprint("real generated tree path/type/size/hash changed") })

				if actual.Kind == "file" {
					files++
					err := f.store.Read(f.ctx, domain.Object{Hash: actual.Hash, Size: actual.Size, State: "ready"}, domain.Pin{Kind: "active_read", OwnerID: "native-corpus-regression"}, func(reader io.ReadSeeker) error {
						hash := sha256.New()
						_, err := io.Copy(hash, reader)
						Expect(err == nil && hex.EncodeToString(hash.Sum(nil)) != expected.Hash).To(BeFalse(), func() string { return fmt.Sprint("real generated retained file bytes changed") })

						return err
					})
					Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })

				}
				seen++
				cursor = actual.Path
			}
		}
		Expect(seen != len(tree.entries) || files != 4).To(BeFalse(), func() string { return fmt.Sprint("real generated directory inventory was incomplete") })

		report[name] = map[string]any{"entries": seen, "files": files, "file_bytes": tree.fileSize, "all_paths_types_hashes_reopened": true}
	}
	if path := os.Getenv("HAERYONG_TASK006_REAL_INPUT_REPORT"); path != "" {
		data, _ := json.MarshalIndent(report, "", "  ")
		{
			err := os.WriteFile(path, append(data, '\n'), 0600)
			Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })
		}

	}
})
