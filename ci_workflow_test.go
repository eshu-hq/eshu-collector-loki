// SPDX-License-Identifier: MIT
// Copyright (c) 2025-2026 eshu-hq

package collector

import (
	"fmt"
	"os"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// TestCIWorkflowPinsActionsByCommitSHA keeps a moved tag from changing what
// pull-request CI executes. CI holds only contents: read, but it still runs
// third-party action code on every pull request.
func TestCIWorkflowPinsActionsByCommitSHA(t *testing.T) {
	t.Parallel()

	raw, err := os.ReadFile(".github/workflows/ci.yml")
	if err != nil {
		t.Fatal(err)
	}
	var workflow workflowFile
	if err := yaml.Unmarshal(raw, &workflow); err != nil {
		t.Fatal(err)
	}
	if len(workflow.Jobs) == 0 {
		t.Fatal("ci.yml defines no jobs")
	}
	for name, job := range workflow.Jobs {
		for _, step := range job.Steps {
			if step.Uses != "" && !pinnedAction.MatchString(step.Uses) {
				t.Errorf("job %s uses %q, want a full commit SHA pin", name, step.Uses)
			}
		}
	}
}

// frozenCILanes are the stable org job names. Only ci-complete is ever
// required in a ruleset, so these names must not churn. vet is a step in
// lint, not a job; boundary stays a job (the import-boundary gate).
var frozenCILanes = []string{"changes", "lint", "test", "race", "shell", "image", "boundary", "govulncheck", "gosec", "actionlint", "ci-complete"}

// TestCIWorkflowKeepsFrozenJobNames keeps the org lane contract: the frozen
// names exist, and the retired vet job is gone (vet is a step in lint now).
func TestCIWorkflowKeepsFrozenJobNames(t *testing.T) {
	t.Parallel()

	workflow := readCIWorkflow(t)
	for _, name := range frozenCILanes {
		if _, ok := workflow.Jobs[name]; !ok {
			t.Errorf("ci.yml has no %q job, want the frozen lane names %v", name, frozenCILanes)
		}
	}
	if _, ok := workflow.Jobs["vet"]; ok {
		t.Errorf("ci.yml still has a %q job, want it folded into the lint lane", "vet")
	}
	complete, ok := workflow.Jobs["ci-complete"]
	if !ok {
		t.Fatal("ci.yml has no ci-complete job")
	}
	needed := make(map[string]bool, len(complete.Needs))
	for _, name := range complete.Needs {
		needed[name] = true
	}
	for _, name := range frozenCILanes {
		if name == "ci-complete" {
			continue
		}
		if !needed[name] {
			t.Errorf("ci-complete needs %v, want every other frozen lane %v", complete.Needs, frozenCILanes)
		}
	}
}

// TestCIWorkflowSetsJobTimeouts keeps a hung runner from holding a PR open
// forever: every lane declares a timeout.
func TestCIWorkflowSetsJobTimeouts(t *testing.T) {
	t.Parallel()

	workflow := readCIWorkflow(t)
	for name, job := range workflow.Jobs {
		if job.Timeout <= 0 {
			t.Errorf("job %s sets no timeout-minutes", name)
		}
	}
}

// TestCIWorkflowCancelsSupersededRuns keeps stale pushes from consuming
// runners: one concurrency group per PR (or ref) with cancellation.
func TestCIWorkflowCancelsSupersededRuns(t *testing.T) {
	t.Parallel()

	workflow := readCIWorkflow(t)
	if group := fmt.Sprint(workflow.Concurrency["group"]); !strings.Contains(group, "github.event.pull_request.number") {
		t.Errorf("concurrency group = %q, want one group per PR", group)
	}
	if cancel := fmt.Sprint(workflow.Concurrency["cancel-in-progress"]); cancel != "true" {
		t.Errorf("cancel-in-progress = %q, want true", cancel)
	}
}

// TestCIWorkflowPRLeanesNeedNoConfiguredSecrets keeps fork PRs working: the
// only secret any lane may touch is the automatic GITHUB_TOKEN (used solely
// by the schedule-only issue step), and the workflow holds only
// contents: read.
func TestCIWorkflowPRLeanesNeedNoConfiguredSecrets(t *testing.T) {
	t.Parallel()

	workflow := readCIWorkflow(t)
	if got := workflow.Permissions["contents"]; got != "read" {
		t.Errorf("top-level contents permission = %q, want read", got)
	}
	for name, job := range workflow.Jobs {
		var texts []string
		for key, value := range job.Env {
			texts = append(texts, key+"="+value)
		}
		for _, step := range job.Steps {
			texts = append(texts, step.Run)
			for key, value := range step.Env {
				texts = append(texts, key+"="+value)
			}
			for key, value := range step.With {
				texts = append(texts, key+"="+fmt.Sprint(value))
			}
		}
		for _, text := range texts {
			for _, match := range secretUsePattern.FindAllStringSubmatch(text, -1) {
				if match[1] != "GITHUB_TOKEN" {
					t.Errorf("job %s uses configured secret %q, want no secrets but GITHUB_TOKEN", name, match[1])
				}
			}
		}
	}
}

// TestChangesDefinesFixtureAndMiscClasses keeps the skip matrix honest: the
// changes job must define the fixtures class (fixture-only PRs run test
// only) and the misc catch-all (unclassified files run the full Go lanes),
// with the some-with-excludes quantifier the misc negations need.
func TestChangesDefinesFixtureAndMiscClasses(t *testing.T) {
	t.Parallel()

	raw, err := os.ReadFile(".github/workflows/ci.yml")
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	for _, want := range []string{"fixtures:", "misc:", "predicate-quantifier: 'some-with-excludes'"} {
		if !strings.Contains(text, want) {
			t.Errorf("ci.yml changes job lacks %q", want)
		}
	}
	// Every fixtures entry must be negated in misc, or a fixture-only PR
	// also trips misc and pays for lint and race.
	miscIdx := strings.Index(text, "misc:")
	if miscIdx < 0 {
		t.Fatal("ci.yml has no misc class")
	}
	misc := text[miscIdx:]
	for _, entry := range []string{"manifest.yaml", "grant.example.json", "config.example.yaml", "**/*.json", "**/*.sh", "Dockerfile"} {
		if !strings.Contains(misc, "'!"+entry+"'") {
			t.Errorf("misc class does not negate %q: a narrow-lane PR would also run the full lanes", entry)
		}
	}
}

// aggregatorProblems returns every way run breaks the ci-complete contract:
// it must compare each job's result (whole-object comparison never matches,
// a bare needs.*.result array breaks green runs), fail failed and cancelled
// lanes, and say skips are allowed.
func aggregatorProblems(run string) []string {
	var problems []string
	if !strings.Contains(run, `v["result"]`) && !strings.Contains(run, `.get("result")`) {
		problems = append(problems, `aggregator must compare v["result"] per job, not whole job objects (false-green)`)
	}
	if strings.Contains(run, "join(needs.*.result") {
		problems = append(problems, "aggregator must not expand needs.*.result (bare array breaks green runs)")
	}
	for _, want := range []string{`"failure"`, `"cancelled"`, "skip"} {
		if !strings.Contains(run, want) {
			problems = append(problems, "aggregator never mentions "+want)
		}
	}
	return problems
}

// TestCICompleteAggregatesPerJobResults pins the aggregator shape against
// its two historical false modes: the pre-gates shell loop over
// join(needs.*.result) and a whole-object comparison. The live ci.yml text
// must pass; both mutants must fail.
func TestCICompleteAggregatesPerJobResults(t *testing.T) {
	t.Parallel()

	workflow := readCIWorkflow(t)
	complete, ok := workflow.Jobs["ci-complete"]
	if !ok {
		t.Fatal("ci.yml has no ci-complete job")
	}
	var runs []string
	for _, step := range complete.Steps {
		runs = append(runs, step.Run)
	}
	if problems := aggregatorProblems(strings.Join(runs, "\n")); len(problems) > 0 {
		t.Errorf("ci-complete aggregator breaks its contract:\n%s", strings.Join(problems, "\n"))
	}

	for name, run := range map[string]string{
		// The pre-gates aggregator: a bare needs.*.result array joined
		// into shell words, blind to skips and cancelled lanes.
		"bare needs array": "RESULTS: ${{ join(needs.*.result, ' ') }}\nfor result in $RESULTS; do if [ \"$result\" != \"success\" ]; then exit 1; fi; done",
		// Whole job objects never equal "failure": every red run passed.
		"whole objects": `bad = {k: v for k, v in needs.items() if v == "failure"}`,
	} {
		if len(aggregatorProblems(run)) == 0 {
			t.Errorf("%s: the gate accepted a violating aggregator", name)
		}
	}
}

// govulnReachabilityProblems returns every way the govulncheck lane breaks
// its schedule-only notification contract: the scan must fail loud (no
// -no-fail, no || true, no continue-on-error) so the failure()-gated issue
// step below it stays reachable, and the issue step must carry failure()
// plus the schedule gate (fork-PR safety).
func govulnReachabilityProblems(scan, issue workflowStep) []string {
	var problems []string
	for _, quiet := range []string{"-no-fail", "|| true"} {
		if strings.Contains(scan.Run, quiet) {
			problems = append(problems, "govulncheck scan contains "+quiet+" (the issue step is unreachable)")
		}
	}
	if scan.ContinueOnError {
		problems = append(problems, "govulncheck scan sets continue-on-error (the issue step is unreachable)")
	}
	for _, want := range []string{"failure()", "schedule"} {
		if !strings.Contains(issue.If, want) {
			problems = append(problems, "govulncheck issue step if lacks "+want)
		}
	}
	return problems
}

// TestGovulncheckIssueStepIsReachable pins the schedule-only notification
// shape: the live ci.yml text must pass, and a quieted scan / ungated issue
// mutant must fail (mirrors TestCICompleteAggregatesPerJobResults).
func TestGovulncheckIssueStepIsReachable(t *testing.T) {
	t.Parallel()

	workflow := readCIWorkflow(t)
	job, ok := workflow.Jobs["govulncheck"]
	if !ok {
		t.Fatal("ci.yml has no govulncheck job")
	}
	scanIdx := stepIndex(job.Steps, func(s workflowStep) bool { return strings.Contains(s.Run, "-scan package") })
	issueIdx := stepIndex(job.Steps, func(s workflowStep) bool { return strings.Contains(s.Run, "gh issue create") })
	if scanIdx < 0 || issueIdx < 0 {
		t.Fatalf("govulncheck job has no scan/issue steps (scan=%d issue=%d)", scanIdx, issueIdx)
	}
	if problems := govulnReachabilityProblems(job.Steps[scanIdx], job.Steps[issueIdx]); len(problems) > 0 {
		t.Errorf("govulncheck notification is unreachable:\n%s", strings.Join(problems, "\n"))
	}

	scan := job.Steps[scanIdx]
	scan.Run += " govulncheck -no-fail"
	if len(govulnReachabilityProblems(scan, job.Steps[issueIdx])) == 0 {
		t.Errorf("-no-fail scan: the gate accepted an unreachable issue step")
	}
	issue := job.Steps[issueIdx]
	issue.If = ""
	if len(govulnReachabilityProblems(job.Steps[scanIdx], issue)) == 0 {
		t.Errorf("ungated issue step: the gate accepted a step without failure()/schedule")
	}
}

// gosecReachabilityProblems returns every way the gosec lane breaks its
// schedule-only notification contract: the count step must fail scheduled
// runs that carry findings (otherwise the failure()-gated issue step below
// it stays reachable only on paper), and the issue step must carry
// failure() plus the schedule gate.
func gosecReachabilityProblems(countRun string, issue workflowStep) []string {
	var problems []string
	if !strings.Contains(countRun, "schedule") || !strings.Contains(countRun, "exit 1") {
		problems = append(problems, "gosec count step never fails a scheduled run with findings (the issue step is unreachable)")
	}
	for _, want := range []string{"failure()", "schedule"} {
		if !strings.Contains(issue.If, want) {
			problems = append(problems, "gosec issue step if lacks "+want)
		}
	}
	return problems
}

// TestGosecIssueStepIsReachable pins the gosec notification shape the same
// way: the live text must pass, and a count step that stays green on
// scheduled findings (or an ungated issue step) must fail.
func TestGosecIssueStepIsReachable(t *testing.T) {
	t.Parallel()

	workflow := readCIWorkflow(t)
	job, ok := workflow.Jobs["gosec"]
	if !ok {
		t.Fatal("ci.yml has no gosec job")
	}
	countIdx := stepIndex(job.Steps, func(s workflowStep) bool {
		return strings.Contains(s.Run, "gosec.sarif") && strings.Contains(s.Run, "schedule")
	})
	issueIdx := stepIndex(job.Steps, func(s workflowStep) bool { return strings.Contains(s.Run, "gh issue create") })
	if countIdx < 0 || issueIdx < 0 {
		t.Fatalf("gosec job has no count/issue steps (count=%d issue=%d)", countIdx, issueIdx)
	}
	if problems := gosecReachabilityProblems(job.Steps[countIdx].Run, job.Steps[issueIdx]); len(problems) > 0 {
		t.Errorf("gosec notification is unreachable:\n%s", strings.Join(problems, "\n"))
	}

	if len(gosecReachabilityProblems("echo no findings", job.Steps[issueIdx])) == 0 {
		t.Errorf("advisory-only count: the gate accepted a count step that never fails")
	}
	issue := job.Steps[issueIdx]
	issue.If = ""
	if len(gosecReachabilityProblems(job.Steps[countIdx].Run, issue)) == 0 {
		t.Errorf("ungated issue step: the gate accepted a step without failure()/schedule")
	}
}

func readCIWorkflow(t *testing.T) workflowFile {
	t.Helper()

	raw, err := os.ReadFile(".github/workflows/ci.yml")
	if err != nil {
		t.Fatal(err)
	}
	var workflow workflowFile
	if err := yaml.Unmarshal(raw, &workflow); err != nil {
		t.Fatal(err)
	}
	if len(workflow.Jobs) == 0 {
		t.Fatal("ci.yml defines no jobs")
	}
	return workflow
}

// conformanceScript is the single source of truth for the test lane body:
// the test job must execute it, so a breaking edit fails the lane instead
// of passing a shellcheck-only run.
const conformanceScript = "scripts/run-conformance.sh"

// testLaneRunsConformance reports whether any test-lane step executes the
// conformance script.
func testLaneRunsConformance(steps []workflowStep) bool {
	for _, s := range steps {
		if strings.Contains(s.Run, conformanceScript) {
			return true
		}
	}
	return false
}

// TestTestLaneExecutesConformanceScript pins the orphan-script fix: the
// test job must execute scripts/run-conformance.sh, and must trigger on
// shell edits so a script-only PR trips the lane. The live ci.yml text
// must pass; an inline-only lane must fail.
func TestTestLaneExecutesConformanceScript(t *testing.T) {
	t.Parallel()
	workflow := readCIWorkflow(t)
	job, ok := workflow.Jobs["test"]
	if !ok {
		t.Fatal("ci.yml has no test job")
	}
	if !testLaneRunsConformance(job.Steps) {
		t.Errorf("test job never runs %s: a mirror-breaking edit passes CI", conformanceScript)
	}
	if !strings.Contains(job.If, "shell") {
		t.Errorf("test job if = %q, want it to trigger on shell edits so a script-only change runs the lane", job.If)
	}

	if testLaneRunsConformance([]workflowStep{{Run: "go build ./...\ngo test ./... -count=1"}}) {
		t.Errorf("inline-only lane: the gate accepted a test lane that never runs the conformance script")
	}
}
