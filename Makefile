# If you update this file, please follow
# https://suva.sh/posts/well-documented-makefiles

.DEFAULT_GOAL := help

PACKAGE = $(shell go list -m)
GIT_COMMIT_HASH = $(shell git rev-parse HEAD)
GIT_VERSION = $(shell git describe --tags --always --dirty)
BUILD_TIME = $(shell date -u '+%Y-%m-%dT%H:%M:%SZ')
BINARY_NAME = kubernetes-mcp-server
LD_FLAGS = -s -w \
	-X '$(PACKAGE)/pkg/version.CommitHash=$(GIT_COMMIT_HASH)' \
	-X '$(PACKAGE)/pkg/version.Version=$(GIT_VERSION)' \
	-X '$(PACKAGE)/pkg/version.BuildTime=$(BUILD_TIME)' \
	-X '$(PACKAGE)/pkg/version.BinaryName=$(BINARY_NAME)'
COMMON_BUILD_ARGS = -ldflags "$(LD_FLAGS)"

# GoReleaser configuration
GORELEASER := $(shell which goreleaser 2>/dev/null)

# Docker Compose detection (v2 plugin or v1 standalone)
DOCKER_COMPOSE := $(shell docker compose version > /dev/null 2>&1 && echo "docker compose" || echo "docker-compose")

GOLANGCI_LINT = $(shell pwd)/_output/tools/bin/golangci-lint
GOLANGCI_LINT_VERSION ?= v2.2.2

# NPM version should not append the -dirty flag
NPM_VERSION ?= $(shell echo $(shell git describe --tags --always) | sed 's/^v//')
OSES = darwin linux windows
ARCHS = amd64 arm64

CLEAN_TARGETS :=
CLEAN_TARGETS += '$(BINARY_NAME)'
CLEAN_TARGETS += $(foreach os,$(OSES),$(foreach arch,$(ARCHS),$(BINARY_NAME)-$(os)-$(arch)$(if $(findstring windows,$(os)),.exe,)))
CLEAN_TARGETS += $(foreach os,$(OSES),$(foreach arch,$(ARCHS),./npm/$(BINARY_NAME)-$(os)-$(arch)/bin/))
CLEAN_TARGETS += ./npm/kubernetes-mcp-server/.npmrc ./npm/kubernetes-mcp-server/LICENSE ./npm/kubernetes-mcp-server/README.md
CLEAN_TARGETS += $(foreach os,$(OSES),$(foreach arch,$(ARCHS),./npm/$(BINARY_NAME)-$(os)-$(arch)/.npmrc))

# The help will print out all targets with their descriptions organized bellow their categories. The categories are represented by `##@` and the target descriptions by `##`.
# The awk commands is responsible to read the entire set of makefiles included in this invocation, looking for lines of the file as xyz: ## something, and then pretty-format the target and help. Then, if there's a line with ##@ something, that gets pretty-printed as a category.
# More info over the usage of ANSI control characters for terminal formatting: https://en.wikipedia.org/wiki/ANSI_escape_code#SGR_parameters
# More info over awk command: http://linuxcommand.org/lc3_adv_awk.php
#
# Notice that we have a little modification on the awk command to support slash in the recipe name:
# origin: /^[a-zA-Z_0-9-]+:.*?##/
# modified /^[a-zA-Z_0-9\/\.-]+:.*?##/
.PHONY: help
help: ## Display this help
	@awk 'BEGIN {FS = ":.*##"; printf "\nUsage:\n  make \033[36m<target>\033[0m\n"} /^[a-zA-Z_0-9\/\.-]+:.*?##/ { printf "  \033[36m%-21s\033[0m %s\n", $$1, $$2 } /^##@/ { printf "\n\033[1m%s\033[0m\n", substr($$0, 5) } ' $(MAKEFILE_LIST)

.PHONY: clean
clean: ## Clean up all build artifacts
	rm -rf $(CLEAN_TARGETS) dist/

##@ Build

.PHONY: build
build: tidy format ## Build the project for current platform
ifdef GORELEASER
	@echo "Building with GoReleaser..."
	goreleaser build --snapshot --clean --single-target
	@cp dist/kubernetes-mcp-server_*/kubernetes-mcp-server ./$(BINARY_NAME) 2>/dev/null || \
		cp dist/kubernetes-mcp-server_*/kubernetes-mcp-server.exe ./$(BINARY_NAME).exe 2>/dev/null || true
else
	@echo "Building with go build (install goreleaser for better builds)..."
	go build $(COMMON_BUILD_ARGS) -o $(BINARY_NAME) ./cmd/kubernetes-mcp-server
endif

.PHONY: build-all-platforms
build-all-platforms: tidy format ## Build the project for all platforms using GoReleaser
ifdef GORELEASER
	@echo "Building all platforms with GoReleaser..."
	goreleaser build --snapshot --clean
else
	@echo "Building all platforms with go build..."
	$(foreach os,$(OSES),$(foreach arch,$(ARCHS), \
		GOOS=$(os) GOARCH=$(arch) go build $(COMMON_BUILD_ARGS) -o $(BINARY_NAME)-$(os)-$(arch)$(if $(findstring windows,$(os)),.exe,) ./cmd/kubernetes-mcp-server; \
	))
endif

##@ GoReleaser

.PHONY: goreleaser-check
goreleaser-check: ## Check GoReleaser configuration
ifdef GORELEASER
	goreleaser check
else
	@echo "GoReleaser not installed. Install with: go install github.com/goreleaser/goreleaser/v2@latest"
	@exit 1
endif

.PHONY: snapshot
snapshot: ## Create a snapshot release with GoReleaser
ifdef GORELEASER
	goreleaser release --snapshot --clean
else
	@echo "GoReleaser not installed. Install with: go install github.com/goreleaser/goreleaser/v2@latest"
	@exit 1
