package render

import (
	"context"
	"errors"
	"io"
	"path"
	"strings"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"
	app "haeryong.local/haeryong/internal/application/storage"
	"haeryong.local/haeryong/internal/domain"
)

// ResolvedContent is one canonical stored entry of a version with its
// verified object identity.
type ResolvedContent struct {
	Path       string `json:"path"`
	Kind       string `json:"kind"`
	HTML       bool   `json:"html"`
	DocumentID string `json:"document_id"`
	Hash       string `json:"bytes_sha256,omitempty"`
	Size       int64  `json:"byte_size"`
	LinkText   string `json:"link_target,omitempty"`
	Object     domain.Object
}

// ResolveVersionContent resolves an exact stored path inside the immutable
// version behind the renderer: exact match first, a unique canonical-
// equivalent fallback second, following only in-version symlinks with cycle
// detection for HTML pages. Directories and links return typed metadata.
func (s *Service) ResolveVersionContent(ctx context.Context, versionID, name string) (ResolvedContent, error) {
	if err := validPath(name); err != nil {
		return ResolvedContent{}, err
	}
	var out ResolvedContent
	err := s.store.Write(ctx, func(tx app.WriteTx) error {
		version, err := visibleVersion(tx, versionID)
		if err != nil {
			return err
		}
		entry, err := lookupTx(tx, version.ContentSetID, name)
		if err != nil {
			return err
		}
		seen := map[string]bool{}
		for entry.Kind == "symlink" {
			if seen[entry.Path] || strings.HasPrefix(entry.LinkText, "/") || !utf8.ValidString(entry.LinkText) || strings.ContainsRune(entry.LinkText, 0) {
				return &Error{Reason: "unresolved_link"}
			}
			seen[entry.Path] = true
			target := path.Clean(path.Join(path.Dir(entry.Path), entry.LinkText))
			if target == ".." || strings.HasPrefix(target, "../") {
				return &Error{Reason: "unresolved_link"}
			}
			entry, err = lookupTx(tx, version.ContentSetID, target)
			if err != nil {
				return &Error{Reason: "unresolved_link"}
			}
		}
		out = ResolvedContent{Path: entry.Path, Kind: entry.Kind, HTML: entry.Kind == "file" && domain.IsHTMLPath(entry.Path), DocumentID: version.DocumentID}
		if entry.Kind == "symlink" {
			out.LinkText = entry.LinkText
		}
		if entry.Kind == "file" {
			out.Hash = entry.Hash
			out.Size = entry.Size
			out.Object = domain.Object{Hash: entry.Hash, Size: entry.Size, State: "ready"}
		}
		return nil
	})
	return out, err
}

// ReadVersionContent streams the verified immutable bytes of a resolved file
// under the delivery session's per-read authority: the pinned verified
// handle is produced before the consumer runs, and every read rechecks the
// session lease and current owner authority.
func (s *Service) ReadVersionContent(ctx context.Context, deliveryID, versionID string, content ResolvedContent, consume func(io.ReadSeeker) error) error {
	if content.Kind != "file" {
		if err := s.GuardDelivery(ctx, versionID, deliveryID); err != nil {
			return err
		}
		return consume(nil)
	}
	if err := s.GuardDelivery(ctx, versionID, deliveryID); err != nil {
		return err
	}
	return s.bytes.Read(ctx, content.Object, domain.Pin{Kind: "read", OwnerID: "render:" + versionID}, func(handle io.ReadSeeker) error {
		if err := s.GuardDelivery(ctx, versionID, deliveryID); err != nil {
			return err
		}
		reader := &guardedRead{ReadSeeker: handle, check: func() error { return s.GuardDelivery(ctx, versionID, deliveryID) }}
		err := consume(reader)
		return errors.Join(err, reader.err)
	})
}

type guardedRead struct {
	io.ReadSeeker
	check func() error
	err   error
}

func (r *guardedRead) Read(p []byte) (int, error) {
	if err := r.check(); err != nil {
		r.err = err
		return 0, err
	}
	if len(p) > 32*1024 {
		p = p[:32*1024]
	}
	n, err := r.ReadSeeker.Read(p)
	if err != nil && err != io.EOF {
		r.err = err
	}
	return n, err
}

func (r *guardedRead) Seek(offset int64, whence int) (int64, error) {
	if err := r.check(); err != nil {
		r.err = err
		return 0, err
	}
	return r.ReadSeeker.Seek(offset, whence)
}

func visibleVersion(tx app.WriteTx, id string) (domain.Version, error) {
	version, err := tx.PublishedVersion(id)
	if err != nil {
		return version, err
	}
	document, err := tx.Document(version.DocumentID)
	if err != nil {
		return version, err
	}
	if document.Lifecycle == "trash" || document.Lifecycle == "purged" {
		return version, domain.ErrNotFound
	}
	return version, nil
}

func validPath(p string) error {
	if p == "" || !utf8.ValidString(p) || strings.ContainsRune(p, 0) || strings.HasPrefix(p, "/") {
		return &Error{Reason: "invalid_path"}
	}
	for _, part := range strings.Split(p, "/") {
		if part == "" || part == "." || part == ".." {
			return &Error{Reason: "invalid_path"}
		}
	}
	if len(p) > 1 && p[1] == ':' && ((p[0] >= 'a' && p[0] <= 'z') || (p[0] >= 'A' && p[0] <= 'Z')) {
		return &Error{Reason: "invalid_path"}
	}
	return nil
}

// lookupTx resolves an exact stored path first; a canonical fallback scans
// finite batches without normalizing stored names, returning a result only
// when it is unique.
func lookupTx(tx app.WriteTx, set, name string) (domain.Entry, error) {
	if err := validPath(name); err != nil {
		return domain.Entry{}, err
	}
	found, err := tx.QueryEntry(set, name)
	if err == nil {
		return found, nil
	}
	if !errors.Is(err, domain.ErrNotFound) {
		return found, err
	}
	canonical := norm.NFC.String(name)
	after := ""
	matched := false
	for {
		entries, err := tx.Entries(set, after, 128)
		if err != nil {
			return domain.Entry{}, err
		}
		for _, entry := range entries {
			after = entry.Path
			if norm.NFC.String(entry.Path) == canonical {
				if matched {
					return domain.Entry{}, &Error{Reason: "ambiguous_path"}
				}
				found = entry
				matched = true
			}
		}
		if len(entries) < 128 {
			break
		}
	}
	if !matched {
		return domain.Entry{}, domain.ErrNotFound
	}
	return found, nil
}

// SanitizeHandoffTarget keeps the post-handoff client redirect inside this
// renderer origin. Only the in-origin path is validated; the exact
// once-encoded query and fragment are preserved byte-for-byte and author
// path spelling is never normalized or retargeted.
func SanitizeHandoffTarget(target string) string {
	if target == "" || !strings.HasPrefix(target, "/") || strings.HasPrefix(target, "//") || strings.Contains(target, "\\") || strings.ContainsRune(target, 0) {
		return "/"
	}
	raw, tail := target, ""
	if index := strings.IndexAny(target, "?#"); index >= 0 {
		raw, tail = target[:index], target[index:]
	}
	if raw == "" || !strings.HasPrefix(raw, "/") || strings.HasPrefix(raw, "//") {
		return "/"
	}
	// The escape check only rejects paths that could resolve outside the
	// origin root; the browser resolves in-path dot segments itself.
	if cleaned := path.Clean(raw); cleaned == ".." || strings.HasPrefix(cleaned, "../") {
		return "/"
	}
	return raw + tail
}
