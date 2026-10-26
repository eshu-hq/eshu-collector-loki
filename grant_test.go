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
// is exactly the set of producer grants the manifest needs.
//
// Every kind this collector emits is core-owned, so the host refuses to
// activate the component without a live grant for each one. A grant's scope is
// a collector kind declared by the manifest, not a scope ID: the host matches
// it against spec.collectorKinds. The observability kinds are shared with the
// grafana, prometheus-mimir, and tempo collectors for their own providers; the
// producer identity plus scope is what separates authorities, not the kind
// names (see the grant package).
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
	var kinds []string
	for _, grant := range grants {
		kinds = append(kinds, grant.Kind)
		if grant.ProducerID != manifest.Metadata.ID || grant.Version != manifest.Metadata.Version {
			t.Errorf("grant %s names %s@%s, want %s@%s", grant.Kind, grant.ProducerID, grant.Version, manifest.Metadata.ID, manifest.Metadata.Version)
		}
		if !slices.Contains(manifest.Spec.CollectorKinds, grant.Scope) {
			t.Errorf("grant %s scope %q is not a collector kind in the manifest %v", grant.Kind, grant.Scope, manifest.Spec.CollectorKinds)
		}
		if grant.Revoked || grant.ExpiresAt.IsZero() {
			t.Errorf("grant %s is revoked or has no expiry: %+v", grant.Kind, grant)
		}
		var declared []string
		for _, family := range manifest.Spec.EmittedFacts {
			if family.Kind == grant.Kind {
				declared = family.SchemaVersions
			}
		}
		if !slices.Equal(grant.SchemaVersions, declared) {
			t.Errorf("grant %s versions %v, want the manifest's %v", grant.Kind, grant.SchemaVersions, declared)
		}
	}
	sort.Strings(kinds)
	want := append([]string(nil), FactKinds()...)
	sort.Strings(want)
	if !slices.Equal(kinds, want) {
		t.Errorf("grant kinds %v, want exactly the emitted kinds %v", kinds, want)
	}
}
