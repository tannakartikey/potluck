# Pre-Launch Plan — Security & Production-Readiness Audit (2026-06-15)

**What this is.** A full pre-launch audit of Potluck against one question: *is it ready
to be published and operated as a live thing people run?* It captures every finding,
its severity, file:line evidence, and the fix — organized as a checklist so nothing
gets lost. Findings that were **empirically reproduced** (not just read) are marked ⚑.

**Headline verdict.** Not ready to publish/operate as-is — but **close**, and the
foundation is unusually strong. There is exactly **one critical security blocker**
(the no-tools "safe mode" is not actually enforced — reproduced) plus a short list of
launch blockers. The RLS layer, web tier, and Go code are genuinely solid. Most fixes
are small and well-scoped; nothing here needs re-architecting.

> **How to read severity:** 🔴 critical (do before anyone runs the binary) · 🟠 launch
> blocker (fix before publishing) · 🟡 should-fix (medium) · ⚪ polish (low). Boxes are
> `[ ]` todo / `[x]` done.

---

## 0. Security model — RESOLVED: capability-first, staged (v1 no-tools → v2 curated tools)

**Decision (owner, 2026-06-15).** Don't neuter the model. The product should let the agent
*do* real tasks (read a PDF, look at a page), so the direction is **capability-first**:
allow real tools and **harden the sandbox to match**, rather than permanently restricting
scope. Open-questions #3 (`[locked: no tools]`) is superseded by this staged plan — update it.

