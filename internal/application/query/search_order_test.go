package query

import (
	"context"
	"encoding/base64"
	"encoding/json"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"haeryong.local/haeryong/internal/application/search"
	"haeryong.local/haeryong/internal/domain"
)

func orderHit(version, page, region string, tier, score int) *rankedSearch {
	return &rankedSearch{hit: SearchHit{ProjectID: "visible", DocumentID: "doc", VersionID: version, Sequence: 1, Kind: "region", PagePath: &page, RegionID: &region}, tier: tier, score: score}
}
func orderIDs(hits []*rankedSearch) []string {
	out := []string{}
	for _, h := range hits {
		out = append(out, h.hit.VersionID+"/"+value(h.hit.PagePath)+"/"+value(h.hit.RegionID))
	}
	return out
}

var _ = Describe("A1 search ordering", Label("unit"), func() {
	It("A1 alternates tied pages before existing tuple keys (reversal A1-ordinal-key)", func() {
		hits := []*rankedSearch{orderHit("v", "a", "4", 4, 1), orderHit("v", "b", "2", 4, 1), orderHit("v", "a", "2", 4, 1), orderHit("v", "a", "1", 4, 1), orderHit("v", "b", "1", 4, 1), orderHit("v", "a", "3", 4, 1)}
		Expect(orderSearch(context.Background(), hits)).To(Succeed())
		Expect(orderIDs(hits)).To(Equal([]string{"v/a/1", "v/b/1", "v/a/2", "v/b/2", "v/a/3", "v/a/4"}))
	})
	It("A1 strength precedes ordinal and counters reset for every tier-score block (reversals A1-strength-first and A1-block-reset)", func() {
		hits := []*rankedSearch{}
		for _, row := range []struct{ tier, score int }{{2, 6}, {4, 6}, {4, 1}} {
			for _, id := range []string{"1", "2", "3"} {
				hits = append(hits, orderHit("v", "a", id, row.tier, row.score))
			}
		}
		hits = append(hits, orderHit("v", "z", "1", 5, 99))
		Expect(orderSearch(context.Background(), hits)).To(Succeed())
		for i, h := range hits[:9] {
			Expect(h.tier).To(Equal([]int{2, 4, 4}[i/3]))
			Expect(h.score).To(Equal([]int{6, 6, 1}[i/3]))
			Expect(h.ordinal).To(Equal(i%3 + 1))
		}
		Expect(hits[9].tier).To(Equal(5))
		Expect(hits[9].ordinal).To(Equal(1))
	})
	It("A1 exact version separates identical page paths (reversal A1-version-group)", func() {
		hits := []*rankedSearch{orderHit("new", "same", "a", 4, 1), orderHit("new", "same", "b", 4, 1), orderHit("new", "same", "c", 4, 1), orderHit("old", "same", "a", 4, 1), orderHit("old", "same", "b", 4, 1)}
		for _, h := range hits {
			if h.hit.VersionID == "new" {
				h.hit.Sequence = 2
			}
		}
		Expect(orderSearch(context.Background(), hits)).To(Succeed())
		Expect(orderIDs(hits)).To(Equal([]string{"new/same/a", "old/same/a", "new/same/b", "old/same/b", "new/same/c"}))
	})
	It("A1 rejects pre-A1 binding and missing-zero-negative ordinal (reversals A1-order-revision and A1-cursor-ordinal)", func() {
		q, e := search.ParseQuery("needle")
		Expect(e).NotTo(HaveOccurred())
		l := domain.Library{ID: "lib"}
		o := SearchOptions{Text: "needle"}
		coverage := searchCoverage(nil)
		binding := visibleSearchState(l, "reader", q, o, nil, coverage)
		old := validator([]any{l.ID, l.RuntimeGeneration, l.AuthEpoch, l.StreamEpoch, "reader", q.Clauses, o.Project, o.Lifecycle, o.Versions, o.Kind, o.Limit, domain.SearchExtractorRevision, 1, []domain.SearchVersionState{}, coverage})
		key := searchKey{Tier: 4, Score: 1, Ordinal: 1, Sequence: 1}
		_, e = decodeSearchCursor(*encodeSearchCursor(old, key), binding)
		Expect(e).To(MatchError(ContainSubstring("cursor_mismatch")))
		legacy, _ := json.Marshal(map[string]any{"binding": old, "after": map[string]any{"Tier": 4, "Score": 1, "Sequence": 1}})
		_, e = decodeSearchCursor(base64.RawURLEncoding.EncodeToString(legacy), binding)
		Expect(e).To(MatchError(ContainSubstring("cursor_mismatch")))
		for _, ordinal := range []any{nil, 0, -1} {
			after := map[string]any{"Tier": 4, "Score": 1, "Sequence": 1}
			if ordinal != nil {
				after["Ordinal"] = ordinal
			}
			raw, _ := json.Marshal(map[string]any{"binding": binding, "after": after})
			_, e = decodeSearchCursor(base64.RawURLEncoding.EncodeToString(raw), binding)
			Expect(e).To(MatchError(ContainSubstring("cursor_mismatch")))
		}
		_, e = decodeSearchCursor(*encodeSearchCursor(binding, key), binding)
		Expect(e).NotTo(HaveOccurred())
	})
})
