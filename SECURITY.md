# Security Policy

Potluck runs community-authored tasks through contributors' own AI accounts, and a
static site talks to the database with a public key. That makes a few bug classes
unusually high-impact. Please report them privately.

## Report privately

For anything exploitable, **do not open a public issue or post a PoC against the
live commons.** Instead, email the maintainer (see the GitHub profile) or use
GitHub's private "Report a vulnerability" advisory flow on the repo.

## Highest-priority classes

| Class | Why it's critical |
|---|---|
| **RLS bypass** | The anon key ships in the website. Any way for anon (or a non-leasing authenticated user) to write `results`, mutate `subtasks.status`, or read something that should be private is platform-killing. |
| **Prompt-injection → artifact leak** | A task that makes the agent dump local/secret context into a public artifact, or bypass the output guard. |
| **Credential exposure** | Any path where the runner could transmit, log, or persist a provider key/token off the contributor's machine. There should be none by design — report any. |
| **Denial-of-wallet** | A task or runner path that spends past the contributor's local budget cap. |

## What's in scope

- The schema/RLS in `db/schema.sql`.
- The runner's safe-mode and budget enforcement (once code lands).
- The output guard.
- The static site's handling of the anon key.

## What's known / out of scope (v1)

These are documented limitations, not vulnerabilities, in v1:

- **Model is self-reported**, not cryptographically verified (see
  [open-questions](plans/open-questions.md)).
- **Results are `unverified`**, single-source.
- **An authenticated contributor can submit junk** — v1 relies on a small trusted
  set; trust levels are deferred (see [threat-model](docs/threat-model.md)).

If you're unsure whether something is in scope, report it anyway.
