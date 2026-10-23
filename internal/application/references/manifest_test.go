package references

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"io"
	"runtime"
	"strings"
)

var _ = It("RF06 resumes manifest declaration slices at exact byte offsets without consuming generated metadata", Label("unit"), func() {
	var refs []any
	for i := 0; i < 70; i++ {
		refs = append(refs, map[string]any{"id": strings.Repeat("r", i+1), "document_id": "11111111-1111-4111-8111-111111111111", "version_id": "22222222-2222-4222-8222-222222222222", "page_path": nil, "region_id": nil})
	}
	raw, _ := json.Marshal(map[string]any{"generated_only": map[string]any{"viewer_profile": "html_js_v1"}, "references": refs, "tail": true})
	offset := manifestPosition{}
	seen := []string{}
	for turns := 0; turns < 100; turns++ {
		items, next, done, err := manifestBatch(context.Background(), strings.NewReader(string(raw)), "33333333-3333-4333-8333-333333333333", offset)
		Expect(err).NotTo(HaveOccurred())
		Expect(len(items)).To(BeNumerically("<=", 64))
		Expect(next.Offset).To(BeNumerically(">", offset.Offset))
		offset = next
		for _, item := range items {
			seen = append(seen, item.prepared.Columns().ID)
		}
		if done {
			break
		}
	}
	Expect(seen).To(HaveLen(70))
	for i, id := range seen {
		Expect(id).To(Equal(strings.Repeat("r", i+1)))
	}
	items, _, done, err := manifestBatch(context.Background(), strings.NewReader(`{"viewer_profile":"html_js_v1"}`), "", manifestPosition{})
	Expect(err).NotTo(HaveOccurred())
	Expect(done).To(BeTrue())
	Expect(items).To(BeEmpty())
})

// Track backward seeks across turns, allowing ordinary buffered read-ahead.
type monotonicManifest struct {
	*bytes.Reader
	floor   int64
	rescans int
}

func (r *monotonicManifest) Seek(n int64, whence int) (int64, error) {
	if whence == io.SeekStart && n < r.floor {
		r.rescans++
	}
	return r.Reader.Seek(n, whence)
}

var _ = It("RF06-PRESCAN streams large question and pages with bounded allocation and member checkpoints (RV-TOKENS/RV-DEADLINE)", Label("unit"), func() {
	var declarations []string
	for i := 0; i < 70; i++ {
		declarations = append(declarations, fmt.Sprintf(`{"id":"after-large-%d","document_id":"11111111-1111-4111-8111-111111111111","version_id":"22222222-2222-4222-8222-222222222222","page_path":null,"region_id":null}`, i))
	}
	for _, size := range []int{20 << 20, 40 << 20} {
		// Construction is outside the measured scan: only scanner allocation is relevant.
		raw := []byte(`{"document":{"question":"` + strings.Repeat("q", size) + `"},"pages":[` + strings.Repeat(`{"page_path":"p.html"},`, 100000) + `{}],"references":[` + strings.Join(declarations, ",") + `]}`)
		r := &monotonicManifest{Reader: bytes.NewReader(raw)}
		pos := manifestPosition{}
		var before, after runtime.MemStats
		runtime.GC()
		runtime.ReadMemStats(&before)
		prescanTurns, count := 0, 0
		var scanErr error
		finished := false
		for turns := 0; turns < 20; turns++ {
			r.floor = pos.Offset
			var items []manifestItem
			items, pos, finished, scanErr = manifestBatch(context.Background(), r, "33333333-3333-4333-8333-333333333333", pos)
			if pos.Member {
				prescanTurns++
			}
			count += len(items)
			if scanErr != nil || finished {
				break
			}
		}
		runtime.ReadMemStats(&after)
		allocation := after.TotalAlloc - before.TotalAlloc
		fmt.Fprintf(GinkgoWriter, "question=%d allocated=%d prescan-turns=%d rescans=%d\n", size, allocation, prescanTurns, r.rescans)
		Expect(allocation).To(BeNumerically("<", 1<<20), "fixed scanner allocation budget, independent of question size")
		Expect(scanErr).NotTo(HaveOccurred())
		Expect(finished).To(BeTrue())
		Expect(count).To(Equal(70))
		Expect(prescanTurns).To(BeNumerically(">=", 2), "question and pages checkpoint in separate prescan turns")
		Expect(r.rescans).To(BeZero(), "resume never rescans preceding members")
	}
	// Both legacy offset-zero checkpoints and the added member bit survive JSON recovery.
	var recovered checkpoint
	Expect(json.Unmarshal([]byte(`{"phase":"declarations","offset":0}`), &recovered)).To(Succeed())
	Expect(recovered.manifestPosition).To(Equal(manifestPosition{}))
	raw, _ := json.Marshal(checkpoint{manifestPosition: manifestPosition{Offset: 42, Member: true}})
	Expect(json.Unmarshal(raw, &recovered)).To(Succeed())
	Expect(recovered.manifestPosition).To(Equal(manifestPosition{Offset: 42, Member: true}))
})
