package stackspike

import (
	"database/sql"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

// Engine capability probe; production search and power-loss durability are separate obligations.
func TestSQLiteCapabilities(t *testing.T) {
	path := filepath.Join(t.TempDir(), "source.sqlite3")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, query := range []string{
		"PRAGMA journal_mode=WAL",
		"CREATE TABLE documents(id INTEGER PRIMARY KEY, body TEXT)",
		"CREATE VIRTUAL TABLE words USING fts5(body, tokenize='unicode61')",
		"CREATE VIRTUAL TABLE grams USING fts5(body, tokenize='trigram')",
	} {
		if _, err := db.Exec(query); err != nil {
			t.Fatal(query, err)
		}
	}
	text := "검토 승인 재시도 source_access readSource source/path.go"
	for _, table := range []string{"documents", "words", "grams"} {
		if _, err := db.Exec("INSERT INTO "+table+"(body) VALUES(?)", text); err != nil {
			t.Fatal(err)
		}
	}
	cases := []struct {
		table, query string
		want         int
	}{
		{"words", "검토", 1}, {"words", "검", 0}, {"words", "검*", 1},
		{"words", "재시도", 1}, {"words", "재 시도", 0},
		{"words", "source_access", 1}, {"words", "readSource", 1},
		{"words", "Source", 1}, {"grams", "검토", 0}, {"grams", "재시도", 1},
	}
	for _, c := range cases {
		var count int
		err := db.QueryRow("SELECT count(*) FROM "+c.table+" WHERE "+c.table+" MATCH ?", c.query).Scan(&count)
		if err != nil {
			t.Fatal(c, err)
		}
		t.Logf("%s MATCH %q: expected=%d observed=%d", c.table, c.query, c.want, count)
		if count != c.want {
			t.Errorf("%s %q: got %d want %d", c.table, c.query, count, c.want)
		}
	}
	var fallback int
	if err := db.QueryRow("SELECT count(*) FROM documents WHERE instr(body, ?) > 0", "검").Scan(&fallback); err != nil || fallback != 1 {
		t.Fatalf("short-query fallback: %d %v", fallback, err)
	}
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec("INSERT INTO documents(body) VALUES('rolled back')"); err != nil {
		t.Fatal(err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	backup := filepath.Join(t.TempDir(), "snapshot.sqlite3")
	if _, err := db.Exec("VACUUM INTO ?", backup); err != nil {
		t.Fatal(err)
	}
	copyDB, err := sql.Open("sqlite", backup)
	if err != nil {
		t.Fatal(err)
	}
	defer copyDB.Close()
	var count int
	if err := copyDB.QueryRow("SELECT count(*) FROM documents").Scan(&count); err != nil || count != 1 {
		t.Fatalf("snapshot: %d %v", count, err)
	}
	var version string
	if err := db.QueryRow("SELECT sqlite_version()").Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version != "3.53.4" {
		t.Fatalf("SQLite engine: got %s want 3.53.4", version)
	}
	t.Log("SQLite engine", version)
}
