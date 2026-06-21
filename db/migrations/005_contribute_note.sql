-- 005_contribute_note.sql
-- "Submit-with-result": a TRUSTED contributor (trust_level >= 1) submits a task AND its already-
-- produced note in one call — for sources only they can process (e.g. a long video transcript the
-- sealed, no-web worker can't fetch and that won't fit a task prompt). The task lands 'done' with the
-- attributed, unverified result; the producer is the donor of record. Untrusted callers must use
-- submit_task instead (the direct write-a-note path is trust-gated to avoid a spam vector).
-- Non-destructive; apply via the Supabase SQL editor / Management API.

create or replace function contribute_note(
  p_key text, p_title text, p_prompt text, p_acceptance text default null,
  p_category_slug text default null, p_tags text[] default '{}', p_token_budget int default 5000,
  p_requested_model text default null,
  p_artifact_md text default '', p_reported_model text default '', p_token_count int default 0,
  p_permalink text default null)
returns subtasks language plpgsql security definer set search_path = public, extensions as $$
declare cid uuid; lvl int; dk text; existing_id uuid; s subtasks;
begin
  cid := _contributor_for_key(p_key);
  if cid is null then raise exception 'invalid key'; end if;
  select trust_level into lvl from contributors where id = cid;
  if coalesce(lvl, 0) < 1 then
    raise exception 'not authorized: contributing a finished note needs trusted status (trust_level >= 1) — otherwise use submit_task and let a donor run it';
  end if;
  if length(coalesce(trim(p_title),  '')) < 8     then raise exception 'title too short'; end if;
  if length(coalesce(trim(p_prompt), '')) < 20    then raise exception 'prompt too short (give a short source reference)'; end if;
  if length(p_title)  > 200   then raise exception 'title too long'; end if;
  if length(p_prompt) > 20000 then raise exception 'prompt too long'; end if;
  if length(coalesce(trim(p_artifact_md), '')) < 40 then raise exception 'note (artifact_md) too short'; end if;
  if length(p_artifact_md) > 100000 then raise exception 'note too long (>100000 chars)'; end if;
  if length(coalesce(trim(p_reported_model), '')) < 1 then raise exception 'reported_model required'; end if;
  if coalesce(p_token_budget, 0) < 500 or p_token_budget > 50000 then raise exception 'token_budget out of range (500..50000)'; end if;

  dk := md5(normalize_task_text(coalesce(p_category_slug, '') || ' ' || p_title || ' ' || p_prompt));
  select id into existing_id from subtasks where dedupe_key = dk limit 1;
  if existing_id is not null then raise exception 'duplicate of task %', existing_id; end if;

  insert into subtasks (category_slug, tags, title, prompt, acceptance, token_budget,
                        requested_model, model_policy, status, submitted_by, dedupe_key)
  values (p_category_slug, coalesce(p_tags, '{}'), p_title, p_prompt, p_acceptance, p_token_budget,
          p_requested_model, 'any', 'done', cid, dk)
  returning * into s;

  insert into results (subtask_id, contributor_id, artifact_md, reported_model, token_count,
                       permalink, verification_status)
  values (s.id, cid, p_artifact_md, p_reported_model, greatest(coalesce(p_token_count, 0), 0),
          p_permalink, 'unverified');
  return s;
end;
$$;

grant execute on function contribute_note(text, text, text, text, text, text[], int, text, text, text, int, text) to anon;
