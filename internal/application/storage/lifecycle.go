package storage

import "haeryong.local/haeryong/internal/domain"

// CheckLifecycleApplication fences a captured request even if an owner restored
// the same identity before the late result arrived. A caller may reauthorize a
// new request explicitly; it cannot silently reuse the old captured revision.
func CheckLifecycleApplication(tx WriteTx, document string, revision int64) error {
	d, e := tx.Document(document)
	if e != nil {
		return e
	}
	if d.Lifecycle != "active" && d.Lifecycle != "archived" {
		return domain.ErrNotFound
	}
	if d.Revision != revision {
		return domain.ErrPrecondition
	}
	return nil
}
