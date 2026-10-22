package query

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"golang.org/x/text/unicode/norm"
	"haeryong.local/haeryong/internal/application/search"
	app "haeryong.local/haeryong/internal/application/storage"
	"haeryong.local/haeryong/internal/domain"
	"io"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

var searchID = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

const SearchTimeout = 30 * time.Second

type SearchOptions struct {
	Options
	Text, Versions string
}
type SearchHit struct {
	Kind        string   `json:"kind"`
	ProjectID   string   `json:"project_id"`
	DocumentID  string   `json:"document_id"`
	VersionID   string   `json:"version_id"`
	Sequence    int64    `json:"sequence"`
	Title       string   `json:"title"`
	Lifecycle   string   `json:"lifecycle"`
	PagePath    *string  `json:"page_path"`
	RegionID    *string  `json:"region_id"`
	SourceID    *string  `json:"source_id"`
	AnchorState string   `json:"anchor_state"`
	BodyState   string   `json:"body_state"`
	MatchFields []string `json:"match_fields"`
	Snippet     *string  `json:"snippet"`
}
type IndexReason struct {
	Code         string `json:"code"`
	VersionCount string `json:"version_count"`
}
type SearchCoverage struct {
	State             string        `json:"state"`
	VersionsTotal     string        `json:"versions_total"`
	VersionsComplete  string        `json:"versions_complete"`
	FilesTotal        string        `json:"files_total"`
	FilesDiscovered   string        `json:"files_discovered"`
	HTMLTotal         string        `json:"html_total"`
	HTMLComplete      string        `json:"html_complete"`
	HTMLPending       string        `json:"html_pending"`
	HTMLFailed        string        `json:"html_failed"`
	UnsupportedBodies string        `json:"unsupported_bodies"`
	DiscoveryComplete bool          `json:"discovery_complete"`
	Limitations       []string      `json:"limitations"`
	Reasons           []IndexReason `json:"reasons"`
}
type SearchPage struct {
	Query struct {
		Text       string `json:"text"`
		Normalized string `json:"normalized"`
		Mode       string `json:"mode"`
	} `json:"query"`
	Filters struct {
		ProjectID *string `json:"project_id"`
		Lifecycle string  `json:"lifecycle"`
		Versions  string  `json:"versions"`
		Kind      *string `json:"kind"`
	} `json:"filters"`
	Items        []SearchHit    `json:"items"`
	NextCursor   *string        `json:"next_cursor"`
	TotalMatches string         `json:"total_matches"`
	Index        SearchCoverage `json:"index"`
}

// SearchUnavailable distinguishes search timeout/storage failures from byte-transfer errors.
type SearchUnavailable struct{ Cause error }

func (e *SearchUnavailable) Error() string { return "search unavailable" }
func (e *SearchUnavailable) Unwrap() error { return e.Cause }
func oneOf(v string, allowed ...string) bool {
	for _, a := range allowed {
		if a == v {
			return true
		}
	}
	return false
}

func (s *Service) Search(ctx context.Context, token string, o SearchOptions) (SearchPage, error) {
	out := SearchPage{Items: []SearchHit{}, TotalMatches: "0"}
	q, err := search.ParseQuery(o.Text)
	if err != nil {
		var e *search.QueryError
		errors.As(err, &e)
		return out, &Error{e.Reason}
	}
	if o.Lifecycle == "" {
		o.Lifecycle = "active"
	}
	if o.Versions == "" {
		o.Versions = "latest"
	}
	if o.Limit == 0 {
		o.Limit = 50
	}
	for _, check := range []struct {
		ok     bool
		reason string
	}{
		{oneOf(o.Lifecycle, "active", "archived", "all"), "invalid_lifecycle"}, {oneOf(o.Versions, "latest", "all"), "invalid_versions"},
		{oneOf(o.Kind, "", "document", "page", "region", "source", "attachment"), "invalid_kind"}, {o.Limit >= 1 && o.Limit <= 200, "invalid_page_size"},
		{o.Project == "" || searchID.MatchString(o.Project), "invalid_query"},
	} {
		if !check.ok {
			return out, &Error{check.reason}
		}
	}
	out.Query.Text, out.Query.Normalized = o.Text, q.Normalized
	out.Query.Mode = "literal_and"
	if len(q.Clauses) == 0 {
		out.Query.Mode = "empty"
	}
	out.Filters.ProjectID, out.Filters.Lifecycle, out.Filters.Versions, out.Filters.Kind = nullable(o.Project), o.Lifecycle, o.Versions, nullable(o.Kind)
	ctx, cancel := context.WithTimeout(ctx, SearchTimeout)
	defer cancel()

	for attempt := 0; attempt < 2; attempt++ {
		result, e := s.searchAttempt(ctx, token, o, q, out)
		if errors.Is(e, errSearchChanged) {
			continue
		}
		if ctx.Err() != nil {
			return out, &SearchUnavailable{ctx.Err()}
		}
		return result, e
	}
	return out, &SearchUnavailable{context.DeadlineExceeded}
}

