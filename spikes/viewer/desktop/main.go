// Actual pinned Wails binding dispatch behind a native sender filter.
package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
)

type fixtureTransport struct{ processor *application.MessageProcessor }

func (t *fixtureTransport) Start(_ context.Context, p *application.MessageProcessor) error {
	t.processor = p
	return nil
}
func (t *fixtureTransport) Stop() error      { return nil }
func (t *fixtureTransport) JSClient() []byte { return nil }

type BoundaryFixture struct {
	base, token, state string
	calls              atomic.Int64
	schemeDenied       atomic.Int64
	mu                 sync.Mutex
	renderers          map[string]bool
	closeOnce          sync.Once
}

func (s *BoundaryFixture) Wait(milliseconds int) error {
	if milliseconds < 0 || milliseconds > 120000 {
		return errors.New("fixture wait range")
	}
	time.Sleep(time.Duration(milliseconds) * time.Millisecond)
	return nil
}

func (s *BoundaryFixture) Probe() map[string]any {
	count := s.calls.Add(1)
	var stats map[string]any
	application.InvokeSync(func() { stats = guardStats() })
	return map[string]any{"binding_calls": count, "native_guard": stats, "native_scheme_denials": s.schemeDenied.Load(), "dispatch": "Wails v3.0.0-beta.28 MessageProcessor -> registered binding"}
}

type ProxyRequest struct {
	Path, Method, Body string
	Headers            map[string]string
}

func (s *BoundaryFixture) Request(in ProxyRequest) (map[string]any, error) {
	u, e := url.Parse(in.Path)
	if e != nil || u.IsAbs() || u.Host != "" || u.Fragment != "" {
		return nil, errors.New("fixture relative path required")
	}
	allowed := map[string]map[string]bool{"/session": {"GET": true}, "/viewer/config": {"GET": true}, "/viewer/page": {"GET": true}, "/viewer/observations": {"GET": true}, "/viewer/deliver": {"POST": true}, "/viewer/download-issue": {"POST": true}, "/viewer/renew": {"POST": true}, "/viewer/close": {"POST": true}, "/viewer/report": {"POST": true}, "/viewer/context": {"POST": true, "PUT": true, "PATCH": true, "DELETE": true}}
	if !allowed[u.Path][in.Method] {
		return nil, errors.New("not a narrow fixture proxy route")
	}
	body, e := base64.StdEncoding.DecodeString(in.Body)
	if e != nil {
		return nil, e
	}
	request, e := http.NewRequest(in.Method, s.base+u.RequestURI(), bytes.NewReader(body))
	if e != nil {
		return nil, e
	}
	request.Header.Set("Authorization", "Bearer "+s.token)
	request.Header.Set("X-Native-Proxy", "1")
	for k, v := range in.Headers {
		switch k {
		case "Content-Type", "X-Page-Binding", "X-Bytes-SHA256":
			request.Header.Set(k, v)
		default:
			return nil, errors.New("proxy header denied")
		}
	}
	response, e := http.DefaultClient.Do(request)
	if e != nil {
		return nil, e
	}
	defer response.Body.Close()
	data, e := io.ReadAll(response.Body)
	if e != nil {
		return nil, e
	}
	if (u.Path == "/viewer/deliver" && response.StatusCode == 201) || (u.Path == "/viewer/close" && response.StatusCode == 204) {
		var issued struct{ ID string }
		if u.Path == "/viewer/deliver" {
			json.Unmarshal(data, &issued)
		} else {
			json.Unmarshal(body, &issued)
		}
		s.mu.Lock()
		if s.renderers == nil {
			s.renderers = map[string]bool{}
		}
		if u.Path == "/viewer/deliver" {
			s.renderers[issued.ID] = true
		} else {
			delete(s.renderers, issued.ID)
		}
		ids := []string{}
		for id := range s.renderers {
			ids = append(ids, id)
		}
		snapshot, _ := json.Marshal(map[string]any{"pid": os.Getpid(), "renderer_ids": ids})
		os.WriteFile(filepath.Join(s.state, fmt.Sprintf("native-observed-%d.json", os.Getpid())), snapshot, 0600)
		s.mu.Unlock()
	}
	return map[string]any{"status": response.StatusCode, "body": string(data)}, nil
}
func (s *BoundaryFixture) closeRenderers() {
	s.closeOnce.Do(func() {
		s.mu.Lock()
		ids := []string{}
		for id := range s.renderers {
			ids = append(ids, id)
		}
		s.mu.Unlock()
		rows := []map[string]any{}
		for _, id := range ids {
			body, _ := json.Marshal(map[string]string{"ID": id})
			response, e := s.Request(ProxyRequest{Path: "/viewer/close", Method: "POST", Body: base64.StdEncoding.EncodeToString(body), Headers: map[string]string{"Content-Type": "application/json"}})
			row := map[string]any{"delivery_id": id, "status": response["status"]}
			if e != nil {
				row["error"] = e.Error()
			}
			rows = append(rows, row)
		}
		receipt, _ := json.Marshal(map[string]any{"pid": os.Getpid(), "event": "actual Wails Common.WindowClosing hook", "at": time.Now().UTC(), "rows": rows})
		os.WriteFile(filepath.Join(s.state, fmt.Sprintf("native-close-%d.json", os.Getpid())), receipt, 0600)
	})
}

