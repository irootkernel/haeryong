package sqlite

// Existing migration bytes are immutable. These records never amend authored rows.
const overlayMigration = `
CREATE TABLE topics(topic_id TEXT PRIMARY KEY,label TEXT NOT NULL UNIQUE);
CREATE TABLE overlay_relations(effect_id TEXT PRIMARY KEY,semantic_key TEXT NOT NULL,version_id TEXT NOT NULL,target_version_id TEXT NOT NULL,decision TEXT NOT NULL CHECK(decision IN ('accepted','rejected','undone')),pinned INTEGER NOT NULL CHECK(pinned IN (0,1)),revision INTEGER NOT NULL CHECK(revision>=1),value BLOB NOT NULL CHECK(json_valid(value)));
CREATE UNIQUE INDEX overlay_relations_active_key ON overlay_relations(semantic_key) WHERE decision='accepted';
CREATE INDEX overlay_relations_version ON overlay_relations(version_id,effect_id);
CREATE TABLE topic_assignments(effect_id TEXT PRIMARY KEY,semantic_key TEXT NOT NULL,version_id TEXT NOT NULL,topic_id TEXT NOT NULL REFERENCES topics(topic_id),decision TEXT NOT NULL CHECK(decision IN ('accepted','rejected','undone')),pinned INTEGER NOT NULL CHECK(pinned IN (0,1)),revision INTEGER NOT NULL CHECK(revision>=1),value BLOB NOT NULL CHECK(json_valid(value)));
CREATE UNIQUE INDEX topic_assignments_active_key ON topic_assignments(semantic_key) WHERE decision='accepted';
CREATE INDEX topic_assignments_version ON topic_assignments(version_id,effect_id);
CREATE TABLE management_effect_history(effect_id TEXT NOT NULL,revision INTEGER NOT NULL,kind TEXT NOT NULL,value BLOB NOT NULL CHECK(json_valid(value)),PRIMARY KEY(effect_id,revision));
CREATE TABLE finding_effects(finding_id TEXT PRIMARY KEY REFERENCES findings(finding_id) ON DELETE CASCADE,effect_id TEXT NOT NULL,kind TEXT NOT NULL,effect_revision INTEGER NOT NULL CHECK(effect_revision>=1));
CREATE INDEX finding_effects_effect ON finding_effects(effect_id,finding_id);
`
