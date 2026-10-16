<script>
  // The guard's question before a command runs: yes or no, or the
  // context's name typed (a destructive command on a protected context).
  let { plan, onanswer } = $props();
  let typed = $state("");
</script>

<div class="backdrop">
  <div class="dialog" role="dialog" aria-modal="true">
    <p class:danger={plan.Ask === "typed"}>{plan.Text}</p>
    {#if plan.Ask === "typed"}
      <p class="muted">Type <b>{plan.Typed}</b> to run it.</p>
      <!-- svelte-ignore a11y_autofocus -->
      <input type="text" bind:value={typed} autofocus onkeydown={(e) => e.key === "Enter" && onanswer(typed)} />
    {/if}
    <div class="buttons">
      <button onclick={() => onanswer("")}>Cancel</button>
      {#if plan.Ask === "typed"}
        <button class="primary" disabled={typed !== plan.Typed} onclick={() => onanswer(typed)}>Run</button>
      {:else}
        <button class="primary" onclick={() => onanswer("yes")}>Yes</button>
      {/if}
    </div>
  </div>
</div>

<style>
  .backdrop { position: fixed; inset: 0; background: rgb(0 0 0 / 0.35); display: grid; place-items: center; z-index: 10; }
  .dialog { background: var(--panel); border: 1px solid var(--line); border-radius: 10px; padding: 18px 20px; width: min(560px, 90vw); }
  .dialog p { margin: 0 0 12px; word-break: break-word; }
  .danger { color: var(--danger); }
  input { width: 100%; margin-bottom: 12px; }
  .buttons { display: flex; justify-content: flex-end; gap: 8px; }
</style>
