package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	"github.com/tannakartikey/potluck/client/internal/broker"
	"github.com/tannakartikey/potluck/client/internal/hook"
	"github.com/tannakartikey/potluck/client/internal/mcp"
	"github.com/tannakartikey/potluck/client/internal/tools"
)

// These __-prefixed subcommands are INTERNAL plumbing for the v2 curated-tools sandbox, not
// user commands (so they're omitted from usage()). The single potluck binary plays three
// roles inside the sandbox: the MCP curated-tools server, the PreToolUse deny hook, and the
// credential broker. Shipping them in one binary means the locked-down container needs only
// this static binary (plus the agent CLI) — no extra artifacts.

// cmdHook: PreToolUse deny-all-except-curated hook. Reads the hook JSON on stdin, writes the
// decision on stdout (+ a stderr reason on deny), and exits 0 (allow) or 2 (deny / fail-closed).
func cmdHook(args []string) {
	fs := flag.NewFlagSet("__hook", flag.ContinueOnError)
	allowCSV := fs.String("allow", "", "comma-separated extra allowed tool names (default: curated set)")
	_ = fs.Parse(args)

	input, _ := io.ReadAll(os.Stdin)
	allowed := hook.CuratedTools
	if extra := splitCSV(*allowCSV); len(extra) > 0 {
		allowed = append(append([]string{}, hook.CuratedTools...), extra...)
	}
	stdout, stderr, code := hook.Decide(input, allowed)
	if len(stdout) > 0 {
		os.Stdout.Write(stdout)
		os.Stdout.Write([]byte("\n"))
	}
	if len(stderr) > 0 {
		os.Stderr.Write(stderr)
	}
	os.Exit(code)
}

// cmdToolsServer: the MCP stdio server exposing exactly fetch_url + read_document. The host
// allowlist and document directory come from flags or env (POTLUCK_FETCH_ALLOW / POTLUCK_DOC_DIR),
// so the runner can configure a per-task allowlist when it spawns this inside the sandbox.
func cmdToolsServer(args []string) {
	fs := flag.NewFlagSet("__tools-server", flag.ContinueOnError)
	allowCSV := fs.String("allow", os.Getenv("POTLUCK_FETCH_ALLOW"), "comma-separated fetch_url host allowlist")
	docDir := fs.String("doc-dir", os.Getenv("POTLUCK_DOC_DIR"), "read_document base directory")
	if err := fs.Parse(args); err != nil {
		os.Exit(2)
	}
	fetcher := tools.NewFetcher(tools.NewAllowlist(splitCSV(*allowCSV)))
	var reader *tools.Reader
	if *docDir != "" {
		reader = tools.NewReader(*docDir)
	}
	srv := mcp.NewServer(fetcher, reader)
	if err := srv.Serve(os.Stdin, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "tools-server:", err)
		os.Exit(1)
	}
}

// cmdBroker: run the credential broker as a long-lived process (used by the sidecar-container
// egress model). The REAL key is read from ANTHROPIC_API_KEY in THIS process's env; it is
// never written anywhere the agent can read. Prints "BROKER_ADDR=<url>" then serves until
// signalled.
func cmdBroker(args []string) {
	fs := flag.NewFlagSet("__broker", flag.ContinueOnError)
	addr := fs.String("addr", "127.0.0.1:0", "bind address (use 0.0.0.0:PORT for a sidecar container)")
	upstream := fs.String("upstream", broker.DefaultUpstream, "provider API origin to forward to")
	if err := fs.Parse(args); err != nil {
		os.Exit(2)
	}
	realKey := os.Getenv("ANTHROPIC_API_KEY")
	b, err := broker.New(realKey, *upstream, *addr)
	if err != nil {
		fmt.Fprintln(os.Stderr, "broker:", err)
		os.Exit(1)
	}
	if err := b.Start(); err != nil {
		fmt.Fprintln(os.Stderr, "broker:", err)
		os.Exit(1)
	}
	defer b.Close()
	fmt.Printf("BROKER_ADDR=%s\n", b.Addr())

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	<-ctx.Done()
}
