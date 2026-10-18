package origins

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

// Frozen legacy fixture origins (G-VIEW spike 9272/9273). The product never
// allocates them and refuses them at every layer: configuration validation
// rejects intersecting pool ranges, and the reservation code independently
// rejects both new allocation on them and any loaded record claiming them.
const (
	LegacyPortFirst = 9272
	LegacyPortLast  = 9273
)

// Record states. Tombstoned identities persist forever and are never reused.
const (
	StateActive     = "active"
	StateTombstoned = "tombstoned"
)

// Record is one immutable origin ownership entry. Owner identity is the
// library; version identity is the exact immutable tuple
// (library_id, version_id, content_set_id, payload_sha256).
type Record struct {
	Port          int    `json:"port"`
	LibraryID     string `json:"library_id"`
	VersionID     string `json:"version_id"`
	ContentSetID  string `json:"content_set_id"`
	PayloadSHA256 string `json:"payload_sha256"`
	State         string `json:"state"`
	CreatedAt     string `json:"created_at"`
}

// SameTuple reports whether two records describe the same immutable version
// identity.
func (r Record) SameTuple(other Record) bool {
	return r.LibraryID == other.LibraryID && r.VersionID == other.VersionID && r.ContentSetID == other.ContentSetID && r.PayloadSHA256 == other.PayloadSHA256
}

// Origin is the canonical browser origin string of a reserved port. Origins
// are canonicalized to exactly http://127.0.0.1:<port> everywhere a
// reservation or binding is recorded or compared.
func Origin(port int) string { return fmt.Sprintf("http://127.0.0.1:%d", port) }

type ledgerFile struct {
	LedgerID  string   `json:"ledger_id"`
	CreatedAt string   `json:"created_at"`
	Records   []Record `json:"records"`
}

// Marker states of the per-user APP continuity marker.
const (
	MarkerInitializing = "initializing"
	MarkerReady        = "ready"
)

type markerFile struct {
	State      string `json:"state"`
	AppStateID string `json:"app_state_id"`
	LedgerID   string `json:"ledger_id"`
	CreatedAt  string `json:"created_at"`
}

// Pool is the configured renderer origin address range. It is an address
// space mechanism, never a content or version admission limit.
type Pool struct {
	BasePort int
	Count    int
}

func (p Pool) Contains(port int) bool { return port >= p.BasePort && port < p.BasePort+p.Count }

// LegacyReserved reports whether a port belongs to the frozen legacy fixture
// origin set the reservation code refuses independently of configuration.
func LegacyReserved(port int) bool { return port >= LegacyPortFirst && port <= LegacyPortLast }

// Typed fail-closed conditions. Every one leaves ordinary library flows
// usable; only renderer-origin allocation/serving stops.
type Error struct{ Reason string }

func (e *Error) Error() string { return "origin ledger: " + e.Reason }

const (
	ReasonPrivatePath        = "private_path"
	ReasonCorruptState       = "corrupt_state"
	ReasonLostLedger         = "lost_ledger"
	ReasonForeignState       = "foreign_state"
	ReasonInvalidLedger      = "invalid_ledger"
	ReasonRetiredIdentity    = "retired_identity"
	ReasonExhausted          = "pool_exhausted"
	ReasonUnknownPublication = "unknown_publication"
	ReasonBindingMismatch    = "binding_mismatch"
)

// Faults are test-injected crash points mirroring the repository's fault
// patterns; production leaves them nil.
type Faults struct {
	// BeforeRename fires after the ledger temp file is written and synced but
	// before the atomic rename: no reservation was published.
	BeforeRename func() error
	// AfterRename fires after the rename but before the directory fsync: the
	// reservation is UNKNOWN/unpublished for this process lifetime.
	AfterRename func() error
	// BeforeMarkerReady fires after the ledger is durably published but
	// before the marker is finalized to ready (partial-init recovery row).
	BeforeMarkerReady func() error
}

