#!/usr/bin/env bash
# Phase-2 sandbox empirical verification — the prime directive in action (plans/prelaunch.md
# §2, §7): every security property of the v2 sandbox is PROVEN by running it, not asserted in
# prose. Exits non-zero on any failed property, so it can gate a launch.
#
#   usage: bash scripts/verify-sandbox.sh
#   needs: Docker, and the image built:
#          docker build -t potluck-sandbox:phase2 -f docker/Dockerfile.phase2 .
set -uo pipefail

IMAGE="${POTLUCK_SANDBOX_IMAGE:-potluck-sandbox:phase2}"
EGRESS=potluck-egress-verify
PUBLIC=potluck-public-verify
BROKER=potluck-broker-verify
fail=0
pass() { printf '  PASS  %s\n' "$1"; }
bad()  { printf '  FAIL  %s\n' "$1"; fail=1; }

cleanup() {
  docker rm -f "$BROKER" >/dev/null 2>&1
  docker network rm "$EGRESS" "$PUBLIC" >/dev/null 2>&1
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
docker run --rm "$IMAGE" potluck version >/dev/null 2>&1 && pass "potluck binary present" || bad "potluck binary missing"
HOOK=$(echo '{"tool_name":"Bash"}' | docker run --rm -i "$IMAGE" potluck __hook 2>/dev/null)
echo "$HOOK" | grep -q '"permissionDecision":"deny"' && pass "deny-hook blocks Bash in-image" || bad "deny-hook did not block Bash"

# ── 3. Default-deny egress: agent reaches ONLY the broker, never the internet ──
echo "[3] default-deny egress (sidecar broker model)"
docker network create --internal "$EGRESS" >/dev/null 2>&1
docker network create "$PUBLIC" >/dev/null 2>&1
# Broker sidecar (fake key — we test reachability/topology, not real forwarding).
docker run -d --rm --name "$BROKER" --network "$EGRESS" \
  -e ANTHROPIC_API_KEY=sk-ant-FAKE-verify \
  "$IMAGE" potluck __broker --addr 0.0.0.0:8787 >/dev/null 2>&1
docker network connect "$PUBLIC" "$BROKER" >/dev/null 2>&1
sleep 2

PROBE='async function p(u){try{const r=await fetch(u,{signal:AbortSignal.timeout(4000)});return "REACHED "+r.status}catch(e){return "BLOCKED "+((e.message||e.name||"e").slice(0,30))}}
(async()=>{console.log("broker:",await p("http://'"$BROKER"':8787/v1/messages"));console.log("internet:",await p("https://example.com"));})();'
AGENT=$(docker run --rm --network "$EGRESS" "$IMAGE" node -e "$PROBE" 2>&1)
echo "$AGENT"
echo "$AGENT" | grep -q "broker: REACHED"   && pass "agent can reach the broker sidecar"            || bad "agent cannot reach the broker"
echo "$AGENT" | grep -q "internet: BLOCKED" && pass "agent CANNOT reach the internet (default-deny)" || bad "agent reached the internet (egress NOT denied)"
BRK=$(docker exec "$BROKER" node -e "fetch('https://example.com',{signal:AbortSignal.timeout(4000)}).then(r=>console.log('REACHED',r.status)).catch(e=>console.log('BLOCKED',e.message))" 2>&1)
echo "$BRK" | grep -q "REACHED" && pass "broker (dual-homed) can reach the provider" || bad "broker cannot reach the provider"

echo
if [ "$fail" -eq 0 ]; then echo "SANDBOX-VERIFY PASSED"; else echo "SANDBOX-VERIFY FAILED"; fi
exit "$fail"
