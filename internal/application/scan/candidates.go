package scan

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

	"haeryong.local/haeryong/internal/application/auth"
	"haeryong.local/haeryong/internal/application/service"
	"haeryong.local/haeryong/internal/application/source"
	app "haeryong.local/haeryong/internal/application/storage"
	"haeryong.local/haeryong/internal/domain"
)

const maxShortlistCandidates = 6

var ErrMaintenanceInvalid = errors.New("invalid maintenance output")

func projectHasProviderID(cfgJSON []byte, projectID, wantProvider string) bool {
	if wantProvider == "" {
		return true
	}
	var parsed struct {
		Projects map[string]struct {
			ProviderID *string `json:"provider_id"`
		} `json:"projects"`
	}
	if json.Unmarshal(cfgJSON, &parsed) != nil {
		return false
	}
	p, ok := parsed.Projects[projectID]
	return ok && p.ProviderID != nil && *p.ProviderID == wantProvider
}

// checkCandidateTargetFence enforces the 4-boundary target visibility and authority
// fence (T-008-02-02) across shortlist selection, byte reads, child dispatch, and Apply.
func (w *Worker) checkCandidateTargetFence(
	tx app.WriteTx,
	run domain.ScanRun,
	targetVersionID string,
	providerID string,
) (domain.ScanCapturedVersion, domain.Document, domain.Version, bool) {
	if targetVersionID == "" {
		return domain.ScanCapturedVersion{}, domain.Document{}, domain.Version{}, false
	}
	cv, err := tx.ScanCapturedVersionByID(run.JobID, targetVersionID)
	if err != nil || cv.ExclusionReason != "" || cv.EnumState == "purged" {
		return domain.ScanCapturedVersion{}, domain.Document{}, domain.Version{}, false
	}
	// Exact owner decision keys are fenced once the candidate is known in
	// saveDecisionFinding. Unrelated owner choices do not invalidate this version.
	if tomb, err := tx.TombstonedVersion(targetVersionID); err != nil || tomb {
		return domain.ScanCapturedVersion{}, domain.Document{}, domain.Version{}, false
	}
	ver, err := tx.PublishedVersion(targetVersionID)
	if err != nil {
		return domain.ScanCapturedVersion{}, domain.Document{}, domain.Version{}, false
	}
	doc, err := tx.Document(ver.DocumentID)
	if err != nil || (doc.Lifecycle != "active" && doc.Lifecycle != "archived") {
		return domain.ScanCapturedVersion{}, domain.Document{}, domain.Version{}, false
	}
	if cv.DocumentID != "" && doc.ID != cv.DocumentID {
		return domain.ScanCapturedVersion{}, domain.Document{}, domain.Version{}, false
	}
	pScope, err := tx.Scope(doc.ProjectID)
	if err != nil || (pScope.Project.Lifecycle != "active" && pScope.Project.Lifecycle != "archived") {
		return domain.ScanCapturedVersion{}, domain.Document{}, domain.Version{}, false
	}
	if capturedPerm, ok := run.PermissionsRevisions[doc.ProjectID]; !ok || capturedPerm != pScope.Project.PermissionsRevision {
		return domain.ScanCapturedVersion{}, domain.Document{}, domain.Version{}, false
	}
	cfg, err := tx.Configuration()
	if err != nil {
		return domain.ScanCapturedVersion{}, domain.Document{}, domain.Version{}, false
	}
	if providerID != "" && !projectHasProviderID(cfg.Value, doc.ProjectID, providerID) {
		return domain.ScanCapturedVersion{}, domain.Document{}, domain.Version{}, false
	}
	authState, err := tx.AuthState()
	if err != nil {
		return domain.ScanCapturedVersion{}, domain.Document{}, domain.Version{}, false
	}
	if run.AuthKind == "owner" {
		if authState.OwnerID == "" || run.PrincipalID != authState.OwnerID {
			return domain.ScanCapturedVersion{}, domain.Document{}, domain.Version{}, false
		}
	} else {
		if !projectPostWorkScanEnabled(cfg.Value, doc.ProjectID) {
			return domain.ScanCapturedVersion{}, domain.Document{}, domain.Version{}, false
		}
		wp, err := tx.WorkflowPrincipal(run.PrincipalID)
		if err != nil || !wp.Active {
			return domain.ScanCapturedVersion{}, domain.Document{}, domain.Version{}, false
		}
		effScope := auth.RestrictScope(run.CapturedScope, wp.Scope)
		if !containsScanAction(effScope.Actions, "scan.request") || !containsScanAction(effScope.Actions, "version.read") || !containsScanAction(effScope.ProjectIDs, doc.ProjectID) {
			return domain.ScanCapturedVersion{}, domain.Document{}, domain.Version{}, false
		}
		actor := domain.Session{
			PrincipalID: run.PrincipalID,
			AuthEpoch:   authState.AuthEpoch,
			Scope:       effScope,
		}
		if _, _, err := app.CheckSourceVersion(tx, actor, targetVersionID); err != nil {
			return domain.ScanCapturedVersion{}, domain.Document{}, domain.Version{}, false
		}
	}
	return cv, doc, ver, true
}

type unitSignals struct {
	ProjectID     string
	DocumentID    string
	VersionID     string
	PagePath      string
	RegionID      string
	Title         string
	Headings      []string
	Excerpt       string
	HeadingTerms  map[string]bool
	HeadingStems  map[string]bool
	BodyTerms     map[string]bool
	BodyStems     map[string]bool
	DeclaredTerms map[string]bool
	DeclaredStems map[string]bool
	Themes        []string
	SourcePaths   map[string]bool
	SourceRepos   map[string]bool
	OutgoingRefs  []domain.AuthoredReference
}

var stopWords = map[string]bool{
	"the": true, "and": true, "for": true, "with": true, "that": true, "this": true,
	"from": true, "into": true, "over": true, "under": true, "when": true, "where": true,
	"which": true, "while": true, "have": true, "has": true, "had": true, "not": true,
	"are": true, "was": true, "were": true, "been": true, "being": true, "only": true,
	"also": true, "than": true, "then": true, "each": true, "both": true, "some": true,
	"such": true, "more": true, "most": true, "other": true, "page": true, "region": true,
	"section": true, "html": true, "body": true, "div": true, "span": true,
}

func canonicalStem(tok string) string {
	s := strings.ToLower(strings.TrimSpace(tok))
	if len(s) <= 4 {
		return s
	}
	for _, suf := range []string{
		"iations", "iation", "ications", "ication", "izations", "ization",
		"ments", "ment", "nesses", "ness", "encies", "ency", "ancies", "ancy",
		"ities", "ity", "ances", "ance", "ences", "ence", "ings", "ing",
		"ional", "ions", "ion", "izing", "izes", "ized", "ize",
		"ently", "antly", "ents", "ent", "ants", "ant",
		"able", "ible", "ies", "ed", "er", "es", "ly", "s",
	} {
		if strings.HasSuffix(s, suf) && len(s)-len(suf) >= 4 {
			s = s[:len(s)-len(suf)]
			break
		}
	}
	if len(s) > 4 && strings.HasSuffix(s, "e") {
		s = s[:len(s)-1]
	}
	if len(s) >= 6 && strings.HasSuffix(s, "y") {
		s = s[:len(s)-1]
	}
	if len(s) > 5 {
		last := s[len(s)-1]
		prev := s[len(s)-2]
		if last == prev && last != 's' && last != 'l' && last != 'z' {
			s = s[:len(s)-1]
		}
	}
	return s
}

func tokenizeWithSubwords(text string) []string {
	base := tokenizeNormalized(text)
	seen := make(map[string]bool, len(base))
	out := make([]string, 0, len(base))
	for _, tok := range base {
		if !seen[tok] {
			seen[tok] = true
			out = append(out, tok)
		}
	}
	runes := []rune(text)
	var cur strings.Builder
	flushSub := func() {
		if cur.Len() > 0 {
			tok := strings.ToLower(cur.String())
			cur.Reset()
			if len(tok) >= 3 && !stopWords[tok] && !seen[tok] {
				seen[tok] = true
				out = append(out, tok)
			}
		}
	}
	for i, r := range runes {
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) {
			flushSub()
			continue
		}
		if i > 0 && cur.Len() > 0 {
			prev := runes[i-1]
			nextIsLower := i+1 < len(runes) && unicode.IsLower(runes[i+1])
			if (unicode.IsLower(prev) && unicode.IsUpper(r)) ||
				(unicode.IsUpper(prev) && unicode.IsUpper(r) && nextIsLower) ||
				(unicode.IsLetter(prev) != unicode.IsLetter(r)) {
				flushSub()
			}
		}
		cur.WriteRune(r)
	}
	flushSub()
	return out
}

func normalizeTopicLabel(raw string) string {
	toks := tokenizeNormalized(raw)
	if len(toks) == 0 {
		return ""
	}
	var dedup []string
	seen := map[string]bool{}
	for _, t := range toks {
		st := canonicalStem(t)
		if !seen[st] {
			seen[st] = true
			dedup = append(dedup, t)
		}
	}
	return strings.Join(dedup, "-")
}

func tokenizeNormalized(text string) []string {
	var out []string
	var cur strings.Builder
	flush := func() {
		if cur.Len() > 0 {
			tok := strings.ToLower(cur.String())
			cur.Reset()
			if len(tok) >= 3 && !stopWords[tok] {
				out = append(out, tok)
			}
		}
	}
	for _, r := range text {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			cur.WriteRune(r)
		} else {
			flush()
		}
	}
	flush()
	return out
}

func extractMetadataStrings(raw []byte) (title string, topics []string) {
	if len(raw) == 0 {
		return "", nil
	}
	var m map[string]any
	if json.Unmarshal(raw, &m) != nil {
		return "", nil
	}
	for _, k := range []string{"title", "label", "heading", "name"} {
		if s, ok := m[k].(string); ok && strings.TrimSpace(s) != "" {
			if title == "" {
				title = strings.TrimSpace(s)
			}
			topics = append(topics, strings.TrimSpace(s))
		}
	}
	for _, k := range []string{"topic", "category", "domain", "theme", "subsystem"} {
		if s, ok := m[k].(string); ok && strings.TrimSpace(s) != "" {
			topics = append(topics, strings.TrimSpace(s))
		}
	}
	for _, k := range []string{"topics", "tags", "keywords", "themes"} {
		switch arr := m[k].(type) {
		case []any:
			for _, item := range arr {
				if s, ok := item.(string); ok && strings.TrimSpace(s) != "" {
					topics = append(topics, strings.TrimSpace(s))
				}
			}
		case []string:
			for _, s := range arr {
				if strings.TrimSpace(s) != "" {
					topics = append(topics, strings.TrimSpace(s))
				}
			}
		}
	}
	return title, topics
}

// extractScopeThemes deterministically extracts candidate themes using only actual
// SQLite columns: version_pages.metadata, regions.metadata, search_units/search_chunks,
// authored_references, scan_captured_versions.targets_json, version_sources, and evidence.
func extractScopeThemes(tx app.WriteTx, cv domain.ScanCapturedVersion, pagePath, regionID string) (unitSignals, error) {
	sig := unitSignals{
		ProjectID:     cv.ProjectID,
		DocumentID:    cv.DocumentID,
		VersionID:     cv.VersionID,
		PagePath:      pagePath,
		RegionID:      regionID,
		HeadingTerms:  map[string]bool{},
		HeadingStems:  map[string]bool{},
		BodyTerms:     map[string]bool{},
		BodyStems:     map[string]bool{},
		DeclaredTerms: map[string]bool{},
		DeclaredStems: map[string]bool{},
		SourcePaths:   map[string]bool{},
		SourceRepos:   map[string]bool{},
	}
	seenThemes := map[string]bool{}
	seenHeadings := map[string]bool{}
	addHeading := func(raw string) {
		h := strings.TrimSpace(raw)
		if h == "" || seenHeadings[h] {
			return
		}
		seenHeadings[h] = true
		sig.Headings = append(sig.Headings, h)
		for _, tok := range tokenizeWithSubwords(h) {
			sig.HeadingTerms[tok] = true
			sig.HeadingStems[canonicalStem(tok)] = true
		}
	}
	addTheme := func(raw string) {
		clean := strings.ToLower(strings.TrimSpace(raw))
		if len(clean) < 3 || stopWords[clean] || seenThemes[clean] {
			return
		}
		seenThemes[clean] = true
		sig.Themes = append(sig.Themes, clean)
		for _, tok := range tokenizeWithSubwords(clean) {
			sig.DeclaredTerms[tok] = true
			sig.DeclaredStems[canonicalStem(tok)] = true
		}
	}

	if pagePath != "" && regionID == "" {
		if pg, err := tx.Page(cv.VersionID, pagePath); err == nil {
			t, topics := extractMetadataStrings(pg.Metadata)
			if sig.Title == "" && t != "" {
				sig.Title = t
			}
			addHeading(t)
			for _, tp := range topics {
				addTheme(tp)
			}
		}
	}
	if pagePath != "" && regionID != "" {
		if reg, err := tx.VersionRegion(cv.VersionID, pagePath, regionID); err == nil {
			t, topics := extractMetadataStrings(reg.Metadata)
			if t != "" {
				sig.Title = t
			}
			addHeading(t)
			for _, tp := range topics {
				addTheme(tp)
			}
		}
	}
	if unitTitle, headings, termsSlice, bodyStr, err := tx.ScanSearchText(cv.VersionID, pagePath, regionID); err == nil {
		if sig.Title == "" && strings.TrimSpace(unitTitle) != "" {
			sig.Title = strings.TrimSpace(unitTitle)
		}
		if strings.TrimSpace(unitTitle) != "" {
			addTheme(unitTitle)
			addHeading(unitTitle)
		}
		for _, h := range headings {
			if strings.TrimSpace(h) != "" {
				addTheme(h)
				addHeading(h)
			}
		}
		for _, tok := range tokenizeWithSubwords(strings.Join(termsSlice, " ") + " " + bodyStr) {
			sig.BodyTerms[tok] = true
			sig.BodyStems[canonicalStem(tok)] = true
		}
		cleanBody := strings.TrimSpace(bodyStr)
		if len(cleanBody) > 1600 {
			cleanBody = cleanBody[:1600]
		}
		sig.Excerpt = cleanBody
	}
	if sig.Title == "" {
		if v, err := tx.Version(cv.VersionID); err == nil && strings.TrimSpace(v.Title) != "" {
			sig.Title = strings.TrimSpace(v.Title)
			if pagePath == "" && regionID == "" {
				addHeading(v.Title)
			}
		}
	}

	// Authored references on this version/page/region.
	afterRef := ""
	for {
		refs, err := tx.AuthoredReferences(cv.VersionID, afterRef, 64)
		if err != nil || len(refs) == 0 {
			break
		}
		for _, r := range refs {
			afterRef = r.ID
			if pagePath != "" && r.FromPagePath != nil && *r.FromPagePath != pagePath {
				continue
			}
			if regionID != "" && r.FromRegionID != nil && *r.FromRegionID != regionID {
				continue
			}
			sig.OutgoingRefs = append(sig.OutgoingRefs, r)
			if r.Label != nil && strings.TrimSpace(*r.Label) != "" {
				addTheme(*r.Label)
			}
		}
		if len(refs) < 64 {
			break
		}
	}

	// Source targets and evidence metadata for this unit.
	for _, st := range cv.Targets {
		if st.SourceID != "" {
			sig.SourceRepos[st.SourceID] = true
		}
	}
	if sources, err := tx.VersionSources(cv.VersionID); err == nil {
		for _, src := range sources {
			var meta map[string]any
			if json.Unmarshal(src.Metadata, &meta) == nil {
				if repoID, ok := meta["repo_id"].(string); ok && repoID != "" {
					sig.SourceRepos[repoID] = true
				}
				if p, ok := meta["path"].(string); ok && p != "" {
					sig.SourcePaths[strings.TrimPrefix(path.Clean(p), "/")] = true
				}
			}
		}
	}
	extractEvMeta := func(ev domain.Evidence) {
		var evMeta struct {
			Locator struct {
				Path    string   `json:"path"`
				Symbols []string `json:"symbols"`
			} `json:"locator"`
			Path    string   `json:"path"`
			Symbols []string `json:"symbols"`
		}
		if json.Unmarshal(ev.Metadata, &evMeta) == nil {
			p := evMeta.Locator.Path
			if p == "" {
				p = evMeta.Path
			}
			if p != "" {
				cleanPath := strings.TrimPrefix(path.Clean(p), "/")
				sig.SourcePaths[cleanPath] = true
				base := strings.TrimSuffix(path.Base(cleanPath), path.Ext(cleanPath))
				if base != "" {
					addTheme(base)
				}
			}
			for _, sym := range append(evMeta.Locator.Symbols, evMeta.Symbols...) {
				if strings.TrimSpace(sym) != "" {
					addTheme(sym)
				}
			}
		}
	}
	linkedEvIDs := map[string]bool{}
	if links, err := tx.VersionRegionEvidence(cv.VersionID); err == nil {
		for _, link := range links {
			linkedEvIDs[link.EvidenceID] = true
			if pagePath != "" && link.PagePath != pagePath {
				continue
			}
			if regionID != "" && link.RegionID != regionID {
				continue
			}
			if ev, err := tx.QueryEvidence(cv.VersionID, link.EvidenceID); err == nil {
				extractEvMeta(ev)
			}
		}
	}
	if regionID == "" {
		if evs, err := tx.VersionEvidence(cv.VersionID); err == nil {
			for _, ev := range evs {
				if !linkedEvIDs[ev.ID] {
					extractEvMeta(ev)
				}
			}
		}
	}

	if len(sig.Themes) == 0 {
		var bodyList []string
		for tok := range sig.HeadingTerms {
			bodyList = append(bodyList, tok)
		}
		if len(bodyList) == 0 {
			for tok := range sig.BodyTerms {
				bodyList = append(bodyList, tok)
			}
		}
		sort.Strings(bodyList)
		for i := 0; i < len(bodyList) && i < 4; i++ {
			addTheme(bodyList[i])
		}
	}
	sort.Strings(sig.Themes)
	return sig, nil
}

func overlapRatio(a, b map[string]bool) (float64, []string) {
	if len(a) == 0 || len(b) == 0 {
		return 0, nil
	}
	var shared []string
	for k := range a {
		if b[k] {
			shared = append(shared, k)
		}
	}
	if len(shared) == 0 {
		return 0, nil
	}
	sort.Strings(shared)
	denom := len(a)
	if len(b) < denom {
		denom = len(b)
	}
	if denom <= 0 {
		denom = 1
	}
	return float64(len(shared)) / float64(denom), shared
}

type focalStemGroup struct {
	stemKey string
	rep     string
	tokens  []string
}

