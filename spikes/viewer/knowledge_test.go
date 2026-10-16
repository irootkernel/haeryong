package viewer

import (
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func knowledgeFixtureConfig(t *testing.T) (Config, knowledgeCorpus) {
	t.Helper()
	state := ownedState(t)
	k := knowledgeCorpus{Versions: map[string]knowledgeVersion{}}
	for _, version := range []string{"a", "b"} {
		root := ownedState(t)
		name := "nested/page.html"
		body := []byte("<!doctype html><h1 id=exact>ordinary " + version + "</h1><script type=module src='../assets/main.js'></script>")
		files := map[string][]byte{name: body, "assets/main.js": []byte("document.body.dataset.ready='ordinary';"), "unfamiliar.payload": []byte{0, 255, 1}}
		inventory := map[string]fileRecord{}
		for p, b := range files {
			target := filepath.Join(root, p)
			if e := os.MkdirAll(filepath.Dir(target), 0700); e != nil {
				t.Fatal(e)
			}
			if e := os.WriteFile(target, b, 0600); e != nil {
				t.Fatal(e)
			}
			inventory[p] = fileRecord{Bytes: int64(len(b)), SHA256: digest(b)}
		}
		source := []byte("<script>source must remain text</script>\n")
		capture := filepath.Join(root, "..", "capture-"+version+"-"+filepath.Base(root))
		if e := os.WriteFile(capture, source, 0600); e != nil {
			t.Fatal(e)
		}
		t.Cleanup(func() { os.Remove(capture) })
		k.Versions[version] = knowledgeVersion{Root: root, Title: "Ordinary " + version, DefaultPage: name, Files: inventory, Evidence: map[string]knowledgeEvidence{"source-id": {Project: "synthetic", Revision: "fixed", Path: "source.go", LineStart: 4, LineEnd: 4, CapturePath: capture, SHA256: digest(source)}}, References: map[string]knowledgeReference{}}
	}
	inv, _ := jsonBytes(k.Versions["a"].Files)
	k.Versions["b"].References["prior"] = knowledgeReference{Version: "a", InventorySHA256: digest(inv), Page: "nested/page.html", Region: "exact"}
	config := Config{State: state, Base: "http://127.0.0.1:9270", Renderers: [2]string{"http://127.0.0.1:9272", "http://127.0.0.1:9273"}, Knowledge: filepath.Join(state, "operator.json")}
	saveKnowledge(t, config, k)
	return config, k
}
func saveKnowledge(t *testing.T, c Config, k knowledgeCorpus) {
	t.Helper()
	b, e := json.Marshal(k)
	if e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(c.Knowledge, b, 0600); e != nil {
		t.Fatal(e)
	}
}

func TestKnowledgeImmutableOrdinaryDirectoriesAndBoundSources(t *testing.T) {
	c, k := knowledgeFixtureConfig(t)
	f, e := New(c)
	if e != nil {
		t.Fatal(e)
	}
	defer f.Close()
	token := fixtureOwner(t, f)
	handler := http.HandlerFunc(f.trusted)
	for _, v := range []string{"a", "b"} {
		for name, expected := range k.Versions[v].Files {
			file, e := f.verifiedFile(v, name)
			if e != nil {
				t.Fatal(e)
			}
			info, _ := file.Stat()
			file.Close()
			if info.Size() != expected.Bytes {
				t.Fatal("file bytes changed")
			}
		}
		// No synthetic scenario, attachment or injected version assets are imposed.
		if len(f.inventories[v]) != 3 {
			t.Fatal("fixture inserted authored files")
		}
	}
	if w := request(t, handler, c.Base, "GET", "/viewer/knowledge?version=a&kind=evidence&id=source-id", "", "", nil, nil); w.Code != 401 {
		t.Fatal("source exposed without owner", w.Code)
	}
	if w := request(t, handler, c.Base, "GET", "/viewer/knowledge?version=a&kind=evidence&id=source-id", token, c.Renderers[0], nil, nil); w.Code != 403 {
		t.Fatal("renderer origin admitted", w.Code)
	}
	w := request(t, handler, c.Base, "GET", "/viewer/knowledge?version=a&kind=evidence&id=source-id", token, c.Base, nil, nil)
	source := object(t, w)
	if w.Code != 200 || source["text"] != "<script>source must remain text</script>\n" || source["sha256"] != k.Versions["a"].Evidence["source-id"].SHA256 || strings.Contains(w.Body.String(), "capture_path") {
		t.Fatal("wrong source projection")
	}
	for _, query := range []string{"version=a&kind=evidence&id=../source.go", "version=a&kind=evidence&id=missing", "version=a&kind=reference&id=prior", "version=wrong&kind=evidence&id=source-id"} {
		if w := request(t, handler, c.Base, "GET", "/viewer/knowledge?"+query, token, c.Base, nil, nil); w.Code != 404 {
			t.Fatal("unknown target resolved", query, w.Code)
		}
	}
	reference := object(t, request(t, handler, c.Base, "GET", "/viewer/knowledge?version=b&kind=reference&id=prior", token, c.Base, nil, nil))
	if reference["version"] != "a" || reference["page"] != "nested/page.html" || reference["region"] != "exact" || reference["inventory_sha256"] != f.inventoryDigest("a") {
		t.Fatal("prior reference retargeted")
	}
	// A later source edit cannot swap already captured displayed bytes.
	if e = os.WriteFile(k.Versions["a"].Evidence["source-id"].CapturePath, []byte("changed after capture"), 0600); e != nil {
		t.Fatal(e)
	}
	after := object(t, request(t, handler, c.Base, "GET", "/viewer/knowledge?version=a&kind=evidence&id=source-id", token, c.Base, nil, nil))
	if after["text"] != source["text"] {
		t.Fatal("mutable source replaced captured text")
	}
	if e = os.WriteFile(filepath.Join(c.State, "verified", "a", "nested/page.html"), []byte("tampered"), 0600); e != nil {
		t.Fatal(e)
	}
	if file, e := f.verifiedFile("a", "nested/page.html"); e == nil {
		file.Close()
		t.Fatal("corrupt immutable bytes accepted")
	}
}
func TestKnowledgeCaptureAndReferenceFailures(t *testing.T) {
	for _, kind := range []string{"root-change", "missing-file", "extra-file", "source-change", "wrong-pin", "wrong-page"} {
		t.Run(kind, func(t *testing.T) {
			c, k := knowledgeFixtureConfig(t)
			switch kind {
			case "root-change":
				os.WriteFile(filepath.Join(k.Versions["a"].Root, "nested/page.html"), []byte("different"), 0600)
			case "missing-file":
				os.Remove(filepath.Join(k.Versions["a"].Root, "unfamiliar.payload"))
			case "extra-file":
				os.WriteFile(filepath.Join(k.Versions["a"].Root, "unexpected.txt"), []byte("not captured"), 0600)
			case "source-change":
				os.WriteFile(k.Versions["a"].Evidence["source-id"].CapturePath, []byte("different"), 0600)
			case "wrong-pin":
				ref := k.Versions["b"].References["prior"]
				ref.InventorySHA256 = strings.Repeat("0", 64)
				k.Versions["b"].References["prior"] = ref
				saveKnowledge(t, c, k)
			case "wrong-page":
				ref := k.Versions["b"].References["prior"]
				ref.Page = "missing.html"
				k.Versions["b"].References["prior"] = ref
				saveKnowledge(t, c, k)
			}
			if f, e := New(c); e == nil {
				f.Close()
				t.Fatal("changed or unavailable immutable target accepted", kind)
			}
		})
	}
}
func TestKnowledgeRestartCannotReassignSlot(t *testing.T) {
	c, k := knowledgeFixtureConfig(t)
	f, e := New(c)
	if e != nil {
		t.Fatal(e)
	}
	f.Close()
	// Same selected bytes/source/pin and origins reopen without recapturing history.
	f, e = New(c)
	if e != nil {
		t.Fatal(e)
	}
	f.Close()
	v := k.Versions["a"]
	body := []byte("new immutable content")
	os.WriteFile(filepath.Join(v.Root, "nested/page.html"), body, 0600)
	v.Files["nested/page.html"] = fileRecord{Bytes: int64(len(body)), SHA256: digest(body)}
	k.Versions["a"] = v
	saveKnowledge(t, c, k)
	if f, e := New(c); e == nil {
		f.Close()
		t.Fatal("existing immutable slot reassigned")
	}
}

func TestKnowledgeMaterializerCompatibleInventory(t *testing.T) {
	c, k := knowledgeFixtureConfig(t)
	unusual := "nested/<&>한글.html"
	body := []byte("<!doctype html><p>Complete selected bytes</p>")
	if e := os.WriteFile(filepath.Join(k.Versions["a"].Root, unusual), body, 0600); e != nil {
		t.Fatal(e)
	}
	refs := filepath.Join(c.State, "references.json")
	if e := os.WriteFile(refs, []byte(`{"b":{"prior":{"version":"a","page":"nested/page.html","region":"exact"}}}`), 0600); e != nil {
		t.Fatal(e)
	}
	c.Knowledge = filepath.Join(c.State, "materialized.json")
	cmd := exec.Command("python3", "-B", "../knowledge-value/materialize.py", "--result-a", k.Versions["a"].Root, "--result-b", k.Versions["b"].Root, "--page-a", "nested/page.html", "--page-b", "nested/page.html", "--project-a", "synthetic", "--project-b", "synthetic", "--references", refs, "--output", c.Knowledge)
	if b, e := cmd.CombinedOutput(); e != nil {
		t.Fatalf("operator materialization failed: %v %s", e, b)
	}
	f, e := New(c)
	if e != nil {
		t.Fatal("materializer/Go inventory identity disagrees", e)
	}
	defer f.Close()
	if f.inventories["a"][unusual].SHA256 != digest(body) {
		t.Fatal("Unicode/HTML-significant path changed")
	}
	pinned := f.knowledge.Versions["b"].References["prior"]
	if pinned.InventorySHA256 != f.inventoryDigest("a") {
		t.Fatal("cross-language pin digest disagrees")
	}
	before, e := os.ReadFile(c.Knowledge)
	if e != nil {
		t.Fatal(e)
	}
	if b, e := cmd.CombinedOutput(); e == nil {
		t.Fatalf("existing operator evidence overwritten: %s", b)
	}
	after, _ := os.ReadFile(c.Knowledge)
	if string(before) != string(after) {
		t.Fatal("existing operator sidecar changed")
	}
}
