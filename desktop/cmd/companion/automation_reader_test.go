//go:build darwin && haeryong_test_state && haeryong_native_automation

package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestChildProbeRequiresOwnedPrivateRegularText(t *testing.T) {
	if value, err := readOwnedChildProbe(""); err != nil || value != "" {
		t.Fatalf("absent flag changed behavior: %q %v", value, err)
	}
	root := t.TempDir()
	path := filepath.Join(root, "probe")
	if err := os.WriteFile(path, []byte("document.documentElement.dataset.test = '한글';"), 0600); err != nil {
		t.Fatal(err)
	}
	if value, err := readOwnedChildProbe(path); err != nil || value == "" {
		t.Fatalf("owned private UTF-8 probe refused: %v", err)
	}
	if err := os.Chmod(path, 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := readOwnedChildProbe(path); err == nil {
		t.Fatal("non-private probe accepted")
	}
	if err := os.Chmod(path, 0600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "link")
	if err := os.Symlink(path, link); err != nil {
		t.Fatal(err)
	}
	if _, err := readOwnedChildProbe(link); err == nil {
		t.Fatal("symlink probe accepted")
	}
	if _, err := readOwnedChildProbe(root); err == nil {
		t.Fatal("directory probe accepted")
	}
	if _, err := readOwnedChildProbe("relative.js"); err == nil {
		t.Fatal("relative probe accepted")
	}
	for _, raw := range [][]byte{nil, {0xff}, {'a', 0, 'b'}} {
		if err := os.WriteFile(path, raw, 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := readOwnedChildProbe(path); err == nil {
			t.Fatal("empty/non-text probe silently accepted")
		}
	}
}

func TestAutomationSnapshotAndKeyControlsStayBounded(t *testing.T) {
	for _, name := range []string{"", "..", ".", "../outside", "nested/file", "x\x00y"} {
		if validSnapshotName(name) {
			t.Fatalf("snapshot path escapes its owned directory: %q", name)
		}
	}
	for _, name := range []string{"reader-wide", "한글 캡처"} {
		if !validSnapshotName(name) {
			t.Fatalf("ordinary snapshot name refused: %q", name)
		}
	}
	for _, key := range []string{"tab", "return", "space", "escape", "left", "right", "up", "down"} {
		if !validAutomationKey(key, "") {
			t.Fatalf("declared native key refused: %q", key)
		}
	}
	if !validAutomationKey("text", "한국어") {
		t.Fatal("nonempty Unicode text refused")
	}
	if validAutomationKey("text", "") || validAutomationKey("text", "x\x00y") || validAutomationKey("global", "x") {
		t.Fatal("unsupported/empty key control accepted")
	}
}
