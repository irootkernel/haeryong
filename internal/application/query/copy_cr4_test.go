package query_test

import (
	"archive/zip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"haeryong.local/haeryong/internal/adapters/foundation"
	"haeryong.local/haeryong/internal/application/query"
	"haeryong.local/haeryong/internal/application/recovery"
	"haeryong.local/haeryong/internal/domain"
)

// Only targeted fixture metadata is buffered. ZIP payload entries are copied
// unchanged through streams; this helper is not production export evidence.
func cr4Read(destination, format, name string) []byte {
	GinkgoHelper()
	if format == "directory" {
		raw, e := os.ReadFile(filepath.Join(destination, filepath.FromSlash(name)))
		Expect(e).NotTo(HaveOccurred())
		return raw
	}
	r, e := zip.OpenReader(destination)
	Expect(e).NotTo(HaveOccurred())
	defer r.Close()
	for _, f := range r.File {
		if f.Name == name {
			in, e := f.Open()
			Expect(e).NotTo(HaveOccurred())
			raw, e := io.ReadAll(in)
			Expect(e).NotTo(HaveOccurred())
			Expect(in.Close()).To(Succeed())
			return raw
		}
	}
	Fail("fixture metadata entry absent: " + name)
	return nil
}

func cr4Write(destination, format string, changes map[string][]byte) {
	GinkgoHelper()
	if format == "directory" {
		for name, raw := range changes {
			Expect(os.WriteFile(filepath.Join(destination, filepath.FromSlash(name)), raw, 0600)).To(Succeed())
		}
		return
	}
	r, e := zip.OpenReader(destination)
	Expect(e).NotTo(HaveOccurred())
	tmp := destination + ".fixture-rewrite"
	f, e := os.Create(tmp)
	Expect(e).NotTo(HaveOccurred())
	w := zip.NewWriter(f)
	for _, entry := range r.File {
		header := entry.FileHeader
		out, e := w.CreateHeader(&header)
		Expect(e).NotTo(HaveOccurred())
		if raw, ok := changes[entry.Name]; ok {
			_, e = out.Write(raw)
			Expect(e).NotTo(HaveOccurred())
		} else {
			in, e := entry.Open()
			Expect(e).NotTo(HaveOccurred())
			_, e = io.Copy(out, in)
			Expect(e).NotTo(HaveOccurred())
			Expect(in.Close()).To(Succeed())
		}
	}
	Expect(w.Close()).To(Succeed())
	Expect(f.Close()).To(Succeed())
	Expect(r.Close()).To(Succeed())
	Expect(os.Rename(tmp, destination)).To(Succeed())
}

func cr4Resource(destination, format, version, resource string, change func([]byte) []byte, separate ...string) {
	if len(separate) > 0 {
		destination, format = separate[0], "directory"
	}
	GinkgoHelper()
	var index map[string]any
	Expect(json.Unmarshal(cr4Read(destination, format, "export-index.json"), &index)).To(Succeed())
	for _, value := range index["versions"].([]any) {
		v := value.(map[string]any)
		if v["version_id"] != version {
			continue
		}
		name := v["inventory_path"].(string)
		field := "payload_sha256"
		if resource != "inventory" {
			name = path.Join(v["metadata_root"].(string), resource+".jsonl")
			field = resource + "_sha256"
		}
		raw := change(cr4Read(destination, format, name))
		sum := sha256.Sum256(raw)
		v[field] = hex.EncodeToString(sum[:])
		updated, e := json.Marshal(index)
		Expect(e).NotTo(HaveOccurred())
		cr4Write(destination, format, map[string][]byte{name: raw, "export-index.json": updated})
		return
	}
	Fail("fixture selected version absent")
}

func cr4Rows(raw []byte, change func(map[string]any)) []byte {
	GinkgoHelper()
	var out []string
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		var row map[string]any
		Expect(json.Unmarshal([]byte(line), &row)).To(Succeed())
		change(row)
		updated, e := json.Marshal(row)
		Expect(e).NotTo(HaveOccurred())
		out = append(out, string(updated))
	}
	return []byte(strings.Join(out, "\n") + "\n")
}

