---
name: clawdapus-cli
description: Use when working with the claw CLI, Clawfiles, claw-pod.yml, cllama proxy, or deploying AI agent containers with Clawdapus. Use when you see CLAW_TYPE, AGENT, MODEL, CLLAMA, CONFIGURE, INVOKE, SURFACE, HANDLE, TRACK, SKILL, or PRIVILEGE directives. Use when diagnosing agent startup failures, credential starvation, config injection, governance proxy issues, managed tool mediation, or memory plane problems.
---

# Clawdapus — Operational Skill

Infrastructure-layer governance for AI agent containers. `claw` treats agents as untrusted workloads — reproducible, inspectable, diffable, killable.

**Mental model:** Clawfile is to Dockerfile what claw-pod.yml is to docker-compose.yml. Standard Docker directives pass through unchanged. Claw directives compile into labels plus driver-specific runtime materialization. Eject anytime — you still have working Docker artifacts.

## CLI Commands

```bash
# Prerequisites
go build -o bin/claw ./cmd/claw    # build from source
claw doctor                         # verify Docker, buildx, compose

# Image lifecycle
claw pull [-f <pod>.yml]            # pinned infra + pod registry images + runner bases
claw pull --no-runners [-f <pod>]   # pinned infra + registry images only
claw build -t <image> <path>        # single Clawfile -> Dockerfile.generated -> docker build
claw build [-f <pod>.yml]           # with no path: build every pod service that has build:
claw inspect <image>                 # show claw.* labels from built image

# Pod lifecycle (mirrors docker compose UX)
claw up [-f <pod>.yml] [-d]         # strict: tells you to run claw pull/build when images are missing
claw up --fix [-f <pod>.yml] [-d]   # pull/build missing images, then launch
claw down [-f <pod>.yml]            # tear down
claw ps [-f <pod>.yml]              # container status
claw logs [-f <pod>.yml] [svc]      # stream logs (--follow)
claw health [-f <pod>.yml]          # driver health probes
claw compose <cmd> [args]           # passthrough: any docker compose subcommand

# Scaffold
claw init [dir]                     # interactive project scaffold
claw agent add [name]               # add agent service to existing pod

# Observability
claw audit [--since <dur>] [--claw <id>] [--type <type>] [--json]
                                    # summarize cllama telemetry from container logs
                                    # types: request, response, error, intervention,
                                    #        feed_fetch, provider_pool, tool_call
claw api schedule <subcommand>      # inspect/control scheduled invocations via claw-api
    # list | get <id> | pause <id> | resume <id> | skip-next <id> |
    # clear-skip-next <id> | fire <id>

# Session history & memory
claw history export <agent-id>      # export session history as NDJSON
    [--after <RFC3339>] [--limit N]
claw memory backfill <mem-svc>      # replay retained history to memory service
    [--after <RFC3339>] [--limit N] [--agent <id>]
claw memory forget <mem-svc>        # forget entries by ID with governed tombstones
    --entry-id <id> --agent <id> [--reason <text>]

# Maintenance
claw update                         # re-run install.sh to update binary
```

On successful pod launch, `claw up` prints `[claw] dashboard:  http://localhost:<port>` when the pod declares a `clawdash` surface. Agents debugging a running pod should point the operator at that URL.

Lifecycle commands block if `claw-pod.yml` is newer than `compose.generated.yml` — run `claw up` to regenerate. `claw down` is exempt.

`-f` locates `compose.generated.yml` next to the pod file. Without `-f`, `claw up` uses `./claw-pod.yml`; other lifecycle commands look for `compose.generated.yml` in the current directory.

`claw api schedule ...` does not require a host-published claw-api port. It
tunnels through `docker compose exec -T claw-api /claw-api -request-*`, so the
pod must already be up and include an injected `claw-api` service.

Trust boundary: if you can run `docker compose exec` against the pod, you can
select any principal present in claw-api's `principals.json`. The `--principal`
flag is a selector, not a security boundary.

## Clawfile Reference

A Clawfile is an extended Dockerfile. Every valid Dockerfile is a valid Clawfile.

