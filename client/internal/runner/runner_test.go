package runner

import (
	"strings"
	"testing"

	"github.com/tannakartikey/potluck/client/internal/api"
)

func TestCommas(t *testing.T) {
	for in, want := range map[int]string{0: "0", 7: "7", 1234: "1,234", 28400: "28,400", 1000000: "1,000,000"} {
		if got := commas(in); got != want {
			t.Errorf("commas(%d) = %q, want %q", in, got, want)
		}
	}
}

func TestGuardDetectsSecrets(t *testing.T) {
	bad := []string{
		"here is a key sk-ant-abcdefghijklmnopqrstuv",
		"-----BEGIN RSA PRIVATE KEY-----",
		"AKIAABCDEFGHIJKLMNOP",
		"see /Users/kartikey/secret.txt",
		"token ghp_abcdefghijklmnopqrstuvwxyz0123",
	}
	for _, s := range bad {
		if guard(s) == "" {
			t.Errorf("guard should flag: %q", s)
		}
	}
	if guard("A clean summary of this week's Rails changes, each with a source URL.") != "" {
		t.Error("guard should pass clean text")
	}
}

func TestBuildPromptIncludesParts(t *testing.T) {
	p := buildPrompt(&api.Subtask{Title: "Explain N+1", Prompt: "Explain the N+1 problem.", Acceptance: "<= 250 words."})
	for _, want := range []string{"Explain N+1", "N+1 problem", "ACCEPTANCE", "250 words", "Markdown"} {
		if !strings.Contains(p, want) {
			t.Errorf("prompt missing %q:\n%s", want, p)
		}
	}
}

func TestShortTruncates(t *testing.T) {
	if got := short(strings.Repeat("x", 80)); len([]rune(got)) > 50 {
		t.Errorf("short() too long: %d runes", len([]rune(got)))
	}
	if short("ok") != "ok" {
		t.Error("short should leave short strings unchanged")
	}
}
