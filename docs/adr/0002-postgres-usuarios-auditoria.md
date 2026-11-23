# 0002 — PostgreSQL para usuários, sessões e auditoria

## Contexto

O relay não tinha persistência: uma senha única de console (HTTP Basic) e uma chave compartilhada
pelos agentes. O próximo passo do produto é cadastrar usuários, e acesso remoto pede rastreabilidade
(quem acessou o quê e quando), inclusive pela LGPD.

## Decisão

- **PostgreSQL 19** como banco do relay, acessado com **pgx v5** (driver nativo, sem `database/sql`).
- **Migrations em SQL puro**, embutidas no binário e aplicadas ao iniciar por um migrador próprio de
  ~40 linhas (`postgres.DB.Migrate`), com advisory lock contra duas instâncias ao mesmo tempo. Foi
  escolhido em vez de goose ou golang-migrate por ser pequeno e didático; vale trocar se as
  migrations precisarem de rollback.
- **Senhas com bcrypt** (custo padrão). **Sessões** com token aleatório de 256 bits no cookie e só o
  SHA-256 no banco, válidas por 12 h.
- **Agentes online continuam em memória** (`hub`): é estado efêmero, reconstruído quando os agentes
  reconectam. Colocá-lo no banco só criaria inconsistência.
- **Auditoria copia o nome do usuário** para o registro sobreviver à remoção do usuário (`user_id`
  vira NULL).

Considerado: SQLite (`modernc.org/sqlite`, sem CGO). Mais simples de operar, mas o projeto já roda
em Docker Compose, e Postgres permite testes com banco real descartável e é o que se encontra no mercado.

## Consequências

- Positivo: usuários, papéis e auditoria; o banco pode ser consultado e copiado com ferramentas padrão.
- Negativo: o relay deixa de ser um binário autossuficiente e passa a precisar de um banco disponível.
  `/healthz` reporta o banco, e o compose espera o banco ficar saudável antes de subir o relay.
- Negativo: uma instância só de relay. Várias instâncias exigiriam compartilhar o `hub` (Redis,
  `LISTEN/NOTIFY`), fora do escopo atual.
