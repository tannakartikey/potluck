// Package tools implements Potluck's CURATED, project-controlled tool surface for the
// Phase-2 (v2) sandbox: a small, hardened set of capabilities the agent may use INSTEAD
// of raw shell/file/network. Today: fetch_url (SSRF-safe, allowlisted) and read_document
// (local text/PDF extraction). Everything here is stdlib-only and the security-critical
// paths are exhaustively unit-tested — per the project's prime directive, a security
// property that isn't tested doesn't exist (see plans/prelaunch.md §2, §7).
package tools

import (
	"fmt"
	"net"
	"strings"
)

// blockedCIDRs are network ranges fetch_url must NEVER connect to, beyond the ones the
// stdlib net.IP helpers already classify. These close the SSRF holes the standard
// helpers miss: cloud metadata is link-local (handled), but CGNAT, IPv4-mapped IPv6,
// NAT64, and a few special-use blocks need explicit entries.
var blockedCIDRs = func() []*net.IPNet {
	cidrs := []string{
		"100.64.0.0/10",   // RFC 6598 CGNAT (carrier / cloud-internal)
		"192.0.0.0/24",    // RFC 6890 IETF protocol assignments
		"192.0.2.0/24",    // TEST-NET-1
		"198.18.0.0/15",   // benchmarking
		"198.51.100.0/24", // TEST-NET-2
		"203.0.113.0/24",  // TEST-NET-3
		"64:ff9b::/96",    // NAT64 (can map to private IPv4)
		"100::/64",        // IPv6 discard-only
		"2001:db8::/32",   // IPv6 documentation
	}
	var out []*net.IPNet
	for _, c := range cidrs {
		if _, n, err := net.ParseCIDR(c); err == nil {
			out = append(out, n)
		}
	}
	return out
}()

// isBlockedIP reports whether an IP is one fetch_url must refuse to connect to: loopback,
// private (RFC1918 + ULA), link-local (incl. the 169.254.169.254 cloud-metadata address),
// multicast, unspecified, and the special-use ranges above. This is the heart of the SSRF
// defense and is validated against a large address table in ssrf_test.go.
//
// It normalises IPv4-mapped IPv6 (::ffff:a.b.c.d) to its IPv4 form first, so an attacker
// cannot smuggle 127.0.0.1 past the v4 checks by mapping it into v6.
func isBlockedIP(ip net.IP) bool {
	if ip == nil {
		return true // unparseable → fail closed
	}
	// Normalise IPv4-mapped IPv6 to plain IPv4 so all the v4 rules apply.
	if v4 := ip.To4(); v4 != nil {
		ip = v4
	}
	if ip.IsLoopback() ||
		ip.IsPrivate() || // RFC1918 + RFC4193 ULA (fc00::/7)
		ip.IsLinkLocalUnicast() || // 169.254/16 (metadata) + fe80::/10
		ip.IsLinkLocalMulticast() ||
		ip.IsInterfaceLocalMulticast() ||
		ip.IsMulticast() ||
		ip.IsUnspecified() {
		return true
	}
	for _, n := range blockedCIDRs {
		if n.Contains(ip) {
			return true
		}
	}
	return false
}

// Allowlist is a per-task set of hosts fetch_url may reach. Matching is exact host OR a
// dot-boundary suffix ("example.com" permits "example.com" and "api.example.com" but NOT
// "evil-example.com" or "example.com.attacker.net"). An empty allowlist denies everything
// — fetch_url is default-deny by construction.
type Allowlist struct {
	hosts []string
}

// NewAllowlist normalises and stores the permitted hosts (lower-cased, port/space trimmed).
func NewAllowlist(hosts []string) *Allowlist {
	a := &Allowlist{}
	for _, h := range hosts {
		h = strings.ToLower(strings.TrimSpace(h))
		if h == "" {
			continue
		}
		// tolerate a leading "*." wildcard form by reducing it to the bare domain
		h = strings.TrimPrefix(h, "*.")
		if host, _, err := net.SplitHostPort(h); err == nil {
			h = host
		}
		a.hosts = append(a.hosts, h)
	}
	return a
}

// Empty reports whether the allowlist permits nothing (default-deny).
func (a *Allowlist) Empty() bool { return a == nil || len(a.hosts) == 0 }

// Allows reports whether host is permitted: exact match or dot-boundary subdomain.
func (a *Allowlist) Allows(host string) bool {
	if a == nil {
		return false
	}
	host = strings.ToLower(strings.TrimSpace(host))
	host = strings.TrimSuffix(host, ".") // drop the fully-qualified trailing dot
	for _, h := range a.hosts {
		if host == h || strings.HasSuffix(host, "."+h) {
			return true
		}
	}
	return false
}

// validateHostIPs resolves host and fails closed if it has NO public IP or ANY IP is
// blocked. This is an early, clear-error check; the authoritative TOCTOU-safe enforcement
// is the dial-time Control hook in fetch.go, which re-validates the exact IP being dialed
// (so a DNS rebind between this check and connect is still caught).
func validateHostIPs(host string) error {
	ips, err := net.LookupIP(host)
	if err != nil {
		return fmt.Errorf("cannot resolve host %q: %w", host, err)
	}
	if len(ips) == 0 {
		return fmt.Errorf("host %q resolved to no addresses", host)
	}
	for _, ip := range ips {
		if isBlockedIP(ip) {
			return fmt.Errorf("host %q resolves to a blocked address (%s)", host, ip)
		}
	}
	return nil
}
