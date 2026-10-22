package domain

import "time"

// The persisted window geometry is shared by extraction and authenticated reads.
const SearchChunkSize = 32768
const SearchChunkOverlap = 4095

// SearchSelection is the exact authenticated corpus; it never authorizes source reads.
type SearchSelection struct {
	Scope                              CredentialScope
	VersionSet                         map[string]bool
	FenceKey                           string // exact authenticated authority identity, computed once per request
	Project, Lifecycle, Versions, Kind string
}
type SearchVersionState struct {
	ID, ProjectID, DocumentID, Title, Lifecycle, PayloadSHA, ManifestSHA          string
	SnapshotRow                                                                   int64  `json:"-"`
	ContentSetID                                                                  string `json:"-"`
	Sequence                                                                      int64
	IndexState, Reason                                                            string
	IndexRevision, ExtractorRevision                                              int64
	DiscoveryComplete                                                             bool
	FilesTotal, FilesDiscovered, HTMLTotal, HTMLComplete, HTMLFailed, Unsupported int64
	UnitReasons                                                                   []string
	VisibleMetadata                                                               string
	VisibleUnits                                                                  string
}
type SearchCandidate struct {
	Metadata      []byte
	Before, After string
	Unit          SearchUnit
	Chunk         SearchChunk
}

// A slice examines physical rows before MATCH/instr, so misses cannot scan an
// unbounded corpus while holding the single store connection.
const SearchSliceRows = 64

// Yield after the first completed physical row at the small iteration budget.
// SQL preparation/nomination has the independent physical row bound above.
const SearchSliceTime = 8 * time.Millisecond

type SearchSlice struct {
	Candidates []SearchCandidate
	After      int64
	Examined   int
}

// SearchSnapshotSlice carries bounded physical rows. The application admits
// only the fully authenticated selected versions before aggregation or hashing.
type SearchSnapshotSlice struct {
	After    int64
	Examined int
	Versions []SearchVersionState
	Units    []SearchSnapshotUnit
	Entries  []SearchSnapshotEntry
	Pages    []SearchSnapshotPage
	Chunks   []SearchChunk
}
type SearchSnapshotUnit struct {
	Unit                                      SearchUnit
	ReadState, ReadBodyState, ReadAnchorState string
}
type SearchSnapshotEntry struct{ ContentSetID, Path string }
type SearchSnapshotPage struct{ VersionID, Path string }
