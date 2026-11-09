# Running and shipping the Loki collector

Day to day, you won't babysit this binary. The host starts it, feeds it one claim, and reads one result. This page covers the bits you do touch: installing it, reading its signals, and sorting out trouble.

## How the host runs it

The extension host uses its `process` runner. It writes one bounded `collector-sdk/v1alpha1` Request to stdin and reads one Result from stdout. With no arguments, or with `--sdk-stdio`, the binary serves exactly one claim that way. There's no daemon mode and no HTTP server hiding in here.

For a local smoke test with a recorded request:

```bash
collector --sdk-stdio < request.json > result.json
```

## Image and seed

The release image is `ghcr.io/eshu-hq/eshu-collector-loki`. Inside you'll find one static binary at `/usr/local/bin/collector` on a distroless base with no shell, running as user `65532:65532`. The binary installs itself, which still feels a little neat. An init container (the setup container that runs before the main one) runs:

```bash
/usr/local/bin/collector seed --dest DIR [--image-ref REPO@sha256:DIGEST]
```

Seed writes `DIR/collector` as a byte copy with mode 0755 and `DIR/manifest.yaml` with mode 0644. Run it twice and nothing bad happens: files that already match stay put, and changed files swap in atomically.

The `--image-ref` flag, or the `COLLECTOR_IMAGE_REF` environment variable when the flag is absent, stamps the deployed digest into the seeded manifest. An image can't embed its own digest, so this is how the manifest learns it. The value must look like `repository@sha256:<64 hex digits>`. Anything else exits non-zero and writes nothing. Skip it entirely and the placeholder from `manifest.yaml` (an all-zero digest) goes out unchanged. After seeding, the host runs the seeded binary directly. It doesn't pull the image again per claim.

Manifest identity, for your runbook: `dev.eshu.collector.loki`, version `0.1.2`, collector kind `loki`, platform `linux/amd64`, compatible core `>=0.0.5 <0.2.0`.

## The claim deadline, and why size matters

Each claim runs against a hard clock. The host kills the process at the initial lease expiry, so the collector stops starting provider calls `min(2s, 5% of remaining time)` early and returns a retryable `claim_deadline_exceeded` with no facts instead of dying mid-read. Reads run sequentially (labels, then series, then rules), so a large target needs a worker lease sized to match. If you see deadline results clustering on one target, that's your first knob.

## Signals you'll actually see

Results land in one of four states, and learning to read them saves you a lot of guessing:

- `complete` means labels, series, and rules each finished on their own. Nothing to do.
- `partial` means you got facts plus at least one coverage warning, say a permission gap, an unsupported ruler, throttling, or truncation. Facts still commit, so treat this as "useful, with footnotes."
- `retryable` means try again later. You'll see `retryable`, `claim_deadline_exceeded`, or `canceled`, with `retry_after_seconds` when the provider offered guidance.
- `terminal` means stop and fix something. Expect classes like `config_invalid`, `credential_missing`, `invalid_claim`, `result_too_large`, or `fact_invalid`.

The failure class and counts ride along in the result status. No status or log line carries a provider body, a token, a tenant ID, or a credential-bearing URL, ever. The collector also writes one `collector health: {...}` line per claim to stderr, which helps during local runs and tests. In production the host keeps only a byte count of stderr and drops the text, so don't build alerts on it.

About metrics, honestly: the manifest declares `telemetry.metricsPrefix: eshu_dp_loki_collector_`, but nothing in this repo emits a metric under that prefix yet. There's no metrics endpoint and no OpenTelemetry export from this binary. The result's failure class is your operator signal today.

## When something looks wrong

Start with the boring checks, because they usually win. A `terminal` result with `config_invalid` points at the activation file, so diff it against [config.example.yaml](../../config.example.yaml) and watch for stray keys. `credential_missing` means a named env var didn't resolve in the collector's environment. Check the variable name in config first, then the workload's env.

Repeated `partial` results deserve a look at the warnings. Permission warnings suggest the credential can't see part of Loki. Truncation warnings suggest `resource_limit` or a label cap is doing its job a little too well. An unsupported-ruler warning on a target with no ruler is expected, not a bug.

If claims keep coming back `retryable` with `claim_deadline_exceeded`, the target is probably bigger than the lease. Raise the lease, narrow the read with matchers, or both. And when you truly can't tell what's happening, run the binary locally with the activation's config and a recorded request. The stderr health line plus the result JSON usually give the game away.

## Build, test, release

```bash
make ci          # lint + test + race + conformance script
make lint        # vet, golangci-lint, format diff, tidy diff, 500-line cap
make test        # go build ./... and go test ./... -count=1
make race        # race tests plus the coverage floor (90.1%)
make actionlint  # workflow lint
docker build -t eshu-collector-loki:local .
```

CI in `.github/workflows/ci.yml` stays the authority, and `make ci` mirrors it locally, including `scripts/run-conformance.sh`.

Releases run from `.github/workflows/release.yml` on pushes to `main` and on version tags. Nothing publishes from a pull request, and the workflow deploys nothing. A `main` push publishes moving `main`, `latest`, and `sha-<full-sha>` tags. A version tag must match `metadata.version` in `manifest.yaml` and point at the protected `main` head. The workflow builds `linux/amd64`, pushes `ghcr.io/eshu-hq/eshu-collector-loki:<tag>`, signs the digest with cosign (keyless), and attaches SLSA provenance plus an SPDX SBOM attestation. Copy the digest from the job summary into your activation, since no commit carries it. Keep the GHCR package public so activations can pull without a token.

One trust caveat worth repeating in runbooks: plain `cosign sign` produces no Eshu trust annotations, so allowlist trust is the supported install mode today. Strict trust is a follow-up, not a switch you can flip yet.

## SDK pins

Exact pins from `go.mod`, no `replace` directives. Trust the file over this page if they ever disagree.

| Module | Pin |
| --- | --- |
| `github.com/eshu-hq/eshu-sdk/collector` | `v0.2.0-rc.1` |
| `github.com/eshu-hq/eshu-sdk/factschema` | `v0.1.0-rc.5` |
| `gopkg.in/yaml.v3` | `v3.0.1` |

Go version in `go.mod`: `1.26.10`. One known gap: the pinned `factschema` has no per-kind schema-version helper, so the locked `1.0.0` map lives in `loki/schema.go` until the SDK provides one.
