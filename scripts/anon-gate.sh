#!/usr/bin/env bash
# Mandatory pre-launch anon-role gate — threat-model §6, "the #1 security-review item".
#
# Drives the LIVE PostgREST API as the public anon role and asserts that reads work but
# every write / key-hash read / privilege column is DENIED. Exits non-zero on any surprise,
# so it can gate a launch (run it manually; do NOT point CI at prod on every push).
#
#   usage: bash scripts/anon-gate.sh        (or: make anon-gate)
#   override target: POTLUCK_SUPABASE_URL=... POTLUCK_ANON_KEY=... bash scripts/anon-gate.sh
#
# The default anon key below is PUBLIC + read-only by design (the same one in web/config.js).
set -uo pipefail

BASE="${POTLUCK_SUPABASE_URL:-https://besocrfzgnkxyykzpkqv.supabase.co}/rest/v1"
ANON="${POTLUCK_ANON_KEY:-eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJpc3MiOiJzdXBhYmFzZSIsInJlZiI6ImJlc29jcmZ6Z25reHl5a3pwa3F2Iiwicm9sZSI6ImFub24iLCJpYXQiOjE3ODEzOTMzMDMsImV4cCI6MjA5Njk2OTMwM30.l4xFN2SiBUvsSv46abx7dYFpM91DL7JF-unOjCSYfQg}"
auth=(-H "apikey: $ANON" -H "Authorization: Bearer $ANON")
fail=0

# code METHOD PATH [json-body] -> prints the HTTP status code
code() {
  local m=$1 p=$2 d=${3:-}
  if [ -n "$d" ]; then
    curl -s -o /dev/null -w '%{http_code}' -X "$m" "$BASE$p" "${auth[@]}" -H 'Content-Type: application/json' -d "$d"
  else
    curl -s -o /dev/null -w '%{http_code}' -X "$m" "$BASE$p" "${auth[@]}"
  fi
}
# want DESC EXPECTED ACTUAL
want() {
  if [ "$3" = "$2" ]; then printf '  PASS  %s (%s)\n' "$1" "$3"
  else printf '  FAIL  %s: want %s got %s\n' "$1" "$2" "$3"; fail=1; fi
}

echo "anon-gate -> $BASE"

# Reads that MUST work (public board + commons).
want "GET subtasks readable"            200 "$(code GET '/subtasks?limit=1')"
want "GET results readable"             200 "$(code GET '/results?limit=1')"

# Key hashes MUST be unreadable (no grant on contributor_keys).
want "GET contributor_keys DENIED"      401 "$(code GET '/contributor_keys?limit=1')"

# Writes MUST be denied (no anon INSERT/UPDATE grant; writes go through key-gated RPCs only).
want "POST results DENIED"              401 "$(code POST '/results' '{"artifact_md":"x"}')"
want "PATCH subtasks DENIED"            401 "$(code PATCH '/subtasks?id=eq.00000000-0000-0000-0000-000000000000' '{"status":"done"}')"

# The internal key resolver MUST NOT be callable directly.
want "rpc _contributor_for_key DENIED"  401 "$(code POST '/rpc/_contributor_for_key' '{"p_key":"x"}')"

# Privilege columns MUST be unreadable (migration 003 column grant): select=* must not leak trust_level.
body=$(curl -s "$BASE/contributors?select=*&limit=1" "${auth[@]}")
if printf '%s' "$body" | grep -q 'trust_level'; then
  printf '  FAIL  contributors.trust_level is anon-readable (privilege leak — apply migration 003)\n'; fail=1
else
  printf '  PASS  contributors.trust_level NOT anon-readable\n'
fi

if [ "$fail" -eq 0 ]; then echo "ANON-GATE PASSED"; exit 0; else echo "ANON-GATE FAILED"; exit 1; fi
