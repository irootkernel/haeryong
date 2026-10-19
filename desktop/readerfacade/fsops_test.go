package readerfacade

// TASK-003-05 M3 filesystem operation regressions over the real invoke
// dispatch, a scripted panel presenter and a real on-disk tree: selection
// fidelity (hidden/Unicode/empty directories/symlink text/exact bytes),
// captured-identity change detection, era/purpose handle binding, FD-anchored
// destination publication (no-replace conflict, explicit consented replace,
// digest verification, parent replacement, attempt-unique orphans) and
// honest cleanup semantics. No HTTP daemon is involved: these operations
// never proxy the network.

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"haeryong.local/haeryong/internal/adapters/client"
)

func encodeB64(raw []byte) string { return base64.StdEncoding.EncodeToString(raw) }

func decodeB64(encoded string) ([]byte, error) { return base64.StdEncoding.DecodeString(encoded) }

func b64Std(raw []byte) string { return base64.StdEncoding.EncodeToString(raw) }

func newHeader(values map[string]string) http.Header {
	header := http.Header{}
	for key, value := range values {
		header.Set(key, value)
	}
	return header
}

func newFsService(t *testing.T) *ReaderService {
	t.Helper()
	service := NewReaderService("http://127.0.0.1:1", t.TempDir(), "test-token")
	service.mu.Lock()
	service.attachmentID = "fs-attachment"
	service.generation = 7
	service.mu.Unlock()
	return service
}

func fsCall(t *testing.T, service *ReaderService, operation string, payload any) (map[string]any, *wireError) {
	t.Helper()
	encoded, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	value, werr := service.invoke(operation, encoded)
	if value == nil {
		if werr != nil {
			return nil, werr
		}
		return map[string]any{}, nil
	}
	reply, ok := value.(map[string]any)
	if !ok {
		t.Fatalf("%s returned a non-object reply", operation)
	}
	return reply, nil
}

func requireFsCode(t *testing.T, werr *wireError, code string) {
	t.Helper()
	if werr == nil {
		t.Fatalf("expected typed failure %s, got success", code)
	}
	if werr.Code != code {
		t.Fatalf("expected typed failure %s, got %s (%v)", code, werr.Code, werr.Details)
	}
}

func setPicker(service *ReaderService, source func(string) (string, bool, error), dest func(string, string) (string, bool, error)) {
	service.SetPickerPresenter(&PickerPresenter{ChooseSource: source, ChooseDestination: dest})
}

func fixedPickerPath(path string) func(string) (string, bool, error) {
	return func(string) (string, bool, error) { return path, false, nil }
}

func chooseSource(t *testing.T, service *ReaderService, mode, path string) map[string]any {
	t.Helper()
	setPicker(service, fixedPickerPath(path), nil)
	reply, werr := fsCall(t, service, "chooseImportSource", map[string]any{"mode": mode})
	if werr != nil {
		t.Fatalf("chooseImportSource failed: %s %v", werr.Code, werr.Details)
	}
	return reply
}

func chooseDest(t *testing.T, service *ReaderService, suggested, mode, path string) map[string]any {
	t.Helper()
	setPicker(service, nil, func(string, string) (string, bool, error) { return path, false, nil })
	reply, werr := fsCall(t, service, "chooseDownloadDestination", map[string]any{"suggested_name": suggested, "mode": mode})
	if werr != nil {
		t.Fatalf("chooseDownloadDestination failed: %s %v", werr.Code, werr.Details)
	}
	return reply
}

func readAllSourceEntries(t *testing.T, service *ReaderService, handle string) []map[string]any {
	t.Helper()
	items := []map[string]any{}
	cursor := ""
	for {
		payload := map[string]any{"handle": handle}
		if cursor != "" {
			payload["cursor"] = cursor
		}
		reply, werr := fsCall(t, service, "sourceEntries", payload)
		if werr != nil {
			t.Fatalf("sourceEntries failed: %s %v", werr.Code, werr.Details)
		}
		switch page := reply["items"].(type) {
		case []map[string]any:
			items = append(items, page...)
		case []any:
			for _, raw := range page {
				items = append(items, raw.(map[string]any))
			}
		default:
			t.Fatalf("unexpected items page shape %T", reply["items"])
		}
		next, _ := reply["next_cursor"].(string)
		if next == "" {
			return items
		}
		cursor = next
	}
}

func readSourceBytes(t *testing.T, service *ReaderService, handle, path string, offset, length int64) []byte {
	t.Helper()
	reply, werr := fsCall(t, service, "readSourceRange", map[string]any{"handle": handle, "path": path, "offset": offset, "length": length})
	if werr != nil {
		t.Fatalf("readSourceRange(%s) failed: %s %v", path, werr.Code, werr.Details)
	}
	decoded, err := decodeB64(reply["bytes_b64"].(string))
	if err != nil {
		t.Fatalf("bytes_b64 decode: %v", err)
	}
	return decoded
}

func buildFidelityTree(t *testing.T) (string, map[string][]byte) {
	t.Helper()
	root := t.TempDir()
	files := map[string][]byte{
		"index.html":       []byte("<!doctype html><meta charset=utf-8><title>첫 페이지</title>"),
		".hidden":          []byte("hidden-by-convention"),
		"nested/페이지.html": []byte("unicode payload \x00\x01\x02 binary-safe"),
		"nested/deep/":     nil,
	}
	if err := os.MkdirAll(filepath.Join(root, "nested", "deep"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "index.html"), files["index.html"], 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".hidden"), files[".hidden"], 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "nested", "페이지.html"), files["nested/페이지.html"], 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("../index.html", filepath.Join(root, "nested", "link.html")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("/absolute/elsewhere", filepath.Join(root, "nested", "absolute-link")); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "empty-dir"), 0o755); err != nil {
		t.Fatal(err)
	}
	return root, files
}

