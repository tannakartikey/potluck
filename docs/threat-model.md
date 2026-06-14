# Threat Model & Safety

This is the most important document in the Potluck repo. Read it before you contribute.

Potluck lets you point **your own** AI agent (your own account, your own API key, on your own machine) at a shared list of **open, public** tasks. The results become open, attributed artifacts. There is no central compute, no pooled keys, and no pay-per-outcome.

This document is written for a developer deciding whether to run the contributor CLI on their own account. It tells you, honestly:

- what the system can and cannot do to your machine and your provider account,
- where the real risks are (and which are borne by *you*, the contributor),
- what v1 SAFE MODE actually enforces,
- and what is explicitly *not* protected yet, so you can decide with eyes open.

If after reading this you would not run the CLI, that is the correct outcome — we would rather lose a contributor than surprise one.

---

## 1. The one-paragraph version

v1 runs **text/knowledge tasks** with **no tools**: no shell, no file access, no code execution, no arbitrary web fetch. Inputs may be text or images (a task can attach an image for the agent to *describe*); the output is always text. The safety-critical invariant is **no tools** — not the input modality. The contributor CLI invokes your agent in a hard, non-overridable **text-only no-tools "safe mode"**, wrapping the (untrusted) task text as *data* inside a fixed system prompt the task cannot override. Because the agent has zero tools, it is *structurally* incapable of touching your filesystem, your credentials, or the network — regardless of what a malicious task says. That single design choice removes the entire catastrophic class of risks seen in tool-enabled coding agents in 2025–2026. What remains is a smaller, honestly-named set: prompt-injection that tries to leak your local context *into the public artifact*, ToS/account risk because the work runs under your own key, misinformation in single-source results, cost-griefing, sybil/spam, and copyright on derived text. Each is addressed below — some fully, some only partially.

---

## 2. The three invariants that never relax

These are non-negotiable across all phases of the project. If a change would break one of these, it does not ship.

1. **Your account, your machine, your key.** Potluck never receives, stores, proxies, or pools any API key, OAuth token, or login credential. You authenticate locally with your own provider account; only finished public artifacts and their metadata cross the network. Pooling or sharing keys is permanently out of scope — not just deferred for v1.

2. **Text-only, no-tools safe mode is a hard property of the runner.** The contributor CLI launches the agent with no tools enabled (for the Claude Code path: `--allowedTools "" --max-turns 1`). This is a security invariant, not a default you casually flip. With zero tools, the agent cannot run shell, read/write files, fetch arbitrary URLs, or call MCP servers.

3. **Row-Level Security (RLS) on every table from creation.** The static site and CLI talk to the database directly using a public anon key; RLS is the *entire* security model. Privileged state transitions happen only through `SECURITY DEFINER` RPCs — never through broad client `UPDATE` grants.

---

## 3. What the scope buys you (the threats v1 does NOT have)

The dominant security asset in v1 is **scope**, not infrastructure. Because v1 tasks are text-only with no tools, the following — all actively exploited against tool-enabled agents in 2025–2026 — are simply **not reachable**:

```
THREAT CLASS                          REACHABLE IN v1?   WHY NOT
------------------------------------  -----------------  --------------------------------
Credential/SSH/cloud-key exfil        NO                 Agent has no file Read tool;
  (read ~/.aws, ~/.ssh, ~/.gcloud)                       cannot open any local file.
Destructive commands (rm -rf $HOME)   NO                 No Bash/shell tool.
Lateral attacks on third parties      NO                 No arbitrary network/web tool.
Crypto-mining / fork bombs            NO                 No code execution; one model turn.
Supply-chain / RCE via injected code  NO                 No code is executed at all.
```

Real-world reference points this scope sidesteps: CVE-2025-53773 (Copilot RCE via repo comments), CVE-2025-54135 (Cursor RCE), CVE-2025-66479 (Claude Code egress-isolation bypass), and multiple documented `rm -rf` home-directory wipes. None of these have an attack surface in a no-tools text-only agent.

This is why the no-tools invariant must be hard to relax accidentally: the moment a task can request a tool, this entire column flips from NO toward YES. Coding tasks (which need tools) are gated behind a separate, published sandbox bar — see §10.

---

## 4. Enumerated threats

Each threat below lists **impact**, **who bears it**, **v1 mitigation**, and an honest **residual** (what is still not covered).

### 4.1 Malicious / prompt-injection task targeting the contributor's machine

