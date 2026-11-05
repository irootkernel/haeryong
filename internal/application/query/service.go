package query

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	appauth "haeryong.local/haeryong/internal/application/auth"
	appconfig "haeryong.local/haeryong/internal/application/configuration"
	app "haeryong.local/haeryong/internal/application/storage"
	"haeryong.local/haeryong/internal/domain"
	"net/url"
	"strconv"
	"sync"
	"time"
)

type Service struct {
	ProviderStatus      func(context.Context) domain.ProviderStatus
	GenerationAvailable bool
	QuestionsAvailable  bool
	LifecycleNow        func() time.Time
	Store               app.Store
	Auth                *appauth.Service
	Bytes               Bytes
	searchMu            sync.Mutex
	searchSnapshotCache *searchSnapshotCache
}

func New(store app.Store, auth *appauth.Service, bytes Bytes) *Service {
	return &Service{Store: store, Auth: auth, Bytes: bytes}
}
func (s *Service) read(ctx context.Context, token, target string, fn func(app.WriteTx) error) error {
	return s.Store.Write(ctx, func(tx app.WriteTx) error {
		record, err := s.Auth.AuthenticateWrite(ctx, tx, token)
		if err != nil {
			return err
		}
		if err := s.Auth.Authority(token).AuthorizeWrite(ctx, tx, domain.Request{Key: domain.OperationKey{PrincipalID: record.PrincipalID}, Method: "GET", Target: target}); err != nil {
			return err
		}
		return fn(tx)
	})
}
func visibleDocument(tx app.WriteTx, id string) (domain.QueryDocument, error) {
	d, err := tx.QueryDocument(id)
	if err != nil {
		return d, err
	}
	if d.Lifecycle == "trash" || d.Lifecycle == "purged" {
		return d, domain.ErrNotFound
	}
	_, err = tx.Scope(d.ProjectID)
	return d, err
}
func visibleVersion(tx app.WriteTx, id string) (domain.Version, error) {
	v, err := tx.PublishedVersion(id)
	if err != nil {
		return v, err
	}
	_, err = visibleDocument(tx, v.DocumentID)
	return v, err
}
func (s *Service) Guard(ctx context.Context, token, version string) error {
	return s.read(ctx, token, "/api/v1/versions/"+version, func(tx app.WriteTx) error { _, err := visibleVersion(tx, version); return err })
}
func validator(v any) string {
	b, _ := json.Marshal(v)
	h := sha256.Sum256(b)
	return `"sha256-` + hex.EncodeToString(h[:]) + `"`
}
func nullable(v string) *string {
	if v == "" {
		return nil
	}
	return &v
}
func project(p domain.QueryProject) Project {
	// S25: the shared projection's validator keeps registration and query
	// reads identical for the same state.
	out := Project{ID: p.ID, Name: p.Name, Lifecycle: p.Lifecycle, Revision: p.Revision, PermissionsRevision: p.PermissionsRevision, UpdatedAt: p.UpdatedAt}
	out.ETag = domain.ProjectETag(p)
	return out
}
func document(d domain.QueryDocument) Document {
	out := Document{ID: d.ID, ProjectID: d.ProjectID, Title: d.Title, Lifecycle: d.Lifecycle, LatestVersionID: nullable(d.LatestVersionID), Revision: d.Revision, UpdatedAt: d.UpdatedAt, VersionsURL: "/api/v1/documents/" + d.ID + "/versions"}
	out.ETag = validator(out)
	return out
}
func version(v domain.Version) Version {
	base := "/api/v1/versions/" + v.ID
	out := Version{ID: v.ID, DocumentID: v.DocumentID, Sequence: v.Sequence, BaseVersionID: nullable(v.BaseVersionID), Title: v.Title, DefaultPage: v.DefaultPage, ViewerProfile: v.ViewerProfile, SourceBasis: v.SourceBasis, PayloadSHA: v.PayloadSHA, ManifestSHA: v.ManifestSHA, SuppliedMetadataSHA: nullable(v.MetadataHash), ManifestURL: base + "/metadata?kind=normalized", FilesURL: base + "/files", BootstrapURL: base + "/render-bootstrap"}
	if v.MetadataHash != "" {
		u := base + "/metadata?kind=supplied"
		out.SuppliedMetadataURL = &u
	}
	out.ETag = validator(out)
	return out
}

