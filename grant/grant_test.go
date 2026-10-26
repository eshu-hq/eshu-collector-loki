// SPDX-License-Identifier: MIT
// Copyright (c) 2025-2026 eshu-hq

package grant

import (
	"strings"
	"testing"
	"time"
)

func draftGrant() Grant {
	now := time.Now().UTC()
	return Grant{
		ProducerID: ProducerID,
		Version:    Version,
		Scope:      Scope,
		Kinds: []KindGrant{
			{Kind: "observability.source_instance", SchemaVersions: []string{SchemaVersionV1}},
			{Kind: "observability.observed_log_signal", SchemaVersions: []string{SchemaVersionV1}},
			{Kind: "observability.observed_rule", SchemaVersions: []string{SchemaVersionV1}},
			{Kind: "observability.coverage_warning", SchemaVersions: []string{SchemaVersionV1}},
		},
		IssuedAt:  now,
		ExpiresAt: now.Add(720 * time.Hour),
	}
}

func TestCheckAcceptsDraftShapedGrant(t *testing.T) {
	t.Parallel()

	if err := Check(draftGrant(), time.Now().UTC()); err != nil {
		t.Fatalf("Check(draft grant) error = %v, want nil", err)
	}
}

func TestCheckFailsClosed(t *testing.T) {
	t.Parallel()

	good := draftGrant()
	now := good.IssuedAt
	tests := []struct {
		name   string
		mutate func(*Grant)
	}{
		{"zero grant", func(g *Grant) { *g = Grant{} }},
		{"foreign producer", func(g *Grant) { g.ProducerID = "dev.eshu.collector.grafana" }},
		{"wrong version", func(g *Grant) { g.Version = "9.9.9" }},
		{"wrong scope", func(g *Grant) { g.Scope = "grafana" }},
		{"missing kind", func(g *Grant) { g.Kinds = g.Kinds[:3] }},
		{"extra declared kind", func(g *Grant) {
			g.Kinds = append(g.Kinds, KindGrant{Kind: "observability.declared_dashboard", SchemaVersions: []string{SchemaVersionV1}})
		}},
		{"wrong schema version", func(g *Grant) { g.Kinds[1].SchemaVersions = []string{"9.9.9"} }},
		{"missing issued_at", func(g *Grant) { g.IssuedAt = time.Time{} }},
		{"expired", func(g *Grant) { g.ExpiresAt = now.Add(-time.Hour) }},
		{"no expiry", func(g *Grant) { g.ExpiresAt = time.Time{} }},
		{"lifetime exceeds maximum", func(g *Grant) { g.ExpiresAt = g.IssuedAt.Add(MaxLifetime + time.Hour) }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := good
			g.Kinds = append([]KindGrant(nil), good.Kinds...)
			tt.mutate(&g)
			if err := Check(g, now); err == nil {
				t.Errorf("Check(%s) = nil, want fail-closed denial", tt.name)
			} else if !strings.Contains(err.Error(), ErrDenied.Error()) {
				t.Errorf("Check(%s) error = %v, want grant.ErrDenied", tt.name, err)
			}
		})
	}
}

func TestCoveredKindsCopyCannotWeakenCheck(t *testing.T) {
	t.Parallel()

	covered := CoveredKinds()
	if len(covered) != 4 {
		t.Fatalf("len(CoveredKinds()) = %d, want 4", len(covered))
	}
	covered[0] = "observability.declared_dashboard"
	again := CoveredKinds()
	if again[0] == "observability.declared_dashboard" {
		t.Fatal("CoveredKinds() exposes shared state: mutating the copy weakened coverage")
	}
}
