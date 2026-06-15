package backend

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"

	"github.com/tannakartikey/potluck/client/internal/broker"
	"github.com/tannakartikey/potluck/client/internal/sandbox"
)

// curatedAllowedTools is the EXACT, complete tool surface in v2 curated mode.
var curatedAllowedTools = []string{"mcp__potluck__fetch_url", "mcp__potluck__read_document"}

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
	DocDir     string   // host dir mounted read-only as the document input dir (optional)
	Memory     string
	CPUs       string
}

func (c *CuratedClaude) Name() string { return "claude-code-curated" }

// mcpConfigJSON tells Claude Code to launch our stdio MCP server (the potluck binary in the
// image) as the ONLY MCP server, configured with this task's fetch allowlist + doc dir.
func mcpConfigJSON(allowHosts []string, docDir string) string {
	srvArgs := []string{"__tools-server"}
	if len(allowHosts) > 0 {
		srvArgs = append(srvArgs, "--allow", strings.Join(allowHosts, ","))
	}
	if docDir != "" {
		srvArgs = append(srvArgs, "--doc-dir", docDir)
	}
	cfg := map[string]interface{}{
		"mcpServers": map[string]interface{}{
			"potluck": map[string]interface{}{"command": "potluck", "args": srvArgs},
		},
	}
	b, _ := json.Marshal(cfg)
	return string(b)
}

// hookSettingsJSON installs the PreToolUse deny-all-except-curated hook (the robust boundary
// per prelaunch §0.4): it runs `potluck __hook` before every tool call.
func hookSettingsJSON() string {
	cfg := map[string]interface{}{
		"hooks": map[string]interface{}{
			"PreToolUse": []interface{}{
				map[string]interface{}{
					"matcher": "*",
					"hooks":   []interface{}{map[string]interface{}{"type": "command", "command": "potluck __hook"}},
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
func claudeCuratedArgs(req Request, allowHosts []string, docDir string) []string {
	args := []string{
		"-p", req.Prompt,
		"--output-format", "json",
		"--strict-mcp-config",
		"--mcp-config", mcpConfigJSON(allowHosts, docDir),
		"--allowed-tools", strings.Join(curatedAllowedTools, " "),
		"--disallowed-tools", noToolsDenyList,
		"--settings", hookSettingsJSON(),
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

	docDirInContainer := ""
	var mounts []string
	if c.DocDir != "" {
		docDirInContainer = curatedDocDirInContainer
		mounts = append(mounts, c.DocDir+":"+curatedDocDirInContainer+":ro")
	}

	cargs := claudeCuratedArgs(req, c.AllowHosts, docDirInContainer)
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
