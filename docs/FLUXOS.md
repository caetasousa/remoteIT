# 🧭 Fluxos do RemoteIT

> 🗣️ **Em palavras simples:** este documento conta, passo a passo, o que acontece por baixo de cada coisa que o usuário faz: abrir o agente, o técnico clicar em **Conectar**, um quadro de vídeo atravessar a internet, uma tecla chegar ao Windows, a tela de bloqueio aparecer, alguém criar uma conta. Cada fluxo tem um diagrama e aponta o código que o implementa.

Os diagramas são [Mermaid](https://mermaid.js.org/): o GitHub os desenha sozinho.

| # | Fluxo | Em uma frase |
|---|---|---|
| 1️⃣ | [Agente entra no ar](#agente) | o PC liga para o relay e fica esperando |
| 2️⃣ | [Login do técnico](#login) | usuário e senha viram um cookie de 12 h |
| 3️⃣ | [Sessão remota](#sessao) | do clique em **Conectar** ao primeiro quadro, e o fim |
| 4️⃣ | [Pipeline de vídeo](#video) | da GPU do PC à GPU do técnico |
| 5️⃣ | [Controle de fluxo e bitrate](#fluxo) | por que a imagem não "atrasa" |
| 6️⃣ | [Mouse e teclado](#entrada) | do evento no navegador ao `SendInput` |
| 7️⃣ | [Bloquear a tela e trocar de usuário](#bloqueio) | Win+L remoto e login com outra conta |
| 8️⃣ | [Modo serviço: tela de login e bloqueio](#servico) | o serviço, o worker SYSTEM e os desktops do Windows |
| 9️⃣ | [Instalar, atualizar e remover](#instalar) | o que cada `.exe` faz com dois cliques |
| 🔟 | [Dois técnicos na mesma máquina](#dois-tecnicos) | o segundo assume |
| 1️⃣1️⃣ | [Contas por e-mail](#contas) | cadastro, aprovação, recuperação e vínculo |
| 1️⃣2️⃣ | [Entregar uma versão nova do agente](#entrega) | por que o download nunca vem do cache |

---

<a id="agente"></a>
## 1️⃣ Agente entra no ar

> 🗣️ **Em palavras simples:** ao abrir, o agente liga para o relay como um navegador abriria um site seguro, se apresenta com a chave secreta embutida no `.exe` e fica com a linha aberta. Se a linha cair, ele liga de novo, esperando cada vez um pouco mais (até 30 s) para não martelar o servidor.

```mermaid
sequenceDiagram
    autonumber
    participant A as 🖥️ Agente<br/>(relayclient)
    participant R as 🔀 Relay<br/>(httpapi + hub)
    participant C as 🧑‍💻 Console
    A->>R: GET /ws/agent?id=pc1&host=PC1&user=ana&key=•••
    alt chave errada
        R-->>A: 403
        Note over A: tenta de novo com backoff
    else chave certa
        R->>R: hub.Register(pc1) — se já havia um pc1, a conexão antiga cai
        R-->>A: 101 WebSocket
        C->>R: GET /api/agents (a cada 3 s)
        R-->>C: [{id: pc1, host: PC1, user: ana, busy: false}]
    end
    loop a cada 20 s
        R->>A: ping
        A-->>R: pong
    end
    Note over R: 60 s sem tráfego → derruba a conexão
    A--xR: rede caiu
    Note over A: espera 1 s, 2 s, 4 s... até 30 s<br/>(volta a 1 s se a conexão durou mais de 30 s)
    A->>R: reconecta
```

| Detalhe | Valor | Onde |
|---|---|---|
| ID da máquina | hostname em minúsculas | `cmd/agent/main.go` |
| Backoff | 1 s, dobrando até 30 s; volta a 1 s após 30 s conectado | `internal/agent/relayclient` |
| Keepalive | ping a cada 20 s; leitura expira em 60 s | `relayclient`, `httpapi` |
| Mesma ID duas vezes | a conexão nova substitui a antiga | `internal/relay/hub` |

> ⚠️ Por causa da última linha, **duas cópias do agente no mesmo PC se derrubam** em loop ("conectado/desconectado" no log do relay). Por isso o agente portátil sai em silêncio quando já há outra cópia ou o serviço rodando ([fluxo 9](#instalar)).

---

<a id="login"></a>
## 2️⃣ Login do técnico

> 🗣️ **Em palavras simples:** a senha nunca é guardada, só um "resumo" dela (bcrypt), que não dá para desfazer. Ao acertar a senha, o relay entrega ao navegador um número aleatório (o token da sessão) num cookie que o JavaScript não consegue ler. No banco fica só o resumo desse número: quem roubar o banco não consegue se passar por ninguém.

```mermaid
sequenceDiagram
    autonumber
    participant N as 🧑‍💻 Navegador
    participant H as 🔀 httpapi
    participant I as 👤 identity
    participant DB as 🐘 PostgreSQL
    N->>H: POST /api/login {username ou e-mail, password}
    H->>I: Login(...)
    I->>DB: busca usuário
    alt usuário não existe
        I->>I: compara com um hash falso (mesmo tempo de resposta)
        I-->>H: credenciais inválidas
        H-->>N: 401
    else senha errada
        I-->>H: credenciais inválidas
        H-->>N: 401
    else conta aguardando aprovação
        H-->>N: 403
    else ok
        I->>I: token = 32 bytes aleatórios
        I->>DB: INSERT sessions (sha256(token), expira em 12 h)
        H-->>N: 200 + Set-Cookie remoteit_session (HttpOnly, SameSite=Strict, Secure)
    end
```

* 🕵️ **Mesmo tempo para "não existe" e "senha errada":** sem isso, medir o tempo da resposta revelaria quais usuários existem.
* 🍪 **`SameSite=Strict` + `Content-Type: application/json` obrigatório:** um site de terceiros não consegue disparar ações com o cookie do técnico (CSRF). Veja [SEGURANCA.md](SEGURANCA.md).

---

<a id="sessao"></a>
## 3️⃣ Sessão remota: do clique ao primeiro quadro

> 🗣️ **Em palavras simples:** quando o técnico clica em **Conectar**, o relay primeiro **anota** o acesso (se não conseguir anotar, não deixa entrar), depois avisa o agente. O agente pergunta ao usuário do PC se ele permite (a não ser no build sem consentimento), e só então começa a mandar a tela e a aceitar mouse e teclado.

```mermaid
sequenceDiagram
    autonumber
    participant T as 🧑‍💻 Técnico
    participant R as 🔀 Relay
    participant DB as 🐘 Auditoria
    participant A as 🖥️ Agente
    participant U as 🙋 Usuário do PC
    T->>R: GET /ws/viewer?id=pc1 (cookie + Origin)
    R->>R: auth: cookie válido? Origin = host?
    R->>DB: audit.Begin (técnico, máquina, IP, início)
    alt banco falhou
        R-->>T: close 1011 — sem registro, sem acesso
    end
    R->>R: hub.Attach — derruba o técnico anterior, se houver
    R->>A: {"t":"start"}
    T->>A: {"t":"hello","h264":true}
    T->>A: {"t":"cfg","p":"quality"}
    A->>T: {"t":"waiting"}
    A->>U: "Permitir o acesso remoto?"
    alt usuário recusou
        A->>T: {"t":"denied"}
    else permitiu (ou build NO_CONSENT)
        A->>T: {"t":"granted"}
        A->>T: quadro 1 (keyframe)
        T->>A: {"t":"ack","s":1}
        Note over T,A: segue o fluxo 5 (controle de fluxo)
    end
    T--xR: fecha a aba ou clica ← Voltar
    R->>A: {"t":"stop"}
    R->>DB: grava ended_at
```

### 🔄 Estados da sessão no agente

```mermaid
stateDiagram-v2
    [*] --> Ocioso
    Ocioso --> Perguntando: start
    Perguntando --> Negado: usuário clicou Não
    Perguntando --> Transmitindo: usuário clicou Sim / NO_CONSENT
    Transmitindo --> TelaProtegida: captura falhou (UAC, bloqueio)
    TelaProtegida --> Transmitindo: captura voltou
    Transmitindo --> Ocioso: stop
    TelaProtegida --> Ocioso: stop
    Negado --> Ocioso: stop
    note right of Transmitindo
        só aqui o agente injeta mouse e teclado
        e atende o pedido de bloquear a tela
    end note
```

| Mensagem para o técnico | O console mostra |
|---|---|
| `waiting` | "aguardando o usuário permitir" |
| `granted` | a tela remota |
| `denied` | "o usuário recusou" |
| `secure` | "tela protegida por UAC/bloqueio — aguardando" (retoma sozinho) |

* 🔒 **Entrada só depois do consentimento:** o agente descarta mouse, teclado e o pedido de bloqueio enquanto `granted` não for verdadeiro ([`internal/agent/session`](../internal/agent/session)). O teste `TestInputOnlyAfterConsent` prova isso.
* 🎬 **H.264 ou JPEG:** se o navegador não mandar `hello` em 1,5 s, o agente assume JPEG.

---

<a id="video"></a>
## 4️⃣ Pipeline de vídeo: da GPU do PC à GPU do técnico

> 🗣️ **Em palavras simples:** a tela é copiada direto da placa de vídeo, reduzida e comprimida ali mesmo em H.264 (o mesmo formato de um vídeo do YouTube), sem passar pelo processador. O navegador do técnico recebe e descomprime também na placa de vídeo dele. Sem placa de vídeo, o plano B é tirar "fotos" JPEG da tela.

```mermaid
graph LR
    subgraph PC["🖥️ PC atendido"]
        D["🎞️ DXGI Desktop<br/>Duplication"] --> S["📐 D3D11<br/>escala + NV12"]
        S --> E["⚡ Media Foundation<br/>H.264 (NVENC/QSV/AMF)"]
        G["🖼️ GDI BitBlt"] -.sem GPU.-> J["🗜️ JPEG"]
        E --> F["📦 cabeçalho 12 bytes<br/>codec, keyframe, w, h, seq"]
        J --> F
    end
    F -- "WebSocket binário<br/>via relay" --> P
    subgraph NAV["🧑‍💻 Navegador"]
        P["🔍 parseFrame"] --> V["🎬 WebCodecs<br/>VideoDecoder"]
        P -.JPEG.-> I["🖼️ createImageBitmap"]
        V --> C["🖵 canvas"]
        I --> C
        C --> K["✅ ack s=seq"]
    end
```

| Etapa | Código | Observação |
|---|---|---|
| Captura + encoder | [`internal/agent/media/h264`](../internal/agent/media/h264) (C em `rcenc.h`) | compilado com Zig; sem Zig, o build sai só com JPEG |
| Fallback | [`media/capture`](../internal/agent/media/capture), [`media/jpeg`](../internal/agent/media/jpeg) | GDI + `image/jpeg`; qualidade 25–85 conforme o bitrate |
| Laço de transmissão | [`media/stream.go`](../internal/agent/media/stream.go) | abre a fonte, mede, envia stats 1x/s |
| Formato do quadro | [`internal/protocol`](../internal/protocol) + [`frontend/src/lib/protocol.ts`](../frontend/src/lib/protocol.ts) | veja [PROTOCOLO.md](PROTOCOLO.md) |
| Decodificação | [`frontend/src/lib/viewer/session.ts`](../frontend/src/lib/viewer/session.ts) | o caminho dos quadros fica fora do Svelte ([ADR 0003](adr/0003-svelte-no-console.md)) |

### 🎚️ Presets de qualidade

| Preset (`cfg.p`) | No console | Largura máx. | FPS | Bitrate |
|---|---|---|:---:|---|
| `turbo` | Ultra-rápido | 1280 px | 60 | 4–10 Mbps |
| `balanced` | Equilibrado | 1920 px | 60 | 8–20 Mbps |
| `quality` (padrão) | Nítido | nativa | 60 | 15–40 Mbps |
| `quality60` | Máximo | nativa | 60 | 25–80 Mbps |

* 🖱️ **Cursor por cima do vídeo:** o agente manda a posição (`cur`) e o formato (`cs`) do cursor separados, e o console desenha o cursor local. Ele se move na hora, mesmo que o quadro ainda esteja a caminho.
* 🔁 **Refino com a tela parada:** quando nada muda, o agente recodifica o último quadro mais duas vezes (`FlagRepeat`) para deixar o texto nítido.

---

<a id="fluxo"></a>
## 5️⃣ Controle de fluxo e bitrate

> 🗣️ **Em palavras simples:** se o agente mandasse quadros mais rápido do que a internet aguenta, eles ficariam numa fila, e o técnico veria a tela de segundos atrás. Por isso o navegador **confirma** cada quadro que recebe, e o agente nunca deixa mais de **dois** sem confirmação no caminho. Quando a rede aperta, ele baixa a qualidade; quando folga, sobe de novo, aos poucos.

```mermaid
flowchart TD
    START(["⏱️ próximo quadro"]) --> Q{"2 quadros<br/>sem ack?"}
    Q -- sim --> W["espera 3 ms<br/>congested++"] --> START
    Q -- não --> N["Source.Next<br/>(espera até 1 quadro de tempo)"]
    N --> CH{"tela mudou?"}
    CH -- não --> START
    CH -- sim --> SEND["envia quadro seq=n<br/>flow.Sent(n)"] --> START
    ACK(["✅ ack s=n chega"]) --> RTT["flow.OnAck: libera 1 vaga<br/>e mede o RTT"]
    SEC(["🕐 a cada 1 s"]) --> B{"houve<br/>congestionamento?"}
    B -- sim --> DOWN["bitrate × 0,75<br/>(nunca abaixo do mínimo do preset)"]
    B -- não --> UP["bitrate × 1,1 + 50 kbps<br/>(nunca acima do máximo)"]
    DOWN --> STATS["envia stats: enc, fps, kbps, target, rtt, w, h"]
    UP --> STATS
```

| Regra | Valor | Onde |
|---|---|---|
| Quadros em trânsito | no máximo 2 | [`media/flow.go`](../internal/agent/media/flow.go) |
| Ack perdido | depois de 3 s sem ack, o agente supõe que ele se perdeu e libera a vaga | `flow.go` (teste `TestFlowControlLostAck`) |
| Ack falso ou repetido | ignorado | `TestFlowControlIgnoresBogusAcks` |
| Queda de bitrate | −25% por segundo congestionado | `media.NextBitrate` |
| Subida de bitrate | +10% + 50 kbps por segundo livre | `media.NextBitrate` |
| Keyframe sob pedido | `kf` descarta o que está em trânsito | `Stream.RequestKeyframe` |

---

<a id="entrada"></a>
## 6️⃣ Mouse e teclado

> 🗣️ **Em palavras simples:** o navegador transforma a posição do mouse em "porcentagem da tela" (0 a 1), para funcionar com qualquer resolução. As teclas vão com o nome físico (`KeyA`) e o caractere (`a`, `ç`). No Windows, letras e acentos são digitados como **caractere**, e não como tecla: assim o `ç` sai certo mesmo que o teclado do PC remoto seja americano.

```mermaid
sequenceDiagram
    autonumber
    participant N as 🧑‍💻 Navegador<br/>(viewer/input.ts)
    participant A as 🖥️ Agente<br/>(session)
    participant Q as 📥 Fila de entrada
    participant TH as 🧵 Thread de injeção<br/>(input_windows.go)
    participant W as 🪟 Windows
    N->>A: {"t":"mm","x":0.52,"y":0.31}
    N->>A: {"t":"kd","code":"KeyC","key":"ç"}
    A->>A: consentimento dado?
    A->>Q: Handle(m) — só enfileira
    Note over Q: fila cheia: descarta movimento de mouse<br/>(o próximo corrige), nunca clique ou tecla
    Q->>TH: próximo evento
    TH->>TH: desktop ativo ainda é o desta thread?<br/>(confere no máximo 1x/s)
    alt caractere imprimível sem Ctrl/Alt/Win
        TH->>W: SendInput(KEYEVENTF_UNICODE 'ç')
    else tecla especial ou atalho
        TH->>W: SendInput(virtual-key, EXTENDEDKEY se preciso)
    end
    alt Windows recusou (0 eventos)
        TH->>TH: segue o desktop ativo e tenta de novo
        TH->>W: SendInput(...)
    end
```

* 🧵 **Por que uma thread só para isso?** No Windows, cada thread está presa a um **desktop** (a área de trabalho normal ou a tela de login), e o `SendInput` só alcança o desktop da thread que o chama. A goroutine que lê a conexão pula de thread em thread, então a injeção roda numa thread fixa que acompanha o desktop ativo ([fluxo 8](#servico)).
* 🧭 **Coordenadas:** `x, y` em [0,1] viram coordenadas absolutas do Windows (0–65535) no monitor principal.
* ⌨️ **Tabela de teclas:** `KeyboardEvent.code` → virtual-key em [`internal/agent/input/keymap.go`](../internal/agent/input/keymap.go) (testada em `keymap_test.go`).
* 🖵 **Tela cheia:** a Keyboard Lock API deixa o navegador capturar Win, Alt+Tab e Esc, que normalmente o sistema do técnico tomaria para si.

---

<a id="bloqueio"></a>
## 7️⃣ Bloquear a tela e trocar de usuário

> 🗣️ **Em palavras simples:** o Windows **não aceita** Win+L simulado: é uma proteção para um programa não conseguir trancar a sessão de alguém. Então o botão 🔒 do console manda uma mensagem própria, e o agente chama a função oficial do Windows que bloqueia a tela. Para depois logar com outra conta pela tela de bloqueio, o agente precisa estar no **modo serviço**.

```mermaid
sequenceDiagram
    autonumber
    participant T as 🧑‍💻 Técnico
    participant A as 🖥️ Agente (worker)
    participant W as 🪟 Windows
    T->>T: clica 🔒 e confirma
    T->>A: {"t":"lock"}
    A->>A: consentimento dado?
    A->>W: LockWorkStation()
    W->>W: troca para o desktop "Winlogon"
    Note over A: a captura falha (desktop mudou)<br/>→ reabre seguindo o desktop ativo
    A->>T: quadros da tela de bloqueio
    T->>A: clique / tecla (levanta a "cortina")
    T->>A: digita a senha da outra conta
    A->>W: SendInput no desktop Winlogon
    W->>W: login → volta ao desktop "Default"
    A->>T: quadros da nova sessão
```

| Agente | 🔒 bloqueia? | Vê a tela de bloqueio? | Digita a senha? |
|---|:---:|:---:|:---:|
| ▶️ Portátil (`suporte-ti.exe`) | ✅ | ❌ "tela protegida" | ❌ |
| 🧩 Serviço (`suporte-ti-servico.exe`) | ✅ | ✅ | ✅ |

> ⚠️ Se o PC exigir **Ctrl+Alt+Del** antes do login (política de grupo), o login remoto para nessa tela: o Windows não deixa nenhum programa simular essa combinação.

---

<a id="servico"></a>
## 8️⃣ Modo serviço: a tela de login e de bloqueio

> 🗣️ **Em palavras simples:** a tela de login do Windows é especial. Ela roda como o próprio sistema, numa "mesa" separada (o desktop **Winlogon**), e o Windows ignora cliques e teclas que venham de programas comuns. Para alcançá-la, o agente precisa rodar **como o sistema** (SYSTEM) e **mudar de mesa** junto com o Windows.

### 🏢 Quem roda onde

```mermaid
graph TB
    subgraph S0["Sessão 0 (sem tela)"]
        SVC["🧩 serviço RemoteITAgent<br/>suporte-ti-servico.exe service<br/>LocalSystem"]
    end
    subgraph S2["Sessão do console (a que aparece no monitor)"]
        WK["🖥️ worker<br/>suporte-ti-servico.exe worker<br/>SYSTEM nesta sessão"]
        subgraph WS["Window station WinSta0"]
            DEF["🗔 desktop Default<br/>(área de trabalho)"]
            WL["🔐 desktop Winlogon<br/>(login, bloqueio, UAC)"]
        end
        WK -. "captura e entrada<br/>no desktop ativo" .-> DEF
        WK -.-> WL
    end
    SVC -- "CreateProcessAsUser<br/>(token do serviço movido para a sessão)" --> WK
    WK -- "wss:443" --> RELAY["🔀 relay"]
```

### 🔁 O serviço vigia a sessão

```mermaid
sequenceDiagram
    autonumber
    participant SCM as ⚙️ Windows (SCM)
    participant S as 🧩 Serviço
    participant W as 🖥️ Worker
    SCM->>S: inicia (boot)
    S->>S: sessão ativa = WTSGetActiveConsoleSessionId
    S->>S: token = cópia do próprio token SYSTEM<br/>SetTokenInformation(TokenSessionId = sessão)
    S->>S: ambiente = o do usuário logado, se houver
    S->>W: CreateProcessAsUser("... worker")
    Note over S: service.log: "worker lançado" sessão=2 pid=...
    loop a cada 2 s
        S->>S: worker vivo? sessão mudou?
        alt morreu ou a sessão mudou
            S->>W: encerra e relança
        end
    end
    SCM->>S: SessionChange: logon/logoff
    S->>W: relança (pega o USERNAME novo)
    SCM->>S: SessionChange: lock/unlock
    Note over S,W: nada: o worker segue o desktop sozinho<br/>e a conexão do técnico continua
```

### 🧵 Por que SYSTEM, e por que trocar de thread

| Problema | Sintoma que aparecia | Solução no código |
|---|---|---|
| A captura via o desktop errado | tela congelada ao bloquear | toda (re)abertura da captura chama `desktop.FollowInput` → `OpenInputDesktop` + `SetThreadDesktop` ([`internal/agent/desktop`](../internal/agent/desktop)) |
| **UIPI**: o Windows descarta entrada de um processo de integridade média para a tela de login (SYSTEM) | a tela de bloqueio aparecia, mas cliques e teclas não chegavam | o worker roda com o **token SYSTEM** do serviço, movido para a sessão ([`lifecycle_windows.go`](../internal/agent/lifecycle/lifecycle_windows.go)) |
| `SetThreadDesktop` falha numa thread que já tem janelas, e o Media Foundation cria janelas ocultas | "tela protegida — aguardando" para sempre | a captura roda numa thread do SO **descartável**: se não conseguir mudar de desktop, a thread morre e a captura recomeça numa nova ([`media/stream.go`](../internal/agent/media/stream.go), teste `TestStreamRecoversWhenDesktopCanBeFollowedAgain`) |
| A injeção rodava em qualquer thread | entrada sumia no desktop Winlogon | thread fixa de injeção que segue o desktop ([fluxo 6](#entrada)) |

```mermaid
flowchart TD
    A(["captura precisa abrir"]) --> F["FollowDesktop()"]
    F --> O{"abriu a captura?"}
    O -- sim --> OK(["transmitindo"])
    O -- não --> E{"FollowDesktop<br/>tinha falhado?"}
    E -- não --> SEC["envia secure<br/>tenta de novo em 500 ms"] --> A
    E -- sim --> NEW["descarta esta thread do SO<br/>(LockOSThread sem Unlock)"]
    NEW --> T["nova goroutine + nova thread limpa"] --> A
```

---

<a id="instalar"></a>
## 9️⃣ Instalar, atualizar e remover

> 🗣️ **Em palavras simples:** o `suporte-ti.exe` só roda. O `suporte-ti-servico.exe` instala: pede a permissão de administrador do Windows, se copia para `C:\Program Files\RemoteIT` e cria o serviço. Rodar um `suporte-ti-servico.exe` mais novo em cima **atualiza**. Nada abre janela, e tudo fica anotado em `C:\ProgramData\RemoteIT\service.log`.

```mermaid
flowchart TD
    DC(["dois cliques"]) --> ED{"edição gravada no .exe"}
    ED -- "portátil" --> SR{"serviço<br/>rodando?"}
    SR -- sim --> EXIT1(["sai em silêncio<br/>(o serviço já dá o acesso)"])
    SR -- não --> MX{"outra cópia<br/>na sessão?"}
    MX -- sim --> EXIT2(["sai em silêncio"])
    MX -- não --> RUN(["conecta ao relay"])
    ED -- "servico" --> ADM{"é administrador?"}
    ADM -- não --> UAC["pede o UAC<br/>(ShellExecute runas install)"] --> ADM2(["processo elevado continua"])
    ADM -- sim --> EX{"serviço já existe?"}
    ADM2 --> EX
    EX -- sim --> STOP["para o serviço e espera<br/>(o worker sai junto)"] --> COPY
    EX -- não --> COPY["copia o .exe para<br/>%ProgramFiles%\RemoteIT\ (grava .new e renomeia)"]
    COPY --> CFG{"já existia?"}
    CFG -- sim --> UPD["UpdateConfig: novo caminho"]
    CFG -- não --> CRT["CreateService: automático,<br/>reinício em caso de falha"]
    UPD --> START["inicia o serviço"]
    CRT --> START
    START --> LOG(["service.log: instalado/atualizado"])
```

| Comando | O que faz |
|---|---|
| dois cliques em `suporte-ti-servico.exe` | o mesmo que `install` |
| `suporte-ti-servico.exe install` | instala ou atualiza (UAC se preciso) |
| `suporte-ti-servico.exe uninstall` | para, remove o serviço e apaga `%ProgramFiles%\RemoteIT` |
| `suporte-ti.exe` | portátil; `install`/`uninstall` também funcionam nele |

👉 Passo a passo para o usuário, e o que fazer quando algo dá errado: [AGENTE-WINDOWS.md](AGENTE-WINDOWS.md).

---

<a id="dois-tecnicos"></a>
## 🔟 Dois técnicos na mesma máquina

> 🗣️ **Em palavras simples:** cada máquina atende um técnico por vez. Se um segundo técnico conecta, ele assume; o primeiro é desconectado, e cada acesso fica registrado separadamente.

```mermaid
sequenceDiagram
    autonumber
    participant T1 as 🧑‍💻 Técnico 1
    participant R as 🔀 Relay (hub)
    participant T2 as 🧑‍💻 Técnico 2
    participant A as 🖥️ Agente
    T1->>R: conectado em pc1
    Note over R: GET /api/agents → busy: true, viewedBy: tecnico1
    T2->>R: GET /ws/viewer?id=pc1
    R->>R: audit.Begin (técnico 2)
    R--xT1: fecha a conexão do técnico 1
    R->>R: fim do acesso do técnico 1 na auditoria
    R->>A: {"t":"start"} (nova transmissão)
    A->>T2: granted + quadros
```

* O `stop` só vale quando quem sai é o **técnico atual**: a saída atrasada do técnico 1 não derruba a sessão do técnico 2 (testado em `internal/relay/hub`).

---

<a id="contas"></a>
## 1️⃣1️⃣ Contas por e-mail

> 🗣️ **Em palavras simples:** quem quer acesso informa o e-mail e recebe um link. Pelo link, escolhe o nome de usuário e a senha. A conta nasce **pendente**, e um administrador precisa aprovar. Esqueceu a senha? Outro link, válido por 1 hora, que troca a senha e derruba todas as sessões abertas. Os links são de uso único, e no banco fica só o resumo (SHA-256) de cada um.

### ✍️ Cadastro e aprovação

```mermaid
sequenceDiagram
    autonumber
    participant P as 🙋 Pessoa
    participant R as 🔀 Relay
    participant M as ✉️ SMTP
    participant AD as 👑 Admin
    P->>R: POST /api/account/signup {email}
    R->>R: limites: 20/h por IP, 3/h por e-mail, 30/dia
    R->>M: link /#/criar-conta?token=••• (vale 24 h)
    R-->>P: 202 (sempre, exista ou não a conta)
    P->>R: POST /api/account/signup/complete {token, username, password}
    R->>R: token válido e não usado? marca como usado
    alt e-mail = RELAY_ADMIN_EMAIL
        R-->>P: 201 conta de admin, já aprovada
    else
        R-->>P: 201 conta pendente
        R->>M: aviso ao admin: conta aguardando
        AD->>R: POST /api/users/{id}/approve
        Note over P: agora o login funciona
    end
```

### 🔑 Recuperação de acesso

```mermaid
sequenceDiagram
    autonumber
    participant P as 🙋 Pessoa
    participant R as 🔀 Relay
    participant M as ✉️ SMTP
    P->>R: POST /api/account/reset {email}
    R->>M: link /#/nova-senha?token=••• (vale 1 h)
    R-->>P: 202 (sempre)
    P->>R: POST /api/account/reset/complete {token, password}
    R->>R: troca a senha
    R->>R: apaga todas as sessões da conta
    R->>R: fecha as telas remotas abertas por ela
    R-->>P: 204
```

* 🔗 **Vincular e-mail a uma conta antiga:** `POST /api/account/email` (pede a senha) → link `/#/confirmar-email` de 1 h → `POST /api/account/email/complete`. Funciona mesmo se a pessoa abrir o link deslogada.
* 🙈 **202 sempre:** a resposta não revela se o e-mail tem conta.
* 👉 Rotas e códigos de resposta: [API.md](API.md). Decisão: [ADR 0006](adr/0006-contas-por-email.md).

---

<a id="entrega"></a>
## 1️⃣2️⃣ Entregar uma versão nova do agente

> 🗣️ **Em palavras simples:** publicar um agente novo é trocar o arquivo no servidor. O cuidado é garantir que ninguém no caminho (Cloudflare ou o navegador) entregue uma **cópia velha guardada**. Isso já aconteceu: o PC baixava "de novo" e recebia a versão de horas antes.

```mermaid
sequenceDiagram
    autonumber
    participant DEV as 👩‍💻 build.sh
    participant SRV as 🗄️ Servidor (dist/)
    participant CF as ☁️ Cloudflare
    participant N as 🧑‍💻 Navegador
    DEV->>SRV: suporte-ti.exe + suporte-ti-servico.exe
    N->>CF: GET /download/agent-servico.exe?t=<agora>
    CF->>SRV: repassa (URL nova a cada carregamento)
    SRV-->>N: Cache-Control: no-store, private
    Note over CF,N: ninguém guarda a resposta
```

* 🧾 **Confira o hash** do arquivo baixado (`Get-FileHash`) contra o publicado; os comandos estão em [AGENTE-WINDOWS.md](AGENTE-WINDOWS.md#conferir-versao).
* 🔐 **Só para quem está logado:** sem cookie, as rotas de download respondem 401. O `private` impede que um cache compartilhado guarde um arquivo que tem a chave do agente embutida.
