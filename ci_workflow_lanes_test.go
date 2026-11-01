// SPDX-License-Identifier: MIT
// Copyright (c) 2025-2026 eshu-hq

package collector

import (
	"fmt"
	"os"
	"regexp"
	"strings"
	"testing"
)

// wantUses is the single source of truth for the pinned reusable: the v2
// merge commit of eshu-hq/.github, resolved from the tag and read out of
// band. Tests that reason about reusable behavior derive from this pin, so
// a pin move fails them until the new body is re-verified.
const wantUses = "eshu-hq/.github/.github/workflows/go-collector-ci.yml@4d01ab90baf5dd77c21cc1ee6bdf701332613449"

// callerJobs are the only jobs the thin caller may define. The org lane
// names (lint, test, race, shell, image, boundary, govulncheck, gosec,
// actionlint) live inside the pinned reusable at an exact SHA, so they
// cannot churn here; ci-complete keeps its top-level name so the ruleset
// context never changes. conformance, scheduled-findings, and gosec-sarif
// are the repo-specific jobs the reusable cannot express.
var callerJobs = []string{"changes", "lanes", "conformance", "scheduled-findings", "gosec-sarif", "ci-complete"}

// Retired inline lane gates, verbatim: each run-* input must be the exact
// expression the inline lane's `if:` carried, so lane selection cannot go
// dark (or widen) unnoticed.
const (
	gateGoMisc = "github.event_name != 'pull_request' || needs.changes.outputs.go == 'true' || needs.changes.outputs.misc == 'true' || needs.changes.outputs.workflows == 'true'"
	gateTest   = "github.event_name != 'pull_request' || needs.changes.outputs.go == 'true' || needs.changes.outputs.fixtures == 'true' || needs.changes.outputs.shell == 'true' || needs.changes.outputs.misc == 'true' || needs.changes.outputs.workflows == 'true'"
	gateImage  = "github.event_name == 'push' || github.event_name == 'schedule' || github.event_name == 'workflow_dispatch' || needs.changes.outputs.image == 'true' || needs.changes.outputs.go == 'true' || needs.changes.outputs.workflows == 'true'"
)

func gate(expr string) string { return "${{ " + expr + " }}" }

var effectiveLanes = map[string]string{
	"run-lint":               gate(gateGoMisc),
	"run-test":               gate(gateTest),
	"run-race":               gate(gateGoMisc),
	"run-shell":              gate("github.event_name != 'pull_request' || needs.changes.outputs.shell == 'true'"),
	"run-image":              gate(gateImage),
	"run-boundary":           gate(gateGoMisc),
	"run-govulncheck":        gate("github.event_name != 'pull_request' || needs.changes.outputs.go == 'true' || needs.changes.outputs.deps == 'true' || needs.changes.outputs.workflows == 'true'"),
	"run-gosec":              gate("github.event_name != 'pull_request' || needs.changes.outputs.go == 'true' || needs.changes.outputs.workflows == 'true'"),
	"run-actionlint":         gate("github.event_name != 'pull_request' || needs.changes.outputs.workflows == 'true'"),
	"boundary-command":       "go test -count=1 -run TestImportBoundaryRejectsCoreAndCrossCollectorImports .",
	"gosec-fail-on-findings": gate("github.event_name == 'schedule'"),
	"image-tag":              "eshu-collector-loki:ci",
	"image-context":          ".",
	"module":                 "loki",
	"go-version-file":        "go.mod",
	// Every tool the retired lanes pinned stays pinned by input: reusable
	// defaults float across tags and must neither downgrade nor upgrade a
	// scanner silently.
	"golangci-lint-version": "v2.12.2",
	"govulncheck-version":   "v1.9.0",
	"gosec-version":         "v2.30.0",
	"actionlint-version":    "1.7.12",
	"actionlint-sha256":     "8aca8db96f1b94770f1b0d72b6dddcb1ebb8123cb3712530b08cc387b349a3d8",
}

func lanesJob(t *testing.T) workflowFile {
	t.Helper()

	workflow := readCIWorkflow(t)
	if _, ok := workflow.Jobs["lanes"]; !ok {
		t.Fatal("ci.yml has no lanes job")
	}
	return workflow
}

