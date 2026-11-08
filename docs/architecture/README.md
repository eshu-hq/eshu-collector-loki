# Architecture

Think of this collector as a careful census taker. It knocks on Loki's metadata endpoints, counts what's there, and leaves the actual log lines alone. Each visit covers one configured target, and the visit only happens because the host asked.

## The neighborhood

See [Diagrams](../diagrams/README.md) for the picture.

Text version: Loki API feeds this collector, the collector feeds the extension host, and the host feeds Eshu core. The host owns claims, leases (timed ownership of the work), envelope building, validation, and commits. Core owns admission, projection, and persistence. This collector's whole job sits between "host asked" and "here's what Loki said."

## One claim, start to finish

See [Diagrams](../diagrams/README.md) for the picture.

Same story without the diagram: the host writes a `collector-sdk/v1alpha1` Request to stdin, the collector sorts out config and credentials before any network call, then reads labels and series first and rules second. Sequential reads keep the blast radius small and the deadline math honest. Facts go back on stdout, and the host takes it from there.

A couple of details matter here. The grant check runs before provider contact, so a bad grant means Loki never hears from you. The deadline guard runs throughout: the collector stops starting new provider calls `min(2s, 5% of remaining time)` before the host's kill line and returns a retryable `claim_deadline_exceeded` with no facts. An incomplete read would rather retry cleanly than sneak in as partial evidence.

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

## Declared versus observed, briefly

You can tell the collector which identities you already declare elsewhere, through `targets[].declared_ids`. The comparison runs in `loki/normalize.go` (`declaredMatchState`) for exactly two record types: a series, matched by its series fingerprint (the hash of its sorted label keys interleaved with their label values), and a ruler rule, matched by its `namespace/group:rule` identity (with `group:rule`, `rule:name`, and query-fingerprint fallbacks). An exact hit earns `matched_declared`; everything else stays `not_compared` — including the label-set signal, which is never compared at all. If you also set `observed_only_hint`, each unmatched series or rule is flagged manually created (`manually_created: true`, `drift_candidate_reason: manual_provider_resource`) while its match state still reads `not_compared`. Nothing is dropped either way. The precise settings live in [Configuration](../reference/configuration.md).

## Boundaries it respects

A few refusals keep this collector trustworthy. It never writes to Loki and never persists log lines. It holds no Postgres, graph, queue, or lease handle, and it never imports Eshu core internals ( `boundary_test.go` checks that on every run). It doesn't build the durable envelope or commit facts, and it doesn't decide a claim's fate beyond the bounded failure class it reports. It also never emits kinds outside the four above, no matter what Loki returns.
