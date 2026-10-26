// SPDX-License-Identifier: MIT
// Copyright (c) 2025-2026 eshu-hq

package collector

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	sdk "github.com/eshu-hq/eshu-sdk/collector"
	"github.com/eshu-hq/eshu-sdk/collector/transport"

	"github.com/eshu-hq/eshu-collector-loki/grant"
	"github.com/eshu-hq/eshu-collector-loki/loki"
)

// TestHandleServesACompleteResult proves one claim against the stub target
// returns every manifest-owned kind, echoes the claim, and passes the
// published SDK validator on the manifest-scoped facts. The handler still
// emits grant-authorized shared kinds at the emission edge, but Contract is
// the narrowed manifest ownership (eshu-hq/eshu#9202), so validation runs on
// the scoped copy; shared-kind shapes stay proven by the untouched loki
// emission unit tests plus live QA re-proof.
func TestHandleServesACompleteResult(t *testing.T) {
	t.Parallel()

	result, summary := testHandler(nil).Handle(
		context.Background(), testRequest(testConfig(testBaseURL, nil)),
	)
	if result.State != sdk.ResultComplete {
		t.Fatalf("state = %q, want complete", result.State)
	}
	if result.Claim != testClaim() {
		t.Fatalf("claim = %+v, want the request claim echoed", result.Claim)
	}
	byKind := factsByKind(result)
	for _, kind := range FactKinds() {
		if len(byKind[kind]) == 0 {
			t.Errorf("no fact of kind %q", kind)
		}
	}
	if summary.FactCount != len(result.Facts) {
		t.Errorf("summary fact_count = %d, want %d", summary.FactCount, len(result.Facts))
	}
	if summary.FreshnessHint == "" || summary.ResultBytes == 0 {
		t.Errorf("summary = %+v, want freshness and byte size", summary)
	}
	scoped := scopeResultToManifest(result, loadManifest(t))
	report, err := sdk.NewValidator(Contract()).ValidateResult(scoped)
	if err != nil {
		t.Fatalf("ValidateResult(scoped) error = %v, want validator-clean output", err)
	}
	if report.FactCount != len(scoped.Facts) {
		t.Errorf("report fact count = %d, want %d", report.FactCount, len(scoped.Facts))
	}
}

// TestHandleRejectsClaimsItCannotServe proves a foreign collector kind and an
// expired deadline end as bounded terminal and retryable results.
func TestHandleRejectsClaimsItCannotServe(t *testing.T) {
	t.Parallel()

	request := testRequest(testConfig(testBaseURL, nil))
	request.Claim.CollectorKind = "prometheus"
	result, summary := testHandler(nil).Handle(context.Background(), request)
	if result.State != sdk.ResultTerminal || summary.FailureClass != FailureClassInvalidClaim {
		t.Errorf("foreign kind = %q/%q, want terminal invalid_claim", result.State, summary.FailureClass)
	}

	expired := testRequest(testConfig(testBaseURL, nil))
	expired.Claim.Deadline = testObservedAt().Add(-time.Second)
	result, summary = testHandler(nil).Handle(context.Background(), expired)
	if result.State != sdk.ResultRetryable || summary.FailureClass != FailureClassDeadlineExceeded {
		t.Errorf("expired deadline = %q/%q, want retryable claim_deadline_exceeded", result.State, summary.FailureClass)
	}
}

// TestHandleFailsClosedOnBadConfig proves an unknown config key ends the
// claim terminal without any provider contact.
func TestHandleFailsClosedOnBadConfig(t *testing.T) {
	t.Parallel()

	contacted := false
	factory := func(loki.TargetConfig) (loki.EvidenceClient, error) {
		contacted = true
		return stubClient{result: testSnapshot()}, nil
	}
	handler := testHandler(factory)
	request := testRequest(testConfig(testBaseURL, map[string]any{"bogus": true}))
	result, summary := handler.Handle(context.Background(), request)
	if result.State != sdk.ResultTerminal || summary.FailureClass != FailureClassConfigInvalid {
		t.Errorf("bad config = %q/%q, want terminal config_invalid", result.State, summary.FailureClass)
	}
	if contacted {
		t.Error("bad config reached the provider")
	}
}

