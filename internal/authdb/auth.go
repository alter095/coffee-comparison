// Package authdb stores a single owner's password verifier and revocable sessions.
// It is deliberately separate from the coffee database and its export/restore.
package authdb

import (
	"context"
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"fmt"
	_ "github.com/mattn/go-sqlite3"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"
)

const Duration = 30 * 24 * time.Hour
const iterations = 600000

type Store struct {
	db      *sql.DB
	hashing chan struct{}
}

func validCredentials(user, password string) bool {
	if len(user) < 1 || len(user) > 64 || strings.Contains(user, ":") || !utf8.ValidString(password) || utf8.RuneCountInString(password) < 12 || len(password) > 512 {
		return false
	}
	for _, b := range []byte(user) {
		if b < 33 || b > 126 {
			return false
		}
	}
	return !strings.ContainsRune(password, 0)
}
func connect(path string) (*sql.DB, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	u := url.URL{Scheme: "file", Path: filepath.ToSlash(absolute)}
	q := u.Query()
	q.Set("mode", "rw")
	q.Set("_busy_timeout", "5000")
	q.Set("_journal_mode", "DELETE")
	q.Set("_synchronous", "FULL")
	u.RawQuery = q.Encode()
	db, err := sql.Open("sqlite3", u.String())
	if err == nil {
		db.SetMaxOpenConns(1)
		db.SetMaxIdleConns(1)
	}
	return db, err
}

// Create atomically publishes a new 0600 database; it never overwrites a file.
func Create(ctx context.Context, path, user, password string) error {
	if path == "" || !validCredentials(user, password) {
		return fmt.Errorf("username or password invalid (password requires at least 12 characters and at most 512 bytes)")
	}
	salt := make([]byte, 32)
	if _, err := rand.Read(salt); err != nil {
		return err
	}
	verifier, err := pbkdf2.Key(sha256.New, password, salt, iterations, 32)
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".coffee-auth-*")
	if err != nil {
		return err
	}
	temp := f.Name()
	defer os.Remove(temp)
	if err = f.Close(); err != nil {
		return err
	}
	db, err := connect(temp)
	if err != nil {
		return err
	}
	defer db.Close()
	if _, err = db.ExecContext(ctx, `CREATE TABLE owner (id INTEGER PRIMARY KEY CHECK(id=1), username TEXT NOT NULL, salt BLOB NOT NULL CHECK(length(salt)=32), verifier BLOB NOT NULL CHECK(length(verifier)=32), iterations INTEGER NOT NULL CHECK(iterations=600000)); CREATE TABLE sessions (token_hash BLOB PRIMARY KEY CHECK(length(token_hash)=32), expires INTEGER NOT NULL) STRICT; PRAGMA user_version=1;`); err != nil {
		return err
	}
	if _, err = db.ExecContext(ctx, `INSERT INTO owner VALUES(1,?,?,?,?)`, user, salt, verifier, iterations); err != nil {
		return err
	}
	if err = db.Close(); err != nil {
		return err
	}
	f, err = os.OpenFile(temp, os.O_RDWR, 0)
	if err != nil {
		return err
	}
	err = f.Sync()
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	if err = os.Link(temp, path); err != nil {
		return err
	}
	d, err := os.Open(filepath.Dir(path))
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}
func Open(ctx context.Context, path string) (*Store, error) {
	if path == "" {
		return nil, fmt.Errorf("production requires an authentication database")
	}
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 {
		return nil, fmt.Errorf("authentication database must be a private regular file (0600)")
	}
	db, err := connect(path)
	if err != nil {
		return nil, err
	}
	fail := func(err error) (*Store, error) {
		db.Close()
		return nil, fmt.Errorf("invalid authentication database; migrate legacy JSON with migrate-credentials: %w", err)
	}
	var version, n int
	if err = db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil {
		return fail(err)
	}
	if version != 1 {
		return fail(fmt.Errorf("unsupported schema"))
	}
	if err = db.QueryRowContext(ctx, "SELECT count(*) FROM owner WHERE id=1 AND length(salt)=32 AND length(verifier)=32 AND iterations=?", iterations).Scan(&n); err != nil || n != 1 {
		return fail(fmt.Errorf("invalid owner record"))
	}
	if _, err = db.ExecContext(ctx, "DELETE FROM sessions WHERE expires<=?", time.Now().Unix()); err != nil {
		return fail(err)
	}
	return &Store{db: db, hashing: make(chan struct{}, 2)}, nil
}
func (s *Store) Close() error { return s.db.Close() }

