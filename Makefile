.PHONY: build test test-integration lint tidy up down migrate ingest-fixture sbom zeek-replay clean

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

clean:
	rm -rf bin out
