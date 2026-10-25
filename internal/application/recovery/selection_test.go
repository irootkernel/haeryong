package recovery

import (
	"encoding/json"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"haeryong.local/haeryong/internal/domain"
)

var _ = It("Copies closed action selector option and record equivalence classes", Label("unit"), func() {
	id := "11111111-1111-4111-8111-111111111111"
	bases := []domain.CopyRequest{{Action: "backup.create", Destination: "/tmp/output"}, {Action: "backup.resume", ID: id}, {Action: "backup.abandon", ID: id, Confirmed: true}, {Action: "export.plan.show", ID: id}, {Action: "export.create", ID: id}, {Action: "export.plan", VersionID: id, Destination: "/tmp/output", Format: "zip"}, {Action: "export.create", VersionID: id, Destination: "/tmp/output", Format: "directory"}}
	for _, r := range bases {
		Expect(Validate(&r)).To(Succeed(), r.Action)
	}
	for _, action := range []string{"", "restore.apply", "host.read", "backup.delete"} {
		r := domain.CopyRequest{Action: action}
		Expect(Validate(&r)).NotTo(Succeed())
	}
	base := domain.CopyRequest{Action: "export.plan", VersionID: id, Destination: "/tmp/output", Format: "directory"}
	for _, field := range []string{"id", "version_id", "document_id", "project_id", "runtime_generation"} {
		raw, _ := json.Marshal(base)
		var m map[string]any
		Expect(json.Unmarshal(raw, &m)).To(Succeed())
		m[field] = "not-a-uuid"
		raw, _ = json.Marshal(m)
		var r domain.CopyRequest
		Expect(json.Unmarshal(raw, &r)).To(Succeed())
		Expect(Validate(&r)).NotTo(Succeed(), field)
	}
	for _, versions := range []string{"", "latest", "all"} {
		for _, lifecycle := range []string{"", "active", "archived", "trash", "all"} {
			for _, selector := range []string{"document", "project"} {
				r := domain.CopyRequest{Action: "export.plan", Destination: "/tmp/output", Format: "zip", Versions: versions, Lifecycle: lifecycle}
				if selector == "document" {
					r.DocumentID = id
				} else {
					r.ProjectID = id
				}
				Expect(Validate(&r)).To(Succeed())
			}
		}
	}
	mutations := []func(*domain.CopyRequest){func(r *domain.CopyRequest) { r.Destination = "" }, func(r *domain.CopyRequest) { r.Format = "tar" }, func(r *domain.CopyRequest) { r.VersionID = "" }, func(r *domain.CopyRequest) { r.DocumentID = id }, func(r *domain.CopyRequest) { r.ProjectID = id }, func(r *domain.CopyRequest) { r.Versions = "all" }, func(r *domain.CopyRequest) { r.Lifecycle = "purged" }, func(r *domain.CopyRequest) { r.Cursor = id }, func(r *domain.CopyRequest) { r.Confirmed = true }, func(r *domain.CopyRequest) { r.Records = []string{"notes", "notes"} }, func(r *domain.CopyRequest) { r.Records = []string{"unknown"} }}
	for n, change := range mutations {
		r := base
		change(&r)
		Expect(Validate(&r)).NotTo(Succeed(), n)
	}
	for _, kind := range []string{"notes", "review", "reader", "revisit", "references"} {
		r := base
		r.Records = []string{kind}
		Expect(Validate(&r)).To(Succeed())
	}
	r := base
	r.Records = []string{"decisions"}
	Expect(Validate(&r)).To(MatchError(ErrRecordUnavailable))
	for _, base := range bases {
		if base.Action == "export.plan" || base.Action == "export.create" && base.ID == "" {
			continue
		}
		for _, change := range []func(*domain.CopyRequest){func(r *domain.CopyRequest) { r.Format = "zip" }, func(r *domain.CopyRequest) { r.Records = []string{"notes"} }, func(r *domain.CopyRequest) { r.RetainedSources = true }, func(r *domain.CopyRequest) { r.Versions = "all" }, func(r *domain.CopyRequest) { r.Lifecycle = "all" }, func(r *domain.CopyRequest) { r.MetadataDestination = "/tmp/metadata" }, func(r *domain.CopyRequest) { r.VersionID = id }} {
			r := base
			change(&r)
			Expect(Validate(&r)).NotTo(Succeed(), base.Action)
		}
	}
})
