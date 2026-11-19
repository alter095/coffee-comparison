package main

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/alter095/coffee-comparison/internal/authdb"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func testCredentials(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "auth.json")
	if err := authdb.Create(context.Background(), path, "coffee", "a-test-password-at-least-32-bytes-long"); err != nil {
		t.Fatal(err)
	}
	return path
}

func testProductionHandler(t *testing.T, origin, path string, next http.Handler) (http.Handler, error) {
	t.Helper()
	h, err := newProductionHandler(origin, path, next)
	if err == nil {
		t.Cleanup(func() { h.(*productionHandler).Close() })
	}
	return h, err
}

func TestProductionRequiresExplicitHTTPSOriginAndCredentials(t *testing.T) {
	for _, origin := range []string{"", "http://coffee.example", "https://", "https://user@coffee.example", "https://coffee.example/path", "https://coffee.example?secret=x", "https://coffee.example/#x"} {
		if _, err := testProductionHandler(t, origin, testCredentials(t), http.NotFoundHandler()); err == nil {
			t.Errorf("accepted %q", origin)
		}
	}
	if _, err := testProductionHandler(t, "https://coffee.example", "", http.NotFoundHandler()); err == nil {
		t.Fatal("missing credentials accepted")
	}
	if _, err := testProductionHandler(t, "https://coffee.example", testCredentials(t), http.NotFoundHandler()); err != nil {
		t.Fatal(err)
	}
}

func TestProductionProtectsPagesAPIsHostsAndCrossOriginWrites(t *testing.T) {
	h, err := testProductionHandler(t, "https://coffee.example", testCredentials(t), http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) }))
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, host, path, user, pass, origin, site, remote, method string
		status                                                     int
	}{
		{name: "page auth", host: "coffee.example", path: "/", status: 401},
		{name: "api auth", host: "coffee.example", path: "/api/beans", status: 401},
		{name: "wrong auth", host: "coffee.example", path: "/", user: "coffee", pass: "wrong", status: 401},
		{name: "valid", host: "coffee.example", path: "/api/beans", user: "coffee", pass: "a-test-password-at-least-32-bytes-long", status: 204},
		{name: "wrong host", host: "evil.example", path: "/", user: "coffee", pass: "a-test-password-at-least-32-bytes-long", status: 403},
		{name: "cross origin", host: "coffee.example", path: "/api/beans", method: "POST", user: "coffee", pass: "a-test-password-at-least-32-bytes-long", origin: "https://evil.example", status: 403},
		{name: "cross site", host: "coffee.example", path: "/api/beans", method: "POST", user: "coffee", pass: "a-test-password-at-least-32-bytes-long", site: "cross-site", status: 403},
		{name: "same origin", host: "coffee.example", path: "/api/beans", method: "POST", user: "coffee", pass: "a-test-password-at-least-32-bytes-long", origin: "https://coffee.example", status: 204},
		{name: "health public", host: "coffee.example", path: "/healthz", status: 401},
		{name: "health local", host: "127.0.0.1:3180", path: "/healthz", remote: "127.0.0.1:55000", status: 204},
		{name: "health nonlocal", host: "127.0.0.1:3180", path: "/healthz", remote: "192.0.2.1:55000", status: 403},
		{name: "local api denied", host: "127.0.0.1:3180", path: "/api/beans", remote: "127.0.0.1:55000", status: 403},
	} {
		t.Run(tc.name, func(t *testing.T) {
			method := tc.method
			if method == "" {
				method = "GET"
			}
			r := httptest.NewRequest(method, "https://coffee.example"+tc.path, nil)
			r.Host = tc.host
			r.RemoteAddr = "192.0.2.1:55000"
			if tc.remote != "" {
				r.RemoteAddr = tc.remote
			}
			if tc.user != "" {
				r.SetBasicAuth(tc.user, tc.pass)
			}
			if tc.origin != "" {
				r.Header.Set("Origin", tc.origin)
			}
			if tc.site != "" {
				r.Header.Set("Sec-Fetch-Site", tc.site)
			}
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != tc.status {
				t.Fatalf("got %d want %d", w.Code, tc.status)
			}
			if w.Header().Get("X-Content-Type-Options") != "nosniff" {
				t.Fatal("missing security header")
			}
			if tc.status == 401 && !strings.HasPrefix(w.Header().Get("WWW-Authenticate"), "Basic ") {
				t.Fatal("missing challenge")
			}
		})
	}
}

