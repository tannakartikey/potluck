// Package backend defines the pluggable execution adapter the runner uses to run
// a single task on the contributor's own model. v0 ships one adapter (Claude Code);
// the interface keeps Codex / raw API / custom-command as drop-in additions.
package backend

import (
	"context"
	"time"
)

type Usage struct {
	InputTokens  int
	OutputTokens int
	CostUSD      float64
}

func (u Usage) Total() int { return u.InputTokens + u.OutputTokens }

type Request struct {
	System  string        // fixed safety/role system prompt (replaces the agent default)
	Prompt  string        // the untrusted task text, as data
	Model   string        // alias (haiku|sonnet|opus) or full id; "" = backend default
	MaxUSD  float64       // optional hard dollar cap for this run; 0 = none
	Timeout time.Duration // wall-clock cap
}

type Response struct {
	Text          string
	Usage         Usage
	ReportedModel string // the model that actually ran (self-reported; not verified)
	Raw           []byte // full backend JSON, for the reserved results.usage column later
}

type Backend interface {
	Name() string
	Run(ctx context.Context, req Request) (*Response, error)
}

// UsageInfo is a contributor's plan-usage snapshot (e.g. Claude Code's /usage).
type UsageInfo struct {
	SessionPct    int    // rolling 5-hour window, % used
	WeekPct       int    // weekly (all models), % used
	SessionResets string // human-readable reset time
	WeekResets    string
	Raw           string
}

// UsageReporter is an optional capability: a backend that can report the
// contributor's current plan usage, so the runner can stop before a limit.
type UsageReporter interface {
	Usage(ctx context.Context) (*UsageInfo, error)
}
