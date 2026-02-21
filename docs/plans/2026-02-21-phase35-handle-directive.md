# Phase 3.5: HANDLE Directive + Social Topology Projection

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** `HANDLE <platform>` in Clawfile declares an agent's public social identity. Two things happen: (1) the driver enables the platform in the runner's native config (so the agent can communicate on it), and (2) `claw up` aggregates handles across the pod and broadcasts `CLAW_HANDLE_<SERVICE>_<PLATFORM>=<user-id>` environment variables into every service — so a Rails API, another agent, or any pod member can target a specific agent by name on a platform.

**The Leviathan Pattern:** A trading bot needs human approval before executing a large trade. A Rails API monitors positions and needs to ping the right agent on Discord. It reads `CLAW_HANDLE_TIVERTON_DISCORD` and constructs `<@123456789>`. The agent sees the mention, reviews, approves. No hardcoding, no service discovery — just an env var injected by Clawdapus.

**Architecture:**

- `HANDLE discord` in Clawfile → `LABEL claw.handle.discord=true` at build time
- `handles: { discord: "123456789" }` in x-claw service block → declares the agent's user ID on that platform
- At `claw up`: collect all `(service, platform, user-id)` triples from the pod, generate env vars, inject into every service's environment
- OpenClaw driver: `HANDLE discord` → `set channels.discord.enabled true` in JSON5 config
- CLAWDAPUS.md: `## Handles` section so the agent knows its own public identities

**Key reference files:**
- `internal/clawfile/directives.go` — directive type definitions
- `internal/clawfile/parser.go` — Clawfile line parser
- `internal/clawfile/emit.go` — Dockerfile label emission
- `internal/inspect/inspect.go` — label parsing from built image
- `internal/pod/types.go` — ClawBlock, ResolvedClaw types
- `internal/pod/parser.go` — x-claw YAML parsing
- `internal/driver/types.go` — ResolvedClaw
- `cmd/claw/compose_up.go` — main orchestration
- `internal/pod/compose_emit.go` — compose YAML generation
- `internal/driver/openclaw/driver.go` — config injection
- `internal/driver/openclaw/clawdapus_md.go` — CLAWDAPUS.md generation

---

## Task 1: Add HandleDirective to Clawfile parser

**File:** `internal/clawfile/directives.go`

Add a `HandleDirective` struct alongside the existing directive types:

```go
type HandleDirective struct {
    Platform string // "discord", "slack", "telegram"
}
```

**File:** `internal/clawfile/parser.go`

Parse `HANDLE <platform>` lines. The directive takes a single argument (the platform name, lowercased). Multiple `HANDLE` directives are allowed for multiple platforms.

- If no argument: parse error
- If more than one token: parse error (platform names are single words)
- Collect into `ParsedClawfile.Handles []HandleDirective`

Add `Handles []HandleDirective` field to `ParsedClawfile`.

**Test:** `internal/clawfile/parser_test.go`

- `HANDLE discord` → HandleDirective{Platform: "discord"}
- `HANDLE slack` → HandleDirective{Platform: "slack"}
- Two HANDLE directives in one file → both collected
- `HANDLE` with no argument → error
- `HANDLE discord extra` → error

---

## Task 2: Emit HANDLE labels in Dockerfile

**File:** `internal/clawfile/emit.go`

For each HandleDirective, emit:
```
LABEL claw.handle.<platform>=true
```

e.g. `LABEL claw.handle.discord=true`

The value is `true` (a boolean flag — the user ID is declared in claw-pod.yml, not the image). Multiple HANDLE directives produce multiple LABEL lines.

Add HANDLE to the directive table in the emitted Dockerfile comment block if one exists.

**Test:** `internal/clawfile/emit_test.go`

- `HANDLE discord` → `LABEL claw.handle.discord=true` present in generated Dockerfile
- `HANDLE discord` + `HANDLE slack` → both labels present

---

## Task 3: Parse claw.handle.* labels in inspect

**File:** `internal/inspect/inspect.go`

Add `Handles []string` to `ClawInfo`. Platforms are collected from `claw.handle.*` labels:

```go
case strings.HasPrefix(key, "claw.handle."):
    platform := strings.TrimPrefix(key, "claw.handle.")
    info.Handles = append(info.Handles, platform)
```

Order: sort `Handles` alphabetically for deterministic output.

**Test:** `internal/inspect/inspect_test.go`

- Image with `claw.handle.discord=true` → ClawInfo.Handles = ["discord"]
- Image with both discord and slack labels → both present, sorted

---

## Task 4: Add handles to x-claw block and pod parser

**File:** `internal/pod/types.go`

Add to `ClawBlock`:
```go
Handles map[string]string // platform → user ID, e.g. {"discord": "123456789"}
```

**File:** `internal/pod/parser.go`

Parse `handles:` from the x-claw service block:

```yaml
x-claw:
  agent: ./AGENTS.md
  handles:
    discord: "123456789"
    slack: "U12345678"
```

`handles:` values are strings. Numeric values in YAML (if the user forgets quotes) should be coerced to string, following the same pattern as `parseExpose`.

Add `rawHandles map[string]interface{}` to `rawClawBlock`. Coerce values to string in parsing.

**Test:** `internal/pod/parser_test.go` or new `internal/pod/parser_handles_test.go`

- `handles: { discord: "123456789" }` → ClawBlock.Handles = map[string]string{"discord": "123456789"}
- Numeric value `discord: 123456789` → coerced to "123456789"
- Missing `handles:` → nil map (not an error)

---

