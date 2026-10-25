package main

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"

	"haeryong.local/haeryong/internal/adapters/client"
	"haeryong.local/haeryong/internal/adapters/clientstate"
	"haeryong.local/haeryong/internal/adapters/foundation"
	"haeryong.local/haeryong/internal/adapters/localcontrol"
	config "haeryong.local/haeryong/internal/application/configuration"
	"haeryong.local/haeryong/internal/application/recovery"
	"haeryong.local/haeryong/internal/application/service"
	"haeryong.local/haeryong/internal/domain"
)

func copyCommand(ctx context.Context, opt options, emit func(any) error) error {
	args := opt.command
	if len(args) < 2 {
		return invalid("copy subcommand required")
	}
	family, verb := args[0], args[1]
	if family == "export" && verb == "show" {
		verb = "plan.show"
	}
	if family == "export" && verb == "verify" {
		if len(args) != 2 || opt.flags["input"] == "" {
			return invalid("export verify --input PATH [--index PATH]")
		}
		result, e := foundation.VerifyExport(ctx, opt.flags["input"], opt.flags["index"])
		if e != nil {
			return commandError{2, e}
		}
		return emit(result)
	}
	if family == "backup" && verb == "verify" {
		if len(args) != 2 || opt.flags["input"] == "" {
			return invalid("backup verify --input SET")
		}
		m, e := foundation.VerifyBackup(ctx, opt.flags["input"])
		if e != nil {
			return commandError{2, e}
		}
		return emit(map[string]any{"valid": true, "manifest": m})
	}
	if family == "restore" {
		if opt.endpoint != "" {
			return invalid("restore requires an explicit local destination profile")
		}
		if !opt.profileSelected && opt.selection.DataDir == "" {
			return invalid("restore requires explicit --profile or --data-dir")
		}
		switch verb {
		case "plan":
			if len(args) != 2 || opt.flags["input"] == "" {
				return invalid("restore plan --input SET --profile DEST")
			}
			plan, e := foundation.PlanRestore(ctx, opt.selection, opt.flags["input"])
			if e != nil {
				return copyError(e)
			}
			return emit(plan)
		case "apply":
			if len(args) != 3 || !operationID.MatchString(args[2]) || opt.flags["confirm"] != "true" {
				return invalid("restore apply PLAN_ID --confirm --profile DEST")
			}
			result, e := foundation.ApplyRestore(ctx, opt.selection, args[2], config.Validate, opt.flags["operation-id"])
			if e != nil {
				return copyError(e)
			}
			return emit(result)
		case "review":
			return reviewRestoredConfiguration(ctx, opt, emit)
		default:
			return invalid("restore supports plan, apply and review")
		}
	}
	r := domain.CopyRequest{Action: family + "." + verb, Destination: opt.flags["destination"], MetadataDestination: opt.flags["metadata-destination"], VersionID: opt.flags["version"], DocumentID: opt.flags["document"], ProjectID: opt.flags["project"], Format: opt.flags["format"], Versions: opt.flags["versions"], Lifecycle: opt.flags["lifecycle"], RetainedSources: opt.flags["include-retained-sources"] == "true", Cursor: opt.flags["cursor"], Confirmed: opt.flags["confirm"] == "true"}
	if opt.flags["records"] != "" {
		r.Records = strings.Split(opt.flags["records"], ",")
	}
	if len(args) == 3 {
		r.ID = args[2]
	} else if len(args) != 2 {
		return invalid("invalid local copy command")
	}

	if e := recovery.Validate(&r); e != nil {
		if errors.Is(e, recovery.ErrRecordUnavailable) {
			return commandError{2, e}
		}
		return invalid("invalid local copy selection/options")
	}
	p, e := service.Load(opt.selection)
	if e != nil {
		return e
	}
	discovery, e := service.ReadDiscovery(p)
	if e != nil {
		return e
	}
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
	if credential.Endpoint != discovery.Endpoint || credential.LibraryID != p.LibraryID || credential.Profile != p.Name || opt.endpoint != "" && opt.endpoint != discovery.Endpoint {
		return invalid("local copy requires the selected protected profile's exact loopback daemon")
	}
	api, state, e := selectedClient(ctx, opt)
	if e != nil {
		return e
	}
	if r.Action == "export.plan.show" {
		capabilities, e := api.Identity(ctx)
		if e != nil {
			return productError(e)
		}
		r.Runtime = capabilities.RuntimeGeneration
		response, e := localcontrol.CopyCall(ctx, p, discovery, credential.Token, "", r)
		if e != nil {
			return productError(e)
		}
		return emit(json.RawMessage(response.Body))
	}
	result, key, e := mutationDispatch(ctx, api, state, opt.flags["operation-id"], "POST", "/local/v1/copy-requests", r, nil, func(id string, body []byte) (domain.Response, error) {
		var request domain.CopyRequest
		if e := json.Unmarshal(body, &request); e != nil {
			return domain.Response{}, e
		}
		journal, e := state.Load(id)
		if e != nil {
			return domain.Response{}, e
		}
		request.Runtime = journal.Identity.RuntimeGeneration
		response, e := localcontrol.CopyCall(ctx, p, discovery, credential.Token, id, request)
		var failure *localcontrol.BootstrapError
		if errors.As(e, &failure) {
			e = &client.Error{Status: failure.Status, Code: failure.Code}
		}
		return response, e
	})
	finish := func() error {
		if opt.flags["operation-id"] != "" {
			return nil
		}
		return finishMutation(context.Background(), state, clientstate.CommandIntent{Method: "POST", Target: "/local/v1/copy-requests", CopyAction: r.Action}, key)
	}
	if e != nil {
		return recordedError(productError(e), result, key, finish)
	}
	if e = emit(map[string]any{"operation_id": key, "result": json.RawMessage(result.Body)}); e != nil {
		return e
	}
	return finish()
}

func reviewRestoredConfiguration(ctx context.Context, opt options, emit func(any) error) error {
	// Offline recovery owns the normal profile lock. Review itself uses the
	// existing configuration plan/commit authority with a fresh owner credential.
	return copyError(reviewRecovery(ctx, opt, emit))
}

func copyError(e error) error {
	if e == nil {
		return nil
	}
	switch {
	case errors.Is(e, domain.ErrConflict), errors.Is(e, domain.ErrPrecondition), errors.Is(e, domain.ErrTransition):
		return commandError{4, e}
	case errors.Is(e, domain.ErrReconciliation), errors.Is(e, domain.ErrStaleWorker):
		return commandError{6, e}
	case errors.Is(e, domain.ErrInvalidTarget), errors.Is(e, domain.ErrContentIntegrity):
		return commandError{2, e}
	case errors.Is(e, domain.ErrDenied):
		return commandError{3, e}
	}
	return productError(e)
}
