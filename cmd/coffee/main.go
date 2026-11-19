// Command coffee serves the comparison app and provides backup/restore utilities.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/alter095/coffee-comparison/internal/api"
	"github.com/alter095/coffee-comparison/internal/store"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if handled, err := command(ctx, os.Args[1:]); handled {
		if err != nil {
			slog.Error("coffee command failed", "error", err)
			os.Exit(1)
		}
		return
	}
	mode := flag.String("mode", "development", "development or production")
	origin := flag.String("origin", "", "external HTTPS origin (production)")
	authFile := flag.String("auth-file", "", "private authentication SQLite database (production)")
	container := flag.Bool("container", false, "allow an unspecified listen IP in production behind a loopback-published container port")
	address := flag.String("listen", "127.0.0.1:3180", "listen IP and port")
	database := flag.String("db", "data/coffee.sqlite", "SQLite file path")
	web := flag.String("web", "frontend/dist", "built static UI directory")
	flag.Parse()
	if err := runConfigured(ctx, *address, *database, *web, *mode, *origin, *authFile, *container); err != nil {
		slog.Error("coffee stopped", "error", err)
		os.Exit(1)
	}
}

func validateAddress(address string) error {
	return validateListener(address, "development", false)
}

func validateListener(address, mode string, container bool) error {
	if container && mode != "production" {
		return fmt.Errorf("-container requires production mode")
	}
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return fmt.Errorf("invalid listen address: %w", err)
	}
	ip := net.ParseIP(host)
	if ip == nil || (!ip.IsLoopback() && !(container && ip.IsUnspecified())) {
		return fmt.Errorf("coffee requires a loopback IP address")
	}
	return nil
}

func run(ctx context.Context, address, database, web string) error {
	return runConfigured(ctx, address, database, web, "development", "", "", false)
}

func runConfigured(ctx context.Context, address, database, web, mode, origin, authFile string, container bool) error {
	if err := validateListener(address, mode, container); err != nil {
		return err
	}
	var app http.Handler
	var handler http.Handler
	switch mode {
	case "development":
		if origin != "" || authFile != "" {
			return fmt.Errorf("-origin and -auth-file require production mode")
		}
		handler = developmentHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { app.ServeHTTP(w, r) }))
	case "production":
		var err error
		handler, err = newProductionHandler(origin, authFile, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { app.ServeHTTP(w, r) }))
		if err != nil {
			return err
		}
		defer handler.(*productionHandler).Close()
		if info, err := os.Stat(filepath.Join(web, "index.html")); err != nil || !info.Mode().IsRegular() {
			return fmt.Errorf("production requires built index.html")
		}
		if info, err := os.Stat(filepath.Join(web, "assets")); err != nil || !info.IsDir() {
			return fmt.Errorf("production requires built assets")
		}
	default:
		return fmt.Errorf("unknown mode %q", mode)
	}
	if database == "" {
		return fmt.Errorf("database path is empty")
	}
	if err := os.MkdirAll(filepath.Dir(database), 0700); err != nil {
		return err
	}
	s, err := store.Open(ctx, database)
	if err != nil {
		return err
	}
	defer s.Close()
	app = api.NewWithAssets(s, os.DirFS(web))
	listener, err := net.Listen("tcp", address)
	if err != nil {
		return err
	}
	defer listener.Close()
	server := &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    16 << 10,
	}
	done := make(chan error, 1)
	go func() { done <- server.Serve(listener) }()
	slog.Info("coffee API listening", "address", listener.Addr().String())
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			server.Close()
			<-done
			return err
		}
		err := <-done
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}

// Reject untrusted Host values as well as non-loopback listeners, including
// browser requests to a remote hostname that resolves to a loopback address.
func developmentHandler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host := r.Host
		if parsed, _, err := net.SplitHostPort(host); err == nil {
			host = parsed
		}
		ip := net.ParseIP(host)
		if host != "localhost" && (ip == nil || !ip.IsLoopback()) {
			http.Error(w, "untrusted development host", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}
