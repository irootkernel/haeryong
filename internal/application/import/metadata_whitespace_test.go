package importer_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"strconv"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	importer "haeryong.local/haeryong/internal/application/import"
	app "haeryong.local/haeryong/internal/application/storage"
	"haeryong.local/haeryong/internal/domain"
)

// Every authored metadata section, with multiple array members to exercise
// whitespace both after colons and after commas in the streaming parser.
const whitespaceMetadata = `{"schema_version":"1.0","document":{"title":"Whitespace 日本語","question":"Why: this, exactly?"},"version":{"base_version_id":null,"source_intent":"historical"},"content":{"default_page":"hello.html","external_dependencies":[{"url":"https://example.invalid/a.js","version":"1","integrity":"fixture"},{"url":"https://example.invalid/b.css"}]},"pages":[{"page_path":"hello.html","label":"Page"}],"sources":[{"id":"s","kind":"snapshot","retention_mode":"reference_only","snapshot_sha256":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}],"evidence":[{"id":"e","source_id":"s","capture_status":"reference_only","path":"hello.html","line_start":1,"line_end":2}],"regions":[{"id":"overview","page_path":"hello.html","label":"Overview","evidence_ids":["e"]},{"id":"detail","page_path":"hello.html","label":"Detail","evidence_ids":[]}],"references":[{"id":"r1","document_id":"11111111-1111-4111-8111-111111111111","version_id":"22222222-2222-4222-8222-222222222222","page_path":"other.html","region_id":"overview","resolution":"unresolved","label":"Other"},{"id":"r2","document_id":"11111111-1111-4111-8111-111111111111","version_id":"22222222-2222-4222-8222-222222222222","page_path":null,"region_id":null}],"generation":{"tool":"whitespace fixture","skill_contract_version":"1.0","provider":null,"model":null}}`

func formatMetadata(raw, format string) string {
	if format == "compact" {
		return raw
	}
	if format == "token spaces" {
		var out strings.Builder
		quoted, escaped := false, false
		for _, b := range []byte(raw) {
			if !quoted && strings.ContainsRune("{}[]:,", rune(b)) {
				out.WriteString(" \t\r\n")
				out.WriteByte(b)
				out.WriteString(" \t\r\n")
			} else {
				out.WriteByte(b)
			}
			if b == '"' && !escaped {
				quoted = !quoted
			}
			escaped = quoted && b == '\\' && !escaped
		}
		return out.String()
	}
	indent := "  "
	if format == "tabs" {
		indent = "\t"
	}
	var out bytes.Buffer
	Expect(json.Indent(&out, []byte(raw), "", indent)).To(Succeed())
	if format == "CRLF" {
		return strings.ReplaceAll(out.String(), "\n", "\r\n") + "\r\n"
	}
	return out.String() + "\n"
}

func metadataValue(raw []byte) any {
	GinkgoHelper()
	var value any
	Expect(json.Unmarshal(raw, &value)).To(Succeed())
	return value
}

func sealWhitespaceMetadata(f *fixture, operation, raw string) *importer.ObjectRef {
	GinkgoHelper()
	t := GinkgoT()
	response, err := f.core.Create(ctx, f.token, key(t), importer.CreateRequest{
		Purpose: "metadata", InputMode: "object",
		Target: encodedTest(map[string]any{"operation_id": operation, "action": "import", "project_id": f.project, "document_id": nil}),
	})
	Expect(err).NotTo(HaveOccurred())
	var created struct {
		ID string `json:"upload_id"`
	}
	Expect(json.Unmarshal(response.Body, &created)).To(Succeed())
	upload := created.ID
	sum := sha256.Sum256([]byte(raw))
	hash, length := hex.EncodeToString(sum[:]), int64(len(raw))
	ack, err := f.core.Put(ctx, f.token, upload, importer.Chunk{Path: "_input", Kind: "file", Hash: hash, Length: &length, Final: true}, strings.NewReader(raw))
	Expect(err).NotTo(HaveOccurred())
	cp := ack.Checkpoint
	cp.Generation++
	cp.Complete = true
	response, err = f.core.Complete(ctx, f.token, upload, key(t), cp)
	Expect(err).NotTo(HaveOccurred())
	Expect(f.core.Process(ctx, decoded(t, response)["job_id"])).To(Succeed())
	sealed, err := f.store.Upload(ctx, upload)
	Expect(err).NotTo(HaveOccurred())
	Expect(sealed.State).To(Equal("sealed"))
	Expect(sealed.ObjectHash).To(Equal(hash))
	return &importer.ObjectRef{ID: upload, Hash: hash, Size: strconv.Itoa(len(raw)), MediaType: "application/json"}
}

