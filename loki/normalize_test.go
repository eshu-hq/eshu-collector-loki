// SPDX-License-Identifier: MIT
// Copyright (c) 2025-2026 eshu-hq

package loki

import (
	"reflect"
	"testing"
	"time"
)

// TestNormalizedOutcomeAdmitsEveryOutcome proves the outcome normalizer is
// total: every known outcome round-trips (whitespace tolerated) and anything
// else degrades to observed, never to an empty or unknown state.
func TestNormalizedOutcomeAdmitsEveryOutcome(t *testing.T) {
	t.Parallel()

	for _, outcome := range []string{OutcomeObserved, OutcomePartial, OutcomePermissionHidden, OutcomeUnsupported, OutcomeRejected, OutcomeStale} {
		if got := normalizedOutcome("  " + outcome + " "); got != outcome {
			t.Errorf("normalizedOutcome(%q) = %q, want it back", outcome, got)
		}
	}
	if got := normalizedOutcome("bogus"); got != OutcomeObserved {
		t.Errorf("normalizedOutcome(bogus) = %q, want observed", got)
	}
	if got := normalizedOutcome(""); got != OutcomeObserved {
		t.Errorf("normalizedOutcome(empty) = %q, want observed", got)
	}
}

// TestNormalizedFreshnessAdmitsEveryFreshness proves the freshness
// normalizer is total: every known state round-trips and anything else
// degrades to current, never to an empty state.
func TestNormalizedFreshnessAdmitsEveryFreshness(t *testing.T) {
	t.Parallel()

	for _, state := range []string{FreshnessUnknown, FreshnessStale, FreshnessPermissionHidden, FreshnessCurrent} {
		if got := normalizedFreshness(state); got != state {
			t.Errorf("normalizedFreshness(%q) = %q, want it back", state, got)
		}
	}
	if got := normalizedFreshness("bogus"); got != FreshnessCurrent {
		t.Errorf("normalizedFreshness(bogus) = %q, want current", got)
	}
}

// TestWarningOutcomeCoversUnsupportedAndManualResources proves the two
// warning reasons the envelope table does not exercise: an unsupported
// endpoint maps to unsupported, and a manual provider-only resource maps to
// observed (it exists, the collector just does not manage it).
func TestWarningOutcomeCoversUnsupportedAndManualResources(t *testing.T) {
	t.Parallel()

	if got := warningOutcome(WarningUnsupported); got != OutcomeUnsupported {
		t.Errorf("warningOutcome(unsupported) = %q, want unsupported", got)
	}
	if got := warningOutcome(WarningManualProviderResource); got != OutcomeObserved {
		t.Errorf("warningOutcome(manual_provider_resource) = %q, want observed", got)
	}
}

// TestNormalizedObservedAtZeroUsesWallClock proves a missing observation
// time degrades to now (UTC) instead of the zero time, while a set time is
// kept exactly.
func TestNormalizedObservedAtZeroUsesWallClock(t *testing.T) {
	t.Parallel()

	before := time.Now().UTC()
	got := normalizedObservedAt(time.Time{})
	after := time.Now().UTC()
	if got.IsZero() || got.Before(before.Add(-time.Minute)) || got.After(after.Add(time.Minute)) {
		t.Errorf("normalizedObservedAt(zero) = %v, want approximately now", got)
	}
	fixed := time.Date(2026, 6, 1, 12, 0, 0, 0, time.FixedZone("x", 3600))
	if got := normalizedObservedAt(fixed); !got.Equal(fixed.UTC()) {
		t.Errorf("normalizedObservedAt(fixed) = %v, want %v", got, fixed.UTC())
	}
}

// TestNormalizeSeriesMarksEmpty proves the series normalizer's edge: an
// empty label set yields no signal instead of a fingerprinted phantom.
//
// NOTE: the stale branch in normalizeSeries is unreachable by construction:
// it calls isStale with the zero time, which always reports fresh. Series
// staleness can only arrive via the client path, not normalization. The
// same holds for the stale branch in normalizeRule (LastEvaluationAt is
// unset at normalization time). Neither is asserted here.
func TestNormalizeSeriesMarksEmpty(t *testing.T) {
	t.Parallel()

	if got := normalizeSeries(map[string]string{}, TargetConfig{}, time.Now().UTC()); !reflect.DeepEqual(got, LogSignal{}) {
		t.Errorf("normalizeSeries(empty) = %+v, want the zero signal", got)
	}
}

// TestNormalizeRuleMarksObservedOnly proves the rule normalizer's edge: the
// observed-only hint flags an unmatched rule as manually created.
func TestNormalizeRuleMarksObservedOnly(t *testing.T) {
	t.Parallel()

	observedAt := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	manual := normalizeRule("prod", ruleGroupResource{Name: "checkout.rules"},
		ruleResource{Alert: "HighLogErrors"}, TargetConfig{ObservedOnlyHint: true}, observedAt)
	if !manual.ManuallyCreated {
		t.Error("normalizeRule(observed-only, unmatched) ManuallyCreated = false, want true")
	}
}

// TestRuleIdentityFallsBackToBareName proves a rule with a name but no
// namespace or group still gets a stable rule:-prefixed identity.
func TestRuleIdentityFallsBackToBareName(t *testing.T) {
	t.Parallel()

	if got := ruleIdentity("", "", "HighLogErrors", ""); got != "rule:HighLogErrors" {
		t.Errorf("ruleIdentity(bare name) = %q, want rule:HighLogErrors", got)
	}
}
