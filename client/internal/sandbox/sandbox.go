// Package sandbox builds and orchestrates the hardened, fail-closed execution sandbox: an
// ephemeral, non-root, read-only, capability-dropped container in which the agent does curated
// work (native web research + read_document, NO shell/file). Egress is open (the agent
// researches the open web); the load-bearing boundary is HOST isolation — no shell, no file
// access, no credential reach (the broker injects the key; the agent holds a placeholder). This
// path FAILS CLOSED: if the daemon, image, or key can't be verified up, the runner refuses
// rather than silently downgrading. stdlib-only (shells out to `docker`).
package sandbox

import (
	"fmt"
	"strconv"
	"strings"
)

const (
	// DefaultImage carries both the agent CLI and the potluck binary (MCP tools server + hook).
	DefaultImage = "potluck-sandbox:phase2"

	// SandboxNetwork is a user-defined bridge (HAS internet) that the agent + broker share, so
	// the agent reaches the broker by name AND can research the open web (native WebSearch/
	// WebFetch). Egress is open by design now; host safety comes from the container hardening +
	// no shell/file tools, not from locking the network. (The credential is still protected: the
	// agent holds only a placeholder; the broker injects the real key.)
	SandboxNetwork = "potluck-net"

	// BrokerName/BrokerPort are fixed because the runner processes tasks sequentially and
	// tears the sandbox down between tasks; the agent reaches the broker by container name.
	BrokerName = "potluck-broker"
	BrokerPort = 8787

	sandboxUser = "10001:10001"
)

// BrokerBaseURL is the ANTHROPIC_BASE_URL the agent container uses — the broker, by name, on
// the shared internal network.
func BrokerBaseURL() string { return fmt.Sprintf("http://%s:%d", BrokerName, BrokerPort) }

// CmdRunner runs an external command and returns combined output. Injected so Preflight and
// the lifecycle helpers are unit-testable without a real Docker daemon.
type CmdRunner func(name string, args ...string) ([]byte, error)

// AgentSpec describes the hardened agent container.
type AgentSpec struct {
	Image     string
	Network   string   // the bridge network to join (default SandboxNetwork)
	Name      string   // container name (for teardown); optional
	Memory    string   // default "2g"
	CPUs      string   // default "2"
	PidsLimit int      // default 256
	Env       []string // agent env: placeholder key, ANTHROPIC_BASE_URL, tools config
	Mounts    []string // "host:container:ro" (e.g. a read-only document input dir)
}

// AgentRunArgs builds the full `docker run …` argv for the hardened agent container wrapping
// `bin cmdArgs…`. Every flag here is part of the boundary; the set is asserted in tests and
// matches the empirically-verified primitives in scripts/verify-sandbox.sh.
func AgentRunArgs(s AgentSpec, bin string, cmdArgs []string) []string {
	image := s.Image
	if image == "" {
		image = DefaultImage
	}
	network := s.Network
	if network == "" {
		network = SandboxNetwork
	}
	mem := s.Memory
	if mem == "" {
		mem = "2g"
	}
	cpus := s.CPUs
	if cpus == "" {
		cpus = "2"
	}
	pids := s.PidsLimit
	if pids <= 0 {
		pids = 256
	}
	const own = ",uid=10001,gid=10001"
	run := []string{
		"run", "--rm", "--init",
		"--user", sandboxUser, // non-root enforced host-side, regardless of image USER
		"--read-only",       // immutable rootfs
		"--cap-drop", "ALL", // no Linux capabilities
		"--security-opt", "no-new-privileges",
		"--network", network, // shared bridge with the broker; open egress for native web research
		"--pids-limit", strconv.Itoa(pids),
		"--memory", mem,
		"--cpus", cpus,
		// Ephemeral writable scratch only; everything else is read-only.
		"--tmpfs", "/tmp:rw,size=256m" + own,
		"--tmpfs", "/home/potluck/work:rw,size=256m" + own,
		"--tmpfs", "/home/potluck/.claude:rw,size=64m" + own,
	}
	if s.Name != "" {
		run = append(run, "--name", s.Name)
	}
	for _, e := range s.Env {
		run = append(run, "-e", e)
	}
	for _, m := range s.Mounts {
		run = append(run, "-v", m)
	}
	run = append(run, image, bin)
	return append(run, cmdArgs...)
}

// BrokerRunArgs builds the `docker run …` argv for the broker sidecar: it joins the shared
// sandbox network (reachable by the agent + the internet) and holds the REAL key (passed by
// NAME via -e, never written to disk). It is itself hardened (read-only, non-root, cap-drop).
func BrokerRunArgs(image, realKeyEnvName, upstream string) []string {
	if image == "" {
		image = DefaultImage
	}
	args := []string{
		"run", "-d", "--rm", "--init",
		"--name", BrokerName,
		"--network", SandboxNetwork,
		"--user", sandboxUser,
		"--read-only",
		"--cap-drop", "ALL",
		"--security-opt", "no-new-privileges",
		"--pids-limit", "128",
		"--memory", "256m",
		"-e", realKeyEnvName, // forward the real key BY NAME from the host process env
		image, "potluck", "__broker",
		"--addr", fmt.Sprintf("0.0.0.0:%d", BrokerPort),
		"--upstream", upstream,
	}
	return args
}

// Preflight verifies the sandbox can actually run, FAILING CLOSED with an actionable error if
// not. The runner calls this before a Phase-2 run and refuses on any error (it does NOT fall
// back to the host — that would silently drop the isolation the whole mode depends on).
func Preflight(run CmdRunner, image string, haveRealKey bool) error {
	if image == "" {
		image = DefaultImage
	}
	if !haveRealKey {
		return fmt.Errorf("phase-2 needs a real ANTHROPIC_API_KEY for the credential broker, and none is set — refusing (the OAuth/subscription path cannot be brokered; use an API key)")
	}
	if _, err := run("docker", "version", "--format", "{{.Server.Version}}"); err != nil {
		return fmt.Errorf("phase-2 requires Docker and the daemon is not reachable — refusing to run on the host (fail closed): %w", err)
	}
	if _, err := run("docker", "image", "inspect", image); err != nil {
		return fmt.Errorf("phase-2 sandbox image %q is not built — refusing (build it: docker build -t %s -f docker/Dockerfile.phase2 .): %w", image, image, err)
	}
	return nil
}

// EnsureNetworks creates the shared sandbox bridge network if absent. Idempotent:
// "already exists" is not an error.
func EnsureNetworks(run CmdRunner) error {
	if _, err := run("docker", "network", "create", SandboxNetwork); err != nil && !alreadyExists(err) {
		return fmt.Errorf("create sandbox network: %w", err)
	}
	return nil
}

// StartBroker launches the broker sidecar on the shared network (reachable by the agent + the
// provider). It injects the real key so the agent only ever holds a placeholder.
func StartBroker(run CmdRunner, image, realKeyEnvName, upstream string) error {
	_, _ = run("docker", "rm", "-f", BrokerName) // clear any stale sidecar
	args := BrokerRunArgs(image, realKeyEnvName, upstream)
	if out, err := run("docker", args...); err != nil {
		return fmt.Errorf("start broker: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// Teardown removes the broker sidecar (best-effort). Networks are left in place (cheap, reused).
func Teardown(run CmdRunner) {
	_, _ = run("docker", "rm", "-f", BrokerName)
}

func alreadyExists(err error) bool {
	return strings.Contains(strings.ToLower(err.Error()), "already exists")
}
