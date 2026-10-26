// SPDX-License-Identifier: MIT
// Copyright (c) 2025-2026 eshu-hq

package collector

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"time"

	sdk "github.com/eshu-hq/eshu-sdk/collector"

	"github.com/eshu-hq/eshu-collector-loki/grant"
	"github.com/eshu-hq/eshu-collector-loki/loki"
)

// Handler turns one host Request into one SDK Result.
type Handler struct {
	// Getenv reads the process environment; nil selects os.Getenv. The
	// activation config names the variables holding the token and tenant ID.
	Getenv func(string) string
	// Now is the collection clock; nil selects time.Now. The emission grant
	// window and the result generation observation time come from it.
	Now func() time.Time
	// HTTPClient performs Loki requests; nil selects the bounded default
	// client that does not follow redirects. Tests inject a stub doer.
	HTTPClient loki.HTTPDoer
	// ClientFactory builds the evidence client per target; nil selects the
	// HTTP client above. Tests inject a stub factory.
	ClientFactory loki.ClientFactory
}

// Summary is the bounded view of one Handle call, written to stderr for a
// local run (see Health: the host drops stderr text today). It carries counts
// and bounded identifiers only: never a token, tenant ID, URL, or provider
// body.
type Summary struct {
	State            sdk.ResultState `json:"state"`
	FailureClass     string          `json:"failure_class,omitempty"`
	FailureOperation string          `json:"failure_operation,omitempty"`
	HTTPStatus       int             `json:"http_status,omitempty"`
	FactCount        int             `json:"fact_count"`
	FactsByKind      map[string]int  `json:"facts_by_kind,omitempty"`
	WarningCount     int             `json:"warning_count,omitempty"`
	ResultBytes      int             `json:"result_bytes,omitempty"`
	FreshnessHint    string          `json:"freshness_hint,omitempty"`
	DurationMS       int64           `json:"duration_ms"`
}

func (h Handler) getenv(key string) string {
	if h.Getenv != nil {
		return h.Getenv(key)
	}
	return os.Getenv(key)
}

func (h Handler) now() time.Time {
	if h.Now != nil {
		return h.Now()
	}
	return time.Now()
}

const (
	// maxDeadlineMargin caps how long before the claim deadline the collector
	// stops starting provider calls.
	maxDeadlineMargin = 2 * time.Second
	// deadlineMarginDivisor makes the margin 5% of the remaining claim time.
	deadlineMarginDivisor = 20
)

// clampedDeadlineTimeout returns how long the collector may work on a claim:
// the time left until deadline minus a safety margin of min(2s, 5% of the
// time left). The host kills the process at the claim deadline, which is the
// initial lease expiry and is not extended by heartbeats, so the collector
// stops a little earlier and ends with a retryable result of its own instead
// of being killed mid-request. It reports false when there is no deadline or
// none left; the caller's expired-deadline check handles the latter.
func clampedDeadlineTimeout(now, deadline time.Time) (time.Duration, bool) {
	if deadline.IsZero() {
		return 0, false
	}
	remaining := deadline.Sub(now)
	if remaining <= 0 {
		return 0, false
	}
	margin := min(maxDeadlineMargin, remaining/deadlineMarginDivisor)
	return remaining - margin, true
}

