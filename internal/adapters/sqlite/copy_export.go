package sqlite

import (
	"context"
	"encoding/json"
	"errors"

	"haeryong.local/haeryong/internal/domain"
)

func CopySelectionPage(ctx context.Context, path, id, after string) ([]domain.CopyVersion, error) {
	db, e := openCopy(path)
	if e != nil {
		return nil, e
	}
	defer db.Close()
	rows, e := db.QueryContext(ctx, `SELECT document_id,version_id,content_set_id,payload_sha,manifest_sha,metadata_sha FROM copy_versions WHERE set_id=? AND version_id>? ORDER BY version_id LIMIT 64`, id, after)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []domain.CopyVersion{}
	for rows.Next() {
		var v domain.CopyVersion
		if e = rows.Scan(&v.DocumentID, &v.VersionID, &v.ContentSetID, &v.PayloadSHA, &v.ManifestSHA, &v.MetadataSHA); e != nil {
			return nil, e
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
func CopyEntryPage(ctx context.Context, path, set, after string) ([]domain.Entry, error) {
	db, e := openCopy(path)
	if e != nil {
		return nil, e
	}
	defer db.Close()
	rows, e := db.QueryContext(ctx, `SELECT relative_path,kind,coalesce(content_hash,''),byte_size,coalesce(exact_link_text,'') FROM version_entries WHERE content_set_id=? AND relative_path>? ORDER BY relative_path COLLATE BINARY LIMIT 64`, set, after)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []domain.Entry{}
	for rows.Next() {
		var v domain.Entry
		v.ContentSetID = set
		if e = rows.Scan(&v.Path, &v.Kind, &v.Hash, &v.Size, &v.LinkText); e != nil {
			return nil, e
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// StreamCopyRecords only admits named persisted record families and typed
// owner/version keys. It never exports the operational or credential ledger.
func StreamCopyRecords(ctx context.Context, path, set, owner string, v domain.CopyVersion, kinds []string, consume func(string, json.RawMessage) error) error {
	db, e := openCopy(path)
	if e != nil {
		return e
	}
	defer db.Close()
	for _, kind := range kinds {
		var query string
		args := []any{owner, v.VersionID}
		switch kind {
		case "review":
			query = `SELECT json_object('owner_id',owner_id,'version_id',version_id,'state',state,'reviewed_at',reviewed_at,'revision',revision,'event_sequence',event_sequence) FROM review_states WHERE owner_id=? AND version_id=? UNION ALL SELECT json_object('event_id',event_id,'owner_id',owner_id,'version_id',version_id,'action',action,'old_state',old_state,'new_state',new_state,'operation_id',operation_id,'created_at',created_at) FROM review_events WHERE owner_id=? AND version_id=?`
			args = append(args, owner, v.VersionID)
		case "revisit":
			query = `SELECT json_object('owner_id',owner_id,'document_id',document_id,'flag',json(CASE WHEN flag THEN 'true' ELSE 'false' END),'revision',revision) FROM revisit_flags WHERE owner_id=? AND document_id=?`
			args = []any{owner, v.DocumentID}
		case "notes":
			query = selectedCopyNotes + ` SELECT value FROM notes WHERE document_id=? UNION SELECT h.value FROM annotation_history h JOIN notes n ON n.annotation_id=h.annotation_id WHERE n.document_id=? ORDER BY 1`
			args = []any{set, v.DocumentID, v.DocumentID}

		case "reader":
			prefix := owner + "/" + v.VersionID
			query = `SELECT json_object('kind',resource_kind,'resource_id',resource_id,'revision',revision,'value',json(value)) FROM resource_revisions WHERE (resource_kind IN ('reading_position','reader_preference') AND resource_id=?) OR (resource_kind='page_position' AND substr(resource_id,1,length(?)+1)=?||'/')`
			args = []any{prefix, prefix, prefix}
		case "references":
			query = `SELECT json_object('from_version_id',from_version_id,'metadata_reference_id',metadata_reference_id,'authored',json(authored_json)) FROM authored_references WHERE from_version_id=?`
			args = []any{v.VersionID}
		case "decisions":
			return errors.New("decision export producer unavailable")
		default:
			return domain.ErrInvalidTarget
		}
		rows, e := db.QueryContext(ctx, query, args...)
		if e != nil {
			return e
		}
		for rows.Next() {
			var raw []byte
			if e = rows.Scan(&raw); e != nil {
				rows.Close()
				return e
			}
			if e = consume(kind, json.RawMessage(raw)); e != nil {
				rows.Close()
				return e
			}
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return e
		}
	}
	return nil
}
