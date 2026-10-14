package stackspike

import (
	"bytes"
	"database/sql"
	"flag"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

// A disposable engine experiment; not a production backup implementation.
func TestSnapshotAcrossConcurrentWrite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "live.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(2)
	for _, q := range []string{"PRAGMA journal_mode=WAL", "CREATE TABLE pair (id INTEGER PRIMARY KEY, generation INTEGER)", "INSERT INTO pair VALUES (1,1),(2,1)"} {
		if _, err := db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	reader, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Rollback()
	var before int
	if err := reader.QueryRow("SELECT SUM(generation) FROM pair").Scan(&before); err != nil || before != 2 {
		t.Fatal(before, err)
	}
	writer, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Exec("UPDATE pair SET generation=2"); err != nil {
		t.Fatal(err)
	}
	if err := writer.Commit(); err != nil {
		t.Fatal(err)
	}
	var old int
	if err := reader.QueryRow("SELECT SUM(generation) FROM pair").Scan(&old); err != nil || old != 2 {
		t.Fatal(old, err)
	}
	if err := reader.Commit(); err != nil {
		t.Fatal(err)
	}
	// Keep a second transaction uncommitted throughout the backup read.
	pending, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer pending.Rollback()
	if _, err := pending.Exec("UPDATE pair SET generation=3"); err != nil {
		t.Fatal(err)
	}
	backup := filepath.Join(t.TempDir(), "snapshot.db")
	if _, err := db.Exec("VACUUM INTO ?", backup); err != nil {
		t.Fatal(err)
	}
	if err := pending.Commit(); err != nil {
		t.Fatal(err)
	}
	copyDB, err := sql.Open("sqlite", backup)
	if err != nil {
		t.Fatal(err)
	}
	defer copyDB.Close()
	var captured int
	if err := copyDB.QueryRow("SELECT SUM(generation) FROM pair").Scan(&captured); err != nil || captured != 4 {
		t.Fatal(captured, err)
	}
	var integrity string
	if err := copyDB.QueryRow("PRAGMA integrity_check").Scan(&integrity); err != nil || integrity != "ok" {
		t.Fatal(integrity, err)
	}
	var live int
	if err := db.QueryRow("SELECT SUM(generation) FROM pair").Scan(&live); err != nil || live != 6 {
		t.Fatal(live, err)
	}
	for _, id := range []int{1, 2} {
		var snapshotGeneration, liveGeneration int
		if err := copyDB.QueryRow("SELECT generation FROM pair WHERE id=?", id).Scan(&snapshotGeneration); err != nil || snapshotGeneration != 2 {
			t.Fatal(id, snapshotGeneration, err)
		}
		if err := db.QueryRow("SELECT generation FROM pair WHERE id=?", id).Scan(&liveGeneration); err != nil || liveGeneration != 3 {
			t.Fatal(id, liveGeneration, err)
		}
	}
}

var restartChildDatabase = flag.String("stack-restart-db", "", "Disposable restart-child database")

func TestProcessRestartChild(t *testing.T) {
	if *restartChildDatabase == "" {
		t.Skip("Only executed by the restart parent")
	}
	db, err := sql.Open("sqlite", *restartChildDatabase)
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{"PRAGMA journal_mode=WAL", "PRAGMA synchronous=FULL", "CREATE TABLE results(id INTEGER PRIMARY KEY, body TEXT)", "INSERT INTO results VALUES(1,'committed')"} {
		if _, err := db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec("INSERT INTO results VALUES(2,'unfinished')"); err != nil {
		t.Fatal(err)
	}
	os.Exit(23) // Abrupt process exit with an open transaction; no DB close.

}

func TestProcessRestart(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "restart.db")
	sentinelPath := filepath.Join(directory, "inherited.db")
	sentinel, err := sql.Open("sqlite", sentinelPath)
	if err != nil {
		t.Fatal(err)
	}
	defer sentinel.Close()
	for _, query := range []string{"CREATE TABLE sentinel(body TEXT)", "INSERT INTO sentinel VALUES('unchanged')"} {
		if _, err := sentinel.Exec(query); err != nil {
			t.Fatal(err)
		}
	}
	if err := sentinel.Close(); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(sentinelPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("HAERYONG_STACK_RESTART_DB", sentinelPath)
	cmd := exec.Command(os.Args[0], "-test.run=^TestProcessRestartChild$", "-stack-restart-db="+path)
	err = cmd.Run()
	exit, ok := err.(*exec.ExitError)
	if !ok || exit.ExitCode() != 23 {
		t.Fatalf("child result: %v", err)
	}
	after, err := os.ReadFile(sentinelPath)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("inherited database bytes changed", err)
	}
	sentinel, err = sql.Open("sqlite", sentinelPath)
	if err != nil {
		t.Fatal(err)
	}
	defer sentinel.Close()
	var tables, body string
	if err := sentinel.QueryRow("SELECT group_concat(name,',') FROM (SELECT name FROM sqlite_master WHERE type='table' ORDER BY name)").Scan(&tables); err != nil || tables != "sentinel" {
		t.Fatal("inherited schema changed", tables, err)
	}
	if err := sentinel.QueryRow("SELECT group_concat(body,',') FROM sentinel").Scan(&body); err != nil || body != "unchanged" {
		t.Fatal("inherited rows changed", body, err)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var count int
	if err := db.QueryRow("SELECT COUNT(*) FROM results WHERE body='committed'").Scan(&count); err != nil || count != 1 {
		t.Fatal(count, err)
	}
	if err := db.QueryRow("SELECT COUNT(*) FROM results WHERE body='unfinished'").Scan(&count); err != nil || count != 0 {
		t.Fatal(count, err)
	}
}
