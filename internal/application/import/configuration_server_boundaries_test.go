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
	"strconv"

	"fmt"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"time"
)

type configurationPathsBarrier struct{ entered, release chan struct{} }

func (p configurationPathsBarrier) Canonical(path string, _ bool) (string, error) {
	close(p.entered)
	<-p.release
	return path, nil
}

var _ = It("TestConfigurationPlanCannotUseFutureRevisionToApproveStaleSnapshot", Label("persistence"), func() {
	t := GinkgoT()

	f := newFixture(t)
	baseCore := config.New(f.store, f.auth, nil)
	baseline, e := baseCore.Read(ctx, f.token)
	Expect(e != nil).To(BeFalse(), func() string { return fmt.Sprint(e) })

	barrier := configurationPathsBarrier{make(chan struct{}), make(chan struct{})}
	staleCore := config.New(f.store, f.auth, barrier)
	type outcome struct {
		response domain.Response
		err      error
	}
	done := make(chan outcome, 1)
	request := config.PlanRequest{Scope: config.Scope{Kind: "library"}, ExpectedRevision: baseline.ConfigRevision + 1, Changes: map[string]any{"projects": map[string]any{f.project: map[string]any{"output_destination": "/labelled-output"}}}}
	go func() {
		defer GinkgoRecover()
		r, e := staleCore.Plan(ctx, f.token, key(t), request)
		done <- outcome{r, e}
	}()
	var early *outcome
	select {
	case <-barrier.entered:
	case result := <-done:
		early = &result
	case <-time.After(5 * time.Second):
		Fail(fmt.Sprint("plan did not reach validation/revision boundary"))
	}
	other, e := baseCore.Plan(ctx, f.token, key(t), config.PlanRequest{Scope: config.Scope{Kind: "library"}, ExpectedRevision: baseline.ConfigRevision, Changes: map[string]any{"security": map[string]any{"remember_device_default": false}}})
	Expect(e != nil).To(BeFalse(), func() string { return fmt.Sprint(e) })

	var plan domain.ConfigurationPlan
	{
		e = json.Unmarshal(other.Body, &plan)
		Expect(e != nil).To(BeFalse(), func() string { return fmt.Sprint(e) })
	}
	{

		_, e = baseCore.Commit(ctx, f.token, plan.ID, "", config.CommitRequest{ExpectedRevision: baseline.ConfigRevision, Confirmed: true})
		Expect(e != nil).To(BeFalse(), func() string { return fmt.Sprint(e) })
	}

	close(barrier.release)
	var result outcome
	if early != nil {
		result = *early
	} else {
		select {
		case result = <-done:
		case <-time.After(5 * time.Second):
			Fail(fmt.Sprint("plan validation did not finish"))
		}
	}
	if result.err == nil {
		var stale domain.ConfigurationPlan
		{
			e = json.Unmarshal(result.response.Body, &stale)
			Expect(e != nil).To(BeFalse(), func() string { return fmt.Sprint(e) })
		}

		_, e = staleCore.Commit(ctx, f.token, stale.ID, "", config.CommitRequest{ExpectedRevision: baseline.ConfigRevision + 1, Confirmed: true})
		actual, readError := baseCore.Read(ctx, f.token)
		Fail(fmt.Sprintf("future expected revision approved stale effective snapshot: commit_error=%v current_read_error=%v unrelated_change_retained=%v", e, readError, bytes.Contains(actual.Value, []byte(`"remember_device_default":false`))))
	}
	Expect(!errors.Is(result.err, domain.ErrPrecondition)).To(BeFalse(), func() string { return fmt.Sprint("unexpected stale snapshot rejection", result.err) })

	current, e := baseCore.Read(ctx, f.token)
	Expect(e != nil || current.ConfigRevision != baseline.ConfigRevision+1 || !bytes.Contains(current.Value, []byte(`"remember_device_default":false`))).To(BeFalse(), func() string { return fmt.Sprint("competing configuration was changed", e) })

})
var _ = It("TestActualHTTPConfigurationEmptyOptionalKeyIsInvalidAndAbsentWorks", Label("persistence"), func() {
	t := GinkgoT()

	f := newFixture(t)
	core := config.New(f.store, f.auth, nil)
	server := httptest.NewUnstartedServer(nil)
	origin := "http://" + server.Listener.Addr().String()
	parsed, _ := url.Parse(origin)
	handler, e := httpauth.New(f.auth, httpauth.Policy{Hosts: []string{parsed.Host}, Origins: []string{origin}}, httpconfig.New(core))
	Expect(e != nil).To(BeFalse(), func() string { return fmt.Sprint(e) })

	server.Config.Handler = handler
	server.Start()
	defer server.Close()
	send := func(path string, body any, empty bool) (int, json.RawMessage) {
		b, _ := json.Marshal(body)
		request, e := http.NewRequest("POST", origin+path, bytes.NewReader(b))
		Expect(e != nil).To(BeFalse(), func() string { return fmt.Sprint(e) })

		request.Header.Set("Authorization", "Bearer "+f.token)
		request.Header.Set("Content-Type", "application/json")
		if empty {
			request.Header["Idempotency-Key"] = []string{""}
		}
		response, e := http.DefaultClient.Do(request)
		Expect(e != nil).To(BeFalse(), func() string { return fmt.Sprint(e) })

		defer response.Body.Close()
		var value struct {
			Data json.RawMessage `json:"data"`
		}
		{
			e = json.NewDecoder(response.Body).Decode(&value)
			Expect(e != nil).To(BeFalse(), func() string { return fmt.Sprint(e) })
		}

		return response.StatusCode, value.Data
	}
	view, e := core.Read(ctx, f.token)
	Expect(e != nil).To(BeFalse(), func() string { return fmt.Sprint(e) })

	request := config.PlanRequest{Scope: config.Scope{Kind: "library"}, ExpectedRevision: view.ConfigRevision, Changes: map[string]any{"security": map[string]any{"remember_device_default": false}}}
	status, _ := send("/api/v1/configuration/plans", request, true)
	Expect(status != 400).To(BeFalse(), func() string { return fmt.Sprintf("supplied empty plan key HTTP%d want400", status) })

	status, data := send("/api/v1/configuration/plans", request, false)
	Expect(status != 200).To(BeFalse(), func() string { return fmt.Sprint("absent optional plan key rejected", status) })

	var plan domain.ConfigurationPlan
	{
		e = json.Unmarshal(data, &plan)
		Expect(e != nil).To(BeFalse(), func() string { return fmt.Sprint(e) })
	}

	commit := config.CommitRequest{ExpectedRevision: view.ConfigRevision, Confirmed: true}
	status, _ = send("/api/v1/configuration/plans/"+plan.ID+"/commit", commit, true)
	Expect(status != 400).To(BeFalse(), func() string { return fmt.Sprintf("supplied empty commit key HTTP%d want400", status) })

	unchanged, e := core.Read(ctx, f.token)
	Expect(e != nil || unchanged.ConfigRevision != view.ConfigRevision).To(BeFalse(), func() string { return fmt.Sprint("invalid empty key changed effective configuration", e) })

	status, _ = send("/api/v1/configuration/plans/"+plan.ID+"/commit", commit, false)
	Expect(status != 200).To(BeFalse(), func() string { return fmt.Sprint("absent optional natural commit rejected", status) })

	final, e := core.Read(ctx, f.token)
	Expect(e != nil || final.ConfigRevision != view.ConfigRevision+1).To(BeFalse(), func() string { return fmt.Sprint("natural commit did not apply once", e) })

})
var _ = It("TestConfigurationExpiryAppliesAfterDurableCommitAndKeepsOriginalReceipt", Label("persistence"), func() {
	t := GinkgoT()

	f := newFixture(t)
	future := time.Now().UTC().Add(2 * time.Hour)
	f.auth.Now = func() time.Time { return future }
	core := config.New(f.store, f.auth, nil)
	core.Now = func() time.Time { return future }
	baseline, e := core.Read(ctx, f.token)
	Expect(e != nil).To(BeFalse(), func() string { return fmt.Sprint(e) })

	response, e := core.Plan(ctx, f.token, key(t), config.PlanRequest{Scope: config.Scope{Kind: "library"}, ExpectedRevision: baseline.ConfigRevision, Changes: map[string]any{"security": map[string]any{"session_absolute_seconds": json.Number("3600")}}})
	Expect(e != nil).To(BeFalse(), func() string { return fmt.Sprint(e) })

	var plan domain.ConfigurationPlan
	{
		e = json.Unmarshal(response.Body, &plan)
		Expect(e != nil).To(BeFalse(), func() string { return fmt.Sprint(e) })
	}

	alias := key(t)
	commit := config.CommitRequest{ExpectedRevision: baseline.ConfigRevision, Confirmed: true}
	epoch, head, e := f.auth.EventHead(ctx, f.token)
	Expect(e != nil).To(BeFalse(), func() string { return fmt.Sprint(e) })

	_, e = core.Commit(ctx, f.token, plan.ID, alias, commit)
	Expect(!errors.Is(e, sec.ErrExpired)).To(BeFalse(), func() string { return fmt.Sprint("new expiry did not guard postcommit delivery", e) })

	current, e := f.store.Configuration(ctx)
	Expect(e != nil || current.Revision != baseline.ConfigRevision+1 || !bytes.Contains(current.Value, []byte(`"session_absolute_seconds":3600`))).To(BeFalse(), func() string { return fmt.Sprint("valid owner change rolled back under its newly selected expiry", e) })
	{

		_, e = f.auth.Authenticate(ctx, f.token)
		Expect(!errors.Is(e, sec.ErrExpired)).To(BeFalse(), func() string { return fmt.Sprint("old session still usable", e) })
	}

	events, e := f.store.Events(ctx, epoch, head, 64)
	Expect(e != nil || len(events) != 1 || events[0].Kind != "configuration.changed" || events[0].Revision != baseline.ConfigRevision+1).To(BeFalse(), func() string { return fmt.Sprint("configuration outcome and atomic policy event diverged", events, e) })

	pair, e := f.auth.CreatePairing(ctx, "http://127.0.0.1:9270", "fresh owner after expiry", "fixture-fresh")
	Expect(e != nil).To(BeFalse(), func() string { return fmt.Sprint(e) })
	{

		_, e = f.auth.BootstrapDecide(ctx, domain.PairingDecision{ID: pair.ID, Code: pair.Code, Origin: pair.Origin, ETag: pair.ETag, OperationID: key(t), Action: "approve"})
		Expect(e != nil).To(BeFalse(), func() string { return fmt.Sprint(e) })
	}

	fresh, e := f.auth.Poll(ctx, pair.ID, pair.PollingSecret, pair.Origin)
	Expect(e != nil).To(BeFalse(), func() string { return fmt.Sprint(e) })

	replay, e := core.Commit(ctx, fresh.Token, plan.ID, "", commit)
	Expect(e != nil || replay.Status != 200).To(BeFalse(), func() string { return fmt.Sprint("new owner could not replay committed original plan", e) })

	receipt, e := f.auth.Operation(ctx, fresh.Token, alias)
	Expect(e != nil || receipt.Response == nil || !bytes.Equal(receipt.Response.Body, replay.Body)).To(BeFalse(), func() string { return fmt.Sprint("original alias/natural outcome missing", e) })

	current, e = f.store.Configuration(ctx)
	Expect(e != nil || current.Revision != baseline.ConfigRevision+1).To(BeFalse(), func() string { return fmt.Sprint("replay committed twice", e) })

})

