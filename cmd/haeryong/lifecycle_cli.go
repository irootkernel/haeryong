package main

import (
	"context"
	"encoding/json"
	client "haeryong.local/haeryong/internal/adapters/client"
	"haeryong.local/haeryong/internal/adapters/clientstate"
	"net/url"
	"strconv"
)

func lifecycleCLI(ctx context.Context, api *client.Client, state *clientstate.Store, opt options, emit func(any) error) error {
	args := opt.command
	if len(args) < 2 {
		return invalid("use lifecycle plan|show|commit or trash list")
	}
	path := ""
	method := "GET"
	var body any
	key := opt.flags["operation-id"]
	if key != "" && !operationID.MatchString(key) {
		return invalid("canonical operation ID required")
	}
	switch {
	case args[0] == "trash" && args[1] == "list" && len(args) == 2:
		path = "/api/v1/trash"
	case args[0] == "lifecycle" && args[1] == "show" && len(args) == 3 && operationID.MatchString(args[2]):
		path = "/api/v1/lifecycle-plans/" + args[2]
	case args[0] == "lifecycle" && args[1] == "plan" && len(args) == 2 && operationID.MatchString(opt.flags["document"]):
		rev, e := strconv.ParseInt(opt.flags["expected-revision"], 10, 64)
		if e != nil || rev < 1 {
			return invalid("positive --expected-revision required")
		}
		action := opt.flags["action"]
		if action != "archive" && action != "trash" && action != "restore" && action != "purge" {
			return invalid("--action archive|trash|restore|purge required")
		}
		path = "/api/v1/documents/" + opt.flags["document"] + "/lifecycle-plans"
		method = "POST"
		body = map[string]any{"operation": action, "expected_revision": rev}
	case args[0] == "lifecycle" && args[1] == "commit" && len(args) == 3 && operationID.MatchString(args[2]):
		rev, e := strconv.ParseInt(opt.flags["expected-revision"], 10, 64)
		if e != nil || rev < 1 || opt.flags["confirm"] != "true" {
			return invalid("--expected-revision N --confirm required")
		}
		path = "/api/v1/lifecycle-plans/" + args[2] + "/commit"
		method = "POST"
		body = map[string]any{"plan_id": args[2], "expected_revision": rev, "confirmed": true}
		// The plan is the durable retry identity; never allocate a new commit key.
		if key == "" {
			key = args[2]
		}
	default:
		return invalid("use lifecycle plan|show|commit or trash list")
	}
	if method == "GET" {
		q := url.Values{}
		for flag, param := range map[string]string{"cursor": "cursor", "limit": "limit", "project": "project_id"} {
			if v := opt.flags[flag]; v != "" {
				q.Set(param, v)
			}
		}
		if len(q) > 0 {
			path += "?" + q.Encode()
		}
		var data json.RawMessage
		_, e := api.JSON(ctx, method, path, "", nil, &data)
		if e != nil {
			return productError(e)
		}
		return emit(data)
	}
	if key == "" {
		var data json.RawMessage
		_, e := api.JSON(ctx, method, path, "", body, &data)
		if e != nil {
			return productError(e)
		}
		return emit(data)
	}
	response, id, e := mutation(ctx, api, state, key, method, path, body)
	if e != nil {
		return &pendingReviewError{cause: productError(e), operation: id}
	}
	return emit(map[string]any{"operation_id": id, "result": json.RawMessage(response.Body)})
}
