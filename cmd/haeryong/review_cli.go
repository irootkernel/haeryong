package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	client "haeryong.local/haeryong/internal/adapters/client"
	"haeryong.local/haeryong/internal/adapters/clientstate"
	"net/url"
	"strconv"
)

func reviewCLIInput(opt options) (string, map[string]string, error) {
	args := opt.command
	if len(args) != 2 || (args[1] != "show" && args[1] != "history" && args[1] != "set") {
		return "", nil, invalid("use review show|history|set")
	}
	if !operationID.MatchString(opt.flags["version"]) {
		return "", nil, invalid("review requires --version UUID")
	}
	path := "/api/v1/versions/" + opt.flags["version"] + "/review"
	conditions := map[string]string{}
	if args[1] == "set" {
		if reviewActionState(opt.flags["action"]) == "" {
			return "", nil, invalid("review requires --action start|complete|reopen|clear")
		}
		match, none := opt.flags["if-match"], opt.flags["if-none-match"]
		if (match == "") == (none == "") || none != "" && none != "*" {
			return "", nil, invalid("review requires one --if-match ETAG or --if-none-match '*'")
		}
		if match != "" {
			conditions["If-Match"] = match
		} else {
			conditions["If-None-Match"] = "*"
		}
		if key := opt.flags["operation-id"]; key != "" && !operationID.MatchString(key) {
			return "", nil, invalid("canonical operation ID required")
		}
	}
	if args[1] == "history" {
		path += "/history"
		values := url.Values{}
		if limit, ok := opt.flags["limit"]; ok {
			n, e := strconv.Atoi(limit)
			if e != nil || n < 1 || n > 200 {
				return "", nil, invalid("--limit must be 1..200")
			}
			values.Set("limit", limit)
		}
		if c := opt.flags["cursor"]; c != "" {
			values.Set("cursor", c)
		}
		if len(values) > 0 {
			path += "?" + values.Encode()
		}
	}
	return path, conditions, nil
}
func reviewActionState(action string) string {
	return map[string]string{"start": "reviewing", "complete": "reviewed", "reopen": "reviewing", "clear": "unreviewed"}[action]
}
func reviewCLI(ctx context.Context, api *client.Client, state *clientstate.Store, opt options, emit func(any) error) error {
	path, conditions, err := reviewCLIInput(opt)
	if err != nil {
		return err
	}
	var capabilities struct {
		Features map[string]bool `json:"features"`
	}
	if _, err := api.JSON(ctx, "GET", "/api/v1/capabilities", "", nil, &capabilities); err != nil {
		return productError(err)
	}
	if !capabilities.Features["review"] {
		return commandError{5, fmt.Errorf("FEATURE_NOT_READY: personal Review")}
	}
	if opt.command[1] != "set" {
		var data json.RawMessage
		_, err := api.JSON(ctx, "GET", path, "", nil, &data)
		if err != nil {
			return productError(err)
		}
		return emit(data)
	}
	body := map[string]string{"action": opt.flags["action"], "state": reviewActionState(opt.flags["action"])}
	result, key, err := mutationConditions(ctx, api, state, opt.flags["operation-id"], "PUT", path, body, conditions)
	finish := func() error {
		if opt.flags["operation-id"] != "" {
			return nil
		}
		return finishMutation(context.Background(), state, clientstate.CommandIntent{Method: "PUT", Target: path}, key)
	}
	if err != nil {
		err = afterDefinitive(recordedError(productError(err), result, key, nil), key, finish)
		var recorded *recordedCommandError
		if key != "" && !errors.As(err, &recorded) {
			return &pendingReviewError{cause: err, operation: key}
		}
		return err
	}
	if err = emit(map[string]any{"operation_id": key, "result": json.RawMessage(result.Body)}); err != nil {
		return err
	}
	return finish()
}

// Unknown dispatch outcomes retain their pending key without acknowledging a failure.
type pendingReviewError struct {
	cause     error
	operation string
}

func (e *pendingReviewError) Error() string              { return e.cause.Error() }
func (e *pendingReviewError) Unwrap() error              { return e.cause }
func (e *pendingReviewError) PendingOperationID() string { return e.operation }
