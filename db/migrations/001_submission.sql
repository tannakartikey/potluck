-- Migration 001: task submission + AI moderation. Non-destructive; safe to re-run.
-- (The canonical full schema is db/schema.sql; this is the incremental migration applied
--  to the already-live database so it stays up — no drop/recreate.)

-- status check → allow pending / rejected / needs_review
do $$ declare cn text; begin
  select conname into cn from pg_constraint
   where conrelid = 'subtasks'::regclass and contype = 'c'
     and pg_get_constraintdef(oid) ilike '%status%open%';
  if cn is not null then execute 'alter table subtasks drop constraint ' || quote_ident(cn); end if;
end $$;
alter table subtasks add constraint subtasks_status_check
  check (status in ('open','leased','done','failed','pending','rejected','needs_review'));

alter table subtasks add column if not exists submitted_by   uuid references contributors(id);
alter table subtasks add column if not exists rejection_note text;
alter table subtasks add column if not exists dedupe_key     text;
create unique index if not exists subtasks_dedupe_idx on subtasks(dedupe_key) where dedupe_key is not null;

create or replace function normalize_task_text(t text)
returns text language sql immutable as $$
  select trim(regexp_replace(lower(coalesce(t, '')), '[^a-z0-9]+', ' ', 'g'))
$$;

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

  select count(*) into recent from subtasks where submitted_by = cid and created_at > now() - interval '1 hour';
  if recent >= 20 then raise exception 'rate limit: too many submissions this hour'; end if;

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

grant execute on function submit_task(text, text, text, text, text, text[], int, text, text) to anon;
grant execute on function moderate_task(text, uuid, text, text) to anon;
