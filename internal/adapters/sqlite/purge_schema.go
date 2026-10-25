package sqlite

import (
	"context"
	"errors"
	"regexp"
	"strings"
)

func (s *Store) validatePurgeDeclarations(ctx context.Context) error {
	normalize := func(v string) string { return strings.TrimSuffix(strings.Join(strings.Fields(v), " "), ";") }
	for _, m := range regexp.MustCompile(`(?s)CREATE (TABLE|INDEX) ([a-z_]+).*?;`).FindAllStringSubmatch(migrations[12], -1) {
		var actual string
		if err := s.db.QueryRowContext(ctx, `SELECT sql FROM sqlite_master WHERE type=? AND name=?`, strings.ToLower(m[1]), m[2]).Scan(&actual); err != nil {
			return errors.New("missing purge schema declaration")
		}
		if normalize(actual) != normalize(m[0]) {
			return errors.New("changed purge schema declaration")
		}
	}
	return nil
}
