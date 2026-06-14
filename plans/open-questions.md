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

## 4. Backend — **[locked: Supabase free tier]**

The only option bundling the five things needed on a real free tier with zero
servers we operate: auto REST (PostgREST), RLS, Auth (JWTs RLS can read),
Realtime, Storage. Neon + Data API is the documented fallback; the schema is in
PostgREST shape so a move is a base-URL + policy change. (Railway: no free tier.
Turso: no native RLS. Firebase: non-SQL/proprietary, conflicts with open-source.)

## 5. Artifact storage — **[locked: public Git repo is canonical]**

Markdown, one file per result, in a public repo. The DB stores only pointers
(`repo_path`, `commit_sha`, `permalink`). Git gives diffs, free hosting, trivial
forking, durable attribution, and — crucially — the commons survives the project's
death. The DB keeps `artifact_md` only transiently until the publisher Action
mirrors it. Discipline required: **batch** commits (built into the Action).

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

## 8. Identity / auth — **[recommended: GitHub OAuth]**

Via Supabase Auth. Gives identity + attribution + a weak, free sybil signal
(account age, public footprint) in one onboarding step. Reserved
`reputation`/`trust_level` columns carry the future graduated-trust system.

## 9. Sybil / spam defense in v1 — **[recommended: thin, and say so]**

GitHub OAuth + RLS (insert only your own results, claim only via RPC) + a small,
trusted/invite-ish contributor set. An authenticated contributor *can* still
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

v0 is text-out, so artifacts are markdown in Git — no object storage needed. But
tasks whose **output** is binary (a generated image / PDF / audio / video) or whose
**input** is a large binary (a PDF or image to host rather than link) will need
**S3-like object storage**: the DB/Git keeps a pointer, the bytes live in a bucket.

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

v0 today: a simple **circuit breaker** stops after 3 consecutive failures (so a hit
limit doesn't spin). A real `--until-limit` mode + weekly-boundary guard + per-provider
limit detection is **deferred**.

## 18. Binary provenance / install integrity — **[deferred]**

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

**Submission.** v0 is maintainer-curated (tasks inserted via SQL / service role; RLS
blocks public inserts — deliberate, since a submitted task is the network's main injection
vector, per the threat model). The path to opening it up, cheapest first:

- **GitHub-native (recommended next):** tasks live as files in a `tasks/` directory
  (YAML/markdown); contributors open a **PR**; review = moderation; a scheduled Action
  syncs approved tasks into the DB. Open, versioned, abuse-resistant, near-zero infra.
- **Authenticated `submit_task(p_key, …)` RPC** later: inserts into a `pending` state,
  promoted to `open` by a maintainer / trust level / N upvotes — needs the moderation +
  trust-level machinery (deferred to the "open the network" phase). The CLI (`potluck
  submit`) and the website form both wrap this RPC.

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
