<script lang="ts">
  import type { AgentInfo } from '../lib/api';
  import { PRESETS, type Preset } from '../lib/protocol';
  import { attachInput, sendCtrlShiftEsc, sendLock } from '../lib/viewer/input';
  import { ViewerSession, type Stats, type StatusLevel } from '../lib/viewer/session';

  let { agent, onBack }: { agent: AgentInfo; onBack: () => void } = $props();

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

  // o RTT é a leitura que mais pesa na sensação de controle: cor por faixa
  const rttLevel = $derived<StatusLevel>(!stats ? '' : stats.rtt < 80 ? 'ok' : stats.rtt < 200 ? 'warn' : 'bad');

  // Ajusta o tamanho exibido do canvas para o maior retângulo que cabe no palco
  // mantendo a proporção do quadro. Mantém a caixa do <canvas> igual à imagem,
  // para o mapeamento de clique e cursor (que usa getBoundingClientRect) valer.
  function fitCanvas() {
    if (!canvas || !stage || !canvas.width || !canvas.height) return;
    const box = stage.getBoundingClientRect();
    const scale = Math.min(box.width / canvas.width, box.height / canvas.height);
    canvas.style.width = `${Math.round(canvas.width * scale)}px`;
    canvas.style.height = `${Math.round(canvas.height * scale)}px`;
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
    session.connect(agent.id, preset);
    canvas.focus();
    // re-ajusta ao mudar o tamanho do palco (janela, tela cheia)
    const ro = new ResizeObserver(fitCanvas);
    ro.observe(stage);
    return () => {
      ro.disconnect();
      detach();
      session.close();
      if (document.fullscreenElement) document.exitFullscreen();
    };
  });


  function changePreset() {
    session.send({ t: 'cfg', p: preset });
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
  function lockStation() {
    if (!confirm(`Bloquear a tela de ${agent.host}? Quem estiver usando o PC vai precisar entrar de novo.`)) return;
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
    <button class="back" onclick={onBack} title="Voltar">← Voltar</button>
    <span class="sep" aria-hidden="true"></span>

    <div class="machine">
      <span class="host">
        {agent.host}{#if agent.user}<span class="user"> / {agent.user}</span>{/if}
      </span>
      <span class={['status', level]} data-testid="viewer-status">{status}</span>
    </div>
    <span class="sep" aria-hidden="true"></span>

    <select class="wide" bind:value={preset} onchange={changePreset} title="Qualidade" aria-label="Qualidade">
      {#each PRESETS as p (p.value)}<option value={p.value}>{p.label}</option>{/each}
    </select>
    <button class="sq" onclick={taskManager} title="Envia Ctrl+Shift+Esc (Gerenciador de Tarefas)">
      <span aria-hidden="true">⌨</span><span class="sr-only">Ctrl+Shift+Esc</span>
    </button>
    <button class="sq" onclick={lockStation} title="Bloquear a tela do PC remoto (equivalente a Win+L)">
      <span aria-hidden="true">🔒</span><span class="sr-only">Bloquear tela</span>
    </button>
    <button class="wide primary" onclick={enterFullscreen} title="Tela cheia: captura Win, Alt+Tab etc.">
      <span aria-hidden="true">⛶</span> Tela cheia
    </button>

    <dl class="telemetry stats" aria-label="Telemetria">
      {#if stats}
        <div class="chip"><span>Codec</span><b>{stats.enc}</b></div>
        <div class="chip"><span>Resolução</span><b>{stats.res}</b></div>
        <div class="chip"><span>FPS</span><b>{stats.fps}</b></div>
        <div class="chip"><span>Banda</span><b>{stats.mbps} Mbps</b></div>
        <div class="chip"><span>RTT</span><b class={rttLevel}>{stats.rtt} ms</b></div>
      {:else}
        <div class="chip"><span>Sinal</span><b class="muted">aguardando…</b></div>
      {/if}
    </dl>
  </header>

  <div class="stage" bind:this={stage}>
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
    flex-wrap: wrap;
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
  .back {
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
    min-width: 0;
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

  /* telemetria: chips verticais compactos, sempre à direita */
  .telemetry {
    display: flex;
    align-items: center;
    gap: 16px;
    margin: 0 0 0 auto;
    padding: 0;
    font-family: var(--mono);
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

  .stage {
    flex: 1;
    display: flex;
    justify-content: center;
    align-items: center;
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

  /* telas estreitas: a telemetria rola por baixo, em vez de espremer a barra */
  @media (max-width: 900px) {
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
      margin-left: 0;
      order: 2;
      flex-basis: 100%;
      overflow-x: auto;
      padding-top: 4px;
    }
  }
</style>