func cr4Export(format string, separate ...string) (*fixture, string) {
	GinkgoHelper()
	f := newFixture(GinkgoT())
	content := "<html>canonical nested page</html>"
	source := map[string]any{"id": "src", "kind": "snapshot", "retention_mode": "selected_snapshot", "asset_path": "real/page.html", "snapshot_sha256": hash("snapshot identity"), "bytes_sha256": hash(content)}
	metadata, _ := json.Marshal(map[string]any{"schema_version": "1.0", "sources": []any{source}})
	f.correctionVersion(GinkgoT(), map[string]string{"real/page.html": content, "附件.bin": "ordinary attachment", "versions/user/page.html": "<html>ordinary user directory</html>", "café.html": "<html>unicode spelling</html>"}, "alias", "real", string(metadata))
	page := "real/page.html"
	note, e := noteMutation(f, key(GinkgoT()), "POST", f.version, "", noteBody(&page, nil, "first"))
	Expect(e).NotTo(HaveOccurred())
	_, e = noteMutation(f, key(GinkgoT()), "PATCH", note.AnnotationID, note.ETag, encodedNote(map[string]any{"action": "reattach", "anchor": domain.AnnotationAnchor{VersionID: f.version, PagePath: &page}, "selected_text": nil, "context": nil, "context_ref": nil}))
	Expect(e).NotTo(HaveOccurred())
	for _, item := range []struct {
		kind       query.PersonalKind
		page, body string
	}{
		{query.ReaderPreference, "", `{"default_page":"real/page.html"}`},
		{query.ReadingPosition, "", `{"page_path":"real/page.html","page_query":"","page_fragment":"","opened":true}`},
		{query.PagePosition, page, `{"page_query":"","page_fragment":"","position":{"progress":1}}`},
	} {
		_, e = f.queries.PersonalPut(ctx, f.token, key(GinkgoT()), f.version, item.kind, item.page, "", true, []byte(item.body))
		Expect(e).NotTo(HaveOccurred())
	}
	destination := filepath.Join(GinkgoT().TempDir(), "export")
	request := domain.CopyRequest{Action: "export.create", VersionID: f.version, Format: format, Destination: destination, Records: []string{"notes", "reader"}}
	if len(separate) > 0 {
		request.MetadataDestination = separate[0]
	}
	r, e := recovery.New(f.store, f.auth, f.store).Submit(ctx, f.token, key(GinkgoT()), request)
	Expect(e).NotTo(HaveOccurred())
	var a domain.CopyResult
	Expect(json.Unmarshal(r.Body, &a)).To(Succeed())
	Expect(copyTurns(f, a.JobID, 300).State).To(Equal("completed"))
	index := ""
	if len(separate) > 0 {
		index = filepath.Join(separate[0], "export-index.json")
	}
	_, e = foundation.VerifyExport(ctx, destination, index)
	Expect(e).NotTo(HaveOccurred())
	if format == "directory" {
		payload := filepath.Join(destination, "payload")
		if len(separate) > 0 {
			payload = destination
		}
		link, e := os.Readlink(filepath.Join(payload, "alias"))
		Expect(e).NotTo(HaveOccurred())
		Expect(link).To(Equal("real"))
	}
	return f, destination
}

