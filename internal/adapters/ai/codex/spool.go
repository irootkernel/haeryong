package codex

import (
	"os"
	"path/filepath"
	"strings"
)

// RecoverSpool runs only after production profile writer ownership is acquired.
// It removes this adapter's abandoned payload spools, never native account data.
func (d *Driver) RecoverSpool() error {
	if e := os.MkdirAll(d.Scratch, 0700); e != nil {
		return e
	}
	entries, e := os.ReadDir(d.Scratch)
	if e != nil {
		return e
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), "provider-input-") || strings.HasPrefix(entry.Name(), "provider-output-") {
			if e = os.Remove(filepath.Join(d.Scratch, entry.Name())); e != nil {
				return e
			}
		}
	}
	return nil
}
