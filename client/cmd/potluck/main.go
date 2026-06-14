// Command potluck is the contributor runner: register an identity, then claim →
// run (on your own model, in safe mode) → submit, with an honest token/cost summary.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/tannakartikey/potluck/client/internal/api"
	"github.com/tannakartikey/potluck/client/internal/backend"
	"github.com/tannakartikey/potluck/client/internal/config"
	"github.com/tannakartikey/potluck/client/internal/runner"
)

var version = "v0.0.1-dev"

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	switch os.Args[1] {
	case "register":
		cmdRegister(os.Args[2:])
	case "run":
		cmdRun(os.Args[2:])
	case "search":
		cmdSearch(os.Args[2:])
	case "usage":
		cmdUsage(os.Args[2:])
	case "status":
		cmdStatus(os.Args[2:])
	case "version", "-v", "--version":
		fmt.Println("potluck", version)
	case "help", "-h", "--help":
		usage()
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n\n", os.Args[1])
		usage()
		os.Exit(2)
	}
}

func usage() {
	fmt.Print(`potluck — donate spare AI agent credits to open, public tasks

usage:
  potluck register [--name <handle>]   create your contributor key (one time)
  potluck run [flags]                  claim → run → submit, until --max-tasks or Ctrl-C
  potluck search <query>               full-text search the open task board
  potluck usage                        show your Claude plan usage (session + week)
  potluck status                       show your identity + what you've donated
  potluck version

run flags:
  --backend B       claude-code | codex (default: config / claude-code)
  --topics a,b      only claim tasks with these categories/tags (default: all)
  --budget N        skip tasks needing more than N tokens (default: config / 8000)
  --model M         model: Claude alias (haiku|sonnet|opus) or full id; for codex
                    pass a Codex model (e.g. gpt-5-codex) or omit to use its default
  --max-tasks N     stop after N tasks (default: 0 = until queue empty / Ctrl-C)
  --max-week N      stop when your weekly plan usage reaches N% (Claude Code; 0 = off)
  --max-session N   stop when your 5-hour session usage reaches N% (Claude Code; 0 = off)

spec & docs: https://github.com/tannakartikey/potluck/blob/main/AGENTS.md
`)
}

func cmdRegister(args []string) {
	fs := flag.NewFlagSet("register", flag.ExitOnError)
	name := fs.String("name", "", "display name / handle")
	_ = fs.Parse(args)

	ctx := context.Background()
	cl := api.New()
	if config.HasKey() {
		fmt.Println("note: a key already exists at", config.Dir()+"/credentials — re-registering creates a NEW identity.")
	}
	key, err := config.GenerateKey()
	check(err)
	c, err := cl.Register(ctx, key, *name)
	check(err)
	check(config.SaveKey(key))

	cfg, _ := config.Load()
	cfg.DisplayName = *name
	cfg.ContributorID = c.ID
	check(cfg.Save())

	fmt.Println("✅ registered. Secret key saved to", config.Dir()+"/credentials (mode 600) — keep it private.")
	fmt.Println("   contributor id:", c.ID)
	fmt.Println("   next: potluck run --topics rails,postgres --max-tasks 3")
}

