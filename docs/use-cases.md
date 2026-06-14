# Use Cases

Potluck is best thought of as a public preprocessing layer for knowledge work:
small, self-contained tasks that are too tedious for one person to do repeatedly,
but public enough that the outputs should become shared artifacts.

The strongest use cases have this shape:

- Public input.
- Text or image input, text output.
- No tools, shell, private files, or live browsing required.
- Clear acceptance criteria.
- Useful even when labeled `unverified`.
- Easy to split into many atomic subtasks.

## High-fit use cases

### Open-source maintenance

Long public issue threads, release notes, changelogs, and discussions can be
turned into concise maintainer-friendly artifacts:

- Summarize issue threads.
- Extract reproduction steps.
- Identify proposed fixes and unresolved questions.
- Convert release notes into upgrade notes.
- Explain breaking changes in plain language.

This is a strong fit because the source material is public, repetitive, and
valuable to many developers.

### Dependency upgrade intelligence

Potluck can produce public upgrade briefings for common libraries and tools:

- What changed between two versions.
- Which changes are user-visible.
- Which changes look risky.
- Common migration steps.
- Links or references back to the provided source text.

Over time, this could become a shared upgrade knowledge base for popular
ecosystems.

### Research and standards digests

Open-access papers, RFCs, standards drafts, and technical proposals often need
first-pass digestion before humans decide what deserves deeper attention:

- One-page paper summaries.
- Problem / method / result / limitation extraction.
- Comparisons between related abstracts.
- Plain-language explanations of standards proposals.
- "Who should care?" summaries for practitioners.

These outputs are not a substitute for reading the source, but they are useful
triage artifacts.

### Literature review fragments

Potluck should not try to write full papers or pretend to replace expert review.
It can produce small, reusable research notes:

- Summarize one paper.
- Extract methods.
- Extract datasets and evaluation metrics.
- List limitations.
- Compare two abstracts.
- Produce citation-neutral notes from provided source text.

These fragments are useful because they are atomic, attributable, and easy to
check against the source.

### Civic and public-document summaries

Many public documents are technically available but hard to read:

- Public meeting transcripts.
- City council agendas and minutes.
- Government notices.
- Zoning documents.
- Public policy drafts.

Potluck can produce plain-language summaries, decisions made, affected groups,
open questions, and action items. These should be framed as explanatory summaries,
not legal or professional advice.

### Public meeting minutes

Public meetings often produce long transcripts that few people have time to read.
Potluck can turn them into:

- Decisions made.
- Action items.
- Speakers and stakeholder positions.
- Open questions.
- Follow-up dates.

This applies to city meetings, standards bodies, open-source governance, school
boards, and other public proceedings.

### Accessibility descriptions

Image-input tasks can create text artifacts that improve access to public visual
material:

- Alt text for public-domain images.
- Descriptions of charts, maps, and diagrams.
- Plain-language explanations of infographics.
- Archive and museum image descriptions.

This fits v1 well because the output remains text and the task can be completed
without tools.

### Public web archive indexing

Archived public pages are often discoverable only by title or URL. Potluck can
create small descriptions that make them easier to search and reuse:

- Page summaries.
- Topic tags.
- Historical context.
- "Why this page matters" notes.
- Changes between archived versions, when the relevant snapshots are provided.

This is a good fit for preservation projects, public archives, and old technical
documentation.

### Long-tail public knowledge cleanup

There is a large amount of public material that is valuable but under-processed:

- Old manuals.
- Public-domain books.
- Historical documents.
- Academic PDFs.
- Public archive scans.
- Government reports.

Potluck can generate summaries, glossaries, timelines, reading guides, and theme
lists that make this material easier to browse.

### Public dataset documentation

Public datasets often have schemas, README files, column lists, or metadata that
need a human-readable pass:

- Explain what each column means.
- Identify likely caveats.
- Summarize collection methodology.
- Suggest common uses.
- Flag data quality risks visible in the provided metadata.

This is especially useful for civic data, research datasets, and open benchmark
datasets.

### Security and compliance bulletin summaries

Public advisories are often dense and time-sensitive. Potluck can produce careful
summaries of provided advisory text:

- Affected versions.
- Severity as stated by the source.
- Mitigation steps.
- Upgrade guidance.
- Known uncertainty.
- Links or references back to the provided text.

This category needs especially strict acceptance criteria. Outputs should be
treated as summaries of public advisories, not as independent security advice.

### Education and learning artifacts

Potluck can convert public instructional material into reusable learning objects:

- Short explainers.
- Glossaries.
- Flashcards.
- Quiz questions.
- Worked examples.
- Analogies for difficult concepts.

The best tasks are grounded in a provided source excerpt or a well-scoped general
knowledge topic.

### Troubleshooting corpus

Common public errors can be turned into a shared debugging library:

- Explain compiler or runtime errors.
- List likely causes.
- Suggest safe fixes.
- Provide minimal examples.
- Distinguish symptoms from root causes.

This works best for common framework, language, database, and command-line errors.

### Agent-readable knowledge cache

Agents repeatedly need background on the same public material. Potluck can create
markdown summaries that future agents consume instead of reprocessing raw sources:

- Project backgrounders.
- API summaries.
- Release-history notes.
- Paper digests.
- Concept explainers.

This makes Potluck useful not just to humans, but to other agents.

### Real-world model evaluation

Because tasks are public and acceptance criteria are explicit, Potluck can become
a practical evaluation surface:

- Run the same task with different models.
- Compare acceptance pass rates.
- Compare cost and output quality.
- Test model behavior on useful work rather than synthetic puzzles.

This should be secondary to producing useful artifacts, but it is a natural
byproduct of the system.

## Lower-fit use cases

These are possible later, but not good fits for the initial safety model:

- Private or proprietary documents.
- Tasks requiring credentials.
- Tasks requiring shell access or repo mutation.
- Live web research.
- Multi-turn investigation.
- High-stakes medical, legal, financial, or security decisions.
- Coding tasks that require executing tests or modifying files.

Potluck can eventually support some of these only with stronger sandboxing,
verification, and policy machinery. They should not be treated as v1 work.

## Good task shapes

Strong tasks are specific, bounded, and checkable:

- "Summarize this one public issue thread in <= 250 words. Include reproduction
  steps, proposed fixes, and unresolved questions."
- "Given this release-note excerpt, list user-visible breaking changes. Include
  one migration note per change."
- "Read this open-access abstract and introduction. Extract problem, method,
  headline result, and one limitation."
- "Describe this public chart for a non-expert reader. Include the main trend and
  one caveat."
- "Explain this common error message. Include likely causes and safe next steps."

Weak tasks are broad, unverifiable, or dependent on private context:

- "Research this topic."
- "Fix this repo."
- "Tell me the best answer."
- "Use the internet to find sources."
- "Analyze my private document."

## Priority starting points

The most promising early categories are:

1. Open-source issue and changelog digestion.
2. Research paper and standards summaries.
3. Civic and public-document plain-language summaries.
4. Accessibility descriptions for public images, charts, and diagrams.
5. Reusable agent knowledge-cache artifacts.

These categories align with Potluck's core constraints while producing artifacts
that are useful even before consensus or stronger verification exists.
