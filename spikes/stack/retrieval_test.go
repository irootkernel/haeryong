package stackspike

import (
	"database/sql"
	"slices"
	"testing"

	_ "modernc.org/sqlite"
)

// Isolate token families so a snake_case hit cannot masquerade as camelCase splitting.
func TestIsolatedRetrievalCandidates(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	for _, q := range []string{
		"CREATE TABLE corpus(id INTEGER PRIMARY KEY, body TEXT, allowed INTEGER, trashed INTEGER)",
		"CREATE VIRTUAL TABLE words USING fts5(body, tokenize='unicode61')",
		"CREATE VIRTUAL TABLE grams USING fts5(body, tokenize='trigram')",
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	rows := []struct {
		body             string
		allowed, trashed int
	}{
		{"검토 승인 재시도", 1, 0}, {"source_access", 1, 0}, {"readSource", 1, 0},
		{"source/path.go", 1, 0}, {"재 시도", 1, 0}, {"검토 비공개", 0, 0}, {"검토 휴지통", 1, 1},
		{"승인 검토", 1, 0},
	}
	for i, row := range rows {
		id := i + 1
		if _, err := db.Exec("INSERT INTO corpus VALUES(?,?,?,?)", id, row.body, row.allowed, row.trashed); err != nil {
			t.Fatal(err)
		}
		for _, table := range []string{"words", "grams"} {
			if _, err := db.Exec("INSERT INTO "+table+"(rowid,body) VALUES(?,?)", id, row.body); err != nil {
				t.Fatal(err)
			}
		}
	}
	cases := []struct {
		table, query string
		id, want     int
	}{
		{"words", "검토", 1, 1}, {"words", "검", 1, 0}, {"words", "검*", 1, 1},
		{"words", "\"검토 승인\"", 1, 1},
		{"words", "재시도", 1, 1}, {"words", "\"재 시도\"", 1, 0}, {"words", "\"재 시도\"", 5, 1},
		{"words", "source", 2, 1}, {"words", "\"source_access\"", 2, 1},
		{"words", "readSource", 3, 1}, {"words", "source", 3, 0},
		{"words", "\"source/path.go\"", 4, 1}, {"words", "go", 4, 1},
		{"grams", "검토", 1, 0}, {"grams", "재시도", 1, 1}, {"grams", "source", 3, 1},
		{"grams", "\"path.go\"", 4, 1},
		{"words", "\"검토 승인\"", 8, 0}, {"words", "검토 승인", 8, 1},
		{"words", "\"source-path.go\"", 4, 1}, {"grams", "\"source-path.go\"", 4, 0},
	}
	for _, c := range cases {
		var n int
		err := db.QueryRow("SELECT COUNT(*) FROM "+c.table+" WHERE rowid=? AND "+c.table+" MATCH ?", c.id, c.query).Scan(&n)
		if err != nil || n != c.want {
			t.Errorf("%s row%d %q expected%d observed%d error%v", c.table, c.id, c.query, c.want, n, err)
		}
		t.Logf("%s row%d %q expected=%d observed=%d", c.table, c.id, c.query, c.want, n)
	}
	// Short-query fallback operates on authorized, non-trash corpus rows, not an empty FTS set.
	for _, query := range []string{"검", "검토"} {
		matches, err := db.Query("SELECT id FROM corpus WHERE allowed=1 AND trashed=0 AND instr(body,?)>0 ORDER BY id", query)
		if err != nil {
			t.Fatal(query, err)
		}
		var ids []int
		for matches.Next() {
			var id int
			if err := matches.Scan(&id); err != nil {
				t.Fatal(query, err)
			}
			ids = append(ids, id)
		}
		err = matches.Err()
		matches.Close()
		if err != nil || !slices.Equal(ids, []int{1, 8}) {
			t.Fatal(query, ids, err)
		}
		t.Logf("authorized corpus fallback %q expected IDs=[1 8] observed=%v", query, ids)
	}
	for _, table := range []string{"words", "grams"} {
		for _, term := range []string{"비공개", "휴지통"} {
			var count int
			if err := db.QueryRow("SELECT COUNT(*) FROM "+table+" WHERE "+table+" MATCH ?", term).Scan(&count); err != nil || count != 1 {
				t.Fatal("unfiltered exclusion control", table, term, count, err)
			}
		}
		for _, c := range []struct {
			query string
			want  int
		}{{"재시도", 1}, {"비공개", 0}, {"휴지통", 0}} {
			var count int
			query := "SELECT COUNT(*) FROM " + table + " JOIN corpus ON corpus.id=" + table + ".rowid WHERE " + table + " MATCH ? AND allowed=1 AND trashed=0"
			if err := db.QueryRow(query, c.query).Scan(&count); err != nil || count != c.want {
				t.Fatal(table, c.query, count, err)
			}
			t.Logf("%s authorized corpus %q expected=%d observed=%d", table, c.query, c.want, count)
		}
	}
}
