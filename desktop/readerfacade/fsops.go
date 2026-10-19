// Package readerfacade native filesystem operations (TASK-003-05 M3).
//
// Owners select a source tree and a download destination through the injected
// panel presenter; Go grants only opaque handles bound to the exact selected
// root/target, the transfer purpose and the current attachment era. There is
// no arbitrary JavaScript path/URL/filesystem proxy: every operation resolves
// relative names inside the anchored os.Root, re-verifies captured entry
// identities around file reads, and publishes destinations through an
// FD-anchored atomic no-replace rename so a replaced parent path can never
// retarget a publication.
//
// Linearization: each handle carries an operation mutex. An operation locks
// it, rechecks that the handle is still the live era's registered handle, and
// only then performs filesystem effects; release paths (dispose, window
// close, explicit release, adoption-era replacement) first detach the handle
// from the era map and then drain the same mutex before closing resources.
// A publication that linearized before a disposal therefore completes on
// live resources, and a disposal that linearized first leaves the racing
// operation a typed stale answer with no filesystem effect — a late picker
// result or in-flight close can never publish under a discarded authority.
//
// Byte/range sizes are transport granularity, never content quotas; hidden,
// Unicode and arbitrarily deep trees are preserved exactly, and names or link
// targets that cannot survive JSON transport fail typed before any
// replacement could occur.
package readerfacade

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"unicode/utf8"

	importer "haeryong.local/haeryong/internal/application/import"
)

// maxSourceRangeBytes is the chunk transport granularity for readSourceRange;
// it is not a content cap of any kind.
const maxSourceRangeBytes = 1024 * 1024

// sourceEntriesPageSize bounds one metadata page; paging is local cursoring
// of the already captured enumeration, not a tree cap.
const sourceEntriesPageSize = 512

// PickerPresenter is the injectable owner-panel seam. Production wiring (M4)
// installs the Wails dialog presenter; headless tests inject scripted paths.
// A canceled panel returns canceled=true (an honest owner cancel, not an
// error). A nil presenter answers the typed unavailable failure.
type PickerPresenter struct {
	ChooseSource      func(mode string) (path string, canceled bool, err error)
	ChooseDestination func(suggestedName string, mode string) (path string, canceled bool, err error)
}

// SetPickerPresenter installs the owner-panel seam. Call before serving.
func (s *ReaderService) SetPickerPresenter(picker *PickerPresenter) {
	s.mu.Lock()
	s.picker = picker
	s.mu.Unlock()
}

type sourceEntry struct {
	Path       string
	Kind       string // file | directory | symlink
	Size       int64
	LinkTarget string
	// Info captures the exact dev/ino (os.SameFile), size and nanosecond
	// mtime identity at enumeration time.
	Info os.FileInfo
}

type sourceHandle struct {
	id            string
	mode          string // directory | archive | file
	purpose       string // import-input
	attachmentID  string
	gen           uint64
	root          *os.Root
	selectionName string
	entries       []*sourceEntry
	// opMu serializes filesystem effects against release (see the package
	// comment). released/map membership are owned by s.mu.
	opMu     sync.Mutex
	released bool
}

type destState int

const (
	destOpenState destState = iota
	// destAborting marks an abort whose own-temp cleanup has NOT completed:
	// retries keep re-attempting the removal with the typed exact path until
	// it actually succeeds; nothing is claimed done while a temp may remain.
	destAborting
	destAborted
	destPublished
)

type destHandle struct {
	id           string
	purpose      string // download-output
	attachmentID string
	gen          uint64
	root         *os.Root
	dirPath      string // owner-selected absolute directory, for honest error details
	finalName    string // relative inside the anchored root
	label        string
	// opMu guards every field and filesystem effect below (temp creation,
	// writes, verification, publication, abort) against concurrent operations
	// and release; see the package comment. s.mu guards only the era and the
	// handle maps.
	opMu          sync.Mutex
	file          *os.File
	tempName      string
	expectedHash  string
	expectedSize  int64
	expecting     bool
	replace       bool
	state         destState
}

func sourceChanged(details map[string]any) *wireError {
	return &wireError{Status: 409, Code: "NATIVE_SOURCE_CHANGED", Retryable: false, Details: details}
}

func handleStale() *wireError {
	return &wireError{Status: 409, Code: "NATIVE_HANDLE_STALE", Retryable: false}
}

func payloadInvalid(detail string) *wireError {
	return &wireError{Status: 400, Code: "NATIVE_PAYLOAD_INVALID", Retryable: false, Details: map[string]any{"detail": detail}}
}

// eraReplaced is the typed answer for a picker result or operation whose
// captured authority was replaced before it could register or act.
func eraReplaced() *wireError {
	return &wireError{Status: 409, Code: "NATIVE_SESSION_REPLACED", Retryable: false}
}

