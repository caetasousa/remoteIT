# 🖥️ Visualizador do técnico (Windows)

> 🗣️ **Em palavras simples:** é um programa para o **técnico** (não para o cliente) abrir as máquinas sem passar pelo navegador. A imagem é decodificada direto pela placa de vídeo, chega com a **cor completa** (texto colorido nítido mesmo com a tela mexendo) e **todas as teclas** (Win, Alt+Tab, Ctrl+Esc) vão para o PC remoto sem precisar de tela cheia. O console no navegador continua existindo: use os dois.

| | 🌐 Console no navegador | 🖥️ Visualizador do técnico |
|---|---|---|
| Onde | qualquer computador, qualquer sistema | Windows |
| Instalar | nada | um `.exe`, sem admin |
| Decodificação | `VideoDecoder` do navegador | Media Foundation na GPU (DXVA) ou CPU |
| Cor com a tela em movimento | 4:2:0 (meia resolução; nítida só quando a tela para) | **4:4:4** (completa), na resolução nativa |
| AV1 (menos banda) | se o navegador decodifica AV1 na GPU | se o Windows tem a *Extensão de Vídeo AV1* |
| Win, Alt+Tab, Ctrl+Esc | só em tela cheia | sempre, com a janela em primeiro plano |
| Área de transferência → PC remoto | — | **Ações ▸ Enviar área de transferência** |
| Gestão (usuários, auditoria, links, remover máquina) | ✅ | ❌ (use o console) |
| Atualização | sempre a versão do site | automática, assinada ([ADR 0011](adr/0011-atualizacao-automatica.md)) |

---

## ⬇️ Instalar

1. No console, em **Máquinas**, clique em **Visualizador do técnico (Windows)**.
2. Guarde o `remoteit-tecnico.exe` onde quiser (ex.: Área de Trabalho) e dê dois cliques. Se o SmartScreen aparecer: *Mais informações* → *Executar assim mesmo*.
3. Entre com o mesmo usuário e senha do console. O endereço do relay já vem preenchido.

O login fica guardado (protegido pelo Windows, só para o seu usuário) em `%LOCALAPPDATA%\RemoteIT\viewer.token`. **Sair** apaga.

## 🧭 Usar

* **Lista:** as suas máquinas (admins veem todas), atualizada a cada 3 s, online primeiro. Cada linha tem um ponto de estado (🟢 online, 🟡 em uso por outro técnico, ⚪ offline), o nome e, embaixo, o usuário logado no PC, o dono (para admins) e a versão do agente. **Buscar** filtra por nome, dono ou usuário. Dois cliques, **Enter** ou **Conectar** abrem uma janela por máquina.
* **Rodapé:** a versão do visualizador e o que **este PC** decodifica (H.264, 4:4:4, AV1).
* **Barra de telemetria** (embaixo da sessão): codec em uso (em ciano quando é **4:4:4**), decodificação na GPU ou CPU, resolução, **FPS desenhados aqui**, banda e RTT (verde até 60 ms, âmbar até 150 ms, rosa acima). À direita, em âmbar, o motivo quando a **Cor 4:4:4** está ligada mas não está em uso. Some em tela cheia; liga e desliga em **Exibir ▸ Barra de telemetria**.

| Menu | O que faz |
|---|---|
| **Qualidade** | Ultra-rápido, Equilibrado, Nítido (padrão), Máximo — troca sem reconectar |
| **Qualidade ▸ Cor 4:4:4** | ligada (padrão): pede a cor completa. Vale nos presets de resolução nativa (Nítido e Máximo) e quando o PC remoto tem encoder H.264 na placa de vídeo; senão o agente manda o vídeo comum sozinho. Desligue para economizar banda numa conexão fraca |
| **Exibir ▸ Tamanho real (1:1)** | um pixel remoto por pixel do seu monitor (sem borrar) |
| **Exibir ▸ Tela cheia** | também por **Ctrl+Alt+F** (esse atalho não vai para o PC remoto) |
| **Ações ▸ Enviar Ctrl+Shift+Esc** | abre o Gerenciador de Tarefas remoto |
| **Ações ▸ Enviar área de transferência** | põe o texto copiado aqui na área de transferência do PC remoto; cole lá com **Ctrl+V** |
| **Ações ▸ Bloquear a tela remota** | como Win+L |
| **Teclado ▸ Enviar Win, Alt+Tab…** | ligado: todas as teclas vão para o PC remoto; desligado: Win e Alt+Tab ficam no seu Windows |
| **Ajuda ▸ Diagnóstico de vídeo** | versão, o que este PC decodifica, codecs pedidos ao agente, codec e encoder em uso no PC remoto, FPS desenhados e enviados, banda e RTT. Copie isso ao relatar um problema de imagem |

> ⚠️ Com o **Teclado** ligado e a janela da sessão em primeiro plano, Alt+Tab **não** troca de janela no seu computador. Clique em outra janela, use a barra de tarefas ou desligue a opção.

## 🩺 Problemas

| Sintoma | O que fazer |
|---|---|
| Barra mostra "Decodifica CPU" | o Windows não ofereceu DXVA para esta placa: decodifica na CPU (funciona, gasta mais) |
| FPS baixo e RTT alto (âmbar ou rosa) | é a rede até o relay: o agente já deixa mais quadros a caminho quando o RTT sobe (até 8), mas o atraso em si só cai com um relay mais perto ([FLUXOS](FLUXOS.md#fluxo)) |
| Imagem em JPEG (título mostra "JPEG") | o decodificador H.264 do Windows não abriu (ex.: Windows N sem o *Media Feature Pack*) |
| Barra mostra "H.264", não "H.264 4:4:4" (o motivo aparece à direita) | preset com resolução reduzida, **Cor 4:4:4** desligada, ou o PC remoto não tem encoder de GPU (ou não abriu o segundo): o motivo fica no `agent.log` do PC remoto |
| Nunca usa AV1 | normal na maioria dos PCs: precisa de encoder AV1 na placa do PC remoto **e** da *Extensão de Vídeo AV1* (Microsoft Store) neste Windows |
| "Sua sessão expirou" | entre de novo; a sessão do relay dura 12 h |
| Tecla presa no PC remoto | troque de janela e volte: o visualizador solta todas as teclas ao perder o foco |
| Log | `%LOCALAPPDATA%\RemoteIT\viewer.log` (recomeça a cada abertura) |

## 🔬 Por dentro

* Código: [`cmd/viewer`](../cmd/viewer), [`internal/viewer/client`](../internal/viewer/client) (API, protocolo e lista de codecs, testado no Linux), [`internal/viewer/ui`](../internal/viewer/ui) (o que as janelas mostram: linhas da lista, barra de telemetria, aviso do 4:4:4, mensagens de erro e diagnóstico; testado no Linux), [`internal/viewer/decode`](../internal/viewer/decode) (H.264, AVC444 e AV1 via Media Foundation, `rcdec.h`; recomposição 4:4:4 em `yuv.h`), [`internal/viewer/win`](../internal/viewer/win) (Win32).
* **Como chega o 4:4:4:** o agente manda duas imagens H.264 comuns por quadro (a principal e a auxiliar, que leva a cor que a principal descarta). O visualizador decodifica as duas com o Media Foundation e junta a cor pixel a pixel. Se o decodificador falhar três vezes seguidas, o visualizador desiste do codec e o agente troca para o próximo da lista.
* Decisões: [ADR 0012](adr/0012-visualizador-nativo.md) (o visualizador) e [ADR 0013](adr/0013-codecs-e-negociacao.md) (codecs e 4:4:4).
