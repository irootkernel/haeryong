package sqlite

import (
	"encoding/json"
	"fmt"
	"haeryong.local/haeryong/internal/domain"
	"strings"
	"time"
)

func (t *writeTx) AuthState() (domain.AuthState, error) {
	var s domain.AuthState
	var config []byte
	err := t.tx.QueryRowContext(t.ctx, "SELECT library_id,auth_epoch,principal_id FROM libraries,auth_owner WHERE libraries.singleton=1 AND auth_owner.singleton=1").Scan(&s.LibraryID, &s.AuthEpoch, &s.OwnerID)
	if err != nil {
		return s, err
	}
	if err = t.tx.QueryRowContext(t.ctx, "SELECT validated_json FROM config_revisions ORDER BY config_revision DESC LIMIT 1").Scan(&config); err != nil {
		return s, err
	}
	var v struct {
		Security domain.AuthSettings `json:"security"`
	}
	if err = json.Unmarshal(config, &v); err != nil {
		return s, err
	}
	s.Settings = v.Security
	if s.Settings.PairingExpirySeconds < 1 {
		return s, fmt.Errorf("invalid pairing policy")
	}
	for _, p := range []*int64{s.Settings.SessionIdleSeconds, s.Settings.SessionAbsoluteSeconds} {
		if p != nil && (*p < 1) {
			return s, fmt.Errorf("invalid session expiry policy")
		}
	}
	return s, nil
}

const sessionColumns = "session_id,principal_id,library_id,auth_epoch,token_hash,device_label,origin,created_at,last_seen_at,revoked,revision"

