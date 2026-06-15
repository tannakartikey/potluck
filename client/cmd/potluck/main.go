// Command potluck is the contributor runner: register an identity, then claim →
// run (on your own model, in safe mode) → submit, with an honest token/cost summary.
package main

import (
	"context"
	"flag"
	"fmt"
	"net/url"
	"os"
	"os/signal"
	"runtime"
	"runtime/debug"
	"strings"
	"syscall"

	"github.com/tannakartikey/potluck/client/internal/api"
	"github.com/tannakartikey/potluck/client/internal/backend"
	"github.com/tannakartikey/potluck/client/internal/config"
	"github.com/tannakartikey/potluck/client/internal/runner"
)

const repoURL = "https://github.com/tannakartikey/potluck"

var version = "v0.0.1-dev"

func main() {
	// Potluck collects NO telemetry. If the binary crashes, we print the stack locally and a
	// link to file a GitHub issue — you choose what to share; nothing is sent automatically.
	defer func() {
		if r := recover(); r != nil {
			stack := truncate(string(debug.Stack()), 2000)
			fmt.Fprintf(os.Stderr, "\n💥 potluck hit an unexpected error: %v\n\n%s\n", r, stack)
			fmt.Fprintln(os.Stderr, "Potluck collects no telemetry — nothing was sent. If you can, please report this (you choose what to share):")
			body := fmt.Sprintf("**Crash:** %v\n\n**Command:** `potluck %s`\n**Version:** %s · %s/%s\n\n**Stack:**\n```\n%s\n```",
				r, strings.Join(os.Args[1:], " "), version, runtime.GOOS, runtime.GOARCH, stack)
			fmt.Fprintln(os.Stderr, "  "+issueURL(fmt.Sprintf("crash: %v", r), body))
			os.Exit(2)
		}
	}()
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
	case "__hook":
		cmdHook(os.Args[2:]) // internal: PreToolUse deny-all-except-curated hook (v2 sandbox)
	case "__tools-server":
		cmdToolsServer(os.Args[2:]) // internal: MCP curated-tools stdio server (v2 sandbox)
	case "__broker":
		cmdBroker(os.Args[2:]) // internal: credential broker (v2 sandbox sidecar)
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
  --budget N        skip tasks needing more than N tokens (default: config / 16000)
  --model M         model: Claude alias (haiku|sonnet|opus) or full id; for codex
                    pass a Codex model (e.g. gpt-5-codex) or omit to use its default
  --max-tasks N     stop after N tasks (default: 0 = until queue empty / Ctrl-C)
  --watch           when the queue is empty, wait and re-poll instead of exiting
  --poll N          --watch poll interval in seconds (default 15)
  --max-week N      stop when your weekly plan usage reaches N% (Claude Code; 0 = off)
  --max-session N   stop when your 5-hour session usage reaches N% (Claude Code; 0 = off)
  --no-container    run on the host instead of the DEFAULT locked-down container
                    (by default each task runs in a container; mounts ONLY your auth
                    file, never your session history. Falls back to host if Docker
                    isn't set up.)
  --image NAME      container image (default potluck-runner:latest)
  --docker-memory   container memory limit (default 2g)
  --docker-cpus     container CPU limit (default 2)
  CURATED TOOLS ARE THE DEFAULT (Claude Code): the agent may use fetch_url +
  read_document (NO raw shell). It auto-picks the strongest lane and degrades — never
  refuses: ANTHROPIC_API_KEY + Docker + the sandbox image → broker + hardened container;
  otherwise → host curated (the curated tool surface is the boundary, no container backstop).
  --no-tools        strict v1 no-tools mode (escape hatch)
  --phase2          force the strongest lane (broker + hardened container) and FAIL CLOSED
                    if it can't come up (needs ANTHROPIC_API_KEY + Docker + the image:
                    docker build -t potluck-sandbox:phase2 -f docker/Dockerfile.phase2 .)
  --fetch-allow a,b fetch_url host allowlist (default-deny; YOU control egress)
  --doc-dir DIR     directory read_document may read (mounted read-only in a container)

moderate flags:
  --backend B       claude-code | codex (default: config / claude-code)
  --model M         model alias or id
  --max-tasks N     stop after N tasks (default: 0 = whole queue)
  --include-escalated  also re-examine 'needs_review' tasks
  --dry-run         print verdicts but don't apply them
  --no-container    run on the host instead of the default locked-down container

submit flags:
  --title, --prompt (required), --acceptance, --category, --tags a,b, --budget N

spec & docs: https://github.com/tannakartikey/potluck/blob/main/AGENTS.md
`)
}

func cmdRegister(args []string) {
	fs := flag.NewFlagSet("register", flag.ExitOnError)
	name := fs.String("name", "", "display name / handle")
	_ = fs.Parse(args)

	printDisclaimer()
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
	noContainer := fs.Bool("no-container", false, "run on the host instead of the default locked-down container")
	image := fs.String("image", "", "container image to use (default potluck-runner:latest)")
	dockerMem := fs.String("docker-memory", "2g", "container memory limit")
	dockerCPUs := fs.String("docker-cpus", "2", "container CPU limit")
	phase2 := fs.Bool("phase2", false, "force the strongest curated lane (broker + hardened container) and FAIL CLOSED if it can't come up")
	noTools := fs.Bool("no-tools", false, "strict v1 no-tools mode (escape hatch; curated tools are the default)")
	fetchAllow := fs.String("fetch-allow", "", "fetch_url host allowlist (default-deny; you control egress)")
	docDir := fs.String("doc-dir", "", "directory the read_document tool may read (mounted read-only in a container)")
	_ = fs.Parse(args)

	curatedOpts := runner.Options{
		Topics:       splitCSV(*topics),
		BudgetTokens: pickInt(*budget, 16000),
		Model:        firstNonEmpty(*model, "haiku"),
		MaxTasks:     *maxTasks,
		Watch:        *watch,
		PollSeconds:  *poll,
	}
	if *phase2 {
		cmdRunPhase2(*image, *fetchAllow, *docDir, *dockerMem, *dockerCPUs, curatedOpts)
		return
	}

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

	// DEFAULT = curated tools. Claude Code gets the real curated lane (MCP-only fetch_url +
	// read_document), auto-degrading from broker+container to host. --no-tools is the strict v1
	// escape hatch; Codex can't be MCP-only (it always keeps a shell), so it stays on the
	// hardened container path, labelled the weaker lane.
	if !*noTools && chosen == "claude-code" {
		curatedOpts.Model = firstNonEmpty(*model, cfg.Model, "haiku")
		cmdRunCurated(key, *image, *fetchAllow, *docDir, *dockerMem, *dockerCPUs, !*noContainer, curatedOpts)
		return
	}

	be := buildBackend(chosen, !*noContainer, *image, *dockerMem, *dockerCPUs)
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

	fmt.Println(disclaimerLine)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := runner.Run(ctx, api.New(), be, key, opts); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

// buildBackend constructs the chosen execution backend. By DEFAULT each task runs inside the
// locked-down Docker container (the safe default); if the container can't run (no Docker/image, or
// no mountable auth) it falls back to host no-tools safe mode with a clear message rather than
// failing. Pass container=false (--no-container) to force the host. Shared by `run` and `moderate`.
func buildBackend(chosen string, container bool, image, dockerMem, dockerCPUs string) backend.Backend {
	var dcfg *backend.DockerConfig
	if container {
		home, _ := os.UserHomeDir()
		mounts, env := backend.AuthMountsFor(chosen, home)
		switch {
		case !backend.HasContainerAuth(chosen, home, mounts, env):
			fmt.Fprintf(os.Stderr, "⚠️  container needs a mountable %s credential and none was found — running on the host in no-tools safe mode.\n", chosen)
			if chosen == "claude-code" {
				fmt.Fprintln(os.Stderr, "    (set ANTHROPIC_API_KEY for full container isolation; or pass --no-container to silence this.)")
			} else {
				fmt.Fprintln(os.Stderr, "    (`codex login` / set OPENAI_API_KEY for full container isolation; or pass --no-container to silence this.)")
			}
		case !backend.ContainerImageReady(image):
			fmt.Fprintln(os.Stderr, "⚠️  the sandbox image isn't ready (Docker not running, or image not built) — running on the host in no-tools safe mode.")
			fmt.Fprintln(os.Stderr, "    build it once for full isolation:  docker build -t potluck-runner:latest docker/")
		default:
			dcfg = &backend.DockerConfig{Image: image, Mounts: mounts, Env: env, Memory: dockerMem, CPUs: dockerCPUs}
		}
	}
	switch chosen {
	case "claude-code":
		return &backend.ClaudeCode{Bin: "claude", Docker: dcfg}
	case "codex":
		fmt.Fprintln(os.Stderr, "⚠️  Codex runs a read-only sandbox, not hard no-tools safe mode — it can still run read-only shell and read local files (best-effort). Claude Code is the safer default for untrusted tasks; the output guard is the backstop.")
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
	noContainer := fs.Bool("no-container", false, "run on the host instead of the default locked-down container")
	image := fs.String("image", "", "container image (default potluck-runner:latest)")
	dockerMem := fs.String("docker-memory", "2g", "container memory limit")
	dockerCPUs := fs.String("docker-cpus", "2", "container CPU limit")
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
	be := buildBackend(chosen, !*noContainer, *image, *dockerMem, *dockerCPUs)

	opts := runner.ModerateOptions{
		Model:            resolveModel(chosen, *model, cfg.Model),
		MaxTasks:         *maxTasks,
		IncludeEscalated: *includeEscalated,
		DryRun:           *dryRun,
	}
	fmt.Println(disclaimerLine)
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
	budget := fs.Int("budget", 12000, "token budget (realistic room for quality work)")
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

// printDisclaimer shows the run-at-your-own-risk notice. Full text in DISCLAIMER.md.
func printDisclaimer() {
	const line = "──────────────────────────────────────────────────────────────────"
	fmt.Println(line)
	fmt.Println("⚠️  DISCLAIMER — please read")
	fmt.Println("Tasks are community-submitted and UNTRUSTED. Potluck does not author or")
	fmt.Println("control them; AI moderation is best-effort, not a guarantee. You run them")
	fmt.Println("on YOUR machine and YOUR provider account, at YOUR OWN RISK, under your")
	fmt.Println("provider's Terms of Service. Artifacts are AI-generated and `unverified`.")
	fmt.Println("Provided AS IS — no warranty, no liability to the extent permitted by law.")
	fmt.Println("By using this client you accept this. Full text: DISCLAIMER.md")
	fmt.Println(line)
}

const disclaimerLine = "⚠️  Community tasks are untrusted · AI-moderated best-effort · run at your own risk · DISCLAIMER.md"

// issueURL builds a prefilled "new GitHub issue" link. We never auto-submit anything — the user
// clicks it if they choose to. (No telemetry; consistent with storing no user data.)
func issueURL(title, body string) string {
	return repoURL + "/issues/new?title=" + url.QueryEscape(title) + "&body=" + url.QueryEscape(body)
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n] + "…"
	}
	return s
}

func check(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		fmt.Fprintln(os.Stderr, "\nPotluck collects no telemetry — nothing was sent. If this looks like a bug, you can report it (you choose what to share):")
		body := fmt.Sprintf("**Error:**\n```\n%v\n```\n\n**Command:** `potluck %s`\n**Version:** %s · %s/%s",
			err, strings.Join(os.Args[1:], " "), version, runtime.GOOS, runtime.GOARCH)
		fmt.Fprintln(os.Stderr, "  "+issueURL("bug: "+truncate(err.Error(), 70), body))
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
