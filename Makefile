# Local parity for CI: `make ci` runs the lint, test, and race lane
# commands the shared reusable workflow runs for .github/workflows/ci.yml,
# plus the conformance script the CI conformance job executes. CI stays authoritative; this target is the
# whole local contract (see README.md, "Build and test").
#
#   make ci            # lint + test + race (with the coverage floor) + conformance script
#   make lint          # vet, golangci-lint, gofumpt, tidy, 500-line cap
#   make test          # build + full test suite
#   make race          # race suite + coverprofile + coverage floor
#   make actionlint    # workflow lint (mirrors the actionlint lane)

GOLANGCI_LINT_VERSION ?= v2.12.2
COVERAGE_FLOOR ?= 90.1

.PHONY: ci lint test race actionlint tools-lint

ci: lint test race
	bash scripts/run-conformance.sh

lint: tools-lint
	go vet ./...
	golangci-lint run ./...
	golangci-lint fmt --diff ./...
	go mod tidy -diff
	git diff --exit-code -- go.mod go.sum
	over=$$(git ls-files '*.go' | while read -r f; do n=$$(wc -l < "$$f"); if [ "$$n" -gt 500 ]; then echo "$$f: $$n lines"; fi; done); \
	if [ -n "$$over" ]; then echo "$$over"; echo "FAIL: .go files over 500 lines"; exit 1; fi
	git diff --check

test:
	go build ./...
	go test ./... -count=1

race:
	go test ./... -race -count=1 -covermode=atomic -coverprofile=coverage.out
	go tool cover -func=coverage.out | tail -n 5
	total=$$(go tool cover -func=coverage.out | awk '/^total:/ {gsub(/%/, "", $$3); print $$3}'); \
	echo "total=$${total}% floor=$(COVERAGE_FLOOR)%"; \
	awk -v t="$$total" -v f="$(COVERAGE_FLOOR)" 'BEGIN { if (t + 0 < f + 0) { print "FAIL: coverage below floor"; exit 1 } }'

# Keep this version equal to the actionlint-version input of the lanes job
# in .github/workflows/ci.yml (1.7.12) so local and CI findings agree.
actionlint:
	go run github.com/rhysd/actionlint/cmd/actionlint@v1.7.12 .github/workflows/*.yml

tools-lint:
	@if ! command -v golangci-lint >/dev/null; then \
		go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION); \
	fi
