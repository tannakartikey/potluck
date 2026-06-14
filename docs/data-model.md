# Data Model

> **v0 auth note:** writes are authenticated by a self-generated contributor
> **key** (no OAuth / Supabase Auth); any `auth.uid()` references below are
> superseded — see [`db/schema.sql`](../db/schema.sql) and [`AGENTS.md`](../AGENTS.md)
> for the current key-gated RPCs. The entity shapes here remain accurate.

This document describes Potluck's data model: the entities, their relationships,
field-level detail, and the lifecycle statuses that drive the task queue.

A companion file, [`db/schema.sql`](../db/schema.sql), implements this model in
Postgres with Row-Level Security (RLS). This document is the human-readable
reference; the SQL is the source of truth for column types, constraints, and
policies.

If you are evaluating whether to contribute, the short version:

- The central database is a **queue + index + provenance store**. It does no
  heavy compute. It holds task definitions, work assignments (leases), and
  result *metadata*.
- The actual result artifacts (markdown) live in a **public Git repo**. The
  database stores only pointers (`repo_path`, commit SHA, permalink).
- **Three tables ship in v1** (`contributors`, `subtasks`, `results`). Everything
  else in this document is either a *reserved column* (present but unused in v1)
  or a *deferred table* (named here so the roadmap is concrete, but not created
  in v1).
- The schema is modeled in **PostgREST shape**, so the mock-JSON frontend used
  during early development and the live Supabase backend return the same shape.
  Switching from mock to live is a base-URL + RLS-policy change, not a rewrite.

---

## Design principles that shape the schema

These constraints explain *why* the model looks the way it does:

1. **The DB is an index, not a filesystem.** Artifacts are markdown committed to
   a public Git repo (ownerless, forkable, diffable, free to host). The DB row
   points at the file. This keeps the central footprint tiny and means the
   commons survives even if the database disappears.

2. **RLS is the entire security model.** The frontend is a static site that talks
   directly to the auto-generated PostgREST API using a public anon key. There is
   no server we operate to enforce access. Therefore **every table has RLS ON from
   creation**, and all privileged state transitions (claiming a task, completing
   it) happen through `SECURITY DEFINER` RPCs — never through broad client
   `UPDATE` grants.

3. **Forward-compatible, never reshaped.** v1 ships the smallest correct loop.
   The heavy machinery from the design (N-of-M consensus, adaptive replication,
   the task-generator fan-out, resume-by-checkpoint) is added later as *new code
   paths reading existing columns*. The columns those features need are reserved
   now (nullable, unused) so they never require a table reshape or data migration.

4. **Provenance proves WHO/WHAT/WHEN, not correctness.** Every result carries a
   provenance manifest (model id, contributor, timestamp, prompt hash, token
   count). v1 results are single-source and explicitly labeled `unverified`.
   Correctness machinery (consensus, gold tasks, challenge windows) is deferred.

---

## Entity overview

```
                        +------------------+
                        |  contributors    |   identity + attribution
                        |  (= auth.uid())  |   (GitHub OAuth)
                        +------------------+
                           |            |
            leased_by /    |            |  contributor_id
            leases (1:N)   |            |  results (1:N)
                           v            v
   +----------------+   +------------------+        +------------------+
   |  categories    |   |    subtasks      | 1    N |     results      |
   |  (tags/slugs)  |<--|  THE QUEUE +     |--------|  metadata +      |
   +----------------+   |  THE INDEX       |        |  provenance      |
     category_slug      |  (BOINC 'work    |        |  (body in Git)   |
                        |   unit')         |        +------------------+
                        +------------------+
                              ^
                              | (deferred) consensus_group, harm_tier
                              | groups multiple subtasks/results for N-of-M
```

In Potluck/BOINC vocabulary, a **subtask** is a *work unit*: the smallest atomic
unit of work a contributor claims and completes. A **result** is one independent
execution of that work unit. In v1 there is exactly one accepted result per
subtask; the model reserves the columns needed for multiple independent results
per subtask (consensus) later.