type cursor struct {
	Binding, After, Key string
	Sequence            int64
}

// cursorStart binds a list cursor to the runtime generation, resource,
// filters, principal and scope, the principal revision and the permission
// generation of every scoped project. bound names further projects whose
// permission generation the cursor binds for every principal, the owner
// included (S26).
func (s *Service) cursorStart(ctx context.Context, token string, tx app.WriteTx, resource string, o Options, bound ...string) (cursor, int, domain.CredentialScope, error) {
	limit := o.Limit
	if limit == 0 {
		limit = 50
	}
	if limit < 1 || limit > 200 {
		return cursor{}, 0, domain.CredentialScope{}, &Error{"invalid_page_size"}
	}
	l, err := tx.Library()
	if err != nil {
		return cursor{}, 0, domain.CredentialScope{}, err
	}
	record, err := s.Auth.AuthenticateWrite(ctx, tx, token)
	if err != nil {
		return cursor{}, 0, domain.CredentialScope{}, err
	}
	principalRevision := int64(0)
	permissions := map[string]int64{}
	if record.Scope.Kind != "owner" {
		principal, err := tx.WorkflowPrincipal(record.PrincipalID)
		if err != nil {
			return cursor{}, 0, domain.CredentialScope{}, err
		}
		principalRevision = principal.Revision
		for _, id := range record.Scope.ProjectIDs {
			state, err := tx.Scope(id)
			if err != nil {
				return cursor{}, 0, domain.CredentialScope{}, err
			}
			permissions[id] = state.Project.PermissionsRevision
		}
	}
	for _, id := range bound {
		state, err := tx.Scope(id)
		if err != nil {
			return cursor{}, 0, domain.CredentialScope{}, err
		}
		permissions[id] = state.Project.PermissionsRevision
	}
	binding := []any{l.RuntimeGeneration, resource, o.Project, o.Lifecycle, o.Directory, o.Kind, o.Review, record.PrincipalID, record.Scope, principalRevision, permissions}
	// Preserve existing cursor identity when the additive personal filter is absent.
	if o.Revisit != "" {
		binding = append(binding, "revisit", o.Revisit)
	}
	b := validator(binding)
	c := cursor{Binding: b}
	if o.Cursor != "" {
		raw, err := base64.RawURLEncoding.DecodeString(o.Cursor)
		if err != nil || json.Unmarshal(raw, &c) != nil || c.Binding != b || c.Sequence < 0 {
			return cursor{}, 0, domain.CredentialScope{}, &Error{"cursor_mismatch"}
		}
	}
	return c, limit, record.Scope, nil
}
func cursorNext(c cursor) *string {
	b, _ := json.Marshal(c)
	s := base64.RawURLEncoding.EncodeToString(b)
	return &s
}
func (s *Service) Projects(ctx context.Context, token string, o Options) (List[Project], error) {
	out := List[Project]{Items: []Project{}}
	err := s.read(ctx, token, "/api/v1/projects", func(tx app.WriteTx) error {
		c, n, scope, err := s.cursorStart(ctx, token, tx, "projects", o)
		if err != nil {
			return err
		}
		rows, err := tx.QueryProjects(scope, c.After, c.Key, n+1)
		if err != nil {
			return err
		}
		for i, p := range rows {
			if i == n {
				last := rows[i-1]
				c.After, c.Key = last.UpdatedAt, last.ID
				out.NextCursor = cursorNext(c)
				break
			}
			out.Items = append(out.Items, project(p))
		}
		return nil
	})
	return out, err
}
func (s *Service) Documents(ctx context.Context, token string, o Options) (List[Document], error) {
	out := List[Document]{Items: []Document{}}
	if o.Revisit != "" && o.Revisit != "true" && o.Revisit != "false" {
		return out, &Error{"invalid_revisit_filter"}
	}
	if o.Review != "" && o.Review != "reviewing" && o.Review != "updated_since_review" {
		return out, &Error{"invalid_review_filter"}
	}
	if o.Lifecycle != "" && o.Lifecycle != "active" && o.Lifecycle != "archived" {
		return out, &Error{"invalid_lifecycle"}
	}
	err := s.read(ctx, token, "/api/v1/documents", func(tx app.WriteTx) error {
		if o.Project != "" {
			if _, err := tx.Scope(o.Project); err != nil {
				return err
			}
		}
		c, n, scope, err := s.cursorStart(ctx, token, tx, "documents", o)
		if err != nil {
			return err
		}
		var rows []domain.QueryDocument
		if o.Review != "" || o.Revisit != "" {
			actor, e := s.Auth.AuthenticateWrite(ctx, tx, token)
			if e != nil {
				return e
			}
			if scope.Kind != "owner" {
				return domain.ErrDenied
			}
			if o.Revisit != "" {
				rows, err = tx.QueryRevisitDocuments(actor.PrincipalID, o.Project, o.Lifecycle, o.Review, o.Revisit, c.After, c.Key, n+1)
			} else {
				rows, err = tx.QueryReviewDocuments(actor.PrincipalID, o.Project, o.Lifecycle, o.Review, c.After, c.Key, n+1)
			}
		} else {
			rows, err = tx.QueryDocuments(scope, o.Project, o.Lifecycle, c.After, c.Key, n+1)
		}
		if err != nil {
			return err
		}
		for i, d := range rows {
			if i == n {
				last := rows[i-1]
				c.After, c.Key = last.UpdatedAt, last.ID
				out.NextCursor = cursorNext(c)
				break
			}
			view, err := s.reviewedDocument(ctx, tx, token, d)
			if err != nil {
				return err
			}
			out.Items = append(out.Items, view)
		}
		return nil
	})
	return out, err
}
func (s *Service) Document(ctx context.Context, token, id string) (Document, error) {
	var out Document
	err := s.read(ctx, token, "/api/v1/documents/"+id, func(tx app.WriteTx) error {
		d, err := visibleDocument(tx, id)
		if err == nil {
			out, err = s.reviewedDocument(ctx, tx, token, d)
		}
		return err
	})
	return out, err
}
func (s *Service) Versions(ctx context.Context, token, id string, o Options) (List[Version], error) {
	out := List[Version]{Items: []Version{}}
	err := s.read(ctx, token, "/api/v1/documents/"+id+"/versions", func(tx app.WriteTx) error {
		if _, err := visibleDocument(tx, id); err != nil {
			return err
		}
		c, n, scope, err := s.cursorStart(ctx, token, tx, "versions:"+id, o)
		if err != nil {
			return err
		}
		rows, err := tx.QueryVersions(scope, id, c.Sequence, n+1)
		if err != nil {
			return err
		}
		for i, v := range rows {
			if i == n {
				c.Sequence = rows[i-1].Sequence
				out.NextCursor = cursorNext(c)
				break
			}
			out.Items = append(out.Items, version(v))
		}
		return nil
	})
	return out, err
}
func (s *Service) Version(ctx context.Context, token, id string) (Version, error) {
	var out Version
	err := s.read(ctx, token, "/api/v1/versions/"+id, func(tx app.WriteTx) error {
		v, err := visibleVersion(tx, id)
		if err == nil {
			out = version(v)
		}
		return err
	})
	return out, err
}
func (s *Service) Capabilities(ctx context.Context, token string) (map[string]any, error) {
	out := map[string]any{}
	err := s.read(ctx, token, "/api/v1/capabilities", func(tx app.WriteTx) error {
		l, err := tx.Library()
		if err != nil {
			return err
		}
		out = map[string]any{"library_id": l.ID, "api_version": "1.0", "runtime_generation": l.RuntimeGeneration, "stream_epoch": l.StreamEpoch, "unfiled_project_id": l.UnfiledProjectID, "features": map[string]bool{"resumable_uploads": true, "atomic_import": true, "cursor_files": true, "streamed_files": true, "byte_ranges": true, "source_verification": true, "html_js_v1": false, "multi_page_loader": false, "renderer": false, "preview": false, "provider": s.ProviderStatus != nil, "generation": s.GenerationAvailable, "questions": s.QuestionsAvailable, "scheduler": false, "search": true, "references": true, "backlinks": true, "review": true, "annotations": true, "revisit": true, "lifecycle": true, "purge": true, "backup": true}}
		return nil
	})
	return out, err
}

