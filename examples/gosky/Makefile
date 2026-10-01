.PHONY: test race build run production-check authorization-smoke project-schema-check mysql-integration

test:
	sh ./bin/go-test.sh -shuffle=on ./...

race:
	sh ./bin/go-test.sh -shuffle=on -race ./...

build:
	go build ./cmd/gosky

run:
	go run ./cmd/gosky

production-check:
	sh ./bin/production-check.sh

authorization-smoke: ## Start the generated API/RPC project and verify JWT/Casbin REST and RPC authorization
	GOSKY_JWT_SECRET='gosky-local-development-secret-at-least-32-bytes' sh ./bin/go-test.sh ./internal/smoke -run '^TestGeneratedProductionServiceSmoke$$'

project-schema-check: ## Verify the Project MySQL schema and its HTTP authorization contract
	sh ./bin/project-schema-check.sh

mysql-integration: ## Verify Project owner/member/Casbin ABAC and audit records against MySQL
	@test -n "$$GOSKY_MYSQL_DSN" || (echo "GOSKY_MYSQL_DSN is required" >&2; exit 1)
	GOSKY_JWT_SECRET='gosky-local-development-secret-at-least-32-bytes' sh ./bin/go-test.sh -tags=integration ./internal/smoke -run '^TestProjectMySQLAuthorizationIntegration$$'
