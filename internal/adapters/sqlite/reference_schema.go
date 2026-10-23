package sqlite

import (
	"context"
	"errors"
	"regexp"
	"strings"
)

// table_list finds missing/extra tables; compare the new table/index declarations
// too, so a partial replacement cannot erase a FK, CHECK or bounded lookup index.
func (s *Store) validateReferenceDeclarations(ctx context.Context) error {
	normalize := func(v string) string { return strings.TrimSuffix(strings.Join(strings.Fields(v), " "), ";") }
	pattern := regexp.MustCompile(`(?s)CREATE (TABLE|INDEX) ([a-z_]+).*?;`)
	for _, match := range pattern.FindAllStringSubmatch(migrations[8], -1) {
		var actual string
		if err := s.db.QueryRowContext(ctx, `SELECT sql FROM sqlite_master WHERE type=? AND name=?`, strings.ToLower(match[1]), match[2]).Scan(&actual); err != nil {
			return errors.New("missing reference schema declaration")
		}
		if normalize(actual) != normalize(match[0]) {
			return errors.New("changed reference schema declaration")
		}
	}
	return nil
}
