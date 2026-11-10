//go:build haeryong_provider_live

// This opt-in native fixture uses an already publicly prepared private profile.
// Manual scheduling is the before-next-admission barrier; no semantic rows are
// seeded and the production dispatcher is not claimed to expose such a barrier.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"haeryong.local/haeryong/internal/adapters/ai/codex"
	"haeryong.local/haeryong/internal/adapters/foundation"
	"haeryong.local/haeryong/internal/adapters/gitreader"
	sourcefiles "haeryong.local/haeryong/internal/adapters/source"
	"haeryong.local/haeryong/internal/application/auth"
	"haeryong.local/haeryong/internal/application/query"
	"haeryong.local/haeryong/internal/application/scan"
	"haeryong.local/haeryong/internal/application/service"
	"haeryong.local/haeryong/internal/application/source"
	app "haeryong.local/haeryong/internal/application/storage"
	"haeryong.local/haeryong/internal/application/work"
	"haeryong.local/haeryong/internal/domain"
)

type request struct {
	Profile, DataDir, CredentialPath, VersionID, ProviderBinary, Receipt string
	ExistingScanJobID                                                    string
}

var stage = "request"

func main() {
	if len(os.Args) != 2 {
		os.Exit(2)
	}
	if err := run(os.Args[1]); err != nil {
		// Native stderr/source/credentials and model reasoning are never printed.
		fmt.Fprintf(os.Stderr, "Task03 interruption failed: stage=%s class=%T\n", stage, err)
		os.Exit(1)
	}
}

