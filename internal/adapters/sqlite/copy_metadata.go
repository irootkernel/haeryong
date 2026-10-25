package sqlite

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"

	"haeryong.local/haeryong/internal/domain"
)

func CopyNoteObjects(ctx context.Context, path, owner string, raw json.RawMessage, consume func(domain.Object) error) ([]json.RawMessage, error) {
	var note domain.Annotation
	if e := json.Unmarshal(raw, &note); e != nil {
		return nil, e
	}
	if note.BodyObject != nil {
		if e := consume(*note.BodyObject); e != nil {
			return nil, e
		}
	}
	out := []json.RawMessage{}
	if note.ContextRef == nil {
		return out, nil
	}
	db, e := openCopy(path)
	if e != nil {
		return nil, e
	}
	defer db.Close()
	var object domain.Object
	var version, media string
	var page sql.NullString
	e = db.QueryRowContext(ctx, `SELECT content_hash,byte_size,version_id,page_path,media_type FROM contexts WHERE context_id=? AND principal_id=?`, *note.ContextRef, owner).Scan(&object.Hash, &object.Size, &version, &page, &media)
	if e != nil {
		return nil, e
	}
	if e = consume(object); e != nil {
		return nil, e
	}
	descriptor, _ := json.Marshal(map[string]any{"context_ref": *note.ContextRef, "version_id": version, "page_path": nullablePage(page), "media_type": media, "sha256": object.Hash, "bytes": object.Size})
	return append(out, descriptor), nil
}

