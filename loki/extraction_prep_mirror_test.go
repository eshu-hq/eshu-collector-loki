// SPDX-License-Identifier: MIT
// Copyright (c) 2025-2026 eshu-hq

package loki

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// Extraction census mirror (#8860, producer path): the contracts the
// extracted collector preserves — admission at schema 1.0.0, stable keys,
// generation binding, label-value hash flow-through, and credential
// redaction — proved against the extracted constructors, including hostile
// inputs.
//
// Ported from go/internal/collector/observability/loki/
// source_parity_8860_test.go in eshu-hq/eshu. Three in-tree assertions
// have no emission-half counterpart and are intentionally not ported:
// FactID (assigned by the core host at admission), FencingToken numerics
// (enforced by the host from claim.fencing_token), and the workflow
// WorkItem shape (replaced by the collector-sdk Claim). Provider identity
// is asserted through SourceRef.SourceSystem instead of CollectorKind,
// which the collector-sdk Fact wire shape carries on the claim.

func extractionPrepContext() EnvelopeContext {
	return EnvelopeContext{
		ScopeID:             "loki:tenant:prod",
		GenerationID:        "loki:generation-7",
		CollectorInstanceID: "collector-loki",
		ObservedAt:          time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC),
		SourceURI:           "https://loki.example.test",
		SourceInstanceID:    "loki-prod",
	}
}

func extractionPrepSignal() LogSignal {
	return LogSignal{
		ProviderObjectID: "labelset:app",
		SignalKind:       SignalKindLabelSet,
		LabelKeys:        []string{"app", "namespace"},
		LabelValueCounts: map[string]int{"app": 12},
		LabelValueHashes: map[string][]string{"app": {"sha256:recorded"}},
		FreshnessState:   FreshnessCurrent,
		Outcome:          OutcomeObserved,
	}
}

func extractionPrepSeries() LogSignal {
	return LogSignal{
		ProviderObjectID:  "series:app:checkout",
		SignalKind:        SignalKindSeries,
		LabelKeys:         []string{"app"},
		SeriesFingerprint: "sha256:recorded-series",
		FreshnessState:    FreshnessCurrent,
		Outcome:           OutcomeObserved,
	}
}

func extractionPrepRule() Rule {
	return Rule{
		ProviderObjectID: "prod/checkout.rules:HighLogErrors",
		Namespace:        "prod",
		GroupName:        "checkout.rules",
		RuleName:         "HighLogErrors",
		RuleType:         RuleTypeAlerting,
		Query:            `sum(rate({app="checkout"} |= "payment failed" [5m])) > 0`,
		QueryRedacted:    true,
		LabelKeys:        []string{"severity"},
		AnnotationKeys:   []string{"summary"},
		FreshnessState:   FreshnessCurrent,
		Outcome:          OutcomeObserved,
	}
}

func extractionPrepFacts(t *testing.T, ctx EnvelopeContext) []emittedFact {
	t.Helper()

	source, err := NewSourceInstanceEnvelope(ctx, SourceInstance{
		Provider:          ProviderLoki,
		SourceInstanceID:  ctx.SourceInstanceID,
		TenantPresent:     true,
		TenantFingerprint: fingerprint("tenant-prod"),
		TenantRedacted:    true,
	}, CollectionStats{Signals: 2, Rules: 1, Warnings: 1})
	if err != nil {
		t.Fatalf("NewSourceInstanceEnvelope() error = %v, want nil", err)
	}
	signal, err := NewObservedLogSignalEnvelope(ctx, extractionPrepSignal())
	if err != nil {
		t.Fatalf("NewObservedLogSignalEnvelope() error = %v, want nil", err)
	}
	series, err := NewObservedLogSignalEnvelope(ctx, extractionPrepSeries())
	if err != nil {
		t.Fatalf("NewObservedLogSignalEnvelope(series) error = %v, want nil", err)
	}
	rule, err := NewObservedRuleEnvelope(ctx, extractionPrepRule())
	if err != nil {
		t.Fatalf("NewObservedRuleEnvelope() error = %v, want nil", err)
	}
	warning, err := NewCoverageWarningEnvelope(ctx, Warning{ResourceClass: ResourceClassLogSignal, ResourceID: "label:trace_id", Reason: WarningHighCardinality})
	if err != nil {
		t.Fatalf("NewCoverageWarningEnvelope() error = %v, want nil", err)
	}
	return []emittedFact{source, signal, series, rule, warning}
}

