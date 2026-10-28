package query_test

import (
	"bufio"
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"haeryong.local/haeryong/internal/adapters/foundation"
	"haeryong.local/haeryong/internal/adapters/sqlite"
	appauth "haeryong.local/haeryong/internal/application/auth"
	config "haeryong.local/haeryong/internal/application/configuration"
	"haeryong.local/haeryong/internal/application/service"
	app "haeryong.local/haeryong/internal/application/storage"
	"haeryong.local/haeryong/internal/application/work"
	"haeryong.local/haeryong/internal/domain"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

type orphanStatusDriver struct {
	unknown           string
	executes, lookups int
}

func (d *orphanStatusDriver) Execute(context.Context, domain.ProviderAttempt, []domain.ProviderInput, func() error, func(work.ProviderProgress) error) (work.ProviderOutcome, error) {
	d.executes++
	return work.ProviderOutcome{State: "outcome_unknown"}, nil
}
func (*orphanStatusDriver) Cancel(context.Context, domain.ProviderAttempt) error { return nil }
func (d *orphanStatusDriver) Reconcile(_ context.Context, a domain.ProviderAttempt, body bool) (work.ProviderOutcome, error) {
	d.lookups++
	Expect(body).To(BeFalse())
	if a.ID == d.unknown {
		return work.ProviderOutcome{State: "outcome_unknown", Reason: "transport_unknown"}, nil
	}
	return work.ProviderOutcome{State: "cancelled", Reason: "cancel_confirmed"}, nil
}

var _ = It("PA48 terminal orphan-only resolution releases only execution inputs through actual GC restart and repeated older restores", Label("persistence"), func() {
	f := newFixture(GinkgoT())
	put := func(text string, pin bool) domain.Object {
		o, e := f.store.Put(ctx, strings.NewReader(text), func(tx app.WriteTx, o domain.Object) error {
			if pin {
				return tx.AcquirePin(domain.Pin{ID: "independent-snapshot", Kind: "selected_snapshot", OwnerID: "independent", Hash: o.Hash})
			}
			return nil
		})
		Expect(e).NotTo(HaveOccurred())
		return o
	}
	loose, owned, result, unknown := put("execution-only input", false), put("independent retained input", true), put("retained diagnostic result", false), put("still unknown input", false)
	const aID = "00000000-0000-4000-8000-000000000001"
	const bID = "00000000-0000-4000-8000-000000000002"
	missingJob := key(GinkgoT())
	sha := strings.Repeat("a", 64)
	target, _ := json.Marshal(map[string]string{"project_id": f.project})
	var owner domain.AuthState
	Expect(f.store.Write(ctx, func(tx app.WriteTx) error { var e error; owner, e = tx.AuthState(); return e })).To(Succeed())
	inputs := []domain.ProviderInput{{AttemptID: aID, ReadID: "loose", Hash: loose.Hash}, {AttemptID: aID, ReadID: "owned", Hash: owned.Hash}}
	var raw bytes.Buffer
	encode := json.NewEncoder(&raw)
	Expect(encode.Encode(map[string]any{"attempt_id": aID, "job_id": missingJob, "principal_id": owner.OwnerID, "job_state": "paused", "cancel_requested": false, "request_sha256": sha, "state": "outcome_unknown", "target": json.RawMessage(target), "provider_request_id": "request-a", "provider_session_id": "session-a", "inputs": inputs, "result_hash": result.Hash})).To(Succeed())
	Expect(encode.Encode(map[string]any{"attempt_id": bID, "job_id": missingJob, "principal_id": owner.OwnerID, "job_state": "paused", "request_sha256": sha, "state": "outcome_unknown", "target": json.RawMessage(target), "provider_request_id": "request-b", "inputs": []domain.ProviderInput{{AttemptID: bID, ReadID: "unknown", Hash: unknown.Hash}}, "resolution_job_id": "id-alone-is-not-terminal-proof"})).To(Succeed())
	const cID = "00000000-0000-4000-8000-000000000003"
	Expect(encode.Encode(map[string]any{"attempt_id": cID, "job_id": missingJob, "principal_id": owner.OwnerID, "job_state": "paused", "request_sha256": sha, "state": "outcome_unknown", "target": json.RawMessage(target), "provider_request_id": "request-c2", "inputs": []domain.ProviderInput{{AttemptID: cID, ReadID: "conflict", Hash: unknown.Hash}}, "conflicting_prior_fact": map[string]any{"attempt_id": cID, "request_sha256": sha, "state": "cancelled", "provider_request_id": "request-c1"}})).To(Succeed())

	// These exact fixtures were fsynced/verified by Put; no whole inventory is read.
	available := func(o domain.Object) (bool, error) {
		return o.Hash == loose.Hash || o.Hash == owned.Hash || o.Hash == result.Hash || o.Hash == unknown.Hash, nil
	}
	Expect(sqlite.RetainProviderOrphans(ctx, f.dbPath, bytes.NewReader(raw.Bytes()), available)).To(Succeed())
	admit := func(ids []string) string {
		job := key(GinkgoT())
		scope, _ := json.Marshal(domain.ProviderScope{PrincipalID: owner.OwnerID, AuthEpoch: owner.AuthEpoch, Target: target, RequestSHA: sha})
		Expect(f.store.Write(ctx, func(tx app.WriteTx) error {
			if e := tx.CreateJob(domain.Job{ID: job, Kind: "question", Scope: scope}); e != nil {
				return e
			}
			return tx.AuthorizeProviderReconciliation(job, ids)
		})).To(Succeed())
		return job
	}
	d := &orphanStatusDriver{unknown: bID}
	applies := 0
	p := work.ProviderCoordinator{Store: f.store, Driver: d, Apply: func(app.WriteTx, domain.ProviderAttempt) error { applies++; return nil }}
	partial := admit([]string{aID, bID})
	Expect(p.Process(ctx, partial)).To(Succeed())
	Expect(p.Process(ctx, partial)).To(Succeed())
	j, e := f.store.Job(ctx, partial)
	Expect(e).NotTo(HaveOccurred())
	Expect(j.State).To(Equal("paused"))
	Expect(f.store.Collect(ctx, loose)).To(MatchError(domain.ErrPinned), "a partial finite status operation retains its inputs")
	conflictCarrier := admit([]string{cID})
	Expect(p.Process(ctx, conflictCarrier)).To(Succeed())
	conflicted, e := f.store.Job(ctx, conflictCarrier)
	Expect(e).NotTo(HaveOccurred())
	Expect(conflicted.State).To(Equal("paused"))
	Expect(conflicted.ProviderAttempts.Unknown).To(Equal(1))

	olderA, olderB := filepath.Join(GinkgoT().TempDir(), "older-a.sqlite"), filepath.Join(GinkgoT().TempDir(), "older-b.sqlite")
	Expect(f.store.SnapshotDatabase(ctx, olderA)).To(Succeed())
	Expect(f.store.SnapshotDatabase(ctx, olderB)).To(Succeed())
	bareProof, _ := json.Marshal(map[string]any{"attempt_id": aID, "job_id": missingJob, "principal_id": owner.OwnerID, "job_state": "paused", "request_sha256": sha, "state": "cancelled", "reason": "cancel_confirmed", "target": json.RawMessage(target), "resolution_job_id": "terminal-evidence-is-required"})
	Expect(sqlite.RetainProviderOrphans(ctx, olderB, bytes.NewReader(bareProof), available)).To(Succeed())
	bareDB, e := sql.Open("sqlite", olderB)
	Expect(e).NotTo(HaveOccurred())
	var barePins int
	Expect(bareDB.QueryRow(`SELECT count(*) FROM object_pins WHERE owner_kind='provider_orphan' AND owner_id=? AND content_hash=?`, aID, loose.Hash).Scan(&barePins)).To(Succeed())
	Expect(barePins).To(Equal(1))
	Expect(bareDB.Close()).To(Succeed())

	resolver := admit([]string{aID})
	Expect(p.Process(ctx, resolver)).To(Succeed())
	j, e = f.store.Job(ctx, resolver)
	Expect(e).NotTo(HaveOccurred())
	Expect(j.State).To(Equal("completed"))
	Expect(string(j.Result)).To(ContainSubstring("provider_reconciliation"))
	Expect(string(j.Result)).NotTo(ContainSubstring("result_sha256"))
	Expect(d.executes).To(BeZero())
	Expect(applies).To(BeZero())
	Expect(f.store.Collect(ctx, loose)).To(Succeed())
	Expect(f.store.Collect(ctx, owned)).To(MatchError(domain.ErrPinned))
	Expect(f.store.Collect(ctx, result)).To(MatchError(domain.ErrPinned))
	Expect(f.store.Collect(ctx, unknown)).To(MatchError(domain.ErrPinned))
	profile, e := service.Load(service.Selection{Profile: "test", DataDir: f.profileDir})
	Expect(e).NotTo(HaveOccurred())
	Expect(f.store.Objects.Close()).To(Succeed())
	Expect(f.store.Close()).To(Succeed())
	restarted := foundation.New(nil)
	_, closeRestarted, e := restarted.Prepare(ctx, profile)
	Expect(e).NotTo(HaveOccurred())
	defer closeRestarted()
	j, e = restarted.Job(ctx, resolver)
	Expect(e).NotTo(HaveOccurred())
	Expect(j.State).To(Equal("completed"))
	Expect(restarted.Read(ctx, loose, domain.Pin{Kind: "test_read", OwnerID: "collected-input"}, func(io.ReadSeeker) error { Fail("collected execution bytes must not be delivered"); return nil })).NotTo(Succeed())
	var inventory bytes.Buffer
	Expect(sqlite.RestoreOutstanding(ctx, f.dbPath, func(record json.RawMessage) error { _, e := inventory.Write(append(record, '\n')); return e })).To(Succeed())
	remaining := func(o domain.Object) (bool, error) { return o.Hash != loose.Hash, nil }
	for _, path := range []string{olderA, olderB} {
		Expect(sqlite.RetainProviderOrphans(ctx, path, bytes.NewReader(inventory.Bytes()), remaining)).To(Succeed())
		Expect(sqlite.CheckCopyDatabase(ctx, path)).To(Succeed())
		db, e := sql.Open("sqlite", path)
		Expect(e).NotTo(HaveOccurred())
		var count int
		Expect(db.QueryRow(`SELECT count(*) FROM jobs WHERE job_id=?`, resolver).Scan(&count)).To(Succeed())
		Expect(count).To(BeZero(), "older snapshots contain neither the terminal resolver nor the original job")
		Expect(db.QueryRow(`SELECT count(*) FROM object_pins WHERE owner_kind='provider_orphan' AND owner_id=? AND content_hash IN (?,?)`, aID, loose.Hash, owned.Hash).Scan(&count)).To(Succeed())
		Expect(count).To(BeZero(), "actual retained terminal proof prevents repinning even without the carrier row")
		Expect(db.QueryRow(`SELECT count(*) FROM object_pins WHERE owner_kind='provider_orphan' AND content_hash=?`, unknown.Hash).Scan(&count)).To(Succeed())
		Expect(count).To(Equal(2))
		Expect(db.QueryRow(`SELECT count(*) FROM object_pins WHERE content_hash=?`, result.Hash).Scan(&count)).To(Succeed())
		Expect(count).To(Equal(1))
		Expect(db.QueryRow(`SELECT count(*) FROM object_pins WHERE pin_id='independent-snapshot'`).Scan(&count)).To(Succeed())
		Expect(count).To(Equal(1))
		var historicalJob, historicalState, resolutionJob string
		Expect(db.QueryRow(`SELECT json_extract(value,'$.job_id'),json_extract(value,'$.job_state'),json_extract(value,'$.resolution.job_id') FROM provider_orphans WHERE attempt_id=?`, aID).Scan(&historicalJob, &historicalState, &resolutionJob)).To(Succeed())
		Expect(historicalJob).To(Equal(missingJob))
		Expect(historicalState).To(Equal("paused"))
		Expect(resolutionJob).To(Equal(resolver))
		Expect(db.Close()).To(Succeed())
		inventory.Reset()
		Expect(sqlite.RestoreOutstanding(ctx, path, func(record json.RawMessage) error { _, e := inventory.Write(append(record, '\n')); return e })).To(Succeed())
	}
	Expect(restarted.Write(ctx, func(tx app.WriteTx) error { return tx.ReleasePin("independent-snapshot") })).To(Succeed())
	Expect(restarted.Collect(ctx, owned)).To(Succeed())
	Expect(restarted.Collect(ctx, result)).To(MatchError(domain.ErrPinned))
	Expect(restarted.Collect(ctx, unknown)).To(MatchError(domain.ErrPinned))
})

var _ = It("PA32 actual protected restore retains only verified copied orphan bytes despite unowned ready metadata", Label("persistence"), func() {
	f := newFixture(GinkgoT())
	ownedText, unownedText := "independently retained source", "ready but outside backup ownership"
	owned, e := f.store.Put(ctx, strings.NewReader(ownedText), func(tx app.WriteTx, object domain.Object) error {
		return tx.AcquirePin(domain.Pin{ID: "availability-owned", Kind: "selected_snapshot", OwnerID: "independent-owner", Hash: object.Hash})
	})
	Expect(e).NotTo(HaveOccurred())
	unowned, e := f.store.Put(ctx, strings.NewReader(unownedText), func(app.WriteTx, domain.Object) error { return nil })
	Expect(e).NotTo(HaveOccurred())
	accepted := createBackup(f, filepath.Join(GinkgoT().TempDir(), "backups"))
	Expect(copyTurns(f, accepted.JobID, 200).State).To(Equal("completed"))
	var backup domain.CopySet
	Expect(f.store.Write(ctx, func(tx app.WriteTx) error { var e error; backup, e = tx.CopySet(accepted.ID); return e })).To(Succeed())
	_, e = foundation.VerifyBackup(ctx, backup.Directory)
	Expect(e).NotTo(HaveOccurred())
	_, e = os.Stat(filepath.Join(backup.Directory, "objects", owned.Hash))
	Expect(e).NotTo(HaveOccurred())
	_, e = os.Stat(filepath.Join(backup.Directory, "objects", unowned.Hash))
	Expect(os.IsNotExist(e)).To(BeTrue(), "an unowned ready row does not put its bytes in the backup")
	probe, e := sql.Open("sqlite", filepath.Join(backup.Directory, "database.sqlite"))
	Expect(e).NotTo(HaveOccurred())
	var ready string
	Expect(probe.QueryRow(`SELECT state FROM content_objects WHERE content_hash=?`, unowned.Hash).Scan(&ready)).To(Succeed())
	Expect(ready).To(Equal("ready"))
	Expect(probe.Close()).To(Succeed())
	newer, e := f.store.Put(ctx, strings.NewReader("newer bytes absent from older database"), func(app.WriteTx, domain.Object) error { return nil })
	Expect(e).NotTo(HaveOccurred())

	selection := service.Selection{Profile: "availability", DataDir: filepath.Join(GinkgoT().TempDir(), "restored")}
	_, e = service.Initialize(selection)
	Expect(e).NotTo(HaveOccurred())
	w, e := service.OpenWriter(selection)
	Expect(e).NotTo(HaveOccurred())
	Expect(w.PrepareStorageBootstrap(nil)).To(Succeed())
	prior := foundation.New(nil)
	_, closePrior, e := prior.Prepare(ctx, w.Profile)
	Expect(e).NotTo(HaveOccurred())
	Expect(w.MarkStorageReady(nil)).To(Succeed())
	unknownID, unavailableResultID, retainedResultID := key(GinkgoT()), key(GinkgoT()), key(GinkgoT())
	observed := "2026-01-01T01:00:00+01:00"
	inputs := []domain.ProviderInput{{AttemptID: unknownID, ReadID: key(GinkgoT()), Hash: owned.Hash, ObservedOrigin: "daemon_supplied", ObservedAt: &observed}, {AttemptID: unknownID, ReadID: key(GinkgoT()), Hash: unowned.Hash}, {AttemptID: unknownID, ReadID: key(GinkgoT()), Hash: newer.Hash}}
	var inventory bytes.Buffer
	encode := json.NewEncoder(&inventory)
	Expect(encode.Encode(map[string]any{"attempt_id": unknownID, "request_sha256": strings.Repeat("a", 64), "state": "outcome_unknown", "cancel_requested": true, "job_state": "paused", "inputs": inputs})).To(Succeed())
	Expect(encode.Encode(map[string]any{"attempt_id": unavailableResultID, "request_sha256": strings.Repeat("b", 64), "state": "result_saved", "job_state": "paused", "result_hash": unowned.Hash})).To(Succeed())
	Expect(encode.Encode(map[string]any{"attempt_id": retainedResultID, "request_sha256": strings.Repeat("c", 64), "state": "result_saved", "job_state": "paused", "result_hash": owned.Hash})).To(Succeed())
	// Explicit synthetic destination facts; no provider call or byte disclosure.
	Expect(sqlite.RetainProviderOrphans(ctx, filepath.Join(selection.DataDir, "storage/library.sqlite"), bytes.NewReader(inventory.Bytes()), nil)).To(Succeed())
	Expect(closePrior()).To(Succeed())
	Expect(w.Close()).To(Succeed())
	plan, e := foundation.PlanRestore(ctx, selection, backup.Directory)
	Expect(e).NotTo(HaveOccurred())
	_, e = foundation.ApplyRestore(ctx, selection, plan.ID, config.Validate)
	Expect(e).NotTo(HaveOccurred(), "missing unowned bytes must not become required orphan owners and break restored health")

	w, e = service.OpenWriter(selection)
	Expect(e).NotTo(HaveOccurred())
	defer w.Close()
	restored := foundation.New(nil)
	_, closeRestored, e := restored.Prepare(ctx, w.Profile)
	Expect(e).NotTo(HaveOccurred())
	defer closeRestored()
	var content []byte
	Expect(restored.Read(ctx, owned, domain.Pin{Kind: "test_read", OwnerID: "availability-check"}, func(reader io.ReadSeeker) error { var e error; content, e = io.ReadAll(reader); return e })).To(Succeed())
	Expect(content).To(Equal([]byte(ownedText)))
	Expect(restored.Read(ctx, unowned, domain.Pin{Kind: "test_read", OwnerID: "unavailable-check"}, func(io.ReadSeeker) error { Fail("missing bytes must not be delivered"); return nil })).NotTo(Succeed())
	dbPath := filepath.Join(selection.DataDir, "storage/library.sqlite")
	db, e := sql.Open("sqlite", dbPath)
	Expect(e).NotTo(HaveOccurred())
	defer db.Close()
	var count int
	Expect(db.QueryRow(`SELECT count(*) FROM object_pins WHERE owner_kind='provider_orphan' AND owner_id=? AND content_hash=?`, unknownID, owned.Hash).Scan(&count)).To(Succeed())
	Expect(count).To(Equal(1))
	Expect(db.QueryRow(`SELECT count(*) FROM object_pins WHERE owner_kind='provider_orphan' AND owner_id=? AND content_hash=?`, retainedResultID, owned.Hash).Scan(&count)).To(Succeed())
	Expect(count).To(Equal(1))
	Expect(db.QueryRow(`SELECT count(*) FROM object_pins WHERE owner_kind='provider_orphan' AND content_hash IN (?,?)`, unowned.Hash, newer.Hash).Scan(&count)).To(Succeed())
	Expect(count).To(BeZero())
	Expect(db.QueryRow(`SELECT count(*) FROM object_pins WHERE pin_id='availability-owned' AND owner_kind='selected_snapshot' AND owner_id='independent-owner'`).Scan(&count)).To(Succeed())
	Expect(count).To(Equal(1))
	Expect(db.QueryRow(`SELECT json_array_length(value,'$.inputs') FROM provider_orphans WHERE attempt_id=?`, unknownID).Scan(&count)).To(Succeed())
	Expect(count).To(Equal(3), "unavailable input identities remain accounted without invented byte ownership")
	var restoredObserved string
	Expect(db.QueryRow(`SELECT json_extract(value,'$.inputs[0].observed_at') FROM provider_orphans WHERE attempt_id=?`, unknownID).Scan(&restoredObserved)).To(Succeed())
	Expect(restoredObserved).To(Equal(observed))
	Expect(db.QueryRow(`SELECT count(*) FROM provider_orphans,json_each(provider_orphans.value,'$.inputs') AS i WHERE attempt_id=? AND json_extract(i.value,'$.observed_at') IS NULL`, unknownID).Scan(&count)).To(Succeed())
	Expect(count).To(Equal(2))
	Expect(db.QueryRow(`SELECT count(*) FROM content_objects WHERE content_hash=?`, newer.Hash).Scan(&count)).To(Succeed())
	Expect(count).To(BeZero())
	Expect(sqlite.CheckCopyDatabase(ctx, dbPath)).To(Succeed())
})

// The normal foundation verifier stays authoritative; this wrapper establishes
// only a deterministic current-owner change before the callback writer begins.
type providerContinuationReadBoundary struct {
	*foundation.Store
	resultHash  string
	beforeClaim func() error
	returned    chan struct{}
}

func (b providerContinuationReadBoundary) Read(ctx context.Context, o domain.Object, pin domain.Pin, consume func(io.ReadSeeker) error) error {
	if o.Hash == b.resultHash && b.returned != nil {
		defer close(b.returned)
	}
	return b.Store.Read(ctx, o, pin, func(reader io.ReadSeeker) error {
		if o.Hash == b.resultHash && b.beforeClaim != nil {
			if e := b.beforeClaim(); e != nil {
				return e
			}
		}
		return consume(reader)
	})
}

var _ = It("PA58 last continue never dispatches when real saved bytes or inputs vanish and stale read callbacks cannot change a newer owner", Label("persistence"), func() {
	for _, change := range []string{"missing-result", "corrupt-result", "missing-input", "corrupt-input", "permissions", "assignment", "cancel"} {
		f := newFixture(GinkgoT())
		source, e := f.store.Put(ctx, strings.NewReader("exact captured continuation source"), func(app.WriteTx, domain.Object) error { return nil })
		Expect(e).NotTo(HaveOccurred())
		job, attempt, targetID, readID := key(GinkgoT()), key(GinkgoT()), key(GinkgoT()), key(GinkgoT())
		var a domain.ProviderAttempt
		Expect(f.store.Write(ctx, func(tx app.WriteTx) error {
			owner, e := tx.AuthState()
			if e != nil {
				return e
			}
			lib, e := tx.Library()
			if e != nil {
				return e
			}
			project, e := tx.Scope(f.project)
			if e != nil {
				return e
			}
			if e = tx.PutTargetSet(domain.TargetSet{ID: targetID, ProjectID: f.project, RequestedSelection: []byte(`{"kind":"pinned"}`), TargetsJSON: []byte(`[]`), CapturedAt: "fixture"}); e != nil {
				return e
			}
			if e = tx.PutCapturedRead(domain.CapturedRead{ID: readID, TargetSetID: targetID, Basis: "project_source", SourceJSON: []byte(`{}`), LocationJSON: []byte(`{}`), BytesSHA: source.Hash, ConfigRevision: 1, PermissionsRevision: project.Project.PermissionsRevision, CreatedAt: "fixture"}); e != nil {
				return e
			}
			target, _ := json.Marshal(map[string]string{"project_id": f.project})
			scope, _ := json.Marshal(domain.ProviderScope{PrincipalID: owner.OwnerID, AuthEpoch: owner.AuthEpoch, Target: target, TargetSetID: targetID, ReadIDs: []string{readID}, RequestSHA: strings.Repeat("f", 64)})
			if e = tx.CreateJob(domain.Job{ID: job, Kind: "question", Scope: scope, Permissions: map[string]int64{f.project: project.Project.PermissionsRevision}}); e != nil {
				return e
			}
			j, e := tx.ClaimJob(job)
			if e != nil {
				return e
			}
			a = domain.ProviderAttempt{ID: attempt, JobID: job, Generation: j.AttemptGeneration, State: "prepared", Runtime: lib.RuntimeGeneration, RequestSHA: strings.Repeat("f", 64), TargetSetID: targetID}
			if e = tx.PrepareProviderAttempt(a, []domain.ProviderInput{{AttemptID: attempt, ReadID: readID, Hash: source.Hash}}); e != nil {
				return e
			}
			return tx.DispatchProviderAttempt(attempt)
		})).To(Succeed())
		result, e := f.store.Put(ctx, strings.NewReader("complete retained result"), func(tx app.WriteTx, o domain.Object) error { return tx.SaveProviderResult(attempt, o) })
		Expect(e).NotTo(HaveOccurred())
		Expect(f.store.Write(ctx, func(tx app.WriteTx) error { return tx.PauseProviderJob(job, "saved_result_requires_continuation") })).To(Succeed())
		resume := func(decision string) error {
			return f.store.Write(ctx, func(tx app.WriteTx) error {
				j, e := tx.Job(job)
				if e != nil {
					return e
				}
				_, e = tx.ResumeProviderJob(job, j.AttemptGeneration, decision)
				return e
			})
		}
		Expect(resume("retry")).To(Succeed())
		nilDriver := work.ProviderCoordinator{Store: f.store}
		Expect(nilDriver.Process(ctx, job)).To(Succeed())
		Expect(resume("continue")).To(Succeed())
		expected, e := f.store.Job(ctx, job)
		Expect(e).NotTo(HaveOccurred())
		d := &orphanStatusDriver{}
		applies := 0
		boundary := providerContinuationReadBoundary{Store: f.store, resultHash: result.Hash}
		if strings.Contains(change, "result") || strings.Contains(change, "input") {
			object := result
			if strings.Contains(change, "input") {
				object = source
			}
			file := filepath.Join(f.profileDir, "storage", "objects", object.Hash)
			if strings.HasPrefix(change, "missing") {
				Expect(os.Remove(file)).To(Succeed())
			} else {
				Expect(os.WriteFile(file, []byte("corrupted bytes"), 0600)).To(Succeed())
			}
		} else {
			boundary.beforeClaim = func() error {
				return f.store.Write(ctx, func(tx app.WriteTx) error {
					switch change {
					case "assignment":
						_, e := tx.ClaimJob(job)
						return e
					case "cancel":
						_, e := tx.CancelJob(job)
						return e
					default:
						p, e := tx.Scope(f.project)
						if e != nil {
							return e
						}
						return tx.ApplyProjectConfiguration(f.project, "", "", true, p.Project.PermissionsRevision)
					}
				})
			}
		}
		p := work.ProviderCoordinator{Store: f.store, Bytes: boundary, Driver: d, Apply: func(app.WriteTx, domain.ProviderAttempt) error { applies++; return nil }}
		if change == "assignment" {
			boundary.returned = make(chan struct{})
			p.Bytes = boundary
			Expect(p.ScheduleProvider(ctx, job)).To(Succeed())
			Eventually(boundary.returned, 30*time.Second).Should(BeClosed())
			// A valid local job cannot enter the provider phase. While the old phase
			// owns the slot Schedule yields; its precise kind refusal proves Poll has
			// drained that phase without creating another provider execution.
			probe := key(GinkgoT())
			Expect(f.store.Write(ctx, func(tx app.WriteTx) error { return tx.CreateJob(domain.Job{ID: probe, Kind: "import"}) })).To(Succeed())
			Eventually(func() error {
				if e := p.PollProvider(ctx); e != nil {
					return e
				}
				return p.ScheduleProvider(ctx, probe)
			}, 30*time.Second).Should(MatchError(domain.ErrTransition))
			Expect(p.StopProviders()).To(Succeed())
		} else {
			Expect(p.Process(ctx, job)).NotTo(Succeed())
		}
		j, e := f.store.Job(ctx, job)
		Expect(e).NotTo(HaveOccurred())
		Expect(applies).To(BeZero())
		Expect(d.executes).To(BeZero())
		Expect(d.lookups).To(BeZero())
		Expect(j.Result).To(BeEmpty())
		switch change {
		case "assignment":
			Expect(j.State).To(Equal("running"))
			Expect(j.AttemptGeneration).To(Equal(expected.AttemptGeneration + 1))
		case "cancel":
			Expect(j.State).To(Equal("cancelled"))
			Expect(j.CancelRequested).To(BeTrue())
		default:
			Expect(j.State).To(Equal("paused"))
			Expect(j.AttemptGeneration).To(Equal(expected.AttemptGeneration))
			want := "saved_result_unavailable"
			switch change {
			case "missing-input":
				want = "captured_input_unavailable"
			case "corrupt-input":
				want = "captured_input_invalid"
			case "permissions":
				want = "authority_reconciliation_required"
			}
			Expect(j.Error).To(Equal(want))
		}
		var pins int
		db, e := sql.Open("sqlite", f.dbPath)
		Expect(e).NotTo(HaveOccurred())
		Expect(db.QueryRow(`SELECT count(*) FROM object_pins WHERE owner_kind='provider_attempt' AND owner_id=?`, attempt).Scan(&pins)).To(Succeed())
		Expect(db.Close()).To(Succeed())
		Expect(pins).To(BeNumerically(">=", 1))
	}
})

var _ = It("PA66 settled never-dispatched orphan carriers release only execution pins through real GC and older snapshot retention", Label("persistence"), func() {
	f := newFixture(GinkgoT())
	loose, e := f.store.Put(ctx, strings.NewReader("never-dispatched execution input"), func(app.WriteTx, domain.Object) error { return nil })
	Expect(e).NotTo(HaveOccurred())
	owned, e := f.store.Put(ctx, strings.NewReader("independent selected input"), func(tx app.WriteTx, o domain.Object) error {
		return tx.AcquirePin(domain.Pin{ID: "no-call-independent", Kind: "selected_snapshot", OwnerID: "independent", Hash: o.Hash})
	})
	Expect(e).NotTo(HaveOccurred())
	var owner domain.AuthState
	Expect(f.store.Write(ctx, func(tx app.WriteTx) error { var e error; owner, e = tx.AuthState(); return e })).To(Succeed())
	target, _ := json.Marshal(map[string]string{"project_id": f.project})
	sha := strings.Repeat("e", 64)
	attempt := key(GinkgoT())
	raw, _ := json.Marshal(map[string]any{"attempt_id": attempt, "job_id": "absent-never-dispatched-job", "principal_id": owner.OwnerID, "target": json.RawMessage(target), "request_sha256": sha, "state": "cancelled", "reason": "cancel_before_dispatch", "may_have_dispatched": false, "job_state": "paused", "inputs": []domain.ProviderInput{{AttemptID: attempt, ReadID: "loose", Hash: loose.Hash}, {AttemptID: attempt, ReadID: "owned", Hash: owned.Hash}}})
	available := func(o domain.Object) (bool, error) { return o.Hash == loose.Hash || o.Hash == owned.Hash, nil }
	Expect(sqlite.RetainProviderOrphans(ctx, f.dbPath, bytes.NewReader(raw), available)).To(Succeed())
	older := filepath.Join(GinkgoT().TempDir(), "no-call-older.sqlite")
	Expect(f.store.SnapshotDatabase(ctx, older)).To(Succeed())
	Expect(f.store.Collect(ctx, loose)).To(MatchError(domain.ErrPinned))
	job := key(GinkgoT())
	scope, _ := json.Marshal(domain.ProviderScope{PrincipalID: owner.OwnerID, AuthEpoch: owner.AuthEpoch, Target: target, RequestSHA: sha})
	Expect(f.store.Write(ctx, func(tx app.WriteTx) error {
		if e := tx.CreateJob(domain.Job{ID: job, Kind: "question", Scope: scope}); e != nil {
			return e
		}
		return tx.AuthorizeProviderReconciliation(job, []string{attempt})
	})).To(Succeed())
	d := &orphanStatusDriver{}
	p := work.ProviderCoordinator{Store: f.store, Driver: d}
	Expect(p.Process(ctx, job)).To(Succeed())
	j, e := f.store.Job(ctx, job)
	Expect(e).NotTo(HaveOccurred())
	Expect(j.State).To(Equal("completed"))
	Expect(d.executes).To(BeZero())
	Expect(d.lookups).To(BeZero())
	Expect(f.store.Collect(ctx, loose)).To(Succeed())
	Expect(f.store.Collect(ctx, owned)).To(MatchError(domain.ErrPinned))
	var inventory bytes.Buffer
	Expect(sqlite.RestoreOutstanding(ctx, f.dbPath, func(raw json.RawMessage) error { inventory.Write(raw); inventory.WriteByte('\n'); return nil })).To(Succeed())
	for turn := 0; turn < 2; turn++ {
		Expect(sqlite.RetainProviderOrphans(ctx, older, bytes.NewReader(inventory.Bytes()), func(o domain.Object) (bool, error) { return o.Hash == owned.Hash, nil })).To(Succeed())
		db, e := sql.Open("sqlite", older)
		Expect(e).NotTo(HaveOccurred())
		var pins int
		Expect(db.QueryRow(`SELECT count(*) FROM object_pins WHERE owner_kind='provider_orphan' AND owner_id=?`, attempt).Scan(&pins)).To(Succeed())
		Expect(pins).To(BeZero())
		Expect(db.Close()).To(Succeed())
		inventory.Reset()
		Expect(sqlite.RestoreOutstanding(ctx, older, func(raw json.RawMessage) error { inventory.Write(raw); inventory.WriteByte('\n'); return nil })).To(Succeed())
	}
	Expect(f.store.Write(ctx, func(tx app.WriteTx) error { return tx.ReleasePin("no-call-independent") })).To(Succeed())
	Expect(f.store.Collect(ctx, owned)).To(Succeed())
})

type continuationCrashRequest struct {
	Selection service.Selection
	Job       string
}
type continuationCrashBytes struct{ *foundation.Store }

func (b continuationCrashBytes) Read(ctx context.Context, o domain.Object, pin domain.Pin, consume func(io.ReadSeeker) error) error {
	return b.Store.Read(ctx, o, pin, func(reader io.ReadSeeker) error {
		if _, e := os.Stdout.WriteString("continuation-read-owned\n"); e != nil {
			return e
		}
		control := os.NewFile(3, "parent-owned-read-barrier")
		defer control.Close()
		var signal [1]byte
		if _, e := control.Read(signal[:]); e != nil {
			return e
		}
		return consume(reader)
	})
}
func init() {
	if os.Getenv("HAERYONG_PROVIDER_CONTINUATION_CHILD") != "1" {
		return
	}
	var request continuationCrashRequest
	if e := json.NewDecoder(os.Stdin).Decode(&request); e != nil {
		panic(e)
	}
	writer, e := service.OpenWriter(request.Selection)
	if e != nil {
		panic(e)
	}
	defer writer.Close()
	store := foundation.New(nil)
	_, closeStore, e := store.Prepare(context.Background(), writer.Profile)
	if e != nil {
		panic(e)
	}
	defer closeStore()
	p := work.ProviderCoordinator{Store: store, Bytes: continuationCrashBytes{store}}
	if e = p.Process(context.Background(), request.Job); e != nil {
		panic(e)
	}
	panic("read barrier unexpectedly completed")
}

var _ = It("PA69 actual SIGKILL during verified continuation Read reclaims only dead reader pins at exclusive reopen", Label("persistence"), func() {
	selection := service.Selection{Profile: "continuation-crash", DataDir: filepath.Join(GinkgoT().TempDir(), "library")}
	_, e := service.Initialize(selection)
	Expect(e).NotTo(HaveOccurred())
	writer, e := service.OpenWriter(selection)
	Expect(e).NotTo(HaveOccurred())
	defer writer.Close()
	Expect(writer.PrepareStorageBootstrap(nil)).To(Succeed())
	store := foundation.New(nil)
	_, closeStore, e := store.Prepare(ctx, writer.Profile)
	Expect(e).NotTo(HaveOccurred())
	defer closeStore()
	Expect(writer.MarkStorageReady(nil)).To(Succeed())
	auth := appauth.New(store)
	pair, e := auth.CreatePairing(ctx, "http://127.0.0.1:9270", "continuation crash fixture", "test")
	Expect(e).NotTo(HaveOccurred())
	_, e = auth.BootstrapDecide(ctx, domain.PairingDecision{ID: pair.ID, Code: pair.Code, Origin: pair.Origin, ETag: pair.ETag, OperationID: key(GinkgoT()), Action: "approve"})
	Expect(e).NotTo(HaveOccurred())
	loose, e := store.Put(ctx, strings.NewReader("crash-only execution input"), func(app.WriteTx, domain.Object) error { return nil })
	Expect(e).NotTo(HaveOccurred())
	owned, e := store.Put(ctx, strings.NewReader("independently retained crash input"), func(tx app.WriteTx, o domain.Object) error {
		return tx.AcquirePin(domain.Pin{ID: "crash-independent", Kind: "selected_snapshot", OwnerID: "independent", Hash: o.Hash})
	})
	Expect(e).NotTo(HaveOccurred())
	unknown, e := store.Put(ctx, strings.NewReader("unknown effect still owns this input"), func(app.WriteTx, domain.Object) error { return nil })
	Expect(e).NotTo(HaveOccurred())
	job, attempt, targetID := key(GinkgoT()), key(GinkgoT()), key(GinkgoT())
	readIDs := []string{key(GinkgoT()), key(GinkgoT())}
	Expect(store.Write(ctx, func(tx app.WriteTx) error {
		owner, e := tx.AuthState()
		if e != nil {
			return e
		}
		lib, e := tx.Library()
		if e != nil {
			return e
		}
		if e = tx.PutTargetSet(domain.TargetSet{ID: targetID, ProjectID: lib.UnfiledProjectID, RequestedSelection: []byte(`{"kind":"pinned"}`), TargetsJSON: []byte(`[]`), CapturedAt: "fixture"}); e != nil {
			return e
		}
		objects := []domain.Object{loose, owned}
		inputs := []domain.ProviderInput{}
		for index, o := range objects {
			if e = tx.PutCapturedRead(domain.CapturedRead{ID: readIDs[index], TargetSetID: targetID, Basis: "project_source", SourceJSON: []byte(`{}`), LocationJSON: []byte(`{}`), BytesSHA: o.Hash, ConfigRevision: 1, PermissionsRevision: 1, CreatedAt: "fixture"}); e != nil {
				return e
			}
			inputs = append(inputs, domain.ProviderInput{AttemptID: attempt, ReadID: readIDs[index], Hash: o.Hash})
		}
		target, _ := json.Marshal(map[string]string{"project_id": lib.UnfiledProjectID})
		scope, _ := json.Marshal(domain.ProviderScope{PrincipalID: owner.OwnerID, AuthEpoch: owner.AuthEpoch, Target: target, TargetSetID: targetID, ReadIDs: readIDs, RequestSHA: strings.Repeat("a", 64)})
		if e = tx.CreateJob(domain.Job{ID: job, Kind: "question", Scope: scope, Permissions: map[string]int64{lib.UnfiledProjectID: 1}}); e != nil {
			return e
		}
		j, e := tx.ClaimJob(job)
		if e != nil {
			return e
		}
		a := domain.ProviderAttempt{ID: attempt, JobID: job, Generation: j.AttemptGeneration, State: "prepared", Runtime: lib.RuntimeGeneration, RequestSHA: strings.Repeat("a", 64), TargetSetID: targetID}
		if e = tx.PrepareProviderAttempt(a, inputs); e != nil {
			return e
		}
		return tx.DispatchProviderAttempt(attempt)
	})).To(Succeed())
	result, e := store.Put(ctx, strings.NewReader("complete crash-retained result"), func(tx app.WriteTx, o domain.Object) error { return tx.SaveProviderResult(attempt, o) })
	Expect(e).NotTo(HaveOccurred())
	Expect(store.Write(ctx, func(tx app.WriteTx) error {
		if e := tx.PauseProviderJob(job, "saved_result_requires_continuation"); e != nil {
			return e
		}
		j, e := tx.Job(job)
		if e != nil {
			return e
		}
		_, e = tx.ResumeProviderJob(job, j.AttemptGeneration, "continue")
		return e
	})).To(Succeed())
	raw, _ := json.Marshal(map[string]any{"attempt_id": "unknown-read-owner", "request_sha256": strings.Repeat("b", 64), "state": "outcome_unknown", "inputs": []domain.ProviderInput{{AttemptID: "unknown-read-owner", ReadID: "unknown", Hash: unknown.Hash}}})
	dbPath := filepath.Join(selection.DataDir, "storage/library.sqlite")
	Expect(sqlite.RetainProviderOrphans(ctx, dbPath, bytes.NewReader(raw), func(o domain.Object) (bool, error) { return o.Hash == unknown.Hash, nil })).To(Succeed())
	Expect(closeStore()).To(Succeed())
	Expect(writer.Close()).To(Succeed())
	controlRead, controlWrite, e := os.Pipe()
	Expect(e).NotTo(HaveOccurred())
	defer controlRead.Close()
	defer controlWrite.Close()
	command := exec.Command(os.Args[0], "-test.run=^TestSuite$", "-test.timeout=90s")
	command.Env = append(os.Environ(), "HAERYONG_PROVIDER_CONTINUATION_CHILD=1")
	request, _ := json.Marshal(continuationCrashRequest{selection, job})
	command.Stdin = bytes.NewReader(request)
	command.ExtraFiles = []*os.File{controlRead}
	var diagnostics bytes.Buffer
	command.Stderr = &diagnostics
	stdout, e := command.StdoutPipe()
	Expect(e).NotTo(HaveOccurred())
	Expect(command.Start()).To(Succeed())
	defer func() {
		if command.ProcessState == nil {
			_ = command.Process.Kill()
			_ = command.Wait()
		}
	}()
	reached := make(chan string, 1)
	go func() { line, _ := bufio.NewReader(stdout).ReadString('\n'); reached <- line }()
	Eventually(reached, 30*time.Second).Should(Receive(Equal("continuation-read-owned\n")))
	_, e = service.OpenWriter(selection)
	Expect(e).To(HaveOccurred(), "the actual child owns the profile writer lock")
	probe, e := sql.Open("sqlite", dbPath)
	Expect(e).NotTo(HaveOccurred())
	var pins int
	Expect(probe.QueryRow(`SELECT count(*) FROM object_pins WHERE owner_kind='provider_continuation' AND owner_id=?`, attempt).Scan(&pins)).To(Succeed())
	Expect(pins).To(Equal(1))
	Expect(probe.Close()).To(Succeed())
	Expect(command.Process.Kill()).To(Succeed())
	Expect(command.Wait()).To(HaveOccurred())
	Expect(command.ProcessState).NotTo(BeNil())
	writer, e = service.OpenWriter(selection)
	Expect(e).NotTo(HaveOccurred())
	defer writer.Close()
	reopened := foundation.New(nil)
	_, closeReopened, e := reopened.Prepare(ctx, writer.Profile)
	Expect(e).NotTo(HaveOccurred())
	defer closeReopened()
	probe, e = sql.Open("sqlite", dbPath)
	Expect(e).NotTo(HaveOccurred())
	defer probe.Close()
	Expect(probe.QueryRow(`SELECT count(*) FROM object_pins WHERE owner_kind='provider_continuation'`).Scan(&pins)).To(Succeed())
	Expect(pins).To(BeZero())
	Expect(probe.QueryRow(`SELECT count(*) FROM object_pins WHERE owner_kind='provider_attempt' AND owner_id=?`, attempt).Scan(&pins)).To(Succeed())
	Expect(pins).To(Equal(3), "live attempt input/result ownership survives dead-reader cleanup")
	Expect(reopened.Collect(ctx, loose)).To(MatchError(domain.ErrPinned))
	Expect(reopened.Write(ctx, func(tx app.WriteTx) error { _, e := tx.CancelJob(job); return e })).To(Succeed())
	Expect(reopened.Collect(ctx, loose)).To(Succeed())
	Expect(reopened.Collect(ctx, owned)).To(MatchError(domain.ErrPinned))
	Expect(reopened.Collect(ctx, result)).To(MatchError(domain.ErrPinned))
	Expect(reopened.Collect(ctx, unknown)).To(MatchError(domain.ErrPinned))
	Expect(reopened.Write(ctx, func(tx app.WriteTx) error { return tx.ReleasePin("crash-independent") })).To(Succeed())
	Expect(reopened.Collect(ctx, owned)).To(Succeed())
})

func providerRealSavedFixture(f *fixture) (domain.ProviderAttempt, domain.Object, domain.Object) {
	GinkgoHelper()
	source, e := f.store.Put(ctx, strings.NewReader("exact captured continuation source"), func(app.WriteTx, domain.Object) error { return nil })
	Expect(e).NotTo(HaveOccurred())
	job, attempt, targetID, readID := key(GinkgoT()), key(GinkgoT()), key(GinkgoT()), key(GinkgoT())
	var a domain.ProviderAttempt
	Expect(f.store.Write(ctx, func(tx app.WriteTx) error {
		owner, e := tx.AuthState()
		if e != nil {
			return e
		}
		lib, e := tx.Library()
		if e != nil {
			return e
		}
		project, e := tx.Scope(f.project)
		if e != nil {
			return e
		}
		if e = tx.PutTargetSet(domain.TargetSet{ID: targetID, ProjectID: f.project, RequestedSelection: []byte(`{"kind":"pinned"}`), TargetsJSON: []byte(`[]`), CapturedAt: "fixture"}); e != nil {
			return e
		}
		if e = tx.PutCapturedRead(domain.CapturedRead{ID: readID, TargetSetID: targetID, Basis: "project_source", SourceJSON: []byte(`{}`), LocationJSON: []byte(`{}`), BytesSHA: source.Hash, ConfigRevision: 1, PermissionsRevision: project.Project.PermissionsRevision, CreatedAt: "fixture"}); e != nil {
			return e
		}
		target, _ := json.Marshal(map[string]string{"project_id": f.project})
		scope, _ := json.Marshal(domain.ProviderScope{PrincipalID: owner.OwnerID, AuthEpoch: owner.AuthEpoch, Target: target, TargetSetID: targetID, ReadIDs: []string{readID}, RequestSHA: strings.Repeat("f", 64)})
		if e = tx.CreateJob(domain.Job{ID: job, Kind: "question", Scope: scope, Permissions: map[string]int64{f.project: project.Project.PermissionsRevision}}); e != nil {
			return e
		}
		j, e := tx.ClaimJob(job)
		if e != nil {
			return e
		}
		a = domain.ProviderAttempt{ID: attempt, JobID: job, Generation: j.AttemptGeneration, State: "prepared", Runtime: lib.RuntimeGeneration, RequestSHA: strings.Repeat("f", 64), TargetSetID: targetID}
		if e = tx.PrepareProviderAttempt(a, []domain.ProviderInput{{AttemptID: attempt, ReadID: readID, Hash: source.Hash}}); e != nil {
			return e
		}
		if e = tx.DispatchProviderAttempt(attempt); e != nil {
			return e
		}
		return tx.ObserveProviderAttempt(attempt, "running", "saved-turn", "saved-thread", "")
	})).To(Succeed())
	result, e := f.store.Put(ctx, strings.NewReader("complete retained result"), func(tx app.WriteTx, o domain.Object) error { return tx.SaveProviderResult(attempt, o) })
	Expect(e).NotTo(HaveOccurred())
	Expect(f.store.Write(ctx, func(tx app.WriteTx) error { return tx.PauseProviderJob(job, "saved_result_requires_continuation") })).To(Succeed())
	return a, source, result
}

var _ = It("PA75 real Foundation continuation verifies exact bytes then atomically claims applies and releases every reader pin", Label("persistence"), func() {
	f := newFixture(GinkgoT())
	a, source, result := providerRealSavedFixture(f)
	Expect(f.store.Write(ctx, func(tx app.WriteTx) error {
		j, e := tx.Job(a.JobID)
		if e != nil {
			return e
		}
		_, e = tx.ResumeProviderJob(j.ID, j.AttemptGeneration, "continue")
		return e
	})).To(Succeed())
	before, e := f.store.Job(ctx, a.JobID)
	Expect(e).NotTo(HaveOccurred())
	d := &orphanStatusDriver{}
	applies := 0
	p := work.ProviderCoordinator{Store: f.store, Bytes: f.store, Driver: d, Apply: func(tx app.WriteTx, got domain.ProviderAttempt) error {
		applies++
		Expect(got.ResultHash).To(Equal(result.Hash))
		Expect(got.ID).To(Equal(a.ID))
		return nil
	}}
	Expect(p.Process(ctx, a.JobID)).To(Succeed())
	j, e := f.store.Job(ctx, a.JobID)
	Expect(e).NotTo(HaveOccurred())
	Expect(j.State).To(Equal("completed"))
	Expect(j.AttemptGeneration).To(Equal(before.AttemptGeneration + 1))
	Expect(string(j.Result)).To(ContainSubstring(result.Hash))
	Expect(applies).To(Equal(1))
	Expect(d.executes).To(BeZero())
	Expect(d.lookups).To(BeZero())
	Expect(f.store.Write(ctx, func(tx app.WriteTx) error {
		got, e := tx.ProviderAttempt(a.ID)
		if e != nil {
			return e
		}
		Expect(got.Generation).To(Equal(a.Generation))
		Expect(got.RequestID).To(Equal("saved-turn"))
		Expect(got.SessionID).To(Equal("saved-thread"))
		Expect(got.Runtime).To(Equal(a.Runtime))
		Expect(got.ApplyGeneration).To(Equal(j.AttemptGeneration))
		return nil
	})).To(Succeed())
	var body []byte
	Expect(f.store.Read(ctx, result, domain.Pin{Kind: "test_read", OwnerID: "exact-result"}, func(r io.ReadSeeker) error { var e error; body, e = io.ReadAll(r); return e })).To(Succeed())
	Expect(string(body)).To(Equal("complete retained result"))
	db, e := sql.Open("sqlite", f.dbPath)
	Expect(e).NotTo(HaveOccurred())
	defer db.Close()
	var pins int
	Expect(db.QueryRow(`SELECT count(*) FROM object_pins WHERE owner_kind='provider_continuation'`).Scan(&pins)).To(Succeed())
	Expect(pins).To(BeZero())
	Expect(f.store.Collect(ctx, source)).To(Succeed())
	Expect(f.store.Collect(ctx, result)).To(MatchError(domain.ErrPinned))
})

var _ = It("PA76 normal preclaim context stop preserves the queued reservation and actual Foundation read-pin cleanup", Label("persistence"), func() {
	for _, stopPhase := range []bool{false, true} {
		f := newFixture(GinkgoT())
		a, _, result := providerRealSavedFixture(f)
		Expect(f.store.Write(ctx, func(tx app.WriteTx) error {
			j, e := tx.Job(a.JobID)
			if e != nil {
				return e
			}
			_, e = tx.ResumeProviderJob(j.ID, j.AttemptGeneration, "continue")
			return e
		})).To(Succeed())
		before, e := f.store.Job(ctx, a.JobID)
		Expect(e).NotTo(HaveOccurred())
		turn, cancel := context.WithCancel(ctx)
		entered := make(chan struct{})
		d := &orphanStatusDriver{}
		applies := 0
		boundary := providerContinuationReadBoundary{Store: f.store, resultHash: result.Hash, beforeClaim: func() error { close(entered); <-turn.Done(); return turn.Err() }}
		p := &work.ProviderCoordinator{Store: f.store, Bytes: boundary, Driver: d, Apply: func(app.WriteTx, domain.ProviderAttempt) error { applies++; return nil }}
		if stopPhase {
			Expect(p.ScheduleProvider(turn, a.JobID)).To(Succeed())
			Eventually(entered, 30*time.Second).Should(BeClosed())
			cancel()
			Expect(p.StopProviders()).To(Succeed())
		} else {
			done := make(chan error, 1)
			go func() { done <- p.Process(turn, a.JobID) }()
			Eventually(entered, 30*time.Second).Should(BeClosed())
			cancel()
			Eventually(done, 30*time.Second).Should(Receive(MatchError(context.Canceled)))
		}
		after, e := f.store.Job(ctx, a.JobID)
		Expect(e).NotTo(HaveOccurred())
		Expect(after).To(Equal(before))
		db, e := sql.Open("sqlite", f.dbPath)
		Expect(e).NotTo(HaveOccurred())
		var pins int
		Expect(db.QueryRow(`SELECT count(*) FROM object_pins WHERE owner_kind='provider_continuation'`).Scan(&pins)).To(Succeed())
		Expect(pins).To(BeZero())
		Expect(db.Close()).To(Succeed())
		Expect(d.executes).To(BeZero())
		Expect(d.lookups).To(BeZero())
		Expect(applies).To(BeZero())
		p.Bytes = f.store
		Expect(p.Process(ctx, a.JobID)).To(Succeed())
		Expect(applies).To(Equal(1))
		Expect(d.executes).To(BeZero())
	}
})

type providerBeforeSendBackup struct {
	prepare func() error
	calls   int
}

func (d *providerBeforeSendBackup) Execute(context.Context, domain.ProviderAttempt, []domain.ProviderInput, func() error, func(work.ProviderProgress) error) (work.ProviderOutcome, error) {
	if e := d.prepare(); e != nil {
		return work.ProviderOutcome{}, e
	}
	return work.ProviderOutcome{}, io.EOF // Explicit local startup failure; the send fence was never called.
}
func (*providerBeforeSendBackup) Cancel(context.Context, domain.ProviderAttempt) error { return nil }
func (*providerBeforeSendBackup) Reconcile(context.Context, domain.ProviderAttempt, bool) (work.ProviderOutcome, error) {
	return work.ProviderOutcome{}, domain.ErrTransition
}

var _ = It("PA79 actual prepared backup plus explicit no-call retention releases execution inputs only after normal owner terminalization", Label("persistence"), func() {
	f, selection, w, closeStore := providerOwnedProfileFixture()
	store := f.store
	var e error
	loose, e := store.Put(ctx, strings.NewReader("raw prepared execution-only bytes"), func(app.WriteTx, domain.Object) error { return nil })
	Expect(e).NotTo(HaveOccurred())
	owned, e := store.Put(ctx, strings.NewReader("raw prepared independent bytes"), func(tx app.WriteTx, o domain.Object) error {
		return tx.AcquirePin(domain.Pin{ID: "raw-independent", Kind: "selected_snapshot", OwnerID: "independent", Hash: o.Hash})
	})
	Expect(e).NotTo(HaveOccurred())
	unknown, e := store.Put(ctx, strings.NewReader("unknown effect bytes"), func(app.WriteTx, domain.Object) error { return nil })
	Expect(e).NotTo(HaveOccurred())
	_, _, result := providerRealSavedFixture(f) // Real durable result ownership independent of this operation.
	raw, _ := json.Marshal(map[string]any{"attempt_id": "raw-gc-unknown", "request_sha256": strings.Repeat("b", 64), "state": "outcome_unknown", "may_have_dispatched": true, "inputs": []domain.ProviderInput{{AttemptID: "raw-gc-unknown", ReadID: "unknown", Hash: unknown.Hash}}})
	Expect(sqlite.RetainProviderOrphans(ctx, f.dbPath, bytes.NewReader(raw), func(o domain.Object) (bool, error) { return o.Hash == unknown.Hash, nil })).To(Succeed())
	job, targetID := key(GinkgoT()), key(GinkgoT())
	reads := []string{key(GinkgoT()), key(GinkgoT())}
	Expect(store.Write(ctx, func(tx app.WriteTx) error {
		owner, e := tx.AuthState()
		if e != nil {
			return e
		}
		if e = tx.PutTargetSet(domain.TargetSet{ID: targetID, ProjectID: f.project, RequestedSelection: []byte(`{"kind":"pinned"}`), TargetsJSON: []byte(`[]`), CapturedAt: "fixture"}); e != nil {
			return e
		}
		for i, o := range []domain.Object{loose, owned} {
			if e = tx.PutCapturedRead(domain.CapturedRead{ID: reads[i], TargetSetID: targetID, Basis: "project_source", SourceJSON: []byte(`{}`), LocationJSON: []byte(`{}`), BytesSHA: o.Hash, ConfigRevision: 1, PermissionsRevision: 1, CreatedAt: "fixture"}); e != nil {
				return e
			}
		}
		target, _ := json.Marshal(map[string]string{"project_id": f.project})
		scope, _ := json.Marshal(domain.ProviderScope{PrincipalID: owner.OwnerID, AuthEpoch: owner.AuthEpoch, Target: target, TargetSetID: targetID, ReadIDs: reads, RequestSHA: strings.Repeat("c", 64)})
		return tx.CreateJob(domain.Job{ID: job, Kind: "question", Scope: scope, Permissions: map[string]int64{f.project: 1}})
	})).To(Succeed())
	var backup domain.CopySet
	var attempt string
	d := &providerBeforeSendBackup{prepare: func() error {
		var rows []domain.ProviderAttempt
		e := store.Write(ctx, func(tx app.WriteTx) error { var e error; rows, e = tx.ProviderAttempts(job); return e })
		if e != nil {
			return e
		}
		Expect(rows).To(HaveLen(1))
		Expect(rows[0].State).To(Equal("prepared"))
		attempt = rows[0].ID
		accepted := createBackup(f, filepath.Join(GinkgoT().TempDir(), "backups"))
		Expect(copyTurns(f, accepted.JobID, 200).State).To(Equal("completed"))
		return store.Write(ctx, func(tx app.WriteTx) error { var e error; backup, e = tx.CopySet(accepted.ID); return e })
	}}
	p := work.ProviderCoordinator{Store: store, Bytes: store, Driver: d}
	Expect(p.Process(ctx, job)).To(Succeed())
	Expect(store.Write(ctx, func(tx app.WriteTx) error {
		j, e := tx.Job(job)
		if e != nil {
			return e
		}
		_, e = tx.ResumeProviderJob(job, j.AttemptGeneration, "continue")
		return e
	})).To(Succeed())
	// The real owner continue cancels only the no-call attempt; the job is still queued/live.
	var rows []domain.ProviderAttempt
	Expect(store.Write(ctx, func(tx app.WriteTx) error { var e error; rows, e = tx.ProviderAttempts(job); return e })).To(Succeed())
	Expect(rows[0].State).To(Equal("cancelled"))
	Expect(rows[0].Reason).To(Equal("cancel_before_dispatch"))
	Expect(closeStore()).To(Succeed())
	Expect(w.Close()).To(Succeed())
	plan, e := foundation.PlanRestore(ctx, selection, backup.Directory)
	Expect(e).NotTo(HaveOccurred())
	_, e = foundation.ApplyRestore(ctx, selection, plan.ID, config.Validate)
	Expect(e).NotTo(HaveOccurred())
	w, e = service.OpenWriter(selection)
	Expect(e).NotTo(HaveOccurred())
	defer w.Close()
	restored := foundation.New(nil)
	_, closeRestored, e := restored.Prepare(ctx, w.Profile)
	Expect(e).NotTo(HaveOccurred())
	defer closeRestored()
	db, e := sql.Open("sqlite", f.dbPath)
	Expect(e).NotTo(HaveOccurred())
	defer db.Close()
	var state string
	var pins int
	Expect(db.QueryRow(`SELECT state FROM provider_attempts WHERE attempt_id=?`, attempt).Scan(&state)).To(Succeed())
	Expect(state).To(Equal("cancelled"))
	for _, kind := range []string{"provider_attempt", "provider_orphan"} {
		Expect(db.QueryRow(`SELECT count(*) FROM object_pins WHERE owner_kind=? AND owner_id=? AND content_hash=?`, kind, attempt, loose.Hash).Scan(&pins)).To(Succeed())
		Expect(pins).To(Equal(1))
	}
	Expect(restored.Collect(ctx, loose)).To(MatchError(domain.ErrPinned), "a restored nonterminal owner still needs its execution inputs")
	fresh := appauth.New(restored)
	pair, e := fresh.CreatePairing(ctx, "http://127.0.0.1:9270", "restored owner", "test")
	Expect(e).NotTo(HaveOccurred())
	_, e = fresh.BootstrapDecide(ctx, domain.PairingDecision{ID: pair.ID, Code: pair.Code, Origin: pair.Origin, ETag: pair.ETag, OperationID: key(GinkgoT()), Action: "approve"})
	Expect(e).NotTo(HaveOccurred())
	session, e := fresh.Poll(ctx, pair.ID, pair.PollingSecret, pair.Origin)
	Expect(e).NotTo(HaveOccurred())
	Expect(restored.Write(ctx, func(tx app.WriteTx) error {
		hold, e := tx.RecoveryHold()
		if e != nil {
			return e
		}
		return tx.ReviewRecovery(hold.ID, nil)
	})).To(Succeed())
	_, e = fresh.CancelJob(ctx, session.Token, job, key(GinkgoT()))
	Expect(e).NotTo(HaveOccurred())
	j, e := restored.Job(ctx, job)
	Expect(e).NotTo(HaveOccurred())
	Expect(j.State).To(Equal("cancelled"))
	for _, kind := range []string{"provider_attempt", "provider_orphan"} {
		Expect(db.QueryRow(`SELECT count(*) FROM object_pins WHERE owner_kind=? AND owner_id=? AND content_hash IN (?,?)`, kind, attempt, loose.Hash, owned.Hash).Scan(&pins)).To(Succeed())
		Expect(pins).To(BeZero())
	}
	// A genuinely new owner-authorized job can deliberately use the same exact
	// captured request after definitive no-call settlement. Cancel it normally
	// before GC so this control does not leave a new execution owner behind.
	nextJob, nextAttempt := key(GinkgoT()), key(GinkgoT())
	Expect(restored.Write(ctx, func(tx app.WriteTx) error {
		original, e := tx.Job(job)
		if e != nil {
			return e
		}
		var scope domain.ProviderScope
		if e = json.Unmarshal(original.Scope, &scope); e != nil {
			return e
		}
		owner, e := tx.AuthState()
		if e != nil {
			return e
		}
		lib, e := tx.Library()
		if e != nil {
			return e
		}
		scope.AuthEpoch = owner.AuthEpoch
		scope.PrincipalID = owner.OwnerID
		encoded, _ := json.Marshal(scope)
		if e = tx.CreateJob(domain.Job{ID: nextJob, Kind: "question", Scope: encoded, Permissions: original.Permissions}); e != nil {
			return e
		}
		claimed, e := tx.ClaimJob(nextJob)
		if e != nil {
			return e
		}
		a := domain.ProviderAttempt{ID: nextAttempt, JobID: nextJob, Generation: claimed.AttemptGeneration, Runtime: lib.RuntimeGeneration, State: "prepared", RequestSHA: scope.RequestSHA, TargetSetID: scope.TargetSetID}
		if e = tx.PrepareProviderAttempt(a, []domain.ProviderInput{{AttemptID: nextAttempt, ReadID: reads[0], Hash: loose.Hash}, {AttemptID: nextAttempt, ReadID: reads[1], Hash: owned.Hash}}); e != nil {
			return e
		}
		_, e = tx.CancelJob(nextJob)
		return e
	})).To(Succeed())

	Expect(restored.Collect(ctx, loose)).To(Succeed())
	_, e = os.Stat(filepath.Join(selection.DataDir, "storage/objects", loose.Hash))
	Expect(os.IsNotExist(e)).To(BeTrue())
	Expect(restored.Collect(ctx, owned)).To(MatchError(domain.ErrPinned))
	Expect(restored.Collect(ctx, unknown)).To(MatchError(domain.ErrPinned))
	Expect(restored.Collect(ctx, result)).To(MatchError(domain.ErrPinned))
	Expect(d.calls).To(BeZero())
})

func providerOwnedProfileFixture() (*fixture, service.Selection, *service.LockedProfile, func() error) {
	GinkgoHelper()
	selection := service.Selection{Profile: "prepared-gc", DataDir: filepath.Join(GinkgoT().TempDir(), "library")}
	_, e := service.Initialize(selection)
	Expect(e).NotTo(HaveOccurred())
	w, e := service.OpenWriter(selection)
	Expect(e).NotTo(HaveOccurred())
	DeferCleanup(func() { w.Close() })
	Expect(w.PrepareStorageBootstrap(nil)).To(Succeed())
	store := foundation.New(nil)
	_, closeStore, e := store.Prepare(ctx, w.Profile)
	Expect(e).NotTo(HaveOccurred())
	DeferCleanup(func() { closeStore() })
	Expect(w.MarkStorageReady(nil)).To(Succeed())
	auth := appauth.New(store)
	pair, e := auth.CreatePairing(ctx, "http://127.0.0.1:9270", "prepared GC fixture", "test")
	Expect(e).NotTo(HaveOccurred())
	_, e = auth.BootstrapDecide(ctx, domain.PairingDecision{ID: pair.ID, Code: pair.Code, Origin: pair.Origin, ETag: pair.ETag, OperationID: key(GinkgoT()), Action: "approve"})
	Expect(e).NotTo(HaveOccurred())
	poll, e := auth.Poll(ctx, pair.ID, pair.PollingSecret, pair.Origin)
	Expect(e).NotTo(HaveOccurred())
	health, e := store.Health(ctx)
	Expect(e).NotTo(HaveOccurred())
	f := &fixture{store: store, auth: auth, token: poll.Token, project: health.Library.UnfiledProjectID, profileDir: selection.DataDir, dbPath: filepath.Join(selection.DataDir, "storage/library.sqlite")}
	return f, selection, w, closeStore
}

type providerDedupResultDriver struct {
	body  string
	calls int
}

func (d *providerDedupResultDriver) Execute(ctx context.Context, a domain.ProviderAttempt, _ []domain.ProviderInput, send func() error, observe func(work.ProviderProgress) error) (work.ProviderOutcome, error) {
	if e := send(); e != nil {
		return work.ProviderOutcome{}, e
	}
	d.calls++
	if e := observe(work.ProviderProgress{RequestID: "dedup-turn", SessionID: "dedup-session"}); e != nil {
		return work.ProviderOutcome{}, e
	}
	return work.ProviderOutcome{State: "result_saved", CompletionObserved: true, Body: io.NopCloser(strings.NewReader(d.body))}, nil
}
func (*providerDedupResultDriver) Cancel(context.Context, domain.ProviderAttempt) error { return nil }
func (*providerDedupResultDriver) Reconcile(context.Context, domain.ProviderAttempt, bool) (work.ProviderOutcome, error) {
	return work.ProviderOutcome{}, domain.ErrTransition
}

var _ = It("PA83 real protected older restore retains a later orphan-only result whose identical bytes already have an independent owner", Label("persistence"), func() {
	f, selection, w, closeStore := providerOwnedProfileFixture()
	const body = "independently retained bytes equal later fake provider output"
	object, e := f.store.Put(ctx, strings.NewReader(body), func(tx app.WriteTx, o domain.Object) error {
		return tx.AcquirePin(domain.Pin{ID: "older-result-owner", Kind: "selected_snapshot", OwnerID: "independent", Hash: o.Hash})
	})
	Expect(e).NotTo(HaveOccurred())
	job := key(GinkgoT())
	Expect(f.store.Write(ctx, func(tx app.WriteTx) error {
		owner, e := tx.AuthState()
		if e != nil {
			return e
		}
		target, _ := json.Marshal(map[string]string{"project_id": f.project})
		scope, _ := json.Marshal(domain.ProviderScope{PrincipalID: owner.OwnerID, AuthEpoch: owner.AuthEpoch, Target: target, RequestSHA: strings.Repeat("d", 64)})
		return tx.CreateJob(domain.Job{ID: job, Kind: "question", Scope: scope, Permissions: map[string]int64{f.project: 1}})
	})).To(Succeed())
	accepted := createBackup(f, filepath.Join(GinkgoT().TempDir(), "backups"))
	Expect(copyTurns(f, accepted.JobID, 200).State).To(Equal("completed"))
	var backup domain.CopySet
	Expect(f.store.Write(ctx, func(tx app.WriteTx) error { var e error; backup, e = tx.CopySet(accepted.ID); return e })).To(Succeed())
	sentinel := errors.New("fixture semantic apply rollback")
	d := &providerDedupResultDriver{body: body}
	p := work.ProviderCoordinator{Store: f.store, Bytes: f.store, Driver: d, Apply: func(app.WriteTx, domain.ProviderAttempt) error { return sentinel }}
	Expect(p.Process(ctx, job)).To(MatchError(sentinel))
	var a domain.ProviderAttempt
	Expect(f.store.Write(ctx, func(tx app.WriteTx) error {
		rows, e := tx.ProviderAttempts(job)
		if e != nil {
			return e
		}
		Expect(rows).To(HaveLen(1))
		a = rows[0]
		Expect(a.State).To(Equal("result_saved"))
		Expect(a.ResultHash).To(Equal(object.Hash))
		return nil
	})).To(Succeed())
	Expect(closeStore()).To(Succeed())
	Expect(w.Close()).To(Succeed())
	plan, e := foundation.PlanRestore(ctx, selection, backup.Directory)
	Expect(e).NotTo(HaveOccurred())
	_, e = foundation.ApplyRestore(ctx, selection, plan.ID, config.Validate)
	Expect(e).NotTo(HaveOccurred())
	w, e = service.OpenWriter(selection)
	Expect(e).NotTo(HaveOccurred())
	defer w.Close()
	restored := foundation.New(nil)
	_, closeRestored, e := restored.Prepare(ctx, w.Profile)
	Expect(e).NotTo(HaveOccurred())
	defer closeRestored()
	Expect(restored.Write(ctx, func(tx app.WriteTx) error {
		hold, e := tx.RecoveryHold()
		if e != nil {
			return e
		}
		if e = tx.ReviewRecovery(hold.ID, nil); e != nil {
			return e
		}
		got, e := tx.ProviderAttempt(a.ID)
		if e != nil {
			return e
		}
		Expect(got.State).To(Equal("result_saved"))
		Expect(got.RequestID).To(Equal("dedup-turn"))
		Expect(got.ResultHash).To(Equal(object.Hash))
		return nil
	})).To(Succeed())
	// The independent global review does not release this restored job's hold.
	Expect(restored.Write(ctx, func(tx app.WriteTx) error {
		j, e := tx.Job(job)
		if e != nil {
			return e
		}
		_, e = tx.ResumeProviderJob(job, j.AttemptGeneration, "reconcile_only")
		return e
	})).To(Succeed())
	local := work.ProviderCoordinator{Store: restored}
	Expect(local.Process(ctx, job)).To(Succeed())
	Expect(restored.Write(ctx, func(tx app.WriteTx) error {
		j, e := tx.Job(job)
		if e != nil {
			return e
		}
		_, e = tx.ResumeProviderJob(job, j.AttemptGeneration, "continue")
		return e
	})).To(MatchError(domain.ErrTransition))
	var count int
	db, e := sql.Open("sqlite", f.dbPath)
	Expect(e).NotTo(HaveOccurred())
	defer db.Close()
	Expect(db.QueryRow(`SELECT count(*) FROM provider_attempts WHERE attempt_id=?`, a.ID).Scan(&count)).To(Succeed())
	Expect(count).To(BeZero())
	Expect(restored.Collect(ctx, object)).To(MatchError(domain.ErrPinned))
	Expect(d.calls).To(Equal(1), "this fixture made one local fake call; the restored candidate cannot execute it")
})
