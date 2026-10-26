// SPDX-License-Identifier: MIT
// Copyright (c) 2025-2026 eshu-hq

package collector

import (
	"os"
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
