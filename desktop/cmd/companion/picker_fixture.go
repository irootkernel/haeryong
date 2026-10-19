//go:build haeryong_test_state

package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"syscall"

	facade "haeryong.local/haeryong/desktop/readerfacade"
)

// --picker-fixture supplies a closed ordered selection queue from an owned
// CLI JSON configuration. The empty default keeps the real production Wails
// dialog presenter; this tagged file alone declares the flag.
var pickerFixturePath = flag.String("picker-fixture", "", "Owned CLI JSON fixture picker configuration (tagged test builds only)")

// fixtureStep is one ordered selection the queue may supply exactly once.
type fixtureStep struct {
	Kind string `json:"kind"` // source | destination
	Mode string `json:"mode"` // directory|archive|file | directory|save-file
	Path string `json:"path"`
}

type fixtureConfig struct {
	Root  string        `json:"root"`
	Steps []fixtureStep `json:"steps"`
}

// fixturePicker is the closed queue presenter. Every callback validates the
// exact next queued kind AND mode, and consumes exactly one step. Any
// mismatch, exhaustion, escaping path, or configuration error fails
// explicitly — never a fake cancellation. All purpose/attachment/era/
// handle/FD/publication checks remain real facade code; this presenter only
// supplies selection results.
type fixturePicker struct {
	mu       sync.Mutex
	root     string
	steps    []fixtureStep
	consumed int
}

func sourceModeValid(mode string) bool {
	return mode == "directory" || mode == "archive" || mode == "file"
}

func destinationModeValid(mode string) bool {
	return mode == "directory" || mode == "save-file"
}

// validateStep checks one configuration entry before any app serving.
func validateStep(step fixtureStep, root string) error {
	rootResolved, err := filepath.EvalSymlinks(root)
	if err != nil {
		return fmt.Errorf("fixture picker: registered root unavailable: %w", err)
	}
	info, err := os.Stat(rootResolved)
	if err != nil || !info.IsDir() {
		return errors.New("fixture picker: root must be an existing directory")
	}
	owner, ok := info.Sys().(*syscall.Stat_t)
	if !ok || int(owner.Uid) != os.Geteuid() {
		return errors.New("fixture picker: root must belong to the current user")
	}
	switch step.Kind {
	case "source":
		if !sourceModeValid(step.Mode) {
			return fmt.Errorf("fixture picker: unsupported source mode %q", step.Mode)
		}
	case "destination":
		if !destinationModeValid(step.Mode) {
			return fmt.Errorf("fixture picker: unsupported destination mode %q", step.Mode)
		}
	default:
		return fmt.Errorf("fixture picker: unsupported kind %q", step.Kind)
	}
	if !filepath.IsAbs(step.Path) {
		return fmt.Errorf("fixture picker: path must be absolute: %q", step.Path)
	}
	// Containment under the registered root, including existing-parent
	// symlink resolution. The leaf itself need not exist (save-file).
	if resolved, err := filepath.EvalSymlinks(step.Path); err == nil {
		if !withinRoot(rootResolved, resolved) {
			return fmt.Errorf("fixture picker: path escapes the registered root: %q", step.Path)
		}
	} else if step.Kind == "destination" && step.Mode == "save-file" && os.IsNotExist(err) {
		if _, leafErr := os.Lstat(step.Path); !os.IsNotExist(leafErr) {
			return fmt.Errorf("fixture picker: existing selection is unresolvable: %q", step.Path)
		}
		resolvedParent, parentErr := filepath.EvalSymlinks(filepath.Dir(step.Path))
		if parentErr != nil {
			return fmt.Errorf("fixture picker: proposed parent unavailable: %w", parentErr)
		}
		if !withinRoot(rootResolved, resolvedParent) {
			return fmt.Errorf("fixture picker: path parent escapes the registered root: %q", step.Path)
		}
	} else {
		return fmt.Errorf("fixture picker: selection unavailable: %q: %w", step.Path, err)
	}
	return nil
}

func withinRoot(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return false
	}
	return rel != ".." && !startsWithDotDot(rel)
}

func startsWithDotDot(rel string) bool {
	return len(rel) >= 2 && rel[0] == '.' && rel[1] == '.' && (len(rel) == 2 || rel[2] == filepath.Separator)
}