var _ = It("TestConfigurationEffectiveEndpointCollisionFailsPlanAndCommit", Label("persistence"), func() {
	t := GinkgoT()
	f := newFixture(t)
	plain := config.New(f.store, f.auth, nil)
	baseline, e := plain.Read(ctx, f.token)
	Expect(e != nil).To(BeFalse(), func() string { return fmt.Sprint(e) })
	var current struct {
		Network struct {
			Listener struct {
				Port int `json:"port"`
			} `json:"listener"`
		} `json:"network"`
	}
	Expect(json.Unmarshal(baseline.Value, &current) != nil).To(BeFalse())
	port := current.Network.Listener.Port
	changes := func(external string) map[string]any {
		return map[string]any{"network": map[string]any{
			"remote_enabled":    true,
			"external_base_url": external,
			"allowed_origins":   []any{"http://127.0.0.1:" + strconv.Itoa(port), external},
		}}
	}

	// A fresh proposal whose external host equals the configured listener
	// host fails the static rule before any plan is persisted.
	_, e = plain.Plan(ctx, f.token, key(t), config.PlanRequest{Scope: config.Scope{Kind: "library"}, ExpectedRevision: baseline.ConfigRevision, Changes: changes("https://127.0.0.1:" + strconv.Itoa(port))})
	Expect(e == nil).To(BeFalse(), func() string { return "static collision accepted at plan time" })

	// A noncolliding external plan commits and advances the revision, even
	// while the daemon runs under a CLI listener override on another port.
	committed, e := plain.Plan(ctx, f.token, key(t), config.PlanRequest{Scope: config.Scope{Kind: "library"}, ExpectedRevision: baseline.ConfigRevision, Changes: changes("https://haeryong.private:9443")})
	Expect(e != nil).To(BeFalse(), func() string { return fmt.Sprint(e) })
	var plan struct {
		ID string `json:"plan_id"`
	}
	Expect(json.Unmarshal(committed.Body, &plan) != nil).To(BeFalse())
	overridden := config.New(f.store, f.auth, nil)
	overridden.ListenerOverride = true
	overridden.RuntimeEndpoint = "http://127.0.0.1:9443"
	restarted := 0
	overridden.Restart = func() { restarted++ }
	_, e = overridden.Commit(ctx, f.token, plan.ID, "", config.CommitRequest{ExpectedRevision: baseline.ConfigRevision, Confirmed: true})
	Expect(e != nil).To(BeFalse(), func() string { return fmt.Sprint("noncolliding commit rejected: ", e) })
	Expect(restarted).To(Equal(1))
	after, e := overridden.Read(ctx, f.token)
	Expect(e != nil).To(BeFalse())
	Expect(after.ConfigRevision != baseline.ConfigRevision+1).To(BeFalse(), "commit did not advance the revision")

	// Under the same override, a fresh proposal whose external host equals
	// the ACTUAL runtime endpoint fails the effective-endpoint rule even
	// though the static listener comparison passes.
	_, e = overridden.Plan(ctx, f.token, key(t), config.PlanRequest{Scope: config.Scope{Kind: "library"}, ExpectedRevision: after.ConfigRevision, Changes: changes("https://127.0.0.1:9443")})
	Expect(e == nil).To(BeFalse(), func() string { return "override collision accepted at plan time" })

	// A plan persisted before the override existed becomes invalid at commit
	// under the override, with no revision, effects or restart.
	preOverride := config.New(f.store, f.auth, nil)
	older, e := preOverride.Plan(ctx, f.token, key(t), config.PlanRequest{Scope: config.Scope{Kind: "library"}, ExpectedRevision: after.ConfigRevision, Changes: changes("https://127.0.0.1:9443")})
	Expect(e != nil).To(BeFalse(), func() string { return fmt.Sprint("pre-override plan rejected: ", e) })
	var olderPlan struct {
		ID string `json:"plan_id"`
	}
	Expect(json.Unmarshal(older.Body, &olderPlan) != nil).To(BeFalse())
	restarted = 0
	_, e = overridden.Commit(ctx, f.token, olderPlan.ID, "", config.CommitRequest{ExpectedRevision: after.ConfigRevision, Confirmed: true})
	Expect(e == nil).To(BeFalse(), func() string { return "stale-collision commit accepted" })
	Expect(restarted).To(Equal(0))
	final, e := overridden.Read(ctx, f.token)
	Expect(e != nil).To(BeFalse())
	Expect(final.ConfigRevision != after.ConfigRevision).To(BeFalse(), "failed commit changed the revision")
	// Replay semantics are unchanged: the failed commit never completed, so
	// retrying the same plan fails identically instead of replaying.
	_, e = overridden.Commit(ctx, f.token, olderPlan.ID, "", config.CommitRequest{ExpectedRevision: after.ConfigRevision, Confirmed: true})
	Expect(e == nil).To(BeFalse(), func() string { return "failed commit retried into an effect" })
})
