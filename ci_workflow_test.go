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
		// The job-level reusable call carries the same pin contract as
		// step-level action refs: a full commit SHA, never a mutable tag.
		if job.Uses != "" && !pinnedAction.MatchString(job.Uses) {
			t.Errorf("job %s uses %q, want a full commit SHA pin", name, job.Uses)
		}
		for _, step := range job.Steps {
			if step.Uses != "" && !pinnedAction.MatchString(step.Uses) {
				t.Errorf("job %s uses %q, want a full commit SHA pin", name, step.Uses)
			}
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
// by the schedule-only findings job), and the workflow holds only
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
		// Reusable-call inputs (the lanes gates) carry the same no-secret
		// contract as step bodies.
		for key, value := range job.With {
			texts = append(texts, key+"="+fmt.Sprint(value))
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
