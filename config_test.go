// SPDX-License-Identifier: MIT
// Copyright (c) 2025-2026 eshu-hq

package collector

import (
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"
)

func baseTarget() map[string]any {
	return map[string]any{
		"scope_id":    "loki:tenant:test",
		"instance_id": "test",
		"base_url":    "http://loki.example.invalid",
		"enabled":     true,
	}
}

func baseConfig() map[string]any {
	return map[string]any{"targets": []any{baseTarget()}}
}

func withConfig(overrides map[string]any) map[string]any {
	target := baseTarget()
	for key, value := range overrides {
		target[key] = value
	}
	return map[string]any{"targets": []any{target}}
}

func withLimits(overrides map[string]any, maxResult int) map[string]any {
	config := withConfig(overrides)
	config["limits"] = map[string]any{"max_result_bytes": maxResult}
	return config
}

// TestParseConfigDefaultsAndMapping proves the minimal config resolves the
// in-tree defaults and the full config maps every in-tree target field.
func TestParseConfigDefaultsAndMapping(t *testing.T) {
	t.Parallel()

	env := envWith(map[string]string{"LOKI_TOKEN": "  secret-value  ", "LOKI_TENANT": "  tenant-1  "})
	minimal, err := ParseConfig(baseConfig(), env)
	if err != nil {
		t.Fatalf("ParseConfig(minimal) error = %v", err)
	}
	if len(minimal.Targets) != 1 {
		t.Fatalf("targets = %d, want 1", len(minimal.Targets))
	}
	if minimal.MaxResultBytes != DefaultMaxResultBytes {
		t.Fatalf("max result = %d, want %d", minimal.MaxResultBytes, DefaultMaxResultBytes)
	}

	full, err := ParseConfig(map[string]any{"targets": []any{map[string]any{
		"scope_id": " loki:tenant:test ", "instance_id": " test ",
		"base_url": " http://loki.example.invalid/ ", "path_prefix": " /loki/ ",
		"token_env": " LOKI_TOKEN ", "tenant_id_env": " LOKI_TENANT ",
		"resource_limit": 25, "label_value_names": []any{" __name__ ", "", "__name__"},
		"max_label_values_per_label": 10, "series_matchers": []any{`{job="x"}`, " "},
		"series_lookback": "12h", "stale_after": "24h", "enabled": true,
		"declared_ids": []any{" a ", "a", ""}, "observed_only_hint": true,
	}}, "limits": map[string]any{"max_result_bytes": 1000000}}, env)
	if err != nil {
		t.Fatalf("ParseConfig(full) error = %v", err)
	}
	target := full.Targets[0]
	if target.ScopeID != "loki:tenant:test" || target.InstanceID != "test" ||
		target.BaseURL != "http://loki.example.invalid" || target.PathPrefix != "/loki/" {
		t.Fatalf("identity = %+v", target)
	}
	if target.Token != "secret-value" || target.TenantID != "tenant-1" {
		t.Fatalf("credentials = %+v, want the trimmed environment values", target)
	}
	if target.ResourceLimit != 25 || target.MaxLabelValuesPerLabel != 10 {
		t.Fatalf("limits = %+v", target)
	}
	if !reflect.DeepEqual(target.LabelValueNames, []string{"__name__"}) {
		t.Fatalf("LabelValueNames = %v, want trimmed and de-duplicated", target.LabelValueNames)
	}
	if !reflect.DeepEqual(target.SeriesMatchers, []string{`{job="x"}`}) {
		t.Fatalf("SeriesMatchers = %v, want trimmed and de-duplicated", target.SeriesMatchers)
	}
	if target.SeriesLookback != 12*time.Hour || target.StaleAfter != 24*time.Hour {
		t.Fatalf("durations = %+v", target)
	}
	if !target.Enabled || !target.ObservedOnlyHint {
		t.Fatalf("flags = %+v", target)
	}
	if !reflect.DeepEqual(target.DeclaredIDs, map[string]struct{}{"a": {}}) {
		t.Fatalf("DeclaredIDs = %v, want the trimmed set", target.DeclaredIDs)
	}
	if full.MaxResultBytes != 1000000 {
		t.Fatalf("max_result_bytes = %d, want 1000000", full.MaxResultBytes)
	}
}

// TestParseConfigPrefersTenantEnvOverLiteral proves the env reference wins
// when both are set, mirroring the in-tree override rule.
func TestParseConfigPrefersTenantEnvOverLiteral(t *testing.T) {
	t.Parallel()

	config, err := ParseConfig(
		withConfig(map[string]any{"tenant_id": "literal", "tenant_id_env": "LOKI_TENANT"}),
		envWith(map[string]string{"LOKI_TENANT": "from-env"}),
	)
	if err != nil {
		t.Fatalf("ParseConfig() error = %v", err)
	}
	if config.Targets[0].TenantID != "from-env" {
		t.Fatalf("TenantID = %q, want the env value", config.Targets[0].TenantID)
	}
}

