package sqlite

import (
	"encoding/json"
	"errors"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"haeryong.local/haeryong/internal/application/references"
	app "haeryong.local/haeryong/internal/application/storage"
	"haeryong.local/haeryong/internal/domain"
)

var _ = DescribeTable("T00805 owner repair is pending until actual guarded deterministic completion", Label("persistence"), func(restrict bool) {
	f, core, token, record := decisionFixture("backlink_index_repair")
	version := *record.VersionID
	f.run(version, "search")
	f.run(version, "references")
	response, e := core.FindingAction(ctx, token, f.id(), record.ID, domain.FindingETag(record), "approve", []byte(`{"decision":"approve"}`))
	Expect(e).NotTo(HaveOccurred())
	Expect(response.Status).To(Equal(202))
	accepted := actionResult(response)
	Expect(accepted.State).To(Equal("repair_pending"))
	Expect(accepted.Finding.Status).To(Equal("proposed"))
	Expect(accepted.JobID).NotTo(BeNil())
	if restrict {
		Expect(f.s.Write(ctx, func(tx app.WriteTx) error {
			cfg, e := tx.Configuration()
			if e != nil {
				return e
			}
			var body map[string]any
			Expect(json.Unmarshal(cfg.Value, &body)).To(Succeed())
			body["maintenance"].(map[string]any)["auto_apply"].(map[string]any)["backlink_index_repair"] = "disabled"
			cfg.Value = jsonBytes(body)
			return tx.SetConfiguration(cfg, cfg.Revision)
		})).To(Succeed())
		e = references.NewWorker(f.s, f.bytes).Process(ctx, *accepted.JobID)
		Expect(errors.Is(e, domain.ErrSearchRestricted)).To(BeTrue())
		Expect(f.s.Write(ctx, func(tx app.WriteTx) error {
			saved, e := tx.Finding(record.ID)
			if e != nil {
				return e
			}
			Expect(saved.Status).To(Equal("proposed"))
			job, e := tx.Job(*accepted.JobID)
			Expect(job.State).To(Equal("paused"))
			return e
		})).To(Succeed())
		return
	}
	f.run(version, "references")
	Expect(f.s.Write(ctx, func(tx app.WriteTx) error {
		saved, e := tx.Finding(record.ID)
		if e != nil {
			return e
		}
		Expect(saved.Status).To(Equal("applied"))
		Expect(saved.Reason).To(Equal("owner_repair_verified"))
		job, e := tx.Job(*accepted.JobID)
		Expect(job.State).To(Equal("completed"))
		return e
	})).To(Succeed())
}, Entry("verified rebuild", false), Entry("current restriction", true))
