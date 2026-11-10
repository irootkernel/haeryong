package query

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"haeryong.local/haeryong/internal/application/source"
	app "haeryong.local/haeryong/internal/application/storage"
	"haeryong.local/haeryong/internal/domain"
	"net/url"
)

func (s *Service) Management(ctx context.Context, token string, f domain.ManagementFilter, o Options) (domain.ManagementPage, error) {
	out := domain.ManagementPage{Items: []domain.ManagementEffect{}}
	if f.Kind != "related_explanation" && f.Kind != "topic_assignment" || f.RegionID != nil && f.PagePath == nil || f.PagePath != nil && f.VersionID == "" {
		return out, domain.ErrInvalidTarget
	}
	for _, id := range []string{f.DocumentID, f.VersionID} {
		if id != "" && !searchID.MatchString(id) {
			return out, domain.ErrInvalidTarget
		}
	}
	limit := o.Limit
	if limit == 0 {
		limit = 50
	}
	if limit < 1 || limit > 200 {
		return out, domain.ErrInvalidTarget
	}
	err := s.Store.Write(ctx, func(tx app.WriteTx) error {
		actor, e := s.Auth.AuthenticateWrite(ctx, tx, token)
		if e != nil {
			return e
		}
		if f.Kind == "topic_assignment" && actor.Scope.Kind != "owner" {
			return domain.ErrDenied
		}
		if f.VersionID != "" {
			if _, e = source.Check(tx, actor, f.VersionID); e != nil {
				return e
			}
		}
		if f.PagePath != nil {
			if _, e = tx.Page(f.VersionID, *f.PagePath); e != nil {
				return e
			}
		}
		if f.RegionID != nil {
			if _, e = tx.VersionRegion(f.VersionID, *f.PagePath, *f.RegionID); e != nil {
				return e
			}
		}
		lib, e := tx.Library()
		if e != nil {
			return e
		}
		binding := domain.ManagementDigest([]any{lib.ID, lib.RuntimeGeneration, actor.PrincipalID, actor.Scope, f, limit})
		after := ""
		if o.Cursor != "" {
			raw, e := base64.RawURLEncoding.DecodeString(o.Cursor)
			if e != nil {
				return domain.ErrInvalidTarget
			}
			var c referenceCursor
			if json.Unmarshal(raw, &c) != nil || c.Binding != binding || c.After == "" {
				return domain.ErrInvalidTarget
			}
			after = c.After
		}
		for {
			rows, e := tx.ManagementEffects(f, after, 64)
			if e != nil {
				return e
			}
			if len(rows) == 0 {
				break
			}
			for _, m := range rows {
				after = m.ID
				if f.Kind == "related_explanation" && m.Decision != "accepted" {
					continue
				}
				// Focal authority is required even for a minimal unavailable row.
				if _, e := source.Check(tx, actor, m.From.VersionID); e != nil {
					continue
				}
				if m.Availability == "unavailable" || !managementVisible(tx, actor, m) {
					if m.Kind != "related_explanation" {
						continue
					}
					m = m.Unavailable()
				} else {
					m.Availability = "available"
					if m.Kind == "topic_assignment" {
						m.EvidenceBasis = domain.TopicEvidenceBasis(m.EvidenceBasis)
					}
				}
				if len(out.Items) == limit {
					last := out.Items[len(out.Items)-1].ID
					b, _ := json.Marshal(referenceCursor{Binding: binding, After: last})
					c := base64.RawURLEncoding.EncodeToString(b)
					out.NextCursor = &c
					return nil
				}
				m.ETag = m.Validator()
				out.Items = append(out.Items, m)
			}
		}
		return nil
	})
	return out, err
}

func managementVisible(tx app.WriteTx, actor domain.Session, m domain.ManagementEffect) bool {
	versions := []string{m.From.VersionID}
	if m.Target != nil {
		versions = append(versions, m.Target.VersionID)
	}
	var evidence []domain.FindingEvidence
	if json.Unmarshal(m.Evidence, &evidence) != nil {
		return false
	}
	for _, ev := range evidence {
		if ev.VersionID != "" {
			versions = append(versions, ev.VersionID)
		}
	}
	for _, v := range versions {
		if _, e := source.Check(tx, actor, v); e != nil {
			return false
		}
	}
	return true
}

// Reader links use immutable exact targets, never a moving document pointer.
func ManagementReaderURL(a domain.ManagementAnchor) string {
	q := url.Values{}
	if a.PagePath != nil {
		q.Set("page", *a.PagePath)
	}
	u := "/documents/" + a.DocumentID + "/versions/" + a.VersionID
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	if a.RegionID != nil {
		u += "#region=" + url.QueryEscape(*a.RegionID)
	}
	return u
}