// Manager owns the ledger, marker and lock of one per-user Haeryong base.
type Manager struct {
	base   string
	faults Faults
	now    func() time.Time
	newID  func() string

	// mu serializes this process's use of the ledger: the poisoned flag is
	// read and written only inside the serialized region, so a waiter can
	// never enter the mutation after another call poisoned the process.
	mu       sync.Mutex
	poisoned bool
}

// NewManager binds a manager to a per-user Haeryong base directory. Product
// code passes the resolved account base; focused checks inject a scratch
// directory. Constructing a manager writes nothing; first use may create the
// idempotent private lock/state directories, which publish no reservation.
func NewManager(base string, faults Faults) *Manager {
	return &Manager{base: base, faults: faults, now: func() time.Time { return time.Now().UTC() }, newID: func() string {
		raw := make([]byte, 16)
		if _, err := rand.Read(raw); err != nil {
			panic(err)
		}
		return hex.EncodeToString(raw[:4]) + "-" + hex.EncodeToString(raw[4:6]) + "-" + hex.EncodeToString(raw[6:8]) + "-" + hex.EncodeToString(raw[8:10]) + "-" + hex.EncodeToString(raw[10:])
	}}
}

func (m *Manager) markerPath() string { return filepath.Join(m.base, "app-state.json") }
func (m *Manager) stateDir() string   { return filepath.Join(m.base, "origin-state") }
func (m *Manager) ledgerPath() string { return filepath.Join(m.stateDir(), "ledger.json") }
func (m *Manager) lockPath() string   { return filepath.Join(m.stateDir(), "ledger.lock") }

func (m *Manager) isManaged(dir string) bool { return dir == m.base || dir == m.stateDir() }

// Identity is the ledger identity recorded in per-library binding markers.
type Identity struct {
	LedgerID string `json:"ledger_id"`
	Path     string `json:"path"`
}

// Reservation requests origin ownership for one exact immutable version
// identity. exclude names additional ports that must never back a NEW
// allocation (the configured management listener and renderer_mappings
// upstreams); a sticky existing record on such a port is returned unchanged
// — origins are never reassigned to dodge a conflict.
type Reservation struct {
	LibraryID     string
	VersionID     string
	ContentSetID  string
	PayloadSHA256 string
}

func (r Reservation) record(port int, created string) Record {
	return Record{Port: port, LibraryID: r.LibraryID, VersionID: r.VersionID, ContentSetID: r.ContentSetID, PayloadSHA256: r.PayloadSHA256, State: StateActive, CreatedAt: created}
}

var (
	uuidPattern   = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
	digestPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)
	intPattern    = regexp.MustCompile(`^(0|[1-9][0-9]*)$`)
)

func validID(value string) bool     { return uuidPattern.MatchString(value) }
func validDigest(value string) bool { return digestPattern.MatchString(value) }

