package sqlite

import (
	"context"
	"errors"
	"regexp"
	"strings"
)

// External-content FTS uses exactly four shadows on the pinned engine. A broad
// suffix exception would admit unknown ordinary tables and hide damaged indexes.
// Compare declarations as well as table_list types; rank=1 integrity is exercised
// in persistence tests rather than rebuilding indexes on every startup.
func (s *Store) validateSearchDeclarations(ctx context.Context) error {
	normalize := func(v string) string { return strings.TrimSuffix(strings.Join(strings.Fields(v), " "), ";") }
	pattern := regexp.MustCompile(`(?s)CREATE VIRTUAL TABLE (search_(?:words|grams)) .*?;|CREATE TRIGGER (search_chunks_[a-z]+) .*?END;`)
	for _, match := range pattern.FindAllStringSubmatch(migrations[7], -1) {
		name := match[1]
		kind := "table"
		if name == "" {
			name = match[2]
			kind = "trigger"
		}
		var actual string
		if err := s.db.QueryRowContext(ctx, "SELECT sql FROM sqlite_master WHERE name=? AND type=?", name, kind).Scan(&actual); err != nil {
			return errors.New("missing search schema declaration")
		}
		if normalize(actual) != normalize(match[0]) {
			return errors.New("changed search schema declaration")
		}
	}
	// These declarations were read back from the pinned FTS5 engine. table_list
	// alone classifies a forged prefix table as shadow, so verify its exact shape.
	shadows := map[string]string{
		"_data":    "(id INTEGER PRIMARY KEY, block BLOB)",
		"_idx":     "(segid, term, pgno, PRIMARY KEY(segid, term)) WITHOUT ROWID",
		"_docsize": "(id INTEGER PRIMARY KEY, sz BLOB)",
		"_config":  "(k PRIMARY KEY, v) WITHOUT ROWID",
	}
	for _, table := range []string{"search_words", "search_grams"} {
		for suffix, shape := range shadows {
			name := table + suffix
			var actual string
			if err := s.db.QueryRowContext(ctx, "SELECT sql FROM sqlite_master WHERE type='table' AND name=?", name).Scan(&actual); err != nil {
				return errors.New("missing search shadow declaration")
			}
			if normalize(actual) != normalize("CREATE TABLE '"+name+"'"+shape) {
				return errors.New("changed search shadow declaration")
			}
		}
	}

	return nil
}
