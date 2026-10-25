// Package service owns the local profile and single-writer lifecycle. It has no
// desktop, database driver, HTTP handler or provider dependency.
package service

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"haeryong.local/haeryong/internal/domain"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"syscall"
	"time"
)

const APIVersion = "local-control-v1"
const DefaultPort = 9270

var ErrLocked = errors.New("profile already has a writer")
var profileName = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)
var canonicalID = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

// Selection identifies a local profile. DataDir overrides the complete profile
// directory, rather than a base directory. VolumeRoot binds external storage to
// an actual mount point; macOS /Volumes paths are bound automatically.
type Selection struct {
	Profile    string
	DataDir    string
	VolumeRoot string
}

type Profile struct {
	Format       int    `json:"format"`
	StorageState string `json:"storage_state,omitempty"`
	LibraryID    string `json:"library_id"`
	ProfileID    string `json:"profile_id"`
	Name         string `json:"profile"`
	DataDir      string `json:"data_dir"`
	VolumeRoot   string `json:"volume_root,omitempty"`
	ControlKey   string `json:"control_key"`
}

type RestoreSwitch struct {
	ID, Phase, Input, Staging, Rollback, NewKey string
	OldProfile                                  Profile
	Library                                     domain.Library
	OldLibrary                                  domain.Library
	FailureAt                                   string
}

type LockedProfile struct {
	Profile Profile
	lock    *os.File
}

func (p *LockedProfile) Close() error { return p.lock.Close() }

func NewID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	s := hex.EncodeToString(b)
	return s[:8] + "-" + s[8:12] + "-" + s[12:16] + "-" + s[16:20] + "-" + s[20:], nil
}

func randomKey() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func (s Selection) path() (string, error) {
	if !profileName.MatchString(s.Profile) {
		return "", errors.New("invalid profile name")
	}
	if s.DataDir != "" {
		return filepath.Abs(s.DataDir)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	switch runtime.GOOS {
	case "darwin":
		return filepath.Join(home, "Library", "Application Support", "Haeryong", "profiles", s.Profile), nil
	case "linux":
		base := os.Getenv("XDG_DATA_HOME")
		if base == "" {
			base = filepath.Join(home, ".local", "share")
		}
		if !filepath.IsAbs(base) {
			return "", errors.New("XDG_DATA_HOME must be absolute")
		}
		return filepath.Join(base, "haeryong", "profiles", s.Profile), nil
	default:
		return "", errors.New("service platform unsupported")
	}
}

func volumeFor(path, explicit string) (string, error) {
	root := explicit
	if root == "" && runtime.GOOS == "darwin" && strings.HasPrefix(path, "/Volumes/") {
		parts := strings.Split(strings.TrimPrefix(path, "/Volumes/"), string(os.PathSeparator))
		root = filepath.Join("/Volumes", parts[0])
	}
	if root == "" {
		return "", nil
	}
	root, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return "", fmt.Errorf("configured volume unavailable: %w", err)
	}
	if !within(root, path) {
		return "", errors.New("data directory is outside volume root")
	}
	if err := requireMount(root); err != nil {
		return "", err
	}
	return root, nil
}

func within(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator))
}

// Initialize creates only an explicitly selected library. A custom directory's
// parent must exist, so a missing mount hierarchy is never recreated implicitly.
func Initialize(s Selection) (Profile, error) {
	path, err := s.path()
	if err != nil {
		return Profile{}, err
	}
	if _, err := volumeFor(path, s.VolumeRoot); err != nil {
		return Profile{}, err
	}
	if s.DataDir == "" {
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			return Profile{}, err
		}
	}
	parent, err := filepath.EvalSymlinks(filepath.Dir(path))
	if err != nil {
		return Profile{}, fmt.Errorf("data parent unavailable (not created): %w", err)
	}
	path = filepath.Join(parent, filepath.Base(path))
	volume, err := volumeFor(path, s.VolumeRoot)
	if err != nil {
		return Profile{}, err
	}
	// Existing empty directories are allowed, but an initialized identity is not.
	if err := os.Mkdir(path, 0700); err != nil && !errors.Is(err, os.ErrExist) {
		return Profile{}, err
	}
	path, err = filepath.EvalSymlinks(path)
	if err != nil {
		return Profile{}, err
	}
	if err := privateDir(path); err != nil {
		return Profile{}, err
	}
	volume, err = volumeFor(path, s.VolumeRoot)
	if err != nil {
		return Profile{}, err
	}
	lock, err := lockProfile(path)
	if err != nil {
		return Profile{}, err
	}
	defer lock.Close()
	if _, err := os.Lstat(filepath.Join(path, "profile.json")); !errors.Is(err, os.ErrNotExist) {
		return Profile{}, errors.New("profile identity already exists or cannot be inspected; initialization refused")
	}
	entries, err := os.ReadDir(path)
	if err != nil {
		return Profile{}, err
	}
	for _, entry := range entries {
		if entry.Name() != "writer.lock" {
			return Profile{}, errors.New("initialization requires an empty profile directory")
		}
	}
	library, err := NewID()
	if err != nil {
		return Profile{}, err
	}
	id, err := NewID()
	if err != nil {
		return Profile{}, err
	}
	key, err := randomKey()
	if err != nil {
		return Profile{}, err
	}
	p := Profile{Format: 2, StorageState: "pending", LibraryID: library, ProfileID: id, Name: s.Profile, DataDir: path, VolumeRoot: volume, ControlKey: key}
	for _, name := range []string{"runtime", "storage"} {
		if err := os.Mkdir(filepath.Join(path, name), 0700); err != nil {
			return Profile{}, err
		}
	}
	if err := writeJSON(path, "profile.json", p); err != nil {
		return Profile{}, err
	}
	if err := syncDir(filepath.Dir(path)); err != nil {
		return Profile{}, err
	}
	return p, nil
}