// Reserve returns the durable record owning an origin for the exact tuple.
// An existing active record for the same tuple is reused (same-tuple retry,
// sticky across pool changes); a tombstoned one is refused forever; the same
// library/version under a differing content identity is rejected before
// anything is published. A new record takes the first free pool port, never
// a legacy fixture port, never an excluded port, and is confirmed only
// after file fsync + rename + directory fsync all succeed.
func (m *Manager) Reserve(request Reservation, pool Pool, exclude func(port int) bool) (Record, error) {
	if !validID(request.LibraryID) || !validID(request.VersionID) || !validID(request.ContentSetID) || !validDigest(request.PayloadSHA256) {
		return Record{}, &Error{Reason: ReasonInvalidLedger}
	}
	if pool.BasePort < 1024 || pool.Count < 1 || pool.BasePort+pool.Count-1 > 65535 {
		return Record{}, &Error{Reason: ReasonInvalidLedger}
	}
	var out Record
	err := m.withLock(func() error {
		ledger, _, err := m.ensureReady()
		if err != nil {
			return err
		}
		for _, existing := range ledger.Records {
			if existing.SameTuple(request.record(0, "")) {
				if existing.State != StateActive {
					return &Error{Reason: ReasonRetiredIdentity}
				}
				// Sticky ownership: the recorded origin is returned as-is,
				// including records that predate a pool or exclusion change.
				// Conflicts with the current management listener or mapping
				// upstreams surface at bind time; the ledger never
				// reassigns an origin to dodge them.
				out = existing
				return nil
			}
		}
		for _, existing := range ledger.Records {
			if existing.LibraryID == request.LibraryID && existing.VersionID == request.VersionID &&
				(existing.ContentSetID != request.ContentSetID || existing.PayloadSHA256 != request.PayloadSHA256) {
				// Immutable-identity conflict: reject before any publication.
				return &Error{Reason: ReasonInvalidLedger}
			}
		}
		taken := map[int]bool{}
		for _, existing := range ledger.Records {
			taken[existing.Port] = true
		}
		port := 0
		for candidate := pool.BasePort; candidate < pool.BasePort+pool.Count; candidate++ {
			if taken[candidate] || LegacyReserved(candidate) {
				continue
			}
			if exclude != nil && exclude(candidate) {
				continue
			}
			port = candidate
			break
		}
		if port == 0 {
			return &Error{Reason: ReasonExhausted}
		}
		updated := *ledger
		updated.Records = append(append([]Record{}, ledger.Records...), request.record(port, m.now().Format(time.RFC3339Nano)))
		if err := m.publish(&updated); err != nil {
			return err
		}
		out = updated.Records[len(updated.Records)-1]
		return nil
	})
	return out, err
}

// BindingMatch compares a per-library binding marker value with the opened
// ledger under the same whole-init lock and full current-state validation.
// It performs no initialization and distinguishes lost, corrupt and
// mismatched states without ever dereferencing an absent ledger.
func (m *Manager) BindingMatch(value []byte) error {
	var bound Identity
	if err := json.Unmarshal(value, &bound); err != nil || !validID(bound.LedgerID) || bound.Path == "" || !filepath.IsAbs(bound.Path) {
		return &Error{Reason: ReasonCorruptState}
	}
	// A marker/ledger state machine validated under the same shared lock.
	return m.withLock(func() error {
		markerBytes, markerExists, err := m.readPrivateFile(m.markerPath())
		if err != nil {
			return err
		}
		ledger, ledgerExists, err := m.loadValidated()
		if err != nil {
			return err
		}
		if !markerExists {
			return &Error{Reason: ReasonCorruptState}
		}
		marker, err := decodeMarker(markerBytes)
		if err != nil {
			return err
		}
		if !ledgerExists {
			if marker.State == MarkerReady {
				return &Error{Reason: ReasonLostLedger}
			}
			return &Error{Reason: ReasonCorruptState}
		}
		switch marker.State {
		case MarkerReady:
			if ledger.LedgerID != marker.LedgerID {
				return &Error{Reason: ReasonLostLedger}
			}
		case MarkerInitializing:
			// A partially initialized state is not a valid binding target;
			// only Reserve's recovery path completes initialization.
			return &Error{Reason: ReasonCorruptState}
		default:
			return &Error{Reason: ReasonCorruptState}
		}
		if bound.LedgerID != ledger.LedgerID || bound.Path != m.ledgerPath() {
			return &Error{Reason: ReasonBindingMismatch}
		}
		return nil
	})
}

// Identity returns the validated ledger identity under the shared lock. It
// performs no initialization and fails closed on any incomplete state.
func (m *Manager) Identity() (Identity, error) {
	var identity Identity
	err := m.withLock(func() error {
		markerBytes, markerExists, err := m.readPrivateFile(m.markerPath())
		if err != nil {
			return err
		}
		ledger, ledgerExists, err := m.loadValidated()
		if err != nil {
			return err
		}
		if !markerExists || !ledgerExists {
			return &Error{Reason: ReasonCorruptState}
		}
		marker, err := decodeMarker(markerBytes)
		if err != nil {
			return err
		}
		if marker.State != MarkerReady {
			return &Error{Reason: ReasonCorruptState}
		}
		if ledger.LedgerID != marker.LedgerID {
			return &Error{Reason: ReasonLostLedger}
		}
		identity = Identity{LedgerID: ledger.LedgerID, Path: m.ledgerPath()}
		return nil
	})
	return identity, err
}

