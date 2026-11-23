# Changelog

Todas as mudanças relevantes do projeto ficam registradas aqui.

O formato segue o [Keep a Changelog](https://keepachangelog.com/pt-BR/1.1.0/) e o projeto adota
[Versionamento Semântico](https://semver.org/lang/pt-BR/).

## [Unreleased]

### Added

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

- WebSocket do técnico só aceita a mesma origem do relay (proteção contra Cross-Site WebSocket Hijacking).
- Sessões guardam só o SHA-256 do token; login responde no mesmo tempo para usuário inexistente.
- Downloads do agente com `Cache-Control: no-store, private`: um cache compartilhado não guarda o
  `.exe`, que tem a chave do agente embutida.

### Other (dependências, ferramentas)

- Go 1.27.4, PostgreSQL 19, Node 26, Svelte 5.59, Vite 8.3, TypeScript 6.0.3, Playwright 1.65,
  pgx 5.13, go-mail 0.8.2, Three.js 0.187, Fontsource 5.3, testcontainers-go 0.45.
- TypeScript 7 ainda não é usado: o svelte-check 4.7 só suporta até o TypeScript 6.
