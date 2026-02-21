# INVOKE + Discord Config Wiring Plan

**Date:** 2026-02-21
**Status:** PENDING
**Goal:** Fix two broken assumptions, add pod-level `invoke:`, and validate end-to-end with a working vertical spike against a live openclaw instance.

---

## What We Learned From the Source

### 1. INVOKE → `/etc/cron.d/claw` is wrong for openclaw

`emit.go:buildInfraLines()` writes `INVOKE` entries as system cron into `/etc/cron.d/claw`. Openclaw never reads system cron. It runs its own scheduler backed by `$OPENCLAW_STATE_DIR/cron/jobs.json` (default: `~/.openclaw/cron/jobs.json`). System cron entries are invisible to it.

Real `jobs.json` entry shape (from `~/.openclaw/cron/jobs.json`):
```json
{
  "id": "uuid-v4",
  "agentId": "main",
  "name": "Human-readable name",
  "enabled": true,
  "createdAtMs": 1770561257389,
  "updatedAtMs": 1770561257389,
  "schedule": {
    "expr": "0 23 * * 0",
    "tz": "America/New_York",
    "kind": "cron"
  },
  "sessionTarget": "isolated",
  "wakeMode": "now",
  "payload": {
    "kind": "agentTurn",
    "message": "Pre-market synthesis. Write to /mnt/clawd-shared/...",
    "timeoutSeconds": 300
  },
  "delivery": {
    "mode": "announce",
    "bestEffort": true,
    "to": "111222333444555666"
  },
  "state": {
    "nextRunAtMs": 0,
    "lastRunAtMs": 0,
    "lastStatus": "",
    "lastDurationMs": 0,
    "consecutiveErrors": 0
  }
}
```

Key fields:
- `delivery.to` — Discord channel ID for announce delivery. From `openclaw cron add --to <channelId>`. When absent, defaults to "last" (last active session channel).
- `agentId` — must match openclaw's agent ID. Default agent in a containerized openclaw is `"main"`.
- `wakeMode` — `"now"` runs immediately; `"next-heartbeat"` waits for next heartbeat cycle.
- `sessionTarget` — `"isolated"` runs in a fresh session; `"main"` injects into the live session.

### 2. Discord config path is wrong in the Clawfile

Our Clawfile generates (via `CONFIGURE`):
```
channels.discord.accounts [{"token":"${DISCORD_BOT_TOKEN}","applicationId":"${DISCORD_APP_ID}"}]
agents.defaults.bindings.discord.accountId ${DISCORD_APP_ID}
```

Neither of these keys exists in real openclaw config. Real structure (from `~/.openclaw/openclaw.json`):
```json
"channels": {
  "discord": {
    "enabled": true,
    "token": "${OPENCLAW_DISCORD_TOKEN}",
    "groupPolicy": "allowlist",
    "dmPolicy": "allowlist",
    "allowFrom": ["167037070349434880"],
    "dm": { "enabled": true },
    "guilds": {
      "1465489501551067136": {
        "requireMention": true,
        "users": ["167037070349434880"]
      }
    }
  }
}
```

The driver must generate this structure — not blindly pass CONFIGURE commands from the Clawfile.

### 3. Openclaw config and state paths

From `paths-CyR9Pa1R.js`:
- **State dir**: `$OPENCLAW_STATE_DIR` → default `~/.openclaw`
- **Config path**: `$OPENCLAW_CONFIG_PATH` → default `$OPENCLAW_STATE_DIR/openclaw.json`
- Jobs live at: `$OPENCLAW_STATE_DIR/cron/jobs.json`

The driver currently mounts `openclaw.json` at `/app/config/openclaw.json` but never sets `OPENCLAW_CONFIG_PATH`. This is broken — openclaw reads from `~/.openclaw/openclaw.json` (which is on tmpfs and empty). Fix: set both env vars explicitly.

