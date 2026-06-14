package backend

import (
	"os"
	"os/exec"
	"path/filepath"
)

// ContainerImageReady reports whether the sandbox image exists and the Docker daemon is reachable
// (so the runner can fall back to host safe-mode with a clear message instead of a cryptic error).
func ContainerImageReady(image string) bool {
	if image == "" {
		image = "potluck-runner:latest"
	}
	return exec.Command("docker", "image", "inspect", image).Run() == nil
}

// DockerConfig, when set on a backend, runs the agent CLI inside an ephemeral, locked-down
// Docker container instead of on the host — the v0 launch-safety isolation (#23). The
// contributor's agent auth is mounted read-only (Mounts) and/or API keys are passed via Env;
// the container gets no other host access.
type DockerConfig struct {
	Image  string   // image with the agent CLIs (default "potluck-runner:latest")
	Mounts []string // "host:container:ro" auth mounts (a single auth FILE, never a whole dir)
	Env    []string // env to forward; "KEY" (forward host value) or "KEY=VALUE"
	Memory string   // e.g. "2g"
	CPUs   string   // e.g. "2"
}

// dockerRunArgs builds the `docker run …` argv that wraps `bin args…` in a locked-down,
// ephemeral container. The rootfs is read-only; the only writable paths are tmpfs scratch
// (discarded on exit), including the agent's home-config dirs so the CLI can write its own
// session/cache there WITHOUT that ever touching — or leaking from — the host.
func dockerRunArgs(d *DockerConfig, bin string, args []string) []string {
	// The writable scratch dirs are tmpfs owned by uid/gid 10001 — the `potluck` user the
	// Dockerfile creates (keep these in sync). The default image runs non-root, so a
	// root-owned tmpfs would be unwritable; `mode=…` is ignored by some Docker/tmpfs
	// builds, but uid/gid is honored. /tmp and work are the agent's scratch; the two
	// home-config dirs give the CLI a writable place for its own cache/session.
	const own = ",uid=10001,gid=10001"
	run := []string{
		"run", "--rm", "--init",
		"--read-only",
		"--tmpfs", "/tmp:rw,size=256m" + own,
		"--tmpfs", "/home/potluck/work:rw,size=256m" + own,
		// The single auth FILE is bind-mounted read-only on top of these (Docker orders
		// parent-before-child), so the agent gets a writable config dir but the host's
		// session history is never present — only the one auth file.
		"--tmpfs", "/home/potluck/.claude:rw,size=64m" + own,
		"--tmpfs", "/home/potluck/.codex:rw,size=64m" + own,
		"--cap-drop", "ALL",
		"--security-opt", "no-new-privileges",
		"--pids-limit", "512",
		// NOTE: egress is the host's default bridge (the agent needs the LLM API); a
		// per-task egress allowlist is a hardening TODO tied to tool-enabled tasks (#22).
	}
	if d.Memory != "" {
		run = append(run, "--memory", d.Memory)
	}
	if d.CPUs != "" {
		run = append(run, "--cpus", d.CPUs)
	}
	for _, m := range d.Mounts {
		run = append(run, "-v", m)
	}
	for _, e := range d.Env {
		run = append(run, "-e", e)
	}
	img := d.Image
	if img == "" {
		img = "potluck-runner:latest"
	}
	run = append(run, img, bin)
	return append(run, args...)
}

// wrapExec returns the program + args to actually run: either (bin, args) directly on the
// host, or ("docker", dockerRunArgs…) when a DockerConfig is set.
func wrapExec(d *DockerConfig, bin string, args []string) (string, []string) {
	if d == nil {
		return bin, args
	}
	return "docker", dockerRunArgs(d, bin, args)
}

// AuthMountsFor returns the read-only auth-file mount(s) and env passthrough(s) a backend
// needs inside the container. Two BYO-agent rules shape this:
//
//   - Mount ONLY the single auth file, never the whole ~/.claude or ~/.codex directory —
//     those hold the contributor's session history, which must not enter the container.
//   - Forward API keys BY NAME (e.g. "ANTHROPIC_API_KEY"), so the secret value is never
//     copied into Potluck's own config, state, or logs — Docker reads it straight from the
//     host process env, and if the var is unset the container simply doesn't get it.
func AuthMountsFor(backendName, home string) (mounts, env []string) {
	switch backendName {
	case "codex":
		// Codex persists auth (OpenAI key and/or OAuth tokens) in one JSON file.
		if p := filepath.Join(home, ".codex", "auth.json"); fileExists(p) {
			mounts = append(mounts, p+":/home/potluck/.codex/auth.json:ro")
		}
		env = append(env, "OPENAI_API_KEY")
	case "claude-code":
		// Claude Code on Linux keeps a single credentials file we can mount; on macOS
		// auth lives in the Keychain (not a mountable file), so the container path is the
		// ANTHROPIC_API_KEY env var instead.
		if p := filepath.Join(home, ".claude", ".credentials.json"); fileExists(p) {
			mounts = append(mounts, p+":/home/potluck/.claude/.credentials.json:ro")
		}
		env = append(env, "ANTHROPIC_API_KEY")
	}
	return mounts, env
}

// HasContainerAuth reports whether the contributor has any usable credential for a
// containerized run — either a mountable auth file or the relevant API key in the host
// env — so the runner can fail fast with a clear message instead of a cryptic auth error.
func HasContainerAuth(backendName, home string, mounts, env []string) bool {
	if len(mounts) > 0 {
		return true
	}
	for _, name := range env {
		if os.Getenv(name) != "" {
			return true
		}
	}
	return false
}

func fileExists(p string) bool {
	info, err := os.Stat(p)
	return err == nil && !info.IsDir()
}
