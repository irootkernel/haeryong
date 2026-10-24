package sqlite

import (
	"context"
	"errors"
	"regexp"
	"strings"
)

func (s *Store) validateLifecycleDeclarations(ctx context.Context) error {
	normalize := func(v string) string { return strings.TrimSuffix(strings.Join(strings.Fields(v), " "), ";") }
	for _, match := range regexp.MustCompile(`(?s)CREATE (TABLE|INDEX) ([a-z_]+).*?;`).FindAllStringSubmatch(migrations[11], -1) {
		var actual string
		if err := s.db.QueryRowContext(ctx, `SELECT sql FROM sqlite_master WHERE type=? AND name=?`, strings.ToLower(match[1]), match[2]).Scan(&actual); err != nil {
			return errors.New("missing lifecycle schema declaration")
		}
		if normalize(actual) != normalize(match[0]) {
			return errors.New("changed lifecycle schema declaration")
		}
	}
	var declaration string
	if err := s.db.QueryRowContext(ctx, `SELECT sql FROM sqlite_master WHERE name='documents'`).Scan(&declaration); err != nil {
		return err
	}
	// Split only at table-level commas: the CHECK clauses contain both nested
	// parentheses and commas. A substring cannot detect an appended constraint.
	body := declaration[strings.IndexByte(declaration, '(')+1 : strings.LastIndexByte(declaration, ')')]
	columns := map[string]string{}
	depth, start := 0, 0
	quoted := false
	for i, c := range body + "," {
		if c == '\'' {
			quoted = !quoted
		}
		if quoted {
			continue
		}
		switch c {
		case '(':
			depth++
		case ')':
			depth--
		case ',':
			if depth == 0 {
				column := normalize(body[start:i])
				columns[strings.Fields(column)[0]] = column
				start = i + 1
			}
		}
	}
	if len(columns) != 10 { // Seven original columns plus the three revision-12 additions.
		return errors.New("changed lifecycle column declaration")
	}
	for _, column := range []string{"previous_lifecycle TEXT CHECK(previous_lifecycle IS NULL OR previous_lifecycle IN ('active','archived'))", "trashed_at TEXT", "trash_generation INTEGER NOT NULL DEFAULT 0 CHECK(trash_generation>=0)"} {
		if columns[strings.Fields(column)[0]] != column {
			return errors.New("changed lifecycle column declaration")
		}
	}
	return nil
}