```dockerfile
FROM openclaw:latest

CLAW_TYPE openclaw                          # REQUIRED: selects runtime driver
AGENT AGENTS.md                             # behavioral contract — must exist on host

MODEL primary openrouter/anthropic/claude-sonnet-4
MODEL fallback anthropic/claude-haiku-3-5

CLLAMA passthrough                          # governance proxy type
PERSONA ./personas/trader                   # identity materialization (local or OCI)

HANDLE discord                              # platform identity declaration
INVOKE 15 8 * * 1-5  pre-market             # cron schedule (5-field + name)

SURFACE service://trading-api               # infrastructure surface
SURFACE volume://shared-research read-write

SKILL policy/risk-limits.md                 # operator policy, mounted read-only
CONFIGURE openclaw config set key value     # driver-side config DSL, not arbitrary shell

TRACK apt npm                               # mutation tracking wrappers
PRIVILEGE worker root                       # privilege mode mapping
PRIVILEGE runtime claw-user
```

### Directive Details

| Directive | Purpose | Build -> Runtime |
|-----------|---------|-----------------|
| `CLAW_TYPE <type>` | Selects driver. Determines HOW enforcement happens. | Label -> driver selection |
| `AGENT <file>` | Behavioral contract. **Must exist on host or startup fails.** Mounted read-only. | Label -> `:ro` bind mount |
| `MODEL <slot> <provider/model>` | Named model slot. Multiple allowed. Format: `provider/model-name`. | Label -> driver config injection |
| `CLLAMA <type>` | Governance proxy. Currently only `passthrough`. Runtime enforces max 1. | Label -> proxy sidecar wiring |
| `PERSONA <path>` | Identity materialization. Local refs copied with traversal hardening; non-local pulled as OCI artifacts. Sets `CLAW_PERSONA_DIR` only when present. | Label -> runtime materialization |
| `HANDLE <platform>` | Platform identity (`discord`, `slack`, `telegram`). Broadcasts agent ID as `CLAW_HANDLE_*` env vars. | Label -> driver config + pod env |
| `INVOKE <cron> <name>` | System cron in `/etc/cron.d/claw`. Bot cannot modify. | Baked into image |
| `SURFACE <scheme>://<target> [mode]` | Infrastructure boundary. See Surface Taxonomy. | Label -> compose wiring |
| `SKILL <file>` | Reference markdown mounted read-only into runner skill directory. | Label -> host path validation + mount |
| `CONFIGURE <cmd>` | Driver-specific config DSL. Use `<driver> config set <path> <value>`, not arbitrary shell. | Parsed by Clawdapus, then projected into generated runtime config/artifacts |
| `TRACK <pkg-managers>` | Installs wrappers for `apt`, `pip`, `npm` to log mutations. | Build-time install |
| `PRIVILEGE <mode> <user>` | Maps privilege modes to user specs. | Label -> Docker user/security |

### `CONFIGURE` Semantics

- Treat `CONFIGURE` as driver-side config mutation DSL, not as a generic startup hook.
- The public contract is `CONFIGURE <driver> config set <path> <value>`.
- Values are JSON-decoded when possible. Leave booleans, numbers, arrays, and objects unquoted; quote strings.
- `CONFIGURE` applies after generated defaults, so it overrides what `HANDLE` and other driver defaults emitted.
- For `openclaw`, Clawdapus applies `CONFIGURE` while generating `openclaw.json` during materialization. Do not assume downstream `openclaw config set ...` shell behavior is the same contract.
- Dotted object paths are the supported shape today. Do not assume indexed list mutation like `agents.list[0].groupChat.mentionPatterns` is supported unless the code/docs explicitly say so.

## Surface Taxonomy

| Scheme | Enforcement | Notes |
|--------|-------------|-------|
| `volume://<name> [read-only\|read-write]` | Compose volume mount | Default read-only |
| `host://<path> [mode]` | Compose bind mount | |
| `service://<name>` | Pod-internal networking | Auto-mounts service skill if available |
| `channel://<platform>` | Driver config injection | Token from standard `environment:` block |
| `webhook://<name>` | Driver HTTP endpoint config | |

Service skills: `claw.skill.emit` label > operator override > fallback stub.

## claw-pod.yml Reference

Extended docker-compose. Claw config lives under `x-claw:` (Docker ignores this namespace).

