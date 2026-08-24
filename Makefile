# The release version, read from the one file that owns it. Never hardcode it
# here: scripts/check-version.sh guards thirteen other sources and does not know
# about this file, so a copy kept here drifts silently — as it had, at 2.11.4
# through two releases.
MDDB_VERSION := $(shell sed -n 's/.*VERSION[[:space:]]*=[[:space:]]*"\([^"]*\)".*/\1/p' services/mddbd/main.go | head -1)

.PHONY: build build-windows check-source-encoding test-source-encoding docs-linkcheck help dev-start dev-stop dev-logs dev-build dev-clean test lint fmt fmt-check vet sec test-graphql lint-all test-all ci chat-build chat-dev chat-test widget-build widget-dev dev-logs-chat check-version test-version agent-instructions check-agent-instructions check-changelog test-changelog docs-metadata docs-dev docs-build airbyte-build airbyte-push airbyte-test airbyte-spec airbyte-check airbyte-clean gha-install gha-build gha-test gha-coverage gha-lint gha-check gha-verify-dist gha-clean chrome-install chrome-build chrome-package chrome-test chrome-coverage chrome-lint chrome-audit chrome-check chrome-clean grafana-install grafana-build grafana-test grafana-coverage grafana-lint grafana-check grafana-package grafana-docker grafana-clean

help: ## Show this help message
	@echo 'Usage: make [target]'
	@echo ''
	@echo 'Available targets:'
	@awk 'BEGIN {FS = ":.*?## "} /^[a-zA-Z_-]+:.*?## / {printf "  \033[36m%-15s\033[0m %s\n", $$1, $$2}' $(MAKEFILE_LIST)

dev-start: ## Start all services in development mode
	@echo "🚀 Starting MDDB development environment..."
	docker compose -f docker-compose.dev.yml up -d
	@echo "✅ Services started!"
	@echo ""
	@echo "📍 Available services:"
	@echo "   - MDDB Server:  http://localhost:11023"
	@echo "   - MDDB Panel:   http://localhost:3000"
	@echo "   - MCP Server:   http://localhost:9000"
	@echo "   - gRPC:         localhost:11024"
	@echo "   - Chat Server:  http://localhost:11030"
	@echo "   - Chat Widget:  http://localhost:11032"
	@echo ""
	@echo "🔑 Default credentials: admin / admin123"
	@echo ""
	@echo "📊 View logs: make dev-logs"

dev-start-with-ollama: ## Start all services including Ollama
	@echo "🚀 Starting MDDB with Ollama for vector embeddings..."
	docker compose -f docker-compose.dev.yml --profile with-ollama up -d
	@echo "✅ Services started with Ollama!"

dev-stop: ## Stop all development services
	@echo "🛑 Stopping MDDB development environment..."
	docker compose -f docker-compose.dev.yml down
	@echo "✅ Services stopped!"

dev-logs: ## Show logs from all services
	docker compose -f docker-compose.dev.yml logs -f

dev-logs-server: ## Show logs from MDDB server only
	docker compose -f docker-compose.dev.yml logs -f mddbd

dev-logs-panel: ## Show logs from MDDB panel only
	docker compose -f docker-compose.dev.yml logs -f mddb-panel

dev-logs-mcp: ## Show MCP logs (MCP is built into the mddbd server)
	docker compose -f docker-compose.dev.yml logs -f mddbd

dev-build: ## Rebuild all Docker images
	@echo "🔨 Rebuilding Docker images..."
	docker compose -f docker-compose.dev.yml build --no-cache
	@echo "✅ Build complete!"

dev-clean: ## Stop services and remove volumes
	@echo "🧹 Cleaning up development environment..."
	docker compose -f docker-compose.dev.yml down -v
	@echo "✅ Cleanup complete!"

dev-restart: dev-stop dev-start ## Restart all services

dev-shell-server: ## Open shell in MDDB server container
	docker compose -f docker-compose.dev.yml exec mddbd sh

dev-shell-panel: ## Open shell in MDDB panel container
	docker compose -f docker-compose.dev.yml exec mddb-panel sh

test: ## Run all Go tests
	@echo "🧪 Running backend tests..."
	cd services/mddbd && go test -v -timeout 5m ./...
	cd clients/go/mddb && go test -timeout 5m ./...
	cd services/mddb-cli && go test -timeout 5m ./...
	cd tools/bench && go test -timeout 5m ./...
	@echo "✅ Tests passed!"

test-langchain: ## Run the LangChain adapter tests (INT-017)
	@cd integrations/langchain-mddb && python3 -m pytest -q

