// SPDX-License-Identifier: MIT
// Copyright (c) 2025-2026 eshu-hq

// Package grant enforces the core-issued producer grant at the emission
// edge of the extracted Loki collector.
//
// The grant is issued by core under docs/public/reference/
// component-producer-grants.md (#6709) and installed at cutover under the
// #4047 gate. The draft terms live in PRODUCER_GRANT_DRAFT.md in
// eshu-hq/eshu (go/internal/collector/observability/loki, #8860):
// producer dev.eshu.collector.loki, version 0.1.0, scope loki, authority
// mode core_issued_grant, covering the four observed kinds at schema 1.0.0
// with a bounded lifetime.
//
// STUB: Check verifies grant shape, producer identity, version, scope,
// kind coverage, schema versions, and expiry only. It does NOT verify a
// core issuance signature — no signature scheme exists yet, and issuance
// itself is core-owned. Until core issues and the host delivers a grant,
// the zero Grant is denied and every emission fails closed.
package grant

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

const (
	// ProducerID is the first-party producer identity from the grant draft.
	ProducerID = "dev.eshu.collector.loki"
	// Version is the draft producer version. It must match
	// metadata.version at admission.
	Version = "0.1.0"
	// Scope must match the manifest-declared collector kind at admission.
	Scope = "loki"
	// SchemaVersionV1 is the schema version every covered kind carries.
	SchemaVersionV1 = "1.0.0"
	// MaxLifetime bounds grant duration: no non-expiring grants. The
	// draft's issuance example uses --expires-in 2160h; anything longer
	// fails closed.
	MaxLifetime = 2160 * time.Hour
)

// coveredKinds lists the core-owned fact kinds this producer may emit
// after cutover. Declared Loki evidence stays core/GitOps-owned and is
// NOT covered: the extracted collector must never emit declared kinds.
// observability.observed_log_signal is emitted only by this producer; the
// other three kinds are shared with the grafana, prometheus-mimir, and
// tempo collectors for their own providers, and the grant's producer
// identity plus scope is what separates authorities, not the kind names.
// Unexported so no importer can shrink or widen coverage and still pass
// Check; read a copy through CoveredKinds.
var coveredKinds = []string{
	"observability.source_instance",
	"observability.observed_log_signal",
	"observability.observed_rule",
	"observability.coverage_warning",
}

// CoveredKinds returns a copy of the covered kind set. The copy defends
// the fail-closed claim: mutating the returned slice cannot change what
// Check enforces.
func CoveredKinds() []string {
	out := make([]string, len(coveredKinds))
	copy(out, coveredKinds)
	return out
}

// ErrDenied is the fail-closed denial returned when no usable core-issued
// grant is present. Every Check failure wraps it.
var ErrDenied = errors.New("loki producer grant denied")

// KindGrant covers one core-owned fact kind at explicit schema versions.
type KindGrant struct {
	Kind           string
	SchemaVersions []string
}

// Grant is the host-delivered core-issued producer grant under check at
// every emission.
type Grant struct {
	ProducerID string
	Version    string
	Scope      string
	Kinds      []KindGrant
	IssuedAt   time.Time
	ExpiresAt  time.Time
}

// Check enforces the grant draft at the emission edge and fails closed on
// any mismatch. A zero now falls back to the current time.
func Check(g Grant, now time.Time) error {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	if strings.TrimSpace(g.ProducerID) == "" {
		return fmt.Errorf("%w: no grant installed", ErrDenied)
	}
	if g.ProducerID != ProducerID {
		return fmt.Errorf("%w: producer %q is not %q", ErrDenied, g.ProducerID, ProducerID)
	}
	if g.Version != Version {
		return fmt.Errorf("%w: version %q does not match %q", ErrDenied, g.Version, Version)
	}
	if g.Scope != Scope {
		return fmt.Errorf("%w: scope %q does not match %q", ErrDenied, g.Scope, Scope)
	}
	if err := checkCoverage(g.Kinds); err != nil {
		return err
	}
	if g.IssuedAt.IsZero() {
		return fmt.Errorf("%w: issued_at is required", ErrDenied)
	}
	if g.IssuedAt.After(now) {
		return fmt.Errorf("%w: grant issued in the future", ErrDenied)
	}
	if g.ExpiresAt.IsZero() || !g.ExpiresAt.After(now) {
		return fmt.Errorf("%w: grant is expired or has no expiry", ErrDenied)
	}
	lifetime := g.ExpiresAt.Sub(g.IssuedAt)
	if lifetime <= 0 {
		return fmt.Errorf("%w: grant lifetime must be positive", ErrDenied)
	}
	if lifetime > MaxLifetime {
		return fmt.Errorf("%w: grant lifetime %s exceeds %s", ErrDenied, lifetime, MaxLifetime)
	}
	return nil
}

// checkCoverage requires exactly the draft kind set, each at schema 1.0.0.
// Extra kinds are denied: after cutover this producer owns only live
// observed evidence, never declared kinds.
func checkCoverage(kinds []KindGrant) error {
	if len(kinds) != len(coveredKinds) {
		return fmt.Errorf("%w: grant covers %d kinds, want %d", ErrDenied, len(kinds), len(coveredKinds))
	}
	covered := make(map[string]string, len(kinds))
	for _, kind := range kinds {
		covered[kind.Kind] = strings.Join(kind.SchemaVersions, ",")
	}
	for _, want := range coveredKinds {
		versions, ok := covered[want]
		if !ok {
			return fmt.Errorf("%w: grant does not cover kind %q", ErrDenied, want)
		}
		if versions != SchemaVersionV1 {
			return fmt.Errorf("%w: kind %q covers schemas %q, want %q", ErrDenied, want, versions, SchemaVersionV1)
		}
	}
	return nil
}
