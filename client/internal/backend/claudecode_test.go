package backend

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestParseUsage(t *testing.T) {
	s := "You are currently using your subscription to power your Claude Code usage\n\n" +
		"Current session: 8% used · resets Jun 14 at 7:09pm (Asia/Calcutta)\n" +
		"Current week (all models): 4% used · resets Jun 15 at 11:29am (Asia/Calcutta)\n" +
		"Current week (Sonnet only): 0% used · resets Jun 15 at 11:30am (Asia/Calcutta)\n"
	u := parseUsage(s)
	if u.SessionPct != 8 {
		t.Errorf("session = %d, want 8", u.SessionPct)
	}
	if u.WeekPct != 4 {
		t.Errorf("week = %d, want 4", u.WeekPct)
	}
	if !strings.Contains(u.SessionResets, "7:09pm") {
		t.Errorf("session resets = %q", u.SessionResets)
	}
	if !strings.Contains(u.WeekResets, "11:29am") {
		t.Errorf("week resets = %q", u.WeekResets)
	}
}

func TestDominantModelByCost(t *testing.T) {
	// A cheap side-call (haiku) has MORE output tokens but LESS cost than the main
	// model (opus); cost is the right discriminator.
	m := map[string]ccModelUsage{
		"claude-haiku-4-5-20251001": {OutputTokens: 12, CostUSD: 0.0006},
		"claude-opus-4-8[1m]":       {OutputTokens: 5, CostUSD: 0.0464},
	}
	if got := dominantModel(m); got != "claude-opus-4-8[1m]" {
		t.Fatalf("dominantModel = %q, want the highest-cost model", got)
	}
	if dominantModel(map[string]ccModelUsage{}) != "" {
		t.Error("empty modelUsage should yield empty string")
	}
}

func TestParseClaudeJSON(t *testing.T) {
	raw := []byte(`{"type":"result","subtype":"success","is_error":false,"result":"hello world","total_cost_usd":0.047,"usage":{"input_tokens":2733,"output_tokens":5},"modelUsage":{"claude-haiku-4-5-20251001":{"inputTokens":508,"outputTokens":12,"costUSD":0.0006}}}`)
	var r ccResult
	if err := json.Unmarshal(raw, &r); err != nil {
		t.Fatal(err)
	}
	if r.Result != "hello world" {
		t.Errorf("result = %q", r.Result)
	}
	if r.Usage.InputTokens != 2733 || r.Usage.OutputTokens != 5 {
		t.Errorf("usage = %+v", r.Usage)
	}
	if r.TotalCostUSD != 0.047 {
		t.Errorf("cost = %v", r.TotalCostUSD)
	}
	if r.IsError {
		t.Error("should not be flagged as error")
	}
}

func TestUsageTotal(t *testing.T) {
	if got := (Usage{InputTokens: 100, OutputTokens: 25}).Total(); got != 125 {
		t.Errorf("Total() = %d, want 125", got)
	}
}