var _ = DescribeTable("CR4 G5 metadata claims require exact canonical inventory facts", func(family, claimed, format string) {
	f, destination := cr4Export(format)
	if family == "source" {
		cr4Resource(destination, format, f.version, "dependencies", func(raw []byte) []byte {
			return cr4Rows(raw, func(row map[string]any) {
				if row["kind"] == "source" {
					row["source"].(map[string]any)["asset_path"] = claimed
				}
			})
		})
	} else {
		cr4Resource(destination, format, f.version, "records", func(raw []byte) []byte {
			return cr4Rows(raw, func(row map[string]any) {
				value := row["value"].(map[string]any)
				if family == "notes" {
					if row["kind"] == "notes" {
						for _, field := range []string{"anchor", "original_anchor"} {
							value[field].(map[string]any)["page_path"] = claimed
						}
					}
					if row["kind"] == "context" {
						value["page_path"] = claimed
					}
				} else if row["kind"] == "reader" && value["kind"] == family {
					v := value["value"].(map[string]any)
					switch family {
					case "reader_preference":
						v["default_page"] = claimed
					case "reading_position":
						v["page_path"] = claimed
					case "page_position":
						value["resource_id"] = row["owner_id"].(string) + "/" + f.version + "/" + claimed
					}
				}
			})
		})
		if family == "notes" {
			cr4Resource(destination, format, f.version, "dependencies", func(raw []byte) []byte {
				return cr4Rows(raw, func(row map[string]any) {
					if row["kind"] == "note_anchor" {
						row["page_path"] = claimed
					}
				})
			})
		}
	}
	_, e := foundation.VerifyExport(ctx, destination, "")
	Expect(e).To(HaveOccurred())
}, func() []TableEntry {
	var entries []TableEntry
	for _, family := range []string{"notes", "reader_preference", "reading_position", "page_position", "source"} {
		for _, claimed := range []string{"alias/page.html", "REAL/page.html"} {
			for _, format := range []string{"directory", "zip"} {
				entries = append(entries, Entry(family+" "+claimed+" "+format, family, claimed, format))
			}
		}
	}
	return entries
}(), Label("persistence"))

var _ = DescribeTable("CR4 G5 selected retained sources cannot waive a required asset", func(asset, state, format string) {
	f, destination := cr4Export(format)
	cr4Resource(destination, format, f.version, "dependencies", func(raw []byte) []byte {
		return cr4Rows(raw, func(row map[string]any) {
			if row["kind"] == "source" {
				row["source"].(map[string]any)["asset_path"] = asset
				row["state"] = state
			}
		})
	})
	_, e := foundation.VerifyExport(ctx, destination, "")
	Expect(e).To(HaveOccurred())
}, func() []TableEntry {
	var entries []TableEntry
	for _, asset := range []string{"missing.html", "real", "alias", "alias/page.html"} {
		for _, state := range []string{"unavailable", "included"} {
			for _, format := range []string{"directory", "zip"} {
				entries = append(entries, Entry(asset+" "+state+" "+format, asset, state, format))
			}
		}
	}
	return entries
}(), Label("persistence"))

var _ = DescribeTable("CR4 G5 serialized inventory must equal actual namespace", func(claimed, format string) {
	f, destination := cr4Export(format)
	cr4Resource(destination, format, f.version, "inventory", func(raw []byte) []byte {
		var rows [][]any
		for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
			var row []any
			Expect(json.Unmarshal([]byte(line), &row)).To(Succeed())
			if row[0] == "real/page.html" {
				row[0] = claimed
			}
			rows = append(rows, row)
		}
		sort.Slice(rows, func(i, j int) bool { return rows[i][0].(string) < rows[j][0].(string) })
		var out []string
		for _, row := range rows {
			b, e := json.Marshal(row)
			Expect(e).NotTo(HaveOccurred())
			out = append(out, string(b))
		}
		return []byte(strings.Join(out, "\n") + "\n")
	})
	_, e := foundation.VerifyExport(ctx, destination, "")
	Expect(e).To(HaveOccurred())
}, Entry("ancestor alias directory", "alias/page.html", "directory"), Entry("ancestor alias ZIP", "alias/page.html", "zip"), Entry("case alias directory", "REAL/page.html", "directory"), Entry("case alias ZIP", "REAL/page.html", "zip"), Label("persistence"))

var _ = DescribeTable("CR4 G5 reference-only source does not assert retained bytes", func(format string) {
	f, destination := cr4Export(format)
	cr4Resource(destination, format, f.version, "dependencies", func(raw []byte) []byte {
		return cr4Rows(raw, func(row map[string]any) {
			if row["kind"] == "source" {
				row["source"].(map[string]any)["retention_mode"] = "reference_only"
				row["source"].(map[string]any)["asset_path"] = "missing-reference-only.bin"
				row["state"] = "external_reference"
			}
		})
	})
	_, e := foundation.VerifyExport(ctx, destination, "")
	Expect(e).NotTo(HaveOccurred())
}, Entry("directory", "directory"), Entry("ZIP", "zip"), Label("persistence"))

