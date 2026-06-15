# Phase 2 (v2 capability sandbox) — honest status

**Date:** 2026-06-15 · **Branch:** `phase-2-sandbox` · **Owner был AFK; built autonomously.**

**What Phase 2 is.** Move from "safe because the agent has no tools" (v1) to "safe because the
agent acts inside a box that contains it" (v2): give the agent a SMALL set of curated,
project-implemented tools — `fetch_url` + `read_document`, **never raw shell** — behind a
credential broker, default-deny egress, and a hardened, fail-closed container. The design is
`plans/prelaunch.md` §0; the bar is `docs/threat-model.md` §10.

**Prime directive followed.** Every security property below is backed by a test or an
empirical run, with the exact command + observed output. Nothing here is claimed on prose
alone. Where something is **not** verified in this session, it is marked **UNVERIFIED** with
the reason and the exact command to verify it.

**v1 is untouched and still the default.** Phase 2 is strictly opt-in (`potluck run --phase2`).
With the flag absent, behavior is byte-for-byte v1 (no-tools). `go build/vet/test` + gofmt pass
at every commit; the live anon-gate stays green (re-run at the end of this doc).

---

## 1. What's built & VERIFIED

### 1.1 `fetch_url` — SSRF-safe, allowlisted, capped  (`client/internal/tools/`)
- Default-deny: an empty allowlist fetches nothing; hosts match exact or dot-boundary
  subdomain only (no `evil-example.com` / `example.com.attacker.net` confusion).
- Blocks loopback / private (RFC1918+ULA) / link-local incl. the **169.254.169.254** metadata
  IP / CGNAT / IPv4-mapped-IPv6 / NAT64 / multicast / unspecified — enforced at **dial time**
  via a `net.Dialer.Control` hook that re-validates the literal post-DNS IP (TOCTOU-safe vs
  DNS-rebinding). Redirects re-validate host allowlist + IP on every hop. Size + time caps.
- **Verified (unit):** `go test ./internal/tools/` — incl. the blocked-IP table (incl. v4-mapped
  smuggling), allowlist-bypass attempts, "dialer refuses loopback even when allowlisted",
  "redirect to metadata IP blocked even when allowlisted", "redirect to non-allowlisted host
  blocked", size + timeout caps.
- **Verified (live, through the real `potluck __tools-server` process, allowlist=example.com):**
  ```
  id2  metadata 169.254.169.254   isError=True  error: URL points at a blocked address 169.254.169.254
  id3  loopback 127.0.0.1         isError=True  error: URL points at a blocked address 127.0.0.1
  id4  non-allowlisted host       isError=True  error: host "evil-not-allowed.example" is not in this task's fetch allowlist
  id5  allowlisted example.com    isError=False  [fetch_url] GET https://example.com/ -> 200 text/html (559 bytes)
  ```

### 1.2 `read_document` — text/HTML + best-effort PDF, traversal-safe  (`client/internal/tools/`)
- Reads ONLY inside a configured base dir; defeats `../`, absolute-path, and symlink escape
  (re-checks containment after `EvalSymlinks`). text/md/csv/json pass-through; HTML is
  script/style + tag stripped; PDF is best-effort pure-Go (finds content streams, inflates
  FlateDecode via `compress/zlib`, extracts Tj/TJ/'/" text incl. hex strings, octal escapes,
  TJ kerning→space). File + output caps; binary refused.
- **Honest limits:** no encrypted, scanned/image-only (no OCR), or exotic CID-font ToUnicode
  PDFs — those yield partial/empty text, never a crash.
- **Verified (unit):** text, HTML (script stripped), PDF uncompressed + FlateDecode + operator
  extraction, and traversal/symlink/absolute/binary/size-cap refusals.
- **Verified (live):** the agent called `read_document` on `notes.txt` and returned its exact
  contents `SECRET-DOC-CONTENT-42` (knowable only by actually invoking the tool).

### 1.3 MCP curated-tools server  (`client/internal/mcp/`)
- A minimal JSON-RPC 2.0 / stdio MCP server exposing **exactly** `fetch_url` + `read_document`
  and nothing else. `tools/call` returns blocked-URL / traversal failures as `isError` results.
- **Verified (unit):** initialize shape; `tools/list` == exactly the two tools (+ a guard that
  no shell/file tool ever appears); read happy + traversal-refused; fetch empty-allowlist
  denied; unknown tool refused; unknown method → JSON-RPC error; notifications get no reply.
- **Verified (live):** real Claude Code consumed it and used both tools (§1.7).

### 1.4 Credential broker — real key injected at the last hop  (`client/internal/broker/`)
- A stdlib reverse proxy that holds the REAL key and injects it only when forwarding to the
  provider. The agent is pointed at the broker (`ANTHROPIC_BASE_URL`) with only a placeholder,
  so the real key is never in the agent env. Fails closed without a key; never logs the key.
  `ScrubbedAgentEnv` builds the agent env: host env minus every real provider secret, plus the
  broker URL + placeholder.
- **Verified (unit, mock upstream):** upstream receives the REAL key, never the placeholder;
  Authorization placeholder stripped; path forwarded; empty key refused; error responses carry
  no key; `ScrubbedAgentEnv` hides the real key and keeps harmless vars.
