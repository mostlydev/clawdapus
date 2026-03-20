# Spike Test: Forced cllama Proxy Validation

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Make `TestSpikeRollCall` prove that every claw routes LLM inference through cllama, not just picoclaw.

**Architecture:** Modify `discord-responder.sh` to make one real LLM call through the cllama proxy before posting to Discord. Add a `claw audit` assertion to the spike test that verifies telemetry from all 7 agents. Fall back to the static message if the LLM call fails so the test degrades visibly but doesn't hard-fail on infra issues.

**Tech Stack:** Shell (curl to cllama proxy), Go (spike test assertions), Docker

---

## Current Problem

All 7 rollcall agents declare `cllama: passthrough` in `claw-pod.yml`, but only picoclaw's native agent loop makes real LLM calls. The other 6 use `discord-responder.sh` which posts a hardcoded string directly to Discord, bypassing cllama entirely. The spike test passes without proving the proxy contract.

## Change Summary

1. `discord-responder.sh` gains an LLM call through cllama before posting
2. The spike test asserts `claw audit` shows telemetry for all 7 agents
3. Picoclaw's base Dockerfile gets `discord-responder.sh` + the entrypoint wrapper so it follows the same pattern as the other stubs (its native gateway doesn't participate in the roll call today anyway)

---

### Task 1: Make `discord-responder.sh` Call cllama

The script already has `CLLAMA_TOKEN` available in the container environment (injected by `compose_emit.go:247`). The cllama proxy is at `http://cllama:8080/v1/chat/completions` on the `claw-internal` network.

**Files:**
- Modify: `examples/rollcall/discord-responder.sh`

**Step 1: Add LLM call function to discord-responder.sh**

Replace the hardcoded response block (lines 92-100) with an LLM-then-fallback pattern. The full updated script:

