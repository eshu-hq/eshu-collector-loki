// SPDX-License-Identifier: MIT
// Copyright (c) 2025-2026 eshu-hq

package loki

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/eshu-hq/eshu-sdk/collector"
	"github.com/eshu-hq/eshu-sdk/collector/transport"

	"github.com/eshu-hq/eshu-collector-loki/grant"
)

type stubEvidenceClient struct {
	result CollectionResult
	err    error
}

func (c *stubEvidenceClient) CollectObservedMetadata(context.Context, TargetConfig) (CollectionResult, error) {
	return c.result, c.err
}

// testClaimSpec overrides one field of the default test claim.
type testClaimSpec struct {
	ScopeID      string
	GenerationID string
	FencingToken string
}

func defaultClaimSpec() testClaimSpec {
	return testClaimSpec{
		ScopeID:      "loki:tenant:prod",
		GenerationID: "loki:generation-7",
		FencingToken: "7",
	}
}

func parityTestClaim(t *testing.T, spec testClaimSpec) collector.Claim {
	t.Helper()

	claim := testClaim()
	if spec.ScopeID != "" {
		claim.Scope.ID = spec.ScopeID
	}
	if spec.GenerationID != "" {
		claim.GenerationID = spec.GenerationID
	}
	if spec.FencingToken != "" {
		claim.FencingToken = spec.FencingToken
	}
	return claim
}

func testClaim() collector.Claim {
	return collector.Claim{
		ComponentID:   "dev.eshu.collector.loki",
		InstanceID:    "collector-loki-parity",
		CollectorKind: CollectorKind,
		SourceSystem:  CollectorKind,
		Scope:         collector.Scope{ID: "loki:tenant:prod", Kind: ScopeKindLogSource},
		SourceRunID:   "run-1",
		GenerationID:  "loki:generation-7",
		WorkItemID:    "work-1",
		FencingToken:  "7",
		Attempt:       1,
		Deadline:      time.Now().UTC().Add(time.Hour),
		ConfigHandle:  "config-1",
	}
}

func testGrant() grant.Grant {
	now := time.Now().UTC()
	return grant.Grant{
		ProducerID: grant.ProducerID,
		Version:    grant.Version,
		Scope:      grant.Scope,
		Kinds: []grant.KindGrant{
			{Kind: KindSourceInstance, SchemaVersions: []string{SchemaVersionV1}},
			{Kind: KindObservedLogSignal, SchemaVersions: []string{SchemaVersionV1}},
			{Kind: KindObservedRule, SchemaVersions: []string{SchemaVersionV1}},
			{Kind: KindCoverageWarning, SchemaVersions: []string{SchemaVersionV1}},
		},
		IssuedAt:  now,
		ExpiresAt: now.Add(720 * time.Hour),
	}
}

func testSource(t *testing.T, client EvidenceClient) *ClaimedSource {
	t.Helper()

	source, err := NewClaimedSource(SourceConfig{
		CollectorInstanceID: "collector-loki-parity",
		Targets: []TargetConfig{{
			ScopeID:       "loki:tenant:prod",
			InstanceID:    "loki-prod",
			BaseURL:       "https://loki.example.test",
			ResourceLimit: 50,
			Enabled:       true,
		}},
		ClientFactory: func(TargetConfig) (EvidenceClient, error) { return client, nil },
	})
	if err != nil {
		t.Fatalf("NewClaimedSource() error = %v, want nil", err)
	}
	return source
}

func stubSnapshot() CollectionResult {
	return CollectionResult{
		Source: SourceInstance{
			Provider:         ProviderLoki,
			SourceInstanceID: "loki-prod",
		},
		Signals: []LogSignal{{
			ProviderObjectID: "signal-1",
			SignalKind:       SignalKindLabelSet,
			LabelKeys:        []string{"app", "namespace"},
		}},
		Rules: []Rule{{
			ProviderObjectID: "prod/checkout.rules:HighLogErrors",
			Namespace:        "prod",
			GroupName:        "checkout.rules",
			RuleName:         "HighLogErrors",
			RuleType:         RuleTypeAlerting,
		}},
		Warnings: []Warning{{
			ResourceClass: ResourceClassLogSignal,
			ResourceID:    "label:trace_id",
			Reason:        WarningHighCardinality,
		}},
		ObservedAt: parityObservedAt,
		Stats: CollectionStats{
			PagesFetched:            3,
			Redactions:              2,
			HighCardinalityRejected: 1,
			Stale:                   1,
		},
	}
}

// grantContract mirrors the grant draft: the four observed kinds at schema
// 1.0.0 with reported confidence. The validator self-check below proves the
// producer's output satisfies the published SDK's own admission rules.
func grantContract() collector.Contract {
	declare := func(kind string) collector.FactDeclaration {
		return collector.FactDeclaration{
			Kind:             kind,
			SchemaVersions:   []string{SchemaVersionV1},
			SourceConfidence: []collector.SourceConfidence{collector.SourceConfidenceReported},
		}
	}
	return collector.Contract{
		ProtocolVersion: collector.ProtocolVersionV1Alpha1,
		Facts: []collector.FactDeclaration{
			declare(KindSourceInstance),
			declare(KindObservedLogSignal),
			declare(KindObservedRule),
			declare(KindCoverageWarning),
		},
	}
}

