package hook

import (
	"encoding/json"
	"strings"
	"testing"
)

func decision(t *testing.T, stdout []byte) string {
	t.Helper()
	var m struct {
		HookSpecificOutput struct {
			PermissionDecision string `json:"permissionDecision"`
		} `json:"hookSpecificOutput"`
	}
	if err := json.Unmarshal(stdout, &m); err != nil {
		t.Fatalf("hook stdout not valid JSON: %v\n%s", err, stdout)
	}
	return m.HookSpecificOutput.PermissionDecision
}

func TestHookAllowsCuratedTools(t *testing.T) {
	for _, tool := range CuratedTools {
		in := []byte(`{"tool_name":"` + tool + `","tool_input":{}}`)
		stdout, _, code := Decide(in, CuratedTools)
		if decision(t, stdout) != "allow" {
			t.Errorf("tool %q should be allowed", tool)
		}
		if code != 0 {
			t.Errorf("allow should exit 0, got %d for %q", code, tool)
		}
	}
}

func TestHookDeniesEverythingElse(t *testing.T) {
	dangerous := []string{"Bash", "Read", "Write", "Edit", "WebFetch", "WebSearch", "Task", "NotebookEdit", "KillShell", "mcp__other__exec", "mcp__potluck__delete"}
	for _, tool := range dangerous {
		in := []byte(`{"tool_name":"` + tool + `","tool_input":{}}`)
		stdout, stderr, code := Decide(in, CuratedTools)
		if decision(t, stdout) != "deny" {
			t.Errorf("tool %q must be denied", tool)
		}
		if code != 2 {
			t.Errorf("deny must exit 2 (fail-safe backstop), got %d for %q", code, tool)
		}
		if len(stderr) == 0 {
			t.Errorf("deny must write a stderr reason for %q", tool)
		}
	}
}

func TestHookFailsClosedOnMalformedInput(t *testing.T) {
	for _, in := range [][]byte{[]byte(``), []byte(`not json`), []byte(`{}`), []byte(`{"tool_name":""}`), []byte(`{"tool_name":"   "}`)} {
		stdout, _, code := Decide(in, CuratedTools)
		if decision(t, stdout) != "deny" || code != 2 {
			t.Errorf("malformed/empty input %q must fail closed (deny, exit 2)", in)
		}
	}
}

func TestHookReasonNamesAllowedTools(t *testing.T) {
	stdout, _, _ := Decide([]byte(`{"tool_name":"Bash"}`), CuratedTools)
	var m struct {
		HookSpecificOutput struct {
			PermissionDecisionReason string `json:"permissionDecisionReason"`
		} `json:"hookSpecificOutput"`
	}
	_ = json.Unmarshal(stdout, &m)
	if !strings.Contains(m.HookSpecificOutput.PermissionDecisionReason, "fetch_url") {
		t.Errorf("deny reason should list the curated tools, got %q", m.HookSpecificOutput.PermissionDecisionReason)
	}
}
