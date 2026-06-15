// Potluck landing — dependency-free, reads live read-only data from Supabase (PostgREST)
// via the public anon key in ./config.js. RLS makes the anon key read-only. If config.js
// is absent it falls back to ./data/*.sample.json so the page still renders.

const cfg = window.POTLUCK_CONFIG || null;
const SRC = cfg
  ? { mode: "live", base: `${cfg.supabaseUrl}/rest/v1`, headers: { apikey: cfg.anonKey, Authorization: `Bearer ${cfg.anonKey}` } }
  : { mode: "sample", base: "data", headers: {} };

const $  = (s) => document.querySelector(s);
const $$ = (s) => Array.from(document.querySelectorAll(s));
const fmt = (n) => (Number(n) || 0).toLocaleString("en-US");
// Hero stat counters: full commas while they're readable (74,014 · 340,000), then abbreviate once
// they'd get wide enough to unbalance the row (1,200,000 → "1.2M" · "1.5B"). Keeps the style intact at any size.
const compact = (n) => {
  n = Number(n) || 0;
  if (n < 1_000_000) return n.toLocaleString("en-US");
  return new Intl.NumberFormat("en-US", { notation: "compact", maximumFractionDigits: 1 }).format(n);
};
const esc = (s) => String(s ?? "").replace(/[&<>"]/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;" }[c]));

const liveUrl   = (table, q) => `${SRC.base}/${table}?${q}`;
const sampleUrl = (table)    => `${SRC.base}/${table}.sample.json`;

async function get(table, q) {
  const u = SRC.mode === "live" ? liveUrl(table, q) : sampleUrl(table);
  try {
    const r = await fetch(u, { headers: SRC.headers });
    return r.ok ? await r.json() : [];
  } catch { return []; }
}

// Exact row count via PostgREST's Content-Range header (no full fetch).
async function count(table, q = "select=id") {
  if (SRC.mode !== "live") return null;
  try {
    const r = await fetch(liveUrl(table, q), { headers: { ...SRC.headers, Prefer: "count=exact", Range: "0-0" } });
    const cr = r.headers.get("content-range") || "";
    const total = cr.split("/")[1];
    return total ? parseInt(total, 10) : 0;
  } catch { return null; }
}

/* ───────── count-up animation ───────── */
function animateCount(el, to) {
  const dur = 900, t0 = performance.now(), from = 0;
  const ease = (x) => 1 - Math.pow(1 - x, 3);
  function tick(now) {
    const p = Math.min(1, (now - t0) / dur);
    el.textContent = compact(Math.round(from + (to - from) * ease(p)));
    if (p < 1) requestAnimationFrame(tick);
  }
  requestAnimationFrame(tick);
}

/* ───────── render: stats ───────── */
function renderStats({ tokens, done, open, people }) {
  const set = (id, v) => { const el = $(id); if (el) animateCount(el, v); };
  set("#stat-tokens", tokens); set("#stat-done", done);
  set("#stat-open", open);     set("#stat-people", people);
}

/* ───────── render: board ───────── */
let BOARD = [];            // all open tasks
let activeCat = "all";
let query = "";

function taskCard(t) {
  const tags = (t.tags || []).slice(0, 4).map((x) => `<span class="tag">${esc(x)}</span>`).join("");
  const prio = (t.priority || 0) > 0 ? `<span class="prio-badge">★ priority</span>` : "";
  const desc = (t.prompt || "").replace(/\s+/g, " ").trim();
  return `<article class="task-card reveal">
    <div class="task-top">
      ${t.category_slug ? `<span class="cat-badge">${esc(t.category_slug)}</span>` : ""}
      ${prio}
    </div>
    <h3 class="task-title">${esc(t.title)}</h3>
    <p class="task-desc">${esc(desc)}</p>
    <div class="task-tags">${tags}</div>
    <div class="task-foot">
      <span class="budget">~${fmt(t.token_budget)} tok</span>
      <span>open · claimable</span>
    </div>
  </article>`;
}

function renderBoard() {
  const grid = $("#board-grid"), empty = $("#board-empty");
  let list = BOARD;
  if (activeCat !== "all") list = list.filter((t) => (t.category_slug || "") === activeCat);
  if (query) {
    const q = query.toLowerCase();
    list = list.filter((t) =>
      (t.title || "").toLowerCase().includes(q) ||
      (t.prompt || "").toLowerCase().includes(q) ||
      (t.tags || []).join(" ").toLowerCase().includes(q) ||
      (t.category_slug || "").toLowerCase().includes(q));
  }
  if (!list.length) {
    grid.innerHTML = "";
    empty.hidden = false;
    empty.textContent = BOARD.length
      ? "No open tasks match that filter."
      : "The pot is empty right now — a perfect time to bring the first dish. 🍲";
    return;
  }
  empty.hidden = true;
  grid.innerHTML = list.map(taskCard).join("");
  revealObserve(grid.querySelectorAll(".reveal"));
}

function renderChips() {
  const counts = {};
  BOARD.forEach((t) => { const c = t.category_slug || "general"; counts[c] = (counts[c] || 0) + 1; });
  const cats = Object.keys(counts).sort();
  const chip = (key, label, n, active) =>
    `<button class="chip${active ? " active" : ""}" data-cat="${esc(key)}">${esc(label)}${n != null ? ` <b>${n}</b>` : ""}</button>`;
  const el = $("#category-list");
  if (!BOARD.length) { el.innerHTML = `<span class="muted">No open topics yet.</span>`; return; }
  el.innerHTML = chip("all", "All", BOARD.length, activeCat === "all") +
    cats.map((c) => chip(c, c, counts[c], activeCat === c)).join("");
  el.querySelectorAll(".chip").forEach((b) =>
    b.addEventListener("click", () => { activeCat = b.dataset.cat; renderChips(); renderBoard(); }));
}