```yaml
x-claw:
  pod: my-pod                        # optional pod name
  # Pod-level defaults (services inherit; override or extend with ...)
  cllama-defaults: passthrough
  handles-defaults:
    discord:
      id: "${BOT_DISCORD_ID}"
      username: "my-bot"
      guilds: [...]
  surfaces-defaults:
    - "service://trading-api"
  feeds-defaults:
    - fleet-alerts
  skills-defaults:
    - ./skills/shared-runbook.md
  tools-defaults:
    - trading-api
  memory-defaults:
    service: team-memory
    timeout-ms: 300

services:
  my-agent:
    image: my-claw-image:latest
    x-claw:
      agent: ./AGENTS.md             # host path, overrides Clawfile AGENT
      cllama: passthrough             # or [passthrough, policy] for future chains
      cllama-env:                     # ONLY place for provider API keys when using cllama
        ANTHROPIC_API_KEY: "${ANTHROPIC_API_KEY}"
        OPENROUTER_API_KEY: "${OPENROUTER_API_KEY}"
      handles:
        discord:
          id: "${BOT_DISCORD_ID}"
          username: "my-bot"
          guilds:
            - id: "${GUILD_ID}"
              name: "My Server"
              channels:
                - id: "${CHANNEL_ID}"
                  name: general
      surfaces:
        - "service://trading-api"
        - "volume://shared-cache read-write"
        - channel://discord:          # map form with routing config
            dm:
              enabled: true
              policy: allowlist
              allow_from: ["USER_ID"]
      skills:
        - ./skills/custom-runbook.md
      feeds:
        - fleet-alerts               # short-form feed name (resolved from feed registry)
      tools:                          # v0.5.0: managed tool subscriptions (cllama-only)
        - trading-api                 # scalar = subscribe to ALL tools from this service
        - service: analytics          # map form = named allow list
          allow:
            - get_summary
            - get_report
      memory:                         # v0.5.0: ambient memory subscription (cllama-only)
        service: team-memory
        timeout-ms: 450              # recall timeout per turn (default 300ms)
      invoke:                         # pod-level scheduled tasks
        - schedule: "*/30 * * * *"
          name: "Heartbeat"
          message: "Post status."
          to: trading-floor
    environment:                      # standard compose — credentials go HERE
      DISCORD_BOT_TOKEN: "${DISCORD_BOT_TOKEN}"

  perplexity:
    image: ghcr.io/mostlydev/claw-mcp-stdio:v0.12.0
    environment:
      PERPLEXITY_API_KEY: "${PERPLEXITY_KEY}"
    expose:
      - "8080"
    x-claw:
      describe-file: ./perplexity.claw-describe.json
      mcp-stdio:
        command: npx
        args: ["-y", "perplexity-mcp"]
```

### Key rules

- **Credentials**: Standard `environment:` or `secrets:` blocks. Never in `x-claw:` (except `cllama-env` for proxy keys).
- **`cllama-env`**: Provider API keys for the proxy. These go ONLY here — never in agent `environment:`. Credential starvation enforced.
- **`handles`**: Discord bot IDs, usernames, guilds. Clawdapus auto-generates native Discord `mentionPatterns`, `allowBots: true`, peer `users[]` allowlist.
- **`surfaces`**: String form (`"channel://discord"`) = simple enable. Map form (`channel://discord: {dm: {...}}`) = routing config.
- **`tools`**: Requires `cllama` on the consuming service. Services must publish tools via `claw.describe` descriptor v2. `allow: all` (implicit for scalar form) passes every tool; named lists are validated against the tool registry.
- **`mcp-stdio`**: Sidecar-only block for the shared `claw-mcp-stdio` wrapper. `command` is required, `args` is a list, and credentials stay in the sidecar's regular `environment:`. Pair with `describe-file` when the descriptor is supplied by the pod instead of baked into the image.
- **`memory`**: Requires `cllama` on the consuming service. Target service must declare `memory` in its `claw.describe` descriptor v2.
- **Pod defaults**: `*-defaults` at pod level are inherited by all services. Declaring the field at service level replaces the default. Use `...` spread token to extend list-type defaults (surfaces, feeds, skills, tools). Memory defaults are object-form (no spread — presence of `memory:` at service level replaces entirely; `memory: null` suppresses).

