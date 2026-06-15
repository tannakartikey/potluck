package mcp

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tannakartikey/potluck/client/internal/tools"
)

// run feeds the given JSON-RPC request lines to a server and returns the parsed responses,
// keyed by request id (string form). Notifications produce no response.
func run(t *testing.T, s *Server, lines ...string) map[string]map[string]interface{} {
	t.Helper()
	in := strings.NewReader(strings.Join(lines, "\n") + "\n")
	var out bytes.Buffer
	if err := s.Serve(in, &out); err != nil {
		t.Fatalf("Serve: %v", err)
	}
	resps := map[string]map[string]interface{}{}
	for _, raw := range strings.Split(strings.TrimSpace(out.String()), "\n") {
		if raw == "" {
			continue
		}
		var m map[string]interface{}
		if err := json.Unmarshal([]byte(raw), &m); err != nil {
			t.Fatalf("bad response line %q: %v", raw, err)
		}
		id := "null"
		if v, ok := m["id"]; ok {
			id = strings.Trim(string(mustJSON(t, v)), `"`)
		}
		resps[id] = m
	}
	return resps
}

func mustJSON(t *testing.T, v interface{}) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func testServer(t *testing.T) (*Server, string) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "doc.txt"), []byte("the document body"), 0o600); err != nil {
		t.Fatal(err)
	}
	return NewServer(tools.NewFetcher(tools.NewAllowlist(nil)), tools.NewReader(dir)), dir
}

func TestInitialize(t *testing.T) {
	s, _ := testServer(t)
	r := run(t, s, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18"}}`)
	res, ok := r["1"]["result"].(map[string]interface{})
	if !ok {
		t.Fatalf("no result: %v", r["1"])
	}
	si, _ := res["serverInfo"].(map[string]interface{})
	if si["name"] != "potluck" {
		t.Errorf("serverInfo.name = %v, want potluck", si["name"])
	}
	if res["protocolVersion"] != "2025-06-18" {
		t.Errorf("protocolVersion = %v, want echoed 2025-06-18", res["protocolVersion"])
	}
}

func TestToolsListExposesExactlyCuratedTools(t *testing.T) {
	s, _ := testServer(t)
	r := run(t, s, `{"jsonrpc":"2.0","id":2,"method":"tools/list"}`)
	res := r["2"]["result"].(map[string]interface{})
	toolsArr, _ := res["tools"].([]interface{})
	names := map[string]bool{}
	for _, ti := range toolsArr {
		tm := ti.(map[string]interface{})
		names[tm["name"].(string)] = true
		if _, ok := tm["inputSchema"]; !ok {
			t.Errorf("tool %v missing inputSchema", tm["name"])
		}
	}
	if len(names) != 2 || !names["fetch_url"] || !names["read_document"] {
		t.Errorf("tool surface = %v, want exactly {fetch_url, read_document}", names)
	}
	// Defense: no shell/file/web tool may ever appear here.
	for _, forbidden := range []string{"Bash", "bash", "shell", "exec", "Read", "Write", "WebFetch"} {
		if names[forbidden] {
			t.Errorf("forbidden tool %q is exposed", forbidden)
		}
	}
}

func TestCallReadDocument(t *testing.T) {
	s, _ := testServer(t)
	r := run(t, s, `{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"read_document","arguments":{"path":"doc.txt"}}}`)
	res := r["3"]["result"].(map[string]interface{})
	if res["isError"] == true {
		t.Fatalf("read_document returned error: %v", res)
	}
	if !strings.Contains(contentText(res), "the document body") {
		t.Errorf("content = %q", contentText(res))
	}
}

func TestCallReadDocumentTraversalRefused(t *testing.T) {
	s, _ := testServer(t)
	r := run(t, s, `{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"read_document","arguments":{"path":"../../etc/passwd"}}}`)
	res := r["4"]["result"].(map[string]interface{})
	if res["isError"] != true {
		t.Errorf("traversal should be a tool error: %v", res)
	}
}

func TestCallFetchEmptyAllowlistDenied(t *testing.T) {
	s, _ := testServer(t)
	r := run(t, s, `{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"fetch_url","arguments":{"url":"https://example.com"}}}`)
	res := r["5"]["result"].(map[string]interface{})
	if res["isError"] != true {
		t.Errorf("fetch with empty allowlist should be a tool error: %v", res)
	}
}

func TestCallUnknownToolRefused(t *testing.T) {
	s, _ := testServer(t)
	r := run(t, s, `{"jsonrpc":"2.0","id":6,"method":"tools/call","params":{"name":"Bash","arguments":{"command":"echo hi"}}}`)
	res := r["6"]["result"].(map[string]interface{})
	if res["isError"] != true {
		t.Errorf("unknown tool (Bash) must be refused: %v", res)
	}
	if !strings.Contains(contentText(res), "unknown tool") {
		t.Errorf("expected 'unknown tool' message, got %q", contentText(res))
	}
}

func TestUnknownMethodErrors(t *testing.T) {
	s, _ := testServer(t)
	r := run(t, s, `{"jsonrpc":"2.0","id":7,"method":"resources/list"}`)
	if _, ok := r["7"]["error"]; !ok {
		t.Errorf("unknown method should return a JSON-RPC error: %v", r["7"])
	}
}

func TestNotificationGetsNoReply(t *testing.T) {
	s, _ := testServer(t)
	r := run(t, s, `{"jsonrpc":"2.0","method":"notifications/initialized"}`)
	if len(r) != 0 {
		t.Errorf("a notification must produce no response, got %v", r)
	}
}

func contentText(res map[string]interface{}) string {
	arr, _ := res["content"].([]interface{})
	var b strings.Builder
	for _, c := range arr {
		cm, _ := c.(map[string]interface{})
		if t, ok := cm["text"].(string); ok {
			b.WriteString(t)
		}
	}
	return b.String()
}
