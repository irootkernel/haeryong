package foundation

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"

	"haeryong.local/haeryong/internal/adapters/sqlite"
	"haeryong.local/haeryong/internal/application/service"
	app "haeryong.local/haeryong/internal/application/storage"
	"haeryong.local/haeryong/internal/domain"
)

type restoreTerminal struct {
	ID, Profile, Error string
	Result             map[string]any
}

func loadRestoreTerminal(p service.Profile, id string) (restoreTerminal, error) {
	var t restoreTerminal
	raw, e := os.ReadFile(filepath.Join(p.DataDir, "runtime", "restore-result-"+id+".json"))
	if e != nil {
		return t, e
	}
	if json.Unmarshal(raw, &t) != nil || t.ID != id || t.Profile != p.ProfileID || t.Result == nil {
		return t, domain.ErrContentIntegrity
	}
	return t, nil
}
func saveRestoreTerminal(p service.Profile, state restoreSwitch, faults ...func(string) error) error {
	var fault func(string) error
	if len(faults) > 0 {
		fault = faults[0]
	}
	t := restoreTerminal{ID: state.ID, Profile: p.ProfileID, Result: restoreResult(state)}
	if state.Phase == "rolled_back" {
		t.Error = "restore_health_failed"
	}
	old, e := loadRestoreTerminal(p, state.ID)
	if e == nil {
		a, _ := json.Marshal(old)
		b, _ := json.Marshal(t)
		if string(a) != string(b) {
			return domain.ErrConflict
		}
		return copyRecordSync(filepath.Join(p.DataDir, "runtime", "restore-result-"+state.ID+".json"), fault)
	}
	if !errors.Is(e, os.ErrNotExist) {
		return e
	}
	return copyJSONWithFault(filepath.Join(p.DataDir, "runtime", "restore-result-"+state.ID+".json"), t, fault)
}
func restoreIdentity(tx app.WriteTx, state restoreSwitch) error {
	lib, e := tx.Library()
	if e != nil {
		return e
	}
	hold, e := tx.RecoveryHold()
	if e != nil {
		return e
	}
	if lib.ID != state.Library.ID || lib.ProfileID != state.Library.ProfileID || lib.RuntimeGeneration != state.Library.RuntimeGeneration || lib.AuthEpoch != state.Library.AuthEpoch || lib.StreamEpoch != state.Library.StreamEpoch || hold == nil || hold.ID != state.ID {
		return domain.ErrContentIntegrity
	}
	return nil
}
func (s *Store) checkRestoreIdentity(ctx context.Context, p service.Profile, state restoreSwitch) error {
	if p.ProfileID != state.Library.ProfileID || p.LibraryID != state.Library.ID || p.ControlKey != state.NewKey {
		return domain.ErrContentIntegrity
	}
	return s.Write(ctx, func(tx app.WriteTx) error { return restoreIdentity(tx, state) })
}

// Each move has a durable direction before it starts. A retry may see either
// side of exactly that move; ambiguous/foreign combinations remain blocked.
func rollbackRestore(ctx context.Context, w *service.LockedProfile, s *restoreSwitch, plan RestorePlan, journal string, boundary func(string) error) error {
	p := w.Profile
	active := filepath.Join(p.DataDir, "storage")
	failed := filepath.Join(p.DataDir, ".restore-"+s.ID+".failed")
	exists := func(path string) bool { _, e := os.Lstat(path); return e == nil }
	advance := func(phase string) error { s.Phase = phase; return copyJSONWithFault(journal, *s, boundary) }
	if s.Phase == "rolling_back" {
		if !exists(failed) {
			lib, e := sqlite.CopyLibrary(ctx, filepath.Join(active, "library.sqlite"))
			if e != nil {
				return e
			}
			if lib.ID != s.Library.ID || lib.RuntimeGeneration != s.Library.RuntimeGeneration {
				return domain.ErrContentIntegrity
			}
			if e = boundary("rollback_move_incoming"); e != nil {
				return e
			}
			if e = os.Rename(active, failed); e != nil {
				return e
			}
		} else if exists(active) {
			return domain.ErrConflict
		}
		if e := boundary("rollback_incoming_moved"); e != nil {
			return e
		}
		if e := copySyncDirectory(p.DataDir); e != nil {
			return e
		}
		if e := advance("rollback_incoming_saved"); e != nil {
			return e
		}
	}
	if s.Phase == "rollback_incoming_saved" {
		if exists(s.Rollback) {
			if exists(active) {
				return domain.ErrConflict
			}
			if e := boundary("rollback_move_prior"); e != nil {
				return e
			}
			if e := os.Rename(s.Rollback, active); e != nil {
				return e
			}
		} else if !exists(active) {
			return domain.ErrContentIntegrity
		}
		if s.OldLibrary.ID != "" {
			lib, e := sqlite.CopyLibrary(ctx, filepath.Join(active, "library.sqlite"))
			if e != nil {
				return e
			}
			if lib != s.OldLibrary {
				return domain.ErrContentIntegrity
			}
		}
		if e := boundary("rollback_prior_moved"); e != nil {
			return e
		}
		// Keep the durable phase at incoming_saved until the existing profile
		// write syncs DataDir. Recovery can then reconcile either side of this
		// unsynced rename, without assuming an entry is already durable.
		s.Phase = "rollback_prior_active"
	}
	if s.Phase == "rollback_prior_active" {
		if e := boundary("rollback_profile_write"); e != nil {
			return e
		}
		// This existing write syncs DataDir and the preceding storage moves.
		if e := w.RestorePriorProfile(s.OldProfile); e != nil {
			return e
		}
		if e := boundary("rollback_profile_restored"); e != nil {
			return e
		}
		if e := advance("rollback_profile_restored"); e != nil {
			return e
		}
	}
	if s.Phase == "rollback_profile_restored" {
		if plan.DestinationLibrary != plan.LibraryID && s.OldLibrary.ID != "" {
			// Trial projection is metadata bookkeeping, not a second strict
			// startup admission of the exact prior storage we just restored.
			if e := boundary("rollback_trial_record"); e != nil {
				return e
			}
			if e := sqlite.RecordRestoreTrialFailureAt(ctx, filepath.Join(p.DataDir, "storage", "library.sqlite"), s.FailureAt); e != nil {
				return e
			}
		}
		if e := boundary("rollback_terminal"); e != nil {
			return e
		}
		s.Phase = "rolled_back"
		if e := saveRestoreTerminal(p, *s, boundary); e != nil {
			return e
		}
		if e := copyJSONWithFault(journal, *s, boundary); e != nil {
			return e
		}
	}
	return errors.New("restore_health_failed")
}
