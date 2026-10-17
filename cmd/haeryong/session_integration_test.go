package main

import (
	"encoding/json"
	"testing"
)

func TestActualCLISessionsListAndNaturalRevoke(t *testing.T) {
	s := selection(t)
	initProfile(t, s)
	daemon := startDaemon(t, s, availablePort(t))
	first, firstToken := bootstrapOwnerCLI(t, s, daemon.id.Endpoint, "session-list fixture")
	second, secondToken := bootstrapOwnerCLI(t, s, daemon.id.Endpoint, "session-revoke fixture")
	_, firstFlags := callerProfile(t, s, first, firstToken, daemon.id.Endpoint)
	_, secondFlags := callerProfile(t, s, second, secondToken, daemon.id.Endpoint)
	out := cli(t, 0, append([]string{"auth", "sessions", "list"}, firstFlags...)...)
	var list struct {
		Data struct {
			Items []struct {
				ID       string `json:"session_id"`
				Revision int64  `json:"revision"`
				ETag     string `json:"etag"`
			} `json:"items"`
		} `json:"data"`
	}
	if e := json.Unmarshal(out, &list); e != nil || len(list.Data.Items) != 2 {
		t.Fatal("session list missing owner devices", e)
	}
	for _, item := range list.Data.Items {
		if item.Revision < 1 || item.ETag == "" {
			t.Fatal("individual session validator missing")
		}
	}
	// Revoke is natural identity: no operation key or ETag obligation is added.
	cli(t, 0, append([]string{"auth", "sessions", "revoke", second.ID}, firstFlags...)...)
	cli(t, 3, append([]string{"auth", "sessions", "list"}, secondFlags...)...)
	cli(t, 0, append([]string{"auth", "sessions", "list"}, firstFlags...)...)
	cli(t, 2, append([]string{"auth", "sessions", "revoke", second.ID, "--operation-id", "12345678-1234-1234-1234-123456789abc"}, firstFlags...)...)
}

func TestCurrentCommandFlagsCannotSilentlyChangeOrIgnoreIntent(t *testing.T) {
	for _, args := range [][]string{{"events", "--metadata", "unused"}, {"files", "download", "--version", "fixture", "--output", "out", "--directory", "ignored"}, {"auth", "sessions", "list", "--project", "ignored"}, {"package", "validate", "input", "--base-version", "ignored"}, {"config", "show", "--expected-config-revision", "7"}, {"config", "plan", "input", "--scope", "library", "--project", "conflicting"}} {
		opt, e := parse(args)
		if e != nil {
			t.Fatal("fixture parser setup", e)
		}
		if e = validateCommandFlags(opt); e == nil {
			t.Errorf("ignored/conflicting flag accepted: %v", args)
		}
	}
	for _, args := range [][]string{{"import", "input", "--metadata", "authored.json", "--project", "chosen"}, {"files", "list", "--version", "chosen", "--limit", "1"}, {"auth", "sessions", "list"}, {"config", "plan", "policy.yaml", "--scope", "project", "--project", "chosen"}, {"events", "--cursor", "retained"}} {
		opt, e := parse(args)
		if e != nil {
			t.Fatal(e)
		}
		if e = validateCommandFlags(opt); e != nil {
			t.Fatal("supported command flag rejected", args, e)
		}
	}
}
