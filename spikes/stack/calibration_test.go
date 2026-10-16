package stackspike

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
	"unicode/utf8"

	_ "modernc.org/sqlite"
)

// This opt-in experiment uses the already selected driver. It is neither a
// production search service nor a crash-durability or physical-client test.
func TestContractCalibration(t *testing.T) {
	output := os.Getenv("HAERYONG_CALIBRATION_OUTPUT")
	if output == "" {
		t.Skip("set an explicit agent-owned output directory for contract calibration")
	}
	if !filepath.IsAbs(output) {
		t.Fatal("calibration output must be an explicit absolute owned directory")
	}
	if err := os.Mkdir(output, 0700); err != nil {
		t.Fatal("exclusive output directory:", err)
	}
	planBytes, err := os.ReadFile("../../tests/contracts/calibration-plan.json")
	if err != nil {
		t.Fatal(err)
	}
	hardwarePath := os.Getenv("HAERYONG_CALIBRATION_HARDWARE")
	if !filepath.IsAbs(hardwarePath) {
		t.Fatal("explicit hardware/storage observation required")
	}
	hardwareBytes, err := os.ReadFile(hardwarePath)
	if err != nil || !json.Valid(hardwareBytes) {
		t.Fatal("hardware/storage JSON", err)
	}
	var plan struct {
		Dataset struct {
			Documents int `json:"documents"`
			Versions  int `json:"versions"`
			Edges     int `json:"canonical_edges"`
			Topics    []struct {
				ID     int    `json:"id"`
				Korean string `json:"Korean"`
				Code   string `json:"code"`
			} `json:"topics"`
		} `json:"dataset"`
		Queries []struct {
			ID       string `json:"id"`
			Query    string `json:"query"`
			Relevant []int  `json:"relevant_topics"`
		} `json:"queries"`
	}
	if err := json.Unmarshal(planBytes, &plan); err != nil {
		t.Fatal(err)
	}
	if plan.Dataset.Documents != 1000 || plan.Dataset.Versions != 5000 || plan.Dataset.Edges != 10000 || len(plan.Queries) != 40 {
		t.Fatal("unexpected predeclared workload")
	}
	path := filepath.Join(output, "calibration.sqlite3")
	open := func() *sql.DB {
		db, err := sql.Open("sqlite", path)
		if err != nil {
			t.Fatal(err)
		}
		db.SetMaxOpenConns(1)
		for _, statement := range []string{"PRAGMA journal_mode=WAL", "PRAGMA synchronous=FULL", "PRAGMA foreign_keys=ON", "PRAGMA busy_timeout=5000"} {
			if _, err := db.Exec(statement); err != nil {
				t.Fatal(err)
			}
		}
		for pragma, want := range map[string]string{"journal_mode": "wal", "synchronous": "2", "foreign_keys": "1", "busy_timeout": "5000"} {
			var got string
			if err := db.QueryRow("PRAGMA " + pragma).Scan(&got); err != nil || got != want {
				t.Fatalf("connection readback %s=%s want%s error%v", pragma, got, want, err)
			}
		}
		return db
	}
	db := open()
	defer db.Close()
	var engine string
	if err := db.QueryRow("SELECT sqlite_version()").Scan(&engine); err != nil || engine != "3.53.4" {
		t.Fatal("selected engine", engine, err)
	}
	for _, statement := range []string{
		"CREATE TABLE documents(id INTEGER PRIMARY KEY,topic INTEGER,title TEXT,allowed INTEGER,trashed INTEGER)",
		"CREATE TABLE versions(id INTEGER PRIMARY KEY,document_id INTEGER REFERENCES documents(id),sequence INTEGER,body TEXT)",
		"CREATE TABLE edges(src INTEGER REFERENCES documents(id),dst INTEGER REFERENCES documents(id),PRIMARY KEY(src,dst))",
		"CREATE INDEX version_document ON versions(document_id,sequence)",
		"CREATE VIRTUAL TABLE grams USING fts5(body,tokenize='trigram')",
		"CREATE TABLE cas(id INTEGER PRIMARY KEY,value INTEGER,revision INTEGER)",
		"INSERT INTO cas VALUES(1,0,1)",
	} {
		if _, err := db.Exec(statement); err != nil {
			t.Fatal(statement, err)
		}
	}
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	for id := 1; id <= 1000; id++ {
		topic := plan.Dataset.Topics[(id-1)%20]
		allowed, trashed := 1, 0
		if id%17 == 0 {
			allowed = 0
		}
		if id%19 == 0 {
			trashed = 1
		}
		title := topic.Korean + " " + topic.Code + fmt.Sprintf(" explanation %d", id)
		if _, err := tx.Exec("INSERT INTO documents VALUES(?,?,?,?,?)", id, topic.ID, title, allowed, trashed); err != nil {
			t.Fatal(err)
		}
		for sequence := 1; sequence <= 5; sequence++ {
			version := (id-1)*5 + sequence
			body := title + fmt.Sprintf(" source-controlled synthetic version%d: exact source, immutable evidence, unavailable scope.", sequence)
			if _, err := tx.Exec("INSERT INTO versions VALUES(?,?,?,?)", version, id, sequence, body); err != nil {
				t.Fatal(err)
			}
			if _, err := tx.Exec("INSERT INTO grams(rowid,body) VALUES(?,?)", version, body); err != nil {
				t.Fatal(err)
			}
		}
	}
	for id := 1; id <= 1000; id++ {
		for offset := 1; offset <= 10; offset++ {
			if _, err := tx.Exec("INSERT INTO edges VALUES(?,?)", id, (id+offset-1)%1000+1); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	counts := map[string]int{}
	for table, want := range map[string]int{"documents": 1000, "versions": 5000, "edges": 10000} {
		var got int
		if err := db.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&got); err != nil || got != want {
			t.Fatal(table, got, err)
		}
		counts[table] = got
	}
	idle := func() map[string]any {
		var before, after syscall.Rusage
		syscall.Getrusage(syscall.RUSAGE_SELF, &before)
		start := time.Now()
		time.Sleep(250 * time.Millisecond)
		syscall.Getrusage(syscall.RUSAGE_SELF, &after)
		elapsed := time.Since(start).Seconds()
		cpu := float64(after.Utime.Sec-before.Utime.Sec+after.Stime.Sec-before.Stime.Sec) + float64(after.Utime.Usec-before.Utime.Usec+after.Stime.Usec-before.Stime.Usec)/1e6
		var mem runtime.MemStats
		runtime.ReadMemStats(&mem)
		unit := "KiB"
		if runtime.GOOS == "darwin" {
			unit = "bytes"
		}
		return map[string]any{"elapsed_seconds": elapsed, "process_cpu_seconds": cpu, "CPU_percent_one_core": 100 * cpu / elapsed, "Go_Alloc_bytes": mem.Alloc, "Go_Sys_bytes": mem.Sys, "process_maxrss_raw": after.Maxrss, "maxrss_unit": unit, "scope": "test process/SQLite fixture, not production daemon"}
	}
	idleBefore := idle()
	metadata := func(connection *sql.DB) float64 {
		start := time.Now()
		rows, err := connection.Query("SELECT d.id,d.title,v.id,(SELECT COUNT(*) FROM edges e WHERE e.src=d.id) FROM documents d JOIN versions v ON v.document_id=d.id AND v.sequence=5 WHERE d.allowed=1 AND d.trashed=0 ORDER BY d.id LIMIT 50")
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		count := 0
		for rows.Next() {
			var id, version, edges int
			var title string
			if err := rows.Scan(&id, &title, &version, &edges); err != nil || edges != 10 {
				t.Fatal("metadata scan", err)
			}
			count++
		}
		if rows.Err() != nil || count != 50 {
			t.Fatal("metadata count", count, rows.Err())
		}
		return float64(time.Since(start).Nanoseconds()) / 1e6
	}
	type result struct {
		Document int `json:"document_id"`
		Topic    int `json:"topic"`
		Allowed  int `json:"allowed"`
		Trashed  int `json:"trashed"`
	}
	search := func(connection *sql.DB, query string) ([]result, float64) {
		start := time.Now()
		var rows *sql.Rows
		var err error
		if utf8.RuneCountInString(query) < 3 {
			rows, err = connection.Query("SELECT d.id,d.topic,d.allowed,d.trashed FROM versions v JOIN documents d ON d.id=v.document_id WHERE v.sequence=5 AND d.allowed=1 AND d.trashed=0 AND instr(lower(v.body),lower(?))>0 ORDER BY instr(lower(d.title),lower(?))>0 DESC,d.id LIMIT 5", query, query)
		} else {
			literal := "\"" + strings.ReplaceAll(query, "\"", "\"\"") + "\""
			rows, err = connection.Query("SELECT d.id,d.topic,d.allowed,d.trashed FROM grams JOIN versions v ON v.id=grams.rowid JOIN documents d ON d.id=v.document_id WHERE grams MATCH ? AND v.sequence=5 AND d.allowed=1 AND d.trashed=0 ORDER BY instr(lower(d.title),lower(?))>0 DESC,d.id LIMIT 5", literal, query)
		}
		if err != nil {
			t.Fatal(query, err)
		}
		defer rows.Close()
		found := []result{}
		for rows.Next() {
			var row result
			if err := rows.Scan(&row.Document, &row.Topic, &row.Allowed, &row.Trashed); err != nil {
				t.Fatal(err)
			}
			if row.Allowed != 1 || row.Trashed != 0 {
				t.Fatal("denied/trash leakage", row)
			}
			found = append(found, row)
		}
		if rows.Err() != nil {
			t.Fatal(rows.Err())
		}
		return found, float64(time.Since(start).Nanoseconds()) / 1e6
	}
	var coldMetadata, warmMetadata, coldSearch, warmSearch []float64
	for i := 0; i < 100; i++ {
		connection := open()
		coldMetadata = append(coldMetadata, metadata(connection))
		connection.Close()
	}
	for i := 0; i < 200; i++ {
		warmMetadata = append(warmMetadata, metadata(db))
	}
	queryResults := []map[string]any{}
	misses := []string{}
	for _, query := range plan.Queries {
		connection := open()
		found, elapsed := search(connection, query.Query)
		connection.Close()
		coldSearch = append(coldSearch, elapsed)
		relevant := false
		for _, row := range found {
			for _, topic := range query.Relevant {
				if row.Topic == topic {
					relevant = true
				}
			}
		}
		if !relevant {
			misses = append(misses, query.ID)
		}
		queryResults = append(queryResults, map[string]any{"id": query.ID, "query": query.Query, "top5": found, "relevant_top5": relevant})
		for repeat := 0; repeat < 5; repeat++ {
			_, elapsed := search(db, query.Query)
			warmSearch = append(warmSearch, elapsed)
		}
	}
	// Independent complete eligibility check, not merely the displayed top five.
	var eligible, excluded, leak int
	if err := db.QueryRow("SELECT COUNT(*) FROM documents WHERE allowed=1 AND trashed=0").Scan(&eligible); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow("SELECT COUNT(*) FROM documents WHERE allowed=0 OR trashed=1").Scan(&excluded); err != nil || excluded == 0 {
		t.Fatal("exclusion positive control", excluded, err)
	}
	if err := db.QueryRow("SELECT COUNT(*) FROM (SELECT d.id,d.allowed,d.trashed FROM grams JOIN versions v ON v.id=grams.rowid JOIN documents d ON d.id=v.document_id WHERE grams MATCH 'synthetic' AND d.allowed=1 AND d.trashed=0) WHERE allowed=0 OR trashed=1").Scan(&leak); err != nil || leak != 0 {
		t.Fatal("independent complete result privacy", leak, err)
	}
	connections := []*sql.DB{open(), open(), open(), open()}
	defer func() {
		for _, connection := range connections {
			connection.Close()
		}
	}()
	wins, conflicts := 0, 0
	for round := 0; round < 20; round++ {
		var revisions [4]int
		for client, connection := range connections {
			if err := connection.QueryRow("SELECT revision FROM cas WHERE id=1").Scan(&revisions[client]); err != nil {
				t.Fatal(err)
			}
		}
		start := make(chan struct{})
		results := make(chan int64, 4)
		errors := make(chan error, 4)
		var wait sync.WaitGroup
		for client, connection := range connections {
			wait.Add(1)
			go func(connection *sql.DB, revision int) {
				defer wait.Done()
				<-start
				result, err := connection.Exec("UPDATE cas SET value=value+1,revision=revision+1 WHERE id=1 AND revision=?", revision)
				if err != nil {
					errors <- err
					return
				}
				count, err := result.RowsAffected()
				if err != nil {
					errors <- err
					return
				}
				results <- count
			}(connection, revisions[client])
		}
		close(start)
		wait.Wait()
		close(results)
		close(errors)
		for err := range errors {
			t.Fatal(err)
		}
		roundWins, roundConflicts := 0, 0
		for count := range results {
			if count == 1 {
				roundWins++
			} else if count == 0 {
				roundConflicts++
			} else {
				t.Fatal(count)
			}
		}
		if roundWins != 1 || roundConflicts != 3 {
			t.Fatal(round, roundWins, roundConflicts)
		}
		wins += roundWins
		conflicts += roundConflicts
	}
	var finalValue, finalRevision int
	if err := db.QueryRow("SELECT value,revision FROM cas WHERE id=1").Scan(&finalValue, &finalRevision); err != nil || finalValue != 20 || finalRevision != 21 {
		t.Fatal("CAS lost write", finalValue, finalRevision, err)
	}
	// Actual rollback on the temporary chosen SQLite DB; model snapshots elsewhere
	// remain simulated recovery rather than process/crash/disk durability evidence.
	rollback, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := rollback.Exec("UPDATE cas SET value=999 WHERE id=1"); err != nil {
		t.Fatal(err)
	}
	if err := rollback.Rollback(); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow("SELECT value FROM cas WHERE id=1").Scan(&finalValue); err != nil || finalValue != 20 {
		t.Fatal("rollback", finalValue, err)
	}
	idleAfter := idle()
	if _, err := db.Exec("PRAGMA wal_checkpoint(TRUNCATE)"); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	var walBytes int64
	if wal, err := os.Stat(path + "-wal"); err == nil {
		walBytes = wal.Size()
	}
	percentiles := func(samples []float64) map[string]any {
		values := append([]float64(nil), samples...)
		sort.Float64s(values)
		return map[string]any{"n": len(values), "p50_ms": values[int(math.Ceil(.50*float64(len(values))))-1], "p95_ms": values[int(math.Ceil(.95*float64(len(values))))-1]}
	}
	cold, warm := percentiles(coldMetadata), percentiles(warmMetadata)
	quality := float64(40-len(misses)) / 40
	report := map[string]any{
		"kind":   "Executed real selected-SQLite synthetic calibration; no production/physical-device claim",
		"engine": engine, "Go": runtime.Version(), "GOOS": runtime.GOOS, "GOARCH": runtime.GOARCH,
		"hardware_storage_observation": json.RawMessage(hardwareBytes),
		"counts":                       counts, "metadata_cold": cold, "metadata_warm": warm,
		"search_cold": percentiles(coldSearch), "search_warm": percentiles(warmSearch),
		"cold_limit": "New SQLite connection/cache; operating-system file cache uncontrolled and likely warm; connection setup excluded from measured SQL+scan",
		"db_bytes":   info.Size(), "wal_bytes_after_checkpoint": walBytes,
		"idle_before": idleBefore, "idle_after": idleAfter,
		"query_results": queryResults, "topical_top5_fraction": quality, "misses": misses,
		"privacy":     map[string]any{"eligible_documents": eligible, "excluded_positive_control": excluded, "denied_trash_leaks": leak, "independent_complete_result_check": true},
		"concurrency": map[string]any{"simulated_owner_clients": 3, "configured_background_workers": 1, "real_connections": 4, "rounds": 20, "CAS_wins": wins, "conflicts": conflicts, "final_value": finalValue, "final_revision": finalRevision, "rollback_retained_value": 20},
		"decision":    map[string]any{"metadata_initial_target_met": cold["p95_ms"].(float64) < 300 && warm["p95_ms"].(float64) < 300, "topical_initial_target_met": quality >= .9, "privacy_and_CAS_required": "pass", "miss_disposition": "Diagnostic-only for bounded calibration; report all misses for TASK-004-02/TASK-010-04. No universal releaseSLO or content cap."},
		"plan":        json.RawMessage(planBytes),
	}
	reportBytes, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(output, "report.json"), append(reportBytes, '\n'), 0600); err != nil {
		t.Fatal(err)
	}
	t.Logf("calibration complete: metadata cold p95 %.3fms warm %.3fms; topical %d/40; privacy0; CAS%d conflicts%d", cold["p95_ms"], warm["p95_ms"], 40-len(misses), wins, conflicts)
}