func TestProductionLoginIssuesCookieAndLeavesOnlyTheUIPublic(t *testing.T) {
	h, err := testProductionHandler(t, "https://coffee.example", testCredentials(t), http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	if err != nil {
		t.Fatal(err)
	}
	request := func(method, path, accept, username, password, origin string, cookie *http.Cookie) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, "https://coffee.example"+path, nil)
		r.Host = "coffee.example"
		r.RemoteAddr = "192.0.2.1:55000"
		if accept != "" {
			r.Header.Set("Accept", accept)
		}
		if username != "" {
			r.SetBasicAuth(username, password)
		}
		if origin != "" {
			r.Header.Set("Origin", origin)
		}
		if cookie != nil {
			r.AddCookie(cookie)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	if got := request(http.MethodGet, "/", "text/html", "", "", "", nil); got.Code != http.StatusNoContent {
		t.Fatalf("public login page returned %d", got.Code)
	}
	if got := request(http.MethodGet, "/assets/app.js", "*/*", "", "", "", nil); got.Code != http.StatusNoContent {
		t.Fatalf("public UI asset returned %d", got.Code)
	}
	if got := request(http.MethodGet, "/favicon.ico", "image/avif,image/webp,*/*", "", "", "", nil); got.Code != http.StatusNoContent {
		t.Fatalf("public browser favicon request returned %d", got.Code)
	}
	if got := request(http.MethodGet, "/api/beans", "application/json", "", "", "", nil); got.Code != http.StatusUnauthorized || got.Header().Get("WWW-Authenticate") != "" {
		t.Fatalf("browser API auth response: %d, challenge %q", got.Code, got.Header().Get("WWW-Authenticate"))
	}
	if got := request(http.MethodPost, "/auth/login", "application/json", "coffee", "wrong", "https://coffee.example", nil); got.Code != http.StatusUnauthorized || got.Header().Get("WWW-Authenticate") != "" {
		t.Fatalf("invalid login response: %d, challenge %q", got.Code, got.Header().Get("WWW-Authenticate"))
	}
	if got := request(http.MethodPost, "/auth/login", "application/json", "coffee", "a-test-password-at-least-32-bytes-long", "https://evil.example", nil); got.Code != http.StatusForbidden {
		t.Fatalf("cross-origin login returned %d", got.Code)
	}
	got := request(http.MethodPost, "/auth/login", "application/json", "coffee", "a-test-password-at-least-32-bytes-long", "https://coffee.example", nil)
	if got.Code != http.StatusNoContent {
		t.Fatalf("valid login returned %d", got.Code)
	}
	cookies := got.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("got %d cookies", len(cookies))
	}
	cookie := cookies[0]
	if cookie.Name != sessionCookieName || cookie.Value == "" || !cookie.Secure || !cookie.HttpOnly || cookie.SameSite != http.SameSiteStrictMode || cookie.Path != "/" || cookie.MaxAge != int(sessionDuration.Seconds()) {
		t.Fatalf("unsafe or incomplete session cookie: %#v", cookie)
	}
	if got := request(http.MethodGet, "/api/beans", "application/json", "", "", "", cookie); got.Code != http.StatusNoContent {
		t.Fatalf("session API request returned %d", got.Code)
	}
	if got := request(http.MethodGet, "/api/beans", "application/json", "coffee", "a-test-password-at-least-32-bytes-long", "", nil); got.Code != http.StatusNoContent {
		t.Fatalf("Basic-auth API client returned %d", got.Code)
	}
}

func TestPersistentProductionSessionAndLogout(t *testing.T) {
	path := testCredentials(t)
	makeHandler := func() http.Handler {
		h, err := testProductionHandler(t, "https://coffee.example", path, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) }))
		if err != nil {
			t.Fatal(err)
		}
		return h
	}
	h := makeHandler()
	req := httptest.NewRequest("POST", "https://coffee.example/auth/login", nil)
	req.Header.Set("Origin", "https://coffee.example")
	req.SetBasicAuth("coffee", "a-test-password-at-least-32-bytes-long")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != 204 {
		t.Fatal(w.Code)
	}
	cookie := w.Result().Cookies()[0]
	h.(*productionHandler).Close()
	h = makeHandler()
	request := func(method, path, origin string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, "https://coffee.example"+path, nil)
		r.AddCookie(cookie)
		r.Header.Set("Origin", origin)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	if w := request("GET", "/api/beans", ""); w.Code != 204 {
		t.Fatal("restart lost session")
	}
	if w := request("POST", "/auth/logout", "https://evil.example"); w.Code != 403 {
		t.Fatal("cross-origin logout accepted")
	}
	if w := request("POST", "/auth/logout", "https://coffee.example"); w.Code != 204 || w.Result().Cookies()[0].MaxAge != -1 {
		t.Fatal("logout failed")
	}
	if w := request("GET", "/api/beans", ""); w.Code != 401 {
		t.Fatal("logged-out session accepted")
	}
}