**Impact.** A task author writes task text designed to hijack your agent — to make it ignore its instructions, exfiltrate your local context, or produce harmful output under your account.

**Who bears it.** You, the contributor, run the work under your own key. The reputational/account consequences land on you, not on the central project.

**Why the catastrophic version is impossible in v1.** The agent has no tools (§2, §3). It cannot read a file, run a command, or make a network call. So a prompt-injection cannot turn into host compromise. The 2025 design-pattern literature (Willison; Beurer-Kellner et al., "Design Patterns for Securing LLM Agents") states the rule plainly: once an agent ingests untrusted input it must be *structurally* incapable of consequential actions. v1 satisfies this trivially by giving the agent zero tools.

**What injection can still attempt in v1.** Two things:

```
  (a) EXFILTRATION-VIA-OUTPUT — the task tries to make the agent dump your
      local/prior context or any secret it can see into the PUBLIC artifact.
  (b) CONTENT HARM — the task tries to make the agent produce toxic, illegal,
      or jailbroken output, which is then billed to YOUR account and published
      under YOUR attribution.
```

**v1 mitigations (defense in depth; none is airtight):**

- **Task text is confined to the DATA role.** The runner wraps the untrusted task inside a fixed, project-controlled system prompt that (i) states the agent's role and limits, (ii) forbids following instructions embedded in the task text, and (iii) forbids revealing local or system context. Task text never occupies the system/developer role.
- **Minimal context.** The agent is fed *only* the wrapped task — no environment dump, no prior conversation, no secrets. There is very little to leak because very little is in scope.
- **Pre-publish output guard (client-side).** Before any artifact is uploaded, the runner scans it for likely secret patterns (API keys, token blobs, private-key blocks), local paths/usernames, and policy-violating content. A result that fails the guard is not published (`output_guard_passed = false`; RLS blocks the insert).

**Residual (honest).** Output scanning is heuristic and will miss novel secrets or cleverly-encoded leaks. Anti-injection system prompts reduce but do not eliminate jailbreaks. The content-harm risk is *real even with zero host access* and is borne by you — see §4.2. Treat the safe-mode guarantee as "your machine and credentials are safe; your account's *reputation* and the artifact's *quality* are not fully guaranteed."

### 4.2 ToS / credit-pooling / account-reputation risk

**Impact.** Two distinct sub-risks:

```
  (i)  POOLING/ROUTING credentials — if Potluck ever collected, relayed, or
       pooled API keys or OAuth tokens, it would directly violate both
       Anthropic's and OpenAI's credential-sharing prohibitions, and match the
       Jan-2026 enforcement pattern where Anthropic blocked third-party tools
       that extracted/routed consumer OAuth tokens. This is platform-killing.
  (ii) INDIVIDUAL-USE drift — using a personal Pro/Max subscription as a 24/7
       community compute service can exceed the "ordinary, individual usage"
       those subscriptions assume.
```

**Who bears it.** (i) is borne by the whole project — it is the one mistake that ends Potluck. (ii) is borne by the contributor: rate-limiting or account action lands on your subscription.

**v1 mitigations:**

- **Pooling is unfixable by design and therefore permanently out of scope** (§2, invariant 1). The architecture has no place to put a key. This is documented so no one builds key-pooling as a "convenience."
- **Two sanctioned execution paths, both first-party:** (a) your own API key via the official SDK/API, or (b) the official first-party CLI (Claude Code / Codex) on your own subscription. The runner shells out to *your* official tool or uses *your* key locally; it never consumes extracted subscription OAuth tokens.
- **API key is the recommended default for unattended/batch runs.** Automation via API is unambiguously permitted by both providers. Subscription-CLI use is framed as interactive, modest-volume only, and per-contributor subscription task volume is capped client-side.
- **Output ownership makes publishing clean.** Both providers assign output rights to the user, so you own your output and can release it under the pool's open license. A submit-time attestation captures this (see §4.6).

**Residual (honest).** Provider terms change; what is "ordinary individual usage" is a judgment call, not a bright line. If you heavily automate a subscription rather than using an API key, you accept that gray-area risk yourself. We surface this clearly and nudge you toward the API-key path.

### 4.3 Misinformation / low-quality results

**Impact.** A published artifact is wrong, fabricated, or low-effort, and someone downstream trusts it.

**Who bears it.** The commons (everyone who reads the artifact). The harm scales with how factual/high-visibility the task is.

**v1 mitigations:**

