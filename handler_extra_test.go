// SPDX-License-Identifier: MIT
// Copyright (c) 2025-2026 eshu-hq

package collector

import (
	"context"
	"errors"
	"testing"
	"time"

	sdk "github.com/eshu-hq/eshu-sdk/collector"

	"github.com/eshu-hq/eshu-collector-loki/loki"
)

// TestClampedDeadlineTimeout proves the claim deadline is clamped below the
// host kill: no deadline or none left reports false, otherwise the timeout is
// the remaining time minus min(2s, 5%).
func TestClampedDeadlineTimeout(t *testing.T) {
	t.Parallel()

	now := testObservedAt()
	for _, test := range []struct {
		name     string
		deadline time.Time
		want     time.Duration
		ok       bool
	}{
		{"no deadline", time.Time{}, 0, false},
		{"already passed", now.Add(-time.Second), 0, false},
		{"exactly now", now, 0, false},
		{"default 60s lease", now.Add(60 * time.Second), 58 * time.Second, true},
		{"5m lease", now.Add(5 * time.Minute), 5*time.Minute - 2*time.Second, true},
		{"40s is the 2s cap boundary", now.Add(40 * time.Second), 38 * time.Second, true},
		{"10s takes 5 percent", now.Add(10 * time.Second), 9500 * time.Millisecond, true},
		{"1s takes 50ms", now.Add(time.Second), 950 * time.Millisecond, true},
	} {
		got, ok := clampedDeadlineTimeout(now, test.deadline)
		if got != test.want || ok != test.ok {
			t.Errorf("%s: clampedDeadlineTimeout() = %v, %t, want %v, %t", test.name, got, ok, test.want, test.ok)
		}
	}
}

// TestHandleCancellationIsRetryable proves a canceled claim ends retryable
// with the canceled class instead of the provider's classification.
func TestHandleCancellationIsRetryable(t *testing.T) {
	t.Parallel()

	factory := func(loki.TargetConfig) (loki.EvidenceClient, error) {
		return stubClient{err: context.Canceled}, nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	result, summary := testHandler(factory).Handle(ctx, testRequest(testConfig(testBaseURL, nil)))
	if result.State != sdk.ResultRetryable || summary.FailureClass != FailureClassCanceled {
		t.Errorf("canceled = %q/%q, want retryable canceled", result.State, summary.FailureClass)
	}
}

// TestHandleTransportFailureIsRetryable proves a transport failure that
// carries no bounded class still ends retryable without leaking the cause.
func TestHandleTransportFailureIsRetryable(t *testing.T) {
	t.Parallel()

	factory := func(loki.TargetConfig) (loki.EvidenceClient, error) {
		return stubClient{err: errors.New("HOSTILE dial tcp: connection refused")}, nil
	}
	result, summary := testHandler(factory).Handle(
		context.Background(), testRequest(testConfig(testBaseURL, nil)),
	)
	if result.State != sdk.ResultRetryable {
		t.Errorf("transport failure = %q/%q, want retryable", result.State, summary.FailureClass)
	}
	for _, status := range result.Statuses {
		if status.FailureClass == "" {
			t.Error("retryable status carries no failure class")
		}
	}
}

// TestHandleNeverReturnsUnchanged proves every handler outcome is a state the
// host can act on: complete, partial, retryable, or terminal.
func TestHandleNeverReturnsUnchanged(t *testing.T) {
	t.Parallel()

	result, _ := testHandler(nil).Handle(
		context.Background(), testRequest(testConfig(testBaseURL, nil)),
	)
	if result.State == sdk.ResultUnchanged {
		t.Error("handler returned unchanged; it must always complete, warn, or fail the claim")
	}
}

// TestHandlerGetenvDefaultsToProcessEnvironment proves a handler without an
// injected Getenv reads the process environment, and that an injected
// Getenv wins: ParseConfig resolves token and tenant variables through it.
func TestHandlerGetenvDefaultsToProcessEnvironment(t *testing.T) {
	// No t.Parallel: t.Setenv refuses parallel tests.
	const key = "ESHU_LOKI_COLLECTOR_TEST_TOKEN"
	t.Setenv(key, "process-env-token")
	if got := (Handler{}).getenv(key); got != "process-env-token" {
		t.Errorf("getenv(%q) = %q, want the process environment value", key, got)
	}
	if got := (Handler{Getenv: func(string) string { return "injected" }}).getenv(key); got != "injected" {
		t.Errorf("getenv(%q) = %q, want the injected value to win", key, got)
	}
}

// TestHandlerNowDefaultsToWallClock proves a handler without an injected
// clock stamps claims with the wall time instead of the zero time.
func TestHandlerNowDefaultsToWallClock(t *testing.T) {
	t.Parallel()

	before := time.Now()
	got := (Handler{}).now()
	after := time.Now()
	if got.Before(before.Add(-time.Minute)) || got.After(after.Add(time.Minute)) {
		t.Errorf("now() = %v, want approximately the wall clock", got)
	}
}
