# Architecture

Potluck is deliberately small in the center and heavy at the edge. The project
operates **one database and a static website**; every token of real work is spent
on contributors' own machines under their own accounts.

## System overview

```
            ┌──────────────────────────── CONTRIBUTOR'S MACHINE ───────────────────────────┐
            │                                                                               │
            │   potluck runner (open source)                                                │
            │     1. claim_subtask(topics)  ──┐                                             │
            │     2. wrap prompt as DATA      │   uses the contributor's OWN                │
            │        in fixed system prompt   │   account / key / subscription             │
            │     3. run agent in SAFE MODE ──┼──►  Claude Code -p  /  Codex  /  API SDK    │
            │        (no tools, 1 turn, budget)│      (no credential ever leaves here)      │
            │     4. output guard (secrets)   │                                             │
            │     5. submit_result(...)     ──┘                                             │
            └───────────────┬──────────────────────────────────────────────────────────────┘
                            │ HTTPS (PostgREST + RPC; write RPCs carry p_key)
                            ▼
   ┌───────────────────────────────────── SUPABASE (free tier) ─────────────────────────────┐
   │   Postgres  =  QUEUE + INDEX + PROVENANCE + ARTIFACT STORE   (RLS on every table)       │
   │     • subtasks (the work queue / public board)                                          │
   │     • results  (metadata + provenance + the full markdown body: artifact_md)            │
   │     • contributors (identity = self-generated key; hash in contributor_keys)            │
   │   Auto REST API (PostgREST)  +  anon role  =  the "thin API" we don't run               │
   └───────────────┬─────────────────────────────────────────────────────────────────────────┘
                   │ anon key (public, RLS-protected) read — board + results.artifact_md
                   ▼
   ┌─────────────────────────────┐
   │  GitHub Pages (static site) │  reads board + feed + the result markdown
   │  web/ — no build step       │  straight from results.artifact_md (DB)
   │  board · submit · feed      │
   └─────────────────────────────┘
```

The DB is the single source of truth: the canonical artifact store **is**
`results.artifact_md`, read straight from Postgres by the static board — there is no
git mirror and no publisher in the v0 loop. The only thing the project itself might
"run" beyond the managed DB is an **optional lightweight keep-alive ping** (a tiny
scheduled job hitting the REST API) to keep the free tier from pausing after ~7 days
idle — not a publisher, and not required to serve results.

## Components

| Component | Where it runs | Responsibility |
|---|---|---|
| **Runner CLI** (`potluck`) | contributor's machine | Claim a lease, wrap the untrusted task as data, run the contributor's own agent in safe mode under a local budget, guard the output, submit the result. The runner — not the task — is the safety/budget enforcement point. |
| **Postgres (Supabase)** | managed free tier | Queue + index + provenance **+ canonical artifact store**: the full result markdown lives in `results.artifact_md`. RLS is the whole access model. Three tables in v1. |
| **PostgREST + RLS** | managed (Supabase) | The "thin API" — auto-generated REST + key-gated `SECURITY DEFINER` RPCs. Reads use the public anon role; writes resolve the contributor from the presented secret key. No server we operate. |
| **Static site** | GitHub Pages | Public board, category pages, the "what your credits built" feed, submit form. Reads PostgREST directly with the public anon key — including each result's markdown straight from `results.artifact_md`. |

## The task lifecycle

```
 SUBMIT ──► FAN-OUT ──► OPEN ──► LEASE ──► EXECUTE ──► GUARD ──► SUBMIT
   │           │          │        │          │          │         │
maintainer  (v1: manual) queue   atomic    safe mode   secret/   result row +
authors a   big task →   row     RPC,15min  no-tools   policy    artifact_md +
small task  many small   (board) lease      budget cap scan      flip to 'done';
            atomic ones                                          board reads it
```

1. **Submit.** In v1, a maintainer hand-writes atomic `subtasks` with crisp,
   machine-checkable `acceptance` criteria (the v1 quality lever). Public
   submissions by strangers are gated behind trust levels — [roadmap](../plans/roadmap.md).
