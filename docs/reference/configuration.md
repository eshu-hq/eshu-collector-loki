# Configuring the Loki collector

You configure this collector through an activation file the host reads. The `host` and `process` sections talk to the host, and everything under `config` reaches this collector as the request config. Start from [config.example.yaml](../../config.example.yaml), swap the placeholders for your own values, and please never commit real credentials. You won't need to, because secrets travel by name only (more on that below).

Two host values shape identity, so choose them once and keep them. `host.sourceSystem` must be `loki`, and `host.scope.kind` is `log_source` in the example. Scope IDs end up inside every stable fact key. Rename one later and Eshu will read the same Loki as a brand-new source.

## Targets

Each entry under `targets` describes one Loki API the collector may read. A claim matches a target by scope, so a claim for some other scope simply won't land here.

| Key | Notes from the field |
| --- | --- |
| `targets` | Required. Name at least one target, and at least one needs to stay enabled. |
| `targets[].scope_id` | Required. The claim's scope picks the target, so spell it exactly as the host will send it. |
| `targets[].instance_id` | Required. Recorded as the fact source instance. |
| `targets[].base_url` | Required. The Loki API origin. Plain `http` works for the in-cluster gateway in the example. |
| `targets[].path_prefix` | Optional path prefix, for gateways that mount Loki below the root. Commented out in the example. |
| `targets[].token_env` | Optional. The name of the environment variable holding the token. The value itself never belongs in this file. |
| `targets[].tenant_id` | Optional literal tenant ID, for single-tenant setups where that reads better. |
| `targets[].tenant_id_env` | Optional. The name of the environment variable holding the tenant ID. When you set both, this one wins. |
| `targets[].resource_limit` | A bound on records read per target. The example uses `50`. Hitting the bound surfaces as a truncation warning, not silent loss. |
| `targets[].label_value_names` | Optional. Which labels get their values read. Commented out in the example. |
| `targets[].max_label_values_per_label` | Optional cap on values per label, so one chatty label can't crowd out the rest. |
| `targets[].series_matchers` | Optional matchers for the series read. Commented out in the example. |
| `targets[].series_lookback` | Optional window for the series read. Leave it alone unless series you expect keep falling outside the default window. |
| `targets[].stale_after` | The freshness window. The example uses `24h`, and evidence older than that reads as stale. |
| `targets[].enabled` | Disabled targets get ignored. Handy when you want to park a target without deleting it. |
| `targets[].declared_ids` | Optional list of exact identities you declare elsewhere. Entries are trimmed, blanks dropped, and duplicates removed (`config.go` `stringSet`, from `rawTarget.DeclaredIDs`); an empty list means no set at all. Matching happens only in `loki/normalize.go`, and only for two record types: a **series**, matched by its series fingerprint — the hash of its sorted label keys interleaved with their label *values*, so a human-readable label-set name will never match — and a **ruler rule**, matched by its `namespace/group:rule` identity (falling back to `group:rule`, then `rule:name`, then a fingerprint of the query). An exact hit earns `declared_match_state: matched_declared`. Everything else, including the label-set signal (which is never compared against this set), stays `not_compared`. |
| `targets[].observed_only_hint` | Optional boolean. Code behavior, exactly: in `loki/normalize.go`, when this is true and a series or ruler rule did **not** earn `matched_declared`, that record is flagged `ManuallyCreated`, which surfaces in the fact as `manually_created: true` plus `drift_candidate_reason: manual_provider_resource`. The record's match state stays `not_compared`, nothing is filtered out, and matched records are untouched. The label-set signal is never flagged. With `declared_ids` empty, turning this on flags every series and rule. |

Unknown keys fail closed, by design. A typo that quietly widened a bound would be a nasty surprise, so the parser would rather stop and tell you.

## Result size

| Key | Notes from the field |
| --- | --- |
| `limits.max_result_bytes` | Above this many encoded bytes the claim fails terminal with `result_too_large`. The example and the default both use `3500000`. Raise `process.stdout_limit_bytes` alongside it if you raise this. |

## Credentials, without the anxiety

Secrets travel by environment-variable name only. `token_env` and `tenant_id_env` name variables the collector process reads at claim time, so the file itself stays safe to share. If you name a variable and it resolves to nothing, the claim fails as `credential_missing` before any provider contact, and the error names the variable, never a value. Token and tenant values never appear in facts, logs, or statuses.

Give the credential only the read access the four GET endpoints need. This repo doesn't name Loki auth scopes or roles, because the code doesn't define them. And if your target sits behind an in-cluster gateway that needs no credential at all, that's fine too. Just leave the `*_env` keys unset.

## Grants you'll need before activation

All four fact kinds belong to core, so the host wants a live producer grant per kind before it activates the component or issues claims. A grant is core's permission slip for emitting a core-owned kind. [grant.example.json](../../grant.example.json) lists the four (scope `loki`, version `0.1.2`, schema `1.0.0`). The collector double-checks grant shape at emission and fails closed before provider contact, but issuance stays the host's job.

One cohabitation note: installing this collector next to the Tempo collector needs a host that supports grant-aware sharing of the shared observability kinds. On an older host the second install fails closed with a fact-kind collision. This repo proves the manifest side only. Host support lives in Eshu core.
