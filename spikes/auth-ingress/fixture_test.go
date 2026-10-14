package authingress

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLocalControlPersistsAndRejectsInsecureFiles(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "control")
	first, err := LocalControl(path)
	if err != nil {
		t.Fatal(err)
	}
	next, err := LocalControl(path)
	if err != nil || first != next || len(first) != 64 {
		t.Fatal("control changed during ordinary restart", err)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("control file permissions", err)
	}
	if err := os.Chmod(path, 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := LocalControl(path); err == nil {
		t.Fatal("insecure file accepted")
	}
	if err := os.Chmod(path, 0600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "link")
	if err := os.Symlink(path, link); err != nil {
		t.Fatal(err)
	}
	if _, err := LocalControl(link); err == nil {
		t.Fatal("symlink accepted")
	}
	if err := os.WriteFile(filepath.Join(root, "empty"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := LocalControl(filepath.Join(root, "empty")); err == nil {
		t.Fatal("empty credential accepted")
	}
}
