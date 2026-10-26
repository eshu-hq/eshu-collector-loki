// SPDX-License-Identifier: MIT
// Copyright (c) 2025-2026 eshu-hq

package loki

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Golden vectors cut from the in-tree constructors at the merged prep
// (eshu-hq/eshu 55d35e04bf, #9157) by running the in-tree parity harness
// against testdata/8860-parity-loki.json. These pin the extracted producer
// to byte-identical emission behavior: any change to stable keys,
// fingerprinting, or payload bytes breaks this test. Regenerate only from
// the in-tree constructors, never from this package's own output.
const (
	goldenTenantFingerprint = "sha256:3a04c5d29101b4160a401683829f6e1dee38501d745caedd9ca715f6def13c70"

	goldenKeySourceInstanceProd = "observability.source_instance:loki:generation-7:4ff20912c13dd3c73a82c26226a05a0fc15886de73c41e162392dd9d5ab5489a"
	goldenKeyLabelSetProd       = "observability.observed_log_signal:loki:generation-7:c338cac3874e4effd2392ad6d1dd9bae819a1be2ee5fdfb00fb86cc5eb4eeedf"
	goldenKeySeriesProd         = "observability.observed_log_signal:loki:generation-7:9a5ce660b859ad80a519f2911fb4fc6d6bf3f1091ffaad85ad247d26c8613a47"
	goldenKeyRuleProd           = "observability.observed_rule:loki:generation-7:b710f63abe81e2caf7efbbfb37b6fc46f952463ca687b4159d23a8cf3e91f071"
	goldenKeyHighCardinality    = "observability.coverage_warning:loki:generation-7:d0d91bce5427cce92903fcaadf9d4414198452a711d39f4ed4bba93744dfa96b"
	goldenKeyPartialRule        = "observability.coverage_warning:loki:generation-7:8f86ce2204ff2f19e57ab83d77b30908046911d004af8dc1a68051922c3be992"

	goldenKeySourceInstanceEdge = "observability.source_instance:loki:generation-8:9d78d0f2684592efec38a4e6744998310ba60960e295fa9077d020b5e45d2815"
	goldenKeyLabelSetEdge       = "observability.observed_log_signal:loki:generation-8:78b0485b1ae30a5d8d3cddf32acf650357c84b9249fa1bc7bb1eaf41dc982b01"
	goldenKeyTruncatedEdge      = "observability.coverage_warning:loki:generation-8:139614a380c54f85da29bee9b6e73f10e67ede003200810990114a425b3461bc"
)

const goldenLabelSetPayload = `{"collector_instance_id":"collector-loki-parity","declared_match_state":"not_compared","freshness_state":"current","generation_id":"loki:generation-7","label_keys":["app","namespace"],"label_value_counts":{"app":12},"label_value_hashes":{"app":["sha256:recorded"]},"last_seen_at":"2026-10-01T12:00:00Z","observed_at":"2026-10-01T12:00:00Z","outcome":"observed","provenance":{"provider":"loki","scope_id":"loki:tenant:prod","source_instance_id":"loki-prod"},"provider":"loki","provider_object_uid":"labelset:app","redaction_state":"applied","redaction_version":"observability-live-loki-v1","resource_class":"log_signal","scope_id":"loki:tenant:prod","signal_kind":"label_set","source_class":"observed","source_instance_id":"loki-prod","source_kind":"loki"}`

// parityObservedAt is the fixed generation timestamp recorded in the
// fixture scopes. Stub results carry it explicitly, so every
// identity-bearing field is deterministic across runs.
var parityObservedAt = time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)

// paritySignal mirrors one recorded log-signal observation.
type paritySignal struct {
	ProviderObjectID string              `json:"provider_object_id"`
	SignalKind       string              `json:"signal_kind"`
	LabelKeys        []string            `json:"label_keys"`
	LabelValueCounts map[string]int      `json:"label_value_counts"`
	LabelValueHashes map[string][]string `json:"label_value_hashes"`
	SeriesFinger     string              `json:"series_fingerprint"`
	FreshnessState   string              `json:"freshness_state"`
	Outcome          string              `json:"outcome"`
}

