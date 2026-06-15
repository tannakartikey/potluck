package tools

import (
	"net"
	"testing"
)

// TestIsBlockedIP is the core SSRF table: every address class an allowlisted host might
// (maliciously or via rebinding) resolve to must be refused, and ordinary public IPs must
// be permitted. This is the load-bearing security property of fetch_url.
func TestIsBlockedIP(t *testing.T) {
	blocked := []string{
		// loopback
		"127.0.0.1", "127.1.2.3", "::1",
		// the cloud metadata endpoint (the canonical SSRF target) — link-local
		"169.254.169.254", "169.254.0.1", "fe80::1",
		// RFC1918 private
		"10.0.0.1", "10.255.255.255", "172.16.0.1", "172.31.255.255", "192.168.0.1", "192.168.1.1",
		// IPv6 ULA
		"fc00::1", "fd12:3456:789a::1",
		// unspecified
		"0.0.0.0", "::",
		// CGNAT / special-use
		"100.64.0.1", "100.127.255.255", "192.0.0.1", "198.18.0.1", "203.0.113.5",
		// multicast
		"224.0.0.1", "ff02::1",
		// IPv4-mapped IPv6 smuggling 127.0.0.1 / 169.254.169.254 / 10.0.0.1 into v6
		"::ffff:127.0.0.1", "::ffff:169.254.169.254", "::ffff:10.0.0.1",
		// NAT64 mapping a private v4
		"64:ff9b::a00:1",
	}
	for _, s := range blocked {
		ip := net.ParseIP(s)
		if ip == nil {
			t.Fatalf("test bug: %q did not parse", s)
		}
		if !isBlockedIP(ip) {
			t.Errorf("isBlockedIP(%s) = false, want true (SSRF hole)", s)
		}
	}
	// nil must fail closed.
	if !isBlockedIP(nil) {
		t.Error("isBlockedIP(nil) must be true (fail closed)")
	}

	allowed := []string{
		"1.1.1.1", "8.8.8.8", "93.184.216.34", // example.com
		"140.82.112.3",         // github
		"2606:4700:4700::1111", // cloudflare v6
		"2001:4860:4860::8888", // google dns v6
	}
	for _, s := range allowed {
		ip := net.ParseIP(s)
		if ip == nil {
			t.Fatalf("test bug: %q did not parse", s)
		}
		if isBlockedIP(ip) {
			t.Errorf("isBlockedIP(%s) = true, want false (blocked a public IP)", s)
		}
	}
}

// TestAllowlistMatching: exact + dot-boundary subdomain match, and rejects the classic
// suffix-confusion bypasses.
func TestAllowlistMatching(t *testing.T) {
	a := NewAllowlist([]string{"example.com", "Docs.Python.org", " arxiv.org "})

	allow := []string{"example.com", "api.example.com", "a.b.example.com", "docs.python.org", "arxiv.org", "example.com."}
	for _, h := range allow {
		if !a.Allows(h) {
			t.Errorf("Allows(%q) = false, want true", h)
		}
	}
	deny := []string{
		"evil-example.com",         // not a dot-boundary subdomain
		"example.com.attacker.net", // suffix-confusion
		"notexample.com",
		"python.org", // parent of an allowed subdomain is NOT allowed
		"example.org",
		"",
	}
	for _, h := range deny {
		if a.Allows(h) {
			t.Errorf("Allows(%q) = true, want false (allowlist bypass)", h)
		}
	}
}

// TestEmptyAllowlistDeniesAll: default-deny is the whole posture.
func TestEmptyAllowlistDeniesAll(t *testing.T) {
	for _, a := range []*Allowlist{nil, NewAllowlist(nil), NewAllowlist([]string{"", "  "})} {
		if !a.Empty() {
			t.Errorf("allowlist %v should be Empty()", a)
		}
		if a.Allows("example.com") {
			t.Error("an empty allowlist must deny every host")
		}
	}
}