var _ = DescribeTable("CR4 G5 forward Note claims use the exact selected target inventory", func(format string) {
	f, _ := cr4Export(format)
	first := f.version
	notes, e := f.queries.AnnotationList(ctx, f.token, first, query.Options{})
	Expect(e).NotTo(HaveOccurred())
	Expect(notes.Items).To(HaveLen(1))
	f.correctionVersion(GinkgoT(), map[string]string{"real/next.html": "<html>next</html>"}, "alias", "real")
	second, page := f.version, "real/next.html"
	_, e = noteMutation(f, key(GinkgoT()), "PATCH", notes.Items[0].AnnotationID, notes.Items[0].ETag, encodedNote(map[string]any{"action": "reattach", "anchor": domain.AnnotationAnchor{VersionID: second, PagePath: &page}, "selected_text": nil, "context": nil, "context_ref": nil}))
	Expect(e).NotTo(HaveOccurred())
	destination := filepath.Join(GinkgoT().TempDir(), "forward")
	r, e := recovery.New(f.store, f.auth, f.store).Submit(ctx, f.token, key(GinkgoT()), domain.CopyRequest{Action: "export.create", ProjectID: f.project, Versions: "all", Format: format, Destination: destination, Records: []string{"notes"}})
	Expect(e).NotTo(HaveOccurred())
	var a domain.CopyResult
	Expect(json.Unmarshal(r.Body, &a)).To(Succeed())
	Expect(copyTurns(f, a.JobID, 300).State).To(Equal("completed"))
	_, e = foundation.VerifyExport(ctx, destination, "")
	Expect(e).NotTo(HaveOccurred())
	for _, bucket := range []string{first, second} {
		cr4Resource(destination, format, bucket, "records", func(raw []byte) []byte {
			return cr4Rows(raw, func(row map[string]any) {
				if row["kind"] != "notes" {
					return
				}
				anchor := row["value"].(map[string]any)["anchor"].(map[string]any)
				if anchor["version_id"] == second {
					anchor["page_path"] = "alias/next.html"
				}
			})
		})
		cr4Resource(destination, format, bucket, "dependencies", func(raw []byte) []byte {
			return cr4Rows(raw, func(row map[string]any) {
				if row["kind"] == "note_anchor" && row["version_id"] == second {
					row["page_path"] = "alias/next.html"
				}
			})
		})
	}
	_, e = foundation.VerifyExport(ctx, destination, "")
	Expect(e).To(HaveOccurred())
}, Entry("directory", "directory"), Entry("ZIP", "zip"), Label("persistence"))