// withLock holds the one app-wide OS lock (flock on ledger.lock) across
// absence checks, all APP initialization transitions, recovery of partial
// initialization and every reservation mutation. A process-wide mutex
// serializes this manager first: the unknown-publication poison state is
// only read inside the serialized region, so no concurrent caller of this
// process can slip past a poisoned reservation decision. Creation of the
// lock/state directories is idempotent, private and publishes no
// reservation; crashed owned temp files are reclaimed under the same lock.
func (m *Manager) withLock(fn func() error) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.poisoned {
		return &Error{Reason: ReasonUnknownPublication}
	}
	if err := m.ensureStateDir(); err != nil {
		return err
	}
	lock, err := m.openLock()
	if err != nil {
		return err
	}
	defer lock.Close()
	if err := flockExclusive(lock); err != nil {
		return &Error{Reason: ReasonPrivatePath}
	}
	defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
	if m.poisoned {
		return &Error{Reason: ReasonUnknownPublication}
	}
	m.cleanupTemps()
	return fn()
}

// openLock opens the lock file as a private regular file of the current
// uid, refusing symlinks and foreign/permissive state both before and after
// the open itself.
func (m *Manager) openLock() (*os.File, error) {
	if info, err := os.Lstat(m.lockPath()); err == nil {
		if err := validatePrivateFile(info); err != nil {
			return nil, err
		}
	} else if !os.IsNotExist(err) {
		return nil, &Error{Reason: ReasonPrivatePath}
	}
	lock, err := os.OpenFile(m.lockPath(), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, &Error{Reason: ReasonPrivatePath}
	}
	info, err := lock.Stat()
	if err != nil {
		lock.Close()
		return nil, &Error{Reason: ReasonPrivatePath}
	}
	if err := validatePrivateFile(info); err != nil {
		lock.Close()
		return nil, err
	}
	return lock, nil
}

// cleanupTemps removes only this app's crashed publication temp files in the
// managed directories, matched by the exact private naming pattern, never
// any other file.
func (m *Manager) cleanupTemps() {
	for dir, prefix := range map[string]string{m.stateDir(): ".ledger.", m.base: ".app-state."} {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			name := entry.Name()
			if len(name) <= len(prefix)+4 || !strings.HasPrefix(name, prefix) || !strings.HasSuffix(name, ".tmp") {
				continue
			}
			info, err := entry.Info()
			if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
				continue
			}
			if stat, ok := info.Sys().(*syscall.Stat_t); ok && int(stat.Uid) != os.Getuid() {
				continue
			}
			os.Remove(filepath.Join(dir, name))
		}
	}
}

// ensureReady resolves the marker/ledger state machine to one durable ready
// state and returns the validated ledger plus current marker. Genuine first
// use is exactly no marker and no ledger; everything else either recovers
// idempotently or fails closed.
func (m *Manager) ensureReady() (*ledgerFile, *markerFile, error) {
	markerBytes, markerExists, err := m.readPrivateFile(m.markerPath())
	if err != nil {
		return nil, nil, err
	}
	ledger, ledgerExists, err := m.loadValidated()
	if err != nil {
		return nil, nil, err
	}
	if !markerExists {
		if ledgerExists {
			// A ledger with no marker at all is foreign/unknown state.
			return nil, nil, &Error{Reason: ReasonForeignState}
		}
		return m.initialize()
	}
	marker, err := decodeMarker(markerBytes)
	if err != nil {
		return nil, nil, err
	}
	switch marker.State {
	case MarkerInitializing:
		if !ledgerExists {
			// The app provably never published a reservation (publication
			// requires ready): restart initialization with a fresh ledger id.
			return m.initialize()
		}
		if ledger.LedgerID != marker.LedgerID {
			return nil, nil, &Error{Reason: ReasonCorruptState}
		}
		// Idempotent finalization with the same ids.
		if err := m.writeMarker(marker, MarkerReady); err != nil {
			return nil, nil, err
		}
		marker.State = MarkerReady
		return ledger, marker, nil
	case MarkerReady:
		if !ledgerExists || ledger.LedgerID != marker.LedgerID {
			return nil, nil, &Error{Reason: ReasonLostLedger}
		}
		return ledger, marker, nil
	default:
		return nil, nil, &Error{Reason: ReasonCorruptState}
	}
}