var errSearchChanged = errors.New("visible search state changed")

func (s *Service) searchRead(ctx context.Context, token string, o SearchOptions, fn func(app.WriteTx, domain.Session) error) error {
	return s.Store.Write(ctx, func(tx app.WriteTx) error {
		record, e := s.Auth.AuthenticateRequestWrite(ctx, tx, token, domain.Request{Method: "GET", Target: "/api/v1/search"})
		if e != nil {
			return e
		}
		if e = searchProject(tx, record, o); e != nil {
			return e
		}
		return fn(tx, record)
	})
}
func searchProject(tx app.WriteTx, record domain.Session, o SearchOptions) error {
	if o.Project == "" {
		return nil
	}
	if record.Scope.Kind != "owner" && !oneOf(o.Project, record.Scope.ProjectIDs...) {
		return domain.ErrNotFound
	}
	_, err := tx.Scope(o.Project)
	return err
}
func (s *Service) searchAttempt(ctx context.Context, token string, o SearchOptions, q search.Query, out SearchPage) (SearchPage, error) {
	corpus, binding, highWater, authority, e := s.searchSnapshot(ctx, token, o, q)
	if e != nil {
		return out, e
	}
	out.Index = searchCoverage(corpus)
	after, e := decodeSearchCursor(o.Cursor, binding)
	if e != nil {
		return out, e
	}
	if len(q.Clauses) == 0 {
		return out, nil
	}
	versions := map[string]domain.SearchVersionState{}
	for _, v := range corpus {
		versions[v.ID] = v
	}
	allowedVersions := map[string]bool{}
	for id := range versions {
		allowedVersions[id] = true
	}
	hits := map[int64]*rankedSearch{}
	hitFor := func(c domain.SearchCandidate) *rankedSearch {
		if h := hits[c.Unit.ID]; h != nil {
			return h
		}
		v := versions[c.Unit.VersionID]
		h := &rankedSearch{hit: SearchHit{Kind: c.Unit.Kind, ProjectID: v.ProjectID, DocumentID: v.DocumentID, VersionID: v.ID, Sequence: v.Sequence, Title: c.Unit.Title, Lifecycle: v.Lifecycle, PagePath: nullable(c.Unit.PagePath), RegionID: nullable(c.Unit.RegionID), SourceID: nullable(c.Unit.SourceID), AnchorState: c.Unit.AnchorState, BodyState: c.Unit.BodyState, MatchFields: []string{}}, clauses: make([]bool, len(q.Clauses)), words: make([]bool, len(q.Clauses)), fields: map[string]bool{}, scored: map[string]bool{}, tier: 5}
		hits[c.Unit.ID] = h
		return h
	}
	metadataLabels := map[int64]string{}
	for ci, clause := range q.Clauses {
		consume := func(c domain.SearchCandidate) error {
			if _, ok := versions[c.Unit.VersionID]; !ok {
				return errSearchChanged
			}
			if e := ctx.Err(); e != nil {
				return e
			}
			supplied := ""
			if c.Chunk.Part == "metadata" && c.Unit.Kind == "page" {
				var cached bool
				supplied, cached = metadataLabels[c.Unit.ID]
				if !cached {
					var e error
					supplied, e = search.MetadataText(c.Metadata, false)
					if e != nil {
						return e
					}
					metadataLabels[c.Unit.ID] = supplied
				}
			}
			// Display windows can differ from folded windows. They supply a
			// preview only; they never satisfy a clause or rescue overlap.
			if c.Chunk.Part == "body" && c.Chunk.DisplayBody != "" {
				considerSnippet(hitFor(c), c.Chunk.DisplayBody, clause.Literal, 1)
			}
			fields := candidateFields(c, supplied)
			for _, f := range fields {
				literal := strings.Contains(f.literal, clause.Literal)
				component := false
				if !clause.Phrase {
					if f.name == "body" {
						component = bodyWordContains(f.literal, c.Chunk.DisplayBody, f.aliases, clause.Literal, c.Before, c.After, c.Chunk.Ordinal%2 == 1)
					} else {
						component = wordContains(f.literal, clause.Literal) || wordContains(f.aliases, clause.Literal)
					}
				}
				// Only literal normalized source chunks satisfy quoted phrases; display/aliases never rescue overlap.
				if !literal && !component {
					continue
				}
				h := hitFor(c)
				h.clauses[ci] = true
				h.words[ci] = h.words[ci] || component || clause.Phrase
				h.fields[f.name] = true
				key := strconv.Itoa(ci) + f.name
				if !h.scored[key] {
					h.score += f.weight
					h.scored[key] = true
				}
				if f.literal == q.Normalized && f.exact {
					h.tier = 1
				}
				if strings.Contains(f.literal, q.Normalized) && h.tier > f.phraseTier {
					h.tier = f.phraseTier
				}
				considerSnippet(h, f.display, clause.Literal, f.weight)
			}
			return nil
		}
		for afterID := int64(0); afterID < highWater; {
			var slice domain.SearchSlice
			e := s.searchSliceRead(ctx, token, o, authority, func(tx app.WriteTx) error {
				selection := authority.selection
				selection.VersionSet = allowedVersions
				var e error
				slice, e = tx.SearchCandidates(selection, clause.Literal, afterID, highWater)
				return e
			})
			if e != nil {
				return out, searchFailure(e)
			}
			for _, candidate := range slice.Candidates {
				if e = consume(candidate); e != nil {
					return out, searchFailure(e)
				}
			}
			if slice.After <= afterID {
				break
			}
			afterID = slice.After
		}
	}

	_, current, _, _, e := s.searchSnapshot(ctx, token, o, q)
	if e != nil {
		return out, e
	}
	if current != binding {
		return out, errSearchChanged
	}

	ordered := []*rankedSearch{}
	for _, h := range hits {
		if e := ctx.Err(); e != nil {
			return out, searchFailure(e)
		}
		if !allTrue(h.clauses) {
			continue
		}
		if h.tier > 4 && allTrue(h.words) {
			h.tier = 4
		}
		for _, name := range []string{"title", "path", "region", "topic", "source", "body"} {
			if h.fields[name] {
				h.hit.MatchFields = append(h.hit.MatchFields, name)
			}
		}
		ordered = append(ordered, h)
	}
	if e := orderSearch(ctx, ordered); e != nil {
		return out, searchFailure(e)
	}
	if e := ctx.Err(); e != nil {
		return out, searchFailure(e)
	}
	out.TotalMatches = strconv.Itoa(len(ordered))
	for _, h := range ordered {
		if e := ctx.Err(); e != nil {
			return out, searchFailure(e)
		}
		key := h.key()
		if after != nil && !lessSearch(*after, key) {
			continue
		}
		if len(out.Items) == o.Limit {
			last := orderedKey(out.Items[len(out.Items)-1], ordered)
			out.NextCursor = encodeSearchCursor(binding, last)
			break
		}
		h.hit.Snippet = nullable(snippet(h.snippetText, h.snippetTerm))
		out.Items = append(out.Items, h.hit)
	}
	if e := ctx.Err(); e != nil {
		return out, searchFailure(e)
	}
	return out, nil
}

