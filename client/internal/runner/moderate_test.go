package runner

import "testing"

func TestParseVerdict(t *testing.T) {
	cases := []struct {
		name        string
		in          string
		wantVerdict string
		wantOK      bool
	}{
		{"clean json", `{"verdict":"accept","reason":"good public task"}`, "accept", true},
		{"prose around json", "Sure, here is my call.\n{\"verdict\": \"reject\", \"reason\": \"needs shell access\"}\nDone.", "reject", true},
		{"uppercase value", `{"verdict":"ESCALATE","reason":"borderline"}`, "escalate", true},
		{"code fence", "```json\n{\"verdict\":\"accept\",\"reason\":\"fine\"}\n```", "accept", true},
		{"field regex fallback", "verdict: reject\nreason: \"spam\"", "reject", true},
		{"no verdict -> not ok", "I think this task is probably fine but I'm not sure.", "", false},
		{"nested braces", `{"meta":{"x":1},"verdict":"accept","reason":"ok {with braces}"}`, "accept", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			v, _, ok := parseVerdict(c.in)
			if ok != c.wantOK {
				t.Fatalf("ok = %v, want %v (verdict=%q)", ok, c.wantOK, v)
			}
			if c.wantOK && v != c.wantVerdict {
				t.Errorf("verdict = %q, want %q", v, c.wantVerdict)
			}
		})
	}
}

func TestParseVerdictReason(t *testing.T) {
	_, reason, ok := parseVerdict(`{"verdict":"reject","reason":"requires running code"}`)
	if !ok || reason != "requires running code" {
		t.Errorf("reason = %q ok=%v, want \"requires running code\" true", reason, ok)
	}
}
