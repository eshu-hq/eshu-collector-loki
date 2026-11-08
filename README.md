# eshu-collector-loki

Status: pre-release. This collector is not yet the live Loki producer for Eshu; Eshu core still owns admission and storage.

This collector reads live Loki log metadata for Eshu and returns it as facts (small, checked records Eshu stores as evidence).

## What it collects

For each work request, the collector reads one configured Loki target through bounded REST calls and emits metadata only. It never stores log lines.

`manifest.yaml` declares 4 fact kinds. Each kind uses schema version `1.0.0` and source confidence `reported` (the provider itself reported the value).

| Fact kind | What it records | Loki read behind it |
| --- | --- | --- |
| `observability.source_instance` | One configured Loki target | The configured target itself |
| `observability.observed_log_signal` | Label and series shape seen live | `GET /loki/api/v1/labels`, `GET /loki/api/v1/label/{label}/values`, `GET /loki/api/v1/series` |
| `observability.observed_rule` | One live ruler rule group | `GET /loki/api/v1/rules` |
| `observability.coverage_warning` | A gap: permission, missing ruler, throttle, truncation, or partial read | Any read above that could not finish cleanly |

Raw LogQL, label values, tenant IDs, tenant headers, credentials, log lines, and provider response bodies never enter a fact. Facts carry presence flags and fingerprints instead where the payload needs them.

## How data flows

```mermaid
flowchart LR
    A[Loki API] --> B[this collector]
    B --> C[Eshu extension host]
    C --> D[Eshu core]
```

Text version: Loki API → this collector → Eshu extension host → Eshu core. The host hands the collector one claim (one bounded unit of work), validates the result, and owns claims, leases (timed work ownership), and commits. Core owns admission, projection, and persistence.

## When to use it

Use it when Eshu needs live evidence about which log labels, series, and ruler rules a Loki target actually serves.

Do not use it to search or export log content, run LogQL queries for people, or replace Loki itself. It reads metadata endpoints only and writes nothing to Loki.

## How Eshu runs it

The Eshu component-extension host runs this program with its `process` runner:

- The host writes one bounded `collector-sdk/v1alpha1` Request to standard input (stdin).
- The collector reads the configured Loki targets and writes one `collector-sdk/v1alpha1` Result to standard output (stdout).
- With no arguments, or with `--sdk-stdio`, the binary serves one claim this way. That is the only serving mode.

Local smoke run with a recorded request:

```bash
collector --sdk-stdio < request.json > result.json
```

### Image and seed

The release image is `ghcr.io/eshu-hq/eshu-collector-loki`. It holds one static binary at `/usr/local/bin/collector` on a distroless base (no shell), running as user `65532:65532`.

The binary installs itself for the host. An init container (a setup container that runs before the main one) runs:

```bash
/usr/local/bin/collector seed --dest DIR [--image-ref REPO@sha256:DIGEST]
```

Seed writes `DIR/collector` (byte copy, mode 0755) and `DIR/manifest.yaml` (mode 0644). Re-running it is safe: correct files are left alone and changed files are replaced atomically.

`--image-ref`, or the `COLLECTOR_IMAGE_REF` environment variable (the flag wins), stamps the deployed digest into the seeded manifest, because an image cannot embed its own digest. The value must be `repository@sha256:<64 hex digits>`; anything else exits non-zero and writes nothing. Without it, the repo placeholder (all-zero digest in `manifest.yaml`) is written unchanged. The host runs the seeded binary; it does not pull this image per claim.

Manifest identity: `dev.eshu.collector.loki`, version `0.1.2`, collector kind `loki`, platform `linux/amd64`, compatible core `>=0.0.5 <0.2.0`.

### Grants

All 4 kinds are core-owned, so the host needs a live producer grant (permission to emit a core-owned kind) per kind before it activates the component or issues claims. `grant.example.json` lists the 4 grants (scope `loki`, version `0.1.2`, schema `1.0.0`). The collector also checks grant shape at emission and fails closed (stops safely) before provider contact; the host remains the issuance gate.

Installing this collector next to the Tempo collector needs a host that supports grant-aware sharing of the shared observability kinds. On an older host, the second install fails closed with a fact-kind collision. This repo proves the manifest side only; host support lives in Eshu core.

## Configuration

The host reads an activation file. `host` and `process` configure the host; everything under `config` reaches this collector. `config.example.yaml` is a complete example with placeholder values. Copy it and replace the placeholders; never commit real credentials.

| Key | Meaning |
| --- | --- |
| `targets` | Required, at least one enabled target. |
| `targets[].scope_id` | Required. A claim for another scope does not match this target. |
| `targets[].instance_id` | Required. Recorded as the fact source instance. |
| `targets[].base_url` | Required. Loki API origin. Plain `http` is accepted for the in-cluster gateway in the example. |
| `targets[].path_prefix` | Optional path prefix (commented in the example). |
| `targets[].token_env` | Optional. **Name** of the environment variable holding the token. The value is never in the file. |
| `targets[].tenant_id` | Optional literal tenant ID. |
| `targets[].tenant_id_env` | Optional. **Name** of the environment variable holding the tenant ID; wins over `tenant_id`. |
| `targets[].resource_limit` | Bound on records read per target (example: `50`). |
| `targets[].label_value_names`, `targets[].max_label_values_per_label` | Optional label-value read bounds (commented in the example). |
| `targets[].series_matchers`, `targets[].series_lookback` | Optional series query bounds (commented in the example). |
| `targets[].stale_after` | Freshness window (example: `24h`). |
| `targets[].enabled` | Disabled targets are ignored. |
| `limits.max_result_bytes` | Fail terminal with `result_too_large` above this many encoded bytes (example and default: `3500000`). |

