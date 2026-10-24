package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	client "haeryong.local/haeryong/internal/adapters/client"
	"haeryong.local/haeryong/internal/adapters/clientstate"
)

func revisitCLIInput(opt options) (string, map[string]string, error) {
	args := opt.command
	if len(args) != 2 || (args[1] != "show" && args[1] != "set") {
		return "", nil, invalid("use revisit show|set")
	}
	if !operationID.MatchString(opt.flags["document"]) {
		return "", nil, invalid("revisit requires --document UUID")
	}
	path := "/api/v1/documents/" + opt.flags["document"] + "/revisit"
	conditions := map[string]string{}
	if args[1] == "set" {
		if opt.flags["flag"] != "true" && opt.flags["flag"] != "false" {
			return "", nil, invalid("revisit requires --flag true|false")
		}
		match, none := opt.flags["if-match"], opt.flags["if-none-match"]
		if (match == "") == (none == "") || none != "" && none != "*" {
			return "", nil, invalid("revisit requires one --if-match ETAG or --if-none-match '*'")
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

	return path, conditions, nil
}
func revisitCLI(ctx context.Context, api *client.Client, state *clientstate.Store, opt options, emit func(any) error) error {
	path, conditions, err := revisitCLIInput(opt)
	if err != nil {
		return err
	}
	var capabilities struct {
		Features map[string]bool `json:"features"`
	}
	if _, err := api.JSON(ctx, "GET", "/api/v1/capabilities", "", nil, &capabilities); err != nil {
		return productError(err)
	}
	if !capabilities.Features["revisit"] {
		return commandError{5, fmt.Errorf("FEATURE_NOT_READY: Revisit")}
	}
	if opt.command[1] != "set" {
		var data json.RawMessage
		_, err := api.JSON(ctx, "GET", path, "", nil, &data)
		if err != nil {
			return productError(err)
		}
		return emit(data)
	}
	body := map[string]bool{"flag": opt.flags["flag"] == "true"}
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
