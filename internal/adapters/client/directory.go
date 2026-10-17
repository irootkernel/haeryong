package client

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	importer "haeryong.local/haeryong/internal/application/import"
	"haeryong.local/haeryong/internal/application/query"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

func fileHash(path string) (string, error) {
	f, e := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if e != nil {
		return "", e
	}
	defer f.Close()
	info, e := f.Stat()
	if e != nil || !info.Mode().IsRegular() {
		return "", fmt.Errorf("destination is not a regular file")
	}
	hash := sha256.New()
	_, e = io.CopyBuffer(hash, f, make([]byte, 64*1024))
	return hex.EncodeToString(hash.Sum(nil)), e
}
func syncDirectory(path string) error {
	f, e := os.Open(path)
	if e != nil {
		return e
	}
	return errors.Join(f.Sync(), f.Close())
}

// DownloadDirectory uses the current entry cursor and verified file API; it does
// not add a portable-export format or metadata files to the author's payload.
func (c *Client) DownloadDirectory(ctx context.Context, version, destination string) (map[string]any, error) {
	full, e := filepath.Abs(destination)
	if e != nil {
		return nil, e
	}
	if _, e = os.Lstat(full); e == nil {
		return nil, fmt.Errorf("destination already exists")
	} else if !os.IsNotExist(e) {
		return nil, e
	}
	identity, e := c.Identity(ctx)
	if e != nil {
		return nil, e
	}
	binding := sha256.Sum256([]byte(c.origin + "\x00" + identity.LibraryID + "\x00" + identity.PrincipalID + "\x00" + version + "\x00" + full))
	stage := filepath.Join(filepath.Dir(full), ".haeryong-directory-"+hex.EncodeToString(binding[:])[:24])

	markerPath := stage + ".json"
	marker, e := os.OpenFile(markerPath, os.O_RDWR|os.O_CREATE|syscall.O_NOFOLLOW, 0600)
	if e != nil {
		return nil, e
	}
	defer marker.Close()
	if e = syscall.Flock(int(marker.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); e != nil {
		return nil, fmt.Errorf("directory download is already active")
	}
	markerInfo, e := marker.Stat()
	if e != nil || !markerInfo.Mode().IsRegular() || markerInfo.Mode().Perm()&0077 != 0 {
		return nil, fmt.Errorf("unsafe directory journal")
	}
	if markerInfo.Size() == 0 {
		if _, e = os.Lstat(stage); e == nil {
			return nil, fmt.Errorf("unowned incomplete directory")
		}
		if e = json.NewEncoder(marker).Encode(map[string]string{"binding": hex.EncodeToString(binding[:])}); e != nil {
			return nil, e
		}
		if e = marker.Sync(); e != nil {
			return nil, e
		}
		if e = syncDirectory(filepath.Dir(stage)); e != nil {
			return nil, e
		}
	} else {
		var metadata map[string]string
		if e = json.NewDecoder(marker).Decode(&metadata); e != nil || metadata["binding"] != hex.EncodeToString(binding[:]) {
			return nil, fmt.Errorf("directory journal binding mismatch")
		}
	}
	if e = os.Mkdir(stage, 0700); e != nil && !os.IsExist(e) {
		return nil, e
	}
	info, e := os.Lstat(stage)
	if e != nil || !info.IsDir() || info.Mode().Perm()&0077 != 0 {
		return nil, fmt.Errorf("unsafe incomplete directory")
	}

	ledger := stage + ".entries"
	if e = os.Mkdir(ledger, 0700); e != nil && !os.IsExist(e) {
		return nil, e
	}
	ledgerInfo, e := os.Lstat(ledger)
	if e != nil || !ledgerInfo.IsDir() || ledgerInfo.Mode().Perm()&0077 != 0 {
		return nil, fmt.Errorf("unsafe directory inventory")
	}
	recordPath := func(path string) string {
		hash := sha256.Sum256([]byte(path))
		return filepath.Join(ledger, hex.EncodeToString(hash[:])+".json")
	}
	// The staging tree is never used as a source of symlink traversal. Parents are
	// checked individually and links are created only at their exact listed path.
	directory := func(path string) error {
		current := stage
		if path == "" {
			return nil
		}
		for _, part := range strings.Split(path, "/") {
			current = filepath.Join(current, part)
			if e := os.Mkdir(current, 0700); e != nil && !os.IsExist(e) {
				return e
			}
			info, e := os.Lstat(current)
			if e != nil || !info.IsDir() {
				return fmt.Errorf("destination path is not a directory")
			}
		}
		return nil
	}
	cursor := ""
	count := int64(0)
	bytesCopied := int64(0)
	for {
		values := url.Values{"limit": {"128"}}
		if cursor != "" {
			values.Set("cursor", cursor)
		}
		var page query.List[query.File]
		if _, e = c.JSON(ctx, "GET", "/api/v1/versions/"+version+"/files?"+values.Encode(), "", nil, &page); e != nil {
			return nil, e
		}
		for _, entry := range page.Items {
			if e = ctx.Err(); e != nil {
				return nil, e
			}
			if e = importer.ValidatePath(entry.Path); e != nil {
				return nil, e
			}
			parent := filepath.ToSlash(filepath.Dir(entry.Path))
			if parent == "." {
				parent = ""
			}
			if e = directory(parent); e != nil {
				return nil, e
			}
			path := filepath.Join(stage, filepath.FromSlash(entry.Path))
			if _, e := os.Lstat(path); e == nil {
				dir, e := os.Open(filepath.Dir(path))
				if e != nil {
					return nil, e
				}
				exact := false
				for {
					names, e := dir.Readdirnames(64)
					for _, name := range names {
						if name == filepath.Base(path) {
							exact = true
						}
					}
					if e == io.EOF {
						break
					}
					if e != nil {
						dir.Close()
						return nil, e
					}
				}
				dir.Close()
				if !exact {
					return nil, fmt.Errorf("destination cannot represent exact path: %s", entry.Path)
				}
			}

			switch entry.Kind {
			case "directory":
				if e = directory(entry.Path); e != nil {
					return nil, e
				}
			case "symlink":
				if entry.LinkTarget == nil {
					return nil, fmt.Errorf("link text missing")
				}
				if existing, e := os.Readlink(path); e == nil {
					if existing != *entry.LinkTarget {
						return nil, fmt.Errorf("incomplete output link changed")
					}
				} else if e = os.Symlink(*entry.LinkTarget, path); e != nil {
					return nil, e
				}
			case "file":
				if entry.Hash == nil {
					return nil, fmt.Errorf("file identity missing")
				}
				if _, e = os.Lstat(path); e == nil {
					hash, e := fileHash(path)
					if e != nil || hash != *entry.Hash {
						return nil, fmt.Errorf("incomplete output file changed")
					}
				} else {
					result, metadata, e := c.Download(ctx, version, entry.Path, path, false)
					if e != nil {
						return nil, e
					}
					if metadata != nil || result.SHA256 != *entry.Hash {
						return nil, fmt.Errorf("entry changed during download")
					}
				}
				var size int64
				if _, e = fmt.Sscan(entry.Size, &size); e != nil {
					return nil, e
				}
				bytesCopied += size
			default:
				return nil, fmt.Errorf("unsupported destination entry kind")
			}

			encoded, e := json.Marshal(entry)
			if e != nil {
				return nil, e
			}
			record, e := os.OpenFile(recordPath(entry.Path), os.O_CREATE|os.O_EXCL|os.O_WRONLY|syscall.O_NOFOLLOW, 0600)
			if e == nil {
				_, e = record.Write(encoded)
				e = errors.Join(e, record.Sync(), record.Close())
				if e != nil {
					return nil, e
				}
			} else if os.IsExist(e) {
				prior, e := os.ReadFile(recordPath(entry.Path))
				if e != nil || string(prior) != string(encoded) {
					return nil, fmt.Errorf("directory inventory changed")
				}
			} else {
				return nil, e
			}
			count++
		}
		if page.NextCursor == nil {
			break
		}
		cursor = *page.NextCursor
	}
	// Sync every represented directory in finite ReadDir batches before publishing
	// the complete staging tree. The same loop rejects late inserted special files.

	if e = syncDirectory(ledger); e != nil {
		return nil, e
	}
	verified := int64(0)
	var syncTree func(string, string) error
	syncTree = func(path, relative string) error {
		dir, e := os.Open(path)
		if e != nil {
			return e
		}
		defer dir.Close()
		for {
			entries, e := dir.ReadDir(64)
			for _, entry := range entries {
				name := entry.Name()
				if relative != "" {
					name = relative + "/" + name
				}
				record, e := os.OpenFile(recordPath(name), os.O_RDONLY|syscall.O_NOFOLLOW, 0)
				if e != nil {
					return fmt.Errorf("unexpected incomplete-output entry: %s", name)
				}
				var expected query.File
				e = json.NewDecoder(record).Decode(&expected)
				record.Close()
				if e != nil || expected.Path != name {
					return fmt.Errorf("directory inventory identity mismatch")
				}
				verified++
				child := filepath.Join(path, entry.Name())
				info, e := os.Lstat(child)
				if e != nil {
					return e
				}
				switch expected.Kind {
				case "directory":
					if !info.IsDir() {
						return fmt.Errorf("output kind changed")
					}
					if e = syncTree(child, name); e != nil {
						return e
					}
				case "file":
					if !info.Mode().IsRegular() || expected.Hash == nil {
						return fmt.Errorf("output kind changed")
					}
					hash, e := fileHash(child)
					if e != nil || hash != *expected.Hash {
						return fmt.Errorf("output bytes changed")
					}
				case "symlink":
					if info.Mode()&os.ModeSymlink == 0 || expected.LinkTarget == nil {
						return fmt.Errorf("output kind changed")
					}
					text, e := os.Readlink(child)
					if e != nil || text != *expected.LinkTarget {
						return fmt.Errorf("output link changed")
					}
				default:
					return fmt.Errorf("unexpected output kind")
				}
			}
			if e == io.EOF {
				break
			}
			if e != nil {
				return e
			}
		}
		return dir.Sync()
	}
	if e = syncTree(stage, ""); e != nil {
		return nil, e
	}
	if verified != count {
		return nil, fmt.Errorf("incomplete output inventory")
	}
	if e = renameNoReplace(stage, full); e != nil {
		return nil, e
	}
	if e = syncDirectory(filepath.Dir(full)); e != nil {
		return nil, e
	}
	if e = os.RemoveAll(ledger); e != nil {
		return nil, e
	}
	if e = os.Remove(markerPath); e != nil {
		return nil, e
	}
	return map[string]any{"version_id": version, "destination": full, "entries": fmt.Sprint(count), "byte_size": fmt.Sprint(bytesCopied), "complete": true, "format": "original_directory"}, nil
}