// Load is read-only and never initializes missing paths or rewrites credentials.
func Load(s Selection) (Profile, error) { return loadProfile(s, false) }
func loadProfile(s Selection, recoverSwitch bool) (Profile, error) {
	path, err := s.path()
	if err != nil {
		return Profile{}, err
	}
	if _, err := volumeFor(path, s.VolumeRoot); err != nil {
		return Profile{}, err
	}
	path, err = filepath.EvalSymlinks(path)
	if err != nil {
		return Profile{}, fmt.Errorf("profile unavailable; explicit init required: %w", err)
	}
	if err := privateDir(path); err != nil {
		return Profile{}, err
	}
	var p Profile
	if err := readJSON(filepath.Join(path, "profile.json"), &p); err != nil {
		return Profile{}, err
	}
	if ((p.Format != 1 || p.StorageState != "") && (p.Format != 2 || (p.StorageState != "pending" && p.StorageState != "ready"))) || p.Name != s.Profile || p.DataDir != path || !validID(p.LibraryID) || !validID(p.ProfileID) {
		return Profile{}, errors.New("profile identity mismatch or unsupported format")
	}
	key, err := hex.DecodeString(p.ControlKey)
	if err != nil || len(key) != 32 {
		return Profile{}, errors.New("invalid protected local-control key")
	}
	if _, err := volumeFor(path, p.VolumeRoot); err != nil {
		return Profile{}, err
	}
	for _, name := range []string{"runtime", "storage"} {
		if err := privateDir(filepath.Join(path, name)); err != nil {
			if recoverSwitch && name == "storage" && errors.Is(err, os.ErrNotExist) {
				var marker RestoreSwitch
				if e := readJSON(filepath.Join(path, "runtime", "restore-active.json"), &marker); e != nil {
					return Profile{}, e
				}
				if !validID(marker.ID) || (marker.Phase != "switching" && marker.Phase != "rolling_back" && marker.Phase != "rollback_incoming_saved") || marker.OldProfile.ProfileID != p.ProfileID || marker.OldProfile.DataDir != path || marker.Rollback != filepath.Join(path, ".restore-"+marker.ID+".prior") || marker.Staging != filepath.Join(path, ".restore-"+marker.ID+".staging") {
					return Profile{}, errors.New("invalid restore switch binding")
				}
				if e := privateDir(marker.Rollback); e != nil {
					return Profile{}, e
				}
				continue
			}
			return Profile{}, err
		}
	}
	return p, nil
}

func validID(s string) bool {
	return canonicalID.MatchString(s)
}

// OpenWriter holds the stable lock inode until all write-capable resources close.
func OpenWriter(s Selection) (*LockedProfile, error)         { return openWriter(s, false) }
func OpenRecoveryWriter(s Selection) (*LockedProfile, error) { return openWriter(s, true) }
func openWriter(s Selection, recoverSwitch bool) (*LockedProfile, error) {
	p, err := loadProfile(s, recoverSwitch)
	if err != nil {
		return nil, err
	}
	lock, err := lockProfile(p.DataDir)
	if err != nil {
		return nil, err
	}
	// Re-read under ownership before any writable resource is opened.
	current, err := loadProfile(s, recoverSwitch)
	if err != nil || current != p {
		lock.Close()
		return nil, errors.New("profile changed while acquiring writer ownership")
	}
	return &LockedProfile{Profile: p, lock: lock}, nil
}

func privateDir(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode().Perm() != 0700 || info.Sys().(*syscall.Stat_t).Uid != uint32(os.Getuid()) {
		return fmt.Errorf("profile directory must be owned by this user with mode 0700: %s", path)
	}
	return nil
}

func privateFile(path string, flags int) (*os.File, error) {
	f, err := os.OpenFile(path, flags|syscall.O_NOFOLLOW, 0600)
	if err != nil {
		return nil, err
	}
	info, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, err
	}
	stat := info.Sys().(*syscall.Stat_t)
	if !info.Mode().IsRegular() || info.Mode().Perm() != 0600 || stat.Uid != uint32(os.Getuid()) || stat.Nlink != 1 {
		f.Close()
		return nil, fmt.Errorf("profile record must be a private regular file: %s", path)
	}
	return f, nil
}

