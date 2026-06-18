package main

import (
	"database/sql"
	"fmt"
	"os"
)

// openEmbeddedDB writes the embedded SQLite bytes to a temp file and opens it.
// modernc sqlite can open from a file path; we use WAL mode for concurrent reads.
func openEmbeddedDB() (*sql.DB, error) {
	f, err := os.CreateTemp("", "vpic-*.sqlite")
	if err != nil {
		return nil, fmt.Errorf("temp file: %w", err)
	}
	if _, err := f.Write(sqliteData); err != nil {
		f.Close()
		return nil, fmt.Errorf("write db: %w", err)
	}
	path := f.Name()
	f.Close()

	// open read-only to avoid write-lock contention; WAL allows concurrent readers
	db, err := sql.Open("sqlite", path+"?mode=ro&_journal_mode=WAL&_busy_timeout=5000")
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	if err := db.Ping(); err != nil {
		return nil, fmt.Errorf("ping: %w", err)
	}
	return db, nil
}