func buildCanonicalStemGroups(a map[string]bool) []focalStemGroup {
	if len(a) == 0 {
		return nil
	}
	keys := make([]string, 0, len(a))
	for k := range a {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	byStem := map[string][]string{}
	var stemOrder []string
	for _, k := range keys {
		st := canonicalStem(k)
		stKey := k
		if len(st) >= 4 {
			stKey = st
		}
		if _, exists := byStem[stKey]; !exists {
			stemOrder = append(stemOrder, stKey)
		}
		byStem[stKey] = append(byStem[stKey], k)
	}
	sort.Strings(stemOrder)
	groups := make([]focalStemGroup, 0, len(stemOrder))
	for _, stKey := range stemOrder {
		toks := byStem[stKey]
		groups = append(groups, focalStemGroup{
			stemKey: stKey,
			rep:     toks[0],
			tokens:  toks,
		})
	}
	return groups
}

func countUniqueCandidateStems(b, bStems map[string]bool) int {
	seen := map[string]bool{}
	for k := range b {
		st := canonicalStem(k)
		if len(st) >= 4 {
			seen[st] = true
		} else {
			seen[k] = true
		}
	}
	for st := range bStems {
		if st != "" {
			seen[st] = true
		}
	}
	return len(seen)
}

func overlapRatioWithStems(a, b, bStems map[string]bool) (float64, []string) {
	if len(a) == 0 || len(b) == 0 {
		return 0, nil
	}
	groups := buildCanonicalStemGroups(a)
	var shared []string
	for _, g := range groups {
		matched := len(g.stemKey) >= 4 && bStems[g.stemKey]
		if !matched {
			for _, tok := range g.tokens {
				if b[tok] {
					matched = true
					break
				}
			}
		}
		if matched {
			shared = append(shared, g.rep)
		}
	}
	if len(shared) == 0 {
		return 0, nil
	}
	sort.Strings(shared)
	denom := len(groups)
	if bCount := countUniqueCandidateStems(b, bStems); bCount > 0 && bCount < denom {
		denom = bCount
	}
	if denom <= 0 {
		denom = 1
	}
	ratio := float64(len(shared)) / float64(denom)
	if ratio > 1.0 {
		ratio = 1.0
	}
	return ratio, shared
}

var lowSignalBoilerplateTokens = map[string]bool{
	"protocol": true, "mechanism": true, "mechanisms": true,
	"model": true, "models": true, "pattern": true, "patterns": true,
	"architecture": true, "architectural": true, "structure": true, "structural": true,
	"design": true, "overview": true, "summary": true, "guide": true,
	"behavior": true, "operations": true, "operation": true,
	"process": true, "processing": true, "system": true,
	"layer": true, "component": true, "module": true, "service": true,
	"runtime": true, "flow": true, "general": true,
}

func discriminativeTermWeight(tok, st string, poolSize int, poolStemDF map[string]int) float64 {
	isGeneric := lowSignalBoilerplateTokens[tok] || lowSignalBoilerplateTokens[st]
	w := 1.0
	if isGeneric {
		w = 0.15
	} else if len(st) >= 6 {
		w = 1.25
	}
	if poolSize > 1 && len(poolStemDF) > 0 {
		df := poolStemDF[st]
		if df < 1 {
			df = 1
		}
		if df > poolSize {
			df = poolSize
		}
		idf := 1.0 + float64(poolSize-df)/float64(poolSize)
		w *= idf
	}
	return w
}

func discriminativeOverlapWithStems(a, b, bStems map[string]bool, poolSize int, poolStemDF map[string]int) (float64, []string, int) {
	if len(a) == 0 || len(b) == 0 {
		return 0, nil, 0
	}
	type matchedTerm struct {
		tok      string
		weight   float64
		specific bool
	}
	groups := buildCanonicalStemGroups(a)
	var matches []matchedTerm
	var allWeights []float64
	specificCount := 0
	for _, g := range groups {
		w := discriminativeTermWeight(g.rep, g.stemKey, poolSize, poolStemDF)
		allWeights = append(allWeights, w)
		matched := len(g.stemKey) >= 4 && bStems[g.stemKey]
		if !matched {
			for _, tok := range g.tokens {
				if b[tok] {
					matched = true
					break
				}
			}
		}
		if matched {
			spec := !lowSignalBoilerplateTokens[g.rep] && !lowSignalBoilerplateTokens[g.stemKey]
			if spec {
				specificCount++
			}
			matches = append(matches, matchedTerm{tok: g.rep, weight: w, specific: spec})
		}
	}
	if len(matches) == 0 {
		return 0, nil, 0
	}
	sort.Slice(matches, func(i, j int) bool {
		if matches[i].specific != matches[j].specific {
			return matches[i].specific
		}
		if matches[i].weight != matches[j].weight {
			return matches[i].weight > matches[j].weight
		}
		return matches[i].tok < matches[j].tok
	})
	var shared []string
	matchedWeight := 0.0
	for _, m := range matches {
		shared = append(shared, m.tok)
		matchedWeight += m.weight
	}
	sort.Float64s(allWeights)
	denomCount := len(groups)
	if bCount := countUniqueCandidateStems(b, bStems); bCount > 0 && bCount < denomCount {
		denomCount = bCount
	}
	if denomCount <= 0 {
		denomCount = 1
	}
	denomWeight := 0.0
	for i := len(allWeights) - 1; i >= 0 && (len(allWeights)-1-i) < denomCount; i-- {
		denomWeight += allWeights[i]
	}
	if denomWeight <= 0 {
		denomWeight = 1.0
	}
	ratio := matchedWeight / denomWeight
	if ratio > 1.0 {
		ratio = 1.0
	}
	if specificCount == 0 {
		ratio *= 0.15
	}
	return ratio, shared, specificCount
}

// shortlistCandidates evaluates a bounded candidate pool from the existing scoped search/reference/source
// indexes within the immutable scan_captured_versions roster, enforcing checkCandidateTargetFence at Boundary 1
// and bounding the shortlist to K <= 6.
func (w *Worker) shortlistCandidates(
	tx app.WriteTx,
	run domain.ScanRun,
	item domain.JobItem,
	focal unitSignals,
) (domain.CandidateShortlist, int, error) {
	shortlist := domain.CandidateShortlist{
		ItemID:     item.ItemID,
		UnitKind:   item.UnitKind,
		VersionID:  item.VersionID,
		PagePath:   item.PagePath,
		RegionID:   item.RegionID,
		Candidates: []domain.CandidateTarget{},
		Themes:     append([]string(nil), focal.Themes...),
	}
	var queryTerms []string
	var genericQueryTerms []string
	seenQueryTerm := map[string]bool{}
	seenQueryStem := map[string]bool{}
	addQueryTerm := func(tok string, allowGeneric bool) {
		tok = strings.ToLower(strings.TrimSpace(tok))
		if len(tok) < 3 || seenQueryTerm[tok] {
			return
		}
		st := canonicalStem(tok)
		isGen := lowSignalBoilerplateTokens[tok] || lowSignalBoilerplateTokens[st]
		if isGen && !allowGeneric {
			genericQueryTerms = append(genericQueryTerms, tok)
			return
		}
		seenQueryTerm[tok] = true
		seenQueryStem[st] = true
		queryTerms = append(queryTerms, tok)
	}
	for _, th := range focal.Themes {
		for _, tok := range tokenizeWithSubwords(th) {
			addQueryTerm(tok, false)
		}
	}
	var headingList []string
	for tok := range focal.HeadingTerms {
		headingList = append(headingList, tok)
	}
	sort.Strings(headingList)
	for _, tok := range headingList {
		addQueryTerm(tok, false)
	}
	var bodyList []string
	for tok := range focal.BodyTerms {
		bodyList = append(bodyList, tok)
	}
	sort.Slice(bodyList, func(i, j int) bool {
		stI := canonicalStem(bodyList[i])
		stJ := canonicalStem(bodyList[j])
		genI := lowSignalBoilerplateTokens[bodyList[i]] || lowSignalBoilerplateTokens[stI]
		genJ := lowSignalBoilerplateTokens[bodyList[j]] || lowSignalBoilerplateTokens[stJ]
		if genI != genJ {
			return !genI
		}
		if len(bodyList[i]) != len(bodyList[j]) {
			return len(bodyList[i]) > len(bodyList[j])
		}
		return bodyList[i] < bodyList[j]
	})
	for _, tok := range bodyList {
		if len(queryTerms) >= 64 {
			break
		}
		st := canonicalStem(tok)
		if seenQueryStem[st] {
			continue
		}
		addQueryTerm(tok, false)
	}
	for _, tok := range bodyList {
		if len(queryTerms) >= 64 {
			break
		}
		addQueryTerm(tok, false)
	}
	for _, tok := range genericQueryTerms {
		if len(queryTerms) >= 64 {
			break
		}
		addQueryTerm(tok, true)
	}
	var queryPaths []string
	for sp := range focal.SourcePaths {
		queryPaths = append(queryPaths, sp)
	}
	sort.Strings(queryPaths)

	pool, skipped, err := tx.ScanCandidateUnits(run.JobID, item.VersionID, item.PagePath, item.RegionID, queryTerms, queryPaths, 64)
	if err != nil {
		return shortlist, skipped, err
	}

	type fencedVer struct {
		cv  domain.ScanCapturedVersion
		doc domain.Document
		ok  bool
	}
	type poolCandEntry struct {
		u       domain.CandidateTarget
		cv      domain.ScanCapturedVersion
		doc     domain.Document
		candSig unitSignals
	}
	checkedVers := map[string]fencedVer{}
	var poolEntries []poolCandEntry
	poolStemDF := map[string]int{}

	for _, u := range pool {
		if u.VersionID == item.VersionID && u.PagePath == item.PagePath && u.RegionID == item.RegionID {
			continue
		}
		fv, seen := checkedVers[u.VersionID]
		if !seen {
			cv, doc, _, ok := w.checkCandidateTargetFence(tx, run, u.VersionID, "codex")
			fv = fencedVer{cv: cv, doc: doc, ok: ok}
			checkedVers[u.VersionID] = fv
			if !ok {
				skipped++
			}
		}
		if !fv.ok {
			continue
		}
		candSig, err := extractScopeThemes(tx, fv.cv, u.PagePath, u.RegionID)
		if err != nil {
			continue
		}
		seenCandStem := map[string]bool{}
		for st := range candSig.HeadingStems {
			if st != "" && !seenCandStem[st] {
				seenCandStem[st] = true
				poolStemDF[st]++
			}
		}
		for st := range candSig.BodyStems {
			if st != "" && !seenCandStem[st] {
				seenCandStem[st] = true
				poolStemDF[st]++
			}
		}
		poolEntries = append(poolEntries, poolCandEntry{
			u:       u,
			cv:      fv.cv,
			doc:     fv.doc,
			candSig: candSig,
		})
	}

	type scoredCandidate struct {
		target      domain.CandidateTarget
		hasSpecific bool
		bodyStems   map[string]bool
	}
	var scored []scoredCandidate
	poolSize := len(poolEntries)

	focalGroundedHeadings := map[string]bool{}
	for tok := range focal.HeadingTerms {
		st := canonicalStem(tok)
		if focal.BodyTerms[tok] || (len(st) >= 4 && focal.BodyStems[st]) || focal.DeclaredTerms[tok] || (len(st) >= 4 && focal.DeclaredStems[st]) {
			focalGroundedHeadings[tok] = true
		}
	}

	for _, entry := range poolEntries {
		u := entry.u
		cv := entry.cv
		doc := entry.doc
		candSig := entry.candSig
		var signals []domain.CandidateSignal
		var totalScore float64
		hasSpecificSignal := false

		// 1. Authored reference signal.
		for _, ref := range focal.OutgoingRefs {
			matchDoc := ref.TargetDocumentID != "" && ref.TargetDocumentID == doc.ID
			matchVer := ref.TargetVersionID != "" && ref.TargetVersionID == cv.VersionID
			if matchDoc || matchVer {
				if ref.TargetPagePath == nil || *ref.TargetPagePath == "" || *ref.TargetPagePath == u.PagePath {
					if ref.TargetRegionID == nil || *ref.TargetRegionID == "" || *ref.TargetRegionID == u.RegionID {
						relDetail := "reference"
						if ref.Relation != nil && *ref.Relation != "" {
							relDetail = *ref.Relation
						}
						signals = append(signals, domain.CandidateSignal{
							Kind:   "authored_reference",
							Weight: 0.55,
							Detail: relDetail,
						})
						totalScore += 0.55
						hasSpecificSignal = true
						break
					}
				}
			}
		}

		// 2. Source path / stem overlap signal.
		if ratio, sharedPaths := overlapRatio(focal.SourcePaths, candSig.SourcePaths); ratio > 0 {
			wgt := 0.45 * ratio
			signals = append(signals, domain.CandidateSignal{
				Kind:   "source_path_overlap",
				Weight: wgt,
				Detail: strings.Join(sharedPaths, ","),
			})
			totalScore += wgt
			hasSpecificSignal = true
		} else {
			focalStems := map[string]bool{}
			for p := range focal.SourcePaths {
				st := strings.TrimSuffix(path.Base(p), path.Ext(p))
				if len(st) >= 3 {
					focalStems[strings.ToLower(st)] = true
				}
			}
			candStems := map[string]bool{}
			for p := range candSig.SourcePaths {
				st := strings.TrimSuffix(path.Base(p), path.Ext(p))
				if len(st) >= 3 {
					candStems[strings.ToLower(st)] = true
				}
			}
			if stemRatio, sharedStems := overlapRatio(focalStems, candStems); stemRatio > 0 {
				wgt := 0.35 * stemRatio
				signals = append(signals, domain.CandidateSignal{
					Kind:   "source_path_overlap",
					Weight: wgt,
					Detail: strings.Join(sharedStems, ","),
				})
				totalScore += wgt
				hasSpecificSignal = true
			}
		}

		// 3. Discriminative heading / metadata theme overlap signal.
		headingSource := focalGroundedHeadings
		if len(headingSource) == 0 {
			headingSource = focal.HeadingTerms
		}
		if ratio, sharedHeadings, specHead := discriminativeOverlapWithStems(headingSource, candSig.HeadingTerms, candSig.HeadingStems, poolSize, poolStemDF); ratio > 0 {
			wgt := 0.20 * ratio
			if specHead > 0 {
				hasSpecificSignal = true
			}
			signals = append(signals, domain.CandidateSignal{
				Kind:   "heading_term_overlap",
				Weight: wgt,
				Detail: strings.Join(sharedHeadings, ","),
			})
			totalScore += wgt
		}

		// 4. Discriminative lexical body mechanism overlap signal.
		if ratio, sharedTerms, specBody := discriminativeOverlapWithStems(focal.BodyTerms, candSig.BodyTerms, candSig.BodyStems, poolSize, poolStemDF); ratio > 0 {
			bonus := 0.0
			if specBody > 0 {
				hasSpecificSignal = true
				cappedSpec := specBody
				if cappedSpec > 4 {
					cappedSpec = 4
				}
				bonus = 0.05 * float64(cappedSpec)
			}
			wgt := 0.40*ratio + bonus
			if len(sharedTerms) > 5 {
				sharedTerms = sharedTerms[:5]
			}
			signals = append(signals, domain.CandidateSignal{
				Kind:   "lexical_bm25",
				Weight: wgt,
				Detail: strings.Join(sharedTerms, ","),
			})
			totalScore += wgt
		}

		if totalScore <= 0 {
			continue
		}
		scored = append(scored, scoredCandidate{
			target: domain.CandidateTarget{
				ProjectID:   cv.ProjectID,
				DocumentID:  doc.ID,
				VersionID:   cv.VersionID,
				PagePath:    u.PagePath,
				RegionID:    u.RegionID,
				UnitKind:    u.UnitKind,
				TargetSetID: cv.TargetSetID,
				Title:       candSig.Title,
				Score:       totalScore,
				Signals:     signals,
			},
			hasSpecific: hasSpecificSignal,
			bodyStems:   candSig.BodyStems,
		})
	}

	sort.SliceStable(scored, func(i, j int) bool {
		if scored[i].hasSpecific != scored[j].hasSpecific {
			return scored[i].hasSpecific
		}
		if scored[i].target.Score != scored[j].target.Score {
			return scored[i].target.Score > scored[j].target.Score
		}
		if scored[i].target.VersionID != scored[j].target.VersionID {
			return scored[i].target.VersionID < scored[j].target.VersionID
		}
		if scored[i].target.PagePath != scored[j].target.PagePath {
			return scored[i].target.PagePath < scored[j].target.PagePath
		}
		return scored[i].target.RegionID < scored[j].target.RegionID
	})

	candidates := make([]domain.CandidateTarget, 0, len(scored))
	if len(scored) > maxShortlistCandidates {
		selected := make([]scoredCandidate, 0, maxShortlistCandidates)
		usedIdx := map[int]bool{}
		for len(selected) < maxShortlistCandidates {
			bestIdx := -1
			bestEffScore := -1.0
			bestHasSpec := false
			for idx, sc := range scored {
				if usedIdx[idx] {
					continue
				}
				effScore := sc.target.Score
				for _, prev := range selected {
					if prev.target.VersionID == sc.target.VersionID {
						if bodyRed, _ := overlapRatio(sc.bodyStems, prev.bodyStems); bodyRed >= 0.80 {
							effScore *= 0.65
						}
					}
				}
				if bestIdx == -1 ||
					(sc.hasSpecific != bestHasSpec && sc.hasSpecific) ||
					(sc.hasSpecific == bestHasSpec && effScore > bestEffScore) {
					bestIdx = idx
					bestEffScore = effScore
					bestHasSpec = sc.hasSpecific
				}
			}
			if bestIdx < 0 {
				break
			}
			usedIdx[bestIdx] = true
			selected = append(selected, scored[bestIdx])
		}
		sort.SliceStable(selected, func(i, j int) bool {
			if selected[i].hasSpecific != selected[j].hasSpecific {
				return selected[i].hasSpecific
			}
			if selected[i].target.Score != selected[j].target.Score {
				return selected[i].target.Score > selected[j].target.Score
			}
			if selected[i].target.VersionID != selected[j].target.VersionID {
				return selected[i].target.VersionID < selected[j].target.VersionID
			}
			if selected[i].target.PagePath != selected[j].target.PagePath {
				return selected[i].target.PagePath < selected[j].target.PagePath
			}
			return selected[i].target.RegionID < selected[j].target.RegionID
		})
		for _, sc := range selected {
			candidates = append(candidates, sc.target)
		}
	} else {
		for _, sc := range scored {
			candidates = append(candidates, sc.target)
		}
	}
	shortlist.Candidates = candidates
	fpCandidates := make([]domain.CandidateTarget, len(candidates))
	copy(fpCandidates, candidates)
	for i := range fpCandidates {
		fpCandidates[i].TargetSetID = ""
	}
	fpBytes, _ := json.Marshal(struct {
		VersionID  string                   `json:"version_id"`
		PagePath   string                   `json:"page_path"`
		RegionID   string                   `json:"region_id"`
		Themes     []string                 `json:"themes"`
		Candidates []domain.CandidateTarget `json:"candidates"`
	}{
		VersionID:  shortlist.VersionID,
		PagePath:   shortlist.PagePath,
		RegionID:   shortlist.RegionID,
		Themes:     shortlist.Themes,
		Candidates: fpCandidates,
	})
	sum := sha256.Sum256(fpBytes)
	shortlist.ShortlistFingerprint = hex.EncodeToString(sum[:])
	return shortlist, skipped, nil
}

var (
	reNonGoImport  = regexp.MustCompile(`(?m)^[ \t]*(?:import\s+(?:[A-Za-z0-9_{},\s*]+\s+from\s+)?["']([^"']+)["']|from\s+([A-Za-z0-9_./-]+)\s+import\s+([A-Za-z0-9_, \t*]+)|import\s+([A-Za-z0-9_./-]+)(?:\s+as\s+([A-Za-z_][A-Za-z0-9_]*))?|#include\s+[<"]([^>"]+)[>"]|(?:(?:const|let|var)\s+([A-Za-z_][A-Za-z0-9_]*)\s*=\s*)?require\(\s*["']([^"']+)["']\s*\)|use\s+([A-Za-z0-9_:]+))`)
	reNonGoTopDecl = regexp.MustCompile(`(?m)^[ \t]*(?:export\s+)?(?:pub\s+)?(?:async\s+)?(?:def|class|function|interface|struct|enum|const|let|var|fn)\s+([A-Za-z_][A-Za-z0-9_]{2,})\b`)
	reQualCall     = regexp.MustCompile(`\b([A-Za-z_][A-Za-z0-9_]*)\.([A-Za-z_][A-Za-z0-9_]{2,})\s*\(`)
)

// stripCommentsAndStringLiterals removes // and /* */ comments, # line comments
// (preserving #include directives), and replaces string literal contents on non-import
// lines so commented-out imports/calls or string literals never match as code facts.
func stripCommentsAndStringLiterals(src string) string {
	var out strings.Builder
	out.Grow(len(src))
	n := len(src)
	i := 0
	lineStart := true
	lineIsDirective := false
	for i < n {
		ch := src[i]
		if ch == '\n' {
			out.WriteByte('\n')
			i++
			lineStart = true
			lineIsDirective = false
			continue
		}
		if lineStart && (ch == ' ' || ch == '\t' || ch == '\r') {
			out.WriteByte(ch)
			i++
			continue
		}
		if lineStart {
			trimmed := src[i:]
			if strings.HasPrefix(trimmed, "import ") || strings.HasPrefix(trimmed, "import\t") || strings.HasPrefix(trimmed, "import(") ||
				strings.HasPrefix(trimmed, "from ") || strings.HasPrefix(trimmed, "#include ") || strings.HasPrefix(trimmed, "#include\t") ||
				strings.HasPrefix(trimmed, "use ") || strings.Contains(trimmed[:minInt(len(trimmed), 80)], "require(") {
				lineIsDirective = true
			}
			lineStart = false
		}
		// Block comment /* ... */
		if ch == '/' && i+1 < n && src[i+1] == '*' {
			i += 2
			for i+1 < n && !(src[i] == '*' && src[i+1] == '/') {
				if src[i] == '\n' {
					out.WriteByte('\n')
				}
				i++
			}
			if i+1 < n {
				i += 2
			}
			continue
		}
		// Line comment // ...
		if ch == '/' && i+1 < n && src[i+1] == '/' {
			for i < n && src[i] != '\n' {
				i++
			}
			continue
		}
		// Python/shell # comment (unless #include)
		if ch == '#' && !strings.HasPrefix(src[i:], "#include") {
			for i < n && src[i] != '\n' {
				i++
			}
			continue
		}
		// Triple-quoted strings """...""" or '''...'''
		if (ch == '"' || ch == '\'') && i+2 < n && src[i+1] == ch && src[i+2] == ch {
			quote := ch
			i += 3
			for i+2 < n && !(src[i] == quote && src[i+1] == quote && src[i+2] == quote) {
				if src[i] == '\n' {
					out.WriteByte('\n')
				}
				i++
			}
			if i+2 < n {
				i += 3
			}
			continue
		}
		// Quoted string literal "..." or '...' or `...`
		if ch == '"' || ch == '\'' || ch == '`' {
			quote := ch
			if lineIsDirective && quote != '`' {
				out.WriteByte(quote)
				i++
				for i < n && src[i] != quote && src[i] != '\n' {
					if src[i] == '\\' && i+1 < n {
						out.WriteByte(src[i])
						out.WriteByte(src[i+1])
						i += 2
						continue
					}
					out.WriteByte(src[i])
					i++
				}
				if i < n && src[i] == quote {
					out.WriteByte(quote)
					i++
				}
				continue
			}
			// Non-import string literal: elide inner characters so string contents cannot fake imports/calls.
			out.WriteByte(quote)
			i++
			for i < n && src[i] != quote {
				if quote != '`' && src[i] == '\\' && i+1 < n {
					i += 2
					continue
				}
				if src[i] == '\n' {
					out.WriteByte('\n')
					if quote != '`' {
						break
					}
				}
				i++
			}
			if i < n && src[i] == quote {
				out.WriteByte(quote)
				i++
			}
			continue
		}
		out.WriteByte(ch)
		i++
	}
	return out.String()
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

type verifiedSourceFile struct {
	RepoKey string
	Path    string
	Text    string
}

var (
	reGoCanonicalImport = regexp.MustCompile(`^(?://\s*import\s+"([^"]+)"\s*|/\*\s*import\s+"([^"]+)"\s*\*/)$`)
	reGoModModule       = regexp.MustCompile(`(?m)^\s*module\s+(?:"([^"\s]+)"|([^\s/]+(?:/[^\s]+)*))\s*$`)
	reTomlPkgName       = regexp.MustCompile(`(?m)^\s*name\s*=\s*["']([^"'\s]+)["']\s*$`)
)

func isManifestFileName(base string) bool {
	return base == "go.mod" || base == "package.json" || base == "pyproject.toml" || base == "Cargo.toml"
}

func extractManifestModule(cleanPath, text string) (modRoot string, modPath string, ok bool) {
	base := path.Base(cleanPath)
	modRoot = path.Dir(cleanPath)
	if modRoot == "" {
		modRoot = "."
	}
	switch base {
	case "go.mod":
		stripped := stripCommentsAndStringLiterals(text)
		if m := reGoModModule.FindStringSubmatch(stripped); len(m) > 0 {
			mp := m[1]
			if mp == "" {
				mp = m[2]
			}
			mp = strings.TrimSpace(mp)
			if mp != "" {
				return modRoot, mp, true
			}
		}
	case "package.json":
		var pkg struct {
			Name string `json:"name"`
		}
		if json.Unmarshal([]byte(text), &pkg) == nil && strings.TrimSpace(pkg.Name) != "" {
			return modRoot, strings.TrimSpace(pkg.Name), true
		}
	case "pyproject.toml", "Cargo.toml":
		stripped := stripCommentsAndStringLiterals(text)
		if m := reTomlPkgName.FindStringSubmatch(stripped); len(m) > 0 && strings.TrimSpace(m[1]) != "" {
			return modRoot, strings.TrimSpace(m[1]), true
		}
	}
	return "", "", false
}

func sameVerifiedRepo(a, b verifiedSourceFile) bool {
	return a.RepoKey != "" && a.RepoKey == b.RepoKey
}

type targetUnitBindings struct {
	verifiedModulePaths map[string]bool
	dstCleanByRepo      map[string]map[string]bool
	dstNoExtByRepo      map[string]map[string]bool
	dstDirsByRepo       map[string]map[string]bool
}

func importResolvesToTarget(impPath string, srcFile verifiedSourceFile, isGo bool, bindings targetUnitBindings) bool {
	raw := strings.TrimSpace(impPath)
	if raw == "" {
		return false
	}
	// 1. Verified module/package namespace binding (exact case-sensitive match; works within or across repos).
	if bindings.verifiedModulePaths[raw] {
		return true
	}
	if !isGo && !strings.HasPrefix(raw, ".") {
		normMod := strings.ReplaceAll(raw, "::", "/")
		if !strings.Contains(normMod, "/") && strings.Contains(normMod, ".") &&
			!strings.HasSuffix(normMod, ".h") && !strings.HasSuffix(normMod, ".js") &&
			!strings.HasSuffix(normMod, ".ts") && !strings.HasSuffix(normMod, ".py") && !strings.HasSuffix(normMod, ".go") {
			normMod = strings.ReplaceAll(normMod, ".", "/")
		}
		if bindings.verifiedModulePaths[normMod] {
			return true
		}
	}

	// 2. Same-repository local resolution (requires non-empty verified RepoKey shared by focal and target).
	if srcFile.RepoKey == "" {
		return false
	}
	dstCleanPaths := bindings.dstCleanByRepo[srcFile.RepoKey]
	dstNoExtPaths := bindings.dstNoExtByRepo[srcFile.RepoKey]
	dstDirs := bindings.dstDirsByRepo[srcFile.RepoKey]
	if len(dstCleanPaths) == 0 {
		return false
	}
	srcClean := strings.TrimPrefix(path.Clean(srcFile.Path), "/")
	if srcClean == "." {
		srcClean = ""
	}
	srcDir := path.Dir(srcClean)
	if srcDir == "." {
		srcDir = ""
	}

	// 2a. Explicit relative path ("./..." or "../...").
	if strings.HasPrefix(raw, "./") || strings.HasPrefix(raw, "../") {
		if srcClean == "" {
			return false
		}
		resolved := path.Clean(path.Join(srcDir, raw))
		if resolved == "." || resolved == "" || strings.HasPrefix(resolved, "..") {
			return false
		}
		resolvedNoExt := strings.TrimSuffix(resolved, path.Ext(resolved))
		return dstCleanPaths[resolved] || dstNoExtPaths[resolved] || dstNoExtPaths[resolvedNoExt] || dstDirs[resolved]
	}

	// For Go files, non-relative imports are module-qualified and MUST resolve via verifiedModulePaths above;
	// never guess module prefixes or suffix-match unverified repository paths.
	if isGo {
		return false
	}

	// 2b. Python leading-dot relative import (".quota" or "..billing.quota").
	if strings.HasPrefix(raw, ".") {
		if srcClean == "" {
			return false
		}
		dots := 0
		for dots < len(raw) && raw[dots] == '.' {
			dots++
		}
		rest := strings.ReplaceAll(raw[dots:], ".", "/")
		relPrefix := "."
		if dots > 1 {
			ups := make([]string, dots-1)
			for i := range ups {
				ups[i] = ".."
			}
			relPrefix = strings.Join(ups, "/")
		}
		joined := relPrefix
		if rest != "" {
			joined = path.Join(relPrefix, rest)
		}
		resolved := path.Clean(path.Join(srcDir, joined))
		if resolved == "." || resolved == "" || strings.HasPrefix(resolved, "..") {
			return false
		}
		resolvedNoExt := strings.TrimSuffix(resolved, path.Ext(resolved))
		return dstCleanPaths[resolved] || dstNoExtPaths[resolved] || dstNoExtPaths[resolvedNoExt] || dstDirs[resolved]
	}

	// 2c. Non-Go same-repository root-relative import/include (exact case-sensitive match from repo root, no suffix matching).
	norm := strings.ReplaceAll(raw, "::", "/")
	if !strings.Contains(norm, "/") && strings.Contains(norm, ".") &&
		!strings.HasSuffix(norm, ".h") && !strings.HasSuffix(norm, ".js") &&
		!strings.HasSuffix(norm, ".ts") && !strings.HasSuffix(norm, ".py") && !strings.HasSuffix(norm, ".go") {
		norm = strings.ReplaceAll(norm, ".", "/")
	}
	cleanNorm := strings.TrimPrefix(path.Clean(norm), "/")
	if cleanNorm == "" || cleanNorm == "." || strings.HasPrefix(cleanNorm, "..") {
		return false
	}
	cleanNormNoExt := strings.TrimSuffix(cleanNorm, path.Ext(cleanNorm))
	return dstCleanPaths[cleanNorm] || dstNoExtPaths[cleanNorm] || dstNoExtPaths[cleanNormNoExt] || dstDirs[cleanNorm]
}

// verifySourceDependencyFact wraps verifySourceDependencyFactWithRepo for callers
// supplying files from a single verified repository context.
func verifySourceDependencyFact(
	focalTexts []string,
	focalPaths []string,
	targetTexts []string,
	targetPaths []string,
) (bool, string) {
	focalFiles := make([]verifiedSourceFile, len(focalTexts))
	for i, txt := range focalTexts {
		var p string
		if i < len(focalPaths) {
			p = focalPaths[i]
		}
		focalFiles[i] = verifiedSourceFile{RepoKey: "same-verified-repo", Path: p, Text: txt}
	}
	targetFiles := make([]verifiedSourceFile, len(targetTexts))
	for i, txt := range targetTexts {
		var p string
		if i < len(targetPaths) {
			p = targetPaths[i]
		}
		targetFiles[i] = verifiedSourceFile{RepoKey: "same-verified-repo", Path: p, Text: txt}
	}
	return verifySourceDependencyFactWithRepo(focalFiles, targetFiles)
}

// verifySourceDependencyFactWithRepo is the general deterministic predicate for T-008-02-01.
// A directional code dependency ('depends_on' / 'source_dependency' with evidence_basis='source_fact')
// from focal -> target is permitted iff verified source bytes of the focal unit contain an actual
// AST/syntax-verified import/include/module path resolving via verified module/namespace or
// same-repository relative binding to the target unit's approved source path/module
// (or a qualified call bound to that resolved import of a top-level symbol declared by the target unit).
// Multi-segment suffix coincidences across different modules, case-merged paths, commented imports/calls,
// string literals, unrelated imported objects with same-named methods, bare package/stem coincidences
// without an import binding, and reverse (target -> focal) dependencies deterministically return false.
func verifySourceDependencyFactWithRepo(
	focalFiles []verifiedSourceFile,
	targetFiles []verifiedSourceFile,
) (bool, string) {
	if len(focalFiles) == 0 || len(targetFiles) == 0 {
		return false, ""
	}
	bindings := targetUnitBindings{
		verifiedModulePaths: map[string]bool{},
		dstCleanByRepo:      map[string]map[string]bool{},
		dstNoExtByRepo:      map[string]map[string]bool{},
		dstDirsByRepo:       map[string]map[string]bool{},
	}

	type declaredManifest struct {
		repoKey  string
		isTarget bool
		modRoot  string
		modPath  string
	}
	var manifests []declaredManifest
	for _, tf := range targetFiles {
		clean := strings.TrimPrefix(path.Clean(tf.Path), "/")
		if clean == "" || clean == "." {
			continue
		}
		if modRoot, modPath, ok := extractManifestModule(clean, tf.Text); ok {
			manifests = append(manifests, declaredManifest{
				repoKey:  tf.RepoKey,
				isTarget: true,
				modRoot:  modRoot,
				modPath:  modPath,
			})
		}
	}
	for _, ff := range focalFiles {
		if ff.RepoKey == "" {
			continue
		}
		clean := strings.TrimPrefix(path.Clean(ff.Path), "/")
		if clean == "" || clean == "." {
			continue
		}
		if modRoot, modPath, ok := extractManifestModule(clean, ff.Text); ok {
			manifests = append(manifests, declaredManifest{
				repoKey:  ff.RepoKey,
				isTarget: false,
				modRoot:  modRoot,
				modPath:  modPath,
			})
		}
	}

	hasTargetCodePath := false
	dstTopSymbols := map[string]bool{}
	dstGoPkgNames := map[string]bool{}
	for _, tf := range targetFiles {
		clean := strings.TrimPrefix(path.Clean(tf.Path), "/")
		if clean == "" || clean == "." || isManifestFileName(path.Base(clean)) {
			continue
		}
		hasTargetCodePath = true
		noExt := strings.TrimSuffix(clean, path.Ext(clean))
		dir := path.Dir(clean)
		if tf.RepoKey != "" {
			if bindings.dstCleanByRepo[tf.RepoKey] == nil {
				bindings.dstCleanByRepo[tf.RepoKey] = map[string]bool{}
				bindings.dstNoExtByRepo[tf.RepoKey] = map[string]bool{}
				bindings.dstDirsByRepo[tf.RepoKey] = map[string]bool{}
			}
			bindings.dstCleanByRepo[tf.RepoKey][clean] = true
			bindings.dstNoExtByRepo[tf.RepoKey][noExt] = true
			if dir != "" && dir != "." {
				bindings.dstDirsByRepo[tf.RepoKey][dir] = true
			}
		}
		for _, m := range manifests {
			if (m.repoKey != "" && m.repoKey == tf.RepoKey) || (m.isTarget && m.repoKey == tf.RepoKey) {
				if m.modRoot == "." || strings.HasPrefix(clean, m.modRoot+"/") {
					relFile := clean
					if m.modRoot != "." {
						relFile = strings.TrimPrefix(clean, m.modRoot+"/")
					}
					relDir := path.Dir(relFile)
					relNoExt := strings.TrimSuffix(relFile, path.Ext(relFile))
					if relDir == "." || relDir == "" {
						bindings.verifiedModulePaths[m.modPath] = true
					} else {
						bindings.verifiedModulePaths[m.modPath+"/"+relDir] = true
					}
					bindings.verifiedModulePaths[m.modPath+"/"+relFile] = true
					bindings.verifiedModulePaths[m.modPath+"/"+relNoExt] = true
				}
			}
		}

		fset := token.NewFileSet()
		if f, err := parser.ParseFile(fset, "", tf.Text, parser.ParseComments|parser.SkipObjectResolution); err == nil && f != nil {
			if f.Name != nil && f.Name.Name != "" {
				dstGoPkgNames[f.Name.Name] = true
				pkgLine := fset.Position(f.Name.Pos()).Line
				for _, cg := range f.Comments {
					for _, c := range cg.List {
						if fset.Position(c.Pos()).Line == pkgLine {
							if cm := reGoCanonicalImport.FindStringSubmatch(strings.TrimSpace(c.Text)); len(cm) > 0 {
								canonImp := cm[1]
								if canonImp == "" {
									canonImp = cm[2]
								}
								if canonImp != "" {
									bindings.verifiedModulePaths[canonImp] = true
								}
							}
						}
					}
				}
			}
			for _, decl := range f.Decls {
				switch d := decl.(type) {
				case *ast.FuncDecl:
					// Only package-level functions (Recv == nil) can be called as pkg.Func().
					// Receiver methods on a struct/type are excluded so obj.Method() on an unrelated type never matches.
					if d.Recv == nil && d.Name != nil && len(d.Name.Name) >= 3 && !stopWords[strings.ToLower(d.Name.Name)] {
						dstTopSymbols[d.Name.Name] = true
					}
				case *ast.GenDecl:
					for _, spec := range d.Specs {
						switch s := spec.(type) {
						case *ast.TypeSpec:
							if s.Name != nil && len(s.Name.Name) >= 3 && !stopWords[strings.ToLower(s.Name.Name)] {
								dstTopSymbols[s.Name.Name] = true
							}
						case *ast.ValueSpec:
							for _, id := range s.Names {
								if id != nil && len(id.Name) >= 3 && !stopWords[strings.ToLower(id.Name)] {
									dstTopSymbols[id.Name] = true
								}
							}
						}
					}
				}
			}
			continue
		}
		stripped := stripCommentsAndStringLiterals(tf.Text)
		for _, m := range reNonGoTopDecl.FindAllStringSubmatch(stripped, -1) {
			sym := m[1]
			if len(sym) >= 3 && !stopWords[strings.ToLower(sym)] {
				dstTopSymbols[sym] = true
			}
		}
	}
	if !hasTargetCodePath {
		return false, ""
	}

	for _, ff := range focalFiles {
		cleanFocal := strings.TrimPrefix(path.Clean(ff.Path), "/")
		if isManifestFileName(path.Base(cleanFocal)) {
			continue
		}
		fset := token.NewFileSet()
		if f, err := parser.ParseFile(fset, "", ff.Text, parser.SkipObjectResolution); err == nil && f != nil {
			boundAliases := map[string]string{}
			var firstResolvedImport string
			for _, imp := range f.Imports {
				if imp.Path == nil {
					continue
				}
				impPath, uErr := strconv.Unquote(imp.Path.Value)
				if uErr != nil || impPath == "" {
					continue
				}
				if !importResolvesToTarget(impPath, ff, true, bindings) {
					continue
				}
				if firstResolvedImport == "" {
					firstResolvedImport = impPath
				}
				alias := path.Base(impPath)
				if imp.Name != nil && imp.Name.Name != "" {
					alias = imp.Name.Name
				} else if len(dstGoPkgNames) == 1 {
					for pkgName := range dstGoPkgNames {
						alias = pkgName
					}
				}
				if alias != "_" && alias != "." && alias != "" {
					boundAliases[alias] = impPath
				}
			}
			if len(boundAliases) > 0 && len(dstTopSymbols) > 0 {
				var matchedCall string
				for _, decl := range f.Decls {
					fn, ok := decl.(*ast.FuncDecl)
					if !ok || fn.Body == nil {
						continue
					}
					shadowed := map[string]bool{}
					if fn.Recv != nil {
						for _, fld := range fn.Recv.List {
							for _, n := range fld.Names {
								shadowed[n.Name] = true
							}
						}
					}
					if fn.Type != nil && fn.Type.Params != nil {
						for _, fld := range fn.Type.Params.List {
							for _, n := range fld.Names {
								shadowed[n.Name] = true
							}
						}
					}
					ast.Inspect(fn.Body, func(node ast.Node) bool {
						switch stmt := node.(type) {
						case *ast.AssignStmt:
							if stmt.Tok == token.DEFINE {
								for _, lhs := range stmt.Lhs {
									if id, ok := lhs.(*ast.Ident); ok {
										shadowed[id.Name] = true
									}
								}
							}
						case *ast.ValueSpec:
							for _, id := range stmt.Names {
								shadowed[id.Name] = true
							}
						case *ast.SelectorExpr:
							if pkgIdent, ok := stmt.X.(*ast.Ident); ok && stmt.Sel != nil {
								if _, bound := boundAliases[pkgIdent.Name]; bound && !shadowed[pkgIdent.Name] && dstTopSymbols[stmt.Sel.Name] {
									if matchedCall == "" {
										matchedCall = "call:" + pkgIdent.Name + "." + stmt.Sel.Name
									}
								}
							}
						}
						return matchedCall == ""
					})
					if matchedCall != "" {
						return true, matchedCall
					}
				}
			}
			if firstResolvedImport != "" {
				return true, "import:" + firstResolvedImport
			}
			// Valid Go file without any import resolving to target: do not fall back to regex!
			continue
		}

		// Non-Go source text: strip comments and string literals first.
		stripped := stripCommentsAndStringLiterals(ff.Text)
		boundAliases := map[string]string{}
		var firstResolvedImport string
		for _, m := range reNonGoImport.FindAllStringSubmatch(stripped, -1) {
			switch {
			case m[1] != "": // JS/TS import ... from "path"
				if importResolvesToTarget(m[1], ff, false, bindings) {
					if firstResolvedImport == "" {
						firstResolvedImport = m[1]
					}
					base := strings.TrimSuffix(path.Base(m[1]), path.Ext(m[1]))
					if base != "" {
						boundAliases[base] = m[1]
					}
				}
			case m[2] != "": // Python from module import sym1, sym2
				if importResolvesToTarget(m[2], ff, false, bindings) {
					if firstResolvedImport == "" {
						firstResolvedImport = m[2]
					}
					for _, part := range strings.Split(m[3], ",") {
						fields := strings.Fields(part)
						if len(fields) > 0 && dstTopSymbols[fields[0]] {
							return true, "call:" + m[2] + "." + fields[0]
						}
					}
				}
			case m[4] != "": // Python import module [as alias]
				if importResolvesToTarget(m[4], ff, false, bindings) {
					if firstResolvedImport == "" {
						firstResolvedImport = m[4]
					}
					alias := m[5]
					if alias == "" {
						alias = path.Base(strings.ReplaceAll(m[4], ".", "/"))
					}
					if alias != "" {
						boundAliases[alias] = m[4]
					}
				}
			case m[6] != "": // C/C++ #include "path"
				if importResolvesToTarget(m[6], ff, false, bindings) {
					return true, "import:" + m[6]
				}
			case m[8] != "": // JS require("path")
				if importResolvesToTarget(m[8], ff, false, bindings) {
					if firstResolvedImport == "" {
						firstResolvedImport = m[8]
					}
					if m[7] != "" {
						boundAliases[m[7]] = m[8]
					}
				}
			case m[9] != "": // Rust use path::...
				if importResolvesToTarget(m[9], ff, false, bindings) {
					return true, "import:" + m[9]
				}
			}
		}
		if len(boundAliases) > 0 && len(dstTopSymbols) > 0 {
			for _, cm := range reQualCall.FindAllStringSubmatch(stripped, -1) {
				alias, sym := cm[1], cm[2]
				if _, ok := boundAliases[alias]; ok && dstTopSymbols[sym] {
					return true, "call:" + alias + "." + sym
				}
			}
		}
		if firstResolvedImport != "" {
			return true, "import:" + firstResolvedImport
		}
	}
	return false, ""
}

func (w *Worker) resolveUnitExplanation(tx app.WriteTx, cv domain.ScanCapturedVersion, docID, pagePath, regionID, role, title string) (domain.MaintenanceExplanation, bool) {
	chosenPath := pagePath
	if chosenPath == "" {
		pages, err := tx.VersionPagesAfter(cv.VersionID, "", "", 1)
		if err == nil && len(pages) > 0 {
			chosenPath = pages[0].Path
		} else {
			entries, eErr := tx.Entries(cv.ContentSetID, "", 32)
			if eErr != nil || len(entries) == 0 {
				return domain.MaintenanceExplanation{}, false
			}
			for _, ent := range entries {
				if ent.Kind == "file" && ent.Hash != "" {
					chosenPath = ent.Path
					break
				}
			}
			if chosenPath == "" {
				return domain.MaintenanceExplanation{}, false
			}
		}
	}
	entry, err := tx.QueryEntry(cv.ContentSetID, chosenPath)
	if err != nil || entry.Kind != "file" || entry.Hash == "" {
		return domain.MaintenanceExplanation{}, false
	}
	obj, err := tx.Object(entry.Hash)
	if err != nil || obj.State != "ready" || obj.Size != entry.Size {
		return domain.MaintenanceExplanation{}, false
	}
	pCopy := chosenPath
	var rPtr *string
	if regionID != "" {
		rc := regionID
		rPtr = &rc
	}
	return domain.MaintenanceExplanation{
		Role:        role,
		ProjectID:   cv.ProjectID,
		DocumentID:  docID,
		VersionID:   cv.VersionID,
		PagePath:    &pCopy,
		RegionID:    rPtr,
		Title:       title,
		ObjectHash:  obj.Hash,
		Size:        obj.Size,
		TargetSetID: cv.TargetSetID,
	}, true
}

func (w *Worker) collectUnitSourceSelections(
	tx app.WriteTx,
	run domain.ScanRun,
	cv domain.ScanCapturedVersion,
	pagePath, regionID string,
) ([]source.CaptureSelection, []string, []domain.MaintenanceReadBinding, []domain.MaintenanceLimitation) {
	return w.collectUnitSourceSelectionsWithAliases(tx, run, cv, pagePath, regionID, nil, false)
}

func (w *Worker) collectUnitSourceSelectionsWithAliases(tx app.WriteTx, run domain.ScanRun, cv domain.ScanCapturedVersion, pagePath, regionID string, sourceAliases map[string]string, includeCaptureContext bool) ([]source.CaptureSelection, []string, []domain.MaintenanceReadBinding, []domain.MaintenanceLimitation) {
	cfg, err := tx.Configuration()
	if err != nil {
		return nil, nil, nil, nil
	}
	var cfgVal struct {
		Projects map[string]struct {
			Sources []domain.ManagedSourceMapping `json:"sources"`
		} `json:"projects"`
	}
	if json.Unmarshal(cfg.Value, &cfgVal) != nil {
		return nil, nil, nil, nil
	}
	configuredBySource := map[string]domain.ManagedSourceMapping{}
	for _, m := range cfgVal.Projects[cv.ProjectID].Sources {
		if m.SourceID != "" {
			configuredBySource[m.SourceID] = m
		}
	}
	targetBySource := map[string]domain.ScanSourceTarget{}
	for _, t := range cv.Targets {
		if t.SourceID != "" {
			targetBySource[t.SourceID] = t
		}
	}

	aliasByVersionSource := map[string]string{}
	if vSources, vsErr := tx.VersionSources(cv.VersionID); vsErr == nil {
		for _, vs := range vSources {
			if mapped := sourceAliases[vs.ID]; mapped != "" {
				aliasByVersionSource[vs.ID] = mapped
				continue
			}
			if _, ok := targetBySource[vs.ID]; ok {
				aliasByVersionSource[vs.ID] = vs.ID
				continue
			}
			if _, ok := configuredBySource[vs.ID]; ok {
				aliasByVersionSource[vs.ID] = vs.ID
				continue
			}
			var meta struct {
				SourceID string `json:"source_id"`
				RepoID   string `json:"repo_id"`
			}
			if json.Unmarshal(vs.Metadata, &meta) == nil {
				if meta.SourceID != "" {
					aliasByVersionSource[vs.ID] = meta.SourceID
					continue
				}
			}
			if len(cv.Targets) == 1 && len(vSources) == 1 && cv.Targets[0].SourceID != "" {
				aliasByVersionSource[vs.ID] = cv.Targets[0].SourceID
			}
		}
	}

	pathsBySource := map[string]map[string]bool{}
	recordEvidencePath := func(ev domain.Evidence) {
		var meta struct {
			Locator struct {
				Path string `json:"path"`
			} `json:"locator"`
			Path string `json:"path"`
		}
		if json.Unmarshal(ev.Metadata, &meta) != nil {
			return
		}
		p := meta.Locator.Path
		if p == "" {
			p = meta.Path
		}
		if p == "" {
			return
		}
		cleanP := strings.TrimPrefix(path.Clean(p), "/")
		if cleanP == "" || cleanP == "." || strings.HasPrefix(cleanP, "..") {
			return
		}
		sid := strings.TrimSpace(ev.SourceID)
		if mapped, ok := aliasByVersionSource[sid]; ok {
			sid = mapped
		}
		if sid == "" {
			if len(cv.Targets) == 1 && cv.Targets[0].SourceID != "" {
				sid = cv.Targets[0].SourceID
			} else {
				sid = "_unmapped_"
			}
		}
		if pathsBySource[sid] == nil {
			pathsBySource[sid] = map[string]bool{}
		}
		pathsBySource[sid][cleanP] = true
	}

	linkedEvIDs := map[string]bool{}
	if links, err := tx.VersionRegionEvidence(cv.VersionID); err == nil {
		for _, link := range links {
			linkedEvIDs[link.EvidenceID] = true
			if pagePath != "" && link.PagePath != pagePath {
				continue
			}
			if regionID != "" && link.RegionID != regionID {
				continue
			}
			if ev, err := tx.QueryEvidence(cv.VersionID, link.EvidenceID); err == nil {
				recordEvidencePath(ev)
			}
		}
	}
	if regionID == "" {
		if evs, err := tx.VersionEvidence(cv.VersionID); err == nil {
			for _, ev := range evs {
				if !linkedEvIDs[ev.ID] {
					recordEvidencePath(ev)
				}
			}
		}
	}

	// Index any pre-existing captured_reads bound to cv.TargetSetID at scan start.
	type boundReadInfo struct {
		readID       string
		sourceID     string
		path         string
		availability string
		hash         string
		exactRev     string
	}
	boundReadsBySourcePath := map[string]boundReadInfo{}
	var allBoundReads []boundReadInfo
	hasCompletedCapture := false
	var explicitContext *source.CaptureRequest
	invalidExplicitContext := false
	if cv.TargetSetID != "" {
		if sc, found, err := tx.SourceCaptureByTargetSet(cv.TargetSetID); err == nil && found && sc.State == "completed" {
			hasCompletedCapture = true
			// Only freshness may include additional paths from an explicitly
			// selected, completed version-bound capture. Implicit captures and
			// span-only verification never expand the cited-file selection.
			if includeCaptureContext && cv.SelectionSource == "target_set" {
				var selected source.CaptureRequest
				if sc.ProjectID != cv.ProjectID || json.Unmarshal(sc.Selection, &selected) != nil || selected.VersionID != cv.VersionID {
					invalidExplicitContext = true
				} else {
					explicitContext = &selected
				}
			}
			afterRead := ""
			for {
				reads, rErr := tx.SourceCaptureReads(sc.ID, afterRead, 64)
				if rErr != nil || len(reads) == 0 {
					break
				}
				for _, r := range reads {
					afterRead = r.ID
					var rLoc struct {
						Path string `json:"path"`
					}
					_ = json.Unmarshal(r.LocationJSON, &rLoc)
					cleanRP := strings.TrimPrefix(path.Clean(rLoc.Path), "/")
					var rSrc struct {
						ExactRevision string `json:"exact_revision"`
					}
					_ = json.Unmarshal(r.SourceJSON, &rSrc)
					exactRev := rSrc.ExactRevision
					if exactRev == "" {
						if cr, crErr := tx.CapturedRead(r.ID); crErr == nil {
							var crSrc struct {
								ExactRevision string `json:"exact_revision"`
							}
							_ = json.Unmarshal(cr.SourceJSON, &crSrc)
							exactRev = crSrc.ExactRevision
						}
					}
					info := boundReadInfo{
						readID:       r.ID,
						sourceID:     r.SourceID,
						path:         cleanRP,
						availability: r.Availability,
						hash:         r.Hash,
						exactRev:     exactRev,
					}
					allBoundReads = append(allBoundReads, info)
					if r.SourceID != "" && cleanRP != "" {
						boundReadsBySourcePath[r.SourceID+"\x00"+cleanRP] = info
					}
				}
				if len(reads) < 64 {
					break
				}
			}
		} else if ts, tsErr := tx.TargetSetByID(cv.TargetSetID); tsErr == nil && len(ts.TargetsJSON) > 0 {
			var rawTargets []struct {
				SourceID string `json:"source_id"`
				ReadID   string `json:"read_id"`
				Path     string `json:"path"`
			}
			if json.Unmarshal(ts.TargetsJSON, &rawTargets) == nil {
				for _, rt := range rawTargets {
					if rt.ReadID == "" {
						continue
					}
					cr, crErr := tx.CapturedRead(rt.ReadID)
					if crErr != nil {
						continue
					}
					var loc struct {
						Path string `json:"path"`
					}
					_ = json.Unmarshal(cr.LocationJSON, &loc)
					p := rt.Path
					if p == "" {
						p = loc.Path
					}
					cleanRP := strings.TrimPrefix(path.Clean(p), "/")
					var sm struct {
						SourceID      string `json:"source_id"`
						ExactRevision string `json:"exact_revision"`
					}
					_ = json.Unmarshal(cr.SourceJSON, &sm)
					sid := rt.SourceID
					if sid == "" {
						sid = sm.SourceID
					}
					avail := "available"
					if cr.BytesSHA == "" {
						avail = "unavailable"
					}
					info := boundReadInfo{
						readID:       cr.ID,
						sourceID:     sid,
						path:         cleanRP,
						availability: avail,
						hash:         cr.BytesSHA,
						exactRev:     sm.ExactRevision,
					}
					allBoundReads = append(allBoundReads, info)
					if sid != "" && cleanRP != "" {
						boundReadsBySourcePath[sid+"\x00"+cleanRP] = info
					}
				}
			}
		}
	}

	var existingReadIDs []string
	var existingBindings []domain.MaintenanceReadBinding
	seenReadID := map[string]bool{}
	var limitations []domain.MaintenanceLimitation
	seenLim := map[string]bool{}
	addLim := func(kind, reason, detail string) {
		if reason == "" {
			reason = "source_not_available"
		}
		key := kind + "\x00" + reason + "\x00" + cv.VersionID + "\x00" + detail
		if seenLim[key] {
			return
		}
		seenLim[key] = true
		limitations = append(limitations, domain.MaintenanceLimitation{
			Kind:            kind,
			Reason:          reason,
			TargetVersionID: cv.VersionID,
			Message:         detail,
		})
	}
	addExistingRead := func(info boundReadInfo) bool {
		if info.availability != "available" || info.hash == "" {
			return false
		}
		if obj, oErr := tx.Object(info.hash); oErr != nil || obj.State != "ready" {
			return false
		}
		if !seenReadID[info.readID] {
			seenReadID[info.readID] = true
			existingReadIDs = append(existingReadIDs, info.readID)
			existingBindings = append(existingBindings, domain.MaintenanceReadBinding{
				ReadID:      info.readID,
				ProjectID:   cv.ProjectID,
				VersionID:   cv.VersionID,
				TargetSetID: cv.TargetSetID,
			})
		}
		return true
	}
	if invalidExplicitContext {
		addLim("source_unavailable", "captured_context_mismatch", cv.TargetSetID)
	}
	if explicitContext != nil {
		for _, selected := range explicitContext.Selections {
			target, ok := targetBySource[selected.SourceID]
			for _, requested := range selected.Paths {
				info, found := boundReadsBySourcePath[selected.SourceID+"\x00"+requested.Path]
				if !ok || !found || info.availability != "available" || info.hash == "" {
					addLim("source_unavailable", "captured_read_unavailable", selected.SourceID+":"+requested.Path)
					continue
				}
				if target.Kind == "git" && (target.ResolvedRevision == "" || info.exactRev != target.ResolvedRevision) {
					addLim("source_unavailable", "captured_revision_mismatch", selected.SourceID+":"+requested.Path)
					continue
				}
				if pathsBySource[selected.SourceID] == nil {
					pathsBySource[selected.SourceID] = map[string]bool{}
				}
				pathsBySource[selected.SourceID][requested.Path] = true
			}
		}
	}

	// If no evidence paths were declared on the unit, check if a completed capture is bound to cv.TargetSetID.
	if len(pathsBySource) == 0 {
		if len(cv.Targets) == 0 {
			limitations = append(limitations, domain.MaintenanceLimitation{
				Kind:            "source_unverified",
				Reason:          "no_source_declared",
				TargetVersionID: cv.VersionID,
				Message:         "explanation_only_no_source_declared",
			})
		}
		for _, t := range cv.Targets {
			if t.Availability != "available" && t.Availability != "reassessment_required" {
				limKind := "source_unavailable"
				if t.Availability == "denied" || strings.Contains(t.Reason, "denied") || strings.Contains(t.Reason, "policy") {
					limKind = "source_disclosure_restricted"
				}
				addLim(limKind, t.Reason, t.SourceID)
			}
		}
		if hasCompletedCapture && len(allBoundReads) > 0 {
			for _, info := range allBoundReads {
				st, hasTarget := targetBySource[info.sourceID]
				approved, pErr := app.ManagedSourcePolicy(tx, cv.ProjectID, info.sourceID, "codex")
				if pErr != nil {
					addLim("source_disclosure_restricted", "source_policy_denied", info.sourceID)
					continue
				}
				if !hasTarget || (st.Availability != "available" && st.Availability != "reassessment_required") || (st.Kind != "" && approved.Kind != "" && st.Kind != approved.Kind) {
					addLim("source_unavailable", st.Reason, info.sourceID)
					continue
				}
				if approved.Kind == "git" && st.ResolvedRevision != "" && info.exactRev != "" && info.exactRev != st.ResolvedRevision {
					addLim("source_unavailable", "captured_revision_mismatch", info.sourceID)
					continue
				}
				if !addExistingRead(info) {
					addLim("source_unavailable", "captured_read_unavailable", info.sourceID+":"+info.path)
				}
			}
		}
		return nil, existingReadIDs, existingBindings, limitations
	}

	var selections []source.CaptureSelection
	var sids []string
	for sid := range pathsBySource {
		sids = append(sids, sid)
	}
	sort.Strings(sids)

	for _, sid := range sids {
		pset := pathsBySource[sid]
		if len(pset) == 0 {
			continue
		}
		st, hasTarget := targetBySource[sid]
		approved, pErr := app.ManagedSourcePolicy(tx, cv.ProjectID, sid, "codex")
		if pErr != nil {
			limKind := "source_unavailable"
			limReason := "source_not_available"
			if _, cfgOK := configuredBySource[sid]; cfgOK || errors.Is(pErr, domain.ErrDenied) || (hasTarget && st.Availability == "denied") {
				limKind = "source_disclosure_restricted"
				limReason = "source_policy_denied"
			}
			addLim(limKind, limReason, sid)
			continue
		}
		if !hasTarget || (st.Availability != "available" && st.Availability != "reassessment_required") || (st.Kind != "" && approved.Kind != "" && st.Kind != approved.Kind) {
			limKind := "source_unavailable"
			limReason := "source_not_available"
			if hasTarget {
				if st.Reason != "" {
					limReason = st.Reason
				}
				if st.Availability == "denied" || strings.Contains(st.Reason, "denied") || strings.Contains(st.Reason, "policy") {
					limKind = "source_disclosure_restricted"
				}
			}
			addLim(limKind, limReason, sid)
			continue
		}

		var sortedPaths []string
		for p := range pset {
			sortedPaths = append(sortedPaths, p)
		}
		sort.Strings(sortedPaths)

		var pathsToCapture []source.CapturePath
		for _, p := range sortedPaths {
			if info, found := boundReadsBySourcePath[sid+"\x00"+p]; found {
				if approved.Kind == "git" && st.ResolvedRevision != "" && info.exactRev != "" && info.exactRev != st.ResolvedRevision {
					// Bound read was from a different git revision; capture the scan-start revision if possible.
					if st.ResolvedRevision != "" {
						pathsToCapture = append(pathsToCapture, source.CapturePath{Path: p})
					} else {
						addLim("source_unavailable", "captured_revision_mismatch", sid+":"+p)
					}
					continue
				}
				if addExistingRead(info) {
					continue
				}
				addLim("source_unavailable", "captured_read_unavailable", sid+":"+p)
				continue
			}
			// Path was not found in bound reads from cv.TargetSetID.
			if approved.Kind == "git" {
				if st.ResolvedRevision == "" {
					addLim("source_unavailable", "git_revision_unresolved", sid+":"+p)
					continue
				}
				pathsToCapture = append(pathsToCapture, source.CapturePath{Path: p})
			} else if cv.SelectedMode == "current" && !hasCompletedCapture {
				pathsToCapture = append(pathsToCapture, source.CapturePath{Path: p})
			} else {
				// For pinned files sources (or partial pre-bound captures), never read live working-copy files outside scan-start retained reads.
				addLim("source_unavailable", "files_snapshot_not_retained", sid+":"+p)
			}
		}

		if len(pathsToCapture) > 0 {
			rev := ""
			if approved.Kind == "git" {
				if st.ResolvedRevision == "" {
					continue
				}
				rev = st.ResolvedRevision
			}
			selections = append(selections, source.CaptureSelection{
				SourceID: sid,
				Revision: rev,
				Paths:    pathsToCapture,
			})
		}
	}

	sort.Slice(selections, func(i, j int) bool { return selections[i].SourceID < selections[j].SourceID })
	return selections, existingReadIDs, existingBindings, limitations
}

// stepUnitSemanticEvaluation orchestrates bounded relationship/topic candidate discovery
// for an active semantic unit ('region', or 'page' when regionless, or 'version' when pageless).
func (w *Worker) stepUnitSemanticEvaluation(
	ctx context.Context,
	j domain.Job,
	run domain.ScanRun,
	item domain.JobItem,
	baseDetail map[string]any,
) error {
	var followUpCaptureJobID string
	err := w.Store.Write(ctx, func(tx app.WriteTx) error {
		curJob, err := tx.Job(j.ID)
		if err != nil {
			return err
		}
		if curJob.CancelRequested {
			return tx.ConfirmStop(j.ID, curJob.AttemptGeneration)
		}
		if curJob.State != "running" || curJob.AttemptGeneration != j.AttemptGeneration {
			return domain.ErrStaleWorker
		}
		curRun, err := tx.ScanRun(j.ID)
		if err != nil {
			return err
		}
		curItem, err := tx.JobItem(j.ID, item.ItemID)
		if err != nil {
			return err
		}
		if curItem.State != "running" && curItem.State != "pending" {
			return nil
		}
		now := w.now()
		nowStr := now.Format(time.RFC3339Nano)
		if elapsedMs, exhausted := w.currentElapsedAndBudget(tx, curRun, now); exhausted {
			return w.exhaustRunBudget(tx, curJob, curRun, elapsedMs, now)
		}
		if fReason, fState, purged, fenced := w.checkItemFence(tx, j, curRun, curItem); fenced {
			existing, hasExisting, e := tx.MaintenanceIntentByItem(j.ID, curItem.ItemID)
			if e != nil {
				return e
			}
			if hasExisting {
				if err := w.cancelSingleRepairChild(tx, existing.JobID, fReason, nowStr); err != nil {
					return err
				}
			}
			curItem.State = fState
			curItem.Reason = fReason
			curItem.Stage = "complete"
			if purged {
				curItem.PagePath = ""
				curItem.RegionID = ""
				curItem.Detail = []byte(`{"purged":true}`)
			}
			curItem.UpdatedAt = nowStr
			if err := tx.UpdateJobItem(curItem); err != nil {
				return err
			}
			curRun.UpdatedAt = nowStr
			return tx.UpdateScanRun(curRun)
		}

		cfg, err := tx.Configuration()
		if err != nil {
			return err
		}
		curPolicy, err := ResolveEffectivePolicy(cfg, curRun.RequestedScope.ProjectIDs, curRun.EffectivePolicy.Depth)
		if err != nil {
			return err
		}
		relMode := StricterAutoApplyMode(curRun.EffectivePolicy.AutoApply.RelatedExplanation, curPolicy.AutoApply.RelatedExplanation)
		topicMode := StricterAutoApplyMode(curRun.EffectivePolicy.AutoApply.TopicAssignment, curPolicy.AutoApply.TopicAssignment)
		contentMode := StricterContentGeneration(curRun.EffectivePolicy.ContentGeneration, curPolicy.ContentGeneration)
		maxAttempts := StricterPositiveInt(curRun.EffectivePolicy.MaxProviderAttemptsPerRun, curPolicy.MaxProviderAttemptsPerRun)

		baseDetail["structural_ready"] = true
		completeWithDetail := func(semState, semReason string) error {
			d := map[string]any{}
			var prev map[string]any
			if len(curItem.Detail) > 0 && json.Unmarshal(curItem.Detail, &prev) == nil {
				for _, keepKey := range []string{
					"structural_ready",
					"nomination_coverage",
					"visited_nomination_batches",
					"nominated_candidates",
					"nomination_abstained",
					"eligible_candidates_at_apply",
					"filtered_relations",
					"downgraded_relations",
					"filtered_topics",
					"filtered_gaps",
					"proposal_decisions",
					"limitations",
					"finding_ids",
				} {
					if v, ok := prev[keepKey]; ok {
						d[keepKey] = v
					}
				}
			}
			for k, v := range baseDetail {
				d[k] = v
			}
			d["semantic_state"] = semState
			if semReason != "" {
				d["semantic_reason"] = semReason
			}
			raw, _ := json.Marshal(d)
			curItem.State = "completed"
			curItem.Reason = "ok"
			curItem.Stage = "complete"
			curItem.Detail = raw
			curItem.UpdatedAt = nowStr
			if err := tx.UpdateJobItem(curItem); err != nil {
				return err
			}
			curRun.UpdatedAt = nowStr
			return tx.UpdateScanRun(curRun)
		}

		if relMode == "disabled" && topicMode == "disabled" && contentMode == "disabled" {
			existing, hasExisting, e := tx.MaintenanceIntentByItem(j.ID, curItem.ItemID)
			if e != nil {
				return e
			}
			if hasExisting {
				if err := w.cancelSingleRepairChild(tx, existing.JobID, "policy_disabled", nowStr); err != nil {
					return err
				}
			}
			return completeWithDetail("skipped", "policy_disabled")
		}
		if !projectHasProviderID(cfg.Value, curItem.ProjectID, "codex") {
			existing, hasExisting, e := tx.MaintenanceIntentByItem(j.ID, curItem.ItemID)
			if e != nil {
				return e
			}
			if hasExisting {
				if err := w.cancelSingleRepairChild(tx, existing.JobID, "provider_not_configured", nowStr); err != nil {
					return err
				}
			}
			return completeWithDetail("skipped", "provider_not_configured")
		}

		// Pass 1 (structural readiness): record structural_ready and yield until all
		// non-ready items in the scan finish before global nomination / Stage 2.
		if !isStructuralReadyItem(curItem) {
			if w.Identity == nil {
				return completeWithDetail("skipped", "provider_unavailable")
			}
			focalCV, focalDoc, _, ok := w.checkCandidateTargetFence(tx, curRun, curItem.VersionID, "codex")
			if !ok {
				return completeWithDetail("skipped", "target_fenced")
			}
			if _, expOK := w.resolveUnitExplanation(tx, focalCV, focalDoc.ID, curItem.PagePath, curItem.RegionID, "focal", ""); !expOK {
				return completeWithDetail("skipped", "explanation_unavailable")
			}
			rawDetail, _ := json.Marshal(baseDetail)
			curItem.State = "pending"
			curItem.Stage = "inspect"
			curItem.Detail = rawDetail
			curItem.UpdatedAt = nowStr
			if err := tx.UpdateJobItem(curItem); err != nil {
				return err
			}
			curRun.UpdatedAt = nowStr
			return tx.UpdateScanRun(curRun)
		}

		// Check if a Stage-2 maintenance child job already exists for this item.
		if existing, hasExisting, e := tx.MaintenanceIntentByItem(j.ID, curItem.ItemID); e == nil && hasExisting && existing.Mode == "" {
			child, cErr := tx.Job(existing.JobID)
			if cErr != nil {
				return cErr
			}
			switch child.State {
			case "completed":
				baseDetail["eval_job_id"] = child.ID
				baseDetail["shortlisted_candidates"] = len(existing.Shortlist.Candidates)
				baseDetail["inspected_candidates"] = len(existing.Shortlist.Candidates)
				if child.ProviderAttempts != nil {
					baseDetail["provider_attempts_used"] = child.ProviderAttempts.Count
				}
				return completeWithDetail("assessed", "")
			case "queued", "running", "reconciling", "cancelling":
				curItem.State = "pending"
				curItem.Stage = "inspect"
				curItem.UpdatedAt = nowStr
				return tx.UpdateJobItem(curItem)
			case "paused":
				if child.Error == "source_capture_pending" {
					if w.Sources != nil {
						followUpCaptureJobID = child.ID
						curItem.State = "pending"
						curItem.Stage = "inspect"
						curItem.UpdatedAt = nowStr
						return tx.UpdateJobItem(curItem)
					}
					if err := w.cancelSingleRepairChild(tx, child.ID, "source_capture_unavailable", nowStr); err != nil {
						return err
					}
					return completeWithDetail("skipped", "provider_unavailable")
				}
				if child.ProviderAttempts != nil && (child.ProviderAttempts.Unknown > 0 || child.ProviderAttempts.LatestState == "result_saved") {
					curItem.State = "pending"
					curItem.Stage = "inspect"
					curItem.UpdatedAt = nowStr
					if err := tx.UpdateJobItem(curItem); err != nil {
						return err
					}
					reason := child.Error
					if reason == "" {
						reason = "outcome_unknown"
					}
					return tx.PauseProviderJob(j.ID, reason)
				}
				switch child.Error {
				case "outcome_unknown", "saved_result_requires_continuation", "reconciliation_required", "authority_reconciliation_required", "preparation_failed", "captured_input_unavailable", "captured_input_invalid":
					curItem.State = "pending"
					curItem.Stage = "inspect"
					curItem.UpdatedAt = nowStr
					if err := tx.UpdateJobItem(curItem); err != nil {
						return err
					}
					return tx.PauseProviderJob(j.ID, child.Error)
				default:
					if err := w.cancelSingleRepairChild(tx, child.ID, child.Error, nowStr); err != nil {
						return err
					}
					baseDetail["eval_job_id"] = child.ID
					return completeWithDetail("skipped", "provider_unavailable")
				}
			default: // failed or cancelled
				baseDetail["eval_job_id"] = child.ID
				return completeWithDetail("skipped", "provider_unavailable")
			}
		}

		if w.Identity == nil {
			return completeWithDetail("skipped", "provider_unavailable")
		}

		// Step global semantic nomination prephase across all structural_ready units before Stage 2.
		nomStatus, err := w.stepGlobalNominationPrephase(tx, curJob, &curRun, cfg.Value, maxAttempts, nowStr)
		if err != nil {
			return err
		}
		if !nomStatus.Settled {
			curItem.State = "pending"
			curItem.Stage = "inspect"
			curItem.UpdatedAt = nowStr
			return tx.UpdateJobItem(curItem)
		}
		baseDetail["nomination_coverage"] = nomStatus.Coverage
		if len(nomStatus.VisitedBatchIDs) > 0 {
			baseDetail["visited_nomination_batches"] = nomStatus.VisitedBatchIDs
		}
		if agg, hasAgg := nomStatus.ByItemID[curItem.ItemID]; hasAgg {
			baseDetail["nominated_candidates"] = len(agg.Candidates)
			baseDetail["nomination_abstained"] = agg.Abstained
		}

		// Check aggregate provider attempt budget before creating a new Stage-2 maintenance child job.
		usedAttempts, err := tx.ScanProviderAttemptCount(j.ID, "")
		if err != nil {
			return err
		}
		if maxAttempts > 0 && usedAttempts >= maxAttempts {
			curRun.Report.BudgetExhausted = true
			if err := tx.UpdateScanRun(curRun); err != nil {
				return err
			}
			return completeWithDetail("skipped", "provider_attempt_budget_exhausted")
		}

		focalCV, focalDoc, _, ok := w.checkCandidateTargetFence(tx, curRun, curItem.VersionID, "codex")
		if !ok {
			return completeWithDetail("skipped", "target_fenced")
		}
		focalSig, err := extractScopeThemes(tx, focalCV, curItem.PagePath, curItem.RegionID)
		if err != nil {
			return err
		}
		shortlist, skippedCount, err := w.shortlistCandidates(tx, curRun, curItem, focalSig)
		if err != nil {
			return err
		}
		shortlist = w.mergeNominatedShortlist(tx, curRun, curItem, shortlist, nomStatus)
		shortlist = w.appendSiblingFallbackIfEmpty(tx, curRun, curItem, focalCV, focalDoc, shortlist)

		var explanations []domain.MaintenanceExplanation
		focalExp, ok := w.resolveUnitExplanation(tx, focalCV, focalDoc.ID, curItem.PagePath, curItem.RegionID, "focal", focalSig.Title)
		if !ok {
			return completeWithDetail("skipped", "explanation_unavailable")
		}
		explanations = append(explanations, focalExp)

		type verCaptureGroup struct {
			projectID  string
			versionID  string
			sourceMode string
			selections map[string]map[string]string
			revisions  map[string]string
		}
		captureGroups := map[string]*verCaptureGroup{}
		var preReadIDs []string
		var preBindings []domain.MaintenanceReadBinding
		var initialLimitations []domain.MaintenanceLimitation
		seenPreReads := map[string]bool{}
		seenLimKey := map[string]bool{}

		addUnitSources := func(uCV domain.ScanCapturedVersion, pPath, rID string) {
			if uCV.ProjectID == "" || uCV.VersionID == "" {
				return
			}
			sels, rIDs, binds, lims := w.collectUnitSourceSelections(tx, curRun, uCV, pPath, rID)
			for _, lim := range lims {
				lk := lim.Kind + "\x00" + lim.Reason + "\x00" + lim.TargetVersionID + "\x00" + lim.Message
				if !seenLimKey[lk] {
					seenLimKey[lk] = true
					initialLimitations = append(initialLimitations, lim)
				}
			}
			for idx, rid := range rIDs {
				if !seenPreReads[rid] {
					seenPreReads[rid] = true
					preReadIDs = append(preReadIDs, rid)
					preBindings = append(preBindings, binds[idx])
				}
			}
			if len(sels) == 0 {
				return
			}
			grpKey := uCV.ProjectID + "\x00" + uCV.VersionID
			grp := captureGroups[grpKey]
			if grp == nil {
				mode := uCV.SelectedMode
				if mode != "current" {
					mode = "pinned"
				}
				grp = &verCaptureGroup{
					projectID:  uCV.ProjectID,
					versionID:  uCV.VersionID,
					sourceMode: mode,
					selections: map[string]map[string]string{},
					revisions:  map[string]string{},
				}
				captureGroups[grpKey] = grp
			}
			for _, sel := range sels {
				if grp.selections[sel.SourceID] == nil {
					grp.selections[sel.SourceID] = map[string]string{}
				}
				for _, p := range sel.Paths {
					if _, exists := grp.selections[sel.SourceID][p.Path]; !exists || grp.selections[sel.SourceID][p.Path] == "" {
						grp.selections[sel.SourceID][p.Path] = p.ReadID
					}
				}
				if sel.Revision != "" && grp.revisions[sel.SourceID] == "" {
					grp.revisions[sel.SourceID] = sel.Revision
				}
			}
		}

		addUnitSources(focalCV, curItem.PagePath, curItem.RegionID)

		var vettedCandidates []domain.CandidateTarget
		for _, cand := range shortlist.Candidates {
			candCV, candDoc, _, cOK := w.checkCandidateTargetFence(tx, curRun, cand.VersionID, "codex")
			if !cOK {
				skippedCount++
				continue
			}
			candExp, expOK := w.resolveUnitExplanation(tx, candCV, candDoc.ID, cand.PagePath, cand.RegionID, "candidate", cand.Title)
			if !expOK {
				skippedCount++
				continue
			}
			explanations = append(explanations, candExp)
			addUnitSources(candCV, cand.PagePath, cand.RegionID)
			vettedCandidates = append(vettedCandidates, cand)
		}
		shortlist.Candidates = vettedCandidates

		var captureSteps []domain.MaintenanceCaptureStep
		for _, grp := range captureGroups {
			var sels []source.CaptureSelection
			for sid, pset := range grp.selections {
				var paths []source.CapturePath
				for p, readID := range pset {
					paths = append(paths, source.CapturePath{Path: p, ReadID: readID})
				}
				sort.Slice(paths, func(i, j int) bool { return paths[i].Path < paths[j].Path })
				sels = append(sels, source.CaptureSelection{
					SourceID: sid,
					Revision: grp.revisions[sid],
					Paths:    paths,
				})
			}
			sort.Slice(sels, func(i, j int) bool { return sels[i].SourceID < sels[j].SourceID })
			opID, err := service.NewID()
			if err != nil {
				return err
			}
			vID := grp.versionID
			selJSON, err := json.Marshal(source.CaptureRequest{
				SourceMode: grp.sourceMode,
				VersionID:  vID,
				ProviderID: "codex",
				Selections: sels,
			})
			if err != nil {
				return err
			}
			captureSteps = append(captureSteps, domain.MaintenanceCaptureStep{
				ProjectID:       grp.projectID,
				OperationID:     opID,
				SourceMode:      grp.sourceMode,
				VersionID:       vID,
				VersionIDs:      []string{vID},
				SourceSelection: selJSON,
			})
		}
		sort.Slice(captureSteps, func(i, j int) bool {
			if captureSteps[i].ProjectID != captureSteps[j].ProjectID {
				return captureSteps[i].ProjectID < captureSteps[j].ProjectID
			}
			return captureSteps[i].VersionID < captureSteps[j].VersionID
		})

		childID, err := service.NewID()
		if err != nil {
			return err
		}
		var pagePtr, regPtr *string
		if curItem.PagePath != "" {
			p := curItem.PagePath
			pagePtr = &p
		}
		if curItem.RegionID != "" {
			r := curItem.RegionID
			regPtr = &r
		}
		var sourcePathList []string
		for sp := range focalSig.SourcePaths {
			sourcePathList = append(sourcePathList, sp)
		}
		sort.Strings(sourcePathList)
		promptCandidates := make([]map[string]any, 0, len(shortlist.Candidates))
		for _, cand := range shortlist.Candidates {
			cm := map[string]any{
				"project_id":    cand.ProjectID,
				"document_id":   cand.DocumentID,
				"version_id":    cand.VersionID,
				"page_path":     cand.PagePath,
				"region_id":     cand.RegionID,
				"unit_kind":     cand.UnitKind,
				"target_set_id": cand.TargetSetID,
				"title":         cand.Title,
				"score":         cand.Score,
				"signals":       cand.Signals,
			}
			if _, cHeadings, _, cBody, sErr := tx.ScanSearchText(cand.VersionID, cand.PagePath, cand.RegionID); sErr == nil {
				if len(cHeadings) > 0 {
					cm["headings"] = cHeadings
				}
				cleanExcerpt := strings.TrimSpace(cBody)
				if len(cleanExcerpt) > 1200 {
					cleanExcerpt = cleanExcerpt[:1200]
				}
				if cleanExcerpt != "" {
					cm["excerpt"] = cleanExcerpt
				}
			}
			promptCandidates = append(promptCandidates, cm)
		}
		promptPayload, _ := json.Marshal(map[string]any{
			"unit_kind":      curItem.UnitKind,
			"project_id":     curItem.ProjectID,
			"document_id":    curItem.DocumentID,
			"version_id":     curItem.VersionID,
			"page_path":      curItem.PagePath,
			"region_id":      curItem.RegionID,
			"title":          focalSig.Title,
			"focal_headings": focalSig.Headings,
			"focal_excerpt":  focalSig.Excerpt,
			"themes":         shortlist.Themes,
			"source_paths":   sourcePathList,
			"candidates":     promptCandidates,
			"requested_scope": map[string]any{
				"scope":       curRun.RequestedScope.Scope,
				"project_ids": curRun.RequestedScope.ProjectIDs,
				"version_id":  curRun.RequestedScope.VersionID,
				"page_path":   curRun.RequestedScope.PagePath,
				"region_id":   curRun.RequestedScope.RegionID,
				"source_mode": focalCV.SelectedMode,
			},
		})
		maintInv := &domain.MaintenanceInvocation{
			ScanJobID:    j.ID,
			ItemID:       curItem.ItemID,
			UnitKind:     curItem.UnitKind,
			ProjectID:    curItem.ProjectID,
			DocumentID:   curItem.DocumentID,
			VersionID:    curItem.VersionID,
			PagePath:     pagePtr,
			RegionID:     regPtr,
			SourceMode:   focalCV.SelectedMode,
			Explanations: explanations,
			ReadBindings: preBindings,
		}
		inv := domain.ProviderInvocation{
			Prompt:      string(promptPayload),
			Maintenance: maintInv,
		}
		docIDCopy := curItem.DocumentID
		targetBytes, _ := json.Marshal(map[string]any{
			"action":      "maintenance",
			"project_id":  curItem.ProjectID,
			"document_id": &docIDCopy,
			"version_id":  curItem.VersionID,
			"page_path":   pagePtr,
			"region_id":   regPtr,
			"scan_job_id": j.ID,
			"item_id":     curItem.ItemID,
		})
		var readsForSHA []domain.CapturedRead
		for _, rid := range preReadIDs {
			r, err := tx.CapturedRead(rid)
			if err != nil {
				return err
			}
			readsForSHA = append(readsForSHA, r)
		}
		identityFn := w.Identity
		reqSHA, err := identityFn(curRun.PrincipalID, targetBytes, inv, readsForSHA)
		if err != nil {
			return err
		}
		authState, err := tx.AuthState()
		if err != nil {
			return err
		}
		credScope := curRun.CapturedScope
		if curRun.AuthKind == "owner" {
			credScope = domain.CredentialScope{Kind: "owner"}
		} else if len(preReadIDs) > 0 || len(captureSteps) > 0 {
			if !containsScanAction(credScope.Actions, "source.read") {
				// Workflow principal without source.read runs explanation-only.
				preReadIDs = nil
				preBindings = nil
				maintInv.ReadBindings = nil
				captureSteps = nil
				reqSHA, err = identityFn(curRun.PrincipalID, targetBytes, inv, nil)
				if err != nil {
					return err
				}
			}
		}
		singleTS := ""
		if len(preBindings) > 0 {
			firstTS := preBindings[0].TargetSetID
			allSame := true
			for _, b := range preBindings[1:] {
				if b.TargetSetID != firstTS {
					allSame = false
					break
				}
			}
			if allSame {
				singleTS = firstTS
			}
		}
		pScope := domain.ProviderScope{
			Purpose:         "body",
			PrincipalID:     curRun.PrincipalID,
			AuthEpoch:       authState.AuthEpoch,
			CredentialScope: credScope,
			ProviderID:      "codex",
			TargetSetID:     singleTS,
			ReadIDs:         preReadIDs,
			RequestSHA:      reqSHA,
			Target:          targetBytes,
			Invocation:      &inv,
		}
		scopeBytes, _ := json.Marshal(pScope)
		perms := map[string]int64{}
		for _, exp := range explanations {
			if rev, ok := curRun.PermissionsRevisions[exp.ProjectID]; ok {
				perms[exp.ProjectID] = rev
			}
		}
		admissionReady := len(captureSteps) == 0
		if initialLimitations == nil {
			initialLimitations = []domain.MaintenanceLimitation{}
		}
		intent := domain.MaintenanceIntent{
			JobID:          childID,
			ScanJobID:      j.ID,
			ItemID:         curItem.ItemID,
			UnitKind:       curItem.UnitKind,
			ProjectID:      curItem.ProjectID,
			DocumentID:     curItem.DocumentID,
			VersionID:      curItem.VersionID,
			PagePath:       curItem.PagePath,
			RegionID:       curItem.RegionID,
			SourceMode:     focalCV.SelectedMode,
			AdmissionReady: admissionReady,
			Shortlist:      shortlist,
			Captures:       captureSteps,
			SourceReadIDs:  preReadIDs,
			ReadBindings:   preBindings,
			Limitations:    initialLimitations,
		}
		if err := tx.CreateMaintenanceJob(domain.Job{
			ID:             childID,
			Kind:           "maintenance",
			ParentID:       j.ID,
			State:          "queued",
			Scope:          scopeBytes,
			ConfigRevision: curRun.ConfigRevision,
			Permissions:    perms,
		}, intent); err != nil {
			return err
		}
		if len(preReadIDs) > 0 {
			if err := tx.AttachSourceInputs(childID, preReadIDs); err != nil {
				return err
			}
		}
		baseDetail["eval_job_id"] = childID
		baseDetail["shortlisted_candidates"] = len(shortlist.Candidates)
		baseDetail["skipped_candidates"] = skippedCount
		baseDetail["semantic_state"] = "pending"
		rawDetail, _ := json.Marshal(baseDetail)
		curItem.State = "pending"
		curItem.Stage = "inspect"
		curItem.Detail = rawDetail
		curItem.UpdatedAt = nowStr
		if err := tx.UpdateJobItem(curItem); err != nil {
			return err
		}
		if !admissionReady && w.Sources != nil {
			followUpCaptureJobID = childID
		}
		curRun.UpdatedAt = nowStr
		return tx.UpdateScanRun(curRun)
	})
	if err != nil {
		return err
	}
	if followUpCaptureJobID != "" && w.Sources != nil && w.Identity != nil {
		if capErr := w.Sources.CaptureMaintenanceAndAdmit(ctx, followUpCaptureJobID, w.Identity); capErr != nil {
			var unpersistedPause bool
			_ = w.Store.Write(ctx, func(tx app.WriteTx) error {
				if cj, e := tx.Job(followUpCaptureJobID); e == nil && cj.State == "paused" && cj.Error == "source_capture_pending" {
					unpersistedPause = true
				}
				return nil
			})
			if unpersistedPause {
				return capErr
			}
		}
	}
	return nil
}

func candidateUnitKeyString(versionID, pagePath, regionID string) string {
	return versionID + "\x00" + pagePath + "\x00" + regionID
}

func computeMaintenanceInputDigest(attemptID, resultHash string, pInputs []domain.ProviderInput) string {
	type inputRecord struct {
		ReadID     string `json:"read_id"`
		Hash       string `json:"hash"`
		Origin     string `json:"origin"`
		ObservedAt string `json:"observed_at"`
	}
	records := make([]inputRecord, 0, len(pInputs))
	for _, pi := range pInputs {
		obs := ""
		if pi.ObservedAt != nil {
			obs = *pi.ObservedAt
		}
		records = append(records, inputRecord{
			ReadID:     pi.ReadID,
			Hash:       pi.Hash,
			Origin:     pi.ObservedOrigin,
			ObservedAt: obs,
		})
	}
	sort.Slice(records, func(i, j int) bool {
		return records[i].ReadID < records[j].ReadID
	})
	raw, _ := json.Marshal(struct {
		AttemptID  string        `json:"attempt_id"`
		ResultHash string        `json:"result_hash"`
		Inputs     []inputRecord `json:"inputs"`
	}{
		AttemptID:  attemptID,
		ResultHash: resultHash,
		Inputs:     records,
	})
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

type preparedSourceReadTask struct {
	unitKeys   []string
	verID      string
	tsID       string
	sourceID   string
	repoKey    string
	path       string
	cleanPath  string
	isManifest bool
	readID     string
	origin     string
	sha        string
	size       int64
	obj        domain.Object
}

func unitEvidenceSourcePaths(tx app.WriteTx, versionID, pagePath, regionID string) map[string]bool {
	out := map[string]bool{}
	extract := func(ev domain.Evidence) {
		var meta struct {
			Locator struct {
				Path string `json:"path"`
			} `json:"locator"`
			Path string `json:"path"`
		}
		if json.Unmarshal(ev.Metadata, &meta) == nil {
			p := meta.Locator.Path
			if p == "" {
				p = meta.Path
			}
			if p != "" {
				cleanP := strings.TrimPrefix(path.Clean(p), "/")
				if cleanP != "" && cleanP != "." && !strings.HasPrefix(cleanP, "..") {
					out[cleanP] = true
				}
			}
		}
	}
	linkedEvIDs := map[string]bool{}
	if links, err := tx.VersionRegionEvidence(versionID); err == nil {
		for _, link := range links {
			linkedEvIDs[link.EvidenceID] = true
			if pagePath != "" && link.PagePath != pagePath {
				continue
			}
			if regionID != "" && link.RegionID != regionID {
				continue
			}
			if ev, err := tx.QueryEvidence(versionID, link.EvidenceID); err == nil {
				extract(ev)
			}
		}
	}
	if regionID == "" {
		if evs, err := tx.VersionEvidence(versionID); err == nil {
			for _, ev := range evs {
				if !linkedEvIDs[ev.ID] {
					extract(ev)
				}
			}
		}
	}
	return out
}

func isModuleManifestPath(cleanPath string) bool {
	base := path.Base(cleanPath)
	return base == "go.mod" || base == "package.json" || base == "pyproject.toml" || base == "Cargo.toml"
}

// PrepareMaintenance reads and validates the saved provider output and all observed
// source objects outside the SQLite writer, computing exact dependency facts without
// truncation or writer-transaction object I/O.
func (w *Worker) PrepareMaintenance(ctx context.Context, attempt domain.ProviderAttempt) error {
	if handled, err := w.prepareFreshness(ctx, attempt); handled || err != nil {
		return err
	}
	var a domain.ProviderAttempt
	var obj domain.Object
	var intent domain.MaintenanceIntent
	var isMaint bool
	var alreadyPrepared bool
	var inputDigest string
	var readTasks []preparedSourceReadTask
	partialSourceVersions := map[string]bool{}
	partialSourceUnits := map[string]bool{}

	err := w.Store.Write(ctx, func(tx app.WriteTx) error {
		j, err := tx.Job(attempt.JobID)
		if err != nil {
			return err
		}
		if j.Kind != "maintenance" {
			return nil
		}
		isMaint = true
		a, err = tx.ProviderAttempt(attempt.ID)
		if err != nil {
			return err
		}
		if a.State != "result_saved" || a.ResultHash == "" {
			return domain.ErrStaleWorker
		}
		intent, err = tx.MaintenanceIntent(j.ID)
		if err != nil {
			return err
		}
		if j.State == "queued" {
			if err := app.GuardProviderContinuation(tx, a); err != nil {
				return err
			}
		} else {
			if err := app.GuardProvider(tx, a); err != nil {
				return err
			}
		}
		pInputs, err := tx.ProviderInputs(a.ID)
		if err != nil {
			return err
		}
		inputDigest = computeMaintenanceInputDigest(a.ID, a.ResultHash, pInputs)
		if intent.Mode == domain.MaintenanceModeSemanticNomination {
			inputDigest = inputDigest + "|" + intent.CardPayloadDigest
			if intent.PreparedAttemptID == a.ID && len(intent.PreparedOutputJSON) > 0 {
				var existing domain.PreparedMaintenanceNominationPayload
				if json.Unmarshal(intent.PreparedOutputJSON, &existing) == nil && existing.InputDigest == inputDigest && existing.CardPayloadDigest == intent.CardPayloadDigest {
					alreadyPrepared = true
					return nil
				}
			}
			obj, err = tx.Object(a.ResultHash)
			return err
		}
		if intent.PreparedAttemptID == a.ID && len(intent.PreparedOutputJSON) > 0 {
			var existing domain.PreparedMaintenancePayload
			if json.Unmarshal(intent.PreparedOutputJSON, &existing) == nil && existing.InputDigest == inputDigest {
				alreadyPrepared = true
				return nil
			}
		}
		obj, err = tx.Object(a.ResultHash)
		if err != nil {
			return err
		}
		var scope domain.ProviderScope
		_ = json.Unmarshal(j.Scope, &scope)

		for _, lim := range intent.Limitations {
			if lim.Kind == "source_unavailable" || lim.Kind == "source_disclosure_restricted" || lim.Kind == "partial_source" {
				tv := lim.TargetVersionID
				if tv == "" {
					tv = intent.VersionID
				}
				partialSourceVersions[tv] = true
			}
		}

		type evalUnitDesc struct {
			uKey      string
			projectID string
			verID     string
			pagePath  string
			regionID  string
		}
		focalUKey := candidateUnitKeyString(intent.VersionID, intent.PagePath, intent.RegionID)
		allUnits := []evalUnitDesc{
			{
				uKey:      focalUKey,
				projectID: intent.ProjectID,
				verID:     intent.VersionID,
				pagePath:  intent.PagePath,
				regionID:  intent.RegionID,
			},
		}
		seenUnit := map[string]bool{focalUKey: true}
		verProject := map[string]string{intent.VersionID: intent.ProjectID}
		orderedVers := []string{intent.VersionID}
		seenVer := map[string]bool{intent.VersionID: true}
		for _, cand := range intent.Shortlist.Candidates {
			if cand.VersionID == "" {
				continue
			}
			verProject[cand.VersionID] = cand.ProjectID
			if !seenVer[cand.VersionID] {
				seenVer[cand.VersionID] = true
				orderedVers = append(orderedVers, cand.VersionID)
			}
			ck := candidateUnitKeyString(cand.VersionID, cand.PagePath, cand.RegionID)
			if !seenUnit[ck] {
				seenUnit[ck] = true
				allUnits = append(allUnits, evalUnitDesc{
					uKey:      ck,
					projectID: cand.ProjectID,
					verID:     cand.VersionID,
					pagePath:  cand.PagePath,
					regionID:  cand.RegionID,
				})
			}
		}

		evPathsByUnit := map[string]map[string]bool{}
		unitsByVer := map[string][]evalUnitDesc{}
		verHasAnyRegionEv := map[string]bool{}
		for _, verID := range orderedVers {
			if links, lErr := tx.VersionRegionEvidence(verID); lErr == nil && len(links) > 0 {
				verHasAnyRegionEv[verID] = true
			}
		}
		for _, u := range allUnits {
			evPathsByUnit[u.uKey] = unitEvidenceSourcePaths(tx, u.verID, u.pagePath, u.regionID)
			unitsByVer[u.verID] = append(unitsByVer[u.verID], u)
		}

		for _, pi := range pInputs {
			tsID, projID, verID, bOK := domain.MaintenanceReadTarget(scope, a.TargetSetID, intent.ProjectID, pi.ReadID)
			if !bOK {
				partialSourceVersions[intent.VersionID] = true
				partialSourceUnits[focalUKey] = true
				continue
			}
			read, rErr := tx.CapturedRead(pi.ReadID)
			var loc struct {
				Path string `json:"path"`
			}
			if rErr == nil {
				_ = json.Unmarshal(read.LocationJSON, &loc)
			}
			cleanLocPath := strings.TrimPrefix(path.Clean(loc.Path), "/")
			isManifest := isModuleManifestPath(cleanLocPath)

			var targetVerIDs []string
			if verID != "" {
				targetVerIDs = []string{verID}
			} else {
				for _, v := range orderedVers {
					if verProject[v] != projID {
						continue
					}
					if isManifest {
						targetVerIDs = append(targetVerIDs, v)
						continue
					}
					matched := false
					for _, u := range unitsByVer[v] {
						uPaths := evPathsByUnit[u.uKey]
						if uPaths[cleanLocPath] || (len(uPaths) == 0 && !verHasAnyRegionEv[v] && len(unitsByVer[v]) == 1) {
							matched = true
							break
						}
					}
					if matched {
						targetVerIDs = append(targetVerIDs, v)
					}
				}
				if len(targetVerIDs) == 0 {
					targetVerIDs = []string{intent.VersionID}
				}
			}

			if pi.ObservedAt == nil || *pi.ObservedAt == "" || (pi.ObservedOrigin != "daemon_supplied" && pi.ObservedOrigin != "native_observed") {
				for _, tv := range targetVerIDs {
					partialSourceVersions[tv] = true
					for _, u := range unitsByVer[tv] {
						partialSourceUnits[u.uKey] = true
					}
				}
				continue
			}
			if rErr != nil || read.BytesSHA == "" || read.BytesSHA != pi.Hash {
				for _, tv := range targetVerIDs {
					partialSourceVersions[tv] = true
					for _, u := range unitsByVer[tv] {
						partialSourceUnits[u.uKey] = true
					}
				}
				continue
			}
			var srcMeta struct {
				SourceID string `json:"source_id"`
				RepoID   string `json:"repo_id"`
			}
			_ = json.Unmarshal(read.SourceJSON, &srcMeta)
			sourceID := srcMeta.SourceID
			var byteSize int64
			if scr, scErr := tx.SourceCaptureRead(pi.ReadID); scErr == nil {
				if scr.SourceID != "" {
					sourceID = scr.SourceID
				}
				byteSize = scr.Size
			}
			repoKey := ""
			if srcMeta.RepoID != "" {
				repoKey = "git:" + srcMeta.RepoID
			} else if projID != "" && sourceID != "" {
				repoKey = "src:" + projID + ":" + sourceID
			} else if sourceID != "" {
				repoKey = "src::" + sourceID
			}
			o, oErr := tx.Object(read.BytesSHA)
			if oErr != nil || o.State != "ready" {
				for _, tv := range targetVerIDs {
					partialSourceVersions[tv] = true
					for _, u := range unitsByVer[tv] {
						partialSourceUnits[u.uKey] = true
					}
				}
				continue
			}
			if byteSize == 0 {
				byteSize = o.Size
			}
			for _, tv := range targetVerIDs {
				var matchedUnits []string
				for _, u := range unitsByVer[tv] {
					uPaths := evPathsByUnit[u.uKey]
					if uPaths[cleanLocPath] || (len(uPaths) == 0 && !verHasAnyRegionEv[tv] && len(unitsByVer[tv]) == 1) {
						matchedUnits = append(matchedUnits, u.uKey)
					}
				}
				if len(matchedUnits) == 0 && !isManifest {
					continue
				}
				readTasks = append(readTasks, preparedSourceReadTask{
					unitKeys:   matchedUnits,
					verID:      tv,
					tsID:       tsID,
					sourceID:   sourceID,
					repoKey:    repoKey,
					path:       loc.Path,
					cleanPath:  cleanLocPath,
					isManifest: isManifest,
					readID:     pi.ReadID,
					origin:     pi.ObservedOrigin,
					sha:        read.BytesSHA,
					size:       byteSize,
					obj:        o,
				})
			}
		}
		for verID, isPartial := range partialSourceVersions {
			if isPartial {
				for _, u := range unitsByVer[verID] {
					partialSourceUnits[u.uKey] = true
				}
			}
		}
		return nil
	})
	if err != nil || !isMaint || alreadyPrepared {
		return err
	}
	if w.Bytes == nil {
		return domain.ErrNotFound
	}
	var rawOut []byte
	err = w.Bytes.Read(ctx, obj, domain.Pin{Kind: "provider_result", OwnerID: a.ID}, func(r io.ReadSeeker) error {
		var readErr error
		rawOut, readErr = io.ReadAll(r)
		return readErr
	})
	if err != nil {
		return err
	}
	if obj.Size > 0 && int64(len(rawOut)) != obj.Size {
		return ErrMaintenanceInvalid
	}
	if intent.Mode == domain.MaintenanceModeSemanticNomination {
		_, resolvedByItem, err := decodeMaintenanceNominationOutput(rawOut, intent.Cards)
		if err != nil {
			return err
		}
		visited := make([]string, len(intent.Cards))
		for i, c := range intent.Cards {
			visited[i] = c.ItemID
		}
		cov := intent.NominationReason
		if cov == "" {
			cov = "single_batch"
		}
		normalized, err := json.Marshal(domain.PreparedMaintenanceNominationPayload{
			BatchID:           intent.BatchID,
			Pass:              intent.BatchPass,
			Coverage:          cov,
			InputDigest:       inputDigest,
			CardPayloadDigest: intent.CardPayloadDigest,
			VisitedItemIDs:    visited,
			Shortlists:        resolvedByItem,
		})
		if err != nil {
			return err
		}
		return w.Store.Write(ctx, func(tx app.WriteTx) error {
			cur, err := tx.ProviderAttempt(a.ID)
			if err != nil {
				return err
			}
			j, err := tx.Job(a.JobID)
			if err != nil {
				return err
			}
			if j.State == "queued" {
				if err := app.GuardProviderContinuation(tx, cur); err != nil {
					return err
				}
			} else {
				if err := app.GuardProvider(tx, cur); err != nil {
					return err
				}
			}
			pInputs, err := tx.ProviderInputs(a.ID)
			if err != nil {
				return err
			}
			curIntent, err := tx.MaintenanceIntent(a.JobID)
			if err != nil {
				return err
			}
			if computeMaintenanceInputDigest(cur.ID, cur.ResultHash, pInputs)+"|"+curIntent.CardPayloadDigest != inputDigest {
				return domain.ErrReconciliation
			}
			return tx.RecordPreparedMaintenanceOutput(a.JobID, a.ID, normalized)
		})
	}
	env, err := decodeMaintenanceOutput(rawOut)
	if err != nil {
		return err
	}

	manifestFilesByVersion := map[string][]verifiedSourceFile{}
	verifiedCodeFilesByUnit := map[string][]verifiedSourceFile{}
	verifiedFilesByVersion := map[string][]verifiedSourceFile{}
	verifiedEvByUnit := map[string][]domain.FindingEvidence{}
	verifiedEvByVersion := map[string][]domain.FindingEvidence{}

	for _, task := range readTasks {
		var srcBytes []byte
		rErr := w.Bytes.Read(ctx, task.obj, domain.Pin{Kind: "provider_attempt", OwnerID: a.ID}, func(r io.ReadSeeker) error {
			var e error
			srcBytes, e = io.ReadAll(r)
			return e
		})
		if rErr != nil || (task.obj.Size > 0 && int64(len(srcBytes)) != task.obj.Size) {
			partialSourceVersions[task.verID] = true
			for _, uk := range task.unitKeys {
				partialSourceUnits[uk] = true
			}
			continue
		}
		vf := verifiedSourceFile{
			RepoKey: task.repoKey,
			Path:    task.path,
			Text:    string(srcBytes),
		}
		fe := domain.FindingEvidence{
			Kind:           "project_source",
			Basis:          "observed_input",
			ReadID:         task.readID,
			InputOrigin:    task.origin,
			TargetSetID:    task.tsID,
			SourceID:       task.sourceID,
			VersionID:      task.verID,
			Path:           task.path,
			SnapshotSHA256: task.sha,
			ByteSize:       task.size,
		}
		verifiedFilesByVersion[task.verID] = append(verifiedFilesByVersion[task.verID], vf)
		verifiedEvByVersion[task.verID] = append(verifiedEvByVersion[task.verID], fe)
		if task.isManifest {
			manifestFilesByVersion[task.verID] = append(manifestFilesByVersion[task.verID], vf)
		}
		for _, uk := range task.unitKeys {
			if !task.isManifest {
				verifiedCodeFilesByUnit[uk] = append(verifiedCodeFilesByUnit[uk], vf)
			}
			verifiedEvByUnit[uk] = append(verifiedEvByUnit[uk], fe)
		}
	}

	focalUnitKey := candidateUnitKeyString(intent.VersionID, intent.PagePath, intent.RegionID)
	verifiedDeps := map[string]string{}
	var verifiedFocalTerms []string
	seenFocalTerm := map[string]bool{}
	for _, vf := range verifiedCodeFilesByUnit[focalUnitKey] {
		for _, tok := range tokenizeWithSubwords(vf.Path + " " + vf.Text) {
			if !seenFocalTerm[tok] {
				seenFocalTerm[tok] = true
				verifiedFocalTerms = append(verifiedFocalTerms, tok)
				if len(verifiedFocalTerms) >= 512 {
					break
				}
			}
		}
		if len(verifiedFocalTerms) >= 512 {
			break
		}
	}
	sort.Strings(verifiedFocalTerms)
	if !partialSourceVersions[intent.VersionID] && !partialSourceUnits[focalUnitKey] && len(verifiedCodeFilesByUnit[focalUnitKey]) > 0 {
		focalFiles := append(append([]verifiedSourceFile(nil), manifestFilesByVersion[intent.VersionID]...), verifiedCodeFilesByUnit[focalUnitKey]...)
		for _, cand := range intent.Shortlist.Candidates {
			uKey := candidateUnitKeyString(cand.VersionID, cand.PagePath, cand.RegionID)
			if partialSourceVersions[cand.VersionID] || partialSourceUnits[uKey] || len(verifiedCodeFilesByUnit[uKey]) == 0 {
				continue
			}
			candFiles := append(append([]verifiedSourceFile(nil), manifestFilesByVersion[cand.VersionID]...), verifiedCodeFilesByUnit[uKey]...)
			if depOK, detail := verifySourceDependencyFactWithRepo(focalFiles, candFiles); depOK && detail != "" {
				verifiedDeps[uKey] = detail
			}
		}
	}

	preparedPayload := domain.PreparedMaintenancePayload{
		Envelope:                  env,
		InputDigest:               inputDigest,
		VerifiedDependencies:      verifiedDeps,
		VerifiedEvidenceByUnit:    verifiedEvByUnit,
		VerifiedEvidenceByVersion: verifiedEvByVersion,
		VerifiedFocalSourceTerms:  verifiedFocalTerms,
		PartialSourceVersions:     partialSourceVersions,
		PartialSourceUnits:        partialSourceUnits,
	}
	normalized, err := json.Marshal(preparedPayload)
	if err != nil {
		return err
	}
	return w.Store.Write(ctx, func(tx app.WriteTx) error {
		cur, err := tx.ProviderAttempt(a.ID)
		if err != nil {
			return err
		}
		j, err := tx.Job(a.JobID)
		if err != nil {
			return err
		}
		if j.State == "queued" {
			if err := app.GuardProviderContinuation(tx, cur); err != nil {
				return err
			}
		} else {
			if err := app.GuardProvider(tx, cur); err != nil {
				return err
			}
		}
		pInputs, err := tx.ProviderInputs(a.ID)
		if err != nil {
			return err
		}
		if computeMaintenanceInputDigest(cur.ID, cur.ResultHash, pInputs) != inputDigest {
			return domain.ErrReconciliation
		}
		return tx.RecordPreparedMaintenanceOutput(a.JobID, a.ID, normalized)
	})
}

var allowedMaintenanceRelationTypes = map[string]bool{
	"depends_on":                true,
	"source_dependency":         true,
	"related":                   true,
	"conceptual_analogy":        true,
	"shared_theme":              true,
	"complementary_explanation": true,
}

var allowedMaintenanceUncertainties = map[string]bool{
	"":                       true,
	"none":                   true,
	"low":                    true,
	"medium":                 true,
	"high":                   true,
	"source_unverified":      true,
	"bounded_shortlist_only": true,
	"partial_source":         true,
}

func decodeMaintenanceOutput(raw []byte) (domain.MaintenanceOutputEnvelope, error) {
	var env domain.MaintenanceOutputEnvelope
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return env, ErrMaintenanceInvalid
	}
	var rootKeys map[string]json.RawMessage
	if err := json.Unmarshal(trimmed, &rootKeys); err != nil || rootKeys == nil {
		return env, ErrMaintenanceInvalid
	}
	for _, reqKey := range []string{"relations", "topics", "content_gaps", "limitations"} {
		val, ok := rootKeys[reqKey]
		valTrim := bytes.TrimSpace(val)
		if !ok || len(valTrim) == 0 || valTrim[0] != '[' {
			return env, ErrMaintenanceInvalid
		}
	}
	dec := json.NewDecoder(bytes.NewReader(trimmed))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&env); err != nil {
		return env, ErrMaintenanceInvalid
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		return env, ErrMaintenanceInvalid
	}
	for _, rel := range env.Relations {
		if strings.TrimSpace(rel.TargetVersionID) == "" || strings.TrimSpace(rel.Rationale) == "" {
			return env, ErrMaintenanceInvalid
		}
		rt := strings.ToLower(strings.TrimSpace(rel.RelationType))
		if !allowedMaintenanceRelationTypes[rt] {
			return env, ErrMaintenanceInvalid
		}
		unc := strings.ToLower(strings.TrimSpace(rel.Uncertainty))
		if !allowedMaintenanceUncertainties[unc] {
			return env, ErrMaintenanceInvalid
		}
	}
	for _, top := range env.Topics {
		if strings.TrimSpace(top.Topic) == "" || strings.TrimSpace(top.Rationale) == "" {
			return env, ErrMaintenanceInvalid
		}
		unc := strings.ToLower(strings.TrimSpace(top.Uncertainty))
		if !allowedMaintenanceUncertainties[unc] {
			return env, ErrMaintenanceInvalid
		}
	}
	for _, gap := range env.ContentGaps {
		if (gap.Kind != "new_version_proposal" && gap.Kind != "new_document_proposal") ||
			strings.TrimSpace(gap.Theme) == "" || strings.TrimSpace(gap.Rationale) == "" {
			return env, ErrMaintenanceInvalid
		}
		unc := strings.ToLower(strings.TrimSpace(gap.Uncertainty))
		if !allowedMaintenanceUncertainties[unc] {
			return env, ErrMaintenanceInvalid
		}
	}
	for _, lim := range env.Limitations {
		if strings.TrimSpace(lim.Reason) == "" && strings.TrimSpace(lim.Code) == "" && strings.TrimSpace(lim.Message) == "" {
			return env, ErrMaintenanceInvalid
		}
	}
	return env, nil
}

func isDeclaredScopeTheme(sig unitSignals, theme string, tokens []string) bool {
	if len(tokens) == 0 {
		return false
	}
	lowerTheme := strings.ToLower(strings.TrimSpace(theme))
	for _, th := range sig.Themes {
		if th == lowerTheme {
			return true
		}
	}
	for _, tok := range tokens {
		st := canonicalStem(tok)
		if !sig.DeclaredTerms[tok] && !sig.DeclaredStems[st] {
			return false
		}
	}
	return true
}

var genericMechanismTokens = map[string]bool{
	"protocol": true, "mechanism": true, "mechanisms": true, "lifecycle": true,
	"workflow": true, "workflows": true, "policy": true, "policies": true,
	"semantics": true, "semantic": true, "model": true, "models": true,
	"pattern": true, "patterns": true, "architecture": true, "architectural": true,
	"boundary": true, "boundaries": true, "handling": true, "handler": true,
	"management": true, "manager": true, "coordination": true, "coordinator": true,
	"recovery": true, "reconciliation": true, "isolation": true, "verification": true,
	"validation": true, "strategy": true, "strategies": true, "execution": true,
	"state": true, "states": true, "transition": true, "transitions": true,
	"pipeline": true, "control": true, "fence": true, "fencing": true,
	"guard": true, "guards": true, "structure": true, "structural": true,
	"design": true, "overview": true, "summary": true, "guide": true,
	"contract": true, "contracts": true, "behavior": true, "operations": true,
	"operation": true, "process": true, "processing": true, "system": true,
	"layer": true, "component": true, "module": true, "service": true,
	"runtime": true, "flow": true, "enforcement": true, "tracking": true,
}

func normalizeOrderedRationaleText(rationale string) string {
	return strings.Join(strings.Fields(strings.ToLower(strings.TrimSpace(rationale))), " ")
}

func relationMechanismSignature(relType, rationale, depDetail, downgradedFrom, uncertainty string) (string, string) {
	normOrdered := normalizeOrderedRationaleText(rationale)
	raw := strings.ToLower(strings.TrimSpace(relType)) + "|" +
		strings.TrimSpace(depDetail) + "|" +
		strings.ToLower(strings.TrimSpace(downgradedFrom)) + "|" +
		strings.ToLower(strings.TrimSpace(uncertainty)) + "|" +
		normOrdered
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:16]), normOrdered
}

