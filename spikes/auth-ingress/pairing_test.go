package authingress

import (
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func fixture(t *testing.T) *store {
	t.Helper()
	s, err := open(filepath.Join(t.TempDir(), "pairing.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.db.Close() })
	return s
}

func trusted(t *testing.T, s *store) string {
	t.Helper()
	r, err := s.create()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.decide(r.ID, r.Code, "approved", "", true); err != nil {
		t.Fatal(err)
	}
	token, err := s.redeem(r.ID, r.Secret, 0)
	if err != nil {
		t.Fatal(err)
	}
	return token
}

func deviceID(t *testing.T, s *store, token string) string {
	t.Helper()
	value, err := s.session(token)
	if err != nil {
		t.Fatal(err)
	}
	return value.ID
}

func TestConcurrentPairing(t *testing.T) {
	s := fixture(t)
	owner := trusted(t, s)
	r, err := s.create()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.decide(r.ID, r.Code, "approved", r.Secret, false); err == nil {
		t.Fatal("polling secret approved itself")
	}
	var decisions atomic.Int32
	var winner string
	var wg sync.WaitGroup
	for _, decision := range []string{"approved", "rejected"} {
		wg.Add(1)
		go func(decision string) {
			defer wg.Done()
			if _, err := s.decide(r.ID, r.Code, decision, owner, false); err == nil {
				decisions.Add(1)
				winner = decision
			}
		}(decision)
	}
	wg.Wait()
	if decisions.Load() != 1 {
		t.Fatal(decisions.Load())
	}
	var terminal string
	if err := s.db.QueryRow("SELECT decision FROM pairing WHERE id=?", r.ID).Scan(&terminal); err != nil {
		t.Fatal(err)
	}
	if (terminal != "approved" && terminal != "rejected") || terminal != winner {
		t.Fatal("stored decision does not match the authoritative winner", terminal, winner)
	}
	var minted atomic.Int32
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := s.redeem(r.ID, r.Secret, 0); err == nil {
				minted.Add(1)
			}
		}()
	}
	wg.Wait()
	if terminal == "approved" && minted.Load() != 1 {
		t.Fatal("approved mint", minted.Load())
	}
	if terminal == "rejected" && minted.Load() != 0 {
		t.Fatal("rejected mint", minted.Load())
	}
}

func TestExactComparisonAndRedemptionSecret(t *testing.T) {
	s := fixture(t)
	owner := trusted(t, s)
	a, err := s.createDevice("Same label")
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.createDevice("Same label")
	if err != nil {
		t.Fatal(err)
	}
	for _, wrong := range []string{b.Code, a.Label} {
		if _, err := s.decide(a.ID, wrong, "approved", owner, false); err == nil {
			t.Fatal("wrong comparison approved")
		}
	}
	if decision, err := s.poll(a.ID, a.Secret); err != nil || decision != "pending" {
		t.Fatal("wrong comparison changed pending decision", decision, err)
	}
	if _, err := s.decide(a.ID, a.Code, "approved", owner, false); err != nil {
		t.Fatal(err)
	}
	if _, err := s.redeem(a.ID, b.Secret, 0); err == nil {
		t.Fatal("another request's secret redeemed")
	}
	if _, err := s.redeem(a.ID, a.Secret, 0); err != nil {
		t.Fatal("wrong secret consumed the approved request", err)
	}
	if _, err := s.redeem(a.ID, a.Secret, 0); err == nil {
		t.Fatal("second redemption minted a token")
	}
	var stored, code, label string
	if err := s.db.QueryRow("SELECT secret_hash,code,label FROM pairing WHERE id=?", b.ID).Scan(&stored, &code, &label); err != nil {
		t.Fatal(err)
	}
	if stored != hash(b.Secret) || stored == b.Secret || code == b.Secret || label == b.Secret {
		t.Fatal("raw polling secret persisted")
	}
}

func TestSingleUseAndRejectedRequest(t *testing.T) {
	s := fixture(t)
	owner := trusted(t, s)
	for _, decision := range []string{"approved", "rejected"} {
		r, err := s.create()
		if err != nil {
			t.Fatal(err)
		}
		if _, err := s.decide(r.ID, r.Code, decision, owner, false); err != nil {
			t.Fatal(err)
		}
		var minted atomic.Int32
		var wg sync.WaitGroup
		for i := 0; i < 12; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				if _, err := s.redeem(r.ID, r.Secret, 0); err == nil {
					minted.Add(1)
				}
			}()
		}
		wg.Wait()
		want := int32(0)
		if decision == "approved" {
			want = 1
		}
		if minted.Load() != want {
			t.Fatal(decision, minted.Load())
		}
	}
}