func allTrue(v []bool) bool {
	for _, x := range v {
		if !x {
			return false
		}
	}
	return true
}

type searchField struct {
	name, literal, aliases, display string
	weight                          int
	exact                           bool
	phraseTier                      int
}

func candidateFields(c domain.SearchCandidate, supplied string) []searchField {
	u, ch := c.Unit, c.Chunk
	if ch.Part == "body" {
		// Authored components are checked directly, without constructing a
		// complete alias list for every display window on every query.
		return []searchField{{name: "body", literal: ch.Body, aliases: ch.Terms, display: ch.DisplayBody, weight: 1, phraseTier: 5}}
	}
	field := func(name, raw string, weight int, exact bool, tier int) searchField {
		return searchField{name: name, literal: search.Normalize(raw), aliases: strings.Join(search.GenerateIdentifierAliases(raw), " "), display: raw, weight: weight, exact: exact, phraseTier: tier}
	}
	path := field("path", u.PagePath, 5, true, 3)
	switch u.Kind {
	case "document":
		return []searchField{field("title", u.Title, 8, true, 2)}
	case "page":
		// The metadata display combines path/title with authored labels/keywords.
		// Those prefixes already have their own fields and must not gain topic weight.
		topic := pageKeywords(c, supplied)
		return []searchField{field("title", u.Title, 8, true, 2), field("title", supplied, 8, true, 2), path, field("topic", topic, 4, false, 3)}
	case "region":
		return []searchField{path, field("region", u.RegionID, 6, true, 5), field("region", u.Title, 6, false, 2)}
	case "source":
		return []searchField{field("source", u.SourceID, 4, true, 3), field("source", metadataRemainder(ch.DisplayBody, u.SourceID), 4, false, 3)}
	case "attachment":
		return []searchField{path}
	}
	return nil
}

