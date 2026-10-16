package foundation

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"haeryong.local/haeryong/internal/adapters/sqlite"
	"haeryong.local/haeryong/internal/application/service"
	app "haeryong.local/haeryong/internal/application/storage"
	"haeryong.local/haeryong/internal/domain"
)

func TestDurableRangeLedgerInterruptionResumeAndNaturalIdentity(t *testing.T) {
	var armed atomic.Bool
	injected := errors.New("injected range ledger commit failure")
	f := newFixture(t, func(b sqlite.Boundary) error {
		if b == sqlite.BeforeCommit && armed.Load() {
			return injected
		}
		return nil
	})
	uuid := func() string {
		id, err := service.NewID()
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	hash := func(data string) string { sum := sha256.Sum256([]byte(data)); return hex.EncodeToString(sum[:]) }
	target, _ := json.Marshal(map[string]any{"project_id": uuid(), "document_id": nil})
	upload := domain.Upload{ID: uuid(), PrincipalID: uuid(), Purpose: "result", InputMode: "directory", SelectionName: "resumable", Target: target, Checkpoint: domain.Checkpoint{Generation: 1, InventorySHA256: hash(""), AcceptedBytes: "0"}}
	if err := f.write(func(tx app.WriteTx) error { return tx.CreateUpload(upload) }); err != nil {
		t.Fatal(err)
	}
	first := f.publish(strings.Repeat("A", 1<<20), "first-stage")
	entry := domain.UploadRange{UploadID: upload.ID, Path: "nested/한글.unknown", Size: first.Size, Hash: first.Hash}
	record := func(tx app.WriteTx) error {
		_, err := tx.RecordRange(entry, 1, hash("inventory-one"))
		if err != nil {
			return err
		}
		return tx.ReleasePin("first-stage")
	}
	armed.Store(true)
	if err := f.write(record); !errors.Is(err, injected) {
		t.Fatalf("range ledger fault=%v", err)
	}
	armed.Store(false)
	f.reopen()
	unchanged, err := f.s.Upload(context.Background(), upload.ID)
	if err != nil || unchanged.Checkpoint != upload.Checkpoint {
		t.Fatalf("failed ledger committed checkpoint: %v %+v", err, unchanged)
	}
	ranges, err := f.s.Ranges(context.Background(), upload.ID, "", -1, 1)
	if err != nil || len(ranges) != 0 {
		t.Fatal("failed ledger retained accepted prefix")
	}
	if err := f.s.Collect(context.Background(), first); !errors.Is(err, domain.ErrPinned) {
		t.Fatal("uncommitted range lost durable staging protection", err)
	}
	start := make(chan struct{})
	results := make(chan error, 2)
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); <-start; results <- f.write(record) }()
	}
	close(start)
	wg.Wait()
	close(results)
	for err := range results {
		if err != nil {
			t.Fatal("sameoffset/samebytes retry failed", err)
		}
	}
	current, err := f.s.Upload(context.Background(), upload.ID)
	if err != nil || current.Checkpoint.Generation != 2 || current.Checkpoint.AcceptedBytes != "1048576" {
		t.Fatalf("duplicate bytes counted twice: %+v %v", current, err)
	}
	changed := f.publish(strings.Repeat("C", 1<<20), "changed-stage")
	entry.Hash = changed.Hash
	if err := f.write(func(tx app.WriteTx) error { _, err := tx.RecordRange(entry, 2, hash("changed-inventory")); return err }); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("sameoffset changedbytes accepted: %v", err)
	}
	second := f.publish(strings.Repeat("B", 2<<20), "second-stage")
	next := domain.UploadRange{UploadID: upload.ID, Path: "e\u0301.bin", Size: second.Size, Hash: second.Hash}
	if err := f.write(func(tx app.WriteTx) error {
		_, err := tx.RecordRange(next, 2, hash("inventory-two"))
		if err != nil {
			return err
		}
		return tx.ReleasePin("second-stage")
	}); err != nil {
		t.Fatal(err)
	}
	f.reopen()
	current, err = f.s.Upload(context.Background(), upload.ID)
	if err != nil || current.Checkpoint.Generation != 3 || current.Checkpoint.AcceptedBytes != "3145728" || current.Checkpoint.Complete {
		t.Fatalf("resume checkpoint: %+v %v", current, err)
	}
	cursorPath := ""
	cursorOffset := int64(-1)
	received := []domain.UploadRange{}
	for {
		batch, err := f.s.Ranges(context.Background(), upload.ID, cursorPath, cursorOffset, 1)
		if err != nil {
			t.Fatal(err)
		}
		if len(batch) == 0 {
			break
		}
		received = append(received, batch...)
		cursorPath = batch[0].Path
		cursorOffset = batch[0].Offset
	}
	if len(received) != 2 || received[0].Path != next.Path || received[1].Path != "nested/한글.unknown" {
		t.Fatalf("range cursor lost exact UTF8 path/order: %+v", received)
	}
	for _, object := range []domain.Object{first, second} {
		if err := f.s.Collect(context.Background(), object); !errors.Is(err, domain.ErrPinned) {
			t.Fatal("accepted range lost upload pin", err)
		}
	}
	var complete domain.Checkpoint
	if err := f.write(func(tx app.WriteTx) error {
		var err error
		complete, err = tx.CompleteCheckpoint(upload.ID, 3, hash("inventory-two"))
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if !complete.Complete || complete.Generation != 4 || complete.AcceptedBytes != "3145728" {
		t.Fatal(fmt.Sprint("completion changed accepted bytes: ", complete))
	}
}

func TestAbandonedRangeHistoryDoesNotRetainOrReviveObjects(t *testing.T) {
	f := newFixture(t, nil)
	uuid := func() string {
		id, err := service.NewID()
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	data := "accepted bytes later explicitly abandoned"
	object := f.publish(data, "pre-transfer")
	target, _ := json.Marshal(map[string]any{"project_id": uuid(), "document_id": nil})
	upload := domain.Upload{ID: uuid(), PrincipalID: uuid(), Purpose: "result", InputMode: "file", Target: target, Checkpoint: domain.Checkpoint{Generation: 1, InventorySHA256: app.EmptyInventorySHA256, AcceptedBytes: "0"}}
	if err := f.write(func(tx app.WriteTx) error {
		if err := tx.CreateUpload(upload); err != nil {
			return err
		}
		if _, err := tx.RecordRange(domain.UploadRange{UploadID: upload.ID, Path: "result.unknown", Size: object.Size, Hash: object.Hash}, 1, object.Hash); err != nil {
			return err
		}
		return tx.ReleasePin("pre-transfer")
	}); err != nil {
		t.Fatal(err)
	}
	if err := f.write(func(tx app.WriteTx) error { return tx.AbandonUpload(upload.ID) }); err != nil {
		t.Fatal(err)
	}
	if err := f.s.Collect(context.Background(), object); err != nil {
		t.Fatalf("abandoned historical range FK prevented completed GC: %v", err)
	}
	rows, err := f.s.Ranges(context.Background(), upload.ID, "", -1, 10)
	if err != nil || len(rows) != 1 || rows[0].Hash != object.Hash {
		t.Fatal("abandoned history lost", err)
	}
	if _, err := f.object(object.Hash); !errors.Is(err, domain.ErrNotFound) {
		t.Fatal("physical GC retained available metadata")
	}
	fresh := f.publish(data, "new-owner")
	if err := f.write(func(tx app.WriteTx) error {
		return tx.AdoptUploadObject(upload.ID, "result", target, domain.Reference{Kind: "saved", ID: "stale", Hash: fresh.Hash}, "")
	}); !errors.Is(err, domain.ErrTransition) {
		t.Fatal("historical digest revived abandoned adoption", err)
	}
	f.reopen()
	if err := f.s.Collect(context.Background(), fresh); !errors.Is(err, domain.ErrPinned) {
		t.Fatal("new same-hash owner was not protected", err)
	}
}
