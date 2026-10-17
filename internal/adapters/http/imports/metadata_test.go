package imports

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"path/filepath"
	"syscall"
	"time"

	"fmt"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"haeryong.local/haeryong/internal/adapters/contentstore"
	importer "haeryong.local/haeryong/internal/application/import"
	appstorage "haeryong.local/haeryong/internal/application/storage"
	"haeryong.local/haeryong/internal/domain"
	"strings"
)

var _ = It("TestActualHTTPMetadataReferenceAndInlineBytesShareReplayIdentity", Label("persistence"), func() {
	t := GinkgoT()

	f := newHTTPFixture(t)
	result := f.upload("file", "metadata.html")
	status, _, data := f.put(result, "_input", "file", []byte("<html>metadata</html>"), 0, true)
	requireStatus(t, status, 200, data)
	f.seal(result)
	key := operation(t)
	metadata := []byte(`{ "document": { "title": "Exact metadata", "question": "` + strings.Repeat("author text ", 6000) + `" } }`)
	status, data = f.json("POST", "/api/v1/uploads", operation(t), map[string]any{"purpose": "metadata", "input_mode": "object", "target": map[string]any{"operation_id": key, "action": "import", "project_id": f.project, "document_id": nil}})
	requireStatus(t, status, 201, data)
	created := valueAs[struct {
		ID    string `json:"upload_id"`
		Entry string `json:"transport_entry"`
	}](t, data)
	status, _, data = f.put(created.ID, created.Entry, "file", metadata, 0, true)
	requireStatus(t, status, 200, data)
	sealed := f.seal(created.ID)
	Expect(sealed.ObjectRef == nil).To(BeFalse(), func() string { return fmt.Sprint("metadata reference absent") })

	previewKey := operation(t)
	status, data = f.json("POST", "/api/v1/packages/validate", previewKey, map[string]any{"upload_id": result, "metadata_ref": sealed.ObjectRef})
	requireStatus(t, status, 200, data)
	previewInline := append([]byte(`{"upload_id":"`+result+`","metadata":`), metadata...)
	previewInline = append(previewInline, '}')
	status, _, data = f.request("POST", "/api/v1/packages/validate", bytes.NewReader(previewInline), map[string]string{"Content-Type": "application/json", "Idempotency-Key": previewKey})
	requireStatus(t, status, 200, data)
	changedPreview := bytes.Replace(previewInline, []byte("Exact metadata"), []byte("Changed metadata"), 1)
	status, _, data = f.request("POST", "/api/v1/packages/validate", bytes.NewReader(changedPreview), map[string]string{"Content-Type": "application/json", "Idempotency-Key": previewKey})
	requireStatus(t, status, 409, data)
	status, data = f.json("POST", "/api/v1/imports", key, map[string]any{"upload_id": result, "document_id": nil, "metadata_ref": sealed.ObjectRef})
	requireStatus(t, status, 202, data)
	job := valueAs[struct {
		ID string `json:"job_id"`
	}](t, data)
	completed := waitJob(t, f, job.ID, "completed")
	var output struct {
		VersionID string `json:"version_id"`
	}
	Expect(json.Unmarshal(completed.Result, &output) != nil || output.VersionID == "").To(BeFalse(), func() string { return fmt.Sprint("metadata import missing result") })

	version, err := f.store.Version(context.Background(), output.VersionID)
	Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })

	digest := sha256.Sum256(metadata)
	Expect(version.MetadataHash != hex.EncodeToString(digest[:]) || version.Title != "Exact metadata").To(BeFalse(), func() string {
		return fmt.Sprintf("submitted metadata readback mismatch: exact_hash=%t title=%q", version.MetadataHash == hex.EncodeToString(digest[:]), version.Title)
	})

	inline := append([]byte(`{"upload_id":"`+result+`","document_id":null,"metadata":`), metadata...)
	inline = append(inline, '}')
	status, _, data = f.request("POST", "/api/v1/imports", bytes.NewReader(inline), map[string]string{"Content-Type": "application/json", "Idempotency-Key": key})
	requireStatus(t, status, 202, data)
	replay := valueAs[struct {
		ID string `json:"job_id"`
	}](t, data)
	Expect(replay.ID != job.ID).To(BeFalse(), func() string { return fmt.Sprint("inline/reference equivalent bytes created another job") })

	changed := bytes.Replace(inline, []byte("Exact metadata"), []byte("Changed metadata"), 1)
	status, _, data = f.request("POST", "/api/v1/imports", bytes.NewReader(changed), map[string]string{"Content-Type": "application/json", "Idempotency-Key": key})
	requireStatus(t, status, 409, data)
	status, data = f.json("POST", "/api/v1/imports", operation(t), map[string]any{"upload_id": result, "document_id": nil, "metadata_ref": sealed.ObjectRef})
	requireStatus(t, status, 400, data)
	f.restart()
	f.verify(version, map[string]string{"metadata.html": "<html>metadata</html>"}, nil)
})
var _ = It("TestActualHTTPMalformedMetadataHasFieldDiagnosticAndRetainsSealedPayload", Label("persistence"), func() {
	t := GinkgoT()

	f := newHTTPFixture(t)
	id := f.upload("file", "retained.html")
	status, _, data := f.put(id, "_input", "file", []byte("<html>retained</html>"), 0, true)
	requireStatus(t, status, 200, data)
	f.seal(id)
	status, data = f.json("POST", "/api/v1/imports", operation(t), map[string]any{"upload_id": id, "document_id": nil, "metadata": map[string]any{"document": map[string]any{"title": 17}}})
	requireStatus(t, status, 422, data)
	var failure struct {
		Error struct {
			Code    string         `json:"code"`
			Details map[string]any `json:"details"`
		} `json:"error"`
	}
	Expect(json.Unmarshal(data, &failure) != nil || failure.Error.Code != "PACKAGE_INVALID").To(BeFalse(), func() string { return fmt.Sprint("metadata error was hidden") })

	pointer, ok := failure.Error.Details["field"].(string)
	Expect(!ok || !strings.Contains(pointer, "title")).To(BeFalse(), func() string { return fmt.Sprint("metadata field diagnostic missing") })

	status, data = f.json("POST", "/api/v1/packages/validate", "", map[string]any{"upload_id": id, "metadata": map[string]any{"document": map[string]any{"title": 17}}})
	requireStatus(t, status, 422, data)
	{
		detail := f.detail(id)
		Expect(detail.State != "sealed" || !detail.Reusable).To(BeFalse(), func() string { return fmt.Sprint("invalid optional metadata discarded sealed payload") })
	}

	version := f.publish(id, operation(t))
	f.verify(version, map[string]string{"retained.html": "<html>retained</html>"}, nil)
})
var