func remoteMappingsFromTx(tx app.WriteTx, externalOrigin string) ([]appconfig.RendererMapping, bool, error) {
	if externalOrigin == "" {
		return nil, false, nil
	}
	cfg, err := tx.Configuration()
	if err != nil {
		return nil, false, err
	}
	var value map[string]any
	if err := json.Unmarshal(cfg.Value, &value); err != nil {
		return nil, false, err
	}
	network, _ := value["network"].(map[string]any)
	if network == nil || network["remote_enabled"] != true {
		return nil, false, nil
	}
	configuredExternal, _ := network["external_base_url"].(string)
	left, _, _, leftOK := appconfig.CanonicalOrigin(configuredExternal)
	right, _, _, rightOK := appconfig.CanonicalOrigin(externalOrigin)
	if !leftOK || !rightOK || left != right {
		return nil, false, nil
	}
	return appconfig.RendererMappings(value), true, nil
}

// RemoteRendererConfigured reports whether the current persisted configuration
// enables at least one supported private HTTPS renderer mapping for the given
// external parent origin without touching per-account origin state.
func (s *Service) RemoteRendererConfigured(ctx context.Context, token, localOrigin, externalOrigin string) (bool, error) {
	supported := false
	err := s.read(ctx, token, "/api/v1/capabilities", func(tx app.WriteTx) error {
		mappings, ok, err := remoteMappingsFromTx(tx, externalOrigin)
		if err != nil || !ok {
			return err
		}
		for _, mapping := range mappings {
			if appconfig.PrivateRendererMappingReason(externalOrigin, mapping, localOrigin) == "" {
				supported = true
				return nil
			}
		}
		return nil
	})
	return supported, err
}

