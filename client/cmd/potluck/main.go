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
	case "moderate":
		cmdModerate(os.Args[2:])
	case "grant-moderator":
		cmdGrantModerator(os.Args[2:])
	case "search":
		cmdSearch(os.Args[2:])
	case "submit":
		cmdSubmit(os.Args[2:])
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
  potluck moderate [flags]             AI-moderate submitted (pending) tasks → accept/reject/escalate
  potluck grant-moderator --contributor ID   (admin) grant/revoke moderator trust
  potluck search <query>               full-text search the open task board
  potluck submit --title T --prompt P  submit a task (lands 'pending' until AI-moderated)
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
  --watch           when the queue is empty, wait and re-poll instead of exiting
  --poll N          --watch poll interval in seconds (default 15)
  --max-week N      stop when your weekly plan usage reaches N% (Claude Code; 0 = off)
  --max-session N   stop when your 5-hour session usage reaches N% (Claude Code; 0 = off)
  --container       run each task in a locked-down Docker container (recommended;
                    mounts ONLY your auth file, never your session history)
  --image NAME      container image (default potluck-runner:latest)
  --docker-memory   container memory limit (default 2g)
  --docker-cpus     container CPU limit (default 2)

moderate flags:
  --backend B       claude-code | codex (default: config / claude-code)
  --model M         model alias or id
  --max-tasks N     stop after N tasks (default: 0 = whole queue)
  --include-escalated  also re-examine 'needs_review' tasks
  --dry-run         print verdicts but don't apply them
  --container       run the moderator agent in a locked-down container

submit flags:
  --title, --prompt (required), --acceptance, --category, --tags a,b, --budget N

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
	watch := fs.Bool("watch", false, "wait and re-poll when the queue is empty")
	poll := fs.Int("poll", 15, "poll interval in seconds for --watch")
	maxWeek := fs.Int("max-week", 0, "stop at N% weekly plan usage (0 = off)")
	maxSession := fs.Int("max-session", 0, "stop at N% session (5h) usage (0 = off)")
	container := fs.Bool("container", false, "run each task inside a locked-down Docker container (recommended)")
	image := fs.String("image", "", "container image to use (default potluck-runner:latest)")
	dockerMem := fs.String("docker-memory", "2g", "container memory limit (with --container)")
	dockerCPUs := fs.String("docker-cpus", "2", "container CPU limit (with --container)")
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
	be := buildBackend(chosen, *container, *image, *dockerMem, *dockerCPUs)
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
		Watch:         *watch,
		PollSeconds:   *poll,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := runner.Run(ctx, api.New(), be, key, opts); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

// buildBackend constructs the chosen execution backend, optionally wrapped in the
// locked-down Docker container (shared by `run` and `moderate`). Exits with a clear
// message if container mode is requested without usable credentials.
func buildBackend(chosen string, container bool, image, dockerMem, dockerCPUs string) backend.Backend {
	var dcfg *backend.DockerConfig
	if container {
		home, _ := os.UserHomeDir()
		mounts, env := backend.AuthMountsFor(chosen, home)
		if !backend.HasContainerAuth(chosen, home, mounts, env) {
			fmt.Fprintf(os.Stderr, "container mode needs your %s credentials, but none were found.\n", chosen)
			if chosen == "claude-code" {
				fmt.Fprintln(os.Stderr, "  set ANTHROPIC_API_KEY, or use a Claude install that writes ~/.claude/.credentials.json.")
			} else {
				fmt.Fprintln(os.Stderr, "  run `codex login` (writes ~/.codex/auth.json) or set OPENAI_API_KEY.")
			}
			os.Exit(1)
		}
		dcfg = &backend.DockerConfig{Image: image, Mounts: mounts, Env: env, Memory: dockerMem, CPUs: dockerCPUs}
	}
	switch chosen {
	case "claude-code":
		return &backend.ClaudeCode{Bin: "claude", Docker: dcfg}
	case "codex":
		return &backend.Codex{Bin: "codex", Docker: dcfg}
	default:
		fmt.Fprintf(os.Stderr, "unknown backend %q (supported: claude-code, codex)\n", chosen)
		os.Exit(1)
		return nil
	}
}

