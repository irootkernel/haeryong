package search

import (
	"encoding/json"
	"haeryong.local/haeryong/internal/domain"
	"strings"
	"unicode"
	"unicode/utf8"
)

// FTS nominates chunks; literal/word/phrase authority is the preserved source.
// One copy of each trigram is sufficient for nomination. Keeping every position
// of a repeated trigram made even a one-page FTS merge consume megabytes before
// the pinned engine could yield at the next term boundary.
func nomination(text string) string {
	runes := []rune(text)
	if len(runes) < 3 {
		return domain.SearchNominationPrefix + text
	}
	seen := map[[3]rune]bool{}
	var out strings.Builder
	out.WriteString(domain.SearchNominationPrefix)
	for i := 0; i+2 < len(runes); i++ {
		gram := [3]rune{runes[i], runes[i+1], runes[i+2]}
		if seen[gram] {
			continue
		}
		seen[gram] = true
		if out.Len() > len(domain.SearchNominationPrefix) {
			out.WriteByte(' ')
		}
		for _, r := range gram {
			out.WriteRune(r)
		}
	}
	return out.String()
}

// Full short words preserve useful unicode61 nomination. Longer words remain
// fully searchable through trigram nomination and preserved literals; this is a
// posting-work budget, never an accepted-content or query-length limit.
func wordNomination(text string) string {
	seen := map[string]bool{}
	words := []string{}
	for _, word := range strings.FieldsFunc(text, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) && !unicode.IsMark(r) }) {
		if utf8.RuneCountInString(word) <= 64 && !seen[word] {
			seen[word] = true
			words = append(words, word)
		}
	}
	return strings.Join(words, " ")
}
func metadataPosting(unit int64, ordinal int, literal, display string) domain.SearchChunk {
	encoded, _ := json.Marshal(domain.SearchDisplay{Literal: literal, Display: display})
	return domain.SearchChunk{UnitID: unit, Part: "metadata", Ordinal: ordinal, Body: nomination(literal), Terms: wordNomination(literal), DisplayBody: string(encoded)}
}
