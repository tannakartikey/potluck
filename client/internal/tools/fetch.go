package tools

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"syscall"
	"time"
)

// Fetch limits. Conservative caps that bound a single read-only fetch: enough to read a
// page or a document, not enough to be used for bulk exfiltration or to exhaust memory.
const (
	DefaultMaxBytes   = 5 << 20 // 5 MiB response cap
	DefaultTimeout    = 20 * time.Second
	DefaultMaxRedirts = 5
)

// Fetcher performs SSRF-safe, allowlisted, capped HTTP(S) GETs. It is the implementation
// behind the fetch_url curated tool. Construct one per task with the task's allowlist; an
// empty allowlist denies everything (default-deny).
type Fetcher struct {
	Allow      *Allowlist
	MaxBytes   int64
	Timeout    time.Duration
	MaxRedirts int

	// blockIP decides whether an IP must be refused. Defaults to the real isBlockedIP;
	// a test may relax it to target a loopback httptest server. It is the ONLY seam —
	// production always uses isBlockedIP — and red-team tests exercise the real predicate.
	blockIP func(net.IP) bool
	client  *http.Client
}

func (f *Fetcher) ipBlocked(ip net.IP) bool {
	if f.blockIP != nil {
		return f.blockIP(ip)
	}
	return isBlockedIP(ip)
}

// FetchResult is the structured tool output.
type FetchResult struct {
	URL         string `json:"url"`          // final URL after redirects
	Status      int    `json:"status"`       // HTTP status code
	ContentType string `json:"content_type"` // response Content-Type
	Body        string `json:"body"`         // response body (UTF-8, truncated to MaxBytes)
	Truncated   bool   `json:"truncated"`    // true if the body hit the size cap
	Bytes       int    `json:"bytes"`        // number of body bytes returned
}

// NewFetcher builds a Fetcher whose HTTP transport refuses to connect to any blocked IP —
// enforced at DIAL time (post-DNS) via a Control hook, which is the TOCTOU-safe point: it
// validates the literal IP the kernel is about to connect to, defeating DNS-rebinding where
// a host first resolves public, then private.
func NewFetcher(allow *Allowlist) *Fetcher {
	f := &Fetcher{Allow: allow, MaxBytes: DefaultMaxBytes, Timeout: DefaultTimeout, MaxRedirts: DefaultMaxRedirts, blockIP: isBlockedIP}
	dialer := &net.Dialer{
		Timeout:   10 * time.Second,
		KeepAlive: -1, // no connection reuse: every request re-resolves + re-validates
		Control: func(network, address string, _ syscall.RawConn) error {
			// address is the post-resolution "ip:port" the dialer is about to connect to.
			if network != "tcp4" && network != "tcp6" && network != "tcp" {
				return fmt.Errorf("fetch_url: refusing non-tcp network %q", network)
			}
			host, _, err := net.SplitHostPort(address)
			if err != nil {
				return fmt.Errorf("fetch_url: bad dial address %q: %w", address, err)
			}
			ip := net.ParseIP(host)
			if ip == nil {
				return fmt.Errorf("fetch_url: dial address is not a literal IP: %q", host)
			}
			if f.ipBlocked(ip) {
				return fmt.Errorf("fetch_url: refusing to connect to blocked address %s", ip)
			}
			return nil
		},
	}
	transport := &http.Transport{
		DialContext:           dialer.DialContext,
		DisableKeepAlives:     true,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: 15 * time.Second,
		MaxIdleConns:          0,
		Proxy:                 nil, // never honour HTTP(S)_PROXY: the dialer is the boundary
	}
	f.client = &http.Client{
		Transport: transport,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= f.MaxRedirts {
				return fmt.Errorf("fetch_url: too many redirects (>%d)", f.MaxRedirts)
			}
			// Re-validate every redirect target: scheme + host allowlist. The dial Control
			// hook independently blocks any private IP on the redirected connection, so a
			// redirect to 127.0.0.1 / 169.254.169.254 is refused at both layers.
			if err := f.checkURL(req.URL); err != nil {
				return fmt.Errorf("fetch_url: refused redirect: %w", err)
			}
			return nil
		},
	}
	return f
}

// checkURL validates scheme + host allowlist for a parsed URL (no DNS).
func (f *Fetcher) checkURL(u *url.URL) error {
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("only http/https URLs are allowed, got %q", u.Scheme)
	}
	host := u.Hostname()
	if host == "" {
		return errors.New("URL has no host")
	}
	// A literal IP in the URL must itself be public (and still allowlisted by IP/host).
	if ip := net.ParseIP(host); ip != nil && f.ipBlocked(ip) {
		return fmt.Errorf("URL points at a blocked address %s", ip)
	}
	if f.Allow.Empty() {
		return errors.New("no fetch allowlist configured (default-deny): this task may not fetch any URL")
	}
	if !f.Allow.Allows(host) {
		return fmt.Errorf("host %q is not in this task's fetch allowlist", host)
	}
	return nil
}

// Fetch performs a single safe GET. It validates scheme + allowlist, fails closed on any
// blocked-IP resolution, then dials with the IP-validating transport, and returns at most
// MaxBytes of body within Timeout.
func (f *Fetcher) Fetch(ctx context.Context, rawURL string) (*FetchResult, error) {
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return nil, fmt.Errorf("invalid URL: %w", err)
	}
	if err := f.checkURL(u); err != nil {
		return nil, err
	}
	// Early, clear-error resolution check (the dial Control hook is the authoritative one).
	if ip := net.ParseIP(u.Hostname()); ip == nil {
		if err := validateHostIPs(u.Hostname()); err != nil {
			return nil, err
		}
	}

	timeout := f.Timeout
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "potluck-fetch/0.2 (+curated-tool; read-only)")
	req.Header.Set("Accept", "text/*, application/json, application/xhtml+xml, */*;q=0.5")

	resp, err := f.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch failed: %w", err)
	}
	defer resp.Body.Close()

	maxBytes := f.MaxBytes
	if maxBytes <= 0 {
		maxBytes = DefaultMaxBytes
	}
	// Read one extra byte to detect truncation.
	limited := io.LimitReader(resp.Body, maxBytes+1)
	body, err := io.ReadAll(limited)
	if err != nil {
		return nil, fmt.Errorf("reading body: %w", err)
	}
	truncated := int64(len(body)) > maxBytes
	if truncated {
		body = body[:maxBytes]
	}

	return &FetchResult{
		URL:         resp.Request.URL.String(),
		Status:      resp.StatusCode,
		ContentType: resp.Header.Get("Content-Type"),
		Body:        string(body),
		Truncated:   truncated,
		Bytes:       len(body),
	}, nil
}
