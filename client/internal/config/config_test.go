package config

import (
	"os"
	"strings"
	"testing"
)

func withTempHome(t *testing.T) {
	t.Helper()
	t.Setenv("POTLUCK_HOME", t.TempDir())
}

func TestGenerateKeyUnique(t *testing.T) {
	k1, err := GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(k1, "potluck_") {
		t.Fatalf("missing prefix: %q", k1)
	}
	if len(k1) < 40 {
		t.Fatalf("key too short: %d", len(k1))
	}
	if k2, _ := GenerateKey(); k1 == k2 {
		t.Fatal("two keys should differ")
	}
}

func TestLoadDefaults(t *testing.T) {
	withTempHome(t)
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if c.Model != "haiku" || c.Backend != "claude-code" || c.BudgetTokens != 16000 {
		t.Fatalf("unexpected defaults: %+v", c)
	}
}

func TestSaveLoadRoundTrip(t *testing.T) {
	withTempHome(t)
	in := &Config{DisplayName: "alice", ContributorID: "c1", Topics: []string{"rails"}, Model: "sonnet", Backend: "claude-code", BudgetTokens: 5000}
	if err := in.Save(); err != nil {
		t.Fatal(err)
	}
	out, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if out.DisplayName != "alice" || out.ContributorID != "c1" || out.Model != "sonnet" || out.BudgetTokens != 5000 || len(out.Topics) != 1 {
		t.Fatalf("round-trip mismatch: %+v", out)
	}
}

func TestKeyRoundTripAndPerms(t *testing.T) {
	withTempHome(t)
	if HasKey() {
		t.Fatal("should not have a key yet")
	}
	if err := SaveKey("potluck_secret"); err != nil {
		t.Fatal(err)
	}
	if !HasKey() {
		t.Fatal("should have a key now")
	}
	k, err := LoadKey()
	if err != nil {
		t.Fatal(err)
	}
	if k != "potluck_secret" {
		t.Fatalf("LoadKey = %q", k)
	}
	info, err := os.Stat(credPath())
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("credentials perm = %o, want 600", perm)
	}
}
