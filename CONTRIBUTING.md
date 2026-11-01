# Contributing

The test suite is the product contract: the parity, golden, hostile-input,
and boundary tests must stay green. Never weaken them to land a change.

Credentials travel by env reference, never inline. The handler resolves
token and tenant variables through `Handler.Getenv`; tests inject a stub.
Never log a token, tenant ID, URL, or provider body.

## Local checks

You need Go (see `go.mod`), `golangci-lint` v2.12.2, and Docker for the
image lane.

```sh
make ci   # lint + test + race, the whole local contract
```

`make lint` runs `go vet`, whole-repo `golangci-lint run` (config in
`.golangci.yml`, org lint set; no baselines), the gofumpt format check,
`go mod tidy -diff`, and the 500-line cap on `.go` files. `make race`
also enforces the statement-coverage floor (`COVERAGE_FLOOR`, at 90.1%
and may only rise). `make actionlint` mirrors the actionlint lane.

`bash scripts/loki-seed-matrix.sh` is a local runner (not a CI gate) that plants a
seeded defect for each of vet, lint, format, race, tidy, the 500-line cap, and the
coverage floor, asserts the lane turns red and names the seed, then asserts green
after removal and leaves the tree as it found it.

## CI lanes

`.github/workflows/ci.yml` is a thin caller. It runs a `changes` job
first, so a PR only pays for lanes its paths can affect, then calls the
pinned `eshu-hq/.github` `go-collector-ci` reusable workflow as the
`lanes` job (lane checks nest as `lanes / lint`, `lanes / test`, and so
on). Go sources run `lint`, `test`, `race`, `image` (the Dockerfile
builds the binary from them), `boundary`, `govulncheck`, and `gosec`;
fixtures and embedded config run `test`; shell scripts run `shell` and
`test`; `Dockerfile` and manifest changes run `image`; dependency
changes run `govulncheck`; workflow changes run `actionlint` plus the Go
lanes. The `conformance` job runs `scripts/run-conformance.sh` under the
same gate as `test`, so a script-only PR still executes the script.
`ci-complete` is the only required check: it fails when a needed job
fails and passes when lanes were legitimately skipped (docs-only PRs).

Weekly, `govulncheck` and `gosec` run on a schedule. Scheduled findings
fail the run and the `scheduled-findings` job files or updates one
combined issue. `gosec` stays advisory on PRs until its false-positive
rate is measured (issue #14); its SARIF reaches the Security tab through
the `gosec-sarif` job on non-PR runs. Dependabot proposes weekly grouped
`gomod` and `github-actions` bumps. `make ci` runs the conformance script
too.
