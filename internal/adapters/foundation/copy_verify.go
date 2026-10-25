package foundation

import (
	"archive/zip"
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"

	"haeryong.local/haeryong/internal/domain"
)

type exportView struct {
	directory       string
	ctx             context.Context
	archive         *zip.ReadCloser
	entries         map[string]*zip.File
	archiveKinds    map[string]string
	payloads        map[string]map[string]exportPayloadEntry
	rootDirectories map[string]map[string]string
}

func copyHashContextIndex(ctx context.Context, name string) (string, error) {
	h, _, e := copyHashContext(ctx, name)
	return h, e
}
func exportIndexHash(ctx context.Context, input, indexPath string) (string, error) {
	if indexPath != "" {
		return copyHashContextIndex(ctx, indexPath)
	}
	view, e := openExportView(ctx, input)
	if e != nil {
		return "", e
	}
	if view.archive != nil {
		defer view.archive.Close()
	}
	f, e := view.open("export-index.json")
	if e != nil {
		return "", e
	}
	h := sha256.New()
	_, e = copyStream(ctx, h, f)
	e = errors.Join(e, f.Close())
	return hex.EncodeToString(h.Sum(nil)), e
}

func openExportView(ctx context.Context, input string) (*exportView, error) {
	info, e := os.Lstat(input)
	if e != nil {
		return nil, e
	}
	v := &exportView{ctx: ctx}
	if info.IsDir() {
		v.directory = input
		return v, nil
	}
	if !info.Mode().IsRegular() {
		return nil, domain.ErrInvalidTarget
	}
	v.archive, e = zip.OpenReader(input)
	if e != nil {
		return nil, e
	}
	v.entries = map[string]*zip.File{}
	for _, f := range v.archive.File {
		if ctx.Err() != nil {
			v.archive.Close()
			return nil, ctx.Err()
		}
		key := strings.TrimSuffix(f.Name, "/")
		if e = domain.ValidatePayloadPath(key); e != nil {
			v.archive.Close()
			return nil, e
		}
		if v.entries[key] != nil {
			v.archive.Close()
			return nil, errors.New("duplicate export ZIP entry")
		}
		v.entries[key] = f
	}
	if e = v.indexZIPNamespace(); e != nil {
		v.archive.Close()
		return nil, e
	}
	return v, nil
}
func (v *exportView) open(name string) (io.ReadCloser, error) {
	if e := domain.ValidatePayloadPath(name); e != nil {
		return nil, e
	}
	if v.archive != nil {
		entry := v.entries[name]
		if entry == nil {
			return nil, os.ErrNotExist
		}
		return entry.Open()
	}
	root, e := os.OpenRoot(v.directory)
	if e != nil {
		return nil, e
	}
	defer root.Close()
	info, e := root.Lstat(name)
	if e != nil {
		return nil, e
	}
	if !info.Mode().IsRegular() {
		return nil, errors.New("export file type mismatch")
	}
	return root.Open(name)
}
func (v *exportView) kind(name string) (string, error) {
	var mode os.FileMode
	if v.archive != nil {
		kind, ok := v.archiveKinds[name]
		if !ok {
			return "", os.ErrNotExist
		}
		if kind == "" {
			return "", domain.ErrInvalidTarget
		}
		return kind, nil
	} else {
		root, e := os.OpenRoot(v.directory)
		if e != nil {
			return "", e
		}
		defer root.Close()
		info, e := root.Lstat(name)
		if e != nil {
			return "", e
		}
		mode = info.Mode()
	}
	switch {
	case mode&os.ModeSymlink != 0:
		return "symlink", nil
	case mode.IsDir():
		return "directory", nil
	case mode.IsRegular():
		return "file", nil
	}
	return "", domain.ErrInvalidTarget
}
func (v *exportView) link(name string) (string, error) {
	if v.archive == nil {
		root, e := os.OpenRoot(v.directory)
		if e != nil {
			return "", e
		}
		defer root.Close()
		return root.Readlink(name)
	}
	f, e := v.open(name)
	if e != nil {
		return "", e
	}
	defer f.Close()
	raw, e := io.ReadAll(f)
	return string(raw), e
}

