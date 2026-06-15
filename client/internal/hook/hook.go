// Package hook implements Potluck's PreToolUse deny-all-except-curated hook for the Claude
// Code execution path. Per plans/prelaunch.md §0.4, a PreToolUse deny-all hook is a MORE
// reliable boundary than CLI flag incantations (which are version-dependent and have shipped
// no-op forms like the inert --allowed-tools ""). So in curated-tools mode the agent runs
// with this hook installed: it ALLOWS only the explicit curated MCP tools and DENIES every
// other tool — fail-closed on any unrecognised or malformed input. stdlib-only.
package hook

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// CuratedTools is the entire set of tool names the agent may call in curated-tools mode.
// These are the MCP-namespaced names Claude Code uses for our stdio server ("potluck").
var CuratedTools = []string{
	"mcp__potluck__fetch_url",
	"mcp__potluck__read_document",
}

type hookInput struct {
	ToolName string `json:"tool_name"`
}

// Decide reads a PreToolUse hook payload and returns (stdout, stderr, exitCode).
//
// Allow  → stdout carries permissionDecision:"allow", exit 0.
// Deny   → stdout carries permissionDecision:"deny" AND exit code 2 with a stderr reason.
//
// The double signal is deliberate: the JSON decision is the modern mechanism, and exit-code-2
// is the fail-safe backstop so that ANY parsing/version hiccup still blocks the call. Anything
// not on the allowlist — including malformed input or an empty tool name — is denied.
func Decide(input []byte, allowed []string) (stdout, stderr []byte, exitCode int) {
	allowSet := map[string]bool{}
	for _, a := range allowed {
		allowSet[a] = true
	}

	var in hookInput
	if err := json.Unmarshal(input, &in); err != nil || strings.TrimSpace(in.ToolName) == "" {
		return denyJSON("malformed or empty tool request"), []byte("potluck: blocked an unrecognised tool request (fail closed)\n"), 2
	}
	if allowSet[in.ToolName] {
		return allowJSON(in.ToolName), nil, 0
	}
	reason := fmt.Sprintf("Potluck curated-tools mode: %q is not permitted. The only allowed tools are: %s.",
		in.ToolName, strings.Join(sortedKeys(allowSet), ", "))
	return denyJSON(reason), []byte("potluck: " + reason + "\n"), 2
}

func allowJSON(tool string) []byte {
	return mustMarshal(map[string]interface{}{
		"hookSpecificOutput": map[string]interface{}{
			"hookEventName":            "PreToolUse",
			"permissionDecision":       "allow",
			"permissionDecisionReason": fmt.Sprintf("%s is a Potluck curated tool", tool),
		},
	})
}

func denyJSON(reason string) []byte {
	return mustMarshal(map[string]interface{}{
		"hookSpecificOutput": map[string]interface{}{
			"hookEventName":            "PreToolUse",
			"permissionDecision":       "deny",
			"permissionDecisionReason": reason,
		},
	})
}

func mustMarshal(v interface{}) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		return []byte(`{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"deny","permissionDecisionReason":"internal error"}}`)
	}
	return b
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