### Vocabulary note: "task" vs. "subtask"

The user-facing concept is a *task* ("summarize today's tech news"). A large task
is fanned out by a **task generator** into many small, self-contained
**subtasks** ("summarize this one article"), each sized to finish inside a small
token budget. **In v1 there is no automated generator and no separate `tasks`
table** — a maintainer hand-writes atomic `subtasks` directly. The `subtasks`
table *is* the queue. The grouping needed to reconstitute "which big task did this
subtask come from" is reserved via `consensus_group` (and a future `tasks` table).

---

## v1 tables

### `contributors`

One row per person who has logged in. The primary key equals `auth.uid()` from
Supabase Auth (GitHub OAuth), so a contributor's identity, attribution, and RLS
ownership are the same id.

| Column         | Type          | Null | Default       | Notes |
|----------------|---------------|------|---------------|-------|
| `id`           | `uuid`        | no   | —             | PK. Equals `auth.uid()` (Supabase Auth / GitHub OAuth). |
| `github_handle`| `text`        | yes  | —             | UNIQUE. Attribution + weak sybil signal (account age checked at onboarding). |
| `display_name` | `text`        | yes  | —             | Shown on leaderboards / contributor pages. |
| `created_at`   | `timestamptz` | no   | `now()`       | |

**Reserved (v2, unused in v1, no later migration needed):**

| Column            | Type  | Default | Future use |
|-------------------|-------|---------|------------|
| `reputation`      | `int` | `0`     | Reputation score for adaptive replication. |
| `trust_level`     | `int` | `0`     | Discourse/OSM-style trust tiers (claim limits, task-creation rights). |
| `validated_streak`| `int` | `0`     | Consecutive validated results; drives BOINC-style spot-check frequency. |

**RLS:**

- Public `SELECT` of `github_handle` / `display_name` (for attribution and
  leaderboards).
- `UPDATE` only of one's own row (`id = auth.uid()`).
- `INSERT` of one's own row on first login (id must equal `auth.uid()`).

---

### `subtasks` — the queue and the index

This is the heart of the system: the work queue contributors claim from, and the
public board the static site renders. One row = one atomic work unit.

