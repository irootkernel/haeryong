package domain

import (
	"crypto/sha1"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"
)

// ReferenceRevision must advance with reference parsing or static anchor changes,
// including HTML classification or any SearchExtractorRevision change. It is not a trigger generation.
const ReferenceRevision int64 = 3

type ReferenceMaterialization struct {
	FromVersionID, ManifestSHA, State, JobID string
	ReferenceRevision, Revision              int64
}

// AuthoredReference preserves supplied paths and optional fields. Canonical
// resolved paths belong to AuthoredEdge; neither changes the immutable manifest.
type AuthoredReference struct {
	FromVersionID, ID, TargetDocumentID, TargetVersionID       string
	FromPagePath, FromRegionID, TargetPagePath, TargetRegionID *string
	PageQuery, PageFragment                                    string
	Relation, Label                                            *string
	AuthoredJSON                                               []byte
}

type AuthoredEdge struct {
	ID, FromVersionID, ReferenceID                             string
	FromPagePath, FromRegionID                                 *string
	TargetProjectID, TargetDocumentID, TargetVersionID         string
	TargetPagePath, TargetRegionID                             *string
	FromPageSHA, FromAnchorSHA, TargetPageSHA, TargetAnchorSHA *string
	AnchorRevision                                             int64
}

var referenceUUID = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
var referenceHash = regexp.MustCompile(`^[0-9a-f]{64}$`)

// Fixed application namespace, not a content hash or authority token.
var referenceNamespace = [16]byte{0xd5, 0xef, 0xb3, 0xc4, 0xaf, 0x71, 0x5d, 0x98, 0x86, 0x21, 0xbd, 0x39, 0x51, 0x28, 0x5a, 0xe0}

func ReferenceEdgeID(library, version, localID string) (string, error) {
	if !referenceUUID.MatchString(library) || !referenceUUID.MatchString(version) || localID == "" || !utf8.ValidString(localID) {
		return "", ErrUnsupportedDerivedData
	}
	h := sha1.New()
	h.Write(referenceNamespace[:])
	for _, part := range []string{library, version, localID} {
		var size [8]byte
		binary.BigEndian.PutUint64(size[:], uint64(len(part)))
		h.Write(size[:])
		h.Write([]byte(part))
	}
	id := h.Sum(nil)[:16]
	id[6] = (id[6] & 0x0f) | 0x50
	id[8] = (id[8] & 0x3f) | 0x80
	raw := hex.EncodeToString(id)
	return fmt.Sprintf("%s-%s-%s-%s-%s", raw[:8], raw[8:12], raw[12:16], raw[16:20], raw[20:]), nil
}

func (m ReferenceMaterialization) Validate() error {
	if !referenceUUID.MatchString(m.FromVersionID) || !referenceUUID.MatchString(m.JobID) || !referenceHash.MatchString(m.ManifestSHA) || m.ReferenceRevision < 1 || m.Revision < 1 || (m.State != "building" && m.State != "ready" && m.State != "failed") {
		return ErrUnsupportedDerivedData
	}
	return nil
}
func validReferenceText(s *string) bool { return s == nil || utf8.ValidString(*s) }

// ValidReferencePath applies exact relative UTF-8 path rules; nil is version scope.
func ValidReferencePath(p *string) bool {
	if p == nil {
		return true
	}
	s := *p
	if s == "" || !utf8.ValidString(s) || strings.ContainsRune(s, 0) || strings.HasPrefix(s, "/") {
		return false
	}
	if len(s) >= 2 && ((s[0] >= 'A' && s[0] <= 'Z') || (s[0] >= 'a' && s[0] <= 'z')) && s[1] == ':' {
		return false
	}
	for _, part := range strings.Split(s, "/") {
		if part == "" || part == "." || part == ".." {
			return false
		}
	}
	return true
}
func validReferenceAnchor(p, r *string) bool {
	return ValidReferencePath(p) && (r == nil || (p != nil && *r != "" && utf8.ValidString(*r)))
}
func (r AuthoredReference) Validate() error {
	if err := r.ValidateFields(); err != nil || !json.Valid(r.AuthoredJSON) {
		return ErrUnsupportedDerivedData
	}
	return nil
}

// ValidateFields checks stored typed columns without reparsing immutable JSON.
func (r AuthoredReference) ValidateFields() error {
	if !referenceUUID.MatchString(r.FromVersionID) || !referenceUUID.MatchString(r.TargetDocumentID) || !referenceUUID.MatchString(r.TargetVersionID) || r.ID == "" || !utf8.ValidString(r.ID) || !validReferenceAnchor(r.FromPagePath, r.FromRegionID) || !validReferenceAnchor(r.TargetPagePath, r.TargetRegionID) || !utf8.ValidString(r.PageQuery) || !utf8.ValidString(r.PageFragment) || !validReferenceText(r.Label) || !validReferenceText(r.Relation) {
		return ErrUnsupportedDerivedData
	}
	return nil
}
func (e AuthoredEdge) Validate() error {
	if !referenceUUID.MatchString(e.ID) || !referenceUUID.MatchString(e.FromVersionID) || !referenceUUID.MatchString(e.TargetProjectID) || !referenceUUID.MatchString(e.TargetDocumentID) || !referenceUUID.MatchString(e.TargetVersionID) || e.ReferenceID == "" || !utf8.ValidString(e.ReferenceID) || !validReferenceAnchor(e.FromPagePath, e.FromRegionID) || !validReferenceAnchor(e.TargetPagePath, e.TargetRegionID) || e.AnchorRevision < 1 {
		return ErrUnsupportedDerivedData
	}
	for _, pair := range [][2]*string{{e.FromPagePath, e.FromPageSHA}, {e.FromRegionID, e.FromAnchorSHA}, {e.TargetPagePath, e.TargetPageSHA}, {e.TargetRegionID, e.TargetAnchorSHA}} {
		if (pair[0] == nil) != (pair[1] == nil) || (pair[1] != nil && !referenceHash.MatchString(*pair[1])) {
			return ErrUnsupportedDerivedData
		}
	}
	return nil
}

// ReferenceAnchor is a current-revision page EOF/anchor snapshot. A reason
// denotes structural unavailability and never grants principal authority.
type ReferenceAnchor struct {
	PageHash, AnchorHash, Reason  string
	SearchJob                     string
	SearchRevision, IndexRevision int64
}

// ReferenceLifecycle is the minimal retained document fact used by D4.
type ReferenceLifecycle struct{ ProjectID, Lifecycle string }
