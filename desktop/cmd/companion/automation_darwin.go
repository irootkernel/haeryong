//go:build darwin && haeryong_test_state && haeryong_native_automation

// Public own-process AppKit automation for the tagged fixture build only
// (TASK-003-05 M4 native proof). The tag set deliberately requires
// haeryong_test_state together with haeryong_native_automation: an
// automation-enabled fixture can never run against production state.
//
// Every route here uses PUBLIC AppKit APIs against objects this process
// itself installed — performClose:, the status-item button's real
// target/action through performClick:, the public
// NSMenuDidBeginTrackingNotification with performActionForItemAtIndex: and
// cancelTracking, and NSSavePanel/NSOpenPanel completion through the public
// ok:/cancel: and directory/name setters. No private SDK handles, no
// reflection, no KVC, no Accessibility, no host installation. The untagged
// production binary contains none of these files. The Objective-C
// implementation lives in automation_darwin.m; this preamble only declares
// it (guard_darwin.go/.m precedent).
package main

/*
#cgo LDFLAGS: -framework Cocoa -framework WebKit
#include <stdlib.h>

void hyAutomationPerformCloseWindow(const char *title);
void hyAutomationActivateWindow(const char *title);
void hyAutomationWindowDetails(const char *title);
char *hyAutomationBundleIdentifier(void);
void hyAutomationClickTrayButton(const char *title);
void hyAutomationArmMenuSelection(const char *itemTitle);
void hyAutomationObserveWindowByTitle(const char *title);
void hyAutomationTryCompletePanel(const char *directory, const char *name, int save);
void hyAutomationTryCancelPanel(void);
void hyAutomationInstallChildProbe(const char *title, const char *source);
void hyAutomationResizeWindow(const char *title, double width, double height);
void hyAutomationSnapshot(const char *title, const char *path);
void hyAutomationKeyPress(const char *title, const char *key, int shift, const char *text);
*/
import "C"

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode/utf8"
	"unsafe"

	"github.com/wailsapp/wails/v3/pkg/application"
)

var (
	automationScript = flag.String("automation-script", "", "Owned automation script JSON for the public own-process AppKit proof (tagged builds only)")
	automationOut    = flag.String("automation-out", "", "Automation results JSON path (tagged builds only)")
	childProbePath   = flag.String("child-probe", "", "Owned child-observer JavaScript (explicit dual-tag test instrumentation only)")
)

type automationEvent struct {
	kind  int
	value int64
}

var (
	automationEvents   chan automationEvent
	automationEventsMu sync.Mutex
)

//export hyAutomationEvent
func hyAutomationEvent(kind C.int, value C.long) {
	automationEventsMu.Lock()
	defer automationEventsMu.Unlock()
	if automationEvents != nil {
		select {
		case automationEvents <- automationEvent{int(kind), int64(value)}:
		default:
		}
	}
}

func waitForEvent(kind int, timeout time.Duration) (int64, bool) {
	deadline := time.After(timeout)
	for {
		select {
		case event := <-automationEvents:
			if event.kind == kind {
				return event.value, true
			}
		case <-deadline:
			return 0, false
		}
	}
}

type automationStepSpec struct {
	ReportName string `json:"report_name"`
	Action     string `json:"action"`
	Ms         int    `json:"ms"`
	Label      string `json:"label"`
	Item       string `json:"item"`
	Directory  string `json:"directory"`
	Name       string `json:"name"`
	Save       bool   `json:"save"`
	TimeoutMs  int    `json:"timeout_ms"`
	Width      int    `json:"width"`
	Height     int    `json:"height"`
	Key        string `json:"key"`
	Shift      bool   `json:"shift"`
	Characters string `json:"characters"`
}

type automationStepResult struct {
	Action string         `json:"action"`
	OK     bool           `json:"ok"`
	Detail map[string]any `json:"detail,omitempty"`
}

type automationReport struct {
	Complete bool                   `json:"complete"`
	Steps    []automationStepResult `json:"steps"`
	BundleID string                 `json:"bundle_id"`
}

func writeReport(out string, report *automationReport) {
	encoded, err := json.MarshalIndent(report, "", " ")
	if err != nil {
		return
	}
	os.WriteFile(out, append(encoded, '\n'), 0600)
}

