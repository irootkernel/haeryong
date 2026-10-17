package importer_test

import (
	"crypto/sha256"
	"encoding/hex"
	"testing"

	app "haeryong.local/haeryong/internal/application/storage"
	"haeryong.local/haeryong/internal/domain"
)

func TestUploadStatsMatchesAllCursorPinsAndDeduplicatesSharedBytes(t *testing.T) {
	f := newFixture(t)
	content := "shared bytes"
	upload := f.sealed(t, nil, content)
	f.imported(t, upload, "")
	sum := sha256.Sum256([]byte(content))
	hash := hex.EncodeToString(sum[:])
	unpublished := key(t)
	if e := f.store.Write(ctx, func(tx app.WriteTx) error {
		if e := tx.CreateContentSet(unpublished, app.EmptyInventorySHA256); e != nil {
			return e
		}
		return tx.PutEntry(domain.Entry{ContentSetID: unpublished, Path: "staged-copy", Kind: "file", Hash: hash, Size: int64(len(content))})
	}); e != nil {
		t.Fatal(e)
	}
	for batch := 0; batch < 9; batch++ {
		if e := f.store.Write(ctx, func(tx app.WriteTx) error {
			for i := 0; i < 15; i++ {
				kind := []string{"backup", "job", "future-owner"}[i%3]
				if e := tx.AcquirePin(domain.Pin{ID: key(t), Kind: kind, OwnerID: key(t), Hash: hash}); e != nil {
					return e
				}
			}
			return nil
		}); e != nil {
			t.Fatal(e)
		}
	}
	var stats domain.UploadStats
	if e := f.store.Write(ctx, func(tx app.WriteTx) error { var e error; stats, e = tx.UploadStats(upload); return e }); e != nil {
		t.Fatal(e)
	}
	if stats.StagingBytes != "12" || stats.Staging != 47 || stats.Adopted != 1 || stats.Active != 45 || stats.Recovery != 45 {
		t.Fatal("summary changed actual ownership or duplicated shared object bytes", stats)
	}
	after := ""
	count := 0
	cursorStats := domain.UploadStats{}
	for {
		var rows []domain.Pin
		if e := f.store.Write(ctx, func(tx app.WriteTx) error { var e error; rows, e = tx.UploadPins(upload, after, 17); return e }); e != nil {
			t.Fatal(e)
		}
		for _, pin := range rows {
			if pin.ID <= after || pin.Hash != hash {
				t.Fatal("cursor changed exact pin identity/order")
			}
			after = pin.ID
			count++
			switch pin.Kind {
			case "adopted":
				cursorStats.Adopted++
			case "job", "input", "upload_read":
				cursorStats.Active++
			case "backup", "recovery", "draft":
				cursorStats.Recovery++
			default:
				cursorStats.Staging++
			}
		}
		if len(rows) < 17 {
			break
		}
	}
	if count != 138 || cursorStats.Staging != stats.Staging || cursorStats.Adopted != stats.Adopted || cursorStats.Active != stats.Active || cursorStats.Recovery != stats.Recovery {
		t.Fatal("summary diverged from complete cursor ownership", count, stats, cursorStats)
	}
	if e := f.store.Collect(ctx, domain.Object{Hash: hash, Size: int64(len(content))}); e != domain.ErrPinned {
		t.Fatal("statistics altered live ownership", e)
	}
}
