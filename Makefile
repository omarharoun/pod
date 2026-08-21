# Pod — repo-level tasks. Each platform keeps its own build; this file only covers the
# things that cross platform boundaries.

SHELL := /bin/bash
ROOT  := $(shell pwd)

.PHONY: help generate verify copylint spec backend-test backend-run migrate clean

help: ## Show this help
	@grep -hE '^[a-z-]+:.*?## ' $(MAKEFILE_LIST) | awk 'BEGIN{FS=":.*?## "}{printf "  \033[36m%-14s\033[0m %s\n", $$1, $$2}'

generate: ## Regenerate models, copy keys, tokens and docs/API.md from the sources of truth
	@cd tools/codegen && go run . --root "$(ROOT)"

spec: ## Validate openapi.yaml without writing anything
	@cd tools/codegen && go run . --root "$(ROOT)" --check

copylint: ## Enforce CLAUDE.md §2 rule 1 over content/microcopy.json
	@python3 tools/copylint/copylint.py --root "$(ROOT)"

backend-test: ## Backend unit + integration tests
	@cd backend && go test ./...

backend-run: ## Run the API against a local Postgres
	@cd backend && go run ./cmd/api

migrate: ## Apply pending migrations to $$DATABASE_URL
	@cd backend && go run ./cmd/migrate up

verify: spec copylint backend-test ## Everything that can run on a Linux CI box
	@echo "verify: ok — note that iOS and Android builds need macOS / the Android SDK"

clean:
	@rm -rf backend/bin
