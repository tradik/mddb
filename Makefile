# Makefile for MDDB project
.PHONY: help build run test clean install dev fmt lint docker-build docker-run docker-stop docker-clean all

# Colors
ifneq (,$(findstring xterm,${TERM}))
	BLACK        := $(shell tput -Txterm setaf 0)
	RED          := $(shell tput -Txterm setaf 1)
	GREEN        := $(shell tput -Txterm setaf 2)
	YELLOW       := $(shell tput -Txterm setaf 3)
	LIGHTPURPLE  := $(shell tput -Txterm setaf 4)
	PURPLE       := $(shell tput -Txterm setaf 5)
	BLUE         := $(shell tput -Txterm setaf 6)
	WHITE        := $(shell tput -Txterm setaf 7)
	RESET        := $(shell tput -Txterm sgr0)
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

# Project variables
PROJECT_NAME := mddb
GO_SERVICE_DIR := services/mddbd
GO_CLI_DIR := services/mddb-cli
BINARY_NAME := mddbd
CLI_BINARY_NAME := mddb-cli
BINARY_PATH := $(GO_SERVICE_DIR)/$(BINARY_NAME)
CLI_BINARY_PATH := $(GO_CLI_DIR)/$(CLI_BINARY_NAME)
GO_VERSION := 1.25

# Build variables
GOCMD := go
GOBUILD := $(GOCMD) build
GOCLEAN := $(GOCMD) clean
GOTEST := $(GOCMD) test
GOGET := $(GOCMD) get
GOFMT := $(GOCMD) fmt
GOVET := $(GOCMD) vet

.DEFAULT_GOAL := help

help: ## 📚 Show this help message
	@echo "${BLUE}════════════════════════════════════════════════════════════${RESET}"
	@echo "${GREEN}  MDDB - Markdown Database${RESET}"
	@echo "${BLUE}════════════════════════════════════════════════════════════${RESET}"
	@grep -h -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | sort | awk 'BEGIN {FS = ":.*?## "}; {printf "${BLUE}%-35s${RESET} %s\n", $$1, $$2}'
	@echo "${BLUE}════════════════════════════════════════════════════════════${RESET}"

all: clean build test ## 🚀 Clean, build and test everything
	@echo "${GREEN}✓ All tasks completed successfully!${RESET}"

# Development targets
dev: ## 🔧 Run in development mode with air (hot reload)
	@echo "${YELLOW}🔄 Starting development server with hot reload...${RESET}"
	@cd $(GO_SERVICE_DIR) && air

install-dev-tools: ## 📦 Install development tools
	@echo "${YELLOW}📦 Installing development tools...${RESET}"
	@go install github.com/air-verse/air@latest
	@echo "${GREEN}✓ Development tools installed${RESET}"

install-grpc-tools: ## 📦 Install gRPC tools
	@echo "${YELLOW}📦 Installing gRPC tools...${RESET}"
	@go install google.golang.org/protobuf/cmd/protoc-gen-go@latest
	@go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest
	@echo "${GREEN}✓ gRPC tools installed${RESET}"
	@echo "${BLUE}  Note: You also need protoc (brew install protobuf)${RESET}"

generate-proto: ## 🔧 Generate gRPC code from shared protobuf
	@echo "${YELLOW}🔧 Generating gRPC code for all languages...${RESET}"
	@./proto/generate.sh

# Build targets
build: ## 🔨 Build the Go service
	@echo "${YELLOW}🔨 Building $(BINARY_NAME)...${RESET}"
	@cd $(GO_SERVICE_DIR) && $(GOBUILD) -o $(BINARY_NAME) -v .
	@echo "${GREEN}✓ Build completed: $(BINARY_PATH)${RESET}"

