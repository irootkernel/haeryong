package foundation

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io/fs"
	"path/filepath"
	"sort"

	"haeryong.local/haeryong/internal/domain"
)

// Bind every formatted entry, including metadata and otherwise unreferenced
// envelope names. The export index alone does not describe those extra names.
// Hash original relative paths/types/bytes/link text with the existing inventory
// codec; ZIP container ordering/compression is not payload identity.
func exportOutputIdentity(ctx context.Context, input string) (string, error) {
	view, e := openExportView(ctx, input)
	if e != nil {
		return "", e
	}
	if view.archive != nil {
		defer view.archive.Close()
	}
	digest := sha256.New()
	consume := func(name string) error {
		if e := ctx.Err(); e != nil {
			return e
		}
		kind, e := view.kind(name)
		if e != nil {
			return e
		}
		entry := domain.Entry{Path: name, Kind: kind}
		switch kind {
		case "file":
			f, e := view.open(name)
			if e != nil {
				return e
			}
			h := sha256.New()
			entry.Size, e = copyStream(ctx, h, f)
			e = errors.Join(e, f.Close())
			if e != nil {
				return e
			}
			entry.Hash = hex.EncodeToString(h.Sum(nil))
		case "symlink":
			entry.LinkText, e = view.link(name)
			if e != nil {
				return e
			}
		}
		line, e := domain.InventoryLine(entry)
		if e != nil {
			return e
		}
		_, e = digest.Write(line)
		return e
	}
	if view.archive != nil {
		names := make([]string, 0, len(view.entries))
		for name := range view.entries {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			if e = consume(name); e != nil {
				return "", e
			}
		}
	} else {
		e = filepath.WalkDir(input, func(name string, _ fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if name == input {
				return nil
			}
			relative, e := filepath.Rel(input, name)
			if e != nil {
				return e
			}
			return consume(filepath.ToSlash(relative))
		})
		if e != nil {
			return "", e
		}
	}
	return hex.EncodeToString(digest.Sum(nil)), nil
}
