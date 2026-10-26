// SPDX-License-Identifier: MIT
// Copyright (c) 2025-2026 eshu-hq

package loki

import (
	"time"
)

// MetricsRecorder is the host-wired seam for collector telemetry. The
// in-tree producer records eshu_dp_loki_* signals on core-owned
// instruments; those instruments stay core-owned after extraction. The
// extracted producer records the same call sites against this minimal
// interface so a host can adapt them to its own signals. A nil recorder
// disables recording. DiscardMetrics returns the no-op recorder.
type MetricsRecorder interface {
	// RecordFetch records one bounded provider request and its latency.
	RecordFetch(provider string, statusClass string, latency time.Duration)
	// RecordRateLimited records provider-throttled requests.
	RecordRateLimited(provider string, count int)
	// RecordFactsEmitted records emitted facts by kind.
	RecordFactsEmitted(provider string, counts map[string]int64)
	// RecordRedactions records metadata-only redactions applied.
	RecordRedactions(provider string, count int)
	// RecordRetries records provider request retries.
	RecordRetries(provider string, count int)
	// RecordStale records stale evidence observations.
	RecordStale(provider string, count int)
	// RecordHighCardinalityRejected records rejected high-cardinality
	// label values.
	RecordHighCardinalityRejected(provider string, count int)
}

type discardMetrics struct{}

// DiscardMetrics returns the no-op MetricsRecorder.
func DiscardMetrics() MetricsRecorder {
	return discardMetrics{}
}

func (discardMetrics) RecordFetch(string, string, time.Duration)   {}
func (discardMetrics) RecordRateLimited(string, int)               {}
func (discardMetrics) RecordFactsEmitted(string, map[string]int64) {}
func (discardMetrics) RecordRedactions(string, int)                {}
func (discardMetrics) RecordRetries(string, int)                   {}
func (discardMetrics) RecordStale(string, int)                     {}
func (discardMetrics) RecordHighCardinalityRejected(string, int)   {}

func (s *ClaimedSource) metrics() MetricsRecorder {
	if s == nil || s.recorder == nil {
		return DiscardMetrics()
	}
	return s.recorder
}

func (s *ClaimedSource) recordFetch(target TargetConfig, statusClass string, startedAt time.Time) {
	s.metrics().RecordFetch(ProviderLoki, statusClass, time.Since(startedAt))
}

func (s *ClaimedSource) recordRateLimit(target TargetConfig, failure ProviderFailure) {
	if failure.FailureClass() != FailureRateLimited {
		return
	}
	s.recordRateLimits(target, 1)
}

func (s *ClaimedSource) recordRateLimits(target TargetConfig, count int) {
	if count <= 0 {
		return
	}
	s.metrics().RecordRateLimited(ProviderLoki, count)
}

func (s *ClaimedSource) recordFacts(target TargetConfig, facts []emittedFact) {
	if len(facts) == 0 {
		return
	}
	counts := map[string]int64{}
	for _, fact := range facts {
		counts[fact.Kind]++
	}
	s.metrics().RecordFactsEmitted(ProviderLoki, counts)
}

func (s *ClaimedSource) recordRedactions(target TargetConfig, count int) {
	if count <= 0 {
		return
	}
	s.metrics().RecordRedactions(ProviderLoki, count)
}

func (s *ClaimedSource) recordRetries(target TargetConfig, count int) {
	if count <= 0 {
		return
	}
	s.metrics().RecordRetries(ProviderLoki, count)
}

func (s *ClaimedSource) recordStale(target TargetConfig, count int) {
	if count <= 0 {
		return
	}
	s.metrics().RecordStale(ProviderLoki, count)
}

func (s *ClaimedSource) recordHighCardinality(target TargetConfig, count int) {
	if count <= 0 {
		return
	}
	s.metrics().RecordHighCardinalityRejected(ProviderLoki, count)
}

func recordObservationStats(freshness string, redacted bool, result *CollectionResult) {
	if freshness == FreshnessStale {
		result.Stats.Stale++
	}
	if redacted {
		result.Stats.Redactions++
	}
}
