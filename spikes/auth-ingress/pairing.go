// Disposable pairing-store proof; not a production authentication service.
package authingress

import (
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"time"

	_ "modernc.org/sqlite"
)

var errDenied = errors.New("request not authorized")
var errTooSoon = errors.New("poll again after two seconds")

type store struct {
	db  *sql.DB
	now func() time.Time
}

type request struct {
	ID, Code, Secret, Label string
	Created, Expires        int64
}

type decisionRecord struct {
	ID, Label, Decision string
	Created, Expires    int64
}

const pairingLifetime = 5 * time.Minute

type device struct {
	ID      string `json:"id"`
	Label   string `json:"label"`
	Expires int64  `json:"expires"`
	Revoked bool   `json:"revoked"`
	Expired bool   `json:"expired"`
}

func random() string {
	var bytes [32]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		panic(err)
	}
	return hex.EncodeToString(bytes[:])
}

func hash(value string) string {
	digest := sha256.Sum256([]byte(value))
	return hex.EncodeToString(digest[:])
}

func open(path string) (*store, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	for _, query := range []string{
		"PRAGMA journal_mode=WAL", "PRAGMA synchronous=FULL", "PRAGMA busy_timeout=5000",
		"CREATE TABLE IF NOT EXISTS pairing(id TEXT PRIMARY KEY, code TEXT, secret_hash TEXT, decision TEXT, expires INTEGER, last_poll INTEGER DEFAULT 0, label TEXT)",
		"CREATE TABLE IF NOT EXISTS sessions(token_hash TEXT PRIMARY KEY, expires INTEGER, revoked INTEGER DEFAULT 0, id TEXT UNIQUE, label TEXT)",
		"CREATE TABLE IF NOT EXISTS device_proofs(device_id TEXT, phase TEXT, payload TEXT NOT NULL, updated INTEGER NOT NULL, PRIMARY KEY(device_id,phase))",
		"CREATE TABLE IF NOT EXISTS receipts(operation_id TEXT PRIMARY KEY, response INTEGER)",
		"CREATE TABLE IF NOT EXISTS fixture_counter(id INTEGER PRIMARY KEY, value INTEGER)",
		"CREATE TABLE IF NOT EXISTS generations(name TEXT PRIMARY KEY, value TEXT)",
		"INSERT OR IGNORE INTO fixture_counter VALUES(1,0)",
	} {
		if _, err := db.Exec(query); err != nil {
			db.Close()
			return nil, err
		}
	}
	for _, name := range []string{"auth_epoch", "runtime_generation", "stream_epoch"} {
		if _, err := db.Exec("INSERT OR IGNORE INTO generations(name,value) VALUES(?,?)", name, random()); err != nil {
			db.Close()
			return nil, err
		}
	}
	return &store{db: db, now: time.Now}, nil
}

func (s *store) invalidateRestoredAuth() error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, query := range []string{"DELETE FROM sessions", "DELETE FROM pairing"} {
		if _, err := tx.Exec(query); err != nil {
			return err
		}
	}
	for _, name := range []string{"auth_epoch", "runtime_generation", "stream_epoch"} {
		if _, err := tx.Exec("UPDATE generations SET value=? WHERE name=?", random(), name); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *store) create() (request, error) {
	return s.createDevice("Fixture device")
}

func (s *store) createDevice(label string) (request, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return request{}, err
	}
	defer tx.Rollback()
	var pending int
	if err := tx.QueryRow("SELECT count(*) FROM pairing WHERE decision='pending' AND expires>?", s.now().UnixNano()).Scan(&pending); err != nil {
		return request{}, err
	}
	if pending >= 5 {
		return request{}, errDenied
	}
	created := s.now()
	r := request{ID: random(), Code: random()[:8], Secret: random(), Label: label, Created: created.UnixNano(), Expires: created.Add(pairingLifetime).UnixNano()}
	if _, err := tx.Exec("INSERT INTO pairing(id,code,secret_hash,decision,expires,label) VALUES(?,?,?,'pending',?,?)", r.ID, r.Code, hash(r.Secret), r.Expires, label); err != nil {
		return request{}, err
	}
	return r, tx.Commit()
}

func (s *store) poll(id, secret string) (string, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	var decision string
	var last int64
	if err := tx.QueryRow("SELECT decision,last_poll FROM pairing WHERE id=? AND secret_hash=? AND expires>?", id, hash(secret), s.now().UnixNano()).Scan(&decision, &last); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", errDenied
		}
		return "", err
	}
	now := s.now().UnixNano()
	if last != 0 && now-last < int64(2*time.Second) {
		return "", errTooSoon
	}
	if _, err := tx.Exec("UPDATE pairing SET last_poll=? WHERE id=?", now, id); err != nil {
		return "", err
	}
	return decision, tx.Commit()
}

