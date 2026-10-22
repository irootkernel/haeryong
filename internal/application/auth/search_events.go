package auth

import (
	"errors"
	app "haeryong.local/haeryong/internal/application/storage"
	"haeryong.local/haeryong/internal/domain"
)

// Search events expose only a version identity/revision, using the same existing
// version.read and exact project/version anchors as Check. Re-evaluate both on
// replay and at live delivery; no search action or job-body access is required.
func visibleIndexEvent(tx app.WriteTx, record domain.Session, event domain.Event) (domain.Event, error) {
	mask := func() domain.Event {
		return domain.Event{StreamEpoch: event.StreamEpoch, Sequence: event.Sequence, ResourceKind: "library", Kind: "library.invalidated"}
	}
	if event.ResourceKind != "version" {
		return mask(), nil
	}
	target := "/api/v1/versions/" + event.ResourceID
	_, err := tx.PublishedVersion(event.ResourceID)
	if err == nil {
		err = authorizeScope(tx, record, domain.Request{Method: "GET", Target: target})
	}
	if err == nil {
		err = visibleTarget(tx, target)
	}
	if errors.Is(err, domain.ErrDenied) || errors.Is(err, domain.ErrNotFound) {
		return mask(), nil
	}
	event.Body = nil
	return event, err
}
