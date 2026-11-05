// Production Haeryong desktop companion (TASK-003-05 M4). A thin pinned
// Wails beta28 shell around the ONE shared readerfacade: it attaches the
// existing authenticated daemon without owning its data or lifetime, starts
// it only through the owner's explicit offer (detached Setsid sibling
// binary), and never installs, initializes, migrates or stops anything.
//
// Binding policy: ONLY the closed typed Invoke(701100) route exists — the
// raw-message handler whitelists the exact trusted main frame, the exact
// static origin, kind 'haeyong.native', object 0, method 0, an empty
// methodName and the single stable method ID. The SDK reflects every
// exported method of whatever type it is handed, so the bound service is a
// non-embedding wrapper whose only exported method is Invoke: SelfcheckReport,
// probes, receipts and the harness fixture bindings are structurally absent
// from production. Diagnostics exist only in haeryong_test_state builds
// behind explicit flags.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"

	"haeryong.local/haeryong/desktop/onboarding"
	facade "haeryong.local/haeryong/desktop/readerfacade"
	"haeryong.local/haeryong/internal/application/render"
)

// productAPIVersion is the product API pin every effective authority
// requires (the daemon's capabilities api_version).
const productAPIVersion = "1.0"

// companionUIPort is the fixed loopback port of the companion's static
// native UI origin. It is deliberately distinct from the daemon API port
// and never falls back: a collision is a typed startup failure.
const companionUIPort = render.CompanionUIPort

// automationAttach is installed only by haeryong_native_automation builds;
// production leaves it nil and contains no automation code at all.
var automationAttach func(app *application.App, window application.Window) error

// guardStatsPath is the diagnostics-only guard counter artifact path set in
// main; empty in unflagged runs.
var guardStatsPath string

// companionBindings is the ONLY type handed to the SDK binding engine. It
// does NOT embed *facade.ReaderService: embedding would promote every
// exported facade method back into the reflected bound surface.
type companionBindings struct {
	service *facade.ReaderService
}

// Invoke mirrors the facade's typed dispatch signature exactly and is the
// single registered stable binding (701100).
func (c *companionBindings) Invoke(in struct {
	Operation string          `json:"Operation"`
	Payload   json.RawMessage `json:"Payload"`
}) (map[string]any, error) {
	return c.service.Invoke(in)
}

type companionTransport struct{ processor *application.MessageProcessor }

func (t *companionTransport) Start(_ context.Context, p *application.MessageProcessor) error {
	t.processor = p
	return nil
}
func (t *companionTransport) Stop() error      { return nil }
func (t *companionTransport) JSClient() []byte { return nil }

const nativeBootstrapCompanion = `(()=>{if(window.haeryongNativeCall)return;let calls=new Map();window.haeryongNativeReply=m=>{let p=calls.get(m.id);if(p){calls.delete(m.id);m.error?p.reject(new Error(m.error)):p.resolve(m.value);}};window.haeryongNativeCall=(methodID,args=[])=>new Promise((resolve,reject)=>{let id=crypto.randomUUID();calls.set(id,{resolve,reject});window.webkit.messageHandlers.external.postMessage(JSON.stringify({kind:'haeyong.native',id,object:0,method:0,args:{'call-id':id,methodID,args}}));});window.dispatchEvent(new Event('haeryong-native-bridge'));})();`

