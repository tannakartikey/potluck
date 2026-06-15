package broker

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

const testRealKey = "sk-ant-REAL-SECRET-do-not-leak-1234567890"

// TestBrokerInjectsRealKey is the core broker property: the upstream receives the REAL key,
// and the placeholder the agent sent is gone.
func TestBrokerInjectsRealKey(t *testing.T) {
	var mu sync.Mutex
	var gotKey, gotAuth, gotPath string
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		gotKey = r.Header.Get("X-Api-Key")
		gotAuth = r.Header.Get("Authorization")
		gotPath = r.URL.Path
		mu.Unlock()
		io.WriteString(w, "upstream-ok")
	}))
	defer up.Close()

	b, err := New(testRealKey, up.URL, "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	if err := b.Start(); err != nil {
		t.Fatal(err)
	}
	defer b.Close()

	req, _ := http.NewRequest(http.MethodPost, b.Addr()+"/v1/messages", strings.NewReader(`{"x":1}`))
	req.Header.Set("X-Api-Key", b.Placeholder())
	req.Header.Set("Authorization", "Bearer "+b.Placeholder())
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if string(body) != "upstream-ok" {
		t.Errorf("body = %q, want upstream-ok (proxy did not forward)", body)
	}

	mu.Lock()
	defer mu.Unlock()
	if gotKey != testRealKey {
		t.Errorf("upstream X-Api-Key = %q, want the real key injected", gotKey)
	}
	if gotKey == b.Placeholder() {
		t.Error("upstream received the PLACEHOLDER, not the real key")
	}
	if strings.Contains(gotAuth, b.Placeholder()) {
		t.Errorf("placeholder leaked through in Authorization: %q", gotAuth)
	}
	if gotPath != "/v1/messages" {
		t.Errorf("path = %q, want /v1/messages forwarded", gotPath)
	}
}

func TestNewRefusesEmptyKey(t *testing.T) {
	if _, err := New("", "", ""); err == nil {
		t.Error("New with no real key must fail closed")
	}
	if _, err := New("   ", "", ""); err == nil {
		t.Error("New with blank key must fail closed")
	}
}

// TestScrubbedAgentEnvHidesRealKey proves the agent's environment contains ONLY the
// placeholder — the real key string appears nowhere — and points at the broker.
func TestScrubbedAgentEnvHidesRealKey(t *testing.T) {
	hostEnv := []string{
		"PATH=/usr/bin",
		"ANTHROPIC_API_KEY=" + testRealKey,
		"ANTHROPIC_BASE_URL=https://api.anthropic.com",
		"OPENAI_API_KEY=sk-openai-REAL-also-secret",
		"HOME=/home/potluck",
	}
	env := ScrubbedAgentEnv(hostEnv, "http://127.0.0.1:9999", DefaultPlaceholder)
	joined := strings.Join(env, "\n")

	if strings.Contains(joined, testRealKey) {
		t.Error("agent env still contains the REAL Anthropic key (broker isolation broken)")
	}
	if strings.Contains(joined, "sk-openai-REAL-also-secret") {
		t.Error("agent env still contains the real OpenAI key")
	}
	if !strings.Contains(joined, "ANTHROPIC_API_KEY="+DefaultPlaceholder) {
		t.Error("agent env missing the placeholder key")
	}
	if !strings.Contains(joined, "ANTHROPIC_BASE_URL=http://127.0.0.1:9999") {
		t.Error("agent env missing the broker base URL")
	}
	if !strings.Contains(joined, "PATH=/usr/bin") || !strings.Contains(joined, "HOME=/home/potluck") {
		t.Error("agent env dropped harmless host vars it should keep")
	}
}

// TestBrokerErrorResponseHasNoKey proves an upstream failure surfaces a generic 502 — the
// key never appears in an error path the agent could read.
func TestBrokerErrorResponseHasNoKey(t *testing.T) {
	// Point at an unreachable upstream so the proxy errors.
	b, err := New(testRealKey, "http://127.0.0.1:1", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	if err := b.Start(); err != nil {
		t.Fatal(err)
	}
	defer b.Close()

	resp, err := http.Get(b.Addr() + "/v1/messages")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadGateway {
		t.Errorf("status = %d, want 502", resp.StatusCode)
	}
	if strings.Contains(string(body), testRealKey) {
		t.Error("error response body leaked the real key")
	}
}
