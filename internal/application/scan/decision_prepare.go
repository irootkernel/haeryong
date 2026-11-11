package scan

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/url"
	"time"

	"haeryong.local/haeryong/internal/application/source"
	app "haeryong.local/haeryong/internal/application/storage"
	"haeryong.local/haeryong/internal/domain"
)

// Preparation only rereads retained objects. Pins outlive verification through
// the final authority/CAS writer; no provider, source capture, or network access.
func (s *Service) prepareFindingAction(ctx context.Context, token, key, id, etag string, body []byte) (domain.Response, bool, func(), error) {
	release := func() {}
	actor, e := s.Auth.Authenticate(ctx, token)
	if e != nil {
		return domain.Response{}, false, release, e
	}
	if actor.Scope.Kind != "owner" {
		return domain.Response{}, false, release, domain.ErrDenied
	}
	req := domain.Request{Key: domain.OperationKey{PrincipalID: actor.PrincipalID, OperationID: key}, Method: "POST", Target: "/api/v1/findings/" + url.PathEscape(id) + "/decisions", Body: body, Preconditions: map[string]string{"if-match": etag}}
	if r, found, e := app.Replay(ctx, s.Store, s.Auth.Authority(token), req); e != nil || found {
		return r, found, release, e
	}
	objects := map[string]domain.Object{}
	pins := []string{}
	release = func() {
		c, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = s.Store.Write(c, func(tx app.WriteTx) error {
			for _, p := range pins {
				if e := tx.ReleasePin(p); e != nil {
					return e
				}
			}
			return nil
		})
	}
	e = s.Store.Write(ctx, func(tx app.WriteTx) error {
		owner, e := s.Auth.AuthenticateWrite(ctx, tx, token)
		if e != nil {
			return e
		}
		if owner.Scope.Kind != "owner" {
			return domain.ErrDenied
		}
		f, e := tx.Finding(id)
		if e != nil {
			return e
		}
		if !visibleFindingForActor(tx, owner, f) {
			return domain.ErrNotFound
		}
		if etag != domain.FindingETag(f) {
			return domain.ErrPrecondition
		}
		if f.Status != "proposed" && f.Status != "blocked" {
			return nil
		}
		add := func(hash string) error {
			if hash == "" {
				return nil
			}
			if _, ok := objects[hash]; ok {
				return nil
			}
			o, e := tx.Object(hash)
			if e != nil || o.State != "ready" {
				return actionError(409, "OPERATION_CONFLICT", "retained_evidence_unavailable")
			}
			pin := domain.Pin{ID: "finding-decision:" + key + ":" + hash, Kind: "finding_decision", OwnerID: key, Hash: hash}
			if e = tx.AcquirePin(pin); e != nil {
				return e
			}
			pins = append(pins, pin.ID)
			objects[hash] = o
			return nil
		}
		for _, version := range decisionVersions(f) {
			if _, e = source.Check(tx, owner, version); e != nil {
				return e
			}
			v, e := tx.PublishedVersion(version)
			if e != nil {
				return e
			}
			if e = add(v.ManifestSHA); e != nil {
				return e
			}
			if e = add(v.MetadataHash); e != nil {
				return e
			}
			after := ""
			for {
				entries, e := tx.Entries(v.ContentSetID, after, 128)
				if e != nil {
					return e
				}
				if len(entries) == 0 {
					break
				}
				for _, entry := range entries {
					after = entry.Path
					if entry.Kind == "file" {
						if e = add(entry.Hash); e != nil {
							return e
						}
					}
				}
			}
		}
		var walk func(any) error
		walk = func(v any) error {
			switch x := v.(type) {
			case map[string]any:
				for k, v := range x {
					if k == "snapshot_sha256" || k == "bytes_sha256" {
						if h, ok := v.(string); ok && h != "" {
							if e := add(h); e != nil {
								return e
							}
						}
					}
					if e := walk(v); e != nil {
						return e
					}
				}
			case []any:
				for _, v := range x {
					if e := walk(v); e != nil {
						return e
					}
				}
			}
			return nil
		}
		var evidence any
		if json.Unmarshal(f.Evidence, &evidence) != nil {
			return domain.ErrReconciliation
		}
		return walk(evidence)
	})
	if e != nil {
		return domain.Response{}, false, release, e
	}
	if s.Bytes == nil {
		return domain.Response{}, false, release, actionError(409, "OPERATION_CONFLICT", "retained_evidence_unavailable")
	}
	for _, o := range objects {
		e = s.Bytes.Read(ctx, o, domain.Pin{Kind: "finding_decision", OwnerID: key}, func(r io.ReadSeeker) error {
			h := sha256.New()
			n, e := io.Copy(h, r)
			if e != nil {
				return e
			}
			if n != o.Size || hex.EncodeToString(h.Sum(nil)) != o.Hash {
				return domain.ErrContentIntegrity
			}
			return nil
		})
		if e != nil {
			if errors.Is(e, context.Canceled) {
				return domain.Response{}, false, release, e
			}
			return domain.Response{}, false, release, actionError(409, "OPERATION_CONFLICT", "retained_evidence_unavailable")
		}
	}
	return domain.Response{}, false, release, nil
}

func (s *Service) refuseFinding(tx app.WriteTx, f domain.Finding, cause error) (domain.Response, error) {
	var failure *domain.FindingActionError
	if !errors.As(cause, &failure) || failure.Status != 409 {
		return domain.Response{}, cause
	}
	old := f.Revision
	f.Revision++
	f.UpdatedAt = s.Now().UTC().Format(time.RFC3339Nano)
	f.Status = "blocked"
	if failure.Code == "VERSION_CONFLICT" || failure.Reason == "owner_classification_changed" || failure.Reason == "owner_decision_changed" {
		f.Status = "stale"
	}
	f.Reason = failure.Reason
	f.ApprovalRequired = false
	if e := tx.ReviseFinding(f, old); e != nil {
		return domain.Response{}, e
	}
	result := decisionFailure(failure.Status, failure.Code, failure.Reason)
	result.Headers = map[string]string{"ETag": domain.FindingETag(f)}
	return result, nil
}
