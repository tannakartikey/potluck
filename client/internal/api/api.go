// Package api is a thin client for Potluck's Supabase (PostgREST) backend.
// Reads use the public anon key (RLS makes it read-only); writes go through the
// key-gated SECURITY DEFINER RPCs. The secret contributor key travels in the RPC
// body over TLS and is only ever stored server-side as a SHA-256 hash.
package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"time"
)

// Public defaults for the live project. The anon key is RLS-protected (read-only)
// and is published in AGENTS.md / web/config.js, so embedding it here is safe.
// Override either with POTLUCK_SUPABASE_URL / POTLUCK_ANON_KEY.
const (
	defaultURL  = "https://besocrfzgnkxyykzpkqv.supabase.co"
	defaultAnon = "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJpc3MiOiJzdXBhYmFzZSIsInJlZiI6ImJlc29jcmZ6Z25reHl5a3pwa3F2Iiwicm9sZSI6ImFub24iLCJpYXQiOjE3ODEzOTMzMDMsImV4cCI6MjA5Njk2OTMwM30.l4xFN2SiBUvsSv46abx7dYFpM91DL7JF-unOjCSYfQg"
)

type Client struct {
	BaseURL string
	AnonKey string
	HTTP    *http.Client
}

func New() *Client {
	url := os.Getenv("POTLUCK_SUPABASE_URL")
	if url == "" {
		url = defaultURL
	}
	anon := os.Getenv("POTLUCK_ANON_KEY")
	if anon == "" {
		anon = defaultAnon
	}
	return &Client{BaseURL: url, AnonKey: anon, HTTP: &http.Client{Timeout: 30 * time.Second}}
}

// IsProd reports whether the client is pointed at the published prod database (i.e. no
// POTLUCK_SUPABASE_URL override is in effect). Used to label which DB a run targets.
func (c *Client) IsProd() bool { return c.BaseURL == defaultURL }

type Contributor struct {
	ID          string `json:"id"`
	DisplayName string `json:"display_name"`
}

type Subtask struct {
	ID             string          `json:"id"`
	CategorySlug   string          `json:"category_slug"`
	Tags           []string        `json:"tags"`
	Title          string          `json:"title"`
	Prompt         string          `json:"prompt"`
	Acceptance     string          `json:"acceptance"`
	TokenBudget    int             `json:"token_budget"`
	Priority       int             `json:"priority"`
	RequestedModel string          `json:"requested_model"`
	ModelPolicy    string          `json:"model_policy"`
	Attachments    json.RawMessage `json:"attachments"`
	Status         string          `json:"status"`
	SubmittedBy    string          `json:"submitted_by"`
}

type Result struct {
	ID            string `json:"id"`
	ReportedModel string `json:"reported_model"`
}

func (c *Client) rpc(ctx context.Context, fn string, body any) ([]byte, error) {
	buf, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/rest/v1/rpc/"+fn, bytes.NewReader(buf))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("apikey", c.AnonKey)
	req.Header.Set("Authorization", "Bearer "+c.AnonKey)
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("%s: %s: %s", fn, resp.Status, truncate(string(data), 300))
	}
	return data, nil
}

func (c *Client) Register(ctx context.Context, key, displayName string) (*Contributor, error) {
	data, err := c.rpc(ctx, "register_contributor", map[string]any{"p_key": key, "p_display_name": displayName})
	if err != nil {
		return nil, err
	}
	var out Contributor
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, fmt.Errorf("decode register: %w", err)
	}
	return &out, nil
}

// Claim returns the next leased subtask, or nil if the queue is empty for these topics.
func (c *Client) Claim(ctx context.Context, key string, topics []string) (*Subtask, error) {
	body := map[string]any{"p_key": key}
	if len(topics) > 0 {
		body["p_topics"] = topics
	}
	data, err := c.rpc(ctx, "claim_subtask", body)
	if err != nil {
		return nil, err
	}
	if isJSONNull(data) {
		return nil, nil
	}
	var out Subtask
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, fmt.Errorf("decode claim: %w", err)
	}
	if out.ID == "" {
		return nil, nil
	}
	return &out, nil
}

func (c *Client) Submit(ctx context.Context, key, subtaskID, md, reportedModel string, tokenCount int, promptHash string, guardPassed bool) (*Result, error) {
	body := map[string]any{
		"p_key":                 key,
		"p_subtask_id":          subtaskID,
		"p_artifact_md":         md,
		"p_reported_model":      reportedModel,
		"p_token_count":         tokenCount,
		"p_prompt_hash":         promptHash,
		"p_output_guard_passed": guardPassed,
	}
	data, err := c.rpc(ctx, "submit_result", body)
	if err != nil {
		return nil, err
	}
	var out Result
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, fmt.Errorf("decode submit: %w", err)
	}
	return &out, nil
}

// Release returns a leased task to the pool. v0 discards partial work (failed=false
// re-queues as 'open'; failed=true marks it 'failed').
func (c *Client) Release(ctx context.Context, key, subtaskID string, failed bool) error {
	_, err := c.rpc(ctx, "release_lease", map[string]any{"p_key": key, "p_subtask_id": subtaskID, "p_failed": failed})
	return err
}