- **Verified (live, in-container, end-to-end):** the real `potluck __broker` binary ran as a
  dual-homed sidecar; an agent on the internal-only network reached the provider **only via the
  broker** — `via broker -> HTTP 401` (agent → broker → real api.anthropic.com → fake key
  rejected, i.e. the full proxy path works) — while `internet -> BLOCKED` directly.

### 1.5 PreToolUse deny hook  (`client/internal/hook/`)
- The robust CLI-layer boundary (prelaunch §0.4): ALLOWS only the curated MCP tools, DENIES
  everything else, with a double signal — the modern `permissionDecision` JSON **and** exit
  code 2 as a fail-safe backstop. Malformed/empty input fails closed.
- **Verified (unit + as a real subprocess + in-container):** denies Bash/Read/Write/WebFetch
  (exit 2), allows the two curated tools (exit 0), fails closed on malformed input.

### 1.6 Hardened, fail-closed container + default-deny egress  (`client/internal/sandbox/`, `docker/Dockerfile.phase2`)
- Agent container: `--user 10001:10001 --read-only --cap-drop ALL --security-opt
  no-new-privileges --pids-limit/--memory/--cpus`, ephemeral tmpfs, on an `--internal`
  (no-internet) network. The broker sidecar is dual-homed (internal + public) and is the ONLY
  thing the agent can reach. Fail-closed `Preflight`: no real key / no Docker / image unbuilt →
  refuse; **never** fall back to the host.
- **Verified (unit):** arg hardening set, broker args (no key value inline), fail-closed
  preflight, idempotent networks, broker start+connect sequence.
- **Verified (Docker, `scripts/verify-sandbox.sh`, 10/10 PASS):**
  ```
  [1] container hardening
    PASS  runs as non-root uid 10001
    PASS  root filesystem is read-only
    PASS  ephemeral tmpfs scratch writable
    PASS  all Linux capabilities dropped          (CapEff: 0000000000000000)
    PASS  no-new-privileges set                   (NoNewPrivs: 1)
  [2] in-container tooling
    PASS  potluck binary present
    PASS  deny-hook blocks Bash in-image
  [3] default-deny egress (sidecar broker model)
    broker: REACHED 405                            (agent → broker → real Anthropic)
    internet: BLOCKED ...timeout                   (agent → internet directly)
    PASS  agent can reach the broker sidecar
    PASS  agent CANNOT reach the internet (default-deny)
    PASS  broker (dual-homed) can reach the provider
  SANDBOX-VERIFY PASSED
  ```
  Also confirmed independently: `--network none` blocks all egress.

### 1.7 `--phase2` runner path  (`client/internal/backend/curated.go`, `cmd/potluck/`)
- `potluck run --phase2 [--fetch-allow hosts] [--doc-dir DIR]` runs the curated backend inside
  the hardened sandbox behind the broker. The tool boundary is **layered** (no single fragile
  flag is load-bearing): MCP exposes only the two tools · `--strict-mcp-config` drops user MCP ·
  `--disallowed-tools` denies every builtin · `--allowed-tools` pre-approves ONLY the two
  curated tools (never the inert `--allowed-tools ""` that was the v1 platform-killing bug) ·
  the PreToolUse hook denies anything else.
- **Verified (live, real Claude Code subscription, host tool-surface):** asked to fetch a URL,
  read a doc, and run Bash:
  ```
  1. fetch_url on https://example.com: <title> = "Example Domain"
  2. read_document on notes.txt: "SECRET-DOC-CONTENT-42"
  3. Bash echo PWNED: BLOCKED — "Bash exists but is not enabled in this context"
  permission_denials: [ToolSearch]   subtype=success  num_turns=5  cost=$0.031
  ```
  → only `fetch_url` + `read_document` were usable; Bash and ToolSearch were blocked.
