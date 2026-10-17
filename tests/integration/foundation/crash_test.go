package foundation_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"haeryong.local/haeryong/internal/adapters/content/staging"
	"haeryong.local/haeryong/internal/adapters/contentstore"
	"haeryong.local/haeryong/internal/adapters/foundation"
	"haeryong.local/haeryong/internal/adapters/sqlite"
	auth "haeryong.local/haeryong/internal/application/auth"
	importer "haeryong.local/haeryong/internal/application/import"
	"haeryong.local/haeryong/internal/application/service"
	app "haeryong.local/haeryong/internal/application/storage"
	"haeryong.local/haeryong/internal/domain"
)

// This descriptor travels on the child test process's private stdin, never in
// argv, logs or a production environment variable. No production kill hook is
// exposed: the child uses the existing typed/fault ports of the actual stack.
type childRequest struct {
	Selection  service.Selection
	Token      string
	Action     string
	Boundary   string
	Upload     string
	Operation  string
	Job        string
	Version    string
	Object     domain.Object
	Checkpoint domain.Checkpoint
	Payload    []byte
}

type observedStore struct {
	app.Store
	barrier             func(string)
	publicationPrepared *bool
}

func (s *observedStore) Write(ctx context.Context, apply func(app.WriteTx) error) error {
	var claimed, completed bool
	err := s.Store.Write(ctx, func(tx app.WriteTx) error {
		return apply(&observedTx{WriteTx: tx, barrier: s.barrier, claimed: &claimed, completed: &completed})
	})
	if err == nil {
		if claimed {
			s.barrier("job_claim_committed")
		}
		if completed {
			s.barrier("accepted_commit_before_return")
		}
		if *s.publicationPrepared {
			s.barrier("publication_committed")
		}
	}
	return err
}

type observedTx struct {
	app.WriteTx
	barrier            func(string)
	claimed, completed *bool
}

func (t *observedTx) ReserveOperation(r domain.Request, normalize func() ([]byte, error)) (domain.Receipt, bool, error) {
	receipt, existing, err := t.WriteTx.ReserveOperation(r, normalize)
	if err == nil && !existing {
		t.barrier("receipt_reserved_uncommitted")
	}
	return receipt, existing, err
}

func (t *observedTx) CompleteOperation(key domain.OperationKey, response domain.Response) error {
	err := t.WriteTx.CompleteOperation(key, response)
	*t.completed = err == nil
	return err
}

func (t *observedTx) ClaimJob(id string) (domain.Job, error) {
	job, err := t.WriteTx.ClaimJob(id)
	*t.claimed = err == nil
	return job, err
}

