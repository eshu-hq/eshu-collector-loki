// SPDX-License-Identifier: MIT
// Copyright (c) 2025-2026 eshu-hq

// Package collector is the Eshu Loki collector, packaged as a component
// extension that the Eshu component-extension host runs with the process
// runner.
//
// The host writes one bounded collector-sdk/v1alpha1 Request to the process's
// standard input and reads one collector-sdk/v1alpha1 Result from its standard
// output. The package turns that Request into bounded Loki REST reads and the
// Result into the observability fact kinds the Eshu core already owns:
// observability.source_instance, observability.observed_log_signal,
// observability.observed_rule, and observability.coverage_warning. The host
// builds the durable envelope, validates the Result, and owns claims, leases,
// and commits.
//
// The producer never persists log lines, raw LogQL, label values, tenant IDs,
// tenant headers, credentials, or provider response bodies. Provider failures
// return as bounded failure classes; the producer never decides
// retryable-vs-terminal claim fate beyond the class it reports.
//
// Boundary: this package imports only the published SDK modules
// (github.com/eshu-hq/eshu-sdk/collector,
// github.com/eshu-hq/eshu-sdk/factschema), the standard library, yaml, and
// this module's own packages. It never imports go/internal from the core
// module, another collector implementation, or any Postgres, graph, queue, or
// lease handle. boundary_test.go enforces that, and the extraction policy in
// eshu-hq/eshu#4047 depends on it.
//
// This repository holds no producer authority until its reviewed cutover
// (eshu-hq/eshu#9202).
package collector
