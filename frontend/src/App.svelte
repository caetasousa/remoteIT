<script lang="ts">
  import { api, setUnauthorizedHandler, type Machine, type User } from './lib/api';
  import Login from './components/Login.svelte';
  import AgentList from './components/AgentList.svelte';
  import Viewer from './components/Viewer.svelte';
  import Users from './components/Users.svelte';
  import Audit from './components/Audit.svelte';
  import CompleteAccount from './components/CompleteAccount.svelte';
  import ConfirmEmail from './components/ConfirmEmail.svelte';
  import LinkEmail from './components/LinkEmail.svelte';
  import Entrance from './components/layout/Entrance.svelte';
  import Frame from './components/layout/Frame.svelte';
  import ConfirmDialog from './components/layout/ConfirmDialog.svelte';
  import { takeAccountLink } from './lib/route';

  type Tab = 'agents' | 'users' | 'audit';

  let user = $state<User | null>(null);
  let loading = $state(true);
  let tab = $state<Tab>('agents');
  let viewing = $state<Machine | null>(null);

  let link = $state(takeAccountLink());
  let accounts = $state(false);
  const tabs = $derived<{ id: Tab; label: string }[]>(
    user?.role === 'admin'
      ? [
          { id: 'agents', label: 'Máquinas' },
          { id: 'users', label: 'Usuários' },
          { id: 'audit', label: 'Auditoria' },
        ]
      : [{ id: 'agents', label: 'Máquinas' }],
  );
  const entering = $derived(!user || !!link);

  setUnauthorizedHandler(() => {
    user = null;
    viewing = null;
  });

  api
    .me()
    .then((u) => (user = u))
    .catch(() => {})
    .finally(() => (loading = false));

  api
    .accountConfig()
    .then((c) => (accounts = c.accounts))
    .catch(() => {});

  // link aberto com o console já carregado na aba
  function onHashChange() {
    link = takeAccountLink() ?? link;
  }

  async function finishLink() {
    link = null;
    // o reset encerra as sessões: confere se ainda há uma
    user = await api.me().catch(() => null);
  }

  async function logout() {
    await api.logout().catch(() => {});
    user = null;
    viewing = null;
    tab = 'agents';
  }
</script>

<svelte:window onhashchange={onHashChange} />

<ConfirmDialog />

{#if loading}
  <p class="label loading">Carregando…</p>
{:else if entering}
  <!-- um só Entrance para as três telas: trocar de formulário não recria o WebGL -->
  <Entrance>
    {#if link && link.kind !== 'link'}
      {#key link}
        <CompleteAccount kind={link.kind} token={link.token} onDone={finishLink} />
      {/key}
    {:else if !user}
      <Login
        {accounts}
        notice={link ? 'Entre na sua conta para confirmar o e-mail.' : ''}
        onLogin={(u) => (user = u)}
      />
    {:else if link}
      <ConfirmEmail
        me={user}
        token={link.token}
        onDone={(u) => {
          user = u;
          link = null;
        }}
      />
    {/if}
  </Entrance>
{:else if viewing}
  <Viewer agent={viewing} onBack={() => (viewing = null)} />
{:else if user}
  <Frame>
    {#snippet nav()}
      <nav>
        {#each tabs as t (t.id)}
          <button class:active={tab === t.id} aria-current={tab === t.id ? 'page' : undefined} onclick={() => (tab = t.id)}>
            {t.label}
          </button>
        {/each}
      </nav>
    {/snippet}
    {#snippet actions()}
      <span class="mono who" data-testid="current-user">{user?.username}</span>
      <button class="pill" onclick={logout}>Sair</button>
    {/snippet}
    {#if accounts && !user.email}<LinkEmail />{/if}
    <main>
      {#if tab === 'agents'}
        <AgentList me={user} onConnect={(a) => (viewing = a)} />
      {:else if tab === 'users'}
        <Users me={user} />
      {:else}
        <Audit />
      {/if}
    </main>
  </Frame>
{/if}

<style>
  .loading {
    padding: 32px 24px;
  }
  main {
    flex: 1;
    display: flex;
    flex-direction: column;
  }
  nav {
    display: flex;
    gap: 8px;
  }
  nav button {
    border-color: transparent;
    color: var(--mut);
    font-size: 12px;
  }
  nav button.active {
    color: var(--acc);
    border-color: var(--acc-line);
    background: var(--acc-wash);
  }
  .who {
    color: var(--mut);
    min-width: 0;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  @media (max-width: 420px) {
    nav {
      gap: 2px;
    }
    nav button {
      padding-inline: 10px;
    }
  }
</style>
