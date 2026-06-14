# Roadmap

This document is the phased build plan for Potluck. It is written for a developer
deciding whether to contribute: it states what each phase ships, the concrete
deliverables, the hard invariants that constrain every phase, and what is
explicitly deferred (and why) so you can pick work that is actually on the
critical path.

If you read nothing else, read **Invariants That Never Relax** below. They are
non-negotiable across every phase. A PR that violates one will not be merged.

---

## What Potluck is (one paragraph)

Potluck is folding@home for AI agent tokens. A contributor points their **own**
coding agent (Claude Code, Codex, or a raw API key) at a shared list of open,
**public** tasks; the results become open, attributed artifacts anyone can fork.
There are no pooled keys, no central compute, and no pay-per-outcome. The entire
central footprint is one Postgres database plus a thin auto-generated API; all
heavy compute runs on contributors' own machines under their own accounts.

---

## Invariants That Never Relax

These hold in **every** phase, including the far-future coding-task phase. They
are the security and compliance spine of the project.

1. **Your account, your machine, your key.** Potluck never receives, stores,
   proxies, or pools any API key or OAuth token. Pooling/sharing credentials is
   permanently out of scope — not just v1. Do not build it as a "convenience."

2. **Text-only no-tools safe mode is a hard property of the runner.** In v1 the
   runner invokes the agent with `--allowedTools "" --max-turns 1`. The agent is
   structurally incapable of consequential actions regardless of what a task
   says. This is a security invariant, not a user-flippable default. It only
   relaxes in Phase 3, and only behind the published sandbox gate.

3. **RLS ON for every table, from the moment it is created.** The anon key ships
   in client code; it is safe *only* because Row-Level Security gates every
   exposed table. All privileged state transitions happen via `SECURITY DEFINER`
   RPCs — never via broad client `UPDATE`/`INSERT` grants.

4. **The runner, not the task definition, enforces budget and safety.** The
   central API treats a task's declared `token_budget` as advisory. The
   contributor's runner holds the hard cap and refuses any task that exceeds the
   local budget. The server can never specify unbounded work (denial-of-wallet
   defense).

5. **Provenance proves who/what/when, not correctness.** Every artifact is
   labeled AI-generated and carries a signed manifest. Signing is for
   attribution and audit. Correctness comes from acceptance criteria
   (v1) and redundancy/consensus (later) — never from the signature.

---

## Architecture at a glance

```
        CONTRIBUTOR MACHINE (all heavy compute)        CENTRAL (one DB + thin API)
  +------------------------------------------+      +-----------------------------+
  |  potluck run  (open-source Runner CLI)    |      |  Supabase Postgres (free)   |
  |   1. claim_subtask() RPC  --------------- | ---> |   - subtasks (queue+index)  |
  |   2. wrap task text as DATA in fixed      |      |   - results  (metadata)     |
  |      anti-injection system prompt         |      |   - contributors            |
  |   3. invoke OWN agent, text-only no-tools |      |   RLS ON every table        |
  |      under HARD local token budget        |      |   PostgREST = "thin API"    |
  |   4. pre-publish output guard (secrets)   |      |   Supabase Auth = JWT       |
  |   5. sign provenance manifest             |      +--------------+--------------+
  |   6. POST result pointer  --------------- | ---> (RLS-gated INSERT)            |
  +------------------------------------------+                     |
                                                                   v
   STATIC GitHub Pages site  <---- reads PostgREST ----  scheduled GitHub Action
   (task board, "what your credits built" feed)          batch-commits markdown to
                                                          PUBLIC git repo + keep-alive ping
                                                                   |
                                                                   v
                                            PUBLIC git repo = permanent forkable artifacts
```

Key division of labor:

