package domain

import "errors"

var ErrUnsupportedDerivedData = errors.New("unsupported derived data")
var ErrSearchRestricted = errors.New("search maintenance authority needs revalidation")

const SearchExtractorRevision int64 = 2
const SearchMergePages = 1

// This bounds derived posting payloads, not accepted author content.
const SearchPostingBytes = 128 * SearchChunkSize
const SearchNominationPrefix = "\x01"

// SearchDisplay is a revisioned derived metadata payload, never an API shape.
type SearchDisplay struct {
	Literal string `json:"literal"`
	Display string `json:"display"`
}

// SearchProgress is shared by running and terminal maintenance jobs. Counts are
// decimal strings, and an unknown total is null. Checkpoints contain no text.
type SearchProgress struct {
	VersionID  string  `json:"version_id"`
	WorkKind   string  `json:"work_kind"`
	Stage      string  `json:"stage"`
	FilesDone  string  `json:"files_done"`
	FilesTotal *string `json:"files_total"`
	BytesDone  string  `json:"bytes_done"`
	BytesTotal *string `json:"bytes_total"`
	PagesDone  string  `json:"pages_done"`
	PagesTotal *string `json:"pages_total"`
	Failed     string  `json:"failed"`
	Checkpoint string  `json:"checkpoint"`
}

type SearchMaintenanceScope struct {
	Actor             string `json:"actor"`
	LibraryID         string `json:"library_id"`
	ProjectID         string `json:"project_id"`
	DocumentID        string `json:"document_id"`
	VersionID         string `json:"version_id"`
	ContentSetID      string `json:"content_set_id"`
	PayloadSHA        string `json:"payload_sha256"`
	ManifestSHA       string `json:"manifest_sha256"`
	ExtractorRevision int64  `json:"extractor_revision"`
}

type SearchInventoryItem struct {
	Key      string
	Unit     SearchUnit
	Metadata []byte
}

type DerivedWork struct {
	JobID, VersionID, WorkKind, AfterPath string
	ExtractorRevision                     int64
}
type SearchIndex struct {
	VersionID, JobID, State, Reason string
	ExtractorRevision, Revision     int64
	DiscoveryComplete               bool
}
type SearchUnit struct {
	ID                                                              int64
	VersionID, Kind, PagePath, RegionID, SourceID, Title, InputHash string
	State, BodyState, AnchorState, Reason                           string
}
type SearchChunk struct {
	ID, UnitID                            int64
	Part                                  string
	Ordinal                               int
	Title, Path, Terms, Body, DisplayBody string
}
type PageAnchor struct {
	VersionID, PagePath, ID, LocatorKind, Label, TextSHA256 string
	Occurrences                                             int
}
