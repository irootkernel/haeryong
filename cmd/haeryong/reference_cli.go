package main

import (
	"context"
	"encoding/json"
	"net/url"
	"regexp"
	"strconv"
	"unicode/utf8"

	client "haeryong.local/haeryong/internal/adapters/client"
	"haeryong.local/haeryong/internal/domain"
)

var referenceVersionID = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

func referenceCLIPath(opt options) (string, error) {
	args := opt.command
	if len(args) != 2 || (args[1] != "list" && args[1] != "resolve") {
		return "", invalid("use references list|resolve")
	}
	version := opt.flags["version"]
	if !referenceVersionID.MatchString(version) {
		return "", invalid("references requires --version UUID")
	}
	values := url.Values{}
	suffix := ""
	if args[1] == "list" {
		if v, ok := opt.flags["limit"]; ok {
			n, err := strconv.Atoi(v)
			if err != nil || n < 1 || n > 200 {
				return "", invalid("--limit must be 1..200")
			}
			values.Set("limit", v)
		}
		if v := opt.flags["cursor"]; v != "" {
			values.Set("cursor", v)
		}
	} else {
		id := opt.flags["reference-id"]
		if id == "" || !utf8.ValidString(id) {
			return "", invalid("references resolve requires --reference-id")
		}
		values.Set("reference_id", id)
		suffix = "/resolve"
		if page, ok := opt.flags["page"]; ok {
			if !domain.ValidReferencePath(&page) {
				return "", invalid("invalid --page")
			}
			values.Set("page_path", page)
		}
		if region, ok := opt.flags["region"]; ok {
			if opt.flags["page"] == "" || region == "" || !utf8.ValidString(region) {
				return "", invalid("--region requires --page")
			}
			values.Set("region_id", region)
		}
	}
	path := "/api/v1/versions/" + version + "/references" + suffix
	if len(values) > 0 {
		path += "?" + values.Encode()
	}
	return path, nil
}

func referenceCLI(ctx context.Context, api *client.Client, opt options, emit func(any) error) error {
	path, err := referenceCLIPath(opt)
	if err != nil {
		return err
	}
	var data json.RawMessage
	_, err = api.JSON(ctx, "GET", path, "", nil, &data)
	if err != nil {
		return productError(err)
	}
	return emit(data)
}