```sh
#!/bin/sh
# discord-responder.sh — Background process for stub runtimes.
# Polls Discord channel via REST API, responds to mentions of DISCORD_BOT_ID.
# When CLLAMA_TOKEN is set, makes a real LLM call through the cllama proxy
# before posting, proving the forced inference proxy contract.
#
# Required env vars: DISCORD_BOT_TOKEN, DISCORD_BOT_ID, ROLLCALL_CHANNEL_ID, CLAW_RUNTIME
# Optional env vars: CLLAMA_TOKEN (enables real LLM call through proxy)
set -eu

TOKEN="${DISCORD_BOT_TOKEN:-}"
BOT_ID="${DISCORD_BOT_ID:-}"
CHANNEL_ID="${ROLLCALL_CHANNEL_ID:-}"
RUNTIME="${CLAW_RUNTIME:-unknown}"
CLLAMA="${CLLAMA_TOKEN:-}"
UA="DiscordBot (https://github.com/mostlydev/clawdapus, 1.0)"

[ -n "$TOKEN" ] && [ -n "$BOT_ID" ] && [ -n "$CHANNEL_ID" ] || {
  echo "[discord-responder] missing env vars (TOKEN=${TOKEN:+set} BOT_ID=$BOT_ID CHANNEL=$CHANNEL_ID), exiting" >&2
  exit 0
}

echo "[discord-responder] polling channel $CHANNEL_ID for mentions of $BOT_ID (runtime=$RUNTIME)" >&2
if [ -n "$CLLAMA" ]; then
  echo "[discord-responder] cllama token present — will route inference through proxy" >&2
else
  echo "[discord-responder] no cllama token — will use static response" >&2
fi

# Call cllama proxy for a real LLM response.
# Returns the LLM text on stdout, or empty string on failure.
call_cllama() {
  [ -n "$CLLAMA" ] || return 0
  payload=$(cat <<ENDJSON
{
  "model": "anthropic/claude-sonnet-4",
  "max_tokens": 80,
  "messages": [
    {"role": "system", "content": "You are ${RUNTIME} in a roll call. Respond in exactly one sentence stating your name and runtime. Do not use markdown."},
    {"role": "user", "content": "Roll call! Introduce yourself and state what runtime you are running on."}
  ]
}
ENDJSON
)
  resp=$(curl -s --max-time 30 \
    -H "Authorization: Bearer $CLLAMA" \
    -H "Content-Type: application/json" \
    -d "$payload" \
    "http://cllama:8080/v1/chat/completions" 2>/dev/null) || { echo ""; return 0; }

  # Extract content from OpenAI-format response
  content=$(printf '%s' "$resp" | jq -r '.choices[0].message.content // empty' 2>/dev/null) || content=""
  printf '%s' "$content"
}

fetch_messages() {
  url="https://discord.com/api/v10/channels/$CHANNEL_ID/messages?limit=20"
  if [ -n "${1:-}" ]; then
    url="${url}&after=$1"
  fi
  curl -s -w "\n---HTTP_CODE:%{http_code}---" \
    -H "Authorization: Bot $TOKEN" \
    -H "User-Agent: $UA" \
    "$url" 2>/dev/null
}

parse_response() {
  resp="$1"
  http_code=$(printf '%s' "$resp" | grep -o 'HTTP_CODE:[0-9]*' | head -1 | cut -d: -f2)
  json_body=$(printf '%s' "$resp" | sed 's/---HTTP_CODE:[0-9]*---$//')
}

# Snapshot the latest message ID so we only react to NEW messages.
echo "[discord-responder] taking baseline snapshot..." >&2
baseline_id=""
for attempt in 1 2 3; do
  resp=$(fetch_messages "")
  parse_response "$resp"
  if [ "$http_code" = "200" ]; then
    clean=$(printf '%s' "$json_body" | tr -d '\000-\011\013-\037')
    baseline_id=$(printf '%s' "$clean" | jq -r '.[0].id // empty' 2>/dev/null) || baseline_id=""
    if [ -n "$baseline_id" ]; then
      echo "[discord-responder] baseline message ID: $baseline_id" >&2
      break
    fi
  fi
  sleep 2
done

if [ -z "$baseline_id" ]; then
  echo "[discord-responder] could not get baseline (empty channel?), using 0" >&2
  baseline_id="0"
fi

# Now poll for new messages after the baseline.
for i in $(seq 1 60); do
  resp=$(fetch_messages "$baseline_id")
  parse_response "$resp"

  if [ "$http_code" != "200" ]; then
    echo "[discord-responder] curl returned HTTP $http_code (attempt $i)" >&2
    sleep 5
    continue
  fi

  # Sanitize control characters that break jq (keep newlines 0x0A).
  clean=$(printf '%s' "$json_body" | tr -d '\000-\011\013-\037')
  msg_count=$(printf '%s' "$clean" | jq 'length' 2>/dev/null) || msg_count="jq-error"

  # Check for a message mentioning our bot ID (not from our own bot).
  has_trigger=$(printf '%s' "$clean" | jq -r \
    --arg bid "$BOT_ID" \
    '[.[] | select(.author.id != $bid) | select(.content | test($bid))] | length' 2>/dev/null) || has_trigger=0

  if [ "$i" -le 3 ] || [ "$((i % 10))" -eq 0 ]; then
    echo "[discord-responder] attempt $i: new_msgs=$msg_count triggers=$has_trigger" >&2
  fi

  if [ "$has_trigger" -gt 0 ]; then
    # Check if we already responded (avoid duplicate responses).
    already=$(printf '%s' "$clean" | jq -r \
      --arg rt "$RUNTIME" \
      '[.[] | select(.author.id == $bid) | select(.content | ascii_downcase | test($rt | ascii_downcase))] | length' \
      --arg bid "$BOT_ID" 2>/dev/null) || already=0

    if [ "$already" -eq 0 ]; then
      echo "[discord-responder] found trigger (attempt $i), calling LLM for $RUNTIME" >&2

      # Try real LLM call through cllama; fall back to static message
      llm_response=$(call_cllama)
      if [ -n "$llm_response" ]; then
        message="$llm_response"
        echo "[discord-responder] got LLM response via cllama proxy" >&2
      else
        message="I'm running on ${RUNTIME}. Stub runtime reporting for duty! (no cllama)"
        echo "[discord-responder] cllama unavailable, using static fallback" >&2
      fi

      # Escape the message for JSON (handle quotes and backslashes)
      escaped=$(printf '%s' "$message" | jq -Rs '.')
      send_resp=$(curl -s -w "\n%{http_code}" -X POST \
        -H "Authorization: Bot $TOKEN" \
        -H "Content-Type: application/json" \
        -H "User-Agent: $UA" \
        -d "{\"content\":$escaped}" \
        "https://discord.com/api/v10/channels/$CHANNEL_ID/messages" 2>/dev/null)
      send_code=$(printf '%s' "$send_resp" | tail -1)
      echo "[discord-responder] send response HTTP $send_code" >&2
      exit 0
    else
      echo "[discord-responder] already responded for $RUNTIME (attempt $i)" >&2
      exit 0
    fi
  fi

  sleep 5
done

echo "[discord-responder] timed out after 5 minutes without finding trigger" >&2
```

