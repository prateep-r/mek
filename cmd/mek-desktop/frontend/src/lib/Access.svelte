<script>
  let { ctx, act, open } = $props();
</script>

{#if !ctx}
  <p class="empty">Choose a context.</p>
{:else}
  <section>
    <div class="bar"><h3>Targets</h3><button onclick={() => open("addEntry", ctx.Name, "target")}>Add a target</button></div>
    {#if (ctx.Targets || []).length === 0}
      <p class="empty">No targets.</p>
    {:else}
      <table>
        <thead><tr><th>Name</th><th>Instance</th><th></th></tr></thead>
        <tbody>
          {#each ctx.Targets as t (t.Name)}
            <tr><td>{t.Name}</td><td class="mono muted">{t.Instance}</td>
              <td class="actions"><button onclick={() => open("shell", ctx.Name, t.Name)}>Shell</button></td></tr>
          {/each}
        </tbody>
      </table>
    {/if}
  </section>
  <section>
    <div class="bar"><h3>Clusters</h3><button onclick={() => open("addEntry", ctx.Name, "cluster")}>Add a cluster</button></div>
    {#if (ctx.Clusters || []).length === 0}
      <p class="empty">No clusters.</p>
    {:else}
      <table>
        <thead><tr><th>Alias</th><th>Cluster</th><th></th></tr></thead>
        <tbody>
          {#each ctx.Clusters as c (c.Alias)}
            <tr><td>{c.Alias}</td><td class="mono muted">{c.Name}</td>
              <td class="actions">
                <button onclick={() => act("kubeconfig", ctx.Name, c.Alias)}>Write kubeconfig</button>
                <button onclick={() => open("k9s", ctx.Name, c.Alias)}>K9s</button>
              </td></tr>
          {/each}
        </tbody>
      </table>
    {/if}
  </section>
{/if}

<style>
  section { margin-bottom: 20px; }
  h3 { margin: 0; font-size: 14px; }
  .bar { display: flex; justify-content: space-between; align-items: center; margin-bottom: 6px; }
  .actions { display: flex; gap: 6px; justify-content: flex-end; }
</style>
