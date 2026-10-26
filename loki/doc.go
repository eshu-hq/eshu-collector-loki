// SPDX-License-Identifier: MIT
// Copyright (c) 2025-2026 eshu-hq

// Package loki collects bounded live Loki metadata as observability source
// facts for the collector-sdk/v1alpha1 wire contract.
//
// The package reads configured Loki API targets and emits metadata-only
// observability facts: observability.observed_log_signal is the manifest-owned
// kind (eshu-hq/eshu#9202), while observability.source_instance,
// observability.observed_rule, and observability.coverage_warning stay
// grant-authorized at the emission edge. It never persists log lines, raw
// LogQL, label values, tenant IDs, tenant headers, credentials, or provider
// response bodies. Core owns admission,
// projection, persistence, claim mutation, fencing, and declared/applied/
// observed comparison: this package returns a collector.Result per host
// claim and enforces the core-issued producer grant (see the grant
// package) at every emission.
//
// Extracted from eshu-hq/eshu go/internal/collector/observability/loki
// (#8860). Contract payloads merge through the pinned
// github.com/eshu-hq/eshu-sdk/factschema module; HTTP transport and failure
// classification come from the pinned
// github.com/eshu-hq/eshu-sdk/collector/transport package.
package loki
