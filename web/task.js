// Potluck — task/note detail page. Reads ONE subtask + its note(s) from the public,
// read-only API (anon key in config.js) and renders the full output. Dependency-free;
// markdown is rendered by a small escape-first renderer (no innerHTML of raw model text).

const cfg = window.POTLUCK_CONFIG || null;
const SRC = cfg
  ? { base: `${cfg.supabaseUrl}/rest/v1`, headers: { apikey: cfg.anonKey, Authorization: `Bearer ${cfg.anonKey}` } }
  : null;

const esc = (s) => String(s ?? "").replace(/[&<>"]/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;" }[c]));
const fmt = (n) => (Number(n) || 0).toLocaleString("en-US");

async function get(table, q) {
  if (!SRC) return [];
  try {
    const r = await fetch(`${SRC.base}/${table}?${q}`, { headers: SRC.headers });
    return r.ok ? await r.json() : [];
  } catch { return []; }
}

/* ── tiny, XSS-safe markdown → HTML ──────────────────────────────────────────
   Every text fragment is HTML-escaped BEFORE any tag is introduced, so raw model
   output can never inject markup. Only a safe subset is rendered; links are limited
   to http(s). Good enough for the notes (headings, lists, bold/italic, code, links). */
function inlineMd(escaped) {
  let t = escaped;
  t = t.replace(/`([^`]+)`/g, (_, c) => `<code>${c}</code>`);
  t = t.replace(/\[([^\]]+)\]\((https?:\/\/[^\s)]+)\)/g, '<a href="$2" target="_blank" rel="noopener noreferrer">$1</a>');
  t = t.replace(/(^|[\s(])(https?:\/\/[^\s<)*]+)/g, '$1<a href="$2" target="_blank" rel="noopener noreferrer">$2</a>');
  t = t.replace(/\*\*([^*]+)\*\*/g, "<strong>$1</strong>");
  t = t.replace(/(^|[^*])\*([^*\s][^*]*?)\*/g, "$1<em>$2</em>");
  return t;
}
function mdToHtml(md) {
  const lines = String(md || "").replace(/\r\n?/g, "\n").split("\n");
  let html = "", inList = false, ordered = false, inCode = false, para = [];
  const flushPara = () => { if (para.length) { html += `<p>${inlineMd(esc(para.join(" ")))}</p>`; para = []; } };
  const flushList = () => { if (inList) { html += ordered ? "</ol>" : "</ul>"; inList = false; } };
  for (const raw of lines) {
    if (/^```/.test(raw.trim())) {
      flushPara(); flushList();
      html += inCode ? "</code></pre>" : "<pre><code>"; inCode = !inCode; continue;
    }
    if (inCode) { html += esc(raw) + "\n"; continue; }
    const t = raw.trim();
    let m;
    if (t === "") { flushPara(); flushList(); continue; }
    if ((m = t.match(/^(#{1,6})\s+(.*)$/))) {
      flushPara(); flushList();
      const lvl = Math.min(m[1].length + 1, 6);
      html += `<h${lvl}>${inlineMd(esc(m[2]))}</h${lvl}>`; continue;
    }
    if ((m = t.match(/^[-*+]\s+(.*)$/))) {
      flushPara(); if (!inList || ordered) { flushList(); html += "<ul>"; inList = true; ordered = false; }
      html += `<li>${inlineMd(esc(m[1]))}</li>`; continue;
    }
    if ((m = t.match(/^\d+[.)]\s+(.*)$/))) {
      flushPara(); if (!inList || !ordered) { flushList(); html += "<ol>"; inList = true; ordered = true; }
      html += `<li>${inlineMd(esc(m[1]))}</li>`; continue;
    }
    flushList(); para.push(t);
  }
  flushPara(); flushList(); if (inCode) html += "</code></pre>";
  return html || "<p>—</p>";
}

function noteBlock(r, isCurrent) {
  const when = r.created_at ? new Date(r.created_at).toLocaleString("en-US", { dateStyle: "medium", timeStyle: "short" }) : "";
  const vs = r.verification_status && r.verification_status !== "unverified" ? esc(r.verification_status) : "unverified";
  return `<article class="note ${isCurrent ? "note-current" : "note-old"}">
    <div class="note-head">
      <span class="ai-label">AI · ${vs}</span>
      ${r.reported_model ? `<span class="model-badge">${esc(r.reported_model)}</span>` : ""}
      <span class="${isCurrent ? "note-current-badge" : "note-old-badge"}">${isCurrent ? "current" : "superseded"}</span>
    </div>
    <div class="note-body">${mdToHtml(r.artifact_md)}</div>
    <div class="note-foot">${fmt(r.token_count)} tok${when ? ` · ${esc(when)}` : ""}</div>
  </article>`;
}

async function render() {
  const root = document.getElementById("task-detail");
  const id = new URLSearchParams(location.search).get("id");
  if (!id) { root.innerHTML = `<p class="detail-empty">No task id in the URL. <a href="index.html#built">Browse the notes</a>.</p>`; return; }
  const [tasks, notes] = await Promise.all([
    get("subtasks", `id=eq.${encodeURIComponent(id)}&select=id,title,prompt,acceptance,category_slug,tags,status,token_budget&limit=1`),
    get("results", `subtask_id=eq.${encodeURIComponent(id)}&select=id,artifact_md,reported_model,token_count,created_at,verification_status&order=created_at.desc`),
  ]);
  const t = tasks[0];
  if (!t) { root.innerHTML = `<p class="detail-empty">That task wasn't found. <a href="index.html#built">Browse the notes</a>.</p>`; return; }
  document.title = `${t.title} — Potluck`;

  const tags = (t.tags || []).map((x) => `<span class="tag">${esc(x)}</span>`).join("");
  let notesHtml;
  if (!notes.length) {
    notesHtml = `<p class="detail-empty">No note yet — this task is <strong>${esc(t.status)}</strong>.${t.status === "open" ? " It's claimable, so an agent should digest it soon." : ""}</p>`;
  } else {
    notesHtml = noteBlock(notes[0], true);
    if (notes.length > 1) {
      const k = notes.length - 1;
      notesHtml += `<details class="older-notes"><summary>${k} earlier ${k === 1 ? "version" : "versions"} — superseded, kept for provenance</summary>${notes.slice(1).map((r) => noteBlock(r, false)).join("")}</details>`;
    }
  }

  root.innerHTML = `
    <div class="detail-task">
      <div class="detail-meta">
        ${t.category_slug ? `<span class="cat-badge">${esc(t.category_slug)}</span>` : ""}
        <span class="detail-status detail-status-${esc(t.status)}">${esc(t.status)}</span>
      </div>
      <h1 class="detail-title">${esc(t.title)}</h1>
      ${tags ? `<div class="detail-tags">${tags}</div>` : ""}
    </div>
    <section class="detail-section">
      <h2 class="detail-h">${notes.length > 1 ? "Notes" : "The note"}</h2>
      ${notesHtml}
    </section>
    <details class="detail-source">
      <summary>Source &amp; acceptance — what the agent was given</summary>
      <h3>Prompt / source</h3>
      <pre class="source-pre">${esc(t.prompt)}</pre>
      ${t.acceptance ? `<h3>Acceptance criteria</h3><pre class="source-pre">${esc(t.acceptance)}</pre>` : ""}
    </details>`;
}

render();
