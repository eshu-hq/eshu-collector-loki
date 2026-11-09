# Concepts

## What this is

A pre-release collector that takes a quick, careful look at a live Loki target and reports what it finds as facts — the small checked records Eshu stores as evidence. It reads labels, series shape, and ruler rules. Log lines themselves never leave Loki through this code.

## Why it exists

Eshu should know which log labels, series, and ruler rules a Loki target really serves right now, not just what a config file promised last quarter. Live shape evidence makes drift visible.

## The one idea to keep: shape, not content

Facts hold label keys, value counts, value fingerprints, and rule identities. Raw LogQL, label values in the clear, tenant IDs, credentials, log lines, and provider response bodies never enter a fact. Where identity matters, a fingerprint or presence flag stands in.

## Claims, grants, and declared vs observed

- A **claim** is one bounded unit of work from the host; the host drives everything one claim at a time.
- A **grant** is core's permission slip for emitting a core-owned kind, checked before any provider contact.
- **Declared vs observed**: `targets[].declared_ids` names identities declared elsewhere; a matching live series or ruler rule earns `matched_declared`, and `observed_only_hint` can flag the rest as likely hand-made. The code-exact meaning of both settings is in [Configuration](../reference/configuration.md) — it is narrower than it first sounds (the label-set signal, for one, is never compared).

## Boundaries

It never writes to Loki, never persists log lines, holds no Postgres, graph, queue, or lease handle, and never imports Eshu core internals. It does not build the durable envelope, commit facts, or decide a claim's fate beyond the bounded failure class it reports.