// These empty-inventory/root combinations are format fixtures derived from a
// real production envelope, not a claim about empty-input import admission.
var _ = DescribeTable("CR4 G5 empty inventory still describes a directory root", func(kind, format string) {
	f := newFixture(GinkgoT())
	destination := filepath.Join(GinkgoT().TempDir(), "empty-root")
	r, e := recovery.New(f.store, f.auth, f.store).Submit(ctx, f.token, key(GinkgoT()), domain.CopyRequest{Action: "export.create", VersionID: f.version, Format: format, Destination: destination, Records: []string{"review"}})
	Expect(e).NotTo(HaveOccurred())
	var a domain.CopyResult
	Expect(json.Unmarshal(r.Body, &a)).To(Succeed())
	Expect(copyTurns(f, a.JobID, 300).State).To(Equal("completed"))
	cr4Resource(destination, format, f.version, "inventory", func([]byte) []byte { return []byte{} })
	if format == "directory" {
		payload := filepath.Join(destination, "payload")
		Expect(os.RemoveAll(payload)).To(Succeed())
		switch kind {
		case "directory":
			Expect(os.Mkdir(payload, 0700)).To(Succeed())
		case "file":
			Expect(os.WriteFile(payload, []byte("not a directory"), 0600)).To(Succeed())
		case "symlink":
			Expect(os.Symlink("metadata", payload)).To(Succeed())
		}
	} else {
		reader, e := zip.OpenReader(destination)
		Expect(e).NotTo(HaveOccurred())
		file, e := os.Create(destination + ".empty-root")
		Expect(e).NotTo(HaveOccurred())
		writer := zip.NewWriter(file)
		for _, entry := range reader.File {
			if strings.HasPrefix(entry.Name, "payload/") {
				if entry.Name != "payload/" || kind == "implicit" {
					continue
				}
				header := zip.FileHeader{Name: "payload", Method: zip.Store}
				body := ""
				switch kind {
				case "directory":
					header.Name += "/"
					header.SetMode(os.ModeDir | 0700)
				case "file":
					header.SetMode(0600)
					body = "not a directory"
				case "symlink":
					header.SetMode(os.ModeSymlink | 0777)
					body = "metadata"
				}
				out, e := writer.CreateHeader(&header)
				Expect(e).NotTo(HaveOccurred())
				_, e = io.WriteString(out, body)
				Expect(e).NotTo(HaveOccurred())
				continue
			}
			header := entry.FileHeader
			out, e := writer.CreateHeader(&header)
			Expect(e).NotTo(HaveOccurred())
			in, e := entry.Open()
			Expect(e).NotTo(HaveOccurred())
			_, e = io.Copy(out, in)
			Expect(e).NotTo(HaveOccurred())
			Expect(in.Close()).To(Succeed())
		}
		Expect(writer.Close()).To(Succeed())
		Expect(file.Close()).To(Succeed())
		Expect(reader.Close()).To(Succeed())
		Expect(os.Rename(destination+".empty-root", destination)).To(Succeed())
	}
	_, e = foundation.VerifyExport(ctx, destination, "")
	if kind == "directory" || kind == "implicit" {
		Expect(e).NotTo(HaveOccurred())
	} else {
		Expect(e).To(HaveOccurred())
	}
}, Entry("empty directory", "directory", "directory"), Entry("directory file root", "file", "directory"), Entry("directory link root", "symlink", "directory"), Entry("ZIP explicit directory", "directory", "zip"), Entry("ZIP implicit root", "implicit", "zip"), Entry("ZIP explicit file", "file", "zip"), Entry("ZIP explicit link", "symlink", "zip"), Label("persistence"))

// Transform represented ZIP directory headers only; all other entries stream
// unchanged. An absent header is a legal implicit parent, not a file/link parent.
func cr4ZIPParent(destination, parent, kind string) {
	GinkgoHelper()
	reader, e := zip.OpenReader(destination)
	Expect(e).NotTo(HaveOccurred())
	file, e := os.Create(destination + ".typed-parent")
	Expect(e).NotTo(HaveOccurred())
	writer := zip.NewWriter(file)
	found := false
	emit := func() {
		if kind == "implicit" {
			return
		}
		header := zip.FileHeader{Name: parent, Method: zip.Store}
		body := "represented parent"
		if kind == "symlink" {
			header.SetMode(os.ModeSymlink | 0777)
		} else {
			header.SetMode(0600)
		}
		out, e := writer.CreateHeader(&header)
		Expect(e).NotTo(HaveOccurred())
		_, e = io.WriteString(out, body)
		Expect(e).NotTo(HaveOccurred())
	}
	for _, entry := range reader.File {
		if strings.TrimSuffix(entry.Name, "/") == parent {
			emit()
			found = true
			continue
		}
		header := entry.FileHeader
		out, e := writer.CreateHeader(&header)
		Expect(e).NotTo(HaveOccurred())
		in, e := entry.Open()
		Expect(e).NotTo(HaveOccurred())
		_, e = io.Copy(out, in)
		Expect(e).NotTo(HaveOccurred())
		Expect(in.Close()).To(Succeed())
	}
	if !found {
		emit()
	}
	Expect(writer.Close()).To(Succeed())
	Expect(file.Close()).To(Succeed())
	Expect(reader.Close()).To(Succeed())
	Expect(os.Rename(destination+".typed-parent", destination)).To(Succeed())
}

