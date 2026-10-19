//go:build haeryong_test_state

package main

import (
	"os"
	"path/filepath"
	"testing"

	facade "haeryong.local/haeryong/desktop/readerfacade"
)

// The fixture picker rejects invalid configuration before any app serving:
// unknown fields, unsupported kind/mode, unowned/escaping paths (including
// existing-parent symlink resolution), and malformed entries.
func TestFixturePickerRejectsInvalidConfiguration(t *testing.T) {
	dir := t.TempDir()
	inner := filepath.Join(dir, "inner")
	if err := os.MkdirAll(inner, 0700); err != nil {
		t.Fatal(err)
	}
	valid := filepath.Join(inner, "corpus.zip")
	if err := os.WriteFile(valid, []byte("zip"), 0600); err != nil {
		t.Fatal(err)
	}

	write := func(content string) string {
		path := filepath.Join(dir, "cfg.json")
		if err := os.WriteFile(path, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
		return path
	}

	for _, tc := range []struct {
		name    string
		content string
	}{
		{"unknown-field", `{"root":"` + dir + `","steps":[],"extra":1}`},
		{"unsupported-kind", `{"root":"` + dir + `","steps":[{"kind":"other","mode":"directory","path":"` + dir + `"}]}`},
		{"unsupported-source-mode", `{"root":"` + dir + `","steps":[{"kind":"source","mode":"save-file","path":"` + valid + `"}]}`},
		{"unsupported-destination-mode", `{"root":"` + dir + `","steps":[{"kind":"destination","mode":"archive","path":"` + valid + `"}]}`},
		{"relative-path", `{"root":"` + dir + `","steps":[{"kind":"source","mode":"archive","path":"corpus.zip"}]}`},
		{"escaping-path", `{"root":"` + inner + `","steps":[{"kind":"source","mode":"archive","path":"` + dir + `/outside.zip"}]}`},
		{"malformed-json", `{"root":`},
		{"nonexistent-root", `{"root":"` + filepath.Join(dir, "missing") + `","steps":[]}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := loadFixturePicker(write(tc.content)); err == nil {
				t.Fatalf("expected rejection for %s", tc.name)
			}
		})
	}
}

// The closed queue consumes steps in order with exact kind/mode matching;
// mismatch and exhaustion fail explicitly and never return a fake cancel.
func TestFixturePickerQueueOrderAndExhaustion(t *testing.T) {
	dir := t.TempDir()
	a := filepath.Join(dir, "a")
	b := filepath.Join(dir, "b")
	for _, path := range []string{a, b} {
		if err := os.WriteFile(path, []byte("x"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	picker := &fixturePicker{root: dir, steps: []fixtureStep{
		{Kind: "source", Mode: "archive", Path: a},
		{Kind: "destination", Mode: "save-file", Path: b},
	}}
	var err error
	picker.root, err = filepath.EvalSymlinks(picker.root)
	if err != nil {
		t.Fatal(err)
	}

	path, canceled, err := picker.ChooseSource("archive")
	if err != nil || canceled || path != a {
		t.Fatalf("first source expected %q got %q canceled=%v err=%v", a, path, canceled, err)
	}
	if _, _, err := picker.ChooseSource("file"); err == nil {
		t.Fatal("kind/mode mismatch must fail explicitly")
	}
	if _, _, err := picker.ChooseSource("archive"); err == nil {
		t.Fatal("duplicate mode after consumption must fail (queue advanced)")
	}
	path, canceled, err = picker.ChooseDestination("name.zip", "save-file")
	if err != nil || canceled || path != b {
		t.Fatalf("destination expected %q got %q canceled=%v err=%v", b, path, canceled, err)
	}
	if _, _, err := picker.ChooseDestination("name.zip", "save-file"); err == nil {
		t.Fatal("exhausted queue must fail explicitly")
	}
}

// Symlink escape through an existing parent is rejected.
func TestFixturePickerSymlinkParentEscape(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "root")
	outside := filepath.Join(dir, "outside")
	for _, path := range []string{root, outside} {
		if err := os.MkdirAll(path, 0700); err != nil {
			t.Fatal(err)
		}
	}
	link := filepath.Join(root, "link")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	escaping := filepath.Join(link, "file.zip")
	if err := os.WriteFile(escaping, []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	step := fixtureStep{Kind: "source", Mode: "archive", Path: escaping}
	if err := validateStep(step, root); err == nil {
		t.Fatal("symlink-resolved parent escape must be rejected")
	}
}

// The empty --picker-fixture default preserves the real presenter: a
// sentinel presenter installed before the hook remains unchanged.
func TestFixturePickerEmptyFlagDefault(t *testing.T) {
	original := *pickerFixturePath
	cached := preflightLoadedPicker
	defer func() { *pickerFixturePath, preflightLoadedPicker = original, cached }()
	*pickerFixturePath = ""
	preflightLoadedPicker = nil

	calls := 0
	sentinel := &facade.PickerPresenter{
		ChooseSource:      func(string) (string, bool, error) { calls++; return "", true, nil },
		ChooseDestination: func(string, string) (string, bool, error) { return "", false, nil },
	}
	service := newTestOnlyService(t)
	service.SetPickerPresenter(sentinel)
	preflightFixturePicker()
	installFixturePicker(service)
	result := pickerTestCall(t, service, "chooseImportSource", `{"mode":"directory"}`)
	if result["ok"] != true || calls != 1 {
		t.Fatalf("existing presenter did not receive the real Invoke: %v calls=%d", result, calls)
	}
	if value, ok := result["value"].(map[string]any); !ok || value["canceled"] != true {
		t.Fatalf("existing presenter's result was not preserved: %v", result)
	}
}

// Trailing garbage after a valid JSON configuration is rejected as malformed.
func TestFixturePickerRejectsTrailingData(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cfg.json")
	valid := `{"root":"` + dir + `","steps":[]} garbage`
	if err := os.WriteFile(path, []byte(valid), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadFixturePicker(path); err == nil {
		t.Fatal("trailing garbage after valid JSON must be rejected")
	}
	second := `{"root":"` + dir + `","steps":[]}{"root":"other"}`
	if err := os.WriteFile(path, []byte(second), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadFixturePicker(path); err == nil {
		t.Fatal("second JSON value after valid first must be rejected")
	}
	// Malformed trailing garbage (not valid JSON whitespace) also rejects.
	garbage := `{"root":"` + dir + `","steps":[]} ] extra`
	if err := os.WriteFile(path, []byte(garbage), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadFixturePicker(path); err == nil {
		t.Fatal("malformed trailing garbage must be rejected")
	}
}

// Preflight caches the parsed instance; install reuses that exact instance
// (the configuration is loaded exactly once).
func TestFixturePickerPreflightCacheInstallOnce(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "cfg.json")
	cfg := `{"root":"` + dir + `","steps":[{"kind":"source","mode":"archive","path":"` + filepath.Join(dir, "a") + `"}]}`
	if err := os.WriteFile(cfgPath, []byte(cfg), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "a"), []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}

	original := *pickerFixturePath
	cached := preflightLoadedPicker
	defer func() { *pickerFixturePath, preflightLoadedPicker = original, cached }()
	*pickerFixturePath = cfgPath

	preflightLoadedPicker = nil
	preflightFixturePicker()
	if preflightLoadedPicker == nil {
		t.Fatal("preflight must cache the parsed picker instance")
	}
	// Reloading the configuration during installation would now fail.
	if err := os.Remove(cfgPath); err != nil {
		t.Fatal(err)
	}
	service := newTestOnlyService(t)
	installFixturePicker(service)
	result := pickerTestCall(t, service, "chooseImportSource", `{"mode":"archive"}`)
	if result["ok"] != true || preflightLoadedPicker.consumed != 1 {
		t.Fatalf("cached presenter did not serve the actual selection: %v", result)
	}
}

func TestFixturePickerRejectsUnavailableOrUnownedRoot(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "input")
	if err := os.WriteFile(file, []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := validateStep(fixtureStep{Kind: "source", Mode: "file", Path: file}, filepath.Join(dir, "missing")); err == nil {
		t.Fatal("unresolvable root must fail even when the selected path exists")
	}
	if os.Geteuid() != 0 {
		if err := validateStep(fixtureStep{Kind: "source", Mode: "file", Path: file}, "/"); err == nil {
			t.Fatal("a different user's root must not grant fixture selections")
		}
	}
}

// Callback-time containment revalidation: if the filesystem changes after
// configuration load (e.g. a symlink is introduced), the callback rejects
// the now-escaping step without consuming it.
func TestFixturePickerCallbackEscapeAfterLoad(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "root")
	outside := filepath.Join(dir, "outside")
	for _, path := range []string{root, outside} {
		if err := os.MkdirAll(path, 0700); err != nil {
			t.Fatal(err)
		}
	}
	target := filepath.Join(root, "file.bin")
	if err := os.WriteFile(target, []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	picker := &fixturePicker{root: root, steps: []fixtureStep{
		{Kind: "source", Mode: "archive", Path: target},
	}}
	var err error
	picker.root, err = filepath.EvalSymlinks(picker.root)
	if err != nil {
		t.Fatal(err)
	}
	// Replace the file with a symlink pointing outside root.
	if err := os.Remove(target); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, target); err != nil {
		t.Fatal(err)
	}
	if _, _, err := picker.ChooseSource("archive"); err == nil {
		t.Fatal("callback must revalidate containment and reject the symlinked escape")
	}
}