func runAutomation(app *application.App, window application.Window) error {
	if *automationScript == "" {
		if *childProbePath != "" {
			return fmt.Errorf("--child-probe requires --automation-script")
		}
		return nil
	}
	if *automationOut == "" {
		return fmt.Errorf("--automation-out is required with --automation-script")
	}
	raw, err := os.ReadFile(*automationScript)
	if err != nil {
		return fmt.Errorf("automation script unreadable: %w", err)
	}
	var steps []automationStepSpec
	if err := json.Unmarshal(raw, &steps); err != nil {
		return fmt.Errorf("automation script must be a JSON array of steps: %w", err)
	}
	probe, err := readOwnedChildProbe(*childProbePath)
	if err != nil {
		return err
	}
	automationEventsMu.Lock()
	automationEvents = make(chan automationEvent, 256)
	automationEventsMu.Unlock()
	bundle := C.hyAutomationBundleIdentifier()
	report := &automationReport{BundleID: C.GoString(bundle)}
	C.free(unsafe.Pointer(bundle))
	writeReport(*automationOut, report)
	go func() {
		if probe != "" {
			installed := false
			for start := time.Now(); time.Since(start) < 30*time.Second; {
				title, source := C.CString("Haeryong"), C.CString(probe)
				C.hyAutomationInstallChildProbe(title, source)
				C.free(unsafe.Pointer(title))
				C.free(unsafe.Pointer(source))
				value, observed := waitForEvent(7, 3*time.Second)
				if observed && value == 1 {
					installed = true
					break
				}
				if observed && value < 0 {
					break
				}
				time.Sleep(100 * time.Millisecond)
			}
			report.Steps = append(report.Steps, automationStepResult{
				Action: "installChildProbe", OK: installed,
				Detail: map[string]any{"installed": installed, "content_world": "haeryong.task006.observer"},
			})
			writeReport(*automationOut, report)
			if !installed {
				report.Complete = true
				writeReport(*automationOut, report)
				return
			}
		}
		for _, step := range steps {
			result := automationStepResult{Action: step.Action}
			switch step.Action {
			case "waitForReport":
				deadline := time.Duration(step.TimeoutMs) * time.Millisecond
				if deadline <= 0 {
					deadline = 90 * time.Second
				}
				path := filepath.Join(diagnosticOutputDir, fmt.Sprintf("native-reader-selfcheck-%d.json", os.Getpid()))
				for start := time.Now(); time.Since(start) < deadline; {
					var observed struct {
						Rows []struct {
							Name   string `json:"name"`
							Status string `json:"status"`
						} `json:"rows"`
					}
					if raw, err := os.ReadFile(path); err == nil && json.Unmarshal(raw, &observed) == nil {
						for _, row := range observed.Rows {
							if row.Name == step.ReportName && row.Status == "pass" {
								result.OK = true
								break
							}
						}
					}
					if result.OK {
						break
					}
					time.Sleep(100 * time.Millisecond)
				}
			case "resizeWindow":
				if step.Width <= 0 || step.Height <= 0 {
					result.Detail = map[string]any{"error": "positive content width/height required"}
					break
				}
				title := C.CString(step.Label)
				C.hyAutomationResizeWindow(title, C.double(step.Width), C.double(step.Height))
				C.free(unsafe.Pointer(title))
				value, observed := waitForEvent(8, 10*time.Second)
				result.OK = observed && value == 1
				result.Detail = map[string]any{"requested_width": step.Width, "requested_height": step.Height, "dispatch_observed": result.OK}
			case "snapshot":
				if !validSnapshotName(step.Name) {
					result.Detail = map[string]any{"error": "snapshot name must be one local filename component"}
					break
				}
				directory := filepath.Join(diagnosticOutputDir, "snapshots")
				if err := os.MkdirAll(directory, 0700); err != nil {
					result.Detail = map[string]any{"error": "snapshot directory unavailable"}
					break
				}
				path := filepath.Join(directory, step.Name+".png")
				title, target := C.CString(step.Label), C.CString(path)
				C.hyAutomationSnapshot(title, target)
				C.free(unsafe.Pointer(title))
				C.free(unsafe.Pointer(target))
				bytes, observed := waitForEvent(9, 30*time.Second)
				info, err := os.Stat(path)
				result.OK = observed && bytes > 0 && err == nil && info.Mode().IsRegular() && info.Size() == bytes
				if result.OK {
					if err := os.Chmod(path, 0600); err != nil {
						result.OK = false
					}
				}
				result.Detail = map[string]any{"png": path, "bytes": bytes}
			case "keyPress":
				if !validAutomationKey(step.Key, step.Characters) {
					result.Detail = map[string]any{"error": "unsupported key or empty/invalid text"}
					break
				}
				title, key, text := C.CString(step.Label), C.CString(step.Key), C.CString(step.Characters)
				C.hyAutomationKeyPress(title, key, boolToInt(step.Shift), text)
				C.free(unsafe.Pointer(title))
				C.free(unsafe.Pointer(key))
				C.free(unsafe.Pointer(text))
				value, observed := waitForEvent(10, 10*time.Second)
				result.OK = observed && value == 1
				result.Detail = map[string]any{"key": step.Key, "shift": step.Shift, "dispatch_observed": result.OK}
			case "windowDetails":
				title := C.CString(step.Label)
				C.hyAutomationWindowDetails(title)
				C.free(unsafe.Pointer(title))
				flags, ok := waitForEvent(6, 3*time.Second)
				result.OK = ok && flags&1 != 0
				result.Detail = map[string]any{"visible": flags&2 != 0, "app_active": flags&4 != 0,
					"occlusion_visible": flags&8 != 0, "active_space": flags&16 != 0,
					"miniaturized": flags&32 != 0, "webview_found": flags&256 != 0,
					"webview_empty_frame": flags&128 != 0, "webview_in_window": flags&512 != 0,
					"webview_hidden": flags&1024 != 0, "key_window": flags&2048 != 0,
					"activation_policy": (flags >> 12) & 3}
			case "activateWindow":
				for start := time.Now(); time.Since(start) < 10*time.Second; {
					title := C.CString(step.Label)
					C.hyAutomationActivateWindow(title)
					C.free(unsafe.Pointer(title))
					value, ok := waitForEvent(5, 3*time.Second)
					if ok && value == 1 {
						result.OK = true
						break
					}
					time.Sleep(100 * time.Millisecond)
				}
			case "wait":
				time.Sleep(time.Duration(step.Ms) * time.Millisecond)
				result.OK = true
			case "closeWindow":
				title := C.CString(step.Label)
				C.hyAutomationPerformCloseWindow(title)
				C.free(unsafe.Pointer(title))
				result.OK = true
			case "windowState":
				title := C.CString(step.Label)
				C.hyAutomationObserveWindowByTitle(title)
				C.free(unsafe.Pointer(title))
				value, ok := waitForEvent(1, 10*time.Second)
				result.OK = ok && value >= 0
				result.Detail = map[string]any{"visible": value == 1, "found": ok && value >= 0}
			case "trayMenuSelect":
				item := C.CString(step.Item)
				C.hyAutomationArmMenuSelection(item)
				C.free(unsafe.Pointer(item))
				time.Sleep(150 * time.Millisecond)
				label := C.CString(step.Label)
				C.hyAutomationClickTrayButton(label)
				C.free(unsafe.Pointer(label))
				value, ok := waitForEvent(2, 20*time.Second)
				result.OK = ok && value >= 0
				result.Detail = map[string]any{"index": value, "observed": ok}
			case "panelComplete":
				deadline := time.Duration(step.TimeoutMs) * time.Millisecond
				if deadline <= 0 {
					deadline = 30 * time.Second
				}
				for start := time.Now(); time.Since(start) < deadline; {
					var directory, name *C.char
					if step.Directory != "" {
						directory = C.CString(step.Directory)
					}
					if step.Save && step.Name != "" {
						name = C.CString(step.Name)
					}
					C.hyAutomationTryCompletePanel(directory, name, boolToInt(step.Save))
					if directory != nil {
						C.free(unsafe.Pointer(directory))
					}
					if name != nil {
						C.free(unsafe.Pointer(name))
					}
					if value, ok := waitForEvent(3, 3*time.Second); ok && value != 0 {
						result.OK = value > 0
						result.Detail = map[string]any{"public_action_route": value, "action_dispatched": value > 0}
						break
					}
					time.Sleep(120 * time.Millisecond)
				}
			case "panelCancel":
				deadline := time.Duration(step.TimeoutMs) * time.Millisecond
				if deadline <= 0 {
					deadline = 30 * time.Second
				}
				for start := time.Now(); time.Since(start) < deadline; {
					C.hyAutomationTryCancelPanel()
					if value, ok := waitForEvent(4, 3*time.Second); ok && value != 0 {
						result.OK = value > 0
						result.Detail = map[string]any{"public_action_route": value, "action_dispatched": value > 0}
						break
					}
					time.Sleep(120 * time.Millisecond)
				}
			default:
				result.OK = false
				result.Detail = map[string]any{"error": "unknown action"}
			}
			report.Steps = append(report.Steps, result)
			writeReport(*automationOut, report)
			if !result.OK {
				// A failed action terminates the script: no later action may
				// fire at an unintended target. Complete marks the report as
				// finished, never as a pass.
				report.Complete = true
				writeReport(*automationOut, report)
				return
			}
		}
		report.Complete = true
		writeReport(*automationOut, report)
	}()
	return nil
}