**But capability-first flips the safety model** from *scope-based* (safe because the agent
*can't* act) to *isolation-based* (safe because it acts inside a box that contains it). That
makes the **container the single load-bearing boundary**, not a nice-to-have. So we stage it:

- **v1 — the first release (ships now): no-tools, enforced for real.** Fix §1 so tools are
  actually denied (verified flags in §1) + keep the container. Shippable immediately, and the
  correct *interim* state while v2 is built (deny tools until the sandbox is real). Optional
  small add: **inputs-as-data** (the runner attaches a PDF/image as content; the agent stays
  no-tools) — covers the PDF use-case without tools.
- **v2 — next milestone: curated tools + broker + hardened sandbox.** Give the agent a small,
  *project-implemented* tool set (`fetch_url` allowlisted + SSRF-safe, `read_document`) — **no
  raw shell** — behind the hardening below. Raw shell (iii) is a later gVisor/microVM track.

### 0.1 The four-layer model — what each check actually buys

| Layer | Type | Stops | Trust |
|---|---|---|---|
| **Container / sandbox** | structural | agent reading host files (`~/.ssh`,`~/.aws`), `rm -rf`, net exfil, escape | **strong — load-bearing** |
| **Broker** | structural | theft of the provider **API key** (API-key path only) | strong but **narrow (key only)** |
| **Moderation** | filter | reduces *how many* bad tasks reach a worker | medium |
| **Prompt fencing** ("don't be harmful") | instructional | casual/clumsy misbehavior | **weak — not a boundary** |

We *rely on* **container + broker** (they hold regardless of the prompt). Moderation + fencing
are load-reducers, not boundaries — proven here: the system prompt said "treat task as DATA,"
the agent ran Bash anyway (§1); and the model's own refusal caught clumsy attempts but is
bypassable (encoding / innocent framing). A task can pass moderation + fencing + broker and
still exfiltrate `~/.ssh/id_rsa` — only the **container** (where `~/.ssh` isn't present) stops it.

### 0.2 The broker — how it works (verified ⚑)

The API key is just a header the *server* checks; the agent never computes with it. Point the
CLI at a local **broker** (`ANTHROPIC_BASE_URL=http://broker`) holding only a placeholder; the
broker holds the real key, injects it at the last hop, forwards to Anthropic. **Verified:** the
real `claude` CLI routed every call through the broker carrying only `sk-ant-PLACEHOLDER`; the
broker injected the real key; the real key was **never in the agent's env** — so a "dump my env"
attack finds only the placeholder. Protects the **API-key path only**.

### 0.3 The credential reality (verified ⚑)

- **Subscription (OAuth) can't be brokered** (the CLI fetches its own token). On macOS it's in
  the **Keychain** (`Claude Code-credentials`); on Linux it's the file `~/.claude/.credentials.json`.
  Either way a tool with shell reads it — confirmed: `security … -w` dumped the 472-byte token
  with **no prompt**. So **subscription + tools** is protected only by *denying the tools that
  can read it* + the container — strictly weaker than API-key + broker.
- **Container bug:** the runner mounts `~/.claude/.credentials.json`, which **doesn't exist on
  macOS** (Keychain) → containerized subscription likely can't authenticate on macOS (add to §4).
- **Conclusion:** tool-enabled tasks → **API-key + broker + container**; subscription stays on the tighter/no-tools lane.

### 0.4 Flag-based no-tools is fragile — the container is the real enforcement (verified ⚑)

CLI flags are an inconsistent, version-dependent way to disable tools: `--allowed-tools ""`
disables nothing; `--tools ""` left MCP attached; behavior shifts with hooks/CLI versions. The
**robust** containment is the **container** (and, at the CLI layer, a PreToolUse **deny-all hook**
is more reliable than flag incantations). Treat the §1 flags as defense-in-depth, not the boundary.

### 0.5 First Release (v1) — definition of done

- [~] §1 — enforce no-tools for real. **Flag enforcement + test DONE & verified e2e**
      (Claude path, worker+moderator). Remaining: Codex-lane decision + container fail-closed/egress.
- [ ] §2.1 — remove the broken `--container` flag from all docs (6 spots).
- [ ] §2.3 — narrow `contributors` RLS to `(id, display_name)`.
- [ ] §2.2 — commit the anon-gate test; wire into CI.
- [ ] §2.5 — add CI (`go build/test/vet`).
- [ ] §2.4 — fix the README "Nothing is live" contradiction.
- [ ] §4 — harden output guard, add CSP, pin image, wire a real budget cap.
- [ ] update open-questions #3 to reflect this staged decision.

v2 (curated tools + broker + sandbox) is its own track — designed above, built after v1 ships.

---

## 1. 🔴 CRITICAL — no-tools "safe mode" is NOT enforced (reproduced ⚑)

**The whole product rests on threat-model invariant #2:** *"text-only, no-tools safe
mode is a HARD property of the runner … with zero tools the agent is structurally
incapable of touching your filesystem, credentials, or network."* It is not true as
implemented.

**Root cause.** `client/internal/backend/claudecode.go:56-60` passes `--allowed-tools ""`
believing an empty allow-list = "zero tools." It does not. `--allowed-tools` is an
*additive allow-list* (tools that **skip the permission prompt**) — a different flag
from `--disallowed-tools` (deny) and `--tools` (the available set). An empty allow-list
means "nothing pre-approved," **not** "everything denied."

**Reproduction (⚑ on this machine, clean env, runner's exact argv):**
```
# A: runner's exact argv — Bash EXECUTES
claude -p "<task: run a bash echo SENTINEL>" --output-format json --allowed-tools ""
  -> subtype=success, num_turns=2, result="POTLUCK_TOOLCHECK_<user>_OK", denials=[]
# D: + explicit --permission-mode default — STILL executes (not a local-settings artifact)
  -> num_turns=2, executed
# B: the FIX — broad --disallowed-tools — BLOCKED
claude -p "..." --disallowed-tools "Bash Edit Write Read WebFetch WebSearch NotebookEdit Task Glob Grep KillShell BashOutput"
  -> result="NO_TOOLS_AVAILABLE"
# C: --tools "" — built-ins gone BUT MCP tools (Gmail/Calendar/Drive) remained ⚠️
```

**Impact.** On the host path the contributor's **own authenticated agent** can be driven
by a malicious task to read `~/.ssh`/`~/.aws`, run `rm -rf`, or exfiltrate via WebFetch —
the exact §3 "not reachable in v1" class. The cascade (note: this **overturns two of the
audit's own "false-positive" refutations**, which were built on the wrong assumption that
tools were off):
- **Worker** (`runner.go`): runs every `open` task with tools on; **host fallback = zero
  isolation**.
- **Moderator** (`moderate.go:83-88`): shares the same backend → **equally exposed**. A
  prompt-injected *submission* can compromise the (trusted, higher-value) moderator's
  machine *during* moderation, before any accept.
- **Container** (default): real auth file mounted read-only + **open egress**
  (`container.go:54` — no `--network`) means a tool-enabled agent inside can still
  exfiltrate the mounted credential. The container's value prop is undermined, not "moot."

**Tests that let this survive since the first commit:** `claudecode_test.go` /
`codex_test.go` assert only output parsing — **nothing asserts the safe-mode argv.**

**Fix — flag enforcement DONE (2026-06-15); container hardening pending:**
- [x] **Stop relying on `--allowed-tools ""`.** `claudecode.go` now emits
      `--tools "" --strict-mcp-config --disallowed-tools "<every built-in>"`. Verified e2e:
      a Bash-requesting task returns `"I don't have any tools available … NO_TOOLS"`,
      `num_turns=1` (no execution). Argv extracted to `claudeArgs()`.
- [x] **Moderation path fixed too** — `moderate.go` uses the same `ClaudeCode` backend, so the
      one change covers worker **and** moderator.
- [x] **Unit test added** — `TestClaudeArgsNoTools` asserts the deny flags are present and
      `--allowed-tools` never returns (the regression guard that was missing).
- [x] **v1 worker policy = strict no-tools** (per §0 staging; curated tools are v2).
- [ ] **Codex path** still runs `--sandbox read-only` (best-effort, can read-only shell) — it
      cannot be truly no-tools. **v1 decision needed:** restrict the first release to Claude
      Code, or ship Codex clearly labeled as the weaker lane. (Claude Code is the safe default.)
- [ ] **Container** still defense-in-depth, not fail-closed (silent host fallback) + open egress
      — the real boundary per §0.4. Harden in §4 / v2 (mandatory + fail-closed + egress-locked).

---

## 2. 🟠 Launch blockers (publish-stoppers)

### 2.1 The headline `run` command is broken (`--container` doesn't exist) ⚑
- **Evidence.** `AGENTS.md:22,83,91,99`, `README.md:133`, **and** `db/README.md:40` tell
  users to run `potluck run … --container …`. The CLI defines only `--no-container`
  (`main.go:159`; container is already the default). `flag.ExitOnError` → Go rejects the
  unknown flag and **exits code 2 before any work** (verified on the compiled binary).
  `SKILL.md` gets it right, proving internal inconsistency.
- [ ] Delete `--container` from all **6** occurrences; document only `--no-container`.

### 2.2 The project's own "#1 security-review item" doesn't exist as code
- **Evidence.** threat-model.md:284 calls a *"mandatory pre-launch anon-role test"* the
  single blocking gate; api-spec.md:565-579 specifies the matrix. There's **no harness** —
  only an unchecked `[ ]` at `plans/mvp.md:25`; `api_test.go:13` is an httptest stub with
  a fake key. Meanwhile `AGENTS.md:42` asserts "writes return 401 — verified."
- **Live state today is actually correct** (I confirmed: `POST /results` → 401,
  `contributor_keys` → 401), but nothing prevents a future `grant`/new-table regression.
- [ ] Commit a runnable anon-gate script executing the api-spec matrix (exit non-zero on
      any unexpected 2xx); wire into Makefile + CI.

### 2.3 `contributors` RLS over-exposes trust_level + admin identity ⚑
- **Evidence.** `schema.sql:129` (`using(true)`) + table grant (`:333`) → `GET
  /contributors?select=*` leaks `trust_level`/`reputation`/`validated_streak`. **Live, the
  admin's `trust_level:2` + id are world-readable**, and that id appears in `results`, so
  anyone can enumerate moderators/admins. Contradicts docs in 3 places
  (threat-model.md:249, api-spec.md:477,539 "attribution fields only").
- [ ] `revoke select on contributors from anon; grant select (id, display_name) on
      contributors to anon;` (or build the documented-but-missing `contributor_stats` RPC).
- [ ] Re-run the live `select=*` probe to confirm `trust_level` no longer returns.

### 2.4 README "Nothing is live yet" contradicts reality
- **Evidence.** `README.md:11` ("pre-alpha scaffold. Nothing is live yet") vs `AGENTS.md:5,
  164-167` ("live end to end … board is live … built and working" — true). First thing a
  visitor reads, on a project whose pitch is honesty.
- [ ] Pick one canonical status sentence; reuse verbatim in both files.

### 2.5 No CI build/test gate
- **Evidence.** Only `pages.yml` (Pages deploy). No `go build/test/vet` on PRs, while
  `CONTRIBUTING.md` solicits PRs that "attack the RLS policies." `Makefile:17` already has
  the `test` target; nothing runs it.
- [ ] Add a CI workflow (`setup-go` + `cd client && go vet ./... && go test ./...`) on
      `pull_request` + push to main.

---

## 3. 🟢 Verified solid — do NOT re-litigate (reproduced/read where noted)

- **RLS write-gate is correct — confirmed live ⚑.** 5 tables RLS-on; anon SELECT-only;
  `contributor_keys` unreadable (401); `_contributor_for_key` revoked (schema.sql:344);
  all 7 `SECURITY DEFINER` RPCs pin `search_path`, resolve contributor from the key, and
  enforce: submit_result lease+`output_guard_passed` (schema.sql:202), claim_subtask
  `FOR UPDATE SKIP LOCKED` (:174), moderate_task `trust_level>=1` (:289) + no
  self-moderation (:296), grant_trust admin `>=2` + cannot mint admin (:318-320). No
  dynamic-SQL injection. **This is the platform-killing class and it's done right.**
- **Web XSS is actually mitigated.** No markdown→HTML path; every untrusted field flows
  through `esc()` (app.js:20) before `innerHTML`; `artifact_md` stripped + escaped
  (app.js:129,137); all `target=_blank` carry `rel=noopener`.
- **Container credential mount is correct + tested.** Only the single auth file, never the
  whole `~/.claude`/`~/.codex` (container.go:94-112; container_test.go:43-77).
- **Output guard is server-enforced** (not client-only): `submit_result` raises if
  `output_guard_passed` isn't true (schema.sql:202-204).
- **Go is clean.** build / vet / gofmt / `go test -race` all pass; **zero non-stdlib deps**
  (go.mod); creds file `0600` + tested (config.go:88, config_test.go:74-80); secret key
  never logged or placed in a URL (api.go).
- **Secrets hygiene correct.** `.env`/`.private/` gitignored + untracked; committed key is
  `role:anon` read-only JWT (safe to ship); `apply-schema.sh` guards the prod ref.

---

## 4. 🟡 Should-fix (medium)

- [ ] **Output guard is thinner than docs claim** (runner.go:172-181): 8 regexes; misses
      GCP/Stripe/AWS-secret/JWT/Windows-&-`$HOME`/`~` paths; trivially bypassed
      (whitespace/newline/base64); docs advertise "policy-violating content" scanning that
      **doesn't exist** (threat-model.md:83,205). Broaden patterns + correct the docs.
- [ ] **Guard false-positives halt the runner.** `/Users/…`/`/home/…` in a legit answer
      trips the guard, and each block counts toward `maxConsecFail=3` (runner.go:33,131-137)
      → 3 in a row stops the loop. Tighten patterns; don't count guard-blocks as failures.
- [ ] **Codex prompt-injection surface** (codex.go:83): preamble + untrusted task joined by
      bare `\n\n`, no role boundary; Codex is agentic w/ read-only shell, compounding it.
- [ ] **Container not fail-closed** (main.go:213): missing Docker silently downgrades to
      host (tools-on per §1). Add `--require-container`; gate Codex behind a started sandbox.
- [ ] **No CSP** on the board (web/index.html) — `esc()` is a single hand-rolled line of
      defense with no backstop. Add a `<meta http-equiv>` CSP.
- [ ] **`subtasks` read policy exposes `pending`/`rejected`/`needs_review` + rejection_note**
      to anon (schema.sql:131). Restrict to claimable/terminal states.
- [ ] **Denial-of-wallet guard unwired** (now matters since tools/loops are on): `MaxUSD`
      is dead code (backend.go:23, runner.go:114-119), no turn cap; only a 5-min wall clock.

---

## 5. ⚪ Polish (low)

- [ ] `--max-turns 1` is a "hard invariant" in ~7 doc locations but absent from code **and**
      from the current CLI (stale docs) — pick: implement an equivalent cap, or de-claim it.
- [ ] Base image `node:22-slim` + `npm install` unpinned (Dockerfile:12,19) — sandbox not
      reproducible. Pin by digest + version.
- [ ] Non-root only via Dockerfile `USER`, not `docker run --user` (container.go) — add
      `--user 10001:10001` so it's enforced host-side regardless of `--image`.
- [ ] **Image/file inputs documented but unimplemented** (`Subtask.Attachments` decoded at
      api.go:66, never passed to the agent; buildPrompt at runner.go:157-169 ignores them).
      Moderator may accept attachment tasks the worker silently can't run. **(Directly
      relevant to §0 — the ingest path.)**
- [ ] Keep-alive ping & DB backup honestly deferred — a ~15-line cron prevents the "live"
      board pausing after ~7 days idle; pair with a `pg_dump`/REST export for durability.
- [ ] `@latest` installs report `v0.0.1-dev` (main.go:25) — stamp from `debug.ReadBuildInfo`
      or cut tags.
- [ ] README claims "release (checksummed binaries on tag)" that doesn't exist
      (README.md:108) — trim the clause; install-from-source story is correct nearby.
- [ ] Flag spelling `--allowedTools` (docs) vs `--allowed-tools` (code) — unify to the CLI's.
- [ ] `client-spec.md` documents phantom flags (`--once`, `--budget-total`) + an `api`/SDK
      backend that doesn't ship; the "API-key path is the recommended default" ToS argument
      has no shippable flag. Add a "shipped vs planned" banner / reconcile.
- [ ] `moderate_task` is redefined across 001/002/schema.sql — only correct if migrations
      apply in strict order. Fresh deploys use schema.sql (fine); make 002 self-guarding.

---

## 6. Prioritized punch-list (the order to actually do it)

1. [ ] **Settle §0** (no-tools vs. safe-ingest-in-container) — gates the §1 fix.
2. [ ] **§1 — fix the tool mechanism** for worker *and* moderator + argv/integration tests.
       *Do before inviting anyone to run the binary.*
3. [ ] **§2.3 + §2.2** — narrow `contributors`; commit the anon-gate test into CI.
4. [ ] **§2.5 + §2.1 + §2.4** — CI gate; delete `--container` from docs; fix README status.
5. [ ] **§4** — harden the output guard (+ stop FP-halting), add CSP, pin the image, wire a
       real per-run budget cap, add the keep-alive cron.

**Bottom line:** architecture, RLS, web tier, and code quality are good; the docs are honest
*in intent*. The blocker is that the **headline safety guarantee isn't delivered by the
code** while the repo is **already live and inviting use**. Fix §1 (small + verified) and
the §2 blockers, settle §0, and this is ready for the small/trusted scope the threat model
describes.

---

## 7. Appendix — methodology & the audit's own miss

- **Method.** 20-agent workflow: 8 specialized auditors (DB/RLS, safe-mode, container,
  output-guard, web-XSS, Go build/test, docs-accuracy, ops/CI) + adversarial verifiers on
  every critical/high finding, then **owner+assistant empirical reproduction** of the §1
  contradiction. Includes a read-only probe of the **live** Supabase API + the documented
  "anon can't write" gate.
- **The miss worth remembering.** **4 of the workflow's verifier agents independently
  concluded tools were off** — because the docs are so confident and precise they're
  persuasive. The bug is invisible to a docs-trusting reviewer; only *running the binary*
  caught it. Lesson for a project whose thesis is "the docs are the product": **assert
  safety-critical behavior with a test, not prose.**
</content>
</invoke>
