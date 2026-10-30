package sqlite

import (
	"bytes"
	"encoding/json"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	app "haeryong.local/haeryong/internal/application/storage"
	"haeryong.local/haeryong/internal/domain"
	"path/filepath"
	"strings"
)

var _ = It("C03R migration17 transport audit survives inventory/restore and cannot rewrite accepted identity", Label("persistence"), func() {
	s, a := providerFixture()
	older := filepath.Join(GinkgoT().TempDir(), "older.sqlite")
	Expect(s.SnapshotDatabase(ctx, older)).To(Succeed())
	audit := domain.ProviderTransport{Provider: "codex", Version: "0.164.0", BinarySHA: strings.Repeat("b", 64), PreparedAt: "2026-10-29T12:00:00Z", SetupThread: "fixture-thread", CWD: "/fixture", Approval: "never", Sandbox: "read-only", AuthMode: "chatgpt", FrameSHA: strings.Repeat("c", 64)}
	Expect(s.Write(ctx, func(tx app.WriteTx) error {
		inputs, e := tx.ProviderInputs(a.ID)
		if e != nil {
			return e
		}
		for _, input := range inputs {
			read, e := tx.CapturedRead(input.ReadID)
			if e != nil {
				return e
			}
			object, e := tx.Object(input.Hash)
			if e != nil {
				return e
			}
			audit.Disclosure = append(audit.Disclosure, domain.ProviderDisclosure{ReadID: read.ID, Hash: input.Hash, Size: object.Size, Source: read.SourceJSON, Location: read.LocationJSON})
		}
		return tx.RecordProviderTransport(a.ID, audit)
	})).To(Succeed())
	Expect(s.Write(ctx, func(tx app.WriteTx) error { return tx.DispatchProviderAttempt(a.ID) })).To(Succeed())
	audit.AcceptedAt = "2026-10-29T12:00:01Z"
	Expect(s.Write(ctx, func(tx app.WriteTx) error {
		if e := tx.RecordProviderTransport(a.ID, audit); e != nil {
			return e
		}
		return tx.ObserveProviderAttempt(a.ID, "running", "fixture-turn", "fixture-thread", "")
	})).To(Succeed())
	changed := audit
	changed.SetupThread = "different-thread"
	Expect(s.Write(ctx, func(tx app.WriteTx) error { return tx.RecordProviderTransport(a.ID, changed) })).To(MatchError(domain.ErrTransition))
	changed = audit
	changed.AcceptedAt = ""
	Expect(s.Write(ctx, func(tx app.WriteTx) error { return tx.RecordProviderTransport(a.ID, changed) })).To(MatchError(domain.ErrTransition))
	var inventory bytes.Buffer
	Expect(RestoreOutstanding(ctx, filepath.Join(s.profile.DataDir, "storage/library.sqlite"), func(raw json.RawMessage) error { _, e := inventory.Write(append(raw, '\n')); return e })).To(Succeed())
	var fact providerRestoreFact
	found := false
	for _, line := range bytes.Split(bytes.TrimSpace(inventory.Bytes()), []byte("\n")) {
		var header struct {
			ID string `json:"attempt_id"`
		}
		Expect(json.Unmarshal(line, &header)).To(Succeed())
		if header.ID == a.ID {
			Expect(json.Unmarshal(line, &fact)).To(Succeed())
			found = true
			break
		}
	}
	Expect(found).To(BeTrue())
	Expect(fact.Transport).NotTo(BeNil())
	Expect(fact.Transport.Disclosure).To(HaveLen(1))
	Expect(fact.Transport.AcceptedAt).To(Equal(audit.AcceptedAt))
	Expect(RetainProviderOrphans(ctx, older, bytes.NewReader(inventory.Bytes()), nil)).To(Succeed())
	db, e := openCopy(older)
	Expect(e).NotTo(HaveOccurred())
	defer db.Close()
	var raw []byte
	Expect(db.QueryRow(`SELECT value FROM provider_orphans WHERE attempt_id=?`, a.ID).Scan(&raw)).To(Succeed())
	Expect(json.Unmarshal(raw, &fact)).To(Succeed())
	Expect(fact.Transport).NotTo(BeNil())
	Expect(fact.Transport.FrameSHA).To(Equal(audit.FrameSHA))
	Expect(s.Write(ctx, func(tx app.WriteTx) error { _, e := tx.CancelJob(a.JobID); return e })).To(Succeed())
	Expect(s.Write(ctx, func(tx app.WriteTx) error { return tx.GuardProviderPayload(a.JobID, a.ID, false) })).To(MatchError(domain.ErrStaleWorker))
})

var _ = It("C03R migration17 rejects altered or absent transport declarations", Label("persistence"), func() {
	s, _ := providerFixture()
	_, e := s.db.Exec(`DROP TABLE provider_transport`)
	Expect(e).NotTo(HaveOccurred())
	Expect(s.validateTables(ctx, SchemaRevision)).NotTo(Succeed())
	_, e = s.db.Exec(`CREATE TABLE provider_transport(attempt_id TEXT PRIMARY KEY,value BLOB NOT NULL)`)
	Expect(e).NotTo(HaveOccurred())
	Expect(s.validateTables(ctx, SchemaRevision)).To(MatchError("changed provider transport schema declaration"))
})

var _ = It("C03R revision16 migration preserves old attempt facts without inventing transport or input observations", Label("persistence"), func() {
	s, a := providerFixture()
	profile := s.profile
	Expect(s.Close()).To(Succeed())
	db := readDB(GinkgoT(), profile)
	_, e := db.Exec(`DROP TABLE provider_transport;DELETE FROM schema_migrations WHERE revision=17;UPDATE libraries SET schema_revision=16;PRAGMA user_version=16`)
	Expect(e).NotTo(HaveOccurred())
	Expect(db.Close()).To(Succeed())
	upgraded := New(nil)
	state, closeStore, e := upgraded.Prepare(ctx, profile)
	Expect(e).NotTo(HaveOccurred())
	defer closeStore()
	Expect(state.Ready).To(BeTrue())
	Expect(upgraded.Write(ctx, func(tx app.WriteTx) error {
		actual, e := tx.ProviderAttempt(a.ID)
		if e != nil {
			return e
		}
		Expect(actual.RequestSHA).To(Equal(a.RequestSHA))
		Expect(actual.Transport).To(BeNil())
		Expect(actual.RequestID).To(BeEmpty())
		Expect(actual.SessionID).To(BeEmpty())
		inputs, e := tx.ProviderInputs(a.ID)
		if e != nil {
			return e
		}
		Expect(inputs).To(HaveLen(1))
		Expect(inputs[0].ObservedAt).To(BeNil())
		Expect(inputs[0].ObservedOrigin).To(BeEmpty())
		return nil
	})).To(Succeed())
	var count int
	Expect(upgraded.db.QueryRow(`SELECT count(*) FROM provider_transport`).Scan(&count)).To(Succeed())
	Expect(count).To(BeZero())
	Expect(upgraded.validateTables(ctx, 17)).To(Succeed())
})