func lockProfile(path string) (*os.File, error) {
	f, err := privateFile(filepath.Join(path, "writer.lock"), os.O_RDWR|os.O_CREATE)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, ErrLocked
		}
		return nil, err
	}
	return f, nil
}

func readJSON(path string, target any) error {
	f, err := privateFile(path, os.O_RDONLY)
	if err != nil {
		return err
	}
	defer f.Close()
	decoder := json.NewDecoder(f)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); err == nil {
		return errors.New("trailing profile data")
	} else if !errors.Is(err, io.EOF) {
		return err
	}
	return nil
}

func writeJSON(dir, name string, value any) error { return writeJSONWithFault(dir, name, value, nil) }

func writeJSONWithFault(dir, name string, value any, fault func(string) error) error {
	f, err := os.CreateTemp(dir, ".record-*")
	if err != nil {
		return err
	}
	temp := f.Name()
	defer os.Remove(temp)
	if err := json.NewEncoder(f).Encode(value); err != nil {
		f.Close()
		return err
	}
	if fault != nil {
		if err := fault("profile_file_sync"); err != nil {
			f.Close()
			return err
		}
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if fault != nil {
		if err := fault("profile_rename"); err != nil {
			return err
		}
	}
	if err := os.Rename(temp, filepath.Join(dir, name)); err != nil {
		return err
	}
	if fault != nil {
		if err := fault("profile_directory_sync"); err != nil {
			return err
		}
	}
	return syncDir(dir)
}

func syncDir(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Sync()
}

// WaitStopped confirms writer release after a verified stop request. It never
// creates a lock or opens storage; the lock inode remains stable across restarts.
func WaitStopped(ctx context.Context, p Profile) error {
	file, err := privateFile(filepath.Join(p.DataDir, "writer.lock"), os.O_RDONLY)
	if err != nil {
		return err
	}
	defer file.Close()
	tick := time.NewTicker(25 * time.Millisecond)
	defer tick.Stop()
	for {
		if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err == nil {
			return nil
		} else if !errors.Is(err, syscall.EWOULDBLOCK) {
			return err
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("stop requested but writer release not confirmed: %w", ctx.Err())
		case <-tick.C:
		}
	}
}

// PrepareStorageBootstrap upgrades only the known empty legacy profile. The
// marker lives outside storage, so loss of storage never becomes first-run init.
func (p *LockedProfile) PrepareStorageBootstrap(fault func(string) error) error {
	if p.Profile.Format == 2 {
		return nil
	}
	entries, err := os.ReadDir(filepath.Join(p.Profile.DataDir, "storage"))
	if err != nil {
		return err
	}
	if len(entries) != 0 {
		return errors.New("legacy profile has unexpected storage; recovery required")
	}
	next := p.Profile
	next.Format = 2
	next.StorageState = "pending"
	if err := writeJSONWithFault(next.DataDir, "profile.json", next, fault); err != nil {
		return err
	}
	p.Profile = next
	return nil
}
func (p *LockedProfile) MarkStorageReady(fault func(string) error) error {
	if p.Profile.StorageState == "ready" {
		return nil
	}
	next := p.Profile
	next.Format = 2
	next.StorageState = "ready"
	if err := writeJSONWithFault(next.DataDir, "profile.json", next, fault); err != nil {
		return err
	}
	p.Profile = next
	return nil
}

// RestoreIdentity is only called by the exclusive offline recovery owner after
// its durable switch journal exists. The stable lock inode is never replaced.
func (p *LockedProfile) RestoreIdentity(library, key string) error {
	if !validID(library) {
		return errors.New("invalid restored library identity")
	}
	if raw, err := hex.DecodeString(key); err != nil || len(raw) != 32 {
		return errors.New("invalid restored control material")
	}
	next := p.Profile
	next.LibraryID = library
	next.ControlKey = key
	next.Format = 2
	next.StorageState = "ready"
	if err := writeJSON(next.DataDir, "profile.json", next); err != nil {
		return err
	}
	p.Profile = next
	return nil
}

func (p *LockedProfile) RestorePriorProfile(prior Profile) error {
	if prior.DataDir != p.Profile.DataDir || prior.ProfileID != p.Profile.ProfileID {
		return errors.New("restore rollback profile mismatch")
	}
	if err := writeJSON(prior.DataDir, "profile.json", prior); err != nil {
		return err
	}
	p.Profile = prior
	return nil
}

func (p *LockedProfile) CheckRecoverySwitch() error {
	var state RestoreSwitch
	err := readJSON(filepath.Join(p.Profile.DataDir, "runtime", "restore-active.json"), &state)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if state.Phase != "complete" && state.Phase != "rolled_back" {
		return errors.New("protected restore incomplete; use local restore apply before normal startup")
	}
	return nil
}
