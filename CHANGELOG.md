# Changelog

Todas as mudanças relevantes do projeto ficam registradas aqui.

O formato segue o [Keep a Changelog](https://keepachangelog.com/pt-BR/1.1.0/) e o projeto adota
[Versionamento Semântico](https://semver.org/lang/pt-BR/).

## [Unreleased]

### Added

- **Visualizador do técnico com interface nova**: cabeçalho com usuário e papel, busca, lista com
  estado colorido, usuário logado, dono e versão do agente, rodapé com o que o PC decodifica
  (H.264, 4:4:4, AV1); na sessão, **barra de telemetria** (codec, GPU/CPU, resolução, FPS
  desenhados, banda e RTT colorido) com o motivo de o 4:4:4 não estar em uso, avisos num cartão
  central e **Ajuda ▸ Diagnóstico de vídeo**. Erros de login em frases claras. Regras em
  `internal/viewer/ui`, testadas no Linux.
- **Negociação de codecs** ([ADR 0013](docs/adr/0013-codecs-e-negociacao.md)): o `hello` leva
  `codecs`, a lista do que o técnico decodifica em ordem de preferência; o agente abre o primeiro que
  conseguir e cai sozinho para o próximo se um encoder não abrir ou quebrar. O técnico também desiste
  de um codec depois de três falhas seguidas do decodificador. O campo `h264` continua para agentes
  antigos.
- **Cor 4:4:4 com a tela em movimento** no visualizador nativo (AVC444, codec 4): duas imagens H.264
  4:2:0 por quadro, montadas por shaders D3D11 e codificadas por dois encoders de GPU, recompostas no
  visualizador. Menu **Qualidade → Cor 4:4:4**, ligado por padrão, nos presets de resolução nativa.
  Referência do arranjo em `internal/avc444`.
- **AV1 opcional** (codec 5): encoder AV1 de GPU do Media Foundation no agente; no navegador, só
  quando o `VideoDecoder` decodifica AV1 na GPU; no visualizador, com a Extensão de Vídeo AV1.
- **Cada técnico com as suas máquinas** ([ADR 0010](docs/adr/0010-maquinas-por-tecnico.md)): o `.exe`
  baixado sai marcado com um código do técnico; na primeira execução a máquina troca o código por
  uma credencial própria (ID aleatório, DPAPI) e fica no nome dele. Só o dono e os admins veem e
  acessam; **Remover** revoga a credencial. Não há compartilhamento entre técnicos.
- **Link de instalação** (`/i/<código>`): o cliente baixa o agente sem conta; validade de 1 hora a
  7 dias ou **sem prazo**, de 1 a 100 máquinas ou **sem limite**, revogável.
- **Visualizador do técnico para Windows** (`remoteit-tecnico.exe`, [ADR 0012](docs/adr/0012-visualizador-nativo.md),
  [guia](docs/VISUALIZADOR.md)): login com a mesma conta, lista de máquinas, uma janela por sessão,
  H.264 pelo Media Foundation (GPU quando possível), todas as teclas sem tela cheia, área de
  transferência para o PC remoto (mensagem `clip`), atualização automática. O console continua.
- **Atualização automática do agente** ([ADR 0011](docs/adr/0011-atualizacao-automatica.md)): versão
  em cada `.exe`, `dist/update.json` assinado com uma chave Ed25519 do projeto (`cmd/signupdate`), e o
  agente se troca sozinho, fora de atendimento, quando o relay anuncia outra versão. Coluna **Versão**
  no console.
- `RELAY_LEGACY_AGENTS`: agentes antigos continuam aceitos (só admins veem) até serem reinstalados.
- Rotas `/api/machines/{id}`, `/api/links`, `/api/agent/enroll`, `/i/{code}`.

- Criação de conta por e-mail, com aprovação de um administrador, e recuperação de acesso.
- Associação de e-mail às contas existentes e login por usuário ou e-mail.
- `RELAY_ADMIN_EMAIL`: primeiro administrador sem senha no ambiente.
- Usuários com papéis (administrador e técnico), cadastrados pelo console, com login por sessão.
- Auditoria de acessos (técnico, máquina, origem, início e fim), visível para administradores.
- O console mostra qual técnico está usando cada máquina.
- PostgreSQL como banco do relay, com migrations aplicadas automaticamente ao iniciar.
- Docker: imagem do relay, `compose.yaml` com banco e Cloudflare Tunnel opcional, e build do agente
  Windows sem instalar Zig (alvo `agent-windows`).
- Endpoint `/healthz` e flag `-healthcheck`.
- `RELAY_CLIENT_IP_HEADER`: a auditoria registra o IP real do técnico atrás do Cloudflare Tunnel.
- Testes de integração (relay, agente e Postgres reais) e E2E (Playwright).
- Duas edições do agente: `suporte-ti.exe` (só executa, sem admin) e `suporte-ti-servico.exe`
  (dois cliques instalam ou atualizam o serviço pelo UAC), com dois botões de download no console
  e a rota `/download/agent-servico.exe` ([ADR 0008](docs/adr/0008-duas-edicoes-do-agente.md)).
- Botão 🔒 **Bloquear tela** no Viewer (mensagem `lock` → `LockWorkStation`), só depois do consentimento.
- Modo serviço: tela de bloqueio e de login visíveis e controláveis, inclusive para logar com outra
  conta do Windows ([ADR 0007](docs/adr/0007-modo-servico-system.md)).
- Logs do serviço e do worker em `%ProgramData%\RemoteIT\service.log` e `agent.log`.
- Documentação no padrão do repositório: [docs/FLUXOS.md](docs/FLUXOS.md) com diagramas de todos
  os fluxos, [docs/AGENTE-WINDOWS.md](docs/AGENTE-WINDOWS.md), [docs/SEGURANCA.md](docs/SEGURANCA.md).

### Changed

- O pacote `internal/agent/media/h264` passou a se chamar `internal/agent/media/gpu` (tem H.264,
  AVC444 e AV1); o decodificador do visualizador (`rcdec.h`) entrega NV12 e a conversão de cor foi
  para `internal/viewer/decode/yuv.h`, testada no Linux.
- Console redesenhado: tema escuro estilo terminal (ciano, monoespaçada) e layout para celular
  ([docs/DESIGN.md](docs/DESIGN.md)). A tela de acesso remoto ganhou barra com telemetria legível
  (codec, resolução, fps, banda, RTT).
- **Incompatível:** o relay exige `DATABASE_URL`. O login do console deixa de ser HTTP Basic
  (qualquer usuário + `RELAY_ADMIN_PASS`) e passa a ser usuário e senha; `RELAY_ADMIN_PASS` agora é
  só a senha do admin inicial. Veja a migração em [docs/DEPLOY.md](docs/DEPLOY.md).
- Console reescrito em Svelte 5 + TypeScript, com abas de Usuários e Auditoria.
- Código reorganizado em pacotes por contexto em `internal/`.
- `build.sh` compila o console antes do relay e recria `dist/` a cada build, sem binários antigos.
- `install` copia o agente para `%ProgramFiles%\RemoteIT`, para e atualiza um serviço já existente
  em vez de só iniciá-lo, e pede o UAC quando falta permissão.
- Nenhuma caixa de mensagem no agente (instalação, remoção, instância repetida); o portátil sai em
  silêncio quando o serviço já está rodando.
- Barra do Viewer com todos os controles na mesma altura (32 px) e telemetria em chips.
- Bloquear/desbloquear a tela não relança mais o worker (o técnico não cai).
- Documentação renomeada para `docs/MAIUSCULAS.md` em português; ADRs em `docs/adr/`.

### Removed

- Título da janela de console com o nome do encoder (o `.exe` é GUI e não tem console).

### Fixed

- **Poucos quadros por segundo com internet lenta** (~14 fps com 140 ms de RTT): o agente deixava só
  2 quadros sem confirmação a caminho; agora a janela acompanha o RTT, de 2 a 8. O visualizador
  nativo confirma o quadro ao recebê-lo, antes de decodificar, como o navegador já fazia.
- O encoder de GPU esperava a saída com `Sleep(1)`, que no Windows dura ~15,6 ms sem
  `timeBeginPeriod`: duas a quatro esperas por quadro seguravam o vídeo em 15-30 fps. Agora usa um
  timer de alta resolução (~0,5 ms), e no 4:4:4 os dois encoders são esperados juntos.
- Console num navegador sem H.264 por hardware (máquina virtual, por exemplo): o decodificador era
  configurado com `prefer-hardware`, que o Chromium recusa sem GPU, e a tela nunca aparecia. Agora a
  aceleração vem da sondagem (GPU se houver, senão software); e depois de três falhas o console pede
  outro codec em vez de insistir.
- Um quadro de codec desconhecido no console não recebia `ack` e podia travar o controle de fluxo.
- A tela remota não preenchia o espaço: uma resolução menor que a janela ficava pequena no centro. Agora cresce mantendo a proporção.
- `NO_CONSENT=1` no `build.sh` gera o agente que transmite sem pedir confirmação (use com aviso/consentimento da empresa).
- Remover um usuário agora derruba a tela remota que ele tinha aberta; antes ele seguia no controle.
- O serviço Windows podia relançar o worker duas vezes ao mesmo tempo (evento de sessão + verificação periódica).
- Tela congelada ao bloquear no modo serviço: captura e entrada agora seguem o desktop ativo (`Winlogon`).
- Cliques e teclas ignorados na tela de bloqueio: o worker roda como SYSTEM (UIPI).
- "Tela protegida" para sempre: a captura recomeça numa thread nova quando a atual não consegue
  trocar de desktop.
- O download do agente podia vir de cache (Cloudflare/navegador) e entregar uma versão antiga.

### Security

- Uma máquina não consegue mais se passar por outra usando o mesmo nome de computador.
- Limite de senhas erradas no login (10 por usuário / 20 por IP em 15 min).
- Content-Security-Policy, `X-Frame-Options`, `nosniff` e `Referrer-Policy` em todas as respostas.
- Acessos deixados abertos por um reinício do relay são encerrados na partida.

- WebSocket do técnico só aceita a mesma origem do relay (proteção contra Cross-Site WebSocket Hijacking).
- Sessões guardam só o SHA-256 do token; login responde no mesmo tempo para usuário inexistente.
- Downloads do agente com `Cache-Control: no-store, private`: um cache compartilhado não guarda o
  `.exe`, que tem a chave do agente embutida.

### Other (dependências, ferramentas)

- Go 1.27.4, PostgreSQL 19, Node 26, Svelte 5.59, Vite 8.3, TypeScript 6.0.3, Playwright 1.65,
  pgx 5.13, go-mail 0.8.2, Three.js 0.187, Fontsource 5.3, testcontainers-go 0.45.
- TypeScript 7 ainda não é usado: o svelte-check 4.7 só suporta até o TypeScript 6.