endif

.PHONY: release
release: ## Create a release with GoReleaser (requires tag)
ifdef GORELEASER
	goreleaser release --clean
else
	@echo "GoReleaser not installed. Install with: go install github.com/goreleaser/goreleaser/v2@latest"
	@exit 1
endif

.PHONY: release-dry-run
release-dry-run: ## Dry run of release process
ifdef GORELEASER
	goreleaser release --skip=publish,announce,sign --clean
else
	@echo "GoReleaser not installed. Install with: go install github.com/goreleaser/goreleaser/v2@latest"
	@exit 1
endif

.PHONY: npm-copy-binaries
npm-copy-binaries: build-all-platforms ## Copy the binaries to each npm package
	$(foreach os,$(OSES),$(foreach arch,$(ARCHS), \
		EXECUTABLE=./$(BINARY_NAME)-$(os)-$(arch)$(if $(findstring windows,$(os)),.exe,); \
		DIRNAME=$(BINARY_NAME)-$(os)-$(arch); \
		mkdir -p ./npm/$$DIRNAME/bin; \
		cp $$EXECUTABLE ./npm/$$DIRNAME/bin/; \
	))

.PHONY: npm-publish
npm-publish: npm-copy-binaries ## Publish the npm packages
	$(foreach os,$(OSES),$(foreach arch,$(ARCHS), \
		DIRNAME="$(BINARY_NAME)-$(os)-$(arch)"; \
		cd npm/$$DIRNAME; \
		echo '//registry.npmjs.org/:_authToken=$(NPM_TOKEN)' >> .npmrc; \
		jq '.version = "$(NPM_VERSION)"' package.json > tmp.json && mv tmp.json package.json; \
		npm publish; \
		cd ../..; \
	))
	cp README.md LICENSE ./npm/kubernetes-mcp-server/
	echo '//registry.npmjs.org/:_authToken=$(NPM_TOKEN)' >> ./npm/kubernetes-mcp-server/.npmrc
	jq '.version = "$(NPM_VERSION)"' ./npm/kubernetes-mcp-server/package.json > tmp.json && mv tmp.json ./npm/kubernetes-mcp-server/package.json; \
	jq '.optionalDependencies |= with_entries(.value = "$(NPM_VERSION)")' ./npm/kubernetes-mcp-server/package.json > tmp.json && mv tmp.json ./npm/kubernetes-mcp-server/package.json; \
	cd npm/kubernetes-mcp-server && npm publish

.PHONY: python-publish
python-publish: ## Publish the python packages
	cd ./python && \
	sed -i "s/version = \".*\"/version = \"$(NPM_VERSION)\"/" pyproject.toml && \
	uv build && \
	uv publish

.PHONY: test
test: ## Run the tests
	go test -count=1 -v ./...

.PHONY: format
format: ## Format the code
	go fmt ./...

.PHONY: tidy
tidy: ## Tidy up the go modules
	go mod tidy

.PHONY: golangci-lint
golangci-lint: ## Download and install golangci-lint if not already installed
		@[ -f $(GOLANGCI_LINT) ] || { \
    	set -e ;\
    	curl -sSfL https://raw.githubusercontent.com/golangci/golangci-lint/master/install.sh | sh -s -- -b $(shell dirname $(GOLANGCI_LINT)) $(GOLANGCI_LINT_VERSION) ;\
    	}

.PHONY: lint
lint: golangci-lint ## Lint the code
	$(GOLANGCI_LINT) run --verbose --print-resources-usage

##@ Docker

.PHONY: docker-build
docker-build: ## Build Docker image
	docker build -t kubernetes-mcp-server:latest .

.PHONY: docker-run
docker-run: ## Run Docker container with kubeconfig directory mounted
	docker run -d \
		--name kubernetes-mcp-server \
		-p 8080:8080 \
		-v $(HOME)/.mcp:/mcp:ro \
		kubernetes-mcp-server:latest \
		--port=8080 \
		--kubeconfig-dir=/mcp

.PHONY: docker-run-rancher
docker-run-rancher: ## Run Docker container with Rancher integration
	docker run -d \
		--name kubernetes-mcp-server \
		-p 8080:8080 \
		-v $(HOME)/.mcp:/mcp:ro \
		-e RANCHER_URL=$(RANCHER_URL) \
		-e RANCHER_TOKEN=$(RANCHER_TOKEN) \
		kubernetes-mcp-server:latest \
		--port=8080 \
		--kubeconfig-dir=/mcp \
		--rancher-url=$(RANCHER_URL) \
		--rancher-token=$(RANCHER_TOKEN)

.PHONY: docker-stop
docker-stop: ## Stop and remove Docker container
	docker stop kubernetes-mcp-server || true
	docker rm kubernetes-mcp-server || true

.PHONY: docker-logs
docker-logs: ## Show Docker container logs
	docker logs -f kubernetes-mcp-server

.PHONY: docker-shell
docker-shell: ## Open shell in running container
	docker exec -it kubernetes-mcp-server /bin/sh

.PHONY: compose-up
compose-up: ## Start services with docker-compose
	$(DOCKER_COMPOSE) up -d

.PHONY: compose-down
compose-down: ## Stop services with docker-compose
	$(DOCKER_COMPOSE) down

.PHONY: compose-logs
compose-logs: ## Show docker-compose logs
	$(DOCKER_COMPOSE) logs -f

.PHONY: compose-rebuild
compose-rebuild: ## Rebuild and restart with docker-compose
	$(DOCKER_COMPOSE) down
	$(DOCKER_COMPOSE) up -d --build

.PHONY: docker-health
docker-health: ## Check Docker container health status
	docker inspect kubernetes-mcp-server --format='{{.State.Health.Status}}'