func TestChooseImportSourceFidelity(t *testing.T) {
	service := newFsService(t)
	root, files := buildFidelityTree(t)
	reply := chooseSource(t, service, "directory", root)
	handle := reply["handle"].(string)
	if reply["selection_name"] != filepath.Base(root) {
		t.Fatalf("selection name %v", reply["selection_name"])
	}
	limits := reply["limits"].(map[string]any)
	if limits["empty_directories_represented"] != true || limits["symlinks_represented"] != true {
		t.Fatalf("native limits must disclose full representation: %v", limits)
	}
	byPath := map[string]map[string]any{}
	for _, raw := range readAllSourceEntries(t, service, handle) {
		byPath[raw["path"].(string)] = raw
	}
	wantKinds := map[string]string{
		".hidden":                    "file",
		"empty-dir":                  "directory",
		"index.html":                 "file",
		"nested":                     "directory",
		"nested/absolute-link":       "symlink",
		"nested/deep":                "directory",
		"nested/link.html":           "symlink",
		"nested/페이지.html": "file",
	}
	if len(byPath) != len(wantKinds) {
		t.Fatalf("captured %d entries, want %d: %v", len(byPath), len(wantKinds), byPath)
	}
	for path, kind := range wantKinds {
		entry, ok := byPath[path]
		if !ok {
			t.Fatalf("missing captured entry %q", path)
		}
		if entry["kind"] != kind {
			t.Fatalf("entry %q kind %v, want %q", path, entry["kind"], kind)
		}
	}
	if got := byPath["nested/link.html"]["link_target"]; got != "../index.html" {
		t.Fatalf("symlink text must be preserved exactly, got %v", got)
	}
	if got := byPath["nested/absolute-link"]["link_target"]; got != "/absolute/elsewhere" {
		t.Fatalf("absolute link text preserved as data, got %v", got)
	}
	if got := byPath["nested/페이지.html"]["size"]; got != int64(len(files["nested/페이지.html"])) {
		t.Fatalf("exact size expected, got %v", got)
	}
	for path, want := range map[string][]byte{"index.html": files["index.html"], ".hidden": files[".hidden"], "nested/페이지.html": files["nested/페이지.html"]} {
		if got := readSourceBytes(t, service, handle, path, 0, int64(len(want))); string(got) != string(want) {
			t.Fatalf("exact bytes differ for %s", path)
		}
	}
	// Zero-byte and offset reads are exact.
	empty := filepath.Join(root, "empty.bin")
	if err := os.WriteFile(empty, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	// (not part of the captured tree; use a fresh selection)
	reply2 := chooseSource(t, service, "file", empty)
	handle2 := reply2["handle"].(string)
	if got := readSourceBytes(t, service, handle2, filepath.Base(empty), 0, 0); len(got) != 0 {
		t.Fatalf("empty range expected empty bytes")
	}
	// Release is idempotent within the same era.
	for i := 0; i < 2; i++ {
		if _, werr := fsCall(t, service, "releaseSource", map[string]any{"handle": handle}); werr != nil {
			t.Fatalf("idempotent release failed: %s", werr.Code)
		}
	}
	if _, werr := fsCall(t, service, "readSourceRange", map[string]any{"handle": handle, "path": "index.html", "offset": 0, "length": 1}); werr == nil || werr.Code != "NATIVE_HANDLE_STALE" {
		t.Fatalf("released handle must be stale, got %v", werr)
	}
}

func TestChooseImportSourceInvalidUTF8FailsTyped(t *testing.T) {
	service := newFsService(t)
	root := t.TempDir()
	name := []byte{'i', 'n', 'v', 0xff, 'i', 'd'}
	_, createErr := os.OpenFile(filepath.Join(root, string(name)), os.O_WRONLY|os.O_CREATE, 0o644)
	if createErr == nil {
		// This filesystem accepted the unrepresentable name; the selection
		// must reject it typed before any JSON replacement.
		setPicker(service, fixedPickerPath(root), nil)
		_, werr := fsCall(t, service, "chooseImportSource", map[string]any{"mode": "directory"})
		requireFsCode(t, werr, "NATIVE_PATH_INVALID")
	} else {
		// darwin rejects unrepresentable names at creation; the typed gate
		// still guards non-darwin trees and is exercised via link targets.
		t.Logf("filesystem rejected an unrepresentable name at creation: %v", createErr)
	}
	// A representable tree whose only extra entry is a symlink with an
	// unrepresentable target fails typed (link text is preserved data, so it
	// must be transportable or the selection honestly fails).
	root2 := t.TempDir()
	if err := os.WriteFile(filepath.Join(root2, "keep.txt"), []byte("ok"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(string([]byte{'t', 0xfe}), filepath.Join(root2, "only-link")); err != nil {
		t.Fatalf("symlink with raw target bytes: %v", err)
	}
	setPicker(service, fixedPickerPath(root2), nil)
	_, werr := fsCall(t, service, "chooseImportSource", map[string]any{"mode": "directory"})
	requireFsCode(t, werr, "NATIVE_PATH_INVALID")
}

func TestReadSourceRangeDetectsCapturedIdentityChanges(t *testing.T) {
	service := newFsService(t)
	root := t.TempDir()
	target := filepath.Join(root, "data.bin")
	original := []byte("0123456789abcdef")
	if err := os.WriteFile(target, original, 0o644); err != nil {
		t.Fatal(err)
	}
	reply := chooseSource(t, service, "directory", root)
	handle := reply["handle"].(string)
	// Same-size content change (mtime differs) is detected.
	if err := os.WriteFile(target, []byte("fedcba9876543210"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, werr := fsCall(t, service, "readSourceRange", map[string]any{"handle": handle, "path": "data.bin", "offset": 0, "length": 4})
	requireFsCode(t, werr, "NATIVE_SOURCE_CHANGED")
	// Path replacement (new inode) is detected even with equal bytes/mtime.
	if err := os.Remove(target); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, original, 0o644); err != nil {
		t.Fatal(err)
	}
	_, werr = fsCall(t, service, "readSourceRange", map[string]any{"handle": handle, "path": "data.bin", "offset": 0, "length": 4})
	requireFsCode(t, werr, "NATIVE_SOURCE_CHANGED")
	// Size change is detected by the completion gate.
	if err := os.WriteFile(target, []byte("short"), 0o644); err != nil {
		t.Fatal(err)
	}
	verify, werr := fsCall(t, service, "sourceVerifyStable", map[string]any{"handle": handle})
	if werr != nil {
		t.Fatalf("sourceVerifyStable failed: %v", werr)
	}
	if verify["stable"] != false || verify["changed_path"] != "data.bin" {
		t.Fatalf("expected unstable verification with the changed path, got %v", verify)
	}
	// A stable untouched selection — including represented empty and nested
	// directories — verifies clean.
	root2 := t.TempDir()
	if err := os.WriteFile(filepath.Join(root2, "keep.bin"), original, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root2, "nested", "deep"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root2, "empty-dir"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root2, "nested", "deep", "leaf.bin"), original, 0o644); err != nil {
		t.Fatal(err)
	}
	reply2 := chooseSource(t, service, "directory", root2)
	verify2, werr := fsCall(t, service, "sourceVerifyStable", map[string]any{"handle": reply2["handle"].(string)})
	if werr != nil || verify2["stable"] != true {
		t.Fatalf("stable selection must verify clean: %v %v", verify2, werr)
	}
	if got := readSourceBytes(t, service, reply2["handle"].(string), "nested/deep/leaf.bin", 0, int64(len(original))); string(got) != string(original) {
		t.Fatalf("nested file bytes must read exactly")
	}
}

func TestHandleEraPurposeAndDisposalBinding(t *testing.T) {
	service := newFsService(t)
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.bin"), []byte("a"), 0o644); err != nil {
		t.Fatal(err)
	}
	sourceReply := chooseSource(t, service, "directory", root)
	sourceHandle := sourceReply["handle"].(string)
	destReply := chooseDest(t, service, "out.bin", "save-file", filepath.Join(root, "out.bin"))
	destHandle := destReply["handle"].(string)
	// Cross-purpose use never resolves.
	_, werr := fsCall(t, service, "destOpen", map[string]any{"handle": sourceHandle})
	requireFsCode(t, werr, "NATIVE_HANDLE_STALE")
	_, werr = fsCall(t, service, "readSourceRange", map[string]any{"handle": destHandle, "path": "a.bin", "offset": 0, "length": 1})
	requireFsCode(t, werr, "NATIVE_HANDLE_STALE")
	// A replaced era invalidates both handles.
	service.mu.Lock()
	service.generation = 8
	service.mu.Unlock()
	_, werr = fsCall(t, service, "readSourceRange", map[string]any{"handle": sourceHandle, "path": "a.bin", "offset": 0, "length": 1})
	requireFsCode(t, werr, "NATIVE_HANDLE_STALE")
	_, werr = fsCall(t, service, "destOpen", map[string]any{"handle": destHandle})
	requireFsCode(t, werr, "NATIVE_HANDLE_STALE")
	// Disposal detaches the owning attachment's live handles. After a fresh
	// re-attach the pre-dispose handles answer typed stale while a new-era
	// selection keeps working.
	if werr := service.dispose("fs-attachment"); werr != nil {
		t.Fatalf("dispose failed: %v", werr)
	}
	service.mu.Lock()
	service.attachmentID = "fs-attachment-2"
	service.generation = 9
	service.mu.Unlock()
	_, werr = fsCall(t, service, "readSourceRange", map[string]any{"handle": sourceHandle, "path": "a.bin", "offset": 0, "length": 1})
	requireFsCode(t, werr, "NATIVE_HANDLE_STALE")
	fresh := chooseSourceRaw(t, service, "directory", root)
	if got := readSourceBytes(t, service, fresh, "a.bin", 0, 1); string(got) != "a" {
		t.Fatalf("a new-era selection must keep working, got %q", got)
	}
}

func chooseSourceRaw(t *testing.T, service *ReaderService, mode, path string) string {
	t.Helper()
	setPicker(service, fixedPickerPath(path), nil)
	reply, werr := fsCall(t, service, "chooseImportSource", map[string]any{"mode": mode})
	if werr != nil {
		t.Fatalf("chooseImportSource failed: %s", werr.Code)
	}
	return reply["handle"].(string)
}

func publish(t *testing.T, service *ReaderService, destHandle string, payload []byte, intent map[string]any) map[string]any {
	t.Helper()
	openPayload := map[string]any{"handle": destHandle}
	if intent != nil {
		openPayload["intent"] = intent
	}
	if _, werr := fsCall(t, service, "destOpen", openPayload); werr != nil {
		t.Fatalf("destOpen failed: %s %v", werr.Code, werr.Details)
	}
	return publishAfterOpen(t, service, destHandle, payload, nil)
}

// publishAfterOpen finishes an ALREADY-open attempt (used by duplicate-open
// regressions where the original attempt must stay the sole owner).
func publishAfterOpen(t *testing.T, service *ReaderService, destHandle string, payload []byte, _ map[string]any) map[string]any {
	t.Helper()
	if len(payload) > 0 {
		if _, werr := fsCall(t, service, "destWrite", map[string]any{"handle": destHandle, "bytes_b64": encodeB64(payload)}); werr != nil {
			t.Fatalf("destWrite failed: %s %v", werr.Code, werr.Details)
		}
	}
	reply, werr := fsCall(t, service, "destClose", map[string]any{"handle": destHandle})
	if werr != nil {
		t.Fatalf("destClose failed: %s %v", werr.Code, werr.Details)
	}
	return reply
}

func digestOf(payload []byte) string {
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:])
}

