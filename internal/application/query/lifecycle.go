package query

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	appauth "haeryong.local/haeryong/internal/application/auth"
	"haeryong.local/haeryong/internal/application/service"
	app "haeryong.local/haeryong/internal/application/storage"
	"haeryong.local/haeryong/internal/domain"
	sec "haeryong.local/haeryong/internal/security/session"
	"strconv"
	"time"
)

var ErrStaleLifecyclePlan = errors.New("STALE_PLAN")
var ErrLifecycleNotReady = errors.New("FEATURE_NOT_READY")

func (s *Service) lifecycleNow() time.Time {
	if s.LifecycleNow != nil {
		return s.LifecycleNow().UTC()
	}
	return time.Now().UTC()
}
func LifecycleNext(current, previous, operation string) (string, error) {
	switch operation {
	case "archive":
		if current == "active" || current == "archived" {
			return "archived", nil
		}
	case "trash":
		if current == "active" || current == "archived" || current == "trash" {
			return "trash", nil
		}
	case "restore":
		if current == "archived" {
			return "active", nil
		}
		if current == "trash" && (previous == "active" || previous == "archived") {
			return previous, nil
		}
	case "purge":
		if current == "trash" {
			return "purged", nil
		}
	default:
		return "", &Error{"invalid_operation"}
	}
	return "", &Transition{Lifecycle: current}
}
func lifecycleDigest(items []domain.LifecycleItem, purge ...bool) string {
	selected := []domain.LifecycleItem{}
	for _, i := range items {
		if len(purge) > 0 && purge[0] || i.Kind != "reading_position" && i.Kind != "page_position" && i.Kind != "reader_preference" {
			selected = append(selected, i)
		}
	}
	sum := sha256.Sum256(encodeJSON(selected))
	return hex.EncodeToString(sum[:])
}
func lifecycleInput(body []byte, commit bool, id string) (string, int64, error) {
	f, e := strictObject(body)
	if e != nil {
		return "", 0, e
	}
	operation := ""
	if commit {
		for k := range f {
			if k != "confirmed" && k != "plan_id" && k != "expected_revision" {
				return "", 0, &Error{"invalid_commit"}
			}
		}
		if string(f["confirmed"]) != "true" {
			return "", 0, &Error{"confirmation_required"}
		}
		var plan string
		if json.Unmarshal(f["plan_id"], &plan) != nil || plan != id {
			return "", 0, &Error{"plan_mismatch"}
		}
	} else {
		for k := range f {
			if k != "operation" && k != "expected_revision" {
				return "", 0, &Error{"invalid_plan"}
			}
		}
		if json.Unmarshal(f["operation"], &operation) != nil {
			return "", 0, &Error{"invalid_operation"}
		}
	}
	raw, ok := f["expected_revision"]
	if !ok {
		return "", 0, sec.ErrMissing
	}
	var rev int64
	if json.Unmarshal(raw, &rev) != nil || rev < 1 {
		return "", 0, &Error{"invalid_revision"}
	}
	return operation, rev, nil
}
func (s *Service) lifecycleOwner(ctx context.Context, tx app.WriteTx, token string) (domain.Session, error) {
	a, e := s.Auth.AuthenticateWrite(ctx, tx, token)
	if e == nil && a.Scope.Kind != "owner" {
		e = domain.ErrNotFound
	}
	return a, e
}
func (s *Service) lifecyclePage(ctx context.Context, tx app.WriteTx, token string, p domain.LifecyclePlan, o Options) (domain.LifecyclePage, error) {
	out := domain.LifecyclePage{LifecyclePlan: p, Items: []domain.LifecycleItem{}}
	if p.Operation == "purge" {
		status, e := tx.CopyBackupStatus()
		if e != nil {
			return out, e
		}
		out.Backup = &domain.PurgeBackup{State: "not_created", LastAttempt: status.LastAttempt, LastTrial: status.LastTrial}
		if status.LastComplete != nil {
			out.Backup.State = "verified"
			id, at := status.LastComplete.ID, status.LastComplete.CompletedAt
			out.Backup.SetID = &id
			out.Backup.LastCompleteAt = &at
		}
		if !status.Available {
			out.Backup.State = "unavailable"
			reason := status.Reason
			out.Backup.Reason = &reason
		}
	}

	c, n, _, err := s.cursorStart(ctx, token, tx, "lifecycle-plan:"+p.ID, o)
	if err != nil {
		return out, err
	}
	all, err := tx.LifecycleItems(p.ID)
	if err != nil {
		return out, err
	}
	_, out.Impact, err = appauth.ProjectLifecycleItems(tx, all)
	if err != nil {
		return out, err
	}
	for offset := c.Sequence; offset < int64(len(all)); offset++ {
		visible, _, e := appauth.ProjectLifecycleItems(tx, all[offset:offset+1])
		if e != nil {
			return out, e
		}
		if len(visible) == 0 {
			continue
		}
		if len(out.Items) == n {
			out.NextCursor = cursorNext(c)
			break
		}
		out.Items = append(out.Items, visible[0])
		c.Sequence = offset + 1
	}
	if p.State == "open" {
		expiry, e := time.Parse(time.RFC3339Nano, p.ExpiresAt)
		if e != nil {
			return out, e
		}
		if !s.lifecycleNow().Before(expiry) {
			out.State = "expired"
		}
	}
	return out, nil
}
func (s *Service) LifecyclePlanCreate(ctx context.Context, token, key, document string, body []byte) (domain.Response, error) {
	op, rev, err := lifecycleInput(body, false, "")
	if err != nil {
		return domain.Response{}, err
	}
	if validOperationKey(document) != nil {
		return domain.Response{}, &Error{"invalid_document"}
	}
	a, err := s.Auth.Authenticate(ctx, token)
	if err != nil {
		return domain.Response{}, err
	}
	if a.Scope.Kind != "owner" {
		return domain.Response{}, domain.ErrNotFound
	}
	keyless := key == ""
	if key == "" {
		key, err = service.NewID()
	} else {
		err = validOperationKey(key)
	}
	if err != nil {
		return domain.Response{}, err
	}
	r := domain.Request{Key: domain.OperationKey{PrincipalID: a.PrincipalID, OperationID: key}, Method: "POST", Target: "/api/v1/documents/" + document + "/lifecycle-plans", Body: encodeJSON(map[string]any{"operation": op, "expected_revision": rev})}
	apply := func(tx app.WriteTx, _ domain.Receipt) (domain.Response, error) {
		actor, e := s.lifecycleOwner(ctx, tx, token)
		if e != nil {
			return domain.Response{}, e
		}
		d, e := tx.LifecycleDocument(document)
		if e != nil {
			return domain.Response{}, e
		}
		if d.Revision != rev {
			return domain.Response{}, &PersonalConflict{Revision: &d.Revision}
		}
		next, e := LifecycleNext(d.Lifecycle, valueString(d.PreviousLifecycle), op)
		if e != nil {
			return domain.Response{}, e
		}
		config, e := tx.Configuration()
		if e != nil {
			return domain.Response{}, e
		}
		scope, e := tx.Scope(d.ProjectID)
		if e != nil {
			return domain.Response{}, e
		}
		items, e := tx.LifecycleImpact(document)
		if op == "purge" && e == nil {
			items, e = tx.PurgeImpact(document)
		}
		if e != nil {
			return domain.Response{}, e
		}
		items, _, e = appauth.ProjectLifecycleItems(tx, items)
		if e != nil {
			return domain.Response{}, e
		}
		id, e := service.NewID()
		if e != nil {
			return domain.Response{}, e
		}
		p := domain.LifecyclePlan{ID: id, OwnerID: actor.PrincipalID, DocumentID: document, Operation: op, State: "open", From: d.Lifecycle, To: next, ExpectedRevision: rev, ConfigRevision: config.Revision, PermissionsRevision: scope.Project.PermissionsRevision, ExpiresAt: s.lifecycleNow().Add(15 * time.Minute).Format(time.RFC3339Nano), Digest: lifecycleDigest(items, op == "purge")}

		if e = tx.PutLifecyclePlan(p, items); e != nil {
			return domain.Response{}, e
		}
		page, e := s.lifecyclePage(ctx, tx, token, p, Options{})
		return domain.Response{Status: 201, Body: encodeJSON(page)}, e
	}
	if !keyless {
		return app.Execute(ctx, s.Store, s.Auth.Authority(token), r, nil, apply)
	}
	var result domain.Response
	err = s.Store.Write(ctx, func(tx app.WriteTx) error { var e error; result, e = apply(tx, domain.Receipt{}); return e })
	if err != nil {
		return domain.Response{}, err
	}
	return s.Auth.Authority(token).Filter(ctx, r, result)
}
func valueString(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}
func (s *Service) LifecyclePlanGet(ctx context.Context, token, id string, o Options) (domain.LifecyclePage, error) {
	var out domain.LifecyclePage
	err := s.Store.Write(ctx, func(tx app.WriteTx) error {
		a, e := s.lifecycleOwner(ctx, tx, token)
		if e != nil {
			return e
		}
		p, e := tx.LifecyclePlan(id)
		if e != nil {
			return e
		}
		if p.OwnerID != a.PrincipalID {
			return domain.ErrNotFound
		}
		d, e := tx.Document(p.DocumentID)
		if e != nil {
			return e
		}
		if _, e = tx.Scope(d.ProjectID); e != nil {
			return e
		}
		out, e = s.lifecyclePage(ctx, tx, token, p, o)
		return e
	})
	return out, err
}
func (s *Service) LifecycleCommit(ctx context.Context, token, id, alias string, body []byte) (domain.Response, error) {
	_, rev, err := lifecycleInput(body, true, id)
	if err != nil {
		return domain.Response{}, err
	}
	if validOperationKey(id) != nil {
		return domain.Response{}, &Error{"invalid_plan"}
	}
	if alias != "" {
		if err = validOperationKey(alias); err != nil {
			return domain.Response{}, err
		}
	}
	var out domain.Response
	var principal string
	err = s.Store.Write(ctx, func(tx app.WriteTx) error {
		a, e := s.lifecycleOwner(ctx, tx, token)
		if e != nil {
			return e
		}
		principal = a.PrincipalID
		p, e := tx.LifecyclePlan(id)
		if e != nil {
			return e
		}
		if p.OwnerID != a.PrincipalID {
			return domain.ErrNotFound
		}
		d, e := tx.LifecycleDocument(p.DocumentID)
		if e != nil {
			return e
		}
		scope, e := tx.Scope(d.ProjectID)
		if e != nil {
			return e
		}
		r := domain.Request{Key: domain.OperationKey{PrincipalID: a.PrincipalID, OperationID: id}, Method: "POST", Target: "/api/v1/lifecycle-plans/" + id + "/commit", Body: encodeJSON(map[string]any{"plan_id": id, "expected_revision": rev, "confirmed": true})}
		receipt, exists, e := tx.ReserveOperation(r, nil)
		if e != nil {
			return e
		}
		if exists {
			if receipt.Response == nil {
				return domain.ErrReconciliation
			}
			out = *receipt.Response
		} else {
			if d.Revision != rev || p.ExpectedRevision != rev {
				return &PersonalConflict{Revision: &d.Revision}
			}
			expiry, e := time.Parse(time.RFC3339Nano, p.ExpiresAt)
			if e != nil {
				return e
			}
			if !s.lifecycleNow().Before(expiry) {
				return ErrStaleLifecyclePlan
			}
			config, e := tx.Configuration()
			if e != nil {
				return e
			}
			if config.Revision != p.ConfigRevision || scope.Project.PermissionsRevision != p.PermissionsRevision {
				return ErrStaleLifecyclePlan
			}
			items, e := tx.LifecycleImpact(p.DocumentID)
			if p.Operation == "purge" && e == nil {
				items, e = tx.PurgeImpact(p.DocumentID)
			}
			if e != nil {
				return e
			}
			items, _, e = appauth.ProjectLifecycleItems(tx, items)
			if e != nil {
				return e
			}
			if lifecycleDigest(items, p.Operation == "purge") != p.Digest {
				return ErrStaleLifecyclePlan
			}
			next, e := LifecycleNext(d.Lifecycle, valueString(d.PreviousLifecycle), p.Operation)
			if e != nil {
				return e
			}
			if next != p.To || d.Lifecycle != p.From {
				return ErrStaleLifecyclePlan
			}
			result := domain.LifecycleResult{OperationID: id, PlanID: id, DocumentID: p.DocumentID, Lifecycle: next, PreviousLifecycle: d.PreviousLifecycle, TrashedAt: d.TrashedAt, Revision: d.Revision, Changed: next != d.Lifecycle}
			if result.Changed {
				result.Revision++
				if next == "trash" {
					stamp := s.lifecycleNow().Format(time.RFC3339Nano)
					previous := d.Lifecycle
					result.PreviousLifecycle = &previous
					result.TrashedAt = &stamp
				} else {
					result.PreviousLifecycle = nil
					result.TrashedAt = nil
				}
			}
			if next == "purged" {
				result.JobID, e = service.NewID()
				if e != nil {
					return e
				}
				result.JobURL = "/api/v1/jobs/" + result.JobID
				if e = tx.StartPurge(p, result); e != nil {
					return e
				}
			}
			if e = tx.CommitLifecycle(p, result, a.ID); e != nil {
				return e
			}
			status := 200
			if next == "purged" {
				status = 202
			}
			out = domain.Response{Status: status, Body: encodeJSON(result)}
			if e = tx.CompleteOperation(r.Key, out); e != nil {
				return e
			}
		}
		if alias != "" && alias != id {
			r.Key.OperationID = alias
			old, exists, e := tx.ReserveOperation(r, nil)
			if e != nil {
				return e
			}
			if exists {
				if old.Response == nil || string(old.Response.Body) != string(out.Body) {
					return domain.ErrConflict
				}
			} else if e = tx.CompleteOperation(r.Key, out); e != nil {
				return e
			}
		}
		return nil
	})
	if err != nil {
		return domain.Response{}, err
	}
	return s.Auth.Authority(token).Filter(ctx, domain.Request{Key: domain.OperationKey{PrincipalID: principal, OperationID: id}, Method: "POST", Target: "/api/v1/lifecycle-plans/" + id + "/commit"}, out)
}
func (s *Service) Trash(ctx context.Context, token string, o Options) (List[domain.TrashItem], error) {
	out := List[domain.TrashItem]{Items: []domain.TrashItem{}}
	err := s.Store.Write(ctx, func(tx app.WriteTx) error {
		if _, e := s.lifecycleOwner(ctx, tx, token); e != nil {
			return e
		}
		c, n, _, e := s.cursorStart(ctx, token, tx, "trash", o)
		if e != nil {
			return e
		}
		rows, e := tx.TrashDocuments(o.Project, c.After, n+1)
		if e != nil {
			return e
		}
		config, e := tx.Configuration()
		if e != nil {
			return e
		}
		var policy struct {
			Retention struct {
				Trash string `json:"trash"`
			} `json:"retention"`
		}
		_ = json.Unmarshal(config.Value, &policy)
		duration := time.Duration(0)
		raw := policy.Retention.Trash
		if len(raw) > 1 && raw != "indefinite" {
			value, e := strconv.ParseInt(raw[:len(raw)-1], 10, 64)
			unit := map[byte]time.Duration{'s': time.Second, 'm': time.Minute, 'h': time.Hour, 'd': 24 * time.Hour, 'w': 7 * 24 * time.Hour}[raw[len(raw)-1]]
			if e == nil && unit > 0 && value > 0 && value <= int64((1<<63-1)/unit) {
				duration = time.Duration(value) * unit
			}
		}
		for i, d := range rows {
			if i == n {
				out.NextCursor = cursorNext(c)
				break
			}
			if duration > 0 && d.TrashedAt != nil {
				if stamp, e := time.Parse(time.RFC3339Nano, *d.TrashedAt); e == nil {
					v := stamp.Add(duration).Format(time.RFC3339Nano)
					d.PurgeEligibleAt = &v
				}
			}
			out.Items = append(out.Items, d)
			c.After = d.DocumentID
		}
		return nil
	})
	return out, err
}
