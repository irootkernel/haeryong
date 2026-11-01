package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"haeryong.local/haeryong/internal/adapters/client"
	"haeryong.local/haeryong/internal/adapters/clientstate"
	generation "haeryong.local/haeryong/internal/application/generation"
	"haeryong.local/haeryong/internal/application/service"
	"haeryong.local/haeryong/internal/application/transfer"
	"haeryong.local/haeryong/internal/domain"
)

func generationCommand(ctx context.Context, opt options, emit func(any) error) (resultErr error) {
	if len(opt.command) != 1 || opt.flags["request-file"] == "" {
		return invalid("generate requires --request-file PATH")
	}
	selected, state, e := selectedClient(ctx, opt)
	if e != nil {
		return e
	}
	identity, e := selected.Identity(ctx)
	if e != nil {
		return productError(e)
	}
	path, e := filepath.Abs(opt.flags["request-file"])
	if e != nil {
		return e
	}
	intent := clientstate.CommandIntent{Method: "POST", Target: "/api/v1/generations", SourcePath: path, ProjectID: opt.flags["project"]}
	key := opt.flags["operation-id"]
	if key == "" {
		key, e = service.NewID()
		if e != nil {
			return e
		}
		key, e = state.UseCommandPending(ctx, identity, intent, key)
		if e != nil {
			return e
		}
	}
	if !operationID.MatchString(key) {
		return invalid("canonical operation ID required")
	}
	defer func() {
		var recorded *recordedCommandError
		if resultErr != nil && !errors.As(resultErr, &recorded) {
			// Preserve the original operation in machine output without
			// acknowledging an uncertain or locally unreadable result.
			resultErr = &pendingReviewError{cause: resultErr, operation: key}
		}
	}()
	release, e := state.Lock(ctx, key)
	if e != nil {
		return e
	}
	// Final dispatch uses mutationConditions, which owns this same operation lock.
	// Release only after the complete frozen preparation journal has been saved.
	j, e := state.Load(key)
	if errors.Is(e, domain.ErrNotFound) {
		j, e = captureGenerationRequest(ctx, selected, state, identity, path, opt.flags["project"], key)
	} else if e == nil {
		if j.Purpose != "generation" || j.Generation == nil || j.Generation.SourcePath != path || j.Generation.ExplicitProject != opt.flags["project"] || j.Generation.Launcher.LibraryID != identity.LibraryID || (j.Generation.Launcher.PrincipalID != identity.PrincipalID && j.Identity.PrincipalID != identity.PrincipalID) {
			e = transfer.ErrIntent
		}
	}
	if e != nil {
		_ = release()
		return productError(e)
	}
	api, e := generationDispatchClient(ctx, opt, selected, state, &j)
	if e != nil {
		_ = release()
		return e
	}
	var acknowledge func() error
	if opt.flags["operation-id"] == "" {
		launcher := j.Generation.Launcher
		acknowledge = func() error { return state.FinishCommandPending(context.Background(), launcher, intent, key) }
	}
	// Receipt-first reentry does not reopen the request file or staged bytes.
	if j.FinalBody != nil {
		response, lookup := transfer.AwaitOperation(ctx, api, key)
		if lookup == nil {
			j.Response = &response
			j.Phase = "accepted"
			e = state.Save(j)
		} else if !errors.Is(lookup, domain.ErrNotFound) {
			if response.Status >= 400 {
				j.Response, j.Phase = &response, "failed"
				if saveErr := state.Save(j); saveErr != nil {
					_ = release()
					return saveErr
				}
			}
			if unlockErr := release(); unlockErr != nil {
				return unlockErr
			}
			return afterDefinitive(recordedError(productError(lookup), response, key, nil), key, acknowledge)
		} else if j.Response != nil {
			e = transfer.ErrReconcile
		}
		if e != nil {
			_ = release()
			return productError(e)
		}
	}
	if j.Response == nil {
		var request generation.Request
		if json.Unmarshal(j.Generation.Request, &request) != nil {
			_ = release()
			return transfer.ErrIntent
		}
		if j.FinalBody == nil {
			if request.BodyRef == nil {
				ref, e := transfer.New(api, state).PrepareGenerationPrompt(ctx, &j)
				if e != nil {
					_ = release()
					return productError(e)
				}
				request.BodyRef = &generation.BodyRef{ID: ref.ID, Hash: ref.Hash, Size: ref.Size, MediaType: ref.MediaType}
			}
			request.Prompt = ""
			j.FinalBody, e = json.Marshal(request)
			if e != nil {
				_ = release()
				return e
			}
			j.Phase = "final_dispatch"
			if e = state.Save(j); e != nil {
				_ = release()
				return e
			}
		}
	}
	if e = release(); e != nil {
		return e
	}
	if j.Response == nil {
		response, id, e := mutationConditions(ctx, api, state, key, "POST", "/api/v1/generations", json.RawMessage(j.FinalBody), nil)
		if e != nil {
			return afterDefinitive(recordedError(productError(e), response, id, nil), id, acknowledge)
		}
		j.Response = &response
	}
	return followGeneration(ctx, api, state, j, intent, opt.flags["operation-id"] == "", emit)
}
func captureGenerationRequest(ctx context.Context, api *client.Client, state *clientstate.Store, identity transfer.Identity, path, explicitProject, key string) (transfer.Journal, error) {
	var j transfer.Journal
	selection, e := state.Association()
	if e != nil && !errors.Is(e, domain.ErrNotFound) {
		return j, e
	}
	if explicitProject != "" {
		selection = transfer.Selection{ProjectID: explicitProject}
	}
	if selection.ProjectID == "" {
		selection.ProjectID = identity.SelectedProjectID
		if selection.ProjectID == "" {
			selection.ProjectID = identity.UnfiledProjectID
		}
	}
	file, e := os.Open(path)
	if e != nil {
		return j, e
	}
	request, e := generation.DecodeRequestWithTarget(file, selection.ProjectID, selection.DocumentID)
	closeErr := file.Close()
	if e != nil {
		return j, e
	}
	if closeErr != nil {
		return j, closeErr
	}
	if request.ProjectID == "" || explicitProject != "" && request.ProjectID != explicitProject {
		return j, transfer.ErrIntent
	}
	if request.DocumentID != nil {
		doc, e := api.Document(ctx, *request.DocumentID)
		if e != nil {
			return j, e
		}
		if doc.ProjectID != request.ProjectID {
			return j, transfer.ErrIntent
		}
		if !request.BasePresent {
			request.BaseVersionID = doc.LatestVersionID
			request.BasePresent = true
		}
		if request.BaseVersionID == nil {
			return j, invalid("existing document requires an expected base")
		}
	} else if request.BaseVersionID != nil {
		return j, transfer.ErrIntent
	}
	j = transfer.Journal{OperationID: key, Purpose: "generation", Method: "POST", Target: "/api/v1/generations", Identity: identity, Selection: transfer.Selection{ProjectID: request.ProjectID, DocumentID: request.DocumentID}, BaseVersionID: request.BaseVersionID, Phase: "capturing", Generation: &transfer.GenerationLaunch{SourcePath: path, ExplicitProject: explicitProject, Launcher: identity}}
	if request.BodyRef == nil {
		if strings.TrimSpace(request.Prompt) == "" {
			return j, invalid("generation prompt is required")
		}
		// Capture the exact prompt in the existing protected snapshot format. This
		// private temporary file is removed after the immutable copy is flushed.
		temp, e := os.CreateTemp(filepath.Join(state.Root, "snapshots"), "generation-request-*")
		if e != nil {
			return j, e
		}
		name := temp.Name()
		defer os.Remove(name)
		_, e = io.Copy(temp, strings.NewReader(request.Prompt))
		e = errors.Join(e, temp.Sync(), temp.Close())
		if e != nil {
			return j, e
		}
		snapshot, e := state.Capture(ctx, name)
		if e != nil {
			return j, e
		}
		snapshot.Mode = "object"
		snapshot.SelectionName = "generation request"
		j.Result.Snapshot = snapshot
	}
	request.Prompt = ""
	j.Generation.Request, e = json.Marshal(request)
	if e != nil {
		return j, e
	}
	if e = state.Save(j); e != nil {
		return j, e
	}
	if e = state.Remember(j.Selection); e != nil {
		return j, e
	}
	return j, nil
}