| Column             | Type          | Null | Default     | Notes |
|--------------------|---------------|------|-------------|-------|
| `id`               | `uuid`        | no   | `gen_random_uuid()` | PK. |
| `category_slug`    | `text`        | yes  | —           | Ships in v1: matchmaking + filtering (`--topics`). Indexed. See [Categories](#categories--tags). |
| `title`            | `text`        | no   | —           | Short human-readable label for the board. |
| `prompt`           | `text`        | no   | —           | **Self-contained** task text, with all needed context inline. Treated strictly as **DATA**, never as instructions (see [Security](#security-notes)). |
| `acceptance`       | `text`        | yes  | —           | Human-readable, ideally machine-checkable done-criteria (e.g. "every claim has a resolvable source URL", "covers these N points"). Shown to the contributor. **This is v1's primary quality lever.** |
| `token_budget`     | `int`         | no   | `5000`      | **Advisory** cap. The runner enforces the real cap locally; the DB value is a hint, never trusted as a bound. |
| `attachments`      | `jsonb`       | yes  | —           | Optional image-input URLs etc. v1 supports image **inputs** (the agent describes them); output stays text. |
| `requested_model`  | `text`        | yes  | —           | Requested model or tier (advisory). The model that *actually* ran is self-reported on `results`. |
| `model_policy`     | `text`        | no   | `'any'`     | `CHECK (... IN ('any','min','exact'))` — how to interpret `requested_model`. |
| `status`           | `text`        | no   | `'open'`    | Lifecycle. `CHECK (status IN ('open','leased','done','failed'))`. See [Status lifecycle](#status-lifecycle). |
| `leased_by`        | `uuid`        | yes  | —           | FK → `contributors(id)`. Who currently holds the lease. |
| `lease_expires_at` | `timestamptz` | yes  | —           | When the current lease lapses and the row becomes claimable again. |
| `created_at`       | `timestamptz` | no   | `now()`     | Queue ordering. |

**Reserved (v2, present now so heavy machinery bolts on without reshaping rows):**

| Column            | Type   | Future use |
|-------------------|--------|------------|
| `consensus_group` | `uuid` | Groups subtasks/results for N-of-M consensus and fan-out grouping. |
| `harm_tier`       | `int`  | Tiered verification: `0` low-harm, `1` factual/news, `2` high-visibility digest. |
| `checkpoint`      | `text` | Partial/resume payload for resume-by-another-contributor (deferred). |

**RLS:**

- Anon `SELECT` (the public board).
- **No broad client `UPDATE`.** `status`, `leased_by`, and `lease_expires_at`
  flip *only* via the `claim_subtask()` RPC (below) or a maintainer role.
- `INSERT` (task creation) is maintainer-only in v1 (contributors cannot create
  tasks until trust levels land — see roadmap).

---

### `results` — metadata + provenance (body lives in Git)

One row per execution of a subtask. The DB row is lightweight metadata and a
provenance manifest; the markdown **body** is mirrored to the public Git repo by
the publisher GitHub Action, after which `artifact_md` can be pruned.

| Column                | Type          | Null | Default       | Notes |
|-----------------------|---------------|------|---------------|-------|
| `id`                  | `uuid`        | no   | `gen_random_uuid()` | PK. |
| `subtask_id`          | `uuid`        | no   | —             | FK → `subtasks(id)`. |
| `contributor_id`      | `uuid`        | no   | —             | FK → `contributors(id)`. Must equal `auth.uid()` at insert. |
| `artifact_md`         | `text`        | no   | —             | Produced markdown. Mirrored to Git by the publisher Action; may be pruned afterward. |
| `reported_model`      | `text`        | no   | —             | **Self-declared** model that produced this result; not verified. WHO/WHAT/WHEN — not correctness, not a model proof. |
| `self_described_model`| `text`        | yes  | —             | Optional: what the model said when asked to name itself. Weak anomaly signal only. |
| `token_count`         | `int`         | yes  | —             | Provenance: tokens spent on this run. |
| `prompt_hash`         | `text`        | yes  | —             | Provenance: hash of the exact wrapped prompt. |
| `output_guard_passed` | `boolean`     | yes  | `true`        | Client-side pre-publish guard verdict (secret/policy scan). Insert is rejected if `false`. |
| `created_at`          | `timestamptz` | no   | `now()`       | |
| `repo_path`           | `text`        | yes  | —             | Set by the publisher Action, e.g. `results/<id>.md`. |

**Reserved (v2):**

| Column                | Type    | Default        | Future use |
|-----------------------|---------|----------------|------------|
| `verification_status` | `text`  | `'unverified'` | `CHECK (... IN ('unverified','consensus','confirmed'))`. v1 stays `unverified`. |
| `structured_output`   | `jsonb` | —              | Normalized claims + citation URLs for consensus comparison (never compare raw prose). |
| `commit_sha`          | `text`  | —              | Set by publisher Action alongside `repo_path`. |
| `permalink`           | `text`  | —              | Public URL of the committed artifact. |

**RLS:**

- Anon `SELECT` (the public commons / "what your credits built" feed).
- `INSERT` only WHERE **all** of:
  - `contributor_id = auth.uid()`, **and**
  - a matching **active lease** exists on the subtask held by this contributor,
    **and**
  - `output_guard_passed = true`.

This insert policy is the join between identity, the lease, and the safety guard.
It is why a contributor cannot submit a result for a subtask they did not claim,
cannot submit on behalf of someone else, and cannot publish output that failed the
local guard.

---

## Status lifecycle

The task prompt for this model references statuses
`open / leased / partial / completed / verified / rejected`. Here is how those map
onto what **ships in v1** versus what is **reserved**.

### v1: `subtasks.status` enum

Only four values exist in v1:

```
        claim_subtask() RPC                result INSERT (RPC/policy)
   open ───────────────────────► leased ───────────────────────────► done
    ▲                              │  │
    │  lease expires (lazy)        │  │  run fails / budget exceeded
    │  OR runner releases lease    │  └──────────────────────────────► failed
    └──────────────────────────────┘                                    │
              (back to open for another contributor)                    │
    ▲                                                                   │
    └───────────────────────────────────────────────────────────────────┘
                     (failed subtasks may be re-opened by a maintainer)
```

| Status   | Meaning | Set by |
|----------|---------|--------|
| `open`   | Claimable. The default; also where a lapsed/released lease returns the row. | default; lazy expiry; lease release |
| `leased` | A contributor holds a 15-minute lease and is running it. | `claim_subtask()` RPC |
| `done`   | An accepted result exists. | result submission |
| `failed` | The run failed or exceeded budget and the contributor reported it. | runner |

There is **no `partial`, `verified`, or `rejected` value in v1.** Here is the
honest mapping of the requested statuses:

| Requested status | v1 reality |
|------------------|-----------|
| `open`           | `subtasks.status = 'open'`. |
| `leased`         | `subtasks.status = 'leased'`. |
| `partial`        | **Deferred.** v1 sizes tasks to be done-or-not in a single sub-budget call. On failure the lease is released (row → `open`) and another contributor retries from scratch. The `checkpoint` column is reserved so resume-by-another-contributor is a column-read later, not a migration. |
| `completed`      | `subtasks.status = 'done'`. |
| `verified`       | **Deferred.** Tracked on the *result*, not the subtask, via the reserved `results.verification_status` (`unverified → consensus → confirmed`). All v1 results are `unverified`. |
| `rejected`       | **Deferred.** Belongs to the future challenge-window / moderation path; no v1 mechanism. |

### Why statuses split across two tables

A *subtask* has a queue status (`open/leased/done/failed`) — is the work claimed
and finished? A *result* has a verification status (`unverified/consensus/
confirmed`) — do we trust it? These are orthogonal, which is why verification
lives on `results`, not `subtasks`. In v1 a subtask is `done` the moment one
`unverified` result exists. In v2, a subtask might be `done` while several of its
results are still racing toward `consensus`.

---

## The claim primitive (concurrency + security)

Claiming work is the one place concurrency matters, and it is handled by a single
`SECURITY DEFINER` RPC. There is **no background worker**; lease reclamation is
lazy (handled inside the same query).

```sql
claim_subtask(p_topics text[]) RETURNS subtasks   -- SECURITY DEFINER
  SELECT * FROM subtasks
   WHERE (status = 'open'
          OR (status = 'leased' AND lease_expires_at < now()))   -- lazy self-heal
     AND (p_topics IS NULL OR category_slug = ANY(p_topics))
   ORDER BY created_at
   FOR UPDATE SKIP LOCKED                                         -- atomic, non-colliding
   LIMIT 1;
  -- then UPDATE the row:
  --   status = 'leased', leased_by = auth.uid(),
  --   lease_expires_at = now() + interval '15 min'
  -- RETURN it.
```

Two properties to note as a contributor:

- **`FOR UPDATE SKIP LOCKED`** means two contributors calling `claim_subtask()`
  at the same time never get the same row — each skips the other's locked row and
  takes the next. No coordination, no double work.
- **The expired-lease branch** means if your machine crashes mid-task, your stuck
  lease is reclaimed automatically the next time anyone claims with no background
  process required. The 15-minute window is the maximum time a crashed lease ties
  up a row.

A symmetric `complete_subtask()` / result-submission RPC (or the insert policy
above) flips the subtask to `done` and writes the `results` row in one step, again
as `SECURITY DEFINER` so the client never needs a broad `UPDATE` grant.

---

## Categories / tags

In v1, categories are **not a table** — they are a `category_slug text` column on
`subtasks`, indexed. This is deliberately the cheapest thing that supports the two
features that matter:

- **Matchmaking / filtering:** `claim_subtask(p_topics)` filters by
  `category_slug = ANY(p_topics)`, so a contributor can run only the topics they
  care about (`potluck run --topics rails-news,book-summaries`).
- **Localized status boards:** the static site renders per-category pages.

A normalized `categories` table (slug, label, description) is a trivial later
addition if categories need their own metadata; nothing in v1 depends on slugs
being foreign keys, so it is a non-breaking change.

---

## Reputation

There is **no reputation logic in v1**, by design. The columns
`contributors.reputation`, `contributors.trust_level`, and
`contributors.validated_streak` are reserved (default `0`) so the later machinery
reads existing columns:

- `validated_streak` drives **BOINC-style adaptive replication** — once a
  (contributor, model) pair has enough consecutive validated results, it gets
  occasional spot-checks instead of full N-of-M replication. Random spot-checks
  never fully stop (to defend against reputation "cash-out").
- `trust_level` drives **Discourse/OSM-style graduated privileges** — new
  accounts get small daily claim limits and cannot create tasks; privileges
  unlock as validated work accumulates.
- `reputation` is the headline leaderboard number.

v1's only anti-abuse posture is: GitHub OAuth identity (account age as a weak,
free sybil signal) + RLS that lets a contributor insert only their own results and
claim only via the RPC + a small, trusted contributor set. This scales exactly as
far as the contributor set is trusted, which is why reputation/trust-levels are
the first thing added when the network opens to strangers.

---

## Deferred tables (named, not created in v1)

These are listed so the roadmap is concrete. Each maps onto reserved columns above
and is added as new code over existing rows — never a reshape of v1 data.

| Table                  | Purpose | Maps onto |
|------------------------|---------|-----------|
| `tasks`                | The large user-facing task that a generator fans out into many `subtasks`. | `subtasks.consensus_group` (grouping) |
| `leases`              | First-class lease history, if claims need an audit trail beyond the inline `leased_by`/`lease_expires_at` columns. | `subtasks.leased_by`, `lease_expires_at` |
| `verifications` / `votes` | N-of-M consensus tallies over structured outputs. | `results.verification_status`, `structured_output` |
| `challenges`          | Optimistic-publishing challenge windows (flag → re-run → confirm). | `results.verification_status` |
| `reputation_events` / `reputation_streaks` | Append-only events feeding adaptive replication. | `contributors.reputation`, `validated_streak` |
| `teams`               | Folding@home-style teams for retention/leaderboards. | new |
| `moderation_log`      | Flag → warn → throttle → ban audit trail. | new |
| `checkpoints`         | Resume-by-another-contributor payloads, if richer than a single column. | `subtasks.checkpoint` |

---

## Security notes

A few model-level facts that matter when reading the schema:

- **RLS is ON for every table from creation.** The anon key ships in client code;
  it is safe *only* because policies are correct on every exposed table. A single
  table with RLS off, or one over-permissive policy, leaks the whole commons. A
  mandatory pre-launch test exercises the PostgREST API **as the anon role** to
  confirm anon cannot write `results` or mutate `subtasks.status`.

- **Task `prompt` is untrusted DATA, never instructions.** The runner wraps it
  inside a fixed, project-controlled system prompt that forbids following embedded
  instructions and forbids revealing local context. The schema's job is just to
  carry that text; safety enforcement is in the runner.

- **`token_budget` is advisory.** The runner is the sole enforcement point for
  spend. The central server must never be able to define an unbounded task — this
  is the denial-of-wallet defense, and it works precisely because the contributor's
  machine holds the real cap.

- **Privileged transitions go through `SECURITY DEFINER` RPCs only.** Clients are
  never granted broad `UPDATE`. The two transitions that exist in v1 (claim,
  complete) are RPCs.

For the full security rationale, threat model, and the coding-task sandbox gate
(deferred, out of v1 scope), see the security documentation. This file covers only
the data model.
