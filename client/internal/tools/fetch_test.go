package tools

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// relaxLoopback un-blocks ONLY 127.0.0.1/::1 so a test can target an httptest server; every
// other blocked range (private, link-local/metadata, …) still uses the real predicate.
func relaxLoopback(ip net.IP) bool {
	if ip.IsLoopback() {
		return false
	}
	return isBlockedIP(ip)
}

func TestFetchRejectsBeforeAnyConnection(t *testing.T) {
	f := NewFetcher(NewAllowlist([]string{"example.com"}))
	cases := map[string]string{
		"file scheme":          "file:///etc/passwd",
		"gopher scheme":        "gopher://example.com/",
		"non-allowlisted host": "https://evil.test/",
		"literal loopback":     "http://127.0.0.1/",
		"literal metadata":     "http://169.254.169.254/latest/meta-data/",
		"literal private":      "http://10.0.0.1/",
	}
	for name, u := range cases {
		if _, err := f.Fetch(context.Background(), u); err == nil {
			t.Errorf("%s: Fetch(%q) returned nil error, want refusal", name, u)
		}
	}
}

func TestFetchEmptyAllowlistDeniesEverything(t *testing.T) {
	f := NewFetcher(NewAllowlist(nil))
	if _, err := f.Fetch(context.Background(), "https://example.com/"); err == nil {
		t.Error("empty allowlist must deny all fetches")
	}
}

func TestFetchHappyPath(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		fmt.Fprint(w, "hello from the page")
	}))
	defer srv.Close()

	f := NewFetcher(NewAllowlist([]string{"127.0.0.1"}))
	f.blockIP = relaxLoopback
	res, err := f.Fetch(context.Background(), srv.URL+"/page")
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if res.Status != 200 {
		t.Errorf("status = %d, want 200", res.Status)
	}
	if !strings.Contains(res.Body, "hello from the page") {
		t.Errorf("body = %q", res.Body)
	}
	if !strings.HasPrefix(res.ContentType, "text/plain") {
		t.Errorf("content-type = %q", res.ContentType)
	}
}

// TestFetchDialerBlocksLoopbackEvenIfAllowlisted proves the dial-time Control hook is the
// authoritative boundary: with the REAL predicate, connecting to the loopback test server is
// refused even though 127.0.0.1 is in the allowlist (defeats DNS-rebinding to private).
func TestFetchDialerBlocksLoopbackEvenIfAllowlisted(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "should never be read")
	}))
	defer srv.Close()

	f := NewFetcher(NewAllowlist([]string{"127.0.0.1"})) // real blockIP (not relaxed)
	_, err := f.Fetch(context.Background(), srv.URL)
	if err == nil {
		t.Fatal("expected the dialer to refuse the loopback connection")
	}
	if !strings.Contains(err.Error(), "blocked address") {
		t.Errorf("error = %v, want a 'blocked address' dial refusal", err)
	}
}

func TestFetchHTMLReaderMode(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, `<html><head><style>.x{color:red}</style><script>evil()</script></head>`+
			`<body><h1>Heading</h1><p>Real article text here.</p></body></html>`)
	}))
	defer srv.Close()

	f := NewFetcher(NewAllowlist([]string{"127.0.0.1"}))
	f.blockIP = relaxLoopback
	res, err := f.Fetch(context.Background(), srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Body, "Heading") || !strings.Contains(res.Body, "Real article text here.") {
		t.Errorf("reader-mode text missing: %q", res.Body)
	}
	if strings.Contains(res.Body, "<p>") || strings.Contains(res.Body, "evil()") || strings.Contains(res.Body, "color:red") {
		t.Errorf("HTML markup/script/style leaked into reader-mode body: %q", res.Body)
	}
}

func TestFetchSizeCap(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, strings.Repeat("A", 1000))
	}))
	defer srv.Close()

	f := NewFetcher(NewAllowlist([]string{"127.0.0.1"}))
	f.blockIP = relaxLoopback
	f.MaxBytes = 10
	res, err := f.Fetch(context.Background(), srv.URL)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if res.Bytes != 10 || len(res.Body) != 10 {
		t.Errorf("body bytes = %d, want 10 (size cap)", res.Bytes)
	}
	if !res.Truncated {
		t.Error("Truncated should be true when the cap is hit")
	}
}

func TestFetchTimeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(2 * time.Second)
		fmt.Fprint(w, "late")
	}))
	defer srv.Close()

	f := NewFetcher(NewAllowlist([]string{"127.0.0.1"}))
	f.blockIP = relaxLoopback
	f.Timeout = 150 * time.Millisecond
	if _, err := f.Fetch(context.Background(), srv.URL); err == nil {
		t.Error("expected a timeout error")
	}
}

// TestFetchRedirectToMetadataBlocked proves an allowlisted page that 302-redirects to the
// cloud metadata IP is refused — even when the metadata host is ALSO allowlisted — because
// the IP-layer block re-validates every redirect target.
func TestFetchRedirectToMetadataBlocked(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://169.254.169.254/latest/meta-data/", http.StatusFound)
	}))
	defer srv.Close()

	// Allowlist BOTH loopback and the metadata host, and un-block only loopback, so the
	// only thing stopping the redirect is the IP-layer block on 169.254.169.254.
	f := NewFetcher(NewAllowlist([]string{"127.0.0.1", "169.254.169.254"}))
	f.blockIP = relaxLoopback
	_, err := f.Fetch(context.Background(), srv.URL)
	if err == nil {
		t.Fatal("expected the redirect to the metadata IP to be refused")
	}
	if !strings.Contains(err.Error(), "blocked address") {
		t.Errorf("error = %v, want a blocked-address refusal", err)
	}
}

// TestFetchRedirectToNonAllowlistedHostBlocked proves host-allowlist re-validation on redirect.
func TestFetchRedirectToNonAllowlistedHostBlocked(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://elsewhere.example/secret", http.StatusFound)
	}))
	defer srv.Close()

	f := NewFetcher(NewAllowlist([]string{"127.0.0.1"})) // elsewhere.example NOT allowed
	f.blockIP = relaxLoopback
	_, err := f.Fetch(context.Background(), srv.URL)
	if err == nil {
		t.Fatal("expected the redirect to a non-allowlisted host to be refused")
	}
	if !strings.Contains(err.Error(), "redirect") && !strings.Contains(err.Error(), "allowlist") {
		t.Errorf("error = %v, want a redirect/allowlist refusal", err)
	}
}

// TestFetchAllowedRedirectFollowed proves a redirect to an allowlisted, public target is
// followed normally (here, loopback→loopback, both allowlisted + un-blocked).
func TestFetchAllowedRedirectFollowed(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/start", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/dest", http.StatusFound)
	})
	mux.HandleFunc("/dest", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "arrived")
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	f := NewFetcher(NewAllowlist([]string{"127.0.0.1"}))
	f.blockIP = relaxLoopback
	res, err := f.Fetch(context.Background(), srv.URL+"/start")
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if !strings.Contains(res.Body, "arrived") {
		t.Errorf("expected to follow the redirect; body = %q", res.Body)
	}
	if !strings.HasSuffix(res.URL, "/dest") {
		t.Errorf("final URL = %q, want .../dest", res.URL)
	}
}
