package source_test

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"haeryong.local/haeryong/internal/adapters/ai/codex"
	"haeryong.local/haeryong/internal/application/source"
	app "haeryong.local/haeryong/internal/application/storage"
	"haeryong.local/haeryong/internal/application/work"
	"haeryong.local/haeryong/internal/domain"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"syscall"
	"time"
)

func codexAdmission(f *vFixture, job string, inv domain.ProviderInvocation, reads *[]domain.SourceCaptureRead) source.CaptureAdmission {
	return func(tx app.WriteTx, c domain.SourceCapture) error {
		var e error
		*reads, e = tx.SourceCaptureReads(c.ID, "", 64)
		if e != nil {
			return e
		}
		ids := []string{}
		captured := []domain.CapturedRead{}
		for _, read := range *reads {
			ids = append(ids, read.ID)
			r, e := tx.CapturedRead(read.ID)
			if e != nil {
				return e
			}
			captured = append(captured, r)
		}
		owner, e := tx.AuthState()
		if e != nil {
			return e
		}
		target, _ := json.Marshal(map[string]string{"project_id": f.project})
		hash, e := codex.SemanticRequestSHA(owner.OwnerID, target, inv, captured)
		if e != nil {
			return e
		}
		scope, _ := json.Marshal(domain.ProviderScope{ProviderID: "codex", PrincipalID: owner.OwnerID, AuthEpoch: owner.AuthEpoch, Target: target, TargetSetID: c.TargetSetID, ReadIDs: ids, RequestSHA: hash, Invocation: &inv})
		if e = tx.CreateJob(domain.Job{ID: job, Kind: "question", Scope: scope, ConfigRevision: c.ConfigRevision, Permissions: map[string]int64{f.project: c.PermissionsRevision}}); e != nil {
			return e
		}
		return tx.AttachSourceInputs(job, ids)
	}
}

type codexLookupFaultStore struct {
	app.Store
	fail atomic.Bool
}
type codexLookupFaultTx struct {
	app.WriteTx
	owner *codexLookupFaultStore
}

func (s *codexLookupFaultStore) Write(ctx context.Context, fn func(app.WriteTx) error) error {
	return s.Store.Write(ctx, func(tx app.WriteTx) error { return fn(codexLookupFaultTx{tx, s}) })
}
func (t codexLookupFaultTx) RecordProviderLookup(id, carrier string, v domain.ProviderLookup) error {
	if t.owner.fail.Load() {
		return errors.New("controlled required lookup audit failure")
	}
	return t.WriteTx.RecordProviderLookup(id, carrier, v)
}