func TestOrdinaryRestartRetainsSession(t *testing.T) {
	path := filepath.Join(t.TempDir(), "remembered.db")
	s, err := open(path)
	if err != nil {
		t.Fatal(err)
	}
	owner := trusted(t, s)
	if _, err := s.action(owner, "same-operation"); err != nil {
		t.Fatal(err)
	}
	if err := s.db.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.db.Close()
	value, err := s.action(owner, "same-operation")
	if err != nil || value != 1 {
		t.Fatal(value, err)
	}
}

func TestPendingBoundAndExpiry(t *testing.T) {
	s := fixture(t)
	var requests []request
	for i := 0; i < 5; i++ {
		r, err := s.create()
		if err != nil {
			t.Fatal(err)
		}
		requests = append(requests, r)
	}
	if _, err := s.create(); err == nil {
		t.Fatal("pending capacity exceeded")
	}
	now := s.now()
	s.now = func() time.Time { return now.Add(6 * time.Minute) }
	for _, r := range requests {
		if _, err := s.decide(r.ID, r.Code, "approved", "", true); err == nil {
			t.Fatal("expired request approved")
		}
		if _, err := s.redeem(r.ID, r.Secret, 0); err == nil {
			t.Fatal("expired request redeemed")
		}
	}
	if _, err := s.create(); err != nil {
		t.Fatal("expired pending entries consumed capacity", err)
	}
}

func TestRememberedExpiryRevocationAndOwnerReceipts(t *testing.T) {
	s := fixture(t)
	owner := trusted(t, s)
	next := trusted(t, s)
	v, err := s.action(owner, "operation-1")
	if err != nil || v != 1 {
		t.Fatal(v, err)
	}
	v, err = s.action(next, "operation-1")
	if err != nil || v != 1 {
		t.Fatal("owner identity was split across devices", v, err)
	}
	if err := s.revoke(owner, deviceID(t, s, next)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.action(next, "operation-1"); err == nil {
		t.Fatal("revoked receipt replay")
	}
	r, err := s.create()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.decide(r.ID, r.Code, "approved", owner, false); err != nil {
		t.Fatal(err)
	}
	token, err := s.redeem(r.ID, r.Secret, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	now := s.now()
	s.now = func() time.Time { return now.Add(2 * time.Second) }
	if _, err := s.action(token, "operation-2"); err == nil {
		t.Fatal("expired session")
	}
	if _, err := s.action(owner, "operation-2"); err != nil {
		t.Fatal("unexpired remembered owner", err)
	}
	var stored string
	if err := s.db.QueryRow("SELECT token_hash FROM sessions WHERE token_hash=?", hash(owner)).Scan(&stored); err != nil || stored == owner {
		t.Fatal("raw token stored", err)
	}
}

func TestPollingCadenceAndSecret(t *testing.T) {
	s := fixture(t)
	r, err := s.create()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.poll(r.ID, "not-the-secret"); err != errDenied {
		t.Fatal(err)
	}
	if decision, err := s.poll(r.ID, r.Secret); err != nil || decision != "pending" {
		t.Fatal(decision, err)
	}
	if _, err := s.poll(r.ID, r.Secret); err != errTooSoon {
		t.Fatal(err)
	}
	now := s.now()
	s.now = func() time.Time { return now.Add(2 * time.Second) }
	if decision, err := s.poll(r.ID, r.Secret); err != nil || decision != "pending" {
		t.Fatal(decision, err)
	}
}
