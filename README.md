# eshu-collector-loki

Extracted Loki log-signal collector producer for [eshu-hq/eshu#8860](https://github.com/eshu-hq/eshu/issues/8860)
(collector-loki extraction, part of epic #6707). Emission half only:
this repository fetches bounded live Loki metadata and returns a
`collector-sdk/v1alpha1` Result per host claim. Core keeps admission,
projection, persistence, claim mutation, fencing, and the status read.

Refs #8860. This repository does not close that issue: cutover stays gated
by #4047 with production acceptance NOT_CHECKED, and no pair is a
qualified consumer/core combination yet (see SDK Compatibility in
eshu-hq/eshu docs).

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

## Layout

- `loki/` — the producer: bounded REST fetch (`client.go`,
  `normalize.go`), four envelope constructors (`envelope.go`),
  claim-to-Result fan-out (`source.go`), metadata-only redaction
  (`redaction.go`), kind/schema admission (`schema.go`), claim-ID minting
  (`claimid.go`), and a host-wired metrics seam (`metrics.go`).
  `testdata/8860-parity-loki.json` is the cutover comparison input,
  byte-identical to the in-tree parity pack.
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
- No Dockerfile, chart, or collector main: the extracted producer is a
  library the host embeds, same as the Grafana producer. Packaging and
  deployment stay host-owned.