// TestFoundationSubprocess is a test-binary entrypoint, not a product command.
func TestFoundationSubprocess(t *testing.T) {
	if os.Getenv("HAERYONG_FOUNDATION_TEST_CHILD") != "1" {
		return
	}
	var request childRequest
	if err := json.NewDecoder(os.Stdin).Decode(&request); err != nil {
		t.Fatal("private child descriptor invalid")
	}
	barrier := func(name string) {
		if request.Boundary != name {
			return
		}
		fmt.Fprintln(os.Stdout, "boundary:"+name)
		// The parent owns this pipe and keeps it open until SIGKILL. This is
		// deterministic acknowledgement of the actual reached boundary.
		control := os.NewFile(3, "foundation-parent-barrier")
		var one [1]byte
		_, _ = control.Read(one[:])
		t.Fatal("parent released a crash-only barrier without terminating child")
	}
	writer, err := service.OpenWriter(request.Selection)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	armed, publicationPrepared := false, false
	store := foundation.New(func(point sqlite.Boundary) error {
		if armed {
			if point == sqlite.BeforeCommit {
				barrier("database_before_commit")
			}
			if point == sqlite.BeforePublicationCommit {
				publicationPrepared = true
				barrier("publication_before_commit")
			}
		}
		return nil
	})
	_, closeStore, err := store.Prepare(context.Background(), writer.Profile)
	if err != nil {
		t.Fatal(err)
	}
	defer closeStore()
	if err = store.Objects.Close(); err != nil {
		t.Fatal(err)
	}
	store.Objects, err = contentstore.New(filepath.Join(writer.Profile.DataDir, "storage"), contentstore.WithFault(func(point contentstore.Boundary) error {
		barrier("filesystem_" + string(point))
		return nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Objects.Close()
	observed := &observedStore{Store: store, barrier: barrier, publicationPrepared: &publicationPrepared}
	core := importer.New(observed, auth.New(store), store, staging.New())
	switch request.Action {
	case "put":
		size := int64(len(request.Payload))
		// Authenticate is allowed before arming the DB fault: the intended
		// boundary is the bytes+range transaction, not a session touch.
		if _, err = core.Auth.Authenticate(context.Background(), request.Token); err != nil {
			t.Fatal(err)
		}
		storeBoundary := request.Boundary == "database_before_commit"
		if storeBoundary {
			// FileClose follows successful FileSync; only the subsequent
			// publication transaction is armed by the filesystem callback.
			store.Objects.Close()
			store.Objects, err = contentstore.New(filepath.Join(writer.Profile.DataDir, "storage"), contentstore.WithFault(func(point contentstore.Boundary) error {
				if point == contentstore.Rename {
					armed = true
				}
				return nil
			}))
			if err != nil {
				t.Fatal(err)
			}
			defer store.Objects.Close()
		}
		_, err = core.Put(context.Background(), request.Token, request.Upload, importer.Chunk{Path: "nested/한글.html", Kind: "file", Hash: digest(request.Payload), Length: &size, Final: true}, bytes.NewReader(request.Payload))
	case "complete":
		_, err = core.Complete(context.Background(), request.Token, request.Upload, request.Operation, request.Checkpoint)
	case "import":
		_, err = core.Import(context.Background(), request.Token, request.Operation, importer.ImportRequest{UploadID: request.Upload, DocumentPresent: true, VersionID: request.Version})
	case "process":
		armed = true
		err = core.Process(context.Background(), request.Job)
	case "collect":
		err = store.Collect(context.Background(), request.Object)
	default:
		t.Fatal("unknown private child action")
	}
	if err != nil {
		t.Fatal(err)
	}
	t.Fatal("selected crash boundary was not reached")
}

func killAtBoundary(t *testing.T, f *fixture, request childRequest) {
	t.Helper()
	request.Selection, request.Token = f.selection, f.token
	f.close(t)
	readControl, writeControl, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer readControl.Close()
	defer writeControl.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestFoundationSubprocess$", "-test.timeout=20s")
	command.Env = append(os.Environ(), "HAERYONG_FOUNDATION_TEST_CHILD=1")
	descriptor, _ := json.Marshal(request)
	command.Stdin = bytes.NewReader(descriptor)
	command.ExtraFiles = []*os.File{readControl}
	var diagnostics bytes.Buffer
	command.Stderr = &diagnostics
	stdout, err := command.StdoutPipe()
	if err != nil || command.Start() != nil {
		t.Fatal("could not start foundation child")
	}
	defer func() {
		if command.ProcessState == nil {
			_ = command.Process.Kill()
			_ = command.Wait()
		}
	}()
	reached := make(chan bool, 1)
	go func() {
		scanner := bufio.NewScanner(stdout)
		for scanner.Scan() {
			if scanner.Text() == "boundary:"+request.Boundary {
				reached <- true
				return
			}
		}
		reached <- false
	}()
	select {
	case ok := <-reached:
		if !ok {
			_ = command.Wait()
			t.Fatalf("child exited before %s; stderr=%s", request.Boundary, diagnostics.String())
		}
	case <-ctx.Done():
		_ = command.Wait()
		t.Fatalf("child did not reach %s", request.Boundary)
	}
	if competing, err := service.OpenWriter(f.selection); !errors.Is(err, service.ErrLocked) {
		if competing != nil {
			competing.Close()
		}
		t.Fatal("live child did not hold the actual writer lock")
	}
	if err := command.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	err = command.Wait()
	var exit *exec.ExitError
	if !errors.As(err, &exit) || !exit.Sys().(syscall.WaitStatus).Signaled() || exit.Sys().(syscall.WaitStatus).Signal() != syscall.SIGKILL {
		t.Fatal("child termination was not actual SIGKILL")
	}
	f.reopen(t)
	if path := os.Getenv("HAERYONG_TASK006_CRASH_REPORT"); path != "" {
		file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
		if err != nil {
			t.Fatal(err)
		}
		err = json.NewEncoder(file).Encode(map[string]any{"action": request.Action, "boundary": request.Boundary, "signal": exit.Sys().(syscall.WaitStatus).Signal().String(), "writer_exclusion_observed": true, "reopened_under_writer_lock": true})
		if closeErr := file.Close(); err != nil || closeErr != nil {
			t.Fatal("could not retain structured process-boundary observation")
		}
	}
}

func TestProcessKillFileBoundariesNeverAcknowledgeNondurableInput(t *testing.T) {
	for _, boundary := range []string{"filesystem_write", "filesystem_file_sync", "filesystem_file_close", "filesystem_rename", "filesystem_directory_sync", "database_before_commit"} {
		t.Run(boundary, func(t *testing.T) {
			f := newFixture(t)
			upload := f.create(t)
			data := []byte("<html>synthetic crash boundary 한글</html>")
			before, err := f.store.Health(f.ctx)
			if err != nil {
				t.Fatal(err)
			}
			original, err := f.store.Upload(f.ctx, upload)
			if err != nil {
				t.Fatal(err)
			}
			killAtBoundary(t, f, childRequest{Action: "put", Boundary: boundary, Upload: upload, Payload: data})
			after, err := f.store.Health(f.ctx)
			if err != nil || after.Library != before.Library {
				t.Fatal("restart changed library/profile/runtime/auth/event identity")
			}
			u, err := f.store.Upload(f.ctx, upload)
			if err != nil || u.Checkpoint != original.Checkpoint {
				t.Fatal("uncommitted input was acknowledged")
			}
			if err := f.store.Write(f.ctx, func(tx app.WriteTx) error {
				_, err := tx.Object(digest(data))
				if !errors.Is(err, domain.ErrNotFound) {
					return errors.New("uncommitted bytes have READY metadata")
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			cp := f.put(t, upload, data)
			job := f.complete(t, upload, cp, key(t))
			if err := f.core.Process(f.ctx, job); err != nil {
				t.Fatal(err)
			}
			sealed, err := f.store.Upload(f.ctx, upload)
			if err != nil || sealed.State != "sealed" {
				t.Fatal("same retained input did not resume after actual process kill")
			}
			entries, err := f.store.Entries(f.ctx, sealed.ContentSetID, "", 64)
			if err != nil || len(entries) != 2 || entries[1].Hash != digest(data) {
				t.Fatal("resumed tree identity differs")
			}
		})
	}
}

func TestProcessKillReceiptReservationAndCommittedAcceptanceReplay(t *testing.T) {
	for _, boundary := range []string{"receipt_reserved_uncommitted", "accepted_commit_before_return"} {
		t.Run(boundary, func(t *testing.T) {
			f := newFixture(t)
			upload := f.create(t)
			cp := f.put(t, upload, []byte("<html>accepted client exit</html>"))
			cp.Generation++
			cp.Complete = true
			operation := key(t)
			principal, err := f.auth.Authenticate(f.ctx, f.token)
			if err != nil {
				t.Fatal(err)
			}
			killAtBoundary(t, f, childRequest{Action: "complete", Boundary: boundary, Upload: upload, Operation: operation, Checkpoint: cp})
			receipt, err := f.store.Receipt(f.ctx, domain.OperationKey{PrincipalID: principal.PrincipalID, OperationID: operation})
			if boundary == "receipt_reserved_uncommitted" && !errors.Is(err, domain.ErrNotFound) {
				t.Fatal("uncommitted receipt survived process kill")
			}
			if boundary == "accepted_commit_before_return" && (err != nil || receipt.State != "completed" || receipt.Response.Status != 202) {
				t.Fatal("committed202 creation receipt was lost")
			}
			response, err := f.core.Complete(f.ctx, f.token, upload, operation, cp)
			if err != nil {
				t.Fatal(err)
			}
			if receipt.Response != nil && !bytes.Equal(receipt.Response.Body, response.Body) {
				t.Fatal("lost-response replay changed the original job")
			}
			job := responseID(t, response, "job_id")
			if err := f.core.Process(f.ctx, job); err != nil {
				t.Fatal(err)
			}
			again, err := f.core.Complete(f.ctx, f.token, upload, operation, cp)
			if err != nil || !bytes.Equal(response.Body, again.Body) {
				t.Fatal("matching receipt did not replay before stale current checkpoint")
			}
			changed := cp
			changed.Generation++
			if _, err := f.core.Complete(f.ctx, f.token, upload, operation, changed); !errors.Is(err, domain.ErrConflict) {
				t.Fatal("changed semantic checkpoint reused original operation")
			}
		})
	}
}

func TestProcessKillClaimAndAtomicPublicationRecoverOriginalIntentOnce(t *testing.T) {
	for _, boundary := range []string{"job_claim_committed", "publication_before_commit", "publication_committed"} {
		t.Run(boundary, func(t *testing.T) {
			f := newFixture(t)
			upload := f.create(t)
			cp := f.put(t, upload, []byte("<html>immutable publication across process death</html>"))
			seal := f.complete(t, upload, cp, key(t))
			if err := f.core.Process(f.ctx, seal); err != nil {
				t.Fatal(err)
			}
			operation, version := key(t), key(t)
			input := importer.ImportRequest{UploadID: upload, DocumentPresent: true, VersionID: version}
			accepted, err := f.core.Import(f.ctx, f.token, operation, input)
			if err != nil {
				t.Fatal(err)
			}
			job := responseID(t, accepted, "job_id")
			original, err := f.store.Job(f.ctx, job)
			if err != nil {
				t.Fatal(err)
			}
			killAtBoundary(t, f, childRequest{Action: "process", Boundary: boundary, Job: job})
			current, err := f.store.Job(f.ctx, job)
			if err != nil {
				t.Fatal(err)
			}
			if boundary != "publication_committed" {
				if current.State != "interrupted" || current.AttemptGeneration <= original.AttemptGeneration {
					t.Fatal("startup did not fence the killed running attempt")
				}
				if _, err := f.store.Version(f.ctx, version); !errors.Is(err, domain.ErrNotFound) {
					t.Fatal("uncommitted publication became visible")
				}
				if err := f.store.Write(f.ctx, func(tx app.WriteTx) error {
					for _, state := range []string{"completed", "failed"} {
						if err := tx.FinishJob(job, original.AttemptGeneration+1, state, func(app.WriteTx) error {
							return errors.New("stale application callback unexpectedly ran")
						}); !errors.Is(err, domain.ErrStaleWorker) {
							return errors.New("killed attempt retained success or failure authority")
						}
					}
					return nil
				}); err != nil {
					t.Fatal(err)
				}
				if err := f.core.Process(f.ctx, job); err != nil {
					t.Fatal(err)
				}
			} else if current.State != "completed" {
				t.Fatal("committed job result did not survive process kill")
			}
			published, err := f.store.Version(f.ctx, version)
			if err != nil || published.Sequence != 1 || published.ManifestSHA == "" {
				t.Fatal("recovery did not publish the original immutable version")
			}
			document, err := f.store.Document(f.ctx, published.DocumentID)
			if err != nil || document.LatestVersionID != version {
				t.Fatal("recovery latest pointer is inconsistent")
			}
			replayed, err := f.core.Import(f.ctx, f.token, operation, input)
			if err != nil || replayed.Status != 202 || !bytes.Equal(replayed.Body, accepted.Body) {
				t.Fatal("committed202 receipt was replaced by the asynchronous outcome")
			}
			health, err := f.store.Health(f.ctx)
			if err != nil {
				t.Fatal(err)
			}
			var publicationEvents int
			var after int64
			for {
				events, err := f.store.Events(f.ctx, health.Library.StreamEpoch, after, 17)
				if err != nil {
					t.Fatal(err)
				}
				if len(events) == 0 {
					break
				}
				for _, event := range events {
					if event.ResourceID == document.ID && event.Kind == "version.published" {
						publicationEvents++
					}
					after = event.Sequence
				}
			}
			if publicationEvents != 1 {
				t.Fatalf("publication effects/events were not once-only: %d", publicationEvents)
			}
		})
	}
}

func TestProcessKillGCReservationRetainsFenceAndOtherOwnersAcrossRestart(t *testing.T) {
	for _, boundary := range []string{"filesystem_before_delete", "filesystem_directory_sync"} {
		t.Run(boundary, func(t *testing.T) {
			f := newFixture(t)
			candidate, stage := pinObject(t, f, "synthetic eligible deletion")
			protected, _ := pinObject(t, f, "synthetic independent persisted owner")
			if err := f.store.Write(f.ctx, func(tx app.WriteTx) error { return tx.ReleasePin(stage) }); err != nil {
				t.Fatal(err)
			}
			killAtBoundary(t, f, childRequest{Action: "collect", Boundary: boundary, Object: candidate})
			if err := f.store.Write(f.ctx, func(tx app.WriteTx) error {
				current, err := tx.Object(candidate.Hash)
				if err != nil || current.State != "deleting" || current.GCToken == "" {
					return errors.New("interrupted collector lost its persisted deletion fence")
				}
				if err := tx.AcquirePin(domain.Pin{ID: key(t), Kind: "active_read", OwnerID: "late reader", Hash: candidate.Hash}); !errors.Is(err, domain.ErrDeleting) {
					return errors.New("late read bypassed deleting reservation")
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			pinProtected(t, f, protected)
			if err := f.store.Collect(f.ctx, candidate); err != nil {
				t.Fatal(err)
			}
			if err := f.store.Write(f.ctx, func(tx app.WriteTx) error {
				_, err := tx.Object(candidate.Hash)
				if !errors.Is(err, domain.ErrNotFound) {
					return errors.New("same deletion reservation did not reconcile")
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			pinProtected(t, f, protected)
		})
	}
}
