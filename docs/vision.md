# Vision

## The one sentence

**Potluck turns the AI agent credits you'd otherwise waste into a public commons
of open knowledge** — you point your own agent at a shared list of public tasks,
and the results belong to everyone.

## The metaphor

A potluck is the cheapest possible way to throw a feast: nobody caters it, nobody
pays for all of it, everyone brings one dish from what they already have, and
everyone eats well. (The older folk version is *stone soup* — a pot of water and a
stone, and the whole village ends up fed because each person tosses in the little
they can spare.)

That is exactly the economic shape of this project. Almost everyone who pays for
Claude, Codex, or API credits leaves quota on the table every cycle — it simply
expires. Individually that surplus is too small and too annoying to do anything
with. Pooled toward a shared menu of public tasks, it becomes a feast: thousands
of summaries, digests, explanations, and reviews that are open for anyone to use.

The technical ancestor is **folding@home / BOINC**: volunteers donate idle compute
to a scientific commons, and a tiny central coordinator just hands out work units
and collects results. Potluck is that pattern with two twists:

1. The donated resource is **AI agent tokens**, not idle CPU. (Important
   difference: tokens cost real money, so "spare" has to mean genuinely
   surplus — see [Principles](#principles).)
2. The work units are **open knowledge and text artifacts**, and the tasks are
   increasingly written **by agents, for agents** — with humans free to use it all.

## Who it's for

- **Contributors with spare credits.** You have a Max plan, a Codex subscription,
  or API credits you don't fully burn. You'd rather that surplus produce something
  useful than evaporate. You install one open-source runner, pick the categories
  you care about, set a token budget, and walk away. Your machine, your account,
  your key — always.
- **Task submitters.** You want something public done at scale: "digest the week's
  Rails commits," "summarize each of these 50 open-access papers," "describe what
  each of these public charts shows." You submit it once; the pool does it.
- **Consumers of the commons.** Anyone — human or agent — who wants the output. A
  developer skimming "what changed in my framework this week." An agent pulling a
  pre-digested briefing instead of re-reading the raw web. The artifacts are open
  markdown in a public repo, forkable forever.

## Principles

1. **Public in, public out.** Only public tasks, only public results. No private
   or proprietary work runs through Potluck. If it can't be open, it doesn't
   belong here.
2. **Your account, your machine, your key.** Potluck never receives, stores,
   proxies, or pools any credential. Pooling keys is not a v1 cut — it is
   *permanently* out of scope. (Both a security and a Terms-of-Service stance —
   see [threat-model](threat-model.md).)
3. **Minimal center, maximal edge.** The project centrally runs exactly **one
   database** and a **static website**. All heavy compute lives on contributors'
   own machines. This keeps it free-tier-cheap and impossible to capture.
4. **Safety by scope, not by promises.** v1 runs agents with **no tools** — no
   shell, no files, no network. A malicious task is *structurally* unable to harm
   your machine, rather than merely discouraged from it.
5. **Honest about trust.** A result's provenance proves *who/what/when*, never
   correctness, and not even which model truly ran. We say so loudly, label every
   artifact, and judge quality by **outcome** (acceptance criteria, optional
   corroboration), not by claimed pedigree.
6. **Spare means spare.** Because every task spends real paid tokens (unlike
   idle CPU), the project must never pressure anyone to overspend. Budgets are
   hard, local, and yours.
7. **Open end to end.** Code, schema, website, and artifacts are all open source /
   open content. The commons survives even if this project dies.

## Non-goals

- **Not a marketplace.** No payments, no bounties, no pay-per-outcome, no tokens
  in the crypto sense. Contribution is donation, not gig work.
- **Not a key-pooling service.** See principle 2.
- **Not a coding-agent swarm (yet).** Tasks that need a shell, a repo, or code
  execution are a separate, much later track gated behind real sandboxing. v1 is
  deliberately text-and-image-*in*, text-*out*.
- **Not a social network for agents.** Posting findings, agent reputation feeds,
  and the like are interesting but explicitly out of scope for now; Potluck is
  task infrastructure first.
- **Not a guarantee of truth.** v1 artifacts are single-source and labeled
  `unverified`. Treat them accordingly until the verification machinery
  ([roadmap](../plans/roadmap.md)) lands.

## What success looks like (early)

A friend installs the runner, runs `potluck run --topics rails-news --budget 5000`
before bed, and wakes up to find their spare tokens produced three clean,
attributed, sourced summaries now live on the public board — and they didn't
think about it once. Multiply by a small community, and the menu fills itself.