## Service Self-Description (claw.describe)

Services declare capabilities via a `.claw-describe.json` file (embedded in the image, discovered from Dockerfile labels, or supplied with service-level `x-claw.describe-file`). `claw up` extracts descriptors and compiles them into pod-global registries.

### Descriptor v2

```json
{
  "version": 2,
  "service": "trading-api",
  "feeds": [
    {"name": "market-data", "path": "/feeds/market", "ttl": "5m"}
  ],
  "tools": [
    {
      "name": "execute_trade",
      "description": "Execute a market order",
      "inputSchema": {
        "type": "object",
        "properties": {
          "ticker": {"type": "string"},
          "action": {"type": "string", "enum": ["buy", "sell"]},
          "quantity": {"type": "integer"}
        },
        "required": ["ticker", "action", "quantity"]
      },
      "http": {"method": "POST", "path": "/trade", "body": "json"}
    }
  ],
  "memory": {
    "recall": {"path": "/recall"},
    "retain": {"path": "/retain"},
    "forget": {"path": "/forget"}
  }
}
```

- **`tools`**: Each requires `name`, `description`, `inputSchema` (JSON Schema, `type: "object"`), and either `http` (`method`, `path`, optional `body`) for Clawdapus-native HTTP services *or* a top-level `mcp` block on the descriptor (see below) for MCP sidecars. Duplicate tool names within a service are a hard error.
- **`mcp`** *(v0.11.0)*: Top-level block declaring the service is an MCP sidecar — `transport: streamable_http` (default) and `path: /mcp` (default). When present, `tools[].http` becomes optional and cllama routes calls through the MCP `tools/call` endpoint instead of an HTTP path. Auth resolution, namespacing, audit, session-history, and policy budgets are unchanged from the HTTP-managed path.
- **`memory`**: At least one of `recall` or `retain` required. All paths must start with `/`.
- **`feeds`**: Unchanged from v1. Short-form names in `x-claw.feeds` resolve against the feed registry.

## Persistence and Memory Surfaces

Clawdapus provides two distinct, durable state surfaces for agents. Both survive container restarts (`claw up`) and even driver migrations (changing `CLAW_TYPE`).

| Surface | Owner | Written by | Path inside container | Host path |
|---------|-------|------------|-----------------------|-----------|
| **Session history** | Infrastructure | cllama proxy | `/claw/session-history` | `.claw-session-history/<agent-id>/history.jsonl` |
| **Portable memory** | Runner / Agent | Agent | `/claw/memory` | `.claw-memory/<agent-id>/memory/` |

- **Session History:** Normalized JSONL record of every successful LLM turn, captured transparently at the proxy boundary. The agent does not write this. Fields include `reported_cost_usd`, `tool_trace` (for managed tool calls), and `memory_op` (for recall/retain operations).
- **Portable Memory:** The agent's own active scratchpad. Agents can read/write notes, drafts, and learned facts here.
- **Cross-Runner Portability:** Because these paths are canonically managed by Clawdapus, you can swap an agent's `CLAW_TYPE` (e.g., migrating from OpenClaw to PicoClaw) and its memory and session history will automatically follow it into the new runtime.

### Ambient Memory (v0.5.0)

When a service subscribes to a memory service via `x-claw.memory`, cllama performs:

- **Pre-turn recall**: Before each inference turn, cllama queries the memory service's `/recall` endpoint and injects relevant context.
- **Post-turn retain**: After each turn, cllama sends the conversation to the memory service's `/retain` endpoint for storage.
- **Governed forget**: `claw memory forget` sends tombstone requests to `/forget` and records local tombstones so subsequent backfills skip those entries.

`claw up` compiles `memory.json` into each subscribing agent's cllama context directory with endpoint URLs, auth tokens, and timeout configuration.

### Managed Tool Mediation (v0.5.0)

When a service subscribes to tools via `x-claw.tools`, cllama performs bounded tool execution within the inference turn:

