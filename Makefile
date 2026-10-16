.PHONY: build test test-integration lint tidy up up-collectors panel down migrate ingest-fixture sbom zeek-replay clean api frontend-build gi-build gi-up gi-down gi-migrate gi-run gi-update gi-search gi-shell

GO            ?= go
COMPOSE       ?= docker compose -f deploy/docker-compose.yml
SBOM_OUT      ?= out/sboms
SBOM_IMAGE    ?= alpine:3.20
SBOM_REPO_DIR ?= .

build:
	$(GO) build -o bin/bellu ./cmd/bellu

test:
	$(GO) test ./...

# Integration tests (testcontainers) are tagged and skipped by default.
test-integration:
	$(GO) test -tags=integration ./...

tidy:
	$(GO) mod tidy

lint:
	$(GO) vet ./...

# --- Local stack ----------------------------------------------------------

up:
	$(COMPOSE) up -d postgres memgraph memgraph-lab

# Bring up the control panel (API + React UI) and its datastores.
panel:
	$(COMPOSE) up -d postgres memgraph bellu-api frontend
	@echo "control panel: http://localhost:8081  (API: http://localhost:8080)"

up-collectors:
	$(COMPOSE) --profile collectors up -d

down:
	$(COMPOSE) down

migrate: build
	BELLU_POSTGRES_DSN?= ./bin/bellu migrate

ingest-fixture: build
	./bin/bellu ingest --source fixture --file testdata/sample.jsonl

# --- SBOM generation (Task 3) ---------------------------------------------
# Runs Syft, Trivy and cdxgen in containers. cdxgen uses its linux-amd64 image
# so the atom helper never needs a JVM.

sbom:
	mkdir -p $(SBOM_OUT)
	docker run --rm -v /var/run/docker.sock:/var/run/docker.sock \
		-v $(PWD)/$(SBOM_OUT):/out anchore/syft:latest \
		$(SBOM_IMAGE) -o cyclonedx-json=/out/syft.cdx.json
	docker run --rm -v /var/run/docker.sock:/var/run/docker.sock \
		-v $(PWD)/$(SBOM_OUT):/out aquasec/trivy:latest \
		image --format cyclonedx --output /out/trivy.cdx.json $(SBOM_IMAGE)
	docker run --rm --platform linux/amd64 \
		-v $(PWD):/app -v $(PWD)/$(SBOM_OUT):/out \
		ghcr.io/cyclonedx/cdxgen:latest \
		-r /app -o /out/cdxgen.cdx.json
	docker run --rm --platform linux/amd64 \
		-v $(PWD):/app -v $(PWD)/$(SBOM_OUT):/out \
		ghcr.io/cyclonedx/cdxgen:latest \
		aibom /app -o /out/aibom.cdx.json

# --- Zeek replay (Task 11) -------------------------------------------------

zeek-replay:
	@test -n "$(PCAP)" || (echo "usage: make zeek-replay PCAP=path/to.pcap" && exit 1)
	mkdir -p deploy/zeek/logs
	docker run --rm -v $(PWD)/deploy/zeek/logs:/logs -v $(PWD)/$(PCAP):/cap.pcap:ro \
		-w /logs zeek/zeek:latest \
		zeek -r /cap.pcap LogAscii::use_json=T

# Build/run the control-panel API locally (needs postgres + memgraph up).
api:
	$(GO) build -o bin/bellu-api ./backend/cmd/bellu-api
	./bin/bellu-api

# Build the React frontend bundle.
frontend-build:
	cd frontend && npm install && npm run build

# --- Global Intelligence (global-intel) local dev -------------------------
# The Global Intelligence plane is a SEPARATE compose project with its own
# Postgres (postgres-intel, host :5433, db bellu_intel), OpenSearch, and
# pipeline container. It never touches the customer Postgres.
#
#   make gi-up       start postgres-intel + opensearch (+ dashboards) + container
#   make gi-migrate  apply migrations into bellu_intel (inside the container)
#   make gi-run      run the pipeline (folds OpenSearch indexing in)
#   make gi-update   drop + rebuild the OpenSearch index from Postgres
#   make gi-shell    open a shell in the global-intel container

GI_COMPOSE ?= docker compose -f deploy/global-intel/docker-compose.yml
# Host-side DSN/endpoint (via the published 5433 port) for running the CLI from
# the host instead of the container; the container itself uses service names.
GI_DSN   ?= postgres://bellu_intel:bellu_intel@localhost:5433/bellu_intel?sslmode=disable
GI_OS    ?= http://localhost:9200

# Build the global-intel CLI binary.
gi-build:
	$(GO) build -o bin/global-intel ./cmd/global-intel

# Start the Global Intelligence plane (postgres-intel + opensearch + dashboards
# + the long-running global-intel container), building images as needed.
gi-up:
	$(GI_COMPOSE) up -d --build
	@echo "postgres-intel :5433   opensearch http://localhost:9200   dashboards http://localhost:5601"

# Tear down the Global Intelligence plane (keeps the intel_pgdata volume).
gi-down:
	$(GI_COMPOSE) down

# Apply migrations into bellu_intel, inside the container (uses its DSN).
gi-migrate:
	$(GI_COMPOSE) exec global-intel global-intel migrate

# Run the full fetch->parse->normalize->correlate pipeline inside the container.
# Indexing into OpenSearch is folded in automatically (BELLU_SEARCH_ENDPOINT is
# set in the compose file). Override MODE=incremental to use stored cursors.
MODE ?= daily
gi-run:
	$(GI_COMPOSE) exec global-intel global-intel run --mode $(MODE) --raw-dir /tmp/intel-raw

# Re-index the whole corpus into OpenSearch from Postgres (drop + rebuild).
gi-update:
	$(GI_COMPOSE) exec global-intel global-intel update --rebuild

# Quick smoke query against the local index (needs curl + jq optional).
# Example: make gi-search Q='pkg:pypi/requests'
Q ?=
gi-search:
	@curl -fsS "$(GI_OS)/intel-vulnerabilities/_search" \
		-H 'Content-Type: application/json' \
		-d '{"size":5,"query":{"query_string":{"query":"$(Q)"}}}' || true

# Open a shell in the global-intel dev container.
gi-shell:
	$(GI_COMPOSE) up -d global-intel
	$(GI_COMPOSE) exec global-intel sh

clean:
	rm -rf bin out frontend/dist

# --- Bellu Cloud Lab -------------------------------------------------------
# Disposable Terraform-based integration-test lab. Targets: lab-up, lab-test,
# lab-down (each SCENARIO=<provider>/<name>), integration-test-cloud, destroy-all.
include bellu-cloud-lab/Makefile.lab
