package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
	facade "haeryong.local/haeryong/desktop/readerfacade"
)

const nativeBootstrapReader = `(()=>{if(window.haeryongNativeCall)return;let calls=new Map();window.haeryongNativeReply=m=>{let p=calls.get(m.id);if(p){calls.delete(m.id);m.error?p.reject(new Error(m.error)):p.resolve(m.value);}};window.haeryongNativeCall=(methodID,args=[])=>new Promise((resolve,reject)=>{let id=crypto.randomUUID();calls.set(id,{resolve,reject});window.webkit.messageHandlers.external.postMessage(JSON.stringify({kind:'haeyong.native',id,object:0,method:0,args:{'call-id':id,methodID,args}}));});window.dispatchEvent(new Event('haeryong-native-bridge'));})();`

func readerCredential(state string) (string, error) {
	path := filepath.Join(state, "native-session")
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", errors.New("no prepaired native credential in state; ReaderMode performs no pairing")
	}
	token := strings.TrimSpace(string(raw))
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0600 {
		return "", errors.New("native credential file must stay mode 0600")
	}
	if token == "" {
		return "", errors.New("empty native credential file")
	}
	return token, nil
}

func runReader(base, state, dist, port string, appPath string, selfcheck bool, probePath string, closeAfter time.Duration) error {
	token, err := readerCredential(state)
	if err != nil {
		return err
	}
	// Test-owned probe instrumentation: enabled ONLY when the driver passes an
	// explicit probe script, injected into the ACTUAL compiled app after the
	// guard and bridge bootstrap. It grants the imported child nothing.
	var probeScript string
	if probePath != "" {
		raw, err := os.ReadFile(probePath)
		if err != nil {
			return fmt.Errorf("reader probe script unreadable: %w", err)
		}
		probeScript = string(raw)
	}
	address := "127.0.0.1:" + port
	listener, err := net.Listen("tcp", address)
	if err != nil {
		// No silent fallback: the fixed static origin must be exactly this.
		return fmt.Errorf("fixed native static origin %s unavailable: %w", address, err)
	}
	service := facade.NewReaderService(base, state, token)
	origin := "http://" + address
	originURL, err := url.Parse(origin)
	if err != nil {
		return err
	}
	withSelfcheck := selfcheck
	schemeDenied := &atomic.Int64{}
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		if withSelfcheck && r.URL.Path == "/-selfcheck.html" {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			io.WriteString(w, readerSelfcheckPage)
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
			http.ServeFile(w, r, entry)
			return
		}
		http.ServeFile(w, r, candidate)
	})}
	go func() {
		if err := server.Serve(listener); err != nil && err != http.ErrServerClosed {
			log.Printf("native static origin ended: %v", err)
		}
	}()
	defer server.Close()

	transport := &fixtureTransport{}
	application.RegisterBindingMethodID((*facade.ReaderService).Invoke, 701100)
	allowReport := selfcheck || probeScript != ""
	if allowReport {
		application.RegisterBindingMethodID((*facade.ReaderService).SelfcheckReport, 701101)
	}
	var app *application.App
	// The pinned SDK runs each raw message handler and window event in its
	// own goroutine (application.go:705/717), so the guard-ready publication
	// must be synchronized, not a plain bool.
	var guardReady atomic.Bool
	app = application.New(application.Options{
		Name: "Haeryong Reader native", Transport: transport,
		Services: []application.Service{application.NewService(service)},
		Mac:      application.MacOptions{ApplicationShouldTerminateAfterLastWindowClosed: true},
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
				// ReaderMode rejects the legacy generic fixture kind.
				return
			}
			if envelope.Object != 0 || envelope.Method != 0 {
				return
			}
			// Closed method whitelist: only the typed ReaderService invoke (and
			// the selfcheck report in the explicit selfcheck mode) dispatch.
			// The pinned SDK gives a nonempty args.methodName precedence over
			// methodID, so a name must never accompany an allowed ID.
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
			value := map[string]any{"id": envelope.ID, "value": response}
			if err != nil {
				value["error"] = err.Error()
			}
			encoded, _ := json.Marshal(value)
			application.InvokeSync(func() { facade.EvalNative(window, "window.haeryongNativeReply("+string(encoded)+")") })
		},
	})
	target := origin + appPath
	if selfcheck {
		// The harness selfcheck page receives its parameters through the
		// query carried by --reader-path.
		target = origin + "/-selfcheck.html" + appPath
	}
	options := application.WebviewWindowOptions{Title: "Haeryong Reader native", Width: 1180, Height: 880, URL: target}
	options.Mac.WebviewPreferences.JavaScriptCanOpenWindowsAutomatically.Set(true)
	window := app.Window.NewWithOptions(options)
	window.RegisterHook(events.Common.WindowClosing, func(_ *application.WindowEvent) {
		service.CloseOwned()
		// Final guard statistics at close time: imported-frame denials and
		// scheme rejections accumulate after the navigation-time snapshot.
		application.InvokeSync(func() {
			stats := facade.GuardStats()
			stats["pid"] = os.Getpid()
			stats["scheme_denied"] = schemeDenied.Load()
			encoded, _ := json.Marshal(stats)
			os.WriteFile(filepath.Join(state, "native-guard.json"), encoded, 0600)
		})
	})
	window.OnWindowEvent(events.Mac.WebViewDidFinishNavigation, func(_ *application.WindowEvent) {
		application.InvokeSync(func() {
			if !facade.InstallGuard(window, origin) {
				log.Print("native guard installation failed")
				window.Close()
				return
			}
			// The facade exists only after the guard: the bridge bootstrap is
			// injected here, once per navigation, without replacing the guard.
			guardReady.Store(true)
			facade.EvalNative(window, nativeBootstrapReader)
			stats := facade.GuardStats()
			stats["pid"] = os.Getpid()
			stats["scheme_denied"] = schemeDenied.Load()
			encoded, _ := json.Marshal(stats)
			os.WriteFile(filepath.Join(state, "native-guard.json"), encoded, 0600)
			// Test-owned instrumentation over the ACTUAL compiled app: the
			// probe runs only after the guard and bridge exist and adds no
			// privilege for imported content.
			if probeScript != "" {
				facade.EvalNative(window, "(async()=>{"+probeScript+"})()")
			}
		})
	})
	if closeAfter > 0 {
		go func() { time.Sleep(closeAfter); window.Close(); time.Sleep(time.Second); app.Quit() }()
	}
	return app.Run()
}