func TestExtractionPrepAllKindsAdmitAtV1(t *testing.T) {
	t.Parallel()

	kinds := []string{
		KindSourceInstance,
		KindObservedLogSignal,
		KindObservedRule,
		KindCoverageWarning,
	}
	for _, kind := range kinds {
		version, ok := SchemaVersionForKind(kind)
		if !ok || version != SchemaVersionV1 {
			t.Errorf("SchemaVersionForKind(%q) = %q, %v; want 1.0.0, true", kind, version, ok)
		}
		if err := CheckSchemaVersion(kind, SchemaVersionV1); err != nil {
			t.Errorf("CheckSchemaVersion(%q, 1.0.0) error = %v, want nil", kind, err)
		}
	}
	for _, fact := range extractionPrepFacts(t, extractionPrepContext()) {
		if fact.SchemaVersion != SchemaVersionV1 {
			t.Errorf("fact %q SchemaVersion = %q, want 1.0.0", fact.Kind, fact.SchemaVersion)
		}
		if fact.SourceRef.SourceSystem != CollectorKind {
			t.Errorf("fact %q SourceSystem = %q, want %q", fact.Kind, fact.SourceRef.SourceSystem, CollectorKind)
		}
		if string(fact.SourceConfidence) != string(reportedConfidence) {
			t.Errorf("fact %q SourceConfidence = %q, want reported", fact.Kind, fact.SourceConfidence)
		}
		if fact.SourceRef.FactKey != fact.StableKey {
			t.Errorf("fact %q source_ref.fact_key does not match stable_key", fact.Kind)
		}
	}
	if err := CheckSchemaVersion(KindObservedLogSignal, "9.9.9"); err == nil {
		t.Error("CheckSchemaVersion(observed_log_signal, 9.9.9) = nil, want incompatible-schema rejection")
	}
}

func TestExtractionPrepDeterminism(t *testing.T) {
	t.Parallel()

	first := extractionPrepFacts(t, extractionPrepContext())
	second := extractionPrepFacts(t, extractionPrepContext())
	if len(first) != len(second) {
		t.Fatalf("len(first) = %d, len(second) = %d, want equal", len(first), len(second))
	}
	for i := range first {
		if first[i].StableKey != second[i].StableKey {
			t.Errorf("fact[%d] StableKey not deterministic: %q vs %q", i, first[i].StableKey, second[i].StableKey)
		}
	}
}

func TestExtractionPrepGenerationRebind(t *testing.T) {
	t.Parallel()

	first := extractionPrepFacts(t, extractionPrepContext())
	rebound := extractionPrepContext()
	rebound.GenerationID = "loki:generation-8"
	second := extractionPrepFacts(t, rebound)
	if len(first) != len(second) {
		t.Fatalf("len(first) = %d, len(second) = %d, want equal", len(first), len(second))
	}
	for i := range first {
		// Stable keys bind the generation: a rebind must change identity,
		// never silently reuse the prior generation's key.
		if first[i].StableKey == second[i].StableKey {
			t.Errorf("fact[%d] StableKey identical across generations; generation must rebind identity", i)
		}
		if second[i].SourceRef.GenerationID != rebound.GenerationID {
			t.Errorf("fact[%d] generation binding lost: %+v", i, second[i])
		}
		// Provider identity survives the rebind.
		if second[i].SourceRef.SourceSystem != CollectorKind {
			t.Errorf("fact[%d] SourceSystem = %q, want %q", i, second[i].SourceRef.SourceSystem, CollectorKind)
		}
	}
}

func TestExtractionPrepLabelValueHashesFlowThrough(t *testing.T) {
	t.Parallel()

	facts := extractionPrepFacts(t, extractionPrepContext())
	var found bool
	for _, fact := range facts {
		if fact.Kind != KindObservedLogSignal {
			continue
		}
		uid, _ := fact.Payload["provider_object_uid"].(string)
		if uid != "labelset:app" {
			continue
		}
		found = true
		hashes, ok := fact.Payload["label_value_hashes"].(map[string][]string)
		if !ok {
			t.Fatalf("label_value_hashes missing: %#v", fact.Payload["label_value_hashes"])
		}
		if len(hashes["app"]) != 1 || hashes["app"][0] != "sha256:recorded" {
			t.Errorf("label_value_hashes = %#v, want recorded app hash", hashes)
		}
	}
	if !found {
		t.Fatal("no observed_log_signal fact for labelset:app")
	}
}