- **Provenance-first, honestly labeled.** Every result carries a signed provenance manifest — model ID, contributor ID, UTC timestamp, prompt hash, token count — stored in the DB and surfaced on the public artifact with an explicit **AI-GENERATED** label. v1 results are accepted **single-source** and labeled **`unverified`**. There is no consensus, no second runner, no LLM judge, and no challenge window in v1.
- **The honest framing, stated everywhere:** provenance proves **who / what / when**, **not** correctness. A signature does not make a claim true, and it does not even prove the named model actually produced the content — a contributor can sign a fabricated result.
- **The real v1 quality lever is task design.** Crowdwork research is emphatic that bad *task design*, not bad workers, is the #1 failure mode. So v1 invests effort in hand-written, ideally **machine-checkable acceptance criteria** on each task (e.g. "every claim has a resolvable source URL", "covers these N required points"). External, tool-checkable criteria beat subjective agreement and resist trivial guessing.

**Residual (honest).** A single authenticated contributor *can* submit junk in v1. Provenance does not catch it. The defenses that actually catch wrong answers — N-of-M consensus on structured outputs, gold/honeypot tasks, optimistic challenge windows, reputation/adaptive replication — are **deferred** (see §9). Until then, treat every `unverified` artifact as exactly that. This is why v1 runs at trusted/invite scale, not as an open public network.

### 4.4 Cost-griefing ("denial of wallet")

**Impact.** A task is crafted to burn as many of your tokens as possible — agentic looping, redundant calls, runaway output — spending real money on your account for nothing. Documented agent-system cases include thousands of dollars burned in hours and hundreds of reasoning steps with no answer.

**Who bears it.** You. Every task spends *real paid tokens* on your account. Unlike Folding@home's idle CPU, there is a marginal dollar cost to every run.

**v1 mitigations — all entirely client-side, because the contributor's machine is the only trustworthy enforcement point:**

```
  HARD GUARD                  PURPOSE
  --------------------------  ------------------------------------------------
  Per-task token budget       Contributor-set hard cap (e.g. 5k–10k). The
    (hard cap)                runner REFUSES any task whose declared budget
                              exceeds the local cap.
  --max-turns 1               Kills agentic looping — one model turn, done.
  Max-iteration / call cap    Bounds tool/iteration calls (moot at 0 tools,
                              but kept as a structural guard).
  Duplicate-call debounce     Breaks repeated-call loops.
  Output-size cap             Bounds runaway generation.
  Wall-clock timeout          Bounds a stuck run.
  Clear SUCCESS/FAILED state  Terminal states alone gave ~7x cost reduction in
                              agent-system research — no ambiguous "still going".
```

**The critical rule:** the central API treats a task's declared `token_budget` as **advisory only**. The contributor's runner is the *sole* enforcement point. The server can never specify an unbounded task, because the cap lives on your machine, not in the task definition. This is the denial-of-wallet defense, and it works precisely because the cap is yours.

**Residual (honest).** A task that runs right up to your budget ceiling and then fails wastes that whole run's tokens. v1 mitigates this by sizing tasks well *below* the cap (a single Haiku-class call under ~5k tokens), so most tasks are cheap and done-or-not. But the wasted-near-ceiling case is real, and you set the budget knowing it.

### 4.5 Sybil / spam / corpus poisoning / free-tier DoS

**Impact.** A bad actor creates many identities to spam the task queue, flood the commons with junk results, defeat any future consensus by outvoting it, or mass-insert rows to exhaust the free-tier database.

**Who bears it.** The commons and the shared infrastructure.

**v1 mitigations (thin, and we say so):**

- **GitHub-OAuth identity.** Contributors authenticate via GitHub; account age and public footprint are a weak, free uniqueness signal.
- **RLS insert-only-own-rows.** A contributor can `INSERT` a result only where `contributor_id = auth.uid()`, a matching active lease exists, and `output_guard_passed = true`. Claims happen only through the `claim_subtask()` RPC (atomic, `FOR UPDATE SKIP LOCKED`) — no broad client `UPDATE`.
- **Small, trusted/invite-ish contributor set.** v1 is run among known contributors, which is what makes thin anti-sybil defenses acceptable.

