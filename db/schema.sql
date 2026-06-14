-- ============================================================================
-- Potluck v1 schema  (Supabase / PostgreSQL)
-- ============================================================================
-- The database is the ENTIRE central footprint: a queue + index + provenance
-- store. It does no heavy compute. Task artifacts (markdown) live in a public
-- Git repo; this DB stores only pointers + metadata.
--
-- RLS is the ENTIRE security model. The static frontend ships a public anon
-- key, so:
--   * every table has RLS ON from creation;
--   * privileged state transitions happen ONLY through SECURITY DEFINER RPCs,
--     never through broad client UPDATE/INSERT grants;
--   * before any launch, run the PostgREST API AS THE ANON ROLE and confirm
--     anon cannot write results or mutate subtask status. (See docs/threat-model.md)
--
-- Apply with the Supabase SQL editor, or: supabase db push  (see .env.example)
-- ============================================================================

create extension if not exists "pgcrypto";   -- gen_random_uuid()

-- ── contributors ────────────────────────────────────────────────────────────
-- One row per person who has logged in. id = auth.uid() (Supabase Auth / GitHub
-- OAuth), so identity, attribution, and RLS ownership are the same id.
create table if not exists contributors (
  id               uuid primary key,                 -- = auth.uid()
  github_handle    text unique,                       -- attribution + weak sybil signal
  display_name     text,
  created_at       timestamptz not null default now(),
  -- reserved (v2 — present now so later machinery never reshapes rows)
  reputation       int  not null default 0,
  trust_level      int  not null default 0,
  validated_streak int  not null default 0
);

-- ── subtasks  (THE QUEUE + THE INDEX) ───────────────────────────────────────
-- One row = one atomic work unit a contributor claims and completes.
create table if not exists subtasks (
  id               uuid primary key default gen_random_uuid(),
  category_slug    text,                              -- matchmaking + filtering (--topics)
  title            text not null,                     -- short label for the board
  prompt           text not null,                     -- SELF-CONTAINED untrusted task text (treated as DATA)
  acceptance       text,                              -- machine-checkable done-criteria (v1 quality lever)
  attachments      jsonb,                             -- optional image input URLs etc. (v1 = image INPUTS only)
  token_budget     int  not null default 5000,        -- ADVISORY cap; the runner enforces the real cap locally
  -- model request (advisory; the actually-used model is self-reported on results)
  requested_model  text,                              -- e.g. 'claude-sonnet-4' or a tier like 'frontier'
  model_policy     text not null default 'any'
                   check (model_policy in ('any','min','exact')),
  status           text not null default 'open'
                   check (status in ('open','leased','done','failed')),
  leased_by        uuid references contributors(id),
  lease_expires_at timestamptz,
  created_at       timestamptz not null default now(),
  -- reserved (v2)
  consensus_group  uuid,                              -- future N-of-M grouping / fan-out parent
  harm_tier        int,                               -- 0 low / 1 factual / 2 high-visibility
  checkpoint       text                               -- future resume-by-another-contributor payload
);
create index if not exists subtasks_status_idx   on subtasks(status);
create index if not exists subtasks_category_idx on subtasks(category_slug);

-- ── results  (metadata + provenance; markdown BODY lives in Git) ─────────────
create table if not exists results (
  id                   uuid primary key default gen_random_uuid(),
  subtask_id           uuid not null references subtasks(id),
  contributor_id       uuid not null references contributors(id),
  artifact_md          text not null,                 -- produced markdown (mirrored to Git, then prunable)
  -- PROVENANCE: who / what / when — NOT correctness, and NOT a proof of the model.
  reported_model       text not null,                 -- SELF-DECLARED by the runner; not cryptographically verified
  self_described_model text,                          -- optional: what the model said when asked (weak anomaly signal)
  token_count          int,
  prompt_hash          text,
  output_guard_passed  boolean not null default true, -- client-side pre-publish secret/policy scan
  created_at           timestamptz not null default now(),
  repo_path            text,                           -- set by publisher Action: results/<id>.md
  -- reserved (v2)
  verification_status  text not null default 'unverified'
                       check (verification_status in ('unverified','consensus','confirmed')),
  structured_output    jsonb,                          -- normalized claims + citations for consensus
  commit_sha           text,
  permalink            text
);
create index if not exists results_subtask_idx on results(subtask_id);

-- ============================================================================
-- Row-Level Security
-- ============================================================================
alter table contributors enable row level security;
alter table subtasks     enable row level security;
alter table results      enable row level security;

-- contributors: public read (attribution/leaderboards); write only your own row
create policy "contributors public read"  on contributors for select using (true);
create policy "contributors insert self"  on contributors for insert with check (id = auth.uid());
create policy "contributors update self"  on contributors for update using (id = auth.uid());

-- subtasks: public read only. NO client writes — status flips via RPC; task
-- creation is maintainer-only (service role) in v1.
create policy "subtasks public read" on subtasks for select using (true);

