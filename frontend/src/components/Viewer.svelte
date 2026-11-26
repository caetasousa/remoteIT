<script lang="ts">
  import { ask } from '../lib/confirm.svelte';
  import type { Machine } from '../lib/api';
  import { PRESETS, type Preset } from '../lib/protocol';
  import { attachInput, sendCtrlShiftEsc, sendLock } from '../lib/viewer/input';
  import { ViewerSession, type Stats, type StatusLevel } from '../lib/viewer/session';

  let { agent, onBack }: { agent: Machine; onBack: () => void } = $props();

  // Keyboard Lock API (Chrome/Edge): ainda fora do lib.dom do TypeScript.
  type KeyboardLock = { lock(): Promise<void>; unlock(): void };
  const keyboard = (navigator as Navigator & { keyboard?: KeyboardLock }).keyboard;

  let canvas: HTMLCanvasElement;
  let stage: HTMLDivElement;
  let session: ViewerSession;

  let status = $state('');
  let level = $state<StatusLevel>('');
  let stats = $state<Stats | null>(null);
  let preset = $state<Preset>('quality');
  let cursorCss = $state('default');
  let overCanvas = $state(false);
  let fullscreen = $state(false);
  let hint = $state(false);
  let remoteCursor = $state<{ left: number; top: number } | null>(null);

  // Modo de exibição, lembrado entre sessões:
  // - fit: cabe na janela (padrão);
  // - real: um pixel remoto = um pixel físico do monitor, com rolagem se não
  //   couber. É o mais nítido: o navegador não precisa interpolar nada.
  type Zoom = 'fit' | 'real';
  const ZOOM_KEY = 'remoteit.zoom';
  let zoom = $state<Zoom>(localStorage.getItem(ZOOM_KEY) === 'real' ? 'real' : 'fit');

  // Cor 4:4:4 (texto colorido nítido também em movimento): decodifica as
  // duas visões em software e recompõe em WebGL. Ligada por padrão onde o
  // navegador dá conta; lembrada entre sessões.
  const COLOR_KEY = 'remoteit.color444';
  let color444 = $state(localStorage.getItem(COLOR_KEY) !== 'off');
  let can444 = $state(false);

  // o RTT é a leitura que mais pesa na sensação de controle: cor por faixa
  const rttLevel = $derived<StatusLevel>(!stats ? '' : stats.rtt < 80 ? 'ok' : stats.rtt < 200 ? 'warn' : 'bad');
  // "H.264 4:4:4 GPU: Intel® Quick Sync Video H.264 Encoder MFT" -> "H.264
  // 4:4:4 GPU": o nome do encoder fica na dica do chip. Corta em ": " (com
  // espaço) para não partir o "4:4:4".
  function codecName(enc: string) {
    const i = enc.indexOf(': ');
    return i < 0 ? enc : enc.slice(0, i);
  }

  // a fila é o atraso que o próprio vídeo causa: até 30 ms o controle de banda nem reage
  const queueLevel = $derived<StatusLevel>(
    !stats || stats.queue === null ? '' : stats.queue <= 30 ? 'ok' : stats.queue <= 80 ? 'warn' : 'bad',
  );

  // Calcula tamanho e posição do canvas na tela. Mantém a caixa do <canvas>
  // igual à imagem, para o mapeamento de clique e cursor (que usa
  // getBoundingClientRect) valer.
  //
  // A conta é feita em pixels FÍSICOS do monitor (CSS px × devicePixelRatio),
  // não em CSS px: com o Windows em 125% ou 150%, 1 CSS px não é 1 pixel da
  // tela, e uma imagem "do tamanho certo" em CSS px ainda sai interpolada
  // (borrada). Tamanho e margens são arredondados para pixels físicos
  // inteiros pelo mesmo motivo: meio pixel de deslocamento já borra o texto.
  function fitCanvas() {
    if (!canvas || !stage || !canvas.width || !canvas.height) return;
    const dpr = window.devicePixelRatio || 1;
    const boxW = stage.clientWidth * dpr;
    const boxH = stage.clientHeight * dpr;
    // pixels físicos por pixel remoto
    let scale = 1;
    if (zoom === 'fit') {
      scale = Math.min(boxW / canvas.width, boxH / canvas.height);
      // sobrando até 10%, prefere 1:1 exato com uma bordinha a ampliar um
      // pouco e borrar tudo
      if (scale >= 1 && scale < 1.1) scale = 1;
    }
    const w = Math.round(canvas.width * scale);
    const h = Math.round(canvas.height * scale);
    canvas.style.width = `${w / dpr}px`;
    canvas.style.height = `${h / dpr}px`;
    canvas.style.marginLeft = `${Math.max(0, Math.floor((boxW - w) / 2)) / dpr}px`;
    canvas.style.marginTop = `${Math.max(0, Math.floor((boxH - h) / 2)) / dpr}px`;
  }

  function toggleZoom() {
    zoom = zoom === 'fit' ? 'real' : 'fit';
    localStorage.setItem(ZOOM_KEY, zoom);
    fitCanvas();
    sendViewport();
    canvas.focus();
  }

  // A área onde a tela aparece, em pixels físicos: o agente manda o vídeo
  // nesse tamanho (no "Tamanho real", 0 x 0 = a resolução nativa). Assim uma
  // tela 4K numa janela menor não manda 4x os pixels para o navegador jogar
  // fora reduzindo — e reduzir lá, com um filtro bom, deixa o texto mais nítido.
  function viewport(): [number, number] {
    if (zoom === 'real' || !stage) return [0, 0];
    const dpr = window.devicePixelRatio || 1;
    return [Math.round(stage.clientWidth * dpr), Math.round(stage.clientHeight * dpr)];
  }

  function sendViewport() {
    session?.setViewport(...viewport());
  }

  // ao redimensionar a janela, espera ela parar: reabrir o encoder custa um keyframe
  let vpTimer: ReturnType<typeof setTimeout> | undefined;
  function viewportLater() {
    clearTimeout(vpTimer);
    vpTimer = setTimeout(sendViewport, 400);
  }

  function toggle444() {
    color444 = !color444;
    localStorage.setItem(COLOR_KEY, color444 ? 'on' : 'off');
    session.setColor444(color444);
    canvas.focus();
  }

  // a densidade de pixels muda ao arrastar a janela para outro monitor ou ao
  // dar zoom no navegador; o ResizeObserver nem sempre percebe a primeira
  function watchDpr(): () => void {
    let mq: MediaQueryList;
    const onChange = () => {
      mq.removeEventListener('change', onChange);
      fitCanvas();
      listen();
    };
    const listen = () => {
      mq = matchMedia(`(resolution: ${window.devicePixelRatio}dppx)`);
      mq.addEventListener('change', onChange);
    };
    listen();
    return () => mq.removeEventListener('change', onChange);
  }

  $effect(() => {
    session = new ViewerSession(canvas, {
      status: (t, l) => {
        status = t;
        level = l;
      },
      stats: (s) => (stats = s),
      resized: fitCanvas,
      cursorShape: (c) => (cursorCss = c),
      cursorPos: (x, y, visible) => {
        if (!visible) {
          remoteCursor = null;
          return;
        }
        // coordenadas do desktop -> coordenadas do canvas na página
        const r = canvas.getBoundingClientRect();
        remoteCursor = { left: r.left + (x * r.width) / canvas.width, top: r.top + (y * r.height) / canvas.height };
      },
    });
    const detach = attachInput(canvas, (m) => session.send(m));
    session.connect(agent.id, preset, ...viewport(), color444).then(() => (can444 = session.supports444));
    canvas.focus();
    // re-ajusta ao mudar o tamanho do palco (janela, tela cheia)
    const ro = new ResizeObserver(() => {
      fitCanvas();
      viewportLater();
    });
    ro.observe(stage);
    const unwatchDpr = watchDpr();
    return () => {
      clearTimeout(vpTimer);
      ro.disconnect();
      unwatchDpr();
      detach();
      session.close();
      if (document.fullscreenElement) document.exitFullscreen();
    };
  });


  function changePreset() {
    session.setPreset(preset);
    canvas.focus();
  }

  function taskManager() {
    sendCtrlShiftEsc((m) => session.send(m));
    canvas.focus();
  }

  // bloqueia a tela do PC remoto (Win+L não dá: SendInput não alcança a
  // troca para a estação de janela segura — veja internal/agent/lock).
  // Confirma antes: ao contrário do Ctrl+Shift+Esc, não dá para desfazer
  // daqui, e quem está na máquina precisa logar de novo para continuar.
  async function lockStation() {
    const ok = await ask({
      title: `Bloquear a tela de ${agent.name}?`,
      message: 'Quem estiver usando o PC vai precisar entrar de novo.',
      confirm: 'Bloquear',
      danger: true,
    });
    if (!ok) {
      canvas.focus();
      return;
    }
    sendLock((m) => session.send(m));
    canvas.focus();
  }

  async function enterFullscreen() {
    try {
      await stage.requestFullscreen({ navigationUI: 'hide' });
      await keyboard?.lock(); // captura Win, Alt+Tab etc.
    } catch {
      // navegador sem suporte: segue sem tela cheia
    }
    canvas.focus();
  }

  function onFullscreenChange() {
    fullscreen = !!document.fullscreenElement;
    hint = fullscreen;
    if (fullscreen) setTimeout(() => (hint = false), 3000);
    else keyboard?.unlock();
  }
