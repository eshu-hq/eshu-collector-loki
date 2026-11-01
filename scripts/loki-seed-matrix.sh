#!/usr/bin/env bash
# Seeded RED/GREEN matrix for the locally provable CI lanes.
#
# Local runner, not a CI gate: for each seed it plants a defect, asserts the
# named lane command turns red AND names the seed file, removes the seed, and
# asserts the lane turns green again. It never compares the tree against a
# committed count or list.
#
# 14 checks: vet, lint, format, race, tidy, the 500-line cap, and the
# coverage floor (RED + GREEN each). Lane commands are the ones the Makefile
# and the pinned go-collector-ci reusable run: `go vet ./...`,
# `golangci-lint run ./...`, `golangci-lint fmt --diff ./...`,
# `go mod tidy -diff`, the CI find/awk 500-line cap (plus the Makefile
# git ls-files loop), and `make race` (COVERAGE_FLOOR overridden for RED).
# The ci-complete aggregation and the govulncheck/gosec lane bodies live in
# the eshu-hq/.github reusable and are proven by its own selftest, not here.
#
# Usage: bash scripts/loki-seed-matrix.sh
# Leaves the tree exactly as found: every seed is removed on every exit path
# and the final `git status --porcelain` is compared with the start snapshot.
set -euo pipefail

cd "$(dirname "$0")/.."

TMP="$(mktemp -d)"
LOG="$TMP/last.log"
pass=0
fail=0
had_coverage_out=0
[ -e coverage.out ] && had_coverage_out=1

cleanup() {
	rm -f zz_seed_vet.go zz_seed_lint.go zz_seed_format.go \
		zz_seed_race_test.go zz_seed_cap.go
	git reset -q -- zz_seed_cap.go 2>/dev/null || true
	git checkout -q -- go.mod go.sum 2>/dev/null || true
	if [ "$had_coverage_out" -eq 0 ]; then
		rm -f coverage.out
	fi
	rm -rf "$TMP"
}
trap cleanup EXIT

if ls zz_seed_* >/dev/null 2>&1; then
	echo "FAIL: leftover seed files present; refusing to seed"
	ls zz_seed_*
	exit 1
fi
if ! git diff --exit-code -- go.mod go.sum >/dev/null 2>&1; then
	echo "FAIL: go.mod/go.sum already drifted; refusing to seed"
	exit 1
fi
SNAPSHOT="$TMP/status-before.txt"
git status --porcelain >"$SNAPSHOT"

ok() { # $1: check name
	echo "PASS: $1"
	pass=$((pass + 1))
}
bad() { # $1: check name
	echo "FAIL: $1"
	tail -n 20 "$LOG" 2>/dev/null || true
	fail=$((fail + 1))
}

# red_check NAME PATTERN CMD...: CMD must exit non-zero and its
# output must contain PATTERN (a finding that names the seed).
red_check() {
	local name=$1 pattern=$2
	shift 2
	if "$@" >"$LOG" 2>&1; then
		bad "$name (lane stayed green)"
	elif grep -q -- "$pattern" "$LOG"; then
		ok "$name"
	else
		bad "$name (red, but no finding names the seed)"
	fi
}

# green_check NAME CMD...: CMD must exit zero.
green_check() {
	local name=$1
	shift
	if "$@" >"$LOG" 2>&1; then
		ok "$name"
	else
		bad "$name"
	fi
}

# The lane commands, as the Makefile and go-collector-ci run them.
lane_vet() { go vet ./...; }
lane_lint() { golangci-lint run ./...; }
lane_format() { golangci-lint fmt --diff ./...; }
lane_race() { go test ./... -race -count=1 -covermode=atomic -coverprofile="$TMP/coverage.out"; }
lane_tidy() {
	go mod tidy -diff
	git diff --exit-code -- go.mod go.sum
}
# CI form of the 500-line cap (find + awk) followed by the Makefile form
# (git ls-files loop); either one flagging the seed turns the lane red.
lane_cap() {
	local over
	over=$(find . -name '*.go' -not -path './vendor/*' -exec wc -l {} + |
		awk -v cap="$cap" '$1 > cap && $2 != "total" {print}')
	if [ -n "$over" ]; then
		echo "$over"
		echo "FAIL: .go files over $cap lines (CI form)"
		return 1
	fi
	over=$(git ls-files '*.go' | while read -r f; do
		n=$(wc -l <"$f")
		if [ "$n" -gt "$cap" ]; then echo "$f: $n lines"; fi
	done)
	if [ -n "$over" ]; then
		echo "$over"
		echo "FAIL: .go files over $cap lines (Makefile form)"
		return 1
	fi
}