func countTemps(dir string) int {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return -1
	}
	count := 0
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".haeryong-") && strings.HasSuffix(entry.Name(), ".partial") {
			count++
		}
	}
	return count
}

func TestDestinationPublishConflictReplaceVerifyAndOrphans(t *testing.T) {
	service := newFsService(t)
	dir := t.TempDir()
	payload := []byte("result archive bytes")

	t.Run("duplicate open never replaces a live attempt", func(t *testing.T) {
		reply := chooseDest(t, service, "dup.zip", "save-file", filepath.Join(dir, "dup.zip"))
		handle := reply["handle"].(string)
		if _, werr := fsCall(t, service, "destOpen", map[string]any{"handle": handle}); werr != nil {
			t.Fatal(werr)
		}
		if _, werr := fsCall(t, service, "destWrite", map[string]any{"handle": handle, "bytes_b64": encodeB64([]byte("first"))}); werr != nil {
			t.Fatal(werr)
		}
		_, werr := fsCall(t, service, "destOpen", map[string]any{"handle": handle})
		requireFsCode(t, werr, "NATIVE_DESTINATION_ALREADY_OPEN")
		// The original attempt still owns the publication.
		done := publishAfterOpen(t, service, handle, payload, nil)
		if done["published"] != true {
			t.Fatalf("original attempt must stay usable: %v", done)
		}
		if countTemps(dir) != 0 {
			t.Fatal("no leaked duplicate temp may remain")
		}
	})

	t.Run("default no-replace publishes once", func(t *testing.T) {
		reply := chooseDest(t, service, "first.zip", "save-file", filepath.Join(dir, "first.zip"))
		if reply["exists"] != false {
			t.Fatalf("fresh target must not exist: %v", reply)
		}
		handle := reply["handle"].(string)
		done := publish(t, service, handle, payload, map[string]any{"expected": map[string]any{"sha256": digestOf(payload), "byte_size": int64(len(payload))}})
		if done["published"] != true || done["durability"] != "confirmed" {
			t.Fatalf("unexpected close reply %v", done)
		}
		if got, err := os.ReadFile(filepath.Join(dir, "first.zip")); err != nil || string(got) != string(payload) {
			t.Fatalf("published bytes differ: %v %v", err, got)
		}
		if countTemps(dir) != 0 {
			t.Fatalf("no temp may remain after publication")
		}
	})

	t.Run("existing target is a typed conflict and stays untouched", func(t *testing.T) {
		existing := filepath.Join(dir, "exists.zip")
		if err := os.WriteFile(existing, []byte("ORIGINAL"), 0o644); err != nil {
			t.Fatal(err)
		}
		reply := chooseDest(t, service, "exists.zip", "save-file", existing)
		if reply["exists"] != true {
			t.Fatalf("exact existence probe failed: %v", reply)
		}
		_, werr := fsCall(t, service, "destOpen", map[string]any{"handle": reply["handle"].(string)})
		if werr != nil {
			t.Fatalf("destOpen: %v", werr)
		}
		if _, werr := fsCall(t, service, "destWrite", map[string]any{"handle": reply["handle"].(string), "bytes_b64": encodeB64(payload)}); werr != nil {
			t.Fatalf("destWrite: %v", werr)
		}
		_, werr = fsCall(t, service, "destClose", map[string]any{"handle": reply["handle"].(string)})
		requireFsCode(t, werr, "NATIVE_DESTINATION_CONFLICT")
		if got, _ := os.ReadFile(existing); string(got) != "ORIGINAL" {
			t.Fatalf("conflict must leave the original untouched, got %q", got)
		}
		if countTemps(dir) != 0 {
			t.Fatalf("conflict must clean its own temp")
		}
	})

	t.Run("explicit consented replace swaps atomically after verification", func(t *testing.T) {
		existing := filepath.Join(dir, "replace.zip")
		if err := os.WriteFile(existing, []byte("OLD"), 0o644); err != nil {
			t.Fatal(err)
		}
		reply := chooseDest(t, service, "replace.zip", "save-file", existing)
		done := publish(t, service, reply["handle"].(string), payload, map[string]any{
			"replace":  true,
			"expected": map[string]any{"sha256": digestOf(payload), "byte_size": int64(len(payload))},
		})
		if done["published"] != true || done["durability"] != "confirmed" {
			t.Fatalf("replace publish reply %v", done)
		}
		if got, _ := os.ReadFile(existing); string(got) != string(payload) {
			t.Fatalf("consented replace must publish the new bytes")
		}
		if countTemps(dir) != 0 {
			t.Fatalf("replace must consume its temp")
		}
	})

	t.Run("digest mismatch never publishes", func(t *testing.T) {
		reply := chooseDest(t, service, "broken.zip", "save-file", filepath.Join(dir, "broken.zip"))
		_, werr := fsCall(t, service, "destOpen", map[string]any{"handle": reply["handle"].(string), "intent": map[string]any{
			"expected": map[string]any{"sha256": digestOf([]byte("wrong")), "byte_size": int64(len(payload))},
		}})
		if werr != nil {
			t.Fatalf("destOpen: %v", werr)
		}
		if _, werr := fsCall(t, service, "destWrite", map[string]any{"handle": reply["handle"].(string), "bytes_b64": encodeB64(payload)}); werr != nil {
			t.Fatal(werr)
		}
		_, werr = fsCall(t, service, "destClose", map[string]any{"handle": reply["handle"].(string)})
		requireFsCode(t, werr, "NATIVE_DESTINATION_VERIFICATION_FAILED")
		if _, err := os.Lstat(filepath.Join(dir, "broken.zip")); !os.IsNotExist(err) {
			t.Fatalf("mismatch must never publish a target")
		}
		if countTemps(dir) != 0 {
			t.Fatalf("mismatch must clean its own temp")
		}
	})

	t.Run("crash orphan never blocks a new attempt and is never glob-deleted", func(t *testing.T) {
		orphan := filepath.Join(dir, ".haeryong-deadbeefcafe-0011223344556677.partial")
		if err := os.WriteFile(orphan, []byte("orphan"), 0o600); err != nil {
			t.Fatal(err)
		}
		reply := chooseDest(t, service, "after-crash.zip", "save-file", filepath.Join(dir, "after-crash.zip"))
		publish(t, service, reply["handle"].(string), payload, nil)
		if got, _ := os.ReadFile(orphan); string(got) != "orphan" {
			t.Fatalf("foreign orphan must remain untouched (no prefix/glob cleanup)")
		}
	})

	t.Run("abort removes only its own temp and is idempotent", func(t *testing.T) {
		reply := chooseDest(t, service, "aborted.zip", "save-file", filepath.Join(dir, "aborted.zip"))
		handle := reply["handle"].(string)
		if _, werr := fsCall(t, service, "destOpen", map[string]any{"handle": handle}); werr != nil {
			t.Fatal(werr)
		}
		if _, werr := fsCall(t, service, "destWrite", map[string]any{"handle": handle, "bytes_b64": encodeB64(payload)}); werr != nil {
			t.Fatal(werr)
		}
		for i := 0; i < 2; i++ {
			aborted, werr := fsCall(t, service, "destAbort", map[string]any{"handle": handle})
			if werr != nil || aborted["aborted"] != true {
				t.Fatalf("abort must be idempotent: %v %v", aborted, werr)
			}
		}
		if countTemps(dir) != 1 { // the foreign orphan only
			t.Fatalf("abort must remove its own temp, found %d", countTemps(dir))
		}
		if _, err := os.Lstat(filepath.Join(dir, "aborted.zip")); !os.IsNotExist(err) {
			t.Fatalf("abort must never publish")
		}
		// Writes after abort are stale, never resurrected.
		_, werr := fsCall(t, service, "destWrite", map[string]any{"handle": handle, "bytes_b64": encodeB64(payload)})
		requireFsCode(t, werr, "NATIVE_HANDLE_STALE")
	})
}