// The worker's initial metadata is path + supplied label. Its EOF metadata is
// path + supplied label + title + keywords, windowed in authored scalar offsets.
// Remove field ranges, including prefixes spanning many windows, so a title
// cannot gain topic weight. No label/path length is a product quota.
func pageKeywords(c domain.SearchCandidate, supplied string) string {
	if c.Chunk.Body != "" {
		prefix := search.Normalize(strings.Join([]string{c.Unit.PagePath, supplied, c.Unit.Title}, " "))
		skip := utf8.RuneCountInString(prefix) + 1 - c.Chunk.Ordinal*(search.ChunkSize-search.ChunkOverlap)
		if skip <= 0 {
			return c.Chunk.DisplayBody
		}
		if skip >= utf8.RuneCountInString(c.Chunk.Body) {
			return ""
		}
		raw := []rune(c.Chunk.DisplayBody)
		// Map the normalized field boundary back into this authored window.
		low, high := 0, len(raw)
		for low < high {
			middle := (low + high) / 2
			if utf8.RuneCountInString(search.Normalize(string(raw[:middle]))) >= skip {
				high = middle
			} else {
				low = middle + 1
			}
		}
		return strings.TrimSpace(string(raw[low:]))
	}
	start := c.Chunk.Ordinal * (search.ChunkSize - search.ChunkOverlap)
	initial := []rune(strings.TrimSpace(c.Unit.PagePath + " " + supplied))
	if start < len(initial) {
		end := start + search.ChunkSize
		if end > len(initial) {
			end = len(initial)
		}
		if c.Chunk.DisplayBody == string(initial[start:end]) {
			return ""
		}
	}
	skip := utf8.RuneCountInString(c.Unit.PagePath) + utf8.RuneCountInString(supplied) + utf8.RuneCountInString(c.Unit.Title) + 3 - start
	text := []rune(c.Chunk.DisplayBody)
	if skip >= len(text) {
		return ""
	}
	if skip > 0 {
		text = text[skip:]
	}
	return strings.TrimSpace(string(text))
}

func metadataRemainder(text string, prefixes ...string) string {
	text = strings.TrimSpace(text)
	for _, prefix := range prefixes {
		prefix = strings.TrimSpace(prefix)
		if prefix == "" {
			continue
		}
		if text == prefix {
			text = ""
		} else if strings.HasPrefix(text, prefix+" ") {
			text = strings.TrimSpace(strings.TrimPrefix(text, prefix))
		}
	}
	return text
}

// Literal and authored component checks share the same neighboring-window
// authority. The query needs a boolean, not a materialized list of all aliases.
func bodyWordContains(literal, display, aliases, term, before, after string, authored bool) bool {
	if wordContainsContext(literal, term, before, after) {
		return true
	}
	// Normalized postings keep their existing alias nomination (including the
	// legacy format). Only authored companion windows need direct component
	// discovery; they must use their own authored neighboring scalars.
	return (authored || wordContains(aliases, term)) && completeBodyComponent(display, term, before, after)
}

