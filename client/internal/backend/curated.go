package backend

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/tannakartikey/potluck/client/internal/broker"
	"github.com/tannakartikey/potluck/client/internal/sandbox"
)

// curatedAllowedTools is the EXACT, complete tool surface in v2 curated mode.
var curatedAllowedTools = []string{"mcp__potluck__fetch_url", "mcp__potluck__read_document", "mcp__potluck__web_search"}

// curatedDisallowed denies every built-in PLUS the harness/plugin tool-entrypoints (ToolSearch,
// Skill, Workflow, …) that can appear when potluck runs inside a Claude Code session — so the
// agent isn't tempted to "search/load" a tool schema instead of calling the curated MCP tools
// directly, and the surface stays exactly the two curated tools.
const curatedDisallowed = noToolsDenyList + " ToolSearch Skill Workflow AskUserQuestion ScheduleWakeup SendMessage DesignSync RemoteTrigger PushNotification"

// curatedDocDirInContainer is where the (optional) host document dir is mounted read-only.
const curatedDocDirInContainer = "/home/potluck/work/in"

// CuratedClaude runs a task via Claude Code with the v2 CURATED tool surface — exactly
// fetch_url + read_document (project-implemented, SSRF-safe / traversal-safe), never raw
// shell, file, or arbitrary web. It executes inside the hardened, default-deny-egress sandbox
// container (internal/sandbox); the provider is reached ONLY through the broker sidecar, and
// the agent holds only a placeholder key. Opt-in (--phase2); the v1 no-tools path is unchanged.
type CuratedClaude struct {
	Image      string
	AllowHosts []string // fetch_url host allowlist — contributor-controlled egress, default-deny
	DocDir     string   // document input dir (mounted ro in a container; used directly on host)
	Memory     string
	CPUs       string

	// Host runs curated mode directly on the host (no container) — the subscription / no-Docker
	// lane. The credential is still safe because the agent's ONLY tools are the curated MCP two
	// (verified live: Bash blocked, fetch_url/read_document work); but there is no container
	// backstop, so this is the weaker tier (worst case for a subscription token = rate-limit).
	Host bool
	// PotluckBin is the path the agent CLI uses to launch the MCP tools server + the deny hook.
	// In a container it's "potluck" (on PATH in the image); on the host it's this binary's abs path.
	PotluckBin string
}

func (c *CuratedClaude) Name() string { return "claude-code-curated" }

// cleanAgentEnv returns the host environment with the Claude Code harness/session variables
// removed, so the spawned agent CLI runs as a plain, predictable one-shot — not a "child
// session" with experimental tool surfaces (deferred tools / ToolSearch) inherited from a
// parent Claude Code process. Without this, a contributor running potluck from inside a Claude
// Code session would get a polluted, unpredictable agent tool surface. Auth vars are kept.
func cleanAgentEnv() []string {
	stripExact := map[string]bool{"CLAUDECODE": true, "CLAUDE_EFFORT": true, "AI_AGENT": true}
	var out []string
	for _, kv := range os.Environ() {
		name := kv
		if i := strings.IndexByte(kv, '='); i >= 0 {
			name = kv[:i]
		}
		if stripExact[name] || strings.HasPrefix(name, "CLAUDE_CODE_") {
			continue
		}
		out = append(out, kv)
	}
	return out
}

// mcpConfigJSON tells Claude Code to launch our stdio MCP server (the potluck binary in the
// image) as the ONLY MCP server, configured with this task's fetch allowlist + doc dir.
func mcpConfigJSON(allowHosts []string, docDir, potluckBin string) string {
	srvArgs := []string{"__tools-server"}
	if len(allowHosts) > 0 {
		srvArgs = append(srvArgs, "--allow", strings.Join(allowHosts, ","))
	}
	if docDir != "" {
		srvArgs = append(srvArgs, "--doc-dir", docDir)
	}
	cfg := map[string]interface{}{
		"mcpServers": map[string]interface{}{
			"potluck": map[string]interface{}{"command": potluckBin, "args": srvArgs},
		},
	}
	b, _ := json.Marshal(cfg)
	return string(b)
}

// hookSettingsJSON installs the PreToolUse deny-all-except-curated hook (the robust boundary
// per prelaunch §0.4): it runs `potluck __hook` before every tool call.
func hookSettingsJSON(potluckBin string) string {
	cfg := map[string]interface{}{
		"hooks": map[string]interface{}{
			"PreToolUse": []interface{}{
				map[string]interface{}{
					"matcher": "*",
					"hooks":   []interface{}{map[string]interface{}{"type": "command", "command": potluckBin + " __hook"}},
				},
			},
		},
	}
	b, _ := json.Marshal(cfg)
	return string(b)
}

