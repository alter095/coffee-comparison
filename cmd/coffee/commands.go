package main

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"github.com/alter095/coffee-comparison/internal/authdb"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/alter095/coffee-comparison/internal/backup"
)

func command(ctx context.Context, args []string) (bool, error) {
	if len(args) == 0 {
		return false, nil
	}
	switch args[0] {
	case "backup", "restore":
		flags := flag.NewFlagSet(args[0], flag.ContinueOnError)
		db := flags.String("db", "", "database path")
		out := flags.String("out", "", "new backup file (backup only)")
		from := flags.String("from", "", "snapshot file (restore only)")
		if err := flags.Parse(args[1:]); err != nil {
			return true, err
		}
		if flags.NArg() != 0 || *db == "" {
			return true, fmt.Errorf("%s requires -db and no positional arguments", args[0])
		}
		source, dest := *db, *out
		if args[0] == "restore" {
			source, dest = *from, *db
			if *out != "" {
				return true, fmt.Errorf("restore does not accept -out")
			}
		} else if *from != "" {
			return true, fmt.Errorf("backup does not accept -from")
		}
		limited, cancel := context.WithTimeout(ctx, 5*time.Minute)
		defer cancel()
		if err := backup.Snapshot(limited, source, dest); err != nil {
			return true, err
		}
		fmt.Fprintln(os.Stdout, args[0]+" completed")
		return true, nil
	case "credentials", "reset-password", "migrate-credentials":
		return true, credentialCommand(ctx, args, os.Stdin, os.Stdout)
	case "healthcheck":
		flags := flag.NewFlagSet("healthcheck", flag.ContinueOnError)
		address := flags.String("url", "http://127.0.0.1:3180/healthz", "local health URL")
		if err := flags.Parse(args[1:]); err != nil {
			return true, err
		}
		if flags.NArg() != 0 {
			return true, fmt.Errorf("unexpected healthcheck arguments")
		}
		req, err := http.NewRequestWithContext(ctx, "GET", *address, nil)
		if err != nil {
			return true, err
		}
		client := http.Client{Timeout: 3 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
		response, err := client.Do(req)
		if err != nil {
			return true, err
		}
		defer response.Body.Close()
		io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
		if response.StatusCode != http.StatusOK {
			return true, fmt.Errorf("health status %d", response.StatusCode)
		}
		return true, nil
	}
	return false, nil
}

// Passwords are accepted from stdin or generated once; never via process arguments.
func credentialCommand(ctx context.Context, args []string, input io.Reader, output io.Writer) error {
	flags := flag.NewFlagSet(args[0], flag.ContinueOnError)
	out := flags.String("out", "", "new authentication database (never overwritten)")
	dbPath := flags.String("db", "", "existing authentication database for reset")
	from := flags.String("from", "", "legacy private JSON file for migration")
	stdin := flags.Bool("password-stdin", false, "read one password line from stdin")
	user := flags.String("username", "coffee", "owner username")
	if err := flags.Parse(args[1:]); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("unexpected arguments")
	}
	if args[0] == "migrate-credentials" {
		if *out == "" || *from == "" || *dbPath != "" || *stdin {
			return fmt.Errorf("migrate-credentials requires -from and -out")
		}
		c, err := readCredentials(*from)
		if err != nil {
			return err
		}
		if err = authdb.Create(ctx, *out, c.Username, c.Password); err != nil {
			return err
		}
		fmt.Fprintln(output, "migration completed; securely remove the legacy plaintext file after verifying login")
		return nil
	}
	if (args[0] == "credentials" && (*out == "" || *dbPath != "")) || (args[0] == "reset-password" && (*dbPath == "" || *out != "")) || *from != "" {
		return fmt.Errorf("credentials requires -out; reset-password requires -db")
	}
	var password string
	if *stdin {
		line, err := bufio.NewReader(io.LimitReader(input, 514)).ReadString('\n')
		if err != nil && err != io.EOF {
			return err
		}
		password = strings.TrimSuffix(strings.TrimSuffix(line, "\n"), "\r")
	} else {
		var random [32]byte
		if _, err := rand.Read(random[:]); err != nil {
			return err
		}
		password = hex.EncodeToString(random[:])
	}
	if args[0] == "reset-password" {
		state, err := authdb.Open(ctx, *dbPath)
		if err != nil {
			return err
		}
		defer state.Close()
		if err = state.Reset(ctx, *user, password); err != nil {
			return err
		}
	} else if err := authdb.Create(ctx, *out, *user, password); err != nil {
		return err
	}
	if !*stdin {
		return json.NewEncoder(output).Encode(credentials{Username: *user, Password: password})
	}
	fmt.Fprintln(output, "password verifier saved; no plaintext password was stored")
	return nil
}
