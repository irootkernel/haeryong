package sqlite

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"haeryong.local/haeryong/internal/application/query"
	app "haeryong.local/haeryong/internal/application/storage"
	"haeryong.local/haeryong/internal/domain"
)

func orderedTuples(hits []query.SearchHit) []string {
	out := []string{}
	for _, h := range hits {
		out = append(out, fmt.Sprintf("%s/%s/%s/%s", h.VersionID, h.Kind, *h.PagePath, *h.RegionID))
	}
	return out
}
func decodeOrderCursor(c *string) map[string]any {
	Expect(c).NotTo(BeNil())
	raw, e := base64.RawURLEncoding.DecodeString(*c)
	Expect(e).NotTo(HaveOccurred())
	var out map[string]any
	Expect(json.Unmarshal(raw, &out)).To(Succeed())
	return out
}

var _ = Describe("A1 scoped page diversification", Label("persistence"), func() {
	It("A1 traverses alternating regions at several page sizes without duplication or omission (reversal A1-ordinal-key)", func() {
		s, a, owner, p := searchFixture()
		v, _, b := seedSearchTree(s, p, "", "", map[string]string{"a.html": "<div id='a1'>needle</div><div id='a2'>needle</div><div id='a3'>needle</div><div id='a4'>needle</div>", "b.html": "<div id='b1'>needle</div><div id='b2'>needle</div>"})
		finishSearch(s, v, b)
		q := query.New(s, a, nil)
		whole := runSearch(q, owner, "needle", "region")
		ids := []string{}
		for _, h := range whole.Items {
			ids = append(ids, *h.RegionID)
		}
		Expect(ids).To(Equal([]string{"a1", "b1", "a2", "b2", "a3", "a4"}))
		for _, size := range []int{1, 2, 3, 4, 5} {
			o := query.SearchOptions{Options: query.Options{Kind: "region", Limit: size}, Text: "needle"}
			got := []query.SearchHit{}
			seen := map[string]bool{}
			for turns := 0; turns < 10; turns++ {
				page, e := q.Search(ctx, owner, o)
				Expect(e).NotTo(HaveOccurred())
				Expect(page.TotalMatches).To(Equal("6"))
				for _, tuple := range orderedTuples(page.Items) {
					Expect(seen[tuple]).To(BeFalse())
					seen[tuple] = true
				}
				got = append(got, page.Items...)
				if page.NextCursor == nil {
					break
				}
				key := decodeOrderCursor(page.NextCursor)
				Expect(key["after"].(map[string]any)["Ordinal"]).To(BeNumerically(">", 0))
				o.Cursor = *page.NextCursor
			}
			Expect(orderedTuples(got)).To(Equal(orderedTuples(whole.Items)))
		}
	})
	It("A1 hidden same-path version cannot shift scoped tuples counts cursor or binding (reversal A1-preeligible-path-group)", func() {
		s, a, _, _ := searchFixture()
		visible := "f0000000-0000-4000-8000-000000000001"
		hidden := "00000000-0000-4000-8000-000000000001"
		Expect(s.Write(ctx, func(tx app.WriteTx) error {
			if e := tx.CreateProject(domain.Project{ID: visible, Name: "Visible"}); e != nil {
				return e
			}
			return tx.CreateProject(domain.Project{ID: hidden, Name: "Denied precedes visible"})
		})).To(Succeed())
		v, _, b := seedSearchTree(s, visible, "", "", map[string]string{"a.html": "<p id='a1'>needle</p><p id='a2'>needle</p>", "b.html": "<p id='b1'>needle</p><p id='b2'>needle</p>"})
		finishSearch(s, v, b)
		token := searchToken(s, domain.CredentialScope{Kind: "workflow", Actions: []string{"version.read"}, ProjectIDs: []string{visible}, VersionIDs: []string{v}})
		q := query.New(s, a, nil)
		o := query.SearchOptions{Options: query.Options{Kind: "region", Limit: 2}, Text: "needle", Versions: "all"}
		before, e := q.Search(ctx, token, o)
		Expect(e).NotTo(HaveOccurred())
		beforeCursor := decodeOrderCursor(before.NextCursor)
		hv, _, hb := seedSearchTree(s, hidden, "", "", map[string]string{"a.html": "<p id='h1'>needle F10Canary</p><p id='h2'>needle F10Canary</p><p id='h3'>needle F10Canary</p>"})
		finishSearch(s, hv, hb)
		after, e := q.Search(ctx, token, o)
		Expect(e).NotTo(HaveOccurred())
		Expect(orderedTuples(after.Items)).To(Equal(orderedTuples(before.Items)))
		Expect(after.TotalMatches).To(Equal(before.TotalMatches))
		Expect(decodeOrderCursor(after.NextCursor)).To(Equal(beforeCursor))
		raw, _ := json.Marshal(after)
		Expect(string(raw)).NotTo(ContainSubstring(hv))
		Expect(string(raw)).NotTo(ContainSubstring(hidden))
		Expect(string(raw)).NotTo(ContainSubstring("F10Canary"))
		o.Cursor = *before.NextCursor
		next, e := q.Search(ctx, token, o)
		Expect(e).NotTo(HaveOccurred())
		Expect(next.Items).To(HaveLen(2))
		Expect(*next.Items[0].RegionID).To(Equal("a2"))
		Expect(*next.Items[1].RegionID).To(Equal("b2"))
	})
})
