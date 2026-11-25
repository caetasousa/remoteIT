# 0013 — Codecs: H.264 de base, AVC444 para cor 4:4:4 e AV1 opcional, por negociação

## Contexto

Até aqui o agente tinha um codec de vídeo só, o H.264 4:2:0 do Media Foundation, e o técnico dizia
apenas "decodifico H.264, sim ou não" (`hello {h264: bool}`). Faltavam duas coisas:

- **Cor com a tela em movimento.** O 4:2:0 guarda a cor em meia resolução nas duas direções. Em tela
  de computador, isso borra texto colorido: código com destaque de sintaxe, planilha, link, mensagem
  de erro em vermelho. O [ADR 0009](0009-refresh-de-nitidez.md) resolveu a tela **parada** com um PNG
  sem perda, mas com a tela **mexendo** a cor continuava pela metade.
- **Banda.** O AV1 tem ferramentas para conteúdo de tela (paleta, cópia intra-bloco) e gasta bem
  menos bits com texto. Só que o encoder AV1 de hardware só existe em placas de 2022 em diante.

Antes de decidir, comparamos as opções pensando no **PC atendido**, que é qualquer PC de escritório,
muitas vezes antigo e com vídeo integrado Intel:

| Opção | Problema para o RemoteIT |
|---|---|
| H.264 4:4:4 direto | nenhum navegador decodifica de forma confiável. O decodificador H.264 do Windows vai só até o perfil High (4:2:0), e os encoders de GPU expostos pelo Media Foundation também são 4:2:0 |
| x264 com 4:4:4 | só CPU (pesa no PC atendido) e licença GPL para distribuir no agente |
| NVENC SDK com 4:4:4 | só placas NVIDIA |
| HEVC | o navegador decodifica de forma irregular (o Firefox não decodifica) e as patentes são confusas |
| VP9 | encoder de hardware raro no Windows, e em software é pesado |
| FFmpeg / OpenH264 | binário grande, mais cgo, sem ganho sobre o que o Windows já oferece |

## Decisão

1. **H.264 do Media Foundation continua sendo a base.** É o único codec que funciona em qualquer PC
   atendido e em qualquer navegador.
2. **Negociação de codecs.** O `hello` passa a levar `codecs`, a lista do que o técnico decodifica
   **em ordem de preferência** (`avc444`, `av1`, `h264`, `jpeg`). O agente percorre a lista e abre o
   primeiro codec que conseguir; o que falhar (na abertura ou no meio da sessão) fica marcado e não é
   tentado de novo naquela sessão. JPEG fecha sempre a lista. Um hello novo reabre a captura: é assim
   que o técnico troca de codec ou desiste de um que o decodificador dele não aguentou. O campo
   `h264` continua no hello para agentes antigos, e um hello sem `codecs` é lido como antes
   (`protocol.Message.Accepts`).
3. **AVC444 para a cor 4:4:4, no visualizador nativo** (o mesmo truque do RDP da Microsoft). O agente
   codifica **duas** imagens 4:2:0 comuns:
   - **principal:** brilho inteiro e cor dos pixels (par, par), ou seja, um H.264 normal;
   - **auxiliar:** as amostras de cor que a principal deixou de fora (linhas ímpares de U e V no
     plano Y; colunas ímpares das linhas pares no plano UV).

   Juntas, as duas têm todas as amostras de cor. O arranjo está em `internal/avc444`, que é a
   referência: o shader do agente e a recomposição em C do visualizador seguem esse código, e os
   testes comparam os dois com ele. No agente, shaders D3D11 escrevem direto nos planos NV12 (visões
   R8 e R8G8, como o OBS faz) e dois encoders H.264 de GPU trabalham ao mesmo tempo, com 60% do
   bitrate para a principal. O quadro vai com o codec 4 e um payload `flags | tamanho | principal |
   auxiliar`. A cor usa BT.709 em **faixa completa** (0-255), porque as duas pontas são nossas.
   - Só na **resolução nativa** (com redução de resolução a nitidez já se perde) e só com **encoder
     de GPU** (em software, seriam dois encoders pesando no PC atendido).
   - Funciona com o encoder H.264 que todo PC já tem: nada a instalar e nenhuma licença nova.
   - Fica no visualizador nativo, ligado por padrão em **Qualidade → Cor 4:4:4**. O navegador não
     tem como recompor as duas visões com o `VideoDecoder`.
