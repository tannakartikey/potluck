package backend

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWrapExecDocker(t *testing.T) {
	d := &DockerConfig{
		Image:  "img:1",
		Mounts: []string{"/h/.codex/auth.json:/home/potluck/.codex/auth.json:ro"},
		Env:    []string{"OPENAI_API_KEY=x"},
		Memory: "2g",
		CPUs:   "2",
	}
	prog, args := wrapExec(d, "codex", []string{"exec", "--json", "hi"})
	if prog != "docker" {
		t.Fatalf("prog = %s, want docker", prog)
	}
	joined := strings.Join(args, " ")
	for _, want := range []string{
		"run --rm", "--read-only", "--cap-drop ALL", "--security-opt no-new-privileges",
		"--memory 2g", "--cpus 2",
		"-v /h/.codex/auth.json:/home/potluck/.codex/auth.json:ro",
		"-e OPENAI_API_KEY=x",
		"img:1 codex exec --json hi",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("missing %q in:\n%s", want, joined)
		}
	}
}

func TestWrapExecHostPassthrough(t *testing.T) {
	prog, args := wrapExec(nil, "claude", []string{"-p", "x"})
	if prog != "claude" || len(args) != 2 || args[0] != "-p" {
		t.Fatalf("host passthrough wrong: %s %v", prog, args)
	}
}

// TestAuthMountsForFileOnly is the load-bearing guarantee: we mount ONLY the single auth
// file, never the whole ~/.codex / ~/.claude directory (which holds session history).
func TestAuthMountsForFileOnly(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "") // no API key → the subscription token-file mount path
	home := t.TempDir()
	codexDir := filepath.Join(home, ".codex")
	if err := os.MkdirAll(codexDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(codexDir, "auth.json"), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	// A session-history file that must NEVER be mounted.
	if err := os.WriteFile(filepath.Join(codexDir, "history.jsonl"), []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}

	mounts, env := AuthMountsFor("codex", home)
	if len(mounts) != 1 {
		t.Fatalf("want exactly 1 mount, got %v", mounts)
	}
	m := mounts[0]
	if !strings.HasSuffix(strings.TrimSuffix(m, ":ro"), "auth.json:/home/potluck/.codex/auth.json") {
		t.Errorf("mount is not the single auth file: %s", m)
	}
	if strings.Contains(m, "history.jsonl") {
		t.Errorf("session history must not be mounted: %s", m)
	}
	// The host source must be a file path, not the directory itself.
	if strings.HasPrefix(m, codexDir+":") {
		t.Errorf("mounted the whole .codex directory, not just the auth file: %s", m)
	}
	if len(env) != 1 || env[0] != "OPENAI_API_KEY" {
		t.Errorf("env forward = %v, want [OPENAI_API_KEY]", env)
	}
}

// TestAuthMountsForSkipsMountWhenKeyPresent is the load-bearing fix: when an API key is set,
// the credential FILE is NOT mounted (so a task's read-only shell — Codex — has no token file
// to read); we forward the key instead. Closes the empirically-proven Codex token-read leak.
func TestAuthMountsForSkipsMountWhenKeyPresent(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "sk-present")
	home := t.TempDir()
	codexDir := filepath.Join(home, ".codex")
	if err := os.MkdirAll(codexDir, 0o700); err != nil {
		t.Fatal(err)
	}
	// auth.json EXISTS, but because the API key is set it must NOT be mounted.
	if err := os.WriteFile(filepath.Join(codexDir, "auth.json"), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	mounts, env := AuthMountsFor("codex", home)
	if len(mounts) != 0 {
		t.Errorf("with OPENAI_API_KEY set, the token file must NOT be mounted, got %v", mounts)
	}
	if len(env) != 1 || env[0] != "OPENAI_API_KEY" {
		t.Errorf("env forward = %v, want [OPENAI_API_KEY]", env)
	}
}

// TestAuthMountsForNoFile: with no auth file (e.g. macOS Keychain / API-key only), there
// is nothing to mount and we fall back to forwarding the API key env var by name.
func TestAuthMountsForNoFile(t *testing.T) {
	home := t.TempDir()
	mounts, env := AuthMountsFor("claude-code", home)
	if len(mounts) != 0 {
		t.Errorf("want no mounts when no credentials file exists, got %v", mounts)
	}
	if len(env) != 1 || env[0] != "ANTHROPIC_API_KEY" {
		t.Errorf("env forward = %v, want [ANTHROPIC_API_KEY]", env)
	}
}

func TestHasContainerAuth(t *testing.T) {
	if HasContainerAuth("codex", "/h", []string{"x:/y:ro"}, nil) != true {
		t.Error("a mount should count as auth")
	}
	t.Setenv("ANTHROPIC_API_KEY", "sk-test")
	if HasContainerAuth("claude-code", "/h", nil, []string{"ANTHROPIC_API_KEY"}) != true {
		t.Error("a set env var should count as auth")
	}
	t.Setenv("ANTHROPIC_API_KEY", "")
	if HasContainerAuth("claude-code", "/h", nil, []string{"ANTHROPIC_API_KEY"}) != false {
		t.Error("no mount + unset env should be no auth")
	}
}
