package importer_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"haeryong.local/haeryong/internal/adapters/sqlite"
	config "haeryong.local/haeryong/internal/application/configuration"
	"testing"
	"time"

	"haeryong.local/haeryong/internal/domain"
)

func TestActualConfigurationPlanNaturalReplayAliasAndRollback(t *testing.T) {
	f := newFixture(t)
	core := config.New(f.store, f.auth, nil)
	now := time.Now().UTC()
	core.Now = func() time.Time { return now }
	baseline, e := core.Read(ctx, f.token)
	if e != nil {
		t.Fatal(e)
	}
	request := config.PlanRequest{Scope: config.Scope{Kind: "library"}, ExpectedRevision: baseline.ConfigRevision, Changes: map[string]any{"security": map[string]any{"remember_device_default": false}}}
	response, e := core.Plan(ctx, f.token, key(t), request)
	if e != nil {
		t.Fatal(e)
	}
	var plan domain.ConfigurationPlan
	if e = json.Unmarshal(response.Body, &plan); e != nil {
		t.Fatal(e)
	}
	alias := key(t)
	commit := config.CommitRequest{ExpectedRevision: baseline.ConfigRevision, Confirmed: true}
	result, e := core.Commit(ctx, f.token, plan.ID, alias, commit)
	if e != nil {
		t.Fatal(e)
	}
	current, e := core.Read(ctx, f.token)
	if e != nil || current.ConfigRevision != baseline.ConfigRevision+1 || current.ETag == baseline.ETag {
		t.Fatal(current, e)
	}
	if !bytes.Contains(current.Value, []byte(`"remember_device_default":false`)) {
		t.Fatal("committed value missing")
	}
	now = now.Add(time.Hour)
	same, e := core.Commit(ctx, f.token, plan.ID, "", commit)
	if e != nil || !bytes.Equal(same.Body, result.Body) {
		t.Fatal("expired/stale committed natural replay", same, e)
	}
	aliasReceipt, e := f.auth.Operation(ctx, f.token, alias)
	if e != nil || aliasReceipt.Response == nil || !bytes.Equal(aliasReceipt.Response.Body, result.Body) {
		t.Fatal("alias not bound", e)
	}
	if _, e = core.Commit(ctx, f.token, plan.ID, key(t), config.CommitRequest{ExpectedRevision: baseline.ConfigRevision + 1, Confirmed: true}); !errors.Is(e, domain.ErrConflict) {
		t.Fatal("changed natural intent", e)
	}
	request.ExpectedRevision = current.ConfigRevision
	request.Changes = map[string]any{"security": map[string]any{"pairing_expiry_seconds": json.Number("200")}}
	response, e = core.Plan(ctx, f.token, "", request)
	if e != nil {
		t.Fatal(e)
	}
	_ = json.Unmarshal(response.Body, &plan)
	fault := errors.New("labelled config commit durability failure")
	f.dbFault = func(b sqlite.Boundary) error {
		if b == sqlite.BeforeCommit {
			return fault
		}
		return nil
	}
	alias = key(t)
	if _, e = core.Commit(ctx, f.token, plan.ID, alias, config.CommitRequest{ExpectedRevision: current.ConfigRevision, Confirmed: true}); !errors.Is(e, fault) {
		t.Fatal(e)
	}
	f.dbFault = nil
	after, e := core.Read(ctx, f.token)
	if e != nil || after.ConfigRevision != current.ConfigRevision || !bytes.Equal(after.Value, current.Value) {
		t.Fatal("failed commit changed effective policy", after, e)
	}
	if _, e = f.auth.Operation(ctx, f.token, alias); !errors.Is(e, domain.ErrNotFound) {
		t.Fatal("failed commit retained alias/effect", e)
	}
	f.reopen(t)
	core = config.New(f.store, f.auth, nil)
	if _, e = core.Commit(ctx, f.token, plan.ID, alias, config.CommitRequest{ExpectedRevision: current.ConfigRevision, Confirmed: true}); e != nil {
		t.Fatal("durable exact plan retry", e)
	}
	// A source-policy restriction atomically advances current project access.
	current, e = core.Read(ctx, f.token)
	if e != nil {
		t.Fatal(e)
	}
	before, e := f.store.Project(ctx, f.project)
	if e != nil {
		t.Fatal(e)
	}
	request.ExpectedRevision = current.ConfigRevision
	request.Changes = map[string]any{"source": map[string]any{"default_retention": "selected_snapshot"}}
	response, e = core.Plan(ctx, f.token, "", request)
	if e != nil {
		t.Fatal(e)
	}
	_ = json.Unmarshal(response.Body, &plan)
	if _, e = core.Commit(ctx, f.token, plan.ID, "", config.CommitRequest{ExpectedRevision: current.ConfigRevision, Confirmed: true}); e != nil {
		t.Fatal(e)
	}
	afterProject, e := f.store.Project(ctx, f.project)
	if e != nil || afterProject.PermissionsRevision != before.PermissionsRevision+1 {
		t.Fatal(afterProject, e)
	}
}