func (s *store) authorize(tx *sql.Tx, token string) error {
	var n int
	err := tx.QueryRow("SELECT count(*) FROM sessions WHERE token_hash=? AND revoked=0 AND (expires=0 OR expires>?)", hash(token), s.now().UnixNano()).Scan(&n)
	if err != nil {
		return err
	}
	if n != 1 {
		return errDenied
	}
	return nil
}

func (s *store) decide(id, code, decision, owner string, bootstrap bool) (decisionRecord, error) {
	if decision != "approved" && decision != "rejected" {
		return decisionRecord{}, errDenied
	}
	tx, err := s.db.Begin()
	if err != nil {
		return decisionRecord{}, err
	}
	defer tx.Rollback()
	if !bootstrap {
		if err := s.authorize(tx, owner); err != nil {
			return decisionRecord{}, err
		}
	}
	result, err := tx.Exec("UPDATE pairing SET decision=? WHERE id=? AND code=? AND decision='pending' AND expires>?", decision, id, code, s.now().UnixNano())
	if err != nil {
		return decisionRecord{}, err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return decisionRecord{}, err
	}
	if n != 1 {
		return decisionRecord{}, errDenied
	}
	value := decisionRecord{ID: id, Decision: decision}
	if err := tx.QueryRow("SELECT label,expires FROM pairing WHERE id=?", id).Scan(&value.Label, &value.Expires); err != nil {
		return decisionRecord{}, err
	}
	value.Created = value.Expires - int64(pairingLifetime)
	if err := tx.Commit(); err != nil {
		return decisionRecord{}, err
	}
	return value, nil
}

func (s *store) redeem(id, secret string, lifetime time.Duration) (string, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	result, err := tx.Exec("UPDATE pairing SET decision='redeemed' WHERE id=? AND secret_hash=? AND decision='approved' AND expires>?", id, hash(secret), s.now().UnixNano())
	if err != nil {
		return "", err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return "", err
	}
	if n != 1 {
		return "", errDenied
	}
	var label string
	if err := tx.QueryRow("SELECT label FROM pairing WHERE id=?", id).Scan(&label); err != nil {
		return "", err
	}
	token := random()
	var expires int64
	if lifetime > 0 {
		expires = s.now().Add(lifetime).UnixNano()
	}
	if _, err := tx.Exec("INSERT INTO sessions(token_hash,expires,id,label) VALUES(?,?,?,?)", hash(token), expires, random(), label); err != nil {
		return "", err
	}
	if err := tx.Commit(); err != nil {
		return "", err
	}
	return token, nil
}

func (s *store) revoke(owner, target string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := s.authorize(tx, owner); err != nil {
		return err
	}
	result, err := tx.Exec("UPDATE sessions SET revoked=1 WHERE id=?", target)
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count != 1 {
		return errDenied
	}
	return tx.Commit()
}

func (s *store) session(token string) (device, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return device{}, err
	}
	defer tx.Rollback()
	if err := s.authorize(tx, token); err != nil {
		return device{}, err
	}
	var value device
	err = tx.QueryRow("SELECT id,label,expires,revoked FROM sessions WHERE token_hash=?", hash(token)).Scan(&value.ID, &value.Label, &value.Expires, &value.Revoked)
	return value, err
}

func (s *store) devices(owner string) ([]device, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if err := s.authorize(tx, owner); err != nil {
		return nil, err
	}
	rows, err := tx.Query("SELECT id,label,expires,revoked FROM sessions ORDER BY id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	values := []device{}
	for rows.Next() {
		var value device
		if err := rows.Scan(&value.ID, &value.Label, &value.Expires, &value.Revoked); err != nil {
			return nil, err
		}
		value.Expired = value.Expires > 0 && value.Expires <= s.now().UnixNano()
		values = append(values, value)
	}
	return values, rows.Err()
}

func (s *store) action(token, id string) (int, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	if err := s.authorize(tx, token); err != nil {
		return 0, err
	}
	var value int
	err = tx.QueryRow("SELECT response FROM receipts WHERE operation_id=?", id).Scan(&value)
	if err == nil {
		return value, tx.Commit()
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return 0, err
	}
	if _, err := tx.Exec("UPDATE fixture_counter SET value=value+1 WHERE id=1"); err != nil {
		return 0, err
	}
	if err := tx.QueryRow("SELECT value FROM fixture_counter WHERE id=1").Scan(&value); err != nil {
		return 0, err
	}
	if _, err := tx.Exec("INSERT INTO receipts VALUES(?,?)", id, value); err != nil {
		return 0, err
	}
	return value, tx.Commit()
}
