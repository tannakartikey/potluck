package tools

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"regexp"
	"strings"
)

// web_search is the first research-grade curated tool (see .private/GOAL.md): it lets the agent
// FIND sources, not just fetch a URL it was handed. It queries a search engine through the same
// SSRF-safe fetcher (engine domain allowlisted) and returns structured results the agent can
// then read with fetch_url. Search is comparatively exfil-safe: the query goes to the engine,
// and results come back — there is no attacker-controlled receiver in the loop. stdlib-only.
type Searcher struct {
	fetcher    *Fetcher
	engine     string // search endpoint (DuckDuckGo HTML, no API key required)
	MaxResults int
}

// SearchResult is one structured hit.
type SearchResult struct {
	Title   string `json:"title"`
	URL     string `json:"url"`
	Snippet string `json:"snippet"`
}

const ddgHTMLEndpoint = "https://html.duckduckgo.com/html/"

// researchAllowlist is a curated set of reputable documentation / reference / source-code
// domains. It exists so a research task ("document the X CLI") can autonomously READ docs and
// source without the contributor pre-naming every domain. It stays exfil-bounded because
// fetch_url is GET-only and these are sites where an attacker cannot read request logs or have
// the agent create attacker-visible records — so a GET cannot ship data to an attacker. Opt-in
// via `potluck run --research`. Dot-boundary matching means e.g. "github.com" covers
// "api.github.com"; "githubusercontent.com" covers "raw.githubusercontent.com".
var researchAllowlist = []string{
	// source hosting
	"github.com", "githubusercontent.com", "github.io", "gitlab.com", "bitbucket.org",
	"codeberg.org", "sourceforge.net", "savannah.gnu.org",
	// package registries
	"pypi.org", "npmjs.com", "pkg.go.dev", "crates.io", "docs.rs", "rubygems.org",
	"packagist.org", "nuget.org", "hex.pm", "pub.dev",
	// language / framework docs
	"python.org", "golang.org", "go.dev", "rust-lang.org", "developer.mozilla.org",
	"nodejs.org", "ruby-lang.org", "ruby-doc.org", "kotlinlang.org", "dart.dev", "php.net",
	"oracle.com", "scala-lang.org", "haskell.org",
	// Q&A / wikis / reference
	"stackoverflow.com", "stackexchange.com", "superuser.com", "serverfault.com",
	"askubuntu.com", "wikipedia.org", "wikimedia.org", "w3.org", "ietf.org", "rfc-editor.org",
	// doc hosting
	"readthedocs.io", "readthedocs.org", "gitbook.io",
	// AI providers
	"developers.openai.com", "platform.openai.com", "openai.com", "anthropic.com", "ai.google.dev",
	"huggingface.co",
	// infra / cloud docs
	"kubernetes.io", "docker.com", "hashicorp.com", "terraform.io", "aws.amazon.com",
	"cloud.google.com", "microsoft.com", "arxiv.org",
}

// ResearchAllowlist returns a copy of the curated reputable-source domain list (see above).
func ResearchAllowlist() []string {
	out := make([]string, len(researchAllowlist))
	copy(out, researchAllowlist)
	return out
}

// NewSearcher builds a searcher whose fetcher is allowlisted for the search engine only.
// The engine defaults to DuckDuckGo's keyless HTML endpoint (BEST-EFFORT — it rate-limits /
// bot-blocks, so results can come back empty; that degrades gracefully, never crashes). For
// reliable search, point POTLUCK_SEARCH_URL at a self-hosted SearXNG or another HTML endpoint
// with the same result markup, or wire a keyed API. The engine host is auto-allowlisted.
func NewSearcher() *Searcher {
	engine := ddgHTMLEndpoint
	if e := os.Getenv("POTLUCK_SEARCH_URL"); e != "" {
		engine = e
	}
	allow := []string{"duckduckgo.com"}
	if u, err := url.Parse(engine); err == nil && u.Hostname() != "" {
		allow = append(allow, u.Hostname())
	}
	return &Searcher{
		fetcher:    NewFetcher(NewAllowlist(allow)),
		engine:     engine,
		MaxResults: 10,
	}
}

// Search runs the query and returns parsed results (best-effort; an engine HTML change degrades
// to fewer/no results, never a crash).
func (s *Searcher) Search(ctx context.Context, query string) ([]SearchResult, error) {
	q := strings.TrimSpace(query)
	if q == "" {
		return nil, fmt.Errorf("web_search: empty query")
	}
	u := s.engine + "?q=" + url.QueryEscape(q)
	res, err := s.fetcher.Fetch(ctx, u)
	if err != nil {
		return nil, fmt.Errorf("web_search: %w", err)
	}
	out := parseDDGResults(res.Body)
	if s.MaxResults > 0 && len(out) > s.MaxResults {
		out = out[:s.MaxResults]
	}
	return out, nil
}

var (
	reResultLink    = regexp.MustCompile(`(?is)<a[^>]*class="[^"]*result__a[^"]*"[^>]*href="([^"]+)"[^>]*>(.*?)</a>`)
	reResultSnippet = regexp.MustCompile(`(?is)<a[^>]*class="[^"]*result__snippet[^"]*"[^>]*>(.*?)</a>`)
)

// parseDDGResults extracts results from DuckDuckGo's HTML results page. DDG wraps each result
// URL in a /l/?uddg=<url-encoded-target> redirect; we decode that back to the real URL.
func parseDDGResults(html string) []SearchResult {
	links := reResultLink.FindAllStringSubmatch(html, -1)
	snips := reResultSnippet.FindAllStringSubmatch(html, -1)

	var out []SearchResult
	for i, m := range links {
		target := decodeDDGHref(m[1])
		if target == "" {
			continue
		}
		r := SearchResult{
			Title: collapseSpaces(unescapeEntities(stripTags(m[2]))),
			URL:   target,
		}
		if i < len(snips) {
			r.Snippet = collapseSpaces(unescapeEntities(stripTags(snips[i][1])))
		}
		out = append(out, r)
	}
	return out
}

// decodeDDGHref turns "//duckduckgo.com/l/?uddg=https%3A%2F%2Fexample.com%2F&rut=…" (or a plain
// https URL) into the real target URL. Returns "" if it can't recover an http(s) URL.
func decodeDDGHref(href string) string {
	href = unescapeEntities(href)
	if i := strings.Index(href, "uddg="); i >= 0 {
		raw := href[i+len("uddg="):]
		if amp := strings.IndexByte(raw, '&'); amp >= 0 {
			raw = raw[:amp]
		}
		if dec, err := url.QueryUnescape(raw); err == nil {
			href = dec
		}
	}
	if strings.HasPrefix(href, "http://") || strings.HasPrefix(href, "https://") {
		return href
	}
	return ""
}

// stripTags removes any HTML tags from a fragment (DDG bolds query terms in titles/snippets).
func stripTags(s string) string {
	var b strings.Builder
	inTag := false
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '<':
			inTag = true
		case '>':
			inTag = false
		default:
			if !inTag {
				b.WriteByte(s[i])
			}
		}
	}
	return b.String()
}
