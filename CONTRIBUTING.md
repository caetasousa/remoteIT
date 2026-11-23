# Contribuindo

Do clone ao primeiro teste passando em menos de 10 minutos.

## Pré-requisitos

| Ferramenta | Versão | Para quê |
| --- | --- | --- |
| Go | 1.27+ | relay e agente (o `go.mod` baixa a toolchain certa sozinho) |
| Node | 26+ | console Svelte |
| Docker + Compose v2 | 29+ | banco, testes de integração e E2E |
| Zig | 0.17+ (opcional) | agente Windows com H.264; sem ele o agente sai só com JPEG |

## Setup

```bash
cd frontend && npm ci && cd ..
make test          # unitários: ~5 s, sem Docker
make dev-db        # depois, em terminais separados: make dev-relay, make dev-agent, make dev-frontend
```

`make help` lista todos os alvos.

## Organização do código

O código está dividido em **contextos delimitados**: cada pacote tem uma responsabilidade e as
dependências apontam para dentro (`cmd` → contextos → `protocol`). Mapa completo e regras em
[docs/ARQUITETURA.md](docs/ARQUITETURA.md) e [docs/FLUXOS.md](docs/FLUXOS.md). Na prática:

- **Regra de negócio** fica no pacote do contexto (`hub`, `identity`, `audit`, `session`, `media`),
  nunca nos handlers HTTP ou no `main`.
- **SQL** só em `internal/relay/storage/postgres`. Os contextos definem interfaces (`identity.Repository`,
  `audit.Store`) e o Postgres as implementa.
- **Código de Windows** usa sufixo `_windows.go` com um par `_other.go`. O resto do código não
  conhece a plataforma, e tudo compila e testa no Linux.
- **`cmd/*/main.go` só monta as peças**: lê flags e ambiente, cria as dependências e chama `Run`.

## Mudanças que tocam mais de um lugar

- **Protocolo WebSocket:** altere `internal/protocol` **e** `frontend/src/lib/protocol.ts`, depois
  atualize [docs/PROTOCOLO.md](docs/PROTOCOLO.md). O teste `TestFrameLayout` fixa o layout binário.
- **Schema do banco:** crie `internal/relay/storage/postgres/migrations/NNNN_descricao.sql` com o
  próximo número. Migrations já aplicadas nunca são editadas; o relay aplica as novas ao iniciar.
- **API HTTP:** handler em `internal/relay/httpapi`, cliente em `frontend/src/lib/api.ts`, tabela
  em [docs/API.md](docs/API.md).

## Testes

| Comando | O que roda | Tempo |
| --- | --- | --- |
| `make test` | unitários com `-race` | segundos |
| `make test-integration` | relay + agente + Postgres reais (testcontainers) | ~1 min |
| `make test-e2e` | Chromium (Playwright) contra o ambiente do `compose.e2e.yaml` | ~2 min na primeira vez |
| `make check` | `go vet` em Linux e Windows + `svelte-check` | segundos |
| `make test-all` | tudo acima | |

Prefira testes de integração com dependências reais a mocks. Fakes só onde o recurso real não
existe no CI: SendInput, caixa de consentimento e tela. Detalhes em [docs/TESTES.md](docs/TESTES.md).

## Estilo

- `gofmt` e `go vet` limpos; comentários e mensagens em português.
- Todo pacote tem comentário `// Package x ...`. Identificadores exportados têm doc comment que
  explica o **porquê** e as restrições, não o que o código já mostra.
- Registre no [CHANGELOG.md](CHANGELOG.md) o que muda para quem usa o sistema.
