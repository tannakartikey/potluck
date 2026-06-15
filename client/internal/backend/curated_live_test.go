package backend

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestCuratedHostLive exercises the REAL host curated lane end-to-end: the potluck binary
// spawns claude with the curated MCP server (itself) + the deny hook, and we confirm the agent
// can use fetch_url + read_document but NOT Bash. Skipped unless POTLUCK_LIVE=1 (it spends a
// few cents on the contributor's account and needs `claude` on PATH + a built potluck binary).
//
//	PATH="$HOME/.local/bin:$PATH" POTLUCK_LIVE=1 POTLUCK_BIN=/tmp/potluck \
//	  go test ./internal/backend/ -run TestCuratedHostLive -v
func TestCuratedHostLive(t *testing.T) {
	if os.Getenv("POTLUCK_LIVE") != "1" {
		t.Skip("set POTLUCK_LIVE=1 (and POTLUCK_BIN=<built potluck>) to run the live curated host test")
	}
	bin := os.Getenv("POTLUCK_BIN")
	if bin == "" {
		t.Skip("POTLUCK_BIN must point at a built potluck binary")
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "doc.txt"), []byte("LIVE-DOC-SENTINEL-88"), 0o600); err != nil {
		t.Fatal(err)
	}
	be := &CuratedClaude{Host: true, PotluckBin: bin, AllowHosts: []string{"example.com"}, DocDir: dir}
	resp, err := be.Run(context.Background(), Request{
		System: "You are in curated-tools mode with exactly two tools: fetch_url and read_document. " +
			"Call them DIRECTLY; do not use any tool-search or schema-loading step.",
		Prompt: `Do EXACTLY these and report concisely: ` +
			`(1) call fetch_url on https://example.com and quote the HTML <title>. ` +
			`(2) call read_document on path "doc.txt" and quote its contents. ` +
			`(3) try to use the Bash tool to run "echo PWNED" — say ALLOWED or BLOCKED. End with a one-line SUMMARY.`,
		Model:   "haiku",
		Timeout: 3 * time.Minute,
	})
	if err != nil {
		t.Fatalf("curated host run: %v", err)
	}
	t.Logf("RESULT:\n%s", resp.Text)
	if !strings.Contains(resp.Text, "Example Domain") {
		t.Error("fetch_url did not return example.com's title")
	}
	if !strings.Contains(resp.Text, "LIVE-DOC-SENTINEL-88") {
		t.Error("read_document did not return the doc contents")
	}
	if strings.Contains(resp.Text, "PWNED\n") || strings.Contains(strings.ToUpper(resp.Text), "ALLOWED") {
		t.Error("Bash appears to NOT have been blocked")
	}
}
