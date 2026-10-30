package source_test

import (
	"context"
	"crypto/sha256"
	"fmt"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"haeryong.local/haeryong/internal/adapters/ai/codex"
	"haeryong.local/haeryong/internal/adapters/foundation"
	"haeryong.local/haeryong/internal/application/service"
	app "haeryong.local/haeryong/internal/application/storage"
	"haeryong.local/haeryong/internal/application/work"
	"haeryong.local/haeryong/internal/domain"
	"io"
	"os"
	"path/filepath"
	"strings"
)

var _ = It("C03W4COMPOSE actual completed Summary survives failed full RPC and cold restart recovers exact output without another turn", Label("persistence"), func() {
	for _, lookupAuditFailure := range []bool{false, true} {
		f, root, id := managedFixture("selected_snapshot")
		ctx := context.Background()
		Expect(os.WriteFile(filepath.Join(root, "source.txt"), []byte("completed recovery source"), 0600)).To(Succeed())
		job := sid()
		var reads []domain.SourceCaptureRead
		inv := domain.ProviderInvocation{Prompt: "Read only the supplied source", CWD: root}
		request := managedRequest(id, "source.txt")
		request.ProviderID = "codex"
		response, e := f.core.CaptureAndAdmit(ctx, f.token, sid(), f.project, request, codexAdmission(f, job, inv, &reads))
		Expect(e).NotTo(HaveOccurred())
		Expect(response.Status).To(Equal(201))
		scratch := GinkgoT().TempDir()
		binary := filepath.Join(scratch, "codex")
		script := `#!/usr/bin/env python3
import json,sys,os
if '--version' in sys.argv: print('codex-cli 0.164.0');sys.exit(0)
def send(x):print(json.dumps(x),flush=True)
for line in sys.stdin:
 r=json.loads(line);m=r['method'];p=r.get('params',{});v={}
 if m=='initialized':continue
 if m=='account/read':v={'requiresOpenaiAuth':True,'account':{'type':'chatgpt'}}
 if m=='config/read':v={'config':{}}
 if m=='account/rateLimits/read':v={'ordinaryUsageAllowed':None}
 if m=='model/list':v={'data':[{'model':'fixture'}],'nextCursor':None}
 if m=='thread/start':v={'approvalPolicy':'never','cwd':p['cwd'],'model':'fixture','modelProvider':'openai','sandbox':{'type':'readOnly','networkAccess':False},'thread':{'id':'owned-thread'}}
 if m=='turn/start':
  assert p['input'][2]['text']=='completed recovery source'
  with open(sys.argv[0]+'.trace','a') as f:f.write('turn/start\n')
  v={'turn':{'id':'owned-turn','status':'inProgress','items':[]}}
 if m=='thread/turns/list':
  assert p['threadId']=='owned-thread'
  with open(sys.argv[0]+'.trace','a') as f:f.write(p['itemsView']+'\n')
  if p['itemsView']=='full' and not os.path.exists(sys.argv[0]+'.release'):
   send({'id':r['id'],'error':{'code':-32603}});continue
  v={'data':[{'id':'owned-turn','status':'completed','itemsView':p['itemsView'],'items':[{'type':'agentMessage','phase':'final_answer','text':'complete recovered public answer'}] if p['itemsView']=='full' else []}],'nextCursor':None}
 send({'id':r['id'],'result':v})
 if m=='turn/start':send({'method':'turn/completed','params':{'threadId':'owned-thread','turn':{'id':'owned-turn','status':'completed','itemsView':'summary','items':[]}}})
`
		Expect(os.WriteFile(binary, []byte(script), 0700)).To(Succeed())
		driver := codex.New(f.store, f.store, scratch)
		driver.Binary = binary
		driver.BinarySHA = fmt.Sprintf("%x", sha256.Sum256([]byte(script)))
		p := work.ProviderCoordinator{Store: f.store, Bytes: f.store, Driver: driver}
		Expect(p.Process(ctx, job)).To(Succeed())
		var attempt domain.ProviderAttempt
		Expect(f.store.Write(ctx, func(tx app.WriteTx) error {
			rows, e := tx.ProviderAttempts(job)
			if e == nil {
				attempt = rows[0]
			}
			return e
		})).To(Succeed())
		Expect(attempt.State).To(Equal("outcome_unknown"))
		Expect(attempt.Reason).To(Equal("result_retrieval_pending"))
		Expect(attempt.Transport.BackendState).To(Equal("completed"))
		Expect(attempt.ResultHash).To(BeEmpty())
		profile, e := service.Load(service.Selection{Profile: "test", DataDir: f.dataDir})
		Expect(e).NotTo(HaveOccurred())
		Expect(f.store.Objects.Close()).To(Succeed())
		Expect(f.store.Close()).To(Succeed())
		reopened := foundation.New(nil)
		_, closeDB, e := reopened.Prepare(ctx, profile)
		Expect(e).NotTo(HaveOccurred())
		defer closeDB()
		faults := &codexLookupFaultStore{Store: reopened}
		faults.fail.Store(lookupAuditFailure)
		driver = codex.New(faults, reopened, scratch)
		driver.Binary = binary
		driver.BinarySHA = fmt.Sprintf("%x", sha256.Sum256([]byte(script)))
		p = work.ProviderCoordinator{Store: faults, Bytes: reopened, Driver: driver}
		// A second transient full read plus a selective audit failure cannot erase
		// the primary completion fact carried across the actual database reopen.
		current, e := reopened.Job(ctx, job)
		Expect(e).NotTo(HaveOccurred())
		Expect(reopened.Write(ctx, func(tx app.WriteTx) error {
			_, e := tx.ResumeProviderJob(job, current.AttemptGeneration, "reconcile_only")
			return e
		})).To(Succeed())
		e = p.Process(ctx, job)
		if lookupAuditFailure {
			Expect(e).To(HaveOccurred())
		} else {
			Expect(e).NotTo(HaveOccurred())
		}
		Expect(reopened.Write(ctx, func(tx app.WriteTx) error {
			actual, e := tx.ProviderAttempt(attempt.ID)
			if e == nil {
				Expect(actual.Reason).To(Equal("result_retrieval_pending"))
				Expect(actual.State).To(Equal("outcome_unknown"))
			}
			return e
		})).To(Succeed())
		faults.fail.Store(false)
		Expect(os.WriteFile(binary+".release", []byte("release"), 0600)).To(Succeed())
		current, e = reopened.Job(ctx, job)
		Expect(e).NotTo(HaveOccurred())
		Expect(reopened.Write(ctx, func(tx app.WriteTx) error {
			_, e := tx.ResumeProviderJob(job, current.AttemptGeneration, "reconcile_only")
			return e
		})).To(Succeed())
		Expect(p.Process(ctx, job)).To(Succeed())
		var result domain.Object
		Expect(reopened.Write(ctx, func(tx app.WriteTx) error {
			actual, e := tx.ProviderAttempt(attempt.ID)
			if e != nil {
				return e
			}
			Expect(actual.State).To(Equal("result_saved"))
			result, e = tx.Object(actual.ResultHash)
			return e
		})).To(Succeed())
		Expect(reopened.Read(ctx, result, domain.Pin{Kind: "provider_continuation", OwnerID: attempt.ID}, func(r io.ReadSeeker) error {
			body, e := io.ReadAll(r)
			Expect(string(body)).To(Equal("complete recovered public answer"))
			return e
		})).To(Succeed())
		trace, e := os.ReadFile(binary + ".trace")
		Expect(e).NotTo(HaveOccurred())
		Expect(strings.Count(string(trace), "turn/start\n")).To(Equal(1))
		current, e = reopened.Job(ctx, job)
		Expect(e).NotTo(HaveOccurred())
		Expect(reopened.Write(ctx, func(tx app.WriteTx) error {
			_, e := tx.ResumeProviderJob(job, current.AttemptGeneration, "continue")
			return e
		})).To(Succeed())
		Expect(p.Process(ctx, job)).To(Succeed())
		after, e := os.ReadFile(binary + ".trace")
		Expect(e).NotTo(HaveOccurred())
		Expect(after).To(Equal(trace), "saved continuation opens no native process")
	}
})
