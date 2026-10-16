// Package session owns credential generation, verifier separation and expiry.
// It knows no transport, SQL, filesystem or trusted-local bootstrap credentials.
package session

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"

	"haeryong.local/haeryong/internal/domain"
)

var (
	ErrRequired    = errors.New("AUTH_REQUIRED")
	ErrExpired     = errors.New("SESSION_EXPIRED")
	ErrRevoked     = errors.New("SESSION_REVOKED")
	ErrInvalid     = errors.New("INVALID_REQUEST")
	ErrLimited     = errors.New("RATE_LIMITED")
	ErrUnavailable = errors.New("SERVICE_RECOVERING")
	ErrOrigin      = errors.New("ORIGIN_REJECTED")
	ErrMissing     = errors.New("PRECONDITION_REQUIRED")
)

func Secret() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
func Code() (string, error) {
	b := make([]byte, 4)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
func Verifier(kind, raw string) string {
	h := sha256.Sum256([]byte(kind + "\x00" + raw))
	return hex.EncodeToString(h[:])
}
func ValidSecret(raw string) bool {
	b, err := hex.DecodeString(raw)
	return err == nil && len(b) == 32 && hex.EncodeToString(b) == raw
}
func Equal(a, b string) bool { return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1 }
func Check(s domain.Session, state domain.AuthState, now time.Time) (domain.Session, error) {
	s.Scope = domain.CredentialScope{Kind: "owner"}
	s.State = "active"
	s.ExpiresAt = nil
	for _, p := range []struct {
		base  time.Time
		limit *int64
	}{{s.CreatedAt, state.Settings.SessionAbsoluteSeconds}, {s.LastSeenAt, state.Settings.SessionIdleSeconds}} {
		if p.limit != nil {
			expiry := time.Unix(p.base.Unix()+*p.limit, int64(p.base.Nanosecond())).UTC()
			if s.ExpiresAt == nil || expiry.Before(*s.ExpiresAt) {
				s.ExpiresAt = &expiry
			}
		}
	}
	var authorityError error
	if s.Revoked || s.AuthEpoch != state.AuthEpoch || s.LibraryID != state.LibraryID || s.PrincipalID != state.OwnerID {
		s.State = "revoked"
		authorityError = ErrRevoked
	} else if s.ExpiresAt != nil && !now.Before(*s.ExpiresAt) {
		s.State = "expired"
		authorityError = ErrExpired
	}
	// ETag covers the complete current public projection, including derived state,
	// without claiming that a clock/policy projection mutates the stored revision.
	s.ETag = ""
	body, err := json.Marshal(s)
	if err != nil {
		return s, ErrUnavailable
	}
	h := sha256.Sum256(body)
	s.ETag = "\"session-" + s.ID + "-" + hex.EncodeToString(h[:]) + "\""
	return s, authorityError
}