const readerSelfcheckPage = `<!doctype html><meta charset=utf-8><title>Haeryong native selfcheck</title><body style="font-family:system-ui;padding:24px"><h1>Haeryong Reader native selfcheck</h1><div id=out style="white-space:pre-wrap"></div>
<script>
window.__pageError=null;
window.onerror=(m,s,l,c)=>{window.__pageError=String(m)+"@"+l+":"+c;};
window.addEventListener('unhandledrejection',e=>{window.__pageError=String(e.reason&&(e.reason.stack||e.reason.message||e.reason));});
window.__progress=[];
window.__done=false;
const params=new URLSearchParams(location.search);
const version=params.get("version")||"";
const call=(op,payload={})=>window.haeryongNativeCall(701100,[{Operation:op,Payload:payload}]);
const rows=[];const log=(name,ok,detail={})=>{rows.push({name,status:ok?"pass":"fail",detail});document.getElementById("out").textContent+=name+": "+(ok?"pass":"FAIL "+JSON.stringify(detail))+"\\n";};
const step=(name)=>{window.__progress.push(name);};
const report=async()=>{try{const r=await window.haeryongNativeCall(701101,[{Rows:[...rows,{name:"page.progress",status:"pass",detail:{steps:window.__progress}},{name:"page.error",status:window.__pageError?"fail":"pass",detail:{error:window.__pageError}},{name:"page.done",status:window.__done?"pass":"pending",detail:{}}]}]);return !!(r&&r.written===true);}catch(e){return false;}};
setInterval(()=>{void report();},2000);
const b64=(bytes)=>{let binary="";for(let i=0;i<bytes.length;i+=0x8000)binary+=String.fromCharCode(...bytes.subarray(i,i+0x8000));return btoa(binary);};
const hex=(buffer)=>[...new Uint8Array(buffer)].map(b=>b.toString(16).padStart(2,"0")).join("");
const noReply=(envelope)=>new Promise((resolve)=>{
  // Only wrap the REAL installed reply handler; never restore an undefined
  // capture from before the bridge bootstrap.
  const original=window.haeryongNativeReply;
  if(typeof original!=="function"){resolve(false);return;}
  let done=false;
  const restore=()=>{window.haeryongNativeReply=original;};
  const timer=setTimeout(()=>{if(!done){done=true;restore();resolve(true);}},700);
  window.haeryongNativeReply=(m)=>{if(!done&&m.id===envelope.id){done=true;clearTimeout(timer);restore();resolve(false);return;}original(m);};
  window.webkit.messageHandlers.external.postMessage(JSON.stringify(envelope));
});
(async()=>{try{
  step("start");
  // The Go harness injects the bridge after the guard on navigation finish;
  // wait for the actual callable bridge before any wrapper runs.
  const bridgeReady=await new Promise((resolve)=>{const deadline=Date.now()+15000;const check=()=>{if(typeof window.haeryongNativeCall==="function"&&typeof window.haeryongNativeReply==="function")resolve(true);else if(Date.now()>deadline)resolve(false);else setTimeout(check,100);};check();});
  log("bridge.ready",bridgeReady,{});
  if(!bridgeReady)throw Error("native bridge was never injected");
  const legacyIgnored=await noReply({kind:"fixture.runtime",id:"legacy-1",object:0,method:0,args:{"call-id":"legacy-1",methodID:701005,args:[]}});
  log("dispatch.legacy-kind-ignored",legacyIgnored,{});
  step("legacy-probe");
  const namedIgnored=await noReply({kind:"haeyong.native",id:"named-1",object:0,method:0,args:{"call-id":"named-1",methodID:701100,methodName:"Probe",args:[]}});
  log("dispatch.methodname-shadow-ignored",namedIgnored,{});
  step("named-probe");
  const attach=await call("attachSession");
  step("attach-replied");
  log("attach.verified",attach.ok&&typeof attach.value.attachment_id==="string"&&attach.value.public_origin===params.get("base"),attach.error||{origin:attach.value&&attach.value.public_origin});
  const caps=await call("capabilities");step("caps-replied");log("capabilities.read",caps.ok,caps.error||{});
  const current=await call("currentSession");step("session-replied");log("session.read",current.ok&&current.value.library_id===attach.value.session.library_id,{});
  const pairing=await call("pair",{device_label:"native"});step("pair-replied");log("pairing.feature_not_ready",!pairing.ok&&pairing.error.status===501,pairing.error||{});
  const projects=await call("listProjects");step("projects-replied");log("projects.read",projects.ok&&!!projects.value&&Array.isArray(projects.value.items)&&projects.value.items.length>0,projects.error||{});
  if(projects.ok&&projects.value&&Array.isArray(projects.value.items)&&projects.value.items.length){
    const projectId=projects.value.items[0].project_id;
    const created=await call("createUpload",{request:{purpose:"result",input_mode:"file",selection_name:"native-selfcheck.html",target:{project_id:projectId,document_id:null}},operation_id:self.crypto.randomUUID()});
    step("upload-create-replied");
    log("upload.create",created.ok&&!!created.value.upload_id,created.error||{});
    if(created.ok){
      const bytes=new TextEncoder().encode("<!doctype html><meta charset=utf-8><title>native upload</title><h1>native typed upload</h1>");
      const digest=hex(await crypto.subtle.digest("SHA-256",bytes));
      const entry=await call("putUploadEntry",{upload_id:created.value.upload_id,chunk:{path:"_input",kind:"file",offset:0,bytes_b64:b64(bytes),chunkSha256:digest,totalLength:bytes.length,final:true}});
      step("entry-replied");
      log("upload.entry-final",entry.ok&&entry.value.accepted_offset===String(bytes.length),entry.error||entry.value);
      const detail=await call("uploadDetail",{upload_id:created.value.upload_id});
      step("detail-replied");
      if(detail.ok&&detail.value.state==="open"&&detail.value.checkpoint){
        const checkpoint=detail.value.checkpoint;
        const completed=await call("completeUpload",{upload_id:created.value.upload_id,checkpoint:{generation:checkpoint.generation+1,inventory_sha256:checkpoint.inventory_sha256,accepted_bytes:checkpoint.accepted_bytes,complete_input:true},operation_id:self.crypto.randomUUID()});
        step("complete-replied");
        log("upload.complete",completed.ok,completed.error||completed.value);
        // Sealing is asynchronous: poll the real upload state like the
        // browser transfer before dispatching the publish.
        let sealed=false;
        for(let i=0;i<40;i++){
          const poll=await call("uploadDetail",{upload_id:created.value.upload_id});
          if(poll.ok&&poll.value.state==="sealed"){sealed=true;break;}
          if(!poll.ok||poll.value.state==="open"||poll.value.state==="abandoned")break;
          await new Promise(r=>setTimeout(r,500));
        }
        log("upload.sealed",sealed,{});
        if(sealed){
          const published=await call("importPublish",{request:{upload_id:created.value.upload_id,document_id:null},operation_id:self.crypto.randomUUID()});
          step("publish-replied");
          log("import.publish-dispatched",published.ok,published.error||published.value);
        }
      }else{
        log("upload.detail-open",false,detail.error||detail.value);
      }
    }
  }
  const unknownStream=await call("streamRead",{stream_id:"not-owned",max_bytes:1024});log("stream.foreign-typed-error",!unknownStream.ok&&unknownStream.error.status===404,unknownStream.error||{});
  if(version){
    const opened=await call("fileStream",{version_id:version,path:params.get("file")||"attachment.bin"});
    log("stream.opened",opened.ok&&!!opened.value.stream_id,opened.error||{});
    if(opened.ok){
      const expected=Uint8Array.from({length:256*513},(_,i)=>i%256);
      let total=0,bytesOk=true,sawNonemptyEof=false,sawEmptyEof=false;
      for(;;){
        const part=await call("streamRead",{stream_id:opened.value.stream_id,max_bytes:65536});
        if(!part.ok){log("stream.read-typed-error",false,part.error);break;}
        const v=part.value;
        if(v.data_b64!==null){
          const bytes=Uint8Array.from(atob(v.data_b64),c=>c.charCodeAt(0));
          if(bytes.length>65536){bytesOk=false;break;}
          let same=total+bytes.length<=expected.length;
          for(let i=0;same&&i<bytes.length;i++)same=bytes[i]===expected[total+i];
          if(!same){bytesOk=false;break;}
          total+=bytes.length;
          if(v.eof)sawNonemptyEof=bytes.length>0;
        }else if(v.eof)sawEmptyEof=true;
        if(v.eof)break;
      }
      log("stream.allbytes-exact-eof",bytesOk&&total===expected.length&&(sawNonemptyEof||sawEmptyEof),{total});
    }
    const before=await call("attachSession");
    // A FRESH owned stream of the old era, still in flight.
    const eraStream=await call("fileStream",{version_id:version,path:params.get("file")||"attachment.bin"});
    // A genuinely NEW attachment era WITHOUT disposal must invalidate the old
    // era's owned stream with the typed 409.
    const newer=await call("attachSession");
    const stale=await call("streamRead",{stream_id:(eraStream.ok?eraStream.value.stream_id:""),max_bytes:1024});
    log("era.attach-replacement-409",newer.ok&&eraStream.ok&&!stale.ok&&stale.error.status===409,stale.error||{});
    log("era.reattach-new-attachment",newer.ok&&newer.value.attachment_id!==before.value.attachment_id,{});
    // Disposal physically closes every owned stream of that attachment and
    // leaves no attachment: any later operation of that context fails closed
    // with the typed 401, never a clean completion.
    const ownedStream=await call("fileStream",{version_id:version,path:params.get("file")||"attachment.bin"});
    const disposed=await call("disposeSession",{attachment_id:newer.value.attachment_id});
    const dropped=await call("streamRead",{stream_id:(ownedStream.ok?ownedStream.value.stream_id:""),max_bytes:1024});
    log("era.post-dispose-fails-closed",disposed.ok&&ownedStream.ok&&!dropped.ok&&dropped.error.status===401,dropped.error||{});
    await call("attachSession");
  }
}catch(error){log("selfcheck.exception",false,String(error));}
window.__done=true;
let reported=false;
for(let i=0;i<3&&!reported;i++)reported=await report();
log("report.written",reported,{});
await report();
})();</script>`
