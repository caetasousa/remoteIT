# 0007 — Worker do serviço como SYSTEM, seguindo o desktop ativo

## Contexto

No modo serviço, o processo do serviço roda na sessão 0, que nunca vê a tela, e lança uma cópia do
agente (o worker) na sessão do console. A primeira versão lançava o worker com o **token do usuário
logado** e com a captura e a entrada presas ao desktop `Default`. Isso falhou na tela de bloqueio:

1. **Tela congelada:** a tela de bloqueio e a de login vivem no desktop `Winlogon`. GDI, DXGI e
   `SendInput` só alcançam o desktop da thread que os chama.
2. **Cliques e teclas ignorados:** mesmo trocando de desktop, um processo de integridade média (o
   token do usuário) não injeta entrada na LogonUI, que roda como SYSTEM (UIPI).
3. **"Tela protegida" para sempre:** `SetThreadDesktop` falha numa thread que já tem janelas, e o
   Media Foundation cria janelas ocultas na thread da captura.
4. **Sem worker antes do login:** sem usuário logado, `WTSQueryUserToken` não devolve token.

## Decisão

- O serviço copia o **próprio token (LocalSystem)**, muda a sessão dele com
  `SetTokenInformation(TokenSessionId)` e lança o worker com `CreateProcessAsUser`. O ambiente
  (`USERNAME`, `USERPROFILE`) vem do usuário logado, quando há um. É o arranjo usado por
  ferramentas comerciais de acesso remoto.
- O pacote `internal/agent/desktop` segue o desktop que recebe entrada (`OpenInputDesktop` +
  `SetThreadDesktop`), guardando um handle por thread do SO.
- A captura roda numa thread do SO **descartável** (`LockOSThread` sem `Unlock`): se a troca de
  desktop falhar e a captura não abrir, a thread morre e o laço recomeça numa nova.
- A entrada é injetada numa **thread fixa**, alimentada por um canal, que confere o desktop ativo no
  máximo uma vez por segundo e tenta de novo quando o `SendInput` recusa.
- Bloquear e desbloquear não relançam mais o worker (só login e logout), para não derrubar o
  técnico justo na tela de bloqueio.
- Worker e serviço gravam log em `%ProgramData%\RemoteIT\`, porque não têm console.

## Consequências

- Positivo: tela de bloqueio, login e UAC visíveis e controláveis; acesso antes de qualquer login;
  diagnóstico possível pelo log.
- Negativo: o worker roda com o privilégio máximo da máquina, então uma falha nele é uma falha como
  SYSTEM. O worker só aceita entrada depois do consentimento e só fala com o relay autenticado, mas
  a superfície de ataque é maior do que no modo portátil (veja [SEGURANCA.md](../SEGURANCA.md)).
- Ainda não resolve Ctrl+Alt+Del (exigiria `SendSAS` e política de grupo).
