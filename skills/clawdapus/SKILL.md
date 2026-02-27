---
name: clawdapus
description: Use when working with the claw CLI, Clawfiles, claw-pod.yml, cllama proxy, or deploying AI agent containers with Clawdapus. Use when you see CLAW_TYPE, AGENT, MODEL, CLLAMA, CONFIGURE, INVOKE, SURFACE, HANDLE, TRACK, SKILL, or PRIVILEGE directives. Use when diagnosing agent startup failures, credential starvation, config injection, or governance proxy issues.
---

# Clawdapus — Operational Skill

Infrastructure-layer governance for AI agent containers. `claw` treats agents as untrusted workloads — reproducible, inspectable, diffable, killable.

**Mental model:** Clawfile is to Dockerfile what claw-pod.yml is to docker-compose.yml. Standard Docker directives pass through unchanged. Claw directives compile to labels + generated scripts. Eject anytime — you still have working Docker artifacts.

## CLI Commands

```bash
# Prerequisites
go build -o bin/claw ./cmd/claw    # build from source
claw doctor                         # verify Docker, buildx, compose

# Build
claw build -t <image> <path>        # Clawfile -> Dockerfile.generated -> docker build
claw inspect <image>                 # show claw.* labels from built image

# Pod lifecycle (mirrors docker compose UX)
claw up [-f <pod>.yml] [-d]      # parse pod, enforce drivers, emit compose.generated.yml, launch
claw down [-f <pod>.yml]         # tear down
claw ps [-f <pod>.yml]           # container status
claw logs [-f <pod>.yml] [svc]   # stream logs
claw health [-f <pod>.yml]       # driver health probes
```

`-f` locates `compose.generated.yml` next to the pod file. Without `-f`, `claw up` uses `./claw-pod.yml`; other lifecycle commands (`down`/`ps`/`logs`/`health`) look for `compose.generated.yml` in the current directory.

## Clawfile Reference

A Clawfile is an extended Dockerfile. Every valid Dockerfile is a valid Clawfile.

```dockerfile
FROM openclaw:latest

CLAW_TYPE openclaw                          # REQUIRED: selects runtime driver
AGENT AGENTS.md                             # behavioral contract — must exist on host

MODEL primary openrouter/anthropic/claude-sonnet-4
MODEL fallback anthropic/claude-haiku-3-5

CLLAMA passthrough                          # governance proxy type

HANDLE discord                              # platform identity declaration
INVOKE 15 8 * * 1-5  pre-market             # cron schedule (5-field + name)

SURFACE service://trading-api               # infrastructure surface
SURFACE volume://shared-research read-write

SKILL policy/risk-limits.md                 # operator policy, mounted read-only
CONFIGURE openclaw config set key value     # runs at container startup, NOT build time

TRACK apt npm                               # mutation tracking wrappers
PRIVILEGE worker root                       # privilege mode mapping
PRIVILEGE runtime claw-user
```

### Directive Details

| Directive | Purpose | Build → Runtime |
|-----------|---------|-----------------|
| `CLAW_TYPE <type>` | Selects driver (`openclaw`, `nanoclaw`, `generic`). Determines HOW enforcement happens. | Label → driver selection |
| `AGENT <file>` | Behavioral contract. **Must exist on host or startup fails.** Mounted read-only. | Label → `:ro` bind mount |
| `MODEL <slot> <provider/model>` | Named model slot. Multiple allowed. Format: `provider/model-name`. | Label → driver config injection |
| `CLLAMA <type>` | Governance proxy. Currently only `passthrough`. Multiple allowed in data model but runtime enforces max 1. | Label → proxy sidecar wiring |
| `HANDLE <platform>` | Platform identity (`discord`, `slack`). Broadcasts agent ID to all pod services as `CLAW_HANDLE_*` env vars. | Label → driver config + pod env |
| `INVOKE <cron> <name>` | System cron in `/etc/cron.d/claw`. Bot cannot modify. | Baked into image |
| `SURFACE <scheme>://<target> [mode]` | Infrastructure boundary. See Surface Taxonomy. | Label → compose wiring |
| `SKILL <file>` | Reference markdown mounted read-only into runner skill directory. | Label → host path validation + mount |
| `CONFIGURE <cmd>` | **Runs at startup** via `/claw/configure.sh`. For init-time config mutations. NOT build time. | Generates script |
| `TRACK <pkg-managers>` | Installs wrappers for `apt`, `pip`, `npm` to log mutations. | Build-time install |
| `PRIVILEGE <mode> <user>` | Maps privilege modes to user specs. | Label → Docker user/security |

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
      invoke:                         # pod-level scheduled tasks
        - schedule: "*/30 * * * *"
          name: "Heartbeat"
          message: "Post status."
          to: trading-floor
    environment:                      # standard compose — credentials go HERE
      DISCORD_BOT_TOKEN: "${DISCORD_BOT_TOKEN}"
```

### Key rules

- **Credentials**: Standard `environment:` or `secrets:` blocks. Never in `x-claw:` (except `cllama-env` for proxy keys).
- **`cllama-env`**: Provider API keys for the proxy. These go ONLY here — never in agent `environment:`. Credential starvation enforced.
- **`handles`**: Discord bot IDs, usernames, guilds. Clawdapus auto-generates `mentionPatterns`, `allowBots: true`, peer `users[]` allowlist.
- **`surfaces`**: String form (`"channel://discord"`) = simple enable. Map form (`channel://discord: {dm: {...}}`) = routing config.

