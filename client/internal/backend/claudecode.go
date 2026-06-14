package backend

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
)

// ClaudeCode runs a task via the Claude Code CLI in headless, no-tools "safe mode":
//
//	claude -p <prompt> --output-format json --allowed-tools "" --system-prompt <safety> [--model M] [--max-budget-usd X]
//
// SAFE MODE = empty allow-list (no Bash/Edit/Read/WebFetch/etc.), a project-controlled
// system prompt that replaces the agent default, and execution in a temp dir so no
// local CLAUDE.md / project files are auto-included. (Known v0 limitation: the user's
// global ~/.claude memory may still load; hardening that is tracked in the threat model.)
type ClaudeCode struct {
	Bin    string
	Docker *DockerConfig // when set, run the CLI inside a locked-down container (#23)
}

func NewClaudeCode() *ClaudeCode { return &ClaudeCode{Bin: "claude"} }

func (c *ClaudeCode) Name() string { return "claude-code" }

type ccModelUsage struct {
	InputTokens  int     `json:"inputTokens"`
	OutputTokens int     `json:"outputTokens"`
	CostUSD      float64 `json:"costUSD"`
}

type ccResult struct {
	Subtype      string  `json:"subtype"`
	IsError      bool    `json:"is_error"`
	Result       string  `json:"result"`
	TotalCostUSD float64 `json:"total_cost_usd"`
	Usage        struct {
		InputTokens  int `json:"input_tokens"`
		OutputTokens int `json:"output_tokens"`
	} `json:"usage"`
	ModelUsage map[string]ccModelUsage `json:"modelUsage"`
}

func (c *ClaudeCode) Run(ctx context.Context, req Request) (*Response, error) {
	if req.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, req.Timeout)
		defer cancel()
	}

	args := []string{
		"-p", req.Prompt,
		"--output-format", "json",
		"--allowed-tools", "", // SAFE MODE: empty allow-list => no tools
	}
	if req.System != "" {
		args = append(args, "--system-prompt", req.System)
	}
	if req.Model != "" {
		args = append(args, "--model", req.Model)
	}
	if req.MaxUSD > 0 {
		args = append(args, "--max-budget-usd", fmt.Sprintf("%.4f", req.MaxUSD))
	}

	prog, runArgs := wrapExec(c.Docker, c.Bin, args)
	cmd := exec.CommandContext(ctx, prog, runArgs...)
	if c.Docker == nil {
		cmd.Dir = os.TempDir() // host: avoid auto-discovering a local CLAUDE.md / project files
	}
	// In container mode WORKDIR is an empty tmpfs, so there are no local files to discover.
	out, err := cmd.Output()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			return nil, fmt.Errorf("claude exited: %v: %s", err, strings.TrimSpace(string(ee.Stderr)))
		}
		return nil, fmt.Errorf("claude: %w", err)
	}

	var r ccResult
	if err := json.Unmarshal(out, &r); err != nil {
		return nil, fmt.Errorf("parse claude json: %w", err)
	}
	if r.IsError || (r.Subtype != "" && r.Subtype != "success") {
		return nil, fmt.Errorf("claude returned error (subtype=%q)", r.Subtype)
	}

	reported := dominantModel(r.ModelUsage)
	if reported == "" {
		reported = req.Model
	}
	return &Response{
		Text:          r.Result,
		Usage:         Usage{InputTokens: r.Usage.InputTokens, OutputTokens: r.Usage.OutputTokens, CostUSD: r.TotalCostUSD},
		ReportedModel: reported,
		Raw:           out,
	}, nil
}

// dominantModel picks the model that did the bulk of the work by cost — robust to
// small side-calls (e.g. a cheap title-generation request) that Claude Code makes.
func dominantModel(m map[string]ccModelUsage) string {
	best := ""
	bestCost := -1.0
	for k, v := range m {
		if v.CostUSD > bestCost {
			bestCost = v.CostUSD
			best = k
		}
	}
	return best
}

var (
	reUsageSession = regexp.MustCompile(`(?mi)^current session:\s*(\d+)% used.*resets\s*(.+)$`)
	reUsageWeek    = regexp.MustCompile(`(?mi)^current week \(all models\):\s*(\d+)% used.*resets\s*(.+)$`)
)

// Usage runs `claude -p "/usage"` and parses the contributor's plan-usage snapshot
// (session = rolling 5-hour window; week = weekly all-models). This is what makes a
// "run until my limit, but don't touch next week" mode possible.
func (c *ClaudeCode) Usage(ctx context.Context) (*UsageInfo, error) {
	cmd := exec.CommandContext(ctx, c.Bin, "-p", "/usage")
	cmd.Dir = os.TempDir()
	cmd.Stdin = nil
	out, err := cmd.Output()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			return nil, fmt.Errorf("claude /usage: %v: %s", err, strings.TrimSpace(string(ee.Stderr)))
		}
		return nil, fmt.Errorf("claude /usage: %w", err)
	}
	return parseUsage(string(out)), nil
}

func parseUsage(s string) *UsageInfo {
	u := &UsageInfo{Raw: s}
	if m := reUsageSession.FindStringSubmatch(s); m != nil {
		u.SessionPct, _ = strconv.Atoi(m[1])
		u.SessionResets = strings.TrimSpace(m[2])
	}
	if m := reUsageWeek.FindStringSubmatch(s); m != nil {
		u.WeekPct, _ = strconv.Atoi(m[1])
		u.WeekResets = strings.TrimSpace(m[2])
	}
	return u
}