// parityRule mirrors one recorded ruler-rule observation.
type parityRule struct {
	ProviderObjectID string   `json:"provider_object_id"`
	Namespace        string   `json:"namespace"`
	GroupName        string   `json:"group_name"`
	RuleName         string   `json:"rule_name"`
	RuleType         string   `json:"rule_type"`
	Query            string   `json:"query"`
	LabelKeys        []string `json:"label_keys"`
	AnnotationKeys   []string `json:"annotation_keys"`
	FreshnessState   string   `json:"freshness_state"`
	Outcome          string   `json:"outcome"`
}

// parityWarning mirrors one recorded coverage warning.
type parityWarning struct {
	ResourceClass string `json:"resource_class"`
	ResourceID    string `json:"resource_id"`
	Reason        string `json:"reason"`
}

// parityScope binds one claim to its recorded fixture scope.
type parityScope struct {
	Name             string          `json:"name"`
	ScopeID          string          `json:"scope_id"`
	InstanceID       string          `json:"instance_id"`
	GenerationID     string          `json:"generation_id"`
	FencingToken     int64           `json:"fencing_token"`
	TenantConfigured bool            `json:"tenant_configured"`
	SourceInstanceID string          `json:"source_instance_id"`
	Signals          []paritySignal  `json:"signals"`
	Rules            []parityRule    `json:"rules"`
	Warnings         []parityWarning `json:"warnings"`
	ExpectedKinds    map[string]int  `json:"expected_kinds"`
}

// parityPack is the recorded cutover comparison input, byte-identical to
// the in-tree testdata/8860-parity-loki.json (#8860, #4047 gate input).
type parityPack struct {
	Issue         int           `json:"issue"`
	CollectorKind string        `json:"collector_kind"`
	SchemaVersion string        `json:"schema_version"`
	Scopes        []parityScope `json:"scopes"`
}

func loadParityPack(t *testing.T) parityPack {
	t.Helper()

	raw, err := os.ReadFile(filepath.Join("testdata", "8860-parity-loki.json"))
	if err != nil {
		t.Fatalf("ReadFile(8860-parity-loki.json) error = %v, want nil", err)
	}
	var pack parityPack
	if err := json.Unmarshal(raw, &pack); err != nil {
		t.Fatalf("Unmarshal parity pack error = %v, want nil", err)
	}
	if pack.CollectorKind != CollectorKind {
		t.Fatalf("pack collector_kind = %q, want %q", pack.CollectorKind, CollectorKind)
	}
	if pack.SchemaVersion != SchemaVersionV1 {
		t.Fatalf("pack schema_version = %q, want %q", pack.SchemaVersion, SchemaVersionV1)
	}
	if len(pack.Scopes) == 0 {
		t.Fatal("parity pack holds no scopes")
	}
	return pack
}

func parityCollectionResult(ps parityScope) CollectionResult {
	result := CollectionResult{
		Source: SourceInstance{
			Provider:         ProviderLoki,
			SourceInstanceID: ps.SourceInstanceID,
		},
		ObservedAt: parityObservedAt,
	}
	if ps.TenantConfigured {
		result.Source.TenantPresent = true
		result.Source.TenantFingerprint = fingerprint("tenant-prod")
		result.Source.TenantRedacted = true
	}
	for _, s := range ps.Signals {
		result.Signals = append(result.Signals, LogSignal{
			ProviderObjectID:  s.ProviderObjectID,
			SignalKind:        s.SignalKind,
			LabelKeys:         s.LabelKeys,
			LabelValueCounts:  s.LabelValueCounts,
			LabelValueHashes:  s.LabelValueHashes,
			SeriesFingerprint: s.SeriesFinger,
			LastSeenAt:        parityObservedAt,
			FreshnessState:    s.FreshnessState,
			Outcome:           s.Outcome,
		})
	}
	for _, r := range ps.Rules {
		result.Rules = append(result.Rules, Rule{
			ProviderObjectID: r.ProviderObjectID,
			Namespace:        r.Namespace,
			GroupName:        r.GroupName,
			RuleName:         r.RuleName,
			RuleType:         r.RuleType,
			Query:            r.Query,
			QueryRedacted:    r.Query != "",
			LabelKeys:        r.LabelKeys,
			AnnotationKeys:   r.AnnotationKeys,
			FreshnessState:   r.FreshnessState,
			Outcome:          r.Outcome,
		})
	}
	for _, w := range ps.Warnings {
		result.Warnings = append(result.Warnings, Warning(w))
	}
	return result
}

