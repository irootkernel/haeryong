package auth

import (
	"encoding/json"
	"errors"
	app "haeryong.local/haeryong/internal/application/storage"
	"haeryong.local/haeryong/internal/domain"
	"net/url"
	"strings"
)

// LifecycleRoute is deliberately narrower than ordinary document visibility.
func LifecycleRoute(target string) bool {
	u, e := url.ParseRequestURI(target)
	if e != nil {
		return false
	}
	p := strings.Split(strings.Trim(u.Path, "/"), "/")
	return len(p) == 3 && p[0] == "api" && p[1] == "v1" && p[2] == "trash" || len(p) >= 4 && p[0] == "api" && p[1] == "v1" && (p[2] == "lifecycle-plans" && (len(p) == 4 || len(p) == 5 && p[4] == "commit") || len(p) == 5 && p[2] == "documents" && p[4] == "lifecycle-plans")
}
func lifecycleTarget(tx app.WriteTx, r domain.Request) error {
	p := strings.Split(strings.Trim(strings.SplitN(r.Target, "?", 2)[0], "/"), "/")
	if p[2] == "trash" {
		return nil
	}
	document := p[3]
	if p[2] == "lifecycle-plans" {
		plan, err := tx.LifecyclePlan(p[3])
		if err != nil {
			return err
		}
		document = plan.DocumentID
	}
	d, err := tx.Document(document)
	if err != nil {
		return err
	}
	_, err = tx.Scope(d.ProjectID)
	return err
}

// ProjectLifecycleItems is shared by live GET, receipt replay and operation GET.
// Target records have owner lifecycle-administration visibility; incoming sources
// keep ordinary exact-version visibility even for the owner.
func ProjectLifecycleItems(tx app.WriteTx, items []domain.LifecycleItem) ([]domain.LifecycleItem, map[string]int, error) {
	out := []domain.LifecycleItem{}
	counts := map[string]int{"version": 0, "incoming": 0, "incoming_edges": 0, "referring_documents": 0}
	sources := map[string]bool{}
	for _, i := range items {
		if i.Kind == "incoming" {
			if i.VersionID == nil {
				return nil, nil, domain.ErrInvalidTarget
			}
			if err := visibleTarget(tx, "/api/v1/versions/"+*i.VersionID); err != nil {
				if errors.Is(err, domain.ErrNotFound) || errors.Is(err, domain.ErrDenied) {
					continue
				}
				return nil, nil, err
			}
			if i.SourceDocumentID != nil {
				sources[*i.SourceDocumentID] = true
			}
			if i.Resolution != nil && *i.Resolution == "pinned" {
				counts["incoming_edges"]++
			}
		}
		counts[i.Kind]++
		out = append(out, i)
	}
	counts["referring_documents"] = len(sources)
	return out, counts, nil
}
func filterLifecycleResponse(tx app.WriteTx, r domain.Request, response domain.Response) (domain.Response, error) {
	if err := lifecycleTarget(tx, r); err != nil {
		return domain.Response{}, err
	}
	var page domain.LifecyclePage
	if err := json.Unmarshal(response.Body, &page); err != nil {
		return domain.Response{}, err
	}
	// A commit result contains no nested impact and is safe owner metadata only.
	if page.Items == nil {
		return response, nil
	}
	all, err := tx.LifecycleItems(page.ID)
	if err != nil {
		return domain.Response{}, err
	}
	_, counts, err := ProjectLifecycleItems(tx, all)
	if err != nil {
		return domain.Response{}, err
	}
	visible, _, err := ProjectLifecycleItems(tx, page.Items)
	if err != nil {
		return domain.Response{}, err
	}
	page.Items = visible
	page.Impact = counts
	response.Body, err = json.Marshal(page)
	return response, err
}