// DonatedStats sums this contributor's published results via the public read API.
func (c *Client) DonatedStats(ctx context.Context, contributorID string) (count, tokens int, err error) {
	url := fmt.Sprintf("%s/rest/v1/results?contributor_id=eq.%s&select=token_count", c.BaseURL, contributorID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return 0, 0, err
	}
	req.Header.Set("apikey", c.AnonKey)
	req.Header.Set("Authorization", "Bearer "+c.AnonKey)
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return 0, 0, err
	}
	defer resp.Body.Close()
	var rows []struct {
		TokenCount int `json:"token_count"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&rows); err != nil {
		return 0, 0, err
	}
	for _, r := range rows {
		tokens += r.TokenCount
	}
	return len(rows), tokens, nil
}

// Search returns up to `limit` OPEN subtasks matching a free-text query via Postgres
// full-text search (the same path agents use). An empty query lists recent open tasks.
func (c *Client) Search(ctx context.Context, query string, limit int) ([]Subtask, error) {
	if limit <= 0 {
		limit = 20
	}
	q := url.Values{}
	q.Set("status", "eq.open")
	q.Set("select", "id,title,category_slug,tags,token_budget")
	q.Set("order", "created_at.desc")
	q.Set("limit", fmt.Sprintf("%d", limit))
	if query != "" {
		q.Set("search", "wfts(english)."+query) // websearch_to_tsquery: phrases, -exclude
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+"/rest/v1/subtasks?"+q.Encode(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("apikey", c.AnonKey)
	req.Header.Set("Authorization", "Bearer "+c.AnonKey)
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("search: %s: %s", resp.Status, truncate(string(data), 200))
	}
	var rows []Subtask
	if err := json.Unmarshal(data, &rows); err != nil {
		return nil, fmt.Errorf("decode search: %w", err)
	}
	return rows, nil
}

// SubmitTask submits a new task; it lands 'pending' (not claimable) until an AI moderator
// accepts it. Returns the created task.
func (c *Client) SubmitTask(ctx context.Context, key, title, prompt, acceptance, category string, tags []string, budget int) (*Subtask, error) {
	body := map[string]any{
		"p_key":           key,
		"p_title":         title,
		"p_prompt":        prompt,
		"p_acceptance":    acceptance,
		"p_category_slug": category,
		"p_tags":          tags,
		"p_token_budget":  budget,
	}
	data, err := c.rpc(ctx, "submit_task", body)
	if err != nil {
		return nil, err
	}
	var out Subtask
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, fmt.Errorf("decode submit_task: %w", err)
	}
	return &out, nil
}

// ModerationQueue returns up to `limit` tasks awaiting moderation (status 'pending', plus
// 'needs_review' if includeEscalated). Reads via the public anon key (RLS allows SELECT).
// excludeContributor, when set, drops that contributor's own submissions (they cannot
// moderate themselves — moderate_task rejects it), keeping the queue actionable.
func (c *Client) ModerationQueue(ctx context.Context, limit int, includeEscalated bool, excludeContributor string) ([]Subtask, error) {
	if limit <= 0 {
		limit = 20
	}
	q := url.Values{}
	if includeEscalated {
		q.Set("status", "in.(pending,needs_review)")
	} else {
		q.Set("status", "eq.pending")
	}
	q.Set("select", "id,title,prompt,acceptance,category_slug,tags,token_budget,status,submitted_by")
	q.Set("order", "created_at.asc") // oldest first — fairest moderation order
	q.Set("limit", fmt.Sprintf("%d", limit))
	if excludeContributor != "" {
		q.Set("submitted_by", "neq."+excludeContributor)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+"/rest/v1/subtasks?"+q.Encode(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("apikey", c.AnonKey)
	req.Header.Set("Authorization", "Bearer "+c.AnonKey)
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("moderation queue: %s: %s", resp.Status, truncate(string(data), 200))
	}
	var rows []Subtask
	if err := json.Unmarshal(data, &rows); err != nil {
		return nil, fmt.Errorf("decode moderation queue: %w", err)
	}
	return rows, nil
}

// Moderate records an AI moderator's verdict on a pending task via the key-gated RPC:
// accept → 'open' (claimable), reject → 'rejected', escalate → 'needs_review'. The RPC
// rejects moderating your own submission.
func (c *Client) Moderate(ctx context.Context, key, subtaskID, verdict, note string) (*Subtask, error) {
	data, err := c.rpc(ctx, "moderate_task", map[string]any{
		"p_key": key, "p_subtask_id": subtaskID, "p_verdict": verdict, "p_note": note,
	})
	if err != nil {
		return nil, err
	}
	var out Subtask
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, fmt.Errorf("decode moderate: %w", err)
	}
	return &out, nil
}

func isJSONNull(b []byte) bool {
	s := bytes.TrimSpace(b)
	return len(s) == 0 || string(s) == "null"
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n] + "…"
	}
	return s
}
