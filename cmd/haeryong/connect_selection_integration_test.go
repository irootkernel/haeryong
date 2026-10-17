package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"haeryong.local/haeryong/internal/adapters/client"
	app "haeryong.local/haeryong/internal/application/storage"
)

func TestActualCLIConnectReusesSelectedCustomCredentialBeforeLocalDiscovery(t *testing.T) {
	f := newCommandFixture(t)
	root, _ := callerProfile(t, f.selection, f.session, f.token, f.base)
	customRoot := filepath.Join(t.TempDir(), "protected-selected")
	if e := os.Mkdir(customRoot, 0700); e != nil {
		t.Fatal(e)
	}
	custom := filepath.Join(customRoot, "selected.json")
	if e := os.Rename(filepath.Join(root, "credential.json"), custom); e != nil {
		t.Fatal(e)
	}
	var before int
	if e := f.store.Write(context.Background(), func(tx app.WriteTx) error { rows, e := tx.Pairings(true); before = len(rows); return e }); e != nil {
		t.Fatal(e)
	}
	flags := []string{"--profile", f.selection.Profile, "--client-dir", root, "--credential", custom, "--data-dir", filepath.Join(t.TempDir(), "no-local-profile"), "--json"}
	for _, withDefault := range []bool{false, true} {
		if withDefault {
			if e := client.SaveCredential(filepath.Join(root, "credential.json"), client.Credential{Endpoint: "http://127.0.0.1:65534", Profile: f.selection.Profile, LibraryID: f.session.LibraryID, Token: f.token}); e != nil {
				t.Fatal(e)
			}
		}
		out := cli(t, 0, append([]string{"auth", "connect"}, flags...)...)
		var result struct {
			Data struct {
				Connected bool   `json:"connected"`
				Endpoint  string `json:"endpoint"`
			} `json:"data"`
		}
		if e := json.Unmarshal(out, &result); e != nil || !result.Data.Connected || result.Data.Endpoint != f.base || bytes.Contains(out, []byte(`"token"`)) {
			t.Fatal("selected remembered credential not reused", e)
		}
	}
	cli(t, 3, append([]string{"auth", "connect", "--endpoint", "http://127.0.0.1:65533"}, flags...)...)
	if e := f.store.Write(context.Background(), func(tx app.WriteTx) error {
		rows, e := tx.Pairings(true)
		if len(rows) != before {
			t.Fatal("credential reuse issued a new pairing")
		}
		return e
	}); e != nil {
		t.Fatal(e)
	}
}
