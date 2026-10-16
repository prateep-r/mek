<script>
  import { stateTone } from "./logic.js";
  let { tunnels, selected, act, open, show } = $props();
  let log = $state(null);
</script>

<div class="bar">
  <span class="muted">Every context's tunnels, and the named ones not running.</span>
  <button disabled={!selected} onclick={() => open("addEntry", selected, "tunnel")}>Add a tunnel to {selected || "…"}</button>
</div>
{#if tunnels.length === 0}
  <p class="empty">No tunnels — add one, or name one under a context's tunnels:.</p>
{:else}
  <table>
    <thead><tr><th>Context</th><th>Name</th><th>Local</th><th>State</th><th>Age</th><th>Target</th><th></th></tr></thead>
    <tbody>
      {#each tunnels as t (t.ID || t.Context + "/" + t.Name)}
        <tr>
          <td>{t.Context}</td>
          <td>{t.Name || "-"}</td>
          <td class="mono">{t.Local}</td>
          <td class={stateTone(t.State)}>{t.State}{t.Background ? "" : t.Named ? "" : " (foreground)"}{t.Restarts ? ` ↻${t.Restarts}` : ""}</td>
          <td class="muted">{t.Age}</td>
          <td class="muted mono">{t.Target}</td>
          <td class="actions">
            {#if t.Named}
              <button onclick={() => act("startTunnel", t.Context, t.Name)}>Start</button>
            {:else}
              {#if t.Background}<button onclick={async () => (log = { id: t.ID, text: await show(t.ID) })}>Log</button>{/if}
              {#if t.Running}<button class="danger" onclick={() => act("stopTunnel", t.ID)}>Stop</button>{/if}
            {/if}
          </td>
        </tr>
      {/each}
    </tbody>
  </table>
{/if}
{#if log}
  <div class="log">
    <div class="bar"><b>Log of {log.id}</b><button onclick={() => (log = null)}>Close</button></div>
    <pre class="mono">{log.text || "(empty)"}</pre>
  </div>
{/if}

<style>
  .bar { display: flex; justify-content: space-between; align-items: center; gap: 8px; margin-bottom: 8px; }
  .actions { display: flex; gap: 6px; justify-content: flex-end; }
  .log { margin-top: 16px; border-top: 1px solid var(--line); padding-top: 8px; }
  pre { white-space: pre-wrap; max-height: 280px; overflow: auto; background: var(--panel); padding: 8px; border-radius: 6px; }
</style>
