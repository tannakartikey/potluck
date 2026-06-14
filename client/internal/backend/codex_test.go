package backend

import "testing"

func TestParseCodexJSONL(t *testing.T) {
	out := []byte(`{"type":"thread.started","thread_id":"x"}
{"type":"turn.started"}
{"type":"item.completed","item":{"id":"item_0","type":"agent_message","text":"hello world"}}
{"type":"turn.completed","usage":{"input_tokens":10630,"cached_input_tokens":9088,"output_tokens":43,"reasoning_output_tokens":35}}`)

	u, msg := parseCodexJSONL(out)
	if msg != "hello world" {
		t.Errorf("msg = %q, want %q", msg, "hello world")
	}
	if u.InputTokens != 10630 {
		t.Errorf("input = %d, want 10630", u.InputTokens)
	}
	if u.OutputTokens != 78 { // 43 output + 35 reasoning
		t.Errorf("output = %d, want 78", u.OutputTokens)
	}
	if u.Total() != 10708 {
		t.Errorf("total = %d, want 10708", u.Total())
	}
}

func TestParseCodexJSONLPicksLastMessage(t *testing.T) {
	out := []byte(`{"type":"item.completed","item":{"type":"agent_message","text":"draft"}}
{"type":"item.completed","item":{"type":"agent_message","text":"final answer"}}
{"type":"turn.completed","usage":{"input_tokens":1,"output_tokens":2,"reasoning_output_tokens":0}}`)
	_, msg := parseCodexJSONL(out)
	if msg != "final answer" {
		t.Errorf("msg = %q, want last message", msg)
	}
}