// TestCIWorkflowKeepsCallerJobs keeps the org lane contract across the
// reusable move: the caller holds exactly its jobs, lanes calls the pinned
// v2 reusable, changes runs on every event, and ci-complete aggregates the
// classifier, the lanes, and the repo-specific conformance job.
func TestCIWorkflowKeepsCallerJobs(t *testing.T) {
	t.Parallel()

	workflow := lanesJob(t)
	if len(workflow.Jobs) != len(callerJobs) {
		names := make([]string, 0, len(workflow.Jobs))
		for name := range workflow.Jobs {
			names = append(names, name)
		}
		t.Errorf("ci.yml jobs = %v, want exactly %v: lane bodies live in the reusable", names, callerJobs)
	}
	for _, name := range callerJobs {
		if _, ok := workflow.Jobs[name]; !ok {
			t.Errorf("ci.yml has no %q job, want the caller jobs %v", name, callerJobs)
		}
	}
	if got := workflow.Jobs["lanes"].Uses; got != wantUses {
		t.Errorf("lanes uses %q, want the pinned v2 reusable %q", got, wantUses)
	}
	// Every lane needs changes; skipping it on any event skips every lane
	// and the run goes vacantly green.
	if cond := workflow.Jobs["changes"].If; cond != "" {
		t.Errorf("changes job if = %q, want none: it must run on every event", cond)
	}
	needed := map[string]bool{}
	for _, name := range workflow.Jobs["ci-complete"].Needs {
		needed[name] = true
	}
	for _, name := range []string{"changes", "lanes", "conformance"} {
		if !needed[name] {
			t.Errorf("ci-complete needs %v, want it to aggregate %s", workflow.Jobs["ci-complete"].Needs, name)
		}
	}
}

// TestLanesInputsKeepRetiredGates pins every reusable input to the value
// the retired inline lane used.
func TestLanesInputsKeepRetiredGates(t *testing.T) {
	t.Parallel()

	workflow := lanesJob(t)
	lanes := workflow.Jobs["lanes"]
	for input, want := range effectiveLanes {
		val, ok := lanes.With[input]
		if !ok || fmt.Sprint(val) != want {
			t.Errorf("lanes input %q = %v, want %q", input, val, want)
		}
	}
	cmd := fmt.Sprint(lanes.With["image-command"])
	for _, want := range []string{
		"eshu-collector-loki:ci seed --dest /component",
		`test -x "$seed/collector"`,
		`test -s "$seed/manifest.yaml"`,
		"@sha256:[0-9a-f]{64}",
		"[ \"$pinned\" -eq \"$total\" ]",
		"[ \"$from_pinned\" -eq \"$from_total\" ]",
	} {
		if !strings.Contains(cmd, want) {
			t.Errorf("lanes image-command lacks %q: the seed or digest-pin gate was dropped", want)
		}
	}
}

// TestCIWorkflowSetsJobTimeouts keeps a hung runner from holding a PR open
// forever: every job declares a timeout except the lanes reusable call,
// where the workflow schema forbids timeout-minutes and each inner lane
// carries its own (lint/race/image 15, test/boundary/govulncheck 10,
// gosec 30, shell/actionlint 5 in the pinned body).
func TestCIWorkflowSetsJobTimeouts(t *testing.T) {
	t.Parallel()

	workflow := lanesJob(t)
	for name, job := range workflow.Jobs {
		if job.Uses != "" {
			if job.Timeout != 0 {
				t.Errorf("job %s sets timeout-minutes: the schema forbids it on reusable calls", name)
			}
			continue
		}
		if job.Timeout <= 0 {
			t.Errorf("job %s sets no timeout-minutes", name)
		}
	}
}

// coverageFloorPattern matches the Makefile floor literal
// (`COVERAGE_FLOOR ?= 90.1`).
var coverageFloorPattern = regexp.MustCompile(`COVERAGE_FLOOR\s*\??=\s*["']?([0-9.]+)["']?`)

