package query

import (
	"context"
	"errors"
	app "haeryong.local/haeryong/internal/application/storage"
	"haeryong.local/haeryong/internal/domain"
)

type RevisitValue struct {
	Flag bool `json:"flag"`
}
type RevisitView struct {
	PersonalView
	DocumentID  string `json:"document_id"`
	OperationID string `json:"operation_id,omitempty"`
}

func revisitView(n domain.Revisit) RevisitView {
	v := RevisitView{DocumentID: n.DocumentID, PersonalView: PersonalView{Value: encodeJSON(RevisitValue{n.Flag}), RequiredPrecondition: "if_none_match_star"}}
	if n.Revision != 0 {
		v.Exists = true
		v.Revision = &n.Revision
		tag := personalETag("revisit", n.OwnerID+"/"+n.DocumentID, n.Revision, v.Value)
		v.ETag = &tag
		v.RequiredPrecondition = ""
	}
	return v
}
func currentRevisit(tx app.WriteTx, owner, doc string) (domain.Revisit, error) {
	n, err := tx.Revisit(owner, doc)
	if errors.Is(err, domain.ErrNotFound) {
		return domain.Revisit{OwnerID: owner, DocumentID: doc}, nil
	}
	return n, err
}
func (s *Service) RevisitGet(ctx context.Context, token, doc string) (RevisitView, error) {
	var out RevisitView
	err := s.read(ctx, token, "/api/v1/documents/"+doc+"/revisit", func(tx app.WriteTx) error {
		a, err := s.noteOwner(ctx, tx, token)
		if err != nil {
			return err
		}
		if _, err = visibleDocument(tx, doc); err != nil {
			return err
		}
		n, err := currentRevisit(tx, a.PrincipalID, doc)
		out = revisitView(n)
		return err
	})
	return out, err
}
func (s *Service) RevisitPut(ctx context.Context, token, key, doc, etag string, create bool, body []byte) (domain.Response, error) {
	f, err := strictObject(body)
	if err != nil || len(f) != 1 {
		return domain.Response{}, &Error{"invalid_body"}
	}
	raw, ok := f["flag"]
	if !ok {
		return domain.Response{}, &Error{"invalid_body"}
	}
	flag, err := strictBool(raw)
	if err != nil {
		return domain.Response{}, err
	}
	a, err := s.Auth.Authenticate(ctx, token)
	if err != nil {
		return domain.Response{}, err
	}
	if a.Scope.Kind != "owner" {
		return domain.Response{}, domain.ErrNotFound
	}
	if err = validOperationKey(key); err != nil {
		return domain.Response{}, err
	}
	req := domain.Request{Key: domain.OperationKey{PrincipalID: a.PrincipalID, OperationID: key}, Method: "PUT", Target: "/api/v1/documents/" + doc + "/revisit", Body: encodeJSON(RevisitValue{flag}), Preconditions: map[string]string{}}
	if create {
		req.Preconditions["if-none-match"] = "*"
	} else {
		req.Preconditions["if-match"] = etag
	}
	return app.Execute(ctx, s.Store, s.Auth.Authority(token), req, nil, func(tx app.WriteTx, _ domain.Receipt) (domain.Response, error) {
		a, err := s.noteOwner(ctx, tx, token)
		if err != nil {
			return domain.Response{}, err
		}
		if _, err = personalWriteDocument(tx, doc); err != nil {
			return domain.Response{}, err
		}
		n, err := currentRevisit(tx, a.PrincipalID, doc)
		if err != nil {
			return domain.Response{}, err
		}
		v := revisitView(n)
		if create && v.Exists || !create && (!v.Exists || *v.ETag != etag) {
			return domain.Response{}, &PersonalConflict{Revision: v.Revision, ETag: v.ETag}
		}
		if !v.Exists || n.Flag != flag {
			n.Flag = flag
			n.Revision++
			if err = tx.SaveRevisit(n, a.ID, key); err != nil {
				return domain.Response{}, err
			}
		}
		v = revisitView(n)
		v.OperationID = key
		return domain.Response{Status: 200, Body: encodeJSON(v), Headers: map[string]string{"ETag": *v.ETag}}, nil
	})
}
