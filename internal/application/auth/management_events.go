package auth

import (
	"encoding/json"
	app "haeryong.local/haeryong/internal/application/storage"
	"haeryong.local/haeryong/internal/domain"
)

// Apply the same current visibility at batch replay and final socket delivery.
// Finding events carry IDs only, but even those must not expose hidden targets.
func visibleFindingEvent(tx app.WriteTx, actor domain.Session, event domain.Event) domain.Event {
	hidden := domain.Event{StreamEpoch: event.StreamEpoch, Sequence: event.Sequence, ResourceKind: "library", Kind: "library.invalidated"}
	if actor.Scope.Kind != "owner" {
		return hidden
	}
	f, e := tx.Finding(event.ResourceID)
	if e != nil || f.Reason == "document_purged" {
		return hidden
	}
	versions := []string{}
	if f.VersionID != nil {
		versions = append(versions, *f.VersionID)
	}
	var collect func(any)
	collect = func(v any) {
		switch x := v.(type) {
		case map[string]any:
			for k, v := range x {
				if k == "version_id" || k == "target_version_id" || k == "source_version_id" {
					if id, ok := v.(string); ok && id != "" {
						versions = append(versions, id)
					}
				}
				collect(v)
			}
		case []any:
			for _, v := range x {
				collect(v)
			}
		}
	}
	for _, raw := range []json.RawMessage{f.Proposal, f.Evidence} {
		var v any
		if json.Unmarshal(raw, &v) != nil {
			return hidden
		}
		collect(v)
	}
	for _, id := range versions {
		if e := visibleTarget(tx, "/api/v1/versions/"+id); e != nil {
			return hidden
		}
	}
	return event
}
