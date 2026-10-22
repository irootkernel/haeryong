// Package search derives static search copies without changing authored identities.
package search

import (
	"golang.org/x/text/cases"
	"golang.org/x/text/unicode/norm"
	"strings"
	"unicode"
)

func Normalize(s string) string {
	return strings.Join(strings.Fields(norm.NFC.String(cases.Fold().String(norm.NFC.String(s)))), " ")
}

// GenerateIdentifierAliases splits before folding so camel/acronym boundaries survive.
// Components aid unquoted matching; they are never substituted for literal phrases.
func GenerateIdentifierAliases(s string) []string {
	var out []string
	seen := map[string]bool{}
	emit := func(s string) {
		s = Normalize(s)
		if s != "" && !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	for _, token := range strings.FieldsFunc(norm.NFC.String(s), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) && !unicode.IsMark(r) }) {
		rs := []rune(token)
		start := 0
		for i := 1; i < len(rs); i++ {
			if (unicode.IsLower(rs[i-1]) && unicode.IsUpper(rs[i])) ||
				(unicode.IsUpper(rs[i-1]) && unicode.IsUpper(rs[i]) && i+1 < len(rs) && unicode.IsLower(rs[i+1])) ||
				(unicode.IsDigit(rs[i-1]) != unicode.IsDigit(rs[i])) {
				emit(string(rs[start:i]))
				start = i
			}
		}
		emit(string(rs[start:]))
	}
	return out
}
