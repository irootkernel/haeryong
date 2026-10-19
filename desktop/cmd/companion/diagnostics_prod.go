//go:build !haeryong_test_state

package main

import (
	"encoding/json"
	"github.com/wailsapp/wails/v3/pkg/application"

	facade "haeryong.local/haeryong/desktop/readerfacade"
)

// Production builds have NO diagnostic mode at all: no receipt observer, no
// probe injection, no selfcheck report binding, no diagnostics state
// override and no such flags. The SDK binding surface is exactly the closed
// non-embedding Invoke wrapper.
func diagnosticsEnabled() bool { return false }

func observeNativeInvoke(string, json.RawMessage, any) {}

func noteCompanionLifecycle(string) {}

func installDiagnostics(*facade.ReaderService, string) {}

func probeScript() string { return "" }

func companionDiagnosticsState(defaultDir string) string { return defaultDir }

// buildServiceBindings hands the SDK the closed production surface.
func buildServiceBindings(service *facade.ReaderService) application.Service {
	return application.NewService(&companionBindings{service: service})
}

// registerBindings registers the stable 701100 on the PRODUCTION receiver.
func registerBindings(*facade.ReaderService) {
	application.RegisterBindingMethodID((*companionBindings).Invoke, 701100)
}
