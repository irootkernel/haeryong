package main

import (
	"database/sql"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sync"

	_ "modernc.org/sqlite"
)

func main() {
	assets := flag.String("assets", "", "Compiled fixture assets")
	endpoint := flag.String("endpoint", "", "Owned endpoint file")
	database := flag.String("database", "", "Disposable SQLite database")
	flag.Parse()
	if *assets == "" || *endpoint == "" || *database == "" {
		log.Fatal("assets, endpoint and database required")
	}
	index, err := os.Stat(filepath.Join(*assets, "index.html"))
	compiled, globErr := filepath.Glob(filepath.Join(*assets, "assets", "*.js"))
	if err != nil || !index.Mode().IsRegular() || globErr != nil || len(compiled) == 0 {
		log.Fatalf("compiled index.html and assets/*.js required in %s", *assets)
	}
	for _, path := range []string{*database, *endpoint} {
		parent, err := os.Lstat(filepath.Dir(path))
		if err != nil || !parent.IsDir() || parent.Mode().Perm() != 0700 {
			log.Fatal("database and endpoint require a private directory with mode 0700")
		}
		info, err := os.Lstat(path)
		if err == nil && !info.Mode().IsRegular() {
			log.Fatal("database and endpoint must not be symlinks or special files")
		}
		if err != nil && !os.IsNotExist(err) {
			log.Fatal(err)
		}
	}
	db, err := sql.Open("sqlite", *database)
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	for _, query := range []string{"PRAGMA journal_mode=WAL", "PRAGMA synchronous=FULL", "PRAGMA foreign_keys=ON", "PRAGMA busy_timeout=5000"} {
		if _, err := db.Exec(query); err != nil {
			log.Fatal(err)
		}
	}
	var engine string
	if err := db.QueryRow("SELECT sqlite_version()").Scan(&engine); err != nil {
		log.Fatal(err)
	}
	settings := map[string]any{}
	var journal string
	if err := db.QueryRow("PRAGMA journal_mode").Scan(&journal); err != nil {
		log.Fatal(err)
	}
	settings["journal_mode"] = journal
	for _, name := range []string{"synchronous", "foreign_keys", "busy_timeout"} {
		var value int
		if err := db.QueryRow("PRAGMA " + name).Scan(&value); err != nil {
			log.Fatal(err)
		}
		settings[name] = value
	}
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		log.Fatal(err)
	}
	defer listener.Close()
	instance := fmt.Sprintf("fixture-%d", os.Getpid())
	var mu sync.Mutex
	type attachment struct {
		Instance  string `json:"instance"`
		UserAgent string `json:"user_agent"`
	}
	attached := map[string]attachment{}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"fixture": "stack-spike", "instance": instance, "sqlite": engine, "settings": settings})
	})
	mux.HandleFunc("POST /attach/{client}", func(w http.ResponseWriter, r *http.Request) {
		var input attachment
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil || input.Instance != instance {
			http.Error(w, "fixture instance mismatch", http.StatusBadRequest)
			return
		}
		input.UserAgent = r.UserAgent()
		mu.Lock()
		attached[r.PathValue("client")] = input
		mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("GET /observations", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(attached)
	})
	mux.Handle("/", http.FileServer(http.Dir(*assets)))
	file, err := os.OpenFile(*endpoint, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		log.Fatal(err)
	}
	_, writeErr := file.WriteString("http://" + listener.Addr().String())
	closeErr := file.Close()
	if writeErr != nil || closeErr != nil {
		log.Fatalf("endpoint write failed: %v %v", writeErr, closeErr)
	}
	log.Fatal(http.Serve(listener, mux))
}
