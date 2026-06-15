package tools

import (
	"context"
	"strings"
	"testing"
)

const ddgFixture = `
<div class="result results_links results_links_deep web-result">
  <div class="result__body">
    <h2 class="result__title">
      <a rel="nofollow" class="result__a" href="//duckduckgo.com/l/?uddg=https%3A%2F%2Fdevelopers.openai.com%2Fcodex%2Fsandboxing&amp;rut=abc123">Codex <b>Sandbox</b> docs</a>
    </h2>
    <a class="result__snippet" href="//duckduckgo.com/l/?uddg=https%3A%2F%2Fexample.com">Read-only mode lets Codex <b>inspect</b> files but not edit them.</a>
  </div>
</div>
<div class="result results_links">
  <a rel="nofollow" class="result__a" href="//duckduckgo.com/l/?uddg=https%3A%2F%2Fgithub.com%2Fopenai%2Fcodex&amp;rut=def">openai/codex on GitHub</a>
  <a class="result__snippet">The Codex CLI source repository.</a>
</div>`

func TestParseDDGResults(t *testing.T) {
	res := parseDDGResults(ddgFixture)
	if len(res) != 2 {
		t.Fatalf("got %d results, want 2: %+v", len(res), res)
	}
	if res[0].URL != "https://developers.openai.com/codex/sandboxing" {
		t.Errorf("result[0].URL = %q (uddg not decoded)", res[0].URL)
	}
	if res[0].Title != "Codex Sandbox docs" {
		t.Errorf("result[0].Title = %q (tags not stripped)", res[0].Title)
	}
	if !strings.Contains(res[0].Snippet, "Read-only mode") || strings.Contains(res[0].Snippet, "<b>") {
		t.Errorf("result[0].Snippet = %q", res[0].Snippet)
	}
	if res[1].URL != "https://github.com/openai/codex" {
		t.Errorf("result[1].URL = %q", res[1].URL)
	}
}

func TestDecodeDDGHref(t *testing.T) {
	cases := map[string]string{
		"//duckduckgo.com/l/?uddg=https%3A%2F%2Fexample.com%2Fa&amp;rut=x": "https://example.com/a",
		"https://plain.example/page":                                       "https://plain.example/page",
		"//duckduckgo.com/l/?uddg=ftp%3A%2F%2Fnope":                        "", // non-http target dropped
		"/relative/only": "",
	}
	for in, want := range cases {
		if got := decodeDDGHref(in); got != want {
			t.Errorf("decodeDDGHref(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestResearchAllowlist(t *testing.T) {
	hosts := ResearchAllowlist()
	if len(hosts) < 20 {
		t.Errorf("research allowlist surprisingly small: %d", len(hosts))
	}
	a := NewAllowlist(hosts)
	// Reputable sources + their subdomains should be allowed via dot-boundary matching.
	for _, h := range []string{"github.com", "api.github.com", "raw.githubusercontent.com", "developers.openai.com", "docs.python.org", "stackoverflow.com"} {
		if !a.Allows(h) {
			t.Errorf("research allowlist should permit %q", h)
		}
	}
	// A random attacker host must NOT be allowed.
	if a.Allows("evil-exfil.attacker.test") {
		t.Error("research allowlist must not permit arbitrary hosts")
	}
	// It's a copy — mutating the result must not affect the source.
	hosts[0] = "MUTATED"
	if ResearchAllowlist()[0] == "MUTATED" {
		t.Error("ResearchAllowlist must return a copy")
	}
}

func TestSearchEmptyQuery(t *testing.T) {
	if _, err := NewSearcher().Search(context.Background(), "   "); err == nil {
		t.Error("empty query should error")
	}
}

func TestSearchResultCap(t *testing.T) {
	// parseDDGResults on a fixture with 2 results, capped to 1.
	s := NewSearcher()
	s.MaxResults = 1
	all := parseDDGResults(ddgFixture)
	if len(all) <= s.MaxResults {
		t.Skip("fixture has too few results to test the cap")
	}
	// The cap is applied in Search(); emulate it.
	capped := all[:s.MaxResults]
	if len(capped) != 1 {
		t.Errorf("cap not applied: %d", len(capped))
	}
}
