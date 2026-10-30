package source_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"haeryong.local/haeryong/internal/adapters/foundation"
	adapter "haeryong.local/haeryong/internal/adapters/source"
	"haeryong.local/haeryong/internal/adapters/sqlite"
	appauth "haeryong.local/haeryong/internal/application/auth"
	"haeryong.local/haeryong/internal/application/service"
	"haeryong.local/haeryong/internal/application/source"
	app "haeryong.local/haeryong/internal/application/storage"
	"haeryong.local/haeryong/internal/domain"
)

type captureCrashRequest struct {
	Selection       service.Selection
	Root, Mode, Key string
}

func init() {
	if os.Getenv("HAERYONG_MANAGED_CAPTURE_CHILD") == "1" {
		if e := captureCrashChild(); e != nil {
			panic(e)
		}
		os.Exit(0)
	}
}

// Fixture setup/signalling only. Parent Ginkgo owns every scenario assertion.
func captureCrashChild() error {
	ctx := context.Background()
	var req captureCrashRequest
	if e := json.NewDecoder(os.Stdin).Decode(&req); e != nil {
		return e
	}
	w, e := service.OpenWriter(req.Selection)
	if e != nil {
		return e
	}
	defer w.Close()
	store := foundation.New(nil)
	_, closeDB, e := store.Prepare(ctx, w.Profile)
	if e != nil {
		return e
	}
	defer closeDB()
	if e = w.MarkStorageReady(nil); e != nil {
		return e
	}
	auth := appauth.New(store)
	pair, e := auth.CreatePairing(ctx, "http://127.0.0.1:9270", "fixture", "fixture")
	if e != nil {
		return e
	}
	key, e := service.NewID()
	if e != nil {
		return e
	}
	_, e = auth.BootstrapDecide(ctx, domain.PairingDecision{ID: pair.ID, Action: "approve", Code: pair.Code, Origin: pair.Origin, ETag: pair.ETag, OperationID: key})
	if e != nil {
		return e
	}
	session, e := auth.Poll(ctx, pair.ID, pair.PollingSecret, pair.Origin)
	if e != nil {
		return e
	}
	health, e := store.Health(ctx)
	if e != nil {
		return e
	}
	project := health.Library.UnfiledProjectID
	mode := "reference_only"
	if e = store.Write(ctx, func(tx app.WriteTx) error {
		cfg, e := tx.Configuration()
		if e != nil {
			return e
		}
		var v map[string]any
		if e = json.Unmarshal(cfg.Value, &v); e != nil {
			return e
		}
		v["projects"] = map[string]any{project: map[string]any{"sources": []domain.ManagedSourceMapping{{SourceID: "selected", Kind: "files", RootPath: req.Root, RetentionMode: mode, ProviderIDs: []string{}}}}}
		cfg.Value, _ = json.Marshal(v)
		return tx.SetConfiguration(cfg, cfg.Revision)
	}); e != nil {
		return e
	}
	core := source.New(store, auth, store, &fakeReader{})
	core.Files = adapter.Files{ExcludedPaths: []string{w.Profile.DataDir}}
	park := func() {
		_, _ = os.Stdout.WriteString("capture-owned\n")
		control := os.NewFile(3, "parent-barrier")
		defer control.Close()
		var b [1]byte
		_, _ = control.Read(b[:])
	}
	if req.Mode != "reader" {
		core.Fault = func(stage string) error {
			if stage == "capture_before_commit" {
				park()
			}
			return nil
		}
	}
	r, e := core.Capture(ctx, session.Token, req.Key, project, source.CaptureRequest{SourceMode: "current", Selections: []source.CaptureSelection{{SourceID: "selected", Paths: []source.CapturePath{{Path: "file"}}}}})
	if e != nil {
		return e
	}
	if req.Mode == "reader" {
		var response struct {
			ID string `json:"capture_id"`
		}
		if e = json.Unmarshal(r.Body, &response); e != nil {
			return e
		}
		var read string
		if e = store.Write(ctx, func(tx app.WriteTx) error {
			rows, e := tx.SourceCaptureReads(response.ID, "", 1)
			if e == nil && len(rows) == 1 {
				read = rows[0].ID
			}
			return e
		}); e != nil {
			return e
		}
		return core.ReadCaptured(ctx, session.Token, read, func(domain.SourceCaptureRead, io.ReadSeeker) error {
			park()
			return errors.New("barrier unexpectedly released")
		})
	}
	return errors.New("capture unexpectedly completed")
}

