# Potluck database

Potluck's backend is a single Supabase (Postgres + PostgREST) project. The database +
Row-Level Security *is* the server — there is no app server to run.

## Files

| File | What |
|---|---|
| `schema.sql` | **Canonical** schema: tables, indexes, RLS policies, and the SECURITY DEFINER RPCs. Idempotent (`create … if not exists` / `create or replace`) — safe to re-apply. |
| `seed.sql` | Demo categories + a few example tasks. **Not** idempotent (re-running duplicates rows). |
| `migrations/` | Historical, non-destructive migrations applied to the live prod DB *before* `schema.sql` absorbed them. A fresh project only needs `schema.sql` — migrations are for the already-running prod DB. |

## Environments

Two separate Supabase projects, same org (`Potluck`), same region (`us-east-1`):

| Env | Project | Ref | Purpose |
|---|---|---|---|
| **prod** | `potluck` | `besocrfzgnkxyykzpkqv` | The launch DB. The published default in `client/internal/api/api.go`, `AGENTS.md`, and `web/config.js`. **Don't run tests against it.** |
| **staging** | `potluck-staging` | `bhretqmpdboasigngnjv` | Tests / e2e / scratch. A faithful, RLS-hardened mirror of prod's schema. |

The two share **identical** schema, RPCs, RLS policies, and anon grants (verified by
structural diff). Only physical column order differs in `subtasks` — prod appended a few
columns via migration; staging has them in canonical position. PostgREST and the client
address columns by name, so this is cosmetic.

Credentials live in `.env` (gitignored): prod under `SUPABASE_*`, staging under
`STAGING_SUPABASE_*`. The anon keys are RLS-protected (read-only) and public-safe; the
**access token** and **DB passwords** are secret and must never be committed.

## Common tasks

**Point the client at staging (for testing):**

```sh
source scripts/use-staging.sh     # maps STAGING_* → POTLUCK_SUPABASE_URL / POTLUCK_ANON_KEY
                                  # and sets POTLUCK_HOME=~/.potluck-staging (separate key)
./potluck register                # one-time: contributor keys are per-DB, so staging needs its own
./potluck run --backend codex --max-tasks 1               # now hits staging
# new shell (or: unset POTLUCK_SUPABASE_URL POTLUCK_ANON_KEY POTLUCK_HOME) → back to prod
```

`potluck run` / `potluck status` print the target host, so you can always see which DB
you're hitting.

**(Re)create the schema on a project:**

```sh
scripts/apply-schema.sh <project-ref>          # schema only (idempotent)
scripts/apply-schema.sh <staging-ref> --seed   # schema + demo tasks (staging only)
```

`apply-schema.sh` uses the Supabase Management API (no psql / DB password needed) and
guards the prod ref (typed confirmation; refuses `--seed`).

**Recreate staging from scratch** (e.g. to reset it): create a new project in the
`Potluck` org, then `scripts/apply-schema.sh <new-ref> --seed`, fetch its anon key, and
update the `STAGING_*` block in `.env`.