test-clients: ## Run the Node.js and Python client tests (TEST-001)
	@echo "🧪 Running Node.js client tests..."
	cd clients/nodejs && npm test
	@echo "🧪 Running Python client tests..."
	cd clients/python && python3 -m pytest -q
	@echo "✅ Client tests passed!"

test-chat: ## Run the mddb-chat (Rust) tests
	@echo "🧪 Running mddb-chat tests..."
	cd services/mddb-chat && cargo test
	@echo "✅ Chat tests passed!"

test-coverage: ## Run tests with coverage
	@echo "🧪 Running tests with coverage..."
	cd services/mddbd && go test -v -coverprofile=coverage.out ./...
	cd services/mddbd && go tool cover -html=coverage.out -o coverage.html
	@echo "✅ Coverage report generated: services/mddbd/coverage.html"

lint: ## Run linter
	@echo "🔍 Running linter..."
	cd services/mddbd && golangci-lint run --timeout 5m
	cd clients/go/mddb && golangci-lint run --timeout 5m
	@echo "✅ Linting passed!"

fmt: ## Format Go code (all modules)
	@echo "🎨 Formatting Go code..."
	gofmt -s -w services/ tools/ test/ clients/go/
	@echo "✅ Code formatted!"

fmt-check: ## Fail if any Go file is not gofmt-formatted (GO-012 — CI gate)
	@echo "🎨 Checking Go formatting..."
	@UNFORMATTED=$$(gofmt -s -l services/ tools/ test/ clients/go/); \
	if [ -n "$$UNFORMATTED" ]; then \
		echo "❌ Files not gofmt-formatted (run 'make fmt'):"; \
		echo "$$UNFORMATTED"; \
		exit 1; \
	fi
	@echo "✅ All Go files gofmt-formatted!"

vet: ## Run go vet
	@echo "🔍 Running go vet..."
	cd services/mddbd && go vet ./...
	cd clients/go/mddb && go vet ./...
	cd services/mddb-cli && go vet ./...
	@echo "✅ go vet passed!"

sec: ## Run security scanner (gosec)
	@echo "🔒 Running security scan..."
	cd services/mddbd && gosec -quiet -exclude-generated -exclude=G115 ./...
	cd clients/go/mddb && gosec -quiet -exclude-generated -exclude=G115 ./...
	cd services/mddb-cli && gosec -quiet -exclude-generated -exclude=G115 ./...
	@echo "✅ Security scan passed!"

test-graphql: ## Run GraphQL tests with coverage
	@echo "🧪 Running GraphQL tests with coverage..."
	cd services/mddbd && go test -v -coverprofile=coverage-graphql.out ./graphql/...
	cd services/mddbd && go tool cover -html=coverage-graphql.out -o coverage-graphql.html
	@echo "✅ GraphQL tests passed! Coverage: services/mddbd/coverage-graphql.html"

lint-all: fmt vet sec lint ## Run all linters
	@echo "✅ All linting passed!"

test-all: test test-graphql test-clients test-langchain ## Run every test suite in the monorepo
	@echo "✅ All tests passed!"

ci: check-go-version check-version check-action-pins check-repo-links check-source-encoding fmt-check lint-all test-all ## Run full CI pipeline (lint + test)
	@echo "✅ CI pipeline complete!"

dev-logs-chat: ## Show logs from chat server only
	docker compose -f docker-compose.dev.yml logs -f mddb-chat

chat-build: ## Build chat server (requires Rust)
	cd services/mddb-chat && cargo build --release

chat-dev: ## Run chat server in dev mode
	cd services/mddb-chat && cargo watch -x run

chat-test: ## Run chat server tests
	cd services/mddb-chat && cargo test

widget-build: ## Build chat widget
	cd services/mddb-chat-widget && npm run build

widget-dev: ## Run widget dev server
	cd services/mddb-chat-widget && npm run dev

version: ## Show current version
	@echo "MDDB Version: $(MDDB_VERSION)"

build: ## Build the server and CLI for the host platform
	@echo "Building $(MDDB_VERSION) for $$(go env GOOS)/$$(go env GOARCH)"
	@set -e; \
	cd services/mddbd && go build -trimpath -ldflags="-s -w -X main.Version=$(MDDB_VERSION)" -o mddbd . ; \
	cd ../mddb-cli && go build -trimpath -ldflags="-s -w -X main.Version=$(MDDB_VERSION)" -o mddb-cli .
	@echo "  services/mddbd/mddbd"
	@echo "  services/mddb-cli/mddb-cli"

