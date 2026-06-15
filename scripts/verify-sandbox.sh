#!/usr/bin/env bash
# Phase-2 sandbox empirical verification — the prime directive in action (plans/prelaunch.md
# §2, §7): every security property of the v2 sandbox is PROVEN by running it, not asserted in
# prose. Exits non-zero on any failed property, so it can gate a launch.
#
#   usage: bash scripts/verify-sandbox.sh
#   needs: Docker, and the image built:
#          docker build -t potluck-sandbox:phase2 -f docker/Dockerfile.phase2 .
#   dev shortcut (no full image build — behaviourally identical): run against any
#   node-based image + a host-built linux potluck binary, e.g.
#     CGO_ENABLED=0 GOOS=linux go -C client build -o /tmp/potluck-linux ./cmd/potluck
#     POTLUCK_SANDBOX_IMAGE=potluck-runner:latest POTLUCK_BIN=/tmp/potluck-linux bash scripts/verify-sandbox.sh
set -uo pipefail

IMAGE="${POTLUCK_SANDBOX_IMAGE:-potluck-sandbox:phase2}"
# POTLUCK_BIN: optional path to a linux potluck binary to bind-mount in (lets the script run
# against a base image + host binary without the full image build — behaviourally identical).
BIND=()
if [ -n "${POTLUCK_BIN:-}" ]; then BIND=(-v "${POTLUCK_BIN}:/usr/local/bin/potluck:ro"); fi
BROKER=potluck-broker-verify
fail=0
pass() { printf '  PASS  %s\n' "$1"; }
bad()  { printf '  FAIL  %s\n' "$1"; fail=1; }

cleanup() {
  docker rm -f "$BROKER" >/dev/null 2>&1
  docker network rm potluck-net-verify >/dev/null 2>&1
}
trap cleanup EXIT

command -v docker >/dev/null 2>&1 || { echo "docker not found"; exit 1; }
if ! docker image inspect "$IMAGE" >/dev/null 2>&1; then
  echo "image $IMAGE not built. Build it:"
  echo "  docker build -t $IMAGE -f docker/Dockerfile.phase2 ."
  exit 1
fi
echo "verify-sandbox -> image=$IMAGE"

# ── 1. Hardening: non-root, read-only rootfs, all caps dropped, no-new-privileges ──
echo "[1] container hardening"
OUT=$(docker run --rm --read-only --cap-drop ALL --security-opt no-new-privileges \
  --user 10001:10001 --pids-limit 256 --memory 512m \
  --tmpfs /tmp:rw,size=64m,uid=10001,gid=10001 \
  "$IMAGE" sh -c '
    echo "uid=$(id -u)"
    echo x > /rootwrite 2>/dev/null && echo "rootwrite=BAD" || echo "rootwrite=denied"
    echo y > /tmp/ok 2>/dev/null && echo "tmpfs=ok" || echo "tmpfs=BAD"
    grep CapEff /proc/self/status
    grep NoNewPrivs /proc/self/status
  ' 2>&1)
echo "$OUT" | grep -q "uid=10001"                  && pass "runs as non-root uid 10001"        || bad "not running as uid 10001"
echo "$OUT" | grep -q "rootwrite=denied"           && pass "root filesystem is read-only"      || bad "root filesystem is writable"
echo "$OUT" | grep -q "tmpfs=ok"                    && pass "ephemeral tmpfs scratch writable"  || bad "tmpfs scratch not writable"
echo "$OUT" | grep -q "CapEff:.*0000000000000000"  && pass "all Linux capabilities dropped"    || bad "capabilities remain (CapEff != 0)"
echo "$OUT" | grep -q "NoNewPrivs:.*1"             && pass "no-new-privileges set"             || bad "no-new-privileges not set"

# ── 2. The potluck binary + curated tools are present in the image ──
echo "[2] in-container tooling"
docker run --rm ${BIND[@]+"${BIND[@]}"} "$IMAGE" potluck version >/dev/null 2>&1 && pass "potluck binary present" || bad "potluck binary missing"
HOOK=$(echo '{"tool_name":"Bash"}' | docker run --rm -i ${BIND[@]+"${BIND[@]}"} "$IMAGE" potluck __hook 2>/dev/null)
echo "$HOOK" | grep -q '"permissionDecision":"deny"' && pass "deny-hook blocks Bash in-image" || bad "deny-hook did not block Bash"

# ── 3. Egress: the agent shares a bridge with the broker AND can research the open web ──
# (Egress is open by design now — host safety comes from no-shell/no-files + the hardening
# above, not from locking the network. The agent reaches the broker by name for the key, and
# the open web for native research.)
echo "[3] sandbox network: broker reachable + open web for research"
NET=potluck-net-verify
docker network create "$NET" >/dev/null 2>&1
docker run -d --rm --name "$BROKER" --network "$NET" \
  -e ANTHROPIC_API_KEY=sk-ant-FAKE-verify ${BIND[@]+"${BIND[@]}"} \
  "$IMAGE" potluck __broker --addr 0.0.0.0:8787 >/dev/null 2>&1
sleep 2

PROBE='async function p(u){try{const r=await fetch(u,{signal:AbortSignal.timeout(5000)});return "REACHED "+r.status}catch(e){return "BLOCKED "+((e.message||e.name||"e").slice(0,30))}}
(async()=>{console.log("broker:",await p("http://'"$BROKER"':8787/v1/messages"));console.log("internet:",await p("https://example.com"));})();'
AGENT=$(docker run --rm --network "$NET" ${BIND[@]+"${BIND[@]}"} "$IMAGE" node -e "$PROBE" 2>&1)
echo "$AGENT"
echo "$AGENT" | grep -q "broker: REACHED"   && pass "agent reaches the broker (key injection path)" || bad "agent cannot reach the broker"
echo "$AGENT" | grep -q "internet: REACHED" && pass "agent can reach the open web (native research)" || bad "agent cannot reach the web"
docker network rm "$NET" >/dev/null 2>&1

echo
if [ "$fail" -eq 0 ]; then echo "SANDBOX-VERIFY PASSED"; else echo "SANDBOX-VERIFY FAILED"; fi
exit "$fail"
