package sqlite

import (
	"encoding/json"
	"haeryong.local/haeryong/internal/domain"
	"io"
	"strings"
	"time"
	"unicode/utf8"
)

// Eligibility precedes every result, coverage, snippet and cursor projection.
// This intentionally uses version.read, independently of scopeValues' project.read.
const searchEligible = `WITH eligible AS MATERIALIZED (
 SELECT v.*, d.project_id,d.lifecycle FROM document_versions v
 JOIN documents d ON d.document_id=v.document_id JOIN projects p ON p.project_id=d.project_id
 WHERE v.sequence>0 AND d.lifecycle NOT IN ('trash','purged')
 AND (? OR (? AND d.project_id IN (SELECT value FROM json_each(?)) AND v.version_id IN (SELECT value FROM json_each(?))))
 AND (?='' OR d.project_id=?) AND (?='all' OR d.lifecycle=?)
 AND (?='all' OR d.latest_version_id=v.version_id)
)`

func searchArgs(o domain.SearchSelection) []any {
	owner, _, projects, versions := scopeValues(o.Scope)
	read := admits("version.read", o.Scope.Actions...)
	return []any{owner, read, projects, versions, o.Project, o.Project, o.Lifecycle, o.Lifecycle, o.Versions}
}

// The first bounded version page seeds a snapshot; subsequent pages use the
// same physical cursor, yielding even across versions outside the grant.
func (t *writeTx) SearchCorpus(o domain.SearchSelection) ([]domain.SearchVersionState, error) {
	page, err := t.SearchSnapshotSlice(o, "versions", 0)
	return page.Versions, err
}
func (t *writeTx) SearchHighWater() (int64, error) {
	var id int64
	err := t.tx.QueryRowContext(t.ctx, `SELECT coalesce(max(chunk_id),0) FROM search_chunks`).Scan(&id)
	return id, err
}
func (t *writeTx) SearchCandidates(o domain.SearchSelection, literal string, afterID, through int64) (out domain.SearchSlice, err error) {
	var started time.Time
	ids := []int64{}
	versions := map[string]bool{}
	scan, e := t.tx.QueryContext(t.ctx, `SELECT c.chunk_id,u.version_id FROM search_chunks c JOIN search_units u ON u.unit_id=c.unit_id WHERE c.chunk_id>? AND c.chunk_id<=? ORDER BY c.chunk_id LIMIT ?`, afterID, through, domain.SearchSliceRows)
	if e != nil {
		return out, e
	}
	started = time.Now()
	for scan.Next() {
		var id int64
		var version string
		if e = scan.Scan(&id, &version); e != nil {
			scan.Close()
			return out, e
		}
		ids = append(ids, id)
		versions[version] = true
		out.After = id
		if time.Since(started) >= domain.SearchSliceTime {
			break
		}
	}
	e = scan.Err()
	scan.Close()
	if e != nil {
		return out, e
	}
	out.Examined = len(ids)
	if len(ids) == 0 {
		return out, nil
	}
	// Restrict only the SQL membership input to this physical slice. The scope
	// was freshly authenticated for this transaction; no cached grant or hidden
	// version is admitted. Avoid rebuilding a thousands-entry SQL IN set for
	// each bounded slice of a large workflow credential.
	if o.Scope.Kind != "owner" {
		if o.VersionSet == nil {
			o.VersionSet = map[string]bool{}
			for _, id := range o.Scope.VersionIDs {
				o.VersionSet[id] = true
			}
		}
		allowed := []string{}
		for version := range versions {
			if o.VersionSet[version] {
				allowed = append(allowed, version)
			}
		}
		o.Scope.VersionIDs = allowed
	}
	before, after := domain.SearchChunkSize-domain.SearchChunkOverlap-1, domain.SearchChunkOverlap+1
	args := append(searchArgs(o), before, before, after, after, jsonBytes(ids))
	// Quote the entire bound value; user punctuation never becomes MATCH syntax.
	// A bounded prefix nominates a superset for long phrases. The full literal
	// postfilter remains authoritative; repeated thousands of trigrams otherwise
	// cause disproportionate FTS phrase work despite the admitted query bound.
	nomination := []rune(literal)
	if len(nomination) > 3 {
		nomination = nomination[:3]
	}
	match := `"` + strings.ReplaceAll(string(nomination), `"`, `""`) + `"`
	// Each FTS iterator is opened once for this physical slice, rather than
	// reopened per chunk. Both rowid ranges remain inside the preselected rows.
	candidate := `c.chunk_id IN (SELECT rowid FROM search_words WHERE search_words MATCH ? AND rowid BETWEEN ? AND ? UNION SELECT rowid FROM search_grams WHERE search_grams MATCH ? AND rowid BETWEEN ? AND ?)`
	if utf8.RuneCountInString(literal) < 3 {
		// Direct fallback covers the eligible ready corpus, even if both FTS sets are empty.
		candidate = `(instr(c.title,?)>0 OR instr(c.path,?)>0 OR instr(c.terms,?)>0 OR instr(c.body,?)>0)`
		args = append(args, literal, literal, literal, literal)
	} else {
		args = append(args, match, ids[0], ids[len(ids)-1], match, ids[0], ids[len(ids)-1])
	}
	args = append(args, o.Kind, o.Kind)
	rows, err := t.tx.QueryContext(t.ctx, strings.Replace(searchEligible, "AS MATERIALIZED", "AS NOT MATERIALIZED", 1)+`
 SELECT u.unit_id,u.version_id,u.kind,u.page_path,u.region_id,u.source_id,u.title,u.input_hash,u.read_state,u.read_body_state,u.read_anchor_state,u.reason,
 c.chunk_id,c.part,c.ordinal,c.title,c.path,c.terms,c.body,coalesce(nullif(c.display_body,''),(SELECT d.display_body FROM search_chunks d WHERE d.unit_id=c.unit_id AND d.part=c.part AND d.ordinal=c.ordinal+1),''),CASE WHEN c.part='metadata' AND u.kind='page' THEN (SELECT p.metadata FROM version_pages p WHERE p.version_id=u.version_id AND p.page_path=u.page_path) ELSE NULL END,
 CASE WHEN c.part='body' THEN coalesce((SELECT CASE WHEN c.ordinal%2=0 AND substr(c.body,1,1)<>char(1) THEN substr(d.body,?,2) ELSE substr(d.display_body,?,2) END FROM search_chunks d WHERE d.unit_id=c.unit_id AND d.part=c.part AND d.ordinal=c.ordinal-2),'') ELSE '' END,
 CASE WHEN c.part='body' THEN coalesce((SELECT CASE WHEN c.ordinal%2=0 AND substr(c.body,1,1)<>char(1) THEN substr(d.body,?,2) ELSE substr(d.display_body,?,2) END FROM search_chunks d WHERE d.unit_id=c.unit_id AND d.part=c.part AND d.ordinal=c.ordinal+2),'') ELSE '' END,
 i.extractor_revision,coalesce((SELECT d.display_body FROM search_chunks d WHERE d.unit_id=c.unit_id AND d.part=c.part AND d.ordinal=c.ordinal+1),'')
 FROM eligible e JOIN search_indexes i ON i.version_id=e.version_id AND i.state<>'pending'
 JOIN (`+searchReadUnits+`) u ON u.version_id=e.version_id AND u.read_state='ready'
 JOIN search_chunks c ON c.unit_id=u.unit_id AND (c.part='metadata' OR u.read_body_state='complete')
 WHERE c.chunk_id IN (SELECT value FROM json_each(?)) AND `+candidate+` AND (?='' OR u.kind=?) ORDER BY c.chunk_id`, args...)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	started = time.Now()
	for rows.Next() {
		var v domain.SearchCandidate
		var revision int64
		var authoredNeighbor string
		u, c := &v.Unit, &v.Chunk
		if err = rows.Scan(&u.ID, &u.VersionID, &u.Kind, &u.PagePath, &u.RegionID, &u.SourceID, &u.Title, &u.InputHash, &u.State, &u.BodyState, &u.AnchorState, &u.Reason, &c.ID, &c.Part, &c.Ordinal, &c.Title, &c.Path, &c.Terms, &c.Body, &c.DisplayBody, &v.Metadata, &v.Before, &v.After, &revision, &authoredNeighbor); err != nil {
			return out, err
		}
		if err = validateSearchUnit(*u); err != nil {
			return out, err
		}
		if !admits(c.Part, "metadata", "body") {
			return out, domain.ErrUnsupportedDerivedData
		}
		if revision >= 2 && strings.HasPrefix(c.Body, domain.SearchNominationPrefix) {
			if c.Part == "metadata" {
				var display struct {
					Literal *string `json:"literal"`
					Display *string `json:"display"`
				}
				decoder := json.NewDecoder(strings.NewReader(c.DisplayBody))
				decoder.DisallowUnknownFields()
				if err = decoder.Decode(&display); err != nil {
					return out, domain.ErrUnsupportedDerivedData
				}
				if display.Literal == nil || display.Display == nil {
					return out, domain.ErrUnsupportedDerivedData
				}
				var trailing any
				if decoder.Decode(&trailing) != io.EOF {
					return out, domain.ErrUnsupportedDerivedData
				}
				c.Body, c.DisplayBody = *display.Literal, *display.Display
			} else if c.Ordinal%2 == 0 {
				c.Body, c.DisplayBody = c.DisplayBody, authoredNeighbor
			} else {
				c.Body = ""
			}
		}
		c.UnitID = u.ID
		out.Candidates = append(out.Candidates, v)
		if time.Since(started) >= domain.SearchSliceTime {
			// Rows are ordered by physical id, so the next slice revisits every
			// unconsumed candidate, including misses following this row.
			out.After = c.ID
			break
		}
	}
	return out, rows.Err()
}
