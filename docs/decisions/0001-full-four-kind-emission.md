# ADR-0001: The manifest owns the full four-kind emission

Status: Accepted
Date: recorded in `manifest.yaml` and `doc.go` (satellite release v0.1.2)

## Context

This collector emits four core-owned observability kinds. It had to be settled whether the manifest declares all four in emission order, or a subset, and how that interacts with co-installing the Tempo collector, which shares observability kinds.

## Decision

Per the arbiter verdict recorded in `manifest.yaml` and `doc.go` (eshu-hq/eshu#9202, satellite release v0.1.2): the manifest owns all four observed kinds in emission order. The emission edge still checks the core-issued producer grant via `grant.CoveredKinds` before any provider contact.

## Consequences

- Co-installing this collector next to the Tempo collector needs a host that supports grant-aware sharing of the shared observability kinds. On an older host the second install fails closed with a fact-kind collision.
- This repo proves the manifest side only. Host support lives in Eshu core.
- If a fact kind ever changes, the manifest and [Facts](../reference/facts.md) change together.