Correct driver env vars to add:
```
OPENCLAW_CONFIG_PATH=/app/config/openclaw.json
OPENCLAW_STATE_DIR=/app/state
```

With `OPENCLAW_STATE_DIR=/app/state`:
- Config: `/app/config/openclaw.json` (bind-mounted read-only)
- Jobs: `/app/state/cron/jobs.json` (bind-mounted read-only)
- Writable state (tmpfs): `/app/state/cron/runs`, `/app/state/logs`, `/app/state/memory`, `/app/state/agents`, `/app/state/delivery-queue`

Remove `/root/.openclaw` from tmpfs — it's no longer the state dir.

---

## What We're Building

Three changes, one spike:

**A.** Fix INVOKE to write labels (not system cron) + driver reads them → jobs.json
**B.** Fix Discord config generation from handles (not raw CONFIGURE commands)
**C.** Add pod-level `invoke:` in x-claw with channel name wiring
**D.** Vertical spike: build mock trading-api, run `claw compose up`, verify the pipeline end-to-end

---

## Task 1: Fix the config and state dir paths in the driver

**Files:** `internal/driver/openclaw/driver.go`

The driver must tell openclaw where its config and state live.

**Step 1:** In `Materialize`, change the config mount container path from `/app/config/openclaw.json` to keep it, and add env vars:
```go
Environment: map[string]string{
    "CLAW_MANAGED":            "true",
    "OPENCLAW_CONFIG_PATH":    "/app/config/openclaw.json",
    "OPENCLAW_STATE_DIR":      "/app/state",
},
```

**Step 2:** Fix `Tmpfs` — replace `/root/.openclaw` with targeted writable state subdirs:
```go
Tmpfs: []string{
    "/tmp",
    "/run",
    "/app/state/cron/runs",
    "/app/state/logs",
    "/app/state/memory",
    "/app/state/agents",
    "/app/state/delivery-queue",
},
```
Remove `/app/data` from tmpfs (unused). Add the `/app/state/cron` parent directory handling — Docker creates mount points for bind-mounted files automatically, but the parent dir structure should be verified in the spike.

**Step 3:** Healthcheck still runs `openclaw health --json` — this should work with the env vars set.

**Step 4:** Run `go test ./...` — no test changes needed here, config path is not unit-tested.

**Step 5:** Commit.

---

## Task 2: Fix Discord config generation from handles

**Files:** `internal/driver/openclaw/config.go`

Currently the HANDLE handler only sets `channels.discord.enabled = true`. The full token and guild config must come from the handles block parsed at compose-up time.

The `ResolvedClaw.Handles["discord"]` has the full `HandleInfo`:
```go
HandleInfo{
    ID:       "${TIVERTON_DISCORD_ID}",   // env var ref — fine, openclaw resolves at startup
    Username: "tiverton",
    Guilds: []GuildInfo{
        {ID: "${DISCORD_GUILD_ID}", Name: "Trading Floor", Channels: [...]},
    },
}
```

**Step 1:** In `GenerateConfig`, extend the discord HANDLE case to build the full config:
```go
case "discord":
    setPath(config, "channels.discord.enabled", true)
    // Token — reference the env var openclaw will resolve at startup
    setPath(config, "channels.discord.token", "${DISCORD_BOT_TOKEN}")
    // Guilds — build object keyed by guild ID
    guilds := map[string]interface{}{}
    for _, g := range info.Guilds {
        guildCfg := map[string]interface{}{
            "requireMention": true,
        }
        // Collect user IDs from all channels' allowed users (if any)
        // For now: no per-guild user allowlist — operator adds via CONFIGURE if needed
        guilds[g.ID] = guildCfg
    }
    if len(guilds) > 0 {
        setPath(config, "channels.discord.guilds", guilds)
    }
    setPath(config, "channels.discord.groupPolicy", "allowlist")
    setPath(config, "channels.discord.dmPolicy", "allowlist")
```

