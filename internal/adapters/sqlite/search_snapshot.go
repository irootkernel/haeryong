package sqlite

import (
	"fmt"
	"haeryong.local/haeryong/internal/domain"
	"time"
)

// Store-owned visibility generations survive replacement of the physical SQLite
// connection. Ordinary session touches, job progress and invisible writes do not
// advance the selected corpus's fence.
func (t *writeTx) SearchReadStamp(o domain.SearchSelection) (string, error) {
	return fmt.Sprint(t.searchGeneration.stamp(o)), nil
}

// Scope JSON is decoded only at the full start/end checks. Between slices,
// revisions, epochs and current permission generations fence the captured grant.
func (t *writeTx) SearchAuthorityStamp(record domain.Session) (string, error) {
	var stamp string
	var err error
	if record.Scope.Kind == "owner" {
		err = t.tx.QueryRowContext(t.ctx, `SELECT json_array(s.revoked,s.auth_epoch,s.principal_id,s.library_id,s.token_hash,s.created_at,l.auth_epoch,l.library_id,o.principal_id,(SELECT max(config_revision) FROM config_revisions))
 FROM device_sessions s,libraries l,auth_owner o WHERE s.session_id=? AND l.singleton=1 AND o.singleton=1`, record.ID).Scan(&stamp)
	} else {
		err = t.tx.QueryRowContext(t.ctx, `SELECT json_array(c.revision,c.revoked,c.auth_epoch,c.expires_at,l.auth_epoch,p.revision,p.active,(SELECT max(config_revision) FROM config_revisions),
 (SELECT coalesce(json_group_array(json_array(j.key,j.value,r.permissions_revision)),'[]') FROM json_each(c.permissions) j LEFT JOIN projects r ON r.project_id=j.key))
 FROM scoped_credentials c JOIN workflow_principals p ON p.principal_id=c.principal_id CROSS JOIN libraries l WHERE c.credential_id=? AND l.singleton=1`, record.ID).Scan(&stamp)
	}
	return stamp, notFound(err)
}

