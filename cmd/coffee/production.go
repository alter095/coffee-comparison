package main

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/alter095/coffee-comparison/internal/authdb"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

const (
	sessionCookieName = "__Host-coffee_session"
	sessionDuration   = authdb.Duration
)

type credentials struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

func readCredentials(path string) (credentials, error) {
	var c credentials
	if path == "" {
		return c, fmt.Errorf("production requires -auth-file")
	}
	f, err := os.Open(path)
	if err != nil {
		return c, fmt.Errorf("read credentials: %w", err)
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return c, err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 || info.Size() > 4096 {
		return c, fmt.Errorf("invalid credentials file")
	}
	d := json.NewDecoder(io.LimitReader(f, 4097))
	d.DisallowUnknownFields()
	if err := d.Decode(&c); err != nil {
		return credentials{}, fmt.Errorf("credentials must be a JSON username/password object")
	}
	var extra any
	if d.Decode(&extra) != io.EOF {
		return credentials{}, fmt.Errorf("credentials must contain one object")
	}
	valid := func(s string) bool {
		for _, b := range []byte(s) {
			if b < 33 || b > 126 {
				return false
			}
		}
		return true
	}
	if len(c.Username) < 1 || len(c.Username) > 64 || strings.Contains(c.Username, ":") || !valid(c.Username) || len(c.Password) < 32 || len(c.Password) > 512 || !valid(c.Password) {
		return credentials{}, fmt.Errorf("credentials require a printable username and a password of 32–512 non-space ASCII characters")
	}
	return c, nil
}

type productionHandler struct {
	http.Handler
	auth *authdb.Store
}

func (h *productionHandler) Close() error { return h.auth.Close() }

// TLS is terminated by a private reverse proxy. Host and write Origin must
// match the configured external HTTPS origin; forwarded headers are not trusted.
func newProductionHandler(origin, authFile string, next http.Handler) (http.Handler, error) {
	u, err := url.Parse(origin)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.Opaque != "" || strings.HasSuffix(origin, "#") {
		return nil, fmt.Errorf("production requires -origin as an HTTPS origin without a path, query or credentials")
	}
	state, err := authdb.Open(context.Background(), authFile)
	if err != nil {
		return nil, err
	}
	matches := func(r *http.Request) (bool, error) {
		user, pass, ok := r.BasicAuth()
		if !ok {
			return false, nil
		}
		return state.Verify(r.Context(), user, pass)
	}
	return &productionHandler{auth: state, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data:; connect-src 'self'; object-src 'none'; base-uri 'none'; frame-ancestors 'none'; form-action 'self'")
		w.Header().Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
		// Health checks are credential-free only over a local socket connection and
		// a loopback Host. The external /healthz still requires authentication.
		remote, _, _ := net.SplitHostPort(r.RemoteAddr)
		host := r.Host
		if parsed, _, err := net.SplitHostPort(host); err == nil {
			host = parsed
		}
		if (r.Method == "GET" || r.Method == "HEAD") && r.URL.Path == "/healthz" && net.ParseIP(remote).IsLoopback() && (host == "localhost" || net.ParseIP(host).IsLoopback()) {
			next.ServeHTTP(w, r)
			return
		}
		if !strings.EqualFold(r.Host, u.Host) {
			http.Error(w, "untrusted host", 403)
			return
		}
		if r.URL.Path == "/auth/login" && r.Method == http.MethodPost {
			if r.Header.Get("Origin") != origin || r.Header.Get("Sec-Fetch-Site") == "cross-site" {
				http.Error(w, "cross-origin request rejected", 403)
				return
			}
			if r.ContentLength != 0 {
				http.Error(w, "login request must not include a body", http.StatusBadRequest)
				return
			}
			user, pass, ok := r.BasicAuth()
			if !ok {
				http.Error(w, "invalid username or password", 401)
				return
			}
			token, authenticated, err := state.Login(r.Context(), user, pass, time.Now())
			if err != nil {
				http.Error(w, "authentication temporarily unavailable", 503)
				return
			}
			if !authenticated {
				http.Error(w, "invalid username or password", 401)
				return
			}
			http.SetCookie(w, &http.Cookie{
				Name:     sessionCookieName,
				Value:    token,
				Path:     "/",
				Expires:  time.Now().Add(sessionDuration),
				MaxAge:   int(sessionDuration.Seconds()),
				Secure:   true,
				HttpOnly: true,
				SameSite: http.SameSiteStrictMode,
			})
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if r.Method != "GET" && r.Method != "HEAD" && r.Method != "OPTIONS" {
			if (r.Header.Get("Origin") != "" && r.Header.Get("Origin") != origin) || r.Header.Get("Sec-Fetch-Site") == "cross-site" {
				http.Error(w, "cross-origin request rejected", 403)
				return
			}
		}
		if r.URL.Path == "/auth/logout" && r.Method == http.MethodPost {
			if r.Header.Get("Origin") != origin {
				http.Error(w, "cross-origin request rejected", 403)
				return
			}
			if cookie, err := r.Cookie(sessionCookieName); err == nil {
				if err = state.Revoke(r.Context(), cookie.Value); err != nil {
					http.Error(w, "logout unavailable", 503)
					return
				}
			}
			http.SetCookie(w, &http.Cookie{Name: sessionCookieName, Value: "", Path: "/", MaxAge: -1, Expires: time.Unix(1, 0), Secure: true, HttpOnly: true, SameSite: http.SameSiteStrictMode})
			w.WriteHeader(204)
			return
		}
		if isPublicUIRequest(r) {
			next.ServeHTTP(w, r)
			return
		}
		var authenticated bool
		if cookie, err := r.Cookie(sessionCookieName); err == nil {
			var lookupErr error
			authenticated, lookupErr = state.Valid(r.Context(), cookie.Value, time.Now())
			if lookupErr != nil {
				http.Error(w, "authentication unavailable", 503)
				return
			}
		}
		if !authenticated {
			var err error
			authenticated, err = matches(r)
			if err != nil {
				http.Error(w, "authentication unavailable", 503)
				return
			}
		}
		if !authenticated {
			// Browser API calls use JSON negotiation and are handled by the app's
			// login form. Keep the Basic challenge for command-line clients.
			if (!strings.HasPrefix(r.URL.Path, "/api/") && r.URL.Path != "/auth/session") || !strings.Contains(r.Header.Get("Accept"), "application/json") {
				w.Header().Set("WWW-Authenticate", `Basic realm="coffee", charset="UTF-8"`)
			}
			http.Error(w, "authentication required", 401)
			return
		}
		if r.URL.Path == "/auth/session" && r.Method == http.MethodGet {
			w.Header().Set("Content-Type", "application/json")
			io.WriteString(w, `{"authenticated":true}`)
			return
		}
		next.ServeHTTP(w, r)
	})}, nil
}

func isPublicUIRequest(r *http.Request) bool {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		return false
	}
	return (r.URL.Path == "/" && strings.Contains(r.Header.Get("Accept"), "text/html")) || r.URL.Path == "/favicon.ico" || strings.HasPrefix(r.URL.Path, "/assets/")
}