func cmdModerate(args []string) {
	fs := flag.NewFlagSet("moderate", flag.ExitOnError)
	backendName := fs.String("backend", "", "backend: claude-code | codex")
	model := fs.String("model", "", "model alias or id")
	maxTasks := fs.Int("max-tasks", 0, "stop after N tasks (0 = whole queue)")
	includeEscalated := fs.Bool("include-escalated", false, "also re-examine 'needs_review' tasks")
	dryRun := fs.Bool("dry-run", false, "print verdicts but don't apply them")
	container := fs.Bool("container", false, "run the moderator agent inside a locked-down Docker container")
	image := fs.String("image", "", "container image (default potluck-runner:latest)")
	dockerMem := fs.String("docker-memory", "2g", "container memory limit (with --container)")
	dockerCPUs := fs.String("docker-cpus", "2", "container CPU limit (with --container)")
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
	be := buildBackend(chosen, *container, *image, *dockerMem, *dockerCPUs)

	opts := runner.ModerateOptions{
		Model:            resolveModel(chosen, *model, cfg.Model),
		MaxTasks:         *maxTasks,
		IncludeEscalated: *includeEscalated,
		DryRun:           *dryRun,
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := runner.Moderate(ctx, api.New(), be, key, cfg.ContributorID, opts); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func cmdGrantModerator(args []string) {
	fs := flag.NewFlagSet("grant-moderator", flag.ExitOnError)
	contributor := fs.String("contributor", "", "contributor id to grant/revoke moderator trust")
	revoke := fs.Bool("revoke", false, "revoke moderator trust instead of granting it")
	_ = fs.Parse(args)

	if !config.HasKey() {
		fmt.Fprintln(os.Stderr, "register first: potluck register")
		os.Exit(1)
	}
	if strings.TrimSpace(*contributor) == "" {
		fmt.Fprintln(os.Stderr, "need --contributor <id>")
		os.Exit(1)
	}
	key, err := config.LoadKey()
	check(err)
	level := 1
	if *revoke {
		level = 0
	}
	c, err := api.New().GrantTrust(context.Background(), key, *contributor, level)
	check(err) // the RPC rejects non-admins with a clear "not authorized" message
	verb := "granted moderator trust to"
	if *revoke {
		verb = "revoked moderator trust from"
	}
	fmt.Printf("✅ %s %s (%s) — trust_level now %d\n", verb, c.ID, orDefault(c.DisplayName, "anonymous"), c.TrustLevel)
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

func cmdSubmit(args []string) {
	fs := flag.NewFlagSet("submit", flag.ExitOnError)
	title := fs.String("title", "", "task title")
	prompt := fs.String("prompt", "", "task prompt (self-contained)")
	acceptance := fs.String("acceptance", "", "machine-checkable acceptance criteria")
	category := fs.String("category", "", "primary category slug")
	tags := fs.String("tags", "", "comma-separated tags")
	budget := fs.Int("budget", 5000, "token budget")
	_ = fs.Parse(args)

	if !config.HasKey() {
		fmt.Fprintln(os.Stderr, "register first: potluck register")
		os.Exit(1)
	}
	if strings.TrimSpace(*title) == "" || strings.TrimSpace(*prompt) == "" {
		fmt.Fprintln(os.Stderr, "need --title and --prompt")
		os.Exit(1)
	}
	key, err := config.LoadKey()
	check(err)
	t, err := api.New().SubmitTask(context.Background(), key, *title, *prompt, *acceptance, *category, splitCSV(*tags), *budget)
	check(err)
	fmt.Printf("✅ submitted (id %s) — status: %s.\n   It becomes claimable once an AI moderator accepts it.\n", t.ID, t.Status)
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
	db := cl.BaseURL
	if cl.IsProd() {
		db += " (prod)"
	} else {
		db += " (non-prod override)"
	}
	fmt.Println("db:     ", db)
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
