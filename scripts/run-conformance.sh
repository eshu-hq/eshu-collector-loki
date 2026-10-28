#!/usr/bin/env bash
# Runs this repository's conformance gate: build, vet, full tests.
# This script IS the test lane body in .github/workflows/ci.yml, and
# `make ci` runs it too: a breaking edit fails CI, not just shellcheck.
set -euo pipefail
cd "$(dirname "$0")/.."
go build ./...
go vet ./...
go test -race ./... -count=1