func (s *BoundaryFixture) Download() (map[string]any, error) {
	started := time.Now()
	issued, e := s.Request(ProxyRequest{Path: "/viewer/download-issue", Method: "POST", Body: base64.StdEncoding.EncodeToString([]byte(`{"Version":"a","Slow":true}`)), Headers: map[string]string{"Content-Type": "application/json"}})
	if e != nil {
		return nil, e
	}
	var d struct{ ID, Credential string }
	if e = json.Unmarshal([]byte(issued["body"].(string)), &d); e != nil {
		return nil, e
	}
	request, _ := http.NewRequest("GET", s.base+"/viewer/download?id="+d.ID, nil)
	request.Header.Set("Authorization", "Delivery "+d.Credential)
	response, e := http.DefaultClient.Do(request)
	if e != nil {
		return nil, e
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		return nil, fmt.Errorf("download HTTP %d", response.StatusCode)
	}
	dest := filepath.Join(s.state, "native-download-"+time.Now().Format("150405.000000")+".bin")
	file, e := os.OpenFile(dest, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if e != nil {
		return nil, e
	}
	hash := sha256.New()
	timed := &firstByteWriter{Writer: io.MultiWriter(file, hash)}
	n, e := io.Copy(timed, response.Body)
	file.Close()
	if e != nil {
		return nil, e
	}
	return map[string]any{"bytes": n, "sha256": hex.EncodeToString(hash.Sum(nil)), "streamed_to_owned_destination": true, "path": dest, "elapsed_seconds": time.Since(started).Seconds(), "first_byte_unix_ms": timed.first.UnixMilli(), "finished_unix_ms": time.Now().UnixMilli()}, nil
}

type firstByteWriter struct {
	io.Writer
	first time.Time
}

func (w *firstByteWriter) Write(b []byte) (int, error) {
	if w.first.IsZero() {
		w.first = time.Now()
	}
	return w.Writer.Write(b)
}
func main() {
	base := flag.String("base", "http://127.0.0.1:9270", "Exact trusted loopback origin")
	state := flag.String("state", "", "Owned private output directory")
	closeAfter := flag.Duration("close-after", 0, "Automatic real window.Close")
	lifecycle := flag.String("lifecycle", "", "Owned lifecycle check: write or read")
	focus := flag.Bool("focus", false, "Owned focused context/UI regression without full checks")
	recovery := flag.Bool("recovery-only", false, "Only current review route and renderer-operation checks")
	pathname := flag.Bool("pathname-only", false, "Only frozen pathname transition and direct regression checks")
	focusUI := flag.Bool("focus-ui-only", false, "Only affected manual recovery UI checks")
	flag.Parse()
	u, e := url.Parse(*base)
	info, se := os.Stat(*state)
	if e != nil || u.Scheme != "http" || u.Hostname() != "127.0.0.1" || u.Port() == "" || u.Path != "" || se != nil || !info.IsDir() || info.Mode().Perm() != 0700 {
		log.Fatal("exact loopback origin and private state required")
	}
	token := ""
	tokenPath := filepath.Join(*state, "native-session")
	if saved, readErr := os.ReadFile(tokenPath); readErr == nil {
		request, _ := http.NewRequest("GET", *base+"/session", nil)
		request.Header.Set("Authorization", "Bearer "+strings.TrimSpace(string(saved)))
		response, requestErr := http.DefaultClient.Do(request)
		if requestErr == nil {
			response.Body.Close()
			if response.StatusCode == 200 {
				token = strings.TrimSpace(string(saved))
			}
		}
	}
	if token == "" {
		token, e = pair(*base)
		if e == nil {
			e = os.WriteFile(tokenPath, []byte(token), 0600)
		}
	}
	if e != nil {
		log.Fatal(e)
	}
	service := &BoundaryFixture{base: *base, token: token, state: *state}
	transport := &fixtureTransport{}
	application.RegisterBindingMethodID((*BoundaryFixture).Probe, 701004)
	application.RegisterBindingMethodID((*BoundaryFixture).Request, 701005)
	application.RegisterBindingMethodID((*BoundaryFixture).Download, 701006)
	application.RegisterBindingMethodID((*BoundaryFixture).Wait, 701007)
	var app *application.App
	app = application.New(application.Options{Name: "Haeryong viewer proof", Transport: transport, Services: []application.Service{application.NewService(service)}, Mac: application.MacOptions{ApplicationShouldTerminateAfterLastWindowClosed: true}, Assets: application.AssetOptions{Middleware: func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "all native scheme endpoints are disabled for this HTTP/custom-transport construction", 403)
			service.schemeDenied.Add(1)
		})
	}}, RawMessageHandler: func(window application.Window, message string, origin *application.OriginInfo) {
		if origin == nil || !origin.IsMainFrame {
			return
		}
		sender, e := url.Parse(origin.Origin)
		if e != nil || sender.Scheme != u.Scheme || sender.Host != u.Host {
			return
		}
		var envelope struct {
			Kind, ID       string
			Object, Method int
			Args           json.RawMessage
		}
		if json.Unmarshal([]byte(message), &envelope) != nil || envelope.Kind != "fixture.runtime" {
			return
		}
		var request application.RuntimeRequest
		if json.Unmarshal([]byte(message), &request) != nil {
			return
		}
		request.WebviewWindowID = uint32(window.ID())
		response, e := transport.processor.HandleRuntimeCallWithIDs(context.Background(), &request)
		value := map[string]any{"id": envelope.ID, "value": response}
		if e != nil {
			value["error"] = e.Error()
		}
		encoded, _ := json.Marshal(value)
		application.InvokeSync(func() { evalNative(window, "window.fixtureNativeReply("+string(encoded)+")") })
	}})
	target := *base + "/?native=1&autorun=1"
	if *focus {
		target = *base + "/?native=1&focus=1"
		if *focusUI {
			target += "&focus_ui_only=1"
		}
	}
	if *lifecycle != "" {
		target = *base + "/?native=1&lifecycle=" + url.QueryEscape(*lifecycle)
	}
	if *recovery {
		target = *base + "/?native=1&recovery_focus=1&ir2_focus=1&version=a&page=pages%2Fnested%2Fdetail.html&page_query=loading%3Dexact%2520selection&page_fragment=step-2"
	}
	if *pathname {
		target = *base + "/?native=1&pathname_focus=1"
	}

	options := application.WebviewWindowOptions{Title: "Haeryong viewer proof", Width: 1100, Height: 850, URL: target}
	options.Mac.WebviewPreferences.JavaScriptCanOpenWindowsAutomatically.Set(true)
	window := app.Window.NewWithOptions(options)
	window.RegisterHook(events.Common.WindowClosing, func(_ *application.WindowEvent) { service.closeRenderers() })
	window.OnWindowEvent(events.Mac.WebViewDidFinishNavigation, func(_ *application.WindowEvent) {
		application.InvokeSync(func() {
			if !installGuard(window, *base) {
				log.Print("native guard installation failed")
				window.Close()
				return
			}
			evalNative(window, nativeBootstrap)
			stats, _ := json.Marshal(guardStats())
			statsMap := guardStats()
			statsMap["pid"] = os.Getpid()
			stats, _ = json.Marshal(statsMap)
			os.WriteFile(filepath.Join(*state, "native-guard.json"), stats, 0600)
		})
	})
	if *closeAfter > 0 {
		go func() { time.Sleep(*closeAfter); window.Close(); time.Sleep(time.Second); app.Quit() }()
	}
	if e = app.Run(); e != nil {
		log.Fatal(e)
	}
}