func parityClaimedSource(t *testing.T, ps parityScope, result CollectionResult) *ClaimedSource {
	t.Helper()

	client := &stubEvidenceClient{result: result}
	source, err := NewClaimedSource(SourceConfig{
		CollectorInstanceID: "collector-loki-parity",
		Targets: []TargetConfig{{
			ScopeID:       ps.ScopeID,
			InstanceID:    ps.InstanceID,
			BaseURL:       "https://loki.example.test",
			ResourceLimit: 100,
			Enabled:       true,
		}},
		ClientFactory: func(TargetConfig) (EvidenceClient, error) { return client, nil },
		Now:           func() time.Time { return parityObservedAt },
	})
	if err != nil {
		t.Fatalf("NewClaimedSource(%s) error = %v, want nil", ps.Name, err)
	}
	return source
}

func parityClaim(ps parityScope) testClaimSpec {
	return testClaimSpec{
		ScopeID:      ps.ScopeID,
		GenerationID: ps.GenerationID,
		FencingToken: fmt.Sprint(ps.FencingToken),
	}
}

func collectParityResult(t *testing.T, ps parityScope, source *ClaimedSource) testResult {
	t.Helper()

	got, err := source.Collect(context.Background(), parityTestClaim(t, parityClaim(ps)), testGrant())
	if err != nil {
		t.Fatalf("Collect(%s) error = %v, want nil", ps.Name, err)
	}
	if len(got.Facts) == 0 {
		t.Fatalf("Collect(%s) emitted no facts", ps.Name)
	}
	return testResult{result: got}
}

func TestExtractionPrepGoldenVectors(t *testing.T) {
	t.Parallel()

	pack := loadParityPack(t)
	wantKeys := map[string][]string{
		"tenant-prod": {
			goldenKeySourceInstanceProd,
			goldenKeyLabelSetProd,
			goldenKeySeriesProd,
			goldenKeyRuleProd,
			goldenKeyHighCardinality,
			goldenKeyPartialRule,
		},
		"edge-noauth": {
			goldenKeySourceInstanceEdge,
			goldenKeyLabelSetEdge,
			goldenKeyTruncatedEdge,
		},
	}
	if got := fingerprint("tenant-prod"); got != goldenTenantFingerprint {
		t.Errorf("fingerprint(tenant) = %q, want in-tree golden %q", got, goldenTenantFingerprint)
	}
	for _, ps := range pack.Scopes {
		res := collectParityResult(t, ps, parityClaimedSource(t, ps, parityCollectionResult(ps)))
		if len(res.facts()) != len(wantKeys[ps.Name]) {
			t.Fatalf("scope %s facts = %d, want %d", ps.Name, len(res.facts()), len(wantKeys[ps.Name]))
		}
		for i, want := range wantKeys[ps.Name] {
			if res.facts()[i].StableKey != want {
				t.Errorf("scope %s fact[%d] StableKey = %q, want in-tree golden %q", ps.Name, i, res.facts()[i].StableKey, want)
			}
		}
		counts := res.countByKind()
		for kind, want := range ps.ExpectedKinds {
			if got := counts[kind]; got != want {
				t.Errorf("scope %s kind %s count = %d, want %d", ps.Name, kind, got, want)
			}
			delete(counts, kind)
		}
		for kind, got := range counts {
			t.Errorf("scope %s emitted unexpected kind %s x%d", ps.Name, kind, got)
		}
		if err := res.validate(); err != nil {
			t.Errorf("scope %s ValidateResult() error = %v, want validator-clean output", ps.Name, err)
		}
	}
}

func TestExtractionPrepGoldenLabelSetPayload(t *testing.T) {
	t.Parallel()

	pack := loadParityPack(t)
	res := collectParityResult(t, pack.Scopes[0], parityClaimedSource(t, pack.Scopes[0], parityCollectionResult(pack.Scopes[0])))
	var found bool
	for _, fact := range res.facts() {
		uid, _ := fact.Payload["provider_object_uid"].(string)
		if fact.Kind != KindObservedLogSignal || uid != "labelset:app" {
			continue
		}
		found = true
		raw, err := json.Marshal(fact.Payload)
		if err != nil {
			t.Fatalf("json.Marshal(label_set payload) error = %v", err)
		}
		if string(raw) != goldenLabelSetPayload {
			t.Errorf("label_set payload bytes differ from in-tree golden:\n got: %s\nwant: %s", string(raw), goldenLabelSetPayload)
		}
	}
	if !found {
		t.Fatal("no observed_log_signal fact for labelset:app")
	}
}
