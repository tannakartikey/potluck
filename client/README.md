# client/ — the Potluck runner

This is where the contributor runner lands. It is **spec-first**: the behavior is
fully described in [`../docs/client-spec.md`](../docs/client-spec.md); the code is
the first deliverable of the [MVP](../plans/mvp.md).

## Plan

1. **v0 — Claude Code skill / tiny script.** The lowest-friction on-ramp:
   `claim_subtask` → run one no-tools completion on the contributor's own Claude →
   output guard → `submit_result`. Proves the loop end to end.
2. **v1 — standalone CLI** (`potluck`) with pluggable backends (API SDK as the
   default, Claude Code / Codex CLIs, any OpenAI-compatible endpoint), local budget
   enforcement, and config in `~/.potluck/config.toml`.

## Non-negotiables (carried from the threat model)

- Uses the contributor's **own** account/key; never transmits a credential.
- **Safe mode**: no tools, single turn, untrusted prompt as data, hard local
  budget, pre-publish output guard.
- On failure/over-budget: **discard** partial work and release the lease.

Nothing here is built yet. Start with [`../plans/mvp.md`](../plans/mvp.md).