const nativeBootstrap = `(()=>{if(window.fixtureNativeCall)return;let calls=new Map();window.fixtureNativeReply=m=>{let p=calls.get(m.id);if(p){calls.delete(m.id);m.error?p.reject(new Error(m.error)):p.resolve(m.value);}};window.fixtureNativeCall=(methodID,args=[])=>new Promise((resolve,reject)=>{let id=crypto.randomUUID();calls.set(id,{resolve,reject});window.webkit.messageHandlers.external.postMessage(JSON.stringify({kind:'fixture.runtime',id,object:0,method:0,args:{'call-id':id,methodID,args}}));});window.fixtureNativeRequest=async(path,method,body,headers={})=>{let bytes=body instanceof ArrayBuffer?new Uint8Array(body):new TextEncoder().encode(body===undefined?'':typeof body==='string'?body:JSON.stringify(body));let raw='';for(const b of bytes)raw+=String.fromCharCode(b);let result=await window.fixtureNativeCall(701005,[{Path:path,Method:method,Body:btoa(raw),Headers:headers}]);return {ok:result.status>=200&&result.status<300,status:result.status,json:async()=>JSON.parse(result.body)};};window.webkit.messageHandlers.external.postMessage('wails:runtime:ready');})();`

func pair(base string) (string, error) {
	post := func(path string, value any) (map[string]any, error) {
		b, _ := json.Marshal(value)
		request, _ := http.NewRequest("POST", base+path, bytes.NewReader(b))
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Origin", base)
		response, e := http.DefaultClient.Do(request)
		if e != nil {
			return nil, e
		}
		defer response.Body.Close()
		if response.StatusCode >= 300 {
			return nil, fmt.Errorf("native pairing HTTP %d", response.StatusCode)
		}
		var out map[string]any
		e = json.NewDecoder(response.Body).Decode(&out)
		return out, e
	}
	request, e := post("/pair", map[string]string{"Label": "Viewer native exact worker"})
	if e != nil {
		return "", e
	}
	log.Print("Native exact fixture pairing awaits local agent approval; secrets remain in memory")
	for i := 0; i < 140; i++ {
		time.Sleep(2100 * time.Millisecond)
		poll, e := post("/pair/poll", map[string]any{"ID": request["ID"], "Secret": request["Secret"]})
		if e != nil {
			return "", e
		}
		if poll["decision"] == "approved" {
			redeemed, e := post("/pair/redeem", map[string]any{"ID": request["ID"], "Secret": request["Secret"]})
			if e != nil {
				return "", e
			}
			return redeemed["token"].(string), nil
		}
	}
	return "", errors.New("native exact pairing expired")
}