// initialize performs the whole initialization as one locked mutation:
// marker initializing → durable ledger publication → marker ready.
func (m *Manager) initialize() (*ledgerFile, *markerFile, error) {
	if err := m.ensureManagedDir(m.base); err != nil {
		return nil, nil, err
	}
	if err := m.ensureManagedDir(m.stateDir()); err != nil {
		return nil, nil, err
	}
	marker := &markerFile{State: MarkerInitializing, AppStateID: m.newID(), LedgerID: m.newID(), CreatedAt: m.now().Format(time.RFC3339Nano)}
	if err := m.writeMarker(marker, MarkerInitializing); err != nil {
		return nil, nil, err
	}
	ledger := &ledgerFile{LedgerID: marker.LedgerID, CreatedAt: marker.CreatedAt, Records: []Record{}}
	if err := m.publish(ledger); err != nil {
		return nil, nil, err
	}
	if m.faults.BeforeMarkerReady != nil {
		if err := m.faults.BeforeMarkerReady(); err != nil {
			return nil, nil, err
		}
	}
	if err := m.writeMarker(marker, MarkerReady); err != nil {
		return nil, nil, err
	}
	marker.State = MarkerReady
	return ledger, marker, nil
}

// loadValidated reads and strictly validates the ledger; corrupt,
// unparseable, unknown-shape or structurally invalid content — including
// records claiming frozen legacy ports — always fails closed regardless of
// markers.
func (m *Manager) loadValidated() (*ledgerFile, bool, error) {
	raw, exists, err := m.readPrivateFile(m.ledgerPath())
	if err != nil || !exists {
		return nil, false, err
	}
	ledger, err := decodeLedger(raw)
	if err != nil {
		return nil, true, err
	}
	return ledger, true, nil
}

