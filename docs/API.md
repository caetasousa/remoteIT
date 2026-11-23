# 🔌 API HTTP do console

> 🗣️ **Em palavras simples:** tudo o que o console faz (entrar, listar máquinas, cadastrar técnicos, ver a auditoria) é um pedido HTTP com JSON para o relay. Esta página lista cada rota, quem pode chamar e o que ela devolve, para quem quiser integrar outro sistema ou testar pelo terminal.

📌 Implementação: [`internal/relay/httpapi`](../internal/relay/httpapi). Cliente: [`frontend/src/lib/api.ts`](../frontend/src/lib/api.ts).

## 📏 Regras gerais

- Corpo e respostas em JSON. Requisições com corpo **devem** usar `Content-Type: application/json`
  (senão 415), o que reforça a proteção contra CSRF.
- Autenticação por cookie `remoteit_session` (`HttpOnly`, `SameSite=Strict`, `Secure` em HTTPS),
  válido por 12 h.
- Erros: `{"error": "mensagem"}` com o status HTTP correspondente.

## 🗺️ Rotas

| Método e rota | Acesso | Corpo | Resposta |
| --- | --- | --- | --- |
| `POST /api/login` | público | `{username, password}` | 200 `User` + cookie · 401 · 403 pendente |
| `POST /api/logout` | qualquer | | 204, cookie apagado |
| `GET /api/account/config` | público | | 200 `{accounts}`: cadastro por e-mail ligado? |
| `POST /api/account/signup` | público | `{email}` | 202 exista ou não a conta · 400 · 429 · 503 |
| `POST /api/account/signup/complete` | link | `{token, username, password}` | 201 `User` · 400 · 409 nome em uso |
| `POST /api/account/reset` | público | `{email}` | 202 exista ou não a conta · 400 · 429 · 503 |
| `POST /api/account/reset/complete` | link | `{token, password}` | 204, sessões e telas abertas encerradas · 400 |
| `POST /api/account/email` | logado | `{email, password}` | 202 · 403 senha errada · 409 já tem e-mail |
| `POST /api/account/email/complete` | logado + link | `{token}` | 200 `User` · 400 |
| `GET /api/me` | logado | | 200 `User` · 401 |
| `GET /api/agents` | logado | | 200 `AgentInfo[]` |
| `GET /api/users` | admin | | 200 `User[]` · 403 |
| `POST /api/users` | admin | `{username, password, role}` | 201 `User` · 400 inválido · 409 duplicado |
| `POST /api/users/{id}/approve` | admin | | 200 `User` · 404 |
| `DELETE /api/users/{id}` | admin | | 204 · 400 a si mesmo · 404 |
| `GET /api/audit` | admin | | 200 `AuditEntry[]` (últimos 200, mais novo primeiro) |
| `GET /ws/viewer?id=` | logado | | WebSocket ([protocolo](PROTOCOLO.md)) · 404 offline · close 1011 sem auditoria |
| `GET /ws/agent?id=&host=&user=&key=` | chave | | WebSocket · 403 |
| `GET /download/agent.exe` | logado | | `suporte-ti.exe`, edição portátil (se `-agent-exe`); `Cache-Control: no-store, private` |
| `GET /download/agent-servico.exe` | logado | | `suporte-ti-servico.exe`, edição de serviço (`-agent-service-exe`, padrão: ao lado do `-agent-exe`) |
| `GET /healthz` | público | | 200 `ok` · 503 banco fora |

## 🧾 Tipos

```ts
User       { id, username, email?, pending, role: "admin" | "technician", createdAt }
AgentInfo  { id, host, user, since, busy, viewedBy? }
AuditEntry { id, userId (0 se removido), username, agentId, host, remoteAddr, startedAt, endedAt? }
```

## 🚦 Limites e regras

`username` no login aceita o nome ou o e-mail. Sem SMTP, as rotas de `/api/account/*` (menos
`config`) respondem 503. Limites de e-mail: 20 pedidos/h por IP; 3/h por destinatário e tipo; por dia,
30 de cadastro, 30 de recuperação e 20 de vínculo.

Regras de cadastro: nome com 3 a 32 caracteres `a-z 0-9 . _ -`; senha de 8 a 72 caracteres.

## 💻 Exemplo pelo terminal

```bash
# entrar e guardar o cookie
curl -c cookie.txt -H 'Content-Type: application/json' \
  -d '{"username":"admin","password":"dev-admin-pass"}' http://localhost:8080/api/login

# máquinas online
curl -b cookie.txt http://localhost:8080/api/agents

# baixar o agente de serviço
curl -b cookie.txt -o suporte-ti-servico.exe http://localhost:8080/download/agent-servico.exe
```

> ⚠️ Sem `Content-Type: application/json`, o `POST` responde **415**: é de propósito ([SEGURANCA.md](SEGURANCA.md)).
