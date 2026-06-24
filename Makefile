# gofly toolkit Makefile
# Common developer workflows: build, test, lint, format, tidy, release.

GO        ?= go
PKGS      ?= ./...
BIN_DIR   ?= bin
CLI_BIN   ?= $(BIN_DIR)/gofly
CLI_PKG   ?= ./cmd/gofly
GOFMT_DIRS ?= app cache cmd core examples gateway ops rest rpc
TESTFLAGS ?= -count=1 -shuffle=on
SCRIPTS_DIR ?= bin/scripts

GOLANGCI_LINT ?= golangci-lint
ACTIONLINT ?= $(GO) run github.com/rhysd/actionlint/cmd/actionlint@v1.7.7
OSV_SCANNER ?= $(GO) run github.com/google/osv-scanner/v2/cmd/osv-scanner@v2.2.2
SHELLCHECK ?= shellcheck

# Governance tools are pinned with Go 1.24+ `tool` directives in go.mod.
GOVULNCHECK ?= $(GO) tool govulncheck
GOSEC       ?= $(GO) tool gosec
GOVULNCHECK_SCAN ?= package
GOSEC_FLAGS ?= -quiet -exclude-generated -exclude-dir=testdata -exclude-dir=vendor -exclude-dir=.tmp-test
GOSEC_INVENTORY_BASELINE ?= $(SCRIPTS_DIR)/gosec-exception-baseline.json
DEPENDENCY_UPGRADE_RUN_INTEGRATION ?= true

# Minimum total line coverage (percent). COVERAGE_RATCHET prevents regression once raised.
COVERAGE_THRESHOLD ?= 60
COVERAGE_RATCHET ?= 90

# Build metadata injected via -ldflags.
PKG_ROOT   := github.com/gofly/gofly/cmd/gofly/internal/command
VERSION    ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT     ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
BUILT_AT   ?= $(shell git log -1 --format=%cI 2>/dev/null || echo unknown)
LDFLAGS    := -s -w \
              -X '$(PKG_ROOT).Version=$(VERSION)' \
              -X '$(PKG_ROOT).Commit=$(COMMIT)' \
              -X '$(PKG_ROOT).BuiltAt=$(BUILT_AT)'

.DEFAULT_GOAL := help

.PHONY: help
help: ## Show this help
	@grep -E '^[a-zA-Z0-9_-]+:.*?## .*$$' $(MAKEFILE_LIST) | \
		awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-16s\033[0m %s\n", $$1, $$2}'

.PHONY: build
build: ## Build the gofly CLI into $(CLI_BIN)
	$(GO) build -trimpath -ldflags "$(LDFLAGS)" -o $(CLI_BIN) $(CLI_PKG)

.PHONY: install
install: ## Install the gofly CLI into GOBIN
	$(GO) install -trimpath -ldflags "$(LDFLAGS)" $(CLI_PKG)

# Completion script installation helpers.  Usage:
#   eval "$(make completion-install)"          # auto-detect current shell
#   make completion-install SHELL=bash          # install for bash explicitly
#   make completion-install SHELL=zsh           # install for zsh
#   make completion-install SHELL=fish          # install for fish
#   make completion-install SHELL=powershell    # install for pwsh
.PHONY: completion-install
completion-install: $(CLI_BIN) ## Install shell completion script for the current or specified $(SHELL)
	@shell="$${SHELL:-bash}"; \
	shell_name="$$(basename "$$shell")"; \
	case "$$shell_name" in \
		bash) \
			$(CLI_BIN) completion bash > /dev/null 2>&1 && eval "$$($(CLI_BIN) completion bash)" && echo "bash completion installed (requires source in .bashrc)" ;; \
		zsh) \
			mkdir -p "$${fpath[1]}" 2>/dev/null; \
			$(CLI_BIN) completion zsh > "$${fpath[1]}/_gofly" 2>/dev/null && echo "zsh completion installed to $${fpath[1]}/_gofly" || echo "could not install zsh completion; try: gofly completion zsh > ~/.zsh/completion/_gofly" ;; \
		fish) \
			mkdir -p "$(HOME)/.config/fish/completions" 2>/dev/null; \
			$(CLI_BIN) completion fish > "$(HOME)/.config/fish/completions/gofly.fish" 2>/dev/null && echo "fish completion installed to ~/.config/fish/completions/gofly.fish" ;; \
		powershell|pwsh) \
			echo "pwsh: run 'gofly completion powershell | Out-String | Invoke-Expression'"; \
			exit 0 ;; \
		*) \
			echo "unsupported shell '$$shell_name'; try: make completion-install SHELL=bash|zsh|fish|powershell"; \
			exit 1 ;; \
	esac

