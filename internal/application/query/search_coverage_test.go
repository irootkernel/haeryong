package query

import (
	"encoding/json"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"haeryong.local/haeryong/internal/application/search"
	"haeryong.local/haeryong/internal/domain"
	"strings"
)

var _ = It("SC14 coverage derives safe distinct sorted reasons without serializing raw diagnostics", Label("unit"), func() {
	complete := domain.SearchVersionState{ID: "v1", IndexRevision: 1, IndexState: "complete", DiscoveryComplete: true, HTMLTotal: 1, HTMLComplete: 1, FilesTotal: 2, FilesDiscovered: 2, Unsupported: 1}
	out := searchCoverage([]domain.SearchVersionState{complete})
	Expect(out.State).To(Equal("complete"))
	raw, e := json.Marshal(out)
	Expect(e).NotTo(HaveOccurred())
	Expect(string(raw)).To(ContainSubstring(`"version_count":"1"`))
	Expect(out.Reasons).To(Equal([]IndexReason{{Code: "unsupported_body", VersionCount: "1"}}))
	complete.Unsupported = 0
	Expect(searchCoverage([]domain.SearchVersionState{complete}).Reasons).To(Equal([]IndexReason{}))
	raw, e = json.Marshal(searchCoverage([]domain.SearchVersionState{complete}))
	Expect(e).NotTo(HaveOccurred())
	Expect(string(raw)).To(ContainSubstring(`"reasons":[]`))
	empty := searchCoverage(nil)
	Expect(empty.State).To(Equal("empty"))
	Expect(empty.Reasons).To(Equal([]IndexReason{}))
	Expect(empty.VersionsTotal).To(Equal("0"))
	v := domain.SearchVersionState{IndexState: "partial", IndexRevision: 1, DiscoveryComplete: true, HTMLTotal: 3, HTMLComplete: 1, HTMLFailed: 2, UnitReasons: []string{"read_failed", "read_failed"}}
	out = searchCoverage([]domain.SearchVersionState{v})
	Expect(out.Reasons).To(Equal([]IndexReason{{Code: "read_failed", VersionCount: "1"}}))
	Expect(out.HTMLPending).To(Equal("0"))
	Expect(out.HTMLFailed).To(Equal("2"))
	v.UnitReasons = []string{"/private/raw diagnostic"}
	v.Reason = ""
	out = searchCoverage([]domain.SearchVersionState{v})
	Expect(out.Reasons).To(Equal([]IndexReason{{Code: "failed_unknown", VersionCount: "1"}}))
	for _, code := range []string{"interrupted", "cancelled", "resource_exhausted", "decode_failed", "integrity_failed", "storage_failed"} {
		v.UnitReasons = nil
		v.Reason = code
		Expect(searchCoverage([]domain.SearchVersionState{v}).Reasons).To(Equal([]IndexReason{{Code: code, VersionCount: "1"}}))
	}
	Expect(searchCoverage([]domain.SearchVersionState{{IndexState: "pending"}}).Reasons).To(Equal([]IndexReason{{Code: "not_indexed", VersionCount: "1"}}))
})

var _ = It("SC08 lexical matching handles large repeated substrings without prefix copies", Label("unit"), func() {
	Expect(wordContains(strings.Repeat("a", 32768), "a")).To(BeFalse())
	Expect(wordContains("read source retry.go", "source")).To(BeTrue())
	Expect(wordContains("resourceful", "source")).To(BeFalse())
	Expect(wordContains("재시도", "재")).To(BeFalse())
	Expect(wordContains("검토 승인", "검토")).To(BeTrue())
})

var _ = It("SC01 metadata field origins retain long labels without counting title fragments as topics", Label("unit"), func() {
	supplied := strings.Repeat("s", 40000)
	title := strings.Repeat("t", 40000)
	raw := []rune("page.html " + supplied + " " + title + " keyword")
	for ordinal, start := 0, 0; start < len(raw); ordinal, start = ordinal+1, start+search.ChunkSize-search.ChunkOverlap {
		end := start + search.ChunkSize
		if end > len(raw) {
			end = len(raw)
		}
		c := domain.SearchCandidate{Unit: domain.SearchUnit{Kind: "page", PagePath: "page.html", Title: title}, Chunk: domain.SearchChunk{Part: "metadata", Ordinal: ordinal, DisplayBody: string(raw[start:end])}}
		fields := candidateFields(c, supplied)
		for _, field := range fields {
			if field.name == "topic" {
				Expect(strings.Trim(field.literal, "keyword ")).To(BeEmpty())
				Expect(field.aliases).NotTo(ContainSubstring("tttt"))
				Expect(field.aliases).NotTo(ContainSubstring("ssss"))
			}
		}
		if end == len(raw) {
			break
		}
	}
})

var _ = It("SC08 authored snippets map expanded normalization offsets without rewriting display text", Label("unit"), func() {
	text := strings.Repeat("ß", 15000) + " Needle " + strings.Repeat("Z", 10000)
	preview := snippet(text, "needle")
	Expect(preview).To(ContainSubstring("Needle"))
	Expect(preview).To(ContainSubstring("ß"))
	Expect(text).To(ContainSubstring(preview))
	Expect(len([]rune(preview))).To(BeNumerically("<=", 240))
})