## cllama Governance Proxy

The proxy sits between agents and LLM providers. Agents get bearer tokens, proxy holds real API keys.

### How it works

1. Agent calls `http://cllama-passthrough:8080/v1/chat/completions` with bearer token
2. Proxy resolves agent identity from token (`<agent-id>:<48-hex-secret>`)
3. Proxy routes to correct provider, swaps bearer token for real API key
4. Proxy extracts token usage, tracks cost, emits audit log
5. Response streamed back to agent transparently

### Bearer token format

`<agent-id>:<48-hex-chars>` — generated by `crypto/rand`, injected into agent env and proxy context.

### Context directory (auto-generated per agent)

```
/claw/context/<agent-id>/
  metadata.json     # token, pod, service, type
  AGENTS.md         # compiled behavioral contract
  CLAWDAPUS.md      # infrastructure map
```

### Provider support

| Provider | Auth | Model format |
|----------|------|-------------|
| OpenAI | Bearer | `openai/gpt-4o` |
| Anthropic | X-Api-Key | `anthropic/claude-sonnet-4` |
| OpenRouter | Bearer | `openrouter/anthropic/claude-sonnet-4` |
| Ollama | None | `ollama/llama3` |

### Credential starvation enforcement

- Real API keys go in `x-claw.cllama-env` (proxy only)
- Agent env is scanned for `*_API_KEY` patterns — preflight fails if found
- Image ENV layer is inspected too — baked keys fail preflight
- Agent only knows its bearer token. No keys, no bypass.

## Generated Artifacts

| File | Purpose | Location |
|------|---------|----------|
| `Dockerfile.generated` | Transpiled Clawfile | Next to Clawfile |
| `compose.generated.yml` | Final compose with all enforcement | Next to claw-pod.yml |
| `CLAWDAPUS.md` | Per-agent infrastructure map | Mounted into container |
| `CLAUDE.md` | Combined contract + CLAWDAPUS.md (NanoClaw) | Mounted into container |
| `openclaw.json` | Generated runner config (OpenClaw) | Bind-mounted directory |
| `jobs.json` | Cron schedule for INVOKE tasks | `/app/state/cron/` |
| `surface-<name>.md` | Service/channel skill files | Runner skill directory |

## Drivers

| Driver | Runner | Config method | Notes |
|--------|--------|--------------|-------|
| `openclaw` | OpenClaw | JSON5-aware Go-native patching | Primary driver. Skills flat in `/claw/skills/`. |
| `nanoclaw` | Claude Agent SDK | Combined CLAUDE.md | Requires `PRIVILEGE docker-socket true`. Skills in `/workspace/skills/<name>/SKILL.md`. |
| `generic` | Any | Minimal (env + mounts only) | No config injection. |

## Fail-Closed Semantics

Clawdapus refuses to start containers when:
- `AGENT` file missing on host
- Driver preflight fails
- Driver post-apply verification fails
- Unsupported surface scheme for the driver
- Credential starvation violated (API keys in agent env or image)

**This is by design. If enforcement can't be confirmed, the container doesn't run.**

## Skill Mounting

- Image-level: `SKILL <file>` → `claw.skill.N` labels
- Pod-level: `x-claw.skills: [./file.md]` — merges with image skills by basename (pod wins)
- Generated: `surface-<name>.md` for service and channel surfaces
- Precedence: pod > image > generated
- Duplicate basenames across same layer → validation error

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
- Config dir (`/app/config`) must be bind-mounted as directory, not file
- OpenClaw does atomic writes via rename — file-only mounts cause EBUSY
- Check generated `openclaw.json` in the runtime directory
- OpenClaw health: `claw health -f <pod>.yml`

### HANDLE/social topology issues
- Handles broadcast as `CLAW_HANDLE_<UPPERCASED_NAME>_DISCORD_ID` etc.
- `mentionPatterns` auto-derived: text `(?i)\b@?<username>\b` + native `<@!?<id>>`
- `allowBots: true` is unconditional — required for bot-to-bot messaging
- Peer handles: each agent's guild `users[]` includes own ID + all peer bot IDs

### cllama proxy not working
- Check proxy container is running: `claw ps -f <pod>.yml`
- Proxy named `cllama-passthrough` in compose — agents reach it at `http://cllama-passthrough:8080`
- Dashboard at port 8081 of proxy container
- Check `/claw/context/<agent-id>/metadata.json` has correct token
- Proxy logs are structured JSON on stdout

## Working Examples

| Example | Path | What it demonstrates |
|---------|------|---------------------|
| Single agent | `examples/openclaw/` | Discord handle, skill emit, service surface |
| Multi-agent | `examples/multi-claw/` | Shared volume, different access modes |
| Trading desk | `examples/trading-desk/` | 3 agents, cllama proxy, scheduling, credential starvation |

## Architecture Key Points

- `claw build` transpiles Clawfile → standard Dockerfile → `docker build` → OCI image
- `claw up` parses pod YAML → driver enforcement → `compose.generated.yml` → `docker compose`
- **docker compose is the sole lifecycle authority**. Docker SDK is read-only.
- Two-pass loop in compose_up: Pass 1 inspect+resolve all services + cllama wiring, Pass 2 materialize
- Generated files are inspectable build artifacts, not hand-edited
- `claw-internal` Docker network is NOT `internal: true` — agents need egress for APIs
