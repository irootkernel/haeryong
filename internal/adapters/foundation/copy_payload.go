package foundation

import (
	"errors"
	"io/fs"
	"os"
	"strings"

	"haeryong.local/haeryong/internal/domain"
)

// Only path/type/hash facts are retained, never file or archive contents. A
// version's facts become available to metadata only after its complete streamed
// inventory, bytes and actual namespace have been verified.
type exportPayloadEntry struct{ kind, hash string }

// ZIP entries describe a directory tree even when parent headers are omitted.
// Match PutUploadEntry's original rule: an explicit file/link cannot also be
// an implied directory. Keep raw entries separately for byte reads and the
// existing frozen formatted-output identity.
func (v *exportView) indexZIPNamespace() error {
	v.archiveKinds = map[string]string{}
	for name, entry := range v.entries {
		if e := v.ctx.Err(); e != nil {
			return e
		}
		v.archiveKinds[name] = exportEntryKind(entry.Mode())
	}
	for name := range v.entries {
		parent := name
		for {
			if e := v.ctx.Err(); e != nil {
				return e
			}
			slash := strings.LastIndexByte(parent, '/')
			if slash < 0 {
				break
			}
			parent = parent[:slash]
			if kind, exists := v.archiveKinds[parent]; exists && kind != "directory" {
				return domain.ErrContentIntegrity
			}
			v.archiveKinds[parent] = "directory"
		}
	}
	return nil
}

func (v *exportView) payloadEntry(root, name string) (exportPayloadEntry, bool) {
	entry, ok := v.payloads[root][name]
	return entry, ok
}

func exportEntryKind(mode fs.FileMode) string {
	switch {
	case mode&fs.ModeSymlink != 0:
		return "symlink"
	case mode.IsDir():
		return "directory"
	case mode.IsRegular():
		return "file"
	}
	return ""
}

// Check each literal root component against directory enumeration, rather than
// Lstat of a potentially aliased/case-equivalent full path. Cache only these
// parent listings ('.' and 'versions' in format 1), avoiding a whole-export walk
// for every selected version or rules about unrelated metadata objects.
func (v *exportView) exactPayloadRoot(handle *os.Root, root string) error {
	if root == "." {
		return nil
	}
	if v.rootDirectories == nil {
		v.rootDirectories = map[string]map[string]string{}
	}
	parent := "."
	for _, component := range strings.Split(root, "/") {
		if e := v.ctx.Err(); e != nil {
			return e
		}
		children, ok := v.rootDirectories[parent]
		if !ok {
			directory, e := handle.Open(parent)
			if e != nil {
				return e
			}
			entries, e := directory.ReadDir(-1)
			e = errors.Join(e, directory.Close())
			if e != nil {
				return e
			}
			children = map[string]string{}
			for _, entry := range entries {
				if e := v.ctx.Err(); e != nil {
					return e
				}
				children[entry.Name()] = exportEntryKind(entry.Type())
			}
			v.rootDirectories[parent] = children
		}
		if children[component] != "directory" {
			return domain.ErrContentIntegrity
		}
		if parent == "." {
			parent = component
		} else {
			parent += "/" + component
		}
	}
	return nil
}

// Enumerate a typed logical namespace. Directory names come from non-following
// traversal after literal root checks; ZIP names include its implied parents.
func (v *exportView) walkNamespace(root string, visit func(string, string) error) error {
	if e := v.ctx.Err(); e != nil {
		return e
	}
	if v.archive != nil {
		// The ZIP prefix may be implicit. An explicit entry at the claimed
		// directory root, however, cannot contradict that logical type.
		if root != "." {
			parent := ""
			for _, component := range strings.Split(root, "/") {
				if parent != "" {
					parent += "/"
				}
				parent += component
				if kind, exists := v.archiveKinds[parent]; exists && kind != "directory" {
					return domain.ErrContentIntegrity
				}
			}
		}
		prefix := ""
		if root != "." {
			prefix = root + "/"
		}
		for name, kind := range v.archiveKinds {
			if e := v.ctx.Err(); e != nil {
				return e
			}
			if name != root && strings.HasPrefix(name, prefix) {
				if e := visit(strings.TrimPrefix(name, prefix), kind); e != nil {
					return e
				}
			}
		}
	} else {
		handle, e := os.OpenRoot(v.directory)
		if e != nil {
			return e
		}
		defer handle.Close()
		if e = v.exactPayloadRoot(handle, root); e != nil {
			return e
		}
		prefix := ""
		if root != "." {
			prefix = root + "/"
		}
		e = fs.WalkDir(handle.FS(), root, func(name string, entry fs.DirEntry, e error) error {
			if e != nil {
				return e
			}
			if e := v.ctx.Err(); e != nil {
				return e
			}
			if name == root {
				return nil
			}
			return visit(strings.TrimPrefix(name, prefix), exportEntryKind(entry.Type()))
		})
		if e != nil {
			return e
		}
	}
	return nil
}

// Set equality uses exact names and types, not reachability or cardinality alone.
func (v *exportView) verifyPayloadNamespace(root string, expected map[string]exportPayloadEntry) error {
	count := 0
	e := v.walkNamespace(root, func(name, kind string) error {
		entry, ok := expected[name]
		if !ok || entry.kind != kind {
			return domain.ErrContentIntegrity
		}
		count++
		return nil
	})
	if e != nil {
		return e
	}
	if count != len(expected) {
		return domain.ErrContentIntegrity
	}
	if v.payloads == nil {
		v.payloads = map[string]map[string]exportPayloadEntry{}
	}
	v.payloads[root] = expected
	return nil
}

// Each payload was independently verified. Reject any structural version root
// not selected by the index; implicit empty ZIP roots need no redundant header
// and cannot be numerically exchanged for a different represented directory.
func (v *exportView) verifyVersionRoots(roots map[string]bool) error {
	return v.walkNamespace("versions", func(name, kind string) error {
		id, _, child := strings.Cut(name, "/")
		if !roots["versions/"+id] || !child && kind != "directory" {
			return domain.ErrContentIntegrity
		}
		return nil
	})
}