Note: `info` here is `rc.Handles["discord"]` — need to thread it through. The HANDLE block in GenerateConfig currently only receives the platform name. Extend to pass the full HandleInfo.

**`allowFrom` note:** Real openclaw config has `"allowFrom": ["<userId>"]` — an allowlist of Discord user IDs allowed to message the bot. Without this, the agent refuses all inbound messages. For the spike, the operator must add their Discord user ID via CONFIGURE: `CONFIGURE openclaw config set channels.discord.allowFrom ["${OPERATOR_DISCORD_ID}"]`. Document this in the trading-desk example.

**Step 2:** Remove the wrong CONFIGURE lines from the trading-desk Clawfile — they're now generated by the driver:
```
# Remove these:
CONFIGURE openclaw config set channels.discord.accounts [...]
CONFIGURE openclaw config set agents.defaults.bindings.discord.accountId ${DISCORD_APP_ID}
```

**Step 3:** Add tests for discord config generation with a full HandleInfo including guilds.

**Step 4:** Run `go test ./...`, commit.

---

## Task 3: INVOKE labels: stop writing system cron, start baking labels

**Files:** `internal/clawfile/emit.go`, `internal/inspect/inspect.go`, `internal/driver/types.go`

INVOKE directives should follow the same path as CONFIGURE: baked as labels in the image, read by inspect at compose-up time, materialized by the driver.

**Step 1:** `emit.go` — remove the system cron block from `buildInfraLines`. Add invocation labels in `buildLabelLines`:
```go
for i, inv := range config.Invocations {
    // Format: "<5-field-schedule> <message>" — first 5 words are schedule, rest is message
    // Same format the parser already produces (Schedule + Command)
    lines = append(lines, formatLabel(
        fmt.Sprintf("claw.invoke.%d", i),
        inv.Schedule+" "+inv.Command,
    ))
}
```

**Step 2:** `inspect.go` — add `Invocations []Invocation` to `ClawInfo` (import clawfile or define a local type; prefer local to avoid circular deps). Parse `claw.invoke.N` labels:
```go
case strings.HasPrefix(key, "claw.invoke."):
    // value = "<schedule> <message>", first 5 words are schedule
    fields := strings.Fields(value)
    if len(fields) >= 6 {
        schedule := strings.Join(fields[:5], " ")
        message := strings.TrimSpace(strings.Join(fields[5:], " "))
        invocations = append(invocations, indexedEntry{Index: idx, Value: schedule+"\t"+message})
    }
```

Use a tab separator internally to preserve spaces in the schedule and message separately.

**Step 3:** `internal/driver/types.go` — add `Invocations []Invocation` to `ResolvedClaw`:
```go
type Invocation struct {
    Schedule string // 5-field cron expression
    Message  string // agent task payload
    To       string // Discord channel ID for delivery (empty = "last")
    Name     string // human-readable job name (optional)
}
```

**Step 4:** `cmd/claw/compose_up.go` — populate `rc.Invocations` from `info.Invocations`.

**Step 5:** Tests — update emit tests (no more cron.d output), add inspect label round-trip test.

**Step 6:** Run `go test ./...`, commit.

---

## Task 4: Driver generates jobs.json and mounts it

**Files:** `internal/driver/openclaw/driver.go`, new `internal/driver/openclaw/jobs.go`

**Step 1:** Create `jobs.go` with `GenerateJobsJSON(rc *driver.ResolvedClaw) ([]byte, error)`:
```go
type job struct {
    ID            string      `json:"id"`
    AgentID       string      `json:"agentId"`
    Name          string      `json:"name"`
    Enabled       bool        `json:"enabled"`
    CreatedAtMs   int64       `json:"createdAtMs"`
    UpdatedAtMs   int64       `json:"updatedAtMs"`
    Schedule      schedule    `json:"schedule"`
    SessionTarget string      `json:"sessionTarget"`
    WakeMode      string      `json:"wakeMode"`
    Payload       payload     `json:"payload"`
    Delivery      delivery    `json:"delivery"`
    State         jobState    `json:"state"`
}
// ... and supporting types matching the real jobs.json schema above
```

