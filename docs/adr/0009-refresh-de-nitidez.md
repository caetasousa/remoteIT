# 0009 — Refresh de nitidez em PNG

## Contexto

Pelo navegador, a imagem perdia qualidade em relação a programas instalados. Eram três causas
diferentes:

- **Cor em meia resolução.** O agente converte a tela para NV12 antes do H.264. Nesse formato
  (4:2:0) o brilho tem resolução total, mas a cor só metade. Em vídeo ninguém nota; em tela de
  computador aparece como texto colorido borrado e franjas no ClearType. Programas instalados usam
  H.264 **4:4:4**, mas o `VideoDecoder` dos navegadores, na prática, só decodifica 4:2:0. Essa é a
  única das três causas que vem do navegador em si.
- **Tela parada não fica perfeita.** O encoder roda em baixa latência com bitrate controlado; as
  duas recodificações do quadro parado (`FlagRepeat`) melhoram o brilho, mas não recuperam a cor.
- **Interpolação no console.** O canvas era ampliado ou reduzido para caber no palco com um fator
  quebrado e centralizado por flex, às vezes em meio pixel. Com o Windows em 125% ou 150%, nem o
  "tamanho certo" em CSS px correspondia a pixels do monitor.

## Decisão

- **Refresh de nitidez:** 300 ms depois da tela parar, o agente faz uma captura GDI (no mesmo
  desktop do vídeo), comprime em **PNG** numa goroutine e manda uma vez, como quadro de codec 3. O
  navegador o desenha por cima do último quadro de vídeo. Ele é descartado se a tela mudar enquanto
  é comprimido (contador de geração nos dois lados), se não tiver o tamanho do vídeo ou se passar
  de 2 s de banda do alvo atual. Só vale nos presets de resolução nativa.
- **Exibição em pixels físicos:** o console calcula tamanho e margens do canvas em
  `CSS px × devicePixelRatio`, arredondados para inteiros, e ganha o modo **1:1 (Tamanho real)**.
  No modo ajustado, se sobra até 10% de espaço, usa 1:1 em vez de ampliar.
- **Bitrate menos nervoso:** só corta 25% com congestionamento real (≥ 10 esperas por ack no
  segundo); num soluço, mantém; e sobe 20% por segundo em vez de 10%.

## Alternativas descartadas

- **H.264 4:4:4 ou HEVC/AV1 4:4:4:** o navegador não decodifica de forma confiável.
- **Dois fluxos 4:2:0 combinados (truque do AVC444 da Microsoft):** dobra o custo do encoder e
  exige recompor as cores num shader no navegador, complexo demais para o ganho.
- **Refresh por regiões (só o que mudou):** mais eficiente em banda, mas exige saber os retângulos
  alterados. O DXGI fornece isso, só que dentro do código C do encoder. Fica como evolução se o
  refresh inteiro pesar.

## Consequências

- Positivo: com a tela parada a imagem fica **idêntica** à do PC, inclusive texto colorido. Ler um
  log ou uma planilha remota deixa de cansar a vista. O modo 1:1 elimina a interpolação do navegador.
- Negativo: um pico de banda a cada parada da tela (centenas de KB a alguns MB, conforme o fundo); a
  captura GDI pode não ver conteúdo protegido ou em overlay de hardware (vídeo DRM), e aí o refresh
  mostraria essa área preta até a tela mudar. A cada parada há uma captura GDI extra no PC remoto.
- Não resolvido: enquanto a tela **mexe**, a cor continua em 4:2:0. Isso só um programa instalado
  com 4:4:4 resolve.
