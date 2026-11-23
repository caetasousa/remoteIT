<script lang="ts">
  import { api, type AuditEntry } from '../lib/api';
  import Sequence from './layout/Sequence.svelte';

  let entries = $state<AuditEntry[]>([]);
  const open = $derived(entries.filter((e) => !e.endedAt).length);

  function duration(e: AuditEntry): string {
    if (!e.endedAt) return 'em andamento';
    const s = Math.round((Date.parse(e.endedAt) - Date.parse(e.startedAt)) / 1000);
    return s < 60 ? `${s} s` : `${Math.floor(s / 60)} min ${s % 60} s`;
  }

  api.audit().then((list) => (entries = list));
</script>

<Sequence
  seq="03"
  name="Auditoria"
  title="Tudo fica registrado."
  description="Quem acessou qual máquina, de onde e por quanto tempo. São exibidos os últimos 200 acessos."
  readings={[
    { label: 'Registros', value: entries.length, on: entries.length > 0 },
    { label: 'Em andamento', value: open, on: open > 0 },
  ]}
>
  <div class="sheet">
    <span class="tag">Acessos</span>
    <div class="scroll">
      {#if entries.length}
        <table>
          <thead><tr><th>Máquina</th><th>Técnico</th><th>Início</th><th>Duração</th><th>Origem</th></tr></thead>
          <tbody>
            {#each entries as e (e.id)}
              <tr data-testid="audit-row">
                <td class="name break">{e.host}</td>
                <td data-label="Técnico">{e.username}</td>
                <td class="mono muted" data-label="Início">{new Date(e.startedAt).toLocaleString()}</td>
                <td class="mono" class:warn={!e.endedAt} data-label="Duração">{duration(e)}</td>
                <td class="mono muted break" data-label="Origem">{e.remoteAddr}</td>
              </tr>
            {/each}
          </tbody>
        </table>
      {:else}
        <p class="empty">Nenhum acesso registrado.</p>
      {/if}
    </div>
  </div>
</Sequence>
