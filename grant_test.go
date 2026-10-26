// SPDX-License-Identifier: MIT
// Copyright (c) 2025-2026 eshu-hq

package collector

import (
	"encoding/json"
	"os"
	"slices"
	"sort"
	"testing"
	"time"

	"github.com/eshu-hq/eshu-collector-loki/grant"
)

// producerGrant is the shape `eshu component grant` records. The core issues
// the grant; this repository only documents the grants it needs.
type producerGrant struct {
	ProducerID     string    `json:"producer_id"`
	Version        string    `json:"version"`
	Kind           string    `json:"kind"`
	SchemaVersions []string  `json:"schema_versions"`
	Scope          string    `json:"scope"`
	ExpiresAt      time.Time `json:"expires_at"`
	Revoked        bool      `json:"revoked"`
}

// TestGrantExampleCoversEveryCoreKindAndNothingElse proves grant.example.json
// is a superset of the producer grants the manifest needs.
//
// The manifest owns only the registry-scoped unique kind
// (observability.observed_log_signal) since the eshu-hq/eshu#9202 arbiter
// verdict: source_instance, observed_rule, and coverage_warning stay
// grant-authorized at the emission edge but are not manifest-owned, so a
// co-installed collector (tempo) no longer collides at install. The grants
// must therefore cover every manifest kind, while extra grant-covered shared
// kinds are allowed by design. Every grant kind must still sit inside
// grant.CoveredKinds: nothing outside the emission-edge coverage is allowed.
// A grant's scope is a collector kind declared by the manifest, not a scope
// ID: the host matches it against spec.collectorKinds. The observability
// kinds are shared with the grafana, prometheus-mimir, and tempo collectors
// for their own providers; the producer identity plus scope is what separates
// authorities, not the kind names (see the grant package).
func TestGrantExampleCoversEveryCoreKindAndNothingElse(t *testing.T) {
	t.Parallel()

	raw, err := os.ReadFile("grant.example.json")
	if err != nil {
		t.Fatalf("os.ReadFile(grant.example.json) error = %v", err)
	}
	var grants []producerGrant
	if err := json.Unmarshal(raw, &grants); err != nil {
		t.Fatalf("json.Unmarshal(grant.example.json) error = %v", err)
	}
	manifest := loadManifest(t)
	manifestVersions := make(map[string][]string, len(manifest.Spec.EmittedFacts))
	for _, family := range manifest.Spec.EmittedFacts {
		manifestVersions[family.Kind] = family.SchemaVersions
	}
	covered := grant.CoveredKinds()
	var kinds []string
	for _, example := range grants {
		kinds = append(kinds, example.Kind)
		if example.ProducerID != manifest.Metadata.ID || example.Version != manifest.Metadata.Version {
			t.Errorf("grant %s names %s@%s, want %s@%s", example.Kind, example.ProducerID, example.Version, manifest.Metadata.ID, manifest.Metadata.Version)
		}
		if !slices.Contains(manifest.Spec.CollectorKinds, example.Scope) {
			t.Errorf("grant %s scope %q is not a collector kind in the manifest %v", example.Kind, example.Scope, manifest.Spec.CollectorKinds)
		}
		if example.Revoked || example.ExpiresAt.IsZero() {
			t.Errorf("grant %s is revoked or has no expiry: %+v", example.Kind, example)
		}
		if !slices.Contains(covered, example.Kind) {
			t.Errorf("grant %s is outside the emission-edge coverage %v", example.Kind, covered)
		}
		if declared, owned := manifestVersions[example.Kind]; owned {
			if !slices.Equal(example.SchemaVersions, declared) {
				t.Errorf("grant %s versions %v, want the manifest's %v", example.Kind, example.SchemaVersions, declared)
			}
		} else if !slices.Equal(example.SchemaVersions, []string{grant.SchemaVersionV1}) {
			t.Errorf("grant %s versions %v, want the locked grant coverage [%s]", example.Kind, example.SchemaVersions, grant.SchemaVersionV1)
		}
	}
	for kind := range manifestVersions {
		if !slices.Contains(kinds, kind) {
			t.Errorf("manifest kind %s has no grant; grants must cover every manifest kind", kind)
		}
	}
	sort.Strings(kinds)
	sortedCovered := append([]string(nil), covered...)
	sort.Strings(sortedCovered)
	if !slices.Equal(kinds, sortedCovered) {
		t.Errorf("grant kinds %v, want the emission-edge coverage %v", kinds, sortedCovered)
	}
}
