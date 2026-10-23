package sqlite

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	httpauth "haeryong.local/haeryong/internal/adapters/http/auth"
	"io"
	"math/rand/v2"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"time"

	"github.com/google/uuid"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	httpv1 "haeryong.local/haeryong/internal/adapters/http/v1"
	"haeryong.local/haeryong/internal/application/query"
	app "haeryong.local/haeryong/internal/application/storage"
	"haeryong.local/haeryong/internal/domain"
	sec "haeryong.local/haeryong/internal/security/session"
)

// Canonical representatives are deliberately excluded from this exhaustive
// remainder. Keys identify actor|target-state|surface, not whole test suites.
func referenceCoverage() map[string]string {
	out := map[string]string{}
	add := func(actor, state, source string, surfaces ...string) {
		for _, surface := range surfaces {
			out[actor+"|"+state+"|"+surface] = source
		}
	}
	const projection = "reference_events_test.go RF02-READONLY/RF03 pending-ready-denied"
	add("owner", "active", projection, "list", "head-list", "head-resolve")
	add("source-only", "active", projection, "list", "resolve", "head-list", "head-resolve")
	add("source+target", "active", projection, "list", "resolve", "head-list", "head-resolve")
	add("missing-read", "active", projection, "list", "head-list")
	add("owner", "archived", "reference_events_test.go RF03 seeded archive/trash/history", "resolve")
	add("owner", "trash", "reference_events_test.go RF03 seeded archive/trash/history", "list", "resolve")
	add("owner", "purged-edge", "reference_events_test.go RF03 no-prior-GET historical", "resolve", "head-resolve")
	add("source+target", "purged-edge", "reference_events_test.go RF03 no-prior-GET historical", "resolve", "head-resolve")
	add("source-only", "purged-edge", "reference_events_test.go RF03 historical source-only", "resolve")
	add("owner", "purged-no-edge", "reference_events_test.go RF03 historical no edge", "resolve")
	add("source-only", "active", "reference_events_test.go RF03-E paired live/replay", "event-live", "event-replay")
	add("owner", "active", "reference_events_test.go RF03-E authorized target subscriber", "event-live")
	add("source+target", "active", "reference_events_test.go RF03-E authorized target subscriber", "event-live")
	add("stale", "active", "reference_events_test.go existing manual_references stale grant", "list")
	return out
}

// Success envelopes require every contract member, including CLI error:null.
func matrixSuccess(raw []byte, cli bool) error {
	var actual map[string]any
	if err := json.Unmarshal(raw, &actual); err != nil {
		return fmt.Errorf("invalid success JSON: %w", err)
	}
	requestID, ok := actual["request_id"].(string)
	id, err := uuid.Parse(requestID)
	if !ok || err != nil || id.String() != requestID {
		return fmt.Errorf("success request_id is not a canonical UUID")
	}
	data, present := actual["data"]
	want := map[string]any{"data": data, "request_id": requestID}
	if cli {
		want["error"] = nil
	}
	if !present || !reflect.DeepEqual(actual, want) {
		return fmt.Errorf("success envelope differs from exact contract (CLI %t)", cli)
	}
	return nil
}