// TestCoverageFloorParity keeps `make ci` honest: the Makefile floor and
// the lanes coverage-floor input name the same number, so a floor ratchet
// that touches one file and not the other fails here.
func TestCoverageFloorParity(t *testing.T) {
	t.Parallel()

	makefile, err := os.ReadFile("Makefile")
	if err != nil {
		t.Fatal(err)
	}
	workflow := lanesJob(t)
	ciFloor, ok := workflow.Jobs["lanes"].With["coverage-floor"]
	makeFloor := coverageFloorPattern.FindStringSubmatch(string(makefile))
	if len(makeFloor) < 2 || !ok {
		t.Fatalf("COVERAGE_FLOOR literal missing (Makefile=%q lanes=%v)", makeFloor, ciFloor)
	}
	if makeFloor[1] != fmt.Sprint(ciFloor) {
		t.Errorf("COVERAGE_FLOOR drift: Makefile=%s lanes=%v, want one shared value", makeFloor[1], ciFloor)
	}
}

// TestToolPinParity keeps local and CI findings in agreement: the Makefile
// golangci-lint and actionlint versions equal the lanes inputs.
func TestToolPinParity(t *testing.T) {
	t.Parallel()

	makefile, err := os.ReadFile("Makefile")
	if err != nil {
		t.Fatal(err)
	}
	workflow := lanesJob(t)
	with := workflow.Jobs["lanes"].With
	lint := regexp.MustCompile(`GOLANGCI_LINT_VERSION\s*\?=\s*(\S+)`).FindStringSubmatch(string(makefile))
	if len(lint) < 2 || lint[1] != fmt.Sprint(with["golangci-lint-version"]) {
		t.Errorf("golangci-lint drift: Makefile=%v lanes=%v", lint, with["golangci-lint-version"])
	}
	action := regexp.MustCompile(`actionlint/cmd/actionlint@v(\S+)`).FindStringSubmatch(string(makefile))
	if len(action) < 2 || action[1] != fmt.Sprint(with["actionlint-version"]) {
		t.Errorf("actionlint drift: Makefile=%v lanes=%v", action, with["actionlint-version"])
	}
}

// scanFailureProblems returns every way a scan lane breaks its scheduled
// failure contract. The scan bodies live in the pinned reusable, so the
// caller owns the gate (the scan must run on schedule and on the paths that
// can newly expose findings) and the gosec-fail-on-findings schedule gate.
func scanFailureProblems(gateExpr string, scheduleFails bool) []string {
	var problems []string
	if !strings.Contains(gateExpr, "github.event_name != 'pull_request'") {
		problems = append(problems, "scan gate never runs on schedule/push (missing the non-PR short-circuit)")
	}
	if !scheduleFails {
		problems = append(problems, "scheduled findings stay green: triage has nothing loud to ride")
	}
	return problems
}

// TestScanLanesFailLoudOnSchedule keeps the govulncheck and gosec promise:
// Go and dependency changes scan, scheduled runs always scan, scheduled
// gosec findings fail red, and PR runs stay advisory (findings cannot fail
// a PR). The reusable fails govulncheck loud by construction and fails
// gosec only under gosec-fail-on-findings.
func TestScanLanesFailLoudOnSchedule(t *testing.T) {
	t.Parallel()

	workflow := lanesJob(t)
	lanes := workflow.Jobs["lanes"]
	vuln := fmt.Sprint(lanes.With["run-govulncheck"])
	sec := fmt.Sprint(lanes.With["run-gosec"])
	failGate := fmt.Sprint(lanes.With["gosec-fail-on-findings"])
	// Issue #14 (blocking gosec on PRs) stays open and time-gated: the
	// fail gate must be schedule-only, never true or PR-reachable.
	if failGate != gate("github.event_name == 'schedule'") {
		t.Errorf("gosec-fail-on-findings = %q, want the schedule-only gate (issue #14 is time-gated)", failGate)
	}
	scheduleFails := lanes.Uses == wantUses && strings.Contains(failGate, "schedule")
	if problems := scanFailureProblems(vuln, scheduleFails); len(problems) > 0 {
		t.Errorf("govulncheck schedule gate is broken:\n%s", strings.Join(problems, "\n"))
	}
	if problems := scanFailureProblems(sec, scheduleFails); len(problems) > 0 {
		t.Errorf("gosec schedule gate is broken:\n%s", strings.Join(problems, "\n"))
	}
	for _, want := range []string{"needs.changes.outputs.deps", "needs.changes.outputs.go"} {
		if !strings.Contains(vuln, want) {
			t.Errorf("govulncheck gate %q lacks %s: findings from those paths would go unscanned", vuln, want)
		}
	}

	if len(scanFailureProblems("needs.changes.outputs.deps == 'true'", true)) == 0 {
		t.Error("PR-only gate: the gate accepted a scan that never runs on schedule")
	}
	if len(scanFailureProblems(vuln, false)) == 0 {
		t.Error("advisory-only schedule: the gate accepted findings that stay green on the sweep")
	}
}

