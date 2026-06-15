package sandbox

import (
	"fmt"
	"strings"
	"testing"
)

// recorder is an injectable CmdRunner that records calls and can fail when a call's joined
// string contains a configured substring.
type recorder struct {
	calls  [][]string
	failOn map[string]bool
}

func (r *recorder) run(name string, args ...string) ([]byte, error) {
	call := append([]string{name}, args...)
	r.calls = append(r.calls, call)
	joined := strings.Join(call, " ")
	for sub := range r.failOn {
		if strings.Contains(joined, sub) {
			return []byte("simulated failure"), fmt.Errorf("simulated failure for %q", sub)
		}
	}
	return []byte("ok"), nil
}

func contains(args []string, want string) bool {
	for _, a := range args {
		if a == want {
			return true
		}
	}
	return false
}

func containsPair(args []string, flag, val string) bool {
	for i := 0; i+1 < len(args); i++ {
		if args[i] == flag && args[i+1] == val {
			return true
		}
	}
	return false
}

func TestAgentRunArgsHardening(t *testing.T) {
	args := AgentRunArgs(AgentSpec{
		Name:   "potluck-agent",
		Env:    []string{"ANTHROPIC_API_KEY=placeholder", "ANTHROPIC_BASE_URL=http://potluck-broker:8787"},
		Mounts: []string{"/host/docs:/home/potluck/work/in:ro"},
	}, "claude", []string{"-p", "hi"})

	// Every load-bearing isolation flag must be present.
	mustPair := map[string]string{
		"--user":         "10001:10001",
		"--cap-drop":     "ALL",
		"--security-opt": "no-new-privileges",
		"--network":      EgressNetwork,
		"--pids-limit":   "256",
		"--memory":       "2g",
		"--cpus":         "2",
	}
	for flag, val := range mustPair {
		if !containsPair(args, flag, val) {
			t.Errorf("missing %s %s in: %v", flag, val, args)
		}
	}
	for _, flag := range []string{"--rm", "--init", "--read-only"} {
		if !contains(args, flag) {
			t.Errorf("missing %s", flag)
		}
	}
	// Read-only rootfs means writable scratch must be explicit tmpfs.
	if !containsPair(args, "--tmpfs", "/tmp:rw,size=256m,uid=10001,gid=10001") {
		t.Errorf("missing /tmp tmpfs: %v", args)
	}
	// env + mounts threaded through
	if !containsPair(args, "-e", "ANTHROPIC_BASE_URL=http://potluck-broker:8787") {
		t.Error("broker base URL env not passed")
	}
	if !containsPair(args, "-v", "/host/docs:/home/potluck/work/in:ro") {
		t.Error("doc mount not passed")
	}
	// image + command come last, in order: [..., image, "claude", "-p", "hi"]
	n := len(args)
	if args[n-4] != DefaultImage || args[n-3] != "claude" || args[n-1] != "hi" {
		t.Errorf("tail = %v, want [%s claude -p hi]", args[n-4:], DefaultImage)
	}
	// A NON-internal network must never be the agent's network by default.
	if !containsPair(args, "--network", "potluck-egress") {
		t.Error("agent must join the internal egress network")
	}
}

func TestBrokerRunArgs(t *testing.T) {
	args := BrokerRunArgs("", "ANTHROPIC_API_KEY", "https://api.anthropic.com")
	for _, want := range []string{"-d", "--read-only", "__broker"} {
		if !contains(args, want) {
			t.Errorf("broker args missing %q: %v", want, args)
		}
	}
	if !containsPair(args, "--name", BrokerName) || !containsPair(args, "--network", EgressNetwork) {
		t.Errorf("broker name/network wrong: %v", args)
	}
	if !containsPair(args, "-e", "ANTHROPIC_API_KEY") {
		t.Error("broker must receive the real key BY NAME (forwarded), not inline")
	}
	if !containsPair(args, "--addr", "0.0.0.0:8787") {
		t.Error("broker must bind 0.0.0.0 so the agent container can reach it")
	}
	// The real key VALUE must never be embedded in the args.
	if strings.Contains(strings.Join(args, " "), "sk-ant") {
		t.Error("a key value must never appear in broker args")
	}
}

func TestPreflightFailsClosed(t *testing.T) {
	// No real key → refuse.
	if err := Preflight((&recorder{}).run, "", false); err == nil {
		t.Error("preflight must refuse without a real API key")
	}
	// Docker daemon down → refuse (no host fallback).
	r := &recorder{failOn: map[string]bool{"docker version": true}}
	err := Preflight(r.run, "", true)
	if err == nil || !strings.Contains(err.Error(), "fail closed") {
		t.Errorf("preflight must fail closed when docker is down, got %v", err)
	}
	// Image missing → refuse with a build hint.
	r = &recorder{failOn: map[string]bool{"image inspect": true}}
	err = Preflight(r.run, "", true)
	if err == nil || !strings.Contains(err.Error(), "not built") {
		t.Errorf("preflight must refuse when the image is missing, got %v", err)
	}
	// All good → pass.
	if err := Preflight((&recorder{}).run, "", true); err != nil {
		t.Errorf("preflight should pass when docker + image + key are present, got %v", err)
	}
}

func TestEnsureNetworksIdempotent(t *testing.T) {
	r := &recorder{} // no configured failures
	if err := EnsureNetworks(r.run); err != nil {
		t.Fatal(err)
	}
	// Simulate "already exists" — must be swallowed.
	r2 := &recorderAlreadyExists{}
	if err := EnsureNetworks(r2.run); err != nil {
		t.Errorf("EnsureNetworks should ignore 'already exists', got %v", err)
	}
}

type recorderAlreadyExists struct{}

func (r *recorderAlreadyExists) run(name string, args ...string) ([]byte, error) {
	return []byte("Error response from daemon: network already exists"), fmt.Errorf("network with name already exists")
}

func TestStartBrokerSequence(t *testing.T) {
	r := &recorder{}
	if err := StartBroker(r.run, "", "ANTHROPIC_API_KEY", "https://api.anthropic.com"); err != nil {
		t.Fatal(err)
	}
	// Expect: rm -f (cleanup) → run -d (start) → network connect (dual-home).
	var sawRun, sawConnect bool
	for _, c := range r.calls {
		j := strings.Join(c, " ")
		if strings.Contains(j, "run -d") {
			sawRun = true
		}
		if strings.Contains(j, "network connect "+PublicNetwork) {
			sawConnect = true
		}
	}
	if !sawRun || !sawConnect {
		t.Errorf("StartBroker must run the sidecar AND connect it to the public net; calls=%v", r.calls)
	}
}
