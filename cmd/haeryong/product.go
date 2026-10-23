package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	client "haeryong.local/haeryong/internal/adapters/client"
	"haeryong.local/haeryong/internal/adapters/clientstate"
	config "haeryong.local/haeryong/internal/application/configuration"
	importer "haeryong.local/haeryong/internal/application/import"
	"haeryong.local/haeryong/internal/application/service"
	"haeryong.local/haeryong/internal/application/transfer"
	"haeryong.local/haeryong/internal/domain"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

func isProductCommand(opt options) bool {
	if len(opt.command) >= 2 && opt.command[0] == "auth" && opt.command[1] == "sessions" {
		return true
	}
	switch opt.command[0] {
	case "backlinks", "indexes", "references", "search", "import", "package", "uploads", "documents", "files", "operations", "jobs", "config", "projects", "events", "sources", "evidence", "assessments":
		return true
	case "doctor", "status":
		if opt.flags["credential"] != "" || opt.flags["client-dir"] != "" {
			return true
		}
		root, e := clientDirectory(opt)
		if e != nil {
			return false
		}
		_, e = os.Stat(filepath.Join(root, "credential.json"))
		return e == nil
	}
	return false
}
func clientDirectory(opt options) (string, error) {
	if v := opt.flags["client-dir"]; v != "" {
		return filepath.Abs(v)
	}
	home, e := os.UserConfigDir()
	if e != nil {
		return "", e
	}
	return filepath.Join(home, "haeryong", "clients", opt.selection.Profile), nil
}
func selectedClient(ctx context.Context, opt options) (*client.Client, *clientstate.Store, error) {
	root, e := clientDirectory(opt)
	if e != nil {
		return nil, nil, e
	}
	state, e := clientstate.New(root)
	if e != nil {
		return nil, nil, e
	}
	path := opt.flags["credential"]
	if path == "" {
		path = filepath.Join(root, "credential.json")
	}
	credential, e := client.LoadCredential(path)
	if e != nil {
		return nil, nil, commandError{3, e}
	}
	endpoint := opt.endpoint
	if endpoint == "" {
		endpoint = credential.Endpoint
	}
	api, e := client.New(endpoint, opt.selection.Profile, credential)
	if e != nil {
		return nil, nil, commandError{3, e}
	}
	if _, e = api.Identity(ctx); e != nil {
		return nil, nil, productError(e)
	}
	return api, state, nil
}
func productError(err error) error {
	if err == nil {
		return nil
	}
	var api *client.Error
	if errors.As(err, &api) {
		code := 5
		switch {
		case api.Code == "OPERATION_RECONCILIATION_REQUIRED" || api.Code == "PROVIDER_OUTCOME_UNKNOWN":
			code = 6
		case api.Code == "SOURCE_UNAVAILABLE" || api.Code == "REVISION_UNAVAILABLE":
			code = 5
		case api.Status == 400 || api.Status == 422:
			code = 2
		case api.Status == 401 || api.Status == 403 || api.Status == 404:
			code = 3
		case api.Status == 416:
			code = 2
		case api.Status == 409 || api.Status == 410 || api.Status == 412 || api.Status == 428:
			code = 4
		}
		return commandError{code, err}
	}
	if errors.Is(err, transfer.ErrInvalid) {
		return commandError{2, err}
	}
	if errors.Is(err, transfer.ErrIntent) || errors.Is(err, domain.ErrConflict) || errors.Is(err, importer.ErrInputChanged) {
		return commandError{4, err}
	}
	if errors.Is(err, transfer.ErrReconcile) {
		return commandError{6, err}
	}
	return err
}
func productCommand(ctx context.Context, opt options, emit func(any) error) error {
	api, state, e := selectedClient(ctx, opt)
	if e != nil {
		return e
	}
	call := func(method, path string, body any) error {
		if method == "GET" {
			var data json.RawMessage
			_, e := api.JSON(ctx, method, path, "", body, &data)
			if e != nil {
				return productError(e)
			}
			return emit(data)
		}
		result, key, e := mutation(ctx, api, state, opt.flags["operation-id"], method, path, body)
		finish := func() error {
			if opt.flags["operation-id"] != "" {
				return nil
			}
			return finishMutation(context.Background(), state, clientstate.CommandIntent{Method: method, Target: path}, key)
		}
		if e != nil {
			return recordedError(productError(e), result, key, finish)
		}
		if e = emit(map[string]any{"operation_id": key, "result": json.RawMessage(result.Body)}); e != nil {
			return e
		}
		return finish()
	}

	args := opt.command
	switch args[0] {
	case "backlinks", "indexes":
		path, err := connectedCLIPath(opt)
		if err != nil {
			return err
		}
		if args[0] == "indexes" && args[1] == "rebuild" {
			return call("POST", path, map[string]string{"kind": opt.flags["kind"]})
		}
		return call("GET", path, nil)
	case "references":
		return referenceCLI(ctx, api, opt, emit)
	case "search":
		if len(args) != 1 {
			return invalid("search accepts flags only")
		}
		values := url.Values{}
		for flag, key := range map[string]string{"query": "q", "project": "project_id", "lifecycle": "lifecycle", "versions": "versions", "kind": "kind", "cursor": "cursor", "limit": "limit"} {
			if v, ok := opt.flags[flag]; ok {
				values.Set(key, v)
			}
		}
		return call("GET", "/api/v1/search?"+values.Encode(), nil)

	case "import", "package":
		validate := args[0] == "package"
		position := 1
		if validate {
			if len(args) < 2 || args[1] != "validate" {
				return invalid("use package validate PATH")
			}
			position = 2
		}
		if len(args) != position+1 {
			return invalid("input PATH required")
		}
		input := transfer.Input{Path: args[position], OperationID: opt.flags["operation-id"], ProjectID: opt.flags["project"], DocumentID: opt.flags["document"], NewDocument: opt.flags["new-document"] == "true", BaseVersionID: opt.flags["base-version"], VersionID: opt.flags["version"], Metadata: opt.flags["metadata"], Validate: validate}
		if input.NewDocument && input.DocumentID != "" {
			return invalid("--new-document and --document conflict")
		}
		journal, e := transfer.New(api, state).Run(ctx, input)
		if e != nil {
			if journal.Response != nil && journal.Response.Status >= 400 {
				return recordedError(productError(e), *journal.Response, journal.OperationID, func() error {
					if input.OperationID != "" {
						return nil
					}
					return state.FinishPending(ctx, journal.Identity, input, journal.OperationID)
				})
			}
			return productError(e)
		}
		out := map[string]any{"operation_id": journal.OperationID, "upload_id": journal.Result.UploadID, "upload_create_operation_id": journal.Result.CreateKey, "upload_complete_operation_id": journal.Result.CompleteKey, "dispatch_phase": journal.Phase, "selection": journal.Selection, "base_version_id": journal.BaseVersionID, "result": json.RawMessage(journal.Response.Body)}

		if !validate && journal.Response.Status == 202 {
			var creation struct {
				JobID string `json:"job_id"`
			}
			if e = json.Unmarshal(journal.Response.Body, &creation); e != nil {
				return e
			}
			if creation.JobID != "" {
				ticker := time.NewTicker(100 * time.Millisecond)
				defer ticker.Stop()
				for {
					job, e := api.Job(ctx, creation.JobID)
					if e != nil {
						return productError(e)
					}
					out["job_outcome"] = job
					if job.State == "completed" {
						var published struct {
							Document string `json:"document_id"`
							Version  string `json:"version_id"`
						}
						if e = json.Unmarshal(job.Result, &published); e != nil {
							return e
						}
						out["document_id"], out["version_id"] = published.Document, published.Version
						if published.Document != "" {
							if e = state.Remember(transfer.Selection{ProjectID: journal.Selection.ProjectID, DocumentID: &published.Document}); e != nil {
								return e
							}
						}
						break
					}
					if job.State == "failed" || job.State == "cancelled" {
						if e = emit(out); e != nil {
							return e
						}
						if e = state.FinishPending(ctx, journal.Identity, input, journal.OperationID); e != nil {
							return e
						}
						return commandError{5, fmt.Errorf("import %s: %s", job.State, job.Error)}
					}
					select {
					case <-ctx.Done():
						return productError(transfer.ErrReconcile)
					case <-ticker.C:
					}
				}
			}
		}
		if e = emit(out); e != nil {
			return e
		}
		return state.FinishPending(ctx, journal.Identity, input, journal.OperationID)
	case "documents":
		if len(args) != 3 || args[1] != "show" {
			return invalid("use documents show ID")
		}
		if opt.flags["version"] != "" {
			var version json.RawMessage
			if _, e := api.JSON(ctx, "GET", "/api/v1/versions/"+opt.flags["version"], "", nil, &version); e != nil {
				return productError(e)
			}
			var binding struct {
				DocumentID string `json:"document_id"`
			}
			if e := json.Unmarshal(version, &binding); e != nil {
				return e
			}
			if binding.DocumentID != args[2] {
				return productError(&client.Error{Status: 404, Code: "NOT_FOUND"})
			}
			return emit(version)
		}
		return call("GET", "/api/v1/documents/"+args[2], nil)
	case "projects":
		if len(args) == 2 && args[1] == "register" {
			return sourceCommand(ctx, opt, api, state, emit)
		}
		if len(args) == 2 && args[1] == "list" {
			values := url.Values{}
			for _, name := range []string{"cursor", "limit"} {
				if value := opt.flags[name]; value != "" {
					values.Set(name, value)
				}
			}
			return call("GET", "/api/v1/projects?"+values.Encode(), nil)
		}
	case "sources", "evidence", "assessments":
		return sourceCommand(ctx, opt, api, state, emit)
	case "operations":
		if len(args) == 3 && args[1] == "show" {
			return call("GET", "/api/v1/operations/"+args[2], nil)
		}
	case "jobs":
		if len(args) == 2 && args[1] == "list" {
			return call("GET", "/api/v1/jobs", nil)
		}
		if len(args) == 3 && args[1] == "show" {
			return call("GET", "/api/v1/jobs/"+args[2], nil)
		}
		if len(args) == 3 && args[1] == "cancel" {
			return call("POST", "/api/v1/jobs/"+args[2]+"/cancel", map[string]any{})
		}
	case "uploads":
		if len(args) == 3 && args[1] == "resume" {
			journal, e := transfer.New(api, state).Resume(ctx, args[2], opt.flags["input"], opt.flags["operation-id"])
			if e != nil {
				return productError(e)
			}
			if e = emit(map[string]any{"upload_id": args[2], "state": journal.Result.Phase, "operation_id": journal.Result.CompleteKey, "checkpoint": journal.Result.Checkpoint}); e != nil {
				return e
			}
			if opt.flags["operation-id"] == "" {
				return state.FinishPending(ctx, journal.Identity, transfer.Input{ResumeUploadID: args[2]}, journal.Result.CompleteKey)
			}
			return nil
		}

		if len(args) == 2 && args[1] == "list" {
			query := url.Values{}
			for _, k := range []string{"state", "cursor", "unadopted"} {
				if v := opt.flags[k]; v != "" {
					query.Set(k, v)
				}
			}
			return call("GET", "/api/v1/uploads?"+query.Encode(), nil)
		}
		if len(args) == 3 && args[1] == "show" {
			return call("GET", "/api/v1/uploads/"+args[2], nil)
		}
		if len(args) == 3 && args[1] == "abandon" {
			return call("POST", "/api/v1/uploads/"+args[2]+"/abandon", map[string]any{})
		}
	case "files":
		return fileCommand(ctx, opt, api, emit)
	case "config":
		return configCommand(ctx, opt, api, state, emit)
	case "doctor", "status":
		if _, e = api.Identity(ctx); e != nil {
			return productError(e)
		}
		return call("GET", "/api/v1/status", nil)
	case "events":
		return eventCommand(ctx, opt, api, emit)
	case "auth":
		if len(args) == 3 && args[1] == "sessions" && args[2] == "list" {
			return call("GET", "/api/v1/sessions", nil)
		}
		if len(args) == 4 && args[1] == "sessions" && args[2] == "revoke" {
			response, e := api.JSON(ctx, "DELETE", "/api/v1/sessions/"+args[3], "", nil, nil)
			if e != nil {
				return productError(e)
			}
			if response.Status != 204 {
				return commandError{5, errors.New("unexpected revoke response")}
			}
			return emit(map[string]any{"session_id": args[3], "revoked": true})
		}
	}
	return commandError{5, fmt.Errorf("FEATURE_NOT_READY: %s", strings.Join(args, " "))}
}
func configCommand(ctx context.Context, opt options, api *client.Client, state *clientstate.Store, emit func(any) error) error {
	args := opt.command
	if len(args) < 2 {
		return invalid("use config show/plan/apply/export/import")
	}
	if args[1] == "show" {
		var value any
		_, e := api.JSON(ctx, "GET", "/api/v1/configuration", "", nil, &value)
		if e != nil {
			return productError(e)
		}
		return emit(value)
	}
	if args[1] == "export" {
		var view config.View
		_, e := api.JSON(ctx, "GET", "/api/v1/configuration", "", nil, &view)
		if e != nil {
			return productError(e)
		}
		value, e := config.Decode(strings.NewReader(string(view.Value)), "json")
		if e != nil {
			return e
		}
		data, e := config.EncodeYAML(value)
		if e != nil {
			return e
		}
		if path := opt.flags["output"]; path != "" {
			f, e := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
			if e != nil {
				return e
			}
			_, e = f.Write(data)
			e = errors.Join(e, f.Sync(), f.Close())
			if e != nil {
				return e
			}
			return emit(map[string]any{"exported": path, "config_revision": view.ConfigRevision})
		}
		return emit(map[string]any{"format": "yaml", "snapshot": string(data), "config_revision": view.ConfigRevision})
	}
	if args[1] == "apply" {
		if len(args) != 3 || opt.flags["confirmed"] != "true" {
			return invalid("config apply PLAN_ID --expected-config-revision REV --confirmed")
		}
		revision, e := strconv.ParseInt(opt.flags["expected-config-revision"], 10, 64)
		if e != nil {
			return invalid("expected config revision required")
		}
		key := opt.flags["operation-id"]
		if key == "" {
			key = args[2]
		}
		response, id, e := mutation(ctx, api, state, key, "POST", "/api/v1/configuration/plans/"+args[2]+"/commit", config.CommitRequest{ExpectedRevision: revision, Confirmed: true})
		if e != nil {
			return productError(e)
		}
		return emit(map[string]any{"operation_id": id, "result": json.RawMessage(response.Body)})
	}
	if args[1] == "plan" || args[1] == "import" {
		path := opt.flags["file"]
		if path == "" && len(args) == 3 {
			path = args[2]
		}
		if path == "" || len(args) > 3 {
			return invalid("configuration input file required")
		}
		scope := config.Scope{Kind: "library"}
		if project := opt.flags["project"]; project != "" {
			scope = config.Scope{Kind: "project", ProjectID: project}
		}
		identity, e := api.Identity(ctx)
		if e != nil {
			return productError(e)
		}
		intent := clientstate.CommandIntent{Method: "POST", Target: "/api/v1/configuration/plans", SourcePath: path, ProjectID: opt.flags["project"], ExpectedRevision: opt.flags["expected-config-revision"]}
		id := opt.flags["operation-id"]
		if id != "" && !operationID.MatchString(id) {
			return invalid("canonical operation ID required")
		}
		if id == "" {
			candidate, e := service.NewID()
			if e != nil {
				return e
			}
			id, e = state.UseCommandPending(ctx, identity, intent, candidate)
			if e != nil {
				return e
			}
		}
		var body any
		saved, e := state.Load(id)
		if e == nil {
			if saved.Identity.LibraryID != identity.LibraryID || saved.Identity.PrincipalID != identity.PrincipalID || saved.Method != intent.Method || saved.Target != intent.Target {
				return productError(transfer.ErrIntent)
			}
			var original config.PlanRequest
			decoder := json.NewDecoder(strings.NewReader(string(saved.FinalBody)))
			decoder.UseNumber()
			if e = decoder.Decode(&original); e != nil {
				return e
			}
			if explicit := opt.flags["expected-config-revision"]; explicit != "" && explicit != strconv.FormatInt(original.ExpectedRevision, 10) {
				return productError(transfer.ErrIntent)
			}
			if opt.flags["project"] != "" && opt.flags["project"] != original.Scope.ProjectID {
				return productError(transfer.ErrIntent)
			}
			if opt.flags["scope"] != "" && opt.flags["scope"] != original.Scope.Kind {
				return productError(transfer.ErrIntent)
			}
			body = json.RawMessage(saved.FinalBody)
		} else if errors.Is(e, domain.ErrNotFound) {
			f, e := os.Open(path)
			if e != nil {
				return e
			}
			format := opt.flags["format"]
			if format == "" {
				format = "yaml"
				if strings.EqualFold(filepath.Ext(path), ".json") {
					format = "json"
				}
			}
			changes, e := config.Decode(f, format)
			closeError := f.Close()
			if e != nil {
				return commandError{2, e}
			}
			if closeError != nil {
				return closeError
			}
			var view config.View
			if _, e = api.JSON(ctx, "GET", "/api/v1/configuration", "", nil, &view); e != nil {
				return productError(e)
			}
			revision := view.ConfigRevision
			if explicit := opt.flags["expected-config-revision"]; explicit != "" {
				revision, e = strconv.ParseInt(explicit, 10, 64)
				if e != nil || revision < 1 {
					return invalid("positive expected config revision required")
				}
			}
			body = config.PlanRequest{Scope: scope, ExpectedRevision: revision, Changes: changes}
		} else {
			return e
		}
		response, id, e := mutation(ctx, api, state, id, intent.Method, intent.Target, body)
		finish := func() error {
			if opt.flags["operation-id"] != "" {
				return nil
			}
			return state.FinishCommandPending(context.Background(), identity, intent, id)
		}
		if e != nil {
			return recordedError(productError(e), response, id, finish)
		}
		if e = emit(map[string]any{"operation_id": id, "result": json.RawMessage(response.Body)}); e != nil {
			return e
		}
		return finish()
	}
	return commandError{5, errors.New("FEATURE_NOT_READY")}
}
func fileCommand(ctx context.Context, opt options, api *client.Client, emit func(any) error) error {
	if len(opt.command) != 2 || opt.flags["version"] == "" {
		return invalid("files list/get/download --version ID")
	}
	version := opt.flags["version"]
	if opt.command[1] == "list" {
		cursor := opt.flags["cursor"]
		for {
			values := url.Values{}
			for _, k := range []string{"directory", "kind", "limit"} {
				if v := opt.flags[k]; v != "" {
					values.Set(k, v)
				}
			}
			if cursor != "" {
				values.Set("cursor", cursor)
			}
			var page struct {
				Items []json.RawMessage `json:"items"`
				Next  *string           `json:"next_cursor"`
			}
			_, e := api.JSON(ctx, "GET", "/api/v1/versions/"+version+"/files?"+values.Encode(), "", nil, &page)
			if e != nil {
				return productError(e)
			}
			for _, item := range page.Items {
				if e = emit(item); e != nil {
					return e
				}
			}
			if page.Next == nil {
				return nil
			}
			cursor = *page.Next
		}
	}
	if opt.command[1] == "download" {
		if opt.flags["output"] == "" {
			return invalid("files download requires --output")
		}
		out, e := api.DownloadDirectory(ctx, version, opt.flags["output"])
		if e != nil {
			return productError(e)
		}
		return emit(out)
	}
	if opt.command[1] == "get" {
		if opt.flags["path"] == "" || opt.flags["output"] == "" {
			return invalid("files get requires --path and --output")
		}
		out, metadata, e := api.Download(ctx, version, opt.flags["path"], opt.flags["output"], opt.flags["resolve-link"] == "true")
		if e != nil {
			return productError(e)
		}
		if metadata != nil {
			return emit(metadata)
		}
		return emit(out)
	}

	return commandError{5, errors.New("FEATURE_NOT_READY")}
}
