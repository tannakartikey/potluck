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
		[]string{"example.com", "arxiv.org"}, curatedDocDirInContainer, "potluck")

	// --allowed-tools must be EXACTLY the two curated MCP tools — and never the inert empty form.
	allowed, ok := argValue(args, "--allowed-tools")
	if !ok || allowed == "" {
		t.Fatalf("--allowed-tools missing or empty (the v1 platform-killing bug): %v", args)
	}
	if !strings.Contains(allowed, "mcp__potluck__fetch_url") || !strings.Contains(allowed, "mcp__potluck__read_document") {
		t.Errorf("--allowed-tools must list both curated tools, got %q", allowed)
	}
	for _, builtin := range []string{"Bash", "Read", "Write", "WebFetch"} {
		if strings.Contains(allowed, builtin) {
			t.Errorf("--allowed-tools must NOT contain builtin %q", builtin)
		}
	}

	// Builtins explicitly denied + strict MCP + hook installed.
	if disallowed, _ := argValue(args, "--disallowed-tools"); !strings.Contains(disallowed, "Bash") {
		t.Errorf("--disallowed-tools must deny Bash, got %q", disallowed)
	}
	if !slices.Contains(args, "--strict-mcp-config") {
		t.Error("--strict-mcp-config must be present (ignore the user's MCP servers)")
	}

	// MCP config is valid JSON and launches our server.
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
	if !slices.Contains(pot.Args, "--allow") || !strings.Contains(strings.Join(pot.Args, " "), "example.com") {
		t.Errorf("mcp config missing the fetch allowlist: %v", pot.Args)
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
	cfg := mcpConfigJSON([]string{"example.com"}, "", "potluck")
	if strings.Contains(cfg, "--doc-dir") {
		t.Errorf("doc-dir should be omitted when no document dir is mounted: %s", cfg)
	}
	cfg = mcpConfigJSON([]string{"example.com"}, "/home/potluck/work/in", "potluck")
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
