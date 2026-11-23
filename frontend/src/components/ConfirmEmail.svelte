<script lang="ts">
  import { api, type User } from '../lib/api';

  let { me, token, onDone }: { me: User; token: string; onDone: (u: User) => void } = $props();

  let error = $state('');
  let busy = $state(false);

  async function submit(e: SubmitEvent) {
    e.preventDefault();
    busy = true;
    error = '';
    try {
      onDone(await api.completeLink(token));
    } catch (err) {
      error = err instanceof Error ? err.message : 'falha ao confirmar';
    } finally {
      busy = false;
    }
  }
</script>

<form class="auth" onsubmit={submit}>
  <h2>Confirmar e-mail</h2>
  <p>O e-mail será usado para recuperar a conta <strong>{me.username}</strong>.</p>
  {#if error}<p class="error" role="alert">{error}</p>{/if}
  <button class="primary" type="submit" disabled={busy}>Confirmar</button>
  <button class="link" type="button" onclick={() => onDone(me)}>Voltar ao console</button>
</form>
