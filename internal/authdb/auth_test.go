package authdb

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

const testPassword = "a-test-password-at-least-32-bytes-long"

func fixture(t *testing.T) (string, *Store) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "auth.sqlite")
	if err := Create(context.Background(), path, "coffee", testPassword); err != nil {
		t.Fatal(err)
	}
	s, err := Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return path, s
}
func TestVerifierIsSaltedAndNoPlaintextIsStored(t *testing.T) {
	ctx := context.Background()
	path, s := fixture(t)
	for _, tc := range []struct {
		u, p string
		want bool
	}{{"coffee", testPassword, true}, {"wrong", testPassword, false}, {"coffee", "wrong", false}} {
		ok, err := s.Verify(ctx, tc.u, tc.p)
		if err != nil || ok != tc.want {
			t.Fatalf("verification: %v %v", ok, err)
		}
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(data, []byte(testPassword)) {
		t.Fatal("plaintext stored")
	}
	_, other := fixture(t)
	var a, b []byte
	s.db.QueryRow("SELECT verifier FROM owner").Scan(&a)
	other.db.QueryRow("SELECT verifier FROM owner").Scan(&b)
	if bytes.Equal(a, b) {
		t.Fatal("salts not independent")
	}
	if err := Create(ctx, path, "coffee", testPassword); err == nil {
		t.Fatal("overwrote auth DB")
	}
	if err := os.Chmod(path, 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(ctx, path); err == nil {
		t.Fatal("publicly readable database accepted")
	}
}
func TestSessionsPersistExpireRejectTamperingAndRevoke(t *testing.T) {
	ctx := context.Background()
	path, s := fixture(t)
	now := time.Now()
	token, ok, err := s.Login(ctx, "coffee", testPassword, now)
	if err != nil || !ok {
		t.Fatal(err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for _, tc := range []struct {
		token string
		at    time.Time
		want  bool
	}{{token, now.Add(29 * 24 * time.Hour), true}, {token, now.Add(Duration), false}, {"invalid", now, false}, {token + "A", now, false}} {
		ok, err := s.Valid(ctx, tc.token, tc.at)
		if err != nil || ok != tc.want {
			t.Fatalf("session result %v %v", ok, err)
		}
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(raw, []byte(token)) {
		t.Fatal("raw session token persisted")
	}
	if err = s.Revoke(ctx, token); err != nil {
		t.Fatal(err)
	}
	if ok, _ := s.Valid(ctx, token, now); ok {
		t.Fatal("revoked token accepted")
	}
}
func TestResetRevokesAllSessionsAndRejectsInFlightOldPassword(t *testing.T) {
	ctx := context.Background()
	_, s := fixture(t)
	now := time.Now()
	token, _, err := s.Login(ctx, "coffee", testPassword, now)
	if err != nil {
		t.Fatal(err)
	}
	ok, oldVerifier, err := s.verify(ctx, "coffee", testPassword)
	if err != nil || !ok {
		t.Fatal(err)
	}
	if err = s.Reset(ctx, "coffee", "new-password-at-least-twelve-bytes"); err != nil {
		t.Fatal(err)
	}
	if ok, _ = s.Valid(ctx, token, now); ok {
		t.Fatal("reset did not revoke")
	}
	if ok, _ = s.Verify(ctx, "coffee", testPassword); ok {
		t.Fatal("old password accepted")
	}
	if ok, err = s.Verify(ctx, "coffee", "new-password-at-least-twelve-bytes"); !ok || err != nil {
		t.Fatal("new password rejected")
	}
	if token, err = s.issue(ctx, now, oldVerifier); err != nil || token != "" {
		t.Fatal("old in-flight login issued a token")
	}
}
func TestSessionCountBounded(t *testing.T) {
	ctx := context.Background()
	_, s := fixture(t)
	for i := 0; i < 70; i++ {
		if _, err := s.issue(ctx, time.Now(), nil); err != nil {
			t.Fatal(err)
		}
	}
	var count int
	if err := s.db.QueryRow("SELECT count(*) FROM sessions").Scan(&count); err != nil || count != 64 {
		t.Fatalf("session count %d: %v", count, err)
	}
}