**Residual (honest).** An *authenticated* contributor can still submit junk in v1. This scales only as far as the contributor set is trusted. The real defenses are **deferred and documented as the gate before opening the network**: Discourse/OSM-style trust levels (new accounts get small daily claim limits and cannot create tasks), proof-of-work-lite, per-identity rate limits, moderation/auto-screening of *new-submitter task submissions* (the injection vector for the whole network), gold/honeypot tasks, and N-of-M redundancy. See §9. Free-tier DoS is bounded today only by RLS write policies + the small contributor set; per-identity write rate limits land with trust levels.

### 4.6 Privacy & copyright on derived artifacts

**Impact.** Two issues, both independent of the AI providers' ToS:

```
  PRIVACY    A leak (§4.1) or a careless task could place personal/local
             information into a permanent, public, forkable git artifact.
             Git history makes deletion hard.
  COPYRIGHT  Tasks like "summarize this book" produce derived text; publishing
             third-party-derived summaries into an open pool can raise
             copyright questions on its own.
```

**Who bears it.** The contributor (whose context could leak) and the commons (which hosts the artifact permanently).

**v1 mitigations:**

- **Minimal context + pre-publish output guard** (§4.1) reduce the chance that local/personal data reaches the artifact.
- **Scope tasks to clearly fair-use / public-domain / transformative material** in v1. Avoid tasks built on freshly-copyrighted full works.
- **Submit-time contributor attestation.** At submission, the contributor affirms: (1) they ran it on their own account within that provider's ToS; (2) they own the output and license it under the pool's open license (e.g. CC BY / MIT / CC0); (3) the task is public and non-prohibited.
- **Provenance + AI-origin label** on every artifact, satisfying disclosure expectations for publicly-published automated content.
- **Pool-use clause** barring use of harvested outputs to train competing frontier models, to stay clear of both providers' anti-competing-model restrictions while permitting normal open reuse.

**Residual (honest).** Output scanning is heuristic; a novel secret or an embedded PII string can slip through into permanent git history. Neither provider warrants outputs are non-infringing, and standard tiers carry limited indemnity. Scoping + attestation reduce but do not eliminate copyright exposure on derived text.

---

## 5. v1 SAFE MODE — exactly what it enforces

SAFE MODE is the property that makes it reasonable to run a stranger's task on your own account. It is the conjunction of the following, all enforced **on the contributor side** by the runner:

```
SAFE MODE = no-tools text-only execution
          + untrusted task confined to the DATA role inside a fixed system prompt
          + minimal context fed to the agent
          + hard client-side budget/loop/output/time guards
          + pre-publish output guard (secrets / paths / policy)
          + signed AI-labeled provenance on every result
```

Concretely, for the Claude Code execution path:

```
  potluck run \
    --allowedTools ""      # NO tools: no Bash, Read/Write, WebFetch, MCP
    --max-turns 1          # one model turn — no agentic looping
    --budget <local-cap>   # hard token cap; task's declared budget is advisory only
    # task text injected as DATA inside a fixed, project-controlled system prompt
    # output guard runs before any upload; failing the guard blocks publication
```

**What SAFE MODE guarantees:** the agent cannot touch your filesystem, your credentials, or the network; it cannot loop or run unbounded; and a result that looks like it leaked a secret is blocked before it becomes public.