// VerifyExport compares complete original inventories and selected metadata.
// Plain output needs its optional separate index to claim identity verification.
func VerifyExport(ctx context.Context, input, indexPath string) (map[string]any, error) {
	v, e := openExportView(ctx, input)
	if e != nil {
		return nil, e
	}
	if v.archive != nil {
		defer v.archive.Close()
	}
	var index io.ReadCloser
	var metadata *exportView
	if indexPath != "" {
		index, e = os.Open(indexPath)
		metadata = &exportView{directory: filepath.Dir(indexPath), ctx: ctx}
	} else {
		index, e = v.open("export-index.json")
		metadata = v
	}
	if e != nil {
		return nil, errors.New("export identity verification requires an embedded or explicit separate export index")
	}
	defer index.Close()
	decoder := json.NewDecoder(index)
	start, e := decoder.Token()
	if e != nil || start != json.Delim('{') {
		return nil, domain.ErrInvalidTarget
	}
	seen := map[string]bool{}
	versions := 0
	roots := map[string]bool{}
	identities := map[string]domain.CopyVersion{}
	bindings := newExportBindings(ctx)
	for decoder.More() {
		key, e := decoder.Token()
		if e != nil {
			return nil, e
		}
		name, ok := key.(string)
		if !ok || seen[name] {
			return nil, domain.ErrInvalidTarget
		}
		seen[name] = true
		switch name {
		case "format_version":
			var version int
			if decoder.Decode(&version) != nil || version != 1 {
				return nil, domain.ErrInvalidTarget
			}
		case "library_id":
			var id string
			if decoder.Decode(&id) != nil || !copyUUID.MatchString(id) {
				return nil, domain.ErrInvalidTarget
			}
		case "versions":
			start, e := decoder.Token()
			if e != nil || start != json.Delim('[') {
				return nil, domain.ErrInvalidTarget
			}
			for decoder.More() {
				var selected domain.CopyVersion
				var raw json.RawMessage
				if e = decoder.Decode(&raw); e != nil {
					return nil, e
				}
				if e = decodeCopyValue(raw, &selected); e != nil {
					return nil, e
				}
				if !validExportVersion(selected) || identities[selected.VersionID].VersionID != "" {
					return nil, domain.ErrInvalidTarget
				}
				identities[selected.VersionID] = selected
				if selected.PayloadRoot != "." && selected.PayloadRoot != "payload" && selected.PayloadRoot != "versions/"+selected.VersionID {
					return nil, domain.ErrInvalidTarget
				}
				if roots[selected.PayloadRoot] {
					return nil, errors.New("duplicate export version root")
				}
				for root := range roots {
					if root == "." || selected.PayloadRoot == "." || strings.HasPrefix(root, selected.PayloadRoot+"/") || strings.HasPrefix(selected.PayloadRoot, root+"/") {
						return nil, errors.New("overlapping export version roots")
					}
				}
				roots[selected.PayloadRoot] = true
				if e = verifyExportVersion(ctx, v, metadata, selected, bindings); e != nil {
					return nil, e
				}
				versions++
			}
			if _, e = decoder.Token(); e != nil {
				return nil, e
			}
		default:
			return nil, domain.ErrInvalidTarget
		}
	}
	if _, e = decoder.Token(); e != nil {
		return nil, e
	}
	if _, e = decoder.Token(); e != io.EOF {
		return nil, domain.ErrInvalidTarget
	}
	if !seen["format_version"] || !seen["library_id"] || versions == 0 {
		return nil, domain.ErrInvalidTarget
	}
	if versions > 1 {
		for root := range roots {
			if !strings.HasPrefix(root, "versions/") {
				return nil, domain.ErrInvalidTarget
			}
		}
	}
	// Layout is chosen by descriptors, not cardinality. Ordinary user paths
	// named versions under a plain/payload root are not structural roots.
	for root := range roots {
		if strings.HasPrefix(root, "versions/") {
			if e = v.verifyVersionRoots(roots); e != nil {
				return nil, e
			}
			break
		}
	}
	if e = bindings.finish(v, identities); e != nil {
		return nil, e
	}
	return map[string]any{"valid": true, "versions": versions, "integrity": "verified", "limitations": "dependency states are not source availability, factual correctness or personal-state merge proof"}, nil
}
func verifyExportVersion(ctx context.Context, payload, metadata *exportView, v domain.CopyVersion, bindings *exportBindings) error {
	if v.PayloadRoot != "." {
		if e := domain.ValidatePayloadPath(strings.TrimSuffix(v.PayloadRoot, "/")); e != nil {
			return e
		}
	}
	inventory, e := metadata.open(v.InventoryPath)
	if e != nil {
		return e
	}
	defer inventory.Close()
	hash := sha256.New()
	reader := bufio.NewReader(io.TeeReader(inventory, hash))
	facts := map[string]exportPayloadEntry{}
	after := ""
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		line, readErr := reader.ReadBytes('\n')
		if readErr == io.EOF && len(line) == 0 {
			break
		}
		if readErr != nil {
			if readErr == io.EOF {
				return domain.ErrContentIntegrity
			}
			return readErr
		}
		var row []json.RawMessage
		if e = json.Unmarshal(line, &row); e != nil {
			return e
		}
		if len(row) < 2 {
			return domain.ErrInvalidTarget
		}
		var name, kind string
		if json.Unmarshal(row[0], &name) != nil || json.Unmarshal(row[1], &kind) != nil {
			return domain.ErrInvalidTarget
		}
		if e = domain.ValidatePayloadPath(name); e != nil {
			return e
		}
		if name <= after {
			return errors.New("export inventory order or duplicate")
		}
		after = name
		fact := exportPayloadEntry{kind: kind}
		entry := domain.Entry{Path: name, Kind: kind}
		full := path.Join(v.PayloadRoot, name)
		actual, e := payload.kind(full)
		if e != nil {
			return e
		}
		if actual != kind {
			return errors.New("export entry type mismatch")
		}
		switch kind {
		case "file":
			if len(row) != 4 {
				return domain.ErrInvalidTarget
			}
			var size, expected string
			if json.Unmarshal(row[2], &size) != nil || json.Unmarshal(row[3], &expected) != nil {
				return domain.ErrInvalidTarget
			}
			byteSize, e := strconv.ParseInt(size, 10, 64)
			if e != nil || byteSize < 0 {
				return domain.ErrInvalidTarget
			}
			f, e := payload.open(full)
			if e != nil {
				return e
			}
			h := sha256.New()
			n, e := copyStream(ctx, h, f)
			e = errors.Join(e, f.Close())
			if e != nil {
				return e
			}
			fact.hash = hex.EncodeToString(h.Sum(nil))
			if n != byteSize || fact.hash != expected {
				return domain.ErrContentIntegrity
			}
			entry.Size, entry.Hash = byteSize, expected
		case "directory":
			if len(row) != 2 {
				return domain.ErrInvalidTarget
			}
		case "symlink":
			if len(row) != 3 {
				return domain.ErrInvalidTarget
			}
			var target string
			if json.Unmarshal(row[2], &target) != nil {
				return domain.ErrInvalidTarget
			}
			actual, e := payload.link(full)
			if e != nil {
				return e
			}
			if actual != target {
				return domain.ErrContentIntegrity
			}
			entry.LinkText = target
		default:
			return domain.ErrInvalidTarget
		}
		canonical, e := domain.InventoryLine(entry)
		if e != nil {
			return e
		}
		if !bytes.Equal(canonical, line) {
			return domain.ErrContentIntegrity
		}
		facts[name] = fact
	}
	if hex.EncodeToString(hash.Sum(nil)) != v.PayloadSHA {
		return domain.ErrContentIntegrity
	}
	if e = payload.verifyPayloadNamespace(v.PayloadRoot, facts); e != nil {
		return e
	}

	if v.MetadataRoot != nil {
		for name, want := range map[string]string{"manifest.json": v.ManifestSHA, "supplied.json": v.MetadataSHA, "records.jsonl": v.RecordsSHA, "dependencies.jsonl": v.DependenciesSHA} {
			if want == "" {
				continue
			}
			f, e := metadata.open(path.Join(*v.MetadataRoot, name))
			if e != nil {
				return e
			}
			h := sha256.New()
			_, e = copyStream(ctx, h, f)
			e = errors.Join(e, f.Close())
			if e != nil {
				return e
			}
			if hex.EncodeToString(h.Sum(nil)) != want {
				return domain.ErrContentIntegrity
			}
		}
		if e = verifyExportMetadata(ctx, payload, metadata, v, bindings); e != nil {
			return e
		}
	}
	return nil
}