type acceptedRelationRecord struct {
	rationale      string
	normOrdered    string
	depDetail      string
	downgradedFrom string
	uncertainty    string
	mechKey        string
}

func areRelationMechanismsEquivalent(a, b acceptedRelationRecord) bool {
	if a.depDetail != b.depDetail || a.downgradedFrom != b.downgradedFrom || a.uncertainty != b.uncertainty {
		return false
	}
	return a.normOrdered != "" && a.normOrdered == b.normOrdered
}

func rationaleDefinesNeighborAbsence(rationale string) bool {
	low := strings.ToLower(rationale)
	neighborMarkers := []string{
		"shortlist", "shortlisted", "candidate", "candidates", "neighbor", "neighbors",
		"neighbour", "neighbours", "other project", "other document", "other version",
		"peer document", "peer version", "counterpart", "target version", "target page",
		"target region", "compared unit", "inspected shortlist",
	}
	hasNeighborMarker := false
	for _, m := range neighborMarkers {
		if strings.Contains(low, m) {
			hasNeighborMarker = true
			break
		}
	}
	if !hasNeighborMarker {
		return false
	}
	focalMissingMarkers := []string{
		"focal unit omits", "focal section omits", "focal region omits", "focal page omits",
		"focal explanation omits", "focal unit does not explain", "focal region does not explain",
		"this section does not explain", "this region does not explain", "this page does not explain",
		"leaves unelaborated in the focal", "missing from the focal",
	}
	for _, fm := range focalMissingMarkers {
		if strings.Contains(low, fm) {
			return false
		}
	}
	return true
}