// Verify bounds hashing concurrency and password length before expensive work.
func (s *Store) verify(ctx context.Context, user, password string) (bool, []byte, error) {
	if len(password) > 512 {
		return false, nil, nil
	}
	select {
	case s.hashing <- struct{}{}:
		defer func() { <-s.hashing }()
	default:
		return false, nil, fmt.Errorf("authentication busy")
	}
	var expected string
	var salt, verifier []byte
	var rounds int
	if err := s.db.QueryRowContext(ctx, "SELECT username,salt,verifier,iterations FROM owner WHERE id=1").Scan(&expected, &salt, &verifier, &rounds); err != nil {
		return false, nil, err
	}
	if len(salt) != 32 || len(verifier) != 32 || rounds != iterations {
		return false, nil, fmt.Errorf("invalid verifier")
	}
	derived, err := pbkdf2.Key(sha256.New, password, salt, rounds, 32)
	if err != nil {
		return false, nil, err
	}
	a, b := sha256.Sum256([]byte(user)), sha256.Sum256([]byte(expected))
	return subtle.ConstantTimeCompare(a[:], b[:])&subtle.ConstantTimeCompare(derived, verifier) == 1, verifier, nil
}
func (s *Store) Verify(ctx context.Context, user, password string) (bool, error) {
	ok, _, err := s.verify(ctx, user, password)
	return ok, err
}
func (s *Store) Login(ctx context.Context, user, password string, now time.Time) (string, bool, error) {
	ok, expected, err := s.verify(ctx, user, password)
	if err != nil || !ok {
		return "", false, err
	}
	token, err := s.issue(ctx, now, expected)
	return token, token != "", err
}
func tokenHash(token string) ([]byte, bool) {
	raw, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil || len(raw) != 32 {
		return nil, false
	}
	sum := sha256.Sum256(raw)
	return sum[:], true
}
func (s *Store) issue(ctx context.Context, now time.Time, expected []byte) (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	token := base64.RawURLEncoding.EncodeToString(raw)
	hash, _ := tokenHash(token)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, "DELETE FROM sessions WHERE expires<=?", now.Unix()); err != nil {
		return "", err
	}
	if expected != nil {
		var current []byte
		if err = tx.QueryRowContext(ctx, "SELECT verifier FROM owner WHERE id=1").Scan(&current); err != nil {
			return "", err
		}
		if subtle.ConstantTimeCompare(current, expected) != 1 {
			return "", nil
		}
	}
	// Bound disk use even if the owner repeatedly logs in without logging out.
	if _, err = tx.ExecContext(ctx, "DELETE FROM sessions WHERE token_hash IN (SELECT token_hash FROM sessions ORDER BY expires, token_hash LIMIT max(0,(SELECT count(*) FROM sessions)-63))"); err != nil {
		return "", err
	}
	if _, err = tx.ExecContext(ctx, "INSERT INTO sessions VALUES(?,?)", hash, now.Add(Duration).Unix()); err != nil {
		return "", err
	}
	return token, tx.Commit()
}
func (s *Store) Valid(ctx context.Context, token string, now time.Time) (bool, error) {
	hash, ok := tokenHash(token)
	if !ok {
		return false, nil
	}
	var n int
	err := s.db.QueryRowContext(ctx, "SELECT count(*) FROM sessions WHERE token_hash=? AND expires>?", hash, now.Unix()).Scan(&n)
	return n == 1, err
}
func (s *Store) Revoke(ctx context.Context, token string) error {
	hash, ok := tokenHash(token)
	if !ok {
		return nil
	}
	_, err := s.db.ExecContext(ctx, "DELETE FROM sessions WHERE token_hash=?", hash)
	return err
}

// Reset changes the verifier and revokes every existing session atomically.
func (s *Store) Reset(ctx context.Context, user, password string) error {
	if !validCredentials(user, password) {
		return fmt.Errorf("invalid username or password")
	}
	salt := make([]byte, 32)
	if _, err := rand.Read(salt); err != nil {
		return err
	}
	verifier, err := pbkdf2.Key(sha256.New, password, salt, iterations, 32)
	if err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, "UPDATE owner SET username=?,salt=?,verifier=?,iterations=? WHERE id=1", user, salt, verifier, iterations); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, "DELETE FROM sessions"); err != nil {
		return err
	}
	return tx.Commit()
}