build-cli: ## 🔨 Build the CLI client
	@echo "${YELLOW}🔨 Building $(CLI_BINARY_NAME)...${RESET}"
	@cd $(GO_CLI_DIR) && $(GOCMD) mod tidy
	@cd $(GO_CLI_DIR) && $(GOBUILD) -o $(CLI_BINARY_NAME) -v .
	@echo "${GREEN}✓ Build completed: $(CLI_BINARY_PATH)${RESET}"

build-all-binaries: build build-cli ## 🔨 Build server and CLI
	@echo "${GREEN}✓ All binaries built${RESET}"

build-linux: ## 🐧 Build for Linux
	@echo "${YELLOW}🔨 Building $(BINARY_NAME) for Linux...${RESET}"
	@cd $(GO_SERVICE_DIR) && GOOS=linux GOARCH=amd64 $(GOBUILD) -o $(BINARY_NAME)-linux -v .
	@echo "${GREEN}✓ Linux build completed${RESET}"

build-windows: ## 🪟 Build for Windows
	@echo "${YELLOW}🔨 Building $(BINARY_NAME) for Windows...${RESET}"
	@cd $(GO_SERVICE_DIR) && GOOS=windows GOARCH=amd64 $(GOBUILD) -o $(BINARY_NAME).exe -v .
	@echo "${GREEN}✓ Windows build completed${RESET}"

build-all: build build-linux build-windows ## 🌍 Build for all platforms
	@echo "${GREEN}✓ All platform builds completed${RESET}"

# Run targets
run: build ## ▶️  Build and run the service
	@echo "${GREEN}▶️  Starting $(BINARY_NAME)...${RESET}"
	@cd $(GO_SERVICE_DIR) && ./$(BINARY_NAME)

run-direct: ## ▶️  Run directly without building (using go run)
	@echo "${GREEN}▶️  Running $(BINARY_NAME) directly...${RESET}"
	@cd $(GO_SERVICE_DIR) && MDDB_ADDR=":11023" MDDB_MODE="wr" MDDB_PATH="mddb.db" go run .

run-prod: build ## 🚀 Run in production mode
	@echo "${GREEN}🚀 Starting $(BINARY_NAME) in production mode...${RESET}"
	@cd $(GO_SERVICE_DIR) && MDDB_MODE=wr MDDB_ADDR=:11023 MDDB_PATH=mddb.db ./$(BINARY_NAME)

# Test targets
test: ## 🧪 Run tests
	@echo "${YELLOW}🧪 Running tests...${RESET}"
	@cd $(GO_SERVICE_DIR) && $(GOTEST) -v ./...
	@echo "${GREEN}✓ Tests completed${RESET}"

test-coverage: ## 📊 Run tests with coverage
	@echo "${YELLOW}📊 Running tests with coverage...${RESET}"
	@cd $(GO_SERVICE_DIR) && $(GOTEST) -v -coverprofile=coverage.out ./...
	@cd $(GO_SERVICE_DIR) && $(GOCMD) tool cover -html=coverage.out -o coverage.html
	@echo "${GREEN}✓ Coverage report generated: $(GO_SERVICE_DIR)/coverage.html${RESET}"

test-race: ## 🏁 Run tests with race detector
	@echo "${YELLOW}🏁 Running tests with race detector...${RESET}"
	@cd $(GO_SERVICE_DIR) && $(GOTEST) -race -v ./...
	@echo "${GREEN}✓ Race tests completed${RESET}"

# Code quality targets
fmt: ## 🎨 Format Go code
	@echo "${YELLOW}🎨 Formatting code...${RESET}"
	@cd $(GO_SERVICE_DIR) && $(GOFMT) ./...
	@echo "${GREEN}✓ Code formatted${RESET}"

lint: ## 🔍 Run linter
	@echo "${YELLOW}🔍 Running linter...${RESET}"
	@cd $(GO_SERVICE_DIR) && $(GOVET) ./...
	@echo "${GREEN}✓ Linting completed${RESET}"

tidy: ## 🧹 Tidy Go modules
	@echo "${YELLOW}🧹 Tidying modules...${RESET}"
	@cd $(GO_SERVICE_DIR) && $(GOCMD) mod tidy
	@echo "${GREEN}✓ Modules tidied${RESET}"

