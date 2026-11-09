# Facts reference

## What it reports back

This table mirrors `manifest.yaml` and its `emittedFacts` list. The manifest stays the source of truth, so if you ever change one, change the other in the same breath. Every kind carries schema version `1.0.0` and source confidence `reported`, meaning Loki itself supplied the value.

| Fact kind | One of these means… | Loki read behind it |
| --- | --- | --- |
| `observability.source_instance` | a configured Loki target exists | the target you configured |
| `observability.observed_log_signal` | a label or series shape is live | `GET /loki/api/v1/labels`, `GET /loki/api/v1/label/{label}/values`, `GET /loki/api/v1/series` |
| `observability.observed_rule` | a ruler rule is live | `GET /loki/api/v1/rules` |
| `observability.coverage_warning` | something limited the view | whichever read above couldn't finish cleanly |

Warnings cover permission gaps, a missing ruler, throttling, truncation, and partial reads. You'll get facts plus warnings rather than silence, because "we saw most of it" beats a shrug.

Facts hold shape, not content. Raw LogQL, label values, tenant IDs, tenant headers, credentials, log lines, and provider response bodies never enter a fact. Where a payload needs to prove identity, it carries a fingerprint or a presence flag instead.

## SDK pins

Exact pins from `go.mod`, no `replace` directives. Trust the file over this page if they ever disagree.

| Module | Pin |
| --- | --- |
| `github.com/eshu-hq/eshu-sdk/collector` | `v0.2.0-rc.1` |
| `github.com/eshu-hq/eshu-sdk/factschema` | `v0.1.0-rc.5` |
| `gopkg.in/yaml.v3` | `v3.0.1` |

Go version in `go.mod`: `1.26.10`. One known gap: the pinned `factschema` has no per-kind schema-version helper, so the locked `1.0.0` map lives in `loki/schema.go` until the SDK provides one.

The manifest ([manifest.yaml](../../manifest.yaml)) stays the source of truth for this list. If a fact kind ever changes, the manifest and this page change together.
