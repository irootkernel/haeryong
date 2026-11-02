package sqlite

import (
	"context"
	"errors"
	"regexp"
	"strings"
)

// Saved Q&A attaches to the existing jobs and immutable source/answer objects.
// It introduces no dispatcher, provider-attempt or operation-receipt ledger.
const questionMigration = `
CREATE TABLE questions(question_id TEXT PRIMARY KEY,owner_id TEXT NOT NULL,project_id TEXT NOT NULL REFERENCES projects(project_id),document_id TEXT NOT NULL REFERENCES documents(document_id),version_id TEXT NOT NULL REFERENCES document_versions(version_id),job_id TEXT NOT NULL UNIQUE REFERENCES jobs(job_id),created_at TEXT NOT NULL,value BLOB NOT NULL CHECK(json_valid(value)));
CREATE INDEX questions_version ON questions(owner_id,version_id,created_at,question_id);
CREATE TABLE answers(answer_id TEXT PRIMARY KEY,question_id TEXT NOT NULL REFERENCES questions(question_id),attempt_id TEXT NOT NULL REFERENCES provider_attempts(attempt_id),state TEXT NOT NULL CHECK(state IN ('final','partial','error')),created_at TEXT NOT NULL,value BLOB NOT NULL CHECK(json_valid(value)),UNIQUE(attempt_id,state));
CREATE INDEX answers_question ON answers(question_id,created_at,answer_id);
`

func (s *Store) validateQuestionDeclarations(ctx context.Context) error {
	normalize := func(v string) string { return strings.TrimSuffix(strings.Join(strings.Fields(v), " "), ";") }
	for _, m := range regexp.MustCompile(`(?s)CREATE (TABLE|INDEX) ([a-z_]+).*?;`).FindAllStringSubmatch(questionMigration, -1) {
		var actual string
		if e := s.db.QueryRowContext(ctx, `SELECT sql FROM sqlite_master WHERE type=? AND name=?`, strings.ToLower(m[1]), m[2]).Scan(&actual); e != nil {
			return errors.New("missing question schema declaration")
		}
		if normalize(actual) != normalize(m[0]) {
			return errors.New("changed question schema declaration")
		}
	}
	return nil
}
