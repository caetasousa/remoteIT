# Atalhos do projeto. `make help` lista os alvos.

COMPOSE_DEV := docker compose -p remoteit-dev -f compose.yaml -f compose.dev.yaml
COMPOSE_E2E := docker compose -p remoteit-e2e -f compose.yaml -f compose.e2e.yaml
E2E_ENV := POSTGRES_PASSWORD=e2e RELAY_AGENT_KEY=e2e-agent-key-0123456789 \
	RELAY_ADMIN_PASS=e2e-admin-pass RELAY_PORT=18080 E2E_UID=$$(id -u) E2E_GID=$$(id -g)

DEV_ENV := DATABASE_URL=postgres://remoteit:dev@localhost:5432/remoteit?sslmode=disable \
	RELAY_AGENT_KEY=dev-agent-key-0123456789 RELAY_ADMIN_PASS=dev-admin-pass

.PHONY: help frontend check test test-integration test-e2e test-all build up down \
	dev-db dev-relay dev-agent dev-frontend

help: ## lista os alvos
	@grep -E '^[a-z-]+:.*## ' $(MAKEFILE_LIST) | awk -F':.*## ' '{printf "  %-18s %s\n", $$1, $$2}'

frontend: ## compila o console Svelte para internal/relay/web/dist
	cd frontend && npm ci && npm run build

check: ## go vet (Linux e Windows) + svelte-check
	go vet ./...
	GOOS=windows go vet ./...
	go vet -tags integration ./test/...
	cd frontend && npm run check

test: ## testes unitários (rápidos, sem Docker)
	go test -race ./...

test-integration: ## relay + agente + Postgres reais (precisa de Docker)
	go test -race -count=1 -tags integration ./test/integration/...

test-e2e: ## navegador real contra o ambiente do compose.e2e.yaml
	$(E2E_ENV) $(COMPOSE_E2E) up -d --build --wait relay agent mailpit
	$(E2E_ENV) $(COMPOSE_E2E) run --rm e2e; status=$$?; \
		$(E2E_ENV) $(COMPOSE_E2E) down -v; exit $$status

test-all: check test test-integration test-e2e ## tudo, na ordem do mais rápido ao mais lento

build: ## binários em dist/ (veja build.sh; precisa de RELAY e AGENT_KEY)
	./build.sh

up: ## sobe relay + banco com o .env
	docker compose up -d --build

down: ## para relay + banco (o volume do banco é mantido)
	docker compose down

dev-db: ## banco de desenvolvimento em localhost:5432
	POSTGRES_PASSWORD=dev RELAY_AGENT_KEY=x $(COMPOSE_DEV) up -d db

dev-relay: ## relay local em :8080 (rode dev-db antes)
	$(DEV_ENV) go run ./cmd/relay -insecure -addr :8080

dev-agent: ## agente local com tela sintética conectado ao dev-relay
	REMOTEIT_RELAY=ws://localhost:8080 REMOTEIT_KEY=dev-agent-key-0123456789 go run ./cmd/agent -noconsent

dev-frontend: ## Vite com hot reload em :5173, proxy para o dev-relay
	cd frontend && npm run dev
