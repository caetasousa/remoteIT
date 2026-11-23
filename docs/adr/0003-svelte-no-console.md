# 0003 — Svelte no console, com o caminho dos quadros fora do framework

## Contexto

O console era um `index.html` de ~240 linhas, com lista de máquinas e visualizador. Com usuários e
auditoria ele ganha telas, formulários e estado de login, e um arquivo único fica difícil de manter.
A parte que pesa é o vídeo: até 60 quadros/s decodificados e desenhados em um canvas.

## Decisão

- **Svelte 5 + TypeScript + Vite.** Svelte compila para JavaScript sem Virtual DOM; o bundle final
  tem ~26 KB gzip.
- O **caminho de cada quadro** (WebSocket → `parseFrame` → `VideoDecoder`/`createImageBitmap` →
  canvas → ACK) e a captura de input ficam em **classes TypeScript puras** (`src/lib/viewer/`). O
  Svelte só recebe status e estatísticas, uma vez por segundo.
- O build vai para `internal/relay/web/dist` e é embutido no binário com `go:embed`: o relay
  continua sendo um único arquivo para distribuir.
- **TypeScript 6.0.3** em vez do 7: o `svelte-check` 4.7 ainda só suporta até o 6.

Considerado: React (o modelo de re-render brigaria com o canvas) e Vue (viável, runtime maior).

## Consequências

- Positivo: componentes separados (Login, AgentList, Viewer, Users, Audit), tipos espelhando o
  protocolo Go, hot reload no desenvolvimento.
- Positivo: desempenho do vídeo igual ao da versão sem framework, porque o framework não está no
  caminho dos quadros.
- Negativo: o build do relay depende de Node. Sem o build, o relay compila e serve uma página
  avisando para rodar `make frontend`.
