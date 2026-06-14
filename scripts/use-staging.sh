# Point the potluck client at the STAGING database for this shell.
#   source scripts/use-staging.sh        # then: ./potluck run … hits staging, not prod
#
# The client (api.New) reads POTLUCK_SUPABASE_URL / POTLUCK_ANON_KEY env overrides; this
# just maps the STAGING_* values from .env onto them. Run in a throwaway shell — open a
# fresh terminal (or `unset POTLUCK_SUPABASE_URL POTLUCK_ANON_KEY`) to go back to prod.
#
# NOTE: must be SOURCED, not executed, so the exports survive in your shell.
__potluck_root="$(cd "$(dirname "${BASH_SOURCE[0]:-$0}")/.." && pwd)"
set -a; . "$__potluck_root/.env"; set +a
if [ -z "${STAGING_SUPABASE_URL:-}" ] || [ -z "${STAGING_SUPABASE_ANON_KEY:-}" ]; then
  echo "STAGING_SUPABASE_URL / STAGING_SUPABASE_ANON_KEY not found in .env" >&2
else
  export POTLUCK_SUPABASE_URL="$STAGING_SUPABASE_URL"
  export POTLUCK_ANON_KEY="$STAGING_SUPABASE_ANON_KEY"
  # Contributor keys are DB-specific (registered into one project's contributor_keys), so
  # give staging its own ~/.potluck-staging home — registering here never clobbers the
  # prod key in ~/.potluck.
  export POTLUCK_HOME="$HOME/.potluck-staging"
  echo "potluck → STAGING ($STAGING_SUPABASE_URL)"
  echo "  config/key home: $POTLUCK_HOME  (register once here: ./potluck register)"
  echo "  (open a new shell or 'unset POTLUCK_SUPABASE_URL POTLUCK_ANON_KEY POTLUCK_HOME' to return to prod)"
fi
unset __potluck_root
