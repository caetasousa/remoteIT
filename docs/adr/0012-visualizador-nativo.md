# 0012 — Visualizador nativo para Windows, junto com o console

## Contexto

No navegador, o vídeo passa pelo `VideoDecoder` e pela composição do Chromium, e o sistema do técnico
toma para si Win, Alt+Tab e afins (a Keyboard Lock API só ajuda em tela cheia). O usuário pediu um
programa instalado para o técnico, **usado junto** com o console no navegador (que continua sendo a
porta de entrada para gestão, auditoria e acesso de qualquer lugar).

## Decisão

- **Go nativo, Win32 puro** (`cmd/viewer` → `remoteit-tecnico.exe`), sem runtime a instalar. Escolhido
  no lugar de Tauri/WebView2 porque este reaproveitaria o decodificador do Chromium e não abriria
  caminho para cor 4:4:4.
- **Camadas:** `internal/viewer/client` (API e protocolo, Go puro, testado no Linux contra relay e
  agente reais), `internal/viewer/decode` (H.264 pelo Media Foundation do Windows em C via cgo — mesmo
  padrão do encoder do agente —, com DXVA na GPU quando possível e CPU como reserva; JPEG/PNG pela
  biblioteca padrão), `internal/viewer/win` (janelas, GDI, mouse, teclado).
- **Mesma conta e mesmo protocolo** do console: login em `/api/login` (cookie de sessão, guardado com
  DPAPI do usuário), lista em `/api/agents`, tela em `/ws/viewer`. Sem rotas novas para a sessão.
- **Teclado por hook de baixo nível:** com a janela da sessão em primeiro plano, todas as teclas
  (inclusive Win, Alt+Tab, Ctrl+Esc) vão para o PC remoto, sem tela cheia; Ctrl+Alt+F alterna a tela
  cheia localmente. Teclas mortas são compostas no layout local, como no navegador. A tabela de teclas
  passou para `internal/protocol` (`KeyToVK` / `CodeForVK`), compartilhada com o agente.
- **Área de transferência:** mensagem nova `clip` (técnico → PC), aplicada só com consentimento; o
  técnico cola com Ctrl+V no PC remoto.
- **Atualização automática** como a do agente ([ADR 0011](0011-atualizacao-automatica.md)), edição
  `tecnico` no `update.json`, autenticada pela sessão do técnico (`/api/viewer/update`).

## Consequências

- Positivo: todas as teclas sem tela cheia; desenho direto, sem a composição do navegador; base para
  4:4:4, vários monitores e transferência de arquivos.
- Negativo: mais um programa para manter, e a parte de janela/decodificação só é exercitada no
  Windows (aqui ela é compilada e passa por um teste de fumaça no Wine; o H.264 por GPU, o hook de
  teclado e o DPAPI precisam de teste manual).
- **Cor 4:4:4 ainda não:** esta versão decodifica o mesmo H.264 4:2:0 do navegador. A segunda etapa
  exige mudar o encoder do agente.
- A captura do teclado inteiro significa que, com a sessão em primeiro plano, Alt+Tab não troca de
  janela no PC do técnico: desligue em **Teclado** ou clique fora da janela.
