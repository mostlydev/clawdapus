# Clawdapus Architecture Plan

**Date:** 2026-02-18
**Status:** Architecture baseline (principles current; execution status delegated to active progress trackers)
**Source of truth:** `MANIFESTO.md`
**Reviews:** Grok (structural critique), Codex (architecture + driver model), operator (cllama clarification, enforcement model)
**Deliberation:** 3-agent talking stick (alpha/Codex, beta/Claude, gamma/Grok) — arch-review room, 3 rounds, consensus reached

**Relevance note (2026-02-27):** This document remains the architecture source of truth, but slice-by-slice execution status lives in `CLAUDE.md` and phase-specific plans/progress docs.

## Implementation Status

| Phase | Slice | Status |
|-------|-------|--------|
| Phase 1 — Clawfile parser + build | — | **DONE** |
| Phase 2 — Driver framework + pod runtime + OpenClaw + volume surfaces | — | **DONE** |
| Phase 3 — Surface manifests | Slice 1: CLAWDAPUS.md + multi-claw | **DONE** |
| Phase 3 — Service surface skills | Slice 2: claw.skill.emit + fallback generation | **DONE** |
| Phase 3.5 — HANDLE directive + social topology | — | **DONE** |
| Phase 3 — Channel surface bindings | Slice 3: driver-mediated channel config | **DONE** |
| Phase 4 — cllama sidecar + policy pipeline | — | **IN PROGRESS** — sidecar contract/wiring shipped; policy pipeline deferred |
| Phase 4.5 — Unified worker architecture | — | **DESIGN** — `docs/plans/2026-02-27-worker-architecture-unified.md` |
| Phase 5 — Drift scoring + fleet governance | — | NOT STARTED |
| Phase 6 — Recipe promotion + worker mode | — | NOT STARTED |

Progress tracker: `docs/plans/phase2-progress.md`

---

## What We're Building

`claw` — a Go CLI that is to agent containers what `docker compose` is to service containers. It governs, deploys, and monitors fleets of AI agent bots as managed, untrusted workloads.

Clawdapus is not an agent framework. It is the infrastructure layer beneath agent frameworks — where deployment meets governance.

---

## Invariants

Invariants are stated as goals from the start. Each is promoted from SHOULD to MUST when its enforcement mechanism ships in the corresponding phase. Until enforcement exists, the invariant is documented but not claimed as a guarantee.

| Invariant | Enforcement mechanism | Promoted to MUST in |
|-----------|----------------------|---------------------|
| No contract → no start | File existence check on `agent:` host path before compose emit | Phase 2 |
| Purpose is immutable from inside | Contract bind mount is always `:ro` | Phase 2 |
| One lifecycle authority | `docker compose` is sole lifecycle authority; Docker SDK is read-only | Phase 2 |
| Driver enforcement verified before up | Preflight validation: all enforcement ops applied and verified | Phase 2 |
| No cllama decision on LLM egress → deny | Sidecar exists and proxies all LLM traffic; direct non-LLM internet egress remains allowed | Phase 4 |
| Missing required policy module → deny service call | Sidecar checks policy modules before routing tool calls to services | Phase 4 |

See also: [ADR-001: cllama Transport](../decisions/001-cllama-transport.md), [ADR-002: Runtime Authority](../decisions/002-runtime-authority.md)

---

## Key Architecture Decisions

### 1. Hybrid Build / Runtime Split

**Build phase** (`claw build`)
- Reads a `Clawfile` (extended Dockerfile syntax)
- Translates Claw-specific directives into standard Dockerfile primitives
- Emits an inspectable `Dockerfile.generated` as a build artifact
- Calls `docker build` on the generated file
- Output is a standard OCI image — runnable on any Docker host without `claw`

**Runtime phase** (`claw up`, `claw ps`, `claw down`, etc.)
- Reads a `claw-pod.yml` (extended docker-compose syntax)
- Parses `x-claw` extension blocks
- Loads the claw-type driver for each service
- Executes enforcement ops (config injection, env vars, mounts) via the driver
- Optionally injects cllama sidecars (when `CLLAMA` directive is present)
- Wires surfaces, assembles skill maps
- Emits a clean `compose.generated.yml` (no `x-claw` keys)
- Shells out to `docker compose` for **all** container lifecycle operations
- Uses Docker SDK **only** for read operations: inspect, logs, events

**Rationale:** Single lifecycle authority eliminates state drift between compose and SDK. Generated files are build artifacts — inspectable, debuggable, but not hand-edited.

### 2. Language: Go

- Single binary distribution — `claw` installs like `kubectl`, not `npm install -g`
- Docker's Go SDK (`github.com/docker/docker/client`) is first-party
- Dockerfile parsing via `github.com/moby/buildkit` — battle-tested parser we extend
- Entire container ecosystem tooling (compose, kubectl, helm, containerd) is Go
- No runtime dependency, no cold start latency

### 3. Compose Strategy

`claw-pod.yml` is a valid `docker-compose.yml`. The `x-claw` extension namespace is already ignored by Docker natively. Clawdapus:
1. Parses `claw-pod.yml` using a Go YAML library
2. Processes `x-claw` blocks (surfaces, cllama config, count scaling, skill maps)
3. Loads the claw-type driver for each Claw service
4. Executes enforcement ops through the driver
5. Optionally injects cllama sidecar containers (when configured)
6. Emits a clean compose file without `x-claw` keys
7. Shells out to `docker compose` with the generated file

### 4. CLAW_TYPE as Driver Selector

`CLAW_TYPE` is not just a label — it selects a **runtime driver** that knows how to enforce Clawfile directives for a specific runner.

**The Clawfile declares WHAT. The driver translates to HOW.**

