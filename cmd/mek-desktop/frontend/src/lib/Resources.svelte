<script>
  // The chosen context's cloud, from mek's catalog: kinds of resource, the
  // resources (a page at a time), one resource's details and its actions.
  import { api } from "./api.js";
  import { filterKinds, stateTone } from "./logic.js";
  let { ctx, plan } = $props();

  let catalog = $state(null);
  let error = $state("");
  let filter = $state("");
  let kind = $state(null);
  let rows = $state([]);
  let next = $state("");
  let loading = $state("");
  let row = $state(null);
  let detail = $state("");
  let gen = 0; // which fetch counts: older ones are dropped

  // The context's name, not the context: the window reads the contexts
  // again every few seconds, and that mustn't start the tab over.
  let name = $derived(ctx?.Name);

  $effect(() => {
    const current = name;
    catalog = null; kind = null; rows = []; row = null; error = ""; filter = "";
    if (!current) return;
    api.resources(current).then((c) => { if (current === name) catalog = c; })
      .catch((e) => { if (current === name) error = String(e?.message ?? e); });
  });

  let kinds = $derived(catalog ? filterKinds(catalog.Kinds || [], filter) : []);

  async function openKind(k, more = "") {
    const g = ++gen;
    if (!more) { kind = k; rows = []; next = ""; row = null; detail = ""; }
    loading = `listing ${k.Service} ${k.Name}…`; error = "";
    try {
      const page = await api.list(ctx.Name, k.Service, k.Name, more);
      if (g !== gen) return;
      rows = [...rows, ...(page.Rows || [])]; next = page.Next;
    } catch (e) { if (g === gen) error = String(e?.message ?? e); }
    if (g === gen) loading = "";
  }

  async function openRow(r) {
    const g = ++gen;
    row = r; detail = ""; loading = `describing ${r.Name || r.ID}…`;
    try { const d = await api.describe(ctx.Name, kind.Service, kind.Name, r); if (g === gen) detail = d; }
    catch (e) { if (g === gen) detail = "✗ " + String(e?.message ?? e); }
    if (g === gen) loading = "";
  }

  async function act(action) {
    const p = await api.act(ctx.Name, kind.Service, kind.Name, action, row);
    plan(p, () => openKind(kind));
  }

  let where = $derived(catalog && catalog.Cloud !== "aws");
</script>

{#if !ctx}
  <p class="empty">Choose a context.</p>
{:else if error && !catalog}
  <p class="empty">✗ {error}</p>
{:else if !catalog}
  <p class="empty">Loading the catalog…</p>
{:else}
  <div class="cols">
    <aside>
      <input type="search" placeholder="Filter: ec2, instances…" bind:value={filter} />
      <p class="muted small">{catalog.Source}</p>
      <ul>
        {#each kinds.slice(0, 400) as k (k.Service + " " + k.Name)}
          <li class:active={kind === k}><button onclick={() => openKind(k)}>
            <span class="svc">{k.Service}</span> {k.Name}
          </button></li>
        {/each}
      </ul>
    </aside>
    <section>
      {#if !kind}
        <p class="empty">Choose a kind of resource. Lists, details and actions run mek, so the guard and the audit log apply.</p>
      {:else}
        <div class="bar"><b>{kind.Service} › {kind.Name}</b><span class="muted">{loading}</span></div>
        {#if error}<p class="bad">✗ {error}</p>{/if}
        {#if rows.length === 0 && !loading && !error}<p class="empty">(none)</p>{/if}
        {#if rows.length}
          <table>
            <thead><tr><th>ID</th><th>Name</th><th>State</th>{#if where}<th>Where</th>{/if}</tr></thead>
            <tbody>
              {#each rows as r, i (r.ID + i)}
                <tr class="clickable" class:selected={row === r} onclick={() => openRow(r)}>
                  <td class="mono">{r.ID}</td><td>{r.Name}</td><td class={stateTone(r.State)}>{r.State}</td>
                  {#if where}<td class="muted">{r.Where}</td>{/if}
                </tr>
              {/each}
            </tbody>
          </table>
          {#if next}<button class="more" onclick={() => openKind(kind, next)}>More</button>{/if}
        {/if}
        {#if row}
          <div class="detail">
            <div class="bar">
              <b>{row.Name || row.ID}</b>
              <span class="actions">
                {#each kind.Actions || [] as a (a)}
                  <button class:danger={/delete|terminate|stop/.test(a)} onclick={() => act(a)}>{a}</button>
                {/each}
              </span>
            </div>
            <pre class="mono">{detail || "…"}</pre>
          </div>
        {/if}
      {/if}
    </section>
  </div>
{/if}

<style>
  .cols { display: grid; grid-template-columns: 280px 1fr; gap: 16px; height: 100%; min-height: 0; }
  aside { display: flex; flex-direction: column; min-height: 0; }
  aside input { width: 100%; }
  .small { font-size: 11px; margin: 4px 0; }
  ul { list-style: none; margin: 0; padding: 0; overflow: auto; flex: 1; }
  li button { width: 100%; text-align: left; border: none; background: none; padding: 3px 6px; border-radius: 4px; }
  li.active button { background: var(--select); }
  .svc { color: var(--muted); }
  section { overflow: auto; min-height: 0; }
  .bar { display: flex; justify-content: space-between; align-items: center; gap: 8px; margin-bottom: 6px; }
  .actions { display: flex; gap: 6px; flex-wrap: wrap; }
  .more { margin-top: 8px; }
  .detail { margin-top: 16px; border-top: 1px solid var(--line); padding-top: 8px; }
  pre { white-space: pre-wrap; background: var(--panel); padding: 8px; border-radius: 6px; max-height: 420px; overflow: auto; }
</style>
