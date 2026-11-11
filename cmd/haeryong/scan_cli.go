package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"strconv"
	"strings"

	client "haeryong.local/haeryong/internal/adapters/client"
	"haeryong.local/haeryong/internal/adapters/clientstate"
	"haeryong.local/haeryong/internal/application/scan"
)

func scanCLIInput(opt options) (map[string]any, error) {
	if len(opt.command) != 1 {
		return nil, invalid("scan accepts flags only")
	}
	if opt.flags["all-allowed-projects"] == "true" && opt.flags["project"] != "" {
		return nil, invalid("--all-allowed-projects conflicts with --project")
	}
	if opt.flags["region"] != "" && opt.flags["path"] == "" {
		return nil, invalid("--region requires --path")
	}
	if opt.flags["target-set"] != "" && opt.flags["capture"] != "" {
		return nil, invalid("--target-set conflicts with --capture")
	}
	if key := opt.flags["operation-id"]; key != "" && !operationID.MatchString(key) {
		return nil, invalid("canonical operation ID required")
	}
	body := map[string]any{}
	if id := opt.flags["finding"]; id != "" {
		n, e := strconv.ParseInt(opt.flags["expected-finding-revision"], 10, 64)
		if e != nil || n < 1 || opt.flags["version"] == "" || opt.flags["document"] == "" || opt.flags["depth"] == "" || opt.flags["source-mode"] == "" {
			return nil, invalid("finding reassessment requires expected revision and explicit document/version/depth/source-mode")
		}
		body["finding_id"] = id
		body["expected_finding_revision"] = n
	} else if opt.flags["expected-finding-revision"] != "" {
		return nil, invalid("--finding is required")
	}
	if opt.flags["all-allowed-projects"] == "true" {
		body["scope"] = "all_allowed_projects"
	}
	if rawProj := opt.flags["project"]; rawProj != "" {
		var ids []string
		for _, part := range strings.Split(rawProj, ",") {
			p := strings.TrimSpace(part)
			if p != "" {
				ids = append(ids, p)
			}
		}
		if len(ids) == 0 {
			return nil, invalid("--project requires at least one project ID")
		}
		body["scope"] = "selected_projects"
		body["project_ids"] = ids
	}
	if v := opt.flags["document"]; v != "" {
		body["document_id"] = v
	}
	if v := opt.flags["version"]; v != "" {
		body["version_id"] = v
	}
	if v := opt.flags["path"]; v != "" {
		if opt.flags["finding"] != "" {
			body["page_path"] = v
		} else {
			body["path_prefix"] = v
		}
	}
	if v := opt.flags["region"]; v != "" {
		body["region_id"] = v
	}
	if rawDepth := opt.flags["depth"]; rawDepth != "" {
		depth, err := scan.NormalizeDepth(rawDepth)
		if err != nil || depth == "" {
			return nil, invalid("invalid --depth; use change-focused, full-scope or deep-semantic")
		}
		body["depth"] = depth
	}
	if m := opt.flags["source-mode"]; m != "" {
		if m != "pinned" && m != "current" {
			return nil, invalid("invalid --source-mode; use pinned or current")
		}
		body["source_mode"] = m
	}
	if ts := opt.flags["target-set"]; ts != "" {
		body["target_set_id"] = ts
	}
	if capID := opt.flags["capture"]; capID != "" {
		body["target_set_id"] = capID
	}
	return body, nil
}

func findingsCLIInput(opt options) (string, error) {
	args := opt.command
	if len(args) >= 2 && (args[1] == "approve" || args[1] == "reject") {
		_, path, _, e := findingActionInput(opt)
		return path, e
	}
	if len(args) != 2 || args[1] != "list" {
		return "", invalid("use findings list --job ID [--kind KIND] [--status STATUS] [--cursor CURSOR] [--limit N]")
	}
	jobID := opt.flags["job"]
	if jobID == "" || !operationID.MatchString(jobID) {
		return "", invalid("findings list requires --job UUID")
	}
	values := url.Values{}
	if k := opt.flags["kind"]; k != "" {
		if !scan.ValidFindingKind(k) {
			return "", invalid("invalid --kind")
		}
		values.Set("kind", k)
	}
	if s := opt.flags["status"]; s != "" {
		if !scan.ValidFindingStatus(s) {
			return "", invalid("invalid --status")
		}
		values.Set("status", s)
	}
	if c := opt.flags["cursor"]; c != "" {
		values.Set("cursor", c)
	}
	if l := opt.flags["limit"]; l != "" {
		n, err := strconv.Atoi(l)
		if err != nil || n < 1 || n > 200 {
			return "", invalid("--limit must be 1..200")
		}
		values.Set("limit", l)
	}
	route := "/api/v1/jobs/" + url.PathEscape(jobID) + "/findings"
	if len(values) > 0 {
		route += "?" + values.Encode()
	}
	return route, nil
}

func scanCLI(ctx context.Context, api *client.Client, state *clientstate.Store, opt options, emit func(any) error) error {
	if opt.command[0] == "scan" {
		body, err := scanCLIInput(opt)
		if err != nil {
			return err
		}
		key := opt.flags["operation-id"]
		var release func() error
		if key == "" {
			if key, release, err = automaticKey(ctx, api, state, clientstate.CommandIntent{Method: "POST", Target: "/api/v1/scans"}); err != nil {
				return err
			}
		}
		err = dispatchCommand(ctx, api, state, emit, key, "POST", "/api/v1/scans", body, release)
		if err != nil {
			var recorded *recordedCommandError
			if key != "" && !errors.As(err, &recorded) {
				return &pendingReviewError{cause: err, operation: key}
			}
			return err
		}
		return nil
	}
	if len(opt.command) > 1 && (opt.command[1] == "approve" || opt.command[1] == "reject") {
		return findingActionCLI(ctx, api, state, opt, emit)
	}
	route, err := findingsCLIInput(opt)
	if err != nil {
		return err
	}
	var data json.RawMessage
	if _, err := api.JSON(ctx, "GET", route, "", nil, &data); err != nil {
		return productError(err)
	}
	return emit(data)
}
