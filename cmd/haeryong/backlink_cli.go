package main

import (
	"haeryong.local/haeryong/internal/domain"
	"net/url"
	"strconv"
	"unicode/utf8"
)

func connectedCLIPath(opt options) (string, error) {
	args := opt.command
	if len(args) != 2 || !referenceVersionID.MatchString(opt.flags["version"]) {
		return "", invalid("backlinks/indexes requires a command and --version UUID")
	}
	path := "/api/v1/versions/" + opt.flags["version"]
	values := url.Values{}
	if args[0] == "backlinks" {
		if args[1] != "list" {
			return "", invalid("use backlinks list")
		}
		path += "/backlinks"
		for flag, key := range map[string]string{"page": "page_path", "region": "region_id", "cursor": "cursor", "limit": "limit"} {
			if v, ok := opt.flags[flag]; ok {
				values.Set(key, v)
			}
		}
		if page, ok := opt.flags["page"]; ok && !domain.ValidReferencePath(&page) {
			return "", invalid("invalid --page")
		}
		if region, ok := opt.flags["region"]; ok && (opt.flags["page"] == "" || region == "" || !utf8.ValidString(region)) {
			return "", invalid("--region requires --page")
		}
		if v, ok := opt.flags["limit"]; ok {
			n, e := strconv.Atoi(v)
			if e != nil || n < 1 || n > 200 {
				return "", invalid("--limit must be 1..200")
			}
		}
	} else {
		if args[1] != "validate" && args[1] != "rebuild" {
			return "", invalid("use indexes validate|rebuild")
		}
		kind := opt.flags["kind"]
		if kind != "search" && kind != "references" && !(args[1] == "rebuild" && kind == "all") {
			return "", invalid("invalid --kind")
		}
		path += "/indexes/" + args[1]
		if args[1] == "validate" {
			values.Set("kind", kind)
		}
	}
	if len(values) > 0 {
		path += "?" + values.Encode()
	}
	return path, nil
}