// Handle runs one claim. It never returns an error: every outcome, including
// a configuration problem or a provider failure, is a Result whose state the
// host maps to a claim action. The returned claim equals the request claim, as
// the host requires.
func (h Handler) Handle(ctx context.Context, request Request) (sdk.Result, Summary) {
	claim := request.Claim
	startedAt := h.now().UTC()
	if timeout, ok := clampedDeadlineTimeout(startedAt, claim.Deadline); ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	finish := func(result sdk.Result, summary Summary) (sdk.Result, Summary) {
		summary.State = result.State
		summary.DurationMS = h.now().UTC().Sub(startedAt).Milliseconds()
		return result, summary
	}
	fail := func(class string, terminal bool, retryAfter time.Duration) (sdk.Result, Summary) {
		return finish(failureResult(claim, startedAt, class, terminal, retryAfter), Summary{FailureClass: class})
	}

	if claim.CollectorKind != CollectorKind {
		return fail(FailureClassInvalidClaim, true, 0)
	}
	if !claim.Deadline.IsZero() && !startedAt.Before(claim.Deadline) {
		return fail(FailureClassDeadlineExceeded, false, 0)
	}
	config, err := ParseConfig(request.Config, h.getenv)
	if err != nil {
		var cfgErr configError
		if errors.As(err, &cfgErr) {
			return fail(cfgErr.class, true, 0)
		}
		return fail(FailureClassConfigInvalid, true, 0)
	}
	if len(config.Targets) == 0 {
		return fail(FailureClassConfigInvalid, true, 0)
	}
	factory := h.ClientFactory
	if factory == nil {
		doer := h.HTTPClient
		factory = func(target loki.TargetConfig) (loki.EvidenceClient, error) {
			return loki.NewHTTPClient(loki.HTTPClientConfig{BaseURL: target.BaseURL, Client: doer})
		}
	}
	source, err := loki.NewClaimedSource(loki.SourceConfig{
		CollectorInstanceID: claim.InstanceID,
		Targets:             config.Targets,
		ClientFactory:       factory,
		Now:                 h.now,
	})
	if err != nil {
		return fail(FailureClassConfigInvalid, true, 0)
	}

	result, err := source.Collect(ctx, claim, emissionGrant(startedAt))
	if err != nil {
		return h.collectFailure(ctx, claim, startedAt, err, finish)
	}

	encoded, err := json.Marshal(result)
	if err != nil {
		return fail(FailureClassFactInvalid, true, 0)
	}
	if len(encoded) > config.MaxResultBytes {
		return fail(FailureClassResultTooLarge, true, 0)
	}
	summary := Summary{
		FactCount:     len(result.Facts),
		FactsByKind:   countByKind(result.Facts),
		WarningCount:  warningCount(result.Statuses),
		ResultBytes:   len(encoded),
		FreshnessHint: result.Generation.FreshnessHint,
	}
	return finish(result, summary)
}

// collectFailure maps a Collect error to a bounded Result. A provider failure
// keeps its retryable-vs-terminal fate and retry guidance; a grant denial or
// an envelope failure is terminal, because retrying the same claim against
// the same code cannot change it.
func (h Handler) collectFailure(
	ctx context.Context,
	claim sdk.Claim,
	observedAt time.Time,
	err error,
	finish func(sdk.Result, Summary) (sdk.Result, Summary),
) (sdk.Result, Summary) {
	var failure loki.ProviderFailure
	if errors.As(err, &failure) {
		class := failure.FailureClass()
		terminal := failure.TerminalFailure()
		switch {
		case errors.Is(ctx.Err(), context.DeadlineExceeded):
			class, terminal = FailureClassDeadlineExceeded, false
		case errors.Is(ctx.Err(), context.Canceled):
			class, terminal = FailureClassCanceled, false
		}
		return finish(
			failureResult(claim, observedAt, class, terminal, failure.RetryAfterDelay()),
			Summary{FailureClass: class, FailureOperation: failure.FailureOperation(), HTTPStatus: failure.FailureStatusCode()},
		)
	}
	class := FailureClassFactInvalid
	if errors.Is(err, grant.ErrDenied) {
		class = FailureClassInvalidClaim
	}
	return finish(
		failureResult(claim, observedAt, class, true, 0),
		Summary{FailureClass: class},
	)
}

func countByKind(facts []sdk.Fact) map[string]int {
	counts := map[string]int{}
	for _, fact := range facts {
		counts[fact.Kind]++
	}
	return counts
}

func warningCount(statuses []sdk.Status) int {
	total := 0
	for _, status := range statuses {
		total += status.WarningCount
	}
	return total
}