For each `rc.Invocations` entry:
- `id`: deterministic UUID from hash of `(serviceName + schedule + message)` — so re-running compose up produces the same job IDs (idempotent).
- `agentId`: `"main"` (openclaw default agent in container)
- `name`: first 60 chars of Message if no explicit Name
- `schedule.expr`: the cron expression
- `schedule.tz`: `"UTC"` (default; operator can override via CONFIGURE)
- `schedule.kind`: `"cron"`
- `sessionTarget`: `"isolated"`
- `wakeMode`: `"now"`
- `payload.kind`: `"agentTurn"`
- `payload.message`: the INVOKE message
- `payload.timeoutSeconds`: 300 (default; operator can override)
- `delivery.mode`: `"announce"`
- `delivery.bestEffort`: true
- `delivery.to`: `inv.To` (empty string = omit field, openclaw defaults to "last")

**Step 2:** In `Materialize`, generate and mount jobs.json:
```go
if len(rc.Invocations) > 0 {
    jobsData, err := GenerateJobsJSON(rc)
    if err != nil {
        return nil, fmt.Errorf("openclaw driver: jobs generation failed: %w", err)
    }
    jobsDir := filepath.Join(opts.RuntimeDir, "state", "cron")
    if err := os.MkdirAll(jobsDir, 0700); err != nil {
        return nil, fmt.Errorf("openclaw driver: create jobs dir: %w", err)
    }
    jobsPath := filepath.Join(jobsDir, "jobs.json")
    if err := os.WriteFile(jobsPath, jobsData, 0644); err != nil {
        return nil, fmt.Errorf("openclaw driver: write jobs.json: %w", err)
    }
    mounts = append(mounts, driver.Mount{
        HostPath:      jobsPath,
        ContainerPath: "/app/state/cron/jobs.json",
        ReadOnly:      true,
    })
}
```

**Step 3:** Tests — `TestGenerateJobsJSON`: one invocation with and without `To`, verify JSON structure matches expected. Test deterministic IDs.

**Step 4:** Run `go test ./...`, commit.

---

## Task 5: Pod-level `invoke:` in x-claw

**Files:** `internal/pod/types.go`, `internal/pod/parser.go`, `cmd/claw/compose_up.go`

**Step 1:** `types.go` — add to ClawBlock:
```go
type InvokeEntry struct {
    Schedule string // 5-field cron expression
    Message  string // agent task payload
    Name     string // optional human-readable name
    To       string // optional channel name (looked up from handles at compose-up)
}
```
Add `Invoke []InvokeEntry` to `ClawBlock`.

**Step 2:** `parser.go` — add to `rawClawBlock`:
```yaml
invoke:
  - schedule: "15 8 * * 1-5"
    message: "Pre-market synthesis..."
    name: "Pre-market (optional)"
    to: "trading-floor"        # channel name, looked up from handles
```

Parse each entry; validate schedule with existing `validateCronSchedule` logic (reuse or import).

**Step 3:** `compose_up.go` — after building `rc`, resolve pod-level invocations:
```go
for _, entry := range svc.Claw.Invoke {
    inv := driver.Invocation{
        Schedule: entry.Schedule,
        Message:  entry.Message,
        Name:     entry.Name,
    }
    // Resolve channel name → ID from handles
    if entry.To != "" {
        inv.To = resolveChannelID(svc.Claw.Handles, entry.To)
        if inv.To == "" {
            fmt.Printf("[claw] warning: service %q: invoke channel %q not found in handles; delivery will use last channel\n", name, entry.To)
        }
    }
    rc.Invocations = append(rc.Invocations, inv)
}
```