func decodeLedger(raw []byte) (*ledgerFile, error) {
	fields, err := strictObject(raw, "ledger_id", "created_at", "records")
	if err != nil {
		return nil, &Error{Reason: ReasonCorruptState}
	}
	ledger := &ledgerFile{Records: []Record{}}
	var decodeErr error
	if ledger.LedgerID, decodeErr = strictUUID(fields["ledger_id"]); decodeErr != nil {
		return nil, &Error{Reason: ReasonCorruptState}
	}
	if ledger.CreatedAt, decodeErr = strictTimestamp(fields["created_at"]); decodeErr != nil {
		return nil, &Error{Reason: ReasonCorruptState}
	}
	var entries []json.RawMessage
	if err := json.Unmarshal(fields["records"], &entries); err != nil || entries == nil {
		return nil, &Error{Reason: ReasonCorruptState}
	}
	ports := map[int]bool{}
	tuples := map[string]bool{}
	versions := map[string]string{}
	for _, entry := range entries {
		recordFields, err := strictObject(entry, "port", "library_id", "version_id", "content_set_id", "payload_sha256", "state", "created_at")
		if err != nil {
			return nil, &Error{Reason: ReasonCorruptState}
		}
		port, err := strictPort(recordFields["port"])
		if err != nil || LegacyReserved(port) {
			return nil, &Error{Reason: ReasonInvalidLedger}
		}
		record := Record{Port: port}
		if record.LibraryID, err = strictUUID(recordFields["library_id"]); err != nil {
			return nil, &Error{Reason: ReasonInvalidLedger}
		}
		if record.VersionID, err = strictUUID(recordFields["version_id"]); err != nil {
			return nil, &Error{Reason: ReasonInvalidLedger}
		}
		if record.ContentSetID, err = strictUUID(recordFields["content_set_id"]); err != nil {
			return nil, &Error{Reason: ReasonInvalidLedger}
		}
		if record.PayloadSHA256, err = strictUUID64(recordFields["payload_sha256"]); err != nil {
			return nil, &Error{Reason: ReasonInvalidLedger}
		}
		if err := json.Unmarshal(recordFields["state"], &record.State); err != nil || (record.State != StateActive && record.State != StateTombstoned) {
			return nil, &Error{Reason: ReasonInvalidLedger}
		}
		if record.CreatedAt, err = strictTimestamp(recordFields["created_at"]); err != nil {
			return nil, &Error{Reason: ReasonInvalidLedger}
		}
		if ports[record.Port] {
			return nil, &Error{Reason: ReasonInvalidLedger}
		}
		ports[record.Port] = true
		tuple := record.LibraryID + "\x00" + record.VersionID + "\x00" + record.ContentSetID + "\x00" + record.PayloadSHA256
		if tuples[tuple] {
			return nil, &Error{Reason: ReasonInvalidLedger}
		}
		tuples[tuple] = true
		versionKey := record.LibraryID + "\x00" + record.VersionID
		digestKey := record.ContentSetID + "\x00" + record.PayloadSHA256
		if previous, seen := versions[versionKey]; seen && previous != digestKey {
			// Same version identity with a differing content digest.
			return nil, &Error{Reason: ReasonInvalidLedger}
		}
		versions[versionKey] = digestKey
		ledger.Records = append(ledger.Records, record)
	}
	return ledger, nil
}

func decodeMarker(raw []byte) (*markerFile, error) {
	fields, err := strictObject(raw, "state", "app_state_id", "ledger_id", "created_at")
	if err != nil {
		return nil, &Error{Reason: ReasonCorruptState}
	}
	marker := &markerFile{}
	if err := json.Unmarshal(fields["state"], &marker.State); err != nil || (marker.State != MarkerInitializing && marker.State != MarkerReady) {
		return nil, &Error{Reason: ReasonCorruptState}
	}
	var decodeErr error
	if marker.AppStateID, decodeErr = strictUUID(fields["app_state_id"]); decodeErr != nil {
		return nil, &Error{Reason: ReasonCorruptState}
	}
	if marker.LedgerID, decodeErr = strictUUID(fields["ledger_id"]); decodeErr != nil {
		return nil, &Error{Reason: ReasonCorruptState}
	}
	if marker.CreatedAt, decodeErr = strictTimestamp(fields["created_at"]); decodeErr != nil {
		return nil, &Error{Reason: ReasonCorruptState}
	}
	return marker, nil
}

// strictObject decodes one closed JSON object with duplicate-key detection
// and an exact key-set requirement; unknown or missing fields fail.
func strictObject(raw []byte, keys ...string) (map[string]json.RawMessage, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	open, err := decoder.Token()
	if err != nil || open != json.Delim('{') {
		return nil, errors.New("object required")
	}
	wanted := map[string]bool{}
	for _, key := range keys {
		wanted[key] = true
	}
	out := map[string]json.RawMessage{}
	for decoder.More() {
		keyToken, err := decoder.Token()
		if err != nil {
			return nil, err
		}
		key, ok := keyToken.(string)
		if !ok || !wanted[key] {
			return nil, errors.New("unknown field")
		}
		if _, exists := out[key]; exists {
			return nil, errors.New("duplicate field")
		}
		var value json.RawMessage
		if err := decoder.Decode(&value); err != nil {
			return nil, err
		}
		out[key] = value
	}
	if _, err := decoder.Token(); err != nil {
		return nil, err
	}
	if _, err := decoder.Token(); err != io.EOF {
		return nil, errors.New("trailing content")
	}
	if len(out) != len(keys) {
		return nil, errors.New("missing field")
	}
	return out, nil
}

