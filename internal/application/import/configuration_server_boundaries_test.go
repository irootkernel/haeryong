package importer_test

import (
	"bytes"
	"encoding/json"
	"errors"
	httpauth "haeryong.local/haeryong/internal/adapters/http/auth"
	httpconfig "haeryong.local/haeryong/internal/adapters/http/configuration"
	config "haeryong.local/haeryong/internal/application/configuration"
	"haeryong.local/haeryong/internal/domain"
	sec "haeryong.local/haeryong/internal/security/session"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"
)

type configurationPathsBarrier struct{ entered, release chan struct{} }

func (p configurationPathsBarrier) Canonical(path string, _ bool) (string, error) {
	close(p.entered)
	<-p.release
	return path, nil
}
func TestConfigurationPlanCannotUseFutureRevisionToApproveStaleSnapshot(t *testing.T) {
	f := newFixture(t)
	baseCore := config.New(f.store, f.auth, nil)
	baseline, e := baseCore.Read(ctx, f.token)
	if e != nil {
		t.Fatal(e)
	}
	barrier := configurationPathsBarrier{make(chan struct{}), make(chan struct{})}
	staleCore := config.New(f.store, f.auth, barrier)
	type outcome struct {
		response domain.Response
		err      error
	}
	done := make(chan outcome, 1)
	request := config.PlanRequest{Scope: config.Scope{Kind: "library"}, ExpectedRevision: baseline.ConfigRevision + 1, Changes: map[string]any{"projects": map[string]any{f.project: map[string]any{"output_destination": "/labelled-output"}}}}
	go func() { r, e := staleCore.Plan(ctx, f.token, key(t), request); done <- outcome{r, e} }()
	var early *outcome
	select {
	case <-barrier.entered:
	case result := <-done:
		early = &result
	case <-time.After(5 * time.Second):
		t.Fatal("plan did not reach validation/revision boundary")
	}
	other, e := baseCore.Plan(ctx, f.token, key(t), config.PlanRequest{Scope: config.Scope{Kind: "library"}, ExpectedRevision: baseline.ConfigRevision, Changes: map[string]any{"security": map[string]any{"remember_device_default": false}}})
	if e != nil {
		t.Fatal(e)
	}
	var plan domain.ConfigurationPlan
	if e = json.Unmarshal(other.Body, &plan); e != nil {
		t.Fatal(e)
	}
	if _, e = baseCore.Commit(ctx, f.token, plan.ID, "", config.CommitRequest{ExpectedRevision: baseline.ConfigRevision, Confirmed: true}); e != nil {
		t.Fatal(e)
	}
	close(barrier.release)
	var result outcome
	if early != nil {
		result = *early
	} else {
		select {
		case result = <-done:
		case <-time.After(5 * time.Second):
			t.Fatal("plan validation did not finish")
		}
	}
	if result.err == nil {
		var stale domain.ConfigurationPlan
		if e = json.Unmarshal(result.response.Body, &stale); e != nil {
			t.Fatal(e)
		}
		_, e = staleCore.Commit(ctx, f.token, stale.ID, "", config.CommitRequest{ExpectedRevision: baseline.ConfigRevision + 1, Confirmed: true})
		actual, readError := baseCore.Read(ctx, f.token)
		t.Fatalf("future expected revision approved stale effective snapshot: commit_error=%v current_read_error=%v unrelated_change_retained=%v", e, readError, bytes.Contains(actual.Value, []byte(`"remember_device_default":false`)))
	}
	if !errors.Is(result.err, domain.ErrPrecondition) {
		t.Fatal("unexpected stale snapshot rejection", result.err)
	}
	current, e := baseCore.Read(ctx, f.token)
	if e != nil || current.ConfigRevision != baseline.ConfigRevision+1 || !bytes.Contains(current.Value, []byte(`"remember_device_default":false`)) {
		t.Fatal("competing configuration was changed", e)
	}
}

