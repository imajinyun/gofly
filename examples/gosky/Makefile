.PHONY: check scripts-check vet fmt-check test race build run production-check authorization-smoke project-schema-check mysql-integration casbin-mysql-integration mysql-e2e

GO ?= go
export GO
SCRIPTS_DIR := bin/scripts
.DEFAULT_GOAL := test

check: ## Verify Gosky scripts, formatting, module tests, vet, schema, runtime authorization and MySQL E2Es
	$(MAKE) scripts-check
	$(MAKE) fmt-check
	$(MAKE) test
	$(MAKE) vet
	$(MAKE) project-schema-check
	$(MAKE) authorization-smoke
	$(MAKE) mysql-e2e

scripts-check: ## Verify the MySQL runner process contracts and shell syntax
	python3 -B -m unittest discover -s $(SCRIPTS_DIR)/tests -p test_gosky_mysql.py
	@for script in $(SCRIPTS_DIR)/*.sh; do sh -n "$$script"; done

fmt-check: ## Verify Gosky Go source formatting
	@test -z "$$(gofmt -s -l cmd internal)" || { gofmt -s -l cmd internal; exit 1; }

vet: ## Vet Gosky packages with a temporary build cache
	sh ./$(SCRIPTS_DIR)/go-test.sh --vet ./...

test:
	sh ./$(SCRIPTS_DIR)/go-test.sh -shuffle=on ./...

race:
	sh ./$(SCRIPTS_DIR)/go-test.sh -shuffle=on -race ./...

build:
	go build ./cmd/gosky

run:
	go run ./cmd/gosky

production-check:
	sh ./$(SCRIPTS_DIR)/production-check.sh

authorization-smoke: ## Start the generated API/RPC project and verify JWT/Casbin REST and RPC authorization
	sh ./$(SCRIPTS_DIR)/authorization-smoke.sh

project-schema-check: ## Verify the Project MySQL schema and its HTTP authorization contract
	sh ./$(SCRIPTS_DIR)/project-schema-check.sh

mysql-e2e: ## Run required HTTP/MySQL E2Es in a migrated disposable Docker database
	GO="$(GO)" python3 -B ./$(SCRIPTS_DIR)/check-gosky-mysql.py

mysql-integration: ## Verify Project owner/member/Casbin ABAC and audit records against MySQL
	@test -n "$$GOSKY_MYSQL_DSN" || (echo "GOSKY_MYSQL_DSN is required" >&2; exit 1)
	GOSKY_JWT_SECRET='gosky-local-development-secret-at-least-32-bytes' sh ./$(SCRIPTS_DIR)/go-test.sh -tags=integration ./internal/smoke -run '^TestProjectMySQLAuthorizationIntegration$$'

casbin-mysql-integration: ## Verify MySQL Casbin roles, mutation audit, HTTP management, and cross-instance refresh
	@test -n "$$GOSKY_MYSQL_DSN" || (echo "GOSKY_MYSQL_DSN is required" >&2; exit 1)
	sh ./$(SCRIPTS_DIR)/go-test.sh -tags=integration ./internal/authz -run '^TestMySQLPolicy.*Integration$$'
