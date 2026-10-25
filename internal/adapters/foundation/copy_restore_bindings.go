package foundation

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"

	"haeryong.local/haeryong/internal/adapters/sqlite"
	"haeryong.local/haeryong/internal/application/service"
	"haeryong.local/haeryong/internal/domain"
)

// All restore identities live outside replaceable storage. Preflight the entire
// request before publishing any optional alias; the protected profile lock owns
// both namespace admission and record publication.
func checkRestoreBindings(ctx context.Context, p service.Profile, id string, aliases []string) error {
	if _, e := restoreExternalBindings(p, id, aliases); e != nil {
		return e
	}
	return checkRestoreDatabases(ctx, p, id, append([]string{id}, aliases...))
}

// External bindings outlive replacement datasets. Return only aliases which
// would establish a new binding; immutable replay is not current-DB admission.
func restoreExternalBindings(p service.Profile, id string, aliases []string) ([]string, error) {
	root := filepath.Join(p.DataDir, "runtime")
	var fresh []string
	for _, key := range append([]string{id}, aliases...) {
		if key == "" {
			continue
		}
		if !copyUUID.MatchString(key) {
			return nil, domain.ErrInvalidTarget
		}
		raw, e := os.ReadFile(filepath.Join(root, "restore-operation-"+key+".json"))
		if e == nil {
			var bound string
			if json.Unmarshal(raw, &bound) != nil || bound != id {
				return nil, domain.ErrConflict
			}
		} else if !errors.Is(e, os.ErrNotExist) {
			return nil, e
		} else if key != id {
			fresh = append(fresh, key)
		}
		if key == id {
			continue
		}
		for _, kind := range []string{"plan", "result"} {
			_, e = os.Lstat(filepath.Join(root, "restore-"+kind+"-"+key+".json"))
			if e == nil {
				return nil, domain.ErrConflict
			}
			if !errors.Is(e, os.ErrNotExist) {
				return nil, e
			}
		}
		active, e := readRestoreSwitch(p)
		if e != nil {
			return nil, e
		}
		if active.ID == key {
			return nil, domain.ErrConflict
		}
	}
	return fresh, nil
}

func readRestoreSwitch(p service.Profile) (restoreSwitch, error) {
	var active restoreSwitch
	raw, e := os.ReadFile(filepath.Join(p.DataDir, "runtime", "restore-active.json"))
	if errors.Is(e, os.ErrNotExist) {
		return active, nil
	}
	if e != nil {
		return active, e
	}
	if json.Unmarshal(raw, &active) != nil || !copyUUID.MatchString(active.ID) {
		return active, domain.ErrContentIntegrity
	}
	return active, nil
}

func ownedRestorePaths(p service.Profile, active restoreSwitch) error {
	if active.OldProfile.ProfileID != p.ProfileID || active.OldProfile.DataDir != p.DataDir ||
		active.Staging != filepath.Join(p.DataDir, ".restore-"+active.ID+".staging") ||
		active.Rollback != filepath.Join(p.DataDir, ".restore-"+active.ID+".prior") {
		return domain.ErrContentIntegrity
	}
	if p != active.OldProfile {
		expected := active.OldProfile
		expected.LibraryID, expected.ControlKey = active.Library.ID, active.NewKey
		expected.Format, expected.StorageState = 2, "ready"
		if active.Library.ID == "" || p != expected {
			return domain.ErrContentIntegrity
		}
	}
	return nil
}