var _ = It("C03COMPOSE actual production driver EOF pauses durable work and exact-ID metadata reconciliation never resends", Label("persistence"), func() {
	for _, auditFailure := range []bool{false, true} {
		f, root, id := managedFixture("selected_snapshot")
		ctx := context.Background()
		Expect(os.WriteFile(filepath.Join(root, "source.txt"), []byte("production fault fixture"), 0600)).To(Succeed())
		job := sid()
		var reads []domain.SourceCaptureRead
		invocation := domain.ProviderInvocation{Prompt: "Read only the supplied source", CWD: root}
		request := managedRequest(id, "source.txt")
		request.ProviderID = "codex"
		response, e := f.core.CaptureAndAdmit(ctx, f.token, sid(), f.project, request, codexAdmission(f, job, invocation, &reads))
		Expect(e).NotTo(HaveOccurred())
		Expect(response.Status).To(Equal(201))
		scratch := GinkgoT().TempDir()
		binary := filepath.Join(scratch, "codex")
		script := `#!/usr/bin/env python3
import json,sys,time,os
if '--version' in sys.argv:print('codex-cli 0.164.0');sys.exit(0)
for line in sys.stdin:
 r=json.loads(line);m=r['method'];p=r.get('params',{});result={}
 if m=='initialized':continue
 if m=='account/read':result={'requiresOpenaiAuth':True,'account':{'type':'chatgpt'}}
 if m=='config/read':result={'config':{}}
 if m=='account/rateLimits/read':result={'ordinaryUsageAllowed':None}
 if m=='model/list':result={'data':[{'model':'fixture-model'}],'nextCursor':None}
 if m=='thread/start':result={'approvalPolicy':'never','cwd':p['cwd'],'model':'fixture-model','modelProvider':'openai','sandbox':{'type':'readOnly','networkAccess':False},'thread':{'id':'owned-thread'}}
 if m=='turn/start':
  assert p['input'][2]['text']=='production fault fixture'
  with open(sys.argv[0]+'.trace','a') as f:f.write('turn/start\n')
  result={'turn':{'id':'owned-turn','status':'inProgress','items':[]}}
 if m=='thread/turns/list':
  assert p['threadId']=='owned-thread' and (p['itemsView']=='notLoaded' or os.path.exists(sys.argv[0]+'.complete'))
  with open(sys.argv[0]+'.trace','a') as f:f.write('metadata-lookup\n' if p['itemsView']=='notLoaded' else 'full-lookup\n')
  complete=os.path.exists(sys.argv[0]+'.complete')
  result={'data':[{'id':'owned-turn','status':'completed' if complete else 'inProgress','itemsView':p['itemsView'],'items':[{'type':'agentMessage','phase':'final_answer','text':'recovered complete output'}] if complete and p['itemsView']=='full' else []}],'nextCursor':None}
 print(json.dumps({'id':r['id'],'result':result}),flush=True)
 if m=='turn/start':time.sleep(.15);sys.exit(0)
`
		Expect(os.WriteFile(binary, []byte(script), 0700)).To(Succeed())
		faults := &codexLookupFaultStore{Store: f.store}
		driver := codex.New(faults, f.store, scratch)
		driver.Binary = binary
		driver.BinarySHA = fmt.Sprintf("%x", sha256.Sum256([]byte(script)))
		coordinator := &work.ProviderCoordinator{Store: faults, Bytes: f.store, Driver: driver}
		Expect(coordinator.Process(ctx, job)).To(Succeed())
		current, e := f.store.Job(ctx, job)
		Expect(e).NotTo(HaveOccurred())
		Expect(current.State).To(Equal("paused"))
		Expect(current.Error).To(Equal("outcome_unknown"))
		var attempt domain.ProviderAttempt
		Expect(f.store.Write(ctx, func(tx app.WriteTx) error {
			rows, e := tx.ProviderAttempts(job)
			if e != nil {
				return e
			}
			Expect(rows).To(HaveLen(1))
			attempt = rows[0]
			Expect(attempt.State).To(Equal("outcome_unknown"))
			Expect(attempt.MayHaveDispatched).To(BeTrue())
			Expect(attempt.Transport.BackendState).To(Equal("inProgress"))
			Expect(attempt.Transport.LocalClosed).To(BeTrue())
			inputs, e := tx.ProviderInputs(attempt.ID)
			if e != nil {
				return e
			}
			Expect(inputs[0].ObservedOrigin).To(Equal("daemon_supplied"))
			return nil
		})).To(Succeed())
		Expect(coordinator.Process(ctx, job)).NotTo(Succeed(), "paused work cannot silently redispatch")
		Expect(f.store.Write(ctx, func(tx app.WriteTx) error {
			_, e := tx.ResumeProviderJob(job, current.AttemptGeneration, "reconcile_only")
			return e
		})).To(Succeed())
		Expect(coordinator.Process(ctx, job)).To(Succeed())
		Expect(f.store.Write(ctx, func(tx app.WriteTx) error {
			a, e := tx.ProviderAttempt(attempt.ID)
			if e != nil {
				return e
			}
			Expect(a.Transport.LastLookup).NotTo(BeNil())
			Expect(a.Transport.LastLookup.BackendState).To(Equal("inProgress"))
			Expect(a.Transport.LastLookup.LocalClosed).To(BeTrue())
			return nil
		})).To(Succeed())
		trace, e := os.ReadFile(binary + ".trace")
		Expect(e).NotTo(HaveOccurred())
		Expect(strings.Count(string(trace), "turn/start\n")).To(Equal(1))
		Expect(strings.Count(string(trace), "metadata-lookup\n")).To(Equal(1))
		// The actual production driver's full-history path commits exact bytes even
		// when its required secondary lookup audit selectively fails.
		Expect(os.WriteFile(binary+".complete", []byte("complete"), 0600)).To(Succeed())
		faults.fail.Store(auditFailure)
		current, e = f.store.Job(ctx, job)
		Expect(e).NotTo(HaveOccurred())
		Expect(f.store.Write(ctx, func(tx app.WriteTx) error {
			_, e := tx.ResumeProviderJob(job, current.AttemptGeneration, "reconcile_only")
			return e
		})).To(Succeed())
		e = coordinator.Process(ctx, job)
		if auditFailure {
			Expect(e).To(HaveOccurred())
		} else {
			Expect(e).NotTo(HaveOccurred())
		}
		var saved domain.ProviderAttempt
		var output domain.Object
		Expect(f.store.Write(ctx, func(tx app.WriteTx) error {
			var e error
			saved, e = tx.ProviderAttempt(attempt.ID)
			if e != nil {
				return e
			}
			output, e = tx.Object(saved.ResultHash)
			return e
		})).To(Succeed())
		Expect(saved.State).To(Equal("result_saved"))
		Expect(saved.RequestID).To(Equal("owned-turn"))
		Expect(f.store.Read(ctx, output, domain.Pin{Kind: "provider_continuation", OwnerID: saved.ID}, func(r io.ReadSeeker) error {
			data, e := io.ReadAll(r)
			Expect(string(data)).To(Equal("recovered complete output"))
			return e
		})).To(Succeed())
		current, e = f.store.Job(ctx, job)
		Expect(e).NotTo(HaveOccurred())
		Expect(current.State).To(Equal("paused"))
		Expect(current.ProviderAttempts.ReconciliationPending).To(BeZero())
		if auditFailure {
			Expect(current.Error).To(Equal("provider_accounting_unavailable"))
			Expect(saved.Transport.LastLookup.BackendState).To(Equal("inProgress"))
		} else {
			Expect(current.Error).To(Equal("saved_result_requires_continuation"))
			Expect(saved.Transport.LastLookup.BackendState).To(Equal("completed"))
		}
		trace, e = os.ReadFile(binary + ".trace")
		Expect(e).NotTo(HaveOccurred())
		Expect(strings.Count(string(trace), "turn/start\n")).To(Equal(1))
		Expect(strings.Count(string(trace), "full-lookup\n")).To(Equal(1))
		faults.fail.Store(false)
		Expect(f.store.Write(ctx, func(tx app.WriteTx) error {
			_, e := tx.ResumeProviderJob(job, current.AttemptGeneration, "continue")
			return e
		})).To(Succeed())
		Expect(coordinator.Process(ctx, job)).To(Succeed())
		after, e := os.ReadFile(binary + ".trace")
		Expect(e).NotTo(HaveOccurred())
		Expect(after).To(Equal(trace), "saved continuation never opens the native driver")
	}

})

