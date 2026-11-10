// Package query supplies current authenticated library queries and immutable
// byte delivery independently of HTTP, SQL and any future renderer.
package query

import (
	"context"
	"haeryong.local/haeryong/internal/domain"
	"io"
)

type Bytes interface {
	Read(context.Context, domain.Object, domain.Pin, func(io.ReadSeeker) error) error
}
type Options struct {
	Cursor, Project, Lifecycle, Directory, Kind, Review, Revisit string
	Limit                                                        int
}
type List[T any] struct {
	Items      []T     `json:"items"`
	NextCursor *string `json:"next_cursor"`
}
type Project struct {
	ID                  string `json:"project_id"`
	Name                string `json:"name"`
	Lifecycle           string `json:"lifecycle"`
	Revision            int64  `json:"revision"`
	PermissionsRevision int64  `json:"permissions_revision"`
	UpdatedAt           string `json:"updated_at"`
	ETag                string `json:"etag"`
}
type Document struct {
	Freshness any `json:"freshness"`
	domain.ReviewSummary
	Revisit         *bool   `json:"revisit"`
	ID              string  `json:"document_id"`
	ProjectID       string  `json:"project_id"`
	Title           string  `json:"title"`
	Lifecycle       string  `json:"lifecycle"`
	LatestVersionID *string `json:"latest_version_id"`
	Revision        int64   `json:"revision"`
	UpdatedAt       string  `json:"updated_at"`
	VersionsURL     string  `json:"versions_url"`
	ETag            string  `json:"etag"`
}
type Version struct {
	ID                  string  `json:"version_id"`
	DocumentID          string  `json:"document_id"`
	Sequence            int64   `json:"sequence"`
	BaseVersionID       *string `json:"base_version_id"`
	Title               string  `json:"title_snapshot"`
	DefaultPage         *string `json:"default_page"`
	ViewerProfile       string  `json:"viewer_profile"`
	SourceBasis         string  `json:"source_basis"`
	PayloadSHA          string  `json:"payload_sha256"`
	ManifestSHA         string  `json:"manifest_sha256"`
	SuppliedMetadataSHA *string `json:"supplied_metadata_sha256"`
	ManifestURL         string  `json:"manifest_url"`
	SuppliedMetadataURL *string `json:"supplied_metadata_url"`
	FilesURL            string  `json:"files_url"`
	BootstrapURL        string  `json:"render_bootstrap_url"`
	ETag                string  `json:"etag"`
}
type File struct {
	Path       string  `json:"path"`
	Kind       string  `json:"kind"`
	Size       string  `json:"byte_size"`
	Hash       *string `json:"bytes_sha256,omitempty"`
	LinkTarget *string `json:"link_target,omitempty"`
	MediaType  string  `json:"media_type,omitempty"`
	Preview    string  `json:"preview"`
	FileURL    string  `json:"file_url"`
	FilesURL   string  `json:"files_url,omitempty"`
	ETag       string  `json:"etag"`
}
type ResolvedFile struct {
	File
	RequestedPath string        `json:"requested_path"`
	ResolvedPath  string        `json:"resolved_path"`
	Object        domain.Object `json:"-"`
}
type Bootstrap struct {
	VersionID            string  `json:"version_id"`
	ViewerProfile        string  `json:"viewer_profile"`
	DefaultPage          *string `json:"default_page"`
	SelectedPage         *string `json:"selected_page"`
	RequestedPage        *string `json:"requested_page"`
	NavigationPath       *string `json:"navigation_path"`
	FilesURL             string  `json:"files_url"`
	SelectedFileURL      *string `json:"selected_file_url"`
	Delivery             string  `json:"delivery"`
	RendererAvailable    bool    `json:"renderer_available"`
	RenderSessionsURL    string  `json:"render_sessions_url"`
	RendererConstruction string  `json:"renderer_construction"`
	// RendererUnavailableReason names the honest condition when the renderer
	// is unavailable for this request's ingress (remote callers see the
	// loopback construction as an unreachable capability); it is omitted
	// whenever the renderer is available to the caller.
	RendererUnavailableReason string `json:"renderer_unavailable_reason,omitempty"`
}
type Error struct{ Reason string }

func (e *Error) Error() string { return "invalid query: " + e.Reason }
