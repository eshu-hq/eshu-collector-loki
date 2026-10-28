// SPDX-License-Identifier: MIT
// Copyright (c) 2025-2026 eshu-hq

package loki

import (
	"errors"
	"testing"
	"time"

	"github.com/eshu-hq/eshu-sdk/collector/transport"
)

// spyRecorder keeps every signal the source records so tests can prove the
// MetricsRecorder seam carries the call sites instead of dropping them.
type spyRecorder struct {
	fetches      int
	rateLimited  int
	emitted      map[string]int64
	redactions   int
	retries      int
	stale        int
	highCardDrop int
}

func (s *spyRecorder) RecordFetch(string, string, time.Duration) { s.fetches++ }
func (s *spyRecorder) RecordRateLimited(string, int)             { s.rateLimited++ }
func (s *spyRecorder) RecordFactsEmitted(_ string, counts map[string]int64) {
	if s.emitted == nil {
		s.emitted = map[string]int64{}
	}
	for kind, n := range counts {
		s.emitted[kind] += n
	}
}
func (s *spyRecorder) RecordRedactions(string, int)              { s.redactions++ }
func (s *spyRecorder) RecordRetries(string, int)                 { s.retries++ }
func (s *spyRecorder) RecordStale(string, int)                   { s.stale++ }
func (s *spyRecorder) RecordHighCardinalityRejected(string, int) { s.highCardDrop++ }

// TestDiscardMetricsAcceptsEverySignal proves the no-op recorder honors the
// whole MetricsRecorder contract without panicking, so a host that wires no
// instruments still runs every record call site.
func TestDiscardMetricsAcceptsEverySignal(t *testing.T) {
	t.Parallel()

	recorder := DiscardMetrics()
	if recorder == nil {
		t.Fatal("DiscardMetrics() = nil, want a usable recorder")
	}
	recorder.RecordFetch(ProviderLoki, "2xx", time.Millisecond)
	recorder.RecordRateLimited(ProviderLoki, 1)
	recorder.RecordFactsEmitted(ProviderLoki, map[string]int64{KindSourceInstance: 1})
	recorder.RecordRedactions(ProviderLoki, 1)
	recorder.RecordRetries(ProviderLoki, 1)
	recorder.RecordStale(ProviderLoki, 1)
	recorder.RecordHighCardinalityRejected(ProviderLoki, 1)
}

// TestMetricsFallsBackToDiscard proves a source without a wired recorder
// still records safely: metrics() on a recorder-less (or nil) source
// returns the discard recorder instead of nil.
func TestMetricsFallsBackToDiscard(t *testing.T) {
	t.Parallel()

	source := testSource(t, &stubEvidenceClient{result: stubSnapshot()})
	if source.metrics() == nil {
		t.Fatal("metrics() on a recorder-less source = nil, want the discard recorder")
	}
	var nilSource *ClaimedSource
	if nilSource.metrics() == nil {
		t.Fatal("metrics() on a nil source = nil, want the discard recorder")
	}
}

// TestClaimedSourceRecordsSignalsOnSpy proves each record call site reaches
// the wired recorder with the provider namespace, and that non-positive
// counts stay silent instead of emitting zero-count signals.
func TestClaimedSourceRecordsSignalsOnSpy(t *testing.T) {
	t.Parallel()

	spy := &spyRecorder{}
	source := testSource(t, &stubEvidenceClient{result: stubSnapshot()})
	source.recorder = spy
	target := TargetConfig{ScopeID: "loki:tenant:prod"}

	source.recordFetch(target, "2xx", time.Now().Add(-time.Millisecond))
	rateLimited := transport.NewProviderFailure("loki", transport.FailureRateLimited, false, errors.New("slow down"))
	source.recordRateLimit(target, rateLimited)
	other := transport.NewProviderFailure("loki", transport.FailureRetryable, false, errors.New("boom"))
	source.recordRateLimit(target, other)
	source.recordRateLimits(target, 0)
	source.recordFacts(target, []emittedFact{{Kind: KindSourceInstance}, {Kind: KindSourceInstance}})
	source.recordFacts(target, nil)
	source.recordRedactions(target, 2)
	source.recordRedactions(target, 0)
	source.recordRetries(target, 1)
	source.recordRetries(target, -1)
	source.recordStale(target, 1)
	source.recordStale(target, 0)
	source.recordHighCardinality(target, 1)
	source.recordHighCardinality(target, 0)

	if spy.fetches != 1 {
		t.Errorf("fetches = %d, want 1", spy.fetches)
	}
	if spy.rateLimited != 1 {
		t.Errorf("rate-limited signals = %d, want 1 (non-rate-limited failures stay silent)", spy.rateLimited)
	}
	if got := spy.emitted[KindSourceInstance]; got != 2 {
		t.Errorf("emitted[%s] = %d, want 2", KindSourceInstance, got)
	}
	for name, got := range map[string]int{"redactions": spy.redactions, "retries": spy.retries, "stale": spy.stale, "high-cardinality": spy.highCardDrop} {
		if got != 1 {
			t.Errorf("%s signals = %d, want 1 (non-positive counts stay silent)", name, got)
		}
	}
}

// TestDefaultClientFactoryBuildsAnHTTPClient proves the nil-factory path in
// NewClaimedSource resolves to a working HTTP evidence client for a valid
// base URL.
func TestDefaultClientFactoryBuildsAnHTTPClient(t *testing.T) {
	t.Parallel()

	client, err := defaultClientFactory(TargetConfig{BaseURL: "https://loki.example.test"})
	if err != nil {
		t.Fatalf("defaultClientFactory() error = %v, want nil", err)
	}
	if client == nil {
		t.Fatal("defaultClientFactory() = nil, want an HTTP evidence client")
	}
}

// TestNormalizeRuleNamesRecordingRules proves a record rule keeps its name
// with the recording type, the last unnamed branch of ruleNameAndType.
func TestNormalizeRuleNamesRecordingRules(t *testing.T) {
	t.Parallel()

	name, ruleType := ruleNameAndType(ruleResource{Record: "loki:ops:rate5m"})
	if name != "loki:ops:rate5m" || ruleType != RuleTypeRecording {
		t.Errorf("ruleNameAndType(record) = %q/%q, want loki:ops:rate5m/%s", name, ruleType, RuleTypeRecording)
	}
	if name, ruleType := ruleNameAndType(ruleResource{}); name != "" || ruleType != "" {
		t.Errorf("ruleNameAndType(empty) = %q/%q, want empty", name, ruleType)
	}
}

// TestRuleIdentityFallsBackToQueryFingerprint proves an unnamed rule still
// gets a stable identity from its query, and a fully empty rule gets none.
func TestRuleIdentityFallsBackToQueryFingerprint(t *testing.T) {
	t.Parallel()

	first := ruleIdentity("", "", "", "sum(rate({app=\"api\"}[5m]))")
	second := ruleIdentity("", "", "", "sum(rate({app=\"api\"}[5m]))")
	if first == "" || first != second {
		t.Errorf("ruleIdentity(query) = %q/%q, want a stable non-empty fingerprint", first, second)
	}
	if got := ruleIdentity("", "", "", "  "); got != "" {
		t.Errorf("ruleIdentity(blank) = %q, want empty", got)
	}
	if got := ruleIdentity("", "checkout.rules", "HighLogErrors", ""); got != "checkout.rules:HighLogErrors" {
		t.Errorf("ruleIdentity(group:name) = %q, want checkout.rules:HighLogErrors", got)
	}
}