func strictUUID(raw json.RawMessage) (string, error) {
	var value string
	if err := json.Unmarshal(raw, &value); err != nil || !validID(value) {
		return "", errors.New("canonical id required")
	}
	return value, nil
}

func strictUUID64(raw json.RawMessage) (string, error) {
	var value string
	if err := json.Unmarshal(raw, &value); err != nil || !validDigest(value) {
		return "", errors.New("canonical digest required")
	}
	return value, nil
}

func strictTimestamp(raw json.RawMessage) (string, error) {
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return "", errors.New("timestamp required")
	}
	if _, err := time.Parse(time.RFC3339Nano, value); err != nil {
		return "", errors.New("timestamp required")
	}
	return value, nil
}

// strictPort accepts only an exact nonnegative decimal integer lexeme.
func strictPort(raw json.RawMessage) (int, error) {
	var number json.Number
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&number); err != nil || !intPattern.MatchString(number.String()) {
		return 0, errors.New("integer port required")
	}
	port, err := strconv.Atoi(number.String())
	if err != nil || port < 1024 || port > 65535 {
		return 0, errors.New("port out of range")
	}
	return port, nil
}

// publish writes the ledger durably: temp file (0600, same directory),
// fsync, atomic rename, directory fsync. A reservation is confirmed only
// after all three succeed; a fault after rename but before the directory
// fsync leaves it UNKNOWN for this process, which then makes no further
// free/reuse decision until restart.
func (m *Manager) publish(ledger *ledgerFile) error {
	data, err := json.Marshal(ledger)
	if err != nil {
		return err
	}
	if err := m.ensureManagedDir(m.stateDir()); err != nil {
		return err
	}
	temp := filepath.Join(m.stateDir(), ".ledger."+m.newID()+".tmp")
	if err := writePrivateFile(temp, data); err != nil {
		return err
	}
	if m.faults.BeforeRename != nil {
		if err := m.faults.BeforeRename(); err != nil {
			os.Remove(temp)
			return err
		}
	}
	if err := os.Rename(temp, m.ledgerPath()); err != nil {
		os.Remove(temp)
		return &Error{Reason: ReasonPrivatePath}
	}
	if m.faults.AfterRename != nil {
		if err := m.faults.AfterRename(); err != nil {
			m.poisoned = true
			return &Error{Reason: ReasonUnknownPublication}
		}
	}
	if err := fsyncDir(m.stateDir()); err != nil {
		m.poisoned = true
		return &Error{Reason: ReasonUnknownPublication}
	}
	return nil
}

func (m *Manager) writeMarker(marker *markerFile, state string) error {
	value := markerFile{State: state, AppStateID: marker.AppStateID, LedgerID: marker.LedgerID, CreatedAt: marker.CreatedAt}
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	if err := m.ensureManagedDir(m.base); err != nil {
		return err
	}
	temp := filepath.Join(m.base, ".app-state."+m.newID()+".tmp")
	if err := writePrivateFile(temp, data); err != nil {
		return err
	}
	if err := os.Rename(temp, m.markerPath()); err != nil {
		os.Remove(temp)
		return &Error{Reason: ReasonPrivatePath}
	}
	if err := fsyncDir(m.base); err != nil {
		m.poisoned = true
		return &Error{Reason: ReasonUnknownPublication}
	}
	return nil
}

// readPrivateFile reads a state file enforcing the private-path discipline:
// no symlink, current-uid ownership and no group/other permission bits.
func (m *Manager) readPrivateFile(path string) ([]byte, bool, error) {
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, &Error{Reason: ReasonPrivatePath}
	}
	if err := validatePrivateFile(info); err != nil {
		return nil, true, err
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, true, &Error{Reason: ReasonPrivatePath}
	}
	return raw, true, nil
}