// lookupSource returns the era-verified, still-registered source handle or
// the typed stale error. Wrong-purpose ids never resolve as sources.
func (s *ReaderService) lookupSource(id string) (*sourceHandle, *wireError) {
	s.mu.Lock()
	defer s.mu.Unlock()
	handle, ok := s.sources[id]
	if !ok || handle.purpose != "import-input" || handle.released {
		return nil, handleStale()
	}
	if handle.attachmentID != s.attachmentID || handle.gen != s.generation {
		return nil, handleStale()
	}
	return handle, nil
}

// sourceEraLive rechecks registration and era under s.mu; callers hold the
// handle's opMu, making this the operation's linearization point against
// release and authority replacement.
func (s *ReaderService) sourceEraLive(handle *sourceHandle) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	registered, ok := s.sources[handle.id]
	return ok && registered == handle && !handle.released &&
		handle.attachmentID == s.attachmentID && handle.gen == s.generation
}

func (s *ReaderService) lookupDest(id string) (*destHandle, *wireError) {
	s.mu.Lock()
	defer s.mu.Unlock()
	handle, ok := s.dests[id]
	if !ok || handle.purpose != "download-output" {
		return nil, handleStale()
	}
	if handle.attachmentID != s.attachmentID || handle.gen != s.generation {
		return nil, handleStale()
	}
	return handle, nil
}

func (s *ReaderService) destEraLive(handle *destHandle) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	registered, ok := s.dests[handle.id]
	return ok && registered == handle &&
		handle.attachmentID == s.attachmentID && handle.gen == s.generation
}

// entryIdentityMatches compares the freshly stat'ed entry against the
// captured enumeration identity: same file (dev/ino), same kind, same size
// and same nanosecond mtime. Size and mtime come from the captured FileInfo
// itself, so directories and symlinks compare with their real stat values
// instead of a zeroed field. A deliberate same-inode/size/mtime restoration
// remains beyond this check; no OS-level atomic snapshot is claimed.
func entryIdentityMatches(captured *sourceEntry, current os.FileInfo) bool {
	if !os.SameFile(captured.Info, current) {
		return false
	}
	if captured.Info.Size() != current.Size() || !captured.Info.ModTime().Equal(current.ModTime()) {
		return false
	}
	return captured.Info.Mode().Type() == current.Mode().Type()
}

// joinRootName builds a child name inside the anchored root without ever
// producing "." or ".." elements the root would reject.
func joinRootName(dir, name string) string {
	if dir == "" || dir == "." {
		return name
	}
	return dir + "/" + name
}