`MODEL primary anthropic/claude-sonnet-4-6` means the same thing regardless of runner. But the enforcement mechanism differs:
- OpenClaw driver: `openclaw config set agents.defaults.model.primary anthropic/claude-sonnet-4-6` (JSON5-aware, uses runner's own CLI)
- Claude Code driver: write to `settings.json` or set env var
- Generic driver: `ENV MODEL_PRIMARY=anthropic/claude-sonnet-4-6`

Each driver implements the **driver contract** (see below). `CLAW_TYPE` still compiles to a label at build time (`LABEL claw.type=openclaw`) for image introspection, but at runtime it selects behavior.

### 5. Enforcement via Config Injection — v1 Hardcoded, v2 LLM Workers

> **Design direction:** The current driver model uses hardcoded Go logic to translate directives into config mutations. A planned evolution (documented in [`docs/plans/2026-02-21-llm-configuration-workers.md`](2026-02-21-llm-configuration-workers.md)) replaces the translation step with an ephemeral LLM worker that runs inside the runner image, applies intents using the runner's own tools, and reports what it did for independent verification. The driver shrinks from *translator* to *intent-generator + verifier*. Phase 2 ships the v1 model (hardcoded). Channel surface config (Phase 3 Slice 3) is the first feature implemented worker-first.

### 5a. Enforcement via Config Injection (not cllama)

The primary enforcement model is **config injection** — surgically writing specific config branches into the runner's existing configuration, using the runner's own tools. This works without cllama.

What config injection enforces:
- **Model restriction** — pin which models the runner can use
- **Behavioral contract** — read-only mount of AGENTS.md / CLAUDE.md
- **Tool/exec permissions** — restrict what the runner is allowed to execute
- **Scheduling** — system-level cron that the runner cannot modify

What compose generation enforces (no driver involvement):
- **Mount access modes** — read-only or read-write on volumes and host paths, written directly into `compose.generated.yml`

What config injection cannot enforce (requires cllama):
- **Prompt/response interception** — modifying what the LLM sees or returns
- **Key isolation** — hiding real provider keys from the runner
- **LLM-level drift scoring** — independent evaluation of conversation quality

What Clawdapus does not enforce at all:
- **Service-level access control** — the Claw authenticates to services with standard credentials (env vars, Docker secrets). The service's own authorization determines what the Claw can do. Clawdapus declares topology, not permissions.

cllama is an **enhancement layer**, not a prerequisite. A Claw can run with config-injection-only enforcement. cllama adds deeper LLM-level governance when needed.

### 6. cllama: The Standardized Sidecar Standard

cllama is not just an output filter — it is a context-aware, bidirectional proxy that sits between the runner and the LLM provider. **It is optional.** A Claw without a `CLLAMA` directive runs with config-injection-only enforcement.

**Standardized Interface:** cllama is a mini-standard. Any OpenAI-compatible proxy image that can consume Clawdapus context (identity, contract, rights) can act as a sidecar. Clawdapus injects this context (identity, ordinal, and the compiled `/claw/AGENTS.md` contract) directly into the sidecar at startup.

**Intelligent Authorization & Compute Metering:** Because it is context-aware, cllama acts as an intelligent governance enforcement point. It evaluates outbound prompts and inbound responses against the agent's specific `enforce` rules. It can drop tools, decorate prompts with guardrails, or amend responses to prevent drift. Crucially, because the proxy holds the sole set of provider keys, it acts as a **hard budget enforcer**. It can seamlessly rewrite a requested model to a cheaper alternative, ensuring untrusted agents cannot run up excessive compute costs. Tools like [ClawRouter](https://github.com/BlockRunAI/ClawRouter) can be integrated as instances of this proxy standard to handle automated model routing and compute metering.

**Enforcement via Credential Starvation:** Isolation is achieved by strictly separating secrets. The real LLM provider API keys are provided *only* to the cllama sidecar. The agent container is provisioned with a local dummy token and its LLM base URL is rewritten to point at the sidecar. 

Because the agent lacks the credentials to call providers directly, all successful inference *must* pass through the proxy. This "Credential Starvation" guarantees interception even if a malicious prompt tricks the agent into ignoring its configured base URL, while still allowing the agent to natively reach the internet for chat platforms and web tools.

**Transport: Shared Pod Proxy (default).**
- Each pod with a `CLLAMA` directive gets a single `cllama-proxy` container, injected by `claw up`
- Proxy serves all Claws in the pod, reducing resource overhead.
- Identity is established via unique per-agent **Bearer Tokens** (`Authorization: Bearer <id>:<secret>`).
- Proxy resolves agent-specific context (contract, policy, identity) from a **Shared Context Mount** (`/claw/context/<agent-id>/`).
- Proxy exposes an OpenAI-compatible API endpoint on a private pod-internal network.
- Runner's LLM base URL is rewritten to point at the shared proxy.
- Proxy applies the policy pipeline: identity resolution → contract validation → intervention.
- Proxy holds real API keys and enforces pod-wide or per-agent compute budgets.

**Dual modes:**
- **Proxy mode (default):** HTTP sidecar. Works with any runner that makes HTTP calls to an LLM endpoint. No runner integration needed.
- **Adapter mode (future):** SDK-level hook for runners using local models or embedded clients. Documented as a known gap; not built until a concrete runner needs it.

See [ADR-001: cllama Transport](../decisions/001-cllama-transport.md) for full decision record.

### 7. Persona as Runtime Mount (not baked into image)

- At build time: `PERSONA` compiles to `LABEL claw.persona.default=<registry-ref>` — declares the default, does not fetch
- At runtime: `claw up` resolves the persona ref, fetches the OCI artifact via `oras`, bind-mounts into container
- Swap a persona without rebuilding the image

**Rationale:** Persona is content, not infrastructure. Independent layer versioning.

### 8. Security Defaults

Every Claw container gets these by default (overridable via `PRIVILEGE`):

- `--read-only` rootfs except `/workspace` and `/claw/tmp`
- User namespace with `claw-user:1000`
- Network policy: egress restricted to declared surfaces only (enforced at compose/network level, not driver level)
- When cllama is enabled: runner's LLM keys are sidecar-local endpoints, not real provider keys

---

## The Driver Contract

A claw-type driver implements the following interface. Drivers are Go packages under `internal/driver/`.

### Capability Map

Each driver declares what it supports:

```go
type DriverCapabilities struct {
    ModelPin       bool  // can enforce model selection
    ContractMount  bool  // can mount behavioral contract
    Schedule       bool  // can inject/override scheduling
    ConfigWrite    bool  // can write runner-specific config
    Healthcheck    bool  // can report runner health
    Restart        bool  // can trigger graceful restart
    Reload         bool  // can reload config without restart (optional)
}
```

### Abstract Enforcement Ops

Drivers translate Clawfile directives into these abstract operations:

| Op | Parameters | What it does |
|----|-----------|--------------|
| `set` | `path`, `value` | Write a config branch (driver picks mechanism) |
| `unset` | `path` | Remove a config branch |
| `mount_ro` | `host_path`, `container_path` | Read-only bind mount |
| `env` | `name`, `value` | Set environment variable |
| `handle` | `platform`, `id` | Configure runner-native platform integration |
| `cron_upsert` | `schedule`, `command` | Create/update a system cron entry |
| `healthcheck` | `command` | Set container healthcheck |
| `wake` | `command` | Invocation trigger |

**Network restriction** (`restrict_network`) is NOT a driver op — it is enforced at the pod/compose level during compose generation. Drivers can declare network needs, but Clawdapus enforces topology consistently across all claw types.

### Validation Hooks

Each driver provides:
- **Preflight** — validate that enforcement can be applied (config file exists, runner CLI is available, etc.) before `claw up` proceeds
- **Post-apply** — verify that enforcement was actually applied (read back config, check env vars) after ops execute but before compose up

**Fail-closed:** If preflight fails or post-apply verification fails, `claw up` refuses to start the container.

### OpenClaw Driver (reference implementation)

| Directive | Enforcement mechanism |
|-----------|----------------------|
| `AGENT` | `mount_ro` AGENTS.md to `/workspace/AGENTS.md` |
| `MODEL primary ...` | `set agents.defaults.model.primary` via Go-native JSON5 patch |
| `MODEL fallbacks ...` | `set agents.defaults.model.fallbacks` via Go-native JSON5 patch |
| `INVOKE` | System cron in `/etc/cron.d/` (bot-unmodifiable) + `wake` via gateway RPC |
| Config path | `env OPENCLAW_CONFIG_PATH` + `mount_ro` openclaw.json |
| Healthcheck | `healthcheck` via `openclaw health --json` (stdout only — see caveat below) |
| Heartbeat | `set agents.defaults.heartbeat.every` via Go-native JSON5 patch + system cron override |
| `HANDLE discord` | `set channels.discord.enabled true` via Go-native JSON5 patch (token from standard env) |
| `HANDLE slack` | `set channels.slack.enabled true` via Go-native JSON5 patch (token from standard env) |
| `SURFACE channel://discord` | `set channels.discord.*` via Go-native JSON5 patch (token from standard env) |
| `SURFACE channel://slack` | `set channels.slack.*` via Go-native JSON5 patch (token from standard env) |
| `SURFACE channel://telegram` | `set channels.telegram.*` via Go-native JSON5 patch (token from standard env) |

**Config injection strategy:** The OpenClaw driver does NOT shell out to `openclaw config set`. The `openclaw` CLI is verbose (splash banners, doctor checks, `.bak` file creation, Node.js cold-start penalty per invocation) and shelling out N times for N config operations is prohibitively slow and noisy. Instead, the driver uses a Go JSON5 library to: (1) read the base `openclaw.json`, (2) apply all required `set` operations in-memory, (3) write the finalized config to the host, and (4) `mount_ro` it into the container. This aligns with the "write config on host, mount read-only" strategy.

**Important:** OpenClaw config is JSON5, not JSON. The driver must use a JSON5-aware patcher — never raw `jq` or standard JSON marshaling.

**Healthcheck caveat:** `openclaw health --json` emits `Config warnings:` to stderr when plugins are misconfigured. The driver must use `cmd.Output()` (stdout only) and ignore stderr. When used as a Docker `HEALTHCHECK CMD` (where Docker merges stdout/stderr into the health status string), the parser must scan for the first `{` rather than assuming the entire output is pure JSON.

**Schema awareness:** OpenClaw validates config keys against an internal Zod schema. Arbitrary keys are rejected at startup (`Unrecognized key: "..."` error). The driver must track the OpenClaw schema and only inject keys that exist in the schema. This is a feature for fail-closed enforcement — config drift by injection of unknown keys is structurally impossible — but the driver must be updated when OpenClaw's schema evolves.

### Common Runner Control Contract

Claw-type developers who want easy Clawdapus integration should support these conventions:

| Convention | Purpose |
|-----------|---------|
| `CONTRACT_PATH` env var | Where the runner looks for its behavioral contract file |
| `MODEL_PRIMARY` env var | Model pin (or equivalent model slot) |
| `HEALTHCHECK_CMD` | Command that returns 0 when runner is healthy |
| `WAKE_CMD` | Command to trigger an invocation (heartbeat, scheduled task) |
| `RELOAD_CMD` (optional) | Reload config without full restart |
| Graceful `SIGTERM` handling | Clean shutdown on container stop |

Runners that support these get a generic driver for free. Runners that don't need a bespoke driver with runner-specific enforcement.

---

## Surface Taxonomy

Surfaces are declared communication channels — they tell Clawdapus what a Claw can talk to. A Claw is a user of the services it consumes (Principle 6): it authenticates with standard credentials, and the service determines what those credentials allow. Clawdapus enforces access modes only on mounts where Docker has authority. Surfaces split into two categories based on where enforcement happens.

### Pod-Level Surfaces (universal, enforced by Clawdapus)

These are enforced during compose generation. Every claw type gets them — no driver involvement needed.

| Scheme | What it is | Enforcement | Access mode? |
|--------|-----------|-------------|:---:|
| `volume://<name>` | Named Docker volume shared between Claws | Compose `volumes:` + per-service mount | Yes — `:ro` or `:rw` (Docker enforces) |
| `host://<path>` | Operator's host filesystem path | Compose bind mount | Yes — `:ro` or `:rw` (Docker enforces) |
| `service://<name>` | MCP/REST/gRPC service in the pod | Compose networking + expose block matching | No — service auth governs |

Pod-level surfaces are wired by Clawdapus directly into `compose.generated.yml`. If a Claw doesn't declare a surface, it doesn't get access — the network and volume topology are locked to declarations.

**Access modes are enforced only on mounts** (`volume://`, `host://`) where Docker has authority. For services, the surface declaration controls network reachability. What the Claw can do within a service is determined by its credentials — delivered through standard compose mechanisms (`environment:`, `secrets:`, or mounted credential files).

### Driver-Level Surfaces (runner-specific, mediated by driver)

These represent external platform bindings that require runner-specific config injection. The claw-pod.yml declares intent; the driver translates to runner-native configuration.

| Scheme | What it is | Enforcement |
|--------|-----------|-------------|
| `channel://<platform>` | Messaging platform binding (Discord, Slack, Telegram, etc.) | Driver injects platform config into runner's config |
| `webhook://<name>` | Inbound webhook endpoint | Driver configures runner's HTTP endpoint handling |

**Channels are the key example.** Connecting an OpenClaw bot to Discord requires:
- A bot token (delivered via standard `environment:` block — Clawdapus doesn't manage secrets)
- Channel routing config (which guilds, which users, DM policies, approval flows)
- Agent-to-channel bindings

The token is a standard env var. The routing config is driver-mediated. The driver reads the channel surface declaration and translates it to runner config ops, referencing env vars by convention.

### Channel Surface Example

**In claw-pod.yml:**
```yaml
services:
  my-claw:
    x-claw:
      agent: ./AGENTS.md
      surfaces:
        - volume://shared-cache: read-write
        - service://company-crm
        - channel://discord:
            guilds:
              "1465489501551067136":
                policy: allowlist
                users: ["167037070349434880"]
                require_mention: true
            dm:
              enabled: true
              policy: allowlist
              allow_from: ["167037070349434880"]
    environment:                        # standard compose — not x-claw
      DISCORD_TOKEN: ${DISCORD_TOKEN}
      CRM_API_KEY: ${CRM_API_KEY}
```

**What happens at `claw up`:**

1. `volume://shared-cache` → Clawdapus generates compose volume mount with `:rw` (pod-level)
2. `service://company-crm` → Clawdapus generates compose network wiring (pod-level)
3. `channel://discord` → passed to the OpenClaw driver, which translates to:
   ```
   op=set  channels.discord.enabled          true
   op=set  channels.discord.guilds.1465489501551067136.requireMention  true
   op=set  channels.discord.guilds.1465489501551067136.users  [...]
   op=set  channels.discord.dmPolicy         allowlist
   op=set  channels.discord.allowFrom        [...]
   ```
   The driver references `DISCORD_TOKEN` from the standard environment by convention — it doesn't manage the secret itself.

**If the driver doesn't support a surface scheme:** preflight fails with a clear error ("openclaw driver supports channel://discord; generic driver does not"). The Claw doesn't start.

### Driver Capability Map (updated)

```go
type DriverCapabilities struct {
    ModelPin       bool      // can enforce model selection
    ContractMount  bool      // can mount behavioral contract
    Schedule       bool      // can inject/override scheduling
    ConfigWrite    bool      // can write runner-specific config
    Healthcheck    bool      // can report runner health
    Restart        bool      // can trigger graceful restart
    Reload         bool      // can reload config without restart (optional)
    Surfaces       []string  // supported surface schemes: ["channel", "webhook", ...]
}
```

Pod-level surface schemes (`volume`, `host`, `service`) are NOT listed in the driver's `Surfaces` field — they're universal and handled by Clawdapus. Only driver-mediated schemes appear here.

---

## Repository Structure

```
clawdapus/
├── MANIFESTO.md                  # Source of truth for vision and principles
├── docs/
│   ├── plans/                    # Architecture and feature plans
│   └── decisions/                # Architecture Decision Records (ADRs)
├── archive/
│   └── openclaw-runtime/         # Former openclaw Docker runtime (reference)
│
├── cmd/
│   └── claw/
│       └── main.go               # CLI entrypoint
│
├── internal/
│   ├── clawfile/                 # Clawfile parser and Dockerfile emitter
│   │   ├── parser.go             # Parse Clawfile, identify Claw directives
│   │   ├── directives.go         # Directive types: CLAW_TYPE, AGENT, CLLAMA, etc.
│   │   └── emit.go               # Emit standard Dockerfile from parsed AST
│   │
│   ├── driver/                   # Claw-type driver framework
│   │   ├── contract.go           # Driver interface, capability map, enforcement ops
│   │   ├── registry.go           # Driver registry (CLAW_TYPE → driver lookup)
│   │   ├── openclaw/             # OpenClaw driver
│   │   │   └── driver.go         # JSON5-aware config injection via openclaw CLI
│   │   ├── generic/              # Generic driver (env var conventions)
│   │   │   └── driver.go         # CONTRACT_PATH, MODEL_PRIMARY, HEALTHCHECK_CMD
│   │   └── validate.go           # Preflight and post-apply verification
│   │
│   ├── pod/                      # claw-pod.yml parser and compose emitter
│   │   ├── parser.go             # Parse claw-pod.yml, extract x-claw blocks
│   │   ├── types.go              # Pod, Service, Surface, ClawConfig types
│   │   ├── identity.go           # Claw naming, ordinal identity, rescale semantics
│   │   ├── network.go            # Network restriction enforcement (pod-level)
│   │   └── emit.go               # Emit clean compose.yml
│   │
│   ├── cllama/                   # cllama sidecar orchestration (Phase 4)
│   │   ├── sidecar.go            # Sidecar container config generation
│   │   ├── policy.go             # Policy module resolution and layering
│   │   └── proxy.go              # LLM API proxy logic (bidirectional interception)
│   │
│   ├── surface/                  # Surface wiring and skill map assembly
│   │   ├── resolver.go           # Match Claw surface declarations to service expose blocks
│   │   ├── skillmap.go           # Assemble capability inventory per Claw
│   │   └── mcp.go                # Query MCP servers for tool listings
│   │
│   ├── persona/                  # Persona fetching and mounting
│   │   ├── registry.go           # OCI artifact resolution via oras
│   │   └── mount.go              # Bind mount generation for compose
│   │
│   ├── runtime/                  # Docker SDK wrapper (read-only operations only)
│   │   ├── client.go             # Docker client initialization
│   │   └── inspect.go            # Status, logs, events (never lifecycle)
│   │
│   └── build/                    # Build orchestration
│       ├── build.go              # Coordinate Clawfile → Dockerfile → docker build
│       └── worker.go             # Worker mode: spin up, snapshot
│
├── examples/
│   ├── crypto-ops/               # Full pod example: Clawfiles + claw-pod.yml
│   ├── simple/                   # Minimal single-Claw example
│   └── one-shot/                 # Minimal Claude Code one-shot Clawfile
│
└── go.mod
```

### Repo Strategy: Single Repo (for now)

Keep everything in one repo until natural seams emerge. Candidates for future split:
- Base images → `clawdapus-images`
- cllama sidecar image → `cllama` (when it stabilizes)
- Persona registry client → when marketplace exists

---

## The Clawfile Directive Set

All directives translate to standard Dockerfile primitives at build time.

| Directive | Translates to | Purpose |
|-----------|--------------|---------|
| `CLAW_TYPE` | `LABEL claw.type=...` | Declares runner type; selects runtime driver |
| `AGENT` | `LABEL claw.agent.file=...` | Contract filename convention |
| `MODEL` | `LABEL claw.model.<slot>=...` | Named model slot bindings |
| `CLLAMA` | `LABEL claw.cllama.default=...` | Policy stack (optional; omit for config-injection-only enforcement) |
| `PERSONA` | `LABEL claw.persona.default=...` | Default persona ref (fetched and mounted at runtime, not baked) |
| `CONFIGURE` | Entrypoint wrapper script (`/claw/configure.sh`) | Shell mutations run at container init before the runner starts |
| `INVOKE` | `RUN echo "..." >> /etc/cron.d/claw` | Cron schedule entries |
| `TRACK` | `RUN claw-track-install apt pip npm` | Install package manager wrappers |
| `ACT` | `RUN ...` (worker mode only) | Worker-mode setup commands |
| `HANDLE` | `LABEL claw.handle.<platform>=...` | Platform identity declaration; driver enables integration, emitter broadcasts identity to pod |
| `SURFACE` | `LABEL claw.surface.<n>=...` | Consumed surface declarations |
| `SKILL` | `LABEL claw.skill.<n>=...` | Skill files to mount read-only into the runner's skill directory |
| `PRIVILEGE` | `LABEL claw.privilege.<mode>=...` | Per-mode privilege config |

Standard Dockerfile directives (`FROM`, `RUN`, `COPY`, `ENV`, `ENTRYPOINT`, etc.) pass through unchanged.

### CONFIGURE clarification

`CONFIGURE` directives are **not** build-time `RUN` commands. They compile into an entrypoint wrapper script (`/claw/configure.sh`) that runs at container startup, before the runner starts. This is for init-time mutations against the base image defaults — things that need to happen fresh on every boot, not baked into the image.

### Build-time vs deploy-time overridability

| Directive | Baked at build | Overridable in claw-pod.yml |
|-----------|:-:|:-:|
| `CLAW_TYPE` | yes | no |
| `AGENT` | yes (default filename) | yes (`agent:` path) |
| `MODEL` | yes (default slots) | yes |
| `CLLAMA` | yes (default stack) | yes (or omit entirely) |
| `PERSONA` | yes (default ref) | yes |
| `INVOKE` | yes (default schedule) | yes |
| `CONFIGURE` | yes | no (runs from image) |
| `TRACK` | yes | no |
| `ACT` | yes | no |
| `HANDLE` | yes | yes (additive) |
| `SURFACE` | yes (default declarations) | yes (additive) |
| `SKILL` | yes (default skills) | yes (additive) |
| `PRIVILEGE` | yes (default modes) | yes |

---

## The claw-pod.yml Extension Schema

All Claw-specific config lives under `x-claw` at the appropriate scope.

**Pod-level:**
```yaml
x-claw:
  pod: <name>
  master: <service-name>
```

**Service-level (Claw):**
```yaml
x-claw:
  agent: <path-to-contract-file>     # host path, bind-mounted read-only
  persona: <registry-ref>            # override Clawfile default
  cllama: <stack-spec>               # override Clawfile default (omit for no cllama)
  count: <n>                         # scale to N identical containers
  skills:                            # skill files mounted r/o into runner's skill dir
    - ./skills/custom-workflow.md
    - ./skills/team-conventions.md
  surfaces:                          # topology declarations
    # Mounts (access mode enforced by Docker):
    - volume://shared-cache: read-write
    - host:///path/to/data: read-only
    # Services (no access mode — service auth governs):
    - service://company-crm
  describe:
    role: <string>
    inputs: [...]
    outputs: [...]
# Credentials go in standard compose blocks (environment, secrets), not in x-claw
environment:
  CRM_API_KEY: ${CRM_API_KEY}
  DISCORD_TOKEN: ${DISCORD_TOKEN}
```

**Service-level (plain container):**
```yaml
x-claw:
  expose:
    protocol: mcp | rest | grpc
    port: <n>
    discover: auto                     # auto-detect (tries MCP, then OpenAPI, then static describe). Extensible.
  require_cllama:                    # pre-call policy gate; does not replace service auth,
    - <policy-module>                # adds an additional allow/deny decision before the request is sent
  describe:                          # static fallback when service can't self-describe
    role: <string>
    capabilities: [...]
```

**Network/volume-level:**
```yaml
networks:
  internal:
    x-claw:
      visibility: pod-only | egress-only | public

volumes:
  shared-cache:
    x-claw:
      access:
        - <service-pattern>: <access-mode>
```

---

## Claw Identity and Scaling

When `count: N` is specified, Clawdapus generates N named instances with stable ordinal identity:

```
crypto-crusher-0, crypto-crusher-1, crypto-crusher-2
```

**Naming:** `<service>-<ordinal>`, zero-indexed. Deterministic and stable across restarts.

**Rescaling up:** New ordinals are appended. Existing containers are untouched. `count: 3 → count: 5` adds `-3` and `-4`.

**Rescaling down:** Highest ordinals are removed first. `count: 5 → count: 3` removes `-4` and `-3`.

**Drift history:** Drift scores and audit logs are keyed by ordinal name. Removing and re-adding an ordinal does not inherit prior history — it starts fresh.

**Command targeting:** CLI commands accept either service name (applies to all) or ordinal name (applies to one):
```
claw logs crypto-crusher        # all 3
claw logs crypto-crusher-2      # just one
claw audit crypto-crusher-2     # just one
```

---

## CLI Commands

```
claw build [path]              # Clawfile → Dockerfile → docker build
claw inspect <image>           # Show resolved Claw labels from built image
claw up [pod]                  # claw-pod.yml → compose.yml → docker compose up
claw down [pod]                # Stop and remove pod containers
claw ps [pod]                  # Status with drift scores and cllama health
claw logs <claw>               # Stream logs from a running Claw
claw skillmap <claw>           # Show assembled capability inventory
claw audit <claw> [--last Xh]  # cllama intervention history and drift events
claw recipe <claw> [--since Xd] # Suggested recipe from mutation log
claw bake <claw> --from-recipe  # Apply recipe to rebuild image
claw snapshot <claw> --as <ref> # Snapshot running Claw as new image
claw doctor                    # Check Docker version, BuildKit, compose plugin, etc.
```

---

## Implementation Phases

### Phase 1 — Clawfile Parser + Build

**Goal:** `claw build`, `claw inspect`, and `claw doctor` work end-to-end.

**Invariants gated:** None (build-only phase, no runtime invariants yet).

1. `claw doctor` — check Docker, BuildKit, compose plugin versions
2. Parse a Clawfile using buildkit's Dockerfile parser as base
3. Identify and extract Claw-specific directives
4. Translate directives to Dockerfile primitives (LABELs, ENV, entrypoint wrapper for CONFIGURE)
5. Emit `Dockerfile.generated`
6. Shell out to `docker build`
7. `claw inspect` reads labels from built image and displays resolved config

**Validation:** Create four test Clawfiles before writing the parser:
- OpenClaw (heavyweight, AGENTS.md contract)
- Nanobot (lightweight, config.json contract)
- Claude Code (one-shot, CLAUDE.md contract)
- Raw Python script (custom runner, custom contract)

Verify every directive translates cleanly for all four. Especially:
- `INVOKE` semantics differ for internal-scheduling vs external-cron runners
- `AGENT` bind-mount path varies per `CLAW_TYPE`
- `CONFIGURE` entrypoint wrapper must not conflict with runner's own entrypoint

**Success criteria:** All four test Clawfiles build to runnable OCI images. `claw inspect` shows correct labels. Generated Dockerfile is valid and inspectable.

### Phase 2 — Driver Framework + Pod Runtime + OpenClaw Driver + Volume Surfaces

**Goal:** `claw up` / `claw down` / `claw ps` work with config-injection enforcement and shared volume access. No cllama required.

**Invariants promoted to MUST:**
- No contract → no start (file existence check)
- Purpose is immutable from inside (`:ro` bind mount)
- One lifecycle authority (compose-only)
- Driver enforcement verified before up (preflight + post-apply)

**Driver framework:**
1. Define the driver interface (capability map, enforcement ops, validation hooks)
2. Implement driver registry (`CLAW_TYPE` label → driver lookup)
3. Implement preflight and post-apply verification (fail-closed)

**OpenClaw driver (reference implementation):**
4. Contract mount: `AGENT` → read-only bind mount of AGENTS.md
5. Model pin: `MODEL` → `openclaw config set` (JSON5-aware, never raw jq)
6. Schedule: `INVOKE` → system cron in `/etc/cron.d/` + gateway wake RPC
7. Config injection: generate and mount `openclaw.json` read-only
8. Healthcheck: `openclaw health --json`

**Pod runtime:**
9. Parse `claw-pod.yml`, extract `x-claw` blocks
10. Handle `count` scaling with ordinal identity
11. Validate contract paths exist on host (**fail-closed**)
12. Execute driver enforcement ops per service
13. Run preflight validation; abort if any driver fails
14. Enforce network restrictions at compose level (not driver level)
15. Emit clean `compose.generated.yml`
16. Shell out to `docker compose up`
17. Run post-apply verification; fail-closed if enforcement cannot be confirmed

**Volume surfaces:**
18. Parse `SURFACE volume://...` declarations from `x-claw` service blocks and volume-level `x-claw.access` ACLs
19. Generate `volumes:` top-level declarations in `compose.generated.yml`
20. Generate per-service volume mounts with correct access modes (`ro` for read-only, default for read-write)
21. Enforce ACL — if a service claims an access mode not permitted by the volume's `x-claw.access` block, fail preflight

Volume surfaces are enforced at the compose generation level — Clawdapus writes volume mount entries directly into `compose.generated.yml` with the declared access mode (`:ro` or `:rw`). No driver involvement needed.

**Success criteria:** A claw-pod.yml with an OpenClaw service starts correctly with enforced model pin and read-only contract. Missing contract blocks startup. Failed driver preflight blocks startup. `claw ps` shows container status and driver enforcement state. Claws sharing a volume can read/write shared files according to their declared access mode; a Claw without declared access to a volume cannot mount it.

### Phase 3 — Surface Manifests + Skills + Channel Bindings + Multi-Driver

**Goal:** Surfaces generate two layers of context: a bootstrapped manifest (SURFACES.md) always visible to the agent, and per-surface skill files for complex surfaces. `claw skillmap` works. Multiple claw types and surface types supported.

Volume surfaces (shared folders) are already wired in Phase 2. Phase 3 adds surface context injection, service surfaces (pod-internal MCP/REST/gRPC endpoints), driver-mediated surfaces (channel bindings), and skill generation.

**Slice 1 — Surface manifests + multi-claw example (volume surfaces):**
1. Parse pod-level `surfaces` from `x-claw` into `ResolvedSurface` structs, wire into `ResolvedClaw`
2. Driver generates `SURFACES.md` — the always-visible manifest describing what's available (name, type, access mode, mount path, connection details). For surfaces with companion skills, it points the agent to the skill file.
3. Driver adds `bootstrap-extra-files` hook to runner config so SURFACES.md is injected into agent context on every session and heartbeat
4. Multi-claw example: two OpenClaw services sharing a volume with different access modes

**Slice 2 — Service surfaces + skill generation:**
5. Resolve `service://` declarations against expose blocks in the pod
6. Wire compose networking so declared services are reachable
7. Driver generates skill files for service surfaces: `skills/surface-<name>.md` containing host, port, protocol, credential env vars, and usage documentation from service discovery
8. Skills are mounted read-only into the container's skills directory — agents discover them through the runner's standard skill mechanism
9. SURFACES.md references skill files: `"See skills/surface-<name>.md for usage"`
10. Run the service discovery pipeline (steps 17-20) to populate skill content

**Slice 3 — Channel bindings (driver-mediated):**
11. Parse `channel://<platform>` surface declarations from `x-claw`
12. Pass channel config to the driver for runner-specific injection
13. Driver translates channel YAML to runner config ops (e.g. `openclaw config set channels.discord.*`)
14. Driver generates skill files for channel surfaces describing platform capabilities and constraints
15. Driver references platform tokens from standard compose `environment:` by convention (Clawdapus does not manage credentials)
16. Preflight validates driver supports the declared channel schemes

**Service discovery:**
17. Query MCP services for tool listings via MCP protocol
18. Query REST services for OpenAPI specs (if available)
19. Fall back to static `describe` blocks for services that can't self-describe
20. Discovery results populate skill file content — the skill is the "manual" for the surface

**Multi-driver:**
21. Implement generic driver (env var conventions: `CONTRACT_PATH`, `MODEL_PRIMARY`, etc.)
22. Implement Claude Code driver (settings.json + CLAUDE.md contract)
23. Prove the driver abstraction works across at least 3 runner types

**CLAWDAPUS.md — single context injection point:**
The driver generates a `CLAWDAPUS.md` file — the infrastructure layer's letter to the agent. Always bootstrapped into the agent's context. Contains identity (pod, service, type), surfaces (name, type, access, location), and a skills index (what skill files are available). This is the agent's map of its environment.

**SKILL directive — explicit skill mounts:**
A new `SKILL` directive in Clawfile and `skills:` in x-claw allows operators to mount skill files from the host into the runner's skill directory. The driver knows where skills go per runner type (OpenClaw: `skills/`, Claude Code: different). Surface-generated skills, discovery skills, and explicit SKILL directives all feed the same skill directory.

**Two-layer context model:**
- **CLAWDAPUS.md** (bootstrapped) = the map. Always in the agent's context. Lists everything: identity, surfaces, skill index.
- **Skills** (`skills/` directory) = the manual. Detailed usage guides for complex surfaces and operator-provided skills. Agents look them up on demand through the runner's standard skill mechanism. Read-only mounted.
- Volume surfaces are simple enough that CLAWDAPUS.md alone covers them.
- Service surfaces get a companion skill with host, port, protocol, credential env vars, and discovered capabilities.
- Channel surfaces get a companion skill describing platform capabilities, routing config, and constraints.

**Success criteria:** Every Claw receives a `CLAWDAPUS.md` bootstrapped into its context with identity, surfaces, and skill index. SKILL directives and surface-generated skills are mounted into the runner's skill directory. `claw skillmap <claw>` shows correct capability inventory. A `channel://discord` surface results in correct config injection plus a generated skill. Mixed pods with different claw types start correctly. A channel surface on a driver that doesn't support it fails preflight with a clear error.

### Phase 3.5 — Social Topology Projection (Leviathan Pattern)

**Goal:** Allow non-claw services (like a Rails API) to know the public chat identities of agents in the pod, so they can emit targeted events (e.g., `@mention` a specific agent on Discord to approve a trade). Also, make it trivial to enable these channels without learning the runner's internal JSON config.

1. Add `HANDLE` directive to Clawfile (e.g., `HANDLE discord`)
2. Driver translates `HANDLE` into runner-native configuration (e.g., OpenClaw driver writes `channels.discord.enabled = true` in JSON5).
3. Add `handles:` map to `claw-pod.yml` `x-claw` block to declare the specific User IDs (e.g., `discord: "123942942"`)
4. Pod emitter aggregates all `handles` across the pod and generates global environment variables (e.g., `CLAW_HANDLE_<SERVICE>_DISCORD=123942942`) and injects them into **every** service in the pod.
5. Driver writes the `HANDLE` IDs into `CLAWDAPUS.md` so the agent knows its own public identity.

**Success criteria:** A Rails app in the pod can read `CLAW_HANDLE_TIVERTON_DISCORD` to dynamically construct a Discord mention. The OpenClaw driver automatically enables Discord based solely on the `HANDLE` directive, abstracting the `openclaw.json` schema.

### Phase 4 — Shared Governance Proxy Integration

**Goal:** Integrate the optional `cllama` governance proxy to achieve bidirectional LLM interception and compute metering via Credential Starvation.

**Invariants promoted to MUST:**
- No cllama decision on egress → deny (when proxy is enabled).
- The agent container must never hold real LLM provider API keys.

**Slice 1: The Base Passthrough Reference (External/Standalone)**
1. Build the `cllama-passthrough` reference image (Go binary, OpenAI-compatible API proxy).
2. Implement the API contract: listen on `0.0.0.0:8080/v1/chat/completions`.
3. Implement Identity Resolution: parse the Bearer Token to identify the calling agent.
4. Implement Context Loading: read the agent's `/claw/context/<agent-id>/AGENTS.md` and metadata.
5. Implement Transparent Forwarding: strip the dummy token, attach the real `PROVIDER_API_KEY`, forward to upstream, return response.
6. Implement Structured Logging: emit `request` and `response` JSON logs to stdout.

**Slice 2: Clawdapus Infrastructure Wiring**
7. Detect if a `CLLAMA` directive exists in *any* of the pod's agents during `claw up`.
8. If present, automatically inject the shared `cllama-proxy` container into the generated compose file.
9. Generate the shared context directory (`/var/run/clawdapus/context/`) on the host.
10. Populate the directory with agent-specific subdirectories containing their compiled `AGENTS.md` and `metadata.json`.
11. Bind-mount the shared context directory into the proxy container at `/claw/context`.

**Slice 3: Driver LLM Rewiring (Credential Starvation)**
12. Update the drivers (e.g., OpenClaw) to recognize when a proxy is active in the pod.
13. Overwrite the runner's LLM configuration to use the proxy's internal address (`http://cllama-proxy:8080/v1`).
14. Inject the generated dummy Bearer token (`Bearer <agent-id>-secret`) into the runner's credentials config, ensuring real keys are starved.

**Slice 4: Policy Pipeline (Future Evolution)**
15. Implement `require_cllama` — proxy checks active policy modules before routing tool calls.
16. Implement prompt decoration and response amendment logic.

**Success criteria:** An OpenClaw agent in a pod with `CLLAMA` enabled successfully completes a task using a remote model. The agent's config shows only a dummy token. The proxy logs show the intercepted request and response.


### Phase 5 — Drift Scoring + Fleet Governance

**Goal:** `claw ps` shows drift scores. Master Claw operates as an autonomous governor.

1. Independent drift scoring process driven by proxy telemetry.
2. When cllama is present: score directly from proxy structured logs (LLM-level).
3. When cllama is absent: score from config-level checks and output sampling.
4. Drift thresholds → capability restriction → quarantine escalation.
5. **Master Claw Deployment:** Deploy the "Top Octopus" as a standard agent whose sole purpose is reading proxy logs (cost, drift, interventions) and making executive administrative decisions (shifting budgets, quarantining agents).
6. **Hub-and-Spoke Model:** Prove the ability for a single Master Claw to ingest telemetry from multiple `cllama` proxies across different pods/zones to autonomously manage a neural fleet.
7. `claw audit` shows full intervention + drift history.

### Phase 6 — Recipe Promotion + Worker Mode

**Goal:** `claw recipe`, `claw bake`, `claw snapshot` work.

1. TRACK wrapper scripts log mutations inside running containers
2. `claw recipe` reads mutation logs and generates promotion suggestions
3. `claw bake` applies recipe to rebuild image
4. Worker mode: `claw up --mode worker` → interact → `claw snapshot`

---

## Open Questions

1. **Skill mount format** — **Resolved.** Two-layer model: `CLAWDAPUS.md` (bootstrapped into agent context) serves as the always-visible index of identity, surfaces, and skills. Individual skill files live in the runner's `skills/` directory as markdown. Skills come from three sources: explicit `SKILL` directives (operator-provided), surface-generated skills (from service/channel surfaces), and discovery-populated skills (from MCP/OpenAPI/describe). The driver knows where skills go per runner type.

2. **Persona registry** — OCI artifacts via `oras` is the leading option. Need to define the artifact structure (manifest, memory, knowledge, style fingerprint as separate layers? or single tarball?).

3. **cllama sidecar image** — What base image? What LLM client library? Needs to be extremely lightweight since every Claw gets one. Likely a small Go binary.

4. **Versioned schemas** — Clawfile directives, x-claw schema, and skill-map format should be independently versioned with compatibility rules. When to formalize this?

5. **Driver discovery** — Should third-party drivers be loadable as plugins, or compiled in? For now, compiled in. Plugin model is a future consideration.

6. **Config injection timing** — **Resolved.** The driver writes finalized config on the host and mounts it read-only into the container. This avoids needing the container's filesystem to exist before enforcement. The OpenClaw driver uses Go-native JSON5 patching to assemble `openclaw.json` on the host, then bind-mounts it `:ro`. This matches how the prior `openclaw-up.sh` worked and avoids the performance/noise problems of shelling out to `openclaw config set` (splash banners, doctor checks, `.bak` files, Node.js cold-start per invocation).
