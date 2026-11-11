package main

import (
	"context"
	"encoding/json"
	"errors"
	client "haeryong.local/haeryong/internal/adapters/client"
	"haeryong.local/haeryong/internal/adapters/clientstate"
	"net/url"
	"strconv"
	"strings"
)

func findingActionInput(opt options) (string, string, any, error) {
	args := opt.command
	if len(args) != 3 || args[2] == "" {
		return "", "", nil, invalid("exact finding or overlay ID required")
	}
	tag := opt.flags["if-match"]
	if !strings.HasPrefix(tag, "\"") || !strings.HasSuffix(tag, "\"") || strings.Contains(tag, ",") {
		return "", "", nil, invalid("individual strong --if-match required")
	}
	if k := opt.flags["operation-id"]; k != "" && !operationID.MatchString(k) {
		return "", "", nil, invalid("canonical operation ID required")
	}
	id := url.PathEscape(args[2])
	switch args[0] {
	case "findings":
		if args[1] != "approve" && args[1] != "reject" {
			break
		}
		return "POST", "/api/v1/findings/" + id + "/decisions", map[string]string{"decision": args[1]}, nil
	case "overlays":
		if args[1] != "undo" {
			break
		}
		return "POST", "/api/v1/overlays/" + id + "/undo", map[string]any{}, nil
	case "topics":
		p, e := strconv.ParseBool(opt.flags["pinned"])
		d := opt.flags["decision"]
		if args[1] != "set" || e != nil || (d != "accepted" && d != "rejected") || p && d == "rejected" {
			return "", "", nil, invalid("topics set requires --pinned true|false --decision accepted|rejected")
		}
		return "PUT", "/api/v1/topic-assignments/" + id, map[string]any{"pinned": p, "decision": d}, nil
	}
	return "", "", nil, invalid("unsupported finding action")
}
func findingActionCLI(ctx context.Context, api *client.Client, state *clientstate.Store, opt options, emit func(any) error) error {
	method, target, body, e := findingActionInput(opt)
	if e != nil {
		return e
	}
	result, key, e := mutationConditions(ctx, api, state, opt.flags["operation-id"], method, target, body, map[string]string{"If-Match": opt.flags["if-match"]})
	finish := func() error {
		if opt.flags["operation-id"] != "" {
			return nil
		}
		return finishMutation(context.Background(), state, clientstate.CommandIntent{Method: method, Target: target}, key)
	}
	if e != nil {
		e = afterDefinitive(recordedError(productError(e), result, key, nil), key, finish)
		var recorded *recordedCommandError
		if key != "" && !errors.As(e, &recorded) {
			return &pendingReviewError{cause: e, operation: key}
		}
		return e
	}
	if e = emit(map[string]any{"operation_id": key, "result": json.RawMessage(result.Body)}); e != nil {
		return e
	}
	return finish()
}
