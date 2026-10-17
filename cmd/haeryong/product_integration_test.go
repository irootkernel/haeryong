package main

import (
	"bytes"
	"encoding/json"
	"haeryong.local/haeryong/internal/adapters/client"
	"haeryong.local/haeryong/internal/application/service"
	"haeryong.local/haeryong/internal/application/transfer"
	"haeryong.local/haeryong/internal/domain"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

func callerProfile(t *testing.T, s service.Selection, session domain.Session, token, base string) (string, []string) {
	t.Helper()
	root := filepath.Join(t.TempDir(), "caller")
	if e := os.Mkdir(root, 0700); e != nil {
		t.Fatal(e)
	}
	path := filepath.Join(root, "credential.json")
	b, _ := json.Marshal(client.Credential{Endpoint: base, Profile: s.Profile, LibraryID: session.LibraryID, Token: token})
	if e := os.WriteFile(path, b, 0600); e != nil {
		t.Fatal(e)
	}
	return root, []string{"--profile", s.Profile, "--client-dir", root, "--credential", path, "--json"}
}
func waitPublishedCLI(t *testing.T, base, token, job string) (string, string) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		status, data := authRequest(t, base, "GET", "/api/v1/jobs/"+job, token, nil)
		if status != 200 {
			t.Fatal("job read", status)
		}
		var j domain.Job
		_ = json.Unmarshal(data, &j)
		if j.State == "completed" {
			var result struct {
				Version  string `json:"version_id"`
				Document string `json:"document_id"`
			}
			_ = json.Unmarshal(j.Result, &result)
			return result.Document, result.Version
		}
		if j.State == "failed" {
			t.Fatal("import job failed", j.Error)
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatal("job did not publish")
	return "", ""
}
func TestActualCLIImportQueryAndCapturedRetry(t *testing.T) {
	s := selection(t)
	initProfile(t, s)
	daemon := startDaemon(t, s, availablePort(t))
	session, token := bootstrapOwnerCLI(t, s, daemon.id.Endpoint, "Task05 CLI fixture")
	root, flags := callerProfile(t, s, session, token, daemon.id.Endpoint)
	input := filepath.Join(t.TempDir(), "result-tree")
	if e := os.MkdirAll(filepath.Join(input, "empty", "nested"), 0700); e != nil {
		t.Fatal(e)
	}
	names := map[string]string{"first.html": "<html>first<script>ordinary()</script></html>", ".hidden": "hidden bytes", "literal\\u2028.txt": "exact backslash"}
	for path, body := range names {
		if e := os.WriteFile(filepath.Join(input, path), []byte(body), 0600); e != nil {
			t.Fatal(e)
		}
	}
	if e := os.Symlink("first.html", filepath.Join(input, "link")); e != nil {
		t.Fatal(e)
	}
	operation, _ := service.NewID()
	out := cli(t, 0, append([]string{"import", input, "--operation-id", operation}, flags...)...)
	var result struct {
		Data struct {
			Operation string `json:"operation_id"`
			Upload    string `json:"upload_id"`
			Create    string `json:"upload_create_operation_id"`
			Complete  string `json:"upload_complete_operation_id"`
			Result    struct {
				Job string `json:"job_id"`
			} `json:"result"`
		} `json:"data"`
	}
	if e := json.Unmarshal(out, &result); e != nil {
		t.Fatal(e)
	}
	if result.Data.Operation != operation || result.Data.Create == operation || result.Data.Complete == operation || result.Data.Create == result.Data.Complete || result.Data.Result.Job == "" {
		t.Fatal("composite identity missing")
	}
	document, version := waitPublishedCLI(t, daemon.id.Endpoint, token, result.Data.Result.Job)
	if document == "" || version == "" {
		t.Fatal("published IDs missing")
	}
	var journal transfer.Journal
	b, e := os.ReadFile(filepath.Join(root, "journals", operation+".json"))
	if e != nil {
		t.Fatal(e)
	}
	if e = json.Unmarshal(b, &journal); e != nil {
		t.Fatal(e)
	}
	if journal.FinalBody == nil || journal.Result.Snapshot.ID == "" || journal.Result.Checkpoint.Generation < 1 {
		t.Fatal("durable dispatch journal missing")
	}
	if e = os.RemoveAll(input); e != nil {
		t.Fatal(e)
	}
	same := cli(t, 0, append([]string{"import", input, "--operation-id", operation}, flags...)...)
	if !bytes.Equal(out, same) {
		var repeated struct {
			Data struct {
				Result struct {
					Job string `json:"job_id"`
				} `json:"result"`
			} `json:"data"`
		}
		_ = json.Unmarshal(same, &repeated)
		if repeated.Data.Result.Job != result.Data.Result.Job {
			t.Fatal("retry recaptured local input")
		}
	}
	list := cli(t, 0, append([]string{"files", "list", "--version", version, "--limit", "1"}, flags...)...)
	decoder := json.NewDecoder(bytes.NewReader(list))
	count := 0
	for decoder.More() {
		var item struct {
			Data struct {
				Path string `json:"path"`
			}
		}
		if e = decoder.Decode(&item); e != nil {
			t.Fatal(e)
		}
		count++
	}
	if count != 6 {
		t.Fatal("cursor prefix counted as complete", count)
	}
	destination := filepath.Join(t.TempDir(), "received.html")
	cli(t, 0, append([]string{"files", "get", "--version", version, "--path", "first.html", "--output", destination}, flags...)...)
	received, e := os.ReadFile(destination)
	if e != nil || string(received) != names["first.html"] {
		t.Fatal("CLI exact file", e)
	}

	copied := filepath.Join(t.TempDir(), "whole-tree")
	cli(t, 0, append([]string{"files", "download", "--version", version, "--output", copied}, flags...)...)
	for path, body := range names {
		b, e := os.ReadFile(filepath.Join(copied, path))
		if e != nil || string(b) != body {
			t.Fatal("complete directory bytes changed", path, e)
		}
	}
	if link, e := os.Readlink(filepath.Join(copied, "link")); e != nil || link != "first.html" {
		t.Fatal("link text changed", e)
	}
	if info, e := os.Stat(filepath.Join(copied, "empty", "nested")); e != nil || !info.IsDir() {
		t.Fatal("empty directory lost", e)
	}
	cli(t, 0, append([]string{"service", "stop"}, profileArgs(s)...)...)
	daemon.wait(t)
	parsed, _ := url.Parse(daemon.id.Endpoint)
	port, _ := strconv.Atoi(parsed.Port())
	startDaemon(t, s, port)
	cli(t, 0, append([]string{"documents", "show", document, "--version", version}, flags...)...)
	cli(t, 3, append([]string{"documents", "show", document, "--endpoint", "http://127.0.0.1:65534"}, flags...)...)
}
