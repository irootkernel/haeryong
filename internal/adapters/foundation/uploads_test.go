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

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"haeryong.local/haeryong/internal/adapters/sqlite"
	"haeryong.local/haeryong/internal/application/service"
	app "haeryong.local/haeryong/internal/application/storage"
	"haeryong.local/haeryong/internal/domain"
)

var _ = It("TestDurableRangeLedgerInterruptionResumeAndNaturalIdentity", Label("persistence"), func() {
	t := GinkgoT()

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
		Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })

		return id
	}
	hash := func(data string) string { sum := sha256.Sum256([]byte(data)); return hex.EncodeToString(sum[:]) }
	target, _ := json.Marshal(map[string]any{"project_id": uuid(), "document_id": nil})
	upload := domain.Upload{ID: uuid(), PrincipalID: uuid(), Purpose: "result", InputMode: "directory", SelectionName: "resumable", Target: target, Checkpoint: domain.Checkpoint{Generation: 1, InventorySHA256: hash(""), AcceptedBytes: "0"}}
	{
		err := f.write(func(tx app.WriteTx) error { return tx.CreateUpload(upload) })
		Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })
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
	{
		err := f.write(record)
		Expect(!errors.Is(err, injected)).To(BeFalse(), func() string { return fmt.Sprintf("range ledger fault=%v", err) })
	}

	armed.Store(false)
	f.reopen()
	unchanged, err := f.s.Upload(context.Background(), upload.ID)
	Expect(err != nil || unchanged.Checkpoint != upload.Checkpoint).To(BeFalse(), func() string { return fmt.Sprintf("failed ledger committed checkpoint: %v %+v", err, unchanged) })

	ranges, err := f.s.Ranges(context.Background(), upload.ID, "", -1, 1)
	Expect(err != nil || len(ranges) != 0).To(BeFalse(), func() string { return fmt.Sprint("failed ledger retained accepted prefix") })
	{

		err := f.s.Collect(context.Background(), first)
		Expect(!errors.Is(err, domain.ErrPinned)).To(BeFalse(), func() string { return fmt.Sprint("uncommitted range lost durable staging protection", err) })
	}

	start := make(chan struct{})
	results := make(chan error, 2)
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() { defer GinkgoRecover(); defer wg.Done(); <-start; results <- f.write(record) }()
	}
	close(start)
	wg.Wait()
	close(results)
	for err := range results {
		Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint("sameoffset/samebytes retry failed", err) })

	}
	current, err := f.s.Upload(context.Background(), upload.ID)
	Expect(err != nil || current.Checkpoint.Generation != 2 || current.Checkpoint.AcceptedBytes != "1048576").To(BeFalse(), func() string { return fmt.Sprintf("duplicate bytes counted twice: %+v %v", current, err) })

	changed := f.publish(strings.Repeat("C", 1<<20), "changed-stage")
	entry.Hash = changed.Hash
	{
		err := f.write(func(tx app.WriteTx) error { _, err := tx.RecordRange(entry, 2, hash("changed-inventory")); return err })
		Expect(!errors.Is(err, domain.ErrConflict)).To(BeFalse(), func() string { return fmt.Sprintf("sameoffset changedbytes accepted: %v", err) })
	}

	second := f.publish(strings.Repeat("B", 2<<20), "second-stage")
	next := domain.UploadRange{UploadID: upload.ID, Path: "e\u0301.bin", Size: second.Size, Hash: second.Hash}
	{
		err := f.write(func(tx app.WriteTx) error {
			_, err := tx.RecordRange(next, 2, hash("inventory-two"))
			if err != nil {
				return err
			}
			return tx.ReleasePin("second-stage")
		})
		Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })
	}

	f.reopen()
	current, err = f.s.Upload(context.Background(), upload.ID)
	Expect(err != nil || current.Checkpoint.Generation != 3 || current.Checkpoint.AcceptedBytes != "3145728" || current.Checkpoint.Complete).To(BeFalse(), func() string { return fmt.Sprintf("resume checkpoint: %+v %v", current, err) })

	cursorPath := ""
	cursorOffset := int64(-1)
	received := []domain.UploadRange{}
	for {
		batch, err := f.s.Ranges(context.Background(), upload.ID, cursorPath, cursorOffset, 1)
		Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })

		if len(batch) == 0 {
			break
		}
		received = append(received, batch...)
		cursorPath = batch[0].Path
		cursorOffset = batch[0].Offset
	}
	Expect(len(received) != 2 || received[0].Path != next.Path || received[1].Path != "nested/한글.unknown").To(BeFalse(), func() string { return fmt.Sprintf("range cursor lost exact UTF8 path/order: %+v", received) })

	for _, object := range []domain.Object{first, second} {
		{
			err := f.s.Collect(context.Background(), object)
			Expect(!errors.Is(err, domain.ErrPinned)).To(BeFalse(), func() string { return fmt.Sprint("accepted range lost upload pin", err) })
		}

	}
	var complete domain.Checkpoint
	{
		err := f.write(func(tx app.WriteTx) error {
			var err error
			complete, err = tx.CompleteCheckpoint(upload.ID, 3, hash("inventory-two"))
			return err
		})
		Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })
	}
	Expect(!complete.Complete || complete.Generation != 4 || complete.AcceptedBytes != "3145728").To(BeFalse(), func() string { return fmt.Sprint(fmt.Sprint("completion changed accepted bytes: ", complete)) })

})
var _ = It("TestAbandonedRangeHistoryDoesNotRetainOrReviveObjects", Label("persistence"), func() {
	t := GinkgoT()

	f := newFixture(t, nil)
	uuid := func() string {
		id, err := service.NewID()
		Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })

		return id
	}
	data := "accepted bytes later explicitly abandoned"
	object := f.publish(data, "pre-transfer")
	target, _ := json.Marshal(map[string]any{"project_id": uuid(), "document_id": nil})
	upload := domain.Upload{ID: uuid(), PrincipalID: uuid(), Purpose: "result", InputMode: "file", Target: target, Checkpoint: domain.Checkpoint{Generation: 1, InventorySHA256: app.EmptyInventorySHA256, AcceptedBytes: "0"}}
	{
		err := f.write(func(tx app.WriteTx) error {
			if err := tx.CreateUpload(upload); err != nil {
				return err
			}
			if _, err := tx.RecordRange(domain.UploadRange{UploadID: upload.ID, Path: "result.unknown", Size: object.Size, Hash: object.Hash}, 1, object.Hash); err != nil {
				return err
			}
			return tx.ReleasePin("pre-transfer")
		})
		Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })
	}
	{

		err := f.write(func(tx app.WriteTx) error { return tx.AbandonUpload(upload.ID) })
		Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })
	}
	{

		err := f.s.Collect(context.Background(), object)
		Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprintf("abandoned historical range FK prevented completed GC: %v", err) })
	}

	rows, err := f.s.Ranges(context.Background(), upload.ID, "", -1, 10)
	Expect(err != nil || len(rows) != 1 || rows[0].Hash != object.Hash).To(BeFalse(), func() string { return fmt.Sprint("abandoned history lost", err) })
	{

		_, err := f.object(object.Hash)
		Expect(!errors.Is(err, domain.ErrNotFound)).To(BeFalse(), func() string { return fmt.Sprint("physical GC retained available metadata") })
	}

	fresh := f.publish(data, "new-owner")
	{
		err := f.write(func(tx app.WriteTx) error {
			return tx.AdoptUploadObject(upload.ID, "result", target, domain.Reference{Kind: "saved", ID: "stale", Hash: fresh.Hash}, "")
		})
		Expect(!errors.Is(err, domain.ErrTransition)).To(BeFalse(), func() string { return fmt.Sprint("historical digest revived abandoned adoption", err) })
	}

	f.reopen()
	{
		err := f.s.Collect(context.Background(), fresh)
		Expect(!errors.Is(err, domain.ErrPinned)).To(BeFalse(), func() string { return fmt.Sprint("new same-hash owner was not protected", err) })
	}

})
