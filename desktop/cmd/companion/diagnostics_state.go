//go:build haeryong_test_state

package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"

	facade "haeryong.local/haeryong/desktop/readerfacade"
)

// Explicitly flagged harness diagnostics for tagged fixture builds only.
// The untagged production binary contains none of these flags and none of
// this code; passing them there is a normal flag rejection, not a silent
// no-op.
var (
	diagnosticsReceipts = flag.Bool("diagnostics-receipts", false, "Install the harness receipt-file observer (test builds only)")
	probePath           = flag.String("reader-probe", "", "Owned test probe script injected after guard+bootstrap into the actual app (test builds only)")
	diagnosticsStateDir = flag.String("state", "", "Diagnostics state directory override (test builds only)")
)

func diagnosticsEnabled() bool { return *diagnosticsReceipts || *probePath != "" }

func installDiagnostics(service *facade.ReaderService, stateDir string) {
	diagnosticOutputDir = stateDir
	if *diagnosticsReceipts {
		service.SetDiagnostics(facade.FileReceiptObserver(stateDir))
	}
}

// Tagged observation of the real closed Invoke boundary. Only identities,
// checkpoints and outcome references are retained, never bearer, authored
// content, source paths or chunk bytes. Transfer chunks are not traced.
var diagnosticOutputDir string
var diagnosticTrace = struct {
	sync.Mutex
	Calls     []map[string]any `json:"calls"`
	Lifecycle []map[string]any `json:"lifecycle"`
}{}

func writeDiagnosticTraceLocked() {
	encoded, err := json.MarshalIndent(map[string]any{"calls": diagnosticTrace.Calls,
		"lifecycle": diagnosticTrace.Lifecycle}, "", " ")
	if err != nil {
		return
	}
	path := filepath.Join(diagnosticOutputDir, fmt.Sprintf("native-trace-%d.json", os.Getpid()))
	if os.WriteFile(path+".tmp", encoded, 0600) == nil {
		_ = os.Rename(path+".tmp", path)
	}
}

func observeNativeInvoke(callID string, raw json.RawMessage, response any) {
	if !diagnosticsEnabled() {
		return
	}
	var in struct {
		Args []struct {
			Operation string
			Payload   map[string]json.RawMessage
		} `json:"args"`
	}
	if json.Unmarshal(raw, &in) != nil || len(in.Args) != 1 {
		return
	}
	operation := in.Args[0].Operation
	switch operation {
	case "attachSession", "pair", "pollPairing", "adoptPairing", "completeUpload", "importPublish",
		"sourceVerifyStable", "chooseImportSource", "chooseDownloadDestination", "destOpen", "destClose", "destAbort", "releaseSource":
	default:
		return
	}
	entry := map[string]any{"call_id": callID, "operation": operation, "at": time.Now().UTC()}
	for _, key := range []string{"upload_id", "operation_id", "checkpoint", "mode"} {
		if value, ok := in.Args[0].Payload[key]; ok {
			entry[key] = value
		}
	}
	var result struct {
		OK    bool `json:"ok"`
		Value struct {
			Session struct {
				ID        string `json:"session_id"`
				LibraryID string `json:"library_id"`
			} `json:"session"`
			Kind string                     `json:"kind"`
			Body map[string]json.RawMessage `json:"body"`
		} `json:"value"`
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if encoded, err := json.Marshal(response); err == nil && json.Unmarshal(encoded, &result) == nil {
		entry["ok"], entry["kind"] = result.OK, result.Value.Kind
		if result.Value.Session.ID != "" {
			entry["session_id"] = result.Value.Session.ID
			entry["library_id"] = result.Value.Session.LibraryID
		}
		if result.Error.Code != "" {
			entry["error_code"] = result.Error.Code
		}
		for _, key := range []string{"job_id", "operation_id", "upload_id"} {
			if value, ok := result.Value.Body[key]; ok {
				entry["result_"+key] = value
			}
		}
	}
	diagnosticTrace.Lock()
	defer diagnosticTrace.Unlock()
	diagnosticTrace.Calls = append(diagnosticTrace.Calls, entry)
	writeDiagnosticTraceLocked()
}

func noteCompanionLifecycle(event string) {
	if !diagnosticsEnabled() {
		return
	}
	diagnosticTrace.Lock()
	defer diagnosticTrace.Unlock()
	diagnosticTrace.Lifecycle = append(diagnosticTrace.Lifecycle, map[string]any{"event": event, "at": time.Now().UTC()})
	writeDiagnosticTraceLocked()
}

func probeScript() string {
	if *probePath == "" {
		return ""
	}
	raw, err := os.ReadFile(*probePath)
	if err != nil {
		log.Fatalf("reader probe script unreadable: %v", err)
	}
	return string(raw)
}

func companionDiagnosticsState(defaultDir string) string {
	if *diagnosticsStateDir != "" {
		return *diagnosticsStateDir
	}
	return defaultDir
}

// testBindings is the tagged fixture build's bound surface: the closed
// production Invoke plus the explicitly flagged selfcheck report. It uses
// explicit methods only (no embedding), so nothing else of the facade is
// ever promoted into the reflected surface.
type testBindings struct {
	service *facade.ReaderService
}

func (t *testBindings) Invoke(in struct {
	Operation string          `json:"Operation"`
	Payload   json.RawMessage `json:"Payload"`
}) (map[string]any, error) {
	return t.service.Invoke(in)
}

func (t *testBindings) SelfcheckReport(in struct {
	Rows json.RawMessage `json:"Rows"`
}) (map[string]any, error) {
	return t.service.SelfcheckReport(in)
}

// buildServiceBindings hands the SDK the tagged fixture surface.
func buildServiceBindings(service *facade.ReaderService) application.Service {
	return application.NewService(&testBindings{service: service})
}

// registerBindings registers the stable 701100 on the ACTUALLY bound tagged
// receiver, plus the report binding on that same receiver — the latter only
// when diagnostics are explicitly enabled; the raw-message guard still
// whitelists 701101 by the same flag.
func registerBindings(*facade.ReaderService) {
	application.RegisterBindingMethodID((*testBindings).Invoke, 701100)
	if diagnosticsEnabled() {
		application.RegisterBindingMethodID((*testBindings).SelfcheckReport, 701101)
	}
}
