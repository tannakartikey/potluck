# Client / Runner Spec

The **runner** is the program a contributor installs. It is the only place real
tokens are spent, and it is the single enforcement point for both **safety** and
**budget**. The central server is never trusted to bound a task; the runner is.

This document specifies behavior, not final code. The code lands with the
[MVP](../plans/mvp.md).

## Design stance

- **The runner is open source.** Its trustworthiness to the contributor comes from
  being auditable; its trustworthiness to the commons comes from the contributor
  choosing to run it unmodified. Neither is cryptographic — see
  [model attestation](../plans/open-questions.md).
- **Your account, your machine, your key.** The runner uses the contributor's own
  provider auth and never transmits a credential anywhere.
- **The task is data, never instructions.** The untrusted `prompt` is wrapped
  inside a fixed, project-controlled system prompt.

## Language

The reference runner is a **single static Go binary** (cross-platform, no runtime
to install — chosen for distribution). The runner is a thin client of the
[participation protocol](../AGENTS.md), so a runner can be written in any language;
Go is just the reference implementation.

## Pluggable backends

The runner shells out to (or calls) whatever the contributor already has. One
adapter interface, several implementations:

| Backend | How it's invoked | Notes |
|---|---|---|
| **Claude Code (headless)** | `claude -p --allowedTools "" --max-turns 1 --model <id>` | Runs on the contributor's Claude subscription or API. Safe mode = empty allowed-tools + single turn. |
| **Codex CLI** | analogous headless, no-tools invocation | OpenAI subscription/API. |
| **Anthropic / OpenAI API (SDK)** | direct single completion call, no tools | **Recommended default for unattended/batch** — automation via API is unambiguously permitted; the runner reads the model id from the response. |
| **Any OpenAI-compatible endpoint** | base-URL + key the user supplies | GLM, MiniMax, local models, etc. Potluck judges by outcome, so any agent is welcome. |

> **API key vs. subscription.** Make the **API-key path the documented default**
> for queue-grinding; frame subscription-CLI use as interactive, modest-volume.
> Provider subscriptions assume "ordinary individual usage"; heavy unattended use
> of a personal subscription is the contributor's own gray-area risk. The runner
> caps per-contributor subscription volume as a nudge. (See [threat-model](threat-model.md) §4.2.)

## Configuration

```
potluck run \
  --topics rails-news,ml-papers   # category filter (claim only these)
  --budget 8000                   # HARD local token cap per task
  --model claude-haiku-4-5        # which model the runner will actually use
  --backend api|claude-code|codex # adapter
  --max-tasks 20                  # stop after N (or run until --budget-total spent)
  --once                          # claim exactly one and exit
```

Persisted in `~/.potluck/config.json`. There is no `potluck login` and no GitHub
OAuth. `potluck register` establishes the contributor identity: it generates a
random secret key locally (`"potluck_"` + 32 random bytes hex, ≥ 24 chars), calls
`register_contributor(p_key, p_display_name)`, and saves the secret to
`~/.potluck/credentials` (mode 600, never uploaded). The server stores only the
SHA-256 hex of the key. The provider credential reference is the contributor's own
and stays local — Potluck never receives it.

## The claim / lease protocol

```
loop:
  task ← rpc claim_subtask(topics=cfg.topics)      # atomic; 15-min lease
  if task is null: sleep/backoff; continue          # queue empty for these topics
  if task.token_budget > cfg.budget: rpc release_lease(task.id); continue   # refuse oversized
  if violates_model_policy(task, cfg.model): rpc release_lease(task.id); continue
  result ← execute(task)                            # see safe mode below
  if result.ok and result.guard_passed:
      rpc submit_result(task.id, result.md, reported_model=cfg.model, ...)
  else:
      rpc release_lease(task.id, failed=result.hard_error)   # v1: discard partial, re-queue
```

- **Lease = 15 min.** If the runner crashes, the lease lapses and the task is
  reclaimed lazily by the next claimer. No heartbeat is required in v1 (tasks are
  short); a `heartbeat`/lease-extend RPC is reserved for longer future tasks.
- **Refuse, don't truncate.** A task whose advisory `token_budget` exceeds the
  local cap is released untouched — the runner never spends past the contributor's
  cap. This is the denial-of-wallet defense.

## Safe mode (the execution contract)

```
SAFE MODE =
    no tools            (no shell, no file read/write, no web fetch, no MCP)
  + single turn         (no agentic looping)
  + untrusted prompt confined to the DATA role inside a fixed system prompt
  + minimal context fed in (only the wrapped task; nothing local)
  + hard guards: token budget, output-size cap, wall-clock timeout, duplicate-call debounce
  + pre-publish output guard before anything is uploaded
```

The fixed system prompt (project-controlled, shipped with the runner) does three
jobs: states the agent's role and limits; forbids following instructions embedded
in the task text; forbids revealing local/system context. The task text is always
passed as user/data content — never as system or developer instructions.

**Image inputs:** if `attachments` contains image URLs, the runner passes them as
image content to a vision-capable model. This stays within safe mode (still no
tools); the new residual is *prompt-injection-via-image* (instructions hidden in a
picture), handled by the same data-role framing and output guard — noted honestly
in the [threat model](threat-model.md).

## Budget enforcement

All client-side, because the contributor's machine is the only trustworthy
enforcement point:

| Guard | Purpose |
|---|---|
| Per-task hard token cap | Refuse/abort above `--budget`. |
| `--max-turns 1` | Kill agentic looping. |
| Output-size cap | Bound runaway generation. |
| Wall-clock timeout | Bound a stuck run. |
| Duplicate-call debounce | Break repeated-call loops. |
| Clear SUCCESS / FAILED terminal state | No ambiguous "still going" (a large cost-saver in agent systems). |

**Partial work in v1:** not implemented. Tasks are sized to be done-or-not within
a single sub-budget call. On failure or over-budget the runner **discards** what it
has and releases the lease so another contributor retries cleanly. The
`subtasks.checkpoint` column is reserved so resume-by-another-contributor can be
added later without a migration.

## Provenance the runner attaches

On submit, the runner reports (none of it a correctness or model proof — see
[open-questions](../plans/open-questions.md)):

- `reported_model` — the model the runner used, read from the backend/API response
  where available (more reliable than asking the model to name itself).
- `self_described_model` *(optional)* — if the task asks the model to declare what
  it is, the answer is captured separately as a weak anomaly signal. Never trusted
  as truth (models are often wrong about their own identity, and a relay can edit
  the answer).
- `token_count`, `prompt_hash`, `output_guard_passed`, UTC timestamp.

## "Just a skill" path

Because safe mode is literally *one no-tools model call against a wrapped prompt*,
the runner can also ship as a **Claude Code skill / tiny script** that:

1. calls `claim_subtask` (HTTP POST to the PostgREST RPC, passing the contributor's
   secret key as `p_key` in the body; the `apikey`/`Authorization` header carries
   only the project's public anon key, not a per-user identity),
2. runs the returned `prompt` as a single no-tools completion,
3. runs the output guard,
4. calls `submit_result`.

This is the lowest-friction on-ramp ("just turn on my Claude and point it here")
and a good first deliverable. The standalone CLI follows for non-Claude backends
and unattended runs.

## Out of scope for the runner (v1)

No shell/code execution, no repo checkout, no arbitrary web browsing, no
multi-turn agent loops, no credential collection, no partial-resume. Each of those
is gated behind a later milestone with its own safety bar
([roadmap](../plans/roadmap.md), [threat-model](threat-model.md) §10).