| Concern                | Lives where                          | Notes                                       |
|------------------------|--------------------------------------|---------------------------------------------|
| Task queue + index     | Postgres (`subtasks`)                | BOINC "work unit"                           |
| Result metadata        | Postgres (`results`)                 | provenance, pointers — not the artifact body|
| Artifact body          | Public git repo (markdown)           | ownerless, forkable, survives project death |
| All LLM compute        | Contributor machine                  | their account, their key                    |
| The only compute we run| One scheduled GitHub Action          | batch-commit + keep-alive ping              |
| Security model         | RLS + `SECURITY DEFINER` RPCs        | the whole thing; no app server we operate   |

---

## Phase 0 — Scaffold and schema lock

**Goal:** a clickable static board backed by mock JSON, and a frozen schema
(including reserved columns) so every later phase is a new code path over
existing columns — never a table reshape or data migration.

**Deliverables**

- Repository scaffold: static frontend, `runner/` CLI skeleton, `db/`
  migrations, `plans/`, LICENSE (open source end to end), CONTRIBUTING.
- Mock JSON for `subtasks`, `results`, `contributors`, and category lists in the
  **exact PostgREST response shape**, served as static files.
- Static GitHub Pages board wired against the mock JSON: task list, task detail,
  contributor page placeholder, category filter.
- **Locked v1 schema** including all reserved (nullable / unused) columns so
  heavy machinery bolts on later without reshaping rows:
  - `subtasks.consensus_group`, `subtasks.harm_tier`, `subtasks.checkpoint`
  - `results.verification_status` (enum `unverified|consensus|confirmed`),
    `results.structured_output` (jsonb), `results.commit_sha`,
    `results.permalink`
  - `contributors.reputation`, `contributors.trust_level`,
    `contributors.validated_streak`
- **RLS policies written on paper** for every table and the `claim_subtask()`
  RPC. These are the entire security model; design them before any live DB.

**Exit criteria:** the board renders from mock JSON; the schema (with reserved
columns) is reviewed and frozen; RLS policies are written and reviewed.

**Good first contributions:** frontend board components, mock-JSON fixtures,
schema review, RLS policy drafting.

---

## Phase 1 — Text-task MVP: the smallest correct loop

**Goal:** the full loop works end to end. A maintainer writes one good atomic
task → a friend runs the CLI on their own Claude account → an attributed,
AI-labeled markdown artifact appears on the public site.

This is the demo. Single-source, provenance-only. No consensus, no second
runner, no LLM judge, no challenge window.

**Central deliverables**

- Live Supabase: three tables (`subtasks`, `results`, `contributors`), **RLS ON
  for all**, the `claim_subtask(p_topics text[])` RPC (`SECURITY DEFINER`).
- The RPC is the concurrency + security primitive:

  ```sql
  -- claim_subtask(p_topics text[]) RETURNS subtasks   (SECURITY DEFINER)
  SELECT * FROM subtasks
   WHERE (status='open' OR (status='leased' AND lease_expires_at < now()))
     AND (p_topics IS NULL OR category_slug = ANY(p_topics))
   ORDER BY created_at
   FOR UPDATE SKIP LOCKED          -- atomic non-colliding claim
   LIMIT 1;
  -- then UPDATE: status='leased', leased_by=auth.uid(),
  --              lease_expires_at = now() + interval '15 min';  RETURN row.
  -- expired-lease branch = lazy self-healing; no background worker needed.
  ```

- Supabase Auth wired to GitHub OAuth; contributor JWT = `auth.uid()` =
  `contributors.id`.
- Publisher GitHub Action: **batch**-commits accepted results to the public git
  repo (never commit-per-result — respect GitHub write rate limits), sets
  `repo_path` / `commit_sha` / `permalink` on the result row.

**Runner CLI (`potluck run`) deliverables**

- GitHub-OAuth login; stores only the contributor's session, never a provider
  credential.
- Claims a leased task via `claim_subtask()`.
- Wraps untrusted task `prompt` as **DATA** inside a fixed, project-controlled
  anti-injection system prompt (forbids following embedded instructions,
  forbids revealing local/system context, constrains output format).
- Invokes the contributor's own agent in **hard text-only no-tools safe mode**
  (`--allowedTools "" --max-turns 1`) under a **client-side hard token budget**;
  refuses any task whose declared budget exceeds the local cap.