func wordContains(text, term string) bool { return wordContainsContext(text, term, "", "") }
func lexicalRune(r rune) bool             { return unicode.IsLetter(r) || unicode.IsDigit(r) || unicode.IsMark(r) }
func wordContainsContext(text, term, before, after string) bool {
	if text == term && before == "" && after == "" {
		return true
	}
	joined := before + text + after
	lower, upper := len(before), len(before)+len(text)
	for start := lower; start <= upper; {
		offset := strings.Index(joined[start:], term)
		if offset < 0 {
			return false
		}
		offset += start
		end := offset + len(term)
		if end > upper {
			return false
		}
		left, right := true, true
		if offset > 0 {
			r, _ := utf8.DecodeLastRuneInString(joined[:offset])
			left = !lexicalRune(r)
		}
		if end < len(joined) {
			r, _ := utf8.DecodeRuneInString(joined[end:])
			right = !lexicalRune(r)
		}
		if left && right {
			return true
		}
		start = offset + len(term)
		// A lexical term that is only part of a word cannot become a whole
		// word at another position in that same run. Visit the remaining
		// scalars once instead of retrying the literal at every occurrence.
		lexical := term != ""
		for _, r := range term {
			if !lexicalRune(r) {
				lexical = false
				break
			}
		}
		if lexical {
			for start < upper {
				r, n := utf8.DecodeRuneInString(joined[start:])
				if !lexicalRune(r) {
					break
				}
				start += n
			}
		}
	}
	return false
}

// Context is two authored scalars from neighboring windows of the same unit.
// Splitting with those neighbors preserves camel/acronym boundaries; only whole
// components inside this window count, never a word fragment created by chunking.
func completeBodyComponent(text, term, before, after string) bool {
	joined := []rune(norm.NFC.String(before + text + after))
	lower := utf8.RuneCountInString(norm.NFC.String(before))
	upper := utf8.RuneCountInString(norm.NFC.String(before + text))
	emit := func(start, end int) bool {
		return start >= lower && end <= upper && !(end == len(joined) && after != "") && search.Normalize(string(joined[start:end])) == term
	}
	for i := 0; i < len(joined); {
		if !lexicalRune(joined[i]) {
			i++
			continue
		}
		end := i + 1
		for end < len(joined) && lexicalRune(joined[end]) {
			end++
		}
		start := i
		for j := i + 1; j < end; j++ {
			if (unicode.IsLower(joined[j-1]) && unicode.IsUpper(joined[j])) || (unicode.IsUpper(joined[j-1]) && unicode.IsUpper(joined[j]) && j+1 < end && unicode.IsLower(joined[j+1])) || (unicode.IsDigit(joined[j-1]) != unicode.IsDigit(joined[j])) {
				if emit(start, j) {
					return true
				}
				start = j
			}
		}
		if emit(start, end) {
			return true
		}
		i = end
	}
	return false
}

func considerSnippet(h *rankedSearch, text, term string, weight int) {
	if text == "" || weight < h.snippetWeight || (weight == h.snippetWeight && h.snippetText != "" && text >= h.snippetText) {
		return
	}
	if !strings.Contains(search.Normalize(text), term) {
		return
	}
	h.snippetWeight, h.snippetText, h.snippetTerm = weight, text, term
}
func snippet(text, term string) string {
	if text == "" {
		return ""
	}
	normalized := search.Normalize(text)
	match := strings.Index(normalized, term)
	if match < 0 {
		return ""
	}
	target := utf8.RuneCountInString(normalized[:match])
	raw := []rune(text)
	// Prefix lengths are nondecreasing under NFC/casefold/whitespace collapse.
	// Locate the authored scalar, rather than applying folded offsets to raw text.
	low, high := 0, len(raw)
	for low < high {
		middle := (low + high) / 2
		if utf8.RuneCountInString(search.Normalize(string(raw[:middle]))) > target {
			high = middle
		} else {
			low = middle + 1
		}
	}
	start := low - 1 - 60
	if start < 0 {
		start = 0
	}
	end := start + 240
	if end > len(raw) {
		end = len(raw)
	}
	window := string(raw[start:end])
	prefix := []rune(term)
	if len(prefix) > 32 {
		prefix = prefix[:32]
	}
	if !strings.Contains(search.Normalize(window), string(prefix)) {
		return ""
	}
	return window
}