-- results: public read. Direct INSERT is allowed ONLY for your own row, while
-- you hold an active lease, and only if the local output guard passed. (The
-- blessed path is submit_result() below; this policy is defense-in-depth.)
create policy "results public read" on results for select using (true);
create policy "results insert own with active lease" on results for insert
  with check (
    contributor_id = auth.uid()
    and output_guard_passed = true
    and exists (
      select 1 from subtasks s
      where s.id = subtask_id
        and s.leased_by = auth.uid()
        and s.status = 'leased'
        and s.lease_expires_at > now()
    )
  );

-- ============================================================================
-- RPCs  (the only privileged state transitions)
-- ============================================================================

-- claim_subtask: atomically lease the next matching open (or expired-lease) task.
-- FOR UPDATE SKIP LOCKED => two concurrent claimers never collide. The
-- expired-lease branch self-heals crashed contributors with no background worker.
create or replace function claim_subtask(p_topics text[] default null,
                                         p_lease_minutes int default 15)
returns subtasks
language plpgsql security definer set search_path = public as $$
declare picked subtasks;
begin
  select * into picked from subtasks s
   where (s.status = 'open' or (s.status = 'leased' and s.lease_expires_at < now()))
     and (p_topics is null or s.category_slug = any(p_topics))
   order by s.created_at
   for update skip locked
   limit 1;

  if not found then
    return null;
  end if;

  update subtasks
     set status = 'leased',
         leased_by = auth.uid(),
         lease_expires_at = now() + make_interval(mins => p_lease_minutes)
   where id = picked.id
   returning * into picked;

  return picked;
end;
$$;

-- submit_result: write the result and flip the subtask to 'done' in one step.
-- Requires an active lease held by the caller and a passing output guard.
create or replace function submit_result(p_subtask_id uuid,
                                         p_artifact_md text,
                                         p_reported_model text,
                                         p_self_described_model text default null,
                                         p_token_count int default null,
                                         p_prompt_hash text default null,
                                         p_output_guard_passed boolean default true)
returns results
language plpgsql security definer set search_path = public as $$
declare r results;
begin
  if not exists (
    select 1 from subtasks s
     where s.id = p_subtask_id
       and s.leased_by = auth.uid()
       and s.status = 'leased'
       and s.lease_expires_at > now()
  ) then
    raise exception 'no active lease for this subtask';
  end if;

  if p_output_guard_passed is not true then
    raise exception 'output guard did not pass; refusing to publish';
  end if;

  insert into results (subtask_id, contributor_id, artifact_md, reported_model,
                       self_described_model, token_count, prompt_hash, output_guard_passed)
  values (p_subtask_id, auth.uid(), p_artifact_md, p_reported_model,
          p_self_described_model, p_token_count, p_prompt_hash, p_output_guard_passed)
  returning * into r;

  update subtasks set status = 'done' where id = p_subtask_id;
  return r;
end;
$$;

-- release_lease: give up a task (over budget / failed / incomplete). v1 policy is
-- to DISCARD partial work and return the task to the pool for a fresh retry.
create or replace function release_lease(p_subtask_id uuid, p_failed boolean default false)
returns void
language plpgsql security definer set search_path = public as $$
begin
  update subtasks
     set status = case when p_failed then 'failed' else 'open' end,
         leased_by = null,
         lease_expires_at = null
   where id = p_subtask_id and leased_by = auth.uid();
end;
$$;

grant execute on function claim_subtask(text[], int)                              to authenticated;
grant execute on function submit_result(uuid, text, text, text, int, text, boolean) to authenticated;
grant execute on function release_lease(uuid, boolean)                            to authenticated;

-- ============================================================================
-- Least-privilege table grants (defense in depth)
-- ============================================================================
-- Supabase grants anon/authenticated broad privileges by default, leaving RLS as
-- the only gate. We additionally REVOKE what we never want, so safety does not
-- depend on the mere ABSENCE of a policy. Writes to subtasks/results happen ONLY
-- through the SECURITY DEFINER RPCs above (which run as the table owner).
revoke all on contributors, subtasks, results from anon, authenticated;

-- anon = read-only public access (SELECT still passes through RLS).
grant select on contributors, subtasks, results to anon;

-- authenticated = read all; create/own a contributor row; everything else via RPC.
grant select on contributors, subtasks, results to authenticated;
grant insert, update on contributors to authenticated;

-- ── seed categories are just slugs on subtasks; no table needed in v1. ───────
-- Example maintainer-authored task (run as service role, not via client):
-- insert into subtasks (category_slug, title, prompt, acceptance, token_budget)
-- values ('rails-news', 'Summarize Rails main changes for the week of 2026-06-08',
--         'Using only the linked release notes/changelog text provided below, ...',
--         'Every change has a one-line summary and a source URL. <= 300 words.', 5000);
