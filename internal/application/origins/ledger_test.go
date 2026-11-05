package origins

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func scratchBase() string {
	dir, err := os.MkdirTemp(os.TempDir(), "haeryong-origins-")
	ExpectWithOffset(1, err).NotTo(HaveOccurred())
	return dir
}

// id builds a canonical UUID-shaped identifier from a short numeric seed so
// ledger validation sees realistic hex-only service identities.
func id(seed int) string {
	return fmt.Sprintf("00000000-0000-0000-0000-%012d", seed)
}

func digestFor(seed byte) string {
	out := make([]byte, 64)
	for i := range out {
		out[i] = '0' + seed%10
	}
	return string(out)
}

func reservation(library, version, set, digest string) Reservation {
	return Reservation{LibraryID: library, VersionID: version, ContentSetID: set, PayloadSHA256: digest}
}

func ledgerBytes(base string) []byte {
	raw, err := os.ReadFile(filepath.Join(base, "origin-state", "ledger.json"))
	ExpectWithOffset(1, err).NotTo(HaveOccurred())
	return raw
}

func writeScratchMarker(base, state, ledgerID string) {
	marker := markerFile{State: state, AppStateID: id(997), LedgerID: ledgerID, CreatedAt: "2026-10-18T00:00:00Z"}
	raw, err := json.Marshal(marker)
	ExpectWithOffset(1, err).NotTo(HaveOccurred())
	ExpectWithOffset(1, os.WriteFile(filepath.Join(base, "app-state.json"), raw, 0o600)).To(Succeed())
}

var _ = Describe("Pool and origin math", Label("unit"), func() {
	It("canonicalizes origins to exact loopback strings", func() {
		Expect(Origin(9281)).To(Equal("http://127.0.0.1:9281"))
	})
	It("bounds the pool", func() {
		pool := Pool{BasePort: 9281, Count: 8}
		Expect(pool.Contains(9280)).To(BeFalse())
		Expect(pool.Contains(9281)).To(BeTrue())
		Expect(pool.Contains(9288)).To(BeTrue())
		Expect(pool.Contains(9289)).To(BeFalse())
	})
	It("refuses exactly the frozen legacy fixture ports", func() {
		Expect(LegacyReserved(9271)).To(BeFalse())
		Expect(LegacyReserved(9272)).To(BeTrue())
		Expect(LegacyReserved(9273)).To(BeTrue())
		Expect(LegacyReserved(9274)).To(BeFalse())
	})
	It("rejects non-canonical reservation inputs before touching state", func() {
		base := scratchBase()
		DeferCleanup(func() { Expect(os.RemoveAll(base)).To(Succeed()) })
		untouched := filepath.Join(base, "untouched")
		manager := NewManager(untouched, Faults{})
		_, err := manager.Reserve(reservation("not-a-uuid", id(101), id(201), digestFor(1)), Pool{BasePort: 9281, Count: 8}, nil)
		Expect(err).To(MatchError(&Error{Reason: ReasonInvalidLedger}))
		_, err = manager.Reserve(reservation(id(1), "not-a-uuid", id(201), digestFor(1)), Pool{BasePort: 9281, Count: 8}, nil)
		Expect(err).To(MatchError(&Error{Reason: ReasonInvalidLedger}))
		_, err = manager.Reserve(reservation(id(1), id(101), "not-a-uuid", digestFor(1)), Pool{BasePort: 9281, Count: 8}, nil)
		Expect(err).To(MatchError(&Error{Reason: ReasonInvalidLedger}))
		_, err = manager.Reserve(reservation(id(1), id(101), id(201), "not-a-digest"), Pool{BasePort: 9281, Count: 8}, nil)
		Expect(err).To(MatchError(&Error{Reason: ReasonInvalidLedger}))
		_, err = manager.Reserve(reservation(id(1), id(101), id(201), digestFor(1)), Pool{BasePort: 80, Count: 8}, nil)
		Expect(err).To(MatchError(&Error{Reason: ReasonInvalidLedger}))
		_, err = manager.Reserve(reservation(id(1), id(101), id(201), digestFor(1)), Pool{BasePort: 9281, Count: 0}, nil)
		Expect(err).To(MatchError(&Error{Reason: ReasonInvalidLedger}))
		badPort := reservation(id(1), id(101), id(201), digestFor(1))
		badPort.PreferredPort = 80
		_, err = manager.Reserve(badPort, Pool{BasePort: 9281, Count: 8}, nil)
		Expect(err).To(MatchError(&Error{Reason: ReasonInvalidLedger}))
		badAlias := reservation(id(1), id(101), id(201), digestFor(1))
		badAlias.ExternalOrigin = "http://node.tailnet.ts.net:9291"
		_, err = manager.Reserve(badAlias, Pool{BasePort: 9281, Count: 8}, nil)
		Expect(err).To(MatchError(&Error{Reason: ReasonInvalidLedger}))
		// Invalid input never reaches the filesystem: no state was created.
		Expect(untouched).NotTo(BeAnExistingFile())
	})
	It("canonicalizes external HTTPS origins and rejects non-origin forms", func() {
		Expect(CanonicalExternalOrigin("https://Node.Tailnet.ts.net:443")).To(Equal("https://node.tailnet.ts.net"))
		Expect(CanonicalExternalOrigin("https://node.tailnet.ts.net")).To(Equal("https://node.tailnet.ts.net"))
		Expect(CanonicalExternalOrigin("https://Node.Tailnet.ts.net:09291")).To(Equal("https://node.tailnet.ts.net:9291"))
		for _, invalid := range []string{
			"",
			"http://node.tailnet.ts.net:9291",
			"HTTPS://node.tailnet.ts.net:9291",
			"https://node.tailnet.ts.net:9291/",
			"https://node.tailnet.ts.net:9291?x=1",
			"https://node.tailnet.ts.net:9291#frag",
			"https://user@node.tailnet.ts.net:9291",
			"https://*.tailnet.ts.net:9291",
			"https://node.tailnet.ts.net:70000",
		} {
			_, err := CanonicalExternalOrigin(invalid)
			Expect(err).To(HaveOccurred(), "expected %q to fail canonicalization", invalid)
		}
	})
})

