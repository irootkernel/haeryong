package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"haeryong.local/haeryong/internal/adapters/client"
	"haeryong.local/haeryong/internal/adapters/foundation"
	"haeryong.local/haeryong/internal/adapters/localcontrol"
	config "haeryong.local/haeryong/internal/application/configuration"
	"haeryong.local/haeryong/internal/application/service"
	app "haeryong.local/haeryong/internal/application/storage"
	"haeryong.local/haeryong/internal/domain"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func seedListenerPolicy(t *testing.T, s service.Selection, port int) {
	t.Helper()
	writer, e := service.OpenWriter(s)
	if e != nil {
		t.Fatal(e)
	}
	defer writer.Close()
	if e = writer.PrepareStorageBootstrap(nil); e != nil {
		t.Fatal(e)
	}
	store := foundation.New(nil)
	_, closeStore, e := store.Prepare(context.Background(), writer.Profile)
	if e != nil {
		t.Fatal(e)
	}
	defer closeStore()
	current, e := store.Configuration(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	value, e := config.Decode(bytes.NewReader(current.Value), "json")
	if e != nil {
		t.Fatal(e)
	}
	network := value["network"].(map[string]any)
	network["listener"].(map[string]any)["port"] = json.Number(fmt.Sprint(port))
	network["allowed_origins"] = []any{fmt.Sprintf("http://127.0.0.1:%d", port)}
	if e = config.Validate(value); e != nil {
		t.Fatal(e)
	}
	if e = store.Write(context.Background(), func(tx app.WriteTx) error {
		return tx.SetConfiguration(domain.Configuration{Value: config.Canonical(value), Actor: "labelled disposable listener fixture"}, current.Revision)
	}); e != nil {
		t.Fatal(e)
	}
	if e = writer.MarkStorageReady(nil); e != nil {
		t.Fatal(e)
	}
}
func startConfiguredDaemon(t *testing.T, s service.Selection) *daemon {
	t.Helper()
	command := exec.Command(testBinary, append([]string{"daemon", "run"}, profileArgs(s)...)...)
	stderr := new(bytes.Buffer)
	command.Stderr = stderr
	stdout, e := command.StdoutPipe()
	if e != nil {
		t.Fatal(e)
	}
	if e = command.Start(); e != nil {
		t.Fatal(e)
	}
	d := &daemon{cmd: command, stderr: stderr, done: make(chan error, 1)}
	t.Cleanup(func() {
		command.Process.Kill()
		select {
		case <-d.done:
		case <-time.After(8 * time.Second):
			t.Error("configured daemon cleanup timed out")
		}
	})
	ready := make(chan []byte, 1)
	go func() {
		scanner := bufio.NewScanner(stdout)
		if scanner.Scan() {
			ready <- bytes.Clone(scanner.Bytes())
		} else {
			ready <- nil
		}
		io.Copy(io.Discard, stdout)
	}()
	go func() { d.done <- command.Wait() }()
	select {
	case data := <-ready:
		var value struct {
			Data service.Identity `json:"data"`
		}
		if e = json.Unmarshal(data, &value); e != nil {
			t.Fatal("configured startup failed", e, stderr)
		}
		d.id = value.Data
	case <-time.After(12 * time.Second):
		t.Fatal("configured daemon readiness timeout")
	}
	return d
}
func TestActualConfigurationRuntimeRestartAndProtectedCLIConnect(t *testing.T) {
	s := selection(t)
	profile := initProfile(t, s)
	oldPort, newPort := availablePort(t), availablePort(t)
	seedListenerPolicy(t, s, oldPort)
	daemon := startConfiguredDaemon(t, s)
	oldBase := fmt.Sprintf("http://127.0.0.1:%d", oldPort)
	if daemon.id.Endpoint != oldBase {
		t.Fatal("persisted listener ignored")
	}
	root := filepath.Join(t.TempDir(), "normal-client")
	flags := []string{"--profile", s.Profile, "--client-dir", root, "--endpoint", oldBase, "--json"}
	created := cli(t, 0, append([]string{"auth", "connect"}, flags...)...)
	var request struct {
		Data struct {
			Connected bool   `json:"connected"`
			PairingID string `json:"pairing_id"`
			Code      string `json:"code"`
			ETag      string `json:"etag"`
			Origin    string `json:"origin"`
		}
	}
	if e := json.Unmarshal(created, &request); e != nil {
		t.Fatal(e)
	}
	if request.Data.Connected || request.Data.Code == "" {
		t.Fatal("connect silently approved or omitted comparison")
	}
	operation, _ := service.NewID()
	intent, _ := json.Marshal(map[string]string{"code": request.Data.Code, "origin": request.Data.Origin, "etag": request.Data.ETag, "operation_id": operation})
	approve := exec.Command(testBinary, append([]string{"auth", "approve", request.Data.PairingID}, profileArgs(s)...)...)
	approve.Stdin = bytes.NewReader(intent)
	if _, e := approve.Output(); e != nil {
		t.Fatal("explicit protected approval", e)
	}
	connected := cli(t, 0, append([]string{"auth", "connect"}, flags...)...)
	if bytes.Contains(connected, []byte(`"token"`)) {
		t.Fatal("connect exposed bearer")
	}
	credential, e := client.LoadCredential(filepath.Join(root, "credential.json"))
	if e != nil {
		t.Fatal(e)
	}
	if credential.LibraryID != profile.LibraryID || credential.Profile != s.Profile {
		t.Fatal("connection binding wrong")
	}
	api, e := client.New(oldBase, s.Profile, credential)
	if e != nil {
		t.Fatal(e)
	}
	var initial config.View
	if _, e = api.JSON(context.Background(), "GET", "/api/v1/configuration", "", nil, &initial); e != nil {
		t.Fatal(e)
	}
	policyFile := filepath.Join(t.TempDir(), "security.yaml")
	if e = os.WriteFile(policyFile, []byte("security:\n  remember_device_default: false\n  session_idle_seconds: 30\n  pairing_expiry_seconds: 120\n"), 0600); e != nil {
		t.Fatal(e)
	}
	planned := cli(t, 0, append([]string{"config", "plan", policyFile}, flags...)...)
	var plan struct {
		Data struct {
			Result domain.ConfigurationPlan `json:"result"`
		} `json:"data"`
	}
	if e = json.Unmarshal(planned, &plan); e != nil {
		t.Fatal(e)
	}
	cli(t, 0, append([]string{"config", "apply", plan.Data.Result.ID, "--expected-config-revision", fmt.Sprint(plan.Data.Result.BaseRevision), "--confirmed"}, flags...)...)
	status, data := authRequest(t, oldBase, "GET", "/", "", nil)
	_ = data
	if status != 200 {
		t.Fatal(status)
	}
	httpResponse, e := http.Get(oldBase + "/")
	if e != nil {
		t.Fatal(e)
	}
	html, e := io.ReadAll(httpResponse.Body)
	httpResponse.Body.Close()
	if e != nil || bytes.Contains(html, []byte(`type="checkbox" checked`)) {
		t.Fatal("new browser default ignores effective policy")
	}
	var current config.View
	if _, e = api.JSON(context.Background(), "GET", "/api/v1/configuration", "", nil, &current); e != nil {
		t.Fatal(e)
	}
	networkChange := config.PlanRequest{Scope: config.Scope{Kind: "library"}, ExpectedRevision: current.ConfigRevision, Changes: map[string]any{"network": map[string]any{"listener": map[string]any{"port": json.Number(fmt.Sprint(newPort))}, "allowed_origins": []any{fmt.Sprintf("http://127.0.0.1:%d", newPort)}}}}
	var networkPlan domain.ConfigurationPlan
	if _, e = api.JSON(context.Background(), "POST", "/api/v1/configuration/plans", "", networkChange, &networkPlan); e != nil {
		t.Fatal(e)
	}
	_, commitError := api.JSON(context.Background(), "POST", "/api/v1/configuration/plans/"+networkPlan.ID+"/commit", "", config.CommitRequest{ExpectedRevision: networkPlan.BaseRevision, Confirmed: true}, nil)
	_ = commitError // the original result may be lost during controlled shutdown; natural ID reconciles it.
	deadline := time.Now().Add(15 * time.Second)
	var actual service.Identity
	for time.Now().Before(deadline) {
		actual, e = service.ReadDiscovery(profile)
		if e == nil && actual.Endpoint == fmt.Sprintf("http://127.0.0.1:%d", newPort) {
			if _, e = localcontrol.Verify(context.Background(), profile, actual); e == nil {
				break
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	if e != nil || actual.Endpoint == oldBase {
		t.Fatal("controlled restart did not apply listener", e)
	}
	if actual.LibraryID != daemon.id.LibraryID || actual.ProfileID != daemon.id.ProfileID || actual.InstanceID == daemon.id.InstanceID {
		t.Fatal("restart changed durable identity or reused runtime instance")
	}
	if _, e = client.New(actual.Endpoint, s.Profile, credential); e == nil {
		t.Fatal("listener change silently widened stored credential")
	}
	status, body := authRequest(t, actual.Endpoint, "GET", "/api/v1/operations/"+networkPlan.ID, credential.Token, nil)
	if status != 200 {
		t.Fatal("committed plan missing after restart", status)
	}
	var receipt struct {
		OriginalStatus int `json:"original_status"`
		Result         struct {
			ConfigRevision int64 `json:"config_revision"`
		} `json:"result_metadata"`
	}
	_ = json.Unmarshal(body, &receipt)
	if receipt.OriginalStatus != 200 || receipt.Result.ConfigRevision != networkPlan.BaseRevision+1 {
		t.Fatal("restart lost natural plan result")
	}
	if response, e := http.Get(oldBase + "/health/live"); e == nil {
		response.Body.Close()
		t.Fatal("old listener still active")
	}
	status, _ = authRequest(t, actual.Endpoint, "GET", "/api/v1/session", credential.Token, nil)
	if status != 200 {
		t.Fatal("normal restart invalidated owner session")
	}
	cli(t, 0, append([]string{"service", "stop"}, profileArgs(s)...)...)
	daemon.wait(t)
	// The explicit port override is an honest local recovery path, not random fallback.
	recovered := startDaemon(t, s, oldPort)
	if recovered.id.Endpoint != oldBase {
		t.Fatal("explicit listener recovery failed")
	}
	status, data = authRequest(t, oldBase, "GET", "/api/v1/status", credential.Token, nil)
	if status != 200 || !bytes.Contains(data, []byte(`"ephemeral_override":true`)) {
		t.Fatal("override not reported", status)
	}
}
