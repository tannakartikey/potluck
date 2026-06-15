# Potluck container runner — runs a task's agent CLI in an isolated, ephemeral container,
# so an untrusted community task can't touch the host. (v0 launch-safety isolation, #23.)
#
# Build:  docker build -t potluck-runner:latest docker/
#
# At runtime the runner adds: --rm --read-only --tmpfs ... --cap-drop ALL
# --security-opt no-new-privileges --pids-limit, plus the contributor's agent auth mounted
# read-only (~/.codex is a mountable auth file; Claude's is keychain-based, so Claude in a
# container needs ANTHROPIC_API_KEY passed via -e). Egress-allowlisting is a hardening TODO
# (today the container shares the default bridge so the agent can reach the LLM API).

# Base is a moving tag. For full reproducibility pin by digest:
#   docker manifest inspect node:22-slim  ->  FROM node:22-slim@sha256:<digest>   (TODO)
FROM node:22-slim

# ca-certificates is required: node:22-slim ships without root CAs, so the agent CLIs'
# TLS connections to the LLM API fail with "no native root CA certificates found".
# Agent CLIs are version-pinned: reproducible sandbox, and the no-tools flags in
# claudecode.go are verified against these exact versions — bump deliberately.
RUN apt-get update \
 && apt-get install -y --no-install-recommends ca-certificates \
 && rm -rf /var/lib/apt/lists/* \
 && npm install -g @anthropic-ai/claude-code@2.1.177 @openai/codex@0.139.0 \
 && useradd --create-home --uid 10001 potluck \
 && mkdir -p /home/potluck/work /home/potluck/.claude /home/potluck/.codex \
 && chown -R potluck:potluck /home/potluck

USER potluck
ENV HOME=/home/potluck
WORKDIR /home/potluck/work
ENTRYPOINT []