Where `resolveChannelID` scans `handles["discord"].Guilds[].Channels[]` matching on `channel.Name`.

**Step 4:** Pod-level invocations merge with image-level (Clawfile) invocations. Image-level come from `info.Invocations`; pod-level from `svc.Claw.Invoke`. Both are appended to `rc.Invocations`.

**Step 5:** Tests — `TestParsePodInvoke`: pod with x-claw.invoke entries, verify parsed. `TestResolveChannelID`: verify name → ID lookup against handles map.

**Step 6:** Run `go test ./...`, commit.

---

## Task 6: Update the trading-desk Clawfile and pod

**Files:** `examples/trading-desk/Clawfile`, `examples/trading-desk/claw-pod.yml`

**Single-channel contract for the spike:** All agents use one canonical channel.
- Env var: `DISCORD_TRADING_FLOOR_CHANNEL`
- Channel name in pod: `trading-floor`
- All `invoke.to` entries use `trading-floor`
- trading-api notifications target this same channel ID

This simplifies end-to-end validation — one channel, all participants visible in one place.

**Step 1:** Remove the wrong CONFIGURE lines from Clawfile (done when Task 2 lands, but update example now):
- Remove `CONFIGURE openclaw config set channels.discord.accounts [...]`
- Remove `CONFIGURE openclaw config set agents.defaults.bindings.discord.accountId ${DISCORD_APP_ID}`
- Keep `HANDLE discord` (driver uses this to trigger discord config generation)
- Add `CONFIGURE openclaw config set agents.defaults.heartbeat.every 15m` (valid key, confirmed in real config)
- Add `CONFIGURE openclaw config set channels.discord.allowFrom ["${OPERATOR_DISCORD_ID}"]` so the bot accepts messages

**Step 2:** Remove `DISCORD_APP_ID` from all agent `environment:` blocks in `claw-pod.yml`. This env var was used by the wrong CONFIGURE commands. It has no role in the driver's generated config.

**Step 3:** Target shape for each agent service in `claw-pod.yml` (no YAML anchors — compose deep-merge of `x-claw` is unreliable; repeat per agent):

```yaml
  tiverton:
    build:
      context: .
      dockerfile: Clawfile
    x-claw:
      agent: ./agents/TIVERTON.md
      handles:
        discord:
          id: "${TIVERTON_DISCORD_ID}"
          username: "tiverton"
          guilds:
            - id: "${DISCORD_GUILD_ID}"
              name: "Trading Floor"
              channels:
                - id: "${DISCORD_TRADING_FLOOR_CHANNEL}"
                  name: trading-floor
      surfaces:
        - "service://trading-api"
        - "volume://clawd-shared read-write"
      skills:
        - ./policy/risk-limits.md
        - ./policy/approval-workflow.md
      invoke:
        - schedule: "15 8 * * 1-5"
          name: "Pre-market synthesis"
          message: "Pre-market synthesis. Write to /mnt/clawd-shared/reports/morning/$(date +%Y-%m-%d)-tiverton.md then post brief to #trading-floor."
          to: trading-floor
        - schedule: "*/5 * * * *"
          name: "News poll"
          message: "Scan latest news in /mnt/clawd-shared/news/latest.md and post high-impact items."
          to: trading-floor
    environment:
      DISCORD_BOT_TOKEN: "${TIVERTON_BOT_TOKEN}"
```

Note: Do NOT use `labels: claw.skill.emit:` in the compose service block — that label must be baked into the image via `Dockerfile.trading-api`. No compose-level redeclaration needed.

**Step 4:** Apply the same shape to all seven agents. Allen uses only `infra` channel; all others use `trading-floor`. Remove the now-redundant `DISCORD_APP_ID` env var from each service.

**Step 5:** Run `go build -o bin/claw ./cmd/claw`, commit.

---

## Task 7: Vertical spike — end-to-end validation