// TestHandleMapsProviderFailures proves retryable and terminal provider
// failures keep their fate and bounded class, with no provider text.
func TestHandleMapsProviderFailures(t *testing.T) {
	t.Parallel()

	for name, tc := range map[string]struct {
		err       error
		state     sdk.ResultState
		class     string
		operation string
		status    int
	}{
		"retryable": {
			err:   transport.NewProviderFailure("loki", transport.FailureRetryable, false, errors.New("HOSTILE-BODY")).WithOperation("labels"),
			state: sdk.ResultRetryable, class: "retryable", operation: "labels",
		},
		"terminal": {
			err:   transport.NewProviderFailure("loki", transport.FailureTerminal, true, errors.New("HOSTILE-BODY")).WithOperation("rules"),
			state: sdk.ResultTerminal, class: "terminal", operation: "rules",
		},
		"rate limited": {
			err:   transport.NewProviderFailure("loki", transport.FailureRateLimited, false, errors.New("HOSTILE-BODY")),
			state: sdk.ResultRetryable, class: "rate_limited",
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			factory := func(loki.TargetConfig) (loki.EvidenceClient, error) {
				return stubClient{err: tc.err}, nil
			}
			result, summary := testHandler(factory).Handle(
				context.Background(), testRequest(testConfig(testBaseURL, nil)),
			)
			if result.State != tc.state || summary.FailureClass != tc.class {
				t.Fatalf("= %q/%q, want %q/%q", result.State, summary.FailureClass, tc.state, tc.class)
			}
			if summary.FailureOperation != tc.operation || summary.HTTPStatus != tc.status {
				t.Fatalf("summary = %+v, want op %q status %d", summary, tc.operation, tc.status)
			}
			for _, status := range result.Statuses {
				if strings.Contains(status.FailureClass, "HOSTILE") {
					t.Fatalf("status carries provider text: %+v", status)
				}
			}
		})
	}
}

// TestHandleKeepsTheConfiguredScope proves a claim for a scope the activation
// does not own ends retryable: the target may appear on a later claim.
func TestHandleKeepsTheConfiguredScope(t *testing.T) {
	t.Parallel()

	request := testRequest(testConfig(testBaseURL, nil))
	request.Claim.Scope.ID = "loki:tenant:other"
	result, summary := testHandler(nil).Handle(context.Background(), request)
	if result.State != sdk.ResultRetryable {
		t.Errorf("foreign scope = %q/%q, want retryable", result.State, summary.FailureClass)
	}
}

// TestHandleFailsTerminalOnOversizedResult proves the byte bound ends the
// claim before the host's standard-output limit can truncate it.
func TestHandleFailsTerminalOnOversizedResult(t *testing.T) {
	t.Parallel()

	request := testRequest(testConfig(testBaseURL, map[string]any{}))
	request.Config["limits"] = map[string]any{"max_result_bytes": 10}
	result, summary := testHandler(nil).Handle(context.Background(), request)
	if result.State != sdk.ResultTerminal || summary.FailureClass != FailureClassResultTooLarge {
		t.Errorf("oversized = %q/%q, want terminal result_too_large", result.State, summary.FailureClass)
	}
}

// TestProducerGrantPassesCheck proves the per-claim emission grant satisfies
// the fail-closed edge it is checked against.
func TestProducerGrantPassesCheck(t *testing.T) {
	t.Parallel()

	if err := grant.Check(emissionGrant(testObservedAt()), testObservedAt()); err != nil {
		t.Fatalf("emissionGrant() fails its own check: %v", err)
	}
}
