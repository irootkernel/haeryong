package foundation

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"haeryong.local/haeryong/internal/adapters/sqlite"
	"haeryong.local/haeryong/internal/application/service"
	app "haeryong.local/haeryong/internal/application/storage"
	"haeryong.local/haeryong/internal/domain"
)

type RestorePlan struct {
	ID                        string `json:"plan_id"`
	Input                     string `json:"-"`
	InputSHA                  string `json:"input_sha256"`
	LibraryID                 string `json:"library_id"`
	DestinationProfile        string `json:"destination_profile"`
	DestinationLibrary        string `json:"replaced_library_id"`
	StateSHA                  string `json:"destination_state_sha256"`
	ExpiresAt                 string `json:"expires_at"`
	SessionImpact             string `json:"session_impact"`
	PolicyImpact              string `json:"policy_impact"`
	KnownCurrentConfiguration json.RawMessage
	PrivateInput              string
}
type restoreSwitch = service.RestoreSwitch

func restoreStateContext(ctx context.Context, p service.Profile) (string, error) {
	// With the exclusive writer lock, database/WAL bytes cannot change beneath
	// the plan. A missing never-initialized database is an explicit empty state.
	h := sha256.New()
	for _, name := range []string{"library.sqlite", "library.sqlite-wal"} {
		f, e := os.Open(filepath.Join(p.DataDir, "storage", name))
		if errors.Is(e, os.ErrNotExist) {
			if name == "library.sqlite" && p.StorageState == "pending" {
				return "empty", nil
			}
			if name == "library.sqlite-wal" {
				continue
			}
		}
		if e != nil {
			return "", e
		}
		fmt.Fprintln(h, name)
		_, e = copyStream(ctx, h, f)
		e = errors.Join(e, f.Close())
		if e != nil {
			return "", e
		}
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
func PlanRestore(ctx context.Context, selection service.Selection, input string) (RestorePlan, error) {
	var plan RestorePlan
	writer, e := service.OpenWriter(selection)
	if e != nil {
		return plan, e
	}
	defer writer.Close()
	input, e = filepath.Abs(input)
	if e != nil {
		return plan, e
	}
	input, e = filepath.EvalSymlinks(input)
	if e != nil {
		return plan, e
	}
	m, e := VerifyBackup(ctx, input)
	if e != nil {
		return plan, e
	}
	id, e := service.NewID()
	if e != nil {
		return plan, e
	}
	if e = checkRestoreBindings(ctx, writer.Profile, id, nil); e != nil {
		return plan, e
	}
	for _, kind := range []string{"plan", "result"} {
		_, err := os.Lstat(filepath.Join(writer.Profile.DataDir, "runtime", "restore-"+kind+"-"+id+".json"))
		if err == nil {
			return plan, domain.ErrConflict
		}
		if !errors.Is(err, os.ErrNotExist) {
			return plan, err
		}
	}
	known := sqlite.CopyCurrentConfiguration(ctx, filepath.Join(writer.Profile.DataDir, "storage", "library.sqlite"))
	state, e := restoreStateContext(ctx, writer.Profile)
	if e != nil {
		return plan, e
	}
	plan = RestorePlan{ID: id, InputSHA: m.SnapshotSHA, LibraryID: m.LibraryID, DestinationProfile: writer.Profile.ProfileID, DestinationLibrary: writer.Profile.LibraryID, StateSHA: state, ExpiresAt: time.Now().UTC().Add(15 * time.Minute).Format(time.RFC3339Nano), SessionImpact: "all_restored_credentials_invalidated_new_bootstrap_required", PolicyImpact: "source_provider_exposure_automation_and_destructive_work_held_for_owner_review", PrivateInput: input, KnownCurrentConfiguration: known}
	e = copyJSON(filepath.Join(writer.Profile.DataDir, "runtime", "restore-plan-"+id+".json"), plan)
	return plan, e
}
func ApplyRestore(ctx context.Context, selection service.Selection, id string, validate func(map[string]any) error, aliases ...string) (map[string]any, error) {
	return applyRestore(ctx, selection, id, validate, nil, aliases...)
}
func applyRestore(ctx context.Context, selection service.Selection, id string, validate func(map[string]any) error, fault func(string) error, aliases ...string) (map[string]any, error) {
	boundary := func(phase string) error {
		if fault != nil {
			return fault(phase)
		}
		return nil
	}
	writer, e := service.OpenRecoveryWriter(selection)
	if e != nil {
		return nil, e
	}
	defer writer.Close()
	p := writer.Profile
	planPath := filepath.Join(p.DataDir, "runtime", "restore-plan-"+id+".json")
	raw, e := os.ReadFile(planPath)
	if e != nil {
		return nil, e
	}
	var plan RestorePlan
	if e = json.Unmarshal(raw, &plan); e != nil {
		return nil, e
	}
	if plan.ID != id || plan.DestinationProfile != p.ProfileID {
		return nil, domain.ErrConflict
	}
	if e = admitRestoreBindings(ctx, p, id, aliases); e != nil {
		return nil, e
	}
	admission, releaseInput, e := admitRestoreInput(ctx, p, plan, aliases)
	if e != nil {
		return nil, e
	}
	if releaseInput != nil {
		defer releaseInput()
	}
	if e = copyRecordSync(planPath, boundary); e != nil {
		return nil, e
	}
	for _, alias := range aliases {
		if alias == "" {
			continue
		}
		if !copyUUID.MatchString(alias) {
			return nil, domain.ErrInvalidTarget
		}
		aliasPath := filepath.Join(p.DataDir, "runtime", "restore-operation-"+alias+".json")
		raw, err := os.ReadFile(aliasPath)
		if err == nil {
			var bound string
			if json.Unmarshal(raw, &bound) != nil || bound != id {
				return nil, domain.ErrConflict
			}
			if e = copyRecordSync(aliasPath, boundary); e != nil {
				return nil, e
			}
		} else if errors.Is(err, os.ErrNotExist) {
			if e = copyJSONWithFault(aliasPath, id, boundary); e != nil {
				return nil, e
			}
		} else {
			return nil, err
		}
	}
	if terminal, err := loadRestoreTerminal(p, id); err == nil {
		if e = copyRecordSync(filepath.Join(p.DataDir, "runtime", "restore-result-"+id+".json"), boundary); e != nil {
			return nil, e
		}
		// Only the latest matching generation may receive optional DB alias
		// receipts. Older terminal replay never opens/mutates replaced storage.
		var active restoreSwitch
		raw, readErr := os.ReadFile(filepath.Join(p.DataDir, "runtime", "restore-active.json"))
		if readErr != nil && !errors.Is(readErr, os.ErrNotExist) {
			return nil, readErr
		}
		if readErr == nil {
			if json.Unmarshal(raw, &active) != nil {
				return nil, domain.ErrContentIntegrity
			}
			if e = copyRecordSync(filepath.Join(p.DataDir, "runtime", "restore-active.json"), boundary); e != nil {
				return nil, e
			}
		}
		if readErr == nil && active.ID == id {
			if terminal.Error != "" {
				if writer.Profile.ProfileID != active.OldProfile.ProfileID || writer.Profile.LibraryID != active.OldProfile.LibraryID || writer.Profile.ControlKey != active.OldProfile.ControlKey {
					return nil, domain.ErrContentIntegrity
				}

				if active.Phase != "rolled_back" {
					active.Phase = "rolled_back"
					if e = copyJSONWithFault(filepath.Join(p.DataDir, "runtime", "restore-active.json"), active, boundary); e != nil {
						return nil, e
					}
				}
				return nil, errors.New(terminal.Error)
			}
			if len(aliases) > 0 {
				probe := New(nil)
				_, closeStore, err := probe.Prepare(ctx, writer.Profile)
				if err != nil {
					return nil, err
				}
				err = probe.checkRestoreIdentity(ctx, writer.Profile, active)
				if err == nil {
					err = probe.recordRestore(ctx, active, plan, aliases)
				}
				err = errors.Join(err, closeStore())
				if err != nil {
					return nil, err
				}
			}
			if active.Phase != "complete" {
				active.Phase = "complete"
				if err = copyJSONWithFault(filepath.Join(p.DataDir, "runtime", "restore-active.json"), active, boundary); err != nil {
					return nil, err
				}
			}
		}
		if terminal.Error != "" {
			return nil, errors.New(terminal.Error)
		}
		return terminal.Result, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	journal := filepath.Join(p.DataDir, "runtime", "restore-active.json")
	state := restoreSwitch{ID: id, Phase: "staging", Input: plan.PrivateInput, Staging: filepath.Join(p.DataDir, ".restore-"+id+".staging"), Rollback: filepath.Join(p.DataDir, ".restore-"+id+".prior"), OldProfile: p}
	if raw, e = os.ReadFile(journal); e == nil {
		if e = copyRecordSync(journal, boundary); e != nil {
			return nil, e
		}
		var prior restoreSwitch
		if e = json.Unmarshal(raw, &prior); e != nil {
			return nil, e
		}
		if prior.ID != id && prior.Phase != "complete" && prior.Phase != "rolled_back" {
			return nil, domain.ErrConflict
		}
		if prior.ID == id {
			state = prior
			if state.Phase == "complete" {
				for _, alias := range aliases {
					if alias != "" {
						probe := New(nil)
						_, closeStore, e := probe.Prepare(ctx, writer.Profile)
						if e != nil {
							return nil, e
						}
						e = probe.recordRestore(ctx, state, plan, aliases)
						e = errors.Join(e, closeStore())
						if e != nil {
							return nil, e
						}
						break
					}
				}
				if e = saveRestoreTerminal(p, state, boundary); e != nil {
					return nil, e
				}
				return restoreResult(state), nil
			}
		}
	}
	if state.Phase == "staging" {
		state.OldLibrary = admission.oldLibrary

		if e = copyJSONWithFault(journal, state, boundary); e != nil {
			return nil, e
		}
		// Staging belongs to this persisted plan and has never been switched.
		// Rebuild a partial staging tree after a failed or interrupted transfer.
		if e = os.RemoveAll(state.Staging); e != nil {
			return nil, e
		}
		if e = os.Mkdir(state.Staging, 0700); e != nil {
			return nil, e
		}
		if e = os.Mkdir(filepath.Join(state.Staging, "objects"), 0700); e != nil {
			return nil, e
		}
		if e = copyRegularFile(ctx, filepath.Join(state.Input, "database.sqlite"), filepath.Join(state.Staging, "library.sqlite")); e != nil {
			return nil, e
		}
		after := ""
		for {
			objects, e := sqlite.CopyObjectPage(ctx, filepath.Join(state.Input, "database.sqlite"), after)
			if e != nil {
				return nil, e
			}
			if len(objects) == 0 {
				break
			}
			for _, o := range objects {
				if e = copyRegularFile(ctx, filepath.Join(state.Input, "objects", o.Hash), filepath.Join(state.Staging, "objects", o.Hash)); e != nil {
					return nil, e
				}
				hash, size, e := copyHashContext(ctx, filepath.Join(state.Staging, "objects", o.Hash))
				if e != nil || hash != o.Hash || size != o.Size {
					return nil, domain.ErrContentIntegrity
				}
				after = o.Hash
			}
		}
		inventory, e := os.OpenFile(filepath.Join(p.DataDir, "runtime", "restore-outstanding-"+id+".jsonl"), os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0600)
		if e != nil {
			return nil, e
		}
		enc := json.NewEncoder(inventory)
		if plan.StateSHA != "empty" {
			e = sqlite.RestoreOutstanding(ctx, filepath.Join(p.DataDir, "storage", "library.sqlite"), func(raw json.RawMessage) error { return enc.Encode(raw) })
		}
		if e == nil {
			e = inventory.Sync()
		}
		e = errors.Join(e, inventory.Close())
		if e != nil {
			return nil, e
		}
		if e = boundary("staged_bytes"); e != nil {
			return nil, e
		}
		state.Library, e = sqlite.ProtectRestoredDatabase(ctx, filepath.Join(state.Staging, "library.sqlite"), p, id, plan.KnownCurrentConfiguration, validate)
		if e != nil {
			return nil, e
		}
		if e = boundary("protected_database"); e != nil {
			return nil, e
		}
		key := make([]byte, 32)
		if _, e = rand.Read(key); e != nil {
			return nil, e
		}
		state.NewKey = hex.EncodeToString(key)
		if e = syncCopyTree(ctx, state.Staging, fault); e != nil {
			return nil, e
		}
		state.Phase = "switching"
		if e = copyJSONWithFault(journal, state, boundary); e != nil {
			return nil, e
		}
	}
	if e = boundary("switch_record"); e != nil {
		return nil, e
	}
	active := filepath.Join(p.DataDir, "storage")
	if state.Phase == "switching" {
		if _, e = os.Stat(state.Rollback); errors.Is(e, os.ErrNotExist) {
			if e = os.Rename(active, state.Rollback); e != nil {
				return nil, e
			}
			if e = copySyncDirectory(p.DataDir); e != nil {
				return nil, e
			}
		}
		if e = boundary("old_renamed"); e != nil {
			return nil, e
		}
		if _, e = os.Stat(active); errors.Is(e, os.ErrNotExist) {
			if e = os.Rename(state.Staging, active); e != nil {
				return nil, e
			}
			if e = copySyncDirectory(p.DataDir); e != nil {
				return nil, e
			}
		}
		if e = boundary("new_renamed"); e != nil {
			return nil, e
		}
		if e = writer.RestoreIdentity(state.Library.ID, state.NewKey); e != nil {
			return nil, e
		}
		if e = boundary("profile_bound"); e != nil {
			return nil, e
		}
		state.Phase = "health"
		if e = copyJSONWithFault(journal, state, boundary); e != nil {
			return nil, e
		}
	}
	var rollbackCause error
	if state.Phase == "health" {
		probe := New(nil)
		healthErr := boundary("health")
		if healthErr == nil {
			_, closer, err := probe.Prepare(ctx, writer.Profile)
			healthErr = err
			if err == nil {
				healthErr = errors.Join(probe.checkRestoreIdentity(ctx, writer.Profile, state), closer())
			}
		}
		if healthErr != nil {
			rollbackCause = healthErr
			state.Phase = "rolling_back"
			state.FailureAt = time.Now().UTC().Format(time.RFC3339Nano)
			if e = copyJSONWithFault(journal, state, boundary); e != nil {
				return nil, errors.Join(healthErr, e)
			}
		} else {
			// Completion I/O is retryable terminalization, never a new health
			// failure that rolls back an already committed operation receipt.
			state.Phase = "committing"
			if e = copyJSONWithFault(journal, state, boundary); e != nil {
				return nil, e
			}
		}
	}
	if state.Phase == "rolling_back" || state.Phase == "rollback_incoming_saved" || state.Phase == "rollback_prior_active" || state.Phase == "rollback_profile_restored" || state.Phase == "rolled_back" {
		e = rollbackRestore(ctx, writer, &state, plan, journal, boundary)
		return nil, errors.Join(rollbackCause, e)
	}
	if state.Phase == "committing" {
		probe := New(nil)
		_, closer, err := probe.Prepare(ctx, writer.Profile)
		if err != nil {
			return nil, err
		}
		e = probe.checkRestoreIdentity(ctx, writer.Profile, state)
		if e == nil {
			e = probe.recordRestore(ctx, state, plan, aliases)
		}
		e = errors.Join(e, closer())
		if e != nil {
			return nil, e
		}
		state.Phase = "complete"
		if e = boundary("terminal_record"); e != nil {
			return nil, e
		}
		if e = saveRestoreTerminal(p, state, boundary); e != nil {
			return nil, e
		}
		if e = copyJSONWithFault(journal, state, boundary); e != nil {
			return nil, e
		}
	}

	return restoreResult(state), nil
}
func restoreResult(s restoreSwitch) map[string]any {
	return map[string]any{"restore_id": s.ID, "job_id": s.ID, "state": s.Phase, "library_id": s.Library.ID, "runtime_generation": s.Library.RuntimeGeneration, "auth_epoch": s.Library.AuthEpoch, "stream_epoch": s.Library.StreamEpoch, "next_action": "bootstrap_new_owner_session_then_review_restored_preferences"}
}

func (s *Store) RecoveryReview(ctx context.Context, id string, jobs []string) error {
	return s.Write(ctx, func(tx app.WriteTx) error { return tx.ReviewRecovery(id, jobs) })
}

// The maintenance owner records its result only after the real strict startup
// and complete referenced-object verification. It is never a background task.
func (s *Store) recordRestore(ctx context.Context, state restoreSwitch, plan RestorePlan, aliases []string) error {
	return s.Write(ctx, func(tx app.WriteTx) error {
		if e := restoreIdentity(tx, state); e != nil {
			return e
		}
		owner, e := tx.AuthState()
		if e != nil {
			return e
		}
		req := domain.Request{Key: domain.OperationKey{PrincipalID: owner.OwnerID, OperationID: state.ID}, Method: "POST", Target: "/local/v1/restore/" + state.ID, Body: []byte(`{}`)}
		_, exists, e := tx.ReserveOperation(req, nil)
		if e != nil {
			return e
		}
		firstCompletion := !exists
		recorded := state
		recorded.Phase = "complete"
		raw, _ := json.Marshal(restoreResult(recorded))
		if !exists {
			scope, _ := json.Marshal(map[string]string{"principal_id": owner.OwnerID, "restore_id": state.ID})
			if e = tx.CreateJob(domain.Job{ID: state.ID, Kind: "restore", Scope: scope}); e != nil {
				return e
			}
			j, e := tx.ClaimJob(state.ID)
			if e != nil {
				return e
			}
			if e = tx.SetJobResult(j.ID, raw); e != nil {
				return e
			}
			if e = tx.FinishJob(j.ID, j.AttemptGeneration, "", nil); e != nil {
				return e
			}
			if e = tx.CompleteOperation(req.Key, domain.Response{Status: 200, Body: raw}); e != nil {
				return e
			}
		}
		for _, alias := range aliases {
			if alias == "" || alias == state.ID {
				continue
			}
			req.Key.OperationID = alias
			_, exists, e = tx.ReserveOperation(req, nil)
			if e != nil {
				return e
			}
			if !exists {
				if e = tx.CompleteOperation(req.Key, domain.Response{Status: 200, Body: raw}); e != nil {
					return e
				}
			}
		}
		// A same-library recovery is not an isolated trial restoration.
		if firstCompletion && plan.DestinationLibrary != plan.LibraryID {
			old, e := tx.ResourceRow("backup_trial_restore", "last")
			if e != nil && !errors.Is(e, domain.ErrNotFound) {
				return e
			}
			now := time.Now().UTC().Format(time.RFC3339Nano)
			raw, _ = json.Marshal(domain.TrialRestore{State: "passed", CompletedAt: &now})
			_, e = tx.MutateResource(domain.Resource{Kind: "backup_trial_restore", ID: "last", Value: raw}, old.Revision, "resource.invalidated")
			return e
		}
		return nil
	})
}

func restoreState(p service.Profile) (string, error) {
	return restoreStateContext(context.Background(), p)
}