// claudeCuratedArgs builds the curated-tools claude argv. The tool boundary is LAYERED so it
// does not depend on getting any single fragile flag exactly right:
//  1. the MCP server exposes only fetch_url + read_document;
//  2. --strict-mcp-config ignores the user's own MCP servers;
//  3. --disallowed-tools denies every builtin (Bash/Read/Write/WebFetch/…);
//  4. --allowed-tools pre-approves ONLY the two curated tools (headless can't prompt);
//  5. the PreToolUse hook denies anything not on the curated allowlist (the real backstop).
//
// Note: we never use the inert --allowed-tools "" (the platform-killing v1 bug).
func claudeCuratedArgs(req Request, allowHosts []string, docDir, potluckBin string) []string {
	if potluckBin == "" {
		potluckBin = "potluck"
	}
	args := []string{
		"-p", req.Prompt,
		"--output-format", "json",
		"--strict-mcp-config",
		"--mcp-config", mcpConfigJSON(allowHosts, docDir, potluckBin),
		"--allowed-tools", strings.Join(curatedAllowedTools, " "),
		"--disallowed-tools", curatedDisallowed,
		"--settings", hookSettingsJSON(potluckBin),
	}
	if req.System != "" {
		args = append(args, "--system-prompt", req.System)
	}
	if req.Model != "" {
		args = append(args, "--model", req.Model)
	}
	return args
}

// Run executes the task in the hardened sandbox container. It assumes the runner has already
// brought the broker sidecar + egress networks up (fail-closed preflight); this method only
// launches the agent container and parses its output.
func (c *CuratedClaude) Run(ctx context.Context, req Request) (*Response, error) {
	if req.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, req.Timeout)
		defer cancel()
	}

	if c.Host {
		return c.runHost(ctx, req)
	}
	return c.runContainer(ctx, req)
}

// runContainer runs curated mode inside the hardened sandbox (strongest lane).
func (c *CuratedClaude) runContainer(ctx context.Context, req Request) (*Response, error) {
	docDirInContainer := ""
	var mounts []string
	if c.DocDir != "" {
		docDirInContainer = curatedDocDirInContainer
		mounts = append(mounts, c.DocDir+":"+curatedDocDirInContainer+":ro")
	}
	cargs := claudeCuratedArgs(req, c.AllowHosts, docDirInContainer, "potluck") // potluck is on PATH in the image
	env := []string{
		// The container starts from a clean env (image ENV only): the real key is simply never
		// passed in. The agent gets the broker URL + a placeholder, so a dump-env finds no key.
		"ANTHROPIC_BASE_URL=" + sandbox.BrokerBaseURL(),
		"ANTHROPIC_API_KEY=" + broker.DefaultPlaceholder,
	}
	spec := sandbox.AgentSpec{Image: c.Image, Env: env, Mounts: mounts, Memory: c.Memory, CPUs: c.CPUs}
	dargs := sandbox.AgentRunArgs(spec, "claude", cargs)

	cmd := exec.CommandContext(ctx, "docker", dargs...)
	out, err := cmd.Output()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			return nil, fmt.Errorf("curated run (docker) exited: %v: %s", err, strings.TrimSpace(string(ee.Stderr)))
		}
		return nil, fmt.Errorf("curated run (docker): %w", err)
	}
	return parseClaudeResult(out, req.Model)
}

// runHost runs curated mode directly on the host (subscription / no-Docker lane). No container
// backstop, but the agent's ONLY tools are the curated MCP two + the deny hook, so it cannot
// read the credential, files, or run shell. The CLI authenticates with the contributor's own
// account/key as usual (we don't touch its env). Verified live: Bash blocked; the two tools work.
func (c *CuratedClaude) runHost(ctx context.Context, req Request) (*Response, error) {
	bin := c.PotluckBin
	if bin == "" {
		bin = "potluck"
	}
	cargs := claudeCuratedArgs(req, c.AllowHosts, c.DocDir, bin)
	cmd := exec.CommandContext(ctx, "claude", cargs...)
	cmd.Dir = os.TempDir()    // no local CLAUDE.md / project files auto-discovered
	cmd.Env = cleanAgentEnv() // scrub harness/session vars so the agent runs in a predictable, clean mode
	out, err := cmd.Output()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			return nil, fmt.Errorf("curated run (host) exited: %v: %s", err, strings.TrimSpace(string(ee.Stderr)))
		}
		return nil, fmt.Errorf("curated run (host): %w", err)
	}
	return parseClaudeResult(out, req.Model)
}
