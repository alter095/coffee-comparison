#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "${BASH_SOURCE[0]}")/.."
if ! command -v go >/dev/null 2>&1; then
  export PATH="$PWD/.tools/go/bin:$PATH"
  export GOPATH="${GOPATH:-$PWD/.tools/gopath}"
  export GOCACHE="${GOCACHE:-$PWD/.tools/gocache}"
fi
mkdir -p .tools
CGO_ENABLED=1 GOTOOLCHAIN=local go build -o .tools/coffee-api ./cmd/coffee
export PLAYWRIGHT_BROWSERS_PATH="${PLAYWRIGHT_BROWSERS_PATH:-$PWD/.tools/playwright}"
npm --prefix frontend run test:e2e
