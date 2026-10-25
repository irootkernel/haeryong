package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"haeryong.local/haeryong/internal/application/service"
	app "haeryong.local/haeryong/internal/application/storage"
	"haeryong.local/haeryong/internal/domain"
	driver "modernc.org/sqlite"
)

// M1 experiment: the test-owned collation pauses the real VACUUM statement
// while its snapshot is being copied, rather than merely before dispatch.
var _ = It("Copy snapshot dedicated VACUUM permits a live store write and keeps one snapshot", Label("persistence"), func() {
	entered, release := make(chan struct{}), make(chan struct{})
	var enabled atomic.Bool
	var enter, unblock sync.Once
	DeferCleanup(func() { unblock.Do(func() { close(release) }) })
	name := "copy_probe_" + strings.ReplaceAll(time.Now().UTC().Format("150405.000000000"), ".", "")
	Expect(driver.RegisterCollationUtf8(name, func(a, b string) int {
		if enabled.Load() {
			enter.Do(func() { close(entered) })
			<-release
		}
		return strings.Compare(a, b)
	})).To(Succeed())
	selection := service.Selection{Profile: "copy-probe", DataDir: filepath.Join(GinkgoT().TempDir(), "library")}
	_, e := service.Initialize(selection)
	Expect(e).NotTo(HaveOccurred())
	w, e := service.OpenWriter(selection)
	Expect(e).NotTo(HaveOccurred())
	DeferCleanup(func() { w.Close() })
	Expect(w.PrepareStorageBootstrap(nil)).To(Succeed())
	s := openStore(GinkgoT(), w, nil)
	// This table exists only in this disposable experimental database. It makes
	// index reconstruction observable and is not admitted by production startup.
	_, err := s.db.Exec("CREATE TABLE copy_probe(value TEXT COLLATE " + name + " UNIQUE); INSERT INTO copy_probe VALUES('a'),('b'),('c')")
	Expect(err).NotTo(HaveOccurred())
	snapshot := filepath.Join(GinkgoT().TempDir(), "snapshot.sqlite")
	enabled.Store(true)
	finished := make(chan error, 1)
	go func() { e := s.SnapshotDatabase(ctx, snapshot); finished <- e }()
	Eventually(entered, 30*time.Second).Should(BeClosed())
	writeCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	Expect(s.Write(writeCtx, func(tx app.WriteTx) error {
		_, e := tx.MutateResource(domain.Resource{Kind: "copy_probe", ID: "concurrent", Value: []byte(`{}`)}, 0, "copy.concurrent_write")
		return e
	})).To(Succeed())
	unblock.Do(func() { close(release) })
	Eventually(finished, 30*time.Second).Should(Receive(Succeed()))
	enabled.Store(false)
	copy, err := sql.Open("sqlite", snapshot)
	Expect(err).NotTo(HaveOccurred())
	defer copy.Close()
	var n int
	Expect(copy.QueryRow("SELECT count(*) FROM events WHERE kind='copy.concurrent_write'").Scan(&n)).To(Succeed())
	Expect(n).To(Equal(0))
	Expect(s.db.QueryRow("SELECT count(*) FROM events WHERE kind='copy.concurrent_write'").Scan(&n)).To(Succeed())
	Expect(n).To(Equal(1))
})

var _ = It("Copies snapshot owner closure deduplicates and crosses every 64 item cursor boundary without omitting missing owners", Label("persistence"), func() {
	_, w := profile(GinkgoT())
	s := openStore(GinkgoT(), w, nil)
	snapshot := filepath.Join(GinkgoT().TempDir(), "snapshot.sqlite")
	Expect(s.SnapshotDatabase(ctx, snapshot)).To(Succeed())
	db, e := openCopy(snapshot)
	Expect(e).NotTo(HaveOccurred())
	defer db.Close()
	// Real schema, controlled immutable owner facts; unowned objects must not join.
	for _, total := range []int{0, 1, 63, 64, 65, 129} {
		_, e = db.Exec(`DELETE FROM object_pins;DELETE FROM object_references;DELETE FROM content_objects`)
		Expect(e).NotTo(HaveOccurred())
		expected := map[string]bool{}
		for n := 0; n < total; n++ {
			hash := fmt.Sprintf("%064x", n+1)
			expected[hash] = true
			_, e = db.Exec(`INSERT INTO content_objects(content_hash,byte_size,state) VALUES(?,?,'ready')`, hash, n)
			Expect(e).NotTo(HaveOccurred())
			_, e = db.Exec(`INSERT INTO object_pins VALUES(?,'backup','owner',?)`, fmt.Sprint(n), hash)
			Expect(e).NotTo(HaveOccurred())
			_, e = db.Exec(`INSERT INTO object_references VALUES('fixture',?,?)`, fmt.Sprint(n), hash)
			Expect(e).NotTo(HaveOccurred())
		}
		_, e = db.Exec(`INSERT INTO content_objects(content_hash,byte_size,state) VALUES(?,0,'ready')`, strings.Repeat("f", 64))
		Expect(e).NotTo(HaveOccurred())
		actual := map[string]bool{}
		after := ""
		for {
			page, e := CopyObjectPage(ctx, snapshot, after)
			Expect(e).NotTo(HaveOccurred())
			if len(page) == 0 {
				break
			}
			Expect(len(page)).To(BeNumerically("<=", 64))
			for _, o := range page {
				Expect(actual[o.Hash]).To(BeFalse())
				actual[o.Hash] = true
				after = o.Hash
			}
		}
		Expect(actual).To(Equal(expected))
	}
	_, e = db.Exec(`UPDATE content_objects SET state='deleting' WHERE content_hash=(SELECT min(content_hash) FROM object_pins)`)
	Expect(e).NotTo(HaveOccurred())
	_, e = CopyObjectPage(ctx, snapshot, "")
	Expect(e).To(HaveOccurred())
	_, e = db.Exec(`UPDATE content_objects SET state='ready'`)
	Expect(e).NotTo(HaveOccurred())
	_, e = db.Exec(`PRAGMA foreign_keys=OFF;DELETE FROM content_objects WHERE content_hash=(SELECT min(content_hash) FROM object_pins)`)
	Expect(e).NotTo(HaveOccurred())
	_, e = CopyObjectPage(ctx, snapshot, "")
	Expect(e).To(HaveOccurred())
})
