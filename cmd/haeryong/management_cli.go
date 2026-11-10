package main

import (
	"net/url"
	"strconv"
)

func managementCLIPath(opt options) (string, error) {
	if len(opt.command) != 2 || opt.command[1] != "list" {
		return "", invalid("use related list or topics list")
	}
	q := url.Values{}
	version := opt.flags["version"]
	if opt.command[0] == "related" && version == "" {
		return "", invalid("related requires --version UUID")
	}
	for _, k := range []string{"version", "document"} {
		if v := opt.flags[k]; v != "" {
			if !referenceVersionID.MatchString(v) {
				return "", invalid("invalid --" + k)
			}
			q.Set(k+"_id", v)
		}
	}
	if p := opt.flags["path"]; p != "" {
		if version == "" {
			return "", invalid("--path requires --version")
		}
		q.Set("page_path", p)
	}
	if r := opt.flags["region"]; r != "" {
		if opt.flags["path"] == "" {
			return "", invalid("--region requires --path")
		}
		q.Set("region_id", r)
	}
	if c := opt.flags["cursor"]; c != "" {
		q.Set("cursor", c)
	}
	if l := opt.flags["limit"]; l != "" {
		n, e := strconv.Atoi(l)
		if e != nil || n < 1 || n > 200 {
			return "", invalid("--limit must be 1..200")
		}
		q.Set("limit", l)
	}
	path := "/api/v1/topic-assignments"
	if opt.command[0] == "related" {
		if opt.flags["document"] != "" {
			return "", invalid("related does not accept --document")
		}
		q.Del("version_id")
		path = "/api/v1/versions/" + version + "/related"
	}
	if len(q) > 0 {
		path += "?" + q.Encode()
	}
	return path, nil
}
