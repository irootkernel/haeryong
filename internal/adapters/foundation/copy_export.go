package foundation

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path"
	"path/filepath"
	"time"

	"haeryong.local/haeryong/internal/adapters/sqlite"
	app "haeryong.local/haeryong/internal/application/storage"
	"haeryong.local/haeryong/internal/domain"
)

// exportOutput writes ZIP directly from the canonical inventory. A destination
// filesystem's case/Unicode/link limitations must not become ZIP limitations.
type exportOutput struct {
	root      *os.Root
	file      *os.File
	archive   *zip.Writer
	directory string
}

func newExportOutput(directory string, archive bool) (*exportOutput, error) {
	out := &exportOutput{directory: directory}
	if archive {
		f, e := os.OpenFile(directory+".zip", os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if e != nil {
			return nil, e
		}
		out.file = f
		out.archive = zip.NewWriter(f)
		return out, nil
	}
	if e := os.Mkdir(directory, 0700); e != nil {
		return nil, e
	}
	root, e := os.OpenRoot(directory)
	out.root = root
	return out, e
}
func (o *exportOutput) close() error {
	if o.archive != nil {
		e := o.archive.Close()
		if e == nil {
			e = o.file.Sync()
		}
		return errors.Join(e, o.file.Close())
	}
	if o.root != nil {
		return o.root.Close()
	}
	return nil
}
func (o *exportOutput) entry(ctx context.Context, name string, e domain.Entry, source string) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if e2 := domain.ValidatePayloadPath(name); e2 != nil {
		return e2
	}
	var w io.Writer
	var closeFile func() error
	if o.archive != nil {
		header := &zip.FileHeader{Name: name, Method: zip.Store}
		switch e.Kind {
		case "directory":
			header.Name += "/"
			header.SetMode(os.ModeDir | 0700)
		case "symlink":
			header.SetMode(os.ModeSymlink | 0777)
		case "file":
			header.SetMode(0600)
		default:
			return domain.ErrInvalidTarget
		}
		var err error
		w, err = o.archive.CreateHeader(header)
		if err != nil {
			return err
		}
	}
	if o.root != nil {
		if parent := path.Dir(name); parent != "." {
			if err := o.root.MkdirAll(parent, 0700); err != nil {
				return err
			}
		}
		switch e.Kind {
		case "directory":
			return o.root.MkdirAll(name, 0700)
		case "symlink":
			return o.root.Symlink(e.LinkText, name)
		case "file":
			f, err := o.root.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
			if err != nil {
				return err
			}
			w = f
			closeFile = func() error { return errors.Join(f.Sync(), f.Close()) }
		default:
			return domain.ErrInvalidTarget
		}
	}
	if e.Kind == "directory" {
		return nil
	}
	if e.Kind == "symlink" {
		_, err := io.WriteString(w, e.LinkText)
		return err
	}
	f, err := os.Open(source)
	if err != nil {
		if closeFile != nil {
			_ = closeFile()
		}
		return err
	}
	h := sha256.New()
	n, err := copyStream(ctx, io.MultiWriter(w, h), f)
	err = errors.Join(err, f.Close())
	if err == nil && (n != e.Size || hex.EncodeToString(h.Sum(nil)) != e.Hash) {
		err = domain.ErrContentIntegrity
	}
	if closeFile != nil {
		err = errors.Join(err, closeFile())
	}
	return err
}
func emitExportFile(ctx context.Context, out *exportOutput, name, source string) error {
	hash, size, e := copyHashContext(ctx, source)
	if e != nil {
		return e
	}
	return out.entry(ctx, name, domain.Entry{Kind: "file", Hash: hash, Size: size}, source)
}