**Step 2: Verify script is valid shell**

Run: `shellcheck examples/rollcall/discord-responder.sh` (if available) or `sh -n examples/rollcall/discord-responder.sh`
Expected: no syntax errors

**Step 3: Commit**

```bash
git add examples/rollcall/discord-responder.sh
git commit -m "feat(rollcall): route inference through cllama proxy in discord-responder"
```

---

### Task 2: Add discord-responder to picoclaw

Picoclaw currently uses its native gateway as the entrypoint. Its gateway handles Discord natively via picoclaw's platform handlers, but in the rollcall test the response actually comes from the gateway's agent loop — which does go through cllama. However, for consistency and to guarantee the stub path works the same way across all drivers, give picoclaw the same `discord-responder.sh` pattern.

**Files:**
- Modify: `examples/rollcall/Dockerfile.picoclaw-base`

**Step 1: Add discord-responder to picoclaw base image**

Add the discord-responder script and an entrypoint wrapper, matching the pattern from the other base images. Insert before the `ENTRYPOINT` line:

```dockerfile
# Base image for picoclaw agents.
# Usage: docker build -t picoclaw:latest -f Dockerfile.picoclaw-base .
#
# Multi-stage: build from source, then copy binary to alpine runtime.

FROM golang:1.25-alpine AS builder

RUN apk add --no-cache git make

WORKDIR /src
RUN git clone --depth 1 https://github.com/sipeed/picoclaw.git .
RUN mkdir -p workspace && make build VERSION=dev

FROM alpine:3.23

RUN apk add --no-cache ca-certificates tzdata curl wget jq

RUN adduser -D -u 1000 picoclaw
USER picoclaw

COPY --from=builder /src/build/picoclaw /usr/local/bin/picoclaw

RUN picoclaw onboard 2>/dev/null || true

# Discord responder for spike tests (same pattern as other stubs)
USER root
COPY discord-responder.sh /usr/local/bin/discord-responder
RUN chmod +x /usr/local/bin/discord-responder

COPY <<'WRAPPER' /usr/local/bin/picoclaw-entrypoint
#!/bin/sh
/usr/local/bin/discord-responder &
exec su -s /bin/sh picoclaw -c "picoclaw gateway"
WRAPPER
RUN chmod +x /usr/local/bin/picoclaw-entrypoint
USER picoclaw

EXPOSE 18790

HEALTHCHECK --interval=30s --timeout=5s --retries=3 \
    CMD wget -q --spider http://localhost:18790/health || exit 1

ENTRYPOINT ["picoclaw-entrypoint"]
```

Note: picoclaw's native gateway may also make LLM calls through cllama (since the driver wires `api_base` to the proxy). That's fine — the discord-responder call guarantees at least one proxied call even if the gateway doesn't trigger on time. The spike test will see telemetry either way.

**Step 2: Add `CLAW_RUNTIME` to picoclaw's pod YAML environment**

The discord-responder needs `CLAW_RUNTIME` to identify itself. Currently pc-roll's environment block in `claw-pod.yml` doesn't set it. Add it:

In `examples/rollcall/claw-pod.yml`, in the `pc-roll` service's `environment:` block, add:

```yaml
      CLAW_RUNTIME: "picoclaw"
```

**Step 3: Commit**

```bash
git add examples/rollcall/Dockerfile.picoclaw-base examples/rollcall/claw-pod.yml
git commit -m "feat(rollcall): add discord-responder to picoclaw for consistent cllama validation"
```

---

### Task 3: Add `claw audit` Telemetry Assertion to Spike Test

After the Discord poll loop succeeds, use `claw audit --json` to verify that cllama recorded telemetry for all 7 agents. This is the key assertion that proves the proxy contract.

**Files:**
- Modify: `cmd/claw/spike_rollcall_test.go`

**Step 1: Add the telemetry assertion**

After the existing cllama costs check (line 261), before the clawdash check, add a `claw audit` assertion block. This calls `claw audit --json --since 10m` against the live pod and checks that each agent's `claw_id` appears in the telemetry.

Insert after line 261 (`}`) and before line 263 (`// ── Verify clawdash`).

The JSON shape from `claw audit --json` is:
```json
{
  "pod": "rollcall",
  "skipped_lines": 2,
  "summary": {
    "agents": [{"claw_id": "pc-roll", "requests": 2, ...}],
    "requests": 4, ...
  },
  "events": [...]
}
```

