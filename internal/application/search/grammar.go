package search

import (
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

const QueryLimit = 4096

type QueryError struct{ Reason string }

func (e *QueryError) Error() string { return fmt.Sprintf("invalid search query: %s", e.Reason) }

type Clause struct {
	Literal string
	Phrase  bool
}
type Query struct {
	Text, Normalized string
	Clauses          []Clause
}

// ParseQuery admits only literal AND clauses, with doubled quotes inside a phrase.
// This pure parser emits no SQL or FTS expression.
func ParseQuery(text string) (Query, error) {
	q := Query{Text: text, Clauses: []Clause{}}
	bad := func(reason string) (Query, error) { return Query{}, &QueryError{reason} }
	if !utf8.ValidString(text) {
		return bad("invalid_query")
	}
	if utf8.RuneCountInString(text) > QueryLimit {
		return bad("query_too_long")
	}
	for _, r := range text {
		if unicode.IsControl(r) && r != '\t' && r != '\n' && r != '\r' {
			return bad("invalid_query")
		}
	}
	rs := []rune(text)
	for i := 0; i < len(rs); {
		if unicode.IsSpace(rs[i]) {
			i++
			continue
		}
		phrase := rs[i] == '"'
		var b strings.Builder
		if phrase {
			i++
			closed := false
			for i < len(rs) {
				if rs[i] == '"' {
					if i+1 < len(rs) && rs[i+1] == '"' {
						b.WriteRune('"')
						i += 2
						continue
					}
					i++
					closed = true
					break
				}
				b.WriteRune(rs[i])
				i++
			}
			if !closed || (i < len(rs) && !unicode.IsSpace(rs[i])) {
				return bad("malformed_phrase")
			}
		} else {
			for i < len(rs) && !unicode.IsSpace(rs[i]) {
				if rs[i] == '"' {
					return bad("malformed_phrase")
				}
				b.WriteRune(rs[i])
				i++
			}
		}
		literal := Normalize(b.String())
		if literal != "" {
			q.Clauses = append(q.Clauses, Clause{literal, phrase})
		}
	}
	texts := make([]string, len(q.Clauses))
	for i, c := range q.Clauses {
		texts[i] = c.Literal
	}
	q.Normalized = strings.Join(texts, " ")
	if utf8.RuneCountInString(q.Normalized) > QueryLimit {
		return bad("query_too_long")
	}
	return q, nil
}