**Goal:** Prove the pipeline works against your live openclaw instance, not just unit tests.

**Acceptance criteria — all must pass:**
1. `examples/trading-desk` contains exactly one trader `Clawfile` used by every agent service.
2. `claw compose up -d` runs without error against the trading-desk pod.
3. `compose.generated.yml` contains correct mounts for: `openclaw.json`, `jobs.json`, `surface-trading-api.md` (via claw.skill.emit).
4. Per-agent generated `openclaw.json` has valid Discord structure: `token`, `guilds` keyed by ID, `groupPolicy`, `dmPolicy`.
5. Per-agent generated `jobs.json` has `agentTurn` jobs with `delivery.mode: "announce"` and `delivery.to` resolved to the `trading-floor` channel ID.
6. `docker exec <container> cat /app/config/openclaw.json` shows the correct config (not empty).
7. `docker exec <container> cat /app/state/cron/jobs.json` shows the jobs with resolved channel IDs.
8. `docker exec <container> ls /claw/skills/` shows `surface-trading-api.md` extracted from the trading-api image.
9. `docker exec <container> openclaw health --json` returns healthy.
10. trading-api can post to the same Discord channel agents use (same `DISCORD_TRADING_FLOOR_CHANNEL` env var).

**Setup for spike:** Use a single-agent version of the trading-desk (e.g., just `tiverton`) with real env vars from your `.env`. The spike doesn't need all seven agents.

**Spike script** (for manual verification):
```bash
cd examples/trading-desk

# Build mock trading-api (has claw.skill.emit)
docker build -f Dockerfile.trading-api -t trading-api:latest .

# Build the claw agent image
# (claw build will transpile Clawfile → Dockerfile.generated, then docker build)
claw build -t claw-trading-agent:latest .

# Run compose up
claw compose up -d claw-pod.yml

# Verify
docker exec trading-desk-tiverton-1 cat /app/config/openclaw.json
docker exec trading-desk-tiverton-1 cat /app/state/cron/jobs.json
docker exec trading-desk-tiverton-1 ls /claw/skills/
docker exec trading-desk-tiverton-1 openclaw health --json
```

**Step 1:** Write a `spike_test.go` with build tag `//go:build spike` that:
- Builds the mock trading-api image programmatically
- Calls `runComposeUp` on a single-agent version of the trading-desk pod
- Asserts `compose.generated.yml` contains the expected mounts
- Asserts the generated `openclaw.json` and `jobs.json` have correct structure

**Step 2:** Run spike manually first, then capture as a test.

**Step 3:** Update `docs/plans/phase2-progress.md` with completion status.

---

## Key Files

| File | Change |
|------|--------|
| `internal/driver/openclaw/driver.go` | Fix env vars, fix tmpfs list, add jobs.json mount |
| `internal/driver/openclaw/config.go` | Fix discord config generation from HandleInfo |
| `internal/driver/openclaw/jobs.go` | New: GenerateJobsJSON |
| `internal/driver/types.go` | Add Invocation type to ResolvedClaw |
| `internal/clawfile/emit.go` | INVOKE → labels (not /etc/cron.d) |
| `internal/inspect/inspect.go` | Parse claw.invoke.N labels |
| `internal/pod/types.go` | Add InvokeEntry, Invoke []InvokeEntry to ClawBlock |
| `internal/pod/parser.go` | Parse x-claw.invoke from YAML |
| `cmd/claw/compose_up.go` | Wire pod invocations + channel resolution into rc |
| `examples/trading-desk/Clawfile` | Remove wrong CONFIGURE lines |
| `examples/trading-desk/claw-pod.yml` | Add per-agent invoke: blocks |

## Verification

1. `go test ./...` — all packages
2. `go build -o bin/claw ./cmd/claw` — compiles
3. `go vet ./...` — clean
4. Manual spike against live openclaw confirms config loads and jobs appear in `openclaw cron list`