func rationaleIdentifiesMissingFocalDetail(rationale string) bool {
	low := strings.ToLower(rationale)
	for _, m := range []string{
		"focal", "this section", "this region", "this page", "this version", "this document",
		"unelaborated", "does not explain", "does not detail", "does not cover", "does not specify",
		"does not establish", "does not define", "does not state", "silent on", "leaves open",
		"omits", "omitted", "missing detail", "missing implementation", "missing explanation",
		"missing guarantee", "unverified guarantee", "internal guarantee", "internal invariant",
		"not yet documented", "not documented", "not covered", "not specified", "unspecified",
		"underspecified", "unexplained", "lacks detail", "lacks explanation",
		"without explaining", "without detailing", "needs a dedicated", "requires a dedicated",
		"cannot verify", "cannot be verified",
	} {
		if strings.Contains(low, m) {
			return true
		}
	}
	return false
}

func clauseHasUnsupportedGapClaim(clause string, hasVerifiedSource bool) bool {
	low := strings.ToLower(clause)
	for _, universalClaim := range []string{
		"nowhere in the wiki", "absent from the entire wiki", "across the entire wiki",
		"missing from all projects", "universal absence", "entire knowledge base",
	} {
		if strings.Contains(low, universalClaim) {
			return true
		}
	}
	if !hasVerifiedSource {
		negations := []string{
			"without ", "no ", "not ", "lack", "absent", "absence", "unavailable", "unverified", "missing", "omitted",
		}
		for _, srcClaim := range []string{
			"inspected source code", "verified source bytes", "supplied source files",
			"source code inspection", "inspected repository files",
		} {
			idx := strings.Index(low, srcClaim)
			if idx >= 0 {
				prefix := low[:idx]
				negated := false
				for _, neg := range negations {
					if strings.Contains(prefix, neg) {
						negated = true
						break
					}
				}
				if !negated {
					return true
				}
			}
		}
	}
	return false
}

