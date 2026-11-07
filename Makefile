# Nanga build tooling. Scripts live here (no scripts/ folder).
SHELL := /bin/bash
MODULE := github.com/codercollo/nanga
COMPOSE := docker compose -f deploy/docker-compose.yaml
PG_ADMIN_DSN ?= postgres://nanga:nanga@localhost:5432/nanga?sslmode=disable
PHASE ?= 1

.PHONY: tidy up up-infra down gen migrate test test-int e2e lint build count verify-phase

tidy: ## resolve dependencies; commit the resulting go.mod and go.sum
	go mod tidy

go.sum: go.mod ## first run after cloning: resolve dependencies automatically
	go mod tidy

up: ## full local stack (infra + services)
	$(COMPOSE) up -d --build

up-infra: ## Postgres, Redis, RabbitMQ only (enough for make test-int)
	$(COMPOSE) up -d postgres redis rabbitmq

down:
	$(COMPOSE) down

gen: ## regenerate sqlc code (buf generate joins in Phase 2)
	sqlc generate

migrate: ## apply migrations for every schema under db/migrations
	@for dir in db/migrations/*/; do \
	  schema=$$(basename $$dir); \
	  echo "migrating $$schema"; \
	  migrate -path $$dir -database "$(PG_ADMIN_DSN)&x-migrations-table=$${schema}_schema_migrations" up || exit 1; \
	done

test: go.sum ## unit tests with the race detector
	go test -race -count=1 ./...

test-int: go.sum ## integration tests (needs Postgres: make up-infra)
	NANGA_REQUIRE_PG=1 NANGA_TEST_PG_ADMIN_DSN="$(PG_ADMIN_DSN)" go test -race -count=1 -tags=integration ./...

e2e: ## cross-service flows (arrives in Phase 5)
	@echo "e2e: no flows yet (Phase 5)"

lint:
	golangci-lint run ./...

build: go.sum ## make build SERVICE=ledger
	@test -n "$(SERVICE)" || { echo "usage: make build SERVICE=ledger"; exit 1; }
	go build -o bin/$(SERVICE) ./cmd/$(SERVICE)

count: ## files per directory (compare with phases/phase-N/context.md)
	@find . -type f -not -path './.git/*' -not -path './bin/*' | sed 's|/[^/]*$$||' | sort | uniq -c
	@echo "total: $$(find . -type f -not -path './.git/*' -not -path './bin/*' | wc -l)"

verify-phase: ## make verify-phase PHASE=1: every file in the manifest exists, count matches
	@f=phases/phase-$(PHASE)/context.md; test -f $$f || { echo "no $$f"; exit 1; }; \
	list=$$(sed -n '/manifest:start/,/manifest:end/p' $$f | grep -E '^[A-Za-z0-9_./-]+$$'); \
	declared=$$(grep -o 'manifest:count [0-9]*' $$f | awk '{print $$2}'); \
	total=0; missing=0; \
	for p in $$list; do total=$$((total+1)); [ -f "$$p" ] || { echo "MISSING: $$p"; missing=$$((missing+1)); }; done; \
	echo "phase $(PHASE): $$total files listed, declared $$declared, missing $$missing"; \
	[ "$$total" = "$$declared" ] && [ "$$missing" = 0 ] || { echo "FAILED"; exit 1; }; echo "OK"
