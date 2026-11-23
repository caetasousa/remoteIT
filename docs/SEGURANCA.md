# 🛡️ Segurança

> 🗣️ **Em palavras simples:** um programa de acesso remoto é, por natureza, uma porta para dentro de um computador. Esta página lista **quem pode abrir essa porta**, as trancas que existem em cada etapa (login, sessão, consentimento, auditoria, contêineres) e, com a mesma honestidade, **o que ainda não está protegido**.

---

## 🎯 Modelo de ameaça

| Quem | O que tentaria | Tranca principal |
|---|---|---|
| 🌐 Qualquer um na internet | entrar no console, listar máquinas, abrir uma tela | login + cookie de sessão; tudo além do login exige autenticação |
| 🕸️ Um site malicioso aberto pelo técnico | usar o cookie do técnico para agir em nome dele (CSRF / CSWSH) | `SameSite=Strict`, JSON obrigatório, checagem de `Origin` no WebSocket |
| 🧑‍💻 Um técnico | ver uma máquina sem o usuário saber | consentimento por padrão + auditoria de todo acesso |
| 🖥️ Alguém com um `.exe` do agente | registrar uma máquina falsa | a chave do agente; ver [riscos](#riscos) |
| 💾 Quem roubar o banco | se passar por alguém | só **hashes**: bcrypt nas senhas, SHA-256 nos tokens de sessão e nos links |

---

## 🔐 Defesas, camada por camada

### 👤 Login e sessão

| Defesa | Detalhe | Onde |
|---|---|---|
| 🧂 Senha com **bcrypt** | custo padrão; senha de 8 a 72 caracteres | `internal/relay/identity` |
| ⏱️ **Mesmo tempo** para usuário inexistente | compara com um hash falso, para não revelar quais usuários existem | `identity/service.go` |
| 🍪 Cookie `remoteit_session` | `HttpOnly` (o JavaScript não lê), `SameSite=Strict`, `Secure` em HTTPS, 12 h | `httpapi/auth.go` |
| #️⃣ **Só o hash do token** no banco | o token (32 bytes aleatórios) vive só no cookie; o banco guarda o SHA-256 | tabela `sessions` |
| 🚪 Conta **pendente** não entra | login responde 403 até um admin aprovar | `identity` |
| 🗑️ Usuário removido | as sessões dele deixam de valer na hora | `TestUserManagement` |

### 🕸️ Pedidos forjados (CSRF e CSWSH)

| Defesa | Detalhe |
|---|---|
| `SameSite=Strict` | o navegador não manda o cookie em pedidos que partem de outro site |
| **JSON obrigatório** | todo `POST` com corpo exige `Content-Type: application/json` (senão **415**). Um formulário HTML de outro site não consegue mandar esse tipo sem um preflight CORS, que o relay não autoriza |
| **`Origin` no WebSocket** | `/ws/viewer` só aceita `Origin` igual ao host (CSWSH); o teste prova o 403 |
| Corpo limitado | JSON de no máximo 64 KiB (`MaxBytesReader`) |

### 🎭 Papéis

| Papel | Pode |
|---|---|
| 👑 `admin` | tudo do técnico + usuários (criar, aprovar, remover) + auditoria |
| 🧑‍💻 `technician` | ver a lista de máquinas, conectar, baixar o agente |

* Rotas de admin passam por `s.admin(...)` e respondem **403** para técnicos. Ninguém remove a si mesmo.

### ✉️ Contas por e-mail

| Defesa | Detalhe |
|---|---|
| 🙈 **Não revela contas** | cadastro e recuperação respondem **202** exista ou não o e-mail |
| 🔗 Link de **uso único** | `account_tokens` guarda só o SHA-256; marcado como usado na primeira vez, inclusive sob pedidos concorrentes |
| ⏳ Validade curta | cadastro 24 h; recuperação e vínculo 1 h |
| 🚦 Limites | 20 pedidos/h por IP; 3/h por destinatário e tipo; por dia: 30 cadastros, 30 recuperações, 20 vínculos |
| 🌐 Base do link fixa | os links usam `RELAY_PUBLIC_URL`, **nunca** o cabeçalho `Host` (que pode ser forjado para mandar o link a um domínio do atacante) |
| 💥 Recuperação derruba tudo | trocar a senha apaga todas as sessões e fecha as telas remotas abertas pela conta |

### 🙋 No PC atendido

| Defesa | Detalhe |
|---|---|
| ✅ **Consentimento** | por padrão o usuário aprova cada acesso; sem `granted`, o agente descarta mouse, teclado e o pedido de bloqueio (`TestInputOnlyAfterConsent`, `TestLockOnlyAfterConsent`) |
| 🔒 Bloqueio só pela API oficial | `LockWorkStation`; o Windows não aceita Win+L simulado, de propósito |
| ⌨️ Ctrl+Alt+Del | o Windows reserva a combinação; o agente não tenta contorná-la |
| 🔇 Sem janelas | nada pede ao usuário para clicar em "OK" sem ler; o resultado fica em log |

### 📜 Auditoria

* 📝 **Sem registro, sem acesso:** o relay grava o início **antes** de ligar o técnico ao agente; se o banco falhar, fecha com 1011 (`TestAuditUnavailableRefusesViewer`).
* 👤 O nome do técnico é **copiado** para o registro, que continua legível depois que o usuário é removido.
* 🌐 **IP real:** atrás do Cloudflare Tunnel, `RELAY_CLIENT_IP_HEADER=Cf-Connecting-IP`. Só use essa variável se o relay **não** for acessível sem o túnel: o cabeçalho pode ser forjado por quem chega direto.

### 📦 Download do agente

* 🔐 As duas rotas exigem login (sem cookie: **401**). O `.exe` carrega a chave do agente embutida.
* 🚫 `Cache-Control: no-store, private`: nenhum cache compartilhado (Cloudflare) guarda o arquivo, e o navegador não reaproveita uma versão antiga (`TestAgentDownloadIsNeverCached`).

### 🐳 Servidor e contêineres

| Defesa | Detalhe |
|---|---|
| 🧱 Imagem **distroless** | sem shell nem gerenciador de pacotes; usuário não-root |
| 👤 **Docker rootless**, um usuário Linux por app | um contêiner comprometido não vira root no host |
| 🚪 Nenhuma porta aberta | o relay escuta só em `127.0.0.1`; o mundo chega pelo túnel, que sai do servidor |
| 🐘 Banco fechado | o PostgreSQL só existe na rede interna do compose |
| 🔑 Segredos fora da imagem | `.env` com modo 600; no build do agente via Docker, a chave entra por `--secret` e não fica no histórico da imagem |

---

<a id="riscos"></a>
## ⚠️ Riscos conhecidos (ainda não resolvidos)

| Risco | Impacto | Mitigação hoje | Caminho |
|---|---|---|---|
| 🔑 **Chave do agente compartilhada** e embutida no `.exe` | quem tem o `.exe` pode registrar uma máquina falsa e ver técnicos conectando nela | o `.exe` só sai para quem está logado | uma chave por máquina, com revogação |
| 🚪 **Login sem limite de tentativas** | força bruta lenta, mas possível | bcrypt + senha de no mínimo 8 caracteres | limite por IP e por usuário, como já existe nos e-mails |
| 🛡️ **Worker como SYSTEM** (modo serviço) | uma falha no agente seria uma falha com o privilégio máximo da máquina | só aceita entrada depois do consentimento e só fala com o relay autenticado | separar captura/entrada (SYSTEM) do transporte (usuário comum) |
| 🙋 **Build `NO_CONSENT=1`** | o técnico vê a tela sem o usuário perceber | a auditoria registra tudo; o padrão do build é pedir | usar só com aviso formal aos usuários |
| 🧾 **`.exe` sem assinatura** | SmartScreen e antivírus reclamam; um `.exe` adulterado não seria detectado pelo Windows | baixar só do console | certificado de code signing ([AGENTE-WINDOWS.md](AGENTE-WINDOWS.md#antivirus)) |
| 🧩 **Sem CSP** no console | um XSS teria menos barreiras | o Svelte escapa o conteúdo por padrão; não há HTML vindo do usuário | `Content-Security-Policy` restritiva no `staticHandler` |
| 🔁 Sem segundo fator (2FA) | uma senha vazada basta para entrar | recuperação derruba sessões | TOTP para admins |
| 📜 Relay reiniciado com sessão aberta | a auditoria fica com `ended_at` nulo | conferir antes de reiniciar | fechar sessões abertas ao iniciar |

---

## 🧪 Como conferir algumas defesas

```bash
B=http://localhost:8080

# 🚫 sem login, nada além do login
curl -s -o /dev/null -w "%{http_code}\n" $B/api/agents                    # 401
curl -s -o /dev/null -w "%{http_code}\n" $B/download/agent-servico.exe    # 401

# 🕸️ POST sem JSON (como um formulário de outro site faria)
curl -s -o /dev/null -w "%{http_code}\n" -d 'username=admin&password=x' $B/api/login   # 415

# 🙈 recuperação não revela se a conta existe
curl -s -o /dev/null -w "%{http_code}\n" -H 'Content-Type: application/json' \
  -d '{"email":"nao-existe@exemplo.com"}' $B/api/account/reset             # 202 (ou 503 sem SMTP)
```

Os testes de integração em [`test/integration`](../test/integration) provam o resto (CSWSH, papéis, auditoria obrigatória, links de uso único, consentimento): veja [TESTES.md](TESTES.md).
