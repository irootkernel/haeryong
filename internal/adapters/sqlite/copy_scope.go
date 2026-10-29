package sqlite

// Scoped immutable ownership is derived from the same snapshot as selection.
// Ordinary payload files are unconditional; additional retained sources and
// note/history context require their explicit selected record family.
const selectedCopyNotes = `WITH choice AS (SELECT set_id,value FROM copy_sets WHERE set_id=?),
picked AS (SELECT v.* FROM copy_versions v JOIN choice c ON c.set_id=v.set_id),
notes AS (SELECT a.* FROM annotations a WHERE a.owner_id=(SELECT json_extract(value,'$.OwnerID') FROM choice)
 AND EXISTS(SELECT 1 FROM choice,json_each(choice.value,'$.Request.records') r WHERE r.value='notes')
 AND EXISTS(SELECT 1 FROM picked p WHERE p.document_id=a.document_id AND
 (p.version_id=a.version_id OR p.version_id=a.original_version_id OR EXISTS(
 SELECT 1 FROM annotation_history h WHERE h.annotation_id=a.annotation_id AND
 (json_extract(h.value,'$.anchor.version_id')=p.version_id OR json_extract(h.value,'$.original_anchor.version_id')=p.version_id))))),
note_values AS (SELECT value FROM notes UNION SELECT h.value FROM annotation_history h JOIN notes n ON n.annotation_id=h.annotation_id)`

const scopedCopyHashes = selectedCopyNotes + `,
hashes AS (
SELECT e.content_hash AS hash FROM version_entries e JOIN picked p ON p.content_set_id=e.content_set_id WHERE e.content_hash IS NOT NULL
UNION SELECT manifest_sha FROM picked WHERE manifest_sha!=''
UNION SELECT metadata_sha FROM picked WHERE metadata_sha!=''
UNION SELECT json_extract(value,'$.body_object.Hash') FROM note_values
UNION SELECT c.content_hash FROM contexts c JOIN note_values n ON c.context_id=json_extract(n.value,'$.context_ref')
UNION SELECT x.expected_excerpt_ref FROM citation_verifications x JOIN picked p ON p.version_id=x.version_id WHERE x.expected_excerpt_ref IS NOT NULL AND (SELECT json_extract(value,'$.Request.include_retained_sources') FROM choice)=1
UNION SELECT r.content_hash FROM source_capture_reads r JOIN source_captures c ON c.capture_id=r.capture_id JOIN picked p ON p.version_id=json_extract(c.requested_selection,'$.version_id') JOIN object_references own ON own.owner_kind='selected_snapshot' AND own.owner_id=r.read_id AND own.content_hash=r.content_hash WHERE c.state='completed' AND r.retention_mode='selected_snapshot' AND r.availability='available' AND (SELECT json_extract(value,'$.Request.include_retained_sources') FROM choice)=1
) SELECT hash FROM hashes WHERE hash IS NOT NULL AND hash!=''`
