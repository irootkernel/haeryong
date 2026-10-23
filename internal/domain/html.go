package domain

import (
	"path"
	"strings"
)

// IsHTMLPath is the renderer's HTML classification for a resolved stored path.
// Metadata page declarations do not change it; callers separately require a regular file.
func IsHTMLPath(p string) bool {
	ext := strings.ToLower(path.Ext(p))
	return ext == ".html" || ext == ".htm"
}