type localSetupStore struct {
	app.Store
	trace               string
	disclosureFault     bool
	reconciliationFault bool
}
type localSetupTx struct {
	app.WriteTx
	owner *localSetupStore
}

var localSetupFault = errors.New("controlled pre-audit disclosure failure")

func (s *localSetupStore) Write(ctx context.Context, fn func(app.WriteTx) error) error {
	return s.Store.Write(ctx, func(tx app.WriteTx) error { return fn(localSetupTx{tx, s}) })
}
func (t localSetupTx) CapturedRead(id string) (domain.CapturedRead, error) {
	b, _ := os.ReadFile(t.owner.trace)
	if t.owner.disclosureFault && strings.Contains(string(b), "mcpServerStatus/list\n") {
		return domain.CapturedRead{}, localSetupFault
	}
	return t.WriteTx.CapturedRead(id)
}
func (t localSetupTx) CompleteProviderReconciliation(job, attempt string) error {
	if t.owner.reconciliationFault {
		return localSetupFault
	}
	return t.WriteTx.CompleteProviderReconciliation(job, attempt)
}

func localSetupFixture(mode string) (*vFixture, string, string, *codex.Driver, *localSetupStore) {
	f, root, id := managedFixture("selected_snapshot")
	ctx := context.Background()
	Expect(os.WriteFile(filepath.Join(root, "source.txt"), []byte("late setup source"), 0600)).To(Succeed())
	job := sid()
	var reads []domain.SourceCaptureRead
	inv := domain.ProviderInvocation{Prompt: "Use supplied source only", CWD: root}
	request := managedRequest(id, "source.txt")
	request.ProviderID = "codex"
	response, e := f.core.CaptureAndAdmit(ctx, f.token, sid(), f.project, request, codexAdmission(f, job, inv, &reads))
	Expect(e).NotTo(HaveOccurred())
	Expect(response.Status).To(Equal(201))
	scratch := GinkgoT().TempDir()
	binary := filepath.Join(scratch, "codex")
	script := `#!/usr/bin/env python3
import json,sys,time,os
mode=MODE
if '--version' in sys.argv:print('codex-cli 0.164.0');sys.exit(0)
with open(sys.argv[0]+'.pid','w') as f:f.write(str(os.getpid()))
for line in sys.stdin:
 r=json.loads(line);m=r['method'];p=r.get('params',{});v={}
 with open(sys.argv[0]+'.trace','a') as f:f.write(m+'\n')
 if m=='initialized':continue
 if m=='account/read':v={'requiresOpenaiAuth':True,'account':{'type':'chatgpt'}}
 if m=='config/read':v={'config':{}}
 if m=='account/rateLimits/read':v={'ordinaryUsageAllowed':None}
 if m=='model/list':v={'data':[{'model':'fixture'}],'nextCursor':None}
 if m=='thread/start':v={'approvalPolicy':'never','cwd':p['cwd'],'model':'fixture','modelProvider':'openai','sandbox':{'type':'readOnly','networkAccess':False},'thread':{'id':'fixture-thread'}}
 if m=='mcpServerStatus/list' and mode=='stall':time.sleep(120)
 if m=='turn/start':
  assert p['input'][2]['text']=='late setup source'
  v={'turn':{'id':'fixture-turn','status':'inProgress','items':[]}}
 if m=='thread/turns/list':
  print(json.dumps({'id':r['id'],'error':{'code':-32603}}),flush=True);continue
 print(json.dumps({'id':r['id'],'result':v}),flush=True)
 if m=='turn/start':sys.exit(0)
`
	script = strings.ReplaceAll(script, "MODE", fmt.Sprintf("%q", mode))
	Expect(os.WriteFile(binary, []byte(script), 0700)).To(Succeed())
	faults := &localSetupStore{Store: f.store, trace: binary + ".trace"}
	d := codex.New(faults, f.store, scratch)
	d.Binary = binary
	d.BinarySHA = fmt.Sprintf("%x", sha256.Sum256([]byte(script)))
	return f, job, binary, d, faults
}

