package storage

import (
	"haeryong.local/haeryong/internal/application/referencecodec"
	"haeryong.local/haeryong/internal/domain"
	"reflect"
)

// PreparedReference owns a validated immutable declaration. Prepare it before
// entering Store.Write: decoding and raw/typed equality may process large text.
// Private fields prevent callers from changing a validated tuple or JSON bytes.
type PreparedReference struct {
	columns domain.AuthoredReference
	raw     string
	ready   bool
}

func PrepareAuthoredReference(r domain.AuthoredReference) (PreparedReference, error) {
	if err := r.Validate(); err != nil {
		return PreparedReference{}, err
	}
	decoded, err := referencecodec.DecodeReference(r.AuthoredJSON)
	if err != nil {
		return PreparedReference{}, domain.ErrUnsupportedDerivedData
	}
	decoded.FromVersionID = r.FromVersionID
	if !reflect.DeepEqual(decoded, r) {
		return PreparedReference{}, domain.ErrUnsupportedDerivedData
	}
	raw := string(decoded.AuthoredJSON)
	decoded.AuthoredJSON = nil
	return PreparedReference{columns: decoded, raw: raw, ready: true}, nil
}

// Columns returns detached optional pointers and no raw bytes. JSON is immutable
// text exposed separately, so mutation cannot invalidate the prepared input.
func (p PreparedReference) Columns() domain.AuthoredReference {
	r := p.columns
	clone := func(v *string) *string {
		if v == nil {
			return nil
		}
		s := *v
		return &s
	}
	r.FromPagePath, r.FromRegionID = clone(r.FromPagePath), clone(r.FromRegionID)
	r.TargetPagePath, r.TargetRegionID = clone(r.TargetPagePath), clone(r.TargetRegionID)
	r.Relation, r.Label = clone(r.Relation), clone(r.Label)
	return r
}
func (p PreparedReference) JSON() string { return p.raw }
func (p PreparedReference) Ready() bool  { return p.ready }

// ReferenceTx exposes only typed, bounded persistence. These internal records
// grant no read authority. Public readers apply both endpoint eligibility rules;
// maintenance supplies structurally verified canonical paths/anchors before a put.
type ReferenceTx interface {
	ReferenceLifecycle(string) (domain.ReferenceLifecycle, error)
	ClearReferenceData(string, int) (bool, error)
	EnqueueReferences(string, int64) (string, error)
	ReferenceSearchTerminal(string) (bool, error)
	ReferenceFence(string, int64) (domain.DerivedWork, error)
	SuspendReferences(string, int64) error
	ResumeReferences(string, int64) error
	CompleteReferences(string) error
	FailReferences(string) error
	ReferenceProgress(string, string, string, int64, int64) error
	ReferenceAnchor(string, string, string) (domain.ReferenceAnchor, error)
	PutReferenceMaterialization(domain.ReferenceMaterialization) error
	ReferenceMaterialization(string) (domain.ReferenceMaterialization, error)
	PutAuthoredReference(PreparedReference) error
	AuthoredReference(string, string) (domain.AuthoredReference, error)
	AuthoredReferences(string, string, int) ([]domain.AuthoredReference, error)
	IncomingReferences(string, string, string, int) ([]domain.AuthoredReference, error)
	PutAuthoredEdge(domain.AuthoredEdge) error
	AuthoredEdge(string, string) (domain.AuthoredEdge, error)
}
