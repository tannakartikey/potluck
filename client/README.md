# client/ — the Potluck runner (Go)

A single static Go binary that donates your spare AI agent credits to open, public
tasks: **register once, then claim → run (on your own model, in safe mode) → submit**,
with an honest token/cost summary at the end.

## Install

From source (recommended — compiles on your machine, straight from this repo; Go's
module checksum DB makes tampering detectable, so there's no prebuilt binary to trust):

```sh
go install github.com/tannakartikey/potluck/client/cmd/potluck@latest
```

Or build locally:

```sh
cd client && go build -o potluck ./cmd/potluck
```

The v0 backend shells out to the **Claude Code CLI** — make sure `claude` is on your PATH.

## Use

```sh
potluck register --name <your-handle>                  # one time → creates your secret key
potluck run --topics rails,postgres --max-tasks 5 --model haiku
potluck search "eager loading"                         # full-text search the open board
potluck status                                         # what you've donated
```

Run flags: `--topics a,b`, `--budget N` (skip tasks needing more than N tokens),
`--model` (`haiku|sonnet|opus` or a full id), `--max-tasks N` (0 = until the queue is
empty or Ctrl-C).

## How it runs a task (safe mode)

For each task the Claude Code backend calls, roughly:

```sh
claude -p "<task text, as DATA>" --output-format json \
  --allowed-tools "" \
  --system-prompt "<fixed safety preamble>" \
  --model <m>
```

That means **no tools** (no shell, file, or web access), a project-controlled system
prompt that replaces the agent default, and execution in a clean temp dir — so a
community-authored task **cannot touch your machine**. The runner parses exact token
usage + cost from the JSON, runs a secret-scanning output guard, then submits through
the key-gated RPC. On failure it discards partial work and re-queues the task; after 3
consecutive failures it stops (likely rate-limited / out of budget).

## Config & state (local — never uploaded)

`~/.potluck/config.json` (topics, model, budget, backend) and `~/.potluck/credentials`
(your secret key, mode `600`). Override the directory with `POTLUCK_HOME`; override the
backend with `POTLUCK_SUPABASE_URL` / `POTLUCK_ANON_KEY` (the bundled anon key is public
and read-only by design).

## Tests

```sh
cd client && go test ./...
```

## Known v0 limitations (tracked in [../plans/open-questions.md](../plans/open-questions.md))

- Backends: **Claude Code** (hard no-tools safe mode) and **Codex** (`codex exec`,
  **best-effort** safe mode — read-only sandbox + isolated empty dir; Codex is agentic,
  so it can still run *read-only* shell, and it reports tokens but **not** cost). Raw
  API / custom-command land behind the same adapter later (#2).
- The future custom-command backend can't enforce no-tools at all (user's responsibility).
- No usage-limit awareness yet (Claude's 5-hour vs weekly windows) — the 3-failure
  circuit breaker is the interim guard (#17).
- Bleeding-edge / install-from-source; signed releases + an auto-updater come later
  (#13, #18).
