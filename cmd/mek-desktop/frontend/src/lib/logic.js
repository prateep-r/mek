// What the window works out itself: filters and labels. No calls here.

/** Kinds of resource whose service or name holds the filter; an exact
 *  match (on either, or "service name") first. */
export function filterKinds(kinds, filter) {
  const f = filter.trim().toLowerCase();
  if (!f) return kinds;
  const exact = [];
  const rest = [];
  for (const k of kinds) {
    const name = `${k.Service} ${k.Name}`.toLowerCase();
    if (k.Name.toLowerCase() === f || k.Service.toLowerCase() === f || name === f) exact.push(k);
    else if (name.includes(f)) rest.push(k);
  }
  return [...exact, ...rest];
}

/** Audit entries matching a search (over context, command and target), a
 *  problems-only toggle (blocked, declined) and an optional context. */
export function filterAudit(entries, { query = "", problems = false, context = "" } = {}) {
  const q = query.toLowerCase();
  return entries.filter((e) => {
    if (problems && e.decision !== "blocked" && e.decision !== "declined") return false;
    if (context && e.context !== context) return false;
    const hay = `${e.context} ${(e.command || []).join(" ")} ${e.target || ""}`.toLowerCase();
    return hay.includes(q);
  });
}

/** A short time: today's as 15:04:05, older ones with the date. */
export function when(iso, now = new Date()) {
  const t = new Date(iso);
  const pad = (n) => String(n).padStart(2, "0");
  const clock = `${pad(t.getHours())}:${pad(t.getMinutes())}:${pad(t.getSeconds())}`;
  if (t.toDateString() === now.toDateString()) return clock;
  const month = t.toLocaleString("en", { month: "short" });
  return `${month} ${pad(t.getDate())} ${clock.slice(0, 5)}`;
}

/** The colour a state gets: good, bad or neutral. */
export function stateTone(state) {
  const s = (state || "").toLowerCase();
  if (["running", "available", "active", "ready", "succeeded", "enabled"].includes(s)) return "good";
  if (["stopped", "terminated", "failed", "error", "deleting", "dead", "deallocated"].some((w) => s.startsWith(w)) || s.startsWith("exited")) return "bad";
  return "";
}

/** The tags a context shows. */
export function tags(ctx) {
  const out = [];
  if (ctx.Protected) out.push("protected");
  if (ctx.ReadOnly) out.push("readonly");
  return out;
}
