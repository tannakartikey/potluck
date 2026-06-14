package backend

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// Codex runs a task via the OpenAI Codex CLI (`codex exec`). Codex is agentic by
// design — there is no hard "no tools" mode like Claude Code — so safe mode here is
// BEST-EFFORT: a read-only sandbox (no writes, no network), an isolated empty working
// dir, user config + rules ignored, an ephemeral session, plus the safety preamble and
// the pre-publish output guard. The agent can still run READ-ONLY shell, so this is
// weaker than the Claude Code backend's hard no-tools mode — see docs/threat-model.md.
type Codex struct {
	Bin string
}

func NewCodex() *Codex { return &Codex{Bin: "codex"} }

func (c *Codex) Name() string { return "codex" }

// codexEvent is one line of `codex exec --json` JSONL output.
type codexEvent struct {
	Type string `json:"type"`
	Item *struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"item"`
	Usage *struct {
		InputTokens           int `json:"input_tokens"`
		OutputTokens          int `json:"output_tokens"`
		ReasoningOutputTokens int `json:"reasoning_output_tokens"`
	} `json:"usage"`
}

func (c *Codex) Run(ctx context.Context, req Request) (*Response, error) {
	if req.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, req.Timeout)
		defer cancel()
	}

	work, err := os.MkdirTemp("", "potluck-codex-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(work)
	msgFile, err := os.CreateTemp("", "potluck-codex-msg-*")
	if err != nil {
		return nil, err
	}
	_ = msgFile.Close()
	defer os.Remove(msgFile.Name())

	args := []string{
		"exec", "--json",
		"-o", msgFile.Name(),
		"--cd", work,
		"--skip-git-repo-check",
		"--ephemeral",
		"--ignore-user-config",
		"--ignore-rules",
		"--sandbox", "read-only", // SAFE MODE (best-effort): no writes, no network
	}
	if req.Model != "" {
		args = append(args, "-m", req.Model)
	}
	args = append(args, req.System+"\n\n"+req.Prompt)

	cmd := exec.CommandContext(ctx, c.Bin, args...)
	cmd.Dir = work
	cmd.Stdin = nil // don't let codex read stdin
	out, err := cmd.Output()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			return nil, fmt.Errorf("codex exited: %v: %s", err, strings.TrimSpace(string(ee.Stderr)))
		}
		return nil, fmt.Errorf("codex: %w", err)
	}

	usage, streamMsg := parseCodexJSONL(out)
	text := streamMsg
	if b, rerr := os.ReadFile(msgFile.Name()); rerr == nil && len(bytes.TrimSpace(b)) > 0 {
		text = strings.TrimRight(string(b), "\n")
	}
	if strings.TrimSpace(text) == "" {
		return nil, fmt.Errorf("codex produced no output")
	}

	model := req.Model
	if model == "" {
		model = "codex"
	}
	return &Response{
		Text:          text,
		Usage:         usage,
		ReportedModel: model,
		Raw:           out,
	}, nil
}

// parseCodexJSONL pulls the final agent message + token usage out of the JSONL stream.
func parseCodexJSONL(out []byte) (Usage, string) {
	var u Usage
	var lastMsg string
	sc := bufio.NewScanner(bytes.NewReader(out))
	sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 || line[0] != '{' {
			continue
		}
		var ev codexEvent
		if json.Unmarshal(line, &ev) != nil {
			continue
		}
		if ev.Item != nil && ev.Item.Type == "agent_message" && ev.Item.Text != "" {
			lastMsg = ev.Item.Text
		}
		if ev.Usage != nil {
			u.InputTokens = ev.Usage.InputTokens
			u.OutputTokens = ev.Usage.OutputTokens + ev.Usage.ReasoningOutputTokens
			// Codex does not report cost in the usage event; leave CostUSD = 0.
		}
	}
	return u, lastMsg
}
