-- 004_flag_result.sql
-- Client-driven note correction (open-questions #24): a TRUSTED moderator (trust_level >= 1) flags
-- the published note on a 'done' task; the task reopens (done -> open) so a contributor can re-run
-- and SUPERSEDE it (canonical = newest). Results stay append-only — the prior note is kept in
-- history for provenance, never deleted. A moderator may not flag a task they themselves have a
-- note on. Non-destructive; apply via the Supabase SQL editor / Management API.

create or replace function flag_result(p_key text, p_subtask_id uuid, p_reason text default null)
returns subtasks language plpgsql security definer set search_path = public, extensions as $$
declare cid uuid; lvl int; s subtasks;
begin
  cid := _contributor_for_key(p_key);
  if cid is null then raise exception 'invalid key'; end if;
  select trust_level into lvl from contributors where id = cid;
  if coalesce(lvl, 0) < 1 then
    raise exception 'not authorized: only trusted moderators (trust_level >= 1) may flag a note — ask an admin to grant you moderator trust';
  end if;
  select * into s from subtasks where id = p_subtask_id;
  if not found then raise exception 'no such task'; end if;
  if s.status <> 'done' then raise exception 'can only flag a done task (status=%)', s.status; end if;
  if exists (select 1 from results where subtask_id = p_subtask_id and contributor_id = cid) then
    raise exception 'cannot flag a task you have a note on';
  end if;
  update subtasks
     set status = 'open', leased_by = null, lease_expires_at = null,
         rejection_note = coalesce(p_reason, rejection_note)
   where id = p_subtask_id
   returning * into s;
  return s;
end;
$$;

grant execute on function flag_result(text, uuid, text) to anon;
