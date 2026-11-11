package auth

import (
	"encoding/json"
	app "haeryong.local/haeryong/internal/application/storage"
	"haeryong.local/haeryong/internal/domain"
	"net/url"
	"strings"
)

func visibleDecisionRequest(tx app.WriteTx, r domain.Request) error {
	u, e := url.ParseRequestURI(r.Target)
	if e != nil {
		return e
	}
	p := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(p) < 3 {
		return nil
	}
	var f domain.Finding
	if p[2] == "findings" && len(p) > 3 {
		f, e = tx.Finding(p[3])
	} else if (p[2] == "overlays" || p[2] == "topic-assignments") && len(p) > 3 {
		var m domain.ManagementEffect
		m, e = tx.ManagementEffect(p[3])
		if e == nil && m.Availability == "unavailable" {
			return domain.ErrNotFound
		}
		if e == nil {
			f, e = tx.Finding(m.FindingID)
		}
	} else if p[2] == "scans" {
		var in struct {
			FindingID string `json:"finding_id"`
		}
		if json.Unmarshal(r.Body, &in) != nil || in.FindingID == "" {
			return nil
		}
		f, e = tx.Finding(in.FindingID)
	} else {
		return nil
	}
	if e != nil {
		return e
	}
	visible := visibleFindingEvent(tx, domain.Session{Scope: domain.CredentialScope{Kind: "owner"}}, domain.Event{ResourceKind: "finding", ResourceID: f.ID, Kind: "finding.updated"})
	if visible.ResourceKind != "finding" {
		return domain.ErrNotFound
	}
	return nil
}