.PHONY: test
test: ## Run all unit tests with the race detector
	$(GO) test $(TESTFLAGS) -race $(PKGS)

.PHONY: test-short
test-short: ## Run fast unit tests (no race)
	$(GO) test $(TESTFLAGS) -short $(PKGS)

.PHONY: test-generated-matrix
test-generated-matrix: ## Verify generated project templates and service contract input matrix end-to-end
	GOFLY_FRAMEWORK_PATH=$(CURDIR) $(GO) test $(TESTFLAGS) ./cmd/gofly/internal/command -run 'Test(AINewGeneratedProjectVerificationMatrix|NewServiceGeneratedProjectSmokeMatrix|NewServiceContractInputMatrix)_BitsUT'

.PHONY: generated-control-plane-smoke
generated-control-plane-smoke: ## Run generated REST service runtime control-plane smoke without the full governance matrix
	GOVERNANCE_ONLY_GENERATED_CONTROL_PLANE_SMOKE=true GO="$(GO)" sh $(SCRIPTS_DIR)/governance-10-rounds.sh

.PHONY: bench
bench: ## Run benchmarks (exclude unit tests)
	$(GO) test -run='^$$' -bench=. -benchmem $(PKGS)

.PHONY: bench-smoke
bench-smoke: ## Run one benchmark iteration for PR smoke checks
	bash $(SCRIPTS_DIR)/benchstat.sh --smoke

.PHONY: bench-stat
bench-stat: ## Run benchmark baseline and save to bench/current.txt
	bash $(SCRIPTS_DIR)/benchstat.sh

.PHONY: bench-baseline
bench-baseline: ## Refresh tracked benchmark baseline and evidence artifacts
	bash $(SCRIPTS_DIR)/benchstat.sh --baseline

.PHONY: bench-evidence
bench-evidence: ## Write benchmark evidence from bench/baseline.txt
	bash $(SCRIPTS_DIR)/benchstat.sh --evidence

.PHONY: bench-evidence-check
bench-evidence-check: ## Validate tracked benchmark baseline, matrix, and evidence
	bash $(SCRIPTS_DIR)/benchstat.sh --check-evidence

.PHONY: bench-compare
bench-compare: ## Compare bench/current.txt against bench/baseline.txt using benchstat
	bash $(SCRIPTS_DIR)/benchstat.sh --compare

.PHONY: bench-trend
bench-trend: ## Write bench/summary.md with raw results and optional benchstat comparison
	bash $(SCRIPTS_DIR)/benchstat.sh --trend

.PHONY: bench-matrix
bench-matrix: ## Write the public REST/RPC/Gateway/Governance benchmark matrix
	bash $(SCRIPTS_DIR)/benchstat.sh --matrix

.PHONY: cover
cover: ## Run tests and write a coverage profile
	$(GO) test $(TESTFLAGS) -covermode=atomic -coverprofile=coverage.out $(PKGS)
	$(GO) tool cover -func=coverage.out | tail -n 1

.PHONY: cover-html
cover-html: cover ## Open an interactive HTML coverage report
	$(GO) tool cover -html=coverage.out

.PHONY: vet
vet: ## Run go vet on all packages
	$(GO) vet $(PKGS)

.PHONY: fmt
fmt: ## Format all Go source with gofmt
	gofmt -s -w $(GOFMT_DIRS)

.PHONY: fmt-check
fmt-check: ## Fail if any Go source is not gofmt-clean
	@out=$$(gofmt -s -l $(GOFMT_DIRS)); \
	if [ -n "$$out" ]; then echo "gofmt needed for:"; echo "$$out"; exit 1; fi

.PHONY: lint
lint: ## Run golangci-lint (requires golangci-lint installed)
	$(GOLANGCI_LINT) run $(PKGS)

.PHONY: tidy
tidy: ## Tidy and verify go.mod / go.sum
	sh $(SCRIPTS_DIR)/check-mod-tidy.sh

.PHONY: mod-verify
mod-verify: ## Verify downloaded module zip checksums against go.sum
	$(GO) mod verify

.PHONY: check
check: fmt-check vet test ## Run the core local verification suite

.PHONY: ci-fast
ci-fast: fmt-check vet build examples-check examples-smoke docs-check test tidy ## Run the default CI build/test/tidy gates

.PHONY: ci
ci: ci-fast test-generated-matrix generated-control-plane-smoke bench-evidence-check governance supply-chain ## Run the full CI verification suite

.PHONY: integration-tests
integration-tests: ## Run Docker-backed integration test packages for dependency upgrades
	@command -v docker >/dev/null 2>&1 || { echo "docker not found; install Docker or skip this Docker-backed gate intentionally"; exit 127; }
	@docker info >/dev/null 2>&1 || { echo "docker daemon is not reachable; start Docker before running integration-tests"; exit 1; }
	$(GO) test -tags=integration -count=1 ./core/storage/ ./core/config/... ./core/discovery/... ./core/mq/... ./gateway/

