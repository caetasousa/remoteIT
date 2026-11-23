<script lang="ts">
  import { api, type Role, type User } from '../lib/api';
  import Sequence from './layout/Sequence.svelte';

  let { me }: { me: User } = $props();

  let users = $state<User[]>([]);
  let username = $state('');
  let password = $state('');
  let role = $state<Role>('technician');
  let error = $state('');

  const pending = $derived(users.filter((u) => u.pending).length);
  const roleLabel: Record<Role, string> = { admin: 'Administrador', technician: 'Técnico' };

  async function load() {
    users = await api.users();
  }

  async function create(e: SubmitEvent) {
    e.preventDefault();
    error = '';
    try {
      await api.createUser(username, password, role);
      username = password = '';
      role = 'technician';
      await load();
    } catch (err) {
      error = err instanceof Error ? err.message : 'falha ao cadastrar';
    }
  }

  async function approve(u: User) {
    if (!confirm(`Aprovar ${u.username} (${u.email})? A conta poderá acessar todas as máquinas.`)) return;
    error = '';
    try {
      await api.approveUser(u.id);
      await load();
    } catch (err) {
      error = err instanceof Error ? err.message : 'falha ao aprovar';
    }
  }

  async function remove(u: User) {
    if (!confirm(`Remover o usuário ${u.username}?`)) return;
    error = '';
    try {
      await api.deleteUser(u.id);
      await load();
    } catch (err) {
      error = err instanceof Error ? err.message : 'falha ao remover';
    }
  }

  load();
</script>

<Sequence
  seq="02"
  name="Usuários"
  title="Quem tem acesso."
  description="Uma conta ativa acessa todas as máquinas. Aprove só quem você conhece."
  readings={[
    { label: 'Ativos', value: users.length - pending, on: users.length > pending },
    { label: 'Aguardando', value: pending, on: pending > 0 },
  ]}
>
  <div class="sheet">
    <span class="tag">Nova conta</span>
    <form onsubmit={create}>
      <input name="new-username" placeholder="usuário" bind:value={username} autocomplete="off" required />
      <input
        name="new-password"
        type="password"
        placeholder="senha (mín. 8)"
        bind:value={password}
        autocomplete="new-password"
        required
      />
      <select name="new-role" aria-label="Papel" bind:value={role}>
        <option value="technician">Técnico</option>
        <option value="admin">Administrador</option>
      </select>
      <button class="primary" type="submit">Cadastrar</button>
    </form>
    {#if error}<p class="error failure" role="alert">{error}</p>{/if}
  </div>

  <div class="sheet">
    <span class="tag">Contas</span>
    <div class="scroll">
      <table>
        <thead><tr><th>Conta</th><th>Papel</th><th>Situação</th><th>Criado em</th><th></th></tr></thead>
        <tbody>
          {#each users as u (u.id)}
            <tr data-testid="user-row">
              <td class="name break">
                {u.username}
                <span class="mono email">{u.email ?? 'sem e-mail'}</span>
              </td>
              <td data-label="Papel">{roleLabel[u.role]}</td>
              <td class="mono" class:warn={u.pending} data-label="Situação">
                {u.pending ? 'Aguardando aprovação' : 'Ativo'}
              </td>
              <td class="mono muted" data-label="Criado em">{new Date(u.createdAt).toLocaleString()}</td>
              <td class="actions">
                <div>
                  {#if u.pending}<button class="primary" onclick={() => approve(u)}>Aprovar</button>{/if}
                  {#if u.id !== me.id}<button class="danger" onclick={() => remove(u)}>Remover</button>{/if}
                </div>
              </td>
            </tr>
          {/each}
        </tbody>
      </table>
    </div>
  </div>
</Sequence>

<style>
  form {
    display: flex;
    gap: 8px;
    flex-wrap: wrap;
    padding: 26px 16px 16px;
  }
  form input,
  form select {
    flex: 1 1 160px;
  }
  .failure {
    margin: 0;
    padding: 0 16px 16px;
  }
  .email {
    display: block;
    color: var(--dim);
  }
</style>