func TestDestinationStaysAnchoredToSelectedDirectoryInstance(t *testing.T) {
	service := newFsService(t)
	work := t.TempDir()
	dirA := filepath.Join(work, "a")
	if err := os.MkdirAll(dirA, 0o755); err != nil {
		t.Fatal(err)
	}
	reply := chooseDest(t, service, "anchored.zip", "save-file", filepath.Join(dirA, "anchored.zip"))
	handle := reply["handle"].(string)
	payload := []byte("anchored payload")
	if _, werr := fsCall(t, service, "destOpen", map[string]any{"handle": handle, "intent": map[string]any{
		"expected": map[string]any{"sha256": digestOf(payload), "byte_size": int64(len(payload))},
	}}); werr != nil {
		t.Fatal(werr)
	}
	if _, werr := fsCall(t, service, "destWrite", map[string]any{"handle": handle, "bytes_b64": encodeB64(payload)}); werr != nil {
		t.Fatal(werr)
	}
	// Replace the selected directory PATH with a decoy directory holding a
	// decoy target; the anchored publication must land in the owner-selected
	// directory instance (now moved), never retarget through the old path.
	dirB := filepath.Join(work, "b")
	if err := os.Rename(dirA, dirB); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dirA, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dirA, "anchored.zip"), []byte("DECOY"), 0o644); err != nil {
		t.Fatal(err)
	}
	done, werr := fsCall(t, service, "destClose", map[string]any{"handle": handle})
	if werr != nil {
		t.Fatalf("anchored publication failed: %s %v", werr.Code, werr.Details)
	}
	if done["published"] != true {
		t.Fatalf("publication reply %v", done)
	}
	if got, _ := os.ReadFile(filepath.Join(dirB, "anchored.zip")); string(got) != string(payload) {
		t.Fatalf("publication must stay inside the selected directory instance")
	}
	if got, _ := os.ReadFile(filepath.Join(dirA, "anchored.zip")); string(got) != "DECOY" {
		t.Fatalf("a replaced parent path must never be retargeted")
	}
}

func TestChooseModeAndPickerSemantics(t *testing.T) {
	service := newFsService(t)
	root := t.TempDir()
	filePath := filepath.Join(root, "single.zip")
	if err := os.WriteFile(filePath, []byte("PK\x03\x04zip"), 0o644); err != nil {
		t.Fatal(err)
	}
	// File mode anchors the parent and captures exactly one entry.
	reply := chooseSource(t, service, "archive", filePath)
	handle := reply["handle"].(string)
	entries := readAllSourceEntries(t, service, handle)
	if len(entries) != 1 || entries[0]["path"] != "single.zip" || entries[0]["kind"] != "file" {
		t.Fatalf("archive selection must capture exactly the selected file: %v", entries)
	}
	if got := readSourceBytes(t, service, handle, "single.zip", 0, 7); string(got) != "PK\x03\x04zip" {
		t.Fatalf("archive bytes must be exact")
	}
	// Mode/kind mismatches and unavailable pickers fail typed.
	setPicker(service, fixedPickerPath(root), nil)
	if _, werr := fsCall(t, service, "chooseImportSource", map[string]any{"mode": "file"}); werr == nil {
		t.Fatalf("directory path in file mode must fail")
	}
	service.SetPickerPresenter(nil)
	_, werr := fsCall(t, service, "chooseImportSource", map[string]any{"mode": "directory"})
	requireFsCode(t, werr, "NATIVE_PICKER_UNAVAILABLE")
	_, werr = fsCall(t, service, "chooseDownloadDestination", map[string]any{"suggested_name": "x.zip", "mode": "save-file"})
	requireFsCode(t, werr, "NATIVE_PICKER_UNAVAILABLE")
	// A canceled panel is an honest owner cancel, not an error.
	service.SetPickerPresenter(&PickerPresenter{ChooseSource: func(string) (string, bool, error) { return "", true, nil }})
	reply, werr = fsCall(t, service, "chooseImportSource", map[string]any{"mode": "directory"})
	if werr != nil || reply["canceled"] != true {
		t.Fatalf("canceled panel reply %v %v", reply, werr)
	}
	// The range granularity cap is transport-only.
	reply2 := chooseSource(t, service, "archive", filePath)
	_, werr = fsCall(t, service, "readSourceRange", map[string]any{"handle": reply2["handle"].(string), "path": "single.zip", "offset": 0, "length": 1024*1024 + 1})
	requireFsCode(t, werr, "NATIVE_PAYLOAD_INVALID")
}

