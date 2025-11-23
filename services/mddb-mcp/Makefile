# Makefile colors
ifneq (,$(findstring xterm,${TERM}))
   BLACK        := $(shell tput -Txterm setaf 0)
   RED          := $(shell tput -Txterm setaf 1)
   GREEN        := $(shell tput -Txterm setaf 2)
   YELLOW       := $(shell tput -Txterm setaf 3)
   LIGHTPURPLE  := $(shell tput -Txterm setaf 4)
   PURPLE       := $(shell tput -Txterm setaf 5)
   BLUE         := $(shell tput -Txterm setaf 6)
   WHITE        := $(shell tput -Txterm setaf 7)
   RESET := $(shell tput -Txterm sgr0)
else
   BLACK        := ""
   RED          := ""
   GREEN        := ""
   YELLOW       := ""
   LIGHTPURPLE  := ""
   PURPLE       := ""
   BLUE         := ""
   WHITE        := ""
   RESET        := ""
endif

.PHONY: help
help: ## Show this help
	@grep -h -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | sort | awk 'BEGIN {FS = ":.*?## "}; {printf "\033[36m%-30s\033[0m %s\n", $$1, $$2}'

.PHONY: build
build: ## Build mddb-mcp binary
	@echo "${GREEN}🔨 Building mddb-mcp...${RESET}"
	go build -o mddb-mcp ./cmd/mddb-mcp

.PHONY: build-stdio
build-stdio: ## Build mddb-mcp-stdio binary for Windsurf
	@echo "${GREEN}🔨 Building mddb-mcp-stdio...${RESET}"
	go build -o mddb-mcp-stdio ./cmd/mddb-mcp-stdio

.PHONY: build-all
build-all: build build-stdio ## Build all binaries
	@echo "${GREEN}✓ All binaries built${RESET}"

.PHONY: run
run: ## Run mddb-mcp locally
	@echo "${BLUE}🚀 Starting mddb-mcp...${RESET}"
	go run ./cmd/mddb-mcp

.PHONY: test
test: ## Run tests
	@echo "${YELLOW}🧪 Running tests...${RESET}"
	go test -v ./...

.PHONY: fmt
fmt: ## Format code
	@echo "${PURPLE}💅 Formatting code...${RESET}"
	go fmt ./...

.PHONY: lint
lint: ## Run linter
	@echo "${LIGHTPURPLE}🔍 Running linter...${RESET}"
	golangci-lint run

.PHONY: tidy
tidy: ## Tidy go modules
	@echo "${GREEN}📦 Tidying modules...${RESET}"
	go mod tidy

.PHONY: clean
clean: ## Clean build artifacts
	@echo "${RED}🧹 Cleaning...${RESET}"
	rm -f mddb-mcp mddb-mcp-stdio

.PHONY: docker-build
docker-build: ## Build Docker image
	@echo "${BLUE}🐳 Building Docker image...${RESET}"
	docker build -t mddb-mcp:latest .

.PHONY: docker-run
docker-run: ## Run Docker container
	@echo "${BLUE}🐳 Running Docker container...${RESET}"
	docker run -p 9000:9000 --name mddb-mcp mddb-mcp:latest

.DEFAULT_GOAL := help