So the assertion must parse `summary.agents` as an array and check that each agent's `claw_id` appears:

```go
	// ── Verify all agents routed inference through cllama ────────────
	// This is the forced proxy contract assertion. Every claw that declares
	// cllama: passthrough must have at least one telemetry event in the
	// cllama proxy logs. If an agent used a static fallback instead of
	// calling the LLM, it will be missing from claw audit output.
	t.Log("checking claw audit telemetry for all agents...")
	auditOut, auditErr := exec.Command(
		"go", "run", "../../cmd/claw/", "audit",
		"-f", spikePodPath,
		"--json", "--since", "10m",
	).CombinedOutput()
	if auditErr != nil {
		t.Logf("warning: claw audit failed: %v\n%s", auditErr, string(auditOut))
	} else {
		var auditResult struct {
			Summary struct {
				Agents []struct {
					ClawID   string `json:"claw_id"`
					Requests int    `json:"requests"`
				} `json:"agents"`
			} `json:"summary"`
		}
		if json.Unmarshal(auditOut, &auditResult) == nil {
			agentSet := make(map[string]bool)
			for _, a := range auditResult.Summary.Agents {
				agentSet[a.ClawID] = true
			}
			t.Logf("claw audit: telemetry for %d agents: %v", len(agentSet), agentSet)
			for _, a := range allAgents {
				if !agentSet[a.name] {
					t.Errorf("claw audit: missing telemetry for %s (%s) — inference did not route through cllama", a.name, a.runtime)
				} else {
					t.Logf("claw audit: confirmed telemetry for %s (%s)", a.name, a.runtime)
				}
			}
		} else {
			t.Logf("warning: could not parse claw audit JSON: %s", string(auditOut))
		}
	}
```

**Step 2: Check that `claw audit --json` output shape matches**

The assertion assumes `claw audit --json` returns a JSON object with an `agents` map keyed by `claw_id`. Read `cmd/claw/audit.go` to verify the JSON output shape. If the shape is different (e.g. `summaries` instead of `agents`, or an array), adjust the struct accordingly.

Run: `go run ./cmd/claw/ audit --help` to check available flags.

**Step 3: Verify the test compiles**

Run: `go build -tags spike ./cmd/claw/`
Expected: clean build

**Step 4: Commit**

```bash
git add cmd/claw/spike_rollcall_test.go
git commit -m "test(spike): assert claw audit shows telemetry for all 7 agents"
```

---

### Task 4: Verify End-to-End

This is a manual verification step, not automated.

**Step 1: Run the spike test**

Run: `go test -tags spike -v -run TestSpikeRollCall -timeout 10m ./cmd/claw/...`

Expected output should include:
- All 7 runtime responses found in Discord
- `claw audit: found telemetry for oc-roll (openclaw)`
- `claw audit: found telemetry for nc-roll (nullclaw)`
- `claw audit: found telemetry for mc-roll (microclaw)`
- `claw audit: found telemetry for nano-roll (nanoclaw)`
- `claw audit: found telemetry for nb-roll (nanobot)`
- `claw audit: found telemetry for pc-roll (picoclaw)`
- `claw audit: found telemetry for hm-roll (hermes)`

If any agent shows "missing telemetry", check:
1. Does the container have `CLLAMA_TOKEN` in its environment? (`docker inspect rollcall-<name>-1`)
2. Can the container reach `cllama:8080`? (`docker exec rollcall-<name>-1 curl -s http://cllama:8080/health`)
3. Check the agent's logs for the `[discord-responder]` output — does it say "got LLM response via cllama proxy" or "cllama unavailable, using static fallback"?

**Step 2: Commit final state if test passes**

```bash
git add -A
git commit -m "feat(spike): rollcall validates forced cllama proxy for all 7 drivers"
```

---

## Notes

- The `call_cllama` function uses `anthropic/claude-sonnet-4` as the model. This matches what most agents declare in their Clawfiles. cllama will route it to whatever provider key is available (`OPENROUTER_API_KEY` or `ANTHROPIC_API_KEY`).
- The `max_tokens: 80` keeps cost low — each agent makes exactly one short LLM call.
- The fallback to a static message (with `(no cllama)` suffix) means the Discord response check still passes even if cllama is down, but the `claw audit` assertion will catch the missing telemetry.
- Some agents (like picoclaw) may generate telemetry from both the discord-responder call AND their native agent loop. That's fine — the test only checks that at least one event exists per agent.