// Inspect logical roles, not just whichever database currently occupies storage.
// Every available owned side retains its own owner-scoped receipt namespace.
// Missing history never establishes non-dispatch and no old directories are scanned.
func checkRestoreDatabases(ctx context.Context, p service.Profile, id string, keys []string) error {
	active, e := readRestoreSwitch(p)
	if e != nil {
		return e
	}
	current := filepath.Join(p.DataDir, "storage", "library.sqlite")
	if active.ID == "" {
		// Never open a missing pending database as a substitute library.
		if _, err := os.Stat(current); errors.Is(err, os.ErrNotExist) && p.StorageState == "pending" {
			return nil
		}
		lib, e := sqlite.CopyLibrary(ctx, current)
		if e != nil {
			return e
		}
		if lib.ID != p.LibraryID || lib.ProfileID != p.ProfileID {
			return domain.ErrContentIntegrity
		}
		return sqlite.CheckRestoreOperationBindings(ctx, current, id, keys)
	}
	if e = ownedRestorePaths(p, active); e != nil {
		return e
	}
	type side struct {
		path      string
		libraries []domain.Library
	}
	prior, incoming := active.OldLibrary, active.Library
	var sides []side
	switch active.Phase {
	case "staging":
		sides = []side{{current, []domain.Library{prior}}}
	case "switching":
		sides = []side{{current, []domain.Library{prior, incoming}}, {filepath.Join(active.Rollback, "library.sqlite"), []domain.Library{prior}}, {filepath.Join(active.Staging, "library.sqlite"), []domain.Library{incoming}}}
	case "health", "committing", "complete":
		sides = []side{{current, []domain.Library{incoming}}, {filepath.Join(active.Rollback, "library.sqlite"), []domain.Library{prior}}}
	case "rolling_back":
		sides = []side{{current, []domain.Library{incoming}}, {filepath.Join(active.Rollback, "library.sqlite"), []domain.Library{prior}}, {filepath.Join(p.DataDir, ".restore-"+active.ID+".failed", "library.sqlite"), []domain.Library{incoming}}}
	case "rollback_incoming_saved", "rollback_prior_active", "rollback_profile_restored", "rolled_back":
		sides = []side{{current, []domain.Library{prior}}, {filepath.Join(active.Rollback, "library.sqlite"), []domain.Library{prior}}, {filepath.Join(p.DataDir, ".restore-"+active.ID+".failed", "library.sqlite"), []domain.Library{incoming}}}
	default:
		return domain.ErrContentIntegrity
	}
	seen := map[string]bool{}
	currentPresent := false
	for _, side := range sides {
		info, e := os.Lstat(side.path)
		if errors.Is(e, os.ErrNotExist) {
			continue
		}
		if e != nil {
			return e
		}
		if !info.Mode().IsRegular() {
			return domain.ErrContentIntegrity
		}
		lib, e := sqlite.CopyLibrary(ctx, side.path)
		if e != nil {
			return e
		}
		role := ""
		for i, expected := range []domain.Library{prior, incoming} {
			for _, allowed := range side.libraries {
				if expected.ID != "" && lib == expected && lib == allowed {
					if i == 0 {
						role = "prior"
					} else {
						role = "incoming"
					}
				}
			}
		}
		if role == "" || seen[role] {
			return domain.ErrContentIntegrity
		}
		seen[role] = true
		if side.path == current {
			currentPresent = true
		}
		if e = sqlite.CheckRestoreOperationBindings(ctx, side.path, id, keys); e != nil {
			return e
		}
	}
	// Nonterminal recovery retains both logical generations; a complete outcome
	// does not manufacture evidence about unavailable older history.
	if prior.ID != "" && !seen["prior"] && active.Phase != "complete" {
		return domain.ErrContentIntegrity
	}
	if incoming.ID != "" && !seen["incoming"] {
		return domain.ErrContentIntegrity
	}
	switch active.Phase {
	case "staging", "health", "committing", "complete", "rollback_prior_active", "rollback_profile_restored", "rolled_back":
		if !currentPresent && !(prior.ID == "" && (active.Phase == "staging" || active.Phase == "rolled_back" || active.Phase == "rollback_profile_restored")) {
			return domain.ErrContentIntegrity
		}
	}
	return nil
}

