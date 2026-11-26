# Changelog

Todas as mudanças relevantes do projeto ficam registradas aqui.

O formato segue o [Keep a Changelog](https://keepachangelog.com/pt-BR/1.1.0/) e o projeto adota
[Versionamento Semântico](https://semver.org/lang/pt-BR/).

## [Unreleased]

### Added

- **Rota local** ([ADR 0014](docs/DECISOES.md#adr-0014)): com `RELAY_LOCAL_URL` (ex.:
  `http://localhost:3000`), o visualizador do técnico abre a tela direto pelo endereço do PC do
  relay, depois de conferir em `/api/relay` (sem cookie) que é o mesmo processo. Com o relay em casa
  atrás do túnel, isso tira ~90 ms medidos de cada ida e volta. Rotas `GET /api/relay` (pública) e
  `GET /api/viewer/route` (logada).
- **Vídeo do tamanho da janela**: o `cfg` leva a área de exibição (`vw`, `vh`, em pixels físicos);
  o agente reduz a tela na GPU pela média da área (`internal/scale`, o filtro certo para texto) e só
  reabre o encoder se o tamanho de saída mudar. No "Tamanho real", resolução nativa.
- **Áreas sem perda por blocos**: o encoder expõe os blocos de 64 px que o DXGI diz que mudaram.
  Cada bloco parado há 300 ms ganha um retalho PNG exato (codec 6), e a mensagem `rg` diz, antes de
  cada quadro, onde o vídeo vale; fora dela o técnico mantém os pixels exatos.
- **Desenho na GPU no visualizador** (`internal/viewer/render`, Direct3D 11): a saída do DXVA fica
  na GPU, shaders convertem a cor e recompõem o 4:4:4, composição por região e ajuste à janela
  (média da área ao reduzir). Sem Direct3D 11 que leia NV12 em shader, GDI como antes.
- **Cor 4:4:4 no console**: com WebGL2 e H.264 em software, as duas visões do AVC444 são
  decodificadas e a cor recomposta em shader (botão **4:4:4**). Se o PC do técnico não der conta,
  o 4:4:4 sai sozinho.
- **Telemetria por etapa**: o `stats` traz o RTT mínimo (dá a fila) e o tempo de captura +
  codificação; o visualizador e o console mostram Fila, Codif., Decod. e Rota.

- **Instalar sem navegador** (`irm https://…/i/<código>/ps | iex`): o console mostra o comando junto
  com o link de instalação. O script baixa o `.exe` do link, confere o SHA-256 calculado pelo relay e
  instala (serviço com um UAC, ou portátil em `%LOCALAPPDATA%\RemoteIT`), com o resultado no
  terminal. Rota pública `GET /i/{código}/ps`, com o mesmo limite por IP do link.
- **Visualizador do técnico com interface nova**: cabeçalho com usuário e papel, busca, lista com
  estado colorido, usuário logado, dono e versão do agente, rodapé com o que o PC decodifica
  (H.264, 4:4:4, AV1); na sessão, **barra de telemetria** (codec, GPU/CPU, resolução, FPS
  desenhados, banda e RTT colorido) com o motivo de o 4:4:4 não estar em uso, avisos num cartão
  central e **Ajuda ▸ Diagnóstico de vídeo**. Erros de login em frases claras. Regras em
  `internal/viewer/ui`, testadas no Linux.
- **Negociação de codecs** ([ADR 0013](docs/DECISOES.md#adr-0013)): o `hello` leva
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
- **Cada técnico com as suas máquinas** ([ADR 0010](docs/DECISOES.md#adr-0010)): o `.exe`
  baixado sai marcado com um código do técnico; na primeira execução a máquina troca o código por
  uma credencial própria (ID aleatório, DPAPI) e fica no nome dele. Só o dono e os admins veem e
  acessam; **Remover** revoga a credencial. Não há compartilhamento entre técnicos.
- **Link de instalação** (`/i/<código>`): o cliente baixa o agente sem conta; validade de 1 hora a
  7 dias ou **sem prazo**, de 1 a 100 máquinas ou **sem limite**, revogável.
- **Visualizador do técnico para Windows** (`remoteit-tecnico.exe`, [ADR 0012](docs/DECISOES.md#adr-0012),
  [guia](docs/WINDOWS.md#visualizador)): login com a mesma conta, lista de máquinas, uma janela por sessão,
  H.264 pelo Media Foundation (GPU quando possível), todas as teclas sem tela cheia, área de
  transferência para o PC remoto (mensagem `clip`), atualização automática. O console continua.
- **Atualização automática do agente** ([ADR 0011](docs/DECISOES.md#adr-0011)): versão
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
  e a rota `/download/agent-servico.exe` ([ADR 0008](docs/DECISOES.md#adr-0008)).
- Botão 🔒 **Bloquear tela** no Viewer (mensagem `lock` → `LockWorkStation`), só depois do consentimento.
- Modo serviço: tela de bloqueio e de login visíveis e controláveis, inclusive para logar com outra
  conta do Windows ([ADR 0007](docs/DECISOES.md#adr-0007)).
- Logs do serviço e do worker em `%ProgramData%\RemoteIT\service.log` e `agent.log`.
- Documentação no padrão do repositório: [docs/FLUXOS.md](docs/FLUXOS.md) com diagramas de todos
  os fluxos, [docs/WINDOWS.md](docs/WINDOWS.md#agente), [docs/SEGURANCA.md](docs/SEGURANCA.md).

### Changed

- **Controle de fluxo e bitrate pela fila**: um quadro pode esperar o RTT mínimo + 50 ms (era 3×,
  ~420 ms com o relay atrás do túnel); o bitrate cai com fila acima de 80 ms e só sobe quando o alvo
  foi usado; pisos de 1 a 5 Mbps (o padrão não descia de 15) e início moderado; o RTT mínimo é o
  menor da janela de 10 s.
- **Encoder**: sem keyframe periódico (era 1 por segundo, ~300× um quadro comum numa tela de
  escritório); buffer do controle de taxa de 4 quadros (era 1 s inteiro: a unidade é bytes);
  qualidade × velocidade começa na qualidade e baixa sozinha se o encoder não der conta (o
  comentário dizia o contrário do que o valor fazia).
- Mexer só o mouse não gera mais quadro de vídeo (a posição do cursor vai à parte): sobra banda e
  vaga no controle de fluxo, e o refresh sem perda não é mais adiado pelo mouse.
- O console WebGL2 (ou 2D) aplica vídeo e retalhos numa fila em ordem, e passa o espaço de cor
  BT.709 explícito ao decodificador.
- A fonte JPEG reduz pela média da área (era vizinho mais próximo).
- **Documentação reorganizada** de 28 para 10 arquivos `.md`, sem perder conteúdo: os 13 ADRs viraram
  [docs/DECISOES.md](docs/DECISOES.md) (com índice, ligações entre as decisões e as revisões
  incorporadas ao texto); protocolo e API entraram em [docs/ARQUITETURA.md](docs/ARQUITETURA.md);
  agente e visualizador no Windows, em [docs/WINDOWS.md](docs/WINDOWS.md); testes e design do
  console, no [CONTRIBUTING.md](CONTRIBUTING.md); o levantamento de melhorias virou
  [docs/ROADMAP.md](docs/ROADMAP.md), separando o que foi feito do que falta.
- Downloads pelo navegador vêm em **`.zip`** (o `.exe` e um `LEIA-ME.txt`): os botões do console
  (`/download/agent.zip`, `agent-servico.zip`, `viewer.zip`) e o link de instalação `/i/<código>`.
  O `.exe` solto continua em `/download/*.exe` e `/i/<código>/exe` (curl, atualização automática e o
  comando do PowerShell).
- O console não usa mais `confirm()` do navegador: remover máquina ou usuário, aprovar conta, revogar
  link e bloquear a tela remota pedem confirmação num modal no tema do console (Esc cancela; nas
  ações destrutivas o foco começa em "Cancelar").
- O pacote `internal/agent/media/h264` passou a se chamar `internal/agent/media/gpu` (tem H.264,
  AVC444 e AV1); o decodificador do visualizador (`rcdec.h`) entrega NV12 e a conversão de cor foi
  para `internal/viewer/decode/yuv.h`, testada no Linux.
- Console redesenhado: tema escuro estilo terminal (ciano, monoespaçada) e layout para celular
  ([CONTRIBUTING.md](CONTRIBUTING.md#design)). A tela de acesso remoto ganhou barra com telemetria legível
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

- Console: com o nome comprido do encoder da Intel e os chips novos, a telemetria da tela remota
  quebrava para uma segunda linha, atrás da tela, e sumia inteira (inclusive Codec, Resolução,
  FPS, Banda e RTT). Agora a barra é uma linha só a partir de 1100 px, o Codec mostra o nome
  curto e, faltando espaço, somem primeiro os chips menos importantes.

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
