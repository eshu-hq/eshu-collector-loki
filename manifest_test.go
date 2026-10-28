// SPDX-License-Identifier: MIT
// Copyright (c) 2025-2026 eshu-hq

package collector

import (
	"regexp"
	"strings"
	"testing"
)

var digestPattern = regexp.MustCompile(`@sha256:[A-Fa-f0-9]{64}$`)

// placeholderDigest is the artifact digest the repository manifest carries
// permanently. manifest.yaml is go:embed'ed into the binary, so a digest
// written into it would belong to an older build than the image that embeds
// it. The deployed digest is stamped at deploy time by `collector seed
// --image-ref` instead (README, "Image and seeding").
const placeholderDigest = "sha256:0000000000000000000000000000000000000000000000000000000000000000"

// isPlaceholderImage reports whether image names the placeholder digest.
func isPlaceholderImage(image string) bool {
	return strings.HasSuffix(image, "@"+placeholderDigest)
}

// TestPlaceholderImageGate pins the gate on both sides: the placeholder passes
// and a real-looking digest fails.
func TestPlaceholderImageGate(t *testing.T) {
	t.Parallel()

	const repo = "ghcr.io/eshu-hq/eshu-collector-loki@"
	if !isPlaceholderImage(repo + placeholderDigest) {
		t.Fatal("the placeholder digest must pass the gate")
	}
	if isPlaceholderImage(repo + "sha256:" + strings.Repeat("a", 64)) {
		t.Fatal("a real-looking digest must fail the gate")
	}
}

// TestManifestReleaseProvenance proves releases are reproducible and the
// component manifest is valid: every artifact is digest-pinned, the manifest
// carries the supported core range and wire protocol, and every emitted fact
// family is versioned with declared confidence.
func TestManifestReleaseProvenance(t *testing.T) {
	t.Parallel()

	manifest := loadManifest(t)
	if strings.TrimSpace(manifest.APIVersion) == "" || strings.TrimSpace(manifest.Kind) == "" {
		t.Fatal("manifest missing apiVersion/kind")
	}
	if strings.TrimSpace(manifest.Metadata.ID) == "" || strings.TrimSpace(manifest.Metadata.Version) == "" {
		t.Fatal("manifest missing metadata id/version")
	}
	if manifest.Metadata.ID != ComponentID {
		t.Fatalf("manifest id = %q, want %q", manifest.Metadata.ID, ComponentID)
	}
	if len(manifest.Spec.Artifacts) == 0 {
		t.Fatal("manifest declares no artifacts")
	}
	for _, artifact := range manifest.Spec.Artifacts {
		if !digestPattern.MatchString(artifact.Image) {
			t.Fatalf("artifact image %q is not digest-pinned", artifact.Image)
		}
		if strings.Contains(artifact.Image, ":latest") {
			t.Fatalf("artifact image %q uses a mutable tag", artifact.Image)
		}
		// The artifact must live in this component's own GHCR repository.
		if !strings.HasPrefix(artifact.Image, "ghcr.io/eshu-hq/eshu-collector-loki@sha256:") {
			t.Fatalf("artifact image %q is not the eshu-collector-loki GHCR image", artifact.Image)
		}
		// The repository manifest names the placeholder, never a real digest:
		// the manifest is embedded in the image, so a committed digest could
		// only describe an older build. Deployments stamp the digest they
		// pulled with `collector seed --image-ref REF@sha256:<digest>`.
		if !isPlaceholderImage(artifact.Image) {
			t.Fatalf("artifact image %q is not the placeholder digest: do not commit a real digest "+
				"(the manifest is embedded in the image); stamp the deployed one with collector seed --image-ref", artifact.Image)
		}
	}
	if len(manifest.Spec.EmittedFacts) == 0 {
		t.Fatal("manifest declares no emitted facts")
	}
	for _, family := range manifest.Spec.EmittedFacts {
		if strings.TrimSpace(family.Kind) == "" {
			t.Fatal("emitted fact kind empty")
		}
		if len(family.SchemaVersions) == 0 {
			t.Fatalf("fact kind %q has no schema versions", family.Kind)
		}
		if len(family.SourceConfidence) == 0 {
			t.Fatalf("fact kind %q has no source confidence", family.Kind)
		}
	}
	// These kinds are core-owned and feed real reducers, so the manifest must
	// not claim the source_evidence_only:no_graph_truth phase, which would say
	// the opposite. No phase is declared; the core grants decide consumption.
	if phases := manifest.Spec.ConsumerContracts.Reducer.Phases; len(phases) != 0 {
		t.Fatalf("manifest declares reducer phases %v, want none", phases)
	}
	if strings.TrimSpace(manifest.Spec.Telemetry.MetricsPrefix) == "" {
		t.Fatal("manifest missing telemetry metrics prefix")
	}
}