// Known terminal natural/existing-alias replay has no dependency on a later
// dataset. New aliases, or latest-success DB projection, still need admission.
func admitRestoreBindings(ctx context.Context, p service.Profile, id string, aliases []string) error {
	fresh, e := restoreExternalBindings(p, id, aliases)
	if e != nil {
		return e
	}
	terminal, e := loadRestoreTerminal(p, id)
	if errors.Is(e, os.ErrNotExist) {
		return checkRestoreDatabases(ctx, p, id, append([]string{id}, aliases...))
	}
	if e != nil {
		return e
	}
	active, e := readRestoreSwitch(p)
	if e != nil {
		return e
	}
	keys := fresh
	if active.ID == id && terminal.Error == "" && len(aliases) > 0 {
		keys = append([]string{id}, aliases...)
	}
	if len(keys) == 0 {
		return nil
	}
	return checkRestoreDatabases(ctx, p, id, keys)
}

// The preserved incoming owner's receipts are another available namespace.
// New/staging admission owns input verification once; completed replay never
// requires that original input again. A switching retry checks its owned,
// already-protected incoming database before adopting any new alias.
type restoreAdmission struct {
	manifest   backupManifest
	oldLibrary domain.Library
}

func admitRestoreInput(ctx context.Context, p service.Profile, plan RestorePlan, aliases []string) (*restoreAdmission, func(), error) {
	if _, e := loadRestoreTerminal(p, plan.ID); e == nil {
		return nil, nil, nil
	} else if !errors.Is(e, os.ErrNotExist) {
		return nil, nil, e
	}
	var active restoreSwitch
	raw, e := os.ReadFile(filepath.Join(p.DataDir, "runtime", "restore-active.json"))
	if e == nil {
		if json.Unmarshal(raw, &active) != nil {
			return nil, nil, domain.ErrContentIntegrity
		}
		if active.ID != plan.ID && active.Phase != "complete" && active.Phase != "rolled_back" {
			return nil, nil, domain.ErrConflict
		}
	} else if !errors.Is(e, os.ErrNotExist) {
		return nil, nil, e
	}
	keys := append([]string{plan.ID}, aliases...)
	if active.ID == plan.ID {
		if e = ownedRestorePaths(p, active); e != nil {
			return nil, nil, e
		}
		if active.Input != plan.PrivateInput || active.OldProfile.ProfileID != plan.DestinationProfile || active.OldProfile.LibraryID != plan.DestinationLibrary {
			return nil, nil, domain.ErrContentIntegrity
		}
		if active.Phase != "staging" {
			return nil, nil, nil
		}
		if active.OldProfile != p {
			return nil, nil, domain.ErrContentIntegrity
		}
	}

	release, e := copyUseContext(ctx, plan.PrivateInput, false)
	if e != nil {
		return nil, nil, e
	}
	admission := &restoreAdmission{}
	check := func() error {
		until, e := time.Parse(time.RFC3339Nano, plan.ExpiresAt)
		if e != nil || active.ID != plan.ID && time.Now().After(until) {
			return domain.ErrPrecondition
		}
		admission.oldLibrary, e = sqlite.CopyLibrary(ctx, filepath.Join(p.DataDir, "storage", "library.sqlite"))
		if e != nil && plan.StateSHA != "empty" {
			return e
		}
		if active.ID == plan.ID && admission.oldLibrary != active.OldLibrary {
			return domain.ErrContentIntegrity
		}
		state, e := restoreStateContext(ctx, p)
		if e != nil {
			return e
		}
		if state != plan.StateSHA {
			return domain.ErrPrecondition
		}
		admission.manifest, e = VerifyBackup(ctx, plan.PrivateInput)
		if e != nil {
			return e
		}
		if admission.manifest.SnapshotSHA != plan.InputSHA || admission.manifest.LibraryID != plan.LibraryID {
			return domain.ErrConflict
		}
		return sqlite.CheckRestoreOperationBindings(ctx, filepath.Join(plan.PrivateInput, "database.sqlite"), plan.ID, keys)
	}
	if e = check(); e != nil {
		release()
		return nil, nil, e
	}
	return admission, release, nil
}
