package sqlite

import (
	"context"
	"errors"
	"regexp"
	"strings"
)

func (s *Store) validateProviderDeclarations(ctx context.Context) error {
	normalize := func(v string) string { return strings.TrimSuffix(strings.Join(strings.Fields(v), " "), ";") }
	for _, m := range regexp.MustCompile(`(?s)CREATE (TABLE|INDEX) ([a-z_]+).*?;`).FindAllStringSubmatch(providerMigration, -1) {
		var actual string
		if e := s.db.QueryRowContext(ctx, `SELECT sql FROM sqlite_master WHERE type=? AND name=?`, strings.ToLower(m[1]), m[2]).Scan(&actual); e != nil {
			return errors.New("missing provider schema declaration")
		}
		if normalize(actual) != normalize(m[0]) {
			return errors.New("changed provider schema declaration")
		}
	}
	return nil
}