var _ = It("C03L6 actual pre-audit production cleanup permits only durably handled collected restart diagnostics without invented native audit", Label("persistence"), func() {
	f, job, binary, driver, faults := localSetupFixture("ready")
	faults.disclosureFault = true
	p := &work.ProviderCoordinator{Store: faults, Bytes: f.store, Driver: driver}
	Expect(p.Process(context.Background(), job)).To(HaveOccurred())
	j, e := f.store.Job(context.Background(), job)
	Expect(e).NotTo(HaveOccurred())
	Expect(j.State).To(Equal("paused"))
	Expect(j.Error).To(Equal("provider_accounting_unavailable"))
	trace, e := os.ReadFile(binary + ".trace")
	Expect(e).NotTo(HaveOccurred())
	Expect(string(trace)).To(ContainSubstring("mcpServerStatus/list\n"))
	Expect(string(trace)).NotTo(ContainSubstring("turn/start\n"))
	Expect(f.store.Write(context.Background(), func(tx app.WriteTx) error {
		rows, e := tx.ProviderAttempts(job)
		if e == nil {
			Expect(rows).To(HaveLen(1))
			Expect(rows[0].State).To(Equal("prepared"))
			Expect(rows[0].MayHaveDispatched).To(BeFalse())
			Expect(rows[0].Transport).To(BeNil())
			Expect(rows[0].RequestID).To(BeEmpty())
			Expect(rows[0].SessionID).To(BeEmpty())
		}
		return e
	})).To(Succeed())
	diagnostic := p.StopProviders()
	Expect(errors.Is(diagnostic, localSetupFault)).To(BeTrue())
	Expect(work.ProviderRestartDiagnostics(diagnostic)).To(BeTrue())
})