var _ = DescribeTable("CR4 G5 ZIP typed parent cannot contradict its prefix children", func(parent, kind string) {
	f, destination := cr4Export("zip")
	if parent == "real" {
		// The forged inventory agrees with the explicit ZIP types, bytes and flat
		// set; only the original implied-directory invariant exposes the conflict.
		cr4Resource(destination, "zip", f.version, "inventory", func(raw []byte) []byte {
			var out []string
			for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
				var row []any
				Expect(json.Unmarshal([]byte(line), &row)).To(Succeed())
				if row[0] == "real" {
					if kind == "file" {
						row = []any{"real", "file", "18", hash("represented parent")}
					} else {
						row = []any{"real", "symlink", "represented parent"}
					}
				}
				value, e := json.Marshal(row)
				Expect(e).NotTo(HaveOccurred())
				out = append(out, string(value))
			}
			return []byte(strings.Join(out, "\n") + "\n")
		})
		cr4ZIPParent(destination, "payload/real", kind)
	} else {
		destination = filepath.Join(GinkgoT().TempDir(), "multi")
		response, e := recovery.New(f.store, f.auth, f.store).Submit(ctx, f.token, key(GinkgoT()), domain.CopyRequest{Action: "export.create", ProjectID: f.project, Versions: "all", Format: "zip", Destination: destination, Records: []string{"notes"}})
		Expect(e).NotTo(HaveOccurred())
		var a domain.CopyResult
		Expect(json.Unmarshal(response.Body, &a)).To(Succeed())
		Expect(copyTurns(f, a.JobID, 300).State).To(Equal("completed"))
		actual := parent
		if parent == "version_root" {
			actual = "versions/" + f.version
		}
		cr4ZIPParent(destination, actual, kind)
	}
	_, e := foundation.VerifyExport(ctx, destination, "")
	Expect(e).To(HaveOccurred())
}, Entry("file parent", "real", "file"), Entry("link parent", "real", "symlink"), Entry("outer versions file", "versions", "file"), Entry("outer versions link", "versions", "symlink"), Entry("selected root file", "version_root", "file"), Entry("selected root link", "version_root", "symlink"), Label("persistence"))

var _ = It("CR4 G5 ZIP implied directory needs no redundant header", Label("persistence"), func() {
	_, destination := cr4Export("zip")
	cr4ZIPParent(destination, "payload/real", "implicit")
	cr4ZIPParent(destination, "payload", "implicit")
	_, e := foundation.VerifyExport(ctx, destination, "")
	Expect(e).NotTo(HaveOccurred())
})

var _ = DescribeTable("CR4 G5 plain payload with separate metadata shares exact facts", func(family, format string) {
	separate := filepath.Join(GinkgoT().TempDir(), "metadata")
	f, destination := cr4Export(format, separate)
	resource := "records"
	if family == "source" {
		resource = "dependencies"
	}
	cr4Resource(destination, format, f.version, resource, func(raw []byte) []byte {
		return cr4Rows(raw, func(row map[string]any) {
			if family == "source" {
				if row["kind"] == "source" {
					row["source"].(map[string]any)["asset_path"] = "alias/page.html"
				}
				return
			}
			value := row["value"].(map[string]any)
			if family == "notes" {
				if row["kind"] == "notes" {
					for _, field := range []string{"anchor", "original_anchor"} {
						value[field].(map[string]any)["page_path"] = "alias/page.html"
					}
				}
				if row["kind"] == "context" {
					value["page_path"] = "alias/page.html"
				}
			}
			if family == "reader" && row["kind"] == "reader" && value["kind"] == "reader_preference" {
				value["value"].(map[string]any)["default_page"] = "alias/page.html"
			}
		})
	}, separate)
	if family == "notes" {
		cr4Resource(destination, format, f.version, "dependencies", func(raw []byte) []byte {
			return cr4Rows(raw, func(row map[string]any) {
				if row["kind"] == "note_anchor" {
					row["page_path"] = "alias/page.html"
				}
			})
		}, separate)
	}
	_, e := foundation.VerifyExport(ctx, destination, filepath.Join(separate, "export-index.json"))
	Expect(e).To(HaveOccurred())
}, Entry("Note directory", "notes", "directory"), Entry("Note ZIP", "notes", "zip"), Entry("reader directory", "reader", "directory"), Entry("reader ZIP", "reader", "zip"), Entry("source directory", "source", "directory"), Entry("source ZIP", "source", "zip"), Label("persistence"))