// testResult wraps a Collect result for the parity assertions.
type testResult struct {
	result collector.Result
}

func (r testResult) facts() []collector.Fact {
	return r.result.Facts
}

func (r testResult) countByKind() map[string]int {
	out := map[string]int{}
	for _, fact := range r.result.Facts {
		out[fact.Kind]++
	}
	return out
}

func (r testResult) validate() error {
	_, err := collector.NewValidator(grantContract()).ValidateResult(r.result)
	return err
}

func TestCollectEmitsValidatorCleanResult(t *testing.T) {
	t.Parallel()

	source := testSource(t, &stubEvidenceClient{result: stubSnapshot()})
	got, err := source.Collect(context.Background(), testClaim(), testGrant())
	if err != nil {
		t.Fatalf("Collect() error = %v, want nil", err)
	}
	if got.State != collector.ResultComplete {
		t.Errorf("State = %q, want complete", got.State)
	}
	// Source instance + one signal + one rule + one warning.
	if len(got.Facts) != 4 {
		t.Fatalf("len(Facts) = %d, want 4", len(got.Facts))
	}
	if got.Generation.ID != "loki:generation-7" {
		t.Errorf("Generation.ID = %q, want claim generation", got.Generation.ID)
	}
	if got.Generation.FreshnessHint != "loki_observed_metadata" {
		t.Errorf("FreshnessHint = %q, want loki_observed_metadata", got.Generation.FreshnessHint)
	}
	for _, fact := range got.Facts {
		if fact.SourceRef.ScopeID != "loki:tenant:prod" {
			t.Errorf("fact %q scope = %q, want loki:tenant:prod", fact.Kind, fact.SourceRef.ScopeID)
		}
		if fact.SourceRef.SourceSystem != CollectorKind {
			t.Errorf("fact %q source_system = %q, want loki", fact.Kind, fact.SourceRef.SourceSystem)
		}
		if fact.SourceRef.FactKey != fact.StableKey {
			t.Errorf("fact %q source_ref.fact_key does not match stable_key", fact.Kind)
		}
	}
	report, err := collector.NewValidator(grantContract()).ValidateResult(got)
	if err != nil {
		t.Fatalf("ValidateResult() error = %v, want validator-clean output", err)
	}
	if report.FactCount != 4 {
		t.Errorf("report.FactCount = %d, want 4", report.FactCount)
	}
}

func TestCollectPartialReadStaysPartial(t *testing.T) {
	t.Parallel()

	snapshot := stubSnapshot()
	snapshot.Stats.Partial = true
	snapshot.Warnings = append(snapshot.Warnings, Warning{ResourceClass: ResourceClassLogSignal, Reason: WarningRateLimited})
	source := testSource(t, &stubEvidenceClient{result: snapshot})
	got, err := source.Collect(context.Background(), testClaim(), testGrant())
	if err != nil {
		t.Fatalf("Collect() error = %v, want nil", err)
	}
	if got.State != collector.ResultPartial {
		t.Fatalf("State = %q, want partial", got.State)
	}
	if _, err := collector.NewValidator(grantContract()).ValidateResult(got); err != nil {
		t.Fatalf("ValidateResult(partial) error = %v, want validator-clean output", err)
	}
}

func TestCollectTruncatedReadStaysPartial(t *testing.T) {
	t.Parallel()

	snapshot := stubSnapshot()
	snapshot.Stats.Truncated = true
	snapshot.Warnings = append(snapshot.Warnings, Warning{ResourceClass: ResourceClassRule, Reason: WarningTruncated})
	source := testSource(t, &stubEvidenceClient{result: snapshot})
	got, err := source.Collect(context.Background(), testClaim(), testGrant())
	if err != nil {
		t.Fatalf("Collect() error = %v, want nil", err)
	}
	if got.State != collector.ResultPartial {
		t.Fatalf("State = %q, want partial", got.State)
	}
	if _, err := collector.NewValidator(grantContract()).ValidateResult(got); err != nil {
		t.Fatalf("ValidateResult(truncated) error = %v, want validator-clean output", err)
	}
}

func TestCollectFailsClosedWithoutGrant(t *testing.T) {
	t.Parallel()

	source := testSource(t, &stubEvidenceClient{result: stubSnapshot()})
	if _, err := source.Collect(context.Background(), testClaim(), grant.Grant{}); err == nil {
		t.Error("Collect(zero grant) = nil, want fail-closed grant denial")
	} else if !errors.Is(err, grant.ErrDenied) {
		t.Errorf("Collect(zero grant) error = %v, want grant.ErrDenied", err)
	}
}