func resolveDist(explicit, exeDir string) (string, error) {
	if explicit != "" {
		return explicit, nil
	}
	candidates := []string{
		filepath.Join(exeDir, "dist-native"),
		filepath.Join(exeDir, "..", "Resources", "dist-native"),
	}
	for _, candidate := range candidates {
		if info, err := os.Stat(candidate); err == nil && info.IsDir() {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("compiled native dist not found; pass --dist explicitly")
}

func main() {
	appPath := flag.String("app-path", "/", "App path (route and query) opened at the fixed static origin")
	dist := flag.String("dist", "", "Compiled dist-native directory served at the fixed static origin")
	dataDir := flag.String("selection-data-dir", "", "Absolute selection data directory (optional)")
	volumeRoot := flag.String("selection-volume-root", "", "Absolute selection volume root (optional)")
	port := flag.Int("selection-port", 0, "Selection listener port override (0 keeps the daemon's configured port)")
	flag.Parse()
	// Tagged fixture picker configuration is validated BEFORE the static
	// listener starts serving; the production no-op is a no-op.
	preflightFixturePicker()
	if *dataDir != "" && !filepath.IsAbs(*dataDir) {
		log.Fatal("--selection-data-dir must be absolute")
	}
	if *volumeRoot != "" && !filepath.IsAbs(*volumeRoot) {
		log.Fatal("--selection-volume-root must be absolute")
	}
	stateBase, err := companionStateBase()
	if err != nil {
		log.Fatalf("companion state base unavailable: %v", err)
	}
	credentialDir := filepath.Join(stateBase, "desktop")
	if err := os.MkdirAll(credentialDir, 0700); err != nil {
		log.Fatalf("protected desktop credential namespace unavailable: %v", err)
	}
	if err := os.Chmod(credentialDir, 0700); err != nil {
		log.Fatalf("protected desktop credential namespace unavailable: %v", err)
	}
	exe, err := os.Executable()
	if err != nil {
		log.Fatal(err)
	}
	exeDir := filepath.Dir(exe)
	daemonBinary := filepath.Join(exeDir, "haeryong")
	servedDist, err := resolveDist(*dist, exeDir)
	if err != nil {
		log.Fatal(err)
	}
	stateDir := companionDiagnosticsState(credentialDir)
	if diagnosticsEnabled() {
		guardStatsPath = filepath.Join(stateDir, "native-guard.json")
	}
	onb, err := onboarding.New(onboarding.Config{
		Selection: onboarding.Selection{
			Profile:    "default",
			DataDir:    *dataDir,
			VolumeRoot: *volumeRoot,
			Port:       *port,
		},
		CredentialDir: credentialDir,
		ProductAPI:    productAPIVersion,
		DaemonBinary:  daemonBinary,
	})
	if err != nil {
		log.Fatal(err)
	}
	service := facade.NewReaderServiceWithOnboarding("", stateDir, "", onb)
	installDiagnostics(service, stateDir)

	address := "127.0.0.1:" + companionUIPort
	listener, err := net.Listen("tcp", address)
	if err != nil {
		// No silent fallback: the fixed static origin must be exactly this.
		log.Fatalf("fixed companion static origin %s unavailable: %v", address, err)
	}
	origin := "http://" + address
	originURL, err := url.Parse(origin)
	if err != nil {
		log.Fatal(err)
	}
	server := &http.Server{Handler: companionStaticHandler(servedDist, address, origin)}
	go func() {
		if err := server.Serve(listener); err != nil && err != http.ErrServerClosed {
			log.Printf("companion static origin ended: %v", err)
		}
	}()
	defer server.Close()

	transport := &companionTransport{}
	registerBindings(service)
	allowReport := diagnosticsEnabled()
	var app *application.App
	// The pinned SDK runs each raw message handler in its own goroutine, so
	// the guard-ready publication must be synchronized, not a plain bool.
	var guardReady atomic.Bool
	schemeDenied := &atomic.Int64{}
	app = application.New(application.Options{
		Name: "Haeryong Companion", Transport: transport,
		Services: []application.Service{buildServiceBindings(service)},
		Assets: application.AssetOptions{Middleware: func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				http.Error(w, "native scheme endpoints remain disabled", 403)
				schemeDenied.Add(1)
			})
		}},
		RawMessageHandler: func(window application.Window, message string, originInfo *application.OriginInfo) {
			if originInfo == nil || !originInfo.IsMainFrame || !guardReady.Load() {
				return
			}
			sender, err := url.Parse(originInfo.Origin)
			if err != nil || sender.Scheme != originURL.Scheme || sender.Host != originURL.Host {
				return
			}
			var envelope struct {
				Kind, ID       string
				Object, Method int
				Args           json.RawMessage
			}
			if json.Unmarshal([]byte(message), &envelope) != nil || envelope.Kind != "haeyong.native" {
				return
			}
			if envelope.Object != 0 || envelope.Method != 0 {
				return
			}
			// Closed method whitelist: only the typed Invoke dispatch (and,
			// in explicitly flagged diagnostics builds, the selfcheck
			// report). The pinned SDK gives a nonempty args.methodName
			// precedence over methodID, so a name must never accompany an
			// allowed ID.
			var call struct {
				MethodID   int    `json:"methodID"`
				MethodName string `json:"methodName"`
			}
			if json.Unmarshal(envelope.Args, &call) != nil {
				return
			}
			allowed := call.MethodName == "" && (call.MethodID == 701100 || (allowReport && call.MethodID == 701101))
			if !allowed {
				return
			}
			var request application.RuntimeRequest
			if json.Unmarshal([]byte(message), &request) != nil {
				return
			}
			request.WebviewWindowID = uint32(window.ID())
			response, err := transport.processor.HandleRuntimeCallWithIDs(context.Background(), &request)
			observeNativeInvoke(envelope.ID, envelope.Args, response)
			value := map[string]any{"id": envelope.ID, "value": response}
			if err != nil {
				value["error"] = err.Error()
			}
			encoded, _ := json.Marshal(value)
			application.InvokeSync(func() { facade.EvalNative(window, "window.haeryongNativeReply("+string(encoded)+")") })
		},
	})

	options := application.WebviewWindowOptions{Title: "Haeryong", Width: 1180, Height: 880, URL: origin + *appPath}
	options.Mac.WebviewPreferences.JavaScriptCanOpenWindowsAutomatically.Set(true)
	// Include links and controls in this WebView's keyboard navigation.
	options.Mac.WebviewPreferences.TabFocusesLinks.Set(true)
	window := app.Window.NewWithOptions(options)

	// The production picker presenter: real Wails NSOpenPanel/NSSavePanel
	// with cancel/path fidelity and no extension restrictions. Hidden files
	// stay visible — the library preserves hidden entries.
	service.SetPickerPresenter(&facade.PickerPresenter{
		ChooseSource: func(mode string) (string, bool, error) {
			dialog := app.Dialog.OpenFile()
			dialog.AttachToWindow(window)
			dialog.SetTitle("Haeryong 가져오기")
			dialog.CanChooseFiles(mode == "file" || mode == "archive")
			dialog.CanChooseDirectories(mode == "directory")
			dialog.ShowHiddenFiles(true)
			dialog.CanCreateDirectories(false)
			path, err := dialog.PromptForSingleSelection()
			if err != nil {
				return "", false, err
			}
			if path == "" {
				return "", true, nil
			}
			return path, false, nil
		},
		ChooseDestination: func(suggestedName string, mode string) (string, bool, error) {
			if mode == "directory" {
				// Directory destinations are real directory NSOpenPanels:
				// the selected path must be a directory (Go re-verifies
				// IsDir); an NSSavePanel could only ever propose a file.
				dialog := app.Dialog.OpenFile()
				dialog.AttachToWindow(window)
				dialog.SetTitle("Haeryong 내보내기 폴더")
				dialog.CanChooseFiles(false)
				dialog.CanChooseDirectories(true)
				dialog.ShowHiddenFiles(true)
				dialog.CanCreateDirectories(false)
				path, err := dialog.PromptForSingleSelection()
				if err != nil {
					return "", false, err
				}
				if path == "" {
					return "", true, nil
				}
				return path, false, nil
			}
			dialog := app.Dialog.SaveFile()
			dialog.AttachToWindow(window)
			dialog.SetMessage("Haeryong 내보내기")
			dialog.ShowHiddenFiles(true)
			dialog.CanCreateDirectories(false)
			dialog.SetFilename(suggestedName)
			path, err := dialog.PromptForSingleSelection()
			if err != nil {
				return "", false, err
			}
			if path == "" {
				return "", true, nil
			}
			return path, false, nil
		},
	})
	// Tagged test builds may override the selection authority with a closed
	// ordered queue from an owned CLI JSON configuration; the empty default
	// preserves this real production Wails dialog presenter.
	installFixturePicker(service)

	// Hide-to-tray: the red button (and any public performClose:) publishes
	// WindowClosing once; the hook cancels the close and hides the window,
	// so the privileged document is never replaced or destroyed by an
	// external navigation and owned streams/renders survive the hide.
	window.RegisterHook(events.Common.WindowClosing, func(event *application.WindowEvent) {
		event.Cancel()
		window.Hide()
		noteCompanionLifecycle("window-hide")
	})
	window.OnWindowEvent(events.Mac.WebViewDidFinishNavigation, func(_ *application.WindowEvent) {
		application.InvokeSync(func() {
			if !facade.InstallGuard(window, origin) {
				// Fail closed: without the native sender guard the window
				// must never stay alive — not hidden behind the tray hook,
				// not navigating further. Terminate the whole process with
				// a nonzero exit.
				log.Print("native guard installation failed; terminating")
				os.Exit(1)
			}
			guardReady.Store(true)
			facade.EvalNative(window, nativeBootstrapCompanion)
			writeGuardStats(schemeDenied.Load())
			if probe := probeScript(); probe != "" {
				// Explicitly flagged diagnostics builds only: a test-owned
				// probe injected AFTER the guard and bridge, granting the
				// imported child nothing.
				facade.EvalNative(window, "(async()=>{"+probe+"})()")
			}
		})
	})
	// Minimal native tray with a distinctive public label. Left-click opens
	// the real menu through the installed status-item action (the SDK's
	// public own-process presentation path); there is no attached-window
	// toggle so the menu is the single tray surface.
	tray := app.SystemTray.New()
	tray.SetLabel("Haeryong")
	menu := application.NewMenu()
	open := menu.Add("열기")
	open.OnClick(func(*application.Context) {
		window.Show().Focus()
		noteCompanionLifecycle("tray-open")
	})
	menu.AddSeparator()
	quit := menu.Add("컴패니언 종료")
	quit.OnClick(func(*application.Context) {
		noteCompanionLifecycle("tray-quit")
		// Owned renderer deliveries and streams close explicitly before the
		// process leaves; the daemon keeps its own lifetime.
		application.InvokeSync(func() { writeGuardStats(schemeDenied.Load()) })
		service.CloseOwned()
		app.Quit()
	})
	tray.SetMenu(menu)

	if automationAttach != nil {
		if err := automationAttach(app, window); err != nil {
			log.Fatalf("native automation unavailable: %v", err)
		}
	}
	if err := app.Run(); err != nil {
		log.Fatal(err)
	}
	noteCompanionLifecycle("run-returned")
}