// Extend the empty-root format fixture to multiple logical roots. Unknown
// structural roots cannot compensate numerically for omitted empty headers.
var _ = DescribeTable("CR4 G5 selected ZIP roots require exact membership rather than count", func(extra bool) {
	f := newFixture(GinkgoT())
	f.correctionVersion(GinkgoT(), map[string]string{"next.html": "<html>next</html>"}, "alias", "next.html")
	destination := filepath.Join(GinkgoT().TempDir(), "empty-roots")
	r, e := recovery.New(f.store, f.auth, f.store).Submit(ctx, f.token, key(GinkgoT()), domain.CopyRequest{Action: "export.create", ProjectID: f.project, Versions: "all", Format: "zip", Destination: destination, Records: []string{"review"}})
	Expect(e).NotTo(HaveOccurred())
	var a domain.CopyResult
	Expect(json.Unmarshal(r.Body, &a)).To(Succeed())
	Expect(copyTurns(f, a.JobID, 300).State).To(Equal("completed"))
	var index map[string]any
	Expect(json.Unmarshal(cr4Read(destination, "zip", "export-index.json"), &index)).To(Succeed())
	Expect(index["versions"].([]any)).To(HaveLen(2))
	for _, item := range index["versions"].([]any) {
		cr4Resource(destination, "zip", item.(map[string]any)["version_id"].(string), "inventory", func([]byte) []byte { return []byte{} })
	}
	reader, e := zip.OpenReader(destination)
	Expect(e).NotTo(HaveOccurred())
	file, e := os.Create(destination + ".empty-roots")
	Expect(e).NotTo(HaveOccurred())
	writer := zip.NewWriter(file)
	for _, entry := range reader.File {
		if strings.HasPrefix(entry.Name, "versions/") {
			continue
		}
		header := entry.FileHeader
		out, e := writer.CreateHeader(&header)
		Expect(e).NotTo(HaveOccurred())
		in, e := entry.Open()
		Expect(e).NotTo(HaveOccurred())
		_, e = io.Copy(out, in)
		Expect(e).NotTo(HaveOccurred())
		Expect(in.Close()).To(Succeed())
	}
	if extra {
		for _, name := range []string{"versions/unselected-one/", "versions/unselected-two/"} {
			header := zip.FileHeader{Name: name}
			header.SetMode(os.ModeDir | 0700)
			_, e := writer.CreateHeader(&header)
			Expect(e).NotTo(HaveOccurred())
		}
	}
	Expect(writer.Close()).To(Succeed())
	Expect(file.Close()).To(Succeed())
	Expect(reader.Close()).To(Succeed())
	Expect(os.Rename(destination+".empty-roots", destination)).To(Succeed())
	_, e = foundation.VerifyExport(ctx, destination, "")
	if extra {
		Expect(e).To(HaveOccurred())
	} else {
		Expect(e).NotTo(HaveOccurred())
	}
}, Entry("implicit selected empty roots", false), Entry("extra roots cannot compensate", true), Label("persistence"))

var _ = DescribeTable("CR4 G5 verified inventory uses original canonical JSONL grammar", func(mode string) {
	f, destination := cr4Export("directory")
	cr4Resource(destination, "directory", f.version, "inventory", func(raw []byte) []byte {
		switch mode {
		case "missing_lf":
			return raw[:len(raw)-1]
		case "blank_lf":
			return append(raw, '\n')
		case "whitespace":
			return append([]byte(" "), raw...)
		case "size":
			lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
			for i, line := range lines {
				var row []any
				Expect(json.Unmarshal([]byte(line), &row)).To(Succeed())
				if row[1] == "file" {
					row[2] = "0" + row[2].(string)
					value, e := json.Marshal(row)
					Expect(e).NotTo(HaveOccurred())
					lines[i] = string(value)
					break
				}
			}
			return []byte(strings.Join(lines, "\n") + "\n")
		}
		panic("unknown fixture mode")
	})
	_, e := foundation.VerifyExport(ctx, destination, "")
	Expect(e).To(HaveOccurred())
}, Entry("size spelling", "size"), Entry("whitespace", "whitespace"), Entry("final LF required", "missing_lf"), Entry("extra blank LF", "blank_lf"), Label("persistence"))

