# MVP — the smallest end-to-end loop

**Goal:** a maintainer writes one good atomic task → a friend runs the runner on
their own Claude → an attributed, AI-labeled markdown artifact appears on the
public board. Nothing more.

If that one loop works and feels good, everything else is incremental.

## Definition of done

> A row in `subtasks` (status `open`) → friend runs `potluck run --once` →
> the task is claimed, executed in safe mode on the friend's own account, the
> result is submitted, the subtask flips to `done`, and the artifact renders on
> the static board, attributed to the friend and their self-reported model.

## Build checklist

### A. Database (Supabase) — *~half a day*
- [ ] Create the Supabase project; capture `SUPABASE_URL` + anon key (public) and
      keep the access token in `.env` (gitignored, already set up).
- [ ] Apply [`db/schema.sql`](../db/schema.sql) (SQL editor or `supabase db push`).
- [ ] Confirm `register_contributor(p_key, p_display_name)` stores only the
      SHA-256 hex of the key in `contributor_keys` (no policy/grant → unreachable
      except via the `SECURITY DEFINER` RPCs).
- [ ] **Mandatory gate:** hit the PostgREST API **as the anon role** and confirm
      anon can `SELECT` but **cannot** insert a result or mutate a subtask's
      status. This is the #1 security check. (See [threat-model](../docs/threat-model.md) §6.)

### B. Seed content — *~1 hour*
- [ ] Hand-write 5–10 atomic `subtasks` with crisp, machine-checkable
      `acceptance` criteria across 1–2 categories (e.g. `rails-news`, `ml-papers`).
- [ ] Each prompt is **self-contained** (paste the source text/links inline) and
      sized for a single sub-5k-token call.

### C. Runner (start as a Claude Code skill) — *~1–2 days*
- [ ] `potluck register` → generate a random secret key locally (`potluck_` +
      32 random bytes hex) → `register_contributor(p_key, p_display_name)` (the
      server stores only the SHA-256); key saved to `~/.potluck/credentials`.
      Never upload the provider credential.
- [ ] `claim_subtask(topics)` via PostgREST RPC.
- [ ] Wrap the untrusted `prompt` as **data** inside the fixed system prompt;
      run **one no-tools turn** on the contributor's own Claude
      (`--allowedTools "" --max-turns 1`), under `--budget`.
- [ ] Minimal output guard: scan for obvious secret patterns / local paths; set
      `output_guard_passed`.
- [ ] `submit_result(...)` with `reported_model`, `token_count`, `prompt_hash`.
- [ ] On failure/over-budget: `release_lease()` (discard partial, re-queue).

### D. Static board (GitHub Pages) — *~1 day*
- [ ] `web/` renders the open queue + the "what your credits built" feed from the
      mock JSON in `web/data/` (already in PostgREST shape).
- [ ] Swap the data source from mock JSON → live Supabase (URL + anon key). Same
      shape, so it's a config change.
- [ ] Per-category view + a contributor attribution line per artifact.

### E. (Optional) Export / keep-alive — *not v1; build only if wanted*
- [ ] v0 needs nothing here: the board reads `artifact_md` **straight from the DB**,
      which is the canonical and only artifact store (decision #5). Done by design.
- [ ] Optional future: a scheduled keep-alive ping (a tiny cron hitting the REST API) to
      dodge the free tier's ~7-day idle pause, plus periodic DB backups/exports — and, if
      durability-beyond-the-DB is ever wanted, an **optional** export mirror that batch-commits
      accepted results to a public git repo and writes back the reserved
      `repo_path`/`commit_sha`/`permalink`. A nice-to-have, never the canonical store.

## Explicitly NOT in the MVP

Consensus, reputation, trust levels, public task submission by strangers, the
automated task generator, partial/resume, non-Claude backends, image inputs,
coding tasks. All tracked in [roadmap](roadmap.md). The MVP is the *loop*, proven.

## Sequencing

```
A (db + anon gate) ──► B (seed tasks) ──► C (runner skill) ──► D (board, reads DB)
        the gate in A is blocking: do not demo until anon-write is confirmed dead.
        D closes the loop: the board reads results.artifact_md straight from the DB.
        (E — optional keep-alive/export — is not part of the v0 loop.)
```

First milestone to celebrate: the first artifact on the board that a friend's
spare tokens produced — with you having spent zero central compute to make it
happen.
