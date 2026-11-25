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
| `GET /api/agents` | logado | | 200 `Machine[]`: as que o usuário acessa (admin: todas), online e offline |
| `DELETE /api/machines/{id}` | dono ou admin | | 204; o agente é desconectado e passa a receber 403 |
| `GET /api/links` | logado | | 200 `InstallLink[]` válidos do usuário |
| `POST /api/links` | logado | `{edition: portatil \| servico, hours: 1–168 ou 0 = sem prazo, uses: 1–100 ou 0 = sem limite}` | 201 `InstallLink` + `path` (`/i/<código>`, só nesta resposta) · 400 |
| `DELETE /api/links/{id}` | dono do link ou admin | | 204 · 404 |
| `GET /api/agent/update?mid=&edition=` | credencial da máquina | | 200 `{version, sha256, signature, size}` do `update.json` · 204 sem atualização publicada · 401/403 |
| `GET /api/agent/update/{edition}?mid=` | credencial da máquina | | o `.exe` da edição, **sem** código gravado ([ADR 0011](adr/0011-atualizacao-automatica.md)) |
| `GET /download/viewer.exe` | logado | | o visualizador nativo do técnico (`remoteit-tecnico.exe`) |
| `GET /api/viewer/update` | logado | | oferta assinada da edição `tecnico` · 204 sem atualização |
| `GET /api/viewer/update.exe` | logado | | o visualizador, para a atualização automática |
| `GET /i/{código}` | **público** | | o `.exe` da edição do link, marcado com o código · 404 inválido/vencido/esgotado · 429 (200/dia por IP) |
| `GET /i/{código}/ps` | **público** | | script PowerShell para `irm <link>/ps \| iex`: baixa o `.exe` do mesmo link, confere o SHA-256 e instala (serviço com UAC, ou portátil em `%LOCALAPPDATA%\RemoteIT`). `text/plain; charset=utf-8`, `no-store` · mesmos 404/429 do link |
| `POST /api/agent/enroll` | **público** + código | `{code, name}` | 201 `{id, secret}` · 403 código inválido · 400 · 429 (120/h por IP) |
| `GET /api/users` | admin | | 200 `User[]` · 403 |
| `POST /api/users` | admin | `{username, password, role}` | 201 `User` · 400 inválido · 409 duplicado |
| `POST /api/users/{id}/approve` | admin | | 200 `User` · 404 |
| `DELETE /api/users/{id}` | admin | | 204 · 400 a si mesmo · 404 |
| `GET /api/audit` | admin | | 200 `AuditEntry[]` (últimos 200, mais novo primeiro) |
| `GET /ws/viewer?id=` | quem acessa a máquina | | WebSocket ([protocolo](PROTOCOLO.md)) · 404 sem acesso ou offline · close 1011 sem auditoria |
| `GET /ws/agent?mid=&host=&user=&ver=` | credencial (`Authorization: Bearer`) | | WebSocket · 401 credencial inválida · 403 máquina removida |
| `GET /ws/agent?id=&host=&user=&key=` | chave (legado) | | WebSocket · 403 (ou com `RELAY_LEGACY_AGENTS=false`) |
| `GET /download/agent.exe` | logado | | `suporte-ti.exe` **marcado com um código novo do usuário** (30 dias, até 50 máquinas); `Cache-Control: no-store, private` · 503 se o `.exe` do servidor não tem o espaço do código |
| `GET /download/agent-servico.exe` | logado | | o mesmo, edição de serviço (`-agent-service-exe`, padrão: ao lado do `-agent-exe`) |
| `GET /healthz` | público | | 200 `ok` · 503 banco fora |

## 🧾 Tipos

```ts
User       { id, username, email?, pending, role: "admin" | "technician", createdAt }
Machine     { id, name, owner?, legacy, createdAt, lastSeenAt?,
              online, user?, since?, busy, viewedBy?, mine, canManage }
InstallLink { id, kind, edition, maxUses (1 000 000 = sem limite), uses, createdAt,
              expiresAt (daqui a 100 anos = sem prazo), path? (só na criação) }
AuditEntry { id, userId (0 se removido), username, agentId, host, remoteAddr, startedAt, endedAt? }
```

## 🚦 Limites e regras

`POST /api/login` responde **429** depois de 10 senhas erradas para o mesmo usuário ou 20 do mesmo IP
em 15 minutos (só falhas contam). Todas as respostas levam `Content-Security-Policy`,
`X-Frame-Options: DENY`, `X-Content-Type-Options: nosniff` e `Referrer-Policy: no-referrer`.

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