var _ = DescribeTable("CR4 G5 one selected versions-layout root rejects a sibling", func(extra bool, format string) {
	f := newFixture(GinkgoT())
	f.correctionVersion(GinkgoT(), map[string]string{"next.html": "<html>next</html>"}, "alias", "next.html")
	destination := filepath.Join(GinkgoT().TempDir(), "one-root")
	response, e := recovery.New(f.store, f.auth, f.store).Submit(ctx, f.token, key(GinkgoT()), domain.CopyRequest{Action: "export.create", ProjectID: f.project, Versions: "all", Format: format, Destination: destination, Records: []string{"review"}})
	Expect(e).NotTo(HaveOccurred())
	var a domain.CopyResult
	Expect(json.Unmarshal(response.Body, &a)).To(Succeed())
	Expect(copyTurns(f, a.JobID, 300).State).To(Equal("completed"))
	var index map[string]any
	Expect(json.Unmarshal(cr4Read(destination, format, "export-index.json"), &index)).To(Succeed())
	versions := index["versions"].([]any)
	Expect(versions).To(HaveLen(2))
	removed := versions[1].(map[string]any)["payload_root"].(string)
	index["versions"] = versions[:1]
	raw, e := json.Marshal(index)
	Expect(e).NotTo(HaveOccurred())
	cr4Write(destination, format, map[string][]byte{"export-index.json": raw})
	if !extra {
		if format == "directory" {
			Expect(os.RemoveAll(filepath.Join(destination, removed))).To(Succeed())
		} else {
			reader, e := zip.OpenReader(destination)
			Expect(e).NotTo(HaveOccurred())
			file, e := os.Create(destination + ".single-root")
			Expect(e).NotTo(HaveOccurred())
			writer := zip.NewWriter(file)
			for _, entry := range reader.File {
				if strings.TrimSuffix(entry.Name, "/") == removed || strings.HasPrefix(entry.Name, removed+"/") {
					continue
				}
				header := entry.FileHeader
				out, e := writer.CreateHeader(&header)
				Expect(e).NotTo(HaveOccurred())
				in, e := entry.Open()
				Expect(e).NotTo(HaveOccurred())
				_, e = io.Copy(out, in)
				Expect(e).NotTo(HaveOccurred())
				Expect(in.Close()).To(Succeed())
			}
			Expect(writer.Close()).To(Succeed())
			Expect(file.Close()).To(Succeed())
			Expect(reader.Close()).To(Succeed())
			Expect(os.Rename(destination+".single-root", destination)).To(Succeed())
		}
	}
	_, e = foundation.VerifyExport(ctx, destination, "")
	if extra {
		Expect(e).To(HaveOccurred())
	} else {
		Expect(e).NotTo(HaveOccurred())
	}
}, Entry("directory extra sibling", true, "directory"), Entry("ZIP extra sibling", true, "zip"), Entry("directory exact root", false, "directory"), Entry("ZIP exact root", false, "zip"), Label("persistence"))

var _ = DescribeTable("CR4 G5 empty indexed ZIP root keeps its parent a directory", func(kind string) {
	// Standalone format fixture: preserve the real generated version descriptor,
	// provide a canonical empty inventory, and declare a logical versions root.
	f, destination := cr4Export("zip")
	cr4Resource(destination, "zip", f.version, "inventory", func([]byte) []byte { return []byte{} })
	var index map[string]any
	Expect(json.Unmarshal(cr4Read(destination, "zip", "export-index.json"), &index)).To(Succeed())
	v := index["versions"].([]any)[0].(map[string]any)
	v["payload_root"] = "versions/" + f.version
	v["metadata_root"] = nil
	v["records_sha256"] = ""
	v["dependencies_sha256"] = ""
	raw, e := json.Marshal(index)
	Expect(e).NotTo(HaveOccurred())
	cr4Write(destination, "zip", map[string][]byte{"export-index.json": raw})
	if kind != "implicit" {
		cr4ZIPParent(destination, "versions", kind)
	}
	_, e = foundation.VerifyExport(ctx, destination, "")
	if kind == "implicit" {
		Expect(e).NotTo(HaveOccurred())
	} else {
		Expect(e).To(HaveOccurred())
	}
}, Entry("file parent", "file"), Entry("link parent", "symlink"), Entry("implicit directory parent", "implicit"), Label("persistence"))