// TestImageGateRunsOnGoSources keeps the shipped artifact validated on PRs
// that change what it embeds: the Dockerfile builds the binary from the Go
// sources, so a Go-source PR must select the image lane (a go.mod toolchain
// bump past the pinned base would otherwise pass every Go lane yet break
// docker build, surfacing as post-merge red main). Pushes, schedules, and
// manual runs always build.
func TestImageGateRunsOnGoSources(t *testing.T) {
	t.Parallel()

	workflow := lanesJob(t)
	cond := fmt.Sprint(workflow.Jobs["lanes"].With["run-image"])
	for _, want := range []string{"needs.changes.outputs.go == 'true'", "github.event_name == 'push'", "github.event_name == 'schedule'", "github.event_name == 'workflow_dispatch'"} {
		if !strings.Contains(cond, want) {
			t.Errorf("run-image gate %q lacks %q", cond, want)
		}
	}
}

// TestConformanceJobRunsScript keeps scripts/run-conformance.sh executed in
// CI: the reusable test lane runs plain build+test and cannot run the
// script, so a caller job does, under the retired test-lane gate (shell
// edits trip it, so a script-only PR still runs the script and a
// mirror-breaking edit fails here, not just shellcheck).
func TestConformanceJobRunsScript(t *testing.T) {
	t.Parallel()

	workflow := lanesJob(t)
	job, ok := workflow.Jobs["conformance"]
	if !ok {
		t.Fatal("ci.yml has no conformance job")
	}
	if job.If != gate(gateTest) {
		t.Errorf("conformance if = %q, want the retired test-lane gate %q", job.If, gate(gateTest))
	}
	ran := false
	for _, step := range job.Steps {
		if strings.Contains(step.Run, "scripts/run-conformance.sh") {
			ran = true
		}
	}
	if !ran {
		t.Error("conformance job never runs scripts/run-conformance.sh: a mirror-breaking edit passes CI")
	}
}

// TestSchedulerJobsStayCallerSide pins the two jobs the reusable cannot own
// (it runs contents:read only and omits the Security-tab upload): the weekly
// findings issue fires only on a failed scheduled run, and the SARIF upload
// is non-PR only, advisory, and fed by the reusable gosec artifact.
func TestSchedulerJobsStayCallerSide(t *testing.T) {
	t.Parallel()

	workflow := lanesJob(t)
	issue := workflow.Jobs["scheduled-findings"]
	for _, want := range []string{"failure()", "schedule"} {
		if !strings.Contains(issue.If, want) {
			t.Errorf("scheduled-findings if = %q, want %s (fork-PR safe, reachable on failure)", issue.If, want)
		}
	}
	if issue.Permissions["issues"] != "write" {
		t.Errorf("scheduled-findings permissions = %v, want issues: write", issue.Permissions)
	}
	filed := false
	for _, step := range issue.Steps {
		filed = filed || strings.Contains(step.Run, "gh issue create")
	}
	if !filed {
		t.Error("scheduled-findings never files an issue")
	}

	sarif := workflow.Jobs["gosec-sarif"]
	if !strings.Contains(sarif.If, "github.event_name != 'pull_request'") {
		t.Errorf("gosec-sarif if = %q, want non-PR only: fork PRs cannot write code scanning", sarif.If)
	}
	if sarif.Permissions["security-events"] != "write" {
		t.Errorf("gosec-sarif permissions = %v, want security-events: write", sarif.Permissions)
	}
	downloads, uploads := false, false
	for _, step := range sarif.Steps {
		if strings.HasPrefix(step.Uses, "actions/download-artifact@") && fmt.Sprint(step.With["name"]) == "gosec-loki" {
			downloads = true
		}
		if strings.HasPrefix(step.Uses, "github/codeql-action/upload-sarif@") {
			uploads = step.ContinueOnError
		}
	}
	if !downloads {
		t.Error("gosec-sarif never downloads the reusable gosec-loki artifact")
	}
	if !uploads {
		t.Error("gosec-sarif upload-sarif step missing or not continue-on-error")
	}
}
