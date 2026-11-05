package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"sync/atomic"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	config "haeryong.local/haeryong/internal/application/configuration"
	"haeryong.local/haeryong/internal/application/service"
	app "haeryong.local/haeryong/internal/application/storage"
	"haeryong.local/haeryong/internal/domain"
)

func privateIngressChanges() map[string]any {
	return map[string]any{"network": map[string]any{
		"remote_enabled": true, "external_base_url": "https://node.private:9271",
		"allowed_origins":   []any{"http://127.0.0.1:9270", "https://node.private:9271"},
		"renderer_mappings": []any{map[string]any{"version_id": "00000000-0000-0000-0000-000000000001", "origin": "https://node.private:9291", "upstream": "http://127.0.0.1:9291", "reassignable": false}},
	}}
}

var _ = It("TestActualCLIPrivateIngressExposurePreviewRequiresConfirmation", Label("persistence"), func() {
	t := GinkgoT()
	f := newCommandFixture(t)
	var restarts atomic.Int64
	f.configuration.Restart = func() { restarts.Add(1) }
	_, flags := callerProfile(t, f.selection, f.session, f.token, f.base)
	input := filepath.Join(t.TempDir(), "private-route.json")
	changes, e := json.Marshal(privateIngressChanges())
	Expect(e).NotTo(HaveOccurred())
	Expect(os.WriteFile(input, changes, 0600)).To(Succeed())
	view, e := f.configuration.Read(context.Background(), f.token)
	Expect(e).NotTo(HaveOccurred())
	out := cli(t, 0, append([]string{"config", "plan", input, "--expected-config-revision", strconv.FormatInt(view.ConfigRevision, 10)}, flags...)...)
	var planned struct {
		Data struct {
			Result domain.ConfigurationPlan `json:"result"`
		} `json:"data"`
	}
	Expect(json.Unmarshal(out, &planned)).To(Succeed())
	plan := planned.Data.Result
	var impact struct {
		Exposure struct {
			Changed bool `json:"changed"`
			Before  struct {
				Remote bool `json:"remote_enabled"`
			} `json:"before"`
			After struct {
				Remote   bool                     `json:"remote_enabled"`
				Origin   string                   `json:"management_origin"`
				Mappings []config.RendererMapping `json:"renderer_mappings"`
			} `json:"after"`
			ProxyChanged bool   `json:"proxy_configuration_changed_by_commit"`
			Action       string `json:"private_proxy_action"`
		} `json:"exposure"`
	}
	Expect(json.Unmarshal(plan.Impact, &impact)).To(Succeed())
	Expect(impact.Exposure.Changed).To(BeTrue())
	Expect(impact.Exposure.Before.Remote).To(BeFalse())
	Expect(impact.Exposure.After.Remote).To(BeTrue())
	Expect(impact.Exposure.After.Origin).To(Equal("https://node.private:9271"))
	Expect(impact.Exposure.After.Mappings).To(HaveLen(1))
	Expect(impact.Exposure.ProxyChanged).To(BeFalse())
	Expect(impact.Exposure.Action).To(Equal("inspect_apply_and_verify_private_proxy_separately"))
	apply := []string{"config", "apply", plan.ID, "--expected-config-revision", strconv.FormatInt(plan.BaseRevision, 10)}
	cli(t, 2, append(append([]string(nil), apply...), flags...)...)
	after, e := f.configuration.Read(context.Background(), f.token)
	Expect(e).NotTo(HaveOccurred())
	Expect(after.ConfigRevision).To(Equal(view.ConfigRevision))
	Expect(restarts.Load()).To(BeZero())
	apply = append(apply, "--confirmed")
	first := cli(t, 0, append(append([]string(nil), apply...), flags...)...)
	Expect(restarts.Load()).To(Equal(int64(1)))
	replay := cli(t, 0, append(apply, flags...)...)
	var one, two struct {
		Data struct {
			Result json.RawMessage `json:"result"`
		} `json:"data"`
	}
	Expect(json.Unmarshal(first, &one)).To(Succeed())
	Expect(json.Unmarshal(replay, &two)).To(Succeed())
	Expect(two.Data.Result).To(Equal(one.Data.Result))
	Expect(restarts.Load()).To(Equal(int64(1)))
})