// writeGuardStats persists the native guard counters for explicitly flagged
// diagnostics builds only (the harness evidence channel); production builds
// never write diagnostics.
func writeGuardStats(schemeDenied int64) {
	if guardStatsPath == "" || !diagnosticsEnabled() {
		return
	}
	stats := facade.GuardStats()
	stats["pid"] = os.Getpid()
	stats["scheme_denied"] = schemeDenied
	encoded, err := json.Marshal(stats)
	if err != nil {
		return
	}
	os.WriteFile(guardStatsPath, encoded, 0600)
}

// companionStaticHandler serves the compiled native dist at the fixed
// static origin with the exact-host and supplied-origin guards running
// BEFORE any static or SPA serving: a request whose Host is not exactly
// 127.0.0.1:<port>, or that carries a foreign Origin header, is refused
// before a single byte of the trusted UI is delivered. Every reply —
// including those early rejects — carries no-store, and no CORS header is
// ever emitted.
func companionStaticHandler(dist, originAddress, origin string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		if r.Host != originAddress {
			http.Error(w, "not found", 404)
			return
		}
		if supplied := r.Header.Get("Origin"); supplied != "" && supplied != origin {
			http.Error(w, "origin refused", 403)
			return
		}
		clean := filepath.Clean("/" + r.URL.Path)
		if strings.Contains(clean, "..") {
			http.Error(w, "not found", 404)
			return
		}
		candidate := filepath.Join(dist, clean)
		if info, err := os.Stat(candidate); err != nil || info.IsDir() {
			// SPA routing fallback: only the compiled entry document.
			entry := filepath.Join(dist, "index.html")
			if _, err := os.Stat(entry); err != nil {
				entry = filepath.Join(dist, "native.html")
			}
			// ServeFile canonicalizes directory-looking request paths and
			// rejects "/" when mapped directly to native.html. The SPA
			// entry is content at the requested route, including the root.
			file, err := os.Open(entry)
			if err != nil {
				http.NotFound(w, r)
				return
			}
			defer file.Close()
			info, err := file.Stat()
			if err != nil || !info.Mode().IsRegular() {
				http.NotFound(w, r)
				return
			}
			http.ServeContent(w, r, info.Name(), info.ModTime(), file)
			return
		}
		http.ServeFile(w, r, candidate)
	})
}
