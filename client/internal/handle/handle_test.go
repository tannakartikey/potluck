package handle

import (
	"regexp"
	"testing"
)

var pattern = regexp.MustCompile(`^[a-z]+-[a-z]+-\d{3}$`)

func TestGenerateFormat(t *testing.T) {
	for i := 0; i < 200; i++ {
		h := Generate()
		if !pattern.MatchString(h) {
			t.Fatalf("handle %q does not match adjective-noun-NNN", h)
		}
	}
}

func TestGenerateVaries(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 100; i++ {
		seen[Generate()] = true
	}
	if len(seen) < 2 {
		t.Fatalf("expected variety across 100 calls, got %d distinct handle(s)", len(seen))
	}
}
