package authingress

import (
	"path/filepath"
	"testing"
)

func epochs(t *testing.T, s *store) map[string]string {
	t.Helper()
	values := map[string]string{}
	for _, name := range []string{"auth_epoch", "runtime_generation", "stream_epoch"} {
		var value string
		if err := s.db.QueryRow("SELECT value FROM generations WHERE name=?", name).Scan(&value); err != nil {
			t.Fatal(err)
		}
		values[name] = value
	}
	return values
}

func TestRestoredAuthInvalidatesOnceAndNewPairingSurvivesRestart(t *testing.T) {
	s := fixture(t)
	old := trusted(t, s)
	pending, err := s.create()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.decide(pending.ID, pending.Code, "approved", old, false); err != nil {
		t.Fatal(err)
	}
	if value, err := s.action(old, "same-owner-operation"); err != nil || value != 1 {
		t.Fatal(value, err)
	}
	snapshot := filepath.Join(t.TempDir(), "restored.db")
	if _, err := s.db.Exec("VACUUM INTO ?", snapshot); err != nil {
		t.Fatal(err)
	}
	restored, err := open(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { restored.db.Close() }()
	if _, err := restored.session(old); err != nil {
		t.Fatal("snapshot did not contain old session; invalidation test would be vacuous", err)
	}
	before := epochs(t, restored)
	if err := restored.invalidateRestoredAuth(); err != nil {
		t.Fatal(err)
	}
	after := epochs(t, restored)
	for name, value := range before {
		if after[name] == value {
			t.Fatal("restored generation did not rotate", name)
		}
	}
	if _, err := restored.session(old); err == nil {
		t.Fatal("restored old session remained valid")
	}
	if _, err := restored.redeem(pending.ID, pending.Secret, 0); err == nil {
		t.Fatal("restored pending request minted a session")
	}
	next := trusted(t, restored)
	if value, err := restored.action(next, "same-owner-operation"); err != nil || value != 1 {
		t.Fatal("same owner receipt was lost", value, err)
	}
	if err := restored.db.Close(); err != nil {
		t.Fatal(err)
	}
	restored, err = open(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := restored.session(next); err != nil {
		t.Fatal("normal restart invalidated newly paired device", err)
	}
	for name, value := range epochs(t, restored) {
		if after[name] != value {
			t.Fatal("normal restart rotated generation", name)
		}
	}
}
