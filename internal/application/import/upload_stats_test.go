package importer_test

import (
	"crypto/sha256"
	"encoding/hex"

	"fmt"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	app "haeryong.local/haeryong/internal/application/storage"
	"haeryong.local/haeryong/internal/domain"
)

var _ = It("TestUploadStatsMatchesAllCursorPinsAndDeduplicatesSharedBytes", Label("persistence"), func() {
	t := GinkgoT()

	f := newFixture(t)
	content := "shared bytes"
	upload := f.sealed(t, nil, content)
	f.imported(t, upload, "")
	sum := sha256.Sum256([]byte(content))
	hash := hex.EncodeToString(sum[:])
	unpublished := key(t)
	{
		e := f.store.Write(ctx, func(tx app.WriteTx) error {
			if e := tx.CreateContentSet(unpublished, app.EmptyInventorySHA256); e != nil {
				return e
			}
			return tx.PutEntry(domain.Entry{ContentSetID: unpublished, Path: "staged-copy", Kind: "file", Hash: hash, Size: int64(len(content))})
		})
		Expect(e != nil).To(BeFalse(), func() string { return fmt.Sprint(e) })
	}

	for batch := 0; batch < 9; batch++ {
		{
			e := f.store.Write(ctx, func(tx app.WriteTx) error {
				for i := 0; i < 15; i++ {
					kind := []string{"backup", "job", "future-owner"}[i%3]
					if e := tx.AcquirePin(domain.Pin{ID: key(t), Kind: kind, OwnerID: key(t), Hash: hash}); e != nil {
						return e
					}
				}
				return nil
			})
			Expect(e != nil).To(BeFalse(), func() string { return fmt.Sprint(e) })
		}

	}
	var stats domain.UploadStats
	{
		e := f.store.Write(ctx, func(tx app.WriteTx) error { var e error; stats, e = tx.UploadStats(upload); return e })
		Expect(e != nil).To(BeFalse(), func() string { return fmt.Sprint(e) })
	}
	Expect(stats.StagingBytes != "12" || stats.Staging != 47 || stats.Adopted != 1 || stats.Active != 45 || stats.Recovery != 45).To(BeFalse(), func() string {
		return fmt.Sprint("summary changed actual ownership or duplicated shared object bytes", stats)
	})

	after := ""
	count := 0
	cursorStats := domain.UploadStats{}
	for {
		var rows []domain.Pin
		{
			e := f.store.Write(ctx, func(tx app.WriteTx) error { var e error; rows, e = tx.UploadPins(upload, after, 17); return e })
			Expect(e != nil).To(BeFalse(), func() string { return fmt.Sprint(e) })
		}

		for _, pin := range rows {
			Expect(pin.ID <= after || pin.Hash != hash).To(BeFalse(), func() string { return fmt.Sprint("cursor changed exact pin identity/order") })

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
	Expect(count != 138 || cursorStats.Staging != stats.Staging || cursorStats.Adopted != stats.Adopted || cursorStats.Active != stats.Active || cursorStats.Recovery != stats.Recovery).To(BeFalse(), func() string {
		return fmt.Sprint("summary diverged from complete cursor ownership", count, stats, cursorStats)
	})
	{

		e := f.store.Collect(ctx, domain.Object{Hash: hash, Size: int64(len(content))})
		Expect(e != domain.ErrPinned).To(BeFalse(), func() string { return fmt.Sprint("statistics altered live ownership", e) })
	}

})