build-windows: ## Cross-compile the Windows binaries into dist/windows-amd64 (WIN-001)
	@# amd64 only. windows/arm64 is deliberately not built — see audit WIN-001.
	@echo "Building $(MDDB_VERSION) for windows/amd64"
	@mkdir -p dist/windows-amd64
	@set -e; \
	cd services/mddbd && CGO_ENABLED=0 GOOS=windows GOARCH=amd64 \
		go build -trimpath -ldflags="-s -w -X main.Version=$(MDDB_VERSION)" \
		-o ../../dist/windows-amd64/mddbd.exe . ; \
	cd ../mddb-cli && CGO_ENABLED=0 GOOS=windows GOARCH=amd64 \
		go build -trimpath -ldflags="-s -w -X main.Version=$(MDDB_VERSION)" \
		-o ../../dist/windows-amd64/mddb-cli.exe .
	@ls -l dist/windows-amd64

check-go-version: ## Verify Go toolchain pins are consistent (go.work/go.mod/CI/Docker)
	@bash scripts/check-go-version.sh --print

test-go-version: ## Run the Go-version-drift guard test suite
	@bash scripts/tests/test-go-version.sh

check-version: ## Verify the release version matches across every source
	@bash scripts/check-version.sh --print

test-version: ## Run the release-version drift guard test suite
	@bash scripts/tests/test-version.sh

agent-instructions: ## Regenerate the Claude Code / Cursor / Windsurf variants from AGENTS.md
	@python3 scripts/gen-agent-instructions.py

check-agent-instructions: ## Fail if the generated agent instructions are stale
	@python3 scripts/gen-agent-instructions.py --check

check-changelog: ## Verify the CHANGELOG has exactly one, leading [Unreleased]
	@bash scripts/check-changelog.sh

test-changelog: ## Run the CHANGELOG structure guard test suite
	@bash scripts/tests/test-changelog.sh

check-source-encoding: ## Verify every tracked text file is plain UTF-8 (no UTF-16, no BOM)
	@bash scripts/check-source-encoding.sh

test-source-encoding: ## Run the source-encoding guard test suite
	@bash scripts/tests/test-source-encoding.sh

check-proto-plugins: ## Verify the protobuf plugin pin matches the runtime in go.mod
	@bash scripts/check-proto-plugins.sh --print

test-proto-plugins: ## Run the protobuf plugin/runtime guard test suite
	@bash scripts/tests/test-proto-plugins.sh

coverage-areas: ## Report per-file coverage for the tracked high-risk areas
	@cd services/mddbd && go test -coverprofile=coverage.out ./... > /dev/null 2>&1
	@bash scripts/check-coverage-areas.sh --print

bench-comparison: ## Measure the numbers docs/COMPARISON.md publishes (DOC-013)
	@echo "🧪 Profiling MDDB — start a server first (make dev-start or ./mddbd)"
	@cd test && go run ./mddb-profile -markdown $(BENCH_ARGS)

bench-comparison-all: ## Full cross-database comparison (needs Docker)
	@cd test && bash compare-all-databases.sh

check-repo-links: ## Verify every relative Markdown link resolves (DOC-013)
	@bash scripts/check-repo-links.sh

test-repo-links: ## Run the repo-link guard test suite
	@bash scripts/tests/test-repo-links.sh

check-action-pins: ## Verify every GitHub Action is pinned to a commit SHA (OPS-018)
	@bash scripts/check-action-pins.sh

test-action-pins: ## Run the action-pin guard test suite
	@bash scripts/tests/test-action-pins.sh

test-coverage-areas: ## Run the per-area coverage guard test suite
	@bash scripts/tests/test-coverage-areas.sh

mcp-tools-count: ## Verify docs' built-in MCP tool count matches the code (DOC-001)
	@cd services/mddbd && go test -run TestMCPToolCountDocsInSync -count=1 . && \
		echo "✅ docs MCP tool count matches len(mcpBuiltinTools())"

docs-metadata: ## Write the empty docs/metadata.json SSG expects (gitignored)
	@test -f docs/metadata.json || echo '{"categories":[],"media":[],"users":[]}' > docs/metadata.json

docs-dev: docs-metadata ## Start SSG docs server in watch mode on :8888
	@ssg --config .ssg.yaml --watch --http --port 8888

docs-build: docs-metadata ## Build static documentation site into dist/
	@ssg --config .ssg.yaml --clean