</script>

<svelte:document onfullscreenchange={onFullscreenChange} />

<section>
  <header class="bar">
    <button class="back" onclick={onBack} title="Voltar">←<span class="lbl">Voltar</span></button>
    <span class="sep" aria-hidden="true"></span>

    <div class="machine">
      <span class="host">
        {agent.name}{#if agent.user}<span class="user"> / {agent.user}</span>{/if}
      </span>
      <span class={['status', level]} data-testid="viewer-status">{status}</span>
    </div>
    <span class="sep" aria-hidden="true"></span>

    <select class="wide" bind:value={preset} onchange={changePreset} title="Qualidade" aria-label="Qualidade">
      {#each PRESETS as p (p.value)}<option value={p.value}>{p.label}</option>{/each}
    </select>
    {#if can444}
      <button
        class="sq zoom c444"
        onclick={toggle444}
        aria-pressed={color444}
        title={color444
          ? 'Cor 4:4:4 ligada: texto colorido nítido também em movimento (usa mais a CPU deste PC)'
          : 'Cor 4:4:4 desligada: cor em meia resolução enquanto a tela mexe'}
      >
        <span aria-hidden="true">4:4:4</span><span class="sr-only">Cor 4:4:4</span>
      </button>
    {/if}
    <button
      class="sq zoom"
      onclick={toggleZoom}
      aria-pressed={zoom === 'real'}
      title={zoom === 'real'
        ? 'Ajustar a tela remota à janela'
        : 'Tamanho real: um pixel remoto por pixel do monitor (mais nítido; rola se não couber)'}
    >
      <span aria-hidden="true">{zoom === 'real' ? '⤢' : '1:1'}</span><span class="sr-only">Tamanho real</span>
    </button>
    <button class="sq" onclick={taskManager} title="Envia Ctrl+Shift+Esc (Gerenciador de Tarefas)">
      <span aria-hidden="true">⌨</span><span class="sr-only">Ctrl+Shift+Esc</span>
    </button>
    <button class="sq" onclick={lockStation} title="Bloquear a tela do PC remoto (equivalente a Win+L)">
      <span aria-hidden="true">🔒</span><span class="sr-only">Bloquear tela</span>
    </button>
    <button class="wide primary" onclick={enterFullscreen} title="Tela cheia: captura Win, Alt+Tab etc.">
      <span aria-hidden="true">⛶</span><span class="lbl">Tela cheia</span>
    </button>

    <!-- telemetria fixa: sempre os mesmos campos, cada um com largura fixa
         ("—" sem dado), para nada pular nem mudar de lugar a cada segundo -->
    <dl class="telemetry stats" aria-label="Telemetria">
      <div class="chip w-codec" title={stats?.enc ?? ''}>
        <span>Codec</span><b class="codec">{stats ? codecName(stats.enc) : '—'}</b>
      </div>
      <div class="chip w-res"><span>Resolução</span><b>{stats?.res ?? '—'}</b></div>
      <div class="chip w-fps"><span>FPS</span><b>{stats?.fps ?? '—'}</b></div>
      <div class="chip w-mbps"><span>Banda</span><b>{stats ? `${stats.mbps} Mbps` : '—'}</b></div>
      <div class="chip w-ms"><span>RTT</span><b class={rttLevel}>{stats ? `${stats.rtt} ms` : '—'}</b></div>
      <div class="chip w-ms" title="Quanto os quadros esperam em fila no caminho (RTT menos o RTT sem fila)">
        <span>Fila</span><b class={queueLevel}>{stats?.queue != null ? `${stats.queue} ms` : '—'}</b>
      </div>
      <div class="chip w-ms" title="Captura + codificação no PC remoto">
        <span>Codif.</span><b>{stats?.encMs != null ? `${stats.encMs.toFixed(1)} ms` : '—'}</b>
      </div>
      <div class="chip w-ms" title="Da chegada do quadro ao desenho, aqui">
        <span>Decod.</span><b>{stats?.decMs != null ? `${stats.decMs.toFixed(1)} ms` : '—'}</b>
      </div>
    </dl>
  </header>

  <div class={['stage', zoom]} bind:this={stage}>
    <div class="corners" aria-hidden="true"><i></i><i></i><i></i><i></i></div>
    <canvas
      bind:this={canvas}
      tabindex="0"
      style:cursor={cursorCss}
      onpointerenter={() => (overCanvas = true)}
      onpointerleave={() => (overCanvas = false)}
      data-testid="screen"
    ></canvas>
    {#if remoteCursor && !overCanvas}
      <!-- com o seu mouse sobre a tela, o cursor local já mostra a posição sem atraso -->
      <div class="rcur" style:left="{remoteCursor.left}px" style:top="{remoteCursor.top}px"></div>
    {/if}
    {#if hint}<div class="hint">Segure Esc para sair da tela cheia</div>{/if}
  </div>
</section>

<style>
  section {
    height: 100vh;
    height: 100dvh;
    display: flex;
    flex-direction: column;
    background: var(--bg);
  }

  /* barra compacta (opção 4): tudo numa linha só de 48px, botões de ícone
   * quadrados (32×32) e controles com texto na mesma altura de 32px — uma
   * única régua de alinhamento para todo mundo, sem grupos com alturas
   * diferentes entre si. */
  .bar {
    display: flex;
    align-items: center;
    flex-wrap: nowrap;
    overflow: hidden; /* janela estreita demais: corta, sem pular de linha */
    gap: 10px;
    height: 48px;
    padding: 0 16px;
    border-bottom: 1px solid var(--line-strong);
    background: var(--panel);
  }
  .sep {
    width: 1px;
    height: 24px;
    flex: none;
    background: var(--line);
  }
  .sq,
  .wide,
  .back {
    height: 32px;
    box-sizing: border-box;
    display: inline-flex;
    align-items: center;
    justify-content: center;
    line-height: 1;
    font: 12px var(--mono);
    white-space: nowrap;
  }
  .sq {
    width: 32px;
    flex: none;
    padding: 0;
    font-size: 14px;
  }
  .wide {
    gap: 6px;
    padding: 0 12px;
  }
  select.wide {
    flex: 0 1 auto;
    min-width: 140px;
    max-width: 270px;
  }
  .back {
    gap: 6px;
    padding: 0 12px;
    font-family: var(--sans);
  }
  .primary {
    color: var(--bg);
    text-transform: none;
    letter-spacing: normal;
    font-weight: 500;
  }
  .sr-only {
    position: absolute;
    width: 1px;
    height: 1px;
    overflow: hidden;
    clip: rect(0, 0, 0, 0);
    white-space: nowrap;
  }

  .machine {
    flex: 0 1 auto;
    min-width: 90px; /* o nome e o "conectado" nunca somem */
    display: flex;
    flex-direction: column;
    gap: 1px;
  }
  .host {
    color: var(--strong);
    font-weight: 500;
    font-size: 12.5px;
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
    max-width: 260px;
  }
  .user {
    color: var(--mut);
    font-weight: 400;
  }
  .status {
    display: inline-flex;
    align-items: center;
    gap: 5px;
    color: var(--mut);
    font: 10px var(--mono);
    letter-spacing: 0.06em;
  }
  .status::before {
    content: '';
    width: 5px;
    height: 5px;
    border-radius: 50%;
    background: currentColor;
    box-shadow: 0 0 5px currentColor;
  }
  .status.ok {
    color: var(--ok);
  }
  .status.warn {
    color: var(--warn);
  }
  .status.bad {
    color: var(--bad);
  }

  /* telemetria: uma faixa fixa à direita. Os campos são sempre os mesmos e
   * cada um tem largura fixa, com números de largura igual: nada muda de
   * lugar quando os valores mudam ou quando um dado chega. */
  .telemetry {
    flex: none;
    margin: 0 0 0 auto;
    display: flex;
    align-items: center;
    gap: 12px;
    padding: 0;
    font-family: var(--mono);
    font-variant-numeric: tabular-nums;
  }
  .telemetry .chip {
    display: flex;
    flex-direction: column;
    align-items: flex-end;
    gap: 1px;
    white-space: nowrap;
    overflow: hidden;
  }
  .telemetry .chip b {
    max-width: 100%;
    overflow: hidden;
    text-overflow: ellipsis;
  }
  .w-codec {
    width: 112px;
  }
  .w-res {
    width: 70px;
  }
  .w-fps {
    width: 28px;
  }
  .w-mbps {
    width: 70px;
  }
  .w-ms {
    width: 50px;
  }
  .c444 {
    width: auto;
    padding: 0 7px;
    font-size: 11px;
  }
  .telemetry .chip {
    display: flex;
    flex-direction: column;
    align-items: flex-end;
    gap: 1px;
    white-space: nowrap;
  }
  .telemetry .chip span {
    font-size: 8.5px;
    letter-spacing: 0.1em;
    text-transform: uppercase;
    color: var(--dim);
  }
  .telemetry .chip b {
    font-size: 11.5px;
    font-weight: 500;
    color: var(--strong);
  }
  .telemetry .chip b.ok {
    color: var(--ok);
  }
  .telemetry .chip b.warn {
    color: var(--warn);
  }
  .telemetry .chip b.bad {
    color: var(--bad);
  }

  /* o canvas é posicionado pelas margens calculadas em fitCanvas (em pixels
   * físicos inteiros), não por flex/centralização, que podem deixá-lo em meio
   * pixel e borrar a imagem */
  .stage {
    flex: 1;
    min-height: 0;
    display: block;
    overflow: hidden;
    background: #000 radial-gradient(circle at center, #0c0c0d, #000 72%);
    position: relative;
  }
  /* cantoneiras discretas, como as miras da referência */
  .corners i {
    position: absolute;
    width: 14px;
    height: 14px;
    border: 1px solid var(--line-strong);
    pointer-events: none;
  }
  .corners i:nth-child(1) {
    top: 16px;
    left: 16px;
    border-right: 0;
    border-bottom: 0;
  }
  .corners i:nth-child(2) {
    top: 16px;
    right: 16px;
    border-left: 0;
    border-bottom: 0;
  }
  .corners i:nth-child(3) {
    bottom: 16px;
    left: 16px;
    border-right: 0;
    border-top: 0;
  }
  .corners i:nth-child(4) {
    bottom: 16px;
    right: 16px;
    border-left: 0;
    border-top: 0;
  }
  /* o tamanho exibido é calculado em JS (fitCanvas): a caixa do <canvas> fica
   * igual à imagem desenhada, então o mapeamento clique/cursor continua valendo.
   * object-fit quebraria essa conta ao letterboxar dentro da caixa. */
  canvas {
    display: block;
    outline: none;
  }
  /* tamanho real: rola quando a tela remota é maior que a janela */
  .stage.real {
    overflow: auto;
  }
  .zoom {
    font-size: 11px;
  }
  .zoom[aria-pressed='true'] {
    border-color: var(--acc);
    color: var(--acc);
    background: var(--acc-wash);
  }
  .rcur {
    position: fixed;
    width: 20px;
    height: 20px;
    pointer-events: none;
    background: url("data:image/svg+xml,%3Csvg xmlns='http://www.w3.org/2000/svg' width='20' height='20'%3E%3Cpolygon points='2,2 2,16 6,12 9,18 11,17 8,11 13,11' fill='white' stroke='black' stroke-width='1'/%3E%3C/svg%3E")
      no-repeat;
    transform: translate(-2px, -2px);
  }
  .hint {
    position: absolute;
    top: 16px;
    left: 50%;
    transform: translateX(-50%);
    background: rgb(0 0 0 / 0.8);
    border: 1px solid var(--line);
    padding: 6px 14px;
    border-radius: var(--radius);
    font: 11px var(--mono);
    letter-spacing: 0.06em;
    color: var(--mut);
    pointer-events: none;
  }

  /* notebook (até 1300 px, inclusive 1366 com escala de 125%): Voltar e Tela
   * cheia só com o ícone, seleção mais estreita e campos mais juntos, para a
   * telemetria inteira caber na linha. Depende só da largura da janela: não
   * muda enquanto se usa. */
  @media (max-width: 1300px) {
    .lbl {
      position: absolute;
      width: 1px;
      height: 1px;
      overflow: hidden;
      clip-path: inset(50%);
      white-space: nowrap;
    }
    .back,
    .wide.primary {
      width: 32px;
      padding: 0;
    }
    select.wide {
      min-width: 120px;
      max-width: 170px;
    }
    .bar {
      gap: 8px;
    }
    .telemetry {
      gap: 10px;
    }
  }
  /* celular: o nome da máquina também ganha uma linha */
  @media (max-width: 760px) {
    .bar {
      height: auto;
      flex-wrap: wrap;
      padding: 8px 14px;
    }
    .machine {
      flex-basis: 100%;
      order: 1;
    }
    .telemetry {
      order: 2;
      margin-left: 0;
      overflow-x: auto;
    }
  }
</style>
