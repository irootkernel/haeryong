package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os/exec"
	"testing"
	"time"

	"haeryong.local/haeryong/internal/application/service"
	"haeryong.local/haeryong/internal/domain"
)

func authRequest(t *testing.T, base, method, path, token string, body []byte) (int, json.RawMessage) {
	t.Helper()
	request, e := http.NewRequest(method, base+path, bytes.NewReader(body))
	if e != nil {
		t.Fatal(e)
	}
	if body != nil {
		request.Header.Set("Origin", base)
		request.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	client := &http.Client{Timeout: 5 * time.Second}
	response, e := client.Do(request)
	if e != nil {
		t.Fatal(e)
	}
	defer response.Body.Close()
	var out struct {
		Data json.RawMessage `json:"data"`
	}
	_ = json.NewDecoder(response.Body).Decode(&out)
	return response.StatusCode, out.Data
}
func bootstrapOwnerCLI(t *testing.T, s service.Selection, base, label string) (domain.Session, string) {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"device_label": label})
	status, data := authRequest(t, base, "POST", "/api/v1/auth/pairings", "", body)
	if status != 201 {
		t.Fatalf("pair create HTTP %d", status)
	}
	var pairing domain.PairingCreated
	if json.Unmarshal(data, &pairing) != nil {
		t.Fatal("pairing response")
	}
	cli(t, 0, append([]string{"auth", "pairings"}, profileArgs(s)...)...)
	op, _ := service.NewID()
	intent, _ := json.Marshal(map[string]string{"code": pairing.Code, "origin": pairing.Origin, "etag": pairing.ETag, "operation_id": op})
	command := exec.Command(testBinary, append([]string{"auth", "approve", pairing.ID}, profileArgs(s)...)...)
	command.Stdin = bytes.NewReader(intent)
	if _, e := command.Output(); e != nil {
		t.Fatal("protected exact bootstrap failed", e)
	}
	body, _ = json.Marshal(map[string]string{"polling_secret": pairing.PollingSecret})
	status, data = authRequest(t, base, "POST", "/api/v1/auth/pairings/"+pairing.ID+"/poll", "", body)
	if status != 200 {
		t.Fatalf("single redemption HTTP %d", status)
	}
	var poll domain.PairingPoll
	if json.Unmarshal(data, &poll) != nil || poll.Session == nil || poll.Token == "" {
		t.Fatal("single credential missing")
	}
	return *poll.Session, poll.Token
}
func TestProductionBootstrapSSEStopRestartAndAllRevokedRecovery(t *testing.T) {
	s := selection(t)
	p := initProfile(t, s)
	port := availablePort(t)
	daemon := startDaemon(t, s, port)
	base := daemon.id.Endpoint
	for _, path := range []string{"/api/v1/session", "/api/v1/sessions", "/api/v1/operations/unknown", "/api/v1/jobs", "/api/v1/events", "/api/v1/versions/unknown/file"} {
		status, _ := authRequest(t, base, "GET", path, "", nil)
		if status != 401 {
			t.Fatalf("private %s HTTP %d", path, status)
		}
	}
	status, _ := authRequest(t, base, "GET", "/local/v1/pairings", "", nil)
	if status != 401 {
		t.Fatalf("unproved bootstrap HTTP %d", status)
	}
	owner, token := bootstrapOwnerCLI(t, s, base, "native-bootstrap-owner")
	streamContext, cancel := context.WithCancel(context.Background())
	defer cancel()
	request, e := http.NewRequestWithContext(streamContext, "GET", base+"/api/v1/events", nil)
	if e != nil {
		t.Fatal(e)
	}
	request.Header.Set("Authorization", "Bearer "+token)
	response, e := http.DefaultClient.Do(request)
	if e != nil {
		t.Fatal(e)
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		t.Fatal("stream refused")
	}
	closed := make(chan error, 1)
	go func() { _, e := io.Copy(io.Discard, response.Body); closed <- e }()
	cli(t, 0, append([]string{"service", "stop"}, profileArgs(s)...)...)
	daemon.wait(t)
	select {
	case <-closed:
	case <-time.After(2 * time.Second):
		t.Fatal("active stream outlived service stop")
	}
	writer, e := service.OpenWriter(s)
	if e != nil {
		t.Fatal("writer not released after handlers drained", e)
	}
	writer.Close()
	restarted := startDaemon(t, s, port)
	if restarted.id.LibraryID != p.LibraryID || restarted.id.ProfileID != p.ProfileID || restarted.id.InstanceID == daemon.id.InstanceID {
		t.Fatal("restart identity invalid")
	}
	status, data := authRequest(t, base, "GET", "/api/v1/session", token, nil)
	if status != 200 {
		t.Fatalf("remembered session restart HTTP %d", status)
	}
	var remembered domain.Session
	if json.Unmarshal(data, &remembered) != nil || remembered.ID != owner.ID || remembered.PrincipalID != owner.PrincipalID {
		t.Fatal("restart rotated device identity")
	}
	status, _ = authRequest(t, base, "DELETE", "/api/v1/session", token, nil)
	if status != 204 {
		t.Fatalf("natural logout HTTP %d", status)
	}
	status, _ = authRequest(t, base, "GET", "/api/v1/session", token, nil)
	if status != 401 {
		t.Fatal("revoked bearer accepted")
	}
	recovery, _ := bootstrapOwnerCLI(t, s, base, "recovery-owner")
	if recovery.PrincipalID != owner.PrincipalID {
		t.Fatal("all-revoked bootstrap changed owner")
	}
	cli(t, 0, append([]string{"service", "stop"}, profileArgs(s)...)...)
	restarted.wait(t)
}
