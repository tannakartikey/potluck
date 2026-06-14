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

-- ── subtasks  (THE QUEUE + THE INDEX) ───────────────────────────────────────
create table if not exists subtasks (
  id               uuid primary key default gen_random_uuid(),
  category_slug    text,                              -- matchmaking + filtering (--topics)
  title            text not null,
  prompt           text not null,                     -- SELF-CONTAINED untrusted task text (DATA)
  acceptance       text,                              -- machine-checkable done-criteria (v1 quality lever)
  attachments      jsonb,                             -- optional image-input URLs (v1 = image INPUTS only)
  token_budget     int  not null default 5000,        -- ADVISORY; the runner enforces the real cap
  requested_model  text,                              -- e.g. 'claude-sonnet-4' or a tier like 'frontier'
  model_policy     text not null default 'any'
                   check (model_policy in ('any','min','exact')),
  status           text not null default 'open'
                   check (status in ('open','leased','done','failed')),
  leased_by        uuid references contributors(id),
  lease_expires_at timestamptz,
  created_at       timestamptz not null default now(),
  -- reserved (v2)
  consensus_group  uuid,
  harm_tier        int,
  checkpoint       text
);
create index if not exists subtasks_status_idx   on subtasks(status);
create index if not exists subtasks_category_idx on subtasks(category_slug);

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
  permalink            text
);
create index if not exists results_subtask_idx on results(subtask_id);

-- ============================================================================
-- Row-Level Security  (reads only; all writes go through the RPCs below)
-- ============================================================================
alter table contributors     enable row level security;
alter table contributor_keys enable row level security;   -- no policy, no grant => unreachable except via definer RPCs
alter table subtasks         enable row level security;
alter table results          enable row level security;

create policy "contributors public read" on contributors for select using (true);
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
create or replace function claim_subtask(p_key text, p_topics text[] default null,
                                         p_lease_minutes int default 15)
returns subtasks language plpgsql security definer set search_path = public, extensions as $$
declare cid uuid; picked subtasks;
begin
  cid := _contributor_for_key(p_key);
  if cid is null then raise exception 'invalid key'; end if;

  select * into picked from subtasks s
   where (s.status = 'open' or (s.status = 'leased' and s.lease_expires_at < now()))
     and (p_topics is null or s.category_slug = any(p_topics))
   order by s.created_at
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
-- Least-privilege grants
-- ============================================================================
revoke all on contributors, contributor_keys, subtasks, results from anon, authenticated;

-- public, read-only access (NOTE: contributor_keys is intentionally excluded)
grant select on contributors, subtasks, results to anon;

-- the key-authenticated write path is exposed as RPCs (the key is the auth)
grant execute on function register_contributor(text, text)                                   to anon;
grant execute on function claim_subtask(text, text[], int)                                    to anon;
grant execute on function submit_result(text, uuid, text, text, text, int, text, boolean)     to anon;
grant execute on function release_lease(text, uuid, boolean)                                  to anon;
-- internal resolver is never callable directly
revoke all on function _contributor_for_key(text) from public, anon, authenticated;

-- ── seed categories are just slugs on subtasks; see db/seed.sql for examples. ─
