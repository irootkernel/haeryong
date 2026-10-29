package sqlite

import (
	"context"
	"errors"
	"regexp"
	"strings"
)

const sourceCaptureMigration = `
CREATE TABLE source_captures(capture_id TEXT PRIMARY KEY,principal_id TEXT NOT NULL,project_id TEXT NOT NULL REFERENCES projects(project_id),target_set_id TEXT NOT NULL,state TEXT NOT NULL CHECK(state IN ('capturing','completed','failed')),requested_selection BLOB NOT NULL CHECK(json_valid(requested_selection)),config_revision INTEGER NOT NULL,permissions_revision INTEGER NOT NULL,captured_at TEXT NOT NULL,operation_id TEXT NOT NULL,reason TEXT NOT NULL DEFAULT '',UNIQUE(principal_id,operation_id));
CREATE TABLE source_capture_reads(read_id TEXT PRIMARY KEY,capture_id TEXT NOT NULL REFERENCES source_captures(capture_id),source_id TEXT NOT NULL,source_json BLOB NOT NULL CHECK(json_valid(source_json)),location_json BLOB NOT NULL CHECK(json_valid(location_json)),content_hash TEXT NOT NULL,byte_size INTEGER NOT NULL CHECK(byte_size>=0),retention_mode TEXT NOT NULL CHECK(retention_mode IN ('reference_only','selected_snapshot')),availability TEXT NOT NULL CHECK(availability IN ('available','unavailable','denied')),reason TEXT NOT NULL DEFAULT '');
CREATE INDEX source_capture_reads_capture ON source_capture_reads(capture_id,read_id);
CREATE TABLE job_inputs(job_id TEXT NOT NULL REFERENCES jobs(job_id),read_id TEXT NOT NULL REFERENCES captured_reads(read_id),PRIMARY KEY(job_id,read_id));
`

func (s *Store) validateSourceCaptureDeclarations(ctx context.Context) error {
	normalize := func(v string) string { return strings.TrimSuffix(strings.Join(strings.Fields(v), " "), ";") }
	for _, m := range regexp.MustCompile(`(?s)CREATE (TABLE|INDEX) ([a-z_]+).*?;`).FindAllStringSubmatch(sourceCaptureMigration, -1) {
		var actual string
		if e := s.db.QueryRowContext(ctx, `SELECT sql FROM sqlite_master WHERE type=? AND name=?`, strings.ToLower(m[1]), m[2]).Scan(&actual); e != nil {
			return errors.New("missing source capture schema declaration")
		}
		if normalize(actual) != normalize(m[0]) {
			return errors.New("changed source capture schema declaration")
		}
	}
	return nil
}