# --- vet: seeded printf mismatch turns the vet step red ---
cat >zz_seed_vet.go <<'EOF'
package collector

import "fmt"

var _ = fmt.Sprintf("%d", "not-a-number")
EOF
red_check "vet RED on seeded printf mismatch" "zz_seed_vet.go" lane_vet
rm -f zz_seed_vet.go
green_check "vet GREEN after seed removal" lane_vet

# --- lint: strings.Replace with -1 trips staticcheck QF1004 ---
cat >zz_seed_lint.go <<'EOF'
package collector

import "strings"

var _ = strings.Replace("aaa", "a", "b", -1)
EOF
red_check "lint RED on seeded QF1004" "zz_seed_lint.go" lane_lint
rm -f zz_seed_lint.go
green_check "lint GREEN after seed removal" lane_lint

# --- format: unformatted file trips the gofumpt diff check ---
printf 'package collector\n\nfunc zzSeedFormat() { x:=1; _=x }\n' >zz_seed_format.go
red_check "format RED on seeded unformatted file" "zz_seed_format.go" lane_format
rm -f zz_seed_format.go
green_check "format GREEN after seed removal" lane_format

# --- race: seeded data race fails the race lane ---
cat >zz_seed_race_test.go <<'EOF'
package collector

import "testing"

func TestSeedMatrixRace(t *testing.T) {
	x := 0
	done := make(chan bool)
	go func() { x = 1; done <- true }()
	_ = x
	<-done
}
EOF
red_check "race RED on seeded data race" "zz_seed_race_test.go" lane_race
rm -f zz_seed_race_test.go
green_check "race GREEN after seed removal" lane_race

# --- tidy: seeded unused requirement trips go mod tidy -diff ---
go mod edit -require=github.com/google/uuid@v1.6.0
if lane_tidy >"$LOG" 2>&1; then
	bad "tidy RED on seeded go.mod drift (lane stayed green)"
elif grep -q "google/uuid" "$LOG"; then
	ok "tidy RED on seeded go.mod drift"
else
	bad "tidy RED on seeded go.mod drift (diff does not name the seed)"
fi
git checkout -q -- go.mod go.sum
green_check "tidy GREEN after seed removal" lane_tidy

# --- 500-line cap: a 501-line .go file, no allowlist ---
# Derive the threshold from the Makefile loop so a lane edit propagates.
cap=$(sed -n 's/.*-gt \([0-9][0-9]*\).*/\1/p' Makefile | head -n 1)
if [ -z "$cap" ]; then
	echo "FAIL: cap RED (no live threshold in Makefile)"
	fail=$((fail + 2))
else
	{
		echo "package collector"
		echo ""
		echo "// zzSeedCapPad pads this seed to $((cap + 1)) lines."
		for i in $(seq 1 "$((cap - 3))"); do echo "var _ = $i"; done
		echo "var _ = 0"
	} >zz_seed_cap.go
	lines=$(wc -l <zz_seed_cap.go)
	if [ "$lines" -ne "$((cap + 1))" ]; then
		echo "FAIL: cap seed has $lines lines, want $((cap + 1))"
		fail=$((fail + 1))
	fi
	# Intent-to-add so the Makefile's `git ls-files '*.go'` loop sees it.
	git add -N zz_seed_cap.go
	red_check "cap RED on seeded $((cap + 1))-line file" "zz_seed_cap.go" lane_cap
	git reset -q -- zz_seed_cap.go
	rm -f zz_seed_cap.go
	green_check "cap GREEN after seed removal" lane_cap
fi

# --- coverage floor: `make race` with a raised floor fails, the real one passes ---
floor=$(sed -n 's/^COVERAGE_FLOOR ?= \([0-9.]*\).*/\1/p' Makefile | head -n 1)
if [ -z "$floor" ]; then
	echo "FAIL: floor (no COVERAGE_FLOOR in Makefile)"
	fail=$((fail + 2))
else
	red_check "floor RED at COVERAGE_FLOOR=99 (real floor $floor)" "FAIL: coverage below floor" \
		make race COVERAGE_FLOOR=99
	green_check "floor GREEN at real floor $floor" make race
	# `make race` writes coverage.out in the tree; drop it before the diff.
	if [ "$had_coverage_out" -eq 0 ]; then
		rm -f coverage.out
	fi
fi

echo "seed matrix: pass=$pass fail=$fail"
if ! diff -u "$SNAPSHOT" <(git status --porcelain) >"$TMP/status-diff.txt" 2>&1; then
	echo "FAIL: tree changed by the matrix run:"
	cat "$TMP/status-diff.txt"
	exit 1
fi
[ "$fail" -eq 0 ]
