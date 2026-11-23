<script lang="ts">
  // Contas criadas pelo admin, ou antes do e-mail existir, não têm como se
  // recuperar até associarem um endereço.
  import { api } from '../lib/api';

  let open = $state(false);
  let email = $state('');
  let password = $state('');
  let error = $state('');
  let busy = $state(false);
  let sent = $state(false);

  async function submit(e: SubmitEvent) {
    e.preventDefault();
    busy = true;
    error = '';
    try {
      await api.requestLink(email, password);
      sent = true;
      password = '';
    } catch (err) {
      error = err instanceof Error ? err.message : 'falha ao enviar';
    } finally {
      busy = false;
    }
  }
</script>

<aside>
  {#if sent}
    <span role="status">Se o e-mail estiver livre, o link chega em instantes. Abra-o neste navegador.</span>
  {:else}
    <span>Sua conta não tem e-mail para recuperação.</span>
    {#if open}
      <form onsubmit={submit}>
        <input name="link-email" type="email" placeholder="seu e-mail" bind:value={email} required />
        <input
          name="link-password"
          type="password"
          placeholder="senha atual"
          bind:value={password}
          autocomplete="current-password"
          required
        />
        <button class="primary" type="submit" disabled={busy}>Enviar confirmação</button>
      </form>
      {#if error}<span class="error" role="alert">{error}</span>{/if}
    {:else}
      <button onclick={() => (open = true)}>Adicionar e-mail</button>
    {/if}
  {/if}
</aside>

<style>
  aside,
  form {
    display: flex;
    gap: 8px 12px;
    align-items: center;
    flex-wrap: wrap;
  }
  aside {
    padding: 10px 24px;
    border-bottom: 1px solid var(--line);
    background: var(--acc-wash);
    color: var(--acc);
    font-size: 12px;
  }
  aside button,
  input {
    padding: 6px 10px;
  }
</style>