update-deps: ## ⬆️  Update dependencies
	@echo "${YELLOW}⬆️  Updating dependencies...${RESET}"
	@cd $(GO_SERVICE_DIR) && $(GOCMD) get -u ./...
	@cd $(GO_SERVICE_DIR) && $(GOCMD) mod tidy
	@echo "${GREEN}✓ Dependencies updated${RESET}"

# Clean targets
clean: ## 🧹 Clean build artifacts
	@echo "${YELLOW}🧹 Cleaning build artifacts...${RESET}"
	@cd $(GO_SERVICE_DIR) && $(GOCLEAN)
	@rm -f $(GO_SERVICE_DIR)/$(BINARY_NAME)
	@rm -f $(GO_SERVICE_DIR)/$(BINARY_NAME)-linux
	@rm -f $(GO_SERVICE_DIR)/$(BINARY_NAME).exe
	@rm -f $(GO_SERVICE_DIR)/coverage.out
	@rm -f $(GO_SERVICE_DIR)/coverage.html
	@rm -f $(GO_SERVICE_DIR)/*.db
	@rm -f $(GO_SERVICE_DIR)/backup-*.db
	@cd $(GO_CLI_DIR) && $(GOCLEAN)
	@rm -f $(GO_CLI_DIR)/$(CLI_BINARY_NAME)
	@rm -f $(GO_CLI_DIR)/$(CLI_BINARY_NAME)-linux
	@rm -f $(GO_CLI_DIR)/$(CLI_BINARY_NAME).exe
	@echo "${GREEN}✓ Cleaned${RESET}"

clean-all: clean ## 🗑️  Clean everything including vendor
	@echo "${YELLOW}🗑️  Deep cleaning...${RESET}"
	@rm -rf $(GO_SERVICE_DIR)/vendor
	@echo "${GREEN}✓ Deep clean completed${RESET}"

# Database targets
db-backup: ## 💾 Create database backup
	@echo "${YELLOW}💾 Creating database backup...${RESET}"
	@curl -s "http://localhost:11023/v1/backup?to=backup-$$(date +%s).db" | jq .
	@echo "${GREEN}✓ Backup created${RESET}"

db-clean: ## 🗑️  Remove all database files
	@echo "${YELLOW}🗑️  Removing database files...${RESET}"
	@rm -f $(GO_SERVICE_DIR)/*.db
	@echo "${GREEN}✓ Database files removed${RESET}"

# Info targets
info: ## ℹ️  Show project information
	@echo "${BLUE}════════════════════════════════════════════════${RESET}"
	@echo "${GREEN}Project:${RESET} $(PROJECT_NAME)"
	@echo "${GREEN}Go Version:${RESET} $(GO_VERSION)"
	@echo "${GREEN}Service Directory:${RESET} $(GO_SERVICE_DIR)"
	@echo "${GREEN}Binary Name:${RESET} $(BINARY_NAME)"
	@echo "${BLUE}════════════════════════════════════════════════${RESET}"

version: ## 📌 Show Go version
	@echo "${GREEN}Go version:${RESET}"
	@$(GOCMD) version

check-deps: ## 🔎 Check for outdated dependencies
	@echo "${YELLOW}🔎 Checking for outdated dependencies...${RESET}"
	@cd $(GO_SERVICE_DIR) && $(GOCMD) list -u -m all
	@echo "${GREEN}✓ Dependency check completed${RESET}"

# CLI targets
install-cli: build-cli ## 📦 Install CLI to system
	@echo "${YELLOW}📦 Installing $(CLI_BINARY_NAME)...${RESET}"
	@sudo cp $(CLI_BINARY_PATH) /usr/local/bin/
	@sudo chmod +x /usr/local/bin/$(CLI_BINARY_NAME)
	@echo "${GREEN}✓ CLI installed to /usr/local/bin/$(CLI_BINARY_NAME)${RESET}"

install-man: ## 📚 Install man page
	@echo "${YELLOW}📚 Installing man page...${RESET}"
	@sudo mkdir -p /usr/local/share/man/man1
	@sudo cp $(GO_CLI_DIR)/mddb-cli.1 /usr/local/share/man/man1/
	@sudo chmod 644 /usr/local/share/man/man1/mddb-cli.1
	@echo "${GREEN}✓ Man page installed${RESET}"
	@echo "${BLUE}  View with: man mddb-cli${RESET}"

install-all: install-cli install-man ## 📦 Install CLI and man page
	@echo "${GREEN}✓ Installation complete${RESET}"

uninstall-cli: ## 🗑️  Uninstall CLI from system
	@echo "${YELLOW}🗑️  Uninstalling $(CLI_BINARY_NAME)...${RESET}"
	@sudo rm -f /usr/local/bin/$(CLI_BINARY_NAME)
	@sudo rm -f /usr/local/share/man/man1/mddb-cli.1
	@echo "${GREEN}✓ CLI uninstalled${RESET}"

# Docker targets
docker-build: ## 🐳 Build Docker image
	@echo "${YELLOW}🐳 Building Docker image...${RESET}"
	@docker build -t mddb:latest -f services/mddbd/Dockerfile .
	@echo "${GREEN}✓ Docker image built: mddb:latest${RESET}"

docker-build-dev: ## 🐳 Build development Docker image
	@echo "${YELLOW}🐳 Building development Docker image...${RESET}"
	@docker build -t mddb:dev -f services/mddbd/Dockerfile.dev .
	@echo "${GREEN}✓ Docker image built: mddb:dev${RESET}"

docker-up: ## 🚀 Start Docker containers (production)
	@echo "${YELLOW}🚀 Starting Docker containers...${RESET}"
	@docker compose up -d
	@echo "${GREEN}✓ Containers started${RESET}"
	@echo "${BLUE}  HTTP API: http://localhost:11023${RESET}"
	@echo "${BLUE}  gRPC API: localhost:11024${RESET}"

docker-up-dev: ## 🔧 Start Docker containers (development with hot reload)
	@echo "${YELLOW}🔧 Starting development containers...${RESET}"
	@docker compose -f docker-compose.dev.yml up -d
	@echo "${GREEN}✓ Development containers started${RESET}"
	@echo "${BLUE}  HTTP API: http://localhost:11023${RESET}"
	@echo "${BLUE}  gRPC API: localhost:11024${RESET}"
	@echo "${BLUE}  Hot reload enabled with Air${RESET}"

docker-down: ## 🛑 Stop Docker containers
	@echo "${YELLOW}🛑 Stopping Docker containers...${RESET}"
	@docker compose down
	@docker compose -f docker-compose.dev.yml down 2>/dev/null || true
	@echo "${GREEN}✓ Containers stopped${RESET}"

docker-logs: ## 📋 Show Docker logs
	@docker compose logs -f

docker-logs-dev: ## 📋 Show development Docker logs
	@docker compose -f docker-compose.dev.yml logs -f

docker-shell: ## 🐚 Open shell in running container
	@docker compose exec mddb sh

docker-clean: ## 🧹 Clean Docker resources
	@echo "${YELLOW}🧹 Cleaning Docker resources...${RESET}"
	@docker compose down -v
	@docker compose -f docker-compose.dev.yml down -v 2>/dev/null || true
	@docker rmi mddb:latest mddb:dev 2>/dev/null || true
	@echo "${GREEN}✓ Docker resources cleaned${RESET}"

docker-setup-network: ## 🌐 Create Docker network
	@echo "${YELLOW}🌐 Creating Docker network...${RESET}"
	@docker network create mddb-network 2>/dev/null || echo "${BLUE}  Network already exists${RESET}"
	@echo "${GREEN}✓ Network ready${RESET}"