// RemoteVersionRendererSupported reports whether the authorized version has a
// supported private HTTPS renderer mapping in the current configuration without
// touching per-account origin state.
func (s *Service) RemoteVersionRendererSupported(ctx context.Context, token, versionID, localOrigin, externalOrigin string) (bool, error) {
	supported := false
	err := s.read(ctx, token, "/api/v1/versions/"+versionID+"/render-bootstrap", func(tx app.WriteTx) error {
		if _, err := visibleVersion(tx, versionID); err != nil {
			return err
		}
		mappings, ok, err := remoteMappingsFromTx(tx, externalOrigin)
		if err != nil || !ok {
			return err
		}
		for _, mapping := range mappings {
			if mapping.VersionID == versionID && appconfig.PrivateRendererMappingReason(externalOrigin, mapping, localOrigin) == "" {
				supported = true
				return nil
			}
		}
		return nil
	})
	return supported, err
}

// AuthorizeRemoteRenderSession authenticates the caller, checks target
// visibility (unless the caller already owns a known session for the version),
// and reports whether the version currently has a supported private HTTPS
// renderer mapping, before any lazy renderer construction or receipt creation.
func (s *Service) AuthorizeRemoteRenderSession(ctx context.Context, token, versionID, localOrigin, externalOrigin string, ownsKnown func(principalID string) bool) (string, bool, error) {
	principalID := ""
	supported := false
	err := s.Store.Write(ctx, func(tx app.WriteTx) error {
		record, err := s.Auth.AuthenticateWrite(ctx, tx, token)
		if err != nil {
			return err
		}
		principalID = record.PrincipalID
		if err := s.Auth.Authority(token).AuthorizeWrite(ctx, tx, domain.Request{
			Key:    domain.OperationKey{PrincipalID: record.PrincipalID},
			Method: "POST",
			Target: "/api/v1/versions/" + versionID + "/render-sessions",
		}); err != nil {
			return err
		}
		if ownsKnown == nil || !ownsKnown(record.PrincipalID) {
			if _, err := visibleVersion(tx, versionID); err != nil {
				return err
			}
		}
		mappings, ok, err := remoteMappingsFromTx(tx, externalOrigin)
		if err != nil || !ok {
			return err
		}
		for _, mapping := range mappings {
			if mapping.VersionID == versionID && appconfig.PrivateRendererMappingReason(externalOrigin, mapping, localOrigin) == "" {
				supported = true
				return nil
			}
		}
		return nil
	})
	return principalID, supported, err
}

