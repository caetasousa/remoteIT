# 🛡️ Segurança

> 🗣️ **Em palavras simples:** um programa de acesso remoto é, por natureza, uma porta para dentro de um computador. Esta página lista **quem pode abrir essa porta**, as trancas que existem em cada etapa (login, sessão, consentimento, auditoria, contêineres) e, com a mesma honestidade, **o que ainda não está protegido**.

---

## 🎯 Modelo de ameaça

| Quem | O que tentaria | Tranca principal |
|---|---|---|
| 🌐 Qualquer um na internet | entrar no console, listar máquinas, abrir uma tela | login + cookie de sessão; tudo além do login exige autenticação |
| 🕸️ Um site malicioso aberto pelo técnico | usar o cookie do técnico para agir em nome dele (CSRF / CSWSH) | `SameSite=Strict`, JSON obrigatório, checagem de `Origin` no WebSocket |
| 🧑‍💻 Um técnico | ver uma máquina sem o usuário saber | consentimento por padrão + auditoria de todo acesso |
| 🧑‍💻 Um técnico | abrir a máquina de **outro** técnico ou cliente | cada máquina tem dono; acessam só o dono e os admins |
| 🖥️ Alguém com um `.exe` do agente | se passar por uma máquina existente | ID aleatório + credencial própria por máquina; o `.exe` só cadastra máquinas **novas** no nome do técnico |
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
| 🚦 **Limite de senhas erradas** | 10 por usuário ou 20 por IP em 15 min → 429, até para a senha certa; só falhas contam | `httpapi/ratelimit.go`, `TestLoginRateLimit` |
| 🗑️ Usuário removido | as sessões dele deixam de valer na hora | `TestUserManagement` |

### 🕸️ Pedidos forjados (CSRF e CSWSH)

| Defesa | Detalhe |
|---|---|
| `SameSite=Strict` | o navegador não manda o cookie em pedidos que partem de outro site |
| 🧩 **CSP** em todas as respostas | `script-src 'self'` (nenhum script inline ou de fora), `connect-src` só o próprio relay, `frame-ancestors 'none'`, `object-src 'none'`; mais `X-Frame-Options: DENY`, `nosniff` e `Referrer-Policy: no-referrer` (a URL de um link de instalação não vaza) |
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

### 🖥️ Máquinas e agentes ([ADR 0010](adr/0010-maquinas-por-tecnico.md))

| Defesa | Detalhe | Onde |
|---|---|---|
| 🏷️ **`.exe` marcado por técnico** | cada download grava um código de cadastro novo do técnico no binário | `internal/relay/agentbin` |
| 🪪 **Credencial por máquina** | ID aleatório `m_...` + segredo de 256 bits; no banco só o SHA-256, comparado em tempo constante | `internal/relay/machines` |
| 🔐 **Credencial protegida no PC** | DPAPI: o `machine.json` copiado para outro PC não abre | `internal/agent/machine` |
| 🚪 **Acesso por dono** | lista e WebSocket filtram por dono / admin; 404 igual para "não existe" e "não é sua" | `httpapi`, `machines.CanAccess` |
| ✂️ **Remover = revogar** | o agente cai na hora, recebe 403 ao reconectar e não se recadastra sozinho | `TestMachineCredentialRules` |
| 🚫 **Legado não toma ID de máquina** | um agente com a chave antiga não pode usar um ID `m_...`; com `RELAY_LEGACY_AGENTS=false`, nem conecta | `machines.ValidLegacyID` |
| 🔗 **Links revogáveis** | validade de 1 hora a 7 dias ou sem prazo, 1 a 100 máquinas ou sem limite; revogáveis a qualquer momento; o código só aparece na criação; 200 downloads/dia por IP | `TestInstallLink` |
| 🚦 **Cadastro limitado** | 120 cadastros/hora por IP | `httpapi` |

### 🔄 Atualização automática ([ADR 0011](adr/0011-atualizacao-automatica.md))

