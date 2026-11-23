<script lang="ts">
  import { api } from '../lib/api';

  let { mode, onBack }: { mode: 'signup' | 'reset'; onBack: () => void } = $props();

  let email = $state('');
  let error = $state('');
  let busy = $state(false);
  let sent = $state(false);

  async function submit(e: SubmitEvent) {
    e.preventDefault();
    busy = true;
    error = '';
    try {
      await (mode === 'signup' ? api.requestSignup(email) : api.requestReset(email));
      sent = true;
    } catch (err) {
      error = err instanceof Error ? err.message : 'falha ao enviar';
    } finally {
      busy = false;
    }
  }
</script>

<form class="auth" onsubmit={submit}>
  <h2>{mode === 'signup' ? 'Criar conta' : 'Recuperar conta'}</h2>
  {#if sent}
    <p role="status">
      {mode === 'signup'
        ? 'Se o e-mail puder ser usado, você vai receber um link em instantes.'
        : 'Se houver uma conta com esse e-mail, você vai receber um link em instantes.'}
      Confira também o spam.
    </p>
  {:else}
    <p class="muted">
      {mode === 'signup'
        ? 'Você escolhe usuário e senha pelo link do e-mail. Depois, um administrador aprova a conta.'
        : 'Enviamos seu nome de usuário e um link para escolher outra senha.'}
    </p>
    <label>E-mail <input name="email" type="email" bind:value={email} autocomplete="email" required /></label>
    {#if error}<p class="error" role="alert">{error}</p>{/if}
    <button class="primary" type="submit" disabled={busy}>Enviar link</button>
  {/if}
  <button class="link" type="button" onclick={onBack}>Voltar ao login</button>
</form>
