// SPDX-License-Identifier: MIT
// Copyright (c) 2025-2026 eshu-hq

package collector

import (
	"fmt"
	"os"
	"regexp"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

var (
	pinnedAction     = regexp.MustCompile(`^[A-Za-z0-9._-]+/[A-Za-z0-9._/-]+@[0-9a-f]{40}$`)
	secretUsePattern = regexp.MustCompile(`secrets\.([A-Za-z0-9_]+)`)
)

const (
	// tagShapeCheck is the exact tag filter the workflow must run before it
	// publishes anything.
	tagShapeCheck = `grep -Eq '^v[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?$'`
	// signerIdentity is the certificate identity a keyless signature from this
	// workflow carries, derived from the running workflow reference.
	signerIdentity = "https://github.com/${{ github.workflow_ref }}"
	// oidcIssuerFlag pins the GitHub Actions OIDC issuer on the verify step.
	oidcIssuerFlag = `--certificate-oidc-issuer "https://token.actions.githubusercontent.com"`
	// sbomFile is the SBOM the generate and attest steps must agree on.
	sbomFile = "eshu-collector-loki.spdx.json"
)

type workflowStep struct {
	Name string            `yaml:"name"`
	Uses string            `yaml:"uses"`
	Run  string            `yaml:"run"`
	With map[string]any    `yaml:"with"`
	Env  map[string]string `yaml:"env"`
	// If holds the step's `if:` condition; ContinueOnError holds
	// `continue-on-error:`. Both default to the zero value when absent.
	// The govulncheck/gosec reachability tests read them; other tests
	// ignore them.
	If              string `yaml:"if"`
	ContinueOnError bool   `yaml:"continue-on-error"`
}

// needsList accepts the scalar (`needs: changes`) and sequence
// (`needs: [a, b]`) forms GitHub allows.
type needsList []string

func (n *needsList) UnmarshalYAML(value *yaml.Node) error {
	var single string
	if err := value.Decode(&single); err == nil {
		*n = []string{single}
		return nil
	}
	var multi []string
	if err := value.Decode(&multi); err != nil {
		return err
	}
	*n = multi
	return nil
}

type workflowFile struct {
	On          map[string]any    `yaml:"on"`
	Permissions map[string]string `yaml:"permissions"`
	Concurrency map[string]any    `yaml:"concurrency"`
	Jobs        map[string]struct {
		Needs       needsList         `yaml:"needs"`
		Permissions map[string]string `yaml:"permissions"`
		Timeout     int               `yaml:"timeout-minutes"`
		Env         map[string]string `yaml:"env"`
		Steps       []workflowStep    `yaml:"steps"`
		// If holds the job's `if:` condition. The conformance pin test
		// reads it; other tests ignore it.
		If string `yaml:"if"`
	} `yaml:"jobs"`
}

// stepIndex returns the position of the first step for which match is true,
// or -1.
func stepIndex(steps []workflowStep, match func(workflowStep) bool) int {
	for i, step := range steps {
		if match(step) {
			return i
		}
	}
	return -1
}

// contractProblems checks the steps that make a published image trustworthy:
// the tag and manifest checks run before anything is pushed, one platform is
// built, the signature is verified against a pinned identity and issuer, and
// the provenance and SBOM attestations are generated and verified.
func contractProblems(steps []workflowStep) []string {
	var problems []string
	tagIdx := stepIndex(steps, func(s workflowStep) bool { return strings.Contains(s.Run, tagShapeCheck) })
	if tagIdx < 0 {
		problems = append(problems, "no step filters the tag with "+tagShapeCheck)
	}
	versionIdx := stepIndex(steps, func(s workflowStep) bool {
		return strings.Contains(s.Run, "manifest.yaml") && strings.Contains(s.Run, "TAG_NAME") && strings.Contains(s.Run, "exit 1")
	})
	if versionIdx < 0 {
		problems = append(problems, "no step fails unless the tag matches manifest.yaml metadata.version")
	}
	publishIdx := stepIndex(steps, func(s workflowStep) bool { return fmt.Sprint(s.With["push"]) == "true" })
	if publishIdx < 0 {
		problems = append(problems, "no step pushes the image")
	} else {
		if tagIdx >= 0 && tagIdx > publishIdx {
			problems = append(problems, "the tag shape check runs after the image is pushed")
		}
		if versionIdx >= 0 && versionIdx > publishIdx {
			problems = append(problems, "the manifest version check runs after the image is pushed")
		}
		if got := fmt.Sprint(steps[publishIdx].With["platforms"]); got != "linux/amd64" {
			problems = append(problems, fmt.Sprintf("build platforms = %q, want exactly linux/amd64", got))
		}
	}
	verifyIdx := stepIndex(steps, func(s workflowStep) bool { return strings.Contains(s.Run, "cosign verify") })
	if verifyIdx < 0 {
		problems = append(problems, "no step runs cosign verify")
	} else {
		verify := steps[verifyIdx]
		if verify.Env["CERTIFICATE_IDENTITY"] != signerIdentity {
			problems = append(problems, fmt.Sprintf("cosign identity = %q, want %q", verify.Env["CERTIFICATE_IDENTITY"], signerIdentity))
		}
		for _, want := range []string{`--certificate-identity "${CERTIFICATE_IDENTITY}"`, oidcIssuerFlag} {
			if !strings.Contains(verify.Run, want) {
				problems = append(problems, "cosign verify lacks "+want)
			}
		}
		if strings.Contains(verify.Run, "regexp") {
			problems = append(problems, "cosign verify matches the identity by regular expression, want the exact identity")
		}
	}
	attestIdx := stepIndex(steps, func(s workflowStep) bool { return strings.Contains(s.Run, "gh attestation verify") })
	if attestIdx < 0 {
		problems = append(problems, "no step runs gh attestation verify")
	} else {
		run := steps[attestIdx].Run
		if strings.Count(run, "gh attestation verify") < 2 {
			problems = append(problems, "gh attestation verify must run for provenance and for the SBOM")
		}
		for _, want := range []string{`-R "${GITHUB_REPOSITORY}"`, "--predicate-type https://spdx.dev/Document/v2.3"} {
			if !strings.Contains(run, want) {
				problems = append(problems, "gh attestation verify lacks "+want)
			}
		}
	}
	for uses, input := range map[string]string{"anchore/sbom-action@": "output-file", "actions/attest@": "sbom-path"} {
		idx := stepIndex(steps, func(s workflowStep) bool { return strings.HasPrefix(s.Uses, uses) })
		if idx < 0 {
			problems = append(problems, "no step uses "+uses)
		} else if got := fmt.Sprint(steps[idx].With[input]); got != sbomFile {
			problems = append(problems, fmt.Sprintf("%s input %s = %q, want %q", uses, input, got, sbomFile))
		}
	}
	return problems
}

// releaseWorkflowProblems returns every way raw breaks the release workflow's
// safety properties: it must run only for pushed v* tags, hold only the
// permissions publishing and attesting an image need, use no secret but
// GITHUB_TOKEN, pin every action by full commit SHA, deploy nothing, and keep
// the tag, platform, signature, and attestation checks.
func releaseWorkflowProblems(raw []byte) []string {
	var workflow workflowFile
	if err := yaml.Unmarshal(raw, &workflow); err != nil {
		return []string{fmt.Sprintf("release.yml does not parse: %v", err)}
	}
	var problems []string
	push, _ := workflow.On["push"].(map[string]any)
	tags, _ := push["tags"].([]any)
	if len(workflow.On) != 1 || len(push) != 1 || len(tags) != 1 || tags[0] != "v*" {
		problems = append(problems, fmt.Sprintf("triggers = %v, want only push of tags [v*]", workflow.On))
	}
	if len(workflow.Permissions) != 1 || workflow.Permissions["contents"] != "read" {
		problems = append(problems, fmt.Sprintf("workflow permissions = %v, want contents: read only", workflow.Permissions))
	}
	want := map[string]string{"contents": "read", "packages": "write", "id-token": "write", "attestations": "write"}
	for name, job := range workflow.Jobs {
		if len(job.Permissions) != len(want) {
			problems = append(problems, fmt.Sprintf("job %s permissions = %v, want %v", name, job.Permissions, want))
		}
		for key, value := range want {
			if job.Permissions[key] != value {
				problems = append(problems, fmt.Sprintf("job %s permission %s = %q, want %q", name, key, job.Permissions[key], value))
			}
		}
		for _, step := range job.Steps {
			if step.Uses != "" && !pinnedAction.MatchString(step.Uses) {
				problems = append(problems, fmt.Sprintf("job %s step %q uses %q, want a full commit SHA pin", name, step.Name, step.Uses))
			}
			for _, word := range []string{"kubectl", "helm ", "argocd", "terraform", "aws "} {
				if strings.Contains(step.Run, word) {
					problems = append(problems, fmt.Sprintf("job %s step %q runs %q: the release workflow must not deploy", name, step.Name, word))
				}
			}
		}
	}
	image, ok := workflow.Jobs["image"]
	if !ok || len(workflow.Jobs) != 1 {
		problems = append(problems, "want exactly one job, named image")
	}
	problems = append(problems, contractProblems(image.Steps)...)
	for _, match := range secretUsePattern.FindAllStringSubmatch(string(raw), -1) {
		if match[1] != "GITHUB_TOKEN" {
			problems = append(problems, "uses secret "+match[1]+", want only GITHUB_TOKEN")
		}
	}
	if strings.Contains(string(raw), "pull_request") {
		problems = append(problems, "mentions pull_request: nothing may publish on a PR")
	}
	return problems
}

func TestReleaseWorkflowShape(t *testing.T) {
	t.Parallel()

	raw, err := os.ReadFile(".github/workflows/release.yml")
	if err != nil {
		t.Fatal(err)
	}
	if problems := releaseWorkflowProblems(raw); len(problems) > 0 {
		t.Fatalf("release.yml:\n%s", strings.Join(problems, "\n"))
	}
}

// TestReleaseWorkflowGateRejectsViolations feeds the check synthetic
// workflows so it is proven to fail, not only to pass on the checked-in file.
func TestReleaseWorkflowGateRejectsViolations(t *testing.T) {
	t.Parallel()

	raw, err := os.ReadFile(".github/workflows/release.yml")
	if err != nil {
		t.Fatal(err)
	}
	good := string(raw)
	for name, mutate := range map[string]func(string) string{
		"pull request trigger": func(s string) string { return strings.Replace(s, "on:\n  push:", "on:\n  pull_request:\n  push:", 1) },
		"branch trigger": func(s string) string {
			return strings.Replace(s, "    tags:\n", "    branches:\n      - main\n    tags:\n", 1)
		},
		"tag pattern widened": func(s string) string { return strings.Replace(s, `- "v*"`, `- "*"`, 1) },
		"tag-pinned action": func(s string) string {
			return strings.Replace(s, "actions/checkout@93cb6efe18208431cddfb8368fd83d5badbf9bfd", "actions/checkout@v5", 1)
		},
		"extra secret": func(s string) string { return s + "\n# ${{ secrets.DEPLOY_KEY }}\n" },
		"extra permission": func(s string) string {
			return strings.Replace(s, "      attestations: write\n", "      attestations: write\n      actions: write\n", 1)
		},
		"deploy step": func(s string) string {
			return strings.Replace(s, "cosign sign --yes", "kubectl apply -f x && cosign sign --yes", 1)
		},
		"tag regex open": func(s string) string {
			return strings.Replace(s, `'^v[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?$'`, `'^v.*$'`, 1)
		},
		"platform adds arm64": func(s string) string {
			return strings.Replace(s, "platforms: linux/amd64", "platforms: linux/amd64,linux/arm64", 1)
		},
		"verify identity by regexp": func(s string) string {
			return strings.Replace(s, `--certificate-identity "${CERTIFICATE_IDENTITY}"`, `--certificate-identity-regexp '.*'`, 1)
		},
		"verify identity from a literal": func(s string) string {
			return strings.Replace(s, "CERTIFICATE_IDENTITY: https://github.com/${{ github.workflow_ref }}", "CERTIFICATE_IDENTITY: https://github.com/eshu-hq", 1)
		},
		"verify issuer dropped": func(s string) string {
			return strings.Replace(s, `--certificate-oidc-issuer "https://token.actions.githubusercontent.com"`, `--certificate-oidc-issuer "https://example.invalid"`, 1)
		},
		"sbom path dropped": func(s string) string {
			return strings.Replace(s, "sbom-path: "+sbomFile, "sbom-path: other.spdx.json", 1)
		},
		"sbom attestation verify dropped": func(s string) string {
			return strings.Replace(s, "--predicate-type https://spdx.dev/Document/v2.3", "", 1)
		},
		"provenance verify dropped": func(s string) string {
			return strings.Replace(s, `gh attestation verify "${OCI_IMAGE_REF}" \
            -R "${GITHUB_REPOSITORY}"
          gh attestation verify`, "gh attestation verify", 1)
		},
		"version check dropped": func(s string) string {
			return strings.ReplaceAll(s, "manifest.yaml", "other.yaml")
		},
		"version check runs after the push": func(s string) string {
			return strings.ReplaceAll(s, "manifest.yaml", "other.yaml") +
				"      - name: Late version check\n        run: |\n          test \"${TAG_NAME}\" = \"v$(cat manifest.yaml)\" || exit 1\n"
		},
	} {
		mutated := mutate(good)
		if mutated == good {
			t.Fatalf("%s: the mutation did not change the workflow", name)
		}
		if len(releaseWorkflowProblems([]byte(mutated))) == 0 {
			t.Errorf("%s: the gate accepted a violating workflow", name)
		}
	}
}
