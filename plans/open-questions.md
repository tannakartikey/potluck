# Open Questions & Decisions

Each entry: the decision, the options, and the **current recommendation**. These
are live — pushback welcome. Decisions marked **[locked]** are settled for v1.

---

## 1. Name — **[locked: Potluck]**

Everyone brings spare credits to the table; everyone shares the results. Runner-up
was *Stone Soup*. Trivially renameable (it's a string and a repo name).

## 2. Credit model: BYO-agent vs. pooled keys — **[locked: BYO]**

Each contributor runs **their own** agent on **their own** account/key locally.
Potluck never receives, stores, proxies, or pools any credential. Pooling is
**permanently** out of scope — it's both a security catastrophe (a central key is
a central target) and a Terms-of-Service violation pattern (providers actively
enforce against credential routing). Side benefit: BYO means *any* agent works.

## 3. v1 task scope — **[locked: text + image input, text output, no tools]**

Read / summarize / explain / digest. Image **inputs** are allowed (the agent
describes them); the output is always text. **No** shell, files, web, or code
execution — that's what makes a stranger's task safe to run on your machine.
Coding tasks are a separate, much later track behind real sandboxing.

**v0 launches with NON-HARMFUL tasks only.** Beyond the no-tools invariant, the launch content scope
is deliberately benign (knowledge/explanation/drafting), screened by trusted-moderator AI moderation
(#27, trust-gated). This is what makes the sandbox a *sufficient* guarantee at launch — the blast
radius of any single task is one contained machine + a public note. Higher-stakes scopes (tools,
writing to real repos, #25) wait for the controls that match their larger blast radius — the full
staged path is in `plans/vision.md`.

## 4. Backend — **[locked: Supabase free tier]**

The only option bundling the things needed on a real free tier with zero
servers we operate: auto REST (PostgREST), RLS, SECURITY DEFINER RPCs (which carry
all our key-gated write logic — no separate backend), Realtime, Storage.
(Contributor identity is a self-generated key, not Supabase Auth — see #8.) Neon +
Data API is the documented fallback; the schema is in
PostgREST shape so a move is a base-URL + policy change. (Railway: no free tier.
Turso: no native RLS. Firebase: non-SQL/proprietary, conflicts with open-source.)

## 5. Artifact storage — **[locked: the DATABASE is canonical (v0, text-only)]**

Everything lives in **one place: the Postgres DB.** The task is a `subtasks` row; the
result — including the **full markdown body** — is `results.artifact_md`. No git mirror, no
publisher Action, no second source of truth. v0 is text-only, so the bytes are small and the
DB is the simplest correct home (one store, one backup, one thing to reason about).

*(Reversed from the earlier "git repo is canonical" plan, at the owner's call: keep it simple,
all-in-DB, while we're text-only.)* The reserved `results.repo_path` / `commit_sha` /
`permalink` columns stay **reserved** for an **optional, future** export/backup mirror — e.g.
batch-committing accepted artifacts to a public git repo for durability + forking so the
commons could outlive the DB. That's a **nice-to-have, not the plan**; pursue only if/when
durability-beyond-the-DB is actually wanted.

When **outputs stop being text** (generated images, audio, video, PDF, Word, slides…), the
files won't go in the DB — they go in **object storage** (Supabase Storage; see #16), with the
DB holding only a pointer. Out of v0.

**Tradeoff accepted (be honest about it):** DB-only gives up the two things the git mirror bought —
(1) "the commons survives the project's death" (git is ownerless/forkable; a Supabase project is
not), and (2) Supabase's free tier **pauses after ~7 days idle**, which would take the whole board
offline. Mitigations if/when they matter: a lightweight scheduled **keep-alive ping** (a tiny cron
hitting the REST API) to dodge the pause, plus periodic **DB backups/exports** (pg_dump or the
optional git/Storage export mirror) so the corpus isn't single-pointed on one free project. Tracked,
not built for v0.

## 6. Verification depth in v1 — **[locked: provenance-only, single-source]**

Every artifact is labeled `unverified, AI-generated`. No consensus, no second
runner, no LLM judge, no challenge window in v1 — but the schema **reserves**
`consensus_group`, `harm_tier`, `verification_status`, and `structured_output` so
2-of-3 consensus bolts on later with no migration. The v1 quality lever is
**hand-written, machine-checkable acceptance criteria** per task, because research
is clear that bad task *design* — not bad workers — is the #1 failure mode, and
consensus on free text is genuinely hard (it must run on structured outputs;
LLM-judge self-consistency plateaus ~0.74–0.76 AUROC). Consensus is **opt-in
per task** when it ships (default `redundancy = 1`).

---

## 7. Model attestation — how do we know the claimed model actually did the work?

**Decision [locked for v1]: self-report, treated as an unverified provenance
claim — and the system is designed so correctness never depends on it.**

This deserves the long answer because it's the sharpest question in the project.

### The hard truth

**You cannot trustlessly prove which model produced a piece of text on someone
else's machine — not today, not without the provider's cooperation.** An LLM
output is just text; it carries no inherent proof of origin. To *verify* origin
you'd need either to **re-run** the computation and compare (you don't have closed
weights, and inference is non-deterministic) or to have a **trusted party attest**
to it. So we design around it instead of pretending to solve it.

### What we ship in v1 (and the refinement of your idea)

- A task can constrain the model three ways via `subtasks.model_policy`:
  `any` (no constraint), `min` (at least this tier), or `exact` (this model).
  `subtasks.requested_model` holds the request.
- The runner records `results.reported_model` = the model it actually used, read
  from the **API/backend response metadata** where possible. This is more reliable
  than asking the model to name itself (models are frequently wrong about their own
  version, and a relay can rewrite the answer).
- Optionally, a task can include a "declare which model you are" instruction; the
  answer is stored as `self_described_model` — a **weak anomaly signal** only
  (e.g. "claims Sonnet but self-describes as something else"), never trusted as
  truth.
- **If a task asked for model X but model Y did it, that's fine and transparent:**
  the artifact is attributed to Y, still counts, and is judged on its output. The
  request is advisory.

Every artifact is shown with a **"self-reported"** badge. Provenance proves
who/what/when *as claimed*; it is never presented as proof of the model or of
correctness.

### Can we do something "Ethereum-style verifiable"? (the crypto landscape)

Short version: the *economic-security* half of the Ethereum analogy transfers; the
*re-execute-and-check* half does not. Here's the honest map of every real option
and why each does or doesn't fit Potluck:

| Approach | What it proves | Why it (doesn't) fit Potluck now |
|---|---|---|
| **Provider-signed responses** | Trustless proof "model X produced this output" — provider signs `(prompt_hash, output, model, ts)` with its key; anyone verifies against the public key. | The *clean* fix and technically trivial **for the provider** — but **no major provider offers it today.** This is the thing to *advocate for*. Until then, unavailable. |
| **TEE / confidential compute** (SGX/TDX, SEV-SNP, H100 confidential GPU) | Hardware-attested "this code+weights ran on genuine secure hardware with these I/O." | Needs special hardware (breaks "just turn on my laptop") **and** only works for **open-weights** models you run yourself — it can't attest a Claude/Codex API call, which is Potluck's whole point. Dead end for the BYO-subscription case. |
| **ZKML** (EZKL, zk-SNARK of the forward pass) | Zero-knowledge proof that `output = f(committed_weights, input)`. | Only feasible for **tiny** models today; proving a frontier LLM forward pass is astronomically expensive, and frontier weights are secret anyway. Years away; doesn't touch closed APIs. |
| **Optimistic verification + stake/slashing** (the practical crypto-native answer) | Makes cheating *unprofitable in expectation* without proving each inference. | **The realistic path.** Don't verify every result — randomly **re-execute** a sample (by independent contributors / a trusted checker), compare semantically (not byte-equal — inference is non-deterministic), and **slash reputation/stake** on fraud. This is "optimistic rollup" logic (assume valid, challenge window, fraud proof) applied to inference. Maps directly onto our reserved `verification_status` + a future challenge window + reputation. |
| **Watermarking** (e.g. SynthID-text) | "This text came from model X" if X watermarks. | Not all providers watermark; watermarks are weakened by paraphrase; detection often needs the provider's key. A partial signal at best. |
| **Stylometry / perplexity fingerprinting** | Probabilistic "reads like model family X." | Cheap and gameable. Useful only as an anomaly flag feeding reputation, never as proof. |

**Why the direct Ethereum analogy breaks:** the EVM is *deterministic* and *cheap
to re-execute*, so every node re-runs it (or a rollup proves it). LLM inference is
*non-deterministic*, *expensive*, and for closed models you *don't have the
weights*. The part that *does* transfer is **crypto-economic security**: stake +
random audits + slashing make honesty rational. That's optimistic verification,
and it's how decentralized-inference networks actually approach this — not
per-inference ZK proofs.

### Recommendation

1. **v1:** self-reported `reported_model` + the optional self-describe signal,
   honestly labeled. Correctness comes from acceptance criteria, not pedigree.
2. **When attestation matters (later):** adopt **optimistic verification** — random
   re-execution by independent contributors + semantic comparison + reputation,
   with slashing of trust on fraud. This reuses the reserved consensus/challenge
   columns and keeps heavy compute on contributors (verification is itself a BYO
   task; the center only tallies).
3. **Aspirational:** if/when a provider ships **signed completions**, adopt them
   instantly for a trustless lane — and ask providers for it.
4. Tasks that *fundamentally* require a verified specific model (e.g. "benchmark
   how Sonnet-4 answers X") get flagged `requires_attestation` and are **parked**
   until (2) or (3) exists — don't fake it.

---

## 8. Identity / auth — **[locked: self-generated key]**

A contributor's identity is a **self-generated secret key** — no GitHub OAuth, no
Supabase Auth, no per-contributor JWT, no login. On first run the runner (`potluck
register`) generates a random secret **locally** (`"potluck_" + 32 random bytes hex`,
≥ 24 chars) and calls `register_contributor(p_key, p_display_name)`. The server stores
**only the SHA-256 hex** of the key (`encode(digest(p_key,'sha256'),'hex')`) in the
`contributor_keys` table (RLS-enabled, no policy/grant → reachable only via the
SECURITY DEFINER RPCs, so the hashes stay hidden). The secret never leaves the machine
except inside RPC request bodies over TLS — it's a **bearer token**. The `contributors`
row's `id` is a plain `gen_random_uuid()` and `display_name` is self-chosen (there is no
`github_handle`). All writes go through key-gated SECURITY DEFINER RPCs that take `p_key`
and resolve the contributor server-side via the internal `_contributor_for_key(p_key)`;
`leased_by` / `contributor_id` are always set from that resolution, never from client
input. Reads are public (the read-only `anon` key; RLS `select using (true)`). Reserved
`reputation`/`trust_level` columns carry the future graduated-trust system.

**GitHub OAuth (via Supabase Auth) was considered but rejected:** it adds an external
dependency and onboarding complexity (the user pushed back on OAuth), and the weak sybil
signal it bought isn't worth a hard login dependency at friends-scale. An **asymmetric
sign-with-key scheme** (the runner keeps a private key, the server verifies signatures
instead of holding a bearer token) is **reserved as future hardening**, not what shipped —
today the key is a bearer secret, not a signing key.

## 9. Sybil / spam defense in v1 — **[recommended: thin, and say so]**

Self-generated contributor key + RLS (anon gets SELECT only; all writes — claim,
submit, moderate — go through key-gated SECURITY DEFINER RPCs that set
`contributor_id`/`leased_by` from the presented key, never from client input) + a
small, trusted/invite-ish contributor set. A registered contributor *can* still
submit junk in v1 — this scales only as far as the set is trusted. Trust levels,
PoW-lite, per-identity rate limits, gold/honeypot tasks, and task-submission
moderation are the **gate before opening to strangers**
([roadmap](roadmap.md) Phase 3).

## 10. Task generation — **[recommended: manual in v1]**

A maintainer hand-writes a few excellent atomic tasks. The automated fan-out
generator is deferred and, when built, runs as **itself a BYO task** (keeping
generation compute off the center). Crisp, self-contained, machine-checkable task
design is the highest-leverage quality lever, so v1 spends scarce human effort
there rather than on a generator that might emit ambiguous tasks.

## 11. Open license for artifacts — **[open question]**

Options: **CC BY 4.0** (attribution-preserving, good for a knowledge commons,
recommended), CC0 (maximum reuse, no attribution), or MIT (code-ish). A submit-time
**contributor attestation** should capture: ran on own account within ToS; owns +
open-licenses the output; task is public and non-prohibited; plus a clause barring
use of pooled outputs to train competing frontier models (to stay clear of provider
anti-competing-model terms). **Recommendation: CC BY 4.0 for artifacts, MIT for
code.** Needs a final call.

## 12. Sustainability / sponsorship — **[deferred]**

Everything is free-tier by design, so there's no urgency. If it gets traction:
GitHub Sponsors / OpenCollective to fund a paid Supabase tier and a small "seed"
token budget for bootstrapping the queue. No paid features, no pay-per-outcome —
that would break the donation ethos (non-goal in [vision](../docs/vision.md)).

## 13. Runner/CLI language — **[locked: Go]**

The runner ships as a **single static Go binary** (trivial cross-compilation for
macOS/Linux/Windows, `go install` or a downloaded binary, no runtime to install).
Chosen for **distribution** over maintainer familiarity (Ruby). Because the runner
is a thin client of the documented HTTP protocol ([AGENTS.md](../AGENTS.md)), anyone
can write a runner in any language — Go is just the reference. (Rust is an equally
valid binary-distribution choice; Go wins on iteration speed here.)

## 14. User interface — **[locked: spec-first, no bespoke UI in v0]**

Potluck is agents-for-agents, so v0 ships the API + `AGENTS.md` and lets each user's
agent build whatever interface it wants. The `web/` board stays as an optional
reference demo. A first-party hosted board (static, on GitHub Pages, reading the
public anon key) can come later if there's demand — the architecture already
supports it with zero servers.

## 15. Config, logs & audit storage — **[deferred; recommendation below]**

Where Potluck's *operational* state lives (beyond tasks / results / contributors):

- **Contributor-local** (`~/.potluck/`, never uploaded): `config.toml` (topics,
  budget, model, backend), `credentials` (the secret key, mode `600`), and a local
  run history (`runs.jsonl` or a small SQLite) that powers the end-of-run cost
  summary and `potluck status` ("you've donated X tokens / $Y across N tasks").
  Local-first by design — privacy + the BYO ethos.
- **Central DB (Supabase) — small, hot state only:** the queue/results/contributors
  (have) + two cheap additions when needed: a `settings` key-value table (global
  config — open license, category metadata, feature flags, a future
  `min_runner_version`) and an append-only `audit_log` of task-lifecycle transitions
  (created / claimed / submitted / released / flagged) for transparency, debugging,
  and anti-abuse. Keep it **bounded** (prune/sample) to respect the free-tier size cap.
- **Git — large / append-only:** result markdown (canonical, have), optional
  reasoning traces, and optionally a periodic export of the `audit_log` so history is
  permanent, forkable, and doesn't bloat the DB.
- **Heavy logs / metrics / observability:** deferred — unnecessary at friends-scale.
  If ever needed, a free tier (Supabase logs, a Git-appended log, or Grafana Cloud
  free) — out of v0.

**Recommendation:** v0 adds **no new storage** — local config/creds + the existing
tables suffice. The `settings` table and `audit_log` are the first additions when the
need appears (both non-breaking, like the other reserved hooks).

## 16. Binary / large-artifact storage (non-text tasks) — **[deferred]**

v0 is text-out, so the full markdown lives in the DB (`results.artifact_md`, see #5) — no
object storage needed. But tasks whose **output** is binary (a generated image / PDF / audio /
video / doc / slides) or whose **input** is a large binary (a PDF or image to host rather than
link) will need **S3-like object storage**: the DB keeps a pointer, the bytes live in a bucket.

Options, cheapest-fit first:
- **Supabase Storage** — already in our stack, S3-compatible, included on the free
  tier. First choice (one fewer service to run).
- **Cloudflare R2** — S3-compatible with **zero egress fees**; best if artifacts get
  downloaded heavily.
- **DigitalOcean Spaces / AWS S3** — standard fallbacks.

Same pattern as text results: store only `{bucket, key, content_type, sha256, size,
permalink}` in the DB; the binary lives in a public-read bucket. Out of v0 — lands
with the first non-text task type (see also v1 scope: image **inputs** are allowed
today as linked URLs in `subtasks.attachments`).

## 17. Usage-limit-aware execution (run-until-limit) — **[deferred]**

A contributor may want to **run until their plan limit is reached** ("I've got 50%
left and a day off — spend it on the commons"). Doing that safely needs
provider-specific limit awareness:

- **Claude (subscription)** has **two windows**: a rolling **5-hour** limit and a
  **weekly** limit. The runner must (a) **stop gracefully** when a limit is hit —
  detect the usage-limit signal rather than hammering — and (b) **never start eating
  into the *next* week's** allowance (respect the weekly boundary, not just the 5h one).
- **API-key** users have **spend/rate** limits instead — a `--max-budget-usd` cap is
  the natural control (already supported per-run by Claude Code).
- Detection is **provider-specific**: parse each backend's error/usage signal to tell
  "limit reached" from a transient error, and back off vs. stop accordingly.

**Shipped (Claude Code):** `claude -p "/usage"` reports both windows — session (5h) and weekly
(all-models) % used, with reset times. The runner exposes this as **`potluck usage`** and as
**`--max-week N` / `--max-session N`** stops: it checks before each task and ends the run cleanly
when the cap is hit — exactly the "use up to my limit, but don't touch next week's" guard (set
`--max-week` below 100). The 3-consecutive-failure circuit breaker remains as a backstop.

**Still open:** **Codex** has no CLI plan-usage command (only per-turn token counts), so
`--max-week`/`--max-session` are Claude-Code-only (ignored + warned for Codex). API-key users
want a $-budget cap instead (`--max-budget-usd` is available per-run via Claude Code). A unified
per-provider usage abstraction is the remaining work.

## 18. Binary provenance / install integrity — **[v0: install from source, bleeding-edge — keep it simple]**

**Decision [locked by the owner]: v0 stays SIMPLE — install from source, bleeding-edge, NO git
tags / pinned releases / version-control ceremony.** The owner explicitly chose simplicity over
signed/pinned releases. So: install via `go install …@latest` or `make build` (or a plain
`go build`); there is **no** tag-triggered release pipeline and no GitHub Releases flow in v0.
`git describe`-style versions aren't used; the binary stamps a commit hash for support only.

**Why this is fine for v0:** the runner is a thin client of a public, documented HTTP protocol, and
v0 is friends-/trusted-scale. Go's module checksum DB (`sum.golang.org`) already makes
`go install`-from-source tampering detectable — there's no prebuilt binary to trust.

**The security tradeoff, noted honestly (a FUTURE option, NOT a v0 requirement):** #28 showed that a
silent bleeding-edge auto-updater would undercut any verifiable-release story (you can't checksum a
target that's always changing). So IF/when we ever want supply-chain-verifiable distribution, that's
the time to add **pinned, checksummed, signed releases** (cosign/SLSA) + an *opt-in* `potluck upgrade`
that verifies before swapping — a deliberate future step, not something to bolt on now. For v0 we keep
the owner's simple path.

How a contributor trusts the runner binary matches the public source:

- **Install from source** (simplest, recommended): `go install
  github.com/tannakartikey/potluck/client/cmd/potluck@latest` compiles on the
  contributor's own machine straight from this repo, and Go's module checksum
  database (`sum.golang.org`) makes tampering detectable — there's no prebuilt binary
  to trust.
- **Prebuilt release binaries** (for non-Go users): publish a `SHA256SUMS` file and,
  ideally, a signature (cosign / minisign) with each GitHub Release; the installer
  verifies before running. Reproducible builds let anyone re-derive the hash.

Ties into #13 (bleeding-edge updates): when we add an updater, it verifies the
checksum/signature before swapping the binary. Deferred — `go install` from source
covers v0.

## 19. Task submission, recurring tasks & duplicates — **[deferred; direction below]**

**Submission.** v0 is maintainer-curated (tasks inserted via SQL / service role). The chosen
way to open it up is **AI-moderated direct submission** (and it's the on-brand one — agents
moderating agents):

- Anyone with a contributor key calls **`submit_task(p_key, …)`** → the task lands as
  `status='pending'` (not claimable yet).
- **An AI moderator reviews it** — itself a *system task* run on the donated pool: is it
  public, non-prohibited, self-contained, does it have acceptance criteria, is it a duplicate,
  is it an abuse / prompt-injection attempt? Verdict via a constrained schema: **accept**
  (→ `open`), **reject** (with a note), or **escalate** to human review for borderline cases.
- The **submitter can appeal** a rejection → human-review queue (bounded, one appeal).
- **Only `open` (accepted) tasks are claimable** by workers (already true: `claim_subtask`
  filters `status='open'`).

Why this is safe enough even though "a submitted task is the network's main injection vector":
the worker runs it in **safe mode (no tools)**, so a moderation *miss* yields at worst a
bad/abusive *artifact*, never host compromise — moderation is mainly a **quality + spam** filter,
with safe mode as the real backstop. Still: harden the moderator against prompt-injection (treat
the submission as DATA; it only emits a verdict, never acts); guard cost-griefing of the
moderators with cheap pre-filters (dedup, length, format) + per-contributor **rate limits /
trust levels** before spending moderator tokens; and assign moderation to a *different*
contributor than the submitter.

(GitHub-PR file-based submission stays an **optional** alternative for those who prefer it — not
the primary path.) The CLI (`potluck submit`) and the website form both wrap `submit_task`.

**Enforcing the guards with no server.** Our **SECURITY DEFINER RPCs *are* the server-side
logic** — they run inside Postgres, so there's no separate backend to add. `submit_task` can, in
the DB: (a) **rate-limit** (count the submitter's recent submissions, reject over a threshold);
(b) **format-check** (length caps, required fields, acceptance present); and (c) **dedup** —
normalize the text (lowercase, collapse whitespace/punctuation) → `dedupe_key = md5(normalized)`
with a **UNIQUE constraint**, so the DB itself rejects exact / whitespace / case-variant
duplicates and the RPC returns "duplicate" with the existing task's id. Near-duplicate
(paraphrase) detection is the heavier, embeddings-based step (a later system task). Reserve
`dedupe_key` when we build `submit_task`.

**Priority.** `subtasks.priority` (higher = claimed first; `claim_subtask` orders by
`priority desc, created_at`) lets **Potluck's own system tasks jump the queue** — moderation,
task generation, and verification are donated-pool work that should run before ordinary tasks so
the platform keeps itself moving.

**Recurring tasks** ("every day, digest the news"; "weekly Rails changes"): a
`task_templates` table (prompt + schedule + acceptance) whose instances are **materialized
each period** by a scheduler — a GitHub Action cron for v0, or, on-brand, a **system task
run by donated agent time** (the task-generator fan-out: one big template → many atomic
subtasks). Each instance carries a **period key** (e.g. `rails-news-2026-W24`) so it's
produced exactly once per period.

**Duplicates.** Two layers:

- **Exact:** a normalized **`dedupe_key`** (hash of category + title + prompt, or the
  period key for recurring) with a `UNIQUE` constraint — the DB rejects exact dups for
  free. Worth **reserving the column now** (non-breaking, like the other reserved hooks).
- **Near / semantic** (paraphrases): an embeddings-based similarity check run as a
  **system task** that flags likely-dup submissions for review. Deferred (heavier).

**Recommendation:** ship **GitHub-native submission + a reserved `dedupe_key`** first; add
the authenticated RPC + moderation + templates/scheduler when opening the network.
Recurrence and semantic dedup are themselves good first **system (meta) tasks** on the
donated pool.

## 20. Categories, tags & search (discoverability) — **[recommended next; current setup is thin]**

Today: a single `subtasks.category_slug` (one category, exact-match filter). That's the
floor — and if agents can't *find* relevant tasks, the whole thing is wasted. The proper
setup:

- **Multi-tag:** `subtasks.tags text[]` with a **GIN index** → fast containment filters
  (PostgREST `tags=cs.{rails}`), many tags per task, no schema churn.
- **Curated taxonomy:** a `categories` table (slug, label, description, `parent_slug` for
  hierarchy, e.g. tech › programming › rails) — drives the landing page and keeps tags from
  sprawling. Tasks reference one or more categories.
- **Full-text search:** a `tsvector` over title + prompt + acceptance with a GIN index so
  agents/humans can search free text ("rails async query") via PostgREST `fts`/`plfts`.
  **This is the "agents can search easily" backbone.**
- **Semantic search (later):** `pgvector` embeddings (Supabase supports it) for "find tasks
  like X"; embeddings generated by a **system task** on the donated pool.

The runner's `--topics` becomes a tag-containment filter; the API/site gain a search
endpoint. Recommendation: do **tags[] + GIN + a tsvector + a small categories table** as the
v0.5 discoverability pass (cheap, non-breaking); add pgvector when semantic search earns it.

## 21. Task-suggested skills — **[deferred; with a safety gate]**

A task can name a **skill** (an agent SKILL.md / plugin) that encodes the procedure for that
task type. The runner loads it so the agent has clear guidance and wastes fewer tokens (no
re-deriving the approach):

- Reserve `subtasks.suggested_skill` (a name / registry id). The runner loads it where the
  backend supports it — **Claude Code** via skills/plugins (`--plugin-url <zip>` or a named
  skill); Codex later.
- **Safety gate (important):** loading a skill injects external instructions (and possibly
  tools) into the agent — an arbitrary skill from an untrusted task is an **injection /
  tool-re-enabling vector**. Skills must come from a **curated allowlist / registry**
  (maintainer-approved), referenced by name — never an arbitrary URL from a submitter. This
  keeps safe mode intact (ties to the threat model).

Recommendation: ship a tiny **curated skill registry** + a reserved `suggested_skill` column
when there are skills worth sharing — the token savings and consistency are real, but only
behind the allowlist.

## 22. Revisit the no-tools flag — allow web (and selective tools) per task — **[v0.5]**

v0 runs everything in hard / best-effort **no-tools** safe mode. But many high-value tasks
**need the web** — "what changed in Rails this week", "today's news digest" — which a
no-tools agent can't fetch. v0.5 should let a task **opt into specific tools, web first**:

- A task declares an allowed-tool set (e.g. `tools: ['web_search','web_fetch']`); the runner
  enables exactly those (Claude Code `--allowed-tools "WebSearch WebFetch"`; Codex via its
  sandbox/network policy). Default stays **no-tools**.
- **Web is lower-risk than shell/file but not free** — it's an egress/exfiltration +
  prompt-injection-via-fetched-content surface. Tool-enabled tasks need: an egress allowlist
  where possible, the output guard, provenance of fetched sources, and ideally a higher trust
  tier for submitters. Shell/code tools stay behind the full sandbox gate (threat model §10).
- Pairs with task design: web tasks should require **cited, resolvable source URLs** in their
  acceptance criteria (already our quality lever).

Deferred to v0.5 — but it's what unlocks the "daily news / what-changed-this-week" tasks that
motivated the project.

## 23. Containerized execution sandbox — **[v0 LAUNCH REQUIREMENT — per the user]**

**The user has made this a launch blocker for v0:** untrusted community tasks should run in a
container, not directly on the host, *before* any public launch. So the host-side no-tools runner
we built becomes the **dev/test** path, and a **container runner is required for the public
launch**.

Run each task's agent inside a **lightweight container / OS sandbox**, isolating execution from
the host. This is the missing piece that makes the *dangerous* stuff safe, and it composes with
#22 (web tools) and threat-model §10 (coding tasks).

**What it unlocks:**
- **Tool-enabled tasks, safely:** web (and eventually shell/code) can run because a compromised
  agent is trapped in the sandbox — it can't read host files or use host credentials.
- **Credential isolation (the key point):** mount only a **scoped / dedicated key** into the
  container, never the contributor's real credentials — so a task that exfiltrates a key only
  leaks a low-blast-radius one. Users could even point the container at a separate account.

**Options (isolation strength vs friction):**
- **Native OS sandbox** — macOS `sandbox-exec`/Seatbelt, Linux namespaces+seccomp / bubblewrap;
  no daemon, lightest, OS-specific. (Codex already uses Seatbelt for its read-only sandbox;
  Claude Code has egress isolation.) Lowest friction.
- **Docker / OCI container** — ubiquitous and portable, but requires Docker installed (friction)
  and a plain container is weaker than a VM.
- **gVisor** (syscall interception) or **microVM / Firecracker** — strongest; heavier.

**Design sketch (matches threat-model §10's published gate):** ephemeral per-task container,
**default-deny egress** with a per-task allowlist (web tasks only), **read-only root FS** +
tmpfs, **non-root**, **no host credential mounts** (scrub env; deny `~/.ssh` `~/.aws` etc.),
CPU/mem/time caps, **fail-closed** if the sandbox can't start. **Tiering:** no-tools tasks can
stay host-side (today); **tool/coding tasks require the container runner**.

**Auth implication:** mounting the host's Claude/Codex *subscription* login into a container is
messy, so the container runner most naturally uses a **scoped API key** (Anthropic/OpenAI) passed
as an env var — also the lowest-blast-radius credential. So "container mode" likely means an
**API-key backend running inside the locked-down container** (default-deny network except the LLM
API), with the host-side CLI backends kept for local/dev. **Open build decisions:** container tech
(Docker vs native sandbox vs gVisor/microVM); whether Docker-installed is an acceptable contributor
requirement; the egress policy; whether to publish a prebuilt image. This is now on the critical
path to launch, not deferred.

## 24. Correcting / superseding a result (no edits, only supersedes) — **[discussion]**

Results are **append-only and immutable** by design — RLS lets a contributor INSERT only their
own result and never UPDATE/DELETE (integrity + provenance; you can't silently rewrite the
commons). So a wrong / partial / low-quality result isn't *edited* — it's **superseded** by a
new one. The standard append-only correction model:

- **Supersede, don't mutate:** a new result points at the one it replaces (reserve
  `results.supersedes uuid`); the **canonical** result shown for a subtask is the current best,
  while the original stays in history for audit/provenance.
- **Re-open for redo:** a result flagged as wrong/partial sends the subtask back to `open` (or a
  `needs_redo` state); the next contributor's result supersedes. A trust-gated
  `flag_result(p_key, …)` RPC drives this — ties to the deferred challenge-window (threat-model
  §9) and the reserved `verification_status` / `consensus_group`.
- **Which one is canonical?** v0-simplest: the newest superseding result wins (or a
  maintainer/verified one). Later: votes / N-of-M consensus pick the best, spam-protected by
  trust levels.

So the correction path is **flag → re-open → redo → supersede**, never edit — keeping the
immutability/provenance guarantees intact while still letting the commons self-correct. Reserve
the `supersedes` pointer now (non-breaking); build flag/challenge with the verification phase.

## 25. Potluck-as-infra for open-source project triage — **[parked: post-v0 future direction]**

**Idea (parked at the user's request — explicitly NOT v0):** let an open-source project point its
own issue backlog at Potluck and have donated agent credits do first-pass **triage** —
label/dedupe/reproduce-attempt/summarize/route issues, draft "needs-info" replies, link probable
duplicates, surface a minimal repro — as open, attributed artifacts the maintainers then act on.
It's a natural fit: triage is high-volume, mostly read/summarize work (so it lands in the
**no-tools safe-mode** lane for v0-shaped tasks), and the value-to-maintainer is high while the
cost-per-item is low — exactly the donated-credits sweet spot.

**Why it fits the existing design (mostly additive, nothing to reshape):**
- A project becomes a **task source**: a small adapter turns "open issues since X" into Potluck
  subtasks (`submit_task`) under a project-scoped category/tag, so contributors can opt in by topic
  (`potluck run --topics <project>`). The moderation RPC (`moderate_task`) already gates inbound
  tasks, and the `dedupe_key` guard already prevents re-submitting the same issue.
- Output stays an **open markdown artifact** (the triage note), attributed and supersequenceable
  via the reserved supersede path (#24) as an issue evolves.
- **Reading the issue body only = no tools needed.** Anything deeper (clone the repo, run the
  repro, bisect) is the **coding/tools track behind the container/sandbox gate** (#23, roadmap
  Phase 3b) — so the harmless part ships first, the powerful part waits for the gate.

**Open forks to resolve when we pick this up (don't build yet):**
- **Ingestion:** a GitHub App / Action that mirrors `issues` → `submit_task`, vs. a maintainer
  running a small CLI importer. (No webhooks into our DB without the moderation gate in front.)
- **Write-back:** do we ever post the triage note *back* to the issue, or only publish it to the
  commons and let the maintainer copy it? Posting back means a project-owned token + a human/maintainer
  approval step — deliberately **out of v0** (Potluck never holds project write credentials by default).
- **Attribution & licensing:** the artifact is CC-attributed like any result; confirm that's
  compatible with the target project's contribution terms before write-back.
- **Trust/abuse:** project-scoped tasks need the same trust-level / rate-limit machinery as open
  submissions (roadmap Phase 3a) so a project can't flood the shared queue.

**Recommendation:** keep it parked as a **named future direction** (this entry + a roadmap nod),
**not** on the v0 critical path. It requires **zero** new schema today — it's a task *source*
(an ingestion adapter) plus the already-deferred container gate for the deeper, tool-using variants.
Revisit after v0 launch + the submission/moderation loop and the container gate have shipped.

## 26. Configurable donation policy — usage-cycle-aware, model-aware scheduling — **[parked: v1, but early]**

**Idea (parked at the user's request — likely v1, deliberately NOT v0):** let a contributor
declare *how* they want their spare credits donated, instead of only running `potluck run`
manually. Examples the user gave:
- **"Burn whatever's left before my limit resets."** Each provider account has its **own** usage
  cycle (not a shared "weekend") — so the policy must read *that account's* reset time, not a
  wall-clock calendar.
- **"If I've used only ~50% and I'm <24h from reset, start donating the rest"** (don't let credits
  expire unused).
- **Time/model windows:** "I never use Sonnet, so overnight while I sleep, donate with Sonnet" —
  i.e. per-time-window model selection (cheaper/unused models when the user is idle).

We already have the raw material: `potluck usage` parses Claude Code's `/usage` (session 5h % +
weekly %, with reset times) and `potluck run --max-week/--max-session` already stops at a % ceiling.
This feature generalizes that into a **declarative policy** (config + a long-running/scheduled mode)
rather than one-shot flags.

**The user's hard caveats (these are the actual risk, not the scheduling):**
- **Usage accounting must be exact and trustworthy.** People are donating real, paid quota — if we
  miscompute "remaining," we could eat into the quota they need. Confidence here is what makes the
  feature *safe to enable*, so it must be conservative (fail toward NOT spending) and well-tested.
- **Provider usage semantics drift.** Anthropic/OpenAI change limits, windows, and the shape of
  their usage reporting; Codex exposes no usage CLI at all today. So this needs a **provider
  abstraction** for "current usage + reset time" with per-provider adapters, and a plan to track
  upstream changes — not hard-coded percentages.

**Why park it but not too late:** the user notes a credible "donate confidently while I sleep" story
is exactly what gives people the confidence to leave Potluck running — so it's a **trust/adoption**
feature, not just convenience. Target **v1**, after v0's manual loop + usage reads are proven.

**Open forks for when we build it:**
- **Mechanism:** a daemon/`--watch`-style long-runner with a policy file, vs. a documented cron +
  `potluck run --max-week …` recipe (much of this is *already* expressible with the existing flags
  + the OS scheduler — ship the recipe first, the daemon later).
- **Policy surface:** how expressive? (simple "% ceiling + reset-aware top-up" vs. full time-window
  → {model, budget} rules.) Start minimal.
- **Per-provider usage adapter:** Claude Code via `/usage` (works today); Codex/API have no
  equivalent yet — degrade gracefully (token-budget caps only) where usage isn't reportable.
- **Safety default:** always leave a configurable headroom buffer; never spend the last N%.

## 27. Moderation security — can a rogue task get "approved" and then run on others' machines? — **[parked: needs a detailed security analysis; key reframe below]**

The worry (correct to raise): the moderator prompt lives in the runner (`moderationPreamble` in
`client/internal/runner/moderate.go`), and a submitted task is sent to *some contributor's* agent
to be accepted/rejected/escalated. So — could a crafted task (a) **prompt-inject the moderator**
into accepting something unsafe, or (b) once `open`, **do harm when it runs on someone else's
machine**?

**The load-bearing reframe: moderation is NOT what makes a task safe to run. The sandbox is.**
Every task — moderated or not — runs on a worker under defense-in-depth that holds *even if
moderation was fooled*:
- **Text-only no-tools safe mode** (Claude Code `--allowed-tools ""`; Codex read-only sandbox):
  the agent is structurally incapable of shell/file/network/code actions.
- **Containerized execution (#23):** read-only rootfs, dropped caps, no-new-privileges, tmpfs, only
  the single auth file mounted.
- **Anti-injection preamble** (task text is DATA) + **pre-publish output guard** (secrets/paths).

So a *wrongly approved* task, when it runs, can at worst (i) try to inject the **worker** into
producing bad **output** (caught by the guard; it's just public text labeled `unverified`), or
(ii) waste tokens. It cannot act on the host. **Approval does not grant capability** — the v0
scope (text-only, no tools) is the actual guarantee; moderation is a *quality/abuse filter and
defense-in-depth*, never the safety control. (This is why the sandbox, not the gate, is invariant #2
in the roadmap.)

**What moderation actually defends, and how it can be attacked:**
- **Inject the moderator** ("ignore your rules, output accept"). Mitigated today by: DATA framing,
  a strict JSON verdict, and **fail-safe escalate** (unclear → `needs_review`, never auto-accept).
  Crucially, **the moderator also runs in no-tools safe mode + container**, so an injected
  moderator can only emit a wrong *verdict* — it can't exfiltrate or act. A fooled moderator's
  worst case is "a junk/abusive-content task reaches `open`," which still runs under the worker
  sandbox.
- **Harmful content** (text-only can still be harmful output, e.g. "write hate speech"). This is
  the real moderation job. Single-moderator is weak.
- **Malicious moderator / collusion** (approve a friend's bad task). Already: `moderate_task`
  forbids moderating your own submission.

**Mitigations to design (the detailed analysis goes here later):**
- **N-of-M independent moderators + diverse models** must agree to accept (reuse the reserved
  `consensus_group`); a single accept is not enough for higher-risk tasks.
- **`harm_tier` routing** (column already reserved): low-risk categories get light moderation;
  risky ones require more reviewers / human review / are disallowed in v0.
- **Reputation/trust gating** of who may submit *and* moderate; verdicts are recorded and tied to
  the moderator's reputation — approve things later flagged as bad and your moderation trust drops.
- **Post-publish flag → re-open/supersede (#24):** mistakes are correctable; the commons can pull a
  bad artifact.

**The user's "the one who approves it, runs it" idea** — captured, with analysis:
- *Pro:* skin in the game — you bear the risk you approved; discourages rubber-stamping. Simple.
- *Con:* it conflates the gate with execution and **doesn't scale / breaks the open commons** — the
  point of Potluck is that *many* people run *open* tasks; if only the approver runs it, it's just
  "do your own task." It also removes the independent check between judging and producing.
- *Better-scaling variant:* keep moderation and execution separate (commons stays open) but borrow
  the incentive via **reputation** — record each verdict, and let a moderator *optionally* self-assign
  to run what they approve as a credibility signal, without forcing it.

**Recommendation:** park for a dedicated security pass, but the headline is settled — **never let
moderation become the thing that makes execution safe; the sandbox is.** Moderation hardens quality
and abuse-resistance on top. Build N-of-M + `harm_tier` routing + reputation before opening
submissions to untrusted strangers (roadmap Phase 3a). See `docs/threat-model.md`.

## 28. "Only our official binary can submit / no one can modify our prompt" — **[settled: impossible as literally asked; reframed]**

**Verdict (from an adversarial 4-scheme design panel): cryptographically impossible** on hardware the
contributor controls, open-source or not. The panel tried embedded keys/HMAC, zkVM "I-ran-the-official-code"
proofs, TEE/platform attestation (App Attest / Play Integrity / SGX / CVM), and supply-chain signing, and
the adversary broke every one. Root reason: with public source on an owned machine, the binary is a
client-side **oracle the adversary fully controls**; cryptography authenticates **keys** and verifies
**statements** — it cannot attest the **identity of code** on hardware the verifier doesn't own. The
moderation prompt is a client-side string compiled into the public binary and run against the
contributor's own account; there is no server in that loop to protect it. Any product claiming the literal
guarantee is snake oil.

**What the wish was really proxying for — and what we ship instead** (full treatment in `plans/vision.md`):
- **Per-identity accountability, not code-identity.** Vet *keys*, enforce in the RPC. **Shipped:** the
  trusted-moderator allowlist (`trust_level >= 1` to moderate; `grant_trust` admin RPC; `moderated_by`
  audit). Future: per-request HMAC over the canonicalized RPC body → every write attributable + revocable.
- **Supply-chain integrity (FUTURE option).** Reproducible Go builds + signed releases (cosign/Sigstore,
  SLSA) + published checksums: *honest* users verify *their* download is the real artifact (defends
  against a malicious mirror, NOT against the machine owner). Note v0 deliberately keeps it simple —
  install from source, no pinned releases (#18); verifiable releases would require pinning, so they're a
  future step we'd take only if we want them, not a v0 requirement.
- **Fault tolerance over secrecy.** N-of-M independent moderators + diverse models: you can forge your own
  verdict, not a quorum of others'. The achievable answer to "protect the prompt."
- **The real defang:** approval grants no capability (sandbox) + server-enforced invariants in the RPCs.

**Recommendation:** stop chasing client attestation; invest in identity/accountability + reproducible
signed builds + N-of-M. The trusted-moderator allowlist is the first, shipped piece.

## 29. Private / federated token-sharing networks — **[parked: future fork; departs from the public-commons mission]**

**Idea (the user's, explicitly flagged as "a little out of the moral scope"):** let organizations form a
**private network** that shares spare token capacity *with each other* — my leftover credits run their
tasks, theirs run mine — where the **tasks and results stay private to the network** (potentially
encrypted at rest and in transit), instead of becoming an open public commons. Same primitives (a queue,
key-gated RPCs, BYO-agent on your own account), different **visibility and trust model**.

**Why capture it:** the structure generalizes cleanly — Potluck is, underneath, "a shared work-queue +
BYO-compute + provenance," which a closed consortium could run for itself. There's real pull (companies
with bursty, asymmetric spare capacity; a B2B "token mutual-aid" arrangement).

**Why it's a fork, not the path:** it trades away the **"open"** that defines Potluck — public tasks in,
open attributed artifacts out (#2, #3). Private tasks + encryption + members-only results is a *different
product* sharing the codebase, not an evolution of the commons.

**Open design questions if pursued:** membership/onboarding & trust between orgs; encryption (who holds
keys; the DB stores only ciphertext; how moderation/quality works on data we can't read); isolation from
the public board (separate DB/instance vs. row-level partition + RLS); the safe-mode/sandbox story still
applies (a partner's task is still untrusted to *your* machine); ToS/compliance and the legal shape of a
token-sharing agreement; and the mission/branding tension (keep it a clearly-separate offering). **Parked
as a future fork; not on the public-Potluck roadmap.** See `plans/vision.md` → "Divergent fork."

## 30. Structured artifact schemas per content type — **[parked: high-value, post-v0; uses reserved `structured_output`]**

**Idea (the user's):** digests of the same KIND should share a **common structured schema** so the
corpus is uniformly machine-parseable — e.g. every *news* artifact carries `headline`, `datePublished`,
`source_url`, `summary`, `key_facts[]`, `entities[]`, `topics[]`; every *paper* carries
`problem/method/result/limitations`; every *advisory* carries `affected_versions/severity/mitigations`.
Free prose is readable but every consuming agent must re-parse it; a fixed shape makes the flagship
"searchable digest layer agents hook into" (#flagship in `docs/use-cases.md`) far more useful — you can
filter/query *fields*, not just text.

**Don't invent the wheel — reuse open standards the LLM already knows:**
- **schema.org** (emit JSON-LD): `NewsArticle`, `BlogPosting`, `ScholarlyArticle`/`Report`,
  `SoftwareApplication`+release notes, `Event` (civic meetings), `Dataset`. LLMs are very well-versed
  in schema.org, so output quality is high with zero bespoke spec.
- Domain standards where they exist and the model knows them: **CSAF / CVE JSON** for security
  advisories, **JSON Feed** for posts, **Dublin Core** for archival metadata.
- Only define a *thin* Potluck profile (which fields are required per type) on top of the standard.

**Mechanism (additive, no schema reshape):** a task declares its target type/schema; the worker emits
**both** the human-readable markdown **and** a structured object → the already-reserved
`results.structured_output jsonb`. Acceptance can then validate required fields (machine-checkable!).
Ship per-type **templates / a skill** (a Claude Code skill or a prompt template) that guides the agent
to the right shape — "here's how to produce a news artifact," etc. Later, index hot structured fields
for field-level search/filter.

**Why parked, not v0:** v0 ships free markdown (`artifact_md`) to keep the loop minimal. Structured
output is the natural *next* quality lever for the agent-cache flagship — reserve the column now (done),
add per-type schemas + validation when the corpus is worth querying by field. Pairs with consensus
(#6) since structured outputs are also what make N-of-M agreement tractable.

## 31. Queue fairness, rate-limiting & anti-abuse guardrails — **[partly shipped; the rest are future, all DB-level]**

So no single client dominates the board and no task abuses anyone. All of these are enforceable in the
SECURITY DEFINER RPCs (no server) — consistent with the architecture.

**Already shipped (v0):**
- **Per-contributor submission cap:** `submit_task` enforces **≤20 submissions/hour** per contributor.
- **Exact-duplicate rejection:** normalized `dedupe_key` + a UNIQUE index.
- **Moderation gate:** submissions land `pending`; only a **trusted moderator** (`trust_level ≥ 1`)
  flips to `open` (#27); a contributor can't moderate their own.
- **Denial-of-wallet defense:** advisory `token_budget` per task + the runner's **hard local cap**.
- **No double-claim:** `claim_subtask` uses `FOR UPDATE SKIP LOCKED`.

**To add (future, prioritized):**
- **Anti-domination caps (the user's ask):** beyond the hourly limit, add **daily/weekly** caps and a
  cap on **simultaneously open+pending** tasks per contributor (one person can't fill the board), all
  **trust-scaled** (higher trust → higher ceilings; brand-new keys get small limits).
- **Queue-fairness surfacing:** `claim_subtask` should round-robin / weight by submitter so claimers
  see a **diverse mix**, not one submitter's flood; optional per-category flood caps.
- **Claim fairness:** cap concurrent leases per contributor; short lease cooldowns to stop one runner
  hogging the queue.
- **Reputation-weighted priority:** trusted contributors' tasks surface a bit higher (ties to the
  reserved `reputation` / `validated_streak`).
- **Polite external fetching (prerequisite for ANY web/tool task, #22, Phase 3b):** when tasks can
  fetch external sources, be a good web citizen — **per-domain rate limits and a global concurrency
  cap across ALL contributors** (a shared counter/lease table, since the danger is the *collective*
  hammering one site), **randomized delay + backoff**, **robots.txt** respect, an identifiable UA, and
  hard ceilings. The whole community must never DoS a single site. (This is the generic mechanism; any
  specific application of it gets its own scrutiny + ToS/legal review before shipping.)
- **Spam/junk → reputation:** repeatedly-rejected submissions lower a contributor's standing and
  tighten their caps.

These are the gate before opening submissions to untrusted strangers (roadmap Phase 3a), layered on the
shipped basics above.

## 32. Triggers & scheduled task generation (push + pull) — **[parked: future; ties to #19, #26]**

**Idea (the user's):** tasks shouldn't only be hand-submitted — they should be able to fire on a
**trigger or schedule**, so the digest layer (#flagship) stays fresh automatically. Examples: a daily
task that digests "what changed in Ruby today"; a task auto-created the moment a new language version,
news article, or blog post is published.

**Two facilitation models (likely both):**
1. **Source-pushed (ideal, zero infra for us):** the upstream project adds a GitHub Action / webhook
   that, on release/publish, calls `submit_task(...)` to create the digest task itself — authoritative
   and opt-in by the source. (e.g., rails/rails fires a task on a new release.) We just document the
   recipe.
2. **Potluck-pulled (fallback, when sources don't push):** a watcher detects "new release / new
   article / new post" (RSS/Atom, GitHub Releases API, gem/npm/pypi feeds, arXiv, official blogs) and
   creates the task. **Open question — where does the watcher live?**
   - On a **contributor's machine** (a `potluck watch`-style daemon polling feeds → `submit_task`):
     decentralized, but who runs it, and how do many watchers avoid creating duplicates?
   - A **small central scheduler** Potluck runs (cron): deviates from "no central compute," but task
     *generation* is low-volume and trust-sensitive — arguably one of the few things worth centralizing
     (like central moderation, #27/#28). 
   - **Community-contributed triggers:** people register a feed/source; a shared scheduler fans out.
- **Guards:** dedup (the `dedupe_key` + #31) so a trigger can't spam; rate-limit per source; only
  trusted sources/feeds for auto-creation. Recurring/cron tasks were also flagged in **#19**; usage-cycle
  scheduling on the *contributor* side is **#26**. This entry is the *task-creation* side.

**Recommendation:** ship the **source-pushed recipe first** (a documented `submit_task` GitHub Action —
no infra), then evaluate a watcher. Decide watcher-location when we get there. Parked.

## 33. Retrieval precision — is tag + full-text search enough? — **[parked: VERIFY, then maybe a richer layer]**

**The problem (the user's example):** the agent-cache is searchable by tag + Postgres full-text (#20).
But for a query like *"what happened in each **official** Ruby version release"*, searching the `ruby`
tag returns **every** ruby-tagged post — not specifically the official release line. Free-text + a flat
tag may be too coarse for precise, structured retrieval. We don't yet know if current search is enough.

**The likely answer (escalating, only as needed):**
- **Controlled metadata, not just free tags:** mark artifacts with structured facets — `type=release`,
  `source=official`, `entity=ruby`, `version=3.4.0`, `published=<date>` — so an agent can filter
  precisely (`type=release AND source=official AND entity=ruby`) instead of guessing from a tag. This is
  exactly what the **structured-schema work (#30)** enables (query *fields*, not just text), backed by
  the reserved `results.structured_output`.
- **Source authority:** distinguish artifacts derived from the *official* source vs. third-party
  commentary (provenance the trigger/source can set).
- **Semantic/embedding retrieval** as a later layer if facet+FTS still isn't enough (pgvector).

**Recommendation:** **verify first** — try real precision queries against the current tag+FTS once the
corpus grows; if it falls short (likely for "official release line" style queries), add the structured
facets from #30 + a small controlled vocabulary (type/source/entity/version) before reaching for
embeddings. Parked pending that verification.