**What SAFE MODE does NOT guarantee:** that the *content* is correct (it's `unverified`), that a jailbreak can never produce policy-violating text under your account (the harm is yours — §4.2), or that a novel secret can never slip past the heuristic guard (§4.1). SAFE MODE protects your *machine and keys*; it does not fully protect your account's *reputation* or the artifact's *quality*.

### Image inputs (v1)

v1 allows **image inputs**: a task may attach image URLs that the runner passes to a vision-capable model to describe or analyze. This stays inside safe mode — describing an image needs no tools. The one new residual is **prompt-injection-via-image** (instructions hidden inside a picture), handled by the same defenses as text injection: the image/task is treated as data, the agent has no tools to act on any injected instruction, and the output guard runs before publication. Output is always text.

### Task moderation & trust levels in v1 — stated plainly

The research recommends moderation of submitted tasks and graduated trust levels as input-side defenses. **In v1 these are intentionally thin:**

- **Tasks are maintainer-authored.** v1 does not accept task submissions from arbitrary strangers; a maintainer hand-writes the atomic tasks (with acceptance criteria). This *is* the input-side moderation in v1 — the injection vector of "anyone can submit a task that fans out to the whole network" does not exist yet.
- **Trust levels are reserved, not active.** The data model reserves `reputation`, `trust_level`, and `validated_streak` columns (unused in v1). Real trust levels — small daily claim limits for new accounts, no task-creation rights until validated work accumulates, graduated flag→warn→throttle→ban moderation — are the **first thing added when the network opens to strangers** (§9, Phase 3).

If you are evaluating whether to contribute *today*: you are joining a small, trusted, maintainer-curated queue, not an open marketplace. The honest reason the thin defenses are acceptable is that the contributor set is small and known.

---

## 6. The data model's security surface (why RLS is the whole game)

The static site ships a **public anon key** in client code. That is safe **only if every exposed table has correct RLS policies.** One table with RLS off, or one over-permissive policy, leaks data or allows public writes — a well-documented Supabase failure mode, and the single platform-killing bug for this architecture.

Policy shape (v1, three tables):

```
  contributors   anon SELECT of handle/display_name (attribution/leaderboards)
                 self-only UPDATE
  subtasks       anon SELECT (public board)
                 status flips ONLY via claim_subtask() RPC + maintainer
                 NO broad client UPDATE
  results        anon SELECT (public commons)
                 INSERT only WHERE contributor_id = auth.uid()
                              AND a matching active lease exists
                              AND output_guard_passed = true
```

The atomic claim primitive prevents two contributors from racing on the same task and self-heals crashed leases:

```
  claim_subtask(p_topics) — SECURITY DEFINER
    SELECT ... FROM subtasks
      WHERE status='open' OR (status='leased' AND lease_expires_at < now())
      ORDER BY created_at
      FOR UPDATE SKIP LOCKED       -- atomic, non-colliding claim
      LIMIT 1;
    -- then mark leased, set leased_by = auth.uid(), lease_expires_at = now()+15min
```

**Mandatory pre-launch gate:** before any demo, run the PostgREST API **as the anon role** and confirm anon **cannot** write results or mutate subtask status. This is the #1 security-review item. RLS is not one control among many here — it is the entire security model for a keyless static frontend.

---

## 7. Provenance ≠ correctness (read this twice)

Every artifact carries a signed manifest: model ID, contributor ID, UTC timestamp, prompt hash, token count. This is genuinely useful — for attribution, audit, dispute resolution, and (later) reputation. But be precise about what it proves:

```
  PROVENANCE PROVES        PROVENANCE DOES NOT PROVE
  -----------------------  ---------------------------------------------
  who submitted it         that the content is true
  which model was claimed   that the claimed model really produced it
  when it was produced      that the work was actually done as described
```

A contributor can sign a fabricated result. Signing supports auditing and attribution; it provides **zero** correctness guarantee on its own. Correctness comes only from the deferred machinery (consensus, gold tasks, challenge windows). Until that ships, the `unverified` label is the truth.

---

## 8. Risk register (summary)

| # | Threat | Severity in v1 | Borne by | v1 status |
|---|--------|----------------|----------|-----------|
| 1 | RLS misconfiguration (anon write/leak) | Platform-killing | Project / commons | Mitigated: RLS-on-all-tables + RPC-only transitions + mandatory anon test |
| 2 | Credential pooling/routing | Platform-killing if built | Project | Eliminated by design: no place to put a key; permanently out of scope |
| 3 | Prompt-injection → host compromise | None reachable | — | Eliminated by no-tools scope |
| 4 | Exfiltration-via-output | Medium | Contributor + commons | Partial: DATA-role wrapping, minimal context, output guard (heuristic) |
| 5 | Content harm under contributor's key | Medium | Contributor | Partial: anti-injection prompt + output guard; ToS risk surfaced to you |
| 6 | Subscription "individual use" drift | Medium | Contributor | Mitigated: API-key path default; per-contributor volume cap |
| 7 | Misinformation / low-quality result | Medium–High (task-dependent) | Commons | Partial: provenance + machine-checkable acceptance criteria; `unverified` label. Consensus deferred |
| 8 | Cost-griefing (denial of wallet) | Medium | Contributor | Mitigated client-side: hard budget, --max-turns 1, debounce, caps, timeout |
| 9 | Sybil / spam / corpus poisoning | Medium | Commons | Thin: GitHub OAuth + RLS insert-own-only + small trusted set. Trust levels deferred |
| 10 | Free-tier DB DoS | Low–Medium | Infra | Thin: RLS write policies + small set. Per-identity rate limits deferred |
| 11 | Copyright / privacy on derived artifacts | Low–Medium | Contributor + commons | Mitigated: fair-use scoping + attestation + AI-origin label; heuristic leak guard |

"Mitigated" means the v1 control substantially addresses it. "Partial"/"Thin" means real residual risk remains and the stronger defense is deferred — see §9.

---

## 9. What is explicitly deferred (and the gate for each)

Honesty requires naming what v1 does *not* protect, and when it will. None of these are built in v1; the data model reserves nullable columns / unused enum values so each bolts on as a new code path over existing columns, never a table reshape.

| Deferred defense | Protects against | Gate / phase |
|------------------|------------------|--------------|
| Trust levels (small new-account limits; no task creation until validated) | Sybil/spam; new-submitter task injection | Before opening to strangers (Phase 3) |
| Moderation / auto-screening of submitted tasks | Malicious task text fanning out to the network | Before opening to strangers (Phase 3) |
| Proof-of-work-lite + per-identity rate limits | Sybil bursts; free-tier DoS | Before opening to strangers (Phase 3) |
| Optimistic challenge windows + gold/honeypot tasks | Low-effort/wrong results (Tier 0) | Phase 3 |
| N-of-M consensus on **structured** outputs, with enforced model/contributor diversity | Misinformation on factual tasks (Tier 1) | Phase 4 |
| LLM-as-judge as its own BYO task by a different model | High-visibility digests (Tier 2) | Phase 4 |
| Adaptive replication via `validated_streak` (spot-checks that never fully stop) | Cost of redundancy vs. small-budget promise | Phase 4 |
| Resume-by-another-contributor via reserved `checkpoint` column | Wasted near-ceiling runs | Phase 4 |

Two notes kept deliberately honest:

- **Consensus on free text is hard.** Two honest LLM runs differ in wording, so raw-text comparison spuriously "disagrees" — consensus must run on normalized/structured outputs (claims, citation URLs, key fields). LLM-judge self-consistency plateaus around 0.74–0.76 AUROC, and *same-model* agreement is weak evidence (correlated hallucinations). That is why consensus is deferred rather than half-built, and why model/contributor diversity is enforced when it does ship.
- **Verification will itself be a BYO task.** When consensus/judging ships, the heavy compute runs on contributors' machines as first-class verification tasks; the central API only does cheap arithmetic (tally votes, compare structured fields). This keeps the "one DB + thin API, all heavy compute on contributors" invariant intact.

---

## 10. The coding-task gate (published now, NOT relied on in v1)

v1 has no shell/code/tool execution, so it sidesteps OS-level sandboxing entirely. Coding tasks are a *separate, much later track*. Before any shell/code task ships, the runner **must** enforce all of the following — this is the published bar, documented now so it cannot be skipped later:

```
  [ ] Containerized isolation: gVisor (syscall interception) or microVM (Firecracker)
      — stronger than a plain container.
  [ ] Default-DENY egress with a narrow allowlist. Be aware hostname-only allowlists
      are bypassable (domain fronting; no TLS inspection) — a real guarantee needs a
      TLS-terminating proxy.
  [ ] Read-only root filesystem + ephemeral writable tmpfs.
  [ ] Non-root execution.
  [ ] No host credential mounts: scrub env; deny-read ~/.aws, ~/.ssh, ~/.gcloud.
  [ ] CPU / memory / time / process caps (stop crypto-mining and fork bombs).
  [ ] One-shot ephemeral throwaway sandboxes.
  [ ] HARD-FAIL CLOSED if the sandbox is unavailable — never warn-and-continue,
      never silent fallback to unsandboxed execution.
```

The last point matters: some agent sandboxes are off by default and can silently fall back to unsandboxed execution if dependencies are missing. Any future Potluck coding runner must fail closed, not fall back. **v1 relies on none of this** — it achieves safety by using zero tools.

---

## 11. How to report a problem

- **Found an RLS hole, an injection bypass, or a way to leak a secret into an artifact?** This is the highest-priority class. Open a security report (see SECURITY.md) — do not post a public PoC against the live commons.
- **Saw a junk, harmful, or infringing artifact?** Use the report/takedown path. v1 moderation is graduated and community-driven; flagging is cheap.
- **Worried a task tried to grief your tokens or jailbreak your agent?** Capture the task ID and the run; that is exactly the signal that tunes the budget guards and (later) the task-moderation screen.

If anything in this document overstates a guarantee, that is a bug in the document — tell us, and we will make it more honest. The goal of this file is not to reassure; it is to let you decide accurately.
