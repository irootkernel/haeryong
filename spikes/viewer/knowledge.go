package viewer

// Optional operator-supplied Task07 adapter. This is not an author manifest or
// production import/source-reader API. Requests can select only prebound IDs.
import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"reflect"
	"strings"
	"unicode/utf8"
)

type knowledgeCorpus struct {
	Versions map[string]knowledgeVersion `json:"versions"`
}
type knowledgeVersion struct {
	Root        string                        `json:"root"`
	Title       string                        `json:"title"`
	DefaultPage string                        `json:"default_page"`
	Files       map[string]fileRecord         `json:"files"`
	Evidence    map[string]knowledgeEvidence  `json:"evidence"`
	References  map[string]knowledgeReference `json:"references"`
}
type knowledgeEvidence struct {
	Project     string `json:"project"`
	Revision    string `json:"revision"`
	Path        string `json:"path"`
	LineStart   int    `json:"line_start"`
	LineEnd     int    `json:"line_end"`
	CapturePath string `json:"capture_path"`
	SHA256      string `json:"sha256"`
	Text        string `json:"-"`
}
type knowledgeReference struct {
	Version         string `json:"version"`
	InventorySHA256 string `json:"inventory_sha256"`
	Page            string `json:"page"`
	Region          string `json:"region"`
}

