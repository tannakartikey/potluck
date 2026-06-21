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

// TestBudgetStop pins the cross-provider token cap: off when unset, and stops once the
// run's cumulative token use reaches (or passes) the cap.
func TestBudgetStop(t *testing.T) {
	if msg := budgetStop(999999, Options{MaxTokens: 0}); msg != "" {
		t.Errorf("MaxTokens=0 must never stop, got %q", msg)
	}
	if msg := budgetStop(500, Options{MaxTokens: 1000}); msg != "" {
		t.Errorf("under the cap must not stop, got %q", msg)
	}
	if msg := budgetStop(1000, Options{MaxTokens: 1000}); msg == "" {
		t.Error("reaching the cap exactly must stop")
	}
	if msg := budgetStop(1500, Options{MaxTokens: 1000}); msg == "" {
		t.Error("passing the cap must stop")
	}
}

func TestGuardDetectsSecrets(t *testing.T) {
	bad := []string{
		"here is a key sk-ant-abcdefghijklmnopqrstuv",
		"-----BEGIN RSA PRIVATE KEY-----",
		"AKIAABCDEFGHIJKLMNOP",
		"see /Users/kartikey/secret.txt",
		"token ghp_abcdefghijklmnopqrstuvwxyz0123",
		"AIzaSyA1234567890abcdefghijklmnopqrstuv",                               // Google API key
		"key sk_live_abcdefghijklmnop1234",                                      // Stripe secret key
		"key sk-proj-abcdefghijklmnop1234",                                      // OpenAI project key
		"eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.abcdefghijklmnop1234", // JWT (session/bearer)
		"path C:\\Users\\bob\\creds.txt",                                        // Windows home path
		"cat ~/.ssh/id_rsa",                                                     // ~/.ssh
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
