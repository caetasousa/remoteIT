<script lang="ts">
  // Uma seção do console: trilho com título e leituras (1/4) e área de
  // trabalho (3/4), no estilo "terminal" ($ comando).
  import type { Snippet } from 'svelte';

  let {
    seq,
    name,
    title,
    description,
    readings,
    aside,
    children,
  }: {
    seq: string;
    name: string;
    title: string;
    description: string;
    readings: { label: string; value: string | number; on?: boolean }[];
    aside?: Snippet;
    children: Snippet;
  } = $props();
</script>

<div class="sequence">
  <aside>
    <div class="path">/console/<b>{name.toLowerCase()}</b></div>
    <h1>{title}</h1>
    <p>{description}</p>
    <dl class="readout">
      {#each readings as r (r.label)}
        <div>
          <dt><span class="led" class:on={r.on}></span>{r.label}</dt>
          <dd>{r.value}</dd>
        </div>
      {/each}
    </dl>
    {@render aside?.()}
  </aside>
  <div class="work">{@render children()}</div>
</div>

<style>
  .sequence {
    flex: 1;
    display: grid;
    grid-template-columns: minmax(0, 1fr) minmax(0, 3fr);
  }
  aside {
    display: flex;
    flex-direction: column;
    gap: 20px;
    padding: 28px;
    border-right: 1px solid var(--line-strong);
    background: var(--panel);
  }
  .path {
    font-family: var(--mono);
    font-size: 11px;
    color: var(--mut);
  }
  .path b {
    color: var(--acc);
  }
  h1 {
    margin: 4px 0 0;
    font-size: 22px;
    font-weight: 700;
    line-height: 1.2;
    letter-spacing: -0.01em;
    color: var(--strong);
  }
  h1::before {
    content: '$ ';
    color: var(--acc);
  }
  p {
    margin: 0;
    font-size: 12px;
    font-weight: 400;
    line-height: 1.6;
    color: var(--mut);
  }
  .work {
    display: flex;
    flex-direction: column;
    gap: 24px;
    padding: 28px 24px;
    background: var(--bg);
  }
  @media (max-width: 1100px) {
    .sequence {
      display: block;
    }
    aside {
      border-right: 0;
      border-bottom: 1px solid var(--line-strong);
      padding: 20px;
      gap: 14px;
    }
    h1 {
      font-size: 19px;
    }
    .work {
      padding: 20px 14px;
    }
  }
</style>
