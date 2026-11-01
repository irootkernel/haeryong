package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"path/filepath"
	"regexp"

	"haeryong.local/haeryong/internal/adapters/clientstate"
	importer "haeryong.local/haeryong/internal/application/import"
	"haeryong.local/haeryong/internal/application/service"
	"haeryong.local/haeryong/internal/application/transfer"
	"haeryong.local/haeryong/internal/domain"
)

var draftETag = regexp.MustCompile(`^"draft-[0-9a-f]{64}"$`)

func draftCommand(ctx context.Context, opt options, emit func(any) error) (resultErr error) {
	args := opt.command
	if len(args) < 2 {
		return invalid("use drafts list/show/history/reject/replace/publish")
	}
	verb := args[1]
	api, state, e := selectedClient(ctx, opt)
	if e != nil {
		return e
	}
	path := "/api/v1/drafts"
	q := url.Values{}
	if verb == "list" {
		if len(args) != 2 {
			return invalid("drafts list takes no draft ID")
		}
		if project := opt.flags["project"]; project != "" {
			q.Set("project_id", project)
		}
	} else {
		if len(args) != 3 || !operationID.MatchString(args[2]) {
			return invalid("canonical draft ID required")
		}
		path += "/" + args[2]
	}
	switch verb {
	case "list", "show", "history":
		if verb == "history" {
			path += "/history"
		}
		if v := opt.flags["cursor"]; v != "" {
			q.Set("cursor", v)
		}
		if q.Encode() != "" {
			path += "?" + q.Encode()
		}
		var body json.RawMessage
		if _, e = api.JSON(ctx, "GET", path, "", nil, &body); e != nil {
			return productError(e)
		}
		return emit(body)
	case "reject", "replace", "publish":
	default:
		return invalid("unknown drafts command")
	}
	method := "PATCH"
	if verb == "publish" {
		method = "POST"
		path += "/publish"
	}
	sourcePath, metadataPath := "", ""
	if verb == "replace" {
		if opt.flags["input"] == "" {
			return invalid("drafts replace requires --input whole-result-directory-or-archive")
		}
		sourcePath, e = filepath.Abs(opt.flags["input"])
		if e != nil {
			return e
		}
		if opt.flags["metadata"] != "" {
			metadataPath, e = filepath.Abs(opt.flags["metadata"])
			if e != nil {
				return e
			}
		}
	}
	explicit := opt.flags["if-match"]
	if explicit != "" && !draftETag.MatchString(explicit) {
		return invalid("strong individual draft If-Match required")
	}
	identity, e := api.Identity(ctx)
	if e != nil {
		return productError(e)
	}
	intent := clientstate.CommandIntent{Method: method, Target: path, SourcePath: sourcePath, ExpectedRevision: explicit, MetadataPath: metadataPath}
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
	defer func() {
		if release != nil {
			_ = release()
		}
	}()
	j, e := state.Load(key)
	if errors.Is(e, domain.ErrNotFound) {
		var draft importer.DraftView
		if _, e = api.JSON(ctx, "GET", "/api/v1/drafts/"+args[2], "", nil, &draft); e != nil {
			return productError(e)
		}
		etag := draft.ETag
		if explicit != "" {
			etag = explicit
		}
		if !draftETag.MatchString(etag) {
			return productError(transfer.ErrReconcile)
		}
		j = transfer.Journal{OperationID: key, Purpose: "draft_" + verb, Method: method, Target: path, Identity: identity, Selection: transfer.Selection{ProjectID: draft.ProjectID, DocumentID: draft.DocumentID}, Preconditions: map[string]string{"If-Match": etag}, Phase: "preparing", Draft: &transfer.DraftLaunch{ID: args[2], Action: verb, SourcePath: sourcePath, MetadataPath: metadataPath, ExplicitETag: explicit}}
		if e = state.Save(j); e != nil {
			return e
		}
	} else if e != nil {
		return e
	} else {
		if j.Draft == nil || j.Purpose != "draft_"+verb || j.Method != method || j.Target != path || j.Identity.LibraryID != identity.LibraryID || j.Identity.PrincipalID != identity.PrincipalID || j.Draft.SourcePath != sourcePath || j.Draft.MetadataPath != metadataPath || j.Draft.ExplicitETag != explicit {
			return transfer.ErrIntent
		}
	}
	var acknowledge func() error
	if opt.flags["operation-id"] == "" {
		original := j.Identity
		acknowledge = func() error { return state.FinishCommandPending(context.Background(), original, intent, key) }
	}
	if j.FinalBody != nil {
		response, lookup := transfer.AwaitOperation(ctx, api, key)
		if lookup == nil {
			j.Response = &response
			if e = state.Save(j); e != nil {
				return e
			}
		} else if !errors.Is(lookup, domain.ErrNotFound) {
			if response.Status >= 400 {
				j.Response, j.Phase = &response, "failed"
				if e = state.Save(j); e != nil {
					return e
				}
			}
			if e = release(); e != nil {
				return e
			}
			release = nil
			return afterDefinitive(recordedError(productError(lookup), response, key, nil), key, acknowledge)
		} else if j.Response != nil || j.Identity.RuntimeGeneration != identity.RuntimeGeneration {
			return productError(transfer.ErrReconcile)
		}
	}
	if j.FinalBody == nil {
		body := map[string]any{}
		if verb != "publish" {
			body["action"] = verb
		}
		if verb == "replace" {
			upload, metadata, e := transfer.New(api, state).PrepareDraftReplacement(ctx, &j)
			if e != nil {
				return productError(e)
			}
			body["upload_id"] = upload
			if metadata != nil {
				body["metadata_ref"] = metadata
			}
		}
		j.FinalBody, e = json.Marshal(body)
		if e != nil {
			return e
		}
		j.Phase = "final_dispatch"
		if e = state.Save(j); e != nil {
			return e
		}
	}
	if e = release(); e != nil {
		return e
	}
	release = nil
	if j.Response == nil {
		response, id, e := mutationConditions(ctx, api, state, key, method, path, json.RawMessage(j.FinalBody), j.Preconditions)
		if e != nil {
			return afterDefinitive(recordedError(productError(e), response, id, nil), id, acknowledge)
		}
		j.Response = &response
	}
	if e = emit(map[string]any{"operation_id": key, "result": json.RawMessage(j.Response.Body)}); e != nil {
		return e
	}
	if opt.flags["operation-id"] == "" {
		return state.FinishCommandPending(ctx, identity, intent, key)
	}
	return nil
}