func cmdRun(args []string) {
	fs := flag.NewFlagSet("run", flag.ExitOnError)
	backendName := fs.String("backend", "", "backend: claude-code | codex")
	topics := fs.String("topics", "", "comma-separated categories/tags")
	budget := fs.Int("budget", 0, "skip tasks needing more than N tokens")
	model := fs.String("model", "", "model alias or id")
	maxTasks := fs.Int("max-tasks", 0, "stop after N tasks (0 = until empty / Ctrl-C)")
	maxWeek := fs.Int("max-week", 0, "stop at N% weekly plan usage (0 = off)")
	maxSession := fs.Int("max-session", 0, "stop at N% session (5h) usage (0 = off)")
	_ = fs.Parse(args)

	if !config.HasKey() {
		fmt.Fprintln(os.Stderr, "no key found — run 'potluck register' first.")
		os.Exit(1)
	}
	key, err := config.LoadKey()
	check(err)
	cfg, err := config.Load()
	check(err)

	chosen := pickStr(*backendName, cfg.Backend)
	if chosen == "" {
		chosen = "claude-code"
	}
	var be backend.Backend
	switch chosen {
	case "claude-code":
		be = backend.NewClaudeCode()
	case "codex":
		be = backend.NewCodex()
	default:
		fmt.Fprintf(os.Stderr, "unknown backend %q (supported: claude-code, codex)\n", chosen)
		os.Exit(1)
	}
	if chosen != "claude-code" && (*maxWeek > 0 || *maxSession > 0) {
		fmt.Fprintf(os.Stderr, "note: --max-week/--max-session need plan-usage reporting (Claude Code only); ignored for %s.\n", chosen)
	}

	opts := runner.Options{
		Topics:        splitCSV(*topics),
		BudgetTokens:  pickInt(*budget, cfg.BudgetTokens),
		Model:         resolveModel(chosen, *model, cfg.Model),
		MaxTasks:      *maxTasks,
		MaxWeekPct:    *maxWeek,
		MaxSessionPct: *maxSession,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := runner.Run(ctx, api.New(), be, key, opts); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func cmdSearch(args []string) {
	fs := flag.NewFlagSet("search", flag.ExitOnError)
	limit := fs.Int("limit", 20, "max results")
	_ = fs.Parse(args)
	query := strings.TrimSpace(strings.Join(fs.Args(), " "))

	rows, err := api.New().Search(context.Background(), query, *limit)
	check(err)
	if len(rows) == 0 {
		fmt.Printf("no open tasks match %q\n", query)
		return
	}
	for _, t := range rows {
		fmt.Printf("· %s\n    %s · %s · ~%d tok\n",
			t.Title, orDefault(t.CategorySlug, "-"), orDefault(strings.Join(t.Tags, ", "), "-"), t.TokenBudget)
	}
	fmt.Printf("\n%d open task(s). Work them: potluck run --topics <category-or-tag>\n", len(rows))
}

func cmdUsage(args []string) {
	u, err := backend.NewClaudeCode().Usage(context.Background())
	check(err)
	fmt.Printf("session (5h): %d%% used · resets %s\n", u.SessionPct, orDefault(u.SessionResets, "?"))
	fmt.Printf("week (all):   %d%% used · resets %s\n", u.WeekPct, orDefault(u.WeekResets, "?"))
	fmt.Println("\nTip: potluck run --max-week 90  → donate until 90% of the weekly limit, then stop.")
}

func cmdStatus(args []string) {
	cl := api.New()
	cfg, err := config.Load()
	check(err)
	fmt.Println("backend:", orDefault(cfg.Backend, "claude-code"))
	fmt.Println("model:  ", orDefault(cfg.Model, "haiku"))
	if !config.HasKey() || cfg.ContributorID == "" {
		fmt.Println("not registered yet — run 'potluck register'.")
		return
	}
	fmt.Println("contributor:", cfg.ContributorID, "("+orDefault(cfg.DisplayName, "anonymous")+")")
	count, tokens, err := cl.DonatedStats(context.Background(), cfg.ContributorID)
	if err != nil {
		fmt.Println("(could not load donation stats:", err, ")")
		return
	}
	fmt.Printf("donated: %d artifact(s), %d tokens\n", count, tokens)
}

func check(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func splitCSV(s string) []string {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// resolveModel: an explicit --model wins. Otherwise the config default applies only to
// claude-code (its default is a Claude alias); other backends use their own default so
// we never hand a Claude alias to Codex (or vice-versa).
func resolveModel(backendName, flag, cfg string) string {
	if flag != "" {
		return flag
	}
	if backendName == "claude-code" {
		return cfg
	}
	return ""
}

func pickInt(v, d int) int {
	if v != 0 {
		return v
	}
	return d
}

func pickStr(v, d string) string {
	if v != "" {
		return v
	}
	return d
}

func orDefault(v, d string) string {
	if v != "" {
		return v
	}
	return d
}
