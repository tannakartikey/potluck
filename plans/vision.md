# Where Potluck is going — vision & security trajectory

This document is the **line of sight**: where the project goes after v0, and — more importantly —
the *security and trust machinery that has to grow with it*. It is deliberately separate from
`roadmap.md` (the concrete phased build plan). Nothing here is built yet unless it says so; this is
the thinking, so we never wander past what our defenses can carry.

If you read one thing, read the thesis.

---

## The thesis: blast radius determines required trust

Every expansion of what Potluck *does* expands **who can be hurt if a task is bad**. The trust
machinery must scale in lockstep — never lag it. Concretely:

| Scope | What a malicious/poisoned task can reach | Who is the load-bearing control |
|---|---|---|
| **Text-only, no tools (v0)** | The runner's own machine (contained) + a public markdown note nobody must act on | **The sandbox.** Safe-mode + container. Moderation is just quality. |
| Open submissions at scale | The shared queue + the commons' reputation | Trust levels + N-of-M moderation + rate limits |
| Tool-enabled / coding tasks | The runner's machine, for real (files, network) | A hardened sandbox gate (egress allowlist, stronger isolation) |
| Writing / triaging real OSS repos | **Third-party projects and everyone downstream** (supply-chain) | Human-merge-only + N-of-M + provenance + harm-tier |
| Private federated networks | Whatever the partners agree to expose | Contractual + encryption + access control (a different model) |

**v0 sits at the safe end on purpose.** We launch with **non-harmful, text-only, no-tools tasks**,
because at that scope the sandbox alone is a sufficient guarantee and the rest is defense-in-depth.
Everything below is the staged path to relax that — each stage gated on the matching controls
existing first.

---

## Guiding principles (these don't change)

