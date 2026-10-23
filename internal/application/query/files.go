package query

import (
	"context"
	"errors"
	"golang.org/x/text/unicode/norm"
	app "haeryong.local/haeryong/internal/application/storage"
	"haeryong.local/haeryong/internal/domain"
	"io"
	"net/url"
	"path"
	"strconv"
	"strings"
	"unicode/utf8"
)

// Missing is a typed 404 whose Reason names the missing input; Details
// carries extra error detail values for the registry envelope (additive,
// TASK-004-01 M3).
type Missing struct {
	Reason  string
	Details map[string]any
}

func (e *Missing) Error() string { return "not found: " + e.Reason }
func (e *Missing) Unwrap() error { return domain.ErrNotFound }

func validPath(p string) error {
	if p == "" || !utf8.ValidString(p) || strings.ContainsRune(p, 0) || strings.HasPrefix(p, "/") {
		return &Error{"invalid_path"}
	}
	for _, part := range strings.Split(p, "/") {
		if part == "" || part == "." || part == ".." {
			return &Error{"invalid_path"}
		}
	}
	if len(p) > 1 && p[1] == ':' && ((p[0] >= 'a' && p[0] <= 'z') || (p[0] >= 'A' && p[0] <= 'Z')) {
		return &Error{"invalid_path"}
	}
	return nil
}