docs-linkcheck: docs-build ## Fail if any internal link would hit a redirect
	@python3 scripts/check-docs-links.py dist

# ----- Airbyte destination connector (integrations/airbyte-destination) -----

airbyte-build: ## Build Airbyte destination docker image
	@$(MAKE) -C integrations/airbyte-destination build

airbyte-push: ## Push Airbyte destination image to registry
	@$(MAKE) -C integrations/airbyte-destination push

airbyte-test: ## Run Airbyte destination unit tests (pytest + coverage)
	@$(MAKE) -C integrations/airbyte-destination test

airbyte-spec: ## Print Airbyte destination spec JSON
	@$(MAKE) -C integrations/airbyte-destination spec

airbyte-check: ## Smoke-check Airbyte destination (URL=<mddb base url>)
	@$(MAKE) -C integrations/airbyte-destination check URL=$(URL)

airbyte-clean: ## Remove Airbyte destination local build artifacts
	@$(MAKE) -C integrations/airbyte-destination clean

# ----- GitHub Action: mddb-sync (integrations/github-action) -----

gha-install: ## Install github-action npm dependencies
	@$(MAKE) -C integrations/github-action install

gha-build: ## Bundle github-action dist/index.js with @vercel/ncc
	@$(MAKE) -C integrations/github-action build

gha-test: ## Run github-action Jest unit tests
	@$(MAKE) -C integrations/github-action test

gha-coverage: ## Run github-action tests with coverage (>=90%)
	@$(MAKE) -C integrations/github-action test-coverage

gha-lint: ## Lint github-action TypeScript sources
	@$(MAKE) -C integrations/github-action lint

gha-check: ## Format check + lint + tests + build for github-action
	@$(MAKE) -C integrations/github-action check

gha-verify-dist: ## Build github-action and assert dist/ is up to date
	@$(MAKE) -C integrations/github-action verify-dist

gha-clean: ## Remove github-action build artefacts and node_modules
	@$(MAKE) -C integrations/github-action clean

# ----- Chrome extension: mddb-browser (integrations/chrome-extension) -----

chrome-install: ## Install chrome-extension npm dependencies
	@$(MAKE) -C integrations/chrome-extension install

chrome-build: ## Bundle chrome-extension into ./build via esbuild
	@$(MAKE) -C integrations/chrome-extension build

chrome-package: ## Build and zip chrome-extension into dist/*.zip
	@$(MAKE) -C integrations/chrome-extension package

chrome-test: ## Run chrome-extension Jest unit tests
	@$(MAKE) -C integrations/chrome-extension test

chrome-coverage: ## Run chrome-extension tests with coverage (>=90%)
	@$(MAKE) -C integrations/chrome-extension test-coverage

chrome-lint: ## Lint chrome-extension TypeScript sources
	@$(MAKE) -C integrations/chrome-extension lint

chrome-audit: ## npm audit for chrome-extension (production deps, >=high)
	@$(MAKE) -C integrations/chrome-extension audit

chrome-check: ## Format check + lint + tests + build + package for chrome-extension
	@$(MAKE) -C integrations/chrome-extension check

chrome-clean: ## Remove chrome-extension build artefacts and node_modules
	@$(MAKE) -C integrations/chrome-extension clean

# ----- Grafana datasource plugin (integrations/grafana-datasource) -----

grafana-install: ## Install grafana-datasource npm dependencies
	@$(MAKE) -C integrations/grafana-datasource install

grafana-build: ## Bundle grafana-datasource into dist/ (webpack production)
	@$(MAKE) -C integrations/grafana-datasource build

grafana-test: ## Run grafana-datasource Jest unit tests
	@$(MAKE) -C integrations/grafana-datasource test

grafana-coverage: ## Run grafana-datasource tests with coverage (>=90%)
	@$(MAKE) -C integrations/grafana-datasource test-coverage

grafana-lint: ## Lint grafana-datasource TypeScript sources
	@$(MAKE) -C integrations/grafana-datasource lint

grafana-check: ## Format check + lint + tests + build for grafana-datasource
	@$(MAKE) -C integrations/grafana-datasource check

grafana-package: ## Build + zip grafana-datasource plugin for distribution
	@$(MAKE) -C integrations/grafana-datasource package

grafana-docker: ## Build Grafana image with the MDDB datasource preinstalled
	@$(MAKE) -C integrations/grafana-datasource docker

grafana-clean: ## Remove grafana-datasource build artefacts and node_modules
	@$(MAKE) -C integrations/grafana-datasource clean

.DEFAULT_GOAL := help