`host.sourceSystem` must be `loki` and `host.scope.kind` is `log_source` in the example. Scope IDs are part of every stable fact key, so choose them once and keep them.

### Credentials

Secrets travel by environment-variable name only: `token_env` and `tenant_id_env` name variables the collector process reads at claim time. A named-but-unset variable fails the claim as `credential_missing` before provider contact. Token and tenant values never appear in facts, logs, or statuses.

Give the credential only the read access the four `GET` endpoints above need. This repo does not name Loki auth scopes or roles, because the code does not define them. A target behind an in-cluster gateway may need no credential at all; leave the `*_env` keys unset then.

## Claim budget and operator signals

One claim has a hard deadline: the host kills the process at the initial lease expiry. The collector stops starting provider calls `min(2s, 5% of remaining time)` before that deadline and returns a retryable `claim_deadline_exceeded` result with no facts, so an incomplete read is never committed as partial. Large targets read sequentially (labels, then series, then rules), so size the worker lease to the target.

Result states an operator will see:

| State | Meaning |
| --- | --- |
| `complete` | Labels, series, and rules each finished on their own. |
| `partial` | Facts plus at least one coverage warning (permission gap, unsupported ruler, throttle, truncation). Facts are still committed. |
| `retryable` | Try again later: `retryable`, `claim_deadline_exceeded`, or `canceled`. Carries `retry_after_seconds` when the provider gave guidance. |
| `terminal` | Stop: bad config or bad data, for example `config_invalid`, `credential_missing`, `invalid_claim`, `result_too_large`, `fact_invalid`. |

The failure class and counts travel in the result status; no status or log line carries a provider body, token, tenant ID, or credential-bearing URL. The collector also writes one `collector health: {...}` line per claim to stderr for local runs and tests; the host process runner keeps only a byte count of stderr and drops the text.

The manifest declares `telemetry.metricsPrefix: eshu_dp_loki_collector_`, but nothing in this repo emits a metric under that prefix. There is no metrics endpoint and no OpenTelemetry export from this binary; the claim result's failure class is the operator signal today.

## Eshu dependencies

Exact pins from `go.mod` in this repo. No `replace` directives.

| Module | Pin |
| --- | --- |
| `github.com/eshu-hq/eshu-sdk/collector` | `v0.2.0-rc.1` |
| `github.com/eshu-hq/eshu-sdk/factschema` | `v0.1.0-rc.5` |
| `gopkg.in/yaml.v3` | `v3.0.1` |

Go version in `go.mod`: `1.26.10`.

One known gap: the pinned `factschema` has no per-kind schema-version helper, so the locked `1.0.0` map lives in `loki/schema.go` until the SDK provides one.

## What it never does

- It never writes to Loki and never persists log lines.
- It has no Postgres, graph, queue, or lease handle, and never imports Eshu core internals (`boundary_test.go` enforces this).
- It never builds the durable fact envelope or commits facts; the host does.
- It never decides claim fate beyond the bounded failure class it reports.
- It never emits kinds outside the 4 in `manifest.yaml`.

## Build, test, and release

```bash
make ci          # lint + test + race + conformance script
make lint        # vet, golangci-lint, format diff, tidy diff, 500-line cap
make test        # go build ./... and go test ./... -count=1
make race        # race tests plus coverage floor (90.1%)
make actionlint  # workflow lint
docker build -t eshu-collector-loki:local .
```

CI (`.github/workflows/ci.yml`) is the authority; `make ci` mirrors it locally, including `scripts/run-conformance.sh`.

Releases run from `.github/workflows/release.yml` on pushes to `main` and on version tags. Nothing publishes from a pull request and the workflow deploys nothing. A `main` push publishes moving `main`, `latest`, and `sha-<full-sha>` tags; a version tag must match `metadata.version` in `manifest.yaml` and point at the protected `main` head. The workflow builds `linux/amd64`, pushes `ghcr.io/eshu-hq/eshu-collector-loki:<tag>`, signs the digest with cosign (keyless), and attaches SLSA provenance and an SPDX SBOM attestation (signed build records). Copy the digest from the job summary into the activation; no commit carries it. The GHCR package must be public so activations can pull without a token.

Plain `cosign sign` produces no Eshu trust annotations, so allowlist trust is the supported install mode today; strict trust is a follow-up.

## Docs and related repos

- [eshu](https://github.com/eshu-hq/eshu) — Eshu core and the extension host that runs this collector.
- [eshu-sdk](https://github.com/eshu-hq/eshu-sdk) — the collector and fact-schema modules pinned above.
- [eshu-deploy](https://github.com/eshu-hq/eshu-deploy) — deployment for Eshu components.

Local files: [config.example.yaml](config.example.yaml), [grant.example.json](grant.example.json), [manifest.yaml](manifest.yaml), [Dockerfile](Dockerfile), and [CONTRIBUTING.md](CONTRIBUTING.md).

## Contributing and license

See [CONTRIBUTING.md](CONTRIBUTING.md). License: MIT — see [LICENSE](LICENSE). This repo has no `SECURITY.md` file.