var _ = It("SCAP10 actual process kill/reap and exclusive reopen settle interrupted capture ownership and reclaim only dead capture reader pins", Label("persistence"), func() {
	for _, mode := range []string{"capture", "reader"} {
		root := GinkgoT().TempDir()
		body := []byte("actual crash source bytes")
		Expect(os.WriteFile(filepath.Join(root, "file"), body, 0600)).To(Succeed())
		selection := service.Selection{Profile: "capture-crash", DataDir: filepath.Join(GinkgoT().TempDir(), "profile")}
		_, e := service.Initialize(selection)
		Expect(e).NotTo(HaveOccurred())
		request := captureCrashRequest{Selection: selection, Root: root, Mode: mode, Key: sid()}
		readControl, writeControl, e := os.Pipe()
		Expect(e).NotTo(HaveOccurred())
		defer readControl.Close()
		defer writeControl.Close()
		cmd := exec.Command(os.Args[0])
		cmd.Env = append(os.Environ(), "HAERYONG_MANAGED_CAPTURE_CHILD=1")
		raw, _ := json.Marshal(request)
		cmd.Stdin = bytes.NewReader(raw)
		cmd.ExtraFiles = []*os.File{readControl}
		stdout, e := cmd.StdoutPipe()
		Expect(e).NotTo(HaveOccurred())
		var diagnostics bytes.Buffer
		cmd.Stderr = &diagnostics
		Expect(cmd.Start()).To(Succeed())
		reaped := false
		DeferCleanup(func() {
			if !reaped {
				_ = cmd.Process.Kill()
				_ = cmd.Wait()
			}
		})
		signal := make(chan string, 1)
		go func() {
			scanner := bufio.NewScanner(stdout)
			if scanner.Scan() {
				signal <- scanner.Text()
			} else {
				signal <- "missing"
			}
		}()
		Eventually(signal, 60*time.Second).Should(Receive(Equal("capture-owned")), "fixture did not reach actual capture/read boundary")
		Expect(cmd.Process.Kill()).To(Succeed())
		Expect(cmd.Wait()).NotTo(Succeed())
		reaped = true
		writer, e := service.OpenWriter(selection)
		Expect(e).NotTo(HaveOccurred())
		store := foundation.New(nil)
		_, closeDB, e := store.Prepare(context.Background(), writer.Profile)
		Expect(e).NotTo(HaveOccurred())
		health, e := store.Health(context.Background())
		Expect(e).NotTo(HaveOccurred())
		var receipt domain.Receipt
		var capture domain.SourceCapture
		var row domain.SourceCaptureRead
		Expect(store.Write(context.Background(), func(tx app.WriteTx) error {
			owner, e := tx.AuthState()
			if e != nil {
				return e
			}
			receipt, e = tx.Receipt(domain.OperationKey{PrincipalID: owner.OwnerID, OperationID: request.Key})
			if e != nil {
				return e
			}
			var ids map[string]string
			if e = json.Unmarshal(receipt.Normalization, &ids); e != nil {
				return e
			}
			capture, e = tx.SourceCapture(ids["capture_id"])
			if e != nil {
				return e
			}
			rows, e := tx.SourceCaptureReads(capture.ID, "", 1)
			if e == nil && len(rows) == 1 {
				row = rows[0]
			}
			return e
		})).To(Succeed())
		Expect(health.Library.SchemaRevision).To(Equal(sqlite.SchemaRevision))
		object := domain.Object{Hash: vsha(body), Size: int64(len(body))}
		if mode == "capture" {
			Expect(capture.State).To(Equal("failed"))
			Expect(capture.Reason).To(Equal("interrupted_before_commit"))
			Expect(receipt.State).To(Equal("failed"))
			Expect(receipt.Response.Status).To(Equal(503))
			var envelope map[string]any
			Expect(json.Unmarshal(receipt.Response.Body, &envelope)).To(Succeed())
			stamp, ok := envelope["request_id"].(string)
			Expect(ok).To(BeTrue())
			Expect(stamp).NotTo(BeEmpty())
			Expect(envelope).To(Equal(map[string]any{"request_id": stamp, "error": map[string]any{"code": "SERVICE_RECOVERING", "message": "source check failed: interrupted_before_commit", "retryable": false, "details": map[string]any{"reason": "interrupted_before_commit"}}}))
			firstBody := append([]byte(nil), receipt.Response.Body...)
			Expect(closeDB()).To(Succeed())
			_, closeDB, e = store.Prepare(context.Background(), writer.Profile)
			Expect(e).NotTo(HaveOccurred())
			replay, e := store.Receipt(context.Background(), domain.OperationKey{PrincipalID: capture.PrincipalID, OperationID: capture.OperationID})
			Expect(e).NotTo(HaveOccurred())
			Expect(replay.Response.Status).To(Equal(503))
			Expect(replay.Response.Body).To(Equal(firstBody))
			Expect(store.Collect(context.Background(), object)).To(Succeed())
		} else {
			Expect(capture.State).To(Equal("completed"))
			Expect(row.RetentionMode).To(Equal("reference_only"))
			Expect(store.Collect(context.Background(), object)).To(Succeed(), "dead reader pin must not permanently retain reference-only execution bytes")
		}

		Expect(closeDB()).To(Succeed())
		Expect(writer.Close()).To(Succeed())
	}
})