func CopyDependencies(ctx context.Context, path, id string, v domain.CopyVersion, includeSources bool, consume func(json.RawMessage) error, object func(domain.Object) error) error {
	db, e := openCopy(path)
	if e != nil {
		return e
	}
	defer db.Close()
	// Note identity closure may retain exact anchors outside payload membership.
	// These are dependencies, never an instruction to expand or retarget payload.
	anchors, e := db.QueryContext(ctx, selectedCopyNotes+` SELECT DISTINCT json_extract(n.value,'$.document_id'),json_extract(n.value,'$.'||a.value||'.version_id'),json_extract(n.value,'$.'||a.value||'.page_path'),json_extract(n.value,'$.'||a.value||'.region_id') FROM note_values n,json_each('["anchor","original_anchor"]') a WHERE json_extract(n.value,'$.document_id')=? ORDER BY 1,2,3,4`, id, v.DocumentID)
	if e != nil {
		return e
	}
	lookup, e := openCopy(path)
	if e != nil {
		anchors.Close()
		return e
	}
	defer lookup.Close()
	for anchors.Next() {
		var doc, version string
		var page, region sql.NullString
		if e = anchors.Scan(&doc, &version, &page, &region); e != nil {
			anchors.Close()
			return e
		}
		var included, purged, present bool
		if e = lookup.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM copy_versions WHERE set_id=? AND version_id=?),EXISTS(SELECT 1 FROM purged_versions WHERE version_id=?),EXISTS(SELECT 1 FROM document_versions WHERE version_id=?)`, id, version, version, version).Scan(&included, &purged, &present); e != nil {
			anchors.Close()
			return e
		}
		state := "unavailable"
		if included {
			state = "included"
		} else if purged {
			state = "tombstoned"
		} else if present {
			state = "excluded_by_scope"
		}
		raw, _ := json.Marshal(map[string]any{"kind": "note_anchor", "document_id": doc, "version_id": version, "page_path": nullablePage(page), "region_id": nullablePage(region), "state": state})
		if e = consume(raw); e != nil {
			anchors.Close()
			return e
		}
	}
	e = anchors.Err()
	anchors.Close()
	if e != nil {
		return e
	}

	rows, e := db.QueryContext(ctx, `SELECT target_document_id,target_version_id,target_page_path,target_region_id,EXISTS(SELECT 1 FROM copy_versions p WHERE p.set_id=? AND p.version_id=r.target_version_id AND p.document_id=r.target_document_id),EXISTS(SELECT 1 FROM purged_versions p WHERE p.version_id=r.target_version_id AND p.document_id=r.target_document_id),EXISTS(SELECT 1 FROM document_versions p WHERE p.version_id=r.target_version_id AND p.document_id=r.target_document_id) FROM authored_references r WHERE from_version_id=? ORDER BY metadata_reference_id`, id, v.VersionID)
	if e != nil {
		return e
	}
	for rows.Next() {
		var doc, version string
		var page, region sql.NullString
		var included, purged, present bool
		if e = rows.Scan(&doc, &version, &page, &region, &included, &purged, &present); e != nil {
			rows.Close()
			return e
		}
		state := "unavailable"
		if included {
			state = "included"
		} else if purged {
			state = "tombstoned"
		} else if present {
			state = "excluded_by_scope"
		}
		var p, r any
		if page.Valid {
			p = page.String
		}
		if region.Valid {
			r = region.String
		}
		raw, _ := json.Marshal(map[string]any{"kind": "reference", "document_id": doc, "version_id": version, "page_path": p, "region_id": r, "state": state})
		if e = consume(raw); e != nil {
			rows.Close()
			return e
		}
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return e
	}
	rows, e = db.QueryContext(ctx, `SELECT source_id,metadata FROM version_sources WHERE version_id=? ORDER BY source_id`, v.VersionID)
	if e != nil {
		return e
	}
	defer rows.Close()
	objectsDB, e := openCopy(path)
	if e != nil {
		return e
	}
	defer objectsDB.Close()
	for rows.Next() {
		var source struct {
			ID  string
			Raw json.RawMessage
		}
		if e = rows.Scan(&source.ID, &source.Raw); e != nil {
			return e
		}
		var meta struct {
			Retention string  `json:"retention_mode"`
			Asset     *string `json:"asset_path"`
		}
		if e = json.Unmarshal(source.Raw, &meta); e != nil {
			return e
		}
		state := "external_reference"
		if meta.Asset != nil && meta.Retention != "reference_only" {
			var present int
			if e = objectsDB.QueryRowContext(ctx, `SELECT count(*) FROM version_entries WHERE content_set_id=? AND relative_path=? AND kind='file'`, v.ContentSetID, *meta.Asset).Scan(&present); e != nil {
				return e
			}
			if present == 1 {
				state = "included"
			} else {
				state = "unavailable"
			}
		}

		raw, _ := json.Marshal(map[string]any{"kind": "source", "source_id": source.ID, "version_id": v.VersionID, "state": state, "source": source.Raw})
		if e = consume(raw); e != nil {
			return e
		}
	}
	if e = rows.Err(); e != nil {
		return e
	}
	if e = rows.Close(); e != nil {
		return e
	}
	// Current production separately retained source material is the immutable
	// expectation excerpt of a source verification, not an invented asset field.
	rows, e = db.QueryContext(ctx, `SELECT x.verification_id,x.expected_excerpt_ref,x.expected_excerpt,o.byte_size,o.state FROM citation_verifications x LEFT JOIN content_objects o ON o.content_hash=x.expected_excerpt_ref WHERE x.version_id=? AND (x.expected_excerpt_ref IS NOT NULL OR x.expected_excerpt IS NOT NULL) ORDER BY x.verification_id`, v.VersionID)
	if e != nil {
		return e
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		var ref, state sql.NullString
		var size sql.NullInt64
		var inline []byte
		if e = rows.Scan(&id, &ref, &inline, &size, &state); e != nil {
			return e
		}
		row := map[string]any{"kind": "verification_expectation", "verification_id": id, "version_id": v.VersionID, "state": "excluded_by_scope"}
		if ref.Valid {
			row["sha256"] = ref.String
		}
		if includeSources {
			row["state"] = "included"
			if ref.Valid {
				if !size.Valid || !state.Valid || state.String != "ready" {
					return domain.ErrContentIntegrity
				}
				if e = object(domain.Object{Hash: ref.String, Size: size.Int64, State: state.String}); e != nil {
					return e
				}
				row["bytes"] = size.Int64
			} else {
				h := sha256.Sum256(inline)
				row["sha256"] = hex.EncodeToString(h[:])
				row["bytes"] = len(inline)
				if inline == nil {
					inline = []byte{}
				}
				row["inline_base64"] = inline
			}
		}
		raw, _ := json.Marshal(row)
		if e = consume(raw); e != nil {
			return e
		}
	}
	return rows.Err()
}

func nullablePage(v sql.NullString) any {
	if v.Valid {
		return v.String
	}
	return nil
}