// TestParseConfigRejectsInvalidValues proves every invalid shape fails closed
// with a bounded class, and that no error text carries a credential or tenant.
func TestParseConfigRejectsInvalidValues(t *testing.T) {
	t.Parallel()

	env := envWith(map[string]string{"LOKI_TOKEN": "TOP-SECRET-VALUE", "LOKI_TENANT": "SECRET-TENANT"})
	tests := []struct {
		name   string
		config map[string]any
		env    func(string) string
		class  string
	}{
		{"nil config", nil, env, FailureClassConfigInvalid},
		{"empty config", map[string]any{}, env, FailureClassConfigInvalid},
		{"missing targets", map[string]any{"limits": map[string]any{"max_result_bytes": 1}}, env, FailureClassConfigInvalid},
		{"empty targets", map[string]any{"targets": []any{}}, env, FailureClassConfigInvalid},
		{"targets wrong type", map[string]any{"targets": "x"}, env, FailureClassConfigInvalid},
		{"unknown top-level key", withConfig(map[string]any{"token": "TOP-SECRET-VALUE"}), env, FailureClassConfigInvalid},
		{"unknown target key", withConfig(map[string]any{"api_token": "TOP-SECRET-VALUE"}), env, FailureClassConfigInvalid},
		{"unknown limits key", func() map[string]any {
			config := baseConfig()
			config["limits"] = map[string]any{"max_result_bytes": 1, "bogus": 2}
			return config
		}(), env, FailureClassConfigInvalid},
		{"string resource limit", withConfig(map[string]any{"resource_limit": "ten"}), env, FailureClassConfigInvalid},
		{"token_env that is not a name", withConfig(map[string]any{"token_env": "TOP-SECRET-VALUE"}), env, FailureClassConfigInvalid},
		{"token_env with a shell fragment", withConfig(map[string]any{"token_env": "X; rm -rf /"}), env, FailureClassConfigInvalid},
		{"tenant_id_env with a shell fragment", withConfig(map[string]any{"tenant_id_env": "X; rm -rf /"}), env, FailureClassConfigInvalid},
		{"unresolved token", withConfig(map[string]any{"token_env": "LOKI_TOKEN"}), envWith(nil), FailureClassCredentialMissing},
		{"whitespace token", withConfig(map[string]any{"token_env": "LOKI_TOKEN"}), envWith(map[string]string{"LOKI_TOKEN": "   "}), FailureClassCredentialMissing},
		{"unresolved tenant", withConfig(map[string]any{"tenant_id_env": "LOKI_TENANT"}), envWith(nil), FailureClassCredentialMissing},
		{"bad stale_after", withConfig(map[string]any{"stale_after": "yesterday"}), env, FailureClassConfigInvalid},
		{"negative stale_after", withConfig(map[string]any{"stale_after": "-1h"}), env, FailureClassConfigInvalid},
		{"bad series_lookback", withConfig(map[string]any{"series_lookback": "yesterday"}), env, FailureClassConfigInvalid},
		{"labels wrong type", withConfig(map[string]any{"label_value_names": "x"}), env, FailureClassConfigInvalid},
		{"max result negative", withLimits(nil, -1), env, FailureClassConfigInvalid},
		{"max result over ceiling", withLimits(nil, 64<<20+1), env, FailureClassConfigInvalid},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			_, err := ParseConfig(test.config, test.env)
			var cfgErr configError
			if !errors.As(err, &cfgErr) {
				t.Fatalf("error = %v, want a configError", err)
			}
			if cfgErr.class != test.class {
				t.Fatalf("class = %q, want %q (%v)", cfgErr.class, test.class, err)
			}
			if strings.Contains(err.Error(), "TOP-SECRET-VALUE") || strings.Contains(err.Error(), "SECRET-TENANT") {
				t.Fatalf("error text carries a credential: %v", err)
			}
		})
	}
}

// TestUnresolvedTokenWithoutEnvNameSkipsCredential proves a target with no
// token_env resolves no credential instead of failing: the in-cluster gateway
// needs none.
func TestUnresolvedTokenWithoutEnvNameSkipsCredential(t *testing.T) {
	t.Parallel()

	config, err := ParseConfig(baseConfig(), envWith(nil))
	if err != nil {
		t.Fatalf("ParseConfig() error = %v", err)
	}
	if config.Targets[0].Token != "" {
		t.Fatalf("Token = %q, want empty", config.Targets[0].Token)
	}
}