/* ───────── render: gallery ───────── */
function artCard(r) {
  const task = r.subtasks || {};
  const body = (r.artifact_md || "").replace(/[#>*`_]/g, "").replace(/\s+/g, " ").trim();
  const when = r.created_at ? new Date(r.created_at).toLocaleDateString("en-US", { month: "short", day: "numeric" }) : "";
  return `<article class="art-card reveal">
    <div class="art-head">
      <span class="ai-label">AI · unverified</span>
      ${r.reported_model ? `<span class="model-badge">${esc(r.reported_model)}</span>` : ""}
    </div>
    <h3 class="art-title">${esc(task.title || "Untitled task")}</h3>
    <p class="art-body">${esc(body || "—")}</p>
    <div class="art-foot">
      ${task.category_slug ? `<span>${esc(task.category_slug)}</span>` : ""}
      <span>${fmt(r.token_count)} tok</span>
      ${when ? `<span>${esc(when)}</span>` : ""}
    </div>
  </article>`;
}

function renderGallery(rows) {
  const g = $("#gallery"), empty = $("#gallery-empty");
  if (!rows.length) { g.innerHTML = ""; empty.hidden = false; return; }
  empty.hidden = true;
  g.innerHTML = rows.map(artCard).join("");
  revealObserve(g.querySelectorAll(".reveal"));
}

/* ───────── reveal on scroll ───────── */
let _io;
function revealObserve(nodes) {
  if (!_io) {
    _io = new IntersectionObserver((entries) => {
      entries.forEach((e) => { if (e.isIntersecting) { e.target.classList.add("in"); _io.unobserve(e.target); } });
    }, { threshold: 0.08, rootMargin: "0px 0px -40px 0px" });
  }
  nodes.forEach((n) => _io.observe(n));
}

/* ───────── load ───────── */
async function load() {
  const boardQ = "status=eq.open&select=id,title,prompt,category_slug,tags,token_budget,priority&order=priority.desc,created_at.desc&limit=60";
  const galleryQ = "select=id,reported_model,token_count,created_at,artifact_md,subtasks(title,category_slug)&order=created_at.desc&limit=9";

  const [board, tokenRows, gallery, people] = await Promise.all([
    get("subtasks", boardQ),
    get("results", "select=token_count"),
    get("results", galleryQ),
    count("contributors"),
  ]);

  // sample mode: filter open client-side
  BOARD = (SRC.mode === "live" ? board : board.filter((t) => t.status === "open")) || [];

  const tokens = (tokenRows || []).reduce((s, r) => s + (r.token_count || 0), 0);
  const done = (tokenRows || []).length;
  const peopleCount = people != null ? people : new Set((gallery || []).map((r) => r.contributor_id)).size;

  renderStats({ tokens, done, open: BOARD.length, people: peopleCount });
  renderChips();
  renderBoard();
  renderGallery(gallery || []);

  // live indicator
  const ok = SRC.mode === "live";
  const dot = $("#live-dot"), label = $("#live-label"), mode = $("#data-mode");
  if (dot) dot.classList.toggle("on", ok);
  if (label) label.textContent = ok ? "live · reading the database" : "sample data";
  if (mode) { mode.textContent = ok ? "live data" : "sample data"; mode.classList.add(ok ? "live" : "sample"); }
}

/* ───────── chrome: nav, search, copy ───────── */
function initChrome() {
  const nav = $("#nav");
  const onScroll = () => nav.classList.toggle("scrolled", window.scrollY > 8);
  window.addEventListener("scroll", onScroll, { passive: true }); onScroll();

  // mobile menu: close the dropdown after a link is tapped
  const navToggle = $("#nav-toggle");
  if (navToggle) $$("#nav-links a").forEach((a) => a.addEventListener("click", () => { navToggle.checked = false; }));

  const search = $("#board-search");
  if (search) {
    let deb;
    search.addEventListener("input", (e) => {
      clearTimeout(deb);
      deb = setTimeout(() => { query = e.target.value.trim(); renderBoard(); }, 120);
    });
  }

  $$(".copy-btn, .copy-prompt").forEach((btn) =>
    btn.addEventListener("click", async () => {
      try {
        await navigator.clipboard.writeText(btn.dataset.copy || "");
        const t = btn.textContent; btn.textContent = "Copied ✓"; btn.classList.add("done");
        setTimeout(() => { btn.textContent = t; btn.classList.remove("done"); }, 1600);
      } catch {}
    }));

  // Get-started tabs: "Hand it to your agent" / "Under the hood"
  $$(".tab").forEach((tab) =>
    tab.addEventListener("click", () => {
      const name = tab.dataset.tab;
      $$(".tab").forEach((t) => { const on = t === tab; t.classList.toggle("is-active", on); t.setAttribute("aria-selected", String(on)); });
      $$(".tab-panel").forEach((p) => { p.hidden = p.dataset.panel !== name; });
    }));

  revealObserve(document.querySelectorAll(".section-head, .steps li, .mini-card, .trust-card, .terminal"));
}

initChrome();
load();
