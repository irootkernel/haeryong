package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"

	"haeryong.local/haeryong/internal/adapters/client"
	"haeryong.local/haeryong/internal/adapters/configpath"
	"haeryong.local/haeryong/internal/adapters/foundation"
	"haeryong.local/haeryong/internal/application/auth"
	config "haeryong.local/haeryong/internal/application/configuration"
	"haeryong.local/haeryong/internal/application/service"
	app "haeryong.local/haeryong/internal/application/storage"
	"haeryong.local/haeryong/internal/domain"
)

func reviewRecovery(ctx context.Context, opt options, emit func(any) error) error {
	if len(opt.command) != 2 {
		return invalid("restore review --profile DEST [--choices FILE --confirm]")
	}
	w, e := service.OpenWriter(opt.selection)
	if e != nil {
		return e
	}
	defer w.Close()
	store := foundation.New(nil)
	_, closeStore, e := store.Prepare(ctx, w.Profile)
	if e != nil {
		return e
	}
	defer closeStore()
	a := auth.New(store)
	credentialPath := opt.flags["credential"]
	if credentialPath == "" {
		root, e := clientDirectory(opt)
		if e != nil {
			return e
		}
		credentialPath = filepath.Join(root, "credential.json")
	}
	credential, e := client.LoadCredential(credentialPath)
	if e != nil {
		return e
	}
	if credential.LibraryID != w.Profile.LibraryID || credential.Profile != w.Profile.Name {
		return commandError{3, domain.ErrDenied}
	}
	record, e := a.Authenticate(ctx, credential.Token)
	if e != nil {
		return e
	}
	if record.Scope.Kind != "owner" {
		return commandError{3, domain.ErrDenied}
	}
	var hold *domain.RecoveryHold
	if e = store.Write(ctx, func(tx app.WriteTx) error { var e error; hold, e = tx.RecoveryHold(); return e }); e != nil {
		return e
	}
	if hold == nil {
		return emit(map[string]any{"state": "no_pending_restore_review"})
	}
	if opt.flags["choices"] == "" {
		return emit(hold)
	}
	if opt.flags["confirm"] != "true" {
		return invalid("restore review choices require --confirm")
	}
	f, e := os.Open(opt.flags["choices"])
	if e != nil {
		return e
	}
	defer f.Close()
	var choices struct {
		Configuration map[string]any `json:"configuration"`
		Expected      int64          `json:"expected_config_revision"`
		Jobs          []string       `json:"resume_cleanup_job_ids"`
	}
	decoder := json.NewDecoder(f)
	decoder.DisallowUnknownFields()
	decoder.UseNumber()
	if e = decoder.Decode(&choices); e != nil {
		return e
	}
	if choices.Expected < 1 || choices.Configuration == nil {
		return invalid("review choices require configuration and expected_config_revision")
	}
	if e = config.Validate(choices.Configuration); e != nil {
		return e
	}
	core := config.New(store, a, configpath.Resolver{})
	core.RestoreReviewID = hold.ID
	core.RestoreCleanupJobs = choices.Jobs
	key, e := service.NewID()
	if e != nil {
		return e
	}
	result, e := core.Plan(ctx, credential.Token, key, config.PlanRequest{Scope: config.Scope{Kind: "library"}, ExpectedRevision: choices.Expected, Changes: choices.Configuration})
	if e != nil {
		return e
	}
	var plan struct {
		ID string `json:"plan_id"`
	}
	if e = json.Unmarshal(result.Body, &plan); e != nil {
		return e
	}
	if _, e = core.Commit(ctx, credential.Token, plan.ID, "", config.CommitRequest{ExpectedRevision: choices.Expected, Confirmed: true}); e != nil {
		return e
	}
	if e = store.Write(ctx, func(tx app.WriteTx) error {
		session, e := a.AuthenticateWrite(ctx, tx, credential.Token)
		if e != nil {
			return e
		}
		if session.Scope.Kind != "owner" {
			return domain.ErrDenied
		}
		return tx.ReviewRecovery(hold.ID, choices.Jobs)
	}); e != nil {
		return e
	}
	return emit(map[string]any{"restore_id": hold.ID, "state": "reviewed", "resumed_cleanup_job_ids": choices.Jobs, "external_outcomes": "reconciliation_still_required"})
}