// A picker held across the REAL CloseOwned path (which invalidates chooser
// leases) can never register its late result — for sources and destinations.
func TestLatePickerResultCannotRegisterAfterCloseOwned(t *testing.T) {
	service := newFsService(t)
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.bin"), []byte("a"), 0o644); err != nil {
		t.Fatal(err)
	}
	sourceEntered := make(chan struct{})
	sourceBlock := make(chan struct{})
	setPicker(service, func(string) (string, bool, error) { sourceEntered <- struct{}{}; <-sourceBlock; return root, false, nil }, nil)
	sourceDone := invokeAsync(service, "chooseImportSource", map[string]any{"mode": "directory"})
	<-sourceEntered
	service.CloseOwned()
	close(sourceBlock)
	result := <-sourceDone
	requireFsCode(t, result.werr, "NATIVE_SESSION_REPLACED")
	service.mu.Lock()
	registered := len(service.sources)
	service.mu.Unlock()
	if registered != 0 {
		t.Fatal("a picker result held across CloseOwned must not register")
	}

	destEntered := make(chan struct{})
	destBlock := make(chan struct{})
	setPicker(service, nil, func(string, string) (string, bool, error) { destEntered <- struct{}{}; <-destBlock; return filepath.Join(root, "late.zip"), false, nil })
	destDone := invokeAsync(service, "chooseDownloadDestination", map[string]any{"suggested_name": "late.zip", "mode": "save-file"})
	<-destEntered
	service.CloseOwned()
	close(destBlock)
	destResult := <-destDone
	requireFsCode(t, destResult.werr, "NATIVE_SESSION_REPLACED")
	service.mu.Lock()
	registeredDest := len(service.dests)
	service.mu.Unlock()
	if registeredDest != 0 {
		t.Fatal("a destination result held across CloseOwned must not register")
	}
}

// Credential activation is an era transition that releases the previous
// attachment's owner-selected resources: anchored source roots close, an
// unfinished destination temp is cleaned, and later eras can select fresh
// resources without interference.
func TestActivationReleasesOldEraResourcesAndPreservesNewSelections(t *testing.T) {
	service := newFsService(t)
	dir := t.TempDir()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "old.bin"), []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	sourceHandle := chooseSourceRaw(t, service, "directory", root)
	destReply := chooseDest(t, service, "unfinished.zip", "save-file", filepath.Join(dir, "unfinished.zip"))
	destHandle := destReply["handle"].(string)
	if _, werr := fsCall(t, service, "destOpen", map[string]any{"handle": destHandle}); werr != nil {
		t.Fatal(werr)
	}
	if _, werr := fsCall(t, service, "destWrite", map[string]any{"handle": destHandle, "bytes_b64": encodeB64([]byte("partial"))}); werr != nil {
		t.Fatal(werr)
	}
	service.activateAdoptedCredential(client.Credential{Endpoint: "http://127.0.0.1:1", Profile: "default", LibraryID: "lib-1", Token: "new-token"}, 0)
	// The replaced era's unfinished destination temp is cleaned.
	if countTemps(dir) != 0 {
		t.Fatal("activation must clean the replaced era's unfinished destination temp")
	}
	// A fresh era selects and uses new resources normally; the replaced
	// era's handles answer typed stale under it.
	service.mu.Lock()
	service.attachmentID = "post-adoption"
	service.mu.Unlock()
	_, werr := fsCall(t, service, "readSourceRange", map[string]any{"handle": sourceHandle, "path": "old.bin", "offset": 0, "length": 3})
	requireFsCode(t, werr, "NATIVE_HANDLE_STALE")
	_, werr = fsCall(t, service, "destWrite", map[string]any{"handle": destHandle, "bytes_b64": encodeB64([]byte("x"))})
	requireFsCode(t, werr, "NATIVE_HANDLE_STALE")
	if err := os.WriteFile(filepath.Join(root, "new.bin"), []byte("new"), 0o644); err != nil {
		t.Fatal(err)
	}
	fresh := chooseSourceRaw(t, service, "directory", root)
	if got := readSourceBytes(t, service, fresh, "new.bin", 0, 3); string(got) != "new" {
		t.Fatalf("fresh era must read new resources, got %q", got)
	}
}

// A cleanup failure on a failed pre-publication path is PRESERVED: the typed
// NATIVE_CLEANUP_FAILED answer carries the exact surviving temp path and the
// primary failure code, instead of hiding the surviving artifact.
func TestFailedCleanupSurfacesAlongsidePrimaryFailure(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("permission-based cleanup failure is not observable for root")
	}
	service := newFsService(t)
	dir := t.TempDir()
	payload := []byte("payload")
	reply := chooseDest(t, service, "stuck.zip", "save-file", filepath.Join(dir, "stuck.zip"))
	handle := reply["handle"].(string)
	if _, werr := fsCall(t, service, "destOpen", map[string]any{"handle": handle, "intent": map[string]any{
		"expected": map[string]any{"sha256": digestOf([]byte("wrong")), "byte_size": int64(len(payload))},
	}}); werr != nil {
		t.Fatal(werr)
	}
	if _, werr := fsCall(t, service, "destWrite", map[string]any{"handle": handle, "bytes_b64": encodeB64(payload)}); werr != nil {
		t.Fatal(werr)
	}
	// A read-only directory makes the own-temp removal fail while the
	// verification mismatch is the primary failure.
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	_, werr := fsCall(t, service, "destClose", map[string]any{"handle": handle})
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	requireFsCode(t, werr, "NATIVE_CLEANUP_FAILED")
	if werr.Details["primary_code"] != "NATIVE_DESTINATION_VERIFICATION_FAILED" {
		t.Fatalf("primary failure must be preserved in details: %v", werr.Details)
	}
	tempPath, _ := werr.Details["temp"].(string)
	if tempPath == "" || !strings.Contains(tempPath, ".haeryong-") || !strings.HasSuffix(tempPath, ".partial") {
		t.Fatalf("the exact surviving temp path must be disclosed: %v", werr.Details)
	}
	if _, err := os.Lstat(filepath.Join(dir, "stuck.zip")); !os.IsNotExist(err) {
		t.Fatal("the mismatched attempt must never publish")
	}
	if countTemps(dir) != 1 {
		t.Fatalf("the surviving temp is the honest residue, found %d", countTemps(dir))
	}
}