// resolveRoot resolves the registered root explicitly; a root that cannot
// be resolved at callback time is a real error, never silently skipped.
func (p *fixturePicker) resolveRoot() (string, error) {
	resolved, err := filepath.EvalSymlinks(p.root)
	if err != nil {
		return "", fmt.Errorf("fixture picker: registered root unresolvable at callback: %w", err)
	}
	return resolved, nil
}

// next consumes the exact next step matching kind and mode, or fails.
// Containment under the registered root is revalidated at callback time
// (the filesystem may have changed since configuration load); a failure
// does not consume the step.
func (p *fixturePicker) next(kind, mode string) (string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.consumed >= len(p.steps) {
		return "", fmt.Errorf("fixture picker: queue exhausted; no step for %s/%s", kind, mode)
	}
	step := p.steps[p.consumed]
	if step.Kind != kind || step.Mode != mode {
		return "", fmt.Errorf("fixture picker: next queued step is %s/%s but %s/%s was requested", step.Kind, step.Mode, kind, mode)
	}
	resolved, err := p.resolveRoot()
	if err != nil {
		return "", err
	}
	if resolved != p.root {
		return "", errors.New("fixture picker: registered root resolution changed")
	}
	if err := validateStep(step, resolved); err != nil {
		return "", err
	}
	p.consumed++
	return step.Path, nil
}

func (p *fixturePicker) ChooseSource(mode string) (string, bool, error) {
	path, err := p.next("source", mode)
	if err != nil {
		return "", false, err
	}
	return path, false, nil
}

func (p *fixturePicker) ChooseDestination(suggestedName, mode string) (string, bool, error) {
	path, err := p.next("destination", mode)
	if err != nil {
		return "", false, err
	}
	return path, false, nil
}

// loadFixturePicker reads and validates the owned CLI JSON configuration.
// It rejects unknown fields, unsupported kind/mode, unowned or escaping
// paths (including existing-parent symlink resolution), and malformed
// entries — all before any app serving.
func loadFixturePicker(path string) (*fixturePicker, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("fixture picker configuration unreadable: %w", err)
	}
	var config fixtureConfig
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&config); err != nil {
		return nil, fmt.Errorf("fixture picker configuration malformed: %w", err)
	}
	// The second Decode must hit exactly io.EOF: any other result means
	// trailing garbage or an extra JSON value, both malformed.
	if err := decoder.Decode(new(json.RawMessage)); err != io.EOF {
		return nil, errors.New("fixture picker configuration has trailing data after the first JSON value")
	}
	if !filepath.IsAbs(config.Root) {
		return nil, errors.New("fixture picker: root must be absolute")
	}
	info, err := os.Stat(config.Root)
	if err != nil || !info.IsDir() {
		return nil, errors.New("fixture picker: root must be an existing directory")
	}
	owner, ok := info.Sys().(*syscall.Stat_t)
	if !ok || int(owner.Uid) != os.Geteuid() {
		return nil, errors.New("fixture picker: root must belong to the current user")
	}
	resolvedRoot, err := filepath.EvalSymlinks(config.Root)
	if err != nil {
		return nil, fmt.Errorf("fixture picker: registered root unavailable: %w", err)
	}
	for _, step := range config.Steps {
		if err := validateStep(step, resolvedRoot); err != nil {
			return nil, err
		}
	}
	return &fixturePicker{root: resolvedRoot, steps: config.Steps}, nil
}

// preflightLoadedPicker holds the exact instance parsed and validated by
// preflightFixturePicker; installFixturePicker reuses this instance so the
// configuration is loaded exactly once.
var preflightLoadedPicker *fixturePicker

// preflightFixturePicker validates the configuration (if any) BEFORE the
// static listener starts serving, rejecting malformed/escaping entries with
// a nonzero exit. The empty default is a no-op.
func preflightFixturePicker() {
	if *pickerFixturePath == "" {
		return
	}
	picker, err := loadFixturePicker(*pickerFixturePath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "fixture picker: %v\n", err)
		os.Exit(2)
	}
	preflightLoadedPicker = picker
}

// installFixturePicker installs the closed-queue presenter using the exact
// instance cached by preflightFixturePicker; the empty default preserves
// the real production Wails dialog presenter installed by main.
func installFixturePicker(service *facade.ReaderService) {
	if preflightLoadedPicker == nil {
		return
	}
	service.SetPickerPresenter(&facade.PickerPresenter{
		ChooseSource:      preflightLoadedPicker.ChooseSource,
		ChooseDestination: preflightLoadedPicker.ChooseDestination,
	})
}
