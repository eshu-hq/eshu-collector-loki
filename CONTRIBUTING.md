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

## CI lanes

`.github/workflows/ci.yml` runs a `changes` job first, so a PR only pays
for lanes its paths can affect: Go sources run `lint`, `test`, `race`,
`image` (the Dockerfile builds the binary from them), `boundary`,
`govulncheck`, and `gosec`; fixtures and embedded config run `test`;
shell scripts run `shell` and `test` (the test lane executes
`scripts/run-conformance.sh`); `Dockerfile` and manifest changes run
`image`;
dependency changes run `govulncheck`; workflow changes run `actionlint`
plus the Go lanes. `ci-complete` is the only required check: it fails
when a needed lane fails and passes when lanes were legitimately skipped
(docs-only PRs).

Weekly, `govulncheck` and `gosec` (SARIF to the Security tab, advisory
until its false-positive rate is measured) run on a schedule and file an
issue on findings. Dependabot proposes weekly grouped `gomod` and
`github-actions` bumps. `scripts/run-conformance.sh` is the test lane
body in `.github/workflows/ci.yml`, and `make ci` runs it too.
