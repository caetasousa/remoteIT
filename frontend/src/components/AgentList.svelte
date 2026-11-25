<script lang="ts">
  import { api, type Edition, type InstallLink, type Machine, type User } from '../lib/api';
  import Sequence from './layout/Sequence.svelte';

  let { me, onConnect }: { me: User; onConnect: (m: Machine) => void } = $props();

  let machines = $state<Machine[]>([]);
  let loaded = $state(false);
  let error = $state('');
  const online = $derived(machines.filter((m) => m.online).length);
  const busy = $derived(machines.filter((m) => m.busy).length);

  // links de instalação
  let links = $state<InstallLink[]>([]);
  let edition = $state<Edition>('portatil');
  let hours = $state(24);
  let uses = $state(1);
  let created = $state(''); // URL do último link criado (o código só aparece agora)
  let copied = $state(false);

  async function refresh() {
    try {
      machines = await api.machines();
      loaded = true;
    } catch {
      // 401 é tratado pelo App; outros erros: tenta de novo no próximo ciclo
    }
  }

  async function loadLinks() {
    try {
      links = await api.links();
    } catch {
      links = [];
    }
  }

  $effect(() => {
    refresh();
    loadLinks();
    const t = setInterval(refresh, 3000);
    return () => clearInterval(t);
  });

  async function run(action: () => Promise<unknown>) {
    error = '';
    try {
      await action();
      await refresh();
    } catch (err) {
      error = err instanceof Error ? err.message : 'falhou';
    }
  }

  function remove(m: Machine) {
    const msg = m.legacy
      ? `Remover ${m.name}? O agente antigo dessa máquina deixa de ser aceito.`
      : `Remover ${m.name}? O agente dela é desconectado e não volta sozinho; para usar de novo, instale um agente novo.`;
    if (confirm(msg)) run(() => api.removeMachine(m.id));
  }

  async function createLink(e: SubmitEvent) {
    e.preventDefault();
    error = '';
    copied = false;
    try {
      const l = await api.createLink(edition, hours, uses);
      created = `${location.origin}${l.path}`;
      await loadLinks();
    } catch (err) {
      error = err instanceof Error ? err.message : 'falha ao criar o link';
    }
  }

  async function copy() {
    await navigator.clipboard.writeText(created);
    copied = true;
  }

  async function revoke(l: InstallLink) {
    if (!confirm('Revogar este link? Quem ainda não instalou não consegue mais baixar.')) return;
    await api.revokeLink(l.id).catch(() => {});
    await loadLinks();
  }

  const when = (iso?: string) => (iso ? new Date(iso).toLocaleString() : '—');
  // o relay guarda "sem prazo" e "sem limite" como valores que nunca são alcançados
  const forever = (iso: string) => new Date(iso).getFullYear() > 2100;
  const unlimited = (n: number) => n >= 1_000_000;
  const editionLabel: Record<Edition, string> = { portatil: 'Executar', servico: 'Serviço' };
</script>

<Sequence
  seq="01"
  name="Máquinas"
  title={me.role === 'admin' ? 'Todas as máquinas.' : 'Suas máquinas.'}
  description={me.role === 'admin'
    ? 'Você vê todas. Cada técnico vê só as máquinas dos agentes e links dele.'
    : 'O agente que você baixa (ou manda por link) já sai no seu nome: só você e os administradores veem essas máquinas.'}
  readings={[
    { label: 'Online', value: loaded ? online : '…', on: online > 0 },
    { label: 'Em uso', value: loaded ? busy : '…', on: busy > 0 },
  ]}
