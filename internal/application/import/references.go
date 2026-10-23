package importer

import (
	"haeryong.local/haeryong/internal/application/referencecodec"
	"haeryong.local/haeryong/internal/domain"
)

// DecodeReference is the supplied-metadata facade over the shared pure codec.
func DecodeReference(raw []byte) (domain.AuthoredReference, error) {
	r, err := referencecodec.DecodeReference(raw)
	if e, ok := err.(*referencecodec.Error); ok {
		return r, invalidMeta(e.Pointer)
	}
	return r, err
}
