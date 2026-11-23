<script lang="ts">
  // Primeira tela das rotas de conta: mensagem à esquerda (3/4) e o formulário
  // no trilho à direita (1/4).
  import { api } from '../../lib/api';
  import Frame from './Frame.svelte';
  import type { Snippet } from 'svelte';

  let { children }: { children: Snippet } = $props();

  let online = $state<boolean | null>(null);

  $effect(() => {
    const check = () => api.healthy().then((v) => (online = v));
    check();
    const t = setInterval(check, 15_000);
    return () => clearInterval(t);
  });
</script>

<Frame>
  <main>
    <div class="hero">
      <div class="strip label">
        <span><span class="led on"></span>Seq. 01 / Acesso</span>
        <span class="port">WSS : 443</span>
      </div>
      <div class="copy">
        <div class="path">/console/<b>acesso</b></div>
        <h1>autenticar sessão</h1>
        <p>
          Tela, mouse e teclado de PCs Windows direto no navegador, atravessando o firewall. Cada acesso fica
          registrado.
        </p>
      </div>
    </div>
    <aside>
      <div class="form">{@render children()}</div>
      <dl class="readout">
        <div>
          <dt><span class="led" class:on={online === true} class:off={online === false}></span>Relay</dt>
          <dd>{online === null ? 'verificando' : online ? 'online' : 'indisponível'}</dd>
        </div>
        <div><dt>Transporte</dt><dd>WebSocket</dd></div>
        <div><dt>Auditoria</dt><dd>todo acesso</dd></div>
      </dl>
    </aside>
  </main>
</Frame>

<style>
  main {
    flex: 1;
    display: grid;
    grid-template-columns: minmax(0, 3fr) minmax(320px, 1fr);
  }
  .hero {
    position: relative;
    overflow: hidden;
    display: flex;
    flex-direction: column;
    min-width: 0;
    background-image: linear-gradient(to right, var(--line-soft) 1px, transparent 1px);
    background-size: 24px 100%;
  }
  .strip {
    position: relative;
    z-index: 1;
    display: flex;
    justify-content: space-between;
    padding: 16px 24px;
    border-bottom: 1px solid var(--line-soft);
  }
  .strip .led {
    margin-right: 10px;
  }
  .copy {
    position: relative;
    margin-top: 72px;
    padding: 0 80px;
  }
  .path {
    font-family: var(--mono);
    font-size: 11px;
    color: var(--mut);
    margin-bottom: 4px;
  }
  .path b {
    color: var(--acc);
  }
  h1 {
    margin: 2px 0 0;
    font-size: clamp(32px, 4.4vw, 52px);
    font-weight: 700;
    line-height: 1.08;
    letter-spacing: -0.01em;
    color: var(--strong);
  }
  h1::before {
    content: '$ ';
    color: var(--acc);
  }
  .copy p {
    max-width: 500px;
    margin: 28px 0 0;
    font-size: 14px;
    font-weight: 400;
    line-height: 1.65;
    color: var(--mut);
  }
  aside {
    position: relative;
    display: flex;
    flex-direction: column;
    justify-content: center;
    gap: 36px;
    padding: 40px 32px;
    border-left: 1px solid var(--line);
    background: var(--panel);
  }
  @media (max-width: 900px) {
    main {
      display: block;
    }
    .strip {
      padding: 12px 16px;
    }
    .copy {
      padding: 28px 24px;
    }
    h1 {
      font-size: 30px;
    }
    .copy p {
      margin-top: 14px;
      font-size: 13px;
    }
    aside {
      border-left: 0;
      border-top: 1px solid var(--line);
      padding: 28px 24px 36px;
    }
  }
</style>
