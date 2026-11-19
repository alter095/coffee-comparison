// Package store persists comparison data in a single local SQLite database.
package store

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/alter095/coffee-comparison/internal/comparison"
	"github.com/mattn/go-sqlite3"
)

//go:embed migrations/*.sql
var migrations embed.FS

var (
	ErrNotFound = errors.New("record not found")
	ErrConflict = errors.New("record changed since it was read")
	ErrInvalid  = errors.New("invalid data or referenced record")
)

type Store struct{ db *sql.DB }

type Variant struct {
	ID       int64 `json:"id"`
	BeanID   int64 `json:"bean_id"`
	WeightG  int64 `json:"weight_g"`
	PriceYen int64 `json:"price_yen"`
	Version  int64 `json:"version"`
}

// Open opens a file database, enforces foreign keys on every connection and
// applies migrations atomically. The caller must create the parent directory.
func Open(ctx context.Context, path string) (*Store, error) {
	if path == "" {
		return nil, fmt.Errorf("database path is empty")
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	u := url.URL{Scheme: "file", Path: filepath.ToSlash(absolute)}
	q := u.Query()
	q.Set("_foreign_keys", "on")
	q.Set("_busy_timeout", "5000")
	q.Set("_journal_mode", "WAL")
	q.Set("_synchronous", "FULL")
	u.RawQuery = q.Encode()
	db, err := sql.Open("sqlite3", u.String())
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	s := &Store{db: db}
	if err := s.migrate(ctx); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) Close() error { return s.db.Close() }

// Health checks that the schema can be read; it is not a full integrity scan.
func (s *Store) Health(ctx context.Context) error {
	var version int
	return s.db.QueryRowContext(ctx, "SELECT MAX(version) FROM schema_migrations").Scan(&version)
}

func (s *Store) migrate(ctx context.Context) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (version INTEGER PRIMARY KEY) STRICT`); err != nil {
		return err
	}
	var version int
	if err = tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(version),0) FROM schema_migrations`).Scan(&version); err != nil {
		return err
	}
	scripts := []string{"001_initial.sql", "002_stable_ids_names.sql", "003_bean_content_version.sql", "004_candidate_buy_again.sql"}
	if version > len(scripts) || version < 0 {
		return fmt.Errorf("database schema %d is unsupported (latest %d)", version, len(scripts))
	}
	for next := version + 1; next <= len(scripts); next++ {
		if next == 2 {
			if err := validateLegacyNames(ctx, tx); err != nil {
				return fmt.Errorf("migration 2: %w", err)
			}
		}
		script, err := migrations.ReadFile("migrations/" + scripts[next-1])
		if err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, string(script)); err != nil {
			return fmt.Errorf("migration %d failed: %w", next, err)
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO schema_migrations(version) VALUES(?)`, next); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// SQLite's text functions do not validate UTF-8; use the same validator as
// normal writes before copying legacy data into the replacement tables.
func validateLegacyNames(ctx context.Context, tx *sql.Tx) error {
	rows, err := tx.QueryContext(ctx, `SELECT name FROM shops UNION ALL SELECT name FROM beans`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return err
		}
		if !validName(name) {
			return fmt.Errorf("%w: legacy shop or bean name requires correction", ErrInvalid)
		}
	}
	return rows.Err()
}

func classify(err error) error {
	var sqliteErr sqlite3.Error
	if errors.As(err, &sqliteErr) && sqliteErr.Code == sqlite3.ErrConstraint {
		return fmt.Errorf("%w: constraint violation", ErrInvalid)
	}
	return err
}

// validName counts the entire untrimmed input, preserving intentional spaces.
func validName(name string) bool {
	return utf8.ValidString(name) && utf8.RuneCountInString(name) <= 200 &&
		strings.TrimSpace(name) != "" && !strings.ContainsRune(name, 0)
}

func (s *Store) CreateShop(ctx context.Context, name string, shipping, threshold *int64) (int64, error) {
	if !validName(name) {
		return 0, ErrInvalid
	}
	result, err := s.db.ExecContext(ctx, `INSERT INTO shops(name,shipping_yen,free_shipping_threshold_yen) VALUES(?,?,?)`, name, shipping, threshold)
	if err != nil {
		return 0, classify(err)
	}
	return result.LastInsertId()
}

func (s *Store) CreateBean(ctx context.Context, shopID int64, name string, productURL *string) (int64, error) {
	if !validName(name) {
		return 0, ErrInvalid
	}
	result, err := s.db.ExecContext(ctx, `INSERT INTO beans(shop_id,name,product_url) VALUES(?,?,?)`, shopID, name, productURL)
	if err != nil {
		return 0, classify(err)
	}
	return result.LastInsertId()
}

func (s *Store) CreateVariant(ctx context.Context, beanID, weight, price int64) (Variant, error) {
	if _, err := comparison.Online(price, weight, nil, nil); err != nil {
		return Variant{}, ErrInvalid
	}
	result, err := s.db.ExecContext(ctx, `INSERT INTO variants(bean_id,weight_g,price_yen) VALUES(?,?,?)`, beanID, weight, price)
	if err != nil {
		var sqliteErr sqlite3.Error
		if errors.As(err, &sqliteErr) && sqliteErr.ExtendedCode == sqlite3.ErrConstraintForeignKey {
			return Variant{}, ErrNotFound
		}
		return Variant{}, classify(err)
	}
	id, err := result.LastInsertId()
	return Variant{ID: id, BeanID: beanID, WeightG: weight, PriceYen: price, Version: 1}, err
}

func (s *Store) Variant(ctx context.Context, id int64) (Variant, error) {
	var v Variant
	err := s.db.QueryRowContext(ctx, `SELECT id,bean_id,weight_g,price_yen,version FROM variants WHERE id=?`, id).Scan(&v.ID, &v.BeanID, &v.WeightG, &v.PriceYen, &v.Version)
	if errors.Is(err, sql.ErrNoRows) {
		return v, ErrNotFound
	}
	return v, err
}

// UpdatePrice returns the current value on conflict so the caller can offer
// explicit reconciliation instead of blindly overwriting another edit.
func (s *Store) UpdatePrice(ctx context.Context, id, version, price int64) (Variant, error) {
	if version <= 0 || price < 0 || price > comparison.MaxValue {
		return Variant{}, ErrInvalid
	}
	var v Variant
	err := s.db.QueryRowContext(ctx, `UPDATE variants SET price_yen=?, version=version+1, updated_at=CURRENT_TIMESTAMP WHERE id=? AND version=? RETURNING id,bean_id,weight_g,price_yen,version`, price, id, version).Scan(&v.ID, &v.BeanID, &v.WeightG, &v.PriceYen, &v.Version)
	if errors.Is(err, sql.ErrNoRows) {
		current, getErr := s.Variant(ctx, id)
		if getErr != nil {
			return current, getErr
		}
		return current, ErrConflict
	}
	return v, classify(err)
}
