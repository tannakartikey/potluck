// Potluck landing page — dependency-free.
//
// Reads live, read-only data from Supabase (PostgREST) using the public anon key
// in ./config.js. If config.js is absent, it falls back to ./data/*.sample.json.
// The page is just a landing/explainer: high-level stats + the topics the
// community is working on. Any richer UI is left to each user's own agent
// (see ../AGENTS.md).

const cfg = window.POTLUCK_CONFIG || null;

const SRC = cfg
  ? { mode: "live", base: `${cfg.supabaseUrl}/rest/v1`, headers: { apikey: cfg.anonKey, Authorization: `Bearer ${cfg.anonKey}` } }
  : { mode: "sample", base: "data", headers: {} };

const url = (table, q) =>
  SRC.mode === "live" ? `${SRC.base}/${table}?${q}` : `${SRC.base}/${table}.sample.json`;

const $ = (s) => document.querySelector(s);
const fmt = (n) => (n || 0).toLocaleString("en-US");
const esc = (s) => String(s ?? "").replace(/[&<>"]/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;" }[c]));
const get = (t, q) => fetch(url(t, q), { headers: SRC.headers }).then((r) => (r.ok ? r.json() : [])).catch(() => []);

async function load() {
  const [subtasks, results, contributors] = await Promise.all([
    get("subtasks", "select=category_slug,status"),
    get("results", "select=token_count"),
    SRC.mode === "live" ? get("contributors", "select=id") : Promise.resolve([]),
  ]);

  const open = subtasks.filter((t) => t.status === "open").length;
  const tokens = results.reduce((s, r) => s + (r.token_count || 0), 0);
  const people = SRC.mode === "live" ? contributors.length : new Set((results || []).map((r) => r.contributor_handle)).size;

  $("#stat-tokens").textContent = fmt(tokens);
  $("#stat-done").textContent = fmt(results.length);
  $("#stat-open").textContent = fmt(open);
  $("#stat-people").textContent = fmt(people);
  $("#data-mode").textContent = SRC.mode;

  // topics: count open tasks per category
  const counts = {};
  subtasks.filter((t) => t.status !== "done").forEach((t) => {
    const c = t.category_slug || "general";
    counts[c] = (counts[c] || 0) + 1;
  });
  const cats = Object.keys(counts).sort();
  $("#category-list").innerHTML = cats.length
    ? cats.map((c) => `<span class="chip">${esc(c)} <b>${counts[c]}</b></span>`).join("")
    : `<span class="muted">No open topics yet — the pot is empty. Add a dish.</span>`;
}

load();
