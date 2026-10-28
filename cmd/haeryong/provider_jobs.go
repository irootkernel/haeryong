package main

import (
	"context"
	"encoding/json"
	client "haeryong.local/haeryong/internal/adapters/client"
	"haeryong.local/haeryong/internal/adapters/clientstate"
	"strconv"
)

func resumeProviderInput(opt options) (map[string]any, error) {
	if len(opt.command) != 3 || !operationID.MatchString(opt.command[2]) {
		return nil, invalid("jobs resume UUID requires an exact job")
	}
	decision := opt.flags["decision"]
	if decision != "reconcile_only" && decision != "continue" && decision != "retry" {
		return nil, invalid("--decision reconcile_only|continue|retry required")
	}
	g, e := strconv.ParseInt(opt.flags["expected-attempt-generation"], 10, 64)
	if e != nil || g < 1 || opt.flags["if-match"] == "" {
		return nil, invalid("--expected-attempt-generation N and --if-match ETAG required")
	}
	return map[string]any{"decision": decision, "expected_attempt_generation": g}, nil
}
func resumeProviderCLI(ctx context.Context, api *client.Client, state *clientstate.Store, opt options, emit func(any) error) error {
	body, e := resumeProviderInput(opt)
	if e != nil {
		return e
	}
	path := "/api/v1/jobs/" + opt.command[2] + "/resume"
	result, key, e := mutationConditions(ctx, api, state, opt.flags["operation-id"], "POST", path, body, map[string]string{"If-Match": opt.flags["if-match"]})
	finish := func() error {
		if opt.flags["operation-id"] != "" {
			return nil
		}
		return finishMutation(context.Background(), state, clientstate.CommandIntent{Method: "POST", Target: path}, key)
	}
	if e != nil {
		return afterDefinitive(recordedError(productError(e), result, key, nil), key, finish)
	}
	if e = emit(map[string]any{"operation_id": key, "result": json.RawMessage(result.Body)}); e != nil {
		return e
	}
	return finish()
}
