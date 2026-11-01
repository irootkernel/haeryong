package sqlite

import (
	"context"
	"errors"
	"regexp"
	"strings"
)

// Generation intent is a local application intent on the existing job. Provider
// attempts, receipts, retries and dispatch continue to use their existing ledger.
const generationMigration = `
CREATE TABLE generation_intents(job_id TEXT PRIMARY KEY REFERENCES jobs(job_id),content_set_id TEXT REFERENCES content_sets(content_set_id),value BLOB NOT NULL CHECK(json_valid(value)));
CREATE INDEX generation_intents_set ON generation_intents(content_set_id,job_id);
CREATE TABLE drafts(draft_id TEXT PRIMARY KEY,project_id TEXT NOT NULL REFERENCES projects(project_id),document_id TEXT REFERENCES documents(document_id),job_id TEXT NOT NULL REFERENCES jobs(job_id),state TEXT NOT NULL CHECK(state IN ('preparing','ready','published','rejected','failed')),revision INTEGER NOT NULL CHECK(revision>=1),content_set_id TEXT REFERENCES content_sets(content_set_id),value BLOB NOT NULL CHECK(json_valid(value)));
CREATE INDEX drafts_project ON drafts(project_id,draft_id);
CREATE INDEX drafts_set ON drafts(content_set_id,draft_id);
CREATE TABLE draft_history(draft_id TEXT NOT NULL REFERENCES drafts(draft_id),revision INTEGER NOT NULL CHECK(revision>=1),content_set_id TEXT REFERENCES content_sets(content_set_id),value BLOB NOT NULL CHECK(json_valid(value)),PRIMARY KEY(draft_id,revision));
CREATE INDEX draft_history_set ON draft_history(content_set_id,draft_id,revision);
CREATE TABLE workflow_selections(binding_id TEXT PRIMARY KEY REFERENCES workflow_principals(binding_id),principal_id TEXT NOT NULL UNIQUE REFERENCES workflow_principals(principal_id),project_id TEXT NOT NULL REFERENCES projects(project_id),document_id TEXT REFERENCES documents(document_id),revision INTEGER NOT NULL CHECK(revision>=1),value BLOB NOT NULL CHECK(json_valid(value)));
`

func (s *Store) validateGenerationDeclarations(ctx context.Context) error {
	normalize := func(v string) string { return strings.TrimSuffix(strings.Join(strings.Fields(v), " "), ";") }
	for _, m := range regexp.MustCompile(`(?s)CREATE (TABLE|INDEX) ([a-z_]+).*?;`).FindAllStringSubmatch(generationMigration, -1) {
		var actual string
		if e := s.db.QueryRowContext(ctx, `SELECT sql FROM sqlite_master WHERE type=? AND name=?`, strings.ToLower(m[1]), m[2]).Scan(&actual); e != nil {
			return errors.New("missing generation schema declaration")
		}
		if normalize(actual) != normalize(m[0]) {
			return errors.New("changed generation schema declaration")
		}
	}
	return nil
}
