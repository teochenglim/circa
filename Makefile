.DEFAULT_GOAL := help
BINARY     := circa
IMAGE      := $(BINARY):local
GHCR_IMAGE := ghcr.io/teochenglim/$(BINARY)

# Read the current version from the VERSION file (no external tooling required).
VERSION_CURRENT := $(shell cat VERSION 2>/dev/null || echo 0.0.0)

.PHONY: help
help: ## Show this menu
	@echo "Circa $(VERSION_CURRENT) - available targets:"
	@grep -E '^[a-zA-Z0-9_-]+:.*?## .*$$' $(MAKEFILE_LIST) | sort | awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-18s\033[0m %s\n", $$1, $$2}'
	@echo ""
	@echo "Release cycle:"
	@echo "  make release VERSION=x.y.z   # bump VERSION + helm/k8s image tags, push HEAD, tag, push tag -> CI"

## --- develop -----------------------------------------------------------

.PHONY: build
build: ## Build the circa binary into ./bin
	CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o bin/$(BINARY) ./cmd/circa

.PHONY: run
run: ## Run circa locally against ./config.yaml (falls back to defaults if absent)
	go run ./cmd/circa -config config.yaml

.PHONY: test
test: ## Run the full test suite with race detector and coverage
	go test ./... -race -cover

.PHONY: test-verbose
test-verbose: ## Run the full test suite with verbose per-test output
	go test ./... -race -cover -v

.PHONY: vet
vet: ## Run go vet
	go vet ./...

.PHONY: fmt
fmt: ## Format all Go source with gofmt
	gofmt -l -w .

.PHONY: tidy
tidy: ## Tidy go.mod/go.sum
	go mod tidy

.PHONY: clean
clean: ## Remove local build artifacts and local RRD data
	rm -rf bin dist data

.PHONY: config-init
config-init: ## Copy config.example.yaml to config.yaml if it doesn't exist yet
	@test -f config.yaml && echo "config.yaml already exists" || cp config.example.yaml config.yaml

## --- local dev (real node_exporter) ----------------------------------------
## A real exporter to scrape against, instead of the fake ones used in unit
## tests. Download a release yourself into ./bin/node_exporter (gitignored,
## see .gitignore's /bin/ entry) from
## https://github.com/prometheus/node_exporter/releases - bin/config.yaml
## already points circa at it on its default port :9100.

.PHONY: node-exporter-up
node-exporter-up: ## Start ./bin/node_exporter in the background, logging to bin/node_exporter.log
	@test -x bin/node_exporter || { echo "bin/node_exporter not found - download a release into ./bin/ first"; exit 1; }
	@./bin/node_exporter > bin/node_exporter.log 2>&1 & echo $$! > bin/node_exporter.pid
	@echo "node_exporter started (pid $$(cat bin/node_exporter.pid)) - logs: bin/node_exporter.log"

.PHONY: node-exporter-down
node-exporter-down: ## Stop the node_exporter started by node-exporter-up
	@test -f bin/node_exporter.pid && kill $$(cat bin/node_exporter.pid) 2>/dev/null; rm -f bin/node_exporter.pid
	@echo "node_exporter stopped"

.PHONY: local-up
local-up: build node-exporter-up ## Start circa (bin/config.yaml) in the background alongside node_exporter - visit http://localhost:9090
	@./bin/circa -config bin/config.yaml > bin/circa.log 2>&1 & echo $$! > bin/circa.pid
	@echo "circa started (pid $$(cat bin/circa.pid)) - visit http://localhost:9090"

.PHONY: local-down
local-down: ## Stop the circa + node_exporter started by local-up
	@test -f bin/circa.pid && kill $$(cat bin/circa.pid) 2>/dev/null; rm -f bin/circa.pid
	@echo "circa stopped"
	@$(MAKE) node-exporter-down

## --- docker --------------------------------------------------------------

.PHONY: docker-build
docker-build: ## Build the circa Docker image
	docker build -t $(IMAGE) .

.PHONY: up
up: ## Start circa via docker-compose in the foreground
	docker compose up --build

.PHONY: up-d
up-d: ## Start circa via docker-compose in the background
	docker compose up --build -d

.PHONY: down
down: ## Stop the docker-compose stack and remove its volume
	docker compose down -v

## --- kubernetes ------------------------------------------------------------
## Circa is a per-node agent (DaemonSet), not a stateless multi-replica web
## app - see ARCHITECTURE.md "Deployment shape" and k8s/README.md.

.PHONY: k8s-apply
k8s-apply: ## Apply the k8s/ manifests to the current kubectl context
	kubectl apply -f k8s/

.PHONY: k8s-delete
k8s-delete: ## Delete the k8s/ manifests from the current kubectl context
	kubectl delete -f k8s/

.PHONY: k8s-logs
k8s-logs: ## Tail logs from the circa DaemonSet in k8s
	kubectl logs -f daemonset/circa

## --- helm -------------------------------------------------------------------

.PHONY: helm-lint
helm-lint: ## Lint the helm/circa chart
	helm lint helm/circa

.PHONY: helm-template
helm-template: ## Render the helm/circa chart locally (no cluster needed)
	helm template circa helm/circa

.PHONY: helm-install
helm-install: ## Install/upgrade the circa release via helm
	helm upgrade --install circa helm/circa

.PHONY: helm-uninstall
helm-uninstall: ## Uninstall the circa helm release
	helm uninstall circa

## --- supply-chain hardening -------------------------------------------------

.PHONY: github-action-bump
github-action-bump: ## Pin .github/workflows/*.yml actions to latest release, full commit SHA (uses pinact)
	@# Unauthenticated GitHub API calls are capped at 60/hour; export GITHUB_TOKEN to raise that limit.
	go run github.com/suzuki-shunsuke/pinact/cmd/pinact@latest run --update
	go run github.com/suzuki-shunsuke/pinact/cmd/pinact@latest run --verify
	@echo "Actions bumped and verified. Review the diff, then run 'make vet test' before committing."

## --- release --------------------------------------------------------------

.PHONY: version
version: ## Print the version currently in VERSION
	@echo $(VERSION_CURRENT)

.PHONY: bump
bump: ## Rewrite VERSION + helm/k8s image tags, and docker build a local image matching the new tag (VERSION=x.y.z required)
	@if [ -z "$(VERSION)" ]; then echo "Usage: make bump VERSION=x.y.z"; exit 1; fi
	@echo "$(VERSION)" > VERSION
	@# No "v" prefix: matches the GHCR tags docker/metadata-action actually
	@# publishes (type=semver,pattern={{version}} strips the git tag's "v").
	sed -i.bak -E 's/^appVersion: ".*"/appVersion: "$(VERSION)"/' helm/circa/Chart.yaml && rm -f helm/circa/Chart.yaml.bak
	sed -i.bak -E 's#(ghcr\.io/teochenglim/circa):[^"]*#\1:$(VERSION)#' k8s/20-daemonset.yaml && rm -f k8s/20-daemonset.yaml.bak
	@# Built under the exact GHCR tag the manifests above now reference, not
	@# the local-only tag "docker-build" uses, so `make bump VERSION=x.y.z
	@# && make k8s-apply` finds it via imagePullPolicy: IfNotPresent with no
	@# registry push needed - a local test loop for a cluster sharing the
	@# host's Docker daemon (e.g. Docker Desktop's Kubernetes). kind/minikube
	@# users still need their own `kind load docker-image` / `minikube image
	@# load` after this.
	docker build -t $(GHCR_IMAGE):$(VERSION) .
	@echo "VERSION -> $(VERSION) (also helm/circa/Chart.yaml appVersion, k8s/20-daemonset.yaml image tag, and built $(GHCR_IMAGE):$(VERSION) locally)"

.PHONY: release
release: ## Bump VERSION + helm/k8s image tags, push HEAD, tag, push the tag - triggers GitHub Actions (VERSION=x.y.z required)
	@if [ -z "$(VERSION)" ]; then echo "Usage: make release VERSION=x.y.z"; exit 1; fi
	$(MAKE) bump VERSION=$(VERSION)
	git add VERSION helm/circa/Chart.yaml k8s/20-daemonset.yaml
	git commit --amend --no-edit
	git push origin HEAD
	git tag v$(VERSION)
	git push origin v$(VERSION)
	@echo "Released v$(VERSION) - GitHub Actions will build and publish."
