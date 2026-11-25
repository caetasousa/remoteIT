# 0010 — Máquinas por técnico, com credencial por máquina

## Contexto

Até aqui o relay tinha um modelo de acesso plano:

- **qualquer técnico logado via e acessava todas as máquinas**; um técnico de um cliente abria a
  tela de um PC de outro cliente;
- o `.exe` era o mesmo para todo mundo, com **uma chave compartilhada** embutida, então o relay não
  sabia quem tinha distribuído cada agente;
- o ID da máquina era **o nome do computador**, e uma conexão nova com o mesmo ID derrubava a
  anterior. Dois PCs com o mesmo nome se derrubavam, e quem tivesse um `.exe` podia se registrar
  com o nome de outra máquina, derrubar a verdadeira e esperar o técnico digitar uma senha numa
  tela falsa.

Decidido com o usuário: **só o dono** (sem compartilhamento, sem organizações e equipes) e
**sem certificado de assinatura de código**.

## Decisão

- **O `.exe` sai marcado com um código do técnico.** O agente reserva um espaço fixo no binário
  (`protocol.EnrollMarker` + 40 caracteres); no download, o relay grava ali um código de cadastro
  novo (`internal/relay/agentbin`), sem recompilar. Códigos de download valem 30 dias e até 50
  máquinas (o técnico leva o mesmo arquivo a vários PCs).
- **Na primeira execução, o agente troca o código por uma credencial da máquina**
  (`POST /api/agent/enroll`): um ID aleatório (`m_` + base32) e um segredo de 256 bits. O banco guarda
  o SHA-256 do segredo; o agente o guarda protegido pelo DPAPI do Windows
  (`%ProgramData%\RemoteIT\machine.json` no serviço, `%LOCALAPPDATA%\RemoteIT` no portátil).
  O nome do computador vira só nome de exibição.
- **O dono da máquina é quem gerou o código.** Acessam e gerenciam (remover): o dono e os
  administradores. O relay
  responde 404 tanto para "não existe" quanto para "não é sua".
- **Remover revoga:** a credencial passa a receber 403 e o agente não se cadastra de novo sozinho.
  Um 401 (credencial desconhecida, por exemplo banco recriado) faz o agente se cadastrar de novo,
  se ainda tiver um código válido.
- **Links de instalação** (`/i/<código>`): o técnico gera no console, com validade (1 hora a 7 dias,
  ou **sem prazo**) e número de máquinas (1 a 100, ou **sem limite**); quem recebe baixa o `.exe` já
  marcado, sem conta. Um link permanente é o "link do técnico" que ele manda a todos os clientes;
  revogá-lo não afeta as máquinas já cadastradas. O código só é
  mostrado na criação.
- **Migração:** agentes antigos (só a chave) continuam aceitos como **legados**, visíveis só para
  administradores. `RELAY_LEGACY_AGENTS=false` os recusa quando todas
  as máquinas tiverem sido reinstaladas. Um legado nunca pode usar um ID `m_...`.

Alternativas para marcar o `.exe` descartadas: código no nome do arquivo (o navegador renomeia),
código só digitado (o portátil não tem janela) e área não assinada do Authenticode (depende de
certificado).

## Consequências

- Positivo: um técnico não vê nem abre máquinas alheias; nenhuma máquina se passa por outra; cada
  máquina pode ser removida sozinha; o cliente instala por um link, sem conta.
- Negativo: cada download gera um `.exe` com hash diferente (conferir hash publicado deixa de fazer
  sentido para o arquivo baixado); o relay lê o `.exe` inteiro (~7 MB) a cada download; o portátil e
  o serviço no mesmo PC são duas máquinas no console.
- Enquanto `RELAY_LEGACY_AGENTS` estiver ligado, a falha antiga (se passar por uma máquina legada)
  continua possível para as máquinas legadas.

## Revisão (25/11/2026): sem compartilhamento, links permanentes

A primeira versão desta decisão tinha **compartilhamento** (o dono dava acesso a outro técnico). Na
prática, cada técnico atende os próprios clientes pelo próprio link, e o compartilhamento só somava
tela e regra. Foi retirado (migration `0004_no_shares`). Pelo mesmo motivo, o link de instalação
ganhou as opções **sem prazo** e **sem limite**: o técnico manda o mesmo link a qualquer cliente,
quando quiser. Quem obtiver um link permanente só consegue cadastrar máquinas **novas** no nome do
técnico (que as vê e pode removê-las); não ganha acesso a nenhuma máquina existente.

