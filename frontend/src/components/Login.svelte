<script lang="ts">
  import { api, type User } from '../lib/api';
  import EmailRequest from './EmailRequest.svelte';

  let { accounts, notice, onLogin }: { accounts: boolean; notice: string; onLogin: (u: User) => void } = $props();

  let mode = $state<'login' | 'signup' | 'reset'>('login');

  let username = $state('');
  let password = $state('');
  let error = $state('');
  let busy = $state(false);

  async function submit(e: SubmitEvent) {
    e.preventDefault();
    busy = true;
    error = '';
    try {
      onLogin(await api.login(username, password));
    } catch (err) {
      error = err instanceof Error ? err.message : 'falha no login';
    } finally {
      busy = false;
    }
  }
</script>

{#if mode === 'login'}
  <form class="auth" onsubmit={submit}>
    <h2>Entrar no console</h2>
    {#if notice}<p role="status">{notice}</p>{/if}
    <label>
      {accounts ? 'Usuário ou e-mail' : 'Usuário'}
      <input name="username" bind:value={username} autocomplete="username" autocapitalize="none" required />
    </label>
    <label>
      Senha <input name="password" type="password" bind:value={password} autocomplete="current-password" required />
    </label>
    {#if error}<p class="error" role="alert">{error}</p>{/if}
    <button class="primary" type="submit" disabled={busy}>Entrar</button>
    {#if accounts}
      <div class="options">
        <button class="link" type="button" onclick={() => (mode = 'signup')}>Criar conta</button>
        <button class="link" type="button" onclick={() => (mode = 'reset')}>Esqueci meu acesso</button>
      </div>
    {/if}
  </form>
{:else}
  <EmailRequest {mode} onBack={() => (mode = 'login')} />
{/if}

<style>
  .options {
    display: flex;
    justify-content: space-between;
    gap: 4px 12px;
    flex-wrap: wrap;
  }
</style>
