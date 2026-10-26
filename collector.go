// SPDX-License-Identifier: MIT
// Copyright (c) 2025-2026 eshu-hq

package collector

import (
	"time"

	sdk "github.com/eshu-hq/eshu-sdk/collector"

	"github.com/eshu-hq/eshu-collector-loki/grant"
	"github.com/eshu-hq/eshu-collector-loki/loki"
)

const (
	// ComponentID is the manifest identity. It must match manifest.yaml
	// metadata.id, the grant.ProducerID the emission edge checks, and the
	// producer grants issued for this component.
	ComponentID = "dev.eshu.collector.loki"
	// CollectorKind is the collector family the host routes claims by. A
	// producer grant's scope is this collector kind.
	CollectorKind = loki.CollectorKind
	// SourceSystem is the source system the activation declares and the
	// in-tree collector uses.
	SourceSystem = loki.CollectorKind
	// ScopeKind is the scope kind the activation declares for one Loki API
	// target.
	ScopeKind = loki.ScopeKindLogSource
	// MetricsPrefix is the component-owned metric prefix from the manifest.
	MetricsPrefix = "eshu_dp_loki_collector_"
)

// FactKinds returns the fact kinds this collector manifest-owns, in manifest
// order: the full four-kind emission (arbiter verdict eshu-hq/eshu#9202,
// satellite release v0.1.2). The emission edge still checks the
// core-issued producer grant via grant.CoveredKinds before any provider
// contact.
func FactKinds() []string {
	return []string{
		loki.KindSourceInstance,
		loki.KindObservedLogSignal,
		loki.KindObservedRule,
		loki.KindCoverageWarning,
	}
}

// Contract returns the SDK fact families this collector may emit. It must
// match manifest.yaml emittedFacts; manifest_test.go enforces that.
func Contract() sdk.Contract {
	kinds := FactKinds()
	declarations := make([]sdk.FactDeclaration, 0, len(kinds))
	for _, kind := range kinds {
		declarations = append(declarations, sdk.FactDeclaration{
			Kind:             kind,
			SchemaVersions:   []string{loki.SchemaVersionV1},
			SourceConfidence: []sdk.SourceConfidence{sdk.SourceConfidenceReported},
		})
	}
	return sdk.Contract{ProtocolVersion: sdk.ProtocolVersionV1Alpha1, Facts: declarations}
}

// producerGrant builds the emission-edge grant for one claim. The host is the
// real issuance gate: the extension host refuses to activate the component
// and to issue claims without live core-issued producer grants covering every
// manifest kind, so a claim reaching this process already passed that gate.
// The in-process check keeps the fail-closed emission shape against code
// drift (wrong identity, version, scope, kind set, or lifetime still denies
// before any provider contact). The lifetime is one claim, bounded by
// grant.MaxLifetime.
func emissionGrant(now time.Time) grant.Grant {
	now = now.UTC()
	kinds := make([]grant.KindGrant, 0, len(grant.CoveredKinds()))
	for _, kind := range grant.CoveredKinds() {
		kinds = append(kinds, grant.KindGrant{Kind: kind, SchemaVersions: []string{grant.SchemaVersionV1}})
	}
	return grant.Grant{
		ProducerID: grant.ProducerID,
		Version:    grant.Version,
		Scope:      grant.Scope,
		Kinds:      kinds,
		IssuedAt:   now,
		ExpiresAt:  now.Add(time.Hour),
	}
}
