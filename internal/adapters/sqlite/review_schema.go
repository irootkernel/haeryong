package sqlite

import (
	"context"
	"errors"
	"regexp"
	"strings"
)

func (s *Store) validateReviewDeclarations(ctx context.Context) error {
	normalize := func(v string) string { return strings.TrimSuffix(strings.Join(strings.Fields(v), " "), ";") }
	for _, match := range regexp.MustCompile(`(?s)CREATE (TABLE|INDEX) ([a-z_]+).*?;`).FindAllStringSubmatch(migrations[9], -1) {
		var actual string
		if err := s.db.QueryRowContext(ctx, `SELECT sql FROM sqlite_master WHERE type=? AND name=?`, strings.ToLower(match[1]), match[2]).Scan(&actual); err != nil {
			return errors.New("missing review schema declaration")
		}
		if normalize(actual) != normalize(match[0]) {
			return errors.New("changed review schema declaration")
		}
	}
	return nil
}
