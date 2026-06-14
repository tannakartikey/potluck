// Potluck board — dependency-free.
//
// Today it reads mock JSON from ./data/ (exact PostgREST shape). To go live,
// set window.POTLUCK_CONFIG below and the same code fetches Supabase instead —
// the row shape is identical, so only the data source changes.
//
//   window.POTLUCK_CONFIG = {
//     supabaseUrl: "https://YOUR-ref.supabase.co",
//     anonKey: "YOUR-PUBLIC-ANON-KEY"   // public; protected by RLS
//   };

const cfg = window.POTLUCK_CONFIG || null;

const SOURCES = cfg
  ? {
      mode: "live",
      subtasks: `${cfg.supabaseUrl}/rest/v1/subtasks?select=*&order=created_at.desc`,
      results: `${cfg.supabaseUrl}/rest/v1/results?select=*&order=created_at.desc`,
      headers: { apikey: cfg.anonKey, Authorization: `Bearer ${cfg.anonKey}` },
    }
  : {
      mode: "sample",
      subtasks: "data/subtasks.sample.json",
      results: "data/results.sample.json",
      headers: {},
    };

const $ = (sel) => document.querySelector(sel);
const fmt = (n) => n.toLocaleString("en-US");
const esc = (s) =>
  String(s ?? "").replace(/[&<>"]/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;" }[c]));

let state = { subtasks: [], results: [], filter: "all" };

async function load() {
  try {
    const [subtasks, results] = await Promise.all([
      fetch(SOURCES.subtasks, { headers: SOURCES.headers }).then((r) => r.json()),
      fetch(SOURCES.results, { headers: SOURCES.headers }).then((r) => r.json()),
    ]);
    state.subtasks = subtasks || [];
    state.results = results || [];
  } catch (e) {
    $("#task-cards").innerHTML = `<p class="muted">Could not load data (${esc(e.message)}).</p>`;
    return;
  }
  $("#data-mode").textContent = SOURCES.mode;
  renderStats();
  renderFilters();
  renderTasks();
  renderFeed();
}

function renderStats() {
  const tokens = state.results.reduce((s, r) => s + (r.token_count || 0), 0);
  const open = state.subtasks.filter((t) => t.status === "open").length;
  const people = new Set(state.results.map((r) => r.contributor_handle).filter(Boolean)).size;
  $("#stat-tokens").textContent = fmt(tokens);
  $("#stat-done").textContent = fmt(state.results.length);
  $("#stat-open").textContent = fmt(open);
  $("#stat-people").textContent = fmt(people);
}

function categories() {
  return [...new Set(state.subtasks.map((t) => t.category_slug).filter(Boolean))].sort();
}

function renderFilters() {
  const cats = ["all", ...categories()];
  $("#filters").innerHTML = cats
    .map(
      (c) =>
        `<button class="chip ${c === state.filter ? "active" : ""}" data-cat="${esc(c)}">${esc(c)}</button>`
    )
    .join("");
  $("#filters")
    .querySelectorAll(".chip")
    .forEach((b) =>
      b.addEventListener("click", () => {
        state.filter = b.dataset.cat;
        renderFilters();
        renderTasks();
      })
    );
}

function visibleTasks() {
  const open = state.subtasks.filter((t) => t.status !== "done");
  return state.filter === "all" ? open : open.filter((t) => t.category_slug === state.filter);
}

function renderTasks() {
  const tasks = visibleTasks();
  if (!tasks.length) {
    $("#task-cards").innerHTML = `<p class="muted">No open tasks in this category. The pot is empty — add a dish.</p>`;
    return;
  }
  $("#task-cards").innerHTML = tasks
    .map((t) => {
      const model =
        t.model_policy === "any" || !t.requested_model
          ? "any model"
          : `${t.model_policy === "exact" ? "needs" : "min"} ${esc(t.requested_model)}`;
      return `
      <article class="card">
        <div class="top">
          <span class="tag">${esc(t.category_slug || "general")}</span>
          <span class="pill ${esc(t.status)}">${esc(t.status)}</span>
        </div>
        <h3>${esc(t.title)}</h3>
        ${t.acceptance ? `<div class="accept">✓ ${esc(t.acceptance)}</div>` : ""}
        <div class="meta">
          <span class="tag">~${fmt(t.token_budget || 0)} tok budget</span>
          <span class="tag">${model}</span>
        </div>
      </article>`;
    })
    .join("");
}

function renderFeed() {
  if (!state.results.length) {
    $("#result-feed").innerHTML = `<p class="muted">Nothing built yet.</p>`;
    return;
  }
  $("#result-feed").innerHTML = state.results
    .map((r) => {
      const title = r.subtask_title || r.subtask_id;
      const link = r.permalink ? `href="${esc(r.permalink)}" target="_blank" rel="noopener"` : "";
      return `
      <div class="feed-item">
        <div>
          <a class="title" ${link}>${esc(title)}</a>
          <div class="by">by <span class="who">${esc(r.contributor_handle || "anon")}</span>
            · <span class="self">model: ${esc(r.reported_model || "?")} (self-reported)</span>
            · ${esc(r.verification_status || "unverified")}</div>
        </div>
        <div class="muted tiny">${fmt(r.token_count || 0)} tok</div>
      </div>`;
    })
    .join("");
}

load();