.PHONY: dependency-upgrade-check
dependency-upgrade-check: mod-verify govulncheck ## Validate dependency updates with module, vuln, and integration gates
	@if [ "$(DEPENDENCY_UPGRADE_RUN_INTEGRATION)" = "true" ]; then \
		$(MAKE) integration-tests; \
	else \
		echo "Skipping integration-tests here; required CI integration matrix provides Docker-backed coverage."; \
	fi

.PHONY: examples-check
examples-check: examples-copyable-check ## Build and vet all examples to keep docs and code in sync
	@if [ ! -d examples ] || ! find examples -type f -name '*.go' | grep -q .; then \
		echo "examples/ not present or empty; skipping examples-check"; \
		exit 0; \
	fi
	@for mod in examples/*/go.mod; do \
		dir=$$(dirname $$mod); \
		out=$$(mktemp -d); \
		trap 'rm -rf $$out' EXIT; \
		echo "checking $$dir"; \
		(cd $$dir && $(GO) build -o $$out/$$(basename $$dir) ./... && $(GO) vet ./...); \
	done

.PHONY: examples-copyable-check
examples-copyable-check: ## Copy each standalone example outside the repo and verify it builds
	sh $(SCRIPTS_DIR)/check-examples-copyable.sh

.PHONY: examples-smoke
examples-smoke: ## Run runnable example smoke tests and machine-readable output checks
	sh $(SCRIPTS_DIR)/examples-smoke.sh

.PHONY: docs-check
docs-check: docs-link-check docs-taxonomy-check migration-docs-check p1-growth-check community-growth-check contract-docs-check doc-manifest-sync-check required-checks-drift-check ## Compile Go code blocks in Markdown docs
	$(GO) env GOMOD >/dev/null
	sh $(SCRIPTS_DIR)/check-doc-go-snippets.sh

.PHONY: docs-taxonomy-check
docs-taxonomy-check: ## Validate Tutorial / How-to / Reference / Explanation navigation
	sh $(SCRIPTS_DIR)/check-doc-taxonomy.sh

.PHONY: migration-docs-check
migration-docs-check: ## Validate case studies and migration guide structure
	sh $(SCRIPTS_DIR)/check-migration-docs.sh

.PHONY: p1-growth-check
p1-growth-check: ## Validate P1 growth roadmap and cloud-native assets
	sh $(SCRIPTS_DIR)/check-p1-growth-assets.sh

.PHONY: community-growth-check
community-growth-check: ## Validate contributor, roadmap, and issue-template adoption signals
	sh $(SCRIPTS_DIR)/check-community-growth.sh

.PHONY: contract-docs-check
contract-docs-check: ## Validate stable CLI JSON and control-plane contract docs
	sh $(SCRIPTS_DIR)/check-contract-docs.sh

.PHONY: doc-manifest-sync-check
doc-manifest-sync-check: ## Validate AI manifest docs, examples, features, templates and verification commands
	sh $(SCRIPTS_DIR)/check-doc-manifest-sync.sh

.PHONY: required-checks-drift-check
required-checks-drift-check: ## Validate CI required checks against docs and release prerequisites
	sh $(SCRIPTS_DIR)/check-required-checks-drift.sh

.PHONY: docs-link-check
docs-link-check: ## Validate local Markdown links in docs, examples, and root README files
	sh $(SCRIPTS_DIR)/check-doc-links.sh

.PHONY: version
version: ## Print build metadata that would be embedded
	@echo "VERSION  = $(VERSION)"
	@echo "COMMIT   = $(COMMIT)"
	@echo "BUILT_AT = $(BUILT_AT)"
	@echo "LDFLAGS  = $(LDFLAGS)"

.PHONY: docker
docker: ## Build a container image tagged gofly:$(VERSION)
	docker build \
		--build-arg VERSION=$(VERSION) \
		--build-arg COMMIT=$(COMMIT) \
		--build-arg BUILT_AT=$(BUILT_AT) \
		-t gofly:$(VERSION) -t gofly:latest .

.PHONY: release-snapshot
release-snapshot: ## Produce and verify a local snapshot release via GoReleaser (requires goreleaser)
	goreleaser release --snapshot --clean --skip-publish
	sh $(SCRIPTS_DIR)/check-release-artifacts.sh

# ---- security & quality gates ------------------------------------------------
.PHONY: govulncheck
govulncheck: ## Run the Go vulnerability scanner across all packages
	$(GOVULNCHECK) -scan=$(GOVULNCHECK_SCAN) -show=traces $(PKGS)

.PHONY: gosec
gosec: ## Run gosec (Go security linter) and emit a summary report
	@GOSEC_INVENTORY_BASELINE=$(GOSEC_INVENTORY_BASELINE) sh $(SCRIPTS_DIR)/gosec-exception-inventory.sh >/dev/null
	$(GOSEC) $(GOSEC_FLAGS) ./...

.PHONY: gosec-inventory
gosec-inventory: ## Emit structured inventory for all #nosec exceptions
	@sh $(SCRIPTS_DIR)/gosec-exception-inventory.sh

.PHONY: gosec-inventory-check
gosec-inventory-check: ## Fail if #nosec inventory differs from the approved baseline
	@GOSEC_INVENTORY_BASELINE=$(GOSEC_INVENTORY_BASELINE) sh $(SCRIPTS_DIR)/gosec-exception-inventory.sh >/dev/null

.PHONY: gosec-inventory-refresh
gosec-inventory-refresh: ## Refresh the approved #nosec exception baseline after reviewed exception changes
	@tmp=$$(mktemp); \
	trap 'rm -f "$$tmp"' EXIT; \
	sh $(SCRIPTS_DIR)/gosec-exception-inventory.sh > $$tmp; \
	python3 -c 'import json, sys; from pathlib import Path; inventory = json.loads(Path(sys.argv[1]).read_text(encoding="utf-8")); baseline_path = Path(sys.argv[2]); allowed = ["|".join([entry["file"], ",".join(entry.get("rules") or []), entry.get("rationale", "")]) for entry in inventory.get("entries", [])]; payload = {"allowed_exceptions": sorted(allowed), "schema": "gofly.gosec_exception_baseline.v1"}; baseline_path.write_text(json.dumps(payload, indent=2, sort_keys=True) + "\n", encoding="utf-8")' $$tmp $(GOSEC_INVENTORY_BASELINE)

.PHONY: release-artifacts-check
release-artifacts-check: ## Verify release archives, checksums, and SBOM artifacts in dist
	sh $(SCRIPTS_DIR)/check-release-artifacts.sh

.PHONY: release-artifacts-test
release-artifacts-test: ## Run release artifact provenance fixture tests
	sh $(SCRIPTS_DIR)/check-release-artifacts-test.sh

.PHONY: cover-check
cover-check: ## Run tests with coverage and fail below threshold/ratchet (%)
	COVERAGE_THRESHOLD=$(COVERAGE_THRESHOLD) COVERAGE_RATCHET=$(COVERAGE_RATCHET) PKGS="$(PKGS)" sh $(SCRIPTS_DIR)/coverage-check.sh

.PHONY: api-compat
api-compat: ## Check public Go API compatibility against API_BASE_REF
	sh $(SCRIPTS_DIR)/check-public-api.sh

.PHONY: api-compat-test
api-compat-test: ## Run public API compatibility skip semantics fixture tests
	sh $(SCRIPTS_DIR)/check-public-api-test.sh

.PHONY: actionlint
actionlint: actions-pin-check ## Lint GitHub Actions workflows
	$(ACTIONLINT) .github/workflows/*.yml

.PHONY: actions-pin-check
actions-pin-check: ## Fail if GitHub Actions are not pinned to full commit SHAs
	sh $(SCRIPTS_DIR)/check-actions-pinned.sh

.PHONY: shellcheck
shellcheck: ## Lint governance shell scripts
	@command -v $(SHELLCHECK) >/dev/null 2>&1 || { echo "shellcheck not found; install shellcheck or set SHELLCHECK=<path>"; exit 127; }
	$(SHELLCHECK) $(SCRIPTS_DIR)/*.sh

.PHONY: osv-scan
osv-scan: ## Scan lockfiles and manifests with OSV Scanner
	$(OSV_SCANNER) scan source --recursive .

.PHONY: supply-chain
supply-chain: actionlint shellcheck release-artifacts-test api-compat-test osv-scan ## Run workflow, shell, release/API provenance, action pin, and OSV supply-chain checks

.PHONY: governance
governance: governance-10-rounds api-compat ## Run governance gates

.PHONY: governance-10-rounds
governance-10-rounds: ## Run the no-cache architecture and quality governance workflow
	COVERAGE_THRESHOLD=$(COVERAGE_THRESHOLD) COVERAGE_RATCHET=$(COVERAGE_RATCHET) sh $(SCRIPTS_DIR)/governance-10-rounds.sh

.PHONY: security
security: govulncheck gosec ## Run govulncheck + gosec (shortcut)

.PHONY: clean
clean: ## Remove build and coverage artifacts
	rm -rf $(BIN_DIR) coverage.out dist