- Client-side guards: per-task token budget, `--max-turns 1`, output-size cap,
  wall-clock timeout, clear terminal SUCCESS/FAILED states. On
  failure/budget-exceed it **releases the lease** (status → `open`) for another
  contributor to retry from scratch.
- Pre-publish **output guard**: scans the artifact for secret patterns (API
  keys, tokens, private-key blocks), local paths/usernames, and policy
  violations before upload.
- Signs a provenance manifest (model id, contributor id, UTC timestamp,
  `prompt_hash`, `token_count`) and POSTs the result pointer back via the
  RLS-gated insert.

**Content deliverables**

- A handful of maintainer hand-written atomic tasks with crisp, ideally
  machine-checkable **acceptance criteria** (this is the #1 v1 quality lever).
- Tasks scoped to clearly public-domain / fair-use / transformative material.

**MANDATORY GATE before the demo:** run the PostgREST API **as the anon role**
and confirm anon **cannot** write results or mutate subtask status. This is the
#1 security-review item — RLS misconfiguration is the single platform-killing
failure mode.

**Exit criteria:** a row exists in `subtasks` → a friend runs `potluck run` →
an attributed, AI-labeled `unverified` markdown artifact appears on the public
GitHub Pages site, with provenance.

**Good first contributions:** runner CLI internals, the output guard, the
anti-injection system prompt, the publisher Action, RLS anon-role test harness.

---

## Phase 2 — Verification polish, reputation hooks, categories, retention

**Goal:** the loop stays single-source and provenance-only, but becomes
rewarding to use and compliance-clean — leaning on the cheap network-effect
hooks already present in the schema.

**Note on naming:** the broader "verification + reputation" machinery
(N-of-M consensus, adaptive replication, trust levels, gold tasks) is **not**
turned on here. This phase ships the cheap, durable retention layer and the
reserved hooks for that machinery, plus compliance polish. The actual
verification/reputation engine lights up in Phases 3–4. This is deliberate: the
research is explicit that bad task *design*, not bad workers, is the dominant
failure mode, so v1/v2 effort goes into acceptance criteria and good tasks
rather than a half-built consensus engine.

**Deliverables**

- **Categories** as first-class boards (`subtasks.category_slug`): category
  pages, `--topics` filtering in the runner (already supported by
  `claim_subtask`).
- **Retention hooks (cheap, from folding@home's engagement stack):**
  - Live "what your credits built" public feed of artifacts being produced.
  - Per-contributor pages (artifacts, totals) backed by durable git-history
    attribution.
- **API-key execution path** documented as the **default** for unattended /
  batch queue-grinding (automation via API is unambiguously ToS-permitted by
  both providers). Frame subscription-CLI use as interactive, modest-volume
  only; cap per-contributor subscription task volume client-side.
- **Contributor attestation at submit time:** affirms (1) ran on own account
  within provider ToS, (2) owns the output and licenses it under the pool's open
  license, (3) the task is public and non-prohibited.
- **Keep-alive ping** (in the publisher Action) to dodge the Supabase free-tier
  7-day pause; artifacts in git never pause regardless.
- Provenance polish: AI-generated label and full manifest surfaced on every
  public artifact.

**Still single-source, still provenance-only.** `verification_status` remains
`unverified`. Reputation columns remain reserved/unused.

**Exit criteria:** category boards live; per-contributor pages and the live feed
work; the API-key path and attestation are documented and enforced; keep-alive
prevents pauses.

**Good first contributions:** category UI, the live feed, per-contributor pages,
attestation flow, API-key runner path, subscription volume cap.

---

## Phase 3 — Open the network: trust, then verification v1, then coding tasks behind the sandbox gate

> This phase has two distinct, separable thrusts. **3a (trust + verification v1)**
> must land before strangers are let in. **3b (coding tasks)** is a separate,
> much-later track that relaxes the text-only invariant **only** behind the
> published sandbox gate. Do not start 3b before 3a is healthy.

### Phase 3a — Trust levels and verification v1 (the gate to a public commons)

**Goal:** convert "invite-only friends" into "public commons" by adding the
anti-abuse machinery that was honestly deferred in v1.

**Deliverables**

- **Trust levels** (Discourse/OSM-style) using reserved
  `contributors.trust_level`: new accounts get small daily claim limits and
  **cannot create tasks**; privileges unlock as validated contributions
  accumulate.
- **Sybil/spam gate:** proof-of-work-lite on account creation / task claiming +
  per-identity rate limits (in addition to GitHub-OAuth account-age signal).
- **Moderation / auto-screening of new-submitter task submissions** — submitted
  task prompts are themselves the injection vector for the whole network, so
  low-trust submissions are queued for review (or LLM-screened) before fan-out;
  reputable submitters publish with less friction. Fast report/takedown path.
- **Optimistic publishing + challenge windows (Tier 0):** publish low-harm
  results as `provisional`; any contributor can flag during a window
  (length inversely proportional to author reputation); only a flag triggers a
  re-run / tie-break; unflagged → `confirmed`. Lights up `verification_status`.
- **Camouflaged programmatic gold tasks:** known-answer honeypots generated
  programmatically and assigned probabilistically (so colluders can't fingerprint
  them), used only to update reputation and trigger spot-checks.

**Exit criteria:** a stranger can sign up, is correctly rate/trust-limited, can
contribute results, and cannot poison the queue or corpus without detection;
challenge windows and gold tasks are operating.

### Phase 3b — Coding tasks behind the published sandbox gate (separate, much-later track)

This is the **only** phase that relaxes the text-only invariant, and only behind
the gate below. It is explicitly out of the v1 product. The gate is published
now so it is a known prerequisite, not a relied-upon v1 control.

**The runner MUST enforce, hard-fail-closed (never warn-and-continue):**

```
[ ] Containerized isolation: gVisor or microVM  (>  plain container)
[ ] Default-DENY network egress + narrow allowlist
    (aware hostname-only allowlists are bypassable via domain fronting /
     no TLS inspection -> needs a TLS-terminating proxy for a real guarantee)
[ ] Read-only root filesystem + ephemeral writable tmpfs
[ ] Non-root execution
[ ] No host credential mounts (deny-read ~/.aws ~/.ssh ~/.gcloud; scrub env)
[ ] CPU / memory / time / process caps (stop crypto-mining, fork bombs)
[ ] One-shot ephemeral throwaway sandbox per task
[ ] HARD-FAIL CLOSED if the sandbox is unavailable (no silent unsandboxed run)
```

**Deliverables:** the sandboxed runner, the egress proxy, the gate test suite,
and a re-confirmation of provider ToS for code-execution use.

**Exit criteria:** a coding task runs only inside a verified sandbox; removing
any gate control causes a hard failure, not a fallback.

---

## Phase 4 — Scale: consensus, fan-out, adaptive replication, federation, sponsorship

**Goal:** scale quality and throughput while keeping all heavy compute off
central infra. The central API still only does cheap arithmetic.

**Verification scale-up (tiered by `harm_tier`):**

- **Tier 1 (factual/news):** N-of-M = 2-of-3 **independent** contributors with
  **enforced model/contributor diversity** (so the pool can't share one
  hallucination). Consensus computed by the thin API on **structured** outputs
  (claims + citation URLs + key fields in `results.structured_output`), **never**
  raw prose; disagreement dispatches a tie-breaker.
- **Tier 2 (high-visibility digests):** add an LLM-as-judge pass run as its
  **own** BYO task by a different contributor/model.
- **Verification is itself a first-class BYO task**, so all heavy verification
  compute stays on contributors' machines; the API only tallies votes and
  compares structured fields.
- **BOINC-style adaptive replication** via `contributors.validated_streak`:
  trusted contributors get occasional spot-checks instead of full replication —
  but random spot-checks **never fully stop** (guards against reputation
  cash-out).

**Throughput / ecosystem:**

- **Task-generator fan-out:** one big task → many atomic self-contained subtasks
  with machine-checkable acceptance criteria. Run as a BYO task (generation
  compute stays off central infra). Removes the maintainer-bound throughput
  ceiling from earlier phases.
- **Resume-by-another-contributor** via the reserved `subtasks.checkpoint`
  column (strictly a fallback; the default remains many small done-or-not tasks).
- **Teams + leaderboards** (folding@home's most cost-effective durable
  engagement lever).
- **Federation / sponsorship** exploration: multiple boards/instances and
  sponsored task pools, consistent with public-only, non-commercial, no-pooled-
  keys principles.

**Honest limits documented throughout this phase:** free-text consensus needs
structured extraction; LLM-judge self-consistency plateaus around 0.74–0.76
AUROC; same-model agreement is weak evidence; signing proves attribution, not
truth.

**Exit criteria:** Tier-1 tasks reach consensus on structured outputs with
enforced diversity; adaptive replication reduces redundant spend for trusted
contributors without halting spot-checks; the fan-out generator produces valid
atomic subtasks.

---

## Phase summary

| Phase | Ships                                                        | Verification           | Trust/anti-abuse                  | Tools/scope        |
|-------|-------------------------------------------------------------|------------------------|-----------------------------------|--------------------|
| 0     | Mock board + frozen schema (with reserved cols) + RLS draft | none                   | none                              | text-only          |
| 1     | Live 3-table DB, RLS, RPC, runner CLI, publisher Action     | provenance, unverified | GitHub OAuth + RLS, small set     | text-only no-tools |
| 2     | Categories, live feed, contributor pages, API-key path, attestation | provenance only | same + attestation                | text-only no-tools |
| 3a    | Trust levels, PoW-lite, submission moderation, gold tasks   | Tier 0 challenge window| trust levels + rate limits        | text-only no-tools |
| 3b    | Sandboxed runner + egress proxy + gate tests                | (carried from 3a)      | (carried)                         | **coding, gated**  |
| 4     | N-of-M consensus, adaptive replication, fan-out, teams, federation | Tiers 1–2       | adaptive replication              | text + gated code  |

---

## What is deferred, and why (so you don't build off-critical-path)

| Deferred                                | Until    | Why                                                                 |
|-----------------------------------------|----------|---------------------------------------------------------------------|
| N-of-M consensus                        | Phase 4  | Free-text consensus is hard; needs structured outputs + diversity   |
| LLM-as-judge                            | Phase 4  | Plateaus ~0.74–0.76 AUROC; only for Tier 2, as a BYO task           |
| Adaptive replication / reputation engine| Phase 4  | Needs history; columns reserved now                                 |
| Trust levels, PoW-lite, gold tasks      | Phase 3a | Disproportionate at friends-demo scale; first thing on opening      |
| Task-generator fan-out                  | Phase 4  | v1 invests human effort in a few excellent hand-written tasks       |
| Partial/checkpoint resume               | Phase 4  | v1 sizes tasks small enough to be done-or-not; `checkpoint` reserved|
| Coding / shell / file tools             | Phase 3b | Whole catastrophic threat class; only behind the sandbox gate       |
| Pooled/shared keys                      | **never**| Prohibited by both providers' ToS; unfixable by design              |
| Staking/slashing, KYC, ZK, on-chain     | **never** (v1 product) | Infra/friction disproportionate to a free public good |
| OSS project issue-triage as a task source | post-v0 | Additive ingestion adapter (issues → `submit_task`); deeper repro/code variants need the container gate. See open-questions #25 |

**Forward-compatibility guarantee:** every deferred mechanism reads an
**already-reserved** column (`consensus_group`, `harm_tier`, `checkpoint`,
`verification_status`, `structured_output`, `reputation`, `trust_level`,
`validated_streak`). Each is a new code path over existing columns — never a
table reshape or data migration. That is the central honesty of the design: we
ship the smallest correct loop, shaped so the research-validated heavy machinery
bolts on as an addition.
