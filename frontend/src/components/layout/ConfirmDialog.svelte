<script lang="ts">
  // Modal de confirmação do console (veja lib/confirm.svelte.ts). Usa o
  // <dialog> nativo: showModal() prende o foco, deixa o resto da página
  // inerte e fica por cima até da tela remota em tela cheia; Esc cancela.
  import { answer, confirmState } from '../../lib/confirm.svelte';

  let el: HTMLDialogElement;
  let cancelBtn = $state<HTMLButtonElement>();
  let okBtn = $state<HTMLButtonElement>();
  const req = $derived(confirmState.current);

  $effect(() => {
    if (req && !el.open) {
      el.showModal();
      // destrutivo: Enter não pode confirmar sem querer
      (req.danger ? cancelBtn : okBtn)?.focus();
    } else if (!req && el.open) {
      el.close();
    }
  });
</script>

<dialog
  bind:this={el}
  aria-labelledby="confirm-title"
  aria-describedby="confirm-message"
  oncancel={(e) => {
    e.preventDefault(); // Esc: fecha pelo answer, para resolver a promessa
    answer(false);
  }}
  onclick={(e) => {
    if (e.target === el) answer(false); // clique fora do cartão
  }}
>
  {#if req}
    <div class="card" class:danger={req.danger}>
      <h2 id="confirm-title">{req.title}</h2>
      {#if req.message}<p id="confirm-message">{req.message}</p>{/if}
      <div class="actions">
        <button type="button" bind:this={cancelBtn} onclick={() => answer(false)}>Cancelar</button>
        <button type="button" class="ok" bind:this={okBtn} onclick={() => answer(true)}>
          {req.confirm ?? 'Confirmar'}
        </button>
      </div>
    </div>
  {/if}
</dialog>

<style>
  dialog {
    padding: 0;
    border: none;
    background: transparent;
    color: var(--fg);
    max-width: min(440px, calc(100vw - 32px));
    width: 100%;
  }
  dialog::backdrop {
    background: rgb(2 4 8 / 0.72);
    backdrop-filter: blur(2px);
  }
  dialog[open] .card {
    animation: rise 180ms var(--ease);
  }
  .card {
    background: var(--panel);
    border: 1px solid var(--line-strong);
    border-left: 3px solid var(--acc);
    padding: 20px 22px 18px;
  }
  .card.danger {
    border-left-color: var(--bad);
  }
  h2 {
    margin: 0 0 8px;
    font-size: 14px;
    font-weight: 600;
    color: var(--strong);
  }
  p {
    margin: 0 0 18px;
    color: var(--fg);
    line-height: 1.6;
  }
  .actions {
    display: flex;
    justify-content: flex-end;
    gap: 8px;
  }
  .ok {
    color: var(--bg);
    background: var(--acc);
    border-color: var(--acc);
    font-weight: 600;
  }
  .danger .ok {
    background: var(--bad);
    border-color: var(--bad);
  }
  @keyframes rise {
    from {
      opacity: 0;
      transform: translateY(6px);
    }
  }
  @media (prefers-reduced-motion: reduce) {
    dialog[open] .card {
      animation: none;
    }
  }
</style>
