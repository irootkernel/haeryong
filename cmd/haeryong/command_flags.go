package main

import "strings"

// validateCommandFlags rejects recognized global parser flags that the selected
// current command cannot consume. A spelling accepted elsewhere cannot silently
// alter, broaden or disappear from this command's captured intent.
func validateCommandFlags(opt options) error {
	allowed := map[string]bool{"credential": true, "client-dir": true}
	add := func(names string) {
		for _, name := range strings.Fields(names) {
			allowed[name] = true
		}
	}
	args := opt.command
	if len(args) == 0 {
		return invalid("command required")
	}
	verb := ""
	if len(args) > 1 {
		verb = args[1]
	}
	switch args[0] {
	case "references":
		if verb == "list" {
			add("version cursor limit")
		} else if verb == "resolve" {
			add("version reference-id page region")
		}
	case "search":
		add("query project lifecycle versions kind cursor limit")
	case "import":
		add("operation-id project document new-document base-version version metadata")
	case "package":
		if verb == "validate" {
			add("operation-id project document new-document metadata")
		}
	case "documents":
		if verb == "show" {
			add("version")
		}
	case "projects":
		switch verb {
		case "list":
			add("cursor limit")
		case "register":
			add("name operation-id")
		}
	case "jobs":
		if verb == "cancel" {
			add("operation-id")
		}
	case "uploads":
		switch verb {
		case "list":
			add("cursor state unadopted")
		case "resume":
			add("input operation-id")
		case "abandon":
			add("operation-id")
		}
	case "files":
		switch verb {
		case "list":
			add("version cursor limit directory kind")
		case "get":
			add("version path output resolve-link")
		case "download":
			add("version output")
		}
	case "config":
		switch verb {
		case "plan", "import":
			add("file format project scope expected-config-revision operation-id")
		case "apply":
			add("expected-config-revision confirmed operation-id")
		case "export":
			add("output")
		}
	case "sources":
		switch verb {
		case "status":
			add("project")
		case "approve":
			add("project source-id kind root-path repo-id retention-mode provider-ids confirmed operation-id plan-id")
		case "observe":
			add("version page region operation-id")
		}
	case "evidence":
		switch verb {
		case "show":
			add("version evidence")
		case "verify":
			add("version evidence expected-sha256 expected-excerpt-file operation-id")
		}
	case "assessments":
		if verb == "list" {
			add("version coverage-set")
		}
	case "events":
		add("cursor")
	}
	for name := range opt.flags {
		if !allowed[name] {
			return invalid("flag is unsupported for this command: --" + name)
		}
	}
	if args[0] == "references" {
		if _, err := referenceCLIPath(opt); err != nil {
			return err
		}
	}
	if scope := opt.flags["scope"]; scope != "" {
		if scope != "library" && scope != "project" {
			return invalid("config --scope must be library or project")
		}
		if scope == "project" && opt.flags["project"] == "" || scope == "library" && opt.flags["project"] != "" {
			return invalid("config scope conflicts with explicit project")
		}
	}
	return nil
}
