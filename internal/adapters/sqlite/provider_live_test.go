//go:build haeryong_provider_live

package sqlite

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"haeryong.local/haeryong/internal/adapters/contentstore"
	app "haeryong.local/haeryong/internal/application/storage"
	"haeryong.local/haeryong/internal/domain"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

var _ = It("PA10 current subscription attempt survives local interruption and retains orphan uncertainty without redispatch", Label("persistence"), func() {
	scratch := os.Getenv("HAERYONG_PROVIDER_LIVE_EVIDENCE")
	Expect(filepath.IsAbs(scratch)).To(BeTrue(), "explicit private evidence directory required")
	s, a := providerFixture()
	p := s.profile
	objects, e := contentstore.New(filepath.Join(p.DataDir, "storage"))
	Expect(e).NotTo(HaveOccurred())
	defer objects.Close()
	put := func(text string, commit func(app.WriteTx, domain.Object) error) {
		stage, e := objects.Stage(ctx, strings.NewReader(text))
		Expect(e).NotTo(HaveOccurred())
		_, e = objects.Finalize(ctx, stage, func(o contentstore.Object, publish func() error) error {
			return s.Write(ctx, func(tx app.WriteTx) error {
				if e := publish(); e != nil {
					return e
				}
				obj := domain.Object{Hash: o.Hash, Size: o.Size, State: "ready"}
				if e := tx.PutDurableObject(obj); e != nil {
					return e
				}
				if commit != nil {
					return commit(tx, obj)
				}
				return nil
			})
		})
		Expect(e).NotTo(HaveOccurred())
	}
	put("provider fixture source", nil)
	Expect(s.SnapshotDatabase(ctx, filepath.Join(scratch, "older.sqlite"))).To(Succeed())
	liveCtx, cancel := context.WithTimeout(context.Background(), 360*time.Second)
	defer cancel()
	cmd := exec.CommandContext(liveCtx, "python3", "-B", "../../../tests/manual/provider_attempt_bridge.py", "--scratch", scratch)
	input, e := cmd.StdinPipe()
	Expect(e).NotTo(HaveOccurred())
	output, e := cmd.StdoutPipe()
	Expect(e).NotTo(HaveOccurred())
	var diagnostics bytes.Buffer
	cmd.Stderr = &diagnostics
	Expect(cmd.Start()).To(Succeed())
	defer func() {
		_ = input.Close()
		if cmd.ProcessState == nil {
			cancel()
			_ = cmd.Wait()
		}
	}()
	reader := bufio.NewReader(output)
	next := func() map[string]any {
		line, e := reader.ReadBytes('\n')
		Expect(e).NotTo(HaveOccurred())
		var v map[string]any
		Expect(json.Unmarshal(line, &v)).To(Succeed())
		return v
	}
	ready := next()
	Expect(ready["phase"]).To(Equal("ready"), "startup may block; never retry or change account")
	Expect(s.Write(ctx, func(tx app.WriteTx) error { return tx.DispatchProviderAttempt(a.ID) })).To(Succeed())
	_, e = fmt.Fprintln(input, "dispatch")
	Expect(e).NotTo(HaveOccurred())
	accepted := next()
	Expect(accepted["phase"]).To(Equal("accepted"))
	Expect(s.Write(ctx, func(tx app.WriteTx) error {
		return tx.ObserveProviderAttempt(a.ID, "running", accepted["request_id"].(string), accepted["session_id"].(string), "")
	})).To(Succeed())
	Expect(s.Close()).To(Succeed())
	s = New(nil)
	_, closeDB, e := s.Prepare(ctx, p)
	Expect(e).NotTo(HaveOccurred())
	defer closeDB()
	Expect(s.Write(ctx, func(tx app.WriteTx) error {
		attempt, e := tx.ProviderAttempt(a.ID)
		if e != nil {
			return e
		}
		Expect(attempt.State).To(Equal("outcome_unknown"))
		if e = tx.ReconcileJob(a.JobID); e != nil {
			return e
		}
		_, e = tx.CancelJob(a.JobID)
		return e
	})).To(Succeed())
	var inventory bytes.Buffer
	Expect(RestoreOutstanding(ctx, filepath.Join(p.DataDir, "storage/library.sqlite"), func(raw json.RawMessage) error { _, e := inventory.Write(append(raw, '\n')); return e })).To(Succeed())
	Expect(os.WriteFile(filepath.Join(scratch, "outstanding.jsonl"), inventory.Bytes(), 0600)).To(Succeed())
	Expect(RetainProviderOrphans(ctx, filepath.Join(scratch, "older.sqlite"), bytes.NewReader(inventory.Bytes()), func(object domain.Object) (bool, error) {
		e := objects.VerifiedRead(ctx, contentstore.Object{Hash: object.Hash, Size: object.Size}, func(io.ReadSeeker) error { return nil })
		return e == nil, e
	})).To(Succeed())
	_, e = fmt.Fprintln(input, "finish")
	Expect(e).NotTo(HaveOccurred())
	complete := next()
	Expect(complete["phase"]).To(Equal("complete"))
	Expect(complete["observed"]).To(BeTrue())
	Expect(complete["answer"]).To(Equal("READY"))
	Expect(s.Write(ctx, func(tx app.WriteTx) error {
		inputs, e := tx.ProviderInputs(a.ID)
		if e != nil {
			return e
		}
		for _, v := range inputs {
			if e = tx.ObserveProviderInput(a.ID, v.ReadID, "daemon_supplied"); e != nil {
				return e
			}
		}
		return nil
	})).To(Succeed())
	put(complete["answer"].(string), func(tx app.WriteTx, o domain.Object) error { return tx.SaveProviderResult(a.ID, o) })
	Expect(s.Write(ctx, func(tx app.WriteTx) error { return tx.ApplyProviderResult(a.ID, nil) })).To(MatchError(domain.ErrStaleWorker))
	Expect(s.Write(ctx, func(tx app.WriteTx) error {
		j, e := tx.Job(a.JobID)
		if e != nil {
			return e
		}
		return tx.ConfirmStop(j.ID, j.AttemptGeneration)
	})).To(Succeed())
	Expect(input.Close()).To(Succeed())
	_, e = io.Copy(io.Discard, output)
	Expect(e).NotTo(HaveOccurred())
	Expect(cmd.Wait()).To(Succeed())
	Expect(s.SnapshotDatabase(ctx, filepath.Join(scratch, "final.sqlite"))).To(Succeed())
	Expect(os.WriteFile(filepath.Join(scratch, "ledger-proof.json"), jsonBytes(map[string]any{"attempt_id": a.ID, "job_id": a.JobID, "one_external_turn": true, "interrupted_unknown": true, "old_snapshot_orphan_retained": true, "cancelled_result_suppressed": true}), 0600)).To(Succeed())
})
