package importer_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"haeryong.local/haeryong/internal/adapters/sqlite"
	config "haeryong.local/haeryong/internal/application/configuration"

	"time"

	"fmt"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"haeryong.local/haeryong/internal/domain"
)

var _ = It("TestActualConfigurationPlanNaturalReplayAliasAndRollback", Label("persistence"), func() {
	t := GinkgoT()

	f := newFixture(t)
	core := config.New(f.store, f.auth, nil)
	now := time.Now().UTC()
	core.Now = func() time.Time { return now }
	baseline, e := core.Read(ctx, f.token)
	Expect(e != nil).To(BeFalse(), func() string { return fmt.Sprint(e) })

	request := config.PlanRequest{Scope: config.Scope{Kind: "library"}, ExpectedRevision: baseline.ConfigRevision, Changes: map[string]any{"security": map[string]any{"remember_device_default": false}}}
	response, e := core.Plan(ctx, f.token, key(t), request)
	Expect(e != nil).To(BeFalse(), func() string { return fmt.Sprint(e) })

	var plan domain.ConfigurationPlan
	{
		e = json.Unmarshal(response.Body, &plan)
		Expect(e != nil).To(BeFalse(), func() string { return fmt.Sprint(e) })
	}

	alias := key(t)
	commit := config.CommitRequest{ExpectedRevision: baseline.ConfigRevision, Confirmed: true}
	result, e := core.Commit(ctx, f.token, plan.ID, alias, commit)
	Expect(e != nil).To(BeFalse(), func() string { return fmt.Sprint(e) })

	current, e := core.Read(ctx, f.token)
	Expect(e != nil || current.ConfigRevision != baseline.ConfigRevision+1 || current.ETag == baseline.ETag).To(BeFalse(), func() string { return fmt.Sprint(current, e) })
	Expect(!bytes.Contains(current.Value, []byte(`"remember_device_default":false`))).To(BeFalse(), func() string { return fmt.Sprint("committed value missing") })

	now = now.Add(time.Hour)
	same, e := core.Commit(ctx, f.token, plan.ID, "", commit)
	Expect(e != nil || !bytes.Equal(same.Body, result.Body)).To(BeFalse(), func() string { return fmt.Sprint("expired/stale committed natural replay", same, e) })

	aliasReceipt, e := f.auth.Operation(ctx, f.token, alias)
	Expect(e != nil || aliasReceipt.Response == nil || !bytes.Equal(aliasReceipt.Response.Body, result.Body)).To(BeFalse(), func() string { return fmt.Sprint("alias not bound", e) })
	{

		_, e = core.Commit(ctx, f.token, plan.ID, key(t), config.CommitRequest{ExpectedRevision: baseline.ConfigRevision + 1, Confirmed: true})
		Expect(!errors.Is(e, domain.ErrConflict)).To(BeFalse(), func() string { return fmt.Sprint("changed natural intent", e) })
	}

	request.ExpectedRevision = current.ConfigRevision
	request.Changes = map[string]any{"security": map[string]any{"pairing_expiry_seconds": json.Number("200")}}
	response, e = core.Plan(ctx, f.token, "", request)
	Expect(e != nil).To(BeFalse(), func() string { return fmt.Sprint(e) })

	_ = json.Unmarshal(response.Body, &plan)
	fault := errors.New("labelled config commit durability failure")
	f.dbFault = func(b sqlite.Boundary) error {
		if b == sqlite.BeforeCommit {
			return fault
		}
		return nil
	}
	alias = key(t)
	{
		_, e = core.Commit(ctx, f.token, plan.ID, alias, config.CommitRequest{ExpectedRevision: current.ConfigRevision, Confirmed: true})
		Expect(!errors.Is(e, fault)).To(BeFalse(), func() string { return fmt.Sprint(e) })
	}

	f.dbFault = nil
	after, e := core.Read(ctx, f.token)
	Expect(e != nil || after.ConfigRevision != current.ConfigRevision || !bytes.Equal(after.Value, current.Value)).To(BeFalse(), func() string { return fmt.Sprint("failed commit changed effective policy", after, e) })
	{

		_, e = f.auth.Operation(ctx, f.token, alias)
		Expect(!errors.Is(e, domain.ErrNotFound)).To(BeFalse(), func() string { return fmt.Sprint("failed commit retained alias/effect", e) })
	}

	f.reopen(t)
	core = config.New(f.store, f.auth, nil)
	{
		_, e = core.Commit(ctx, f.token, plan.ID, alias, config.CommitRequest{ExpectedRevision: current.ConfigRevision, Confirmed: true})
		Expect(e != nil).To(BeFalse(), func() string { return fmt.Sprint("durable exact plan retry", e) })
	}

	// A source-policy restriction atomically advances current project access.
	current, e = core.Read(ctx, f.token)
	Expect(e != nil).To(BeFalse(), func() string { return fmt.Sprint(e) })

	before, e := f.store.Project(ctx, f.project)
	Expect(e != nil).To(BeFalse(), func() string { return fmt.Sprint(e) })

	request.ExpectedRevision = current.ConfigRevision
	request.Changes = map[string]any{"source": map[string]any{"default_retention": "selected_snapshot"}}
	response, e = core.Plan(ctx, f.token, "", request)
	Expect(e != nil).To(BeFalse(), func() string { return fmt.Sprint(e) })

	_ = json.Unmarshal(response.Body, &plan)
	{
		_, e = core.Commit(ctx, f.token, plan.ID, "", config.CommitRequest{ExpectedRevision: current.ConfigRevision, Confirmed: true})
		Expect(e != nil).To(BeFalse(), func() string { return fmt.Sprint(e) })
	}

	afterProject, e := f.store.Project(ctx, f.project)
	Expect(e != nil || afterProject.PermissionsRevision != before.PermissionsRevision+1).To(BeFalse(), func() string { return fmt.Sprint(afterProject, e) })

})
