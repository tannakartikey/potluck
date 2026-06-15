// Package broker is Potluck's credential broker for the API-key execution path (v2). It is
// a small local reverse proxy that holds the contributor's REAL provider API key and injects
// it only at the last hop to the provider. The agent CLI is pointed at the broker
// (ANTHROPIC_BASE_URL=http://<broker>) and given ONLY a placeholder key, so the real key is
// never in the agent's environment: a "dump my env" / printenv attack finds only the
// placeholder. This protects the API key even though everything else about the run is
// untrusted. See plans/prelaunch.md §0.2. stdlib-only.
package broker

import (
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync"
)

// DefaultUpstream is the Anthropic API origin the broker forwards to.
const DefaultUpstream = "https://api.anthropic.com"

// DefaultPlaceholder is the dummy key handed to the agent. It is deliberately recognisable
// and obviously not a real secret; the broker swaps it for the real key on the way out.
const DefaultPlaceholder = "sk-ant-PLACEHOLDER-broker-injects-the-real-key"

// Broker forwards agent → provider requests, injecting the real key. It never logs the key.
type Broker struct {
	realKey     string
	placeholder string
	upstream    *url.URL
	bindAddr    string

	ln     net.Listener
	srv    *http.Server
	mu     sync.Mutex
	closed bool
}

// New builds a broker. realKey must be non-empty (fail closed: no key, no broker). upstream
// may be "" for the Anthropic default. bindAddr may be "" for loopback ("127.0.0.1:0"); pass
// "0.0.0.0:PORT" to expose it to an agent running in a sidecar container.
func New(realKey, upstream, bindAddr string) (*Broker, error) {
	if strings.TrimSpace(realKey) == "" {
		return nil, fmt.Errorf("broker: refusing to start without a real API key (fail closed)")
	}
	if upstream == "" {
		upstream = DefaultUpstream
	}
	u, err := url.Parse(upstream)
	if err != nil {
		return nil, fmt.Errorf("broker: bad upstream %q: %w", upstream, err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, fmt.Errorf("broker: upstream must be http(s), got %q", u.Scheme)
	}
	if bindAddr == "" {
		bindAddr = "127.0.0.1:0"
	}
	return &Broker{realKey: realKey, placeholder: DefaultPlaceholder, upstream: u, bindAddr: bindAddr}, nil
}

// Placeholder returns the dummy key the agent should be given.
func (b *Broker) Placeholder() string { return b.placeholder }

// handler builds the reverse proxy. The Director rewrites every request to the upstream and
// replaces the inbound auth headers with the real key — so whatever the agent sends (the
// placeholder, or nothing) is discarded and the real key is injected here, at the edge.
func (b *Broker) handler() http.Handler {
	proxy := &httputil.ReverseProxy{
		Director: func(req *http.Request) {
			req.URL.Scheme = b.upstream.Scheme
			req.URL.Host = b.upstream.Host
			req.Host = b.upstream.Host // correct Host header / TLS SNI for the upstream
			if b.upstream.Path != "" && b.upstream.Path != "/" {
				req.URL.Path = singleJoin(b.upstream.Path, req.URL.Path)
			}
			// Strip whatever the agent sent and inject the real credential at the last hop.
			req.Header.Del("Authorization")
			req.Header.Del("X-Api-Key")
			req.Header.Set("X-Api-Key", b.realKey)
		},
		// ErrorLog must never receive the key; ReverseProxy logs only transport errors here.
		ErrorLog: log.New(io.Discard, "", 0),
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			// Generic message only — never echo headers or the key.
			http.Error(w, "broker: upstream request failed", http.StatusBadGateway)
		},
	}
	return proxy
}

// Start binds the listener and serves in a background goroutine.
func (b *Broker) Start() error {
	ln, err := net.Listen("tcp", b.bindAddr)
	if err != nil {
		return fmt.Errorf("broker: listen %s: %w", b.bindAddr, err)
	}
	b.ln = ln
	b.srv = &http.Server{Handler: b.handler()}
	go func() { _ = b.srv.Serve(ln) }()
	return nil
}

// Addr returns the base URL the agent should use as ANTHROPIC_BASE_URL.
func (b *Broker) Addr() string {
	if b.ln == nil {
		return ""
	}
	return "http://" + b.ln.Addr().String()
}

// Port returns the bound TCP port (useful when bindAddr used :0).
func (b *Broker) Port() int {
	if b.ln == nil {
		return 0
	}
	return b.ln.Addr().(*net.TCPAddr).Port
}

// Close stops the broker.
func (b *Broker) Close() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return nil
	}
	b.closed = true
	if b.srv != nil {
		return b.srv.Close()
	}
	return nil
}

func singleJoin(a, bb string) string {
	return strings.TrimSuffix(a, "/") + "/" + strings.TrimPrefix(bb, "/")
}

// ScrubbedAgentEnv returns the environment to launch the agent with: the host environment
// MINUS any real provider credential, PLUS the broker base URL and a placeholder key. This
// is the function that guarantees the agent process can never read the real key — there is
// nothing to read. (Used for the host execution path; the container path passes the same
// placeholder/URL and simply never sets the real key in the agent container.)
func ScrubbedAgentEnv(hostEnv []string, brokerAddr, placeholder string) []string {
	// Variables that could carry a real provider secret into the agent — all removed.
	strip := map[string]bool{
		"ANTHROPIC_API_KEY":     true,
		"ANTHROPIC_AUTH_TOKEN":  true,
		"ANTHROPIC_BASE_URL":    true, // we set our own below
		"OPENAI_API_KEY":        true,
		"OPENAI_BASE_URL":       true,
		"AWS_ACCESS_KEY_ID":     true,
		"AWS_SECRET_ACCESS_KEY": true,
		"AWS_SESSION_TOKEN":     true,
		"GOOGLE_API_KEY":        true,
		"GEMINI_API_KEY":        true,
	}
	var out []string
	for _, kv := range hostEnv {
		name := kv
		if i := strings.IndexByte(kv, '='); i >= 0 {
			name = kv[:i]
		}
		if strip[name] {
			continue
		}
		out = append(out, kv)
	}
	out = append(out, "ANTHROPIC_BASE_URL="+brokerAddr)
	out = append(out, "ANTHROPIC_API_KEY="+placeholder)
	return out
}