func entryFile(id string, e domain.Entry) File {
	base := "/api/v1/versions/" + id
	out := File{Path: e.Path, Kind: e.Kind, Size: strconv.FormatInt(e.Size, 10), Preview: "download", FileURL: base + "/file?path=" + url.QueryEscape(e.Path)}
	if e.Kind == "file" {
		out.Hash = nullable(e.Hash)
		out.MediaType = "application/octet-stream"
		out.ETag = `"sha256-` + e.Hash + `"`
	} else {
		out.Preview = "unavailable"
		if e.Kind == "symlink" {
			out.LinkTarget = &e.LinkText
		}
		if e.Kind == "directory" {
			out.FilesURL = base + "/files?directory=" + url.QueryEscape(e.Path)
		}
		out.ETag = validator(out)
	}
	return out
}
func (s *Service) Files(ctx context.Context, token, id string, o Options) (List[File], error) {
	out := List[File]{Items: []File{}}
	directory := o.Directory
	if directory != "" {
		info, err := s.ResolveFile(ctx, token, id, directory, false)
		if err != nil {
			return out, err
		}
		if info.Kind != "directory" {
			return out, &Error{"filter_is_not_directory"}
		}
		directory = info.ResolvedPath
	}
	err := s.read(ctx, token, "/api/v1/versions/"+id+"/files", func(tx app.WriteTx) error {
		v, err := visibleVersion(tx, id)
		if err != nil {
			return err
		}
		if o.Kind != "" && o.Kind != "file" && o.Kind != "directory" && o.Kind != "symlink" {
			return &Error{"invalid_kind"}
		}
		if o.Directory != "" {
			if err := validPath(o.Directory); err != nil {
				return err
			}
		}
		c, n, _, err := s.cursorStart(ctx, token, tx, "files:"+id+":"+v.ContentSetID, o)
		if err != nil {
			return err
		}
		rows, err := tx.QueryEntries(v.ContentSetID, c.After, directory, o.Kind, n+1)
		if err != nil {
			return err
		}
		for i, e := range rows {
			if i == n {
				c.After = rows[i-1].Path
				out.NextCursor = cursorNext(c)
				break
			}
			out.Items = append(out.Items, entryFile(id, e))
		}
		return nil
	})
	return out, err
}

// Evidence composes the immutable authored record with the separate service
// truth (plan §3.1 state table): this read starts no new source capture or
// verification and never upgrades authored capture_status into a service
// check; retained snapshot bytes are the only excerpt source.
func (s *Service) Evidence(ctx context.Context, token, id, evidenceID string) (map[string]any, error) {
	return s.composeEvidence(ctx, token, id, evidenceID)
}

// Providers reads only current owner status; it cannot change authentication,
// start an inference or interpret catalog presence as request entitlement.
func (s *Service) Providers(ctx context.Context, token string) (domain.ProviderStatus, error) {
	out := domain.ProviderStatus{Provider: "codex", State: "provider_error", Reason: "transport_unavailable", Authentication: "unknown", Models: []string{}}
	actor, e := s.Auth.Authenticate(ctx, token)
	if e != nil {
		return out, e
	}
	if actor.Scope.Kind != "owner" {
		return out, domain.ErrDenied
	}
	if s.ProviderStatus != nil {
		out = s.ProviderStatus(ctx)
	}
	if e = s.Auth.Guard(ctx, token, func() error { return nil }); e != nil {
		return domain.ProviderStatus{}, e
	}
	return out, nil
}
