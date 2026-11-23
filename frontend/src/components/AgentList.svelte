<script lang="ts">
  import { api, type AgentInfo } from '../lib/api';
  import Sequence from './layout/Sequence.svelte';

  let { onConnect }: { onConnect: (a: AgentInfo) => void } = $props();

  let agents = $state<AgentInfo[]>([]);
  let loaded = $state(false);
  const busy = $derived(agents.filter((a) => a.busy).length);

  async function refresh() {
    try {
      agents = await api.agents();
      loaded = true;
    } catch {
      // 401 é tratado pelo App; outros erros: tenta de novo no próximo ciclo
    }
  }

  $effect(() => {
    refresh();
    const t = setInterval(refresh, 3000);
    return () => clearInterval(t);
  });
</script>

<Sequence
  seq="01"
  name="Máquinas"
  title="Máquinas online."
  description="Os PCs com o agente aberto aparecem aqui. Cada máquina atende um técnico por vez."
  readings={[
    { label: 'Online', value: loaded ? agents.length : '…', on: agents.length > 0 },
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
    </div>
  {/snippet}
  <div class="sheet">
    <span class="tag">Conectadas agora</span>
    <div class="scroll">
      {#if agents.length}
        <table>
          <thead><tr><th>Computador</th><th>Usuário</th><th>Online desde</th><th></th></tr></thead>
          <tbody>
            {#each agents as a (a.id)}
              <tr class:busy={a.busy} data-testid="agent-row">
                <td class="name"><span class="led on"></span>{a.host}</td>
                <td data-label="Usuário">{a.user}</td>
                <td class="mono muted" data-label="Online desde">{new Date(a.since).toLocaleString()}</td>
                <td class="actions">
                  <div>
                    {#if a.viewedBy}<span class="mono warn">em uso por {a.viewedBy}</span>{/if}
                    <button class="primary" onclick={() => onConnect(a)}>{a.busy ? 'Assumir sessão' : 'Conectar'}</button>
                  </div>
                </td>
              </tr>
            {/each}
          </tbody>
        </table>
      {:else}
        <p class="empty">
          {loaded ? 'Nenhum computador online. Peça ao usuário para executar o agente.' : 'Procurando máquinas…'}
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
  .downloads small {
    color: var(--mut);
    font-size: 11px;
    margin: 0 0 8px 18px;
  }
  .download {
    display: flex;
    justify-content: space-between;
    gap: 12px;
    padding: 10px 18px;
  }
  .name .led {
    margin-right: 10px;
  }
  .busy .name .led {
    animation: pulse 2s ease-in-out infinite;
  }
  @keyframes pulse {
    50% {
      opacity: 0.35;
    }
  }
</style>