// captureDestRoot captures the handle's ACTUAL *os.Root pointer BEFORE a
// transition; after it, capturedRoot.Open(".") must fail with os.ErrClosed
// to prove the original root FD closed (a separately opened probe FD would
// stay open independently and prove nothing).
func captureDestRoot(handle *destHandle) *os.Root {
	return handle.root
}

func captureSourceRoot(handle *sourceHandle) *os.Root {
	return handle.root
}

// capturedRootClosed proves the anchored root FD actually closed.
func capturedRootClosed(t *testing.T, root *os.Root) bool {
	t.Helper()
	_, err := root.Open(".")
	return err != nil && errors.Is(err, os.ErrClosed)
}

// capturedRootUsable proves the anchored root FD stayed open for a
// still-owned decision or retry path.
func capturedRootUsable(t *testing.T, root *os.Root) bool {
	t.Helper()
	probe, err := root.Open(".")
	if err != nil {
		return false
	}
	probe.Close()
	return true
}

// A successful re-attach through the REAL facade attach (fake HTTP session/
// capabilities fixture) is a filesystem-era boundary: the previous
// attachment's owner-selected handles are detached and drained (anchored
// roots CLOSED — proven directly on the captured FDs, not just via stale
// replies — and unfinished temps cleaned), a picker held across the
// transition never registers, and the fresh era selects new resources.
func TestRealReattachReleasesPreviousEraFilesystemHandles(t *testing.T) {
	service, _, _ := newReaderFixture(t, nil)
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.bin"), []byte("a"), 0o644); err != nil {
		t.Fatal(err)
	}
	setPicker(service, fixedPickerPath(root), func(string, string) (string, bool, error) { return filepath.Join(root, "left.zip"), false, nil })
	sourceReply, werr := fsCall(t, service, "chooseImportSource", map[string]any{"mode": "directory"})
	if werr != nil {
		t.Fatalf("chooseImportSource failed: %s", werr.Code)
	}
	sourceHandle := sourceReply["handle"].(string)
	destReply, werr := fsCall(t, service, "chooseDownloadDestination", map[string]any{"suggested_name": "left.zip", "mode": "save-file"})
	if werr != nil {
		t.Fatalf("chooseDownloadDestination failed: %s", werr.Code)
	}
	destHandleID := destReply["handle"].(string)
	if _, werr := fsCall(t, service, "destOpen", map[string]any{"handle": destHandleID}); werr != nil {
		t.Fatal(werr)
	}
	if _, werr := fsCall(t, service, "destWrite", map[string]any{"handle": destHandleID, "bytes_b64": encodeB64([]byte("partial"))}); werr != nil {
		t.Fatal(werr)
	}
	if countTemps(root) != 1 {
		t.Fatalf("precondition: one live temp expected, found %d", countTemps(root))
	}
	// Capture the ACTUAL root pointers BEFORE the transition: after it, the
	// SAME roots must refuse new opens (os.ErrClosed), proving the FDs
	// actually closed rather than merely going stale in the maps.
	service.mu.Lock()
	capturedSource, sourceKnown := service.sources[sourceHandle]
	capturedDest, destKnown := service.dests[destHandleID]
	service.mu.Unlock()
	if !sourceKnown || !destKnown {
		t.Fatal("precondition: both handles expected registered")
	}
	sourceRoot := captureSourceRoot(capturedSource)
	destRoot := captureDestRoot(capturedDest)

	// A picker held across the REAL re-attach must never register.
	entered := make(chan struct{})
	block := make(chan struct{})
	setPicker(service, func(string) (string, bool, error) { entered <- struct{}{}; <-block; return root, false, nil }, nil)
	heldDone := invokeAsync(service, "chooseImportSource", map[string]any{"mode": "directory"})
	<-entered

	// The real attach transition (era B) over the fake HTTP fixture.
	if _, werr := fsCall(t, service, "attachSession", map[string]any{}); werr != nil {
		t.Fatalf("re-attach failed: %s %v", werr.Code, werr.Details)
	}

	// The previous era's handles are drained: stale answers, no surviving
	// temp, and the anchored root FDs are ACTUALLY closed.
	_, werr = fsCall(t, service, "readSourceRange", map[string]any{"handle": sourceHandle, "path": "a.bin", "offset": 0, "length": 1})
	requireFsCode(t, werr, "NATIVE_HANDLE_STALE")
	_, werr = fsCall(t, service, "destWrite", map[string]any{"handle": destHandleID, "bytes_b64": encodeB64([]byte("x"))})
	requireFsCode(t, werr, "NATIVE_HANDLE_STALE")
	if countTemps(root) != 0 {
		t.Fatal("re-attach must clean the replaced era's unfinished destination temp")
	}
	if !capturedRootClosed(t, sourceRoot) {
		t.Fatal("the replaced era's SOURCE root FD must actually close")
	}
	if !capturedRootClosed(t, destRoot) {
		t.Fatal("the replaced era's DESTINATION root FD must actually close")
	}

	// The held picker result arrives after its lease died: typed rejection.
	close(block)
	heldResult := <-heldDone
	requireFsCode(t, heldResult.werr, "NATIVE_SESSION_REPLACED")

	// The fresh era selects and reads new resources normally.
	if err := os.WriteFile(filepath.Join(root, "b.bin"), []byte("b"), 0o644); err != nil {
		t.Fatal(err)
	}
	setPicker(service, fixedPickerPath(root), nil)
	freshReply, werr := fsCall(t, service, "chooseImportSource", map[string]any{"mode": "directory"})
	if werr != nil {
		t.Fatalf("fresh chooseImportSource failed: %s", werr.Code)
	}
	if got := readSourceBytes(t, service, freshReply["handle"].(string), "b.bin", 0, 1); string(got) != "b" {
		t.Fatalf("fresh era must read new resources, got %q", got)
	}
}

