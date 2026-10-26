// SPDX-License-Identifier: MIT
// Copyright (c) 2025-2026 eshu-hq

package collector

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"gopkg.in/yaml.v3"

	sdk "github.com/eshu-hq/eshu-sdk/collector"
	"github.com/eshu-hq/eshu-sdk/collector/conformance"
	"github.com/eshu-hq/eshu-sdk/factschema/fixturepack"

	"github.com/eshu-hq/eshu-collector-loki/loki"
)

const (
	testScopeID    = "loki:tenant:synthetic-1"
	testInstanceID = "loki-primary"
	testGeneration = "generation-1"
	testTokenEnv   = "ESHU_TEST_LOKI_TOKEN"
	testTokenValue = "SYNTHETIC-API-TOKEN-0001"
	testBaseURL    = "http://loki.example.invalid"
)

func envWith(values map[string]string) func(string) string {
	return func(key string) string { return values[key] }
}

// testObservedAt is the fixed collection clock every handler test uses.
func testObservedAt() time.Time {
	return time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
}

// testClaim is the claim the host would build for the synthetic target.
func testClaim() sdk.Claim {
	return sdk.Claim{
		ComponentID:   ComponentID,
		InstanceID:    testInstanceID,
		CollectorKind: CollectorKind,
		SourceSystem:  SourceSystem,
		Scope:         sdk.Scope{ID: testScopeID, Kind: ScopeKind},
		SourceRunID:   "run-1",
		GenerationID:  testGeneration,
		WorkItemID:    "work-1",
		FencingToken:  "7",
		Attempt:       1,
		Deadline:      time.Date(2099, time.January, 1, 0, 0, 0, 0, time.UTC),
		ConfigHandle:  "component-config:loki",
	}
}

// testConfig returns the activation config map the host would pass, with the
// given target overrides applied.
func testConfig(baseURL string, overrides map[string]any) map[string]any {
	target := map[string]any{
		"scope_id":    testScopeID,
		"instance_id": "synthetic-1",
		"base_url":    baseURL,
		"enabled":     true,
	}
	for key, value := range overrides {
		target[key] = value
	}
	return map[string]any{"targets": []any{target}}
}

func testRequest(config map[string]any) Request {
	return Request{
		ProtocolVersion: sdk.ProtocolVersionV1Alpha1,
		Claim:           testClaim(),
		Contract:        Contract(),
		Config:          config,
	}
}

// stubClient returns a fixed CollectionResult for the synthetic target.
type stubClient struct {
	result loki.CollectionResult
	err    error
}

func (c stubClient) CollectObservedMetadata(context.Context, loki.TargetConfig) (loki.CollectionResult, error) {
	return c.result, c.err
}

// testSnapshot is one bounded Loki metadata snapshot exercising every emitted
// kind with minimal valid structs.
func testSnapshot() loki.CollectionResult {
	return loki.CollectionResult{
		Source: loki.SourceInstance{
			Provider:         loki.ProviderLoki,
			SourceInstanceID: "synthetic-1",
		},
		Signals: []loki.LogSignal{{
			ProviderObjectID: "sig-1",
			SignalKind:       loki.SignalKindLabelSet,
			Outcome:          loki.OutcomeObserved,
			FreshnessState:   loki.FreshnessCurrent,
		}},
		Rules: []loki.Rule{{
			ProviderObjectID: "rule-1",
			RuleType:         loki.RuleTypeRecording,
			Outcome:          loki.OutcomeObserved,
			FreshnessState:   loki.FreshnessCurrent,
		}},
		Warnings: []loki.Warning{{
			ResourceClass: loki.ResourceClassRule,
			Reason:        loki.WarningUnsupported,
		}},
		ObservedAt: testObservedAt(),
	}
}

func testHandler(factory loki.ClientFactory) Handler {
	if factory == nil {
		factory = func(loki.TargetConfig) (loki.EvidenceClient, error) {
			return stubClient{result: testSnapshot()}, nil
		}
	}
	return Handler{
		Getenv: envWith(map[string]string{testTokenEnv: testTokenValue}),
		// Real clock: the emission edge checks the grant against wall time,
		// so a fixed past clock would read as an expired grant. Only the
		// snapshot's ObservedAt is fixed (see testSnapshot).
		Now:           time.Now,
		ClientFactory: factory,
	}
}

func loadManifest(t *testing.T) conformance.Manifest {
	t.Helper()
	raw, err := os.ReadFile("manifest.yaml")
	if err != nil {
		t.Fatalf("os.ReadFile(manifest.yaml) error = %v", err)
	}
	var manifest conformance.Manifest
	if err := yaml.Unmarshal(raw, &manifest); err != nil {
		t.Fatalf("yaml.Unmarshal(manifest.yaml) error = %v", err)
	}
	return manifest
}

// mustFixtureResult runs the synthetic target through the handler and fails
// the test unless the result is usable.
func mustFixtureResult(t *testing.T) sdk.Result {
	t.Helper()
	result, summary := testHandler(nil).Handle(
		context.Background(), testRequest(testConfig(testBaseURL, nil)),
	)
	if result.State != sdk.ResultComplete && result.State != sdk.ResultPartial {
		t.Fatalf("fixture result state = %q (failure class %q), want complete or partial", result.State, summary.FailureClass)
	}
	return result
}

// payloadSchemas resolves the manifest's payloadSchemaRef entries from the
// pinned fixture pack, exactly as the host does before it accepts a result.
func payloadSchemas(t *testing.T, manifest conformance.Manifest) map[string]json.RawMessage {
	t.Helper()
	schemas := map[string]json.RawMessage{}
	for _, family := range manifest.Spec.EmittedFacts {
		if family.PayloadSchemaRef == "" {
			continue
		}
		schema, ok := fixturepack.SchemaFor(family.PayloadSchemaRef)
		if !ok {
			t.Fatalf("pinned fixture pack has no schema for %q", family.PayloadSchemaRef)
		}
		schemas[family.Kind] = schema
	}
	return schemas
}

func hasFinding(report conformance.Report, code conformance.FindingCode) bool {
	for _, finding := range report.Findings {
		if finding.Code == code {
			return true
		}
	}
	return false
}

// factsByKind groups result facts by kind.
func factsByKind(result sdk.Result) map[string][]sdk.Fact {
	grouped := map[string][]sdk.Fact{}
	for _, fact := range result.Facts {
		grouped[fact.Kind] = append(grouped[fact.Kind], fact)
	}
	return grouped
}
