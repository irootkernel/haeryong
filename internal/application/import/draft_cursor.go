package importer

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"

	app "haeryong.local/haeryong/internal/application/storage"
	"haeryong.local/haeryong/internal/domain"
	sec "haeryong.local/haeryong/internal/security/session"
)

type draftCursor struct {
	Binding  string `json:"binding"`
	After    string `json:"after,omitempty"`
	Revision int64  `json:"revision,omitempty"`
}

func (s *Service) draftCursor(ctx context.Context, tx app.WriteTx, token, resource, project, input string) (draftCursor, domain.Session, error) {
	actor, e := s.Auth.AuthenticateWrite(ctx, tx, token)
	if e != nil {
		return draftCursor{}, actor, e
	}
	l, e := tx.Library()
	if e != nil {
		return draftCursor{}, actor, e
	}
	revision := int64(0)
	permissions := map[string]int64{}
	projects := actor.Scope.ProjectIDs
	if actor.Scope.Kind != "owner" {
		principal, e := tx.WorkflowPrincipal(actor.PrincipalID)
		if e != nil {
			return draftCursor{}, actor, e
		}
		revision = principal.Revision
	}
	if project != "" {
		projects = append(append([]string{}, projects...), project)
	}
	for _, id := range projects {
		scope, e := tx.Scope(id)
		if e != nil {
			return draftCursor{}, actor, e
		}
		permissions[id] = scope.Project.PermissionsRevision
	}
	allPermissions := ""
	if actor.Scope.Kind == "owner" && project == "" {
		h := sha256.New()
		afterUpdated, afterID := "", ""
		for {
			rows, e := tx.QueryProjects(actor.Scope, afterUpdated, afterID, 64)
			if e != nil {
				return draftCursor{}, actor, e
			}
			if len(rows) == 0 {
				break
			}
			for _, p := range rows {
				_, _ = h.Write(encoded([]any{p.ID, p.PermissionsRevision}))
				afterUpdated, afterID = p.UpdatedAt, p.ID
			}
		}
		allPermissions = fmt.Sprintf("%x", h.Sum(nil))
	}
	binding := fmt.Sprintf("%x", sha256.Sum256(encoded([]any{l.ID, l.RuntimeGeneration, resource, project, actor.PrincipalID, actor.Scope, revision, permissions, allPermissions})))
	c := draftCursor{Binding: binding}
	if input != "" {
		raw, e := base64.RawURLEncoding.DecodeString(input)
		if e != nil {
			return c, actor, sec.ErrInvalid
		}
		d := json.NewDecoder(bytes.NewReader(raw))
		d.DisallowUnknownFields()
		var extra any
		if d.Decode(&c) != nil || d.Decode(&extra) != io.EOF || c.Binding != binding || c.Revision < 0 || c.After != "" && !idPattern.MatchString(c.After) {
			return c, actor, sec.ErrInvalid
		}
		canonical, _ := json.Marshal(c)
		if base64.RawURLEncoding.EncodeToString(canonical) != input {
			return c, actor, sec.ErrInvalid
		}
	}
	return c, actor, nil
}
func nextDraftCursor(c draftCursor) *string {
	raw, _ := json.Marshal(c)
	v := base64.RawURLEncoding.EncodeToString(raw)
	return &v
}
func (s *Service) DraftPage(ctx context.Context, token, project, cursor string) (out Page[DraftView], err error) {
	out.Items = []DraftView{}
	if project != "" && !idPattern.MatchString(project) {
		return out, sec.ErrInvalid
	}
	err = s.Store.Write(ctx, func(tx app.WriteTx) error {
		c, actor, e := s.draftCursor(ctx, tx, token, "drafts", project, cursor)
		if e != nil {
			return e
		}
		if c.Revision != 0 {
			return sec.ErrInvalid
		}
		if e = s.Auth.Authority(token).AuthorizeWrite(ctx, tx, domain.Request{Key: domain.OperationKey{PrincipalID: actor.PrincipalID}, Method: "GET", Target: "/api/v1/drafts"}); e != nil {
			return e
		}
		rows, e := tx.Drafts(project, c.After, 64, actor.Scope)
		if e != nil {
			return e
		}
		for _, d := range rows {
			out.Items = append(out.Items, draftView(d))
		}
		if len(rows) == 64 {
			c.After = rows[len(rows)-1].ID
			out.NextCursor = nextDraftCursor(c)
		}
		return nil
	})
	return
}
func (s *Service) DraftHistoryPage(ctx context.Context, token, id, cursor string) (out Page[DraftView], err error) {
	out.Items = []DraftView{}
	if !idPattern.MatchString(id) {
		return out, sec.ErrInvalid
	}
	err = s.Store.Write(ctx, func(tx app.WriteTx) error {
		d, e := s.draftCurrent(ctx, tx, token, id, "GET", "")
		if e != nil {
			return e
		}
		c, _, e := s.draftCursor(ctx, tx, token, "draft-history:"+id, d.ProjectID, cursor)
		if e != nil {
			return e
		}
		if c.After != "" {
			return sec.ErrInvalid
		}
		rows, e := tx.DraftHistory(id, c.Revision, 64)
		if e != nil {
			return e
		}
		for _, d := range rows {
			out.Items = append(out.Items, draftView(d))
		}
		if len(rows) == 64 {
			c.Revision = rows[len(rows)-1].Revision
			out.NextCursor = nextDraftCursor(c)
		}
		return nil
	})
	return
}
