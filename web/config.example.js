// Copy to config.js and fill in your Supabase project's PUBLIC values.
//
// The anon key is SAFE to commit and expose: Row-Level Security makes it
// effectively read-only for the public tables (the static site can SELECT, but
// cannot write). Real secrets — the service_role key and the management access
// token — live in .env and must NEVER appear here.
//
// For the live GitHub Pages deployment, config.js IS committed (the anon key is
// public by design). If config.js is absent, the board falls back to the bundled
// sample data in ./data/.
window.POTLUCK_CONFIG = {
  supabaseUrl: "https://YOUR-REF.supabase.co",
  anonKey: "YOUR-PUBLIC-ANON-KEY"
};