var negatingPrefixSet = map[string]bool{
	"counter": true, "anti": true, "non": true, "dis": true, "mis": true,
	"un": true, "in": true, "im": true, "ir": true, "il": true, "de": true,
	"an": true, "a": true,
}

func stripOppositeAffixCandidates(tok string) []string {
	var bases []string
	for _, pfx := range []string{"counter", "anti", "non", "dis", "mis", "un", "in", "im", "ir", "il", "de", "an", "a"} {
		if strings.HasPrefix(tok, pfx) && len(tok)-len(pfx) >= 4 {
			bases = append(bases, tok[len(pfx):])
		}
	}
	for _, sfx := range []string{"less", "free"} {
		if strings.HasSuffix(tok, sfx) && len(tok)-len(sfx) >= 3 {
			base := tok[:len(tok)-len(sfx)]
			bases = append(bases, base, base+"ful")
		}
	}
	if strings.HasSuffix(tok, "ful") && len(tok)-3 >= 3 {
		base := tok[:len(tok)-3]
		bases = append(bases, base+"less", base+"free")
	}
	return bases
}

func hasPrivationalSuffix(tok string) bool {
	return (strings.HasSuffix(tok, "less") && len(tok) >= 7) ||
		(strings.HasSuffix(tok, "free") && len(tok) >= 7)
}