func TestExtractionPrepRedactionSentinel(t *testing.T) {
	t.Parallel()

	const sentinel = "PREP-SENTINEL-8860"
	ctx := extractionPrepContext()

	// Raw LogQL never reaches the payload: only the redaction flag does.
	// Label values have no payload field at all (counts plus fingerprints
	// only), so the hostile signal half is structural, asserted below.
	hostileRule := extractionPrepRule()
	hostileRule.Query = "secret=" + sentinel
	hostileRule.QueryRedacted = true
	ruleFact, err := NewObservedRuleEnvelope(ctx, hostileRule)
	if err != nil {
		t.Fatalf("NewObservedRuleEnvelope(hostile query) error = %v, want nil", err)
	}
	raw, err := json.Marshal(ruleFact)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(raw), sentinel) {
		t.Errorf("credential sentinel reached persisted fact %q", ruleFact.Kind)
	}
	if ruleFact.Payload["query_redacted"] != true {
		t.Error("rule query_redacted = false, want LogQL redaction flag")
	}
	for _, fact := range extractionPrepFacts(t, ctx) {
		raw, err := json.Marshal(fact)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		for _, key := range []string{`"token"`, `"tenant_id"`, `"query"`, `"logql"`, `"label_values"`} {
			if strings.Contains(string(raw), key) {
				t.Errorf("fact %q carries forbidden payload key %s", fact.Kind, key)
			}
		}
		for _, forbidden := range []string{"tenant-prod", "payment failed", `{app="checkout"}`} {
			if strings.Contains(string(raw), forbidden) {
				t.Errorf("fact %q carries forbidden raw value %q", fact.Kind, forbidden)
			}
		}
	}
	// Tenant identity never travels raw: the source-instance fact carries
	// only presence plus a fingerprint.
	sourceFact := extractionPrepFacts(t, ctx)[0]
	raw, _ = json.Marshal(sourceFact)
	if strings.Contains(string(raw), "tenant-prod") {
		t.Error("raw tenant value reached persisted source_instance fact")
	}
	if sourceFact.Payload["tenant_redacted"] != true {
		t.Error("source tenant_redacted = false, want tenant redaction flag")
	}
	// Positive control: benign provider identity survives so the absence
	// above is meaningful.
	benign := extractionPrepFacts(t, ctx)
	benignRaw, _ := json.Marshal(benign)
	if !strings.Contains(string(benignRaw), "labelset:app") {
		t.Error("positive control: benign provider UID missing from persisted facts")
	}
}

func TestExtractionPrepHostileInputsFailClosed(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		mutate func(*EnvelopeContext)
	}{
		{"blank scope", func(c *EnvelopeContext) { c.ScopeID = "  " }},
		{"blank generation", func(c *EnvelopeContext) { c.GenerationID = "" }},
		{"blank collector instance", func(c *EnvelopeContext) { c.CollectorInstanceID = "" }},
		{"blank source instance", func(c *EnvelopeContext) { c.SourceInstanceID = "" }},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ctx := extractionPrepContext()
			tt.mutate(&ctx)
			if _, err := NewSourceInstanceEnvelope(ctx, SourceInstance{}, CollectionStats{}); err == nil {
				t.Errorf("NewSourceInstanceEnvelope() = nil, want fail-closed error for %s", tt.name)
			}
			if _, err := NewObservedLogSignalEnvelope(ctx, extractionPrepSignal()); err == nil {
				t.Errorf("NewObservedLogSignalEnvelope() = nil, want fail-closed error for %s", tt.name)
			}
			if _, err := NewObservedRuleEnvelope(ctx, extractionPrepRule()); err == nil {
				t.Errorf("NewObservedRuleEnvelope() = nil, want fail-closed error for %s", tt.name)
			}
			if _, err := NewCoverageWarningEnvelope(ctx, Warning{Reason: WarningPartial}); err == nil {
				t.Errorf("NewCoverageWarningEnvelope() = nil, want fail-closed error for %s", tt.name)
			}
		})
	}
	// Blank signal identity fails closed; blank warning reason degrades
	// to a partial-read warning instead of failing.
	ctx := extractionPrepContext()
	badSignal := extractionPrepSignal()
	badSignal.ProviderObjectID = "  "
	if _, err := NewObservedLogSignalEnvelope(ctx, badSignal); err == nil {
		t.Error("NewObservedLogSignalEnvelope(blank identity) = nil, want error")
	}
	badRule := extractionPrepRule()
	badRule.ProviderObjectID = ""
	badRule.Namespace = ""
	badRule.GroupName = ""
	badRule.RuleName = ""
	if _, err := NewObservedRuleEnvelope(ctx, badRule); err == nil {
		t.Error("NewObservedRuleEnvelope(blank identity) = nil, want error")
	}
	warning, err := NewCoverageWarningEnvelope(ctx, Warning{})
	if err != nil {
		t.Fatalf("NewCoverageWarningEnvelope(blank) error = %v, want partial-degrade not fatal", err)
	}
	if warning.Payload["warning_kind"] != WarningPartial {
		t.Errorf("warning_kind = %v, want %q degrade", warning.Payload["warning_kind"], WarningPartial)
	}
}
