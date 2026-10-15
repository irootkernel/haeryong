package viewer

import (
	"crypto/sha256"
	"embed"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"math/rand"
	"os"
	"path/filepath"
)

//go:embed corpus
var corpus embed.FS

type fileRecord struct {
	Bytes  int64  `json:"bytes"`
	SHA256 string `json:"sha256"`
}

func (f *Fixture) prepareCorpus() error {
	for _, version := range []string{"a", "b"} {
		inventoryPath := filepath.Join(f.config.State, "inventory-"+version+".json")
		if existing, e := os.ReadFile(inventoryPath); e == nil {
			var inventory map[string]fileRecord
			if e = json.Unmarshal(existing, &inventory); e != nil {
				return e
			}
			f.inventories[version] = inventory
			continue
		} else if !os.IsNotExist(e) {
			return e
		}
		root := filepath.Join(f.config.State, "corpus", version)
		verified := filepath.Join(f.config.State, "verified", version)
		if e := os.MkdirAll(root, 0700); e != nil {
			return e
		}
		if e := os.MkdirAll(verified, 0700); e != nil {
			return e
		}
		e := fs.WalkDir(corpus, "corpus", func(p string, d fs.DirEntry, e error) error {
			if e != nil {
				return e
			}
			if d.IsDir() {
				return nil
			}
			b, e := corpus.ReadFile(p)
			if e != nil {
				return e
			}
			name := p[len("corpus/"):]
			target := filepath.Join(root, name)
			if e = os.MkdirAll(filepath.Dir(target), 0700); e != nil {
				return e
			}
			return os.WriteFile(target, b, 0600)
		})
		if e != nil {
			return e
		}
		for name, body := range map[string]string{"data.json": fmt.Sprintf(`{"version":%q,"factor":%d}`, version, map[string]int{"a": 2, "b": 3}[version]), "assets/version.js": fmt.Sprintf("export const version=%q; export const factor=%d;", version, map[string]int{"a": 2, "b": 3}[version]), "assets/worker.js": fmt.Sprintf("self.onmessage=e=>self.postMessage({version:%q,value:e.data*%d});", version, map[string]int{"a": 2, "b": 3}[version])} {
			if e = os.WriteFile(filepath.Join(root, name), []byte(body), 0600); e != nil {
				return e
			}
		}
		for i := 0; i < 300; i++ {
			name := filepath.Join(root, "unused", fmt.Sprintf("entry-%03d.unknown", i))
			os.MkdirAll(filepath.Dir(name), 0700)
			if e = os.WriteFile(name, []byte(fmt.Sprintf("retained %s %d", version, i)), 0600); e != nil {
				return e
			}
		}
		large := filepath.Join(root, "attachment.bin")
		if _, e = os.Stat(large); os.IsNotExist(e) {
			file, e := os.OpenFile(large, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
			if e != nil {
				return e
			}
			_, e = io.CopyN(file, rand.New(rand.NewSource(20261015)), 36*1024*1024)
			file.Close()
			if e != nil {
				return e
			}
		}
		wasm := []byte{0, 97, 115, 109, 1, 0, 0, 0, 1, 7, 1, 96, 2, 127, 127, 1, 127, 3, 2, 1, 0, 7, 7, 1, 3, 97, 100, 100, 0, 0, 10, 9, 1, 7, 0, 32, 0, 32, 1, 106, 11}
		if e = os.WriteFile(filepath.Join(root, "assets/add.wasm"), wasm, 0600); e != nil {
			return e
		}
		inventory := map[string]fileRecord{}
		e = filepath.WalkDir(root, func(p string, d fs.DirEntry, e error) error {
			if e != nil || d.IsDir() {
				return e
			}
			relative, e := filepath.Rel(root, p)
			if e != nil {
				return e
			}
			dest := filepath.Join(verified, relative)
			if e = os.MkdirAll(filepath.Dir(dest), 0700); e != nil {
				return e
			}
			src, e := os.Open(p)
			if e != nil {
				return e
			}
			defer src.Close()
			temp, e := os.CreateTemp(filepath.Dir(dest), "verify-")
			if e != nil {
				return e
			}
			hash := sha256.New()
			n, e := io.Copy(io.MultiWriter(temp, hash), src)
			if e == nil {
				e = temp.Sync()
			}
			temp.Close()
			if e != nil {
				os.Remove(temp.Name())
				return e
			}
			if e = os.Rename(temp.Name(), dest); e != nil {
				return e
			}
			inventory[filepath.ToSlash(relative)] = fileRecord{Bytes: n, SHA256: hex.EncodeToString(hash.Sum(nil))}
			return nil
		})
		if e != nil {
			return e
		}
		data, _ := jsonBytes(inventory)
		if e = os.WriteFile(inventoryPath, data, 0600); e != nil {
			return e
		}
		f.inventories[version] = inventory
	}
	return nil
}
func jsonBytes(v any) ([]byte, error) { return json.MarshalIndent(v, "", "  ") }