func TestCredentialValidationAndPrivateGeneration(t *testing.T) {
	for _, content := range []string{`{}`, `{"username":"coffee","password":"short"}`, `{"username":"a:b","password":"a-test-password-at-least-32-bytes-long"}`, `{"username":"coffee","password":"a-test-password-at-least-32-bytes-long","extra":1}`, `{"username":"coffee","password":"a-test-password-at-least-32-bytes-long"} {}`} {
		path := filepath.Join(t.TempDir(), "invalid.json")
		if err := os.WriteFile(path, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := readCredentials(path); err == nil {
			t.Fatal("invalid credentials accepted")
		}
	}
	path := filepath.Join(t.TempDir(), "generated.json")
	var output bytes.Buffer
	if err := credentialCommand(context.Background(), []string{"credentials", "-out", path}, strings.NewReader(""), &output); err != nil {
		t.Fatal(err)
	}
	var generated credentials
	if err := json.Unmarshal(output.Bytes(), &generated); err != nil || len(generated.Password) != 64 {
		t.Fatal("password not provided once")
	}
	state, err := authdb.Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	if ok, err := state.Verify(context.Background(), generated.Username, generated.Password); err != nil || !ok {
		t.Fatal("generated password rejected")
	}
	raw, err := os.ReadFile(path)
	if err != nil || bytes.Contains(raw, []byte(generated.Password)) {
		t.Fatal("plaintext password persisted")
	}
	if err := credentialCommand(context.Background(), []string{"credentials", "-out", path}, strings.NewReader(""), &output); err == nil {
		t.Fatal("credentials overwritten")
	}
	if err := os.Chmod(path, 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := authdb.Open(context.Background(), path); err == nil {
		t.Fatal("public credentials accepted")
	}

}

func TestInvalidProductionConfigCannotCreateDatabase(t *testing.T) {
	dir := t.TempDir()
	db := filepath.Join(dir, "data", "coffee.sqlite")
	err := runConfigured(context.Background(), "127.0.0.1:0", db, dir, "production", "http://invalid.example", testCredentials(t), false)
	if err == nil {
		t.Fatal("configuration accepted")
	}
	if _, err := os.Stat(db); !os.IsNotExist(err) {
		t.Fatalf("database touched on invalid config: %v", err)
	}
}

func TestLegacyMigrationAndPasswordReset(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	legacy := filepath.Join(dir, "legacy.json")
	path := filepath.Join(dir, "auth.sqlite")
	const content = `{"username":"coffee","password":"a-test-password-at-least-32-bytes-long"}`
	if err := os.WriteFile(legacy, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := credentialCommand(ctx, []string{"migrate-credentials", "-from", legacy, "-out", path}, strings.NewReader(""), &out); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "a-test-password") {
		t.Fatal("migration leaked password")
	}
	before, err := os.ReadFile(legacy)
	if err != nil || string(before) != content {
		t.Fatal("legacy file modified")
	}
	s, err := authdb.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	token, ok, err := s.Login(ctx, "coffee", "a-test-password-at-least-32-bytes-long", time.Now())
	if err != nil || !ok {
		t.Fatal("migration changed password")
	}
	out.Reset()
	password := "これは十分に長いテスト用パスワードです"
	if err := credentialCommand(ctx, []string{"reset-password", "-db", path, "-password-stdin"}, strings.NewReader(password+"\n"), &out); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), password) {
		t.Fatal("reset leaked password")
	}
	if valid, err := s.Valid(ctx, token, time.Now()); valid || err != nil {
		t.Fatal("reset retained old session")
	}
	if ok, err := s.Verify(ctx, "coffee", password); !ok || err != nil {
		t.Fatal("new password not usable")
	}
	if err := credentialCommand(ctx, []string{"migrate-credentials", "-from", legacy, "-out", path}, strings.NewReader(""), &out); err == nil {
		t.Fatal("migration overwrote existing DB")
	}
}

func TestAuthenticationDatabaseFailureDeniesAccess(t *testing.T) {
	reached := false
	h, err := testProductionHandler(t, "https://coffee.example", testCredentials(t), http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { reached = true; w.WriteHeader(204) }))
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest("POST", "https://coffee.example/auth/login", nil)
	r.Header.Set("Origin", "https://coffee.example")
	r.SetBasicAuth("coffee", "a-test-password-at-least-32-bytes-long")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 204 {
		t.Fatal("login failed")
	}
	cookie := w.Result().Cookies()[0]
	h.(*productionHandler).Close()
	r = httptest.NewRequest("GET", "https://coffee.example/api/beans", nil)
	r.AddCookie(cookie)
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 503 || reached {
		t.Fatal("database failure did not deny access")
	}
}