func (f *Fixture) loadKnowledge() error {
	if f.config.Knowledge == "" {
		return nil
	}
	file, e := os.Open(f.config.Knowledge)
	if e != nil {
		return e
	}
	defer file.Close()
	var k knowledgeCorpus
	decoder := json.NewDecoder(file)
	decoder.DisallowUnknownFields()
	if e = decoder.Decode(&k); e != nil {
		return e
	}
	if e = decoder.Decode(new(any)); e != io.EOF {
		return errors.New("one operator configuration required")
	}
	if len(k.Versions) != 2 {
		return errors.New("operator configuration requires exact a/b result slots")
	}
	for _, version := range []string{"a", "b"} {
		v, ok := k.Versions[version]
		if !ok || !filepath.IsAbs(v.Root) || len(v.Files) == 0 {
			return errors.New("complete absolute result roots and inventories required")
		}
		if _, ok = v.Files[v.DefaultPage]; !ok {
			return errors.New("selected default page missing from result inventory")
		}
		for name, record := range v.Files {
			if name == "" || strings.HasPrefix(name, "/") || path.Clean(name) != name || strings.HasPrefix(name, "../") || record.Bytes < 0 {
				return errors.New("invalid operator inventory entry")
			}
			if b, e := hex.DecodeString(record.SHA256); e != nil || len(b) != 32 {
				return errors.New("invalid operator inventory digest")
			}
		}
	}
	f.knowledge = &k
	return nil
}
func (f *Fixture) prepareKnowledge(version string) error {
	v := f.knowledge.Versions[version]
	actual := map[string]fileRecord{}
	// Inspect the complete selected tree and copy incrementally. No source/type or
	// count quota is inferred from this small experimental corpus.
	e := filepath.WalkDir(v.Root, func(p string, d fs.DirEntry, e error) error {
		if e != nil {
			return e
		}
		relative, e := filepath.Rel(v.Root, p)
		if e != nil {
			return e
		}
		name := filepath.ToSlash(relative)
		if d.IsDir() {
			return nil
		}
		if !d.Type().IsRegular() {
			return fmt.Errorf("fixture cannot capture selected special entry %q", name)
		}
		file, e := os.Open(p)
		if e != nil {
			return e
		}
		hash := sha256.New()
		n, e := io.Copy(hash, file)
		file.Close()
		if e != nil {
			return e
		}
		actual[name] = fileRecord{Bytes: n, SHA256: hex.EncodeToString(hash.Sum(nil))}
		return nil
	})
	if e != nil {
		return e
	}
	if !reflect.DeepEqual(actual, v.Files) {
		return errors.New("result directory differs from frozen operator inventory")
	}
	inventoryPath := filepath.Join(f.config.State, "inventory-"+version+".json")
	if saved, e := os.ReadFile(inventoryPath); e == nil {
		var prior map[string]fileRecord
		if e = json.Unmarshal(saved, &prior); e != nil {
			return e
		}
		if !reflect.DeepEqual(prior, v.Files) {
			return errors.New("immutable result slot cannot be reassigned")
		}
		f.inventories[version] = prior
		return nil
	} else if !os.IsNotExist(e) {
		return e
	}
	for name, expected := range v.Files {
		for _, kind := range []string{"corpus", "verified"} {
			dest := filepath.Join(f.config.State, kind, version, filepath.FromSlash(name))
			if e = os.MkdirAll(filepath.Dir(dest), 0700); e != nil {
				return e
			}
			src, e := os.Open(filepath.Join(v.Root, filepath.FromSlash(name)))
			if e != nil {
				return e
			}
			temp, e := os.CreateTemp(filepath.Dir(dest), "capture-")
			if e != nil {
				src.Close()
				return e
			}
			hash := sha256.New()
			n, e := io.Copy(io.MultiWriter(temp, hash), src)
			src.Close()
			if e == nil {
				e = temp.Sync()
			}
			temp.Close()
			if e != nil || n != expected.Bytes || hex.EncodeToString(hash.Sum(nil)) != expected.SHA256 {
				os.Remove(temp.Name())
				return errors.New("result changed during immutable capture")
			}
			if e = os.Rename(temp.Name(), dest); e != nil {
				os.Remove(temp.Name())
				return e
			}
		}
	}
	data, e := jsonBytes(v.Files)
	if e != nil {
		return e
	}
	if e = os.WriteFile(inventoryPath, data, 0600); e != nil {
		return e
	}
	f.inventories[version] = v.Files
	return nil
}
func (f *Fixture) inventoryDigest(version string) string {
	b, _ := jsonBytes(f.inventories[version])
	return digest(b)
}
func (f *Fixture) bindKnowledge() error {
	if f.knowledge == nil {
		return nil
	}
	for key, v := range f.knowledge.Versions {
		for id, source := range v.Evidence {
			if id == "" || source.Project == "" || source.Revision == "" || source.Path == "" || source.LineStart < 1 || source.LineEnd < source.LineStart || !filepath.IsAbs(source.CapturePath) {
				return errors.New("invalid operator source identity")
			}
			b, e := os.ReadFile(source.CapturePath)
			if e != nil {
				return e
			}
			if digest(b) != source.SHA256 || !utf8.Valid(b) {
				return errors.New("captured source differs from pinned bytes")
			}
			source.Text = string(b)
			v.Evidence[id] = source
		}
		for _, reference := range v.References {
			if reference.Version != "a" && reference.Version != "b" {
				return errors.New("unknown reference slot")
			}
			if reference.InventorySHA256 != f.inventoryDigest(reference.Version) {
				return errors.New("reference does not pin the exact immutable result")
			}
			if _, ok := f.inventories[reference.Version][reference.Page]; !ok {
				return errors.New("pinned reference page unavailable")
			}
		}
		f.knowledge.Versions[key] = v
	}
	return nil
}
func (f *Fixture) knowledgeSummary() any {
	if f.knowledge == nil {
		return nil
	}
	versions := map[string]any{}
	for key, v := range f.knowledge.Versions {
		versions[key] = map[string]any{"title": v.Title, "default_page": v.DefaultPage, "inventory_sha256": f.inventoryDigest(key)}
	}
	return map[string]any{"fixture_only": true, "versions": versions}
}
func (f *Fixture) knowledgeRead(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		http.Error(w, "read only", 405)
		return
	}
	if f.knowledge == nil {
		http.NotFound(w, r)
		return
	}
	v, ok := f.knowledge.Versions[r.URL.Query().Get("version")]
	if !ok {
		http.NotFound(w, r)
		return
	}
	id := r.URL.Query().Get("id")
	switch r.URL.Query().Get("kind") {
	case "evidence":
		source, ok := v.Evidence[id]
		if !ok {
			http.Error(w, "evidence unavailable", 404)
			return
		}
		// Never disclose local capture paths or forward source bytes to an artifact.
		reply(w, 200, map[string]any{"id": id, "project": source.Project, "revision": source.Revision, "path": source.Path, "line_start": source.LineStart, "line_end": source.LineEnd, "sha256": source.SHA256, "text": source.Text})
	case "reference":
		reference, ok := v.References[id]
		if !ok {
			http.Error(w, "reference unavailable", 404)
			return
		}
		reply(w, 200, reference)
	default:
		http.NotFound(w, r)
	}
}
