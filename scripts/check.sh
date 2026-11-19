#!/usr/bin/env bash
set -euo pipefail

cd "$(dirname "${BASH_SOURCE[0]}")/.."

# Prefer an explicitly configured Go; fall back to the local development install.
if ! command -v go >/dev/null 2>&1; then
  export PATH="$PWD/.tools/go/bin:$PATH"
  export GOPATH="${GOPATH:-$PWD/.tools/gopath}"
  export GOCACHE="${GOCACHE:-$PWD/.tools/gocache}"
fi
if ! command -v go >/dev/null 2>&1; then
  echo 'Go is required; see README.md.' >&2
  exit 1
fi
export CGO_ENABLED=1
export GOTOOLCHAIN=local

# Include new, untracked source files; exclude ignored dependencies and output.
mapfile -d '' -t go_files < <(git ls-files -z --cached --others --exclude-standard -- '*.go')
if ((${#go_files[@]})); then
  unformatted="$(gofmt -l "${go_files[@]}")"
  if [[ -n "$unformatted" ]]; then
    printf 'Run gofmt on these files:\n%s\n' "$unformatted" >&2
    exit 1
  fi
fi

go mod verify
go vet ./...
go test -race -count=1 ./...
go build ./...
npm --prefix frontend run format:check
npm --prefix frontend run check
npm --prefix frontend test
npm --prefix frontend run build
git diff --check