type localRecordedDriver struct {
	*codex.Driver
	out      work.ProviderOutcome
	err      error
	executes int
}

func (d *localRecordedDriver) Execute(ctx context.Context, a domain.ProviderAttempt, in []domain.ProviderInput, send func() error, ack func(work.ProviderProgress) error) (work.ProviderOutcome, error) {
	d.executes++
	d.out, d.err = d.Driver.Execute(ctx, a, in, send, ack)
	return d.out, d.err
}
func (d *localRecordedDriver) Reconcile(ctx context.Context, a domain.ProviderAttempt, allowed bool) (work.ProviderOutcome, error) {
	d.out, d.err = d.Driver.Reconcile(ctx, a, allowed)
	return d.out, d.err
}
func localSetupDrained(binary string, out work.ProviderOutcome) {
	Expect(out.LocalClosed).To(BeTrue())
	Expect(out.CleanupError).NotTo(HaveOccurred())
	Expect(out.LocalCleanupReason == "" || out.LocalCleanupReason == "signal_error_drained").To(BeTrue())
	raw, e := os.ReadFile(binary + ".pid")
	Expect(e).NotTo(HaveOccurred())
	var pid int
	_, e = fmt.Sscan(string(raw), &pid)
	Expect(e).NotTo(HaveOccurred())
	Expect(syscall.Kill(pid, 0)).To(Equal(syscall.ESRCH))
}