func tokensHaveOppositePolarity(a, aSt, b, bSt string) bool {
	if a == b || (len(aSt) >= 4 && aSt == bSt) {
		return false
	}
	if hasPrivationalSuffix(a) != hasPrivationalSuffix(b) {
		return true
	}
	for _, cand := range stripOppositeAffixCandidates(a) {
		cSt := canonicalStem(cand)
		if cand == b || (len(cSt) >= 4 && (cSt == bSt || cSt == b)) {
			return true
		}
	}
	for _, cand := range stripOppositeAffixCandidates(b) {
		cSt := canonicalStem(cand)
		if cand == a || (len(cSt) >= 4 && (cSt == aSt || cSt == a)) {
			return true
		}
	}
	return false
}

func tokenContradictsFocalScope(tok, st string, focalSig unitSignals, focalSourceTerms, focalSourceStems map[string]bool) bool {
	matchesActiveFocal := func(cand string) bool {
		if focalSig.BodyTerms[cand] || focalSig.HeadingTerms[cand] || (focalSourceTerms != nil && focalSourceTerms[cand]) {
			return true
		}
		cSt := canonicalStem(cand)
		return len(cSt) >= 4 && (focalSig.BodyStems[cSt] || focalSig.HeadingStems[cSt] || (focalSourceStems != nil && focalSourceStems[cSt]))
	}
	// 1. Topic token carries a negating/privational affix whose counterpart exists in focal body/headings/verified source.
	for _, base := range stripOppositeAffixCandidates(tok) {
		if matchesActiveFocal(base) {
			return true
		}
	}
	// 2. Focal body/headings/verified source carries the opposite/negated form while the topic proposes the ungrounded counterpart.
	checkMap := func(m map[string]bool) bool {
		for fTok := range m {
			for _, fBase := range stripOppositeAffixCandidates(fTok) {
				fBaseSt := canonicalStem(fBase)
				if fBase == tok || (len(st) >= 4 && (fBaseSt == st || fBase == st)) {
					return true
				}
			}
		}
		return false
	}
	return checkMap(focalSig.BodyTerms) || checkMap(focalSig.HeadingTerms) || checkMap(focalSourceTerms)
}

func tokenSharesRootOrCompound(tok, st string, terms, stems, extraTerms, extraStems map[string]bool) bool {
	if terms[tok] || (extraTerms != nil && extraTerms[tok]) {
		return true
	}
	if len(st) >= 4 && (stems[st] || (extraStems != nil && extraStems[st])) {
		return true
	}
	// Check active terms first so we can guard both stem-prefix and compound substring matches against polarity inversion.
	checkTerm := func(k string) bool {
		kSt := canonicalStem(k)
		if tokensHaveOppositePolarity(tok, st, k, kSt) {
			return false
		}
		if len(st) >= 5 && len(kSt) >= 5 && strings.HasPrefix(kSt, st[:5]) {
			return true
		}
		if len(tok) >= 6 && len(k) >= 4 {
			if strings.HasPrefix(tok, k) {
				rem := tok[len(k):]
				if rem != "less" && rem != "free" {
					return true
				}
			}
			if strings.HasSuffix(tok, k) {
				pfx := tok[:len(tok)-len(k)]
				if !negatingPrefixSet[pfx] {
					return true
				}
			}
		}
		return false
	}
	for k := range terms {
		if checkTerm(k) {
			return true
		}
	}
	for k := range extraTerms {
		if checkTerm(k) {
			return true
		}
	}
	if len(st) >= 5 && !hasPrivationalSuffix(tok) {
		prefix5 := st[:5]
		for k := range stems {
			if len(k) >= 5 && strings.HasPrefix(k, prefix5) && !hasPrivationalSuffix(k) && !tokensHaveOppositePolarity(tok, st, k, k) {
				return true
			}
		}
		for k := range extraStems {
			if len(k) >= 5 && strings.HasPrefix(k, prefix5) && !hasPrivationalSuffix(k) && !tokensHaveOppositePolarity(tok, st, k, k) {
				return true
			}
		}
	}
	return false
}