1. **The sandbox is the load-bearing safety control — not moderation, not attestation.** A wrongly
   approved task still only runs text-only, no-tools, in a locked container. Approval grants **no
   capability**. Keep it that way: never let any higher layer become the thing that makes execution
   safe. (See open-questions #27.)
2. **Never trust the client; enforce on the server.** All privileged transitions are key-gated
   `SECURITY DEFINER` RPCs. A modified client or a hand-rolled `curl` can only do what the RPC
   allows. The database is the trust boundary.
3. **Trust identities, not binaries.** You cannot attest an open-source binary on hardware you don't
   own (proven — see #28). You *can* vet a *person/key* and enforce it in the RPC. (Shipped: the
   trusted-moderator allowlist.)
4. **Public commons is the mission.** Public tasks in, open attributed artifacts out. Private/closed
   variants (federation, #29) are an explicit, labeled departure — not the default.
5. **Scope relaxes only behind its gate.** Tools/coding, third-party write-back, and federation each
   wait for their named controls. No "convenience" shortcut past a gate.

---

## The scope-and-trust ladder

### Stage 0 — v0 launch (where we are)
- **Tasks:** non-harmful, text/knowledge, image *inputs* allowed, text output, **no tools**.
- **Safety:** hard safe-mode + container isolation; anti-injection preamble; output guard.
- **Submission:** anyone with a key can `submit_task` (lands `pending`, DB-guarded for
  format/rate/dedupe); **AI moderation by a *trusted* contributor** (`trust_level >= 1`) flips it to
  `open`. Single-moderator screening among a small, vetted set.
- **Trust:** curated allowlist — admins (`trust_level >= 2`) grant moderator trust via `grant_trust`;
  the root admin is bootstrapped out-of-band. Verdicts are attributable (`moderated_by`).
- **Honest residual:** a single trusted moderator can err or be injected; the contributor set is
  small and known, and the sandbox bounds the damage regardless.

### Stage 1 — open the network to strangers
- **N-of-M moderation:** K independent moderators running *diverse* models must agree to accept a
  task (reuse reserved `consensus_group`). Converts "protect the moderation prompt" (impossible)
  into "tolerate a corrupted/injected prompt via diversity + consensus" (achievable).
- **Reputation, earned not assigned:** `reputation` / `validated_streak` activate; moderator weight
  and submit rights scale with a track record; bad verdicts cost standing (skin-in-the-game without
  forcing approvers to execute).
- **`harm_tier` routing:** low-risk categories get light moderation; higher tiers demand more
  reviewers / human sign-off / are disallowed.
- **Anti-sybil:** Discourse/OSM-style trust levels (new keys get small daily limits, no task
  creation until validated), PoW-lite on `submit_task`, gold/honeypot tasks.

### Stage 2 — richer artifacts
- Image inputs (mostly here), then structured outputs (`structured_output` reserved), then
  optional consensus/verification (`verification_status`: unverified → consensus → confirmed) and a
  challenge-window + supersede flow (#24).

### Stage 3 — tool-enabled / coding tasks (behind the sandbox gate)
- This is a whole new catastrophic class (files, network, code execution). Gated on: a published,
  hardened sandbox (default-deny egress with a per-task allowlist, no host credential mounts,
  fail-closed), per-task capability scoping, and output review. The container work (#23) is the
  foundation; the *gate* is more.

### Stage 4 — writing / triaging real third-party repos (#25)
- **The blast radius leaves our walls.** A poisoned patch/triage note can flow into a widely-used
  repo and harm everyone downstream — a supply-chain attack.
- **Containment rule (hard):** *Potluck is never the actor that mutates a third-party repo.* Output
  is always a **proposal** (a diff, a draft reply) that the project's **own maintainers** review and
  merge through **their own** gates (human review + CI + branch protection). Potluck holds no project
  write credentials; no auto-merge, no PR from a Potluck-controlled account that bypasses review.
- Plus everything from Stages 1–3: N-of-M, `harm_tier` (anything touching real code is high-tier),
  full provenance, reputation-gated eligibility. This is also where **centralized/attested
  moderation** finally earns its cost (a tampered prompt here *can* cause downstream harm), if we
  ever run a small central moderation service on Potluck's own credits.

---

## The attestation question, settled (#28)

The recurring wish — "make it so only our official binary can submit, and no one can modify our
prompt" — is **cryptographically impossible** on hardware the contributor controls, open-source or
not. An adversarial design panel tried four families (embedded keys/HMAC, zkVM proofs, TEE/platform
attestation, supply-chain signing) and broke every one: with public source on an owned machine, the
binary is a client-side oracle the adversary fully controls; crypto authenticates *keys* and verifies
*statements*, it cannot attest the *identity of code* on hardware the verifier doesn't own. Any scheme
claiming otherwise is snake oil.

What the goals were really proxies for — and what we **can** deliver:

1. **Per-identity accountability** (not code-identity). Trust *keys*, enforce in the RPC. **Shipped**
   for moderation (trusted-moderator allowlist). Extensible: per-request HMAC over the canonicalized
   RPC body so every write is attributable + revocable.
2. **Supply-chain integrity (a FUTURE option).** Reproducible Go builds + signed releases
   (cosign/Sigstore, SLSA provenance) + published checksums, so *honest* users verify *their* download
   is the real artifact. Protects against a malicious mirror, **not** against the machine owner.
   *Note:* v0 deliberately chooses simplicity — install from source / bleeding-edge, no pinned releases
   (#18). Verifiable releases require pinning, so that's a future step we'd take together IF we want it,
   not a v0 requirement.
3. **Fault tolerance over secrecy.** N-of-M independent moderators with diverse models: one party can
   forge their own verdict, not a quorum of others'. The right answer to "protect the prompt."
4. **Capability-free approval + server-enforced invariants.** The structural defang: approval grants
   no capability, and the RPCs enforce every rule regardless of client.

---

## Divergent fork — private / federated networks (#29)

A deliberately-labeled departure from the public-commons mission, parked as a *possible* future:
organizations form a **private network** to share spare token capacity *with each other* — my
leftover credits run their tasks and vice-versa — where tasks and results stay **private to the
network** (potentially encrypted), rather than becoming an open commons. Same primitives (queue,
key-gated RPCs, BYO-agent), different visibility and trust model. It is **morally and architecturally
distinct** from public Potluck and would need its own design (membership, encryption-at-rest and in
transit, isolation from the public board, ToS/compliance review). Captured because the structure
generalizes; flagged because it trades away the "open" that defines the project. Details in
open-questions #29.

---

## Where this leaves v0

Launch **non-harmful, text-only, no-tools** tasks, moderated by a **small vetted set** of trusted
contributors, with the **sandbox as the guarantee**. Everything above is the mapped road out — each
stage a new code path over already-reserved columns, gated on the controls that make its larger blast
radius survivable. We ship the smallest safe loop, and we never let ambition outrun the sandbox.
