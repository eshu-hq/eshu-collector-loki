# eshu-collector-loki

Eshu Loki log-signal collector, packaged as an Eshu component extension.
Extracted from `go/cmd/collector-loki` and
`go/internal/collector/observability/loki` in `eshu-hq/eshu`
([eshu-hq/eshu#8860](https://github.com/eshu-hq/eshu/issues/8860), part of epic #6707):
this repository fetches bounded live Loki metadata and returns a
`collector-sdk/v1alpha1` Result per host claim. Core keeps admission,
projection, persistence, claim mutation, fencing, and the status read.

Refs #8860. This repository does not close that issue: cutover stays gated
by #4047 with production acceptance NOT_CHECKED, and no pair is a
qualified consumer/core combination yet (see SDK Compatibility in
eshu-hq/eshu docs).

Pre-release. Holds no producer authority until its reviewed cutover
(eshu-hq/eshu#9202).

## Pins

Exact published pins from `eshu-hq/eshu-sdk` — never `@latest`, no
`replace` or workspace directives:

| Module | Pin |
|---|---|
| `github.com/eshu-hq/eshu-sdk/collector` | `v0.1.0-rc.3` |
| `github.com/eshu-hq/eshu-sdk/factschema` | `v0.1.0-rc.5` |
| `gopkg.in/yaml.v3` | `v3.0.1` |

Verify resolution before any pin bump:

```bash
GOPROXY=proxy.golang.org go list -m github.com/eshu-hq/eshu-sdk/collector@v0.1.0-rc.3
GOPROXY=proxy.golang.org go list -m github.com/eshu-hq/eshu-sdk/factschema@v0.1.0-rc.5
```

## What it does

The Eshu component-extension host runs this program with its `process`
runner. For each claim the host writes one bounded `collector-sdk/v1alpha1`
Request to standard input; the program reads the configured Loki API targets
and writes one `collector-sdk/v1alpha1` Result to standard output. The host
builds the durable envelope, validates the Result, and owns claims, leases,
and commits. This program has no Postgres, graph, queue, or lease handle and
never imports Eshu internals (`boundary_test.go` enforces it).

It emits the same four core fact kinds, with the same stable keys and
payloads, as the in-tree collector it replaces. `loki/testdata/README.md`
explains the parity proof (or `loki/` tests where that file is absent).

| Fact kind | Source | Manifest-owned |
| --- | --- | --- |
| `observability.source_instance` | One configured Loki target | Yes |
| `observability.observed_log_signal` | `GET /loki/api/v1/labels`, `/series` (+ label values) | Yes |
| `observability.observed_rule` | `GET /loki/api/v1/rules` | Yes |
| `observability.coverage_warning` | A bound, permission, capability, or truncation gap | Yes |

The manifest owns the full four-kind emission (eshu-hq/eshu#9202 arbiter
verdict, satellite release v0.1.2). The emission edge still checks the
core-issued producer grant before any provider contact. Every kind is
schema `1.0.0` and source confidence `reported`. Live re-proof of the
widened emission stays pending. The program never persists log lines, raw
LogQL, label values, tenant IDs, tenant headers, credentials, or provider
response bodies.

## Result states

| State | When |
| --- | --- |
| `complete` | Labels, series, and rules each ended on their own. |
| `partial` | At least one coverage warning was emitted (a 401/403 permission gap, a 404/405 or ruler-absent 502 capability gap, a 429 throttle, a resource-limit truncation, or a partial read). The facts are still committed. |
| `retryable` | `retryable` (non-degradable 5xx, transport failure, malformed body, unconfigured scope), `claim_deadline_exceeded`, or `canceled`. The collector sends `retry_after_seconds` when the provider gives guidance. |
| `terminal` | `terminal` (deterministic bad data), or a config problem: `config_invalid`, `credential_missing`, `invalid_claim`, `result_too_large`, `fact_invalid`. |

A status holds a failure class and counts only. No Result, status, or log line
carries a provider response body, a token, a tenant ID, or a URL with
credentials.

## Configuration

An activation file configures the host (`host`, `process`) and this program
(`config`). `config.example.yaml` is a complete example with placeholder
values. The `config` keys mirror the in-tree collector's target JSON
(`go/cmd/collector-loki` in `eshu-hq/eshu`).

| Key | Meaning |
| --- | --- |
| `targets` | Required, at least one. Each entry names one Loki API target. |
| `targets[].scope_id` | Required. A claim for any other scope ends retryable. |
| `targets[].instance_id` | Required. Recorded as the fact source instance. |
| `targets[].base_url` | Required. Plain `http` is accepted for the in-cluster gateway. |
| `targets[].token_env`, `targets[].tenant_id_env` | Optional. **Names** of environment variables holding the token and tenant ID; the values are never in the config. Unset when the target needs no credential. |
| `targets[].tenant_id` | Optional literal tenant; `tenant_id_env` wins when both are set. |
| `targets[].resource_limit`, `targets[].max_label_values_per_label` | Bounds (in-tree defaults apply at 0). |
| `targets[].label_value_names`, `series_matchers`, `series_lookback`, `stale_after`, `declared_ids`, `observed_only_hint` | Same semantics as the in-tree target. |
| `targets[].enabled` | Disabled targets are ignored; at least one enabled target is required. |
| `limits.max_result_bytes` | Fail terminal above this many encoded bytes (default 3500000, under the host's 4 MiB output limit). |

## Run it with Eshu

The component lifecycle, using the `eshu` CLI against a component home:

```sh
eshu component grant dev.eshu.collector.loki --component-home "$HOME_DIR" \
  --version 0.1.2 --kind observability.source_instance --schema-version 1.0.0 \
  --scope loki --expires-in 8760h        # repeat for each kind in grant.example.json
eshu component install manifest.yaml --component-home "$HOME_DIR" \
  --trust-mode allowlist --allow-id dev.eshu.collector.loki --allow-publisher eshu-hq
eshu component enable dev.eshu.collector.loki --component-home "$HOME_DIR" \
  --instance loki-example --mode scheduled --claims --config config.example.yaml
```

A grant's scope is the collector kind (`loki`), not a scope ID.
`grant.example.json` lists the four grants. The host enforces issuance: it
refuses to activate the component and to issue claims without live grants.
The in-process `grant.Check` at every emission keeps the fail-closed shape
against code drift; it is not the issuance gate.

Co-install prerequisite: v0.1.2 re-declares core-owned kinds shared with the
tempo collector, so `install` and `enable` need a host with grant-aware
sharing (eshu-hq/eshu with the grant-sharing change for eshu-hq/eshu#9202);
on an older host the second install fails closed with a fact-kind collision.

### Trust mode

The release signs the image digest keyless with cosign, but it does a plain
`cosign sign`, so the signature carries no `eshu.component.*` annotations.
That suits **allowlist** trust, which the install command above uses.
**Strict** trust has Eshu run `cosign verify` with those annotations, and this
workflow does not produce them, so strict trust is not supported yet. Adding
it is a follow-up.

## Operator signals

The extension host passes the **failure class** (`retryable`,
`claim_deadline_exceeded`, ...) from a claim's result to the work item. On
eshu `main` since eshu-hq/eshu#9008 (a release carries it once one is cut
after that change), the worker also sets a status recorder: it records every
claim result as six `eshu_dp_component_extension_*` metrics and one
`component extension result` log line, and it honors `retry_after_seconds`.

- **Recorded: fact count, warning count and source latency.** The collector
  fills them in, and the host records them in the metrics above.
- **Honored: `retry_after_seconds`.** The collector sends it on every
  retryable result with provider guidance. The worker retries after the larger
  of the poll interval and that request, capped at one hour, until the attempt
  budget is spent.
- **Dropped: the stderr health line.** The collector writes one
  `collector health: {...}` line per claim to stderr with the health,
  resource and claim summary. The host's process runner keeps stderr only as
  a byte count and drops the text. The line is for a local run
  (`collector --sdk-stdio < request.json`) and for tests.
- **No spans or metrics.** The manifest declares
  `telemetry.metricsPrefix: eshu_dp_loki_collector_`, but nothing in this
  repository emits a metric under it.

The host records the failure class, not the cause behind it. Find the cause of
a failure by running the collector with the activation's config and a recorded
request.

## Image and seeding

The image is `ghcr.io/eshu-hq/eshu-collector-loki`: one static binary at
`/usr/local/bin/collector` on a distroless base, running as `65532:65532`,
with no shell. It is also the delivery artifact. An init container runs:

```sh
/usr/local/bin/collector seed --dest DIR [--image-ref REPO@sha256:DIGEST]
```

which writes `DIR/collector` (a byte copy of the binary, mode 0755) and
`DIR/manifest.yaml` (mode 0644). The command is idempotent: a file that
already holds the right bytes is left alone, and a changed file is replaced
atomically. The host's `process.command` then points at the seeded
`collector`.

The manifest is embedded in the image it describes, so it cannot hold that
image's digest. The deployer passes the image it runs instead:
`--image-ref REF`, or the `COLLECTOR_IMAGE_REF` environment variable (the flag
wins). After one trailing newline is trimmed, `REF` must match
`^[a-z0-9]+([._-][a-z0-9]+)*(:[0-9]+)?(/[a-z0-9]+([._-][a-z0-9]+)*)*@sha256:[0-9a-f]{64}$`,
so it carries no quote, `$`, whitespace, or other YAML-significant character.
Seed stamps it literally into the seeded manifest's `artifacts[].image` and
changes nothing else. A reference of any other shape exits non-zero without
writing anything or echoing the value.
Without `REF`, the repository placeholder (an all-zero digest) is written
unchanged. The `process` adapter runs the seeded binary and never pulls this
image, so the stamp is provenance for the installed manifest.

## Releasing

Only a pushed tag publishes an image. `.github/workflows/release.yml` runs on
`v*` tags and nothing else, so pull requests and branch pushes publish
nothing. It uses only the job's `GITHUB_TOKEN`, pins every action by commit
SHA, and has no deploy step.

Before the first tag, check the repository settings, because a workflow
cannot enforce them. Any pushed `v*` tag publishes a public image, so
restrict `v*` tag creation to maintainers (a repository ruleset for tags).
The workflow checks the tag against `manifest.yaml` but does not check that
the tagged commit is on `main`, so tag only a commit that was merged there.

1. Merge the change to `main`. The tag must match `metadata.version` in
   `manifest.yaml` (`v0.1.0` for version `0.1.0`; a `-rc1` style suffix is
   allowed). The workflow first requires the tag to be
   `vMAJOR.MINOR.PATCH` with an optional `-prerelease` suffix, so a tag such
   as `v0.1.0-` or `v0.1.0-rc/a` fails before anything is built. Bump the
   manifest version in the same PR.
2. Tag the merged commit and push the tag: `git tag v0.1.0 && git push origin v0.1.0`.
3. The workflow builds `linux/amd64` only, pushes
   `ghcr.io/eshu-hq/eshu-collector-loki:<tag>`, and writes the digest into
   the job summary. It signs the digest with cosign (keyless), attests SLSA
   build provenance, attests an SPDX SBOM, and verifies all three.
4. Copy the digest from the job summary into the activation's `seed.image`
   (`ghcr.io/eshu-hq/eshu-collector-loki@sha256:<digest>`). The init
   container passes the same reference to `collector seed` as
   `COLLECTOR_IMAGE_REF` (or `--image-ref`), which stamps it into the seeded
   manifest. No commit needs to carry the digest.

Co-install prerequisite: v0.1.2 re-declares core-owned kinds shared with the
tempo collector, so the host must install with grant-aware sharing
(eshu-hq/eshu with the grant-sharing change for eshu-hq/eshu#9202). Do not
seed v0.1.2 alongside tempo v0.1.3 on an older host image.

The package must be **public**: the activation pulls it without a token. A
new GHCR package starts private, so after the first release set its
visibility to public in the package settings and re-run the job if the pull
fails.

Verify a release before using it:

```sh
cosign verify ghcr.io/eshu-hq/eshu-collector-loki@sha256:<digest> \
  --certificate-identity https://github.com/eshu-hq/eshu-collector-loki/.github/workflows/release.yml@refs/tags/v0.1.0 \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com
gh attestation verify oci://ghcr.io/eshu-hq/eshu-collector-loki@sha256:<digest> \
  -R eshu-hq/eshu-collector-loki
# The SPDX predicate version is the one in the SBOM's spdxVersion
# (SPDX-2.3 gives v2.3). The workflow reads it from the SBOM file.
gh attestation verify oci://ghcr.io/eshu-hq/eshu-collector-loki@sha256:<digest> \
  -R eshu-hq/eshu-collector-loki --predicate-type https://spdx.dev/Document/v2.3
```

## Develop

```sh
go build ./...
go vet ./...
go test -race -count=1 ./...
docker build -t eshu-collector-loki:local .
```

`make ci` runs the lint, test, and race lanes CI runs
(`.github/workflows/ci.yml`; the `ci-complete` job aggregates the others).
`scripts/run-conformance.sh` runs the same build, vet, and race gate.

## Known limits

- **A claim has a hard time bound.** The host sets the claim deadline to the
  initial lease expiry and kills the process there. The collector reads Loki
  sequentially (labels, then series, then rules), so a large target can need
  more than the default 60 s worker lease. It therefore stops starting
  provider calls `min(2 s, 5% of the remaining time)` before the deadline and
  returns `retryable` with `claim_deadline_exceeded` and no facts, never
  `partial`, so an incomplete read is not committed. An activation that
  replaces the in-tree collector should set the worker lease TTL to at least
  the lease TTL the in-tree collector ran with (5 m in the reference
  deployment).
- **No OpenTelemetry.** The process runner has no metrics endpoint, and
  nothing in this repository emits a metric under the manifest's
  `eshu_dp_loki_collector_` prefix. See Operator signals for what an operator
  can see today.
- **Grant enforcement is host-side.** The `grant` package keeps the
  fail-closed emission shape (identity, version, scope, kind coverage,
  lifetime), but the core-issued grant itself is enforced by the extension
  host at activation and claim time. The process never receives a grant; the
  handler mints a per-claim grant from the package constants so `Collect`
  still checks shape before any provider contact.

## Layout

- `collector.go`, `request.go`, `config.go`, `handler.go`, `result.go`,
  `health.go` — the standalone surface: stdio Request/Result wiring, config
  parsing, claim handling, and the health seam. `config.example.yaml` is the
  documented activation config; `grant.example.json` lists the producer
  grants the host must issue.
- `cmd/collector/` — the `collector` binary: stdio serving plus the `seed`
  subcommand the init container uses.
- `Dockerfile`, `manifest.yaml`, `.github/workflows/` — the release
  surface: reproducible image, component manifest, CI and tag-triggered
  release.
- `loki/` — the producer: bounded REST fetch (`client.go`,
  `normalize.go`), four envelope constructors (`envelope.go`),
  claim-to-Result fan-out (`source.go`), metadata-only redaction
  (`redaction.go`), kind/schema admission (`schema.go`), claim-ID minting
  (`claimid.go`), and a host-wired metrics seam (`metrics.go`).
  `testdata/8860-parity-loki.json` is the cutover comparison input,
  byte-identical to the in-tree parity pack. Regenerate only from the
  in-tree constructors, never from this package's own output.
- `grant/` — fail-closed stub enforcing the core-issued producer grant
  draft (`PRODUCER_GRANT_DRAFT.md` in eshu-hq/eshu,
  `go/internal/collector/observability/loki`) at every emission. Until
  core issues a grant, the zero Grant is denied and `Collect` fails
  closed before any provider contact.

## Parity evidence

- Census mirror: `loki/extraction_prep_mirror_test.go` ports the
  in-tree extraction PREP mirror (admission at 1.0.0, determinism,
  generation rebind, label-value hash flow-through, redaction sentinel,
  fail-closed hostile inputs). `TestClaimIDNamespace` ports the
  `loki-claim-` namespace case.
- Golden vectors: `loki/extraction_prep_golden_test.go` pins the nine
  in-tree stable keys and the label-set payload bytes cut from the
  in-tree constructors at the merged prep (eshu-hq/eshu `55d35e04bf`)
  against `testdata/8860-parity-loki.json`, plus per-scope kind census
  and a validator self-check. Regenerate only from the in-tree
  constructors, never from this package's own output.
- Stable keys: `kind:generationID:identity.StableID(kind, identity)` is
  byte-stable with the in-tree scheme (the published
  `factschema/identity` algorithm is identical to in-tree
  `facts.StableID`; the golden test proves it empirically).
- Contract payloads: `observability/v1` struct fields are identical
  between the in-tree `sdk/go/factschema` source and the pinned module,
  and the same `EncodeObservability*` entry points merge payloads.
- SDK self-check: `TestCollectEmitsValidatorCleanResult`,
  `TestCollectPartialReadStaysPartial`, and
  `TestCollectTruncatedReadStaysPartial` run emitted Results through the
  published SDK's own `collector.NewValidator(...).ValidateResult`,
  including its sensitive-payload-key gate.
- In-tree `FactID` (host-assigned at admission) and numeric
  `FencingToken` (host-enforced from `claim.fencing_token`) have no
  emission-half counterpart and are intentionally not ported.
  `source_ref.uri` carries the target base URL because the SDK Fact wire
  shape requires it; the in-tree envelope has no URI counterpart.

## Boundaries (core-owned, not ported)

Instance selection, `token_env`/`tenant_id_env` resolution, the
heartbeat-under-lease guard, the Postgres commit boundary, cassette
replay, and `eshu_dp_loki_*` signals stay in core. The
`loki.observe`/`loki.fetch` spans stay core-traced; the host observes
the `Collect` call itself. Telemetry records against the local
`MetricsRecorder` seam; the host adapts it to its own signals.
`source_system` binding is strict (`loki` exactly): the SDK host
validator requires fact source_system to equal the claim's, so anything
else fails closed at the edge.

## Deferred to the host

- Claim acquisition wiring for `newClaimID` (the in-tree
  `service.go:93` equivalent).
- `SeriesLookback` default: the generous 24h fallback is preserved; a
  `/series` time-window exclusion stays inherently silent (Loki cannot
  report what a window excluded).

## Open gaps

- P1: the published factschema `v0.1.0-rc.5` has no per-kind
  schema-version map or `ValidateSchemaVersion` admission helper
  (compare `ObservabilitySchemaVersion` / `ValidateSchemaVersion` in
  eshu-hq/eshu `go/internal/facts`). The locked `1.0.0` map lives in
  `loki/schema.go` until the SDK gains one.
- Grant `MaxLifetime` (2160h) follows the draft's issuance example;
  expiry/rotation ownership is recorded at cutover, not here.

## Relation to eshu-collector-grafana (#8857)

This repository mirrors that layout file for file (`doc.go`, `types.go`,
`envelope.go`, `source.go`, `client.go`, `redaction.go`, `metrics.go`,
`schema.go`, `claimid.go`, `grant/`, CI). Deliberate Loki deltas:

- `normalize.go`: series/rule normalization split out of `client.go`
  so both files stay under the 500-line cap.
- `testdata/8860-parity-loki.json`: the #4047 cutover comparison input
  travels with the repo (byte-identical to in-tree).
- `MetricsRecorder` additionally covers stale and
  high-cardinality-rejected counters, which the Loki emission path
  records.
- Standalone surface: unlike the Grafana producer (a library the host
  embeds), this repository ships the process adapter (`cmd/collector/`),
  `Dockerfile`, `manifest.yaml`, and the CI/release workflows, mirroring
  `eshu-collector-pagerduty` (same SDK pins) for the release gates and
  `eshu-collector-jira` for `release.yml`/`ci.yml`. Packaging and deployment
  stay host-owned past the image.

<!-- skip-proof: docs-only lane check -->
