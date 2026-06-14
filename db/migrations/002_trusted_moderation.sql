-- 002_trusted_moderation.sql
-- Only TRUSTED contributors may moderate. Trust is bound to the contributor KEY and enforced in
-- the RPC — the sound alternative to attesting an open-source client binary (which is impossible
-- on hardware we don't control; see plans/vision.md and open-questions.md #27/#28). Non-destructive.
--
-- trust_level (on contributors, already present): 0 = untrusted (default) · >=1 = trusted
-- moderator · >=2 = admin (may grant moderator trust). Bootstrap the first admin out-of-band:
--   update contributors set trust_level = 2 where id = '<owner-contributor-id>';
-- Apply via the Supabase SQL editor / Management API.

-- audit: which trusted moderator accepted/rejected a task
alter table subtasks add column if not exists moderated_by uuid references contributors(id);

-- moderate_task: now requires trust_level >= 1 and records moderated_by.
create or replace function moderate_task(p_key text, p_subtask_id uuid, p_verdict text, p_note text default null)
returns subtasks language plpgsql security definer set search_path = public, extensions as $$
declare cid uuid; lvl int; s subtasks;
begin
  cid := _contributor_for_key(p_key);
  if cid is null then raise exception 'invalid key'; end if;
  select trust_level into lvl from contributors where id = cid;
  if coalesce(lvl, 0) < 1 then
    raise exception 'not authorized: only trusted moderators (trust_level >= 1) may moderate — ask an admin to grant you moderator trust';
  end if;
  if p_verdict not in ('accept','reject','escalate') then raise exception 'bad verdict'; end if;
  select * into s from subtasks where id = p_subtask_id;
  if not found then raise exception 'no such task'; end if;
  if s.status not in ('pending','needs_review') then raise exception 'task is not pending (status=%)', s.status; end if;
  if s.submitted_by = cid then raise exception 'cannot moderate your own submission'; end if;
  update subtasks
     set status = case p_verdict when 'accept' then 'open' when 'reject' then 'rejected' else 'needs_review' end,
         rejection_note = case when p_verdict = 'reject' then p_note else rejection_note end,
         moderated_by = cid
   where id = p_subtask_id
   returning * into s;
  return s;
end;
$$;

-- grant_trust: an ADMIN (trust_level >= 2) grants/revokes MODERATOR trust (0 or 1). Admin level is
-- never grantable here (no self-escalation); bootstrap it out-of-band.
create or replace function grant_trust(p_key text, p_contributor_id uuid, p_level int)
returns contributors language plpgsql security definer set search_path = public, extensions as $$
declare admin_id uuid; admin_lvl int; c contributors;
begin
  admin_id := _contributor_for_key(p_key);
  if admin_id is null then raise exception 'invalid key'; end if;
  select trust_level into admin_lvl from contributors where id = admin_id;
  if coalesce(admin_lvl, 0) < 2 then raise exception 'not authorized: only admins (trust_level >= 2) may grant trust'; end if;
  if p_level not in (0, 1) then raise exception 'level must be 0 (revoke moderator) or 1 (grant moderator)'; end if;
  if p_contributor_id = admin_id then raise exception 'cannot change your own trust level'; end if;
  update contributors set trust_level = p_level where id = p_contributor_id returning * into c;
  if not found then raise exception 'no such contributor'; end if;
  return c;
end;
$$;

grant execute on function moderate_task(text, uuid, text, text) to anon;
grant execute on function grant_trust(text, uuid, int)          to anon;
