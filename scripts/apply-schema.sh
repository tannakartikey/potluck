#!/usr/bin/env bash
# Apply Potluck's canonical schema (and optionally seed) to a Supabase project via the
# Management API — no psql / DB password needed. Use this to (re)create the schema on a
# fresh project (e.g. staging) or to push schema changes.
#
#   scripts/apply-schema.sh <project-ref> [--seed]
#
# schema.sql is idempotent (create … if not exists / create or replace), so re-running is
# safe and non-destructive. --seed inserts the demo tasks; it is NOT idempotent, so it is
# opt-in and guarded on prod (re-seeding would duplicate rows).
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
PROD_REF="besocrfzgnkxyykzpkqv"   # the launch DB — guarded below

REF="${1:-}"
SEED=0
[ "${2:-}" = "--seed" ] && SEED=1
if [ -z "$REF" ]; then
  echo "usage: scripts/apply-schema.sh <project-ref> [--seed]" >&2
  exit 2
fi

# Load SUPABASE_ACCESS_TOKEN from .env (gitignored).
set -a; . "$ROOT/.env"; set +a
: "${SUPABASE_ACCESS_TOKEN:?SUPABASE_ACCESS_TOKEN not set in .env}"

echo "target project ref : $REF"
echo "apply schema       : yes"
echo "apply seed         : $([ $SEED = 1 ] && echo yes || echo no)"

if [ "$REF" = "$PROD_REF" ]; then
  echo "⚠️  this is the PROD launch DB."
  if [ "$SEED" = 1 ]; then
    echo "refusing to --seed prod (would duplicate tasks). Remove --seed." >&2
    exit 1
  fi
  read -r -p "Type 'PROD' to apply schema to prod: " confirm
  [ "$confirm" = "PROD" ] || { echo "aborted."; exit 1; }
fi

apply() {
  local file="$1" label="$2"
  local body; body="$(python3 -c 'import json,sys;print(json.dumps({"query":open(sys.argv[1]).read()}))' "$file")"
  local code; code="$(curl -s -o /tmp/potluck_apply_out -w '%{http_code}' \
    -X POST "https://api.supabase.com/v1/projects/$REF/database/query" \
    -H "Authorization: Bearer $SUPABASE_ACCESS_TOKEN" -H "Content-Type: application/json" \
    -d "$body")"
  if [ "$code" = "200" ] || [ "$code" = "201" ]; then
    echo "[$label] OK (HTTP $code)"
  else
    echo "[$label] FAILED (HTTP $code): $(head -c 400 /tmp/potluck_apply_out)" >&2
    rm -f /tmp/potluck_apply_out; exit 1
  fi
  rm -f /tmp/potluck_apply_out
}

apply "$ROOT/db/schema.sql" schema
[ "$SEED" = 1 ] && apply "$ROOT/db/seed.sql" seed
echo "done."
