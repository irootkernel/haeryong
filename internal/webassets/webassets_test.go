package webassets

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func frontendInputs(root string) ([]string, error) {
	fixed := []string{"index.html", "native.html", "package.json", "package-lock.json", "tsconfig.json", "vite.config.ts", "vite.native.config.ts", "vite.loader.config.ts"}
	var files []string
	for _, name := range fixed {
		files = append(files, filepath.Join(root, "frontend", name))
	}
	err := filepath.WalkDir(filepath.Join(root, "frontend", "src"), func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() {
			files = append(files, path)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(files)
	return files, nil
}

func inputsDigest(root string) (string, int, error) {
	files, err := frontendInputs(root)
	if err != nil {
		return "", 0, err
	}
	digest := sha256.New()
	count := 0
	for _, path := range files {
		relative, err := filepath.Rel(filepath.Join(root, "frontend"), path)
		if err != nil {
			return "", 0, err
		}
		digest.Write([]byte(filepath.ToSlash(relative)))
		digest.Write([]byte{0})
		body, err := os.ReadFile(path)
		if err != nil {
			return "", 0, err
		}
		digest.Write(body)
		digest.Write([]byte{0})
		count++
	}
	return hex.EncodeToString(digest.Sum(nil)), count, nil
}

type manifestShape struct {
	Kind         string            `json:"kind"`
	InputsDigest string            `json:"inputs_digest"`
	InputFiles   int               `json:"input_files"`
	Files        map[string]string `json:"files"`
}

var _ = It("TestCommittedWebAssetsMatchManifestAndFrontendInputs", Label("unit"), func() {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	Expect(err).To(BeNil())
	dist := Dist()
	body, err := fs.ReadFile(dist, "assets.manifest.json")
	Expect(err).To(BeNil())
	var manifest manifestShape
	Expect(json.Unmarshal(body, &manifest)).To(BeNil())
	Expect(manifest.Kind).To(Equal("haeryong-webassets/v1"))

	// Every committed file matches its recorded digest, and no file is missing.
	recorded := map[string]bool{}
	for name := range manifest.Files {
		recorded[name] = true
	}
	visited := map[string]bool{}
	err = fs.WalkDir(dist, ".", func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		if path == "assets.manifest.json" {
			return nil
		}
		data, err := fs.ReadFile(dist, path)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(data)
		Expect(manifest.Files[path]).To(Equal(hex.EncodeToString(sum[:])), "dist file not recorded or drifted: "+path)
		visited[path] = true
		return nil
	})
	Expect(err).To(BeNil())
	for name := range recorded {
		Expect(visited[name]).To(BeTrue(), "manifest records a missing dist file: "+name)
	}

	// The shell document mounts the app and keeps the server-injected defaults
	// placeholder; compiled assets exist and carry no native-bridge markers.
	index, err := Index()
	Expect(err).To(BeNil())
	text := string(index)
	Expect(strings.Contains(text, `id="root"`)).To(BeTrue())
	Expect(strings.Contains(text, `name="haeryong-session-defaults"`)).To(BeTrue())
	for _, name := range manifest.Files {
		if strings.HasPrefix(name, "assets/") {
			asset, err := fs.ReadFile(dist, name)
			Expect(err).To(BeNil())
			for _, marker := range []string{"wails", "window.runtime", "@wailsapp", "NATIVE_TRANSPORT_NOT_IMPLEMENTED", "HAEYONG_NATIVE_TRANSPORT_BOUND", "haeryongNativeCall"} {
				Expect(strings.Contains(string(asset), marker)).To(BeFalse(), "browser asset contains native marker "+marker)
			}
		}
	}

	// Node-free drift gate: the committed dist was built from these sources.
	digest, count, err := inputsDigest(root)
	Expect(err).To(BeNil())
	Expect(count).To(Equal(manifest.InputFiles))
	Expect(digest).To(Equal(manifest.InputsDigest), "frontend sources drifted from the committed dist; run frontend/check_build.py --regenerate")
})