var _ = Describe("Metadata JSON whitespace", Label("persistence"), func() {
	for _, format := range []string{"compact", "two spaces", "tabs", "CRLF", "token spaces"} {
		for _, delivery := range []string{"inline", "sealed object"} {
			It("validates and publishes identical parsed and indexed values with "+format+" via "+delivery, func() {
				t := GinkgoT()
				f := newFixture(t)
				upload := f.sealed(t, nil, "<html><section id=overview>Overview</section><section id=detail>Detail</section></html>")
				raw := formatMetadata(whitespaceMetadata, format)
				want := metadataValue([]byte(whitespaceMetadata)).(map[string]any)
				Expect(metadataValue([]byte(raw))).To(Equal(want))
				operation, version := key(t), key(t)
				input := importer.ImportRequest{UploadID: upload, DocumentPresent: true, VersionID: version}
				if delivery == "sealed object" {
					input.MetadataRef = sealWhitespaceMetadata(f, operation, raw)
				} else {
					input.Metadata = strings.NewReader(raw)
				}
				preview, err := f.core.Validate(ctx, f.token, "", input)
				Expect(err).NotTo(HaveOccurred())
				Expect(preview.Status).To(Equal(200))
				if delivery == "inline" {
					input.Metadata = strings.NewReader(raw)
				}
				response, err := f.core.Import(ctx, f.token, operation, input)
				Expect(err).NotTo(HaveOccurred())
				job := decoded(t, response)["job_id"]
				// Compare every staged collection semantically; its stored raw JSON
				// is deliberately allowed to retain the author's formatting.
				Expect(f.store.Write(ctx, func(tx app.WriteTx) error {
					for _, kind := range []string{"pages", "regions", "sources", "evidence", "references"} {
						rows, err := tx.ImportMetadata(job, kind, "", 64)
						if err != nil {
							return err
						}
						values := []any{}
						for _, row := range rows {
							values = append(values, metadataValue(row.Value))
						}
						Expect(values).To(ConsistOf(want[kind]), kind)
					}
					return nil
				})).To(Succeed())
				Expect(f.core.Process(ctx, job)).To(Succeed())
				published, err := f.store.Version(ctx, version)
				Expect(err).NotTo(HaveOccurred())
				Expect(published.Sequence).To(BeNumerically(">", 0))
				Expect(published.Title).To(Equal("Whitespace 日本語"))
				Expect(published.SourceBasis).To(Equal("evidence_recorded"))
				Expect(published.DefaultPage).To(HaveValue(Equal("hello.html")))
				readObject := func(hash string) []byte {
					GinkgoHelper()
					var data []byte
					Expect(f.store.Read(ctx, domain.Object{Hash: hash, Size: objectSize(t, f.store, hash)}, domain.Pin{Kind: "test", OwnerID: version}, func(r io.ReadSeeker) error {
						var err error
						data, err = io.ReadAll(r)
						return err
					})).To(Succeed())
					sum := sha256.Sum256(data)
					Expect(hex.EncodeToString(sum[:])).To(Equal(hash))
					return data
				}
				Expect(readObject(published.MetadataHash)).To(Equal([]byte(raw)))
				manifest := metadataValue(readObject(published.ManifestSHA)).(map[string]any)
				for _, kind := range []string{"schema_version", "pages", "regions", "sources", "evidence", "references", "generation"} {
					Expect(manifest[kind]).To(Equal(want[kind]), kind)
				}
				for _, kind := range []string{"document", "version", "content"} {
					fields := map[string]any{}
					for field := range want[kind].(map[string]any) {
						Expect(manifest[kind]).To(HaveKey(field))
						fields[field] = manifest[kind].(map[string]any)[field]
					}
					Expect(fields).To(Equal(want[kind]), kind)
				}
				page, err := f.store.Page(ctx, version, "hello.html")
				Expect(err).NotTo(HaveOccurred())
				Expect(metadataValue(page.Metadata)).To(Equal(want["pages"].([]any)[0]))
				for _, item := range want["regions"].([]any) {
					region, err := f.store.Region(ctx, version, "hello.html", item.(map[string]any)["id"].(string))
					Expect(err).NotTo(HaveOccurred())
					Expect(metadataValue(region.Metadata)).To(Equal(item))
				}
				evidence, err := f.store.Evidence(ctx, version, "e")
				Expect(err).NotTo(HaveOccurred())
				Expect(evidence.SourceID).To(Equal("s"))
				Expect(metadataValue(evidence.Metadata)).To(Equal(want["evidence"].([]any)[0]))
			})
		}
	}

	It("retains closed-schema and byte-validity rejection in preview and admission", func() {
		t := GinkgoT()
		f := newFixture(t)
		upload := f.sealed(t, nil, "<html>negative controls</html>")
		for _, raw := range []string{
			`{"schema_version":"1.1"}`, `{"schema_version":"1.0 "}`,
			`{"schema_version":1.0}`, `{"schema_version":null}`, `{"schema_version":true}`,
			`{"schema_version":[]}`, `{"schema_version":{}}`,
			`{"unknown":null}`, `{"document":{"unknown":null}}`,
			`{"schema_version":"1.0","schema_version":"1.0"}`,
			`{"document":{"title":"a","title":"b"}}`,
			`{"references":[{"id":"r","id":"duplicate"}]}`,
			`{"document":{"question":42}}`, `{"content":{"default_page":false}}`,
			`{"schema_version":"1.0"} garbage`,
			"{\"document\":{\"question\":\"\xff\"}}",
			"{\"schema_version\":\v\"1.0\"}", "{\"schema_version\":\u00a0\"1.0\"}",
		} {
			// Exercise legal whitespace around invalid tokens too, without
			// changing the invalid value itself.
			for _, value := range []string{raw, formatMetadata(raw, "token spaces")} {
				By(strconv.Quote(value))
				_, err := f.core.Validate(ctx, f.token, "", importer.ImportRequest{UploadID: upload, Metadata: strings.NewReader(value)})
				Expect(err).To(MatchError(importer.ErrPackageInvalid))
				_, err = f.core.Import(ctx, f.token, key(t), importer.ImportRequest{UploadID: upload, DocumentPresent: true, Metadata: strings.NewReader(value)})
				Expect(err).To(MatchError(importer.ErrPackageInvalid))
			}
		}
	})
})