func TestCollectRejectsForeignClaim(t *testing.T) {
	t.Parallel()

	source := testSource(t, &stubEvidenceClient{result: stubSnapshot()})

	wrongInstance := testClaim()
	wrongInstance.InstanceID = "collector-other"
	if _, err := source.Collect(context.Background(), wrongInstance, testGrant()); err == nil {
		t.Error("Collect(foreign instance) = nil, want rejection")
	}

	wrongKind := testClaim()
	wrongKind.CollectorKind = "prometheus"
	if _, err := source.Collect(context.Background(), wrongKind, testGrant()); err == nil {
		t.Error("Collect(foreign kind) = nil, want rejection")
	}

	wrongSystem := testClaim()
	wrongSystem.SourceSystem = "grafana"
	if _, err := source.Collect(context.Background(), wrongSystem, testGrant()); err == nil {
		t.Error("Collect(foreign source_system) = nil, want rejection")
	}

	blankGeneration := testClaim()
	blankGeneration.GenerationID = ""
	if _, err := source.Collect(context.Background(), blankGeneration, testGrant()); err == nil {
		t.Error("Collect(blank generation) = nil, want rejection")
	}
}

func TestCollectUnknownScopeIsRetryable(t *testing.T) {
	t.Parallel()

	source := testSource(t, &stubEvidenceClient{result: stubSnapshot()})
	claim := testClaim()
	claim.Scope.ID = "loki:tenant:missing"
	_, err := source.Collect(context.Background(), claim, testGrant())
	if err == nil {
		t.Fatal("Collect(unknown scope) = nil, want retryable failure")
	}
	var failure transport.ProviderFailure
	if !errors.As(err, &failure) {
		t.Fatalf("Collect(unknown scope) error type = %T, want transport.ProviderFailure", err)
	}
	if failure.FailureClass() != FailureRetryable {
		t.Errorf("FailureClass() = %q, want retryable", failure.FailureClass())
	}
}

func TestCollectClassifiesProviderFailure(t *testing.T) {
	t.Parallel()

	providerErr := LokiError{Provider: "loki", StatusCode: 429, Message: "Too Many Requests"}
	source := testSource(t, &stubEvidenceClient{err: providerErr})
	_, err := source.Collect(context.Background(), testClaim(), testGrant())
	if err == nil {
		t.Fatal("Collect(provider 429) = nil, want classified failure")
	}
	var failure transport.ProviderFailure
	if !errors.As(err, &failure) {
		t.Fatalf("Collect(provider 429) error type = %T, want transport.ProviderFailure", err)
	}
	if failure.FailureClass() != FailureRateLimited {
		t.Errorf("FailureClass() = %q, want rate_limited", failure.FailureClass())
	}
}

func TestCollectClassifiesTerminalFailures(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		err          error
		wantClass    string
		wantTerminal bool
	}{
		{
			name:         "auth denied http",
			err:          LokiError{Provider: "loki", StatusCode: http.StatusForbidden, Message: "forbidden token-secret"},
			wantClass:    FailureAuthDenied,
			wantTerminal: true,
		},
		{
			name:         "bad data api",
			err:          ProviderAPIError{Status: "error", ErrorType: "bad_data"},
			wantClass:    FailureTerminal,
			wantTerminal: true,
		},
		{
			name:      "provider api retryable",
			err:       ProviderAPIError{Status: "error", ErrorType: "timeout"},
			wantClass: FailureRetryable,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			source := testSource(t, &stubEvidenceClient{err: tt.err})
			_, err := source.Collect(context.Background(), testClaim(), testGrant())
			if err == nil {
				t.Fatal("Collect() error = nil, want provider failure")
			}
			var failure transport.ProviderFailure
			if !errors.As(err, &failure) {
				t.Fatalf("Collect() error = %T, want transport.ProviderFailure", err)
			}
			if got := failure.FailureClass(); got != tt.wantClass {
				t.Fatalf("FailureClass() = %q, want %q", got, tt.wantClass)
			}
			if got := failure.TerminalFailure(); got != tt.wantTerminal {
				t.Fatalf("TerminalFailure() = %v, want %v", got, tt.wantTerminal)
			}
		})
	}
}

func TestClaimedSourceSkipsDisabledTargets(t *testing.T) {
	t.Parallel()

	_, err := NewClaimedSource(SourceConfig{
		CollectorInstanceID: "collector-loki-prod",
		Targets: []TargetConfig{{
			ScopeID:    "loki:tenant:prod",
			InstanceID: "loki-prod",
			BaseURL:    "https://loki.example.internal",
			Enabled:    false,
		}},
		ClientFactory: func(TargetConfig) (EvidenceClient, error) {
			t.Fatal("disabled target should not construct a client")
			return nil, nil
		},
	})
	if err == nil {
		t.Fatal("NewClaimedSource() error = nil, want no enabled target error")
	}
}