var _ = Describe("Ledger reservations", Label("persistence"), func() {
	var base string

	BeforeEach(func() { base = scratchBase() })
	AfterEach(func() { os.RemoveAll(base) })

	It("initializes first use with a ready marker and a durable filled ledger", func() {
		manager := NewManager(base, Faults{})
		record, err := manager.Reserve(reservation(id(1+0), id(1+100), id(1+200), digestFor(1)), Pool{BasePort: 9281, Count: 8}, nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(record.Port).To(Equal(9281))
		Expect(record.State).To(Equal(StateActive))
		Expect(Origin(record.Port)).To(Equal("http://127.0.0.1:9281"))
		raw, err := os.ReadFile(filepath.Join(base, "app-state.json"))
		Expect(err).NotTo(HaveOccurred())
		var marker markerFile
		Expect(json.Unmarshal(raw, &marker)).To(Succeed())
		Expect(marker.State).To(Equal(MarkerReady))
		info, err := os.Stat(filepath.Join(base, "origin-state"))
		Expect(err).NotTo(HaveOccurred())
		Expect(info.Mode().Perm()).To(Equal(os.FileMode(0o700)))
		file, err := os.Stat(filepath.Join(base, "origin-state", "ledger.json"))
		Expect(err).NotTo(HaveOccurred())
		Expect(file.Mode().Perm()).To(Equal(os.FileMode(0o600)))
	})

	It("creates the managed base under realistic 0755 user parents without modifying them", func() {
		home := filepath.Join(base, "home")
		support := filepath.Join(home, "Library", "Application Support")
		Expect(os.MkdirAll(support, 0o755)).To(Succeed())
		parents := []string{home, filepath.Join(home, "Library"), support}
		// The common gate intentionally uses umask 077. Establish this
		// fixture's intended existing permissions explicitly, without changing
		// the process mask or any real account directory.
		for _, parent := range parents {
			Expect(os.Chmod(parent, 0o755)).To(Succeed())
		}
		manager := NewManager(filepath.Join(support, "Haeryong"), Faults{})
		_, err := manager.Reserve(reservation(id(1+0), id(1+100), id(1+200), digestFor(1)), Pool{BasePort: 9281, Count: 8}, nil)
		Expect(err).NotTo(HaveOccurred())
		created, err := os.Stat(filepath.Join(support, "Haeryong"))
		Expect(err).NotTo(HaveOccurred())
		Expect(created.Mode().Perm()).To(Equal(os.FileMode(0o700)))
		for _, parent := range parents {
			kept, err := os.Stat(parent)
			Expect(err).NotTo(HaveOccurred())
			Expect(kept.Mode().Perm()).To(Equal(os.FileMode(0o755)))
		}
	})

	It("reuses the exact tuple without rewriting the ledger", func() {
		manager := NewManager(base, Faults{})
		first, err := manager.Reserve(reservation(id(1+0), id(1+100), id(1+200), digestFor(1)), Pool{BasePort: 9281, Count: 8}, nil)
		Expect(err).NotTo(HaveOccurred())
		before := ledgerBytes(base)
		second, err := NewManager(base, Faults{}).Reserve(reservation(id(1+0), id(1+100), id(1+200), digestFor(1)), Pool{BasePort: 9281, Count: 8}, nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(second).To(Equal(first))
		Expect(ledgerBytes(base)).To(Equal(before))
	})

	It("returns a sticky record unchanged even when an exclusion now covers its port", func() {
		manager := NewManager(base, Faults{})
		first, err := manager.Reserve(reservation(id(1+0), id(1+100), id(1+200), digestFor(1)), Pool{BasePort: 9281, Count: 8}, nil)
		Expect(err).NotTo(HaveOccurred())
		before := ledgerBytes(base)
		again, err := manager.Reserve(reservation(id(1+0), id(1+100), id(1+200), digestFor(1)), Pool{BasePort: 9281, Count: 8}, func(port int) bool { return port == first.Port })
		Expect(err).NotTo(HaveOccurred())
		Expect(again).To(Equal(first))
		Expect(ledgerBytes(base)).To(Equal(before))
	})

	It("rejects an immutable identity conflict before publishing anything", func() {
		manager := NewManager(base, Faults{})
		_, err := manager.Reserve(reservation(id(1+0), id(1+100), id(1+200), digestFor(1)), Pool{BasePort: 9281, Count: 8}, nil)
		Expect(err).NotTo(HaveOccurred())
		before := ledgerBytes(base)
		_, err = manager.Reserve(reservation(id(1+0), id(1+100), id(2+200), digestFor(2)), Pool{BasePort: 9281, Count: 8}, nil)
		Expect(err).To(MatchError(&Error{Reason: ReasonInvalidLedger}))
		Expect(ledgerBytes(base)).To(Equal(before))
	})

	It("allocates distinct ports for concurrent reservations through one manager", func() {
		manager := NewManager(base, Faults{})
		pool := Pool{BasePort: 9281, Count: 8}
		const workers = 6
		type outcome struct {
			record Record
			err    error
		}
		results := make([]outcome, workers)
		var wait sync.WaitGroup
		for i := 0; i < workers; i++ {
			wait.Add(1)
			go func(slot int) {
				defer wait.Done()
				record, err := manager.Reserve(reservation(id(slot+300), id(1+100), id(1+200), digestFor(byte(slot+1))), pool, nil)
				results[slot] = outcome{record: record, err: err}
			}(i)
		}
		wait.Wait()
		seen := map[int]bool{}
		for _, result := range results {
			Expect(result.err).NotTo(HaveOccurred())
			Expect(seen[result.record.Port]).To(BeFalse())
			seen[result.record.Port] = true
		}
	})

	It("converges concurrent first use from separate managers onto one ledger", func() {
		pool := Pool{BasePort: 9281, Count: 8}
		first := NewManager(base, Faults{})
		second := NewManager(base, Faults{})
		var wait sync.WaitGroup
		outcomes := make(chan Record, 2)
		failures := make(chan error, 2)
		wait.Add(2)
		go func() {
			defer wait.Done()
			record, err := first.Reserve(reservation(id(1+0), id(1+100), id(1+200), digestFor(1)), pool, nil)
			if err != nil {
				failures <- err
				return
			}
			outcomes <- record
		}()
		go func() {
			defer wait.Done()
			record, err := second.Reserve(reservation(id(2+0), id(2+100), id(2+200), digestFor(2)), pool, nil)
			if err != nil {
				failures <- err
				return
			}
			outcomes <- record
		}()
		wait.Wait()
		close(outcomes)
		close(failures)
		for err := range failures {
			Expect(err).NotTo(HaveOccurred())
		}
		records := []Record{}
		for record := range outcomes {
			records = append(records, record)
		}
		Expect(records).To(HaveLen(2))
		Expect(records[0].Port).NotTo(Equal(records[1].Port))
		var ledger ledgerFile
		Expect(json.Unmarshal(ledgerBytes(base), &ledger)).To(Succeed())
		Expect(ledger.Records).To(HaveLen(2))
		raw, err := os.ReadFile(filepath.Join(base, "app-state.json"))
		Expect(err).NotTo(HaveOccurred())
		var marker markerFile
		Expect(json.Unmarshal(raw, &marker)).To(Succeed())
		Expect(marker.State).To(Equal(MarkerReady))
		Expect(marker.LedgerID).To(Equal(ledger.LedgerID))
	})

	It("never allocates legacy or excluded ports and reports exhaustion", func() {
		manager := NewManager(base, Faults{})
		pool := Pool{BasePort: 9270, Count: 6}
		exclude := func(port int) bool { return port == 9270 || port == 9271 }
		var ports []int
		for i := 0; i < 2; i++ {
			record, err := manager.Reserve(reservation(id(i+400), id(1+100), id(1+200), digestFor(byte(i+1))), pool, exclude)
			Expect(err).NotTo(HaveOccurred())
			ports = append(ports, record.Port)
		}
		Expect(ports).To(Equal([]int{9274, 9275}))
		_, err := manager.Reserve(reservation(id(995), id(1+100), id(1+200), digestFor(9)), pool, exclude)
		Expect(err).To(MatchError(&Error{Reason: ReasonExhausted}))
	})

	It("refuses a loaded record claiming a frozen legacy port", func() {
		manager := NewManager(base, Faults{})
		_, err := manager.Reserve(reservation(id(1+0), id(1+100), id(1+200), digestFor(1)), Pool{BasePort: 9281, Count: 8}, nil)
		Expect(err).NotTo(HaveOccurred())
		var ledger ledgerFile
		Expect(json.Unmarshal(ledgerBytes(base), &ledger)).To(Succeed())
		ledger.Records = append(ledger.Records, Record{Port: 9272, LibraryID: id(2 + 0), VersionID: id(2 + 100), ContentSetID: id(2 + 200), PayloadSHA256: digestFor(2), State: StateActive, CreatedAt: "2026-10-18T00:00:00Z"})
		raw, err := json.Marshal(ledger)
		Expect(err).NotTo(HaveOccurred())
		Expect(os.WriteFile(filepath.Join(base, "origin-state", "ledger.json"), raw, 0o600)).To(Succeed())
		_, err = NewManager(base, Faults{}).Reserve(reservation(id(3+0), id(3+100), id(3+200), digestFor(3)), Pool{BasePort: 9281, Count: 8}, nil)
		Expect(err).To(MatchError(&Error{Reason: ReasonInvalidLedger}))
	})

	It("cleans crashed owned publication temp files under the lock", func() {
		Expect(os.MkdirAll(filepath.Join(base, "origin-state"), 0o700)).To(Succeed())
		crashed := filepath.Join(base, "origin-state", ".ledger.abandoned.tmp")
		Expect(os.WriteFile(crashed, []byte("{}"), 0o600)).To(Succeed())
		other := filepath.Join(base, "origin-state", "unrelated.txt")
		Expect(os.WriteFile(other, []byte("keep"), 0o600)).To(Succeed())
		manager := NewManager(base, Faults{})
		_, err := manager.Reserve(reservation(id(1+0), id(1+100), id(1+200), digestFor(1)), Pool{BasePort: 9281, Count: 8}, nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(crashed).NotTo(BeAnExistingFile())
		Expect(other).To(BeAnExistingFile())
	})

	It("leaves no reservation when the fault strikes before the rename", func() {
		manager := NewManager(base, Faults{BeforeRename: func() error { return fmt.Errorf("crash before rename") }})
		_, err := manager.Reserve(reservation(id(1+0), id(1+100), id(1+200), digestFor(1)), Pool{BasePort: 9281, Count: 8}, nil)
		Expect(err).To(MatchError("crash before rename"))
		_, statErr := os.Stat(filepath.Join(base, "origin-state", "ledger.json"))
		Expect(os.IsNotExist(statErr)).To(BeTrue())
		record, err := NewManager(base, Faults{}).Reserve(reservation(id(1+0), id(1+100), id(1+200), digestFor(1)), Pool{BasePort: 9281, Count: 8}, nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(record.Port).To(Equal(9281))
	})

	It("keeps a post-rename reservation unknown for the process and resolves it via the ledger after restart", func() {
		healthy := NewManager(base, Faults{})
		_, err := healthy.Reserve(reservation(id(1+0), id(1+100), id(1+200), digestFor(1)), Pool{BasePort: 9281, Count: 8}, nil)
		Expect(err).NotTo(HaveOccurred())
		before := ledgerBytes(base)
		faulted := NewManager(base, Faults{AfterRename: func() error { return fmt.Errorf("crash before dirsync") }})
		_, err = faulted.Reserve(reservation(id(2+0), id(2+100), id(2+200), digestFor(2)), Pool{BasePort: 9281, Count: 8}, nil)
		Expect(err).To(MatchError(&Error{Reason: ReasonUnknownPublication}))
		// The running process makes no further free/reuse decision.
		_, err = faulted.Reserve(reservation(id(3+0), id(3+100), id(3+200), digestFor(3)), Pool{BasePort: 9281, Count: 8}, nil)
		Expect(err).To(MatchError(&Error{Reason: ReasonUnknownPublication}))
		Expect(string(ledgerBytes(base))).NotTo(Equal(string(before)))
		restarted := NewManager(base, Faults{})
		// A different identity never claims the unknown-window port...
		other, err := restarted.Reserve(reservation(id(3+0), id(3+100), id(3+200), digestFor(3)), Pool{BasePort: 9281, Count: 8}, nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(other.Port).To(Equal(9283))
		// ...while the same tuple retry resolves via the survived ledger record.
		same, err := restarted.Reserve(reservation(id(2+0), id(2+100), id(2+200), digestFor(2)), Pool{BasePort: 9281, Count: 8}, nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(same.Port).To(Equal(9282))
	})

	It("finalizes an interrupted initialization idempotently", func() {
		manager := NewManager(base, Faults{BeforeMarkerReady: func() error { return fmt.Errorf("crash before ready") }})
		_, err := manager.Reserve(reservation(id(1+0), id(1+100), id(1+200), digestFor(1)), Pool{BasePort: 9281, Count: 8}, nil)
		Expect(err).To(MatchError("crash before ready"))
		raw, err := os.ReadFile(filepath.Join(base, "app-state.json"))
		Expect(err).NotTo(HaveOccurred())
		var marker markerFile
		Expect(json.Unmarshal(raw, &marker)).To(Succeed())
		Expect(marker.State).To(Equal(MarkerInitializing))
		var ledger ledgerFile
		Expect(json.Unmarshal(ledgerBytes(base), &ledger)).To(Succeed())
		Expect(ledger.LedgerID).To(Equal(marker.LedgerID))
		record, err := NewManager(base, Faults{}).Reserve(reservation(id(1+0), id(1+100), id(1+200), digestFor(1)), Pool{BasePort: 9281, Count: 8}, nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(record.Port).To(Equal(9281))
		raw, err = os.ReadFile(filepath.Join(base, "app-state.json"))
		Expect(err).NotTo(HaveOccurred())
		Expect(json.Unmarshal(raw, &marker)).To(Succeed())
		Expect(marker.State).To(Equal(MarkerReady))
		Expect(marker.LedgerID).To(Equal(ledger.LedgerID))
	})

	It("restarts initialization with a fresh ledger when nothing was published", func() {
		Expect(os.MkdirAll(filepath.Join(base, "origin-state"), 0o700)).To(Succeed())
		stale := id(998)
		writeScratchMarker(base, MarkerInitializing, stale)
		manager := NewManager(base, Faults{})
		_, err := manager.Reserve(reservation(id(1+0), id(1+100), id(1+200), digestFor(1)), Pool{BasePort: 9281, Count: 8}, nil)
		Expect(err).NotTo(HaveOccurred())
		var ledger ledgerFile
		Expect(json.Unmarshal(ledgerBytes(base), &ledger)).To(Succeed())
		Expect(ledger.LedgerID).NotTo(Equal(stale))
		raw, err := os.ReadFile(filepath.Join(base, "app-state.json"))
		Expect(err).NotTo(HaveOccurred())
		var marker markerFile
		Expect(json.Unmarshal(raw, &marker)).To(Succeed())
		Expect(marker.State).To(Equal(MarkerReady))
		Expect(marker.LedgerID).To(Equal(ledger.LedgerID))
	})

	It("fails closed on a lost ledger under a ready marker", func() {
		manager := NewManager(base, Faults{})
		_, err := manager.Reserve(reservation(id(1+0), id(1+100), id(1+200), digestFor(1)), Pool{BasePort: 9281, Count: 8}, nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(os.Remove(filepath.Join(base, "origin-state", "ledger.json"))).To(Succeed())
		_, err = NewManager(base, Faults{}).Reserve(reservation(id(2+0), id(2+100), id(2+200), digestFor(2)), Pool{BasePort: 9281, Count: 8}, nil)
		Expect(err).To(MatchError(&Error{Reason: ReasonLostLedger}))
	})

	It("fails closed on a foreign ledger without any marker", func() {
		Expect(os.MkdirAll(filepath.Join(base, "origin-state"), 0o700)).To(Succeed())
		ledger := ledgerFile{LedgerID: id(999), CreatedAt: "2026-10-18T00:00:00Z", Records: []Record{}}
		raw, err := json.Marshal(ledger)
		Expect(err).NotTo(HaveOccurred())
		Expect(os.WriteFile(filepath.Join(base, "origin-state", "ledger.json"), raw, 0o600)).To(Succeed())
		_, err = NewManager(base, Faults{}).Reserve(reservation(id(1+0), id(1+100), id(1+200), digestFor(1)), Pool{BasePort: 9281, Count: 8}, nil)
		Expect(err).To(MatchError(&Error{Reason: ReasonForeignState}))
	})

	It("fails closed on corrupt bytes, unknown shapes and structural conflicts", func() {
		manager := NewManager(base, Faults{})
		first, err := manager.Reserve(reservation(id(1+0), id(1+100), id(1+200), digestFor(1)), Pool{BasePort: 9281, Count: 8}, nil)
		Expect(err).NotTo(HaveOccurred())
		ledgerPath := filepath.Join(base, "origin-state", "ledger.json")
		var ledger ledgerFile
		Expect(json.Unmarshal(ledgerBytes(base), &ledger)).To(Succeed())
		Expect(os.WriteFile(ledgerPath, []byte("{not json"), 0o600)).To(Succeed())
		Expect(NewManager(base, Faults{}).BindingMatch([]byte(`{"ledger_id":"` + id(999) + `","path":"` + ledgerPath + `"}`))).To(MatchError(&Error{Reason: ReasonCorruptState}))
		ledger.Records = append(ledger.Records, Record{Port: first.Port, LibraryID: id(2 + 0), VersionID: id(2 + 100), ContentSetID: id(2 + 200), PayloadSHA256: digestFor(2), State: StateActive, CreatedAt: "2026-10-18T00:00:00Z"})
		raw, err := json.Marshal(ledger)
		Expect(err).NotTo(HaveOccurred())
		Expect(os.WriteFile(ledgerPath, raw, 0o600)).To(Succeed())
		_, err = NewManager(base, Faults{}).Reserve(reservation(id(3+0), id(3+100), id(3+200), digestFor(3)), Pool{BasePort: 9281, Count: 8}, nil)
		Expect(err).To(MatchError(&Error{Reason: ReasonInvalidLedger}))
		// Unknown ledger fields are rejected even with an otherwise valid body.
		unknown, err := json.Marshal(map[string]any{"ledger_id": ledger.LedgerID, "created_at": "2026-10-18T00:00:00Z", "records": []any{}, "extra": 1})
		Expect(err).NotTo(HaveOccurred())
		Expect(os.WriteFile(ledgerPath, unknown, 0o600)).To(Succeed())
		_, err = NewManager(base, Faults{}).Reserve(reservation(id(4+0), id(4+100), id(4+200), digestFor(4)), Pool{BasePort: 9281, Count: 8}, nil)
		Expect(err).To(MatchError(&Error{Reason: ReasonCorruptState}))
	})

	It("fails closed on null records without reassigning a reserved origin", func() {
		pool := Pool{BasePort: 9281, Count: 8}
		first, err := NewManager(base, Faults{}).Reserve(reservation(id(1), id(101), id(201), digestFor(1)), pool, nil)
		Expect(err).NotTo(HaveOccurred())
		var fields map[string]json.RawMessage
		Expect(json.Unmarshal(ledgerBytes(base), &fields)).To(Succeed())
		fields["records"] = json.RawMessage("null")
		corrupt, err := json.Marshal(fields)
		Expect(err).NotTo(HaveOccurred())
		Expect(os.WriteFile(filepath.Join(base, "origin-state", "ledger.json"), corrupt, 0o600)).To(Succeed())
		markerPath := filepath.Join(base, "app-state.json")
		markerBefore, err := os.ReadFile(markerPath)
		Expect(err).NotTo(HaveOccurred())

		record, err := NewManager(base, Faults{}).Reserve(reservation(id(2), id(102), id(202), digestFor(2)), pool, nil)
		Expect(err).To(MatchError(&Error{Reason: ReasonCorruptState}), "invalid records:null must not reassign reserved origin %s to %+v", Origin(first.Port), record)
		Expect(record).To(Equal(Record{}))
		Expect(ledgerBytes(base)).To(Equal(corrupt))
		markerAfter, err := os.ReadFile(markerPath)
		Expect(err).NotTo(HaveOccurred())
		Expect(markerAfter).To(Equal(markerBefore))
	})

	It("fails closed on an unknown marker shape", func() {
		Expect(os.MkdirAll(filepath.Join(base, "origin-state"), 0o700)).To(Succeed())
		unknown, err := json.Marshal(map[string]any{"state": MarkerReady, "app_state_id": id(997), "ledger_id": id(998), "created_at": "2026-10-18T00:00:00Z", "extra": true})
		Expect(err).NotTo(HaveOccurred())
		Expect(os.WriteFile(filepath.Join(base, "app-state.json"), unknown, 0o600)).To(Succeed())
		_, err = NewManager(base, Faults{}).Reserve(reservation(id(1+0), id(1+100), id(1+200), digestFor(1)), Pool{BasePort: 9281, Count: 8}, nil)
		Expect(err).To(MatchError(&Error{Reason: ReasonCorruptState}))
	})

	It("never reuses a tombstoned identity or port", func() {
		manager := NewManager(base, Faults{})
		first, err := manager.Reserve(reservation(id(1+0), id(1+100), id(1+200), digestFor(1)), Pool{BasePort: 9281, Count: 8}, nil)
		Expect(err).NotTo(HaveOccurred())
		var ledger ledgerFile
		Expect(json.Unmarshal(ledgerBytes(base), &ledger)).To(Succeed())
		ledger.Records[0].State = StateTombstoned
		raw, err := json.Marshal(ledger)
		Expect(err).NotTo(HaveOccurred())
		Expect(os.WriteFile(filepath.Join(base, "origin-state", "ledger.json"), raw, 0o600)).To(Succeed())
		restarted := NewManager(base, Faults{})
		_, err = restarted.Reserve(reservation(id(1+0), id(1+100), id(1+200), digestFor(1)), Pool{BasePort: 9281, Count: 8}, nil)
		Expect(err).To(MatchError(&Error{Reason: ReasonRetiredIdentity}))
		other, err := restarted.Reserve(reservation(id(2+0), id(2+100), id(2+200), digestFor(2)), Pool{BasePort: 9281, Count: 8}, nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(other.Port).To(Equal(first.Port + 1))
	})

	It("validates per-library binding markers against the opened ledger under the shared lock", func() {
		manager := NewManager(base, Faults{})
		_, err := manager.Reserve(reservation(id(1+0), id(1+100), id(1+200), digestFor(1)), Pool{BasePort: 9281, Count: 8}, nil)
		Expect(err).NotTo(HaveOccurred())
		var ledger ledgerFile
		Expect(json.Unmarshal(ledgerBytes(base), &ledger)).To(Succeed())
		ledgerPath := filepath.Join(base, "origin-state", "ledger.json")
		good, err := json.Marshal(Identity{LedgerID: ledger.LedgerID, Path: ledgerPath})
		Expect(err).NotTo(HaveOccurred())
		Expect(manager.BindingMatch(good)).To(Succeed())
		wrongID, err := json.Marshal(Identity{LedgerID: id(996), Path: ledgerPath})
		Expect(err).NotTo(HaveOccurred())
		Expect(manager.BindingMatch(wrongID)).To(MatchError(&Error{Reason: ReasonBindingMismatch}))
		wrongPath, err := json.Marshal(Identity{LedgerID: ledger.LedgerID, Path: "/tmp/elsewhere/ledger.json"})
		Expect(err).NotTo(HaveOccurred())
		Expect(manager.BindingMatch(wrongPath)).To(MatchError(&Error{Reason: ReasonBindingMismatch}))
		// A lost ledger reports lost — never a nil dereference.
		Expect(os.Remove(ledgerPath)).To(Succeed())
		Expect(manager.BindingMatch(good)).To(MatchError(&Error{Reason: ReasonLostLedger}))
		// An initializing marker without a published ledger never validates.
		Expect(os.Remove(filepath.Join(base, "app-state.json"))).To(Succeed())
		writeScratchMarker(base, MarkerInitializing, ledger.LedgerID)
		Expect(manager.BindingMatch(good)).To(MatchError(&Error{Reason: ReasonCorruptState}))
		// And no initialization was performed by the validation itself.
		_, statErr := os.Stat(ledgerPath)
		Expect(os.IsNotExist(statErr)).To(BeTrue())
	})

	It("fails closed on permissive or substituted state files", func() {
		manager := NewManager(base, Faults{})
		_, err := manager.Reserve(reservation(id(1+0), id(1+100), id(1+200), digestFor(1)), Pool{BasePort: 9281, Count: 8}, nil)
		Expect(err).NotTo(HaveOccurred())
		ledger := filepath.Join(base, "origin-state", "ledger.json")
		Expect(os.Chmod(ledger, 0o644)).To(Succeed())
		_, err = NewManager(base, Faults{}).Reserve(reservation(id(2+0), id(2+100), id(2+200), digestFor(2)), Pool{BasePort: 9281, Count: 8}, nil)
		Expect(err).To(MatchError(&Error{Reason: ReasonPrivatePath}))
		Expect(os.Chmod(ledger, 0o600)).To(Succeed())
		lock := filepath.Join(base, "origin-state", "ledger.lock")
		Expect(os.Remove(lock)).To(Succeed())
		Expect(os.Symlink(ledger, lock)).To(Succeed())
		_, err = NewManager(base, Faults{}).Reserve(reservation(id(2+0), id(2+100), id(2+200), digestFor(2)), Pool{BasePort: 9281, Count: 8}, nil)
		Expect(err).To(MatchError(&Error{Reason: ReasonPrivatePath}))
	})

	It("fails closed on a group-writable or symlinked ancestor above the managed base", func() {
		support := filepath.Join(base, "home", "Library", "Application Support")
		Expect(os.MkdirAll(support, 0o755)).To(Succeed())
		Expect(os.Chmod(support, 0o0770)).To(Succeed())
		_, err := NewManager(filepath.Join(support, "Haeryong"), Faults{}).Reserve(reservation(id(1+0), id(1+100), id(1+200), digestFor(1)), Pool{BasePort: 9281, Count: 8}, nil)
		Expect(err).To(MatchError(&Error{Reason: ReasonPrivatePath}))
		Expect(os.Chmod(support, 0o755)).To(Succeed())
		Expect(os.Remove(support)).To(Succeed())
		target := filepath.Join(base, "elsewhere")
		Expect(os.Mkdir(target, 0o755)).To(Succeed())
		Expect(os.Symlink(target, support)).To(Succeed())
		_, err = NewManager(filepath.Join(support, "Haeryong"), Faults{}).Reserve(reservation(id(1+0), id(1+100), id(1+200), digestFor(1)), Pool{BasePort: 9281, Count: 8}, nil)
		Expect(err).To(MatchError(&Error{Reason: ReasonPrivatePath}))
	})

	It("reserves outside-pool preferred ports, records canonical external origin history and refuses cross-tuple or sticky-port conflicts", func() {
		manager := NewManager(base, Faults{})
		pool := Pool{BasePort: 9281, Count: 4}
		firstReq := reservation(id(1), id(101), id(201), digestFor(1))
		firstReq.PreferredPort = 9291
		firstReq.ExternalOrigin = "https://Node.Tailnet.ts.net:9291"
		first, err := manager.Reserve(firstReq, pool, func(port int) bool { return port == 9270 })
		Expect(err).NotTo(HaveOccurred())
		Expect(first.Port).To(Equal(9291))
		Expect(first.ExternalOrigins).To(Equal([]string{"https://node.tailnet.ts.net:9291"}))

		// Repeating the same reservation reuses the record without rewriting the ledger.
		before := ledgerBytes(base)
		same, err := manager.Reserve(firstReq, pool, func(port int) bool { return port == 9270 })
		Expect(err).NotTo(HaveOccurred())
		Expect(same).To(Equal(first))
		Expect(ledgerBytes(base)).To(Equal(before))

		// A new external alias for the same immutable tuple appends durably without releasing the prior alias.
		secondAliasReq := firstReq
		secondAliasReq.ExternalOrigin = "https://Node.Tailnet.ts.net:443"
		updated, err := manager.Reserve(secondAliasReq, pool, nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(updated.Port).To(Equal(9291))
		Expect(updated.ExternalOrigins).To(Equal([]string{"https://node.tailnet.ts.net:9291", "https://node.tailnet.ts.net"}))

		// Sticky port never migrates when a different preferred port is requested for the same tuple.
		wrongPortReq := firstReq
		wrongPortReq.PreferredPort = 9292
		_, err = manager.Reserve(wrongPortReq, pool, nil)
		Expect(err).To(MatchError(&Error{Reason: ReasonOriginConflict}))

		// Another tuple cannot claim an already-owned external alias (including default-port equivalent) or port.
		otherAliasConflict := reservation(id(2), id(102), id(202), digestFor(2))
		otherAliasConflict.PreferredPort = 9292
		otherAliasConflict.ExternalOrigin = "https://node.tailnet.ts.net"
		_, err = manager.Reserve(otherAliasConflict, pool, nil)
		Expect(err).To(MatchError(&Error{Reason: ReasonOriginConflict}))

		otherPortConflict := reservation(id(2), id(102), id(202), digestFor(2))
		otherPortConflict.PreferredPort = 9291
		otherPortConflict.ExternalOrigin = "https://node.tailnet.ts.net:9292"
		_, err = manager.Reserve(otherPortConflict, pool, nil)
		Expect(err).To(MatchError(&Error{Reason: ReasonOriginConflict}))

		// Excluded or legacy preferred ports fail closed with origin_conflict.
		excludedPortReq := reservation(id(2), id(102), id(202), digestFor(2))
		excludedPortReq.PreferredPort = 9270
		_, err = manager.Reserve(excludedPortReq, pool, func(port int) bool { return port == 9270 })
		Expect(err).To(MatchError(&Error{Reason: ReasonOriginConflict}))

		legacyPortReq := reservation(id(2), id(102), id(202), digestFor(2))
		legacyPortReq.PreferredPort = 9272
		_, err = manager.Reserve(legacyPortReq, pool, nil)
		Expect(err).To(MatchError(&Error{Reason: ReasonOriginConflict}))
	})

	It("preserves fault semantics when appending an external origin alias and refuses tombstoned or corrupt external_origins", func() {
		healthy := NewManager(base, Faults{})
		pool := Pool{BasePort: 9281, Count: 4}
		localRec, err := healthy.Reserve(reservation(id(1), id(101), id(201), digestFor(1)), pool, nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(localRec.ExternalOrigins).To(BeEmpty())

		aliasReq := reservation(id(1), id(101), id(201), digestFor(1))
		aliasReq.PreferredPort = localRec.Port
		aliasReq.ExternalOrigin = "https://node.tailnet.ts.net:9291"

		beforeRenameMgr := NewManager(base, Faults{BeforeRename: func() error { return fmt.Errorf("crash before alias rename") }})
		_, err = beforeRenameMgr.Reserve(aliasReq, pool, nil)
		Expect(err).To(MatchError("crash before alias rename"))

		afterRenameMgr := NewManager(base, Faults{AfterRename: func() error { return fmt.Errorf("crash after alias rename") }})
		_, err = afterRenameMgr.Reserve(aliasReq, pool, nil)
		Expect(err).To(MatchError(&Error{Reason: ReasonUnknownPublication}))
		_, err = afterRenameMgr.Reserve(aliasReq, pool, nil)
		Expect(err).To(MatchError(&Error{Reason: ReasonUnknownPublication}))

		// Restart resolves the durable alias from the ledger.
		restarted := NewManager(base, Faults{})
		recovered, err := restarted.Reserve(aliasReq, pool, nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(recovered.ExternalOrigins).To(Equal([]string{"https://node.tailnet.ts.net:9291"}))

		// Tombstoning the record keeps its external alias permanently unavailable to other tuples.
		ledgerPath := filepath.Join(base, "origin-state", "ledger.json")
		var ledger ledgerFile
		Expect(json.Unmarshal(ledgerBytes(base), &ledger)).To(Succeed())
		ledger.Records[0].State = StateTombstoned
		raw, err := json.Marshal(ledger)
		Expect(err).NotTo(HaveOccurred())
		Expect(os.WriteFile(ledgerPath, raw, 0o600)).To(Succeed())

		other := reservation(id(2), id(102), id(202), digestFor(2))
		other.PreferredPort = 9292
		other.ExternalOrigin = "https://node.tailnet.ts.net:9291"
		_, err = NewManager(base, Faults{}).Reserve(other, pool, nil)
		Expect(err).To(MatchError(&Error{Reason: ReasonOriginConflict}))

		// Non-canonical external_origins on disk fail closed with invalid_ledger.
		ledger.Records[0].ExternalOrigins = []string{"https://Node.Tailnet.ts.net:443"}
		raw, err = json.Marshal(ledger)
		Expect(err).NotTo(HaveOccurred())
		Expect(os.WriteFile(ledgerPath, raw, 0o600)).To(Succeed())
		_, err = NewManager(base, Faults{}).Reserve(reservation(id(3), id(103), id(203), digestFor(3)), pool, nil)
		Expect(err).To(MatchError(&Error{Reason: ReasonInvalidLedger}))
	})
})