// enumerateSource walks the anchored selection capturing every entry with its
// identity: represented empty directories, symlink link text (never
// followed), hidden and arbitrary Unicode names, with deterministic ordering.
// A name or link target that is not valid UTF-8 fails typed before any JSON
// replacement could occur.
func enumerateSource(root *os.Root, top string, includeTop bool) ([]*sourceEntry, error) {
	var out []*sourceEntry
	var walk func(rel, disk string) error
	walk = func(rel, disk string) error {
		info, err := root.Lstat(disk)
		if err != nil {
			return err
		}
		switch {
		case info.Mode()&os.ModeSymlink != 0:
			target, err := root.Readlink(disk)
			if err != nil {
				return err
			}
			if !utf8.ValidString(target) || strings.ContainsRune(target, 0) {
				return &wireError{Status: 400, Code: "NATIVE_PATH_INVALID", Retryable: false, Details: map[string]any{"path": rel, "reason": "link_target_not_representable"}}
			}
			if rel != "" {
				out = append(out, &sourceEntry{Path: rel, Kind: "symlink", LinkTarget: target, Info: info})
			}
		case info.IsDir():
			if rel != "" {
				out = append(out, &sourceEntry{Path: rel, Kind: "directory", Info: info})
			}
			children, err := root.Open(disk)
			if err != nil {
				return err
			}
			names, readErr := children.Readdirnames(-1)
			closeErr := children.Close()
			if readErr != nil {
				return readErr
			}
			if closeErr != nil {
				return closeErr
			}
			for _, name := range names {
				if !utf8.ValidString(name) || strings.ContainsRune(name, 0) {
					return &wireError{Status: 400, Code: "NATIVE_PATH_INVALID", Retryable: false, Details: map[string]any{"path": rel, "reason": "name_not_representable"}}
				}
				childRel := name
				if rel != "" {
					childRel = rel + "/" + name
				}
				if err := importer.ValidatePath(childRel); err != nil {
					return &wireError{Status: 400, Code: "NATIVE_PATH_INVALID", Retryable: false, Details: map[string]any{"path": childRel}}
				}
				if err := walk(childRel, joinRootName(disk, name)); err != nil {
					return err
				}
			}
		case info.Mode().IsRegular():
			if rel != "" {
				out = append(out, &sourceEntry{Path: rel, Kind: "file", Size: info.Size(), Info: info})
			}
		default:
			return &wireError{Status: 400, Code: "NATIVE_PATH_INVALID", Retryable: false, Details: map[string]any{"path": rel, "reason": "unsupported_filesystem_object"}}
		}
		return nil
	}
	if err := walk("", top); err != nil {
		return nil, err
	}
	if includeTop {
		// File/archive modes: the single selected regular file is the whole
		// captured source.
		info, err := root.Lstat(top)
		if err != nil {
			return nil, err
		}
		out = append(out, &sourceEntry{Path: top, Kind: "file", Size: info.Size(), Info: info})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out, nil
}

// registerSource performs the late-result era and picker-lease recheck and
// registers the new handle atomically: a picker answer arriving after its
// authority was replaced (reattach, activation, logout, disposal) or after
// an owned cleanup closed the window never becomes usable.
func (s *ReaderService) registerSource(handle *sourceHandle, leaseEpoch uint64) *wireError {
	s.mu.Lock()
	defer s.mu.Unlock()
	if handle.attachmentID != s.attachmentID || handle.gen != s.generation || s.fsEpoch != leaseEpoch {
		return eraReplaced()
	}
	s.sources[handle.id] = handle
	return nil
}

func (s *ReaderService) registerDest(handle *destHandle, leaseEpoch uint64) *wireError {
	s.mu.Lock()
	defer s.mu.Unlock()
	if handle.attachmentID != s.attachmentID || handle.gen != s.generation || s.fsEpoch != leaseEpoch {
		return eraReplaced()
	}
	s.dests[handle.id] = handle
	return nil
}

func (s *ReaderService) chooseImportSource(payload json.RawMessage, auth authority) (any, *wireError) {
	var in struct {
		Mode string `json:"mode"`
	}
	if werr := decodePayload(payload, &in); werr != nil {
		return nil, werr
	}
	if in.Mode != "directory" && in.Mode != "archive" && in.Mode != "file" {
		return nil, payloadInvalid("mode must be directory, archive or file")
	}
	s.mu.Lock()
	picker := s.picker
	leaseEpoch := s.fsEpoch
	s.mu.Unlock()
	if picker == nil || picker.ChooseSource == nil {
		return nil, &wireError{Status: 503, Code: "NATIVE_PICKER_UNAVAILABLE", Retryable: false}
	}
	path, canceled, err := picker.ChooseSource(in.Mode)
	if err != nil {
		return nil, &wireError{Status: 500, Code: "NATIVE_PICKER_FAILED", Retryable: false, Details: map[string]any{"io_error": err.Error()}}
	}
	if canceled {
		return map[string]any{"canceled": true}, nil
	}
	info, err := os.Lstat(path)
	if err != nil {
		return nil, &wireError{Status: 400, Code: "NATIVE_PATH_INVALID", Retryable: false, Details: map[string]any{"reason": "selected path unreadable"}}
	}
	anchorPath, top := path, "."
	if info.IsDir() {
		if in.Mode != "directory" {
			return nil, payloadInvalid("selected path is a directory but mode is " + in.Mode)
		}
	} else if info.Mode().IsRegular() {
		if in.Mode == "directory" {
			return nil, payloadInvalid("selected path is a regular file but mode is directory")
		}
		anchorPath, top = filepath.Dir(path), filepath.Base(path)
	} else {
		return nil, &wireError{Status: 400, Code: "NATIVE_PATH_INVALID", Retryable: false, Details: map[string]any{"reason": "selected object is not a directory or regular file"}}
	}
	root, err := os.OpenRoot(anchorPath)
	if err != nil {
		return nil, &wireError{Status: 400, Code: "NATIVE_PATH_INVALID", Retryable: false, Details: map[string]any{"reason": "selected root unavailable"}}
	}
	entries, err := enumerateSource(root, top, !info.IsDir())
	if err != nil {
		root.Close()
		if werr, ok := err.(*wireError); ok {
			return nil, werr
		}
		return nil, &wireError{Status: 400, Code: "NATIVE_PATH_INVALID", Retryable: false, Details: map[string]any{"reason": "enumeration failed", "io_error": err.Error()}}
	}
	id := randomID()
	if id == "" {
		root.Close()
		return nil, networkError()
	}
	handle := &sourceHandle{id: id, mode: in.Mode, purpose: "import-input", attachmentID: auth.attachmentID, gen: auth.gen, root: root, selectionName: filepath.Base(path), entries: entries}
	if werr := s.registerSource(handle, leaseEpoch); werr != nil {
		root.Close()
		return nil, werr
	}
	page, werr := sourceEntryPage(handle, "0")
	if werr != nil {
		s.releaseSourceHandle(id)
		return nil, werr
	}
	return map[string]any{
		"handle":         id,
		"selection_name": handle.selectionName,
		"mode":           in.Mode,
		"entries":        page,
		"limits": map[string]any{
			"empty_directories_represented": true,
			"symlinks_represented":          true,
		},
	}, nil
}

// sourceEntryPage pages the captured metadata in deterministic order. The
// cursor is opaque local pagination, unrelated to any server upload cursor.
func sourceEntryPage(handle *sourceHandle, cursor string) (map[string]any, *wireError) {
	start := 0
	if cursor != "" {
		parsed, err := strconv.Atoi(cursor)
		if err != nil || parsed < 0 || parsed > len(handle.entries) {
			return nil, payloadInvalid("unknown entries cursor")
		}
		start = parsed
	}
	end := len(handle.entries)
	if end-start > sourceEntriesPageSize {
		end = start + sourceEntriesPageSize
	}
	items := make([]map[string]any, 0, end-start)
	for _, entry := range handle.entries[start:end] {
		item := map[string]any{"path": entry.Path, "kind": entry.Kind}
		if entry.Kind == "file" {
			item["size"] = entry.Size
		}
		if entry.Kind == "symlink" {
			item["link_target"] = entry.LinkTarget
		}
		items = append(items, item)
	}
	var next any
	if end < len(handle.entries) {
		next = strconv.Itoa(end)
	}
	return map[string]any{"items": items, "next_cursor": next}, nil
}

func (s *ReaderService) sourceEntries(payload json.RawMessage) (any, *wireError) {
	var in struct {
		Handle string  `json:"handle"`
		Cursor *string `json:"cursor"`
	}
	if werr := decodePayload(payload, &in); werr != nil {
		return nil, werr
	}
	handle, werr := s.lookupSource(in.Handle)
	if werr != nil {
		return nil, werr
	}
	handle.opMu.Lock()
	defer handle.opMu.Unlock()
	if !s.sourceEraLive(handle) {
		return nil, handleStale()
	}
	cursor := ""
	if in.Cursor != nil {
		cursor = *in.Cursor
	}
	return sourceEntryPage(handle, cursor)
}

func (s *ReaderService) readSourceRange(payload json.RawMessage) (any, *wireError) {
	var in struct {
		Handle string `json:"handle"`
		Path   string `json:"path"`
		Offset int64  `json:"offset"`
		Length int64  `json:"length"`
	}
	if werr := decodePayload(payload, &in); werr != nil {
		return nil, werr
	}
	if in.Length < 0 || in.Length > maxSourceRangeBytes || in.Offset < 0 {
		return nil, payloadInvalid("range beyond the 1MiB transport granularity")
	}
	handle, werr := s.lookupSource(in.Handle)
	if werr != nil {
		return nil, werr
	}
	handle.opMu.Lock()
	defer handle.opMu.Unlock()
	if !s.sourceEraLive(handle) {
		return nil, handleStale()
	}
	var captured *sourceEntry
	for _, entry := range handle.entries {
		if entry.Path == in.Path {
			captured = entry
			break
		}
	}
	if captured == nil || captured.Kind != "file" {
		return nil, payloadInvalid("path is not a captured file of this source")
	}
	// Pre-read identity check against the captured enumeration identity.
	current, err := handle.root.Lstat(in.Path)
	if err != nil {
		return nil, sourceChanged(map[string]any{"path": in.Path, "reason": "stat failed"})
	}
	if !entryIdentityMatches(captured, current) {
		return nil, sourceChanged(map[string]any{"path": in.Path})
	}
	file, err := handle.root.OpenFile(in.Path, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return nil, sourceChanged(map[string]any{"path": in.Path, "reason": "open failed"})
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !os.SameFile(captured.Info, opened) || opened.Size() != captured.Size || !captured.Info.ModTime().Equal(opened.ModTime()) {
		return nil, sourceChanged(map[string]any{"path": in.Path})
	}
	if in.Offset+in.Length > opened.Size() {
		return nil, payloadInvalid("range beyond the captured file size")
	}
	buffer := make([]byte, in.Length)
	read, err := file.ReadAt(buffer, in.Offset)
	if err != nil && (err != io.EOF || int64(read) != in.Length) {
		return nil, sourceChanged(map[string]any{"path": in.Path, "reason": "read failed"})
	}
	if int64(read) != in.Length {
		return nil, sourceChanged(map[string]any{"path": in.Path, "reason": "short read"})
	}
	// Post-read identity re-check: the delivered bytes must belong to the
	// captured file instance.
	after, err := file.Stat()
	if err != nil || !os.SameFile(captured.Info, after) || after.Size() != captured.Size || !captured.Info.ModTime().Equal(after.ModTime()) {
		return nil, sourceChanged(map[string]any{"path": in.Path})
	}
	return map[string]any{"bytes_b64": base64.StdEncoding.EncodeToString(buffer)}, nil
}

func (s *ReaderService) releaseSource(payload json.RawMessage) (any, *wireError) {
	var in struct {
		Handle string `json:"handle"`
	}
	if werr := decodePayload(payload, &in); werr != nil {
		return nil, werr
	}
	s.mu.Lock()
	handle, ok := s.sources[in.Handle]
	if ok && (handle.purpose != "import-input" || handle.attachmentID != s.attachmentID || handle.gen != s.generation) {
		s.mu.Unlock()
		return nil, handleStale()
	}
	if ok {
		// Detach first (same-era re-release stays idempotent success), then
		// drain any in-flight operation before closing the anchored root.
		handle.released = true
		delete(s.sources, in.Handle)
	}
	s.mu.Unlock()
	if ok {
		handle.opMu.Lock()
		handle.root.Close()
		handle.opMu.Unlock()
	}
	return nil, nil
}

func (s *ReaderService) releaseSourceHandle(id string) {
	s.mu.Lock()
	handle, ok := s.sources[id]
	delete(s.sources, id)
	s.mu.Unlock()
	if ok {
		handle.released = true
		handle.opMu.Lock()
		handle.root.Close()
		handle.opMu.Unlock()
	}
}

// sourceVerifyStable re-stats every captured entry without reading bodies:
// the completion gate runs before the first S dispatch, so any changed
// identity blocks that completion honestly while staging stays retained.
func (s *ReaderService) sourceVerifyStable(payload json.RawMessage) (any, *wireError) {
	var in struct {
		Handle string `json:"handle"`
	}
	if werr := decodePayload(payload, &in); werr != nil {
		return nil, werr
	}
	handle, werr := s.lookupSource(in.Handle)
	if werr != nil {
		return nil, werr
	}
	handle.opMu.Lock()
	defer handle.opMu.Unlock()
	if !s.sourceEraLive(handle) {
		return nil, handleStale()
	}
	for _, entry := range handle.entries {
		current, err := handle.root.Lstat(entry.Path)
		if err != nil {
			return map[string]any{"stable": false, "changed_path": entry.Path}, nil
		}
		if entry.Kind == "symlink" {
			if current.Mode()&os.ModeSymlink == 0 {
				return map[string]any{"stable": false, "changed_path": entry.Path}, nil
			}
			target, err := handle.root.Readlink(entry.Path)
			if err != nil || target != entry.LinkTarget {
				return map[string]any{"stable": false, "changed_path": entry.Path}, nil
			}
			continue
		}
		if !entryIdentityMatches(entry, current) {
			return map[string]any{"stable": false, "changed_path": entry.Path}, nil
		}
	}
	return map[string]any{"stable": true}, nil
}

func (s *ReaderService) chooseDownloadDestination(payload json.RawMessage, auth authority) (any, *wireError) {
	var in struct {
		SuggestedName string `json:"suggested_name"`
		Mode          string `json:"mode"`
	}
	if werr := decodePayload(payload, &in); werr != nil {
		return nil, werr
	}
	if in.Mode != "directory" && in.Mode != "save-file" {
		return nil, payloadInvalid("mode must be directory or save-file")
	}
	if in.Mode == "directory" {
		if in.SuggestedName == "" || strings.ContainsAny(in.SuggestedName, "/\\") || in.SuggestedName == "." || in.SuggestedName == ".." || !utf8.ValidString(in.SuggestedName) {
			return nil, payloadInvalid("suggested_name must be one representable path element")
		}
	}
	s.mu.Lock()
	picker := s.picker
	leaseEpoch := s.fsEpoch
	s.mu.Unlock()
	if picker == nil || picker.ChooseDestination == nil {
		return nil, &wireError{Status: 503, Code: "NATIVE_PICKER_UNAVAILABLE", Retryable: false}
	}
	path, canceled, err := picker.ChooseDestination(in.SuggestedName, in.Mode)
	if err != nil {
		return nil, &wireError{Status: 500, Code: "NATIVE_PICKER_FAILED", Retryable: false, Details: map[string]any{"io_error": err.Error()}}
	}
	if canceled {
		return map[string]any{"canceled": true}, nil
	}
	info, err := os.Lstat(path)
	if err != nil {
		if !(in.Mode == "save-file" && os.IsNotExist(err)) {
			return nil, &wireError{Status: 400, Code: "NATIVE_PATH_INVALID", Retryable: false, Details: map[string]any{"reason": "selected destination unreadable"}}
		}
		// A save-file selection is a PROPOSED location: the owner may name a
		// file that does not exist yet. The exact existence probe below then
		// reports false and publication itself is the no-replace authority.
		info = nil
	}
	var dirPath, finalName string
	if in.Mode == "directory" {
		if !info.IsDir() {
			return nil, payloadInvalid("directory mode requires the selected path to be a directory")
		}
		dirPath, finalName = path, in.SuggestedName
	} else {
		if info != nil && info.IsDir() {
			return nil, payloadInvalid("save-file mode must select a file location, not a directory")
		}
		dirPath, finalName = filepath.Dir(path), filepath.Base(path)
	}
	root, err := os.OpenRoot(dirPath)
	if err != nil {
		return nil, &wireError{Status: 400, Code: "NATIVE_PATH_INVALID", Retryable: false, Details: map[string]any{"reason": "destination root unavailable"}}
	}
	// Exact existence probe inside the anchor (never inferred from sizes).
	exists := false
	if _, statErr := root.Lstat(finalName); statErr == nil {
		exists = true
	} else if !os.IsNotExist(statErr) {
		root.Close()
		return nil, &wireError{Status: 400, Code: "NATIVE_PATH_INVALID", Retryable: false, Details: map[string]any{"reason": "destination probe failed"}}
	}
	id := randomID()
	if id == "" {
		root.Close()
		return nil, networkError()
	}
	handle := &destHandle{
		id: id, purpose: "download-output", attachmentID: auth.attachmentID, gen: auth.gen,
		root: root, dirPath: dirPath, finalName: finalName, label: filepath.Join(filepath.Base(dirPath), finalName),
	}
	if werr := s.registerDest(handle, leaseEpoch); werr != nil {
		root.Close()
		return nil, werr
	}
	return map[string]any{"handle": id, "label": handle.label, "exists": exists}, nil
}

// nonceHex returns fresh random hex for attempt-unique sibling temp names so
// a crashed attempt's orphan can never block a later attempt. Entropy
// failure is a typed error (never a process-stable fallback id).
func nonceHex() (string, *wireError) {
	var raw [8]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", &wireError{Status: 500, Code: "NATIVE_ENTROPY_UNAVAILABLE", Retryable: true, Details: map[string]any{"io_error": err.Error()}}
	}
	return hex.EncodeToString(raw[:]), nil
}

func (s *ReaderService) destOpen(payload json.RawMessage) (any, *wireError) {
	var in struct {
		Handle string `json:"handle"`
		Intent *struct {
			Replace  *bool `json:"replace"`
			Expected *struct {
				SHA256   string `json:"sha256"`
				ByteSize int64  `json:"byte_size"`
			} `json:"expected"`
		} `json:"intent"`
	}
	if werr := decodePayload(payload, &in); werr != nil {
		return nil, werr
	}
	handle, werr := s.lookupDest(in.Handle)
	if werr != nil {
		return nil, werr
	}
	handle.opMu.Lock()
	defer handle.opMu.Unlock()
	if !s.destEraLive(handle) {
		return nil, handleStale()
	}
	if handle.state != destOpenState {
		return nil, handleStale()
	}
	if handle.file != nil {
		// One attempt per handle: a duplicate open must never replace the
		// live temp/FD references (that would leak the previous attempt).
		return nil, &wireError{Status: 409, Code: "NATIVE_DESTINATION_ALREADY_OPEN", Retryable: false}
	}
	binding := sha256.Sum256([]byte(handle.attachmentID + "\x00" + handle.finalName))
	nonce, werr := nonceHex()
	if werr != nil {
		return nil, werr
	}
	tempName := ".haeryong-" + hex.EncodeToString(binding[:6]) + "-" + nonce + ".partial"
	// The final target is never touched here: bytes go only to the private
	// sibling temp, so every pre-publication failure leaves it intact.
	file, err := handle.root.OpenFile(tempName, os.O_RDWR|os.O_CREATE|os.O_EXCL|syscall.O_NOFOLLOW, 0600)
	if err != nil {
		if os.IsExist(err) {
			return nil, &wireError{Status: 409, Code: "NATIVE_TEMP_UNAVAILABLE", Retryable: true}
		}
		return nil, &wireError{Status: 500, Code: "NATIVE_DESTINATION_IO_FAILED", Retryable: false, Details: map[string]any{"io_error": err.Error()}}
	}
	handle.tempName = tempName
	handle.file = file
	handle.replace = in.Intent != nil && in.Intent.Replace != nil && *in.Intent.Replace
	if in.Intent != nil && in.Intent.Expected != nil {
		handle.expecting = true
		handle.expectedHash = in.Intent.Expected.SHA256
		handle.expectedSize = in.Intent.Expected.ByteSize
	}
	return map[string]any{"opened": true}, nil
}

func (s *ReaderService) destWrite(payload json.RawMessage) (any, *wireError) {
	var in struct {
		Handle   string `json:"handle"`
		BytesB64 string `json:"bytes_b64"`
	}
	if werr := decodePayload(payload, &in); werr != nil {
		return nil, werr
	}
	handle, werr := s.lookupDest(in.Handle)
	if werr != nil {
		return nil, werr
	}
	handle.opMu.Lock()
	defer handle.opMu.Unlock()
	if !s.destEraLive(handle) || handle.state != destOpenState || handle.file == nil {
		return nil, handleStale()
	}
	raw, err := base64.StdEncoding.DecodeString(in.BytesB64)
	if err != nil {
		return nil, payloadInvalid("bytes_b64 is not valid base64")
	}
	if _, err := handle.file.Write(raw); err != nil {
		return nil, &wireError{Status: 500, Code: "NATIVE_DESTINATION_IO_FAILED", Retryable: false, Details: map[string]any{"io_error": err.Error(), "temp": filepath.Join(handle.dirPath, handle.tempName)}}
	}
	return map[string]any{"written": len(raw)}, nil
}

// removeOwnTemp deletes only this handle's attempt temp (never a prefix or
// glob) and reports an honest typed cleanup failure with the exact path.
// Caller holds handle.opMu.
func removeOwnTemp(handle *destHandle) *wireError {
	if handle.tempName == "" {
		return nil
	}
	if err := handle.root.Remove(handle.tempName); err != nil && !os.IsNotExist(err) {
		return &wireError{Status: 500, Code: "NATIVE_CLEANUP_FAILED", Retryable: false, Details: map[string]any{"temp": filepath.Join(handle.dirPath, handle.tempName), "io_error": err.Error()}}
	}
	handle.tempName = ""
	return nil
}

// finishDestPublish completes a successful publication: temp consumed, file
// closed, state published and the anchored root CLOSED (the attempt is
// terminal; only a small tombstone remains for honest repeated replies),
// with the directory-fsync durability verdict. A failed post-publication
// directory sync is an honest success warning, never an incomplete claim.
// Caller holds handle.opMu and rechecked the era.
func (s *ReaderService) finishDestPublish(handle *destHandle, file *os.File) map[string]any {
	handle.tempName = ""
	if file != nil {
		file.Close()
	}
	handle.file = nil
	var syncErr error
	if dirFile, err := handle.root.Open("."); err == nil {
		syncErr = dirFile.Sync()
		dirFile.Close()
	} else {
		syncErr = err
	}
	handle.state = destPublished
	handle.root.Close()
	if syncErr != nil {
		return map[string]any{"published": true, "durability": "unconfirmed"}
	}
	return map[string]any{"published": true, "durability": "confirmed"}
}

func (s *ReaderService) destClose(payload json.RawMessage) (any, *wireError) {
	var in struct {
		Handle string `json:"handle"`
	}
	if werr := decodePayload(payload, &in); werr != nil {
		return nil, werr
	}
	handle, werr := s.lookupDest(in.Handle)
	if werr != nil {
		return nil, werr
	}
	handle.opMu.Lock()
	defer handle.opMu.Unlock()
	if !s.destEraLive(handle) || handle.state != destOpenState || handle.file == nil {
		return nil, handleStale()
	}
	// abortTemp closes the attempt temp and removes it. A failed cleanup is
	// PRESERVED: when a primary failure also occurred, the typed cleanup
	// failure surfaces (with the primary code kept in the details), because
	// the exact surviving temp path is what the owner must be told about.
	abortTemp := func(primary *wireError) *wireError {
		if handle.file != nil {
			handle.file.Close()
			handle.file = nil
		}
		cleanup := removeOwnTemp(handle)
		if cleanup == nil {
			return primary
		}
		details := map[string]any{}
		for key, value := range cleanup.Details {
			details[key] = value
		}
		if primary != nil {
			details["primary_code"] = primary.Code
			for key, value := range primary.Details {
				if key != "temp" {
					details["primary_"+key] = value
				}
			}
		}
		return &wireError{Status: cleanup.Status, Code: cleanup.Code, Retryable: false, Details: details}
	}
	// Durable temp bytes first.
	if err := handle.file.Sync(); err != nil {
		return nil, abortTemp(&wireError{Status: 500, Code: "NATIVE_DESTINATION_IO_FAILED", Retryable: false, Details: map[string]any{"io_error": err.Error()}})
	}
	// Pre-publication verification against the SAME delivered representation:
	// the digest and length came from the one HTTP response the engine
	// streamed; a mismatch never publishes and never touches the target.
	if handle.expecting {
		if _, err := handle.file.Seek(0, io.SeekStart); err != nil {
			return nil, abortTemp(&wireError{Status: 500, Code: "NATIVE_DESTINATION_IO_FAILED", Retryable: false, Details: map[string]any{"io_error": err.Error()}})
		}
		hash := sha256.New()
		size, err := io.Copy(hash, handle.file)
		if err != nil {
			return nil, abortTemp(&wireError{Status: 500, Code: "NATIVE_DESTINATION_IO_FAILED", Retryable: false, Details: map[string]any{"io_error": err.Error()}})
		}
		actual := hex.EncodeToString(hash.Sum(nil))
		if actual != handle.expectedHash || size != handle.expectedSize {
			return nil, abortTemp(&wireError{Status: 409, Code: "NATIVE_DESTINATION_VERIFICATION_FAILED", Retryable: false, Details: map[string]any{
				"expected_sha256": handle.expectedHash, "actual_sha256": actual,
				"expected_bytes": handle.expectedSize, "actual_bytes": size,
			}})
		}
	}
	// Publication. Explicit owner-consented replace uses the root-anchored
	// plain rename (the original stays intact until this atomic instant);
	// the default is the FD-anchored no-replace rename inside the selected
	// directory, so an existing target is a typed conflict and a replaced
	// parent path can never retarget the publication. Both run under the
	// operation lock after the era recheck, so a disposal that linearized
	// first leaves this close a typed stale answer with no effect.
	if handle.replace {
		if err := handle.root.Rename(handle.tempName, handle.finalName); err != nil {
			return nil, abortTemp(&wireError{Status: 500, Code: "NATIVE_DESTINATION_IO_FAILED", Retryable: false, Details: map[string]any{"io_error": err.Error()}})
		}
		return s.finishDestPublish(handle, handle.file), nil
	}
	dirFile, err := handle.root.Open(".")
	if err != nil {
		return nil, abortTemp(&wireError{Status: 500, Code: "NATIVE_DESTINATION_IO_FAILED", Retryable: false, Details: map[string]any{"io_error": err.Error()}})
	}
	renameErr := renameNoReplaceDir(int(dirFile.Fd()), handle.tempName, handle.finalName)
	if renameErr != nil {
		dirFile.Close()
		if os.IsExist(renameErr) {
			return nil, abortTemp(&wireError{Status: 409, Code: "NATIVE_DESTINATION_CONFLICT", Retryable: false, Details: map[string]any{"target": filepath.Join(handle.dirPath, handle.finalName)}})
		}
		return nil, abortTemp(&wireError{Status: 500, Code: "NATIVE_DESTINATION_IO_FAILED", Retryable: false, Details: map[string]any{"io_error": renameErr.Error()}})
	}
	dirFile.Close()
	return s.finishDestPublish(handle, handle.file), nil
}

func (s *ReaderService) destAbort(payload json.RawMessage) (any, *wireError) {
	var in struct {
		Handle string `json:"handle"`
	}
	if werr := decodePayload(payload, &in); werr != nil {
		return nil, werr
	}
	handle, werr := s.lookupDest(in.Handle)
	if werr != nil {
		return nil, werr
	}
	handle.opMu.Lock()
	defer handle.opMu.Unlock()
	if !s.destEraLive(handle) {
		return nil, handleStale()
	}
	if handle.state == destPublished {
		// The attempt already published: an abort must never claim it stopped
		// an incomplete transfer. (The root is already closed terminal-side.)
		return map[string]any{"aborted": false, "published": true}, nil
	}
	if handle.state == destAborted {
		// Idempotent explicit cancel of a fully cleaned, already aborted
		// attempt (terminal tombstone, no live resources).
		return map[string]any{"aborted": true}, nil
	}
	if handle.file != nil {
		handle.file.Close()
		handle.file = nil
	}
	// The abort is DONE only when the own-temp removal actually succeeded:
	// a failed cleanup keeps the attempt in destAborting with the typed
	// exact surviving path, and every retry re-attempts the removal.
	if cleanup := removeOwnTemp(handle); cleanup != nil {
		handle.state = destAborting
		return nil, cleanup
	}
	handle.state = destAborted
	// Fully cleaned abort: the anchored root closes now (terminal
	// tombstone); the exact selection anchor stays open only while a still
	// owned conflict decision may re-open the attempt.
	handle.root.Close()
	return map[string]any{"aborted": true}, nil
}

// releaseOwnedHandles drops every source/destination handle bound to the
// given attachment era (disposal, window-owned close, logout). Detach happens
// under s.mu first — new operations and late picker results see a dead era
// immediately — then each handle's operation lock drains before any resource
// is closed, so an effect that already linearized completes on live
// resources and never races the cleanup.
func (s *ReaderService) releaseOwnedHandles(attachmentID string) {
	s.mu.Lock()
	var sources []*sourceHandle
	var dests []*destHandle
	for id, handle := range s.sources {
		if handle.attachmentID == attachmentID {
			handle.released = true
			sources = append(sources, handle)
			delete(s.sources, id)
		}
	}
	for id, handle := range s.dests {
		if handle.attachmentID == attachmentID {
			dests = append(dests, handle)
			delete(s.dests, id)
		}
	}
	s.mu.Unlock()
	for _, handle := range sources {
		handle.opMu.Lock()
		handle.root.Close()
		handle.opMu.Unlock()
	}
	for _, handle := range dests {
		handle.opMu.Lock()
		if handle.file != nil {
			handle.file.Close()
			handle.file = nil
		}
		if handle.state == destOpenState || handle.state == destAborting {
			// Best-effort own-temp cleanup of an unfinished or unresolved
			// attempt; a publication never happens on the release path.
			removeOwnTemp(handle)
		}
		handle.root.Close()
		handle.opMu.Unlock()
	}
}

// parseFileValidator derives the strong content validator of one delivered
// file representation from its own response headers: the Repr-Digest sha-256
// and the exact Content-Length. Absent or malformed values yield no
// validator; the download adapter then fails typed instead of silently
// degrading to an unverified save.
func parseFileValidator(header http.Header) (sha256Hex string, byteSize int64, ok bool) {
	value := header.Get("Repr-Digest")
	if !strings.HasPrefix(value, "sha-256=:") || !strings.HasSuffix(value, ":") {
		return "", 0, false
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSuffix(strings.TrimPrefix(value, "sha-256=:"), ":"))
	if err != nil || len(raw) != sha256.Size {
		return "", 0, false
	}
	size, err := strconv.ParseInt(header.Get("Content-Length"), 10, 64)
	if err != nil || size < 0 {
		return "", 0, false
	}
	return hex.EncodeToString(raw), size, true
}