// An unresolved abort cleanup stays retryable with the typed exact path until
// the removal actually succeeds; only then is the attempt done (terminal
// tombstone, root closed) — never on the first failed try.
func TestAbortCleanupRetriesUntilActualSuccess(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("permission-based cleanup failure is not observable for root")
	}
	service := newFsService(t)
	dir := t.TempDir()
	reply := chooseDest(t, service, "retry.zip", "save-file", filepath.Join(dir, "retry.zip"))
	handle := reply["handle"].(string)
	if _, werr := fsCall(t, service, "destOpen", map[string]any{"handle": handle}); werr != nil {
		t.Fatal(werr)
	}
	if _, werr := fsCall(t, service, "destWrite", map[string]any{"handle": handle, "bytes_b64": encodeB64([]byte("partial"))}); werr != nil {
		t.Fatal(werr)
	}
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	_, werr := fsCall(t, service, "destAbort", map[string]any{"handle": handle})
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	requireFsCode(t, werr, "NATIVE_CLEANUP_FAILED")
	if tempPath, _ := werr.Details["temp"].(string); tempPath == "" || !strings.Contains(tempPath, ".haeryong-") {
		t.Fatalf("the typed failure must carry the exact surviving path: %v", werr.Details)
	}
	if countTemps(dir) != 1 {
		t.Fatalf("the unresolved temp must still exist for retry, found %d", countTemps(dir))
	}
	// The unresolved abort keeps its anchored root FD OPEN for the retry
	// (direct root probe): the removal must be re-attemptable.
	service.mu.Lock()
	abortingHandle, abortingKnown := service.dests[handle]
	service.mu.Unlock()
	if !abortingKnown {
		t.Fatal("aborting handle expected in the map")
	}
	if !capturedRootUsable(t, captureDestRoot(abortingHandle)) {
		t.Fatal("an unresolved abort must keep its anchored root FD open for the retry")
	}
	// A write cannot resurrect the aborting attempt.
	_, werr = fsCall(t, service, "destWrite", map[string]any{"handle": handle, "bytes_b64": encodeB64([]byte("x"))})
	requireFsCode(t, werr, "NATIVE_HANDLE_STALE")
	// The retry re-attempts the SAME removal; only success claims done.
	aborted, werr := fsCall(t, service, "destAbort", map[string]any{"handle": handle})
	if werr != nil {
		t.Fatalf("retry abort failed: %s %v", werr.Code, werr.Details)
	}
	if aborted["aborted"] != true {
		t.Fatalf("successful retry reply %v", aborted)
	}
	if countTemps(dir) != 0 {
		t.Fatal("the retried abort must actually remove the temp")
	}
	// Terminal tombstone: fully cleaned abort keeps no live resources and
	// stays idempotent; the attempt cannot be re-opened. The anchored root
	// FD actually closed (direct os.Root probe, not just a stale reply).
	again, werr := fsCall(t, service, "destAbort", map[string]any{"handle": handle})
	if werr != nil || again["aborted"] != true {
		t.Fatalf("idempotent tombstone reply %v %v", again, werr)
	}
	_, werr = fsCall(t, service, "destOpen", map[string]any{"handle": handle})
	requireFsCode(t, werr, "NATIVE_HANDLE_STALE")
	service.mu.Lock()
	captured, known := service.dests[handle]
	service.mu.Unlock()
	if !known {
		t.Fatal("terminal tombstone expected in the handle map")
	}
	if !capturedRootClosed(t, captureDestRoot(captured)) {
		t.Fatal("a fully cleaned abort must actually close its anchored root FD")
	}
}

// A published attempt is terminal with no live anchor: it cannot be
// re-opened, and a later abort reports the truthful published outcome.
func TestPublishedAttemptIsTerminal(t *testing.T) {
	service := newFsService(t)
	dir := t.TempDir()
	payload := []byte("terminal payload")
	reply := chooseDest(t, service, "done.zip", "save-file", filepath.Join(dir, "done.zip"))
	handle := reply["handle"].(string)
	done := publish(t, service, handle, payload, nil)
	if done["published"] != true || done["durability"] != "confirmed" {
		t.Fatalf("publication reply %v", done)
	}
	_, werr := fsCall(t, service, "destOpen", map[string]any{"handle": handle})
	requireFsCode(t, werr, "NATIVE_HANDLE_STALE")
	after, werr := fsCall(t, service, "destAbort", map[string]any{"handle": handle})
	if werr != nil || after["aborted"] != false || after["published"] != true {
		t.Fatalf("post-publish abort reply %v %v", after, werr)
	}
	// A published attempt actually closed its anchored root FD.
	service.mu.Lock()
	publishedHandle, publishedKnown := service.dests[handle]
	service.mu.Unlock()
	if !publishedKnown {
		t.Fatal("published tombstone expected in the handle map")
	}
	if !capturedRootClosed(t, captureDestRoot(publishedHandle)) {
		t.Fatal("a published attempt must actually close its anchored root FD")
	}
	// A conflict decision left pending keeps the anchor open for the owner's
	// explicit replace: the same handle opens a fresh attempt.
	existing := filepath.Join(dir, "conflict.zip")
	if err := os.WriteFile(existing, []byte("ORIGINAL"), 0o644); err != nil {
		t.Fatal(err)
	}
	conflictReply := chooseDest(t, service, "conflict.zip", "save-file", existing)
	conflictHandle := conflictReply["handle"].(string)
	if _, werr := fsCall(t, service, "destOpen", map[string]any{"handle": conflictHandle}); werr != nil {
		t.Fatal(werr)
	}
	if _, werr := fsCall(t, service, "destWrite", map[string]any{"handle": conflictHandle, "bytes_b64": encodeB64(payload)}); werr != nil {
		t.Fatal(werr)
	}
	_, werr = fsCall(t, service, "destClose", map[string]any{"handle": conflictHandle})
	requireFsCode(t, werr, "NATIVE_DESTINATION_CONFLICT")
	// The PENDING decision keeps the exact selected anchor OPEN (direct
	// root probe): the owner may still choose an explicit replace.
	service.mu.Lock()
	conflictPending, conflictKnown := service.dests[conflictHandle]
	service.mu.Unlock()
	if !conflictKnown {
		t.Fatal("pending-conflict handle expected in the map")
	}
	if !capturedRootUsable(t, captureDestRoot(conflictPending)) {
		t.Fatal("a pending conflict decision must keep the anchored root FD open")
	}
	// The SAME handle opens a fresh attempt under the owner's explicit
	// replace consent.
	if _, werr := fsCall(t, service, "destOpen", map[string]any{"handle": conflictHandle, "intent": map[string]any{"replace": true}}); werr != nil {
		t.Fatalf("re-open after conflict failed: %s %v", werr.Code, werr.Details)
	}
	if _, werr := fsCall(t, service, "destWrite", map[string]any{"handle": conflictHandle, "bytes_b64": encodeB64(payload)}); werr != nil {
		t.Fatal(werr)
	}
	replaced, werr := fsCall(t, service, "destClose", map[string]any{"handle": conflictHandle})
	if werr != nil || replaced["published"] != true {
		t.Fatalf("consented replace after pending conflict must publish: %v %v", replaced, werr)
	}
	if got, _ := os.ReadFile(existing); string(got) != string(payload) {
		t.Fatal("consented replace must publish the new bytes")
	}
}

func TestParseFileValidator(t *testing.T) {
	sum := sha256.Sum256([]byte("body"))
	good := "sha-256=:" + b64Std(sum[:]) + ":"
	header := newHeader(map[string]string{"Repr-Digest": good, "Content-Length": "4"})
	sha, size, ok := parseFileValidator(header)
	if !ok || sha != hex.EncodeToString(sum[:]) || size != 4 {
		t.Fatalf("validator parse failed: %v %v %v", sha, size, ok)
	}
	for _, bad := range []map[string]string{
		{"Repr-Digest": "sha-256=:notbase64:", "Content-Length": "4"},
		{"Content-Length": "4"},
		{"Repr-Digest": good},
		{"Repr-Digest": good, "Content-Length": "-1"},
	} {
		if _, _, ok := parseFileValidator(newHeader(bad)); ok {
			t.Fatalf("malformed validator must not parse: %v", bad)
		}
	}
}

