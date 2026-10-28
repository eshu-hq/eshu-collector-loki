#!/usr/bin/env bash
# Runs this repository's conformance gate: build, vet, full tests.
# Mirrors the test lane in .github/workflows/ci.yml (`make ci` runs the
# same commands plus the lint and race lanes).
set -euo pipefail
cd "$(dirname "$0")/.."
go build ./...
go vet ./...
go test -race ./... -count=1