func validatePrivateFile(info os.FileInfo) error {
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return &Error{Reason: ReasonPrivatePath}
	}
	if stat, ok := info.Sys().(*syscall.Stat_t); ok && int(stat.Uid) != os.Getuid() {
		return &Error{Reason: ReasonPrivatePath}
	}
	if info.Mode().Perm()&0o077 != 0 {
		return &Error{Reason: ReasonPrivatePath}
	}
	return nil
}

// ensureStateDir creates the origin-state directory (and the Haeryong base)
// when absent without touching anything above the account-derived base.
func (m *Manager) ensureStateDir() error {
	if err := m.ensureManagedDir(m.base); err != nil {
		return err
	}
	return m.ensureManagedDir(m.stateDir())
}

// ensureManagedDir ensures the Haeryong base or origin-state directory
// exists. Managed directories are held to the private discipline: created
// 0700, owned by the current uid, never a symlink, no group/other bits.
// Existing ancestors above the managed base are validated without being
// modified: they must be real directories, not symlinks and not group- or
// world-writable (a macOS "Application Support" at 0755 or a linux
// ~/.local/share at 0755 is legitimate). Nothing is ever chmod-ed; missing
// non-managed parents are created with the standard 0755 user-directory
// mode, and every creation fsyncs its parent for durability. A concurrent
// creator winning the race converges by revalidating the now-existing
// directory instead of failing.
func (m *Manager) ensureManagedDir(dir string) error {
	var missing []string
	cur := dir
	for {
		info, err := os.Lstat(cur)
		if err == nil {
			if err := m.validateExisting(cur, info); err != nil {
				return err
			}
			break
		}
		if !os.IsNotExist(err) {
			return &Error{Reason: ReasonPrivatePath}
		}
		missing = append(missing, cur)
		parent := filepath.Dir(cur)
		if parent == cur {
			return &Error{Reason: ReasonPrivatePath}
		}
		cur = parent
	}
	for i := len(missing) - 1; i >= 0; i-- {
		target := missing[i]
		mode := os.FileMode(0o755)
		if m.isManaged(target) {
			mode = 0o700
		}
		if err := os.Mkdir(target, mode); err != nil {
			if !errors.Is(err, os.ErrExist) {
				return &Error{Reason: ReasonPrivatePath}
			}
			// A concurrent creator won the race: converge by validating it.
		}
		info, err := os.Lstat(target)
		if err != nil {
			return &Error{Reason: ReasonPrivatePath}
		}
		if err := m.validateExisting(target, info); err != nil {
			return err
		}
		if err := fsyncDir(filepath.Dir(target)); err != nil {
			return &Error{Reason: ReasonUnknownPublication}
		}
	}
	return nil
}

func (m *Manager) validateExisting(dir string, info os.FileInfo) error {
	if m.isManaged(dir) {
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm() != 0o700 {
			return &Error{Reason: ReasonPrivatePath}
		}
		if stat, ok := info.Sys().(*syscall.Stat_t); ok && int(stat.Uid) != os.Getuid() {
			return &Error{Reason: ReasonPrivatePath}
		}
		return nil
	}
	// An existing user/system ancestor: a real directory that is not a
	// symlink and not group- or world-writable. Ownership stays with the
	// OS/user layout; nothing here is ever chmod-ed.
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return &Error{Reason: ReasonPrivatePath}
	}
	if info.Mode().Perm()&0o022 != 0 {
		return &Error{Reason: ReasonPrivatePath}
	}
	return nil
}

func writePrivateFile(path string, data []byte) error {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_EXCL, 0o600)
	if err != nil {
		return &Error{Reason: ReasonPrivatePath}
	}
	_, err = file.Write(data)
	if err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err != nil || closeErr != nil {
		os.Remove(path)
		return &Error{Reason: ReasonPrivatePath}
	}
	return nil
}

func fsyncDir(dir string) error {
	handle, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer handle.Close()
	return handle.Sync()
}

func flockExclusive(file *os.File) error {
	for {
		err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX)
		if err != syscall.EINTR {
			return err
		}
	}
}
