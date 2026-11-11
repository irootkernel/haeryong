package sqlite

// The decision ledger includes Undo; older migration bytes stay immutable.
const decisionMigration = `
CREATE TABLE management_decisions(decision_key TEXT PRIMARY KEY,revision INTEGER NOT NULL CHECK(revision>=1),value BLOB NOT NULL CHECK(json_valid(value)));
CREATE TABLE management_decision_history(decision_key TEXT NOT NULL,revision INTEGER NOT NULL CHECK(revision>=1),value BLOB NOT NULL CHECK(json_valid(value)),PRIMARY KEY(decision_key,revision));
CREATE TABLE finding_decision_bases(finding_id TEXT PRIMARY KEY REFERENCES findings(finding_id) ON DELETE CASCADE,value BLOB NOT NULL CHECK(json_valid(value)));
CREATE TABLE management_link_states(finding_id TEXT PRIMARY KEY REFERENCES findings(finding_id) ON DELETE CASCADE,effect_id TEXT NOT NULL,finding_revision INTEGER NOT NULL CHECK(finding_revision>=1),active INTEGER NOT NULL CHECK(active IN (0,1)));
CREATE INDEX management_link_states_effect ON management_link_states(effect_id,finding_id);
`