// Every stream limits physical rows before any visibility filter or aggregate.
// Encoding, validation, counts, hashes and sorting happen after releasing Store.
func (t *writeTx) SearchSnapshotSlice(o domain.SearchSelection, stage string, after int64) (out domain.SearchSnapshotSlice, err error) {
	var started time.Time
	var statement string
	args := []any{after, domain.SearchSliceRows}
	switch stage {
	case "versions":
		statement = `WITH physical AS MATERIALIZED (SELECT rowid AS physical_row,* FROM document_versions WHERE rowid>? ORDER BY rowid LIMIT ?)
 SELECT v.physical_row,v.version_id,v.content_set_id,d.project_id,v.document_id,v.title_snapshot,d.lifecycle,v.payload_sha256,v.manifest_sha256,coalesce(v.sequence,0),
 coalesce(i.state,'pending'),coalesce(i.reason,''),coalesce(i.revision,0),coalesce(i.extractor_revision,0),coalesce(i.discovery_complete,0),d.latest_version_id
 FROM physical v JOIN documents d ON d.document_id=v.document_id JOIN projects p ON p.project_id=d.project_id LEFT JOIN search_indexes i ON i.version_id=v.version_id ORDER BY v.physical_row`
	case "pages":
		statement = `SELECT rowid,version_id,page_path FROM version_pages WHERE rowid>? ORDER BY rowid LIMIT ?`
	case "entries":
		statement = `SELECT rowid,content_set_id,relative_path FROM version_entries WHERE rowid>? ORDER BY rowid LIMIT ?`
	case "units":
		statement = `WITH physical AS MATERIALIZED (SELECT unit_id FROM search_units WHERE unit_id>? ORDER BY unit_id LIMIT ?)
 SELECT u.unit_id,u.version_id,u.kind,u.page_path,u.region_id,u.source_id,u.title,u.input_hash,u.state,u.body_state,u.anchor_state,u.reason,u.read_state,u.read_body_state,u.read_anchor_state
 FROM physical p JOIN (` + searchReadUnits + `) u ON u.unit_id=p.unit_id ORDER BY u.unit_id`
	case "chunks":
		statement = `SELECT chunk_id,unit_id,part,ordinal,
 CASE WHEN part='metadata' THEN title ELSE '' END,CASE WHEN part='metadata' THEN path ELSE '' END,CASE WHEN part='metadata' THEN terms ELSE '' END,
 CASE WHEN part='metadata' THEN body ELSE '' END,CASE WHEN part='metadata' THEN display_body ELSE '' END
 FROM search_chunks WHERE chunk_id>? ORDER BY chunk_id LIMIT ?`
	default:
		return out, domain.ErrUnsupportedDerivedData
	}
	rows, err := t.tx.QueryContext(t.ctx, statement, args...)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	started = time.Now()
	for rows.Next() {
		out.Examined++
		switch stage {
		case "versions":
			var v domain.SearchVersionState
			var latest *string
			if err = rows.Scan(&out.After, &v.ID, &v.ContentSetID, &v.ProjectID, &v.DocumentID, &v.Title, &v.Lifecycle, &v.PayloadSHA, &v.ManifestSHA, &v.Sequence, &v.IndexState, &v.Reason, &v.IndexRevision, &v.ExtractorRevision, &v.DiscoveryComplete, &latest); err != nil {
				return out, err
			}
			v.SnapshotRow = out.After
			visible := o.Scope.Kind == "owner" || admits("version.read", o.Scope.Actions...) && admits(v.ProjectID, o.Scope.ProjectIDs...) && o.VersionSet[v.ID]
			if visible && v.Sequence > 0 && v.Lifecycle != "trash" && v.Lifecycle != "purged" && (o.Project == "" || o.Project == v.ProjectID) && (o.Lifecycle == "all" || o.Lifecycle == v.Lifecycle) && (o.Versions == "all" || latest != nil && *latest == v.ID) {
				if v.IndexRevision > 0 {
					if err = validateSearchIndex(domain.SearchIndex{VersionID: v.ID, State: v.IndexState, Reason: v.Reason, Revision: v.IndexRevision, ExtractorRevision: v.ExtractorRevision}); err != nil {
						return out, err
					}
				}
				out.Versions = append(out.Versions, v)
			}
		case "pages":
			var p domain.SearchSnapshotPage
			if err = rows.Scan(&out.After, &p.VersionID, &p.Path); err != nil {
				return out, err
			}
			out.Pages = append(out.Pages, p)
		case "entries":
			var e domain.SearchSnapshotEntry
			if err = rows.Scan(&out.After, &e.ContentSetID, &e.Path); err != nil {
				return out, err
			}
			out.Entries = append(out.Entries, e)
		case "units":
			var u domain.SearchSnapshotUnit
			v := &u.Unit
			if err = rows.Scan(&v.ID, &v.VersionID, &v.Kind, &v.PagePath, &v.RegionID, &v.SourceID, &v.Title, &v.InputHash, &v.State, &v.BodyState, &v.AnchorState, &v.Reason, &u.ReadState, &u.ReadBodyState, &u.ReadAnchorState); err != nil {
				return out, err
			}
			if o.VersionSet[v.VersionID] {
				if err = validateSearchUnit(*v); err != nil {
					return out, err
				}
			}
			out.After = v.ID
			out.Units = append(out.Units, u)
		case "chunks":
			var c domain.SearchChunk
			if err = rows.Scan(&c.ID, &c.UnitID, &c.Part, &c.Ordinal, &c.Title, &c.Path, &c.Terms, &c.Body, &c.DisplayBody); err != nil {
				return out, err
			}
			out.After = c.ID
			out.Chunks = append(out.Chunks, c)
		}
		if time.Since(started) >= domain.SearchSliceTime {
			break
		}
	}
	return out, rows.Err()
}
