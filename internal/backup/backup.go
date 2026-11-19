// Package backup creates private, consistent SQLite snapshots without migrating
// or creating the source. Both backup and restore publish a new file only.
package backup

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"path/filepath"

	_ "github.com/mattn/go-sqlite3"
)

// Snapshot includes committed WAL data using VACUUM INTO. It validates the
// result before atomically publishing it, and never overwrites a destination.
// The source may be live. A restore must target a new, unused database path.
func Snapshot(ctx context.Context, source, destination string) error {
	if source == "" || destination == "" {
		return fmt.Errorf("source and destination are required")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	src, err := filepath.Abs(source)
	if err != nil {
		return err
	}
	dst, err := filepath.Abs(destination)
	if err != nil {
		return err
	}
	// Existing sidecars may belong to a running/restorable DB even if its main
	// file is missing. Refuse instead of risking reuse with unrelated WAL data.
	for _, p := range []string{dst, dst + "-wal", dst + "-shm", dst + "-journal"} {
		if _, err := os.Lstat(p); err == nil {
			return fmt.Errorf("destination exists: %w", os.ErrExist)
		} else if !os.IsNotExist(err) {
			return err
		}
	}
	db, err := openReadOnly(src)
	if err != nil {
		return err
	}
	defer db.Close()
	if err := db.PingContext(ctx); err != nil {
		return err
	}
	temp, err := os.CreateTemp(filepath.Dir(dst), ".coffee-snapshot-*")
	if err != nil {
		return err
	}
	name := temp.Name()
	defer os.Remove(name)
	if err := temp.Close(); err != nil {
		return err
	}
	if _, err := db.ExecContext(ctx, "VACUUM INTO ?", name); err != nil {
		return fmt.Errorf("snapshot: %w", err)
	}
	check, err := openReadOnly(name)
	if err != nil {
		return err
	}
	err = validate(ctx, check)
	closeErr := check.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	f, err := os.OpenFile(name, os.O_RDWR, 0)
	if err != nil {
		return err
	}
	err = f.Sync()
	closeErr = f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	// Link is atomic and fails if the destination appeared during the snapshot.
	if err := os.Link(name, dst); err != nil {
		return err
	}
	dir, err := os.Open(filepath.Dir(dst))
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}

func openReadOnly(path string) (*sql.DB, error) {
	u := url.URL{Scheme: "file", Path: filepath.ToSlash(path)}
	q := u.Query()
	q.Set("mode", "ro")
	q.Set("_busy_timeout", "5000")
	q.Set("_synchronous", "FULL")
	u.RawQuery = q.Encode()
	db, err := sql.Open("sqlite3", u.String())
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	return db, nil
}

func validate(ctx context.Context, db *sql.DB) error {
	rows, err := db.QueryContext(ctx, "PRAGMA quick_check")
	if err != nil {
		return err
	}
	count := 0
	for rows.Next() {
		var result string
		if err := rows.Scan(&result); err != nil {
			rows.Close()
			return err
		}
		if result != "ok" {
			rows.Close()
			return fmt.Errorf("snapshot integrity check failed")
		}
		count++
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	if count != 1 {
		return fmt.Errorf("snapshot integrity check incomplete")
	}
	rows, err = db.QueryContext(ctx, "PRAGMA foreign_key_check")
	if err != nil {
		return err
	}
	bad := rows.Next()
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	if bad {
		return fmt.Errorf("snapshot foreign key check failed")
	}
	var version int
	if err := db.QueryRowContext(ctx, "SELECT COALESCE(MAX(version),0) FROM schema_migrations").Scan(&version); err != nil {
		return fmt.Errorf("not a coffee database: %w", err)
	}
	if version < 1 || version > 4 {
		return fmt.Errorf("unsupported snapshot schema %d", version)
	}
	return nil
}