| Defesa | Detalhe |
|---|---|
| 🔏 Assinatura própria (Ed25519) | o agente só aceita a versão cuja edição + versão + SHA-256 foram assinados pela chave do projeto, que fica fora do servidor |
| #️⃣ Hash do `.exe` | o arquivo baixado precisa ter exatamente o SHA-256 assinado |
| 🪪 Só máquinas cadastradas | oferta e download exigem a credencial da máquina |
| 🧪 Testado | `.exe` trocado, manifesto reescrito e chave de outro projeto são recusados (`TestAgentUpdateRejectsTampering`) |

### 🖥️ Visualizador do técnico ([ADR 0012](adr/0012-visualizador-nativo.md))

| Defesa | Detalhe |
|---|---|
| 🍪 Mesma sessão do console | login em `/api/login`, as mesmas regras (limite de senhas, só máquinas do técnico) |
| 🔐 Token guardado com DPAPI | `%LOCALAPPDATA%\RemoteIT\viewer.token` só abre para o usuário do Windows que fez login; **Sair** apaga |
| 📋 Área de transferência só com consentimento | a mensagem `clip` é ignorada antes de `granted`, como mouse e teclado; até 1 MiB |
| ⌨️ Hook de teclado só em primeiro plano | as teclas só são capturadas com a janela da sessão em primeiro plano e a opção **Teclado** ligada |

### 📜 Auditoria

* 🔚 **Reinício do relay:** os acessos que ficaram abertos são encerrados na partida seguinte (`TestOrphanAccessesClosedOnStart`).
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
| 🏚️ **Agentes legados** (chave compartilhada) enquanto `RELAY_LEGACY_AGENTS` estiver ligado | uma máquina legada ainda pode ser imitada por quem tem a chave | só admins veem legados; eles têm selo "antigo" no console | reinstalar as máquinas e desligar `RELAY_LEGACY_AGENTS` |
| 🔗 **Link permanente vazado** | quem tiver o link cadastra máquinas novas no nome do técnico, para sempre | as máquinas aparecem na lista dele; não dá acesso a nenhuma máquina existente; o link pode ser revogado | conferir de vez em quando a lista de links e revogar os que não usa mais |
| 🏷️ **Código de download vale 30 dias e 50 máquinas** | quem pegar um `.exe` de um técnico cadastra máquinas no nome dele | as máquinas aparecem na lista dele, que pode removê-las; não dá acesso a máquinas existentes | encurtar a validade ou usar links de 1 uso |
| 🛡️ **Worker como SYSTEM** (modo serviço) | uma falha no agente seria uma falha com o privilégio máximo da máquina | só aceita entrada depois do consentimento e só fala com o relay autenticado | separar captura/entrada (SYSTEM) do transporte (usuário comum) |
| 🙋 **Build `NO_CONSENT=1`** | o técnico vê a tela sem o usuário perceber | a auditoria registra tudo; o padrão do build é pedir | usar só com aviso formal aos usuários |
| 🧾 **`.exe` sem assinatura** | SmartScreen e antivírus reclamam; um `.exe` adulterado não seria detectado pelo Windows | baixar só do console | certificado de code signing ([AGENTE-WINDOWS.md](AGENTE-WINDOWS.md#antivirus)) |
| 🎨 **CSP com `style-src 'unsafe-inline'`** | estilos injetados não seriam bloqueados | scripts inline continuam proibidos | trocar os estilos dinâmicos por variáveis CSS e tirar o `unsafe-inline` |
| 🔑 **Chave de atualização vazada ou perdida** | vazada: alguém assina um `.exe` malicioso e todos os agentes o instalam; perdida: nenhum agente atualiza mais | a chave fica só na máquina de build, fora do repositório e do servidor | cópia offline protegida; no futuro, trocar a chave por uma nova assinada pela antiga |
| 🔁 Sem segundo fator (2FA) | uma senha vazada basta para entrar | recuperação derruba sessões | TOTP para admins |

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
