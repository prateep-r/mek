<script>
  // mek-desktop's window: the contexts on the left, the chosen one's
  // tunnels, access, resources and the audit log on the right. Everything
  // that acts runs mek (through internal/desktop): the guard asks here first.
  import { onMount } from "svelte";
  import { api } from "./lib/api.js";
  import { tags } from "./lib/logic.js";
  import Confirm from "./lib/Confirm.svelte";
  import Tunnels from "./lib/Tunnels.svelte";
  import Access from "./lib/Access.svelte";
  import Resources from "./lib/Resources.svelte";
  import Audit from "./lib/Audit.svelte";

  let overview = $state({ Contexts: [], Tunnels: [], Current: "", Error: "" });
  let audit = $state([]);
  let selected = $state("");
  let tab = $state("Tunnels");
  let status = $state({ ok: true, text: "" });
  let pending = $state(null); // a plan waiting for its answer, and what follows

  let ctx = $derived(overview.Contexts.find((c) => c.Name === selected));

  async function refresh() {
    try {
      overview = await api.overview();
      audit = (await api.audit(500)) || [];
      if (!selected || !overview.Contexts.some((c) => c.Name === selected)) selected = overview.Current || overview.Contexts[0]?.Name || "";
    } catch (e) {
      status = { ok: false, text: String(e?.message ?? e) };
    }
  }

  onMount(() => {
    refresh();
    const t = setInterval(refresh, 2000);
    return () => clearInterval(t);
  });

  function report(r, label) {
    status = { ok: r.OK, text: (r.OK ? "✓ " : "✗ ") + (label ? label + (r.Output ? " — " + r.Output : "") : r.Output) };
    refresh();
  }

  // plan asks a plan's question, if it has one, then runs it.
  function plan(p, after) {
    if (!p.Ask) return run(p, "", after);
    pending = { p, after };
  }
  async function run(p, answer, after) {
    const r = await api.run(p, answer);
    report(r, r.OK ? p.Label : "");
    if (r.OK && after) after();
  }
  function answer(a) {
    const { p, after } = pending;
    pending = null;
    if (!a) return (status = { ok: true, text: "cancelled: " + p.Label });
    run(p, a, after);
  }

  // act runs something mek does in the background.
  async function act(what, ...args) {
    try {
      if (what === "startTunnel") return plan(await api.startTunnel(...args));
      if (what === "removeContext") return plan(await api.removeContext(...args));
      const r = await api[what](...args);
      report(r, `${what} ${args.join(" ")}`);
    } catch (e) {
      status = { ok: false, text: "✗ " + String(e?.message ?? e) };
    }
  }

  // open runs something that needs a terminal: it opens there.
  async function open(what, ...args) {
    try {
      await api[what](...args);
      status = { ok: true, text: "opened in a terminal: " + [what, ...args].join(" ") };
    } catch (e) {
      status = { ok: false, text: "✗ " + String(e?.message ?? e) };
    }
  }
</script>

<div class="app">
  <aside>
    <div class="brand">mek</div>
    <ul>
      {#each overview.Contexts as c (c.Name)}
        <li class:active={c.Name === selected}>
          <button onclick={() => (selected = c.Name)}>
            <span class="name">{c.Name === overview.Current ? "● " : ""}{c.Name}</span>
            <span class="cloud">{c.Cloud}</span>
            {#each tags(c) as t (t)}<span class="tag {t}">{t}</span>{/each}
          </button>
        </li>
      {/each}
    </ul>
    <button class="add" onclick={() => open("addContext")}>Add a context</button>
  </aside>

  <main>
    {#if overview.Error}
      <p class="bad">✗ {overview.Error}</p>
    {/if}
    {#if ctx}
      <header>
        <div>
          <h1>{ctx.Name} {#each tags(ctx) as t (t)}<span class="tag {t}">{t}</span>{/each}</h1>
          <p class="muted">{ctx.Summary}</p>
        </div>
        <div class="actions">
          <button disabled={ctx.Name === overview.Current} onclick={() => act("use", ctx.Name)}>Use</button>
          <button onclick={() => open("login", ctx.Name)}>Log in</button>
          <button onclick={() => open("editContext", ctx.Name)}>Edit</button>
          <button class="danger" onclick={() => act("removeContext", ctx.Name)}>Remove</button>
        </div>
      </header>
    {:else if !overview.Error}
      <header><div><h1>No contexts</h1><p class="muted">Add one to start.</p></div></header>
    {/if}

    <nav>
      {#each ["Tunnels", "Access", "Resources", "Audit"] as t (t)}
        <button class:on={tab === t} onclick={() => (tab = t)}>{t}</button>
      {/each}
    </nav>

    <div class="body">
      {#if tab === "Tunnels"}
        <Tunnels tunnels={overview.Tunnels || []} {selected} {act} {open} show={api.tunnelLog} />
      {:else if tab === "Access"}
        <Access {ctx} {act} {open} />
      {:else if tab === "Resources"}
        <Resources {ctx} {plan} />
      {:else}
        <Audit entries={audit} {selected} />
      {/if}
    </div>

    <footer class:bad={!status.ok}>{status.text}</footer>
  </main>
</div>

{#if pending}
  <Confirm plan={pending.p} onanswer={answer} />
{/if}

<style>
  .app { display: grid; grid-template-columns: 240px 1fr; height: 100vh; }
  aside { border-right: 1px solid var(--line); display: flex; flex-direction: column; padding: 12px 8px; gap: 8px; min-height: 0; }
  .brand { font-weight: 700; font-size: 20px; padding: 0 8px 4px; letter-spacing: 0.5px; }
  ul { list-style: none; margin: 0; padding: 0; overflow: auto; flex: 1; }
  li button { width: 100%; text-align: left; border: none; background: none; display: flex; gap: 6px; align-items: center; flex-wrap: wrap; padding: 6px 8px; }
  li.active button { background: var(--select); }
  .name { font-weight: 500; }
  .cloud { color: var(--muted); font-size: 12px; }
  .add { margin: 0 8px; }
  main { display: flex; flex-direction: column; min-width: 0; min-height: 0; padding: 12px 18px 0; }
  header { display: flex; justify-content: space-between; align-items: flex-start; gap: 12px; }
  h1 { font-size: 18px; margin: 0; display: flex; gap: 8px; align-items: center; }
  header p { margin: 2px 0 0; }
  .actions { display: flex; gap: 6px; }
  nav { display: flex; gap: 4px; margin: 14px 0 10px; border-bottom: 1px solid var(--line); }
  nav button { border: none; border-bottom: 2px solid transparent; border-radius: 0; background: none; padding: 6px 12px; }
  nav button.on { border-bottom-color: var(--accent); font-weight: 600; }
  .body { flex: 1; overflow: auto; min-height: 0; }
  footer { border-top: 1px solid var(--line); padding: 6px 0; min-height: 30px; color: var(--muted); }
  footer.bad { color: var(--danger); }
</style>