4. **AV1 opcional.** O agente usa o encoder AV1 de GPU do Media Foundation quando a placa tem
   (NVIDIA RTX 40+, Intel Arc / Core Ultra, AMD RX 7000+), sem reserva em software. O técnico só
   oferece AV1 quando decodifica **na GPU**: no navegador, `isConfigSupported` com
   `prefer-hardware`; no visualizador nativo, o decodificador AV1 do Windows (Extensão de Vídeo AV1
   da Microsoft Store). Se o encoder só guarda o cabeçalho de sequência no tipo de mídia, o agente o
   repõe em cada keyframe (`gpu.WithSequenceHeader`). Ordem no navegador: AV1, H.264. No visualizador
   nativo: 4:4:4, AV1, H.264.
5. **O técnico desiste sozinho de um codec que não funciona.** Três falhas seguidas do decodificador
   (ou, no AV1 do navegador, 120 quadros entregues sem sair imagem) tiram o codec do hello, e o agente
   troca para o próximo. Uma falha isolada só reinicia o decodificador e pede keyframe.
6. **WebRTC e WebTransport continuam fora** ([ADR 0004](0004-websocket-sobre-tcp.md)): sem UDP nas
   redes atendidas, não mudam nada.

## Medição

Uma captura 1280x720 com texto colorido (editor de código em fundo escuro e planilha com valores em
cores), codificada como keyframe com x264 em vários QPs. O 4:2:0 é o de hoje: cor pela média de cada
bloco 2x2, repetida na volta (como o visualizador nativo faz). PSNR em RGB:

| QP | 4:2:0: bytes | 4:2:0: dB | AVC444: bytes (principal + auxiliar) | AVC444: dB |
|---:|---:|---:|---:|---:|
| 18 | 106.720 | 31,0 | 220.479 (110.847 + 109.632) | 50,7 |
| 24 | 82.264 | 30,9 | 169.523 (86.781 + 82.742) | 45,0 |
| 30 | 60.156 | 30,5 | 120.687 (64.514 + 56.173) | 39,2 |
| 36 | 41.079 | 29,5 | 77.912 (45.307 + 32.605) | 34,1 |

- O 4:2:0 fica preso em ~31 dB em **qualquer** bitrate: o teto é a cor descartada, não a compressão.
  Sem compressão nenhuma, o 4:2:0 dá 31,1 dB e o AVC444, 65 dB.
- Com bytes parecidos, o AVC444 ganha com folga: 78 KB a 34,1 dB contra 82 KB a 30,9 dB.
- Num keyframe, a auxiliar custa quase o mesmo que a principal. Nos quadros seguintes de uma tela de
  escritório, as duas visões mudam pouco e custam pouco.

## Alternativas descartadas

- **Trocar o H.264 por AV1 ou HEVC:** deixaria sem vídeo rápido os PCs sem encoder novo, que são a
  maioria dos atendidos.
- **AVC444 com um encoder só, alternando as visões quadro a quadro** (como o RDP pode fazer): exige
  controlar as referências de longo prazo de cada visão, o que o Media Foundation não expõe de forma
  portável entre NVIDIA, Intel e AMD.
- **AVC444 em CPU** (conversão e dois encoders software): pesaria no PC atendido, o oposto do que o
  agente promete.
- **AVC444 no navegador:** exigiria dois `VideoDecoder`, copiar os planos para a CPU (`copyTo`) e
  recompor num shader WebGL. É possível, mas a cópia anula o ganho de decodificar na GPU. O console
  fica com o refresh PNG do ADR 0009.

## Consequências

- Positivo: no visualizador nativo, texto colorido fica nítido também com a tela em movimento, nos
  PCs com encoder H.264 de GPU (praticamente todos). PCs com GPU nova gastam menos banda com AV1. E
  um codec que falha na máquina de um cliente não derruba a sessão: o agente e o técnico caem para o
  próximo sozinhos.
- Negativo: o AVC444 dobra o trabalho do encoder de GPU (duas sessões de codificação por quadro) e,
  num keyframe, quase dobra os bytes. Placas NVIDIA de consumo limitam as sessões simultâneas de
  NVENC; se a segunda não abrir, o agente cai para H.264 4:2:0.
- **Testado aqui, sem GPU:** o arranjo das visões (Go), a recomposição em C (byte a byte contra o
  Go), os **shaders HLSL** contra a referência (Direct3D 11 do Wine, com o compilador de shaders do
  Wine), a negociação e o fallback (unitários e integração com agente real), e a decodificação AV1 do
  navegador (Chromium com fluxos do SVT-AV1 em 720p e 4K, usando as codec strings do console).
- **Só num PC Windows real:** desenhar direto nos planos NV12, os dois encoders H.264 em paralelo,
  o encoder AV1 do Media Foundation (formato da saída e cabeçalho de sequência) e os decodificadores
  do Windows. Ficam no checklist manual de [../TESTES.md](../TESTES.md). Se algo falhar ali, o
  fallback leva a sessão para H.264 4:2:0, e o motivo fica em `agent.log`.