func generationDispatchClient(ctx context.Context, opt options, selected *client.Client, state *clientstate.Store, j *transfer.Journal) (*client.Client, error) {
	if j.Generation.DispatchReady {
		current, e := selected.Identity(ctx)
		if e != nil {
			return nil, productError(e)
		}
		if current.LibraryID == j.Identity.LibraryID && current.PrincipalID == j.Identity.PrincipalID {
			return selected, nil
		}
		if j.Generation.CredentialPath == "" {
			return nil, transfer.ErrIntent
		}
	}
	if j.Generation.CredentialPath != "" {
		credential, e := client.LoadCredential(j.Generation.CredentialPath)
		if e != nil {
			return nil, productError(e)
		}
		endpoint := opt.endpoint
		if endpoint == "" {
			endpoint = credential.Endpoint
		}
		api, e := client.New(endpoint, opt.selection.Profile, credential)
		if e != nil {
			return nil, e
		}
		identity, e := api.Identity(ctx)
		if e != nil {
			return nil, productError(e)
		}
		if identity.LibraryID != j.Identity.LibraryID || identity.PrincipalID != j.Identity.PrincipalID {
			return nil, transfer.ErrIntent
		}
		return api, nil
	}
	var actor domain.Session
	if _, e := selected.JSON(ctx, "GET", "/api/v1/session", "", nil, &actor); e != nil {
		return nil, productError(e)
	}
	// An explicitly selected owner API is also valid remotely. Automatic scoped
	// issuance is used only after exact local discovery matches that owner API.
	local := false
	if actor.Scope.Kind == "owner" && opt.endpoint == "" {
		profile, e := service.Load(opt.selection)
		if e == nil {
			discovery, e := service.ReadDiscovery(profile)
			if e == nil && discovery.LibraryID == j.Generation.Launcher.LibraryID {
				ownerPath := opt.flags["credential"]
				if ownerPath == "" {
					ownerPath = filepath.Join(state.Root, "credential.json")
				}
				credential, e := client.LoadCredential(ownerPath)
				if e != nil {
					return nil, e
				}
				local = credential.Endpoint == discovery.Endpoint
			}
		}
	}
	if !local {
		j.Identity = j.Generation.Launcher
		j.Generation.DispatchReady = true
		if e := state.Save(*j); e != nil {
			return nil, e
		}
		return selected, nil
	}
	var request generation.Request
	if json.Unmarshal(j.Generation.Request, &request) != nil {
		return nil, transfer.ErrIntent
	}
	versions := []string{}
	seen := map[string]bool{}
	for _, ref := range request.PriorReferences {
		if !seen[ref.VersionID] {
			versions = append(versions, ref.VersionID)
			seen[ref.VersionID] = true
		}
	}
	selection := workflowSelectionInput{Workflow: "generation", ProjectID: request.ProjectID, DocumentID: request.DocumentID, VersionIDs: versions, ProviderID: request.ProviderID, SourceSelection: func() json.RawMessage { b, _ := json.Marshal(request.Sources); return b }()}
	path, release, e := state.GenerationGrantInput(ctx, j.Generation.Launcher, request.ProjectID, selection)
	if e != nil {
		return nil, e
	}
	defer release()
	grantOpt := opt
	grantOpt.command = []string{"auth", "grant", "create"}
	grantOpt.flags = map[string]string{"input": path, "client-dir": state.Root}
	if value := opt.flags["credential"]; value != "" {
		grantOpt.flags["credential"] = value
	}
	var credentialPath string
	e = grantCommand(ctx, grantOpt, func(value any) error {
		data, e := json.Marshal(value)
		if e != nil {
			return e
		}
		var response struct {
			Path string `json:"credential_path"`
		}
		if json.Unmarshal(data, &response) != nil || response.Path == "" {
			return transfer.ErrIntent
		}
		credentialPath = response.Path
		return nil
	})
	if e != nil {
		return nil, e
	}
	credential, e := client.LoadCredential(credentialPath)
	if e != nil {
		return nil, e
	}
	api, e := client.New(credential.Endpoint, opt.selection.Profile, credential)
	if e != nil {
		return nil, e
	}
	identity, e := api.Identity(ctx)
	if e != nil {
		return nil, productError(e)
	}
	if identity.LibraryID != j.Generation.Launcher.LibraryID {
		return nil, transfer.ErrIntent
	}
	j.Identity = identity
	j.Generation.CredentialPath = credentialPath
	j.Generation.DispatchReady = true
	j.Phase = "prepared"
	if e = state.Save(*j); e != nil {
		return nil, e
	}
	return api, nil
}
func followGeneration(ctx context.Context, api *client.Client, state *clientstate.Store, j transfer.Journal, intent clientstate.CommandIntent, automatic bool, emit func(any) error) error {
	if j.Response == nil {
		return productError(transfer.ErrReconcile)
	}
	var accepted struct {
		Job string `json:"job_id"`
	}
	if json.Unmarshal(j.Response.Body, &accepted) != nil || accepted.Job == "" {
		return productError(transfer.ErrReconcile)
	}
	out := map[string]any{"operation_id": j.OperationID, "result": json.RawMessage(j.Response.Body)}
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		job, e := api.Job(ctx, accepted.Job)
		if e != nil {
			return productError(e)
		}
		out["job_outcome"] = job
		switch job.State {
		case "completed", "failed", "cancelled", "paused":
			if e = emit(out); e != nil {
				return e
			}
			if job.ProviderAttempts != nil && job.ProviderAttempts.Unknown > 0 {
				return commandError{6, fmt.Errorf("generation outcome unknown")}
			}
			// Paused work still owns possible reconciliation/continuation. A
			// routine rerun must recover this K rather than allocate another job.
			if job.State == "paused" {
				return commandError{5, fmt.Errorf("generation paused: %s", job.Error)}
			}
			if automatic {
				if e = state.FinishCommandPending(ctx, j.Generation.Launcher, intent, j.OperationID); e != nil {
					return e
				}
			}
			if job.State == "completed" {
				return nil
			}
			return commandError{5, fmt.Errorf("generation %s: %s", job.State, job.Error)}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}