func run(path string) (result error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var input request
	if err = json.Unmarshal(raw, &input); err != nil {
		return err
	}
	var credential struct{ Token string }
	raw, err = os.ReadFile(input.CredentialPath)
	if err != nil {
		return err
	}
	if err = json.Unmarshal(raw, &credential); err != nil || credential.Token == "" {
		return errors.New("credential unavailable")
	}
	receipt := map[string]any{"class": "production service-method/native-provider integration; controlled manual scheduling", "complete": false}
	defer func() {
		receipt["last_stage"] = stage
		receipt["operation_success"] = result == nil
		if result != nil {
			receipt["complete"] = false
		}
		encoded, e := json.MarshalIndent(receipt, "", "  ")
		if e == nil {
			e = os.WriteFile(input.Receipt, append(encoded, '\n'), 0600)
		}
		result = errors.Join(result, e)
	}()
	stage = "owned_profile_open"
	writer, err := service.OpenWriter(service.Selection{Profile: input.Profile, DataDir: input.DataDir})
	if err != nil {
		return err
	}
	defer func() { e := writer.Close(); receipt["profile_closed"] = e == nil; result = errors.Join(result, e) }()
	if err = writer.PrepareStorageBootstrap(nil); err != nil {
		return err
	}
	store := foundation.New(nil)
	_, closeDB, err := store.Prepare(context.Background(), writer.Profile)
	if err != nil {
		return err
	}
	defer func() { e := closeDB(); receipt["storage_closed"] = e == nil; result = errors.Join(result, e) }()
	if err = writer.MarkStorageReady(nil); err != nil {
		return err
	}
	core := auth.New(store)
	sources := source.New(store, core, store, gitreader.New())
	profiles, err := service.DefaultProfilesRoot()
	if err != nil {
		return err
	}
	sources.Files = sourcefiles.ManagedFiles(input.DataDir, profiles, input.CredentialPath)
	worker := scan.NewWorker(store, store)
	worker.Sources, worker.Identity = sources, codex.SemanticRequestSHA
	driver := codex.New(store, store, filepath.Join(input.DataDir, "storage", "provider-spool"))
	driver.Binary = input.ProviderBinary
	coordinator := &work.ProviderCoordinator{Store: store, Bytes: store, Driver: driver,
		MaintenanceSource: func(c context.Context, id string) error {
			return sources.CaptureMaintenanceAndAdmit(c, id, codex.SemanticRequestSHA)
		},
		Prepare: worker.PrepareMaintenance, Apply: worker.ApplyMaintenance}
	defer func() {
		e := coordinator.StopProviders()
		receipt["provider_coordinator_closed"] = e == nil
		result = errors.Join(result, e)
	}()
	configuration, err := store.Configuration(context.Background())
	if err != nil {
		return err
	}
	var policy struct {
		Maintenance struct {
			MaxRunSeconds int `json:"max_run_seconds"`
		} `json:"maintenance"`
	}
	if json.Unmarshal(configuration.Value, &policy) != nil || policy.Maintenance.MaxRunSeconds <= 0 {
		return errors.New("existing finite run duration unavailable")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(policy.Maintenance.MaxRunSeconds+120)*time.Second)
	defer cancel()
	var accepted struct {
		JobID string `json:"job_id"`
	}
	var first domain.MaintenanceIntent
	accepted.JobID = input.ExistingScanJobID
	receipt["native_execution_in_this_invocation"] = accepted.JobID == ""
	if accepted.JobID == "" {
		stage = "public_scan_admission"
		key, err := service.NewID()
		if err != nil {
			return err
		}
		started, err := scan.New(store, core, store, nil).StartScan(ctx, credential.Token, key, nil, domain.ScanRequest{VersionID: input.VersionID, Depth: "full_scope", SourceMode: "pinned"})
		if err != nil {
			return err
		}
		if json.Unmarshal(started.Body, &accepted) != nil || accepted.JobID == "" {
			return errors.New("scan not admitted")
		}
		stage = "first_unit_production_steps"
		for {
			if err = worker.Process(ctx, accepted.JobID); err != nil {
				return err
			}
			var intents []domain.MaintenanceIntent
			if err = store.Write(ctx, func(tx app.WriteTx) error {
				var e error
				intents, e = tx.MaintenanceIntentsByScan(accepted.JobID)
				return e
			}); err != nil {
				return err
			}
			if len(intents) > 1 {
				return errors.New("next unit admitted before barrier")
			}
			if len(intents) == 0 {
				continue
			}
			first = intents[0]
			child, e := store.Job(ctx, first.JobID)
			if e != nil {
				return e
			}
			if child.State == "completed" {
				break
			}
			if child.State == "queued" || child.State == "running" || child.State == "paused" && child.Error == "source_capture_pending" {
				if err = coordinator.Process(ctx, child.ID); err != nil {
					return err
				}
				child, err = store.Job(ctx, child.ID)
				if err != nil {
					return err
				}
				if child.State == "completed" {
					break
				}
			} else {
				receipt["first_child_state"] = child.State
				return errors.New("first unit did not complete")
			}
		}
		// The next scan turn has not run. Authenticate cancellation now, rather
		// than race the daemon dispatcher or fabricate an unfinished assessment.
		stage = "authenticated_cancel_before_next_admission"
		key, err = service.NewID()
		if err != nil {
			return err
		}
		if _, err = core.CancelJob(ctx, credential.Token, accepted.JobID, key); err != nil {
			return err
		}
		if err = worker.Process(ctx, accepted.JobID); err != nil {
			return err
		}
	}
	receipt["scan_job_id"] = accepted.JobID
	stage = "retained_fixed_scope_query"
	var set domain.CoverageSet
	var attempts []domain.ProviderAttempt
	var inputs []domain.ProviderInput
	if err = store.Write(ctx, func(tx app.WriteTx) error {
		intents, e := tx.MaintenanceIntentsByScan(accepted.JobID)
		if e != nil {
			return e
		}
		if len(intents) != 1 {
			return errors.New("unexpected second admission")
		}
		// Source admission can replace the metadata target and coverage IDs.
		// Read the actual committed intent after the first child has settled.
		first = intents[0]
		set, e = tx.CoverageSetByID(first.FullCoverageSetID)
		if e != nil {
			return e
		}
		attempts, e = tx.ProviderAttempts(first.JobID)
		if e != nil {
			return e
		}
		if len(attempts) != 1 {
			return errors.New("unexpected provider attempts")
		}
		inputs, e = tx.ProviderInputs(attempts[0].ID)
		return e
	}); err != nil {
		return err
	}
	receipt["attempts"], receipt["inputs"] = attempts, inputs
	q := query.New(store, core, store)
	view, err := q.Assessments(ctx, credential.Token, input.VersionID, query.AssessmentQuery{TargetSetID: set.TargetSetID})
	if err != nil {
		return err
	}
	coverage, err := q.AssessmentCoverage(ctx, credential.Token, input.VersionID, set.ID, "", query.Options{})
	if err != nil {
		return err
	}
	receipt["view"], receipt["coverage"] = view, coverage
	stage = "fixed_scope_assertions"
	a := view["applicable"].(map[string]any)
	c := a["coverage"].(map[string]any)
	if c["eligible"] != 2 || c["assessed"] != 1 || c["complete"] != false {
		return errors.New("applicable fixed coverage differs from actual units")
	}
	transport := attempts[0].Transport
	if attempts[0].State != "result_saved" || transport == nil || transport.DriverOutcome != "result_ready" || transport.CleanupState != "drained" || !transport.LocalClosed {
		return errors.New("native result or positive cleanup not established")
	}
	if len(inputs) == 0 {
		return errors.New("actual source delivery missing")
	}
	for _, input := range inputs {
		if input.ObservedAt == nil || input.ObservedOrigin != "daemon_supplied" {
			return errors.New("actual input observation missing")
		}
	}
	if a["status"] != "not_assessed" || len(coverage.Items) != 2 || coverage.NextCursor != nil {
		return errors.New("full fixed scope changed")
	}
	assessed, unattempted := 0, 0
	for _, unit := range coverage.Items {
		switch unit["outcome"] {
		case "assessed":
			assessed++
		case "not_checked":
			if unit["reason"] == nil || unit["assessment_id"] != nil {
				return errors.New("unattempted reason or provenance missing")
			}
			unattempted++
		default:
			return errors.New("first result was not fully assessed; frozen case failure")
		}
	}
	if assessed != 1 || unattempted != 1 {
		return errors.New("required first-assessed/remainder-unattempted outcome missing")
	}
	job, err := store.Job(ctx, accepted.JobID)
	if err != nil {
		return err
	}
	receipt["job"] = job
	if job.State != "cancelled" {
		return errors.New("cancellation did not settle")
	}
	receipt["complete"] = true
	stage = "complete"
	return nil
}