// Fixture declarations and the registry define every member, never observed output.
func matrixReferenceData(raw []byte, resolve bool, resolution, document, version, edge string) error {
	view := map[string]any{
		"id": "pin", "from": map[string]any{"page_path": "page.html", "region_id": "retry"},
		"relation": nil, "label": nil, "resolution": resolution, "reason": nil,
		"edge_id": nil, "target": nil, "reader_url": nil,
	}
	switch resolution {
	case "resolved":
		view["label"] = "F10RegionLabelCanary"
		view["edge_id"] = edge
		view["target"] = map[string]any{
			"document_id": document, "version_id": version, "page_path": "page.html", "region_id": "retry",
			"requested_path": "page.html", "resolved_path": "page.html", "page_query": "", "page_fragment": "",
		}
		view["reader_url"] = "/documents/" + document + "/versions/" + version + "?page=" + url.QueryEscape("page.html") + "#region=" + url.QueryEscape("retry")
	case "unavailable":
		// The closed unavailable branch permits no private diagnostic reason.
		view["reason"] = "target_unavailable"
	case "tombstoned":
		view["target"] = map[string]any{"document_id": document, "version_id": version}
	default:
		return fmt.Errorf("unexpected fixture resolution %q", resolution)
	}
	var want any = view
	if !resolve {
		want = map[string]any{"items": []any{view}, "next_cursor": nil, "materialization": map[string]any{"state": "ready", "reason": nil}}
	}
	var actual any
	if err := json.Unmarshal(raw, &actual); err != nil {
		return fmt.Errorf("invalid reference data JSON: %w", err)
	}
	if !reflect.DeepEqual(actual, want) {
		return fmt.Errorf("reference data differs from exact %s projection (resolve %t)", resolution, resolve)
	}
	return nil
}

func matrixVisibleEvent(event domain.Event, version string, revision int64) error {
	if event.Kind != "references.updated" || event.ResourceKind != "version" || event.ResourceID != version || len(event.Body) != 0 || event.Revision != revision {
		return fmt.Errorf("visible event violates independent materialization contract for version %s", version)
	}
	return nil
}