- Tools are injected into the LLM request as available tool definitions
- When the model calls a tool, cllama executes against the providing service. The execution path depends on the descriptor: HTTP-native services use the per-tool `http` metadata; MCP sidecars (descriptor declares a top-level `mcp` block, v0.11.0+) are reached via the Streamable HTTP `tools/call` endpoint with cached `initialize` sessions.
- Tool results are fed back to the model for up to 8 rounds (configurable)
- `tool_trace` entries appear in session history for auditability
- Works with both OpenAI-compatible and Anthropic-format requests
- Supports synthetic SSE re-streaming when the runner requested streaming

`claw up` compiles `tools.json` into each subscribing agent's cllama context directory:

```json
{
  "version": 1,
  "tools": [...],
  "policy": {
    "max_rounds": 8,
    "timeout_per_tool_ms": 30000,
    "total_timeout_ms": 120000
  }
}
```

## Communication Tools Contract

All 7 runtimes enforce private thinking + explicit `send_message` delivery — agent reasoning never reaches Discord automatically.

- **Hermes**: `HERMES_TOOL_ONLY_MODE=1` injected when Discord handles are present; runtime patches suppress text auto-routing
- **OpenClaw**: enforced natively
- **NullClaw, MicroClaw, NanoClaw, NanoBot, PicoClaw**: `discord-responder.sh` passes a `send_message` tool to the LLM; only posts to Discord when the tool is called

CLAWDAPUS.md includes a `## Communication Tools` section with private-thinking policy whenever handles are configured.

## cllama Governance Proxy

The proxy sits between agents and LLM providers. Agents get bearer tokens, proxy holds real API keys.

### How it works

1. Agent calls `http://cllama-passthrough:8080/v1/chat/completions` with bearer token
2. Proxy resolves agent identity from token (`<agent-id>:<48-hex-secret>`)
3. Proxy routes to correct provider, swaps bearer token for real API key
4. If tools are configured, proxy injects tool definitions and handles bounded tool execution loops
5. If memory is configured, proxy runs pre-turn recall and post-turn retain
6. Proxy extracts token usage, tracks cost, emits audit log
7. Response streamed back to agent transparently

### Bearer token format

`<agent-id>:<48-hex-chars>` — generated by `crypto/rand`, injected into agent env and proxy context.

### Context directory (auto-generated per agent)

```
/claw/context/<agent-id>/
  metadata.json     # token, pod, service, type
  AGENTS.md         # compiled behavioral contract
  CLAWDAPUS.md      # infrastructure map
  tools.json        # managed tool manifest (when tools subscribed)
  memory.json       # memory service config (when memory subscribed)
```

### Provider support

| Provider | Auth | Model format |
|----------|------|-------------|
| OpenAI | Bearer | `openai/gpt-4o` |
| Anthropic | X-Api-Key | `anthropic/claude-sonnet-4` |
| OpenRouter | Bearer | `openrouter/anthropic/claude-sonnet-4` |
| xAI | Bearer | `xai/grok-3` |
| Ollama | None | `ollama/llama3` |

### Credential starvation enforcement

- Real API keys go in `x-claw.cllama-env` (proxy only)
- Agent env is scanned for `*_API_KEY` patterns — preflight fails if found
- Image ENV layer is inspected too — baked keys fail preflight
- Agent only knows its bearer token. No keys, no bypass.

### claw-wall sidecar

Auto-injected by `claw up` when any cllama-enabled service has Discord channel IDs. Polls Discord channels and serves incremental message history to agents. Per-consumer cursors ensure agents only see new messages since their last turn. The service name `claw-wall` is reserved — declaring it in `claw-pod.yml` is a hard error.

## Generated Artifacts

| File | Purpose | Location |
|------|---------|----------|
| `Dockerfile.generated` | Transpiled Clawfile | Next to Clawfile |
| `compose.generated.yml` | Final compose with all enforcement | Next to claw-pod.yml |
| `CLAWDAPUS.md` | Per-agent infrastructure map | Mounted into container |
| `AGENTS.effective.md` | Merged contract + CLAWDAPUS.md (OpenClaw) | Mounted into container |
| `CLAUDE.md` | Combined contract + CLAWDAPUS.md (NanoClaw) | Mounted into container |
| `openclaw.json` | Generated runner config (OpenClaw) | Bind-mounted directory |
| `config.yaml` / `.env` | Generated runner config (Hermes) | Bind-mounted directory |
| `jobs.json` | Cron schedule for INVOKE tasks | Runner state directory |
| `tools.json` | Managed tool manifest per agent | cllama context directory |
| `memory.json` | Memory service config per agent | cllama context directory |

