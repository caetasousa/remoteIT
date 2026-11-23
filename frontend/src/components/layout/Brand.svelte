<script lang="ts">
  // Marca do RemoteIT: um prompt de shell. `compact` mostra só o símbolo,
  // para barras onde o nome por extenso disputaria espaço.
  let { compact = false }: { compact?: boolean } = $props();
</script>

<span class="brand" class:compact>
  <span class="prompt">&gt;</span>
  {#if !compact}RemoteIT{/if}
</span>

<style>
  .brand {
    display: inline-flex;
    align-items: center;
    gap: 10px;
    color: var(--strong);
    font-weight: 700;
    letter-spacing: 0.14em;
    text-transform: uppercase;
  }
  .prompt {
    color: var(--acc);
    font-weight: 700;
  }
</style>