// Check both wire spelling and JSON semantics with the same hidden-data set.
func matrixHiddenData(raw []byte, hidden []string) error {
	check := func(text string) error {
		for _, value := range hidden {
			if value != "" && strings.Contains(text, value) {
				return fmt.Errorf("hidden response data leak")
			}
		}
		return nil
	}
	if err := check(string(raw)); err != nil {
		return err
	}
	if !json.Valid(raw) {
		return nil // Response shape/JSON validity is checked separately.
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	// Tokens visit every nested key and string, including repeated members.
	for {
		value, err := decoder.Token()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		if text, ok := value.(string); ok {
			if err := check(text); err != nil {
				return err
			}
		}
	}
}

// Exact object equality closes every envelope member and requires all fields,
// including null data and false retryable (which struct zero values can hide).
func matrixError(raw []byte, cli bool, status int, document, version string) error {
	var actual map[string]any
	if err := json.Unmarshal(raw, &actual); err != nil {
		return fmt.Errorf("invalid error JSON: %w", err)
	}
	requestID, ok := actual["request_id"].(string)
	id, err := uuid.Parse(requestID)
	if !ok || err != nil || id.String() != requestID {
		return fmt.Errorf("error request_id is not a canonical UUID")
	}
	code := map[int]string{403: "FORBIDDEN", 404: "NOT_FOUND", 410: "TARGET_GONE"}[status]
	want := map[string]any{"request_id": requestID}
	if cli {
		// docs/contracts/cli.md and cmd/haeryong/main.go own this exit mapping.
		exit := map[int]int{403: 3, 404: 3, 410: 4}[status]
		want["data"] = nil
		want["error"] = map[string]any{"code": map[int]string{3: "DENIED", 4: "CONFLICT", 5: "UNAVAILABLE"}[exit], "message": code}
	} else {
		details := map[string]any{}
		if status == 410 {
			details = map[string]any{"document_id": document, "version_id": version}
		}
		want["error"] = map[string]any{"code": code, "message": code, "details": details, "retryable": false}
	}
	if !reflect.DeepEqual(actual, want) {
		return fmt.Errorf("error envelope differs from exact contract for status %d (CLI %t)", status, cli)
	}
	return nil
}

type referenceMatrixRow struct {
	ID       string `json:"id"`
	Status   string `json:"status"`
	Expected any    `json:"expected"`
	Actual   any    `json:"actual"`
	Miss     string `json:"miss,omitempty"`
}

var _ = Describe("RF12 exhaustive reference authority surfaces", Label("manual_references"), func() {
	It("evaluates only the predeclared remainder of the canonical authority/lifecycle surface matrix", func() {
		actors := []string{"owner", "source-only", "source+target", "missing-read", "stale", "denied-project"}
		states := []string{"active", "archived", "trash", "purged-edge", "purged-no-edge", "unknown"}
		surfaces := []string{"list", "resolve", "head-list", "head-resolve", "cli-list", "cli-resolve", "event-replay", "event-live"}
		coverage := referenceCoverage()
		rows := []referenceMatrixRow{}
		for _, actor := range actors {
			for _, state := range states {
				for _, surface := range surfaces {
					id := actor + "|" + state + "|" + surface
					if coverage[id] == "" {
						rows = append(rows, referenceMatrixRow{ID: id, Status: "not-run"})
					}
				}
			}
		}
		proof := map[string]any{"label": "manual_references", "seeded": true, "canonical_layers": false, "coverage": coverage, "predeclared_rows": len(rows), "rows": rows, "complete": false,
			"scope": "Exhaustive remaining surfaces; canonical RF03-C/E cover paired cursor/event noninterference and canonical RF02 covers publisher-independent edge creation. All remaining cells also assert byte-identical reference/job/checkpoint/outbox state."}
		path := os.Getenv("HAERYONG_REFERENCE_MATRIX_RECEIPT")
		Expect(path).NotTo(BeEmpty(), "explicit manual driver receipt is required")
		save := func() {
			raw, e := json.MarshalIndent(proof, "", "  ")
			Expect(e).NotTo(HaveOccurred())
			Expect(os.WriteFile(path, raw, 0600)).To(Succeed())
		}
		save()
		s, a, w, _ := authFixture(GinkgoT())
		defer s.Close()
		_, poll := pairOwner(GinkgoT(), a, "reference exhaustive matrix")
		f := &referenceWorld{s: s, writer: w, bytes: &searchBytes{files: map[string]string{}}, n: rand.IntN(1 << 30)}
		health, e := s.Health(ctx)
		Expect(e).NotTo(HaveOccurred())
		sourceProject := health.Library.UnfiledProjectID
		targetProject := searchID()
		Expect(s.Write(ctx, func(tx app.WriteTx) error {
			return tx.CreateProject(domain.Project{ID: targetProject, Name: "F10 denied project"})
		})).To(Succeed())
		sources, targets, documents, edges := map[string]string{}, map[string]string{}, map[string]string{}, map[string]string{}
		allVersions, sourceVersions := []string{}, []string{}
		const targetBody = "<section id='retry'>F10BodyCanary</section>"
		const sourceBody = "<section id='retry'><a data-haeryong-reference='pin'>Public source</a></section>"
		for _, state := range states {
			target, document := searchID(), searchID()
			if state != "unknown" {
				target = f.publish(nil, targetBody)
				f.run(target, "search")
				f.run(target, "references")
				v, err := s.Version(ctx, target)
				Expect(err).NotTo(HaveOccurred())
				document = v.DocumentID
				_, err = s.db.Exec(`UPDATE documents SET project_id=?,title='F10TitleCanary' WHERE document_id=?`, targetProject, document)
				Expect(err).NotTo(HaveOccurred())
				_, err = s.db.Exec(`UPDATE document_versions SET title_snapshot='F10TitleCanary' WHERE version_id=?`, target)
				Expect(err).NotTo(HaveOccurred())
			}
			targets[state], documents[state] = target, document
			if state == "purged-no-edge" {
				_, err := s.db.Exec(`UPDATE documents SET lifecycle='purged' WHERE document_id=?`, document)
				Expect(err).NotTo(HaveOccurred())
			}
			declaration := map[string]any{"id": "pin", "document_id": document, "version_id": target, "page_path": "page.html", "region_id": "retry", "label": "F10RegionLabelCanary", "from": map[string]any{"page_path": "page.html", "region_id": "retry"}}
			source := f.publish([]any{declaration}, sourceBody)
			f.run(source, "search")
			f.run(source, "references")
			sources[state] = source
			edge, err := f.edge(source, "pin")
			if state == "purged-no-edge" || state == "unknown" {
				Expect(err).To(MatchError(domain.ErrNotFound))
			} else {
				Expect(err).NotTo(HaveOccurred())
				edges[state] = edge.ID
			}
			// The historical edge is asserted before any GET/list/CLI, then purge is seeded.
			lifecycle := state
			if state == "purged-edge" {
				lifecycle = "purged"
			}
			if lifecycle == "archived" || lifecycle == "trash" || lifecycle == "purged" {
				_, err = s.db.Exec(`UPDATE documents SET lifecycle=? WHERE document_id=?`, lifecycle, document)
				Expect(err).NotTo(HaveOccurred())
			}
			sourceVersions = append(sourceVersions, source)
			allVersions = append(allVersions, source, target)
		}
		proof["identities"] = map[string]any{"sources": sources, "targets": targets, "target_documents": documents, "source_project": sourceProject, "target_project": targetProject, "canonical_edges": edges}
		proof["payload_hashes"] = map[string]string{"source": digest([]byte(sourceBody)), "target": digest([]byte(targetBody))}
		tokens := map[string]string{"owner": poll.Token}
		for _, actor := range actors[1:] {
			projects, versions, actions := []string{sourceProject, targetProject}, allVersions, []string{"version.read"}
			if actor == "source-only" {
				versions = sourceVersions
			}
			if actor == "denied-project" {
				projects = []string{sourceProject}
			}
			if actor == "missing-read" {
				actions = []string{"job.read"}
			}
			tokens[actor] = searchToken(s, domain.CredentialScope{Kind: "workflow", Actions: actions, ProjectIDs: projects, VersionIDs: versions})
		}
		// Only this credential's captured grant is stale; other matrix readers keep
		// their real current revisions and do not acquire authority from the seed.
		stale, _ := json.Marshal(map[string]int64{sourceProject: 999999})
		_, e = s.db.Exec(`UPDATE scoped_credentials SET permissions=? WHERE token_hash=?`, stale, sec.Verifier("workflow", tokens["stale"]))
		Expect(e).NotTo(HaveOccurred())
		q := query.New(s, a, f.bytes)
		handler := httpv1.New(q, nil, nil)
		server := httptest.NewUnstartedServer(nil)
		base := "http://" + server.Listener.Addr().String()
		endpoint, e := url.Parse(base)
		Expect(e).NotTo(HaveOccurred())
		server.Config.Handler, e = httpauth.New(a, httpauth.Policy{Hosts: []string{endpoint.Host}, Origins: []string{base}}, handler)
		Expect(e).NotTo(HaveOccurred())
		server.Start()
		defer server.Close()
		binary := os.Getenv("HAERYONG_REFERENCE_MATRIX_BINARY")
		Expect(binary).NotTo(BeEmpty())
		cliRoot := GinkgoT().TempDir()
		credentials := map[string]string{}
		for actor, token := range tokens {
			raw, _ := json.Marshal(map[string]string{"endpoint": server.URL, "profile": "matrix", "library_id": health.Library.ID, "token": token})
			p := filepath.Join(cliRoot, actor+".json")
			Expect(os.WriteFile(p, raw, 0600)).To(Succeed())
			credentials[actor] = p
		}
		epoch, _ := referenceHead(s)
		events := []domain.Event{}
		after := int64(0)
		for {
			batch, err := s.Events(ctx, epoch, after, 64)
			Expect(err).NotTo(HaveOccurred())
			if len(batch) == 0 {
				break
			}
			events = append(events, batch...)
			after = batch[len(batch)-1].Sequence
		}
		eventByVersion := map[string]domain.Event{}
		for _, event := range events {
			if event.Kind == "references.updated" {
				eventByVersion[event.ResourceID] = event
			}
		}
		// Nonexistent targets have no target event. Source-only readiness still has
		// exactly its own real persisted event and conveys no target-derived count.
		misses := 0
		for i := range rows {
			row := &rows[i]
			parts := strings.Split(row.ID, "|")
			actor, state, surface := parts[0], parts[1], parts[2]
			token, source, target := tokens[actor], sources[state], targets[state]
			denied := actor == "missing-read" || actor == "stale"
			authorized := actor == "owner" || actor == "source+target"
			resolution, resolveStatus := "unavailable", 404
			if authorized && (state == "active" || state == "archived") {
				resolution, resolveStatus = "resolved", 200
			}
			if authorized && state == "purged-edge" {
				resolution, resolveStatus = "tombstoned", 410
			}
			listStatus := 200
			if denied {
				listStatus, resolveStatus = 403, 403
			}
			row.Expected = map[string]any{"source_version": source, "target_version": target, "resolution": resolution, "list_status": listStatus, "resolve_status": resolveStatus, "reference_state_unchanged": true}
			before := referenceRowsSnapshot(s)
			err := func() error {
				base := "/api/v1/versions/" + source + "/references"
				resolve := strings.Contains(surface, "resolve")
				status := listStatus
				if resolve {
					base += "/resolve?reference_id=pin"
					status = resolveStatus
				}
				var raw, responseRaw []byte
				if strings.HasPrefix(surface, "event-") {
					if actor == "stale" {
						if surface == "event-live" {
							_, err := a.FilterEvent(ctx, token, eventByVersion[source])
							if !errors.Is(err, domain.ErrDenied) {
								return fmt.Errorf("stale live auth: %v", err)
							}
						} else {
							_, err := a.Events(ctx, token, epoch, 0)
							if !errors.Is(err, domain.ErrDenied) {
								return fmt.Errorf("stale replay auth: %v", err)
							}
						}
						row.Actual = map[string]any{"auth_denied": true}
						return nil
					}
					projected := []domain.Event{}
					originals := []domain.Event{}
					if surface == "event-live" {
						for _, id := range []string{source, target} {
							event, ok := eventByVersion[id]
							if !ok {
								continue
							}
							value, err := a.FilterEvent(ctx, token, event)
							if err != nil {
								return err
							}
							projected = append(projected, value)
							originals = append(originals, event)
						}
					} else {
						after := int64(0)
						for {
							batch, err := a.Events(ctx, token, epoch, after)
							if err != nil {
								return err
							}
							if len(batch) == 0 {
								break
							}
							for _, value := range batch {
								for _, id := range []string{source, target} {
									original, ok := eventByVersion[id]
									if ok && original.Sequence == value.Sequence {
										projected = append(projected, value)
										originals = append(originals, original)
									}
								}
							}
							after = batch[len(batch)-1].Sequence
						}
					}
					row.Actual = projected
					expectedCount := 1
					if state != "unknown" {
						expectedCount = 2
					}
					if len(projected) != expectedCount {
						return fmt.Errorf("event membership %d, expected %d", len(projected), expectedCount)
					}
					seen := map[int64]bool{}
					for i, event := range projected {
						original := originals[i]
						if seen[original.Sequence] {
							return fmt.Errorf("duplicate projected event sequence %d", original.Sequence)
						}
						seen[original.Sequence] = true
						visible := !denied && original.Sequence == eventByVersion[source].Sequence || authorized && (state == "active" || state == "archived") && original.Sequence == eventByVersion[target].Sequence
						want := original
						if !visible {
							want = domain.Event{StreamEpoch: original.StreamEpoch, Sequence: original.Sequence, ResourceKind: "library", Kind: "library.invalidated"}
						} else {
							version := source
							if original.Sequence == eventByVersion[target].Sequence {
								version = target
							}
							// Read current materialization independently of the stored event.
							var materialization domain.ReferenceMaterialization
							if err := s.Write(ctx, func(tx app.WriteTx) error {
								var err error
								materialization, err = tx.ReferenceMaterialization(version)
								return err
							}); err != nil {
								return err
							}
							if err := matrixVisibleEvent(event, version, materialization.Revision); err != nil {
								return err
							}
						}
						if !reflect.DeepEqual(event, want) {
							return fmt.Errorf("event differs from exact persisted or masked projection (sequence %d, visible %t)", original.Sequence, visible)
						}
					}
					return nil
				}
				if strings.HasPrefix(surface, "cli-") {
					args := []string{"references", "list", "--version", source}
					if resolve {
						args = []string{"references", "resolve", "--version", source, "--reference-id", "pin"}
					}
					args = append(args, "--profile", "matrix", "--client-dir", filepath.Join(cliRoot, actor), "--credential", credentials[actor], "--json")
					commandCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
					defer cancel()
					out, err := exec.CommandContext(commandCtx, binary, args...).Output()
					exit := 0
					if err != nil {
						var failure *exec.ExitError
						if !errors.As(err, &failure) {
							return fmt.Errorf("CLI failed before execution")
						}
						exit = failure.ExitCode()
					}
					expected := 0
					if status == 403 {
						expected = 3
					}
					if status == 404 {
						expected = 3
					}
					if status == 410 {
						expected = 4
					}
					row.Actual = map[string]any{"exit": exit, "response": string(out)}
					if exit != expected {
						return fmt.Errorf("CLI exit %d, expected %d", exit, expected)
					}
					var envelope map[string]json.RawMessage
					if json.Unmarshal(out, &envelope) != nil {
						return fmt.Errorf("CLI invalid JSON")
					}
					responseRaw = out
					raw = envelope["data"]
					if status != 200 {
						raw = out
					}
					row.Expected.(map[string]any)["cli_command"] = []string{"haeryong", "references", strings.TrimPrefix(surface, "cli-"), "--version", "<source-version>", "--credential", "<redacted>"}
				} else {
					method := "GET"
					if strings.HasPrefix(surface, "head-") {
						method = "HEAD"
					}
					response := referenceHTTP(handler, method, token, base)
					raw = response.Body.Bytes()
					responseRaw = raw
					row.Actual = map[string]any{"status": response.Code, "body": string(raw)}
					if response.Code != status {
						return fmt.Errorf("HTTP %d, expected %d", response.Code, status)
					}
					if method == "HEAD" {
						if len(raw) != 0 {
							return fmt.Errorf("HEAD has body")
						}
						return nil
					}
					var envelope map[string]json.RawMessage
					if json.Unmarshal(raw, &envelope) != nil {
						return fmt.Errorf("HTTP invalid JSON")
					}
					if status == 200 {
						raw = envelope["data"]
					}
				}
				hidden := []string{}
				if resolution != "resolved" || denied {
					hidden = append(hidden, "F10TitleCanary", "F10BodyCanary", "F10RegionLabelCanary", edges[state])
				}
				if resolution == "unavailable" || denied {
					hidden = append(hidden, target, documents[state], targetProject)
				}
				if err := matrixHiddenData(responseRaw, hidden); err != nil {
					return err
				}
				if status != 200 {
					return matrixError(raw, strings.HasPrefix(surface, "cli-"), status, documents[state], target)
				}
				if err := matrixSuccess(responseRaw, strings.HasPrefix(surface, "cli-")); err != nil {
					return err
				}
				return matrixReferenceData(raw, resolve, resolution, documents[state], target, edges[state])
			}()
			if !bytes.Equal(referenceRowsSnapshot(s), before) {
				err = fmt.Errorf("read changed references/materialization/edge/job/checkpoint/outbox state")
			}
			row.Status = "pass"
			if err != nil {
				row.Status = "fail"
				row.Miss = err.Error()
				misses++
			}
			proof["rows"] = rows
			save()
		}
		proof["misses"] = misses
		proof["complete"] = misses == 0
		save()
		AddReportEntry("manual_references coverage and exhaustive remainder", ReportEntryVisibilityFailureOrVerbose, map[string]any{"canonical_cells": len(coverage), "remaining_cells": len(rows), "misses": misses})
		Expect(misses).To(BeZero(), "all predeclared rows were evaluated and retained before this assertion")
	})
})