// invokeAsync runs one real invoke dispatch in a goroutine; goroutine-safe
// (no t.Fatalf off the test goroutine).
func invokeAsync(service *ReaderService, operation string, payload any) <-chan struct {
	value map[string]any
	werr  *wireError
} {
	encoded, err := json.Marshal(payload)
	out := make(chan struct {
		value map[string]any
		werr  *wireError
	}, 1)
	if err != nil {
		out <- struct {
			value map[string]any
			werr  *wireError
		}{nil, payloadInvalid(err.Error())}
		return out
	}
	go func() {
		value, werr := service.invoke(operation, encoded)
		reply := map[string]any{}
		if value != nil {
			if typed, ok := value.(map[string]any); ok {
				reply = typed
			}
		}
		out <- struct {
			value map[string]any
			werr  *wireError
		}{reply, werr}
	}()
	return out
}

func waitForDetached(service *ReaderService, id string) bool {
	deadline := time.Now().Add(2 * time.Second)
	for {
		service.mu.Lock()
		_, present := service.dests[id]
		service.mu.Unlock()
		if !present {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(2 * time.Millisecond)
	}
}

// A picker result arriving after its authority era was replaced (reattach,
// logout, disposal) must never register a usable handle — for sources and
// destinations alike.
func TestLatePickerResultCannotRegisterUnderReplacedEra(t *testing.T) {
	service := newFsService(t)
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.bin"), []byte("a"), 0o644); err != nil {
		t.Fatal(err)
	}
	entered := make(chan struct{})
	block := make(chan struct{})
	setPicker(service, func(string) (string, bool, error) { entered <- struct{}{}; <-block; return root, false, nil }, nil)
	sourceDone := invokeAsync(service, "chooseImportSource", map[string]any{"mode": "directory"})
	<-entered
	service.mu.Lock()
	service.generation++
	service.mu.Unlock()
	close(block)
	result := <-sourceDone
	requireFsCode(t, result.werr, "NATIVE_SESSION_REPLACED")
	service.mu.Lock()
	registered := len(service.sources)
	service.mu.Unlock()
	if registered != 0 {
		t.Fatalf("a replaced-era picker result must not register a handle")
	}

	destEntered := make(chan struct{})
	destBlock := make(chan struct{})
	setPicker(service, nil, func(string, string) (string, bool, error) { destEntered <- struct{}{}; <-destBlock; return filepath.Join(root, "out.zip"), false, nil })
	destDone := invokeAsync(service, "chooseDownloadDestination", map[string]any{"suggested_name": "out.zip", "mode": "save-file"})
	<-destEntered
	service.mu.Lock()
	service.generation++
	service.mu.Unlock()
	close(destBlock)
	destResult := <-destDone
	requireFsCode(t, destResult.werr, "NATIVE_SESSION_REPLACED")
	service.mu.Lock()
	registeredDest := len(service.dests)
	service.mu.Unlock()
	if registeredDest != 0 {
		t.Fatalf("a replaced-era destination result must not register a handle")
	}
}

// Disposal of the owning era linearizes against an in-flight publication:
// the detach happens before the operation's filesystem effect, so the racing
// destClose answers typed stale and never publishes, while the release path
// cleans the unfinished attempt's own temp. A close that linearized first
// (sequential order) completes before the release and stays published.
func TestDisposalLinearizesAgainstInFlightPublication(t *testing.T) {
	service := newFsService(t)
	dir := t.TempDir()
	payload := []byte("race payload")

	// Order 1: disposal detaches while the close is queued on the handle's
	// operation lock — the close must end stale with zero effect.
	reply := chooseDest(t, service, "race.zip", "save-file", filepath.Join(dir, "race.zip"))
	handle := reply["handle"].(string)
	if _, werr := fsCall(t, service, "destOpen", map[string]any{"handle": handle, "intent": map[string]any{
		"expected": map[string]any{"sha256": digestOf(payload), "byte_size": int64(len(payload))},
	}}); werr != nil {
		t.Fatal(werr)
	}
	if _, werr := fsCall(t, service, "destWrite", map[string]any{"handle": handle, "bytes_b64": encodeB64(payload)}); werr != nil {
		t.Fatal(werr)
	}
	service.mu.Lock()
	locked := service.dests[handle]
	service.mu.Unlock()
	locked.opMu.Lock()
	closeDone := invokeAsync(service, "destClose", map[string]any{"handle": handle})
	releaseDone := make(chan struct{})
	go func() { service.CloseOwned(); close(releaseDone) }()
	if !waitForDetached(service, handle) {
		t.Fatal("CloseOwned never detached the racing handle")
	}
	locked.opMu.Unlock()
	closeResult := <-closeDone
	requireFsCode(t, closeResult.werr, "NATIVE_HANDLE_STALE")
	<-releaseDone
	if _, err := os.Lstat(filepath.Join(dir, "race.zip")); !os.IsNotExist(err) {
		t.Fatal("a close that lost the disposal race must never publish")
	}
	if countTemps(dir) != 0 {
		t.Fatal("the release path must clean the unfinished attempt's own temp")
	}

	// Order 2: the close linearized first and completed; a later release
	// leaves the publication intact.
	reply2 := chooseDest(t, service, "race2.zip", "save-file", filepath.Join(dir, "race2.zip"))
	publish(t, service, reply2["handle"].(string), payload, nil)
	service.CloseOwned()
	if got, err := os.ReadFile(filepath.Join(dir, "race2.zip")); err != nil || string(got) != string(payload) {
		t.Fatal("a completed publication must survive the later release")
	}
}

// An explicit abort ends the attempt deterministically (a later close is
// stale with no effect and no temp leak), and aborting an already-published
// attempt never claims it stopped an incomplete transfer.
func TestAbortFinalityAndPostPublishTruthfulness(t *testing.T) {
	service := newFsService(t)
	dir := t.TempDir()
	payload := []byte("abort payload")
	reply := chooseDest(t, service, "abort.zip", "save-file", filepath.Join(dir, "abort.zip"))
	handle := reply["handle"].(string)
	if _, werr := fsCall(t, service, "destOpen", map[string]any{"handle": handle}); werr != nil {
		t.Fatal(werr)
	}
	if _, werr := fsCall(t, service, "destWrite", map[string]any{"handle": handle, "bytes_b64": encodeB64(payload)}); werr != nil {
		t.Fatal(werr)
	}
	aborted, werr := fsCall(t, service, "destAbort", map[string]any{"handle": handle})
	if werr != nil || aborted["aborted"] != true {
		t.Fatalf("abort reply %v %v", aborted, werr)
	}
	if _, werr := fsCall(t, service, "destClose", map[string]any{"handle": handle}); werr == nil || werr.Code != "NATIVE_HANDLE_STALE" {
		t.Fatalf("close after abort must be stale, got %v", werr)
	}
	if _, err := os.Lstat(filepath.Join(dir, "abort.zip")); !os.IsNotExist(err) {
		t.Fatal("abort must never publish")
	}
	if countTemps(dir) != 0 {
		t.Fatal("no temp may leak from the aborted attempt")
	}
	// Post-publish abort truthfulness.
	reply2 := chooseDest(t, service, "published.zip", "save-file", filepath.Join(dir, "published.zip"))
	handle2 := reply2["handle"].(string)
	publish(t, service, handle2, payload, nil)
	after, werr := fsCall(t, service, "destAbort", map[string]any{"handle": handle2})
	if werr != nil {
		t.Fatalf("abort after publish failed: %v", werr)
	}
	if after["aborted"] != false || after["published"] != true {
		t.Fatalf("abort must never claim stopping a published transfer: %v", after)
	}
}