var _ = It("C03L6 public production setup and late caller stop persist exact no-send facts through trusted admission and real Foundation", Label("persistence"), func() {
	for _, mode := range []string{"internal", "caller_cancel", "caller_deadline"} {
		f, job, binary, driver, faults := localSetupFixture("stall")
		record := &localRecordedDriver{Driver: driver}
		p := &work.ProviderCoordinator{Store: faults, Bytes: f.store, Driver: record}
		caller, cancel := context.WithCancel(context.Background())
		if mode == "caller_deadline" {
			cancel()
			caller, cancel = context.WithTimeout(context.Background(), 2*time.Second)
		}
		defer cancel()
		done := make(chan error, 1)
		go func() { defer GinkgoRecover(); done <- p.Process(caller, job) }()
		Eventually(func() bool {
			b, _ := os.ReadFile(binary + ".trace")
			return strings.Contains(string(b), "mcpServerStatus/list\n")
		}, 5*time.Second).Should(BeTrue())
		Expect(caller.Err()).NotTo(HaveOccurred())
		if mode == "caller_cancel" {
			cancel()
		}
		Eventually(done, 90*time.Second).Should(Receive(Succeed()))
		if mode == "internal" {
			Expect(caller.Err()).NotTo(HaveOccurred())
			Expect(record.out.Reason).To(Equal("setup_timeout"))
			Expect(errors.Is(record.err, context.DeadlineExceeded)).To(BeTrue())
		} else {
			Expect(record.out.Reason).NotTo(Equal("setup_timeout"))
			Expect(caller.Err()).To(HaveOccurred())
		}
		localSetupDrained(binary, record.out)
		Expect(record.out.Transport).To(BeNil())
		Expect(record.out.Lookup).To(BeNil())
		Expect(record.out.RequestID).To(BeEmpty())
		Expect(record.executes).To(Equal(1))
		Expect(p.StopProviders()).To(Succeed())
		j, e := f.store.Job(context.Background(), job)
		Expect(e).NotTo(HaveOccurred())
		Expect(j.State).To(Equal("paused"))
		reason := "dispatch_not_authorized"
		if mode == "internal" {
			reason = "setup_timeout"
		}
		Expect(j.Error).To(Equal(reason))
		Expect(f.store.Write(context.Background(), func(tx app.WriteTx) error {
			rows, e := tx.ProviderAttempts(job)
			if e != nil {
				return e
			}
			Expect(rows).To(HaveLen(1))
			a := rows[0]
			Expect(a.State).To(Equal("prepared"))
			Expect(a.MayHaveDispatched).To(BeFalse())
			Expect(a.RequestID).To(BeEmpty())
			Expect(a.SessionID).To(BeEmpty())
			Expect(a.Transport).To(BeNil())
			in, e := tx.ProviderInputs(a.ID)
			for _, v := range in {
				Expect(v.ObservedOrigin).To(BeEmpty())
				Expect(v.ObservedAt).To(BeNil())
			}
			return e
		})).To(Succeed())
		trace, e := os.ReadFile(binary + ".trace")
		Expect(e).NotTo(HaveOccurred())
		Expect(string(trace)).NotTo(ContainSubstring("turn/start\n"))
	}
})
var _ = It("C03L6 real no-observation reconciliation carries local drain through failed required accounting without resending", Label("persistence"), func() {
	f, job, binary, driver, faults := localSetupFixture("ready")
	record := &localRecordedDriver{Driver: driver}
	p := &work.ProviderCoordinator{Store: faults, Bytes: f.store, Driver: record}
	ctx := context.Background()
	Expect(p.Process(ctx, job)).To(Succeed())
	j, e := f.store.Job(ctx, job)
	Expect(e).NotTo(HaveOccurred())
	Expect(j.State).To(Equal("paused"))
	Expect(f.store.Write(ctx, func(tx app.WriteTx) error {
		_, e := tx.ResumeProviderJob(job, j.AttemptGeneration, "reconcile_only")
		return e
	})).To(Succeed())
	faults.reconciliationFault = true
	Expect(p.Process(ctx, job)).To(HaveOccurred())
	localSetupDrained(binary, record.out)
	Expect(record.out.Lookup).To(BeNil())
	Expect(record.out.Transport).To(BeNil())
	Expect(record.out.CompletionObserved).To(BeFalse())
	stopped := p.StopProviders()
	Expect(errors.Is(stopped, localSetupFault)).To(BeTrue())
	Expect(work.ProviderRestartDiagnostics(stopped)).To(BeTrue())
	Expect(record.executes).To(Equal(1))
	trace, e := os.ReadFile(binary + ".trace")
	Expect(e).NotTo(HaveOccurred())
	Expect(strings.Count(string(trace), "turn/start\n")).To(Equal(1))
	Expect(strings.Count(string(trace), "thread/turns/list\n")).To(Equal(1))
	Expect(f.store.Write(ctx, func(tx app.WriteTx) error {
		rows, e := tx.ProviderAttempts(job)
		if e == nil {
			Expect(rows[0].State).To(Equal("outcome_unknown"))
			Expect(rows[0].RequestID).To(Equal("fixture-turn"))
			Expect(rows[0].SessionID).To(Equal("fixture-thread"))
			Expect(rows[0].Transport.LastLookup).To(BeNil())
		}
		return e
	})).To(Succeed())
})