- **Verified (live, fail-closed):** `potluck run --phase2` exits non-zero and refuses when
  `ANTHROPIC_API_KEY` is unset ("the OAuth/subscription path cannot be brokered; use an API
  key") and when the sandbox image is not built — it never falls back to the host.

---

## 2. The execution model (how it fits together)

```
  host: potluck run --phase2  ── Preflight (key+docker+image) FAILS CLOSED ──┐
        │ EnsureNetworks (potluck-egress = --internal, potluck-public)        │ refuse if any
        │ StartBroker sidecar  ─ holds REAL key (-e by name) ─ dual-homed ────┘ precondition unmet
        ▼
  ┌─ agent container (per task) ──────────────────────────┐     ┌─ broker sidecar ─────────┐
  │ --user 10001 --read-only --cap-drop ALL               │     │ holds the REAL key        │
  │ --no-new-privileges --pids/mem/cpu --network egress   │     │ injects it at the last hop│
  │ env: ANTHROPIC_BASE_URL=broker, ANTHROPIC_API_KEY=ph  │────▶│ egress: internal+public   │──▶ api.anthropic.com
  │ claude  ──spawns──▶ potluck __tools-server (MCP)       │     └───────────────────────────┘
  │   tools: fetch_url (allowlist+SSRF), read_document     │  (agent has NO route to the internet;
  │   PreToolUse hook: potluck __hook (deny non-curated)   │   only the broker is reachable)
  └───────────────────────────────────────────────────────┘
```

The agent's key never exists in its world (placeholder only); its only network peer is the
broker; its only tools are the two curated ones; its filesystem is read-only and non-root.

---

## 3. What is NOT verified in this session (honest gaps)

1. **A full successful generation THROUGH the broker with a real key** is **UNVERIFIED here**
   — this machine authenticates Claude via macOS Keychain **OAuth**, and OAuth cannot be
   brokered (the CLI fetches its own token); there is no `ANTHROPIC_API_KEY` to inject. What IS
   verified: the broker injects the real key (mock-upstream unit test), the agent→broker→**real
   Anthropic** path works (live 401/405 through the broker), and the agent env carries only the
   placeholder (`ScrubbedAgentEnv` unit test). The missing piece is purely "real key →
   200 completion," which needs an API key. **To verify:**
   `ANTHROPIC_API_KEY=sk-ant-… potluck run --phase2 --fetch-allow example.com --max-tasks 1`
2. **The baked `potluck-sandbox:phase2` image** was building on this host's 2023-era Docker
   daemon and was very slow (large npm install); it had not finished at write time. All
   container properties were instead verified against the **behaviorally identical** base
   (`potluck-runner:latest` = the same `node:22-slim` + uid-10001 runtime stage) with the exact
   cross-compiled linux binary **bind-mounted** in — the only difference from the baked image is
   baked-vs-mounted binary. The Dockerfile's two stages are each independently sound: the Go
   cross-compile is proven (`/tmp/potluck-linux`, static ELF), and the runtime stage mirrors the
   proven runner image + `COPY` binary. **To verify the artifact itself:**
   `docker build -t potluck-sandbox:phase2 -f docker/Dockerfile.phase2 . && bash scripts/verify-sandbox.sh`
3. **Live SSRF *via the LLM*** was flaky (a haiku run got confused about MCP invocation and
   didn't attempt the calls). SSRF blocking is instead proven deterministically through the real
   `potluck __tools-server` process (§1.1) and exhaustively in unit tests — the LLM is not part
   of the SSRF boundary, so this is not a gap in the property, only in the demo path.

---

## 4. Security caveats (read before relying on this)

- **Hostname egress allowlists are not a cryptographic guarantee.** Network-layer default-deny
  (the `--internal` net) is the real egress boundary and is strong: the agent has NO internet
  route, only the broker. `fetch_url`'s per-host allowlist is an application-layer control on top
  of an SSRF-safe fetcher. A TLS-terminating egress proxy (threat-model §10) would be needed for
  a guarantee against domain-fronting; it is not built. For v2 the agent has no raw network tool
  at all, so the only egress is `fetch_url` (allowlisted) + the broker (provider) — both vetted.
- **Codex has no curated-tools path.** Phase 2 is Claude Code only; Codex remains the weaker,
  read-only-sandbox lane (`docs/threat-model.md` §9a). `--phase2 --backend codex` is not wired.
- **Concurrency:** the sandbox uses fixed broker name/port/networks and assumes the runner
  processes tasks sequentially (it does), tearing the sidecar down per run. Parallel `--phase2`
  runs on one host would collide — not supported yet.
- **Base image not digest-pinned** (still `node:22-slim`); agent CLIs ARE version-pinned.
- **The system prompt is not a boundary** (prelaunch §0.1) — the container + broker + curated
  surface are. The curated preamble is a load-reducer only.

---

## 5. How to run & reproduce the verification

```sh
# unit tests (all the tool/broker/hook/mcp/sandbox properties)
cd client && go test ./...

# container hardening + default-deny egress (Docker) — against the baked image:
docker build -t potluck-sandbox:phase2 -f docker/Dockerfile.phase2 .
bash scripts/verify-sandbox.sh
# …or without the full build (behaviorally identical):
CGO_ENABLED=0 GOOS=linux go -C client build -o /tmp/potluck-linux ./cmd/potluck
POTLUCK_SANDBOX_IMAGE=potluck-runner:latest POTLUCK_BIN=/tmp/potluck-linux bash scripts/verify-sandbox.sh

# run a real phase-2 task (needs an API key; fails closed otherwise):
ANTHROPIC_API_KEY=sk-ant-… potluck run --phase2 --fetch-allow example.com --max-tasks 1
```

---

## 6. What remains (prioritized)

1. Finish + commit a built `potluck-sandbox:phase2` (digest-pin the base) and run
   `verify-sandbox.sh` against the artifact in CI.
2. Full live e2e with a real API key (the one UNVERIFIED property — §3.1).
3. TLS-terminating egress proxy for a real hostname-allowlist guarantee (threat-model §10).
4. Per-task fetch allowlist carried by the task/DB (today it's contributor-set via `--fetch-allow`,
   which is the safer default but less automatic).
5. Concurrency: unique broker/network names per run for parallel `--phase2`.
6. Decide Codex's curated story (or keep it excluded from `--phase2`).
