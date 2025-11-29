package queue

import (
	"database/sql"
	"fmt"

	_ "github.com/mattn/go-sqlite3"
)

const schema = `
CREATE TABLE IF NOT EXISTS builds (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    pkg_name TEXT NOT NULL,
    pkg_id TEXT NOT NULL,
    recipe_path TEXT NOT NULL,
    status TEXT NOT NULL,
    priority INTEGER DEFAULT 0,
    arch TEXT NOT NULL,
    force_build BOOLEAN DEFAULT 0,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    started_at DATETIME,
    completed_at DATETIME,
    duration_seconds INTEGER,
    error_message TEXT,
    build_log_url TEXT
);

CREATE INDEX IF NOT EXISTS idx_status ON builds(status);
CREATE INDEX IF NOT EXISTS idx_pkg_name ON builds(pkg_name);
CREATE INDEX IF NOT EXISTS idx_created_at ON builds(created_at DESC);
CREATE INDEX IF NOT EXISTS idx_priority ON builds(priority DESC, created_at ASC);

CREATE TABLE IF NOT EXISTS metadata (
    key TEXT PRIMARY KEY,
    value TEXT,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS sync_state (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    repo_name TEXT NOT NULL,
    last_commit_hash TEXT,
    last_sync_time DATETIME DEFAULT CURRENT_TIMESTAMP,
    packages_synced INTEGER
);
`

// InitDB initializes the SQLite database
func InitDB(dbPath string) (*sql.DB, error) {
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

	// Enable WAL mode for better concurrency
	if _, err := db.Exec("PRAGMA journal_mode=WAL"); err != nil {
		return nil, fmt.Errorf("failed to enable WAL mode: %w", err)
	}

	// Create tables
	if _, err := db.Exec(schema); err != nil {
		return nil, fmt.Errorf("failed to create schema: %w", err)
	}

	return db, nil
}
