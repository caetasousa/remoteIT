# 0004 — WebSocket sobre TCP 443 em vez de WebRTC

## Contexto

As redes atendidas liberam só HTTPS de saída (443, às vezes 8080), muitas vezes com proxy
corporativo. As máquinas ficam atrás de NAT e não aceitam conexões de entrada.

## Decisão

Agente e técnico abrem **WebSocket de saída** para o relay, que faz a ponte. Vídeo vai em mensagens
binárias e controle em JSON, na mesma conexão. Para compensar o TCP, o agente faz **controle de
fluxo fim a fim**: só codifica um novo quadro quando há menos de 2 sem ACK do navegador.

WebRTC foi descartado: sem UDP, ele cairia em TURN sobre TCP (mesmo transporte, com ICE, DTLS, SRTP
e um servidor TURN a mais para operar).

## Consequências

- Positivo: passa em qualquer firewall ou proxy que libera HTTPS; uma única porta; o relay é só um pipe.
- Negativo: perda de pacote no TCP atrasa os quadros seguintes (head-of-line blocking). O controle de
  fluxo limita o atraso, mas não o elimina. Se UDP estiver disponível em algum cenário, WebRTC volta
  a ser candidato.

## Revisão 25/11/2026

Com o relay atrás de um Cloudflare Tunnel numa internet doméstica, o RTT de um quadro chega a ~140 ms,
e "menos de 2 sem ACK" limitava a transmissão a ~14 fps. A janela agora acompanha o **menor** RTT
visto: os quadros que cabem nele, mais um, de 2 a 8 (`media.FlowControl.Window`). A proteção contra
fila continua: um quadro sem ACK há mais de 3× o menor RTT segura o envio. O técnico confirma cada
quadro assim que ele chega, antes de decodificar, para o RTT medir só a rede.