func TestActualHTTPConfigurationEmptyOptionalKeyIsInvalidAndAbsentWorks(t *testing.T) {
	f := newFixture(t)
	core := config.New(f.store, f.auth, nil)
	server := httptest.NewUnstartedServer(nil)
	origin := "http://" + server.Listener.Addr().String()
	parsed, _ := url.Parse(origin)
	handler, e := httpauth.New(f.auth, httpauth.Policy{Hosts: []string{parsed.Host}, Origins: []string{origin}}, httpconfig.New(core))
	if e != nil {
		t.Fatal(e)
	}
	server.Config.Handler = handler
	server.Start()
	defer server.Close()
	send := func(path string, body any, empty bool) (int, json.RawMessage) {
		b, _ := json.Marshal(body)
		request, e := http.NewRequest("POST", origin+path, bytes.NewReader(b))
		if e != nil {
			t.Fatal(e)
		}
		request.Header.Set("Authorization", "Bearer "+f.token)
		request.Header.Set("Content-Type", "application/json")
		if empty {
			request.Header["Idempotency-Key"] = []string{""}
		}
		response, e := http.DefaultClient.Do(request)
		if e != nil {
			t.Fatal(e)
		}
		defer response.Body.Close()
		var value struct {
			Data json.RawMessage `json:"data"`
		}
		if e = json.NewDecoder(response.Body).Decode(&value); e != nil {
			t.Fatal(e)
		}
		return response.StatusCode, value.Data
	}
	view, e := core.Read(ctx, f.token)
	if e != nil {
		t.Fatal(e)
	}
	request := config.PlanRequest{Scope: config.Scope{Kind: "library"}, ExpectedRevision: view.ConfigRevision, Changes: map[string]any{"security": map[string]any{"remember_device_default": false}}}
	status, _ := send("/api/v1/configuration/plans", request, true)
	if status != 400 {
		t.Errorf("supplied empty plan key HTTP%d want400", status)
	}
	status, data := send("/api/v1/configuration/plans", request, false)
	if status != 200 {
		t.Fatal("absent optional plan key rejected", status)
	}
	var plan domain.ConfigurationPlan
	if e = json.Unmarshal(data, &plan); e != nil {
		t.Fatal(e)
	}
	commit := config.CommitRequest{ExpectedRevision: view.ConfigRevision, Confirmed: true}
	status, _ = send("/api/v1/configuration/plans/"+plan.ID+"/commit", commit, true)
	if status != 400 {
		t.Errorf("supplied empty commit key HTTP%d want400", status)
	}
	unchanged, e := core.Read(ctx, f.token)
	if e != nil || unchanged.ConfigRevision != view.ConfigRevision {
		t.Error("invalid empty key changed effective configuration", e)
	}
	status, _ = send("/api/v1/configuration/plans/"+plan.ID+"/commit", commit, false)
	if status != 200 {
		t.Fatal("absent optional natural commit rejected", status)
	}
	final, e := core.Read(ctx, f.token)
	if e != nil || final.ConfigRevision != view.ConfigRevision+1 {
		t.Fatal("natural commit did not apply once", e)
	}
}

func TestConfigurationExpiryAppliesAfterDurableCommitAndKeepsOriginalReceipt(t *testing.T) {
	f := newFixture(t)
	future := time.Now().UTC().Add(2 * time.Hour)
	f.auth.Now = func() time.Time { return future }
	core := config.New(f.store, f.auth, nil)
	core.Now = func() time.Time { return future }
	baseline, e := core.Read(ctx, f.token)
	if e != nil {
		t.Fatal(e)
	}
	response, e := core.Plan(ctx, f.token, key(t), config.PlanRequest{Scope: config.Scope{Kind: "library"}, ExpectedRevision: baseline.ConfigRevision, Changes: map[string]any{"security": map[string]any{"session_absolute_seconds": json.Number("3600")}}})
	if e != nil {
		t.Fatal(e)
	}
	var plan domain.ConfigurationPlan
	if e = json.Unmarshal(response.Body, &plan); e != nil {
		t.Fatal(e)
	}
	alias := key(t)
	commit := config.CommitRequest{ExpectedRevision: baseline.ConfigRevision, Confirmed: true}
	epoch, head, e := f.auth.EventHead(ctx, f.token)
	if e != nil {
		t.Fatal(e)
	}
	_, e = core.Commit(ctx, f.token, plan.ID, alias, commit)
	if !errors.Is(e, sec.ErrExpired) {
		t.Fatal("new expiry did not guard postcommit delivery", e)
	}
	current, e := f.store.Configuration(ctx)
	if e != nil || current.Revision != baseline.ConfigRevision+1 || !bytes.Contains(current.Value, []byte(`"session_absolute_seconds":3600`)) {
		t.Fatal("valid owner change rolled back under its newly selected expiry", e)
	}
	if _, e = f.auth.Authenticate(ctx, f.token); !errors.Is(e, sec.ErrExpired) {
		t.Fatal("old session still usable", e)
	}
	events, e := f.store.Events(ctx, epoch, head, 64)
	if e != nil || len(events) != 1 || events[0].Kind != "configuration.changed" || events[0].Revision != baseline.ConfigRevision+1 {
		t.Fatal("configuration outcome and atomic policy event diverged", events, e)
	}
	pair, e := f.auth.CreatePairing(ctx, "http://127.0.0.1:9270", "fresh owner after expiry", "fixture-fresh")
	if e != nil {
		t.Fatal(e)
	}
	if _, e = f.auth.BootstrapDecide(ctx, domain.PairingDecision{ID: pair.ID, Code: pair.Code, Origin: pair.Origin, ETag: pair.ETag, OperationID: key(t), Action: "approve"}); e != nil {
		t.Fatal(e)
	}
	fresh, e := f.auth.Poll(ctx, pair.ID, pair.PollingSecret, pair.Origin)
	if e != nil {
		t.Fatal(e)
	}
	replay, e := core.Commit(ctx, fresh.Token, plan.ID, "", commit)
	if e != nil || replay.Status != 200 {
		t.Fatal("new owner could not replay committed original plan", e)
	}
	receipt, e := f.auth.Operation(ctx, fresh.Token, alias)
	if e != nil || receipt.Response == nil || !bytes.Equal(receipt.Response.Body, replay.Body) {
		t.Fatal("original alias/natural outcome missing", e)
	}
	current, e = f.store.Configuration(ctx)
	if e != nil || current.Revision != baseline.ConfigRevision+1 {
		t.Fatal("replay committed twice", e)
	}
}