func evaluateTopicFocalGrounding(
	topicName, normTopic string,
	topicTokens []string,
	rationale string,
	focalSig unitSignals,
	focalSourceTerms, focalSourceStems map[string]bool,
) (grounded bool, usedParaphrase bool, convertDeclaredGap bool) {
	hasFocalScope := len(focalSig.BodyTerms) > 0 || len(focalSig.HeadingTerms) > 0 || len(focalSourceTerms) > 0
	allDirectOrStemInBody := hasFocalScope
	anyDirectOrStemInBody := false
	allTokensInScopeOrMechanism := hasFocalScope
	anySpecificTokenInBodyOrSource := false
	usedParaphraseOrStem := false
	hasContradictedQualifier := false
	groundedScopeOrMechCount := 0
	ungroundedCount := 0

	for _, tok := range topicTokens {
		st := canonicalStem(tok)
		inBodyExact := focalSig.BodyTerms[tok] || focalSourceTerms[tok]
		inHeadExact := focalSig.HeadingTerms[tok]
		inBodyStem := (len(st) >= 4 && (focalSig.BodyStems[st] || focalSourceStems[st])) ||
			tokenSharesRootOrCompound(tok, st, focalSig.BodyTerms, focalSig.BodyStems, focalSourceTerms, focalSourceStems)
		inHeadStem := (len(st) >= 4 && focalSig.HeadingStems[st]) ||
			tokenSharesRootOrCompound(tok, st, focalSig.HeadingTerms, focalSig.HeadingStems, nil, nil)
		inDecl := focalSig.DeclaredTerms[tok] || (len(st) >= 4 && focalSig.DeclaredStems[st])
		isGenericMech := genericMechanismTokens[tok] || genericMechanismTokens[st]

		if !inBodyExact && !inHeadExact {
			if inBodyStem || inHeadStem || isGenericMech {
				usedParaphraseOrStem = true
			}
		}
		if !inBodyExact && !inHeadExact && !inBodyStem && !inHeadStem {
			allDirectOrStemInBody = false
			if tokenContradictsFocalScope(tok, st, focalSig, focalSourceTerms, focalSourceStems) {
				hasContradictedQualifier = true
			}
		}
		if inBodyExact || inBodyStem {
			anyDirectOrStemInBody = true
			if !isGenericMech {
				anySpecificTokenInBodyOrSource = true
			}
		}
		if inBodyExact || inHeadExact || inBodyStem || inHeadStem || isGenericMech || (inDecl && anyDirectOrStemInBody) {
			groundedScopeOrMechCount++
		} else {
			allTokensInScopeOrMechanism = false
			ungroundedCount++
		}
	}

	declaredTheme := isDeclaredScopeTheme(focalSig, topicName, topicTokens)
	if declaredTheme && !anyDirectOrStemInBody && !anySpecificTokenInBodyOrSource {
		return false, false, true
	}
	if hasContradictedQualifier {
		return false, usedParaphraseOrStem, false
	}

	if (allDirectOrStemInBody && anyDirectOrStemInBody) || (allTokensInScopeOrMechanism && anySpecificTokenInBodyOrSource) {
		return true, usedParaphraseOrStem, false
	}

	// OR4: Accept a source-grounded topic paraphrase without demanding literal stem overlap for every slug token,
	// provided specific focal mechanism tokens are present, ungrounded paraphrase tokens are bounded, and the rationale is grounded in the focal scope.
	if anySpecificTokenInBodyOrSource && hasFocalScope && len(topicTokens) >= 2 {
		withinParaphraseBound := (len(topicTokens) <= 3 && ungroundedCount <= 1 && groundedScopeOrMechCount*2 >= len(topicTokens)) ||
			(len(topicTokens) >= 4 && ungroundedCount*3 <= len(topicTokens) && groundedScopeOrMechCount >= 2)
		if withinParaphraseBound {
			rationaleSpecificHits := 0
			seenRatStem := map[string]bool{}
			for _, rTok := range tokenizeWithSubwords(rationale) {
				rSt := canonicalStem(rTok)
				if seenRatStem[rSt] || genericMechanismTokens[rTok] || genericMechanismTokens[rSt] {
					continue
				}
				if focalSig.BodyTerms[rTok] || focalSourceTerms[rTok] || focalSig.HeadingTerms[rTok] ||
					(len(rSt) >= 4 && (focalSig.BodyStems[rSt] || focalSourceStems[rSt] || focalSig.HeadingStems[rSt])) {
					seenRatStem[rSt] = true
					rationaleSpecificHits++
				}
			}
			minHits := 2
			if len(focalSig.BodyTerms)+len(focalSourceTerms) <= 2 {
				minHits = 1
			}
			if rationaleSpecificHits >= minHits {
				return true, true, false
			}
		}
	}
	return false, usedParaphraseOrStem, false
}

func evaluateGapUnsupportedClaims(rationale string, hasVerifiedSource, isConvertedDeclaredGap bool) (cleanRationale string, withdrewClaim bool, rejectReason string) {
	r := strings.TrimSpace(rationale)
	if !clauseHasUnsupportedGapClaim(r, hasVerifiedSource) {
		return r, false, ""
	}
	rawClauses := strings.FieldsFunc(r, func(c rune) bool {
		return c == '.' || c == ';' || c == '!' || c == '?'
	})
	var supported []string
	for _, cl := range rawClauses {
		cl = strings.TrimSpace(cl)
		if cl == "" {
			continue
		}
		if clauseHasUnsupportedGapClaim(cl, hasVerifiedSource) {
			continue
		}
		supported = append(supported, cl)
	}
	if len(supported) == 0 {
		return "", true, "unsupported_source_or_universal_absence_claim"
	}
	joined := strings.Join(supported, ". ") + "."
	if !isConvertedDeclaredGap && !rationaleIdentifiesMissingFocalDetail(joined) {
		return "", true, "unsupported_source_or_universal_absence_claim"
	}
	return "Unsupported universal-scope or uninspected-source claim withdrawn; retained only for focal scope: " + joined, true, ""
}