func boolToInt(value bool) C.int {
	if value {
		return 1
	}
	return 0
}

func init() {
	automationAttach = runAutomation
}

// These validate only test controls, never imported content.
func validSnapshotName(name string) bool {
	return name != "" && name != "." && name != ".." && filepath.Base(name) == name && !strings.ContainsRune(name, 0)
}
func validAutomationKey(key, text string) bool {
	switch key {
	case "tab", "return", "space", "escape", "left", "right", "up", "down":
		return true
	case "text":
		return text != "" && utf8.ValidString(text) && !strings.ContainsRune(text, 0)
	}
	return false
}
func readOwnedChildProbe(path string) (string, error) {
	if path == "" {
		return "", nil
	}
	if !filepath.IsAbs(path) {
		return "", fmt.Errorf("--child-probe must be absolute")
	}
	info, err := os.Lstat(path)
	if err != nil {
		return "", fmt.Errorf("child probe unavailable: %w", err)
	}
	owner, ok := info.Sys().(*syscall.Stat_t)
	if !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 || !ok || owner.Uid != uint32(os.Getuid()) {
		return "", fmt.Errorf("child probe must be an owned private regular file")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("child probe unreadable: %w", err)
	}
	if len(raw) == 0 || !utf8.Valid(raw) || strings.ContainsRune(string(raw), 0) {
		return "", fmt.Errorf("child probe must be nonempty UTF-8 JavaScript text")
	}
	return string(raw), nil
}
