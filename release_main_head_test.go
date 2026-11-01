// SPDX-License-Identifier: MIT
// Copyright (c) 2025-2026 eshu-hq

package collector

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

const (
	// mainHeadGuardName is the release step that refuses a version tag cut
	// from any commit other than the protected main head.
	mainHeadGuardName = "Require the tag to point at the protected main head"
	// manifestGuardName is the tag-only step the main-head guard must precede.
	manifestGuardName = "Require the tag to match the manifest version"
	tagOnlyCondition  = "github.ref_type == 'tag'"
)

// releaseCommentText returns the workflow's comment lines joined into one
// string, so header prose can be checked without caring about line wraps.
func releaseCommentText(raw string) string {
	var words []string
	for _, line := range strings.Split(raw, "\n") {
		if text, ok := strings.CutPrefix(strings.TrimSpace(line), "#"); ok {
			words = append(words, strings.Fields(text)...)
		}
	}
	return strings.Join(words, " ")
}

// mainHeadGuardProblems returns every way raw lets a v* tag publish from a
// commit other than the protected main head: the guard step must exist, run
// only on tag events, compare the tagged commit with a freshly fetched
// origin/main, fail on a mismatch, and run after a credential-free checkout
// and before the manifest check and the image push. The workflow header
// must say so.
func mainHeadGuardProblems(raw []byte) []string {
	var workflow workflowFile
	if err := yaml.Unmarshal(raw, &workflow); err != nil {
		return []string{fmt.Sprintf("release.yml does not parse: %v", err)}
	}
	var problems []string
	image := workflow.Jobs["image"]
	if got := image.Env["TAG_NAME"]; got != "${{ github.ref_name }}" {
		problems = append(problems, fmt.Sprintf("job env TAG_NAME = %q, want ${{ github.ref_name }}", got))
	}
	guardIdx := stepIndex(image.Steps, func(s workflowStep) bool { return s.Name == mainHeadGuardName })
	if guardIdx < 0 {
		return append(problems, fmt.Sprintf("no step named %q", mainHeadGuardName))
	}
	guard := image.Steps[guardIdx]
	if guard.If != tagOnlyCondition {
		problems = append(problems, fmt.Sprintf("main-head guard runs when %q, want only tag events", guard.If))
	}
	if got := guard.Env["TAG_COMMIT"]; got != "${{ github.sha }}" {
		problems = append(problems, fmt.Sprintf("main-head guard TAG_COMMIT = %q, want ${{ github.sha }}", got))
	}
	for _, want := range []string{
		"git fetch origin main",
		`main_head="$(git rev-parse origin/main)"`,
		`[[ "${TAG_COMMIT}" != "${main_head}" ]]`,
		"exit 1",
	} {
		if !strings.Contains(guard.Run, want) {
			problems = append(problems, "main-head guard lacks "+want)
		}
	}
	checkoutIdx := stepIndex(image.Steps, func(s workflowStep) bool { return strings.HasPrefix(s.Uses, "actions/checkout@") })
	switch {
	case checkoutIdx < 0:
		problems = append(problems, "no checkout step")
	case checkoutIdx > guardIdx:
		problems = append(problems, "the main-head guard runs before the checkout")
	case fmt.Sprint(image.Steps[checkoutIdx].With["persist-credentials"]) != "false":
		problems = append(problems, "checkout must set persist-credentials: false")
	}
	if idx := stepIndex(image.Steps, func(s workflowStep) bool { return s.Name == manifestGuardName }); idx >= 0 && idx < guardIdx {
		problems = append(problems, "the main-head guard runs after the manifest version check")
	}
	if idx := stepIndex(image.Steps, func(s workflowStep) bool { return fmt.Sprint(s.With["push"]) == "true" }); idx >= 0 && idx < guardIdx {
		problems = append(problems, "the main-head guard runs after the image is pushed")
	}
	comment := releaseCommentText(string(raw))
	if strings.Contains(comment, "does not check") {
		problems = append(problems, "the header says the workflow does not check the tagged commit")
	}
	if !strings.Contains(comment, "protected main head") {
		problems = append(problems, `the header does not mention the "protected main head"`)
	}
	return problems
}

func TestReleaseWorkflowRequiresTagAtMainHead(t *testing.T) {
	t.Parallel()

	raw, err := os.ReadFile(".github/workflows/release.yml")
	if err != nil {
		t.Fatal(err)
	}
	if problems := mainHeadGuardProblems(raw); len(problems) > 0 {
		t.Fatalf("release.yml:\n%s", strings.Join(problems, "\n"))
	}
}

