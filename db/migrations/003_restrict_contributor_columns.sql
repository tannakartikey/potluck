-- 003: restrict anon reads on `contributors` to attribution columns only.
--
-- Before: anon held a TABLE-level SELECT on contributors, so `GET /contributors?select=*`
-- returned trust_level / reputation / validated_streak / created_at — letting anyone
-- enumerate which contributors are moderators/admins (trust_level >= 1/2). The RLS row
-- policy ("contributors public read", USING(true)) is unchanged; this narrows COLUMNS.
--
-- After: anon may read only id + display_name (all attribution/leaderboards need).
-- Postgres enforces column privileges even under USING(true), so `select=*` now fails
-- and trust_level is unreadable by anon. Verify with: bash scripts/anon-gate.sh
--
-- Idempotent: safe to re-run.

revoke select on contributors from anon;
grant  select (id, display_name) on contributors to anon;
