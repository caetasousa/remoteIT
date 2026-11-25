# 🖥️ Visualizador do técnico (Windows)

> 🗣️ **Em palavras simples:** é um programa para o **técnico** (não para o cliente) abrir as máquinas sem passar pelo navegador. A imagem é decodificada direto pela placa de vídeo e **todas as teclas** (Win, Alt+Tab, Ctrl+Esc) vão para o PC remoto sem precisar de tela cheia. O console no navegador continua existindo: use os dois.

| | 🌐 Console no navegador | 🖥️ Visualizador do técnico |
|---|---|---|
| Onde | qualquer computador, qualquer sistema | Windows |
| Instalar | nada | um `.exe`, sem admin |
| Decodificação | `VideoDecoder` do navegador | Media Foundation na GPU (DXVA) ou CPU |
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

* **Lista:** as suas máquinas (admins veem todas), atualizada a cada 3 s. Dois cliques ou **Conectar** abrem uma janela por máquina.
* **Título da janela:** codec, resolução, FPS, banda, RTT e se a decodificação está na GPU.

| Menu | O que faz |
|---|---|
| **Qualidade** | Ultra-rápido, Equilibrado, Nítido (padrão), Máximo — troca sem reconectar |
| **Exibir ▸ Tamanho real (1:1)** | um pixel remoto por pixel do seu monitor (sem borrar) |
| **Exibir ▸ Tela cheia** | também por **Ctrl+Alt+F** (esse atalho não vai para o PC remoto) |
| **Ações ▸ Enviar Ctrl+Shift+Esc** | abre o Gerenciador de Tarefas remoto |
| **Ações ▸ Enviar área de transferência** | põe o texto copiado aqui na área de transferência do PC remoto; cole lá com **Ctrl+V** |
| **Ações ▸ Bloquear a tela remota** | como Win+L |
| **Teclado ▸ Enviar Win, Alt+Tab…** | ligado: todas as teclas vão para o PC remoto; desligado: Win e Alt+Tab ficam no seu Windows |

> ⚠️ Com o **Teclado** ligado e a janela da sessão em primeiro plano, Alt+Tab **não** troca de janela no seu computador. Clique em outra janela, use a barra de tarefas ou desligue a opção.

## 🩺 Problemas

| Sintoma | O que fazer |
|---|---|
| Título sem "decodificação na GPU" | o Windows não ofereceu DXVA para esta placa: decodifica na CPU (funciona, gasta mais) |
| Imagem em JPEG (título mostra "JPEG") | o decodificador H.264 do Windows não abriu (ex.: Windows N sem o *Media Feature Pack*) |
| "Sua sessão expirou" | entre de novo; a sessão do relay dura 12 h |
| Tecla presa no PC remoto | troque de janela e volte: o visualizador solta todas as teclas ao perder o foco |
| Log | `%LOCALAPPDATA%\RemoteIT\viewer.log` (recomeça a cada abertura) |

## 🔬 Por dentro

* Código: [`cmd/viewer`](../cmd/viewer), [`internal/viewer/client`](../internal/viewer/client) (API e protocolo, testado no Linux), [`internal/viewer/decode`](../internal/viewer/decode) (H.264 via Media Foundation, `rcdec.h`), [`internal/viewer/win`](../internal/viewer/win) (Win32).
* Decisão: [ADR 0012](adr/0012-visualizador-nativo.md). Próximo passo: cor 4:4:4 (exige mudar o encoder do agente).
