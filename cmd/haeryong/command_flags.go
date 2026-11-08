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
	case "drafts":
		switch verb {
		case "list":
			add("project cursor")
		case "history":
			add("cursor")
		case "reject", "publish":
			add("if-match operation-id")
		case "replace":
			add("input metadata if-match operation-id")
		}
	case "generate":
		add("project request-file operation-id")
	case "ask":
		add("version page region source-mode request-file operation-id")
	case "questions":
		if verb == "list" {
			add("version cursor")
		}
	case "auth":
		if len(args) >= 3 && args[1] == "grant" {
			switch args[2] {
			case "create", "rotate":
				add("input operation-id")
			case "revoke":
				add("operation-id")
			}
		}
	case "backup":
		switch verb {
		case "create":
			add("destination operation-id")
		case "verify":
			add("input")
		case "resume":
			add("operation-id")
		case "abandon":
			add("operation-id confirm")
		}
	case "export":
		switch verb {
		case "plan", "create":
			add("version document project versions lifecycle records include-retained-sources format destination metadata-destination operation-id")
		case "verify":
			add("input index")
		case "show", "plan.show":
			add("cursor")
		}
	case "restore":
		switch verb {
		case "plan":
			add("input")
		case "apply":
			add("operation-id confirm")
		case "review":
			add("confirm choices")
		}
	case "lifecycle":
		switch verb {
		case "plan":
			add("document action expected-revision operation-id")
		case "show":
			add("cursor limit")
		case "commit":
			add("expected-revision operation-id confirm")
		}
	case "trash":
		if verb == "list" {
			add("project cursor limit")
		}
	case "revisit":
		add("document")
		if verb == "set" {
			add("flag if-match if-none-match operation-id")
		}
		if _, _, err := revisitCLIInput(opt); err != nil {
			return err
		}
	case "review":
		add("version")
		if verb == "set" {
			add("action if-match if-none-match operation-id")
		}
		if verb == "history" {
			add("cursor limit")
		}
		if _, _, err := reviewCLIInput(opt); err != nil {
			return err
		}
	case "backlinks":
		if verb == "list" {
			add("version cursor limit page region")
		}
	case "indexes":
		add("version kind")
		if verb == "rebuild" {
			add("operation-id")
		}
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
		if verb == "resume" {
			add("decision expected-attempt-generation if-match operation-id")
		}
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
		case "capture":
			add("project input operation-id")
		case "captured":
			add("capture cursor")
		case "diff":
			add("capture other cursor")
		case "read":
			add("read output")
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
	case "scan":
		add("project all-allowed-projects document version path region depth source-mode target-set capture operation-id")
	case "findings":
		switch verb {
		case "list":
			add("job kind status cursor limit")
		case "approve", "reject":
			add("if-match operation-id")
		}
	}
	for name := range opt.flags {
		if !allowed[name] {
			return invalid("flag is unsupported for this command: --" + name)
		}
	}
	if args[0] == "scan" {
		if _, err := scanCLIInput(opt); err != nil {
			return err
		}
	}
	if args[0] == "findings" {
		if _, err := findingsCLIInput(opt); err != nil {
			return err
		}
	}
	if args[0] == "backlinks" || args[0] == "indexes" {
		if _, err := connectedCLIPath(opt); err != nil {
			return err
		}
	}
	if args[0] == "references" {
		if _, err := referenceCLIPath(opt); err != nil {
			return err
		}
	}
	if args[0] == "jobs" && verb == "resume" {
		if _, e := resumeProviderInput(opt); e != nil {
			return e
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
