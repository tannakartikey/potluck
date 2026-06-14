-- ============================================================================
-- Potluck v1 schema  (Supabase / PostgreSQL)
-- ============================================================================
-- The database is the ENTIRE central footprint: a queue + index + provenance
-- store. It does no heavy compute. Task artifacts (markdown) live in a public
-- Git repo; this DB stores only pointers + metadata.
--
-- AUTH MODEL (v0): NO OAuth, NO Supabase Auth. Each contributor's runner
-- generates a random secret KEY on first run and registers it; we store only its
-- SHA-256 hash. Reads are public (anon key, read-only via RLS). Writes go through
-- SECURITY DEFINER RPCs that take the key, resolve the contributor by hash, and
-- act. The secret key is a bearer token (sent over TLS); a sign-with-private-key
-- scheme is a future hardening (Postgres can't cheaply verify Ed25519).
--
-- DISCOVERY: tasks carry a primary category + a tags[] array (GIN) and a
-- generated full-text `search` vector (GIN), so agents can filter by tag and
-- search free text via PostgREST (`?tags=cs.{rails}`, `?search=wfts(english).…`).
--
-- RLS is the access model for direct table reads; all writes are RPC-only.
-- Before launch, exercise the REST API AS THE ANON ROLE and confirm anon cannot
-- write (see docs/threat-model.md). Apply via Supabase SQL editor / Management API.
-- ============================================================================

create extension if not exists pgcrypto;   -- digest() for key hashing; gen_random_uuid()

-- ── contributors  (public-safe; holds NO secret) ───────────────────────────
create table if not exists contributors (
  id               uuid primary key default gen_random_uuid(),
  display_name     text,                              -- self-chosen; attribution/leaderboards
  created_at       timestamptz not null default now(),
  -- reserved (v2)
  reputation       int  not null default 0,
  trust_level      int  not null default 0,
  validated_streak int  not null default 0
);

-- ── contributor_keys  (SECRET; no anon access — only the RPCs touch it) ──────
create table if not exists contributor_keys (
  contributor_id   uuid primary key references contributors(id) on delete cascade,
  key_hash         text unique not null,              -- sha256 hex of the contributor's secret key
  created_at       timestamptz not null default now()
);

-- ── categories  (curated taxonomy; hierarchical via parent_slug) ─────────────
create table if not exists categories (
  slug         text primary key,
  label        text not null,
  description  text,
  parent_slug  text references categories(slug),
  created_at   timestamptz not null default now()
);

-- ── subtasks  (THE QUEUE + THE INDEX) ───────────────────────────────────────
create table if not exists subtasks (
  id               uuid primary key default gen_random_uuid(),
  category_slug    text,                              -- primary category (references categories.slug)
  tags             text[] not null default '{}',      -- multi-tag for discovery (GIN-indexed)
  title            text not null,
  prompt           text not null,                     -- SELF-CONTAINED untrusted task text (DATA)
  acceptance       text,                              -- machine-checkable done-criteria (v1 quality lever)
  attachments      jsonb,                             -- optional image-input URLs (v1 = image INPUTS only)
  token_budget     int  not null default 5000,        -- ADVISORY; the runner enforces the real cap
  priority         int  not null default 0,           -- higher = claimed first; system/Potluck tasks are high
  requested_model  text,                              -- e.g. 'claude-sonnet-4' or a tier like 'frontier'
  model_policy     text not null default 'any'
                   check (model_policy in ('any','min','exact')),
  status           text not null default 'open'
                   check (status in ('open','leased','done','failed','pending','rejected','needs_review')),
  leased_by        uuid references contributors(id),
  lease_expires_at timestamptz,
  created_at       timestamptz not null default now(),
  -- submission / moderation: tasks land 'pending' via submit_task, moderated to 'open'/'rejected'
  submitted_by     uuid references contributors(id),
  rejection_note   text,
  dedupe_key       text,                              -- md5(normalize(category+title+prompt)); unique
  -- generated full-text search vector over the task text + tags (GIN-indexed below)
  search           tsvector generated always as (
                     to_tsvector('english',
                       coalesce(title, '') || ' ' || coalesce(prompt, '') || ' ' ||
                       coalesce(acceptance, '') || ' ' || coalesce(category_slug, ''))
                   ) stored,
  -- reserved (v2)
  consensus_group  uuid,
  harm_tier        int,
  checkpoint       text
);
create index if not exists subtasks_status_idx   on subtasks(status);
create index if not exists subtasks_category_idx on subtasks(category_slug);
create index if not exists subtasks_tags_idx     on subtasks using gin(tags);
create index if not exists subtasks_search_idx   on subtasks using gin(search);
create index if not exists subtasks_priority_idx on subtasks(priority desc, created_at);
create unique index if not exists subtasks_dedupe_idx on subtasks(dedupe_key) where dedupe_key is not null;

-- ── results  (metadata + provenance; markdown BODY lives in Git) ─────────────
create table if not exists results (
  id                   uuid primary key default gen_random_uuid(),
  subtask_id           uuid not null references subtasks(id),
  contributor_id       uuid not null references contributors(id),
  artifact_md          text not null,
  reported_model       text not null,                 -- SELF-DECLARED; not cryptographically verified
  self_described_model text,                          -- optional weak anomaly signal
  token_count          int,
  prompt_hash          text,
  output_guard_passed  boolean not null default true,
  created_at           timestamptz not null default now(),
  repo_path            text,
  -- reserved (v2)
  verification_status  text not null default 'unverified'
                       check (verification_status in ('unverified','consensus','confirmed')),
  structured_output    jsonb,
  commit_sha           text,
  permalink            text,
  -- reserved: richer provenance (v0 leaves these null; the runner fills them later)
  usage                jsonb,         -- full token breakdown: input / output / reasoning / cache
  reasoning_path       text,          -- git pointer to the model's thinking trace, when captured + ToS-allowed
  has_reasoning        boolean not null default false
);
create index if not exists results_subtask_idx on results(subtask_id);

-- ============================================================================
-- Row-Level Security  (reads only; all writes go through the RPCs below)
-- ============================================================================
alter table contributors     enable row level security;
alter table contributor_keys enable row level security;   -- no policy, no grant => unreachable except via definer RPCs
alter table categories       enable row level security;
alter table subtasks         enable row level security;
alter table results          enable row level security;

create policy "contributors public read" on contributors for select using (true);
create policy "categories public read"   on categories   for select using (true);
create policy "subtasks public read"     on subtasks     for select using (true);
create policy "results public read"      on results      for select using (true);

-- ============================================================================
-- Auth-by-key RPCs  (the only writes)
-- ============================================================================

-- internal: resolve a contributor id from a presented secret key (never granted to anon)
create or replace function _contributor_for_key(p_key text)
returns uuid language sql security definer set search_path = public, extensions as $$
  select contributor_id from contributor_keys
   where key_hash = encode(digest(p_key, 'sha256'), 'hex');
$$;

-- register: the runner generates a random key locally and calls this once.
create or replace function register_contributor(p_key text, p_display_name text default null)
returns contributors language plpgsql security definer set search_path = public, extensions as $$
declare c contributors;
begin
  if length(coalesce(p_key,'')) < 24 then
    raise exception 'key too short (need >= 24 chars of entropy)';
  end if;
  insert into contributors (display_name) values (p_display_name) returning * into c;
  insert into contributor_keys (contributor_id, key_hash)
    values (c.id, encode(digest(p_key, 'sha256'), 'hex'));
  return c;
end;
$$;

-- claim: atomically lease the next matching task for the key's contributor.
-- p_topics matches the primary category OR any tag (array overlap).
create or replace function claim_subtask(p_key text, p_topics text[] default null,
                                         p_lease_minutes int default 15)
returns subtasks language plpgsql security definer set search_path = public, extensions as $$
declare cid uuid; picked subtasks;
begin
  cid := _contributor_for_key(p_key);
  if cid is null then raise exception 'invalid key'; end if;

  select * into picked from subtasks s
   where (s.status = 'open' or (s.status = 'leased' and s.lease_expires_at < now()))
     and (p_topics is null or s.category_slug = any(p_topics) or s.tags && p_topics)
   order by s.priority desc, s.created_at
   for update skip locked
   limit 1;
  if not found then return null; end if;

  update subtasks
     set status = 'leased', leased_by = cid,
         lease_expires_at = now() + make_interval(mins => p_lease_minutes)
   where id = picked.id
   returning * into picked;
  return picked;
end;
$$;

-- submit: write the result and flip the task to done (requires an active lease).
create or replace function submit_result(p_key text, p_subtask_id uuid, p_artifact_md text,
                                         p_reported_model text, p_self_described_model text default null,
                                         p_token_count int default null, p_prompt_hash text default null,
                                         p_output_guard_passed boolean default true)
returns results language plpgsql security definer set search_path = public, extensions as $$
declare cid uuid; r results;
begin
  cid := _contributor_for_key(p_key);
  if cid is null then raise exception 'invalid key'; end if;
  if not exists (select 1 from subtasks s
                  where s.id = p_subtask_id and s.leased_by = cid
                    and s.status = 'leased' and s.lease_expires_at > now()) then
    raise exception 'no active lease for this subtask';
  end if;
  if p_output_guard_passed is not true then
    raise exception 'output guard did not pass; refusing to publish';
  end if;

  insert into results (subtask_id, contributor_id, artifact_md, reported_model,
                       self_described_model, token_count, prompt_hash, output_guard_passed)
  values (p_subtask_id, cid, p_artifact_md, p_reported_model,
          p_self_described_model, p_token_count, p_prompt_hash, p_output_guard_passed)
  returning * into r;

  update subtasks set status = 'done' where id = p_subtask_id;
  return r;
end;
$$;

-- release: give up a task (over budget / failed). v0 DISCARDS partial work.
create or replace function release_lease(p_key text, p_subtask_id uuid, p_failed boolean default false)
returns void language plpgsql security definer set search_path = public, extensions as $$
declare cid uuid;
begin
  cid := _contributor_for_key(p_key);
  update subtasks
     set status = case when p_failed then 'failed' else 'open' end,
         leased_by = null, lease_expires_at = null
   where id = p_subtask_id and leased_by = cid;
end;
$$;

-- ============================================================================
-- Task submission + AI moderation
-- ============================================================================

-- normalize text for dedup (lowercase; collapse non-alphanumerics → single spaces; trim)
create or replace function normalize_task_text(t text)
returns text language sql immutable as $$
  select trim(regexp_replace(lower(coalesce(t, '')), '[^a-z0-9]+', ' ', 'g'))
$$;

-- submit_task: anyone with a key submits; the task lands 'pending' (NOT claimable) until an AI
-- moderator accepts it. DB-level guards (no server): format-check, per-hour rate limit, and exact
-- duplicate rejection via a normalized dedupe_key (a UNIQUE index is the backstop).
create or replace function submit_task(p_key text, p_title text, p_prompt text,
                                       p_acceptance text default null, p_category_slug text default null,
                                       p_tags text[] default '{}', p_token_budget int default 5000,
                                       p_requested_model text default null, p_model_policy text default 'any')
returns subtasks language plpgsql security definer set search_path = public, extensions as $$
declare cid uuid; dk text; existing_id uuid; recent int; s subtasks;
begin
  cid := _contributor_for_key(p_key);
  if cid is null then raise exception 'invalid key'; end if;
  if length(coalesce(trim(p_title),  '')) < 8     then raise exception 'title too short'; end if;
  if length(coalesce(trim(p_prompt), '')) < 20    then raise exception 'prompt too short (make it self-contained)'; end if;
  if length(p_title)  > 200   then raise exception 'title too long'; end if;
  if length(p_prompt) > 20000 then raise exception 'prompt too long'; end if;
  if coalesce(p_token_budget, 0) < 500 or p_token_budget > 50000 then raise exception 'token_budget out of range (500..50000)'; end if;
  if p_model_policy not in ('any','min','exact') then raise exception 'bad model_policy'; end if;

  -- rate limit: at most 20 submissions per contributor per hour
  select count(*) into recent from subtasks where submitted_by = cid and created_at > now() - interval '1 hour';
  if recent >= 20 then raise exception 'rate limit: too many submissions this hour'; end if;

  -- exact-duplicate rejection
  dk := md5(normalize_task_text(coalesce(p_category_slug, '') || ' ' || p_title || ' ' || p_prompt));
  select id into existing_id from subtasks where dedupe_key = dk limit 1;
  if existing_id is not null then raise exception 'duplicate of task %', existing_id; end if;

  insert into subtasks (category_slug, tags, title, prompt, acceptance, token_budget,
                        requested_model, model_policy, status, submitted_by, dedupe_key)
  values (p_category_slug, coalesce(p_tags, '{}'), p_title, p_prompt, p_acceptance, p_token_budget,
          p_requested_model, p_model_policy, 'pending', cid, dk)
  returning * into s;
  return s;
end;
$$;

-- moderate_task: an AI moderator (a DIFFERENT contributor) records a verdict on a pending task.
create or replace function moderate_task(p_key text, p_subtask_id uuid, p_verdict text, p_note text default null)
returns subtasks language plpgsql security definer set search_path = public, extensions as $$
declare cid uuid; s subtasks;
begin
  cid := _contributor_for_key(p_key);
  if cid is null then raise exception 'invalid key'; end if;
  if p_verdict not in ('accept','reject','escalate') then raise exception 'bad verdict'; end if;
  select * into s from subtasks where id = p_subtask_id;
  if not found then raise exception 'no such task'; end if;
  if s.status not in ('pending','needs_review') then raise exception 'task is not pending (status=%)', s.status; end if;
  if s.submitted_by = cid then raise exception 'cannot moderate your own submission'; end if;
  update subtasks
     set status = case p_verdict when 'accept' then 'open' when 'reject' then 'rejected' else 'needs_review' end,
         rejection_note = case when p_verdict = 'reject' then p_note else rejection_note end
   where id = p_subtask_id
   returning * into s;
  return s;
end;
$$;

-- ============================================================================
-- Least-privilege grants
-- ============================================================================
revoke all on contributors, contributor_keys, categories, subtasks, results from anon, authenticated;

-- public, read-only access (NOTE: contributor_keys is intentionally excluded)
grant select on contributors, categories, subtasks, results to anon;

-- the key-authenticated write path is exposed as RPCs (the key is the auth)
grant execute on function register_contributor(text, text)                                   to anon;
grant execute on function claim_subtask(text, text[], int)                                    to anon;
grant execute on function submit_result(text, uuid, text, text, text, int, text, boolean)     to anon;
grant execute on function release_lease(text, uuid, boolean)                                  to anon;
grant execute on function submit_task(text, text, text, text, text, text[], int, text, text)  to anon;
grant execute on function moderate_task(text, uuid, text, text)                               to anon;
-- internal resolver is never callable directly
revoke all on function _contributor_for_key(text) from public, anon, authenticated;

-- ── categories + tags + full-text search; see db/seed.sql for the starter taxonomy. ─