// lookupTx resolves an exact stored path first inside the caller's
// transaction; a canonical fallback scans finite batches without normalizing
// stored names, returning a result only when it is unique.
func (s *Service) lookupTx(ctx context.Context, tx app.WriteTx, id, set, name string) (domain.Entry, error) {
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
		var entries []domain.Entry
		var err error
		entries, err = tx.Entries(set, after, 128)
		if err != nil {
			return domain.Entry{}, err
		}
		for _, e := range entries {
			after = e.Path
			if norm.NFC.String(e.Path) == canonical {
				if matched {
					return domain.Entry{}, &Error{"ambiguous_path"}
				}
				found = e
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

// Exact lookup always wins. A canonical fallback scans finite batches without
// normalizing stored names, returning a result only when it is unique.
func (s *Service) lookup(ctx context.Context, token, id, set, name string) (domain.Entry, error) {
	if err := validPath(name); err != nil {
		return domain.Entry{}, err
	}
	var found domain.Entry
	err := s.read(ctx, token, "/api/v1/versions/"+id+"/file", func(tx app.WriteTx) error {
		v, err := visibleVersion(tx, id)
		if err != nil {
			return err
		}
		if v.ContentSetID != set {
			return domain.ErrNotFound
		}
		found, err = tx.QueryEntry(set, name)
		return err
	})
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
		var entries []domain.Entry
		err := s.read(ctx, token, "/api/v1/versions/"+id+"/file", func(tx app.WriteTx) error {
			if _, err := visibleVersion(tx, id); err != nil {
				return err
			}
			var err error
			entries, err = tx.Entries(set, after, 128)
			return err
		})
		if err != nil {
			return domain.Entry{}, err
		}
		for _, e := range entries {
			after = e.Path
			if norm.NFC.String(e.Path) == canonical {
				if matched {
					return domain.Entry{}, &Error{"ambiguous_path"}
				}
				found = e
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

func (s *Service) ResolveFile(ctx context.Context, token, id, name string, resolve bool) (ResolvedFile, error) {
	var v domain.Version
	if err := s.read(ctx, token, "/api/v1/versions/"+id+"/file", func(tx app.WriteTx) error { var err error; v, err = visibleVersion(tx, id); return err }); err != nil {
		return ResolvedFile{}, err
	}
	e, err := s.lookup(ctx, token, id, v.ContentSetID, name)
	if err != nil {
		return ResolvedFile{}, err
	}
	seen := map[string]bool{}
	for resolve && e.Kind == "symlink" {
		if seen[e.Path] || strings.HasPrefix(e.LinkText, "/") || !utf8.ValidString(e.LinkText) || strings.ContainsRune(e.LinkText, 0) {
			return ResolvedFile{}, &Missing{Reason: "unresolved_link"}
		}
		seen[e.Path] = true
		target := path.Clean(path.Join(path.Dir(e.Path), e.LinkText))
		if target == ".." || strings.HasPrefix(target, "../") {
			return ResolvedFile{}, &Missing{Reason: "unresolved_link"}
		}
		e, err = s.lookup(ctx, token, id, v.ContentSetID, target)
		if err != nil {
			var invalid *Error
			if errors.Is(err, domain.ErrNotFound) || errors.As(err, &invalid) && invalid.Reason == "invalid_path" {
				return ResolvedFile{}, &Missing{Reason: "unresolved_link"}
			}
			return ResolvedFile{}, err
		}
	}
	out := ResolvedFile{File: entryFile(id, e), RequestedPath: name, ResolvedPath: e.Path}
	if e.Kind == "file" {
		out.Object = domain.Object{Hash: e.Hash, Size: e.Size, State: "ready"}
	}
	return out, nil
}

type guardedReader struct {
	io.ReadSeeker
	check func() error
	err   error
}

func (r *guardedReader) Read(p []byte) (int, error) {
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
func (r *guardedReader) Seek(offset int64, whence int) (int64, error) {
	if err := r.check(); err != nil {
		r.err = err
		return 0, err
	}
	return r.ReadSeeker.Seek(offset, whence)
}

func (s *Service) ReadFile(ctx context.Context, token, id, name string, resolve bool, consume func(ResolvedFile, io.ReadSeeker) error) error {
	info, err := s.ResolveFile(ctx, token, id, name, resolve)
	if err != nil {
		return err
	}
	if info.Kind != "file" {
		if err := s.Guard(ctx, token, id); err != nil {
			return err
		}
		return consume(info, nil)
	}
	return s.readBytes(ctx, token, id, info, consume)
}
func (s *Service) readBytes(ctx context.Context, token, id string, info ResolvedFile, consume func(ResolvedFile, io.ReadSeeker) error) error {
	if err := s.Guard(ctx, token, id); err != nil {
		return err
	}
	return s.Bytes.Read(ctx, info.Object, domain.Pin{Kind: "read", OwnerID: id}, func(handle io.ReadSeeker) error {
		if err := s.Guard(ctx, token, id); err != nil {
			return err
		}
		reader := &guardedReader{ReadSeeker: handle, check: func() error { return s.Guard(ctx, token, id) }}
		err := consume(info, reader)
		return errors.Join(err, reader.err)
	})
}
func (s *Service) ReadMetadata(ctx context.Context, token, id, kind string, consume func(ResolvedFile, io.ReadSeeker) error) error {
	if kind == "" {
		kind = "normalized"
	}
	if kind != "normalized" && kind != "supplied" {
		return &Error{"invalid_metadata_kind"}
	}
	var object domain.Object
	err := s.read(ctx, token, "/api/v1/versions/"+id+"/metadata", func(tx app.WriteTx) error {
		v, err := visibleVersion(tx, id)
		if err != nil {
			return err
		}
		hash := v.ManifestSHA
		if kind == "supplied" {
			hash = v.MetadataHash
		}
		if hash == "" {
			return &Missing{Reason: "metadata_not_supplied"}
		}
		object, err = tx.Object(hash)
		return err
	})
	if err != nil {
		return err
	}
	info := ResolvedFile{File: File{Path: kind + ".json", Kind: "file", Size: strconv.FormatInt(object.Size, 10), Hash: &object.Hash, MediaType: "application/json", Preview: "download", ETag: `"sha256-` + object.Hash + `"`}, RequestedPath: kind, ResolvedPath: kind, Object: object}
	return s.readBytes(ctx, token, id, info, consume)
}

func (s *Service) RenderBootstrap(ctx context.Context, token, id, requested string) (Bootstrap, error) {
	var v domain.Version
	err := s.read(ctx, token, "/api/v1/versions/"+id+"/render-bootstrap", func(tx app.WriteTx) error { var err error; v, err = visibleVersion(tx, id); return err })
	if err != nil {
		return Bootstrap{}, err
	}
	out := Bootstrap{VersionID: id, ViewerProfile: v.ViewerProfile, DefaultPage: v.DefaultPage, FilesURL: "/api/v1/versions/" + id + "/files", Delivery: "authenticated_attachment"}
	out.RequestedPage = nullable(requested)
	if requested != "" {
		info, err := s.ResolveFile(ctx, token, id, requested, true)
		if err != nil {
			return out, err
		}
		if info.Kind != "file" {
			return out, &Error{"page_is_not_regular_file"}
		}
		out.SelectedPage = &info.ResolvedPath
	} else {
		if v.DefaultPage != nil {
			info, err := s.ResolveFile(ctx, token, id, *v.DefaultPage, true)
			if err == nil && info.Kind == "file" && isHTML(info.ResolvedPath) {
				out.SelectedPage = &info.ResolvedPath
			} else if err != nil && !errors.Is(err, domain.ErrNotFound) {
				return out, err
			}
		}
		if out.SelectedPage == nil {
			info, err := s.ResolveFile(ctx, token, id, "index.html", false)
			if err == nil && info.Kind == "file" {
				out.SelectedPage = &info.ResolvedPath
			} else if err != nil && !errors.Is(err, domain.ErrNotFound) {
				return out, err
			}
		}
		if out.SelectedPage == nil {
			after := ""
			count := 0
			sole := ""
			for {
				var rows []domain.Entry
				err := s.read(ctx, token, "/api/v1/versions/"+id+"/render-bootstrap", func(tx app.WriteTx) error {
					if _, err := visibleVersion(tx, id); err != nil {
						return err
					}
					var err error
					rows, err = tx.Entries(v.ContentSetID, after, 128)
					return err
				})
				if err != nil {
					return out, err
				}
				for _, e := range rows {
					after = e.Path
					if e.Kind == "file" && isHTML(e.Path) {
						count++
						sole = e.Path
					}
				}
				if count > 1 || len(rows) < 128 {
					break
				}
			}
			if count == 1 {
				out.SelectedPage = &sole
			}
		}
	}
	if out.SelectedPage != nil {
		if requested != "" && requested != *out.SelectedPage {
			out.NavigationPath = nullable(requested)
		}
		u := "/api/v1/versions/" + id + "/file?path=" + url.QueryEscape(*out.SelectedPage)
		out.SelectedFileURL = &u
	}
	return out, nil
}
func isHTML(p string) bool {
	return domain.IsHTMLPath(p)
}
