package sqlite

import (
	"context"
	"errors"
	"regexp"
	"strings"
)

const scanMigration = `
CREATE TABLE scan_runs(job_id TEXT PRIMARY KEY REFERENCES jobs(job_id) ON DELETE CASCADE,principal_id TEXT NOT NULL,auth_kind TEXT NOT NULL CHECK(auth_kind IN ('owner','workflow')),scope_hash TEXT NOT NULL,target_set_id TEXT,requested_scope BLOB NOT NULL CHECK(json_valid(requested_scope)),effective_policy BLOB NOT NULL CHECK(json_valid(effective_policy)),config_revision INTEGER NOT NULL CHECK(config_revision>=1),permissions_revisions BLOB NOT NULL CHECK(json_valid(permissions_revisions)),stage TEXT NOT NULL CHECK(stage IN ('inventory','inspect','repair','complete')),enum_version_ordinal INTEGER NOT NULL DEFAULT 0 CHECK(enum_version_ordinal>=0),enum_after_path TEXT NOT NULL DEFAULT '',enum_after_region TEXT NOT NULL DEFAULT '',enumeration_complete INTEGER NOT NULL DEFAULT 0 CHECK(enumeration_complete IN (0,1)),next_item_ordinal INTEGER NOT NULL DEFAULT 1 CHECK(next_item_ordinal>=1),denominator BLOB NOT NULL CHECK(json_valid(denominator)),report BLOB NOT NULL CHECK(json_valid(report)),captured_at TEXT NOT NULL,started_at TEXT,elapsed_ms INTEGER NOT NULL DEFAULT 0 CHECK(elapsed_ms>=0),updated_at TEXT NOT NULL);
CREATE INDEX scan_runs_by_scope_hash ON scan_runs(scope_hash,job_id);
CREATE TABLE scan_captured_versions(job_id TEXT NOT NULL REFERENCES jobs(job_id) ON DELETE CASCADE,version_ordinal INTEGER NOT NULL CHECK(version_ordinal>=1),project_id TEXT NOT NULL,document_id TEXT NOT NULL,version_id TEXT NOT NULL DEFAULT '',content_set_id TEXT NOT NULL DEFAULT '',manifest_sha256 TEXT NOT NULL DEFAULT '',metadata_sha256 TEXT NOT NULL DEFAULT '',source_basis TEXT NOT NULL DEFAULT '',source_intent TEXT CHECK(source_intent IS NULL OR source_intent IN ('moving_target','historical')),selected_mode TEXT NOT NULL DEFAULT 'pinned' CHECK(selected_mode IN ('pinned','current')),selection_source TEXT NOT NULL DEFAULT 'default_pinned' CHECK(selection_source IN ('target_set','request_override','historical_intent','project_default','default_pinned')),target_set_id TEXT NOT NULL DEFAULT '',requested_selection BLOB NOT NULL DEFAULT '{}' CHECK(json_valid(requested_selection)),targets_json BLOB NOT NULL DEFAULT '[]' CHECK(json_valid(targets_json)),exclusion_reason TEXT NOT NULL DEFAULT '',page_count INTEGER NOT NULL DEFAULT 0 CHECK(page_count>=0),region_count INTEGER NOT NULL DEFAULT 0 CHECK(region_count>=0),entry_count INTEGER NOT NULL DEFAULT 0 CHECK(entry_count>=0),source_count INTEGER NOT NULL DEFAULT 0 CHECK(source_count>=0),unit_count INTEGER NOT NULL DEFAULT 0 CHECK(unit_count>=0),enum_state TEXT NOT NULL DEFAULT 'pending' CHECK(enum_state IN ('pending','pages_done','regions_done','entries_done','complete','purged')),enum_after_path TEXT NOT NULL DEFAULT '',enum_after_region TEXT NOT NULL DEFAULT '',entry_after_path TEXT NOT NULL DEFAULT '',source_after_idx INTEGER NOT NULL DEFAULT -1,materialized_units INTEGER NOT NULL DEFAULT 0 CHECK(materialized_units>=0),entries_checked INTEGER NOT NULL DEFAULT 0 CHECK(entries_checked>=0),captured_at TEXT NOT NULL,PRIMARY KEY(job_id,version_ordinal));
CREATE INDEX scan_captured_versions_by_job_doc ON scan_captured_versions(job_id,document_id,version_id);
CREATE TABLE job_items(job_id TEXT NOT NULL REFERENCES jobs(job_id) ON DELETE CASCADE,item_id TEXT NOT NULL,ordinal INTEGER NOT NULL CHECK(ordinal>=1),unit_kind TEXT NOT NULL CHECK(unit_kind IN ('document','version','page','region','entry','source','index')),project_id TEXT NOT NULL,document_id TEXT NOT NULL DEFAULT '',version_id TEXT NOT NULL DEFAULT '',page_path TEXT NOT NULL DEFAULT '',region_id TEXT NOT NULL DEFAULT '',source_id TEXT NOT NULL DEFAULT '',target_set_id TEXT NOT NULL DEFAULT '',stage TEXT NOT NULL CHECK(stage IN ('inventory','inspect','repair','complete')),state TEXT NOT NULL CHECK(state IN ('pending','running','completed','skipped','blocked','failed','cancelled')),attempt_generation INTEGER NOT NULL DEFAULT 0 CHECK(attempt_generation>=0),reason TEXT NOT NULL DEFAULT '',detail BLOB NOT NULL DEFAULT '{}' CHECK(json_valid(detail)),finding_id TEXT NOT NULL DEFAULT '',repair_job_id TEXT NOT NULL DEFAULT '',updated_at TEXT NOT NULL,PRIMARY KEY(job_id,item_id),UNIQUE(job_id,ordinal));
CREATE INDEX job_items_by_job_state_ordinal ON job_items(job_id,state,ordinal);
CREATE INDEX job_items_by_document ON job_items(document_id,job_id);
CREATE INDEX job_items_by_version ON job_items(version_id,job_id);
CREATE INDEX job_items_by_repair_job ON job_items(repair_job_id) WHERE repair_job_id!='';
CREATE TABLE findings(finding_id TEXT PRIMARY KEY,job_id TEXT NOT NULL REFERENCES jobs(job_id) ON DELETE CASCADE,project_id TEXT NOT NULL REFERENCES projects(project_id),document_id TEXT,version_id TEXT,page_path TEXT,region_id TEXT,kind TEXT NOT NULL CHECK(kind IN ('backlink_index_repair','related_explanation','topic_assignment','freshness_assessment','new_version_proposal','new_document_proposal')),origin TEXT NOT NULL CHECK(origin IN ('deterministic','ai')),target_set_id TEXT,evidence_json BLOB NOT NULL DEFAULT '[]' CHECK(json_valid(evidence_json)),evidence_fingerprint TEXT NOT NULL,rationale TEXT NOT NULL DEFAULT '',uncertainty TEXT NOT NULL DEFAULT '',proposal_json BLOB NOT NULL DEFAULT '{}' CHECK(json_valid(proposal_json)),status TEXT NOT NULL CHECK(status IN ('proposed','applied','rejected','stale','blocked','undone')),approval_required INTEGER NOT NULL CHECK(approval_required IN (0,1)),reason TEXT NOT NULL DEFAULT '',config_revision INTEGER NOT NULL CHECK(config_revision>=1),permissions_revision INTEGER NOT NULL CHECK(permissions_revision>=1),applied_ref_json BLOB CHECK(applied_ref_json IS NULL OR json_valid(applied_ref_json)),decided_by TEXT,decided_at TEXT,revision INTEGER NOT NULL DEFAULT 1 CHECK(revision>=1),created_at TEXT NOT NULL,updated_at TEXT NOT NULL,CHECK(region_id IS NULL OR page_path IS NOT NULL),CHECK(kind='new_document_proposal' OR (document_id IS NOT NULL AND document_id!='' AND version_id IS NOT NULL AND version_id!='')));
CREATE INDEX findings_by_job_order ON findings(job_id,created_at,finding_id);
CREATE INDEX findings_by_version_status ON findings(version_id,status,finding_id);
CREATE INDEX findings_by_project_status ON findings(project_id,status,created_at,finding_id);
CREATE INDEX findings_by_fingerprint ON findings(project_id,kind,evidence_fingerprint);
`

