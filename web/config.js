// PUBLIC config — safe to commit & deploy. The anon key is RLS-protected (read-only).
// Real secrets (service_role key, access token) live in .env and are NEVER here.
window.POTLUCK_CONFIG = {
  supabaseUrl: "https://besocrfzgnkxyykzpkqv.supabase.co",
  anonKey: "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJpc3MiOiJzdXBhYmFzZSIsInJlZiI6ImJlc29jcmZ6Z25reHl5a3pwa3F2Iiwicm9sZSI6ImFub24iLCJpYXQiOjE3ODEzOTMzMDMsImV4cCI6MjA5Njk2OTMwM30.l4xFN2SiBUvsSv46abx7dYFpM91DL7JF-unOjCSYfQg"
};