// TestMainHeadGuardGateRejectsViolations feeds the check synthetic workflows
// so it is proven to fail, not only to pass on the checked-in file.
func TestMainHeadGuardGateRejectsViolations(t *testing.T) {
	t.Parallel()

	raw, err := os.ReadFile(".github/workflows/release.yml")
	if err != nil {
		t.Fatal(err)
	}
	good := string(raw)
	guardHeader := "      - name: " + mainHeadGuardName + "\n"
	for name, mutate := range map[string]func(string) string{
		"guard renamed": func(s string) string {
			return strings.Replace(s, guardHeader, "      - name: Check the tag\n", 1)
		},
		"guard runs on every event": func(s string) string {
			return strings.Replace(s, guardHeader+"        if: github.ref_type == 'tag'\n", guardHeader, 1)
		},
		"guard compares the wrong commit": func(s string) string {
			return strings.Replace(s, "TAG_COMMIT: ${{ github.sha }}", "TAG_COMMIT: ${{ github.ref }}", 1)
		},
		"guard skips the fetch": func(s string) string {
			return strings.Replace(s, "git fetch origin main --quiet\n", "", 1)
		},
		"guard cannot fail": func(s string) string {
			return strings.Replace(s, "            exit 1\n          fi\n      - name: "+manifestGuardName,
				"          fi\n      - name: "+manifestGuardName, 1)
		},
		"guard runs after the manifest check": func(s string) string {
			start := strings.Index(s, guardHeader)
			end := strings.Index(s, "      - name: "+manifestGuardName+"\n")
			if start < 0 || end < start {
				return s
			}
			block := s[start:end]
			s = s[:start] + s[end:]
			next := strings.Index(s, "      - name: Set up Docker Buildx\n")
			return s[:next] + block + s[next:]
		},
		"checkout keeps credentials": func(s string) string {
			return strings.Replace(s, "persist-credentials: false", "persist-credentials: true", 1)
		},
		"tag name env dropped": func(s string) string {
			return strings.Replace(s, "      TAG_NAME: ${{ github.ref_name }}\n", "", 1)
		},
		"header claims no main check": func(s string) string {
			return strings.Replace(s, "protected main head", "protected branch; the workflow does not check", 1)
		},
	} {
		mutated := mutate(good)
		if mutated == good {
			t.Fatalf("%s: the mutation did not change the workflow", name)
		}
		if len(mainHeadGuardProblems([]byte(mutated))) == 0 {
			t.Errorf("%s: the gate accepted a violating workflow", name)
		}
	}
}

// gitIn runs git in dir with a hermetic environment and returns its trimmed
// standard output.
func gitIn(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = []string{
		"PATH=" + os.Getenv("PATH"),
		"HOME=" + dir,
		"GIT_CONFIG_GLOBAL=/dev/null",
		"GIT_CONFIG_SYSTEM=/dev/null",
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.invalid",
		"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.invalid",
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

// TestMainHeadGuardStepComparesTagWithMainHead runs the workflow's own script
// the way the runner does (bash -e) in a clone of a scratch origin, so a tag
// cut from an older main commit or an unmerged branch cannot publish.
func TestMainHeadGuardStepComparesTagWithMainHead(t *testing.T) {
	t.Parallel()

	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash is not available")
	}
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not available")
	}
	raw, err := os.ReadFile(".github/workflows/release.yml")
	if err != nil {
		t.Fatal(err)
	}
	var workflow workflowFile
	if err := yaml.Unmarshal(raw, &workflow); err != nil {
		t.Fatal(err)
	}
	steps := workflow.Jobs["image"].Steps
	idx := stepIndex(steps, func(s workflowStep) bool { return s.Name == mainHeadGuardName })
	if idx < 0 {
		t.Fatalf("release.yml has no step named %q", mainHeadGuardName)
	}
	script := steps[idx].Run

	root := t.TempDir()
	origin := filepath.Join(root, "origin.git")
	work := filepath.Join(root, "work")
	gitIn(t, root, "init", "--bare", "--initial-branch=main", origin)
	gitIn(t, root, "clone", origin, work)
	gitIn(t, work, "checkout", "-b", "main")
	commit := func(file string) string {
		if err := os.WriteFile(filepath.Join(work, file), []byte(file), 0o600); err != nil {
			t.Fatal(err)
		}
		gitIn(t, work, "add", file)
		gitIn(t, work, "commit", "-m", file)
		return gitIn(t, work, "rev-parse", "HEAD")
	}
	older := commit("a")
	head := commit("b")
	gitIn(t, work, "push", "origin", "main")
	gitIn(t, work, "checkout", "-b", "unmerged")
	unmerged := commit("c")

	for name, tc := range map[string]struct {
		commit string
		want   bool
	}{
		"tag at the main head":     {head, true},
		"tag at an older main":     {older, false},
		"tag on an unmerged topic": {unmerged, false},
	} {
		t.Run(name, func(t *testing.T) {
			cmd := exec.Command(bash, "-e", "-c", script)
			cmd.Dir = work
			cmd.Env = []string{
				"PATH=" + os.Getenv("PATH"),
				"HOME=" + root,
				"GIT_CONFIG_GLOBAL=/dev/null",
				"GIT_CONFIG_SYSTEM=/dev/null",
				"TAG_NAME=v0.1.0",
				"TAG_COMMIT=" + tc.commit,
			}
			out, err := cmd.CombinedOutput()
			if got := err == nil; got != tc.want {
				t.Fatalf("passed = %t, want %t\n%s", got, tc.want, out)
			}
			if !tc.want && !strings.Contains(string(out), "not the protected main head "+head) {
				t.Fatalf("failure message does not name the main head %s:\n%s", head, out)
			}
		})
	}
}