func (s *Store) finishExport(ctx context.Context, set domain.CopySet, job domain.Job) (result error) {
	if set.PublicationReady && (set.ExpectedIndexSHA == "" || set.ExpectedOutputSHA == "" || set.Request.MetadataDestination != "" && set.ExpectedMetadataSHA == "") {
		return domain.ErrContentIntegrity
	}
	if set.Request.MetadataDestination != "" {
		physical, e := s.CopyDestination(set.Request.MetadataDestination)
		if e != nil {
			return e
		}
		if physical != set.Request.MetadataDestination {
			return domain.ErrConflict
		}
	}

	if set.Stage == "publishing" {
		if info, e := os.Lstat(set.Destination); e == nil {
			if set.Request.Format == "zip" && !info.Mode().IsRegular() || set.Request.Format == "directory" && !info.IsDir() {
				return domain.ErrContentIntegrity
			}
			index := ""
			if set.Request.MetadataDestination != "" {
				index = filepath.Join(set.Request.MetadataDestination, "export-index.json")
			} else {
				first, e := sqlite.CopySelectionPage(ctx, set.Snapshot, set.ID, "")
				if e != nil {
					return e
				}
				if len(first) == 1 && len(set.Request.Records) == 0 && !set.Request.RetainedSources {
					index = filepath.Join(set.Directory, "metadata-output", "export-index.json")
				}
			}
			if set.ExpectedIndexSHA == "" {
				return domain.ErrContentIntegrity
			}
			actual, e := exportIndexHash(ctx, set.Destination, index)
			if e != nil || actual != set.ExpectedIndexSHA {
				return domain.ErrContentIntegrity
			}
			if _, e = VerifyExport(ctx, set.Destination, index); e != nil {
				return e
			}
			actual, e = exportOutputIdentity(ctx, set.Destination)
			if e != nil || actual != set.ExpectedOutputSHA {
				return domain.ErrContentIntegrity
			}
			if set.Request.MetadataDestination != "" {
				actual, e = exportOutputIdentity(ctx, set.Request.MetadataDestination)
				if e != nil || actual != set.ExpectedMetadataSHA {
					return domain.ErrContentIntegrity
				}
			}
			return s.publishExport(ctx, set, job, "", "")
		}
	}
	if _, e := os.Lstat(set.Destination); !errors.Is(e, os.ErrNotExist) {
		return errors.New("export destination exists or is unavailable")
	}
	first, e := sqlite.CopySelectionPage(ctx, set.Snapshot, set.ID, "")
	if e != nil {
		return e
	}
	if len(first) == 0 {
		return domain.ErrNotFound
	}
	multi := len(first) > 1
	metadataRequested := len(set.Request.Records) > 0 || set.Request.RetainedSources || set.Request.MetadataDestination != ""
	envelope := multi || metadataRequested && set.Request.MetadataDestination == ""
	output := filepath.Join(set.Directory, "payload-output")
	// The immutable snapshot and object checkpoints remain intact. Formatting
	// is deterministic and can restart after interruption without changing scope.
	for _, name := range []string{output, output + ".zip", filepath.Join(set.Directory, "metadata-output")} {
		if e = os.RemoveAll(name); e != nil {
			return e
		}
	}
	out, e := newExportOutput(output, set.Request.Format == "zip")
	if e != nil {
		return e
	}
	closed := false
	defer func() {
		if !closed {
			result = errors.Join(result, out.close())
		}
	}()
	metadata := filepath.Join(set.Directory, "metadata-output")
	if e = os.Mkdir(metadata, 0700); e != nil {
		return e
	}
	index, e := os.OpenFile(filepath.Join(metadata, "export-index.json"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if e != nil {
		return e
	}
	defer index.Close()
	if _, e = io.WriteString(index, `{"format_version":1,"library_id":`); e != nil {
		return e
	}
	raw, _ := json.Marshal(set.LibraryID)
	if _, e = index.Write(raw); e != nil {
		return e
	}
	if _, e = io.WriteString(index, `,"versions":[`); e != nil {
		return e
	}
	count := 0
	after := ""
	for {
		versions, e := sqlite.CopySelectionPage(ctx, set.Snapshot, set.ID, after)
		if e != nil {
			return e
		}
		if len(versions) == 0 {
			break
		}
		for _, v := range versions {
			if e = s.exportCurrent(ctx, set, v, job.AttemptGeneration); e != nil {
				return e
			}
			prefix := ""
			if multi {
				prefix = "versions/" + v.VersionID
			} else if envelope {
				prefix = "payload"
			}
			if prefix != "" {
				if e = out.entry(ctx, prefix, domain.Entry{Kind: "directory"}, ""); e != nil {
					return e
				}
			}
			v.PayloadRoot = prefix
			if prefix == "" {
				v.PayloadRoot = "."
			}
			metaRoot := "versions/" + v.VersionID
			dir := filepath.Join(metadata, filepath.FromSlash(metaRoot))
			if e = os.MkdirAll(dir, 0700); e != nil {
				return e
			}
			v.InventoryPath = metaRoot + "/inventory.jsonl"
			if envelope && set.Request.MetadataDestination == "" {
				v.InventoryPath = "metadata/" + v.InventoryPath
			}
			if metadataRequested {
				m := metaRoot
				if envelope && set.Request.MetadataDestination == "" {
					m = "metadata/" + m
				}
				v.MetadataRoot = &m
			}
			inventory, e := os.OpenFile(filepath.Join(dir, "inventory.jsonl"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
			if e != nil {
				return e
			}
			inventoryHash := sha256.New()
			entryAfter := ""
			for {
				entries, e := sqlite.CopyEntryPage(ctx, set.Snapshot, v.ContentSetID, entryAfter)
				if e != nil {
					inventory.Close()
					return e
				}
				if len(entries) == 0 {
					break
				}
				for _, entry := range entries {
					line, e := domain.InventoryLine(entry)
					if e != nil {
						inventory.Close()
						return e
					}
					if _, e = io.MultiWriter(inventory, inventoryHash).Write(line); e != nil {
						inventory.Close()
						return e
					}
					if e = s.copyFault("export_final_transfer"); e != nil {
						inventory.Close()
						return e
					}
					if e = out.entry(ctx, path.Join(prefix, entry.Path), entry, filepath.Join(set.Directory, "objects", entry.Hash)); e != nil {
						inventory.Close()
						return e
					}
					entryAfter = entry.Path
				}
			}
			if e = inventory.Sync(); e != nil {
				inventory.Close()
				return e
			}
			if e = inventory.Close(); e != nil {
				return e
			}
			if hex.EncodeToString(inventoryHash.Sum(nil)) != v.PayloadSHA {
				return domain.ErrContentIntegrity
			}
			if metadataRequested {
				if e = s.exportMetadata(ctx, set, v, dir); e != nil {
					return e
				}
				v.RecordsSHA, _, e = copyHashContext(ctx, filepath.Join(dir, "records.jsonl"))
				if e != nil {
					return e
				}
				v.DependenciesSHA, _, e = copyHashContext(ctx, filepath.Join(dir, "dependencies.jsonl"))
				if e != nil {
					return e
				}
			}
			if count > 0 {
				if _, e = io.WriteString(index, ","); e != nil {
					return e
				}
			}
			if e = json.NewEncoder(index).Encode(v); e != nil {
				return e
			}
			count++
			after = v.VersionID
		}
	}
	if _, e = io.WriteString(index, "]}\n"); e != nil {
		return e
	}
	if e = index.Sync(); e != nil {
		return e
	}
	if e = index.Close(); e != nil {
		return e
	}
	if envelope && set.Request.MetadataDestination == "" {
		if e = emitExportFile(ctx, out, "export-index.json", filepath.Join(metadata, "export-index.json")); e != nil {
			return e
		}
		if e = filepath.WalkDir(metadata, func(name string, d os.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if d.IsDir() || name == filepath.Join(metadata, "export-index.json") {
				return nil
			}
			rel, e := filepath.Rel(metadata, name)
			if e != nil {
				return e
			}
			return emitExportFile(ctx, out, "metadata/"+filepath.ToSlash(rel), name)
		}); e != nil {
			return e
		}
	}
	if e = out.close(); e != nil {
		return e
	}
	closed = true
	if set.Request.Format != "zip" {
		if e = syncCopyTree(ctx, output, s.CopyFault); e != nil {
			return e
		}
	}
	stagedOutput := output
	if set.Request.Format == "zip" {
		stagedOutput += ".zip"
	}
	stagedIndex := filepath.Join(metadata, "export-index.json")
	if envelope && set.Request.MetadataDestination == "" {
		stagedIndex = ""
	}
	if e = s.copyFault("export_verification"); e != nil {
		return e
	}
	if _, e = VerifyExport(ctx, stagedOutput, stagedIndex); e != nil {
		return e
	}
	after = ""
	for {
		versions, e := sqlite.CopySelectionPage(ctx, set.Snapshot, set.ID, after)
		if e != nil {
			return e
		}
		if len(versions) == 0 {
			break
		}
		for _, v := range versions {
			if e = s.exportCurrent(ctx, set, v, job.AttemptGeneration); e != nil {
				return e
			}
			after = v.VersionID
		}
	}
	publish := output
	if set.Request.Format == "zip" {
		publish += ".zip"
	}
	metadataStage := ""
	if set.Request.MetadataDestination != "" {
		metadataStage = filepath.Join(filepath.Dir(set.Request.MetadataDestination), "."+set.ID+".metadata")
		if e = os.RemoveAll(metadataStage); e != nil {
			return e
		}
		if e = s.copyExportDirectory(ctx, metadata, metadataStage); e != nil {
			return e
		}
	}
	expected, e := exportIndexHash(ctx, stagedOutput, stagedIndex)
	if e != nil {
		return e
	}
	if set.ExpectedIndexSHA != "" && set.ExpectedIndexSHA != expected {
		return domain.ErrContentIntegrity
	}
	set.ExpectedIndexSHA = expected
	outputIdentity, e := exportOutputIdentity(ctx, stagedOutput)
	if e != nil {
		return e
	}
	if set.ExpectedOutputSHA != "" && set.ExpectedOutputSHA != outputIdentity {
		return domain.ErrContentIntegrity
	}
	set.ExpectedOutputSHA = outputIdentity
	if metadataStage != "" {
		identity, e := exportOutputIdentity(ctx, metadataStage)
		if e != nil {
			return e
		}
		if set.ExpectedMetadataSHA != "" && set.ExpectedMetadataSHA != identity {
			return domain.ErrContentIntegrity
		}
		set.ExpectedMetadataSHA = identity
	}
	if e = syncCopyTree(ctx, metadata, s.CopyFault); e != nil {
		return e
	}
	if e = copySyncDirectory(set.Directory); e != nil {
		return e
	}
	set.PublicationReady = true
	set.Stage = "publishing"
	if e = s.Write(ctx, func(tx app.WriteTx) error {
		if e := app.GuardCopy(tx, set, job.ID, job.AttemptGeneration); e != nil {
			return e
		}
		return tx.PutCopySet(set)
	}); e != nil {
		return e
	}
	if e = s.copyFault("export_verified"); e != nil {
		return e
	}
	return s.publishExport(ctx, set, job, publish, metadataStage)
}

func (s *Store) publishExport(ctx context.Context, set domain.CopySet, job domain.Job, publish, metadataStage string) error {
	sidecarOwned := false
	if metadataStage != "" {
		if _, e := os.Lstat(set.Request.MetadataDestination); e == nil {
			actual, e := copyHashContextIndex(ctx, filepath.Join(set.Request.MetadataDestination, "export-index.json"))
			if e != nil || actual != set.ExpectedIndexSHA {
				return domain.ErrConflict
			}
			if _, e = VerifyExport(ctx, publish, filepath.Join(set.Request.MetadataDestination, "export-index.json")); e != nil {
				return e
			}
			identity, e := exportOutputIdentity(ctx, set.Request.MetadataDestination)
			if e != nil || identity != set.ExpectedMetadataSHA {
				return domain.ErrContentIntegrity
			}
			sidecarOwned = true
		} else if !errors.Is(e, os.ErrNotExist) {
			return e
		}
	}

	return s.Write(ctx, func(tx app.WriteTx) error {
		if e := app.GuardCopy(tx, set, job.ID, job.AttemptGeneration); e != nil {
			return e
		}
		after := ""
		for {
			versions, e := tx.CopyVersions(set.ID, after, 64)
			if e != nil {
				return e
			}
			if len(versions) == 0 {
				break
			}
			for _, v := range versions {
				if e = exportGuard(tx, set, v, job.AttemptGeneration); e != nil {
					return e
				}
				after = v.VersionID
			}
		}
		if e := s.copyFault("export_publish"); e != nil {
			return e
		}
		return tx.FinishJob(job.ID, job.AttemptGeneration, "", func(tx app.WriteTx) error {
			if publish != "" {
				if metadataStage != "" {
					destination := set.Request.MetadataDestination
					if _, e := os.Lstat(destination); e == nil {
						// A previous interrupted publication may already own
						// this exact sidecar. Never replace a foreign output.
						if !sidecarOwned {
							return domain.ErrConflict
						}
					} else if errors.Is(e, os.ErrNotExist) {
						if e = os.Rename(metadataStage, destination); e != nil {
							return e
						}
						if e = s.copyFault("export_sidecar_renamed"); e != nil {
							return e
						}
					} else {
						return e
					}
					if e := copySyncDirectory(filepath.Dir(destination)); e != nil {
						return e
					}
				}
				if _, e := os.Lstat(set.Destination); !errors.Is(e, os.ErrNotExist) {
					return domain.ErrConflict
				}
				if e := os.Rename(publish, set.Destination); e != nil {
					return e
				}
				if e := s.copyFault("export_after_rename"); e != nil {
					return e
				}
			}
			// Recovered payloads and separate metadata can both have survived a
			// rename without the following directory sync or database commit.
			if set.Request.MetadataDestination != "" {
				if e := s.copyFault("export_metadata_parent_sync"); e != nil {
					return e
				}
				if e := copySyncDirectory(filepath.Dir(set.Request.MetadataDestination)); e != nil {
					return e
				}
			}
			if e := s.copyFault("export_source_parent_sync"); e != nil {
				return e
			}
			if e := copySyncDirectory(set.Directory); e != nil {
				return e
			}
			if e := s.copyFault("export_parent_sync"); e != nil {
				return e
			}
			if e := copySyncDirectory(filepath.Dir(set.Destination)); e != nil {
				return e
			}
			set.Stage = "complete"
			set.CompletedAt = time.Now().UTC().Format(time.RFC3339Nano)
			set.VerifiedAt = set.CompletedAt
			if e := tx.PutCopySet(set); e != nil {
				return e
			}
			return tx.ReleaseOwnerPins("backup", set.ID)
		})
	})
}

func (s *Store) exportMetadata(ctx context.Context, set domain.CopySet, v domain.CopyVersion, directory string) error {
	for name, hash := range map[string]string{"manifest.json": v.ManifestSHA, "supplied.json": v.MetadataSHA} {
		if hash != "" {
			if e := copyRegularFile(ctx, filepath.Join(set.Directory, "objects", hash), filepath.Join(directory, name)); e != nil {
				return e
			}
		}
	}
	objects := filepath.Join(directory, "objects")
	if e := os.Mkdir(objects, 0700); e != nil {
		return e
	}
	copyObject := func(o domain.Object) error {
		source := filepath.Join(set.Directory, "objects", o.Hash)
		hash, size, e := copyHashContext(ctx, source)
		if e != nil {
			return e
		}
		if hash != o.Hash || size != o.Size {
			return domain.ErrContentIntegrity
		}
		destination := filepath.Join(objects, o.Hash)
		if _, e = os.Stat(destination); e == nil {
			return nil
		}
		return copyRegularFile(ctx, source, destination)
	}
	f, e := os.OpenFile(filepath.Join(directory, "records.jsonl"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if e != nil {
		return e
	}
	enc := json.NewEncoder(f)
	e = sqlite.StreamCopyRecords(ctx, set.Snapshot, set.ID, set.OwnerID, v, set.Request.Records, func(kind string, raw json.RawMessage) error {
		if e := enc.Encode(map[string]any{"kind": kind, "owner_id": set.OwnerID, "version_id": v.VersionID, "value": raw}); e != nil {
			return e
		}
		if kind == "notes" {
			contexts, e := sqlite.CopyNoteObjects(ctx, set.Snapshot, set.OwnerID, raw, copyObject)
			if e != nil {
				return e
			}
			for _, c := range contexts {
				if e = enc.Encode(map[string]any{"kind": "context", "value": c}); e != nil {
					return e
				}
			}
		}
		return nil
	})
	if e == nil {
		e = f.Sync()
	}
	e = errors.Join(e, f.Close())
	if e != nil {
		return e
	}
	f, e = os.OpenFile(filepath.Join(directory, "dependencies.jsonl"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if e != nil {
		return e
	}
	enc = json.NewEncoder(f)
	e = sqlite.CopyDependencies(ctx, set.Snapshot, set.ID, v, set.Request.RetainedSources, func(raw json.RawMessage) error { return enc.Encode(raw) }, copyObject)
	if e == nil {
		e = f.Sync()
	}
	return errors.Join(e, f.Close())
}
func (s *Store) exportCurrent(ctx context.Context, set domain.CopySet, v domain.CopyVersion, generation int64) error {
	return s.Write(ctx, func(tx app.WriteTx) error { return exportGuard(tx, set, v, generation) })
}
func exportGuard(tx app.WriteTx, set domain.CopySet, v domain.CopyVersion, generation int64) error {
	if e := app.GuardCopy(tx, set, set.JobID, generation); e != nil {
		return e
	}
	stored, e := tx.Version(v.VersionID)
	if e != nil {
		return e
	}
	d, e := tx.Document(v.DocumentID)
	if e != nil {
		return e
	}
	if d.Lifecycle == "purged" || set.Request.Lifecycle != "all" && d.Lifecycle != set.Request.Lifecycle {
		return domain.ErrDenied
	}
	if stored.DocumentID != v.DocumentID || stored.PayloadSHA != v.PayloadSHA || stored.ManifestSHA != v.ManifestSHA {
		return domain.ErrConflict
	}
	return nil
}

func copyRegularFile(ctx context.Context, source, destination string) error {
	f, e := os.Open(source)
	if e != nil {
		return e
	}
	defer f.Close()
	out, e := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if e != nil {
		return e
	}
	_, e = copyStream(ctx, out, f)
	if e == nil {
		e = out.Sync()
	}
	return errors.Join(e, out.Close())
}
func syncCopyTree(ctx context.Context, root string, fault ...func(string) error) error {
	return filepath.WalkDir(root, func(name string, d os.DirEntry, e error) error {
		if e != nil {
			return e
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if d.IsDir() {
			if len(fault) > 0 && fault[0] != nil {
				if e := fault[0]("directory_sync:" + name); e != nil {
					return e
				}
			}
			return copySyncDirectory(name)
		}
		return nil
	})
}
func (s *Store) copyExportDirectory(ctx context.Context, source, destination string) error {
	stage, e := os.MkdirTemp(filepath.Dir(destination), ".export-metadata-")
	if e != nil {
		return e
	}
	defer os.RemoveAll(stage)
	if e = filepath.WalkDir(source, func(name string, d os.DirEntry, e error) error {
		if e != nil {
			return e
		}
		rel, e := filepath.Rel(source, name)
		if e != nil {
			return e
		}
		target := filepath.Join(stage, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0700)
		}
		return copyRegularFile(ctx, name, target)
	}); e != nil {
		return e
	}
	if e = syncCopyTree(ctx, stage, s.CopyFault); e != nil {
		return e
	}
	if e = os.Rename(stage, destination); e != nil {
		return e
	}
	return copySyncDirectory(filepath.Dir(destination))
}
