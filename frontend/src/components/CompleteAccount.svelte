<script lang="ts">
  import { api } from '../lib/api';

  let { kind, token, onDone }: { kind: 'signup' | 'reset'; token: string; onDone: () => void } = $props();

  let username = $state('');
  let password = $state('');
  let confirmation = $state('');
  let error = $state('');
  let success = $state('');
  let busy = $state(false);

  async function submit(e: SubmitEvent) {
    e.preventDefault();
    error = '';
    if (password !== confirmation) {
      error = 'as senhas não são iguais';
      return;
    }
    busy = true;
    try {
      if (kind === 'signup') {
        const u = await api.completeSignup(token, username, password);
        success = u.pending ? 'Conta criada. Aguarde a aprovação de um administrador.' : 'Conta de administrador criada.';
      } else {
        await api.completeReset(token, password);
        success = 'Senha alterada. As sessões abertas foram encerradas.';
      }
    } catch (err) {
      error = err instanceof Error ? err.message : 'falha ao salvar';
    } finally {
      busy = false;
    }
  }
</script>

<form class="auth" onsubmit={submit}>
  <h2>{kind === 'signup' ? 'Concluir cadastro' : 'Nova senha'}</h2>
  {#if success}
    <p role="status">{success}</p>
  {:else}
    {#if kind === 'signup'}
      <label>
        Nome de usuário
        <input
          name="username"
          bind:value={username}
          autocomplete="username"
          autocapitalize="none"
          minlength="3"
          maxlength="32"
          placeholder="ex.: maria.silva"
          required
        />
      </label>
    {/if}
    <label>
      Senha (mín. 8)
      <input name="password" type="password" bind:value={password} autocomplete="new-password" minlength="8" required />
    </label>
    <label>
      Repita a senha
      <input name="confirmation" type="password" bind:value={confirmation} autocomplete="new-password" required />
    </label>
    {#if error}<p class="error" role="alert">{error}</p>{/if}
    <button class="primary" type="submit" disabled={busy}>Salvar</button>
  {/if}
  <button class="link" type="button" onclick={onDone}>Ir para o login</button>
</form>