func verifyExportMetadata(ctx context.Context, payload, view *exportView, selected domain.CopyVersion, bindings *exportBindings) error {
	f, e := view.open(path.Join(*selected.MetadataRoot, "records.jsonl"))
	if e != nil {
		return e
	}
	defer f.Close()
	d := json.NewDecoder(f)
	for {
		var raw json.RawMessage
		if e = d.Decode(&raw); e == io.EOF {
			break
		}
		if e != nil {
			return e
		}
		var row exportRecord
		if e = decodeCopyValue(raw, &row); e != nil {
			return e
		}
		if e = bindings.record(payload, view, selected, row); e != nil {
			return e
		}
	}

	f2, e := view.open(path.Join(*selected.MetadataRoot, "dependencies.jsonl"))
	if e != nil {
		return e
	}
	defer f2.Close()
	d = json.NewDecoder(f2)
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		var raw json.RawMessage
		if e = d.Decode(&raw); e == io.EOF {
			break
		}
		if e != nil {
			return e
		}
		if e = verifyExportDependency(ctx, payload, view, selected, raw, bindings); e != nil {
			return e
		}
	}

	return nil
}
func verifyMetadataObject(ctx context.Context, view *exportView, root string, object domain.Object) error {
	if len(object.Hash) != 64 || object.Size < 0 {
		return domain.ErrContentIntegrity
	}
	if _, e := hex.DecodeString(object.Hash); e != nil {
		return e
	}
	f, e := view.open(path.Join(root, "objects", object.Hash))
	if e != nil {
		return e
	}
	h := sha256.New()
	n, e := copyStream(ctx, h, f)
	e = errors.Join(e, f.Close())
	if e != nil {
		return e
	}
	if n != object.Size || hex.EncodeToString(h.Sum(nil)) != object.Hash {
		return domain.ErrContentIntegrity
	}
	return nil
}
