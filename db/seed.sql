-- Potluck starter tasks — maintainer-authored, self-contained, no-tools-safe.
-- Apply as the maintainer (Supabase SQL editor / service role / Management query API).
-- These need no web/tools to complete, so they're ideal for testing the v0 runner.

insert into subtasks (category_slug, title, prompt, acceptance, token_budget, model_policy, requested_model) values
('rails', 'Explain the N+1 query problem in Rails',
  $$Explain the N+1 query problem in Ruby on Rails to a junior developer. Cover: what it is, a minimal ActiveRecord example that triggers it, and how includes/eager loading fixes it. Use only general knowledge; do not invent gem names or APIs.$$,
  $$<= 250 words. Includes a before/after ActiveRecord snippet. Names eager loading as the fix.$$,
  4000, 'any', null),

('postgres', 'Explain Row-Level Security in Postgres with one example policy',
  $$Explain what Row-Level Security (RLS) is in PostgreSQL and why it matters for apps that expose the database directly to clients. Include one concrete CREATE POLICY example (e.g. users can read only their own rows).$$,
  $$<= 200 words. Includes one valid CREATE POLICY statement. Mentions RLS must be enabled per table.$$,
  4000, 'any', null),

('writing', 'One-paragraph plain-language summary: what is folding@home',
  $$In one paragraph, explain folding@home to a non-technical reader: what it does, how volunteers help, and why distributing the work matters. General knowledge only.$$,
  $$<= 120 words. No unexplained jargon. Mentions volunteer/distributed compute.$$,
  3000, 'any', null),

('ml-papers', 'Define "attention" in transformers for a web developer',
  $$Explain the concept of "attention" in transformer models to an experienced web developer with no ML background. Use an analogy and avoid math beyond a high-level description.$$,
  $$<= 220 words. Includes one analogy. No equations.$$,
  4500, 'min', 'frontier');
