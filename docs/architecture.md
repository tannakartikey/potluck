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
            └───────────────┬───────────────────────────────────────────────────▲──────────┘
                            │ HTTPS (PostgREST + RPC, contributor's JWT)          │
                            ▼                                                     │
   ┌───────────────────────────────────── SUPABASE (free tier) ──────────────────┼──────────┐
   │   Postgres  =  QUEUE + INDEX + PROVENANCE     (RLS on every table)           │          │
   │     • subtasks (the work queue / public board)                              │          │
   │     • results  (metadata + provenance; body pointer)                        │          │
   │     • contributors (identity via GitHub OAuth)                              │          │
   │   Auto REST API (PostgREST)  +  Auth (JWT)  =  the "thin API" we don't run   │          │
   └───────────────┬─────────────────────────────────────────────────────────────┼──────────┘
                   │ anon key (public, RLS-protected) read                        │ batch commit
                   ▼                                                              │ (1 GH Action)
   ┌─────────────────────────────┐                          ┌─────────────────────┴──────────┐
   │  GitHub Pages (static site) │  reads board + feed      │  Public RESULTS Git repo        │
   │  web/ — no build step       │◄─────────────────────────┤  results/<id>.md  (open, forkable)
   │  board · submit · feed      │   links to artifacts     │  the permanent commons          │
   └─────────────────────────────┘                          └─────────────────────────────────┘
```

The only thing the project itself "runs" beyond the managed DB is **one scheduled
GitHub Action** that batch-commits accepted results into the public repo (never a
commit per result — that respects GitHub write limits) and pings the DB to keep
the free tier from pausing.

## Components

| Component | Where it runs | Responsibility |
|---|---|---|
| **Runner CLI** (`potluck`) | contributor's machine | Claim a lease, wrap the untrusted task as data, run the contributor's own agent in safe mode under a local budget, guard the output, submit the result. The runner — not the task — is the safety/budget enforcement point. |
| **Postgres (Supabase)** | managed free tier | Queue + index + provenance. RLS is the whole access model. Three tables in v1. |
| **PostgREST + Auth** | managed (Supabase) | The "thin API" — auto-generated REST + `SECURITY DEFINER` RPCs + GitHub-OAuth JWTs. No server we operate. |
| **Static site** | GitHub Pages | Public board, category pages, the "what your credits built" feed, submit form. Reads PostgREST directly with the public anon key. |
| **Results repo** | public Git repo | Canonical artifact store: markdown, one file per result. Diffable, forkable, free, outlives the project. |
| **Publisher Action** | GitHub Actions (scheduled) | Batch-mirror accepted `results.artifact_md` → repo files; write back `repo_path`/`commit_sha`/`permalink`; keep-alive ping. |

## The task lifecycle

```
 SUBMIT ──► FAN-OUT ──► OPEN ──► LEASE ──► EXECUTE ──► GUARD ──► SUBMIT ──► PUBLISH
   │           │          │        │          │          │         │          │
maintainer  (v1: manual) queue   atomic    safe mode   secret/   result    GH Action
authors a   big task →   row     RPC,15min  no-tools   policy    + flip    mirrors to
small task  many small   (board) lease      budget cap scan      to 'done' Git + board
            atomic ones
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
6. **Submit.** `submit_result(...)` writes the result row (with self-reported
   provenance) and flips the subtask to `done`, in one transaction, only if the
   caller holds an active lease.
7. **Publish.** The scheduled Action mirrors the markdown into the public repo and
   the site renders it, attributed to the contributor and the model they reported.

If a run fails or blows the budget, the runner calls `release_lease()`: v1
**discards the partial work** and returns the task to the pool for a fresh retry.

## The single-database / minimal-compute design

Everything that *could* be a server is pushed to one of three places:

- **Into Postgres + RLS.** Access control, the atomic claim, and result
  submission are SQL (`SECURITY DEFINER` RPCs). There is no application server to
  operate, scale, or secure.
- **Onto the contributor's machine.** All inference, all budget enforcement, all
  output guarding. The center never spends a token.
- **Into Git + a scheduled Action.** Durable artifact storage and the only
  recurring "job" the project runs.

This is what makes Potluck free-tier-viable and hard to capture: there is almost
nothing in the middle to pay for or take over.

## How the static frontend talks to the DB safely

The site is keyless in the sense that matters: it ships only the **public anon
key**, which is safe **only because RLS is correct on every exposed table**.

```
  anon (the website, logged-out)     →  SELECT subtasks / results / contributors   ✅
                                     →  any write                                  ❌ (no policy)
  authenticated (logged-in runner)   →  claim_subtask()/submit_result() RPCs       ✅
                                     →  INSERT results (own + active lease + guard) ✅
                                     →  UPDATE subtasks.status directly             ❌ (no policy)
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
| Database + API + Auth | Supabase free tier | $0 |
| Website | GitHub Pages | $0 |
| Artifact store | public GitHub repo | $0 |
| Publisher / keep-alive | GitHub Actions | $0 (within free minutes) |

**Portability:** because the schema is modeled in PostgREST shape, moving off
Supabase (e.g. to Neon + Data API) later is a base-URL + policy change, not a
rewrite. The artifact commons is plain Git and already portable by construction.

See [`api-spec.md`](api-spec.md) for the concrete endpoints/RPCs and
[`client-spec.md`](client-spec.md) for the runner.
