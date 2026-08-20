.PHONY: build check contract-check contract-release-check coverage frontend-check integration lint security test workflow-check

GOLANGCI_LINT_VERSION ?= v2.12.2
GOVULNCHECK_VERSION ?= v1.6.0
ACTIONLINT_VERSION ?= v1.7.10
NPM ?= npx --yes npm@12.0.2
GO_FILES := $(shell find cmd internal -name '*.go' -type f)

build:
	$(NPM) --prefix frontend run build
	mkdir -p bin
	go build -mod=vendor -trimpath -ldflags "-s -w" -o bin/cineko-central ./cmd/cineko-central

lint:
	@test -z "$$(gofmt -l $(GO_FILES))" || (gofmt -l $(GO_FILES) && exit 1)
	go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION) run ./...

security:
	go run golang.org/x/vuln/cmd/govulncheck@$(GOVULNCHECK_VERSION) ./...
	$(NPM) --prefix frontend audit --audit-level=moderate

coverage:
	bash scripts/unit-coverage.sh

test:
	go test -mod=vendor -race ./...

integration:
	CINEKO_CENTRAL_INTEGRATION=1 go test -mod=vendor -race ./internal/central/postgres -count=1 -v

frontend-check:
	$(NPM) --prefix frontend run check

contract-check:
	grep -Eq '^# github.com/cineko-org/contracts/v3 v3.3.0( => ../contracts)?$$' vendor/modules.txt

contract-release-check:
	@! grep -Eq '^[[:space:]]*replace([[:space:]]|\()' go.mod
	@grep -Eq '^[[:space:]]*github.com/cineko-org/contracts/v3 v3.3.0$$' go.mod
	@grep -Eq '^# github.com/cineko-org/contracts/v3 v3.3.0$$' vendor/modules.txt
	@grep -Eq '^github.com/cineko-org/contracts/v3 v3.3.0 h1:' go.sum

workflow-check:
	go run github.com/rhysd/actionlint/cmd/actionlint@$(ACTIONLINT_VERSION) .github/workflows/*.yml
	python3 scripts/verify-release-metadata.py

check: lint security coverage test integration frontend-check contract-release-check workflow-check