const maintenanceMigration = `
CREATE TABLE maintenance_intents(job_id TEXT PRIMARY KEY REFERENCES jobs(job_id),scan_job_id TEXT NOT NULL REFERENCES jobs(job_id),item_id TEXT NOT NULL,value BLOB NOT NULL CHECK(json_valid(value)));
CREATE INDEX maintenance_intents_by_scan_item ON maintenance_intents(scan_job_id,item_id);
`

func (s *Store) validateScanDeclarations(ctx context.Context) error {
	return s.validateDDL(ctx, scanMigration, "scan")
}

func (s *Store) validateMaintenanceDeclarations(ctx context.Context) error {
	return s.validateDDL(ctx, maintenanceMigration, "maintenance")
}

func (s *Store) validateDDL(ctx context.Context, ddl, label string) error {
	normalize := func(v string) string { return strings.TrimSuffix(strings.Join(strings.Fields(v), " "), ";") }
	for _, m := range regexp.MustCompile(`(?s)CREATE (TABLE|INDEX) ([a-z_]+).*?;`).FindAllStringSubmatch(ddl, -1) {
		var actual string
		if e := s.db.QueryRowContext(ctx, `SELECT sql FROM sqlite_master WHERE type=? AND name=?`, strings.ToLower(m[1]), m[2]).Scan(&actual); e != nil {
			return errors.New("missing " + label + " schema declaration")
		}
		if normalize(actual) != normalize(m[0]) {
			return errors.New("changed " + label + " schema declaration")
		}
	}
	return nil
}