// ApplyMaintenance runs inside ApplyProviderResult's guarded write transaction,
// re-verifies parent scan liveness, exact prepared input binding, and all 4-boundary
// candidate/policy fences without performing any object I/O inside the writer.
func (w *Worker) ApplyMaintenance(tx app.WriteTx, attempt domain.ProviderAttempt) error {
	j, err := tx.Job(attempt.JobID)
	if err != nil || j.Kind != "maintenance" {
		return err
	}
	a, err := tx.ProviderAttempt(attempt.ID)
	if err != nil {
		return err
	}
	if err := app.GuardProvider(tx, a); err != nil {
		return err
	}
	intent, err := tx.MaintenanceIntent(j.ID)
	if err != nil {
		return err
	}
	if intent.Mode == domain.MaintenanceModeFreshness {
		return w.applyFreshness(tx, a, intent)
	}
	if intent.PreparedAttemptID != a.ID || len(intent.PreparedOutputJSON) == 0 {
		return domain.ErrReconciliation
	}
	pInputs, err := tx.ProviderInputs(a.ID)
	if err != nil {
		return err
	}
	if intent.Mode == domain.MaintenanceModeSemanticNomination {
		var prepared domain.PreparedMaintenanceNominationPayload
		if err := json.Unmarshal(intent.PreparedOutputJSON, &prepared); err != nil || prepared.InputDigest == "" || prepared.CardPayloadDigest == "" {
			return domain.ErrReconciliation
		}
		expectedDigest := computeMaintenanceInputDigest(a.ID, a.ResultHash, pInputs) + "|" + intent.CardPayloadDigest
		if prepared.InputDigest != expectedDigest || prepared.CardPayloadDigest != intent.CardPayloadDigest {
			return domain.ErrReconciliation
		}
		var scope domain.ProviderScope
		if err := json.Unmarshal(j.Scope, &scope); err != nil || scope.Invocation == nil || scope.Invocation.Maintenance == nil {
			return domain.ErrReconciliation
		}
		if scope.Invocation.Maintenance.Mode != domain.MaintenanceModeSemanticNomination || scope.Invocation.Maintenance.CardPayloadDigest != intent.CardPayloadDigest {
			return domain.ErrReconciliation
		}
		parent, err := tx.Job(intent.ScanJobID)
		if err != nil {
			return err
		}
		if parent.Kind != "scan" || parent.CancelRequested || (parent.State != "running" && parent.State != "queued") {
			return domain.ErrStaleWorker
		}
		run, err := tx.ScanRun(parent.ID)
		if err != nil {
			return err
		}
		now := w.now()
		if err := w.checkGlobalRunAuthority(tx, parent, run, now); err != nil {
			return domain.ErrDenied
		}
		if len(intent.Cards) == 0 || len(intent.Cards) > MaxNominationCardsPerBatch {
			return domain.ErrDenied
		}
		for _, card := range intent.Cards {
			it, err := tx.JobItem(parent.ID, card.ItemID)
			if err != nil {
				return domain.ErrDenied
			}
			if it.Stage != "inspect" || (it.State != "pending" && it.State != "running") {
				return domain.ErrStaleWorker
			}
			if _, _, _, fenced := w.checkItemFence(tx, parent, run, it); fenced {
				return domain.ErrDenied
			}
			if _, _, _, ok := w.checkCandidateTargetFence(tx, run, card.VersionID, "codex"); !ok {
				return domain.ErrDenied
			}
		}
		intent.NominationState = "completed"
		return tx.UpdateMaintenanceIntent(intent)
	}
	var prepared domain.PreparedMaintenancePayload
	if err := json.Unmarshal(intent.PreparedOutputJSON, &prepared); err != nil || prepared.InputDigest == "" {
		return domain.ErrReconciliation
	}
	if computeMaintenanceInputDigest(a.ID, a.ResultHash, pInputs) != prepared.InputDigest {
		return domain.ErrReconciliation
	}
	env := prepared.Envelope
	verifiedEvByUnit := prepared.VerifiedEvidenceByUnit
	if verifiedEvByUnit == nil {
		verifiedEvByUnit = map[string][]domain.FindingEvidence{}
	}
	verifiedEvByVersion := prepared.VerifiedEvidenceByVersion
	if verifiedEvByVersion == nil {
		verifiedEvByVersion = map[string][]domain.FindingEvidence{}
	}
	partialSourceVersions := prepared.PartialSourceVersions
	if partialSourceVersions == nil {
		partialSourceVersions = map[string]bool{}
	}
	partialSourceUnits := prepared.PartialSourceUnits
	if partialSourceUnits == nil {
		partialSourceUnits = map[string]bool{}
	}
	for _, lim := range intent.Limitations {
		if lim.Kind == "source_unavailable" || lim.Kind == "source_disclosure_restricted" || lim.Kind == "partial_source" {
			tv := lim.TargetVersionID
			if tv == "" {
				tv = intent.VersionID
			}
			partialSourceVersions[tv] = true
		}
	}

	unitVerifiedEv := func(uKey, verID string) []domain.FindingEvidence {
		if evs, ok := verifiedEvByUnit[uKey]; ok {
			return evs
		}
		if len(verifiedEvByUnit) == 0 {
			return verifiedEvByVersion[verID]
		}
		return nil
	}
	unitIsPartial := func(uKey, verID string) bool {
		return partialSourceVersions[verID] || partialSourceUnits[uKey]
	}

	parent, err := tx.Job(intent.ScanJobID)
	if err != nil {
		return err
	}
	if parent.Kind != "scan" || parent.CancelRequested || (parent.State != "running" && parent.State != "queued") {
		return domain.ErrStaleWorker
	}
	run, err := tx.ScanRun(parent.ID)
	if err != nil {
		return err
	}
	now := w.now()
	nowStr := now.Format(time.RFC3339Nano)
	if err := w.checkGlobalRunAuthority(tx, parent, run, now); err != nil {
		return domain.ErrDenied
	}
	curItem, err := tx.JobItem(parent.ID, intent.ItemID)
	if err != nil {
		return err
	}
	if curItem.Stage != "inspect" || (curItem.State != "pending" && curItem.State != "running") {
		return domain.ErrStaleWorker
	}
	if _, _, _, fenced := w.checkItemFence(tx, parent, run, curItem); fenced {
		return domain.ErrDenied
	}
	focalCV, focalDoc, _, ok := w.checkCandidateTargetFence(tx, run, curItem.VersionID, "codex")
	if !ok {
		return domain.ErrDenied
	}

	cfg, err := tx.Configuration()
	if err != nil {
		return err
	}
	curPolicy, err := ResolveEffectivePolicy(cfg, run.RequestedScope.ProjectIDs, run.EffectivePolicy.Depth)
	if err != nil {
		return err
	}
	relMode := StricterAutoApplyMode(run.EffectivePolicy.AutoApply.RelatedExplanation, curPolicy.AutoApply.RelatedExplanation)
	topicMode := StricterAutoApplyMode(run.EffectivePolicy.AutoApply.TopicAssignment, curPolicy.AutoApply.TopicAssignment)
	contentMode := StricterContentGeneration(run.EffectivePolicy.ContentGeneration, curPolicy.ContentGeneration)

	var scope domain.ProviderScope
	_ = json.Unmarshal(j.Scope, &scope)
	expByUnit := map[string]domain.MaintenanceExplanation{}
	if scope.Invocation != nil && scope.Invocation.Maintenance != nil {
		for _, exp := range scope.Invocation.Maintenance.Explanations {
			pPath := ""
			if exp.PagePath != nil {
				pPath = *exp.PagePath
			}
			rID := ""
			if exp.RegionID != nil {
				rID = *exp.RegionID
			}
			expByUnit[candidateUnitKeyString(exp.VersionID, pPath, rID)] = exp
		}
	}

	// Build exact (version, page, region) candidate lookup maps and re-run Boundary 4 checkCandidateTargetFence.
	shortlistByUnit := map[string]domain.CandidateTarget{}
	candidatesByVer := map[string][]domain.CandidateTarget{}
	candidatesByVerPage := map[string][]domain.CandidateTarget{}
	candidatesByVerRegion := map[string][]domain.CandidateTarget{}
	for _, cand := range intent.Shortlist.Candidates {
		if _, _, _, cOK := w.checkCandidateTargetFence(tx, run, cand.VersionID, "codex"); !cOK {
			continue
		}
		uKey := candidateUnitKeyString(cand.VersionID, cand.PagePath, cand.RegionID)
		shortlistByUnit[uKey] = cand
		candidatesByVer[cand.VersionID] = append(candidatesByVer[cand.VersionID], cand)
		if cand.PagePath != "" {
			vpKey := cand.VersionID + "\x00" + cand.PagePath
			candidatesByVerPage[vpKey] = append(candidatesByVerPage[vpKey], cand)
		}
		if cand.RegionID != "" {
			vrKey := cand.VersionID + "\x00" + cand.RegionID
			candidatesByVerRegion[vrKey] = append(candidatesByVerRegion[vrKey], cand)
		}
	}
	eligibleCandidatesAtApply := len(shortlistByUnit)
	filteredRelations := 0
	downgradedRelations := 0
	filteredTopics := 0
	filteredGaps := 0
	proposalDecisions := []domain.MaintenanceProposalDecision{}

	focalUnitKey := candidateUnitKeyString(curItem.VersionID, curItem.PagePath, curItem.RegionID)
	focalExp := expByUnit[focalUnitKey]
	focalEv := domain.FindingEvidence{
		Kind:           "explanation_context",
		Basis:          "explanation_context",
		VersionID:      curItem.VersionID,
		PagePath:       curItem.PagePath,
		RegionID:       curItem.RegionID,
		TargetSetID:    focalCV.TargetSetID,
		SnapshotSHA256: focalExp.ObjectHash,
		ByteSize:       focalExp.Size,
	}
	focalVerifiedEv := unitVerifiedEv(focalUnitKey, curItem.VersionID)
	focalPartial := unitIsPartial(focalUnitKey, curItem.VersionID)
	focalHasVerifiedSrc := !focalPartial && len(focalVerifiedEv) > 0

	var findingIDs []string
	seq := 0
	putFinding := func(kind string, docID, verID, pagePath, regionID *string, tsID *string, ev []domain.FindingEvidence, fpPayload any, rationale, uncertainty string, proposal map[string]any) error {
		rationale = strings.TrimSpace(rationale)
		if rationale == "" {
			return nil
		}
		seq++
		fpVer := curItem.VersionID
		if verID != nil && *verID != "" {
			fpVer = *verID
		}
		fp := ComputeEvidenceFingerprint(kind, fpVer, fpPayload)
		evBytes, _ := json.Marshal(ev)
		propBytes, _ := json.Marshal(proposal)
		permRev := run.PermissionsRevisions[curItem.ProjectID]
		if permRev < 1 {
			permRev = 1
		}
		if strings.TrimSpace(uncertainty) == "" {
			uncertainty = "none"
		}
		existing, exists, err := tx.FindingByFingerprint(run.JobID, kind, fp)
		if err != nil {
			return err
		}
		if exists {
			// An applied or historical finding is immutable under repeated delivery.
			if existing.Status != "proposed" {
				if existing.Status == "applied" && (kind == "related_explanation" || kind == "topic_assignment") {
					if err := tx.ValidateManagementFinding(existing); err != nil {
						return err
					}
				}
				findingIDs = append(findingIDs, existing.ID)
				return nil
			}
			existing.Evidence = evBytes
			existing.Proposal = propBytes
			existing.Rationale = rationale
			existing.Uncertainty = uncertainty
			existing.Status = "proposed"
			existing.ApprovalRequired = true
			existing.AppliedRef = nil
			existing.Revision++
			existing.UpdatedAt = nowStr
			existing.ETag = domain.FindingETag(existing)
			mode := relMode
			if kind == "topic_assignment" {
				mode = topicMode
			}
			if err := saveDecisionFinding(tx, &existing, mode, cfg.Revision, nowStr); err != nil {
				return err
			}
			findingIDs = append(findingIDs, existing.ID)
			return nil
		}
		f := domain.Finding{
			ID:                     fmt.Sprintf("fnd-%s-%06d-%02d", run.JobID, curItem.Ordinal, seq),
			JobID:                  run.JobID,
			ProjectID:              curItem.ProjectID,
			DocumentID:             docID,
			VersionID:              verID,
			PagePath:               pagePath,
			RegionID:               regionID,
			Kind:                   kind,
			Origin:                 "ai",
			TargetSetID:            tsID,
			Evidence:               evBytes,
			EvidenceFingerprint:    fp,
			Rationale:              rationale,
			Uncertainty:            uncertainty,
			Proposal:               propBytes,
			Status:                 "proposed",
			ApprovalRequired:       true,
			Reason:                 "candidate_discovered",
			CapturedConfigRevision: run.ConfigRevision,
			PermissionsRevision:    permRev,
			AppliedRef:             nil,
			Revision:               1,
			CreatedAt:              nowStr,
			UpdatedAt:              nowStr,
		}
		f.ETag = domain.FindingETag(f)
		mode := relMode
		if kind == "topic_assignment" {
			mode = topicMode
		}
		if err := saveDecisionFinding(tx, &f, mode, cfg.Revision, nowStr); err != nil {
			return err
		}
		findingIDs = append(findingIDs, f.ID)
		return nil
	}

	focalSig, _ := extractScopeThemes(tx, focalCV, curItem.PagePath, curItem.RegionID)
	focalSourceTerms := map[string]bool{}
	focalSourceStems := map[string]bool{}
	for _, tok := range prepared.VerifiedFocalSourceTerms {
		focalSourceTerms[tok] = true
		focalSourceStems[canonicalStem(tok)] = true
		focalSig.BodyTerms[tok] = true
		focalSig.BodyStems[canonicalStem(tok)] = true
	}
	for sp := range focalSig.SourcePaths {
		for _, tok := range tokenizeWithSubwords(sp) {
			focalSourceTerms[tok] = true
			focalSourceStems[canonicalStem(tok)] = true
		}
	}

	// 1. Evaluate Relations (T-008-02-01, T-008-02-02, QR2, OR3).
	acceptedByPairType := map[string][]acceptedRelationRecord{}
	if relMode != "disabled" {
		for _, rel := range env.Relations {
			origRelType := strings.ToLower(strings.TrimSpace(rel.RelationType))
			if strings.TrimSpace(rel.Rationale) == "" {
				filteredRelations++
				proposalDecisions = append(proposalDecisions, domain.MaintenanceProposalDecision{
					Category:        "relation",
					Decision:        "rejected",
					Reason:          "blank_rationale",
					TargetVersionID: rel.TargetVersionID,
					TargetPagePath:  rel.TargetPagePath,
					TargetRegionID:  rel.TargetRegionID,
					RelationType:    origRelType,
				})
				continue
			}
			uKey := candidateUnitKeyString(rel.TargetVersionID, rel.TargetPagePath, rel.TargetRegionID)
			cand, ok := shortlistByUnit[uKey]
			if !ok {
				if rel.TargetPagePath == "" && rel.TargetRegionID == "" {
					if cands := candidatesByVer[rel.TargetVersionID]; len(cands) == 1 {
						cand = cands[0]
						uKey = candidateUnitKeyString(cand.VersionID, cand.PagePath, cand.RegionID)
						ok = true
					}
				} else if rel.TargetPagePath != "" && rel.TargetRegionID == "" {
					if cands := candidatesByVerPage[rel.TargetVersionID+"\x00"+rel.TargetPagePath]; len(cands) == 1 {
						cand = cands[0]
						uKey = candidateUnitKeyString(cand.VersionID, cand.PagePath, cand.RegionID)
						ok = true
					}
				} else if rel.TargetPagePath == "" && rel.TargetRegionID != "" {
					if cands := candidatesByVerRegion[rel.TargetVersionID+"\x00"+rel.TargetRegionID]; len(cands) == 1 {
						cand = cands[0]
						uKey = candidateUnitKeyString(cand.VersionID, cand.PagePath, cand.RegionID)
						ok = true
					}
				} else if rel.TargetPagePath != "" && rel.TargetRegionID != "" {
					// When a shortlisted candidate is a regionless page and the model cited a verified unique DOM anchor on that page, resolve to the page candidate.
					pageKey := candidateUnitKeyString(rel.TargetVersionID, rel.TargetPagePath, "")
					if pageCand, hasPageCand := shortlistByUnit[pageKey]; hasPageCand && pageCand.UnitKind == "page" {
						if pa, paErr := tx.PageAnchor(rel.TargetVersionID, rel.TargetPagePath, rel.TargetRegionID); paErr == nil && pa.Occurrences == 1 {
							cand = pageCand
							uKey = pageKey
							ok = true
						}
					}
				}
			}
			if !ok {
				filteredRelations++
				proposalDecisions = append(proposalDecisions, domain.MaintenanceProposalDecision{
					Category:        "relation",
					Decision:        "rejected",
					Reason:          "target_not_in_shortlist",
					TargetVersionID: rel.TargetVersionID,
					TargetPagePath:  rel.TargetPagePath,
					TargetRegionID:  rel.TargetRegionID,
					RelationType:    origRelType,
				})
				continue
			}
			if cand.VersionID == curItem.VersionID && cand.PagePath == curItem.PagePath && cand.RegionID == curItem.RegionID {
				filteredRelations++
				proposalDecisions = append(proposalDecisions, domain.MaintenanceProposalDecision{
					Category:        "relation",
					Decision:        "rejected",
					Reason:          "self_target",
					TargetVersionID: cand.VersionID,
					TargetPagePath:  cand.PagePath,
					TargetRegionID:  cand.RegionID,
					RelationType:    origRelType,
				})
				continue
			}
			// Re-verify Boundary 4 fence on candidate target right before persisting.
			candCV, _, _, cOK := w.checkCandidateTargetFence(tx, run, cand.VersionID, "codex")
			if !cOK {
				filteredRelations++
				proposalDecisions = append(proposalDecisions, domain.MaintenanceProposalDecision{
					Category:        "relation",
					Decision:        "rejected",
					Reason:          "target_fenced_at_apply",
					TargetVersionID: cand.VersionID,
					TargetPagePath:  cand.PagePath,
					TargetRegionID:  cand.RegionID,
					RelationType:    origRelType,
				})
				continue
			}
			relType := origRelType
			if !allowedMaintenanceRelationTypes[relType] {
				filteredRelations++
				proposalDecisions = append(proposalDecisions, domain.MaintenanceProposalDecision{
					Category:        "relation",
					Decision:        "rejected",
					Reason:          "invalid_relation_type",
					TargetVersionID: cand.VersionID,
					TargetPagePath:  cand.PagePath,
					TargetRegionID:  cand.RegionID,
					RelationType:    origRelType,
				})
				continue
			}
			evBasis := "thematic_analogy"
			depDetail := ""
			downgradedFrom := ""
			decisionKind := "accepted"
			decisionReason := "verified_shortlist_relation"
			relRationale := strings.TrimSpace(rel.Rationale)
			unc := strings.ToLower(strings.TrimSpace(rel.Uncertainty))
			candVerifiedEv := unitVerifiedEv(uKey, cand.VersionID)
			candPartial := unitIsPartial(uKey, cand.VersionID)
			candHasVerifiedSrc := !candPartial && len(candVerifiedEv) > 0

			if relType == "depends_on" || relType == "source_dependency" {
				if focalHasVerifiedSrc && candHasVerifiedSrc && prepared.VerifiedDependencies[uKey] != "" {
					relType = "depends_on"
					evBasis = "source_fact"
					depDetail = prepared.VerifiedDependencies[uKey]
					decisionReason = "verified_source_dependency"
					if unc == "" {
						unc = "none"
					}
				} else {
					// T-008-02-01 & B12: shared keywords or unverified/partial source cannot create a code dependency edge,
					// and persisted rationale must never present an unverified dependency claim as fact.
					downgradedRelations++
					if len(cand.Signals) == 0 {
						filteredRelations++
						proposalDecisions = append(proposalDecisions, domain.MaintenanceProposalDecision{
							Category:        "relation",
							Decision:        "rejected",
							Reason:          "unverified_dependency_without_shortlist_signals",
							TargetVersionID: cand.VersionID,
							TargetPagePath:  cand.PagePath,
							TargetRegionID:  cand.RegionID,
							RelationType:    origRelType,
						})
						continue
					}
					var sigKinds []string
					for _, s := range cand.Signals {
						if s.Kind != "" {
							sigKinds = append(sigKinds, s.Kind)
						}
					}
					if len(sigKinds) == 0 {
						sigKinds = []string{"thematic_overlap"}
					}
					downgradedFrom = origRelType
					relType = "related"
					evBasis = "thematic_analogy"
					decisionKind = "downgraded"
					decisionReason = "unverified_source_dependency_downgraded"
					if focalPartial || candPartial {
						unc = "partial_source"
					} else {
						unc = "source_unverified"
					}
					relRationale = fmt.Sprintf(
						"Unverified code-dependency claim withdrawn (%s); retained only as thematic analogy based on %s.",
						unc,
						strings.Join(sigKinds, ", "),
					)
				}
			} else {
				if unc == "" || unc == "none" {
					if focalPartial || candPartial {
						unc = "partial_source"
					} else if !focalHasVerifiedSrc || !candHasVerifiedSrc || len(focalCV.Targets) == 0 || len(candCV.Targets) == 0 {
						unc = "source_unverified"
					} else {
						unc = "none"
					}
				}
			}

			mechKey, normOrdered := relationMechanismSignature(relType, relRationale, depDetail, downgradedFrom, unc)
			candRec := acceptedRelationRecord{
				rationale:      relRationale,
				normOrdered:    normOrdered,
				depDetail:      depDetail,
				downgradedFrom: downgradedFrom,
				uncertainty:    unc,
				mechKey:        mechKey,
			}
			pairTypeKey := uKey + "\x00" + relType
			isDuplicate := false
			for _, prev := range acceptedByPairType[pairTypeKey] {
				if areRelationMechanismsEquivalent(prev, candRec) {
					isDuplicate = true
					break
				}
			}
			if isDuplicate {
				filteredRelations++
				proposalDecisions = append(proposalDecisions, domain.MaintenanceProposalDecision{
					Category:        "relation",
					Decision:        "rejected",
					Reason:          "duplicate_relation",
					Uncertainty:     unc,
					TargetVersionID: cand.VersionID,
					TargetPagePath:  cand.PagePath,
					TargetRegionID:  cand.RegionID,
					RelationType:    relType,
					DowngradedFrom:  downgradedFrom,
					MechanismKey:    mechKey,
				})
				continue
			}
			acceptedByPairType[pairTypeKey] = append(acceptedByPairType[pairTypeKey], candRec)
			if len(acceptedByPairType[pairTypeKey]) > 1 && decisionKind == "accepted" {
				decisionReason = "distinct_mechanism_on_pair"
			}

			candExp := expByUnit[uKey]
			evList := []domain.FindingEvidence{
				focalEv,
				{
					Kind:           "explanation_context",
					Basis:          "explanation_context",
					VersionID:      cand.VersionID,
					PagePath:       cand.PagePath,
					RegionID:       cand.RegionID,
					TargetSetID:    cand.TargetSetID,
					SnapshotSHA256: candExp.ObjectHash,
					ByteSize:       candExp.Size,
				},
			}
			evList = append(evList, focalVerifiedEv...)
			evList = append(evList, candVerifiedEv...)

			proposal := map[string]any{
				"action":                "link_related_explanation",
				"relation_type":         relType,
				"evidence_basis":        evBasis,
				"mechanism_key":         mechKey,
				"source_version_id":     curItem.VersionID,
				"source_page_path":      curItem.PagePath,
				"source_region_id":      curItem.RegionID,
				"target_project_id":     cand.ProjectID,
				"target_document_id":    cand.DocumentID,
				"target_version_id":     cand.VersionID,
				"target_page_path":      cand.PagePath,
				"target_region_id":      cand.RegionID,
				"shortlist_fingerprint": intent.Shortlist.ShortlistFingerprint,
			}
			if depDetail != "" {
				proposal["verified_dependency"] = depDetail
			}
			if downgradedFrom != "" {
				proposal["downgraded_from"] = downgradedFrom
			}
			fpPayload := map[string]any{
				"source_version_id": curItem.VersionID,
				"source_page_path":  curItem.PagePath,
				"source_region_id":  curItem.RegionID,
				"target_version_id": cand.VersionID,
				"target_page_path":  cand.PagePath,
				"target_region_id":  cand.RegionID,
				"relation_type":     relType,
				"mechanism_key":     mechKey,
			}
			var tsPtr *string
			if focalCV.TargetSetID != "" {
				ts := focalCV.TargetSetID
				tsPtr = &ts
			}
			if err := putFinding(
				"related_explanation",
				nullableStr(focalDoc.ID),
				nullableStr(curItem.VersionID),
				nullableStr(curItem.PagePath),
				nullableStr(curItem.RegionID),
				tsPtr,
				evList,
				fpPayload,
				relRationale,
				unc,
				proposal,
			); err != nil {
				return err
			}
			proposalDecisions = append(proposalDecisions, domain.MaintenanceProposalDecision{
				Category:        "relation",
				Decision:        decisionKind,
				Reason:          decisionReason,
				Uncertainty:     unc,
				TargetVersionID: cand.VersionID,
				TargetPagePath:  cand.PagePath,
				TargetRegionID:  cand.RegionID,
				RelationType:    relType,
				DowngradedFrom:  downgradedFrom,
				MechanismKey:    mechKey,
			})
		}
	}

	// 2. Evaluate Topics vs Missing-Theme Content Gaps (T-008-02-03, QR3, QR4, OR3, OR4).
	coveredTopics := map[string]bool{}
	seenAcceptedTopics := map[string]bool{}
	type candidateGapEntry struct {
		gap       domain.MaintenanceGapProposal
		converted bool
	}
	var gapQueue []candidateGapEntry
	for _, g := range env.ContentGaps {
		gapQueue = append(gapQueue, candidateGapEntry{gap: g, converted: false})
	}

	for _, top := range env.Topics {
		rawTopic := strings.TrimSpace(top.Topic)
		topicName := strings.ToLower(rawTopic)
		normTopic := normalizeTopicLabel(rawTopic)
		if topicName == "" || normTopic == "" || strings.TrimSpace(top.Rationale) == "" {
			filteredTopics++
			proposalDecisions = append(proposalDecisions, domain.MaintenanceProposalDecision{
				Category: "topic",
				Decision: "rejected",
				Reason:   "blank_topic_or_rationale",
				Topic:    rawTopic,
			})
			continue
		}
		topicTokens := tokenizeNormalized(normTopic)
		if len(topicTokens) == 0 {
			filteredTopics++
			proposalDecisions = append(proposalDecisions, domain.MaintenanceProposalDecision{
				Category: "topic",
				Decision: "rejected",
				Reason:   "empty_normalized_tokens",
				Topic:    rawTopic,
			})
			continue
		}
		if seenAcceptedTopics[normTopic] {
			filteredTopics++
			proposalDecisions = append(proposalDecisions, domain.MaintenanceProposalDecision{
				Category: "topic",
				Decision: "rejected",
				Reason:   "duplicate_topic",
				Topic:    normTopic,
			})
			continue
		}

		grounded, usedParaphraseOrStem, convertDeclaredGap := evaluateTopicFocalGrounding(
			topicName,
			normTopic,
			topicTokens,
			top.Rationale,
			focalSig,
			focalSourceTerms,
			focalSourceStems,
		)
		// T-008-02-03: If the topic is a declared scope/source/reference theme whose tokens are absent from the focal body/source,
		// convert it to a bounded new_version_proposal content gap rather than assigning a false topic coverage tag.
		if convertDeclaredGap {
			filteredTopics++
			gapUnc := "bounded_shortlist_only"
			if focalPartial {
				gapUnc = "partial_source"
			} else if !focalHasVerifiedSrc {
				gapUnc = "source_unverified"
			}
			gapQueue = append(gapQueue, candidateGapEntry{
				gap: domain.MaintenanceGapProposal{
					Kind:        "new_version_proposal",
					Theme:       normTopic,
					Rationale:   top.Rationale,
					Uncertainty: gapUnc,
				},
				converted: true,
			})
			proposalDecisions = append(proposalDecisions, domain.MaintenanceProposalDecision{
				Category:    "topic",
				Decision:    "converted_to_content_gap",
				Reason:      "declared_theme_absent_from_focal_body",
				Uncertainty: gapUnc,
				Topic:       normTopic,
			})
			continue
		}

		if !grounded {
			filteredTopics++
			proposalDecisions = append(proposalDecisions, domain.MaintenanceProposalDecision{
				Category: "topic",
				Decision: "rejected",
				Reason:   "ungrounded_tokens_in_focal_scope",
				Topic:    normTopic,
			})
			continue
		}

		seenAcceptedTopics[normTopic] = true
		coveredTopics[topicName] = true
		coveredTopics[normTopic] = true

		if topicMode == "disabled" {
			proposalDecisions = append(proposalDecisions, domain.MaintenanceProposalDecision{
				Category: "topic",
				Decision: "skipped",
				Reason:   "policy_disabled",
				Topic:    normTopic,
			})
			continue
		}
		topUnc := strings.ToLower(strings.TrimSpace(top.Uncertainty))
		if topUnc == "" || topUnc == "none" {
			if focalPartial {
				topUnc = "partial_source"
			} else if !focalHasVerifiedSrc || len(focalCV.Targets) == 0 {
				topUnc = "source_unverified"
			} else {
				topUnc = "none"
			}
		}
		evList := []domain.FindingEvidence{focalEv}
		evList = append(evList, focalVerifiedEv...)
		// This discriminator describes the already established grounding. It does
		// not change topic admission, source input, ranking or historical output.
		basis := "explanation_text"
		if focalHasVerifiedSrc {
			basis = "source_verified"
		}
		proposal := map[string]any{
			"evidence_basis": basis,
			"origin_unit": map[string]any{
				"project_id": curItem.ProjectID, "document_id": focalDoc.ID,
				"version_id": curItem.VersionID, "page_path": curItem.PagePath,
				"region_id": curItem.RegionID,
			},
			"action":                "assign_topic",
			"topic":                 normTopic,
			"version_id":            curItem.VersionID,
			"page_path":             curItem.PagePath,
			"region_id":             curItem.RegionID,
			"shortlist_fingerprint": intent.Shortlist.ShortlistFingerprint,
		}
		if rawTopic != normTopic {
			proposal["raw_topic"] = rawTopic
		}
		fpPayload := map[string]any{
			"version_id": curItem.VersionID,
			"page_path":  curItem.PagePath,
			"region_id":  curItem.RegionID,
			"topic":      normTopic,
		}
		var tsPtr *string
		if focalCV.TargetSetID != "" {
			ts := focalCV.TargetSetID
			tsPtr = &ts
		}
		if err := putFinding(
			"topic_assignment",
			nullableStr(focalDoc.ID),
			nullableStr(curItem.VersionID),
			nullableStr(curItem.PagePath),
			nullableStr(curItem.RegionID),
			tsPtr,
			evList,
			fpPayload,
			top.Rationale,
			topUnc,
			proposal,
		); err != nil {
			return err
		}
		topicReason := "grounded_in_focal_explanation"
		if usedParaphraseOrStem {
			topicReason = "grounded_paraphrase_in_focal_scope"
		}
		proposalDecisions = append(proposalDecisions, domain.MaintenanceProposalDecision{
			Category:    "topic",
			Decision:    "accepted",
			Reason:      topicReason,
			Uncertainty: topUnc,
			Topic:       normTopic,
		})
	}

	// 3. Evaluate Content Gaps (explicit + declared missing-theme gaps) (T-008-02-03, QR1, QR4).
	seenGapThemes := map[string]bool{}
	if contentMode != "disabled" {
		for _, entry := range gapQueue {
			gap := entry.gap
			theme := strings.TrimSpace(gap.Theme)
			normTheme := normalizeTopicLabel(theme)
			rationale := strings.TrimSpace(gap.Rationale)
			gapKind := gap.Kind
			if theme == "" || normTheme == "" || rationale == "" {
				filteredGaps++
				proposalDecisions = append(proposalDecisions, domain.MaintenanceProposalDecision{
					Category: "content_gap",
					Decision: "rejected",
					Reason:   "blank_theme_or_rationale",
					Kind:     gapKind,
					Theme:    theme,
				})
				continue
			}
			if gapKind != "new_document_proposal" && gapKind != "new_version_proposal" {
				filteredGaps++
				proposalDecisions = append(proposalDecisions, domain.MaintenanceProposalDecision{
					Category: "content_gap",
					Decision: "rejected",
					Reason:   "invalid_gap_kind",
					Kind:     gapKind,
					Theme:    normTheme,
				})
				continue
			}
			if seenGapThemes[normTheme] {
				filteredGaps++
				proposalDecisions = append(proposalDecisions, domain.MaintenanceProposalDecision{
					Category: "content_gap",
					Decision: "rejected",
					Reason:   "duplicate_content_gap",
					Kind:     gapKind,
					Theme:    normTheme,
				})
				continue
			}
			gapTokens := tokenizeNormalized(normTheme)
			coveredByExactTopic := (coveredTopics[strings.ToLower(theme)] || coveredTopics[normTheme]) && (entry.converted || !rationaleIdentifiesMissingFocalDetail(rationale))
			if coveredByExactTopic {
				filteredGaps++
				proposalDecisions = append(proposalDecisions, domain.MaintenanceProposalDecision{
					Category: "content_gap",
					Decision: "rejected",
					Reason:   "already_explained_in_focal_scope",
					Kind:     gapKind,
					Theme:    normTheme,
				})
				continue
			}

			cleanRationale, withdrewUnsupportedClaim, unsupportedRejectReason := evaluateGapUnsupportedClaims(rationale, focalHasVerifiedSrc, entry.converted)
			if unsupportedRejectReason != "" {
				filteredGaps++
				proposalDecisions = append(proposalDecisions, domain.MaintenanceProposalDecision{
					Category: "content_gap",
					Decision: "rejected",
					Reason:   unsupportedRejectReason,
					Kind:     gapKind,
					Theme:    normTheme,
				})
				continue
			}

			gapReason := "missing_declared_focal_theme"
			if !entry.converted {
				allInFocalBodyOrHead := len(gapTokens) > 0
				allInFocalHeadAndBody := len(gapTokens) > 0
				anyRelatedToFocalScope := false
				for _, tok := range gapTokens {
					st := canonicalStem(tok)
					inBody := focalSig.BodyTerms[tok] || focalSig.BodyStems[st]
					inHead := focalSig.HeadingTerms[tok] || focalSig.HeadingStems[st]
					inDecl := focalSig.DeclaredTerms[tok] || focalSig.DeclaredStems[st]
					inSrc := focalSourceTerms[tok] || focalSourceStems[st]
					if inBody || inHead || inDecl || inSrc {
						anyRelatedToFocalScope = true
					}
					if !inBody && !inHead && !genericMechanismTokens[tok] && !genericMechanismTokens[st] {
						allInFocalBodyOrHead = false
					}
					if !(inBody && inHead) && !genericMechanismTokens[tok] && !genericMechanismTokens[st] {
						allInFocalHeadAndBody = false
					}
				}
				// QR1: A theme already explained by the focal content is NOT missing merely because shortlisted neighbors omit it.
				if allInFocalBodyOrHead && rationaleDefinesNeighborAbsence(cleanRationale) {
					filteredGaps++
					proposalDecisions = append(proposalDecisions, domain.MaintenanceProposalDecision{
						Category: "content_gap",
						Decision: "rejected",
						Reason:   "defined_only_by_neighbor_absence",
						Kind:     gapKind,
						Theme:    normTheme,
					})
					continue
				}
				if allInFocalHeadAndBody && !rationaleIdentifiesMissingFocalDetail(cleanRationale) {
					filteredGaps++
					proposalDecisions = append(proposalDecisions, domain.MaintenanceProposalDecision{
						Category: "content_gap",
						Decision: "rejected",
						Reason:   "already_explained_in_focal_scope",
						Kind:     gapKind,
						Theme:    normTheme,
					})
					continue
				}
				if !anyRelatedToFocalScope && !isDeclaredScopeTheme(focalSig, theme, gapTokens) {
					filteredGaps++
					proposalDecisions = append(proposalDecisions, domain.MaintenanceProposalDecision{
						Category: "content_gap",
						Decision: "rejected",
						Reason:   "ungrounded_in_focal_scope",
						Kind:     gapKind,
						Theme:    normTheme,
					})
					continue
				}
				gapReason = "missing_focal_explanation_detail"
			}
			if withdrewUnsupportedClaim {
				gapReason = "unsupported_claim_withdrawn_focal_detail_retained"
			}

			seenGapThemes[normTheme] = true
			unc := strings.ToLower(strings.TrimSpace(gap.Uncertainty))
			if unc == "" || unc == "none" || unc == "low" || withdrewUnsupportedClaim {
				if focalPartial {
					unc = "partial_source"
				} else if !focalHasVerifiedSrc || len(focalCV.Targets) == 0 {
					unc = "source_unverified"
				} else {
					unc = "bounded_shortlist_only"
				}
			}
			evList := []domain.FindingEvidence{focalEv}
			evList = append(evList, focalVerifiedEv...)
			proposal := map[string]any{
				"action":           gapKind,
				"theme":            theme,
				"normalized_theme": normTheme,
				"origin_unit": map[string]any{
					"project_id":  curItem.ProjectID,
					"document_id": focalDoc.ID,
					"version_id":  curItem.VersionID,
					"page_path":   curItem.PagePath,
					"region_id":   curItem.RegionID,
				},
				"inspected_scope": map[string]any{
					"scan_job_id":            run.JobID,
					"project_ids":            run.RequestedScope.ProjectIDs,
					"shortlisted_candidates": len(intent.Shortlist.Candidates),
					"shortlist_fingerprint":  intent.Shortlist.ShortlistFingerprint,
				},
			}
			fpPayload := map[string]any{
				"origin_version_id": curItem.VersionID,
				"origin_page_path":  curItem.PagePath,
				"origin_region_id":  curItem.RegionID,
				"theme":             normTheme,
				"kind":              gapKind,
			}
			var tsPtr *string
			if focalCV.TargetSetID != "" {
				ts := focalCV.TargetSetID
				tsPtr = &ts
			}
			var docPtr, verPtr, pagePtr, regPtr *string
			if gapKind == "new_version_proposal" {
				docPtr = nullableStr(focalDoc.ID)
				verPtr = nullableStr(curItem.VersionID)
				pagePtr = nullableStr(curItem.PagePath)
				regPtr = nullableStr(curItem.RegionID)
			}
			if err := putFinding(
				gapKind,
				docPtr,
				verPtr,
				pagePtr,
				regPtr,
				tsPtr,
				evList,
				fpPayload,
				cleanRationale,
				unc,
				proposal,
			); err != nil {
				return err
			}
			proposalDecisions = append(proposalDecisions, domain.MaintenanceProposalDecision{
				Category:    "content_gap",
				Decision:    "accepted",
				Reason:      gapReason,
				Uncertainty: unc,
				Kind:        gapKind,
				Theme:       normTheme,
			})
		}
	}

	var mergedLimitations []map[string]any
	for _, lim := range intent.Limitations {
		m := map[string]any{
			"kind":   lim.Kind,
			"reason": lim.Reason,
		}
		if lim.TargetVersionID != "" {
			m["target_version_id"] = lim.TargetVersionID
		}
		if lim.Message != "" {
			m["detail"] = lim.Message
		}
		mergedLimitations = append(mergedLimitations, m)
	}
	for _, lim := range env.Limitations {
		reason := strings.TrimSpace(lim.Reason)
		if reason == "" {
			reason = strings.TrimSpace(lim.Code)
		}
		if reason == "" {
			reason = strings.TrimSpace(lim.Message)
		}
		m := map[string]any{
			"kind":   "provider_reported",
			"reason": reason,
		}
		if strings.TrimSpace(lim.TargetVersionID) != "" {
			m["target_version_id"] = strings.TrimSpace(lim.TargetVersionID)
		}
		if strings.TrimSpace(lim.Message) != "" {
			m["detail"] = strings.TrimSpace(lim.Message)
		}
		mergedLimitations = append(mergedLimitations, m)
	}

	// Update parent job_item detail and complete the item.
	var detail map[string]any
	_ = json.Unmarshal(curItem.Detail, &detail)
	if detail == nil {
		detail = map[string]any{}
	}
	detail["eval_job_id"] = j.ID
	detail["shortlisted_candidates"] = len(intent.Shortlist.Candidates)
	detail["inspected_candidates"] = len(intent.Shortlist.Candidates)
	detail["eligible_candidates_at_apply"] = eligibleCandidatesAtApply
	detail["filtered_relations"] = filteredRelations
	detail["downgraded_relations"] = downgradedRelations
	detail["filtered_topics"] = filteredTopics
	detail["filtered_gaps"] = filteredGaps
	detail["proposal_decisions"] = proposalDecisions
	if len(mergedLimitations) > 0 {
		detail["limitations"] = mergedLimitations
	}
	detail["provider_attempts_used"] = 1
	if j.ProviderAttempts != nil && j.ProviderAttempts.Count > 0 {
		detail["provider_attempts_used"] = j.ProviderAttempts.Count
	}
	detail["semantic_state"] = "assessed"
	delete(detail, "semantic_reason")
	if len(findingIDs) > 0 {
		curItem.FindingID = findingIDs[0]
		detail["finding_ids"] = findingIDs
	}
	rawDetail, _ := json.Marshal(detail)
	curItem.State = "completed"
	curItem.Reason = "ok"
	curItem.Stage = "complete"
	curItem.Detail = rawDetail
	curItem.UpdatedAt = nowStr
	if err := tx.UpdateJobItem(curItem); err != nil {
		return err
	}
	run.UpdatedAt = nowStr
	if err := tx.UpdateScanRun(run); err != nil {
		return err
	}
	resMap := map[string]any{
		"scan_job_id":                  run.JobID,
		"item_id":                      curItem.ItemID,
		"attempt_id":                   a.ID,
		"shortlisted_candidates":       len(intent.Shortlist.Candidates),
		"inspected_candidates":         len(intent.Shortlist.Candidates),
		"eligible_candidates_at_apply": eligibleCandidatesAtApply,
		"filtered_relations":           filteredRelations,
		"downgraded_relations":         downgradedRelations,
		"filtered_topics":              filteredTopics,
		"filtered_gaps":                filteredGaps,
		"proposal_decisions":           proposalDecisions,
		"findings_emitted":             len(findingIDs),
	}
	if len(mergedLimitations) > 0 {
		resMap["limitations"] = mergedLimitations
	}
	resBytes, _ := json.Marshal(resMap)
	return tx.SetJobResult(j.ID, resBytes)
}