## Task 5: Add Handles to ResolvedClaw and driver types

**File:** `internal/driver/types.go`

Add to `ResolvedClaw`:
```go
Handles map[string]string // platform → user ID (from x-claw handles block)
```

This is the per-service handles map. The pod-wide broadcast env vars are computed from all services' handles at compose time, not stored here.

---

## Task 6: Wire handles into ResolvedClaw in compose_up

**File:** `cmd/claw/compose_up.go`

When building `ResolvedClaw` for each service, populate `rc.Handles` from `svc.Claw.Handles`.

---

## Task 7: Generate CLAW_HANDLE_* env vars and inject into all services

**File:** `cmd/claw/compose_up.go`

After all services are resolved, compute the handle broadcast map:

```go
// Collect all (service, platform, userID) triples from the pod
handleEnvs := map[string]string{}
for _, (name, rc) := range resolvedClaws {
    for platform, userID := range rc.Handles {
        envKey := fmt.Sprintf("CLAW_HANDLE_%s_%s",
            strings.ToUpper(name),
            strings.ToUpper(platform),
        )
        handleEnvs[envKey] = userID
    }
}
```

Pass `handleEnvs` to the compose emitter so it can inject them into every service (both claw and non-claw services — the whole point is that Rails, APIs, etc. can read them).

**File:** `internal/pod/compose_emit.go`

Accept the handle env vars. For each service in the pod (all services, not just claws), merge `handleEnvs` into the service's environment. Handle env vars never override existing env vars — they are lower-priority. Sorted for determinism.

**Tests:** `internal/pod/compose_emit_handle_test.go`

- Pod with one agent with `handles: { discord: "123" }` → every service gets `CLAW_HANDLE_GATEWAY_DISCORD=123`
- Two agents with different handles → both broadcast to all services
- No handles declared → no CLAW_HANDLE_* vars injected
- Existing env var with same name as CLAW_HANDLE_* → existing wins (not overridden)

---

## Task 8: OpenClaw driver — HANDLE enables platform config

**File:** `internal/driver/openclaw/driver.go` (or a new `handle.go`)

During `Materialize`, for each platform in `rc.Handles` (or from the image labels if no user ID declared), apply:

```
op=set  channels.<platform>.enabled  true
```

This is a JSON patch on the openclaw config. The token itself comes from the standard environment (`DISCORD_TOKEN`, `SLACK_BOT_TOKEN`, etc.) — Clawdapus doesn't manage credentials and doesn't inject them. The runner reads them from env vars at startup.

For the OpenClaw driver specifically:
- `discord` → `channels.discord.enabled = true`
- `slack` → `channels.slack.enabled = true`
- `telegram` → `channels.telegram.enabled = true`
- Unknown platform → log a warning, continue (don't fail — the driver may not support every platform)

Note: `SURFACE channel://discord` (Phase 3 Slice 3) will apply the full routing config. `HANDLE discord` alone just enables the platform with defaults. When both are present, `SURFACE channel://discord` takes precedence for routing details; `HANDLE` still fires for env var broadcasting regardless.

**Test:** Update `internal/driver/openclaw/driver_test.go` or add `handle_test.go`

- ResolvedClaw with Handles={"discord": "123"} → generated config has `channels.discord.enabled = true`
- HANDLE slack → `channels.slack.enabled = true`
- HANDLE unknown-platform → no error, warning logged

---

## Task 9: CLAWDAPUS.md — Handles section

**File:** `internal/driver/openclaw/clawdapus_md.go`

Add a `## Handles` section to the generated CLAWDAPUS.md, placed after Identity and before Surfaces:

```markdown
## Handles
- **discord:** @123456789
- **slack:** U12345678
```

Only included when `rc.Handles` is non-empty.

**Test:** `internal/driver/openclaw/clawdapus_md_test.go`

- ResolvedClaw with discord handle → CLAWDAPUS.md contains `## Handles` section
- Empty handles → no `## Handles` section

---

## Task 10: Update the openclaw example

**File:** `examples/openclaw/claw-pod.yml`

Add a `handles:` block to the gateway service:
```yaml
x-claw:
  agent: ./AGENTS.md
  handles:
    discord: "${DISCORD_USER_ID}"
  surfaces:
    - "channel://discord"
    - "service://fleet-master"
```

**File:** `examples/openclaw/Clawfile`

Already has `SURFACE channel://discord` (restored). No HANDLE directive needed unless we want to show the simple enablement path — but since the example uses `SURFACE channel://discord`, HANDLE is redundant for enablement. The example claw-pod.yml `handles:` block is the right place for the user ID.

---

## Task 11: Final verification

```bash
go test ./...
go build -o bin/claw ./cmd/claw
go vet ./...
```

All pass. Then:

- Build the openclaw example: `claw build -t claw-openclaw-example examples/openclaw`
- `claw inspect claw-openclaw-example` shows `claw.handle.discord=true` in labels
- A test pod with handles generates correct CLAW_HANDLE_* env vars in compose.generated.yml

---

## Success Criteria

- `HANDLE discord` in Clawfile → `LABEL claw.handle.discord=true` in built image
- `handles: { discord: "123" }` in x-claw → `CLAW_HANDLE_<SERVICE>_DISCORD=123` in every service's environment in compose.generated.yml
- OpenClaw driver enables Discord in JSON5 config when HANDLE discord is declared
- CLAWDAPUS.md includes `## Handles` section when handles are declared
- No claw without a declared handle receives a CLAW_HANDLE_* var for itself
- All existing tests continue to pass
