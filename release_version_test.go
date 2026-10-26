// SPDX-License-Identifier: MIT
// Copyright (c) 2025-2026 eshu-hq

package collector

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"gopkg.in/yaml.v3"
)

// versionStepRun returns the script of the release workflow step that compares
// the pushed tag with the manifest version.
func versionStepRun(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile(".github/workflows/release.yml")
	if err != nil {
		t.Fatal(err)
	}
	var workflow workflowFile
	if err := yaml.Unmarshal(raw, &workflow); err != nil {
		t.Fatal(err)
	}
	steps := workflow.Jobs["image"].Steps
	idx := stepIndex(steps, func(s workflowStep) bool { return s.Name == "Require the tag to match the manifest version" })
	if idx < 0 {
		t.Fatal("release.yml has no step named \"Require the tag to match the manifest version\"")
	}
	return steps[idx].Run
}

// TestReleaseVersionStepComparesTagWithManifest runs the workflow's own script
// the way the runner does (bash -e) against manifests and tags, so a wrong
// version cannot publish a signed image whose manifest says something else.
func TestReleaseVersionStepComparesTagWithManifest(t *testing.T) {
	t.Parallel()

	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash is not available")
	}
	script := versionStepRun(t)
	manifest := func(version string) string {
		return "apiVersion: eshu.dev/v1alpha1\nmetadata:\n  id: dev.eshu.collector.loki\n  version: " + version + "\nspec:\n  version: 9.9.9\n"
	}
	for name, tc := range map[string]struct {
		manifest string
		tag      string
		want     bool
	}{
		"exact match":                      {manifest("0.1.0"), "v0.1.0", true},
		"pre-release of the manifest":      {manifest("0.1.0"), "v0.1.0-rc.1", true},
		"quoted manifest version":          {manifest(`"0.1.0"`), "v0.1.0", true},
		"manifest version with a comment":  {manifest("0.1.0 # next"), "v0.1.0", true},
		"manifest with CRLF line endings":  {"metadata:\r\n  version: 0.1.0\r\n", "v0.1.0", true},
		"pre-release manifest, same tag":   {manifest("0.1.0-rc.1"), "v0.1.0-rc.1", true},
		"newer tag than the manifest":      {manifest("0.1.0"), "v0.2.0", false},
		"older tag than the manifest":      {manifest("0.2.0"), "v0.1.0", false},
		"patch differs":                    {manifest("0.1.0"), "v0.1.1", false},
		"longer patch number":              {manifest("0.1.0"), "v0.1.00", false},
		"pre-release tag of a newer patch": {manifest("0.1.0"), "v0.1.1-rc.1", false},
		"pre-release manifest, other tag":  {manifest("0.1.0-rc.1"), "v0.1.0-rc.2", false},
		"pre-release manifest, final tag":  {manifest("0.1.0-rc.1"), "v0.1.0", false},
		"pre-release manifest, longer tag": {manifest("0.1.0-rc.1"), "v0.1.0-rc.1-x", false},
		"version only under spec":          {"metadata:\n  id: x\nspec:\n  version: 0.1.0\n", "v0.1.0", false},
		"unparsable manifest version":      {manifest("latest"), "vlatest", false},
		"no manifest version":              {"metadata:\n  id: x\n", "v", false},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, "manifest.yaml"), []byte(tc.manifest), 0o600); err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command(bash, "-e", "-c", script)
			cmd.Dir = dir
			cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "TAG_NAME=" + tc.tag}
			out, err := cmd.CombinedOutput()
			if got := err == nil; got != tc.want {
				t.Fatalf("tag %q against manifest %q: passed = %t, want %t\n%s", tc.tag, tc.manifest, got, tc.want, out)
			}
		})
	}
}

// TestReleaseVersionStepAcceptsTheCheckedInManifest ties the script to the
// real manifest: tagging the version the repository declares must pass.
func TestReleaseVersionStepAcceptsTheCheckedInManifest(t *testing.T) {
	t.Parallel()

	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash is not available")
	}
	raw, err := os.ReadFile("manifest.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var manifest struct {
		Metadata struct {
			Version string `yaml:"version"`
		} `yaml:"metadata"`
	}
	if err := yaml.Unmarshal(raw, &manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.Metadata.Version == "" {
		t.Fatal("manifest.yaml has no metadata.version")
	}
	cmd := exec.Command(bash, "-e", "-c", versionStepRun(t))
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "TAG_NAME=v" + manifest.Metadata.Version}
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("tag v%s against the checked-in manifest: %v\n%s", manifest.Metadata.Version, err, out)
	}
}