// The negative fixtures follow tests/contracts/contract.py metadata(), including
// collection-local uniqueness and exact retained source identity. They do not
// require optional enrichment for ordinary payloads.
_ = It("TestActualHTTPMetadataSemanticNegativesPreserveReusablePayload", Label("persistence"), func() {
	t := GinkgoT()

	f := newHTTPFixture(t)
	id := f.upload("directory", "metadata rules")
	payload := []byte("<html>source bytes</html>\n")
	status, _, data := f.put(id, "evidence.html", "file", payload, 0, true)
	requireStatus(t, status, 200, data)
	status, _, data = f.put(id, "assets", "directory", []byte(`{}`), 0, true)
	requireStatus(t, status, 200, data)
	f.seal(id)
	hash := sha256.Sum256(payload)
	digest := hex.EncodeToString(hash[:])
	source := `{"id":"s","kind":"snapshot","retention_mode":"reference_only","snapshot_sha256":"` + digest + `"}`
	evidence := `{"id":"e","source_id":"s","capture_status":"reference_only","path":"evidence.html"}`
	reference := `{"id":"r","document_id":"` + operation(t) + `","version_id":"` + operation(t) + `","page_path":null,"region_id":null,"resolution":"unresolved"}`
	cases := []struct{ name, metadata, field string }{
		{"git_missing_identity", `{"sources":[{"id":"s","kind":"git","retention_mode":"reference_only"}]}`, "/sources"},
		{"git_short_revision", `{"sources":[{"id":"s","kind":"git","retention_mode":"reference_only","repo_id":"` + operation(t) + `","object_format":"sha1","exact_revision":"abc123"}]}`, "/sources"},
		{"git_snapshot_identity_mixed", `{"sources":[{"id":"s","kind":"git","retention_mode":"reference_only","repo_id":"` + operation(t) + `","object_format":"sha1","exact_revision":"` + strings.Repeat("a", 40) + `","snapshot_sha256":"` + digest + `"}]}`, "/sources"},
		{"snapshot_missing_identity", `{"sources":[{"id":"s","kind":"snapshot","retention_mode":"reference_only"}]}`, "/sources"},
		{"snapshot_git_identity_mixed", `{"sources":[{"id":"s","kind":"snapshot","retention_mode":"reference_only","snapshot_sha256":"` + digest + `","repo_id":"` + operation(t) + `"}]}`, "/sources"},
		{"selected_missing_bytes", `{"sources":[{"id":"s","kind":"snapshot","retention_mode":"selected_snapshot","snapshot_sha256":"` + digest + `","asset_path":"absent.html"}]}`, "/sources"},
		{"selected_directory_is_not_bytes", `{"sources":[{"id":"s","kind":"snapshot","retention_mode":"selected_snapshot","snapshot_sha256":"` + digest + `","asset_path":"assets"}]}`, "/sources"},
		{"selected_git_missing_bytes_hash", `{"sources":[{"id":"s","kind":"git","retention_mode":"selected_snapshot","repo_id":"` + operation(t) + `","object_format":"sha1","exact_revision":"` + strings.Repeat("a", 40) + `","asset_path":"evidence.html"}]}`, "/sources"},
		{"selected_wrong_hash", `{"sources":[{"id":"s","kind":"snapshot","retention_mode":"selected_snapshot","snapshot_sha256":"` + strings.Repeat("0", 64) + `","asset_path":"evidence.html"}]}`, "/sources"},
		{"range_missing_end", `{"sources":[` + source + `],"evidence":[` + strings.TrimSuffix(evidence, "}") + `,"line_start":1}]}`, "/evidence"},
		{"range_missing_start", `{"sources":[` + source + `],"evidence":[` + strings.TrimSuffix(evidence, "}") + `,"line_end":1}]}`, "/evidence"},
		{"range_reversed", `{"sources":[` + source + `],"evidence":[` + strings.TrimSuffix(evidence, "}") + `,"line_start":9,"line_end":2}]}`, "/evidence"},
		{"region_missing_page", `{"regions":[{"id":"overview"}]}`, "/regions"},
		{"reference_region_without_page", `{"references":[` + strings.Replace(reference, `"region_id":null`, `"region_id":"overview"`, 1) + `]}`, "/references"},
		{"identical_duplicate_source", `{"sources":[` + source + `,` + source + `]}`, "/sources"},
		{"identical_duplicate_evidence", `{"sources":[` + source + `],"evidence":[` + evidence + `,` + evidence + `]}`, "/evidence"},
		{"identical_duplicate_reference", `{"references":[` + reference + `,` + reference + `]}`, "/references"},
		{"identical_duplicate_region", `{"regions":[{"id":"overview","page_path":"evidence.html"},{"id":"overview","page_path":"evidence.html"}]}`, "/regions"},
		{"missing_local_source", `{"evidence":[` + evidence + `]}`, "/evidence"},
		{"missing_local_evidence", `{"regions":[{"id":"overview","page_path":"evidence.html","evidence_ids":["missing"]}]}`, "/regions"},
	}
	for _, test := range cases {
		func() {
			By(test.name)
			metadata := json.RawMessage(test.metadata)
			for _, route := range []string{"/api/v1/packages/validate", "/api/v1/imports"} {
				request := map[string]any{"upload_id": id, "metadata": metadata}
				if route == "/api/v1/imports" {
					request["document_id"] = nil
				}
				status, data := f.json("POST", route, operation(t), request)
				requireStatus(t, status, 422, data)
				var failure struct {
					Error struct {
						Code    string `json:"code"`
						Details struct {
							Field string `json:"field"`
						} `json:"details"`
					} `json:"error"`
				}
				{
					err := json.Unmarshal(data, &failure)
					Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })
				}
				Expect(failure.Error.Code != "PACKAGE_INVALID" && failure.Error.Code != "INVALID_EVIDENCE").To(BeFalse(), func() string { return fmt.Sprint("metadata rejection lost canonical error", failure.Error.Code) })
				Expect(!strings.HasPrefix(failure.Error.Details.Field, test.field)).To(BeFalse(), func() string {
					return fmt.Sprintf("%s diagnostic field %q does not identify %s", route, failure.Error.Details.Field, test.field)
				})

			}
			{
				detail := f.detail(id)
				Expect(detail.State != "sealed" || !detail.Reusable).To(BeFalse(), func() string { return fmt.Sprint("malformed optional metadata consumed payload") })
			}

		}()
	}
	// An unresolved exact Haeryong relationship is optional enrichment, not a
	// missing local source/evidence reference or a reason to discard the payload.
	valid := json.RawMessage(`{"sources":[{"id":"s","kind":"snapshot","retention_mode":"selected_snapshot","snapshot_sha256":"` + digest + `","asset_path":"evidence.html"}],"evidence":[` + evidence + `],"pages":[{"page_path":"evidence.html"}],"regions":[{"id":"overview","page_path":"evidence.html","evidence_ids":["e"]}],"references":[` + reference + `]}`)
	status, data = f.json("POST", "/api/v1/packages/validate", operation(t), map[string]any{"upload_id": id, "metadata": valid})
	requireStatus(t, status, 200, data)
	status, data = f.json("POST", "/api/v1/imports", operation(t), map[string]any{"upload_id": id, "document_id": nil, "metadata": valid})
	requireStatus(t, status, 202, data)
	accepted := valueAs[struct {
		ID string `json:"job_id"`
	}](t, data)
	waitJob(t, f, accepted.ID, "completed")
	// The same ordinary directory also remains admissible without metadata.
	version := f.publish(id, operation(t))
	f.verify(version, map[string]string{"evidence.html": string(payload)}, map[string]string{"assets": "directory"})
})
var _ = It("TestActualHTTPFailedImportMetadataReferenceReplayAfterAbandonAndGC", Label("persistence"), func() {
	t := GinkgoT()

	f := newHTTPFixture(t)
	result := f.upload("file", "failed-job.html")
	status, _, data := f.put(result, "_input", "file", []byte("<html>retained after failure</html>"), 0, true)
	requireStatus(t, status, 200, data)
	f.seal(result)
	key := operation(t)
	makeMetadata := func(title string) (string, *importer.ObjectRef) {
		status, data := f.json("POST", "/api/v1/uploads", operation(t), map[string]any{"purpose": "metadata", "input_mode": "object", "target": map[string]any{"operation_id": key, "action": "import", "project_id": f.project, "document_id": nil}})
		requireStatus(t, status, 201, data)
		created := valueAs[struct {
			ID    string `json:"upload_id"`
			Entry string `json:"transport_entry"`
		}](t, data)
		content := []byte(`{"document":{"title":"` + title + `"}}`)
		status, _, data = f.put(created.ID, created.Entry, "file", content, 0, true)
		requireStatus(t, status, 200, data)
		sealed := f.seal(created.ID)
		Expect(sealed.ObjectRef == nil).To(BeFalse(), func() string { return fmt.Sprint("metadata object reference missing") })

		return created.ID, sealed.ObjectRef
	}
	metadataID, ref := makeMetadata("original failed job")
	sameMetadataID, sameRef := makeMetadata("original failed job")
	Expect(sameRef.ID == ref.ID || sameRef.Hash != ref.Hash).To(BeFalse(), func() string {
		return fmt.Sprint("equivalent metadata fixture did not use a distinct handle with exact bytes")
	})

	_, changedRef := makeMetadata("different valid metadata")
	// Pause the fixture executor after sealing so the fault is applied to this
	// accepted import's generated-object write, rather than an unrelated job.
	f.stop()
	select {
	case err := <-f.worker:
		if err != nil && !errors.Is(err, context.Canceled) {
			Fail(fmt.Sprint(err))
		}
	case <-time.After(10 * time.Second):
		Fail(fmt.Sprint("fixture worker did not stop"))
	}
	f.stop = nil
	request := map[string]any{"upload_id": result, "document_id": nil, "metadata_ref": ref}
	status, data = f.json("POST", "/api/v1/imports", key, request)
	requireStatus(t, status, 202, data)
	accepted := valueAs[struct {
		ID string `json:"job_id"`
	}](t, data)
	original := f.store.Objects
	injected, err := contentstore.New(filepath.Join(f.writer.Profile.DataDir, "storage"), contentstore.WithFault(func(boundary contentstore.Boundary) error {
		if boundary == contentstore.Write {
			return syscall.ENOSPC
		}
		return nil
	}))
	Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })

	f.store.Objects = injected
	processErr := f.core.Process(context.Background(), accepted.ID)
	f.store.Objects = original
	{
		err := injected.Close()
		Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })
	}
	Expect(!errors.Is(processErr, syscall.ENOSPC)).To(BeFalse(), func() string { return fmt.Sprintf("actual generated-object write fault not reached: %v", processErr) })

	waitJob(t, f, accepted.ID, "failed")
	for _, id := range []string{metadataID, sameMetadataID} {
		status, data = f.json("POST", "/api/v1/uploads/"+id+"/abandon", operation(t), map[string]any{})
		requireStatus(t, status, 200, data)
	}
	var object domain.Object
	{
		err := f.store.Write(context.Background(), func(tx appstorage.WriteTx) error { var err error; object, err = tx.Object(ref.Hash); return err })
		Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })
	}
	{

		err := f.store.Collect(context.Background(), object)
		Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprintf("failed job or abandoned metadata retained an active pin: %v", err) })
	}
	{

		err := f.store.Write(context.Background(), func(tx appstorage.WriteTx) error { _, err := tx.Object(ref.Hash); return err })
		Expect(!errors.Is(err, domain.ErrNotFound)).To(BeFalse(), func() string { return fmt.Sprint("metadata object was not actually collected", err) })
	}

	status, data = f.json("POST", "/api/v1/imports", key, request)
	requireStatus(t, status, 202, data)
	replay := valueAs[struct {
		ID string `json:"job_id"`
	}](t, data)
	Expect(replay.ID != accepted.ID).To(BeFalse(), func() string { return fmt.Sprint("completed receipt replay reminted a job after metadata GC") })

	status, data = f.json("POST", "/api/v1/imports", key, map[string]any{"upload_id": result, "document_id": nil, "metadata_ref": sameRef})
	requireStatus(t, status, 202, data)
	Expect(valueAs[struct {
		ID string `json:"job_id"`
	}](t, data).ID != accepted.ID).To(BeFalse(), func() string {
		return fmt.Sprint("same exact metadata through another collected handle reminted a job")
	})
	{

		job := waitJob(t, f, accepted.ID, "failed")
		Expect(len(job.Result) != 0).To(BeFalse(), func() string { return fmt.Sprint("failed job replay became a success publication") })
	}

	status, data = f.json("POST", "/api/v1/imports", key, map[string]any{"upload_id": result, "document_id": nil, "metadata_ref": changedRef})
	requireStatus(t, status, 409, data)
})