>
  {#snippet aside()}
    <!-- ?t= muda a cada carregamento: nenhum cache entrega um agente antigo -->
    <div class="downloads">
      <a class="btn pill download" href={`/download/agent.exe?t=${Date.now()}`}>
        Executar agente (.exe) <span aria-hidden="true">↓</span>
      </a>
      <small>Dois cliques e roda. Sem instalar, sem admin.</small>
      <a class="btn pill download" href={`/download/agent-servico.exe?t=${Date.now()}`}>
        Instalar como serviço (.exe) <span aria-hidden="true">↓</span>
      </a>
      <small>Acesso antes do login e na tela de bloqueio. Pede admin.</small>
      <a class="btn pill download viewer" href={`/download/viewer.exe?t=${Date.now()}`}>
        Visualizador do técnico (Windows) <span aria-hidden="true">↓</span>
      </a>
      <small>Para você: tela mais fluida e todas as teclas (Win, Alt+Tab) sem tela cheia.</small>
    </div>

    <form class="link-form" onsubmit={createLink} aria-label="Link de instalação">
      <span class="tag">Link de instalação</span>
      <small>O cliente baixa sem conta. As máquinas ficam no seu nome.</small>
      <select aria-label="Edição" bind:value={edition}>
        <option value="portatil">Executar (portátil)</option>
        <option value="servico">Instalar como serviço</option>
      </select>
      <div class="row">
        <select aria-label="Validade" bind:value={hours}>
          <option value={1}>1 hora</option>
          <option value={24}>24 horas</option>
          <option value={168}>7 dias</option>
          <option value={0}>sem prazo</option>
        </select>
        <select aria-label="Máquinas" bind:value={uses}>
          <option value={1}>1 máquina</option>
          <option value={5}>até 5</option>
          <option value={20}>até 20</option>
          <option value={100}>até 100</option>
          <option value={0}>sem limite</option>
        </select>
      </div>
      <button class="pill" type="submit">Gerar link</button>
      {#if created}
        <div class="created" data-testid="install-link">
          <input readonly value={created} aria-label="Link gerado" onfocus={(e) => e.currentTarget.select()} />
          <button type="button" onclick={copy}>{copied ? 'Copiado' : 'Copiar'}</button>
        </div>
        <small>Copie agora: por segurança, o link não aparece de novo.</small>
      {/if}
      {#if links.length}
        <ul class="links">
          {#each links as l (l.id)}
            <li>
              <span class="mono">
                {editionLabel[l.edition]} · {l.uses}{unlimited(l.maxUses) ? ' máq.' : `/${l.maxUses}`} ·
                {forever(l.expiresAt) ? 'sem prazo' : `até ${when(l.expiresAt)}`}
              </span>
              <button type="button" class="link" onclick={() => revoke(l)}>revogar</button>
            </li>
          {/each}
        </ul>
      {/if}
    </form>
  {/snippet}

  <div class="sheet">
    <span class="tag">{me.role === 'admin' ? 'Máquinas' : 'Minhas máquinas'}</span>
    {#if error}<p class="error" role="alert">{error}</p>{/if}
    <div class="scroll">
      {#if machines.length}
        <table>
          <thead>
            <tr><th>Computador</th><th>Dono</th><th>Situação</th><th></th></tr>
          </thead>
          <tbody>
            {#each machines as m (m.id)}
              <tr class:busy={m.busy} class:off={!m.online} data-testid="agent-row">
                <td class="name">
                  <span class="led" class:on={m.online}></span>{m.name}
                  {#if m.legacy}<span class="badge" title="Agente antigo, sem cadastro: reinstale com um agente novo">antigo</span>{/if}
                </td>
                <td data-label="Dono">{m.mine ? 'você' : (m.owner ?? '—')}</td>
                <td class="mono muted" data-label="Situação">
                  {#if m.online}
                    {m.user ? `${m.user} · ` : ''}desde {when(m.since)}
                    <span class="ver" title="Versão do agente">{m.version ? `v${m.version}` : 'sem versão'}</span>
                  {:else}
                    offline · visto {when(m.lastSeenAt)}
                  {/if}
                </td>
                <td class="actions">
                  <div>
                    {#if m.viewedBy}<span class="mono warn">em uso por {m.viewedBy}</span>{/if}
                    {#if m.canManage}
                      <button class="danger" onclick={() => remove(m)}>Remover</button>
                    {/if}
                    {#if m.online}
                      <button class="primary" onclick={() => onConnect(m)}>{m.busy ? 'Assumir sessão' : 'Conectar'}</button>
                    {/if}
                  </div>
                </td>
              </tr>
            {/each}
          </tbody>
        </table>
      {:else}
        <p class="empty">
          {loaded
            ? 'Nenhuma máquina ainda. Baixe o agente (ou gere um link) e rode no computador a atender.'
            : 'Procurando máquinas…'}
        </p>
      {/if}
    </div>
  </div>
</Sequence>

<style>
  .downloads {
    display: flex;
    flex-direction: column;
    gap: 6px;
  }
  small {
    color: var(--mut);
    font-size: 11px;
  }
  .downloads small {
    margin: 0 0 8px 18px;
  }
  .viewer {
    margin-top: 8px;
    border-style: dashed;
  }
  .download {
    display: flex;
    justify-content: space-between;
    gap: 12px;
    padding: 10px 18px;
  }
  .link-form {
    display: flex;
    flex-direction: column;
    gap: 8px;
    margin-top: 18px;
  }
  .link-form .row {
    display: flex;
    gap: 8px;
  }
  .link-form .row select {
    flex: 1;
  }
  .created {
    display: flex;
    gap: 6px;
  }
  .created input {
    flex: 1;
    font-size: 11px;
  }
  .links {
    list-style: none;
    margin: 4px 0 0;
    padding: 0;
    display: flex;
    flex-direction: column;
    gap: 4px;
    font-size: 11px;
  }
  .links li {
    display: flex;
    justify-content: space-between;
    gap: 8px;
  }
  .name .led {
    margin-right: 10px;
  }
  .off .name {
    color: var(--mut);
  }
  .busy .name .led {
    animation: pulse 2s ease-in-out infinite;
  }
  .badge {
    margin-left: 8px;
    padding: 1px 6px;
    border: 1px solid var(--line-strong);
    font-size: 10px;
    color: var(--mut);
    text-transform: uppercase;
  }
  .error {
    color: var(--bad);
  }
  .ver {
    display: block;
    font-size: 10px;
    color: var(--dim);
  }
  @keyframes pulse {
    50% {
      opacity: 0.35;
    }
  }
</style>
