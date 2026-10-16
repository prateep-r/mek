<script>
  import { filterAudit, when } from "./logic.js";
  let { entries, selected } = $props();
  let query = $state("");
  let problems = $state(false);
  let mine = $state(false);
  let shown = $derived(filterAudit(entries, { query, problems, context: mine ? selected : "" }));
</script>

<div class="bar">
  <input type="search" placeholder="Search context, command, target" bind:value={query} />
  <label><input type="checkbox" bind:checked={problems} /> blocked / declined only</label>
  <label><input type="checkbox" bind:checked={mine} disabled={!selected} /> {selected || "this context"} only</label>
  <span class="muted">{shown.length} of the last {entries.length}</span>
</div>
<table>
  <thead><tr><th>Time</th><th>Context</th><th>Decision</th><th>Class</th><th>Exit</th><th>Command</th></tr></thead>
  <tbody>
    {#each shown as e, i (e.time + i)}
      <tr>
        <td class="muted">{when(e.time)}</td>
        <td>{e.context}</td>
        <td class:bad={e.decision === "blocked" || e.decision === "declined"}>{e.decision}</td>
        <td>{e.class}{e.event ? " " + e.event : ""}</td>
        <td class="mono">{e.event === "start" ? "-" : e.exit_code}</td>
        <td class="mono">{(e.command || []).join(" ")}</td>
      </tr>
    {/each}
  </tbody>
</table>

<style>
  .bar { display: flex; align-items: center; gap: 12px; margin-bottom: 8px; flex-wrap: wrap; }
  .bar input[type="search"] { flex: 1; min-width: 200px; }
</style>