2. **Fan-out.** A big task ("digest today's news") becomes many small,
   self-contained subtasks ("summarize this one article"), each sized to finish
   under a small budget. v1 does this by hand; the automated generator is itself a
   future Potluck task.
3. **Claim.** `claim_subtask(topics)` atomically leases the next matching task
   (`FOR UPDATE SKIP LOCKED`), setting a 15-minute lease. Expired leases are
   reclaimed lazily inside the same query — no background worker.
4. **Execute.** The runner wraps the untrusted `prompt` as **data** inside a
   fixed, project-controlled system prompt and runs the contributor's own agent in
   **safe mode**: no tools, one turn, hard local token budget. (Image inputs are
   allowed as task attachments; the agent describes them — output is still text.)
5. **Guard.** A client-side pre-publish scan checks for secret patterns, local
   paths, and policy violations. Failing the guard blocks publication.
6. **Submit.** `submit_result(...)` writes the result row — including the full
   markdown body in `results.artifact_md` — with self-reported provenance, and flips
   the subtask to `done`, in one transaction, only if the caller holds an active lease.
   That single write is the publish: the static board reads `artifact_md` straight from
   the DB and renders it, attributed to the contributor and the model they reported.

If a run fails or blows the budget, the runner calls `release_lease()`: v1
**discards the partial work** and returns the task to the pool for a fresh retry.

## The single-database / minimal-compute design

Everything that *could* be a server is pushed to one of two places:

- **Into Postgres + RLS.** Access control, the atomic claim, result submission,
  and artifact storage are all SQL — the result markdown lives in
  `results.artifact_md` and the board reads it directly. There is no application
  server to operate, scale, or secure, and no separate artifact pipeline.
- **Onto the contributor's machine.** All inference, all budget enforcement, all
  output guarding. The center never spends a token.

This is what makes Potluck free-tier-viable and hard to capture: there is almost
nothing in the middle to pay for or take over. The known cost of all-in-DB is that
a Supabase project can pause (~7 days idle) and isn't ownerless the way a git repo
is; both are addressed by an **optional** future export/backup mirror (a periodic
`pg_dump` or a batch git/Storage export) rather than by any always-on publisher —
see [open-questions](../plans/open-questions.md) #5.

## How the static frontend talks to the DB safely

The site is keyless in the sense that matters: it ships only the **public anon
key**, which is safe **only because RLS is correct on every exposed table**.

```
  anon (everyone — site + runner)    →  SELECT subtasks / results / contributors   ✅
                                     →  SELECT contributor_keys (key hashes)        ❌ (no grant)
                                     →  any direct INSERT/UPDATE                     ❌ (no policy/grant)
                                     →  claim_subtask()/submit_result() RPCs (p_key) ✅ (key-gated)
  via write RPC (resolves the key)   →  RPC sets leased_by / contributor_id server- ✅
                                        side from _contributor_for_key(p_key)
  maintainer (service role)          →  author subtasks                            ✅
```

The single platform-killing bug for this architecture is an RLS
misconfiguration (a table with RLS off, or an over-permissive policy). So there is
a **mandatory pre-launch test that exercises the API as the anon role** and
confirms anon cannot write. See [threat-model](threat-model.md) §6 and
[data-model](data-model.md).

## Deployment

| Piece | Service | Cost |
|---|---|---|
| Database + API (PostgREST + RPCs) | Supabase free tier | $0 |
| Website | GitHub Pages | $0 |
| Artifact store | Supabase Postgres (`results.artifact_md`) | $0 (same DB) |
| Keep-alive ping (optional) | GitHub Actions (scheduled) | $0 (within free minutes) |

**Portability:** because the schema is modeled in PostgREST shape, moving off
Supabase (e.g. to Neon + Data API) later is a base-URL + policy change, not a
rewrite — and the artifacts move with the DB since they *are* DB rows. Durability
beyond a single project (forkable, ownerless storage) is the job of the optional
future export mirror (open-questions #5), not a present property.

See [`api-spec.md`](api-spec.md) for the concrete endpoints/RPCs and
[`client-spec.md`](client-spec.md) for the runner.
