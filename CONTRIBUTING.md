# Contributing to Potluck

Potluck is pre-alpha. The most valuable contributions right now are **finding
holes**, not adding features.

## High-leverage right now

1. **Attack the threat model.** Read [`docs/threat-model.md`](docs/threat-model.md)
   and try to break it. Can a malicious task leak something into an artifact? Can
   an RLS policy be bypassed? File an issue (or a private security report for
   anything exploitable — see [SECURITY.md](SECURITY.md)).
2. **Attack the RLS policies.** [`db/schema.sql`](db/schema.sql) is the entire
   security model. If you can write to `results` or mutate `subtasks.status` as the
   anon or a non-leasing authenticated role, that's the #1 bug class.
3. **Write good tasks.** Crisp, self-contained tasks with machine-checkable
   acceptance criteria are the v1 quality lever. Propose some.
4. **Build the MVP loop.** See [`plans/mvp.md`](plans/mvp.md).

## Principles to respect in any PR

- **Never add a place to store or relay a credential.** BYO-agent is permanent.
- **Never relax safe mode** (no tools, single turn) on the v1 path. Coding/tool
  tasks are a separate track behind the sandbox gate in the threat model.
- **Keep the center tiny.** If a change needs a server we operate, prefer pushing
  it into Postgres+RLS, the contributor's machine, or a scheduled Action.
- **Be honest in copy.** Don't claim verification or correctness we don't have.
  Every artifact is `unverified` and AI-generated until the machinery ships.

## Dev setup

- Static site: `cd web && python3 -m http.server 8000`. It runs on mock JSON in
  `web/data/` with no build step.
- Database: apply `db/schema.sql` to a Supabase project; never commit secrets
  (`.env` is gitignored — use `.env.example` as the template).

## Conventions

- Docs are the source of truth for design; keep them in sync with code in the same
  PR. Prefer concrete, honest prose over marketing.
- Small, reviewable PRs. Link the issue / roadmap item.