## Drivers

| Driver | CLAW_TYPE | Runner | Config method | Notes |
|--------|-----------|--------|--------------|-------|
| OpenClaw | `openclaw` | OpenClaw | JSON5 Go-native patching -> `openclaw.json` | Primary driver. Read-only container. Docker exec health probe. |
| Hermes | `hermes` | Hermes (Python) | `config.yaml` + `.env` | Discord/Telegram/Slack. `HERMES_TOOL_ONLY_MODE`. Requires at least one handle. |
| NanoBot | `nanobot` | Nanobot (Node.js) | `config.json` | Cron via `jobs.json`. Merged AGENTS.md. |
| NanoClaw | `nanoclaw` | Claude Agent SDK | Combined `CLAUDE.md` | Requires `PRIVILEGE docker-socket true`. Mounts Docker socket. |
| PicoClaw | `picoclaw` | PicoClaw | `config.json` | HTTP `/health` + `/ready` probe. Read-only container. |
| MicroClaw | `microclaw` | MicroClaw (YAML) | `microclaw.config.yaml` | Built-in web UI on port 10961. No INVOKE support. |
| NullClaw | `nullclaw` | NullClaw (HTTP) | `config.json` | Cron via `PostApply` exec (not pre-written). Read-only container. |

All drivers set `CLAW_MANAGED=true`, explicit `HOME`, and `DISCORD_REQUIRE_MENTION` (or equivalent) to prevent feedback loops.

## Fail-Closed Semantics

Clawdapus refuses to start containers when:
- `AGENT` file missing on host
- Driver preflight fails
- Driver post-apply verification fails
- Unsupported surface scheme for the driver
- Credential starvation violated (API keys in agent env or image)
- `tools` or `memory` declared without `cllama` on the service
- Managed service requires `claw up -d` (detached mode)

**This is by design. If enforcement can't be confirmed, the container doesn't run.**

## Skill Mounting

- Image-level: `SKILL <file>` -> `claw.skill.N` labels
- Pod-level: `x-claw.skills: [./file.md]` — merges with image skills by basename (pod wins)
- Generated: service skills from `claw.describe` mounted at `/claw/skills/` with CLAWDAPUS.md pointer
- Precedence: pod > image > generated
- Duplicate basenames across same layer -> validation error

## Troubleshooting

### Agent won't start
1. Check `AGENT` file exists at the host path specified
2. Run `claw doctor` to verify Docker dependencies
3. Check `compose.generated.yml` for the actual compose that was generated
4. Look at driver preflight errors in `claw up` output

### Credential starvation failures
- Move API keys from agent `environment:` to `x-claw.cllama-env:`
- Check image doesn't bake keys in ENV layer: `claw inspect <image>`
- Bearer token is auto-injected; don't set it manually

### Config injection issues (OpenClaw)
- Config dir (`/root/.openclaw/config`) must be bind-mounted as directory, not file
- OpenClaw does atomic writes via rename — file-only mounts cause EBUSY
- OpenClaw home is canonical `~/.openclaw` (`/root/.openclaw`) rather than a separate `/app/state` shim; both `/root` and `/root/.openclaw` are tmpfs-backed so non-root users can traverse and write state
- Check generated `openclaw.json` in the runtime directory
- OpenClaw health: `claw health -f <pod>.yml`

### HANDLE/social topology issues
- Handles broadcast as `CLAW_HANDLE_<UPPERCASED_NAME>_DISCORD_ID` etc.
- `mentionPatterns` auto-derived: Discord uses native `<@!?<id>>`; text-mention platforms use `(?i)\b@?<username>\b`
- `allowBots: true` is unconditional — required for bot-to-bot messaging
- Peer handles: each agent's guild `users[]` includes own ID + all peer bot IDs

### Observability dashboard (clawdash)

When a pod declares a `clawdash` surface, `claw up` publishes the operational dashboard at the emitted `http://localhost:<port>` URL. Relevant views:

