package backend

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
)

func argValue(args []string, flag string) (string, bool) {
	i := slices.Index(args, flag)
	if i < 0 || i+1 >= len(args) {
		return "", false
	}
	return args[i+1], true
}

func TestClaudeCuratedArgs(t *testing.T) {
	args := claudeCuratedArgs(Request{Prompt: "summarize this", System: "curated preamble", Model: "haiku"},
		curatedDocDirInContainer, "potluck")

	// --allowed-tools must be the curated surface: native WebSearch + WebFetch + read_document —
	// and never the inert empty form.
	allowed, ok := argValue(args, "--allowed-tools")
	if !ok || allowed == "" {
		t.Fatalf("--allowed-tools missing or empty (the v1 platform-killing bug): %v", args)
	}
	for _, want := range []string{"WebSearch", "WebFetch", "mcp__potluck__read_document"} {
		if !strings.Contains(allowed, want) {
			t.Errorf("--allowed-tools must include %q, got %q", want, allowed)
		}
	}
	// Shell/file builtins must NEVER be allowed.
	for _, builtin := range []string{"Bash", "Read", "Write", "Edit"} {
		if strings.Contains(allowed, builtin) {
			t.Errorf("--allowed-tools must NOT contain host-touching builtin %q", builtin)
		}
	}

	// Disallowed denies shell/file but NOT the web tools; strict MCP + hook installed.
	disallowed, _ := argValue(args, "--disallowed-tools")
	if !strings.Contains(disallowed, "Bash") || !strings.Contains(disallowed, "Read") {
		t.Errorf("--disallowed-tools must deny Bash + Read, got %q", disallowed)
	}
	if strings.Contains(disallowed, "WebSearch") || strings.Contains(disallowed, "WebFetch") {
		t.Errorf("--disallowed-tools must NOT deny the web tools, got %q", disallowed)
	}
	if !slices.Contains(args, "--strict-mcp-config") {
		t.Error("--strict-mcp-config must be present (ignore the user's MCP servers)")
	}

	// MCP config is valid JSON and launches our server (read_document only).
	mcp, _ := argValue(args, "--mcp-config")
	var mcpCfg struct {
		MCPServers map[string]struct {
			Command string   `json:"command"`
			Args    []string `json:"args"`
		} `json:"mcpServers"`
	}
	if err := json.Unmarshal([]byte(mcp), &mcpCfg); err != nil {
		t.Fatalf("--mcp-config not valid JSON: %v", err)
	}
	pot, ok := mcpCfg.MCPServers["potluck"]
	if !ok || pot.Command != "potluck" || !slices.Contains(pot.Args, "__tools-server") {
		t.Errorf("mcp config does not launch the potluck tools server: %+v", mcpCfg)
	}

	// Settings install the PreToolUse deny hook.
	settings, _ := argValue(args, "--settings")
	if !strings.Contains(settings, "PreToolUse") || !strings.Contains(settings, "potluck __hook") {
		t.Errorf("--settings must install the PreToolUse potluck __hook, got %q", settings)
	}

	// The untrusted prompt travels as the -p value (DATA), never the system prompt.
	if p, _ := argValue(args, "-p"); p != "summarize this" {
		t.Errorf("prompt not in -p DATA position: %v", args)
	}
	if sys, _ := argValue(args, "--system-prompt"); sys != "curated preamble" {
		t.Errorf("system prompt not passed: %v", args)
	}
}

func TestMcpConfigOmitsDocDirWhenEmpty(t *testing.T) {
	cfg := mcpConfigJSON("", "potluck")
	if strings.Contains(cfg, "--doc-dir") {
		t.Errorf("doc-dir should be omitted when no document dir is mounted: %s", cfg)
	}
	cfg = mcpConfigJSON("/home/potluck/work/in", "potluck")
	if !strings.Contains(cfg, "--doc-dir") {
		t.Errorf("doc-dir should be present when mounted: %s", cfg)
	}
}

func TestCuratedBackendName(t *testing.T) {
	c := &CuratedClaude{}
	if c.Name() != "claude-code-curated" {
		t.Errorf("Name() = %q", c.Name())
	}
}

// TestClaudeCuratedArgsMaxBudgetUSD: the per-task $ cap must also reach the DEFAULT
// (curated) Claude lane, not just the --no-tools lane — else --max-budget-usd would be
// silently dropped on the path most contributors actually use.
func TestClaudeCuratedArgsMaxBudgetUSD(t *testing.T) {
	v, ok := argValue(claudeCuratedArgs(Request{Prompt: "x", MaxUSD: 1.25}, "", "potluck"), "--max-budget-usd")
	if !ok || v != "1.2500" {
		t.Errorf("--max-budget-usd = %q (ok=%v), want 1.2500", v, ok)
	}
	if slices.Contains(claudeCuratedArgs(Request{Prompt: "x"}, "", "potluck"), "--max-budget-usd") {
		t.Error("--max-budget-usd must be omitted when MaxUSD is 0")
	}
}
