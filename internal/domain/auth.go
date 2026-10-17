package domain

import "time"

// Private verifier fields never serialize into API responses or operation receipts.
type CredentialScope struct {
	Kind       string   `json:"kind"`
	Actions    []string `json:"actions,omitempty"`
	ProjectIDs []string `json:"project_ids,omitempty"`
	DraftIDs   []string `json:"draft_ids,omitempty"`
	VersionIDs []string `json:"version_ids,omitempty"`
}

type Session struct {
	Revision    int64           `json:"revision"`
	ETag        string          `json:"etag"`
	Scope       CredentialScope `json:"scope"`
	ID          string          `json:"session_id"`
	PrincipalID string          `json:"principal_id"`
	LibraryID   string          `json:"library_id"`
	DeviceLabel string          `json:"device_label"`
	Origin      string          `json:"origin"`
	CreatedAt   time.Time       `json:"created_at"`
	LastSeenAt  time.Time       `json:"last_seen_at"`
	ExpiresAt   *time.Time      `json:"expires_at"`
	State       string          `json:"state"`
	TokenHash   string          `json:"-"`
	AuthEpoch   string          `json:"-"`
	Revoked     bool            `json:"-"`
}
type Pairing struct {
	ID          string    `json:"pairing_id"`
	DeviceLabel string    `json:"device_label"`
	Origin      string    `json:"origin"`
	CreatedAt   time.Time `json:"created_at"`
	ExpiresAt   time.Time `json:"expires_at"`
	State       string    `json:"state"`
	Revision    int64     `json:"revision"`
	ETag        string    `json:"etag"`
	SecretHash  string    `json:"-"`
	CodeHash    string    `json:"-"`
	ClientHash  string    `json:"-"`
	AuthEpoch   string    `json:"-"`
	PollAfter   time.Time `json:"-"`
	Consumed    bool      `json:"-"`
}
type PairingCreated struct {
	Pairing
	Code          string `json:"code"`
	PollingSecret string `json:"polling_secret"`
}
type PairingPoll struct {
	State   string   `json:"state"`
	Session *Session `json:"session,omitempty"`
	Token   string   `json:"token,omitempty"`
}
type PairingDecision struct{ ID, Code, Origin, ETag, OperationID, Action string }
type AuthSettings struct {
	RememberDeviceDefault  bool   `json:"remember_device_default"`
	SessionIdleSeconds     *int64 `json:"session_idle_seconds"`
	SessionAbsoluteSeconds *int64 `json:"session_absolute_seconds"`
	PairingExpirySeconds   int64  `json:"pairing_expiry_seconds"`
}
type AuthState struct {
	LibraryID, OwnerID, AuthEpoch string
	Settings                      AuthSettings
}

// WorkflowPrincipal is the stable authorization binding, independently of any
// credential's lifetime. Issuance/UI is a future capability.
type WorkflowPrincipal struct {
	ID        string
	BindingID string
	Active    bool
	Scope     CredentialScope
	Revision  int64
}
type ScopedCredential struct {
	ID             string
	PrincipalID    string
	TokenHash      string
	AuthEpoch      string
	Scope          CredentialScope
	Permissions    map[string]int64
	ConfigRevision int64
	CreatedAt      time.Time
	ExpiresAt      *time.Time
	Revoked        bool
	Revision       int64
}
