// SPDX-License-Identifier: MIT
// Copyright (c) 2025-2026 eshu-hq

package collector

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/eshu-hq/eshu-collector-loki/loki"
)

// Failure classes a configuration problem maps to. They are bounded
// identifiers safe for result statuses.
const (
	// FailureClassConfigInvalid marks an activation config that cannot run.
	FailureClassConfigInvalid = "config_invalid"
	// FailureClassCredentialMissing marks a token_env or tenant_id_env name
	// that resolved no credential in the process environment.
	FailureClassCredentialMissing = "credential_missing"
	// FailureClassInvalidClaim marks a claim this collector cannot serve.
	FailureClassInvalidClaim = "invalid_claim"
	// FailureClassResultTooLarge marks a result that exceeds max_result_bytes.
	FailureClassResultTooLarge = "result_too_large"
	// FailureClassFactInvalid marks provider data that could not be converted
	// into a valid fact.
	FailureClassFactInvalid = "fact_invalid"
	// FailureClassDeadlineExceeded marks a claim whose deadline passed.
	FailureClassDeadlineExceeded = "claim_deadline_exceeded"
)

const (
	// DefaultMaxResultBytes keeps one result under the host's default 4 MiB
	// standard-output limit.
	DefaultMaxResultBytes = 3500000
	// maxResultBytesCeiling bounds an operator-raised max_result_bytes.
	maxResultBytesCeiling = 64 << 20
)

var envNamePattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,127}$`)

// configError is a configuration failure carrying its bounded failure class.
// Its text never includes a credential, a token, a tenant ID, a URL, or an
// environment value. It may name the environment variable that failed to
// resolve, never the value it held.
type configError struct {
	class string
	msg   string
}

func (e configError) Error() string { return e.msg }

// rawTarget is one Loki API target as it appears under `config.targets` in
// the activation file. Field names mirror the in-tree collector's target
// JSON (go/cmd/collector-loki in eshu-hq/eshu).
type rawTarget struct {
	ScopeID                string   `json:"scope_id"`
	InstanceID             string   `json:"instance_id"`
	BaseURL                string   `json:"base_url"`
	PathPrefix             string   `json:"path_prefix"`
	TokenEnv               string   `json:"token_env"`
	TenantID               string   `json:"tenant_id"`
	TenantIDEnv            string   `json:"tenant_id_env"`
	ResourceLimit          int      `json:"resource_limit"`
	LabelValueNames        []string `json:"label_value_names"`
	MaxLabelValuesPerLabel int      `json:"max_label_values_per_label"`
	SeriesMatchers         []string `json:"series_matchers"`
	SeriesLookback         string   `json:"series_lookback"`
	StaleAfter             string   `json:"stale_after"`
	Enabled                bool     `json:"enabled"`
	DeclaredIDs            []string `json:"declared_ids"`
	ObservedOnlyHint       bool     `json:"observed_only_hint"`
}

// rawLimits bounds the encoded result. Only max_result_bytes is enforced;
// any other key fails closed so an operator's typo cannot silently widen or
// narrow the bound.
type rawLimits struct {
	MaxResultBytes int `json:"max_result_bytes"`
}

// rawConfig is the activation config as it appears under `config:` in the
// activation file.
type rawConfig struct {
	Targets []rawTarget `json:"targets"`
	Limits  *rawLimits  `json:"limits"`
}

// Config is the validated activation config: mapped Loki targets plus the
// result byte bound. Semantic target validation (required scope, instance,
// base URL, limit bounds) stays with loki.NewClaimedSource at Handle time,
// mirroring the in-tree layering.
type Config struct {
	// Targets are the mapped Loki API targets, in activation order.
	Targets []loki.TargetConfig
	// MaxResultBytes bounds the encoded result; zero selects the default.
	MaxResultBytes int
}

// ParseConfig validates the Request config map and resolves token_env and
// tenant_id_env references from the environment. getenv reads the process
// environment. Unknown keys, wrong types, unresolvable env references, and
// out-of-range bounds fail closed.
func ParseConfig(raw map[string]any, getenv func(string) string) (Config, error) {
	if len(raw) == 0 {
		return Config{}, configError{FailureClassConfigInvalid, "config is required"}
	}
	encoded, err := json.Marshal(raw)
	if err != nil {
		return Config{}, configError{FailureClassConfigInvalid, "config is not valid JSON"}
	}
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	var decoded rawConfig
	if err := decoder.Decode(&decoded); err != nil {
		return Config{}, configError{FailureClassConfigInvalid, "config has an unknown key or a value of the wrong type"}
	}
	if len(decoded.Targets) == 0 {
		return Config{}, configError{FailureClassConfigInvalid, "config.targets must name at least one Loki target"}
	}
	targets := make([]loki.TargetConfig, 0, len(decoded.Targets))
	for i, target := range decoded.Targets {
		mapped, err := mapTarget(target, getenv)
		if err != nil {
			return Config{}, fmt.Errorf("targets[%d]: %w", i, err)
		}
		targets = append(targets, mapped)
	}
	maxResult := 0
	if decoded.Limits != nil {
		maxResult = decoded.Limits.MaxResultBytes
	}
	if maxResult < 0 || maxResult > maxResultBytesCeiling {
		return Config{}, configError{FailureClassConfigInvalid, "limits.max_result_bytes must be between 0 and 67108864"}
	}
	if maxResult == 0 {
		maxResult = DefaultMaxResultBytes
	}
	return Config{Targets: targets, MaxResultBytes: maxResult}, nil
}

// mapTarget resolves one raw target the way the in-tree collector does:
// token_env and tenant_id_env name environment variables, durations parse as
// Go durations, and slices are trimmed of blanks.
func mapTarget(target rawTarget, getenv func(string) string) (loki.TargetConfig, error) {
	token, err := optionalEnvValue(target.TokenEnv, getenv, "token_env", FailureClassCredentialMissing)
	if err != nil {
		return loki.TargetConfig{}, err
	}
	tenantID, err := optionalEnvOverride(target.TenantID, target.TenantIDEnv, getenv)
	if err != nil {
		return loki.TargetConfig{}, err
	}
	staleAfter, err := parseOptionalDuration(target.StaleAfter, "stale_after")
	if err != nil {
		return loki.TargetConfig{}, err
	}
	seriesLookback, err := parseOptionalDuration(target.SeriesLookback, "series_lookback")
	if err != nil {
		return loki.TargetConfig{}, err
	}
	return loki.TargetConfig{
		ScopeID:                strings.TrimSpace(target.ScopeID),
		InstanceID:             strings.TrimSpace(target.InstanceID),
		BaseURL:                strings.TrimRight(strings.TrimSpace(target.BaseURL), "/"),
		PathPrefix:             strings.TrimSpace(target.PathPrefix),
		Token:                  token,
		TenantID:               tenantID,
		ResourceLimit:          target.ResourceLimit,
		LabelValueNames:        cleanStrings(target.LabelValueNames),
		MaxLabelValuesPerLabel: target.MaxLabelValuesPerLabel,
		SeriesMatchers:         cleanStrings(target.SeriesMatchers),
		SeriesLookback:         seriesLookback,
		StaleAfter:             staleAfter,
		Enabled:                target.Enabled,
		DeclaredIDs:            stringSet(target.DeclaredIDs),
		ObservedOnlyHint:       target.ObservedOnlyHint,
	}, nil
}

// optionalEnvValue resolves an env reference that carries a credential. An
// empty reference means no credential; a named-but-unset variable fails
// closed without echoing any value.
func optionalEnvValue(envName string, getenv func(string) string, field string, class string) (string, error) {
	envName = strings.TrimSpace(envName)
	if envName == "" {
		return "", nil
	}
	if !envNamePattern.MatchString(envName) {
		return "", configError{FailureClassConfigInvalid, field + " must name an environment variable"}
	}
	value := strings.TrimSpace(getenv(envName))
	if value == "" {
		return "", configError{class, fmt.Sprintf("%s %s did not resolve a value", field, envName)}
	}
	return value, nil
}

// optionalEnvOverride resolves a tenant override: the env reference wins when
// present, otherwise the literal stands. A named-but-unset variable fails
// closed; the tenant ID is credential-grade and never appears in the error.
func optionalEnvOverride(raw string, envName string, getenv func(string) string) (string, error) {
	envName = strings.TrimSpace(envName)
	if envName == "" {
		return strings.TrimSpace(raw), nil
	}
	if !envNamePattern.MatchString(envName) {
		return "", configError{FailureClassConfigInvalid, "tenant_id_env must name an environment variable"}
	}
	value := strings.TrimSpace(getenv(envName))
	if value == "" {
		return "", configError{FailureClassCredentialMissing, fmt.Sprintf("tenant_id_env %s did not resolve a value", envName)}
	}
	return value, nil
}

func parseOptionalDuration(raw string, field string) (time.Duration, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return 0, nil
	}
	value, err := time.ParseDuration(trimmed)
	if err != nil {
		return 0, configError{FailureClassConfigInvalid, "parse " + field}
	}
	if value < 0 {
		return 0, configError{FailureClassConfigInvalid, field + " must be non-negative"}
	}
	return value, nil
}

func cleanStrings(values []string) []string {
	out := make([]string, 0, len(values))
	seen := map[string]struct{}{}
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			continue
		}
		if _, ok := seen[trimmed]; ok {
			continue
		}
		seen[trimmed] = struct{}{}
		out = append(out, trimmed)
	}
	return out
}

func stringSet(values []string) map[string]struct{} {
	if len(values) == 0 {
		return nil
	}
	set := make(map[string]struct{}, len(values))
	for _, value := range cleanStrings(values) {
		set[value] = struct{}{}
	}
	return set
}
