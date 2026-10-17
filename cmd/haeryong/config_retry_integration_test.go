package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"haeryong.local/haeryong/internal/application/service"
	"haeryong.local/haeryong/internal/domain"
)

func configLossProxy(t *testing.T, endpoint, lostPath string) (*httptest.Server, *atomic.Bool, *atomic.Int64) {
	t.Helper()
	target, e := url.Parse(endpoint)
	if e != nil {
		t.Fatal(e)
	}
	blocked, lost := new(atomic.Bool), new(atomic.Int64)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		match := r.Method == "POST" && r.URL.Path == lostPath
		if match && blocked.Load() {
			http.Error(w, "fixture acknowledgement unavailable", 503)
			return
		}
		out := r.Clone(r.Context())
		out.URL.Scheme = target.Scheme
		out.URL.Host = target.Host
		out.Host = target.Host
		out.RequestURI = ""
		response, e := http.DefaultTransport.RoundTrip(out)
		if e != nil {
			http.Error(w, "fixture transport unavailable", 502)
			return
		}
		defer response.Body.Close()
		if match && lost.Add(1) == 1 {
			blocked.Store(true)
			io.Copy(io.Discard, response.Body)
			connection, _, e := w.(http.Hijacker).Hijack()
			if e != nil {
				t.Error(e)
				return
			}
			connection.Close()
			return
		}
		for key, values := range response.Header {
			for _, value := range values {
				w.Header().Add(key, value)
			}
		}
		w.WriteHeader(response.StatusCode)
		io.Copy(w, response.Body)
	}))
	t.Cleanup(server.Close)
	return server, blocked, lost
}

func TestActualCLIConfigurationPlanRecoversOriginalFileAndRevision(t *testing.T) {
	for _, explicit := range []bool{false, true} {
		t.Run(map[bool]string{false: "automatic_id", true: "explicit_id"}[explicit], func(t *testing.T) {
			s := selection(t)
			initProfile(t, s)
			daemon := startDaemon(t, s, availablePort(t))
			session, token := bootstrapOwnerCLI(t, s, daemon.id.Endpoint, "configuration-retry fixture")
			proxy, blocked, lost := configLossProxy(t, daemon.id.Endpoint, "/api/v1/configuration/plans")
			root, flags := callerProfile(t, s, session, token, proxy.URL)
			file := filepath.Join(t.TempDir(), "policy.yaml")
			if e := os.WriteFile(file, []byte("security:\n  remember_device_default: false\n"), 0600); e != nil {
				t.Fatal(e)
			}
			args := []string{"config", "plan", file}
			operation := ""
			if explicit {
				operation, _ = service.NewID()
				args = append(args, "--operation-id", operation)
			}
			args = append(args, flags...)
			cli(t, 5, args...)
			if lost.Load() != 1 {
				t.Fatal("plan acknowledgement was not lost after one forwarded POST")
			}
			journals, e := os.ReadDir(filepath.Join(root, "journals"))
			if e != nil || len(journals) != 1 {
				t.Fatal("one pre-dispatch plan journal missing", e, len(journals))
			}
			if operation == "" {
				operation = journals[0].Name()[:36]
			}
			before := readCallerJournal(t, root, operation)
			var original struct {
				Expected int64           `json:"expected_config_revision"`
				Changes  json.RawMessage `json:"changes"`
			}
			if e = json.Unmarshal(before.FinalBody, &original); e != nil || original.Expected < 1 || !bytes.Contains(original.Changes, []byte(`"remember_device_default":false`)) {
				t.Fatal("original plan semantics missing", e)
			}
			// Advance current configuration independently. A retry must not capture it.
			_, directFlags := callerProfile(t, s, session, token, daemon.id.Endpoint)
			other := filepath.Join(t.TempDir(), "other.yaml")
			if e = os.WriteFile(other, []byte("security:\n  pairing_expiry_seconds: 121\n"), 0600); e != nil {
				t.Fatal(e)
			}
			planned := cli(t, 0, append([]string{"config", "plan", other}, directFlags...)...)
			var otherPlan struct {
				Data struct {
					Result domain.ConfigurationPlan `json:"result"`
				} `json:"data"`
			}
			if e = json.Unmarshal(planned, &otherPlan); e != nil {
				t.Fatal(e)
			}
			cli(t, 0, append([]string{"config", "apply", otherPlan.Data.Result.ID, "--expected-config-revision", jsonNumber(otherPlan.Data.Result.BaseRevision), "--confirmed"}, directFlags...)...)
			if e = os.Remove(file); e != nil {
				t.Fatal(e)
			}
			blocked.Store(false)
			out := cli(t, 0, args...)
			var repeated struct {
				Data struct {
					Operation string                   `json:"operation_id"`
					Result    domain.ConfigurationPlan `json:"result"`
				} `json:"data"`
			}
			if e = json.Unmarshal(out, &repeated); e != nil {
				t.Fatal(e)
			}
			if repeated.Data.Operation != operation || repeated.Data.Result.BaseRevision != original.Expected {
				t.Fatal("retry recaptured operation/revision")
			}
			after := readCallerJournal(t, root, operation)
			if !bytes.Equal(after.FinalBody, before.FinalBody) {
				t.Fatal("retry replaced original file intent")
			}
			cli(t, 4, append([]string{"config", "apply", repeated.Data.Result.ID, "--expected-config-revision", jsonNumber(original.Expected), "--confirmed"}, flags...)...)
		})
	}
}

func jsonNumber(n int64) string { b, _ := json.Marshal(n); return string(b) }

func TestActualCLINaturalConfigurationCommitRecoversLostAcknowledgement(t *testing.T) {
	s := selection(t)
	initProfile(t, s)
	daemon := startDaemon(t, s, availablePort(t))
	session, token := bootstrapOwnerCLI(t, s, daemon.id.Endpoint, "natural-commit fixture")
	_, directFlags := callerProfile(t, s, session, token, daemon.id.Endpoint)
	file := filepath.Join(t.TempDir(), "policy.yaml")
	if e := os.WriteFile(file, []byte("security:\n  remember_device_default: false\n"), 0600); e != nil {
		t.Fatal(e)
	}
	out := cli(t, 0, append([]string{"config", "plan", file}, directFlags...)...)
	var planned struct {
		Data struct {
			Result domain.ConfigurationPlan `json:"result"`
		} `json:"data"`
	}
	if e := json.Unmarshal(out, &planned); e != nil {
		t.Fatal(e)
	}
	plan := planned.Data.Result
	proxy, blocked, lost := configLossProxy(t, daemon.id.Endpoint, "/api/v1/configuration/plans/"+plan.ID+"/commit")
	root, flags := callerProfile(t, s, session, token, proxy.URL)
	args := append([]string{"config", "apply", plan.ID, "--expected-config-revision", jsonNumber(plan.BaseRevision), "--confirmed"}, flags...)
	cli(t, 5, args...)
	journal := readCallerJournal(t, root, plan.ID)
	if journal.OperationID != plan.ID || lost.Load() != 1 {
		t.Fatal("natural commit identity was replaced")
	}
	blocked.Store(false)
	cli(t, 0, args...)
	status, data := authRequest(t, daemon.id.Endpoint, "GET", "/api/v1/configuration", token, nil)
	if status != 200 {
		t.Fatal(status)
	}
	var current struct {
		Revision int64 `json:"config_revision"`
	}
	if e := json.Unmarshal(data, &current); e != nil || current.Revision != plan.BaseRevision+1 {
		t.Fatal("commit retry advanced policy twice", e)
	}
}
