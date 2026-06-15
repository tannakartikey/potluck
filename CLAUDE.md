# CLAUDE.md — read this first, every session

## ⭐ The wall (read before building anything)

Potluck's north star lives in **`.private/GOAL.md`** (private, gitignored). Open it at the start
of every session. The one sentence: **a contributor's agent claims a public task and does it so
thoroughly, so correctly, that nobody ever has to do it again — 100%, for any tool/CLI, installed
or not, run safely on the contributor's machine.** Quality AND security, in that priority: a safe
project nobody uses protects nothing. If a change doesn't move toward that, reconsider it.

## What Potluck is (orientation)

Central Potluck = **just a database** (Supabase/PostgREST): tasks in, public artifacts out. No
server, no pooled credentials. Each contributor runs the `potluck` CLI on **their own** machine,
which claims a task, runs it on **their own** agent (Claude/Codex), guards the output, and
publishes the result. The hard problem is running an untrusted stranger's task on an agent logged
into your account — that's what all the safety machinery is for.

## Canonical docs (don't duplicate; update these)

- `docs/threat-model.md` — the most important file; the safety model + the three invariants.
- `plans/prelaunch.md` — the security/readiness audit; §0 is the capability-first staged design.
- `plans/phase-2-status.md` — honest status of the v2 curated-tools sandbox (what's verified vs not).
- `AGENTS.md` — the public participation spec.

## How we work here (hard rules)

- **Verify, never claim.** An unverified security property is worse than none (this nearly killed
  the project once: `--allowed-tools ""` was *documented* to disable tools and didn't). Prove every
  security-critical claim by running it; mark anything unproven as UNVERIFIED. The same rule applies
  to claimed *weaknesses* — don't assert a hole without an exploit that demonstrates it.
- `cd client && go build ./... && go vet ./... && go test ./...` must pass at every commit; keep
  `gofmt` clean (CI enforces it). Stdlib-only Go unless truly unavoidable.
- Never touch the prod DB; `bash scripts/anon-gate.sh` must stay green.
- Work on a branch; commit tested increments; don't merge to main or force-push.
