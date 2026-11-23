<script lang="ts">
  // Moldura de 1280px no estilo terminal: barra de título com os três pontos
  // (como uma janela de shell) e o cabeçalho de navegação logo abaixo.
  import type { Snippet } from 'svelte';
  import Brand from './Brand.svelte';

  let { nav, actions, children }: { nav?: Snippet; actions?: Snippet; children: Snippet } = $props();
</script>

<div class="frame">
  <div class="titlebar">
    <span class="path"><b>root@relay</b>:~/console$</span>
    <span class="dots"><i class="d1"></i><i class="d2"></i><i class="d3"></i></span>
  </div>
  <header>
    <div class="cell brand"><Brand /></div>
    <div class="cell middle" class:blank={!nav}>{@render nav?.()}</div>
    <div class="cell end">{@render actions?.()}</div>
  </header>
  {@render children()}
</div>

<style>
  .frame {
    position: relative;
    max-width: var(--width);
    min-height: 100vh;
    min-height: 100dvh;
    margin: 0 auto;
    display: flex;
    flex-direction: column;
    border-inline: 1px solid var(--line-strong);
    background: var(--bg);
  }
  @media (max-width: 1290px) {
    .frame {
      overflow-x: clip;
    }
  }
  .titlebar {
    display: flex;
    justify-content: space-between;
    align-items: center;
    padding: 8px 16px;
    background: var(--panel);
    border-bottom: 1px solid var(--line);
    font-size: 11px;
    color: var(--mut);
    letter-spacing: 0.08em;
  }
  .titlebar .path b {
    color: var(--acc);
  }
  .dots {
    display: flex;
    gap: 5px;
  }
  .dots i {
    display: block;
    width: 7px;
    height: 7px;
    border-radius: 50%;
  }
  .dots .d1 {
    background: var(--bad);
  }
  .dots .d2 {
    background: var(--warn);
  }
  .dots .d3 {
    background: var(--ok);
  }
  header {
    position: relative;
    z-index: 2;
    display: grid;
    grid-template-columns: 1fr 2fr 1fr;
    min-height: 56px;
    border-bottom: 1px solid var(--line-strong);
    background: var(--bg);
  }
  .cell {
    display: flex;
    align-items: center;
    min-width: 0;
    padding: 10px 24px;
  }
  .brand {
    border-right: 1px solid var(--line-soft);
  }
  .middle {
    justify-content: center;
  }
  .end {
    justify-content: flex-end;
    gap: 16px;
    border-left: 1px solid var(--line-soft);
  }
  @media (max-width: 760px) {
    header {
      grid-template-columns: auto 1fr;
      min-height: 52px;
    }
    .cell {
      padding: 10px 16px;
    }
    .brand,
    .end {
      border: 0;
    }
    .end {
      grid-column: 2;
      grid-row: 1;
      gap: 10px;
    }
    .middle {
      grid-column: 1 / -1;
      grid-row: 2;
      justify-content: flex-start;
      border-top: 1px solid var(--line-soft);
    }
    .middle.blank {
      display: none;
    }
  }
</style>