// This retained-row fixture represents a plan made by the pre-preview binary.
// It exercises durable compatibility, separately from the real CLI flow above.
func legacyExposurePlan(f commandFixture, changes map[string]any) domain.ConfigurationPlan {
	GinkgoHelper()
	view, e := f.configuration.Read(context.Background(), f.token)
	Expect(e).NotTo(HaveOccurred())
	old, e := config.Decode(bytes.NewReader(view.Value), "json")
	Expect(e).NotTo(HaveOccurred())
	next := config.Merge(old, changes)
	Expect(config.Validate(next)).To(Succeed())
	id, e := service.NewID()
	Expect(e).NotTo(HaveOccurred())
	encodedChanges, e := json.Marshal(changes)
	Expect(e).NotTo(HaveOccurred())
	legacyImpact, e := json.Marshal(map[string]any{
		"controlled_restart_required": !reflect.DeepEqual(old["network"], next["network"]),
		"security":                    "immediate", "workflow_scope": "new capture; current restrictions apply", "provider_execution": "admission_not_ready",
	})
	Expect(e).NotTo(HaveOccurred())
	return domain.ConfigurationPlan{
		ID: id, PrincipalID: f.session.PrincipalID, BaseRevision: view.ConfigRevision,
		ExpiresAt: time.Now().UTC().Add(15 * time.Minute).Format(time.RFC3339Nano),
		Scope:     json.RawMessage(`{"kind":"library"}`), Changes: encodedChanges,
		Effective: config.Canonical(next), Permissions: map[string]int64{},
		Impact: legacyImpact,
	}
}

var _ = It("TestConfigurationExposureLegacyFreshPlansRequireNewExposurePreview", Label("persistence"), func() {
	for _, exposure := range []bool{true, false} {
		f := newCommandFixture(GinkgoT())
		changes := privateIngressChanges()
		if !exposure {
			changes = map[string]any{"security": map[string]any{"session_absolute_seconds": json.Number("3600")}}
		}
		plan := legacyExposurePlan(f, changes)
		Expect(f.store.Write(context.Background(), func(tx app.WriteTx) error { return tx.PutConfigurationPlan(plan) })).To(Succeed())
		var restarts atomic.Int64
		f.configuration.Restart = func() { restarts.Add(1) }
		response, e := f.configuration.Commit(context.Background(), f.token, plan.ID, "", config.CommitRequest{ExpectedRevision: plan.BaseRevision, Confirmed: true})
		current, readErr := f.store.Configuration(context.Background())
		Expect(readErr).NotTo(HaveOccurred())
		if exposure {
			Expect(e).To(MatchError(config.ErrStalePlan))
			Expect(current.Revision).To(Equal(plan.BaseRevision))
		} else {
			Expect(e).NotTo(HaveOccurred())
			Expect(response.Status).To(Equal(200))
			Expect(current.Revision).To(Equal(plan.BaseRevision + 1))
		}
		Expect(restarts.Load()).To(BeZero())
	}
})

var _ = It("TestConfigurationExposureLegacyCompletedNaturalAndAliasReceiptsReplayExactly", Label("persistence"), func() {
	f := newCommandFixture(GinkgoT())
	plan := legacyExposurePlan(f, privateIngressChanges())
	plan.ExpiresAt = time.Now().UTC().Add(-time.Hour).Format(time.RFC3339Nano)
	input := config.CommitRequest{ExpectedRevision: plan.BaseRevision, Confirmed: true}
	body, e := json.Marshal(input)
	Expect(e).NotTo(HaveOccurred())
	result, e := json.Marshal(map[string]any{"plan_id": plan.ID, "config_revision": plan.BaseRevision + 1, "impact": plan.Impact})
	Expect(e).NotTo(HaveOccurred())
	response := domain.Response{Status: 200, Body: result}
	key := domain.OperationKey{PrincipalID: f.session.PrincipalID, OperationID: plan.ID}
	Expect(f.store.Write(context.Background(), func(tx app.WriteTx) error {
		if e := tx.PutConfigurationPlan(plan); e != nil {
			return e
		}
		if _, _, e := tx.ReserveOperation(domain.Request{Key: key, Method: "POST", Target: "/api/v1/configuration/plans/" + plan.ID + "/commit", Body: body}, nil); e != nil {
			return e
		}
		if e := tx.SetConfiguration(domain.Configuration{Value: plan.Effective, Actor: f.session.PrincipalID}, plan.BaseRevision); e != nil {
			return e
		}
		return tx.CompleteOperation(key, response)
	})).To(Succeed())
	var restarts atomic.Int64
	f.configuration.Restart = func() { restarts.Add(1) }
	alias, e := service.NewID()
	Expect(e).NotTo(HaveOccurred())
	for _, operation := range []string{"", alias, alias} {
		replayed, e := f.configuration.Commit(context.Background(), f.token, plan.ID, operation, input)
		Expect(e).NotTo(HaveOccurred())
		Expect(replayed).To(Equal(response))
	}
	current, e := f.store.Configuration(context.Background())
	Expect(e).NotTo(HaveOccurred())
	Expect(current.Revision).To(Equal(plan.BaseRevision + 1))
	Expect(restarts.Load()).To(BeZero())
})