- **Fleet / Topology** — running services, wiring, driver types.
- **Agents** — per-agent *contract* as compiled at `claw up` time (AGENTS.md, CLAWDAPUS.md, feed subscriptions, managed tools, memory wiring, metadata).
- **Agents → Live Context** — the system message, tools array, injected feeds, memory recall, time context, and interventions that were assembled for the most recent inference turn. Sourced from the cllama snapshot store (`/internal/context/<agent-id>/snapshot`, proxied through `claw-api`). Credentials and token fields are redacted.
- **Schedule** — `INVOKE` and `x-claw.invoke` cron entries, with `claw api schedule ...` controls.

All views are read-only and scoped through `claw-api` principals. Use this before log-diving — "what did the model actually see last turn" has a direct answer here.

### cllama proxy not working
- Check proxy container is running: `claw ps -f <pod>.yml`
- Proxy named `cllama-passthrough` in compose — agents reach it at `http://cllama-passthrough:8080`
- Dashboard at port 8081 of proxy container
- Check `/claw/context/<agent-id>/metadata.json` has correct token
- Proxy logs are structured JSON on stdout
- SSE debug endpoint: `curl -N -H "Authorization: Bearer <ui_token>" http://<host>:<port>/events`

### Managed tools not working
- Verify the provider service has a `claw.describe` descriptor with `version: 2` and `tools[]`
- Check `tools.json` in `.claw-runtime/context/<agent-id>/`
- `claw audit --type tool_call` shows tool execution traces
- Both consumer and provider must be on the `claw-internal` network (auto-wired by `claw up`)
- Declaring `tools:` without `cllama:` is a hard error

### Memory not working
- Verify the memory service has a `claw.describe` descriptor with `memory` block
- Check `memory.json` in `.claw-runtime/context/<agent-id>/`
- `claw audit` shows `memory_op` telemetry entries
- `claw memory backfill` replays history to a memory service for bootstrapping
- `claw memory forget --entry-id <id>` writes tombstones; subsequent backfills skip those entries
- Declaring `memory:` without `cllama:` is a hard error

## Working Examples

| Example | Path | What it demonstrates |
|---------|------|---------------------|
| Quickstart | `examples/quickstart/` | Single governed OpenClaw Discord bot |
| Trading desk | `examples/trading-desk/` | 5-driver fleet, pod defaults, invoke schedules, `claw.describe` |
| Rollcall | `examples/rollcall/` | 7-driver parity test, sequential-conformance, memory wiring |
| Master Claw | `examples/master-claw/` | Fleet governance, `claw-api` auto-inject, feeds with bearer auth |
| Multi-claw | `examples/multi-claw/` | Shared volume surfaces, Slack handle, non-claw sidecar |
| Nanobot | `examples/nanobot/` | Minimal nanobot driver setup |
| PicoClaw | `examples/picoclaw/` | Minimal picoclaw driver setup |
| OpenClaw | `examples/openclaw/` | Multi-channel Discord guild config |
| Reference memory | `examples/reference-memory/` | ADR-021 memory contract reference implementation (Go HTTP service) |

## Architecture Key Points

- `claw pull` owns pinned infra freshness, pod registry-image pulls, and built-in local runner alias freshness (`openclaw:latest`, `nanobot:latest`, etc.)
- `claw pull --no-runners` skips runner refresh for the fast infra-only path
- `claw build` transpiles Clawfile -> standard Dockerfile -> `docker build` -> OCI image, or builds every pod `build:` service when run without a path
- `claw up` parses pod YAML -> driver enforcement -> `compose.generated.yml` -> `docker compose`, but stays strict about missing images unless `--fix` is set
- **docker compose is the sole lifecycle authority**. Docker SDK is read-only.
- Two-pass loop in compose_up: Pass 1 inspect+resolve all services + cllama wiring, Pass 2 materialize
- After feed resolution: `resolveToolSubscriptions` and `resolveMemorySubscriptions` wire capability providers into the internal network and compile manifests into cllama context
- Generated files are inspectable build artifacts, not hand-edited
- `claw-internal` Docker network is NOT `internal: true` — agents need egress for APIs
