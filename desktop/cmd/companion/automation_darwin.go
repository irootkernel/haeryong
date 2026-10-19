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
#cgo LDFLAGS: -framework Cocoa
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
*/
import "C"

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
	"unsafe"

	"github.com/wailsapp/wails/v3/pkg/application"
)

var (
	automationScript = flag.String("automation-script", "", "Owned automation script JSON for the public own-process AppKit proof (tagged builds only)")
	automationOut    = flag.String("automation-out", "", "Automation results JSON path (tagged builds only)")
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
	automationEventsMu.Lock()
	automationEvents = make(chan automationEvent, 256)
	automationEventsMu.Unlock()
	bundle := C.hyAutomationBundleIdentifier()
	report := &automationReport{BundleID: C.GoString(bundle)}
	C.free(unsafe.Pointer(bundle))
	writeReport(*automationOut, report)
	go func() {
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
