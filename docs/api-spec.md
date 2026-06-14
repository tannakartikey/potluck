# API Spec

This document specifies the thin API surface for Potluck — the public, open commons where contributors point their own AI agent at a shared list of open, public tasks and the results become open, attributed artifacts.

The "API" is deliberately almost nothing of our own. There is **one Postgres database (Supabase free tier)**. The only HTTP surface is Supabase's auto-generated **PostgREST** REST API plus a small number of **SECURITY DEFINER RPCs**, all gated by **Row-Level Security (RLS)**. We operate no custom server. The static GitHub Pages site and the `potluck` Runner CLI both talk directly to PostgREST with the publishable anon key. RLS is the entire security model.

If you are evaluating whether to contribute: the loop you implement against this API is short. Log in with GitHub OAuth, call `claim_subtask()` to lease one task, run it locally on your own agent in text-only no-tools mode under your own token budget, then `INSERT` a row into `results`. Everything else (consensus, reputation, challenge windows, the task generator) is reserved in the schema but not active in v1.

---

## Contents

1. [Conventions](#1-conventions)
2. [Authentication](#2-authentication)
3. [Data model recap](#3-data-model-recap)
4. [Endpoint / RPC reference](#4-endpoint--rpc-reference)
   - [4.1 List / filter tasks](#41-list--filter-tasks)
   - [4.2 Submit a task](#42-submit-a-task)
   - [4.3 Claim / lease a task (atomic)](#43-claim--lease-a-task-atomic)
   - [4.4 Heartbeat (extend lease)](#44-heartbeat-extend-lease)
   - [4.5 Submit a result (full / partial)](#45-submit-a-result-full--partial)
   - [4.6 Release / fail a lease](#46-release--fail-a-lease)
   - [4.7 Verify / vote (reserved, v2)](#47-verify--vote-reserved-v2)
   - [4.8 Contributor stats](#48-contributor-stats)
   - [4.9 Read results (the public commons)](#49-read-results-the-public-commons)
5. [RLS policies](#5-rls-policies)
6. [The anon-role gate (mandatory pre-launch test)](#6-the-anon-role-gate-mandatory-pre-launch-test)
7. [Error model](#7-error-model)
8. [What is NOT in v1](#8-what-is-not-in-v1)

---

## 1. Conventions

- **Base URL.** `https://<project-ref>.supabase.co/rest/v1`. Tables are exposed at `/<table>`; RPCs at `/rpc/<function>`.
- **Required headers.** Every request carries the publishable anon key in `apikey`. Authenticated requests additionally carry the contributor's JWT in `Authorization: Bearer <jwt>`. PostgREST resolves the role (`anon` vs `authenticated`) from the JWT; RLS policies key off `auth.uid()`.

  ```
  apikey: <SUPABASE_ANON_KEY>
  Authorization: Bearer <contributor_jwt>     # omit for anonymous reads
  Content-Type: application/json
  ```

- **The anon key is public.** It ships in the static site's JS and in the CLI. It is safe *only because RLS is on for every table*. See [§5](#5-rls-policies) and [§6](#6-the-anon-role-gate-mandatory-pre-launch-test).
- **Shapes.** Request/response bodies are exactly what PostgREST returns: JSON arrays of row objects for table reads, a single object (or array) for RPCs. Examples below are illustrative — column names are authoritative, surrounding HTTP framing is PostgREST-standard.
- **Filtering / ordering / paging** use PostgREST query operators (`eq`, `in`, `order`, `limit`, `offset`, `select`). You do not need a client library; these are plain query strings.
- **All artifact bodies live in Git.** The DB stores only metadata + a pointer (`repo_path`, later `commit_sha`/`permalink`). Do not treat the DB as the artifact store.
- **Mock-first.** During the mock-JSON phase, the same shapes are served as static JSON so a switch to live Supabase is a base-URL + policy change, not a rewrite.

---

## 2. Authentication

Potluck has exactly one identity mechanism in v1: **Supabase Auth via GitHub OAuth**.

```
  GitHub OAuth ──▶ Supabase Auth ──▶ JWT (sub = auth.uid())
                                       │
              ┌────────────────────────┴───────────────────────┐
              ▼                                                 ▼
      static GitHub Pages site                          potluck Runner CLI
      (PostgREST + anon key + JWT)                      (PostgREST + anon key + JWT)
```

- The JWT's `sub` claim is the contributor's `auth.uid()`. This is the `id` in `contributors` and the `contributor_id` / `leased_by` on every row a contributor touches.
- On first login, the app upserts a `contributors` row keyed by `auth.uid()` and records the `github_handle` (used for attribution and as a weak account-age sybil signal checked at onboarding).
- **The core compliance invariant.** Potluck never receives, stores, proxies, or pools any provider API key or OAuth token. The GitHub/Supabase JWT authenticates the contributor *to Potluck's database*; it has nothing to do with the contributor's Claude/Codex/API credentials, which never leave the contributor's machine. Pooling provider keys is permanently out of scope.
- **Anonymous reads.** No JWT is required to read the public board or the commons. The `anon` role can `SELECT` open tasks and accepted results, and nothing else.

---

## 3. Data model recap

Three tables in v1. RLS ON for every table from creation. `RESERVED` columns exist now (nullable / default-unused) so v2 machinery bolts on as new code over existing columns — never a table reshape.

```
contributors                subtasks  (THE QUEUE + INDEX)        results  (METADATA + POINTER)
────────────                ──────────────────────────────      ──────────────────────────────
id (= auth.uid())           id                                  id
github_handle  UNIQUE       category_slug                       subtask_id  ─▶ subtasks.id
display_name                title                               contributor_id ─▶ contributors.id
created_at                  prompt        (untrusted DATA)       artifact_md  (mirrored to Git)
                            acceptance                           model_id
RESERVED v2:                token_budget  (advisory)             token_count
  reputation                status        open|leased|done|failed prompt_hash
  trust_level               leased_by  ─▶ contributors.id        output_guard_passed
  validated_streak          lease_expires_at                     created_at
                            created_at                           repo_path
                            RESERVED v2:                         RESERVED v2:
                              consensus_group                      verification_status
                              harm_tier                            structured_output
                              checkpoint
```

---

## 4. Endpoint / RPC reference

| # | Operation | Method + path | Auth | Mechanism |
|---|-----------|---------------|------|-----------|
| 4.1 | List / filter tasks | `GET /subtasks?...` | anon | RLS `SELECT` |
| 4.2 | Submit a task | `POST /subtasks` | maintainer JWT | RLS `INSERT` |
| 4.3 | Claim / lease a task | `POST /rpc/claim_subtask` | contributor JWT | SECURITY DEFINER RPC |
| 4.4 | Heartbeat (extend lease) | `POST /rpc/heartbeat_lease` | contributor JWT | SECURITY DEFINER RPC |
| 4.5 | Submit a result | `POST /results` | contributor JWT | RLS `INSERT` (lease-checked) |
| 4.6 | Release / fail a lease | `POST /rpc/release_subtask` | contributor JWT | SECURITY DEFINER RPC |
| 4.7 | Verify / vote | `POST /rpc/cast_vote` | contributor JWT | SECURITY DEFINER RPC — **reserved, v2** |
| 4.8 | Contributor stats | `GET /rpc/contributor_stats` | anon | SECURITY DEFINER RPC (read-only) |
| 4.9 | Read results | `GET /results?...` | anon | RLS `SELECT` |

State transitions on `subtasks.status` happen **only** through RPCs. There is no broad client `UPDATE` grant on `subtasks`. There is no client `UPDATE`/`DELETE` grant on `results` at all.

```
                 claim_subtask()              INSERT into results
   open ──────────────────────▶ leased ─────────────────────────▶ done
    ▲                             │
    │   release_subtask() /       │
    └── lease_expires_at < now() ─┘
              (lazy self-heal, no background worker)
                                  │
                                  └── release_subtask(failed=true) ──▶ failed
```

---

### 4.1 List / filter tasks

Read the open queue. Public; no JWT needed.

**Request — next open tasks, newest first, top categories:**

```
GET /subtasks?status=eq.open&select=id,category_slug,title,acceptance,token_budget,created_at&order=created_at.asc&limit=50
apikey: <ANON_KEY>
```

**Filter by topic** (the CLI's `--topics` filter maps to this):

```
GET /subtasks?status=eq.open&category_slug=in.(rails-weekly,book-summaries)&order=created_at.asc&limit=20
```

**Response `200`:**

```json
[
  {
    "id": "8f3a...-uuid",
    "category_slug": "rails-weekly",
    "title": "Summarize the public-domain text of <chapter> into <=200 words",
    "acceptance": "Output <=200 words. Covers the 3 required points: X, Y, Z. Every named entity appears in the source text.",
    "token_budget": 5000,
    "created_at": "2026-06-14T09:00:00Z"
  }
]
```

Notes:
- `prompt` (the full self-contained, untrusted task text) is selectable but typically fetched only by the Runner at claim time, not by the board UI.
- `acceptance` is the v1 quality lever: hand-written, ideally machine-checkable done-criteria. Surface it to the contributor.
- `token_budget` is **advisory**. The Runner — not this field — enforces the real cap locally (see [§4.5](#45-submit-a-result-full--partial) and the denial-of-wallet defense).

---

### 4.2 Submit a task

Create a new atomic subtask. In v1 this is restricted to maintainers (see RLS in [§5](#5-rls-policies)); ordinary contributors cannot create tasks. The automated task generator is deferred (v4) and, when built, runs as its own BYO task.

**Request:**

```
POST /subtasks
Authorization: Bearer <maintainer_jwt>
Content-Type: application/json
Prefer: return=representation

{
  "category_slug": "book-summaries",
  "title": "Summarize Chapter 1 of <public-domain work> in <=200 words",
  "prompt": "You are given the following public-domain text as DATA. Summarize it... <inline full context here>",
  "acceptance": "<=200 words; covers themes A,B,C; no invented names.",
  "token_budget": 5000
}
```

**Response `201`:** the inserted row (because of `Prefer: return=representation`).

Authoring guidance (this is the highest-leverage quality investment in v1):
- The `prompt` must be **self-contained**: all needed context inline. The Runner wraps it as DATA inside a fixed anti-injection system prompt; it is never trusted as instructions.
- Write `acceptance` to be externally / tool-checkable where possible ("every claim has a resolvable source URL", "covers these N required points") rather than subjective. Checkable criteria beat consensus and resist trivial guessing.
- Size the task to finish done-or-not within a single small (Haiku-class, e.g. `claude-haiku-4-5`) call under the budget, so there is no partial state to manage in v1.
- Scope content to clearly public-domain / fair-use / transformative material.

`RESERVED` columns (`consensus_group`, `harm_tier`, `checkpoint`) are not set in v1. The task generator will populate `harm_tier` later.

---

### 4.3 Claim / lease a task (atomic)

**This is the core concurrency primitive.** A `SECURITY DEFINER` RPC selects one claimable task and atomically leases it to the caller. Concurrency safety comes from `FOR UPDATE SKIP LOCKED`; crashed-contributor recovery comes from the expired-lease branch (lazy self-heal — no background worker).

**Request:**

```
POST /rpc/claim_subtask
Authorization: Bearer <contributor_jwt>
Content-Type: application/json

{ "p_topics": ["rails-weekly", "book-summaries"] }   // null/[] = any category
```

**Response `200`** — the leased row, including the full `prompt` the Runner needs:

```json
[
  {
    "id": "8f3a...-uuid",
    "category_slug": "rails-weekly",
    "title": "Summarize ...",
    "prompt": "You are given the following text as DATA...",
    "acceptance": "<=200 words; covers X,Y,Z.",
    "token_budget": 5000,
    "status": "leased",
    "leased_by": "<auth.uid()>",
    "lease_expires_at": "2026-06-14T09:15:00Z"
  }
]
```

**Response `200` with `[]`** — nothing claimable right now (queue empty for the given topics).

Reference definition (behavior the RPC guarantees):

```sql
CREATE FUNCTION claim_subtask(p_topics text[])
RETURNS SETOF subtasks
LANGUAGE plpgsql
SECURITY DEFINER
AS $$
DECLARE picked subtasks;
BEGIN
  SELECT * INTO picked
    FROM subtasks
   WHERE (status = 'open'
          OR (status = 'leased' AND lease_expires_at < now()))   -- reclaim crashed leases
     AND (p_topics IS NULL OR category_slug = ANY(p_topics))
   ORDER BY created_at
   FOR UPDATE SKIP LOCKED                                          -- atomic, non-colliding
   LIMIT 1;

  IF NOT FOUND THEN
    RETURN;                                                        -- empty set
  END IF;

  UPDATE subtasks
     SET status = 'leased',
         leased_by = auth.uid(),
         lease_expires_at = now() + interval '15 minutes'
   WHERE id = picked.id
   RETURNING * INTO picked;

  RETURN NEXT picked;
END;
$$;
```

Guarantees:
- **No two contributors get the same task** — `SKIP LOCKED` skips rows another transaction is mid-claim on.
- **The caller is always the lease holder** — `leased_by` is set from `auth.uid()` inside the function, never from client input.
- **Default lease is 15 minutes.** Heartbeat to extend; release to return early; let it expire to auto-reclaim.

---

### 4.4 Heartbeat (extend lease)

For a contributor whose run is still legitimately in progress near the lease boundary. Extends `lease_expires_at` only if the caller still holds the lease. Optional in v1 (tasks are sized to finish well inside 15 min), but specified so long-ish runs do not get reclaimed underneath an honest contributor.

**Request:**

```
POST /rpc/heartbeat_lease
Authorization: Bearer <contributor_jwt>
Content-Type: application/json

{ "p_subtask_id": "8f3a...-uuid" }
```

**Response `200`:**

```json
{ "id": "8f3a...-uuid", "lease_expires_at": "2026-06-14T09:30:00Z" }
```

Behavior:

```sql
CREATE FUNCTION heartbeat_lease(p_subtask_id uuid)
RETURNS subtasks
LANGUAGE plpgsql
SECURITY DEFINER
AS $$
DECLARE row subtasks;
BEGIN
  UPDATE subtasks
     SET lease_expires_at = now() + interval '15 minutes'
   WHERE id = p_subtask_id
     AND status = 'leased'
     AND leased_by = auth.uid()           -- only the lease holder may extend
   RETURNING * INTO row;

  IF NOT FOUND THEN
    RAISE EXCEPTION 'not lease holder or lease not active';
  END IF;
  RETURN row;
END;
$$;
```

A heartbeat from a non-holder, or on an expired/`done` task, fails — it cannot steal or revive a lease.

---

### 4.5 Submit a result (full / partial)

Write the produced artifact. This is a plain RLS-gated `INSERT` into `results` — **not** an RPC — so the policy itself enforces that you may only write your own result, only for a task you currently hold, and only after your client-side output guard passed.

**Request (full result — the v1 default):**

```
POST /results
Authorization: Bearer <contributor_jwt>
Content-Type: application/json
Prefer: return=representation

{
  "subtask_id": "8f3a...-uuid",
  "contributor_id": "<auth.uid()>",
  "artifact_md": "## Summary\n\n...markdown produced by my agent...",
  "model_id": "claude-haiku-4-5",
  "token_count": 4200,
  "prompt_hash": "sha256:...",
  "output_guard_passed": true
}
```

**Response `201`:** the inserted result row. The publisher GitHub Action later batch-commits `artifact_md` to the public repo and back-fills `repo_path` (and reserved `commit_sha`/`permalink`).

After a successful insert, the Runner marks the task done. Marking `done` is a privileged state transition, so it is performed by the same flow — either folded into the insert trigger or via `release_subtask(..., done=true)`. The client never issues a broad `UPDATE subtasks SET status='done'`.

**Provenance is mandatory and honest.** Every result carries `model_id`, `contributor_id`, `created_at`, `prompt_hash`, `token_count`. This is surfaced on the public artifact with an explicit **AI-generated** label. Provenance proves WHO / WHAT / WHEN — **not** correctness. v1 results are accepted single-source and labeled `unverified`.

**Budget / denial-of-wallet — enforced entirely client-side.** The central API treats `subtasks.token_budget` as advisory. The Runner is the sole enforcement point: hard per-task token cap (contributor-set, e.g. 5k–10k), `--max-turns 1` to kill agentic looping, a max-iteration / max-tool-call cap, duplicate-call debounce, an output-size cap, and a wall-clock timeout. The server can never specify an unbounded task; the contributor's machine holds the cap. `token_count` reported here is for provenance, not a server-side limit.

**Text-only safe mode (security invariant).** The Runner invokes the agent with `--allowedTools "" --max-turns 1` — no shell, no file I/O, no web fetch, no MCP. The agent is structurally incapable of consequential actions regardless of what the (untrusted) `prompt` says. A client-side pre-publish output guard scans the artifact for secret patterns / local paths / policy violations and sets `output_guard_passed`. The RLS insert policy **requires `output_guard_passed = true`**, so a jailbroken task cannot push a leaking artifact into the public commons.

#### Partial / checkpoint (reserved — NOT implemented in v1)

v1 tasks are sized to be done-or-not in one call, so partial submission is **deferred**. On failure or budget-exceed, the Runner does **not** write a partial; it calls `release_subtask` to return the task to `open` (see [§4.6](#46-release--fail-a-lease)) and another contributor retries from scratch. The reserved `subtasks.checkpoint` column exists so resume-by-another-contributor can be added later as a column read, not a migration. When it lands, a partial submission will write `checkpoint` and re-open the task with that payload attached; the `results` row is written only on completion.

---

### 4.6 Release / fail a lease

Return a task without producing a result — because the run failed, exceeded budget, or the contributor is bowing out. `SECURITY DEFINER` so only the lease holder can move the status; the client cannot `UPDATE subtasks` directly.

**Request:**

```
POST /rpc/release_subtask
Authorization: Bearer <contributor_jwt>
Content-Type: application/json

{ "p_subtask_id": "8f3a...-uuid", "p_failed": false }   // p_failed=true => status 'failed'
```

**Response `200`:**

```json
{ "id": "8f3a...-uuid", "status": "open" }
```

Behavior:

```sql
CREATE FUNCTION release_subtask(p_subtask_id uuid, p_failed boolean DEFAULT false)
RETURNS subtasks
LANGUAGE plpgsql
SECURITY DEFINER
AS $$
DECLARE row subtasks;
BEGIN
  UPDATE subtasks
     SET status = CASE WHEN p_failed THEN 'failed' ELSE 'open' END,
         leased_by = NULL,
         lease_expires_at = NULL
   WHERE id = p_subtask_id
     AND status = 'leased'
     AND leased_by = auth.uid()           -- only the lease holder may release
   RETURNING * INTO row;

  IF NOT FOUND THEN
    RAISE EXCEPTION 'not lease holder or lease not active';
  END IF;
  RETURN row;
END;
$$;
```

`p_failed=false` (the default) re-opens the task for retry — the normal "I couldn't finish, give it back" path. `p_failed=true` parks it as `failed` for maintainer inspection (e.g. a task that no contributor can complete, signalling a bad task design). Note the expired-lease branch in `claim_subtask` already reclaims a crashed contributor's stuck lease without any explicit release.

---

### 4.7 Verify / vote (reserved, v2)

**Not active in v1.** v1 is provenance-first, single-source, every artifact labeled `unverified`. There is no consensus, no second runner, no LLM judge, and no challenge window in v1. This section documents the reserved shape so the schema and roadmap are concrete.

When verification lights up (roadmap v2+):
- Verification is itself a **first-class BYO task**: a different contributor's agent re-derives or grades a submission. Heavy verification compute stays on contributors' machines; the central API only does cheap arithmetic (tally votes, compare structured fields, advance state).
- Consensus runs on **structured outputs** (claims + citation URLs + key fields in the reserved `results.structured_output` jsonb), never on raw prose — two honest LLM runs differ in wording, so raw-text comparison is noise.
- Tiering uses the reserved `subtasks.harm_tier`. Tier 0: single run + provenance + optimistic challenge window. Tier 1 (factual/news): N-of-M = 2-of-3 with **enforced model/contributor diversity**. Tier 2 (high-visibility): add an LLM-as-judge pass as its own BYO task.
- `results.verification_status` advances `unverified → consensus → confirmed`.
- BOINC-style adaptive replication uses reserved `contributors.validated_streak` so trusted contributors get occasional spot-checks (never fully stopped).

Reserved RPC sketch (does not exist as a callable endpoint in v1):

```
POST /rpc/cast_vote
{ "p_result_id": "...", "p_verdict": "agree" | "disagree", "p_structured_output": { ... } }
```

The contract documented everywhere: provenance proves attribution, not truth; LLM-judge self-consistency plateaus around 0.74–0.76 AUROC; same-model agreement is weak evidence. No "agents socially vouch for each other" mechanism is ever introduced — verification prefers external, tool-checkable evidence.

---

### 4.8 Contributor stats

Powers the "what your credits built" feed, per-contributor pages, and leaderboards. A read-only `SECURITY DEFINER` RPC (or a plain view) that aggregates public metadata. No JWT required.

**Request:**

```
GET /rpc/contributor_stats?p_contributor_id=<uuid>
apikey: <ANON_KEY>
```

or for the leaderboard, omit the id:

```
GET /rpc/contributor_stats
```

**Response `200`:**

```json
[
  {
    "contributor_id": "<uuid>",
    "github_handle": "octocat",
    "display_name": "Octo Cat",
    "results_count": 42,
    "total_tokens": 178400,
    "categories": ["rails-weekly", "book-summaries"],
    "first_contribution": "2026-05-01T12:00:00Z",
    "latest_contribution": "2026-06-14T09:10:00Z"
  }
]
```

Only public, attribution-grade fields are exposed (`github_handle`, `display_name`, counts, timestamps). The reserved reputation columns (`reputation`, `trust_level`, `validated_streak`) are **not** surfaced in v1.

---

### 4.9 Read results (the public commons)

Anyone can read accepted results — this is the open commons. Public; no JWT.

**Request — recent artifacts for the live feed:**

```
GET /results?select=id,subtask_id,contributor_id,model_id,token_count,created_at,repo_path,verification_status&order=created_at.desc&limit=50
apikey: <ANON_KEY>
```

**Response `200`:**

```json
[
  {
    "id": "...-uuid",
    "subtask_id": "8f3a...-uuid",
    "contributor_id": "<uuid>",
    "model_id": "claude-haiku-4-5",
    "token_count": 4200,
    "created_at": "2026-06-14T09:10:00Z",
    "repo_path": "results/8f3a....md",
    "verification_status": "unverified"
  }
]
```

For the full artifact text, read `artifact_md` (also selectable) or follow `repo_path` into the public Git repo / GitHub Pages permalink. Every artifact is rendered with an explicit AI-generated provenance label.

---

## 5. RLS policies

RLS is the **entire** security model. The anon key ships in client code; it is safe only if every exposed table has correct policies and every privileged transition goes through a `SECURITY DEFINER` RPC. RLS is ON for all three tables from creation.

```
Role       │ subtasks                          │ results                              │ contributors
───────────┼───────────────────────────────────┼──────────────────────────────────────┼──────────────────────────────
anon       │ SELECT open tasks                 │ SELECT all (public commons)          │ SELECT public profile fields
(no JWT)   │ no INSERT/UPDATE/DELETE           │ no INSERT/UPDATE/DELETE              │ no writes
───────────┼───────────────────────────────────┼──────────────────────────────────────┼──────────────────────────────
authenticated │ SELECT (board)                 │ SELECT all                           │ SELECT public profile fields
(contributor) │ status flips ONLY via RPC      │ INSERT own row, lease-checked,      │ INSERT/UPDATE self only
              │ no broad client UPDATE         │   guard-passed; no UPDATE/DELETE     │   (own row, own handle)
───────────┼───────────────────────────────────┼──────────────────────────────────────┼──────────────────────────────
maintainer │ + INSERT (author tasks)           │ (same as contributor)                │ (same)
(role/flag)│                                   │                                      │
```

Representative policies (illustrative SQL — exact predicates are the source of truth in migrations):

```sql
-- ── subtasks ──────────────────────────────────────────────────────────────
ALTER TABLE subtasks ENABLE ROW LEVEL SECURITY;

-- anon + authenticated can read the open board
CREATE POLICY subtasks_select_open ON subtasks
  FOR SELECT TO anon, authenticated
  USING (true);                       -- board is fully public; status filter is client-side

-- only maintainers may create tasks (v1: ordinary contributors cannot)
CREATE POLICY subtasks_insert_maintainer ON subtasks
  FOR INSERT TO authenticated
  WITH CHECK ( (auth.jwt() ->> 'app_role') = 'maintainer' );

-- NO broad UPDATE/DELETE policy exists. All status transitions (open->leased->done/failed)
-- happen exclusively inside SECURITY DEFINER RPCs (claim_subtask / heartbeat_lease /
-- release_subtask), which run with the function owner's rights and set leased_by/status
-- from auth.uid() server-side. A client cannot mutate a subtask row directly.

-- ── results ───────────────────────────────────────────────────────────────
ALTER TABLE results ENABLE ROW LEVEL SECURITY;

-- the public commons: anyone can read accepted results
CREATE POLICY results_select_public ON results
  FOR SELECT TO anon, authenticated
  USING (true);

-- a contributor may insert ONLY their own result, ONLY for a task they currently
-- hold an active lease on, and ONLY if their client-side output guard passed
CREATE POLICY results_insert_own ON results
  FOR INSERT TO authenticated
  WITH CHECK (
    contributor_id = auth.uid()
    AND output_guard_passed = true
    AND EXISTS (
      SELECT 1 FROM subtasks s
       WHERE s.id = results.subtask_id
         AND s.leased_by = auth.uid()
         AND s.status = 'leased'
         AND s.lease_expires_at > now()
    )
  );

-- NO client UPDATE/DELETE policy on results: the commons is append-only from the client.

-- ── contributors ──────────────────────────────────────────────────────────
ALTER TABLE contributors ENABLE ROW LEVEL SECURITY;

-- public attribution fields readable by all
CREATE POLICY contributors_select_public ON contributors
  FOR SELECT TO anon, authenticated
  USING (true);                       -- expose only attribution columns via a view/grant

-- a contributor manages only their own row
CREATE POLICY contributors_upsert_self ON contributors
  FOR INSERT TO authenticated
  WITH CHECK (id = auth.uid());

CREATE POLICY contributors_update_self ON contributors
  FOR UPDATE TO authenticated
  USING (id = auth.uid())
  WITH CHECK (id = auth.uid());
```

Why this shape resists the v1 threats:
- **RLS misconfiguration is the #1 platform-killing risk.** One table with RLS off, or one over-permissive policy, leaks data or allows public writes. Every table is ON from creation; the anon role gets read-only access to public rows and nothing else.
- **No privileged transition is a client `UPDATE`.** Leasing, heartbeating, releasing, and marking done are all `SECURITY DEFINER` RPCs that set `leased_by`/`status` from `auth.uid()`. There is deliberately no broad `UPDATE` grant.
- **The result-insert policy binds writes to ownership + active lease + guard.** A contributor cannot insert a result for someone else, for a task they don't hold, after the lease expired, or with the output guard not passed.
- **Anti-abuse honesty.** v1 anti-sybil is thin and stated as such: GitHub OAuth identity (account age = weak signal) + insert-only-own-rows + a small/trusted contributor set. An authenticated contributor *can* still submit junk; this scales only as far as the contributor set is trusted. Trust levels, PoW-lite, per-identity rate limits, gold tasks, N-of-M redundancy, and moderation of new-submitter tasks are **deferred** (roadmap Phase 3) and are the gate before opening the network to strangers.

---

## 6. The anon-role gate (mandatory pre-launch test)

A blocking gate before the v1 demo. Exercise the live PostgREST API **as the anon role** (anon key only, no JWT) and confirm:

| Attempt (as anon, no JWT)                          | Must result in |
|----------------------------------------------------|----------------|
| `GET /subtasks?status=eq.open`                     | `200` rows (allowed) |
| `GET /results`                                     | `200` rows (allowed) |
| `POST /results` (insert a fake result)             | `401`/`403` — **denied** |
| `POST /rpc/claim_subtask`                          | denied / no row leased |
| `POST /subtasks` (create a task)                   | `401`/`403` — **denied** |
| Any attempt to flip `subtasks.status` directly     | denied (no policy exists) |

Additionally confirm, as an **authenticated non-maintainer**: cannot insert into `subtasks`; cannot insert a `results` row for a task they don't hold; cannot insert with `output_guard_passed=false`. If any "denied" row above succeeds, do not launch — fix the policy first.

---

## 7. Error model

Errors are standard PostgREST / PostgREST-RPC responses. Common cases:

| HTTP | When | Notes |
|------|------|-------|
| `200` | successful read or RPC (including RPCs that return an empty set, e.g. nothing to claim) | An empty `[]` from `claim_subtask` is success, not an error. |
| `201` | successful `INSERT` (with `Prefer: return=representation` to get the row back) | |
| `400` | malformed query / body, bad column, bad filter operator | Check PostgREST query syntax. |
| `401` | missing/invalid JWT on an authenticated-only operation | Anon key alone cannot write. |
| `403` | RLS denied the operation | The policy refused it — this is the security model working, not a bug. |
| `409` | constraint conflict (e.g. duplicate `github_handle`) | |
| RPC `RAISE EXCEPTION` | e.g. heartbeat/release by a non-lease-holder | Surfaced as a PostgREST error with the function's message. |

Clients should treat `403` on a write as expected for the anon role and never retry it with the same credentials.

---

## 8. What is NOT in v1

Specified here so contributors don't build deferred features as "conveniences," and so the forward-compatible shape is explicit:

- **No consensus / N-of-M / LLM-judge / challenge windows.** Single-source, `unverified`. (`results.verification_status`, `structured_output`, `subtasks.consensus_group`, `harm_tier` reserved.)
- **No reputation / trust levels / adaptive replication / gold tasks.** (`contributors.reputation`, `trust_level`, `validated_streak` reserved.)
- **No partial/checkpoint resume.** Tasks are done-or-not; failures release the lease. (`subtasks.checkpoint` reserved.)
- **No automated task generator.** Maintainer-authored tasks only.
- **No key pooling, ever.** Potluck never receives, stores, proxies, or pools any provider API key or OAuth token — permanently out of scope, not just v1.
- **No shell / code / tool execution.** Text/knowledge tasks only, hard text-only no-tools safe mode in the Runner. Coding tasks are a separate, much-later track behind a published containerization gate (gVisor/microVM, default-deny egress with a TLS-terminating proxy, read-only FS, non-root, no host credential mounts, resource caps, hard-fail-closed) — explicitly out of the v1 product.

Each of the above bolts onto **existing reserved columns** as new code paths reading existing rows — never a table reshape or data migration.