func scanSession(row interface{ Scan(...any) error }) (domain.Session, error) {
	var s domain.Session
	var created, seen int64
	err := row.Scan(&s.ID, &s.PrincipalID, &s.LibraryID, &s.AuthEpoch, &s.TokenHash, &s.DeviceLabel, &s.Origin, &created, &seen, &s.Revoked, &s.Revision)
	s.CreatedAt = time.UnixMilli(created).UTC()
	s.LastSeenAt = time.UnixMilli(seen).UTC()
	return s, notFound(err)
}
func (t *writeTx) SessionByHash(hash string) (domain.Session, error) {
	return scanSession(t.tx.QueryRowContext(t.ctx, "SELECT "+sessionColumns+" FROM device_sessions WHERE token_hash=?", hash))
}
func (t *writeTx) SessionByID(id string) (domain.Session, error) {
	return scanSession(t.tx.QueryRowContext(t.ctx, "SELECT "+sessionColumns+" FROM device_sessions WHERE session_id=?", id))
}
func (t *writeTx) Sessions() ([]domain.Session, error) {
	rows, err := t.tx.QueryContext(t.ctx, "SELECT "+sessionColumns+" FROM device_sessions ORDER BY created_at,session_id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.Session{}
	for rows.Next() {
		s, err := scanSession(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}
func (t *writeTx) PutSession(s domain.Session) error {
	// Identity/verifier/creation provenance are immutable on a touch or revocation.
	return t.exec("INSERT INTO device_sessions(session_id,principal_id,library_id,auth_epoch,token_hash,device_label,origin,created_at,last_seen_at,revoked) VALUES(?,?,?,?,?,?,?,?,?,?) ON CONFLICT(session_id) DO UPDATE SET revision=device_sessions.revision+CASE WHEN excluded.last_seen_at>device_sessions.last_seen_at OR excluded.revoked>device_sessions.revoked THEN 1 ELSE 0 END,last_seen_at=max(device_sessions.last_seen_at,excluded.last_seen_at),revoked=max(device_sessions.revoked,excluded.revoked)", s.ID, s.PrincipalID, s.LibraryID, s.AuthEpoch, s.TokenHash, s.DeviceLabel, s.Origin, s.CreatedAt.UnixMilli(), s.LastSeenAt.UnixMilli(), s.Revoked)
}

const pairingColumns = "pairing_id,device_label,origin,created_at,expires_at,state,revision,secret_hash,code_hash,client_hash,auth_epoch,poll_after,consumed"

func scanPairing(row interface{ Scan(...any) error }) (domain.Pairing, error) {
	var p domain.Pairing
	var created, expires, poll int64
	err := row.Scan(&p.ID, &p.DeviceLabel, &p.Origin, &created, &expires, &p.State, &p.Revision, &p.SecretHash, &p.CodeHash, &p.ClientHash, &p.AuthEpoch, &poll, &p.Consumed)
	p.CreatedAt = time.UnixMilli(created).UTC()
	p.ExpiresAt = time.UnixMilli(expires).UTC()
	p.PollAfter = time.UnixMilli(poll).UTC()
	p.ETag = fmt.Sprintf("\"pairing-%s-r%d\"", p.ID, p.Revision)
	return p, notFound(err)
}
func (t *writeTx) Pairing(id string) (domain.Pairing, error) {
	return scanPairing(t.tx.QueryRowContext(t.ctx, "SELECT "+pairingColumns+" FROM pairings WHERE pairing_id=?", id))
}
func (t *writeTx) Pairings(pending bool) ([]domain.Pairing, error) {
	query := "SELECT " + pairingColumns + " FROM pairings"
	if pending {
		query += " WHERE state IN ('pending','approved') AND consumed=0"
	}
	query += " ORDER BY created_at,pairing_id"
	rows, err := t.tx.QueryContext(t.ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.Pairing{}
	for rows.Next() {
		p, err := scanPairing(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}
func (t *writeTx) PutPairing(p domain.Pairing) error {
	return t.exec("INSERT INTO pairings VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(pairing_id) DO UPDATE SET state=excluded.state,revision=excluded.revision,poll_after=excluded.poll_after,consumed=excluded.consumed", p.ID, p.DeviceLabel, p.Origin, p.CreatedAt.UnixMilli(), p.ExpiresAt.UnixMilli(), p.State, p.Revision, p.SecretHash, p.CodeHash, p.ClientHash, p.AuthEpoch, p.PollAfter.UnixMilli(), p.Consumed)
}
func (t *writeTx) AuthAudit(actor, target, kind string) error {
	resourceKind := "pairing"
	revision := int64(1)
	event := "resource.invalidated"
	if strings.HasPrefix(kind, "session_") {
		resourceKind = "session"
		record, err := t.SessionByID(target)
		if err != nil {
			return err
		}
		revision = record.Revision
		if kind == "session_revoked" {
			event = "session.revoked"
		}
	} else {
		record, err := t.Pairing(target)
		if err != nil {
			return err
		}
		revision = record.Revision
	}
	return t.appendEvent(resourceKind, target, event, revision, jsonBytes(map[string]string{"actor": actor}))
}
func (t *writeTx) Job(id string) (domain.Job, error) { return job(t.ctx, t.tx, id) }
func (t *writeTx) Jobs() ([]domain.Job, error) {
	// Close the ID cursor before reading rows on the same serialized connection.
	rows, err := t.tx.QueryContext(t.ctx, "SELECT job_id FROM jobs ORDER BY job_id")
	if err != nil {
		return nil, err
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	out := []domain.Job{}
	for _, id := range ids {
		j, err := t.Job(id)
		if err != nil {
			return nil, err
		}
		out = append(out, j)
	}
	return out, nil
}
func (t *writeTx) Receipt(key domain.OperationKey) (domain.Receipt, error) {
	return receipt(t.ctx, t.tx, key)
}
func (t *writeTx) Events(epoch string, after int64, limit int) ([]domain.Event, error) {
	var current string
	if err := t.tx.QueryRowContext(t.ctx, "SELECT stream_epoch FROM libraries").Scan(&current); err != nil {
		return nil, err
	}
	if epoch != "" && epoch != current {
		return nil, domain.ErrPrecondition
	}
	rows, err := t.tx.QueryContext(t.ctx, "SELECT stream_epoch,sequence,resource_kind,resource_id,kind,revision,body FROM events WHERE sequence>? ORDER BY sequence LIMIT ?", after, batch(limit))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.Event{}
	for rows.Next() {
		var e domain.Event
		if err := rows.Scan(&e.StreamEpoch, &e.Sequence, &e.ResourceKind, &e.ResourceID, &e.Kind, &e.Revision, &e.Body); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func (t *writeTx) PairingStats(epoch string, now int64, clientHash string) (int, int, error) {
	var pending, created int
	err := t.tx.QueryRowContext(t.ctx, "SELECT count(*) FROM pairings WHERE auth_epoch=? AND state IN ('pending','approved') AND consumed=0 AND expires_at>?", epoch, now).Scan(&pending)
	if err != nil {
		return 0, 0, err
	}
	err = t.tx.QueryRowContext(t.ctx, "SELECT count(*) FROM pairings WHERE client_hash=? AND created_at>?", clientHash, now-60000).Scan(&created)
	return pending, created, err
}

func (t *writeTx) Document(id string) (domain.Document, error) {
	d := domain.Document{ID: id}
	err := t.tx.QueryRowContext(t.ctx, "SELECT project_id,title,lifecycle,coalesce(latest_version_id,''),revision FROM documents WHERE document_id=?", id).Scan(&d.ProjectID, &d.Title, &d.Lifecycle, &d.LatestVersionID, &d.Revision)
	return d, notFound(err)
}
func (t *writeTx) Version(id string) (domain.Version, error) {
	v := domain.Version{ID: id}
	err := t.tx.QueryRowContext(t.ctx, "SELECT document_id,content_set_id,coalesce(base_version_id,''),title_snapshot,viewer_profile,source_basis,coalesce(sequence,0),default_page,metadata FROM document_versions WHERE version_id=?", id).Scan(&v.DocumentID, &v.ContentSetID, &v.BaseVersionID, &v.Title, &v.ViewerProfile, &v.SourceBasis, &v.Sequence, &v.DefaultPage, &v.Metadata)
	return v, notFound(err)
}

func (t *writeTx) EventHead() (string, int64, error) {
	var epoch string
	var head int64
	err := t.tx.QueryRowContext(t.ctx, "SELECT stream_epoch,(SELECT coalesce(max(sequence),0) FROM events) FROM libraries WHERE singleton=1").Scan(&epoch, &head)
	return epoch, head, err
}