// Strict cursor decoding rejects unknown fields, trailing JSON and malformed keys.
func decodeSearchCursor(value, binding string) (*searchKey, error) {
	if value == "" {
		return nil, nil
	}
	bad := func() (*searchKey, error) { return nil, &Error{"cursor_mismatch"} }
	raw, e := base64.RawURLEncoding.DecodeString(value)
	if e != nil {
		return bad()
	}
	var c struct {
		Binding string    `json:"binding"`
		After   searchKey `json:"after"`
	}
	d := json.NewDecoder(strings.NewReader(string(raw)))
	d.DisallowUnknownFields()
	if d.Decode(&c) != nil || c.Binding != binding || c.After.Ordinal < 1 || c.After.Tier < 1 || c.After.Tier > 5 || c.After.Score < 0 || c.After.Sequence < 1 || d.Decode(&struct{}{}) != io.EOF {
		return bad()
	}
	return &c.After, nil
}
func encodeSearchCursor(binding string, k searchKey) *string {
	raw, _ := json.Marshal(struct {
		Binding string    `json:"binding"`
		After   searchKey `json:"after"`
	}{binding, k})
	value := base64.RawURLEncoding.EncodeToString(raw)
	return &value
}

type rankedSearch struct {
	hit                                 SearchHit
	clauses, words                      []bool
	fields, scored                      map[string]bool
	tier, score, ordinal, snippetWeight int
	snippetText, snippetTerm            string
}
type searchKey struct {
	Tier, Score, Ordinal       int
	Project, Document          string
	Sequence                   int64
	Kind, Page, Region, Source string
}

func (h *rankedSearch) key() searchKey {
	return searchKey{h.tier, h.score, h.ordinal, h.hit.ProjectID, h.hit.DocumentID, h.hit.Sequence, h.hit.Kind, value(h.hit.PagePath), value(h.hit.RegionID), value(h.hit.SourceID)}
}
func value(v *string) string {
	if v == nil {
		return ""
	}
	return *v
}
func lessSearch(a, b searchKey) bool {
	if a.Tier != b.Tier {
		return a.Tier < b.Tier
	}
	if a.Score != b.Score {
		return a.Score > b.Score
	}
	if a.Ordinal != b.Ordinal {
		return a.Ordinal < b.Ordinal
	}
	if a.Project != b.Project {
		return a.Project < b.Project
	}
	if a.Document != b.Document {
		return a.Document < b.Document
	}
	if a.Sequence != b.Sequence {
		return a.Sequence > b.Sequence
	}
	if a.Kind != b.Kind {
		return a.Kind < b.Kind
	}
	if a.Page != b.Page {
		return a.Page < b.Page
	}
	if a.Region != b.Region {
		return a.Region < b.Region
	}
	return a.Source < b.Source
}
func orderedKey(hit SearchHit, ordered []*rankedSearch) searchKey {
	for _, h := range ordered {
		if h.hit.VersionID == hit.VersionID && h.hit.Kind == hit.Kind && value(h.hit.PagePath) == value(hit.PagePath) && value(h.hit.RegionID) == value(hit.RegionID) && value(h.hit.SourceID) == value(hit.SourceID) {
			return h.key()
		}
	}
	return searchKey{}
}

// orderSearch diversifies only equally strong, fully matched eligible hits.
// It runs before cursor/LIMIT, retaining every unit and the existing tuple ties.
func orderSearch(ctx context.Context, ordered []*rankedSearch) error {
	sort.Slice(ordered, func(i, j int) bool { return lessSearch(ordered[i].key(), ordered[j].key()) })
	type pageGroup struct{ version, path string }
	counts := map[pageGroup]int{}
	tier, score := 0, 0
	for _, hit := range ordered {
		if err := ctx.Err(); err != nil {
			return err
		}
		if hit.tier != tier || hit.score != score {
			counts = map[pageGroup]int{}
			tier, score = hit.tier, hit.score
		}
		group := pageGroup{hit.hit.VersionID, value(hit.hit.PagePath)}
		counts[group]++
		hit.ordinal = counts[group]
	}
	sort.Slice(ordered, func(i, j int) bool { return lessSearch(ordered[i].key(), ordered[j].key()) })
	return ctx.Err()
}
