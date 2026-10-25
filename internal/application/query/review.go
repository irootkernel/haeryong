package query

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"haeryong.local/haeryong/internal/application/service"
	app "haeryong.local/haeryong/internal/application/storage"
	"haeryong.local/haeryong/internal/domain"
	"io"
	"time"
)

type ReviewValue struct {
	State      string  `json:"state"`
	ReviewedAt *string `json:"reviewed_at"`
}
type ReviewView struct {
	OperationID          string      `json:"operation_id,omitempty"`
	VersionID            string      `json:"version_id"`
	Exists               bool        `json:"exists"`
	Value                ReviewValue `json:"value"`
	Revision             *int64      `json:"revision"`
	ETag                 *string     `json:"etag"`
	PagePositionURL      *string     `json:"page_position_url"`
	RequiredPrecondition string      `json:"required_precondition,omitempty"`
}
type ReviewRequest struct {
	Action string `json:"action"`
	State  string `json:"state"`
}
type ReviewError struct {
	Code    string
	Current *ReviewView
}

func (e *ReviewError) Error() string { return e.Code }

// ReviewNextState implements the explicit owner action table, never a reading heuristic.
func ReviewNextState(current string, in ReviewRequest) (string, error) {
	expected := map[string]string{"start": "reviewing", "complete": "reviewed", "reopen": "reviewing", "clear": "unreviewed"}
	state, ok := expected[in.Action]
	if !ok || (in.State != "unreviewed" && in.State != "reviewing" && in.State != "reviewed") {
		return "", &ReviewError{Code: "INVALID_ENUM"}
	}
	if state != in.State {
		return "", &ReviewError{Code: "INVALID_REQUEST"}
	}
	if in.Action == "start" && current == "reviewed" || in.Action == "reopen" && current != "reviewed" {
		return "", &ReviewError{Code: "INVALID_TRANSITION"}
	}
	return state, nil
}
func reviewView(r domain.Review, version string) ReviewView {
	v := ReviewView{VersionID: version, Value: ReviewValue{State: "unreviewed"}, RequiredPrecondition: "if_none_match_star"}
	if r.Revision == 0 {
		return v
	}
	v.Exists = true
	v.RequiredPrecondition = ""
	v.Value = ReviewValue{r.State, r.ReviewedAt}
	v.Revision = &r.Revision
	etag := personalETag("review", personalID(r.OwnerID, version, ""), r.Revision, encodeJSON(v.Value))
	v.ETag = &etag
	return v
}
func reviewCurrent(tx app.WriteTx, owner, version string) (domain.Review, error) {
	r, err := tx.Review(owner, version)
	if errors.Is(err, domain.ErrNotFound) {
		return domain.Review{OwnerID: owner, VersionID: version, State: "unreviewed"}, nil
	}
	return r, err
}
func (s *Service) ReviewGet(ctx context.Context, token, version string) (ReviewView, error) {
	var out ReviewView
	err := s.read(ctx, token, "/api/v1/versions/"+version+"/review", func(tx app.WriteTx) error {
		actor, err := s.Auth.AuthenticateWrite(ctx, tx, token)
		if err != nil {
			return err
		}
		if actor.Scope.Kind != "owner" {
			return domain.ErrDenied
		}
		if _, err = visibleVersion(tx, version); err != nil {
			return err
		}
		r, err := reviewCurrent(tx, actor.PrincipalID, version)
		if err != nil {
			return err
		}
		out = reviewView(r, version)
		return nil
	})
	return out, err
}
func (s *Service) ReviewPut(ctx context.Context, token, key, version, etag string, create bool, body []byte) (domain.Response, error) {
	var wire struct {
		Action *string `json:"action"`
		State  *string `json:"state"`
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&wire); err != nil {
		return domain.Response{}, &ReviewError{Code: "INVALID_REQUEST"}
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return domain.Response{}, &ReviewError{Code: "INVALID_REQUEST"}
	}
	if wire.Action == nil || wire.State == nil {
		return domain.Response{}, &ReviewError{Code: "INVALID_REQUEST"}
	}
	in := ReviewRequest{Action: *wire.Action, State: *wire.State}
	if _, err := ReviewNextState("unreviewed", ReviewRequest{Action: in.Action, State: in.State}); err != nil {
		var e *ReviewError
		if errors.As(err, &e) && e.Code != "INVALID_TRANSITION" {
			return domain.Response{}, err
		}
	}
	actor, err := s.Auth.Authenticate(ctx, token)
	if err != nil {
		return domain.Response{}, err
	}
	if actor.Scope.Kind != "owner" {
		return domain.Response{}, domain.ErrDenied
	}
	if err := validOperationKey(key); err != nil {
		return domain.Response{}, err
	}
	request := domain.Request{Key: domain.OperationKey{PrincipalID: actor.PrincipalID, OperationID: key}, Method: "PUT", Target: "/api/v1/versions/" + version + "/review", Body: encodeJSON(in), Preconditions: map[string]string{}}
	if create {
		request.Preconditions["if-none-match"] = "*"
	} else {
		request.Preconditions["if-match"] = etag
	}
	return app.Execute(ctx, s.Store, s.Auth.Authority(token), request, nil, func(tx app.WriteTx, _ domain.Receipt) (domain.Response, error) {
		writer, err := s.Auth.AuthenticateWrite(ctx, tx, token)
		if err != nil {
			return domain.Response{}, err
		}
		v, err := tx.PublishedVersion(version)
		if err != nil {
			return domain.Response{}, err
		}
		d, err := tx.Document(v.DocumentID)
		if err != nil {
			return domain.Response{}, err
		}
		if d.Lifecycle == "trash" || d.Lifecycle == "purged" {
			return domain.Response{}, &Transition{Lifecycle: d.Lifecycle}
		}
		r, err := reviewCurrent(tx, writer.PrincipalID, version)
		if err != nil {
			return domain.Response{}, err
		}
		current := reviewView(r, version)
		if create && current.Exists || !create && (!current.Exists || etag == "" || *current.ETag != etag) {
			return domain.Response{}, &ReviewError{Code: "PRECONDITION_FAILED", Current: &current}
		}
		next, err := ReviewNextState(r.State, in)
		if err != nil {
			return domain.Response{}, err
		}
		if next != r.State {
			id, err := service.NewID()
			if err != nil {
				return domain.Response{}, err
			}
			stamp := time.Now().UTC().Format(time.RFC3339Nano)
			event := domain.ReviewEvent{ID: id, VersionID: version, Action: in.Action, OldState: r.State, NewState: next, ActorSession: writer.ID, OperationID: key, CreatedAt: stamp}
			r.State = next
			r.Revision++
			r.ReviewedAt = nil
			if next == "reviewed" {
				r.ReviewedAt = &stamp
			}
			if err := tx.SaveReview(r, event); err != nil {
				return domain.Response{}, err
			}
		}
		view := reviewView(r, version)
		view.OperationID = key
		headers := map[string]string{}
		if view.ETag != nil {
			headers["ETag"] = *view.ETag
		}
		return domain.Response{Status: 200, Body: encodeJSON(view), Headers: headers}, nil
	})
}
func (s *Service) ReviewHistory(ctx context.Context, token, version string, o Options) (List[domain.ReviewEvent], error) {
	out := List[domain.ReviewEvent]{Items: []domain.ReviewEvent{}}
	err := s.read(ctx, token, "/api/v1/versions/"+version+"/review/history", func(tx app.WriteTx) error {
		actor, err := s.Auth.AuthenticateWrite(ctx, tx, token)
		if err != nil {
			return err
		}
		if actor.Scope.Kind != "owner" {
			return domain.ErrDenied
		}
		if _, err := visibleVersion(tx, version); err != nil {
			return err
		}
		c, n, _, err := s.cursorStart(ctx, token, tx, "review-history/"+version, o)
		if err != nil {
			return err
		}
		rows, err := tx.ReviewHistory(actor.PrincipalID, version, c.Sequence, n+1)
		if err != nil {
			return err
		}
		for i, e := range rows {
			if i == n {
				c.Sequence = rows[i-1].Sequence
				out.NextCursor = cursorNext(c)
				break
			}
			out.Items = append(out.Items, e)
		}
		return nil
	})
	return out, err
}

func (s *Service) reviewedDocument(ctx context.Context, tx app.WriteTx, token string, d domain.QueryDocument) (Document, error) {
	out := document(d)
	actor, err := s.Auth.AuthenticateWrite(ctx, tx, token)
	if err != nil {
		return out, err
	}
	if actor.Scope.Kind == "owner" {
		r, e := currentRevisit(tx, actor.PrincipalID, d.ID)
		if e != nil {
			return out, e
		}
		out.Revisit = &r.Flag
		out.ReviewSummary, err = tx.ReviewSummary(actor.PrincipalID, d.ID)
		if err != nil {
			return out, err
		}
	}
	out.ETag = ""
	out.ETag = validator(out)
	return out, nil
}
