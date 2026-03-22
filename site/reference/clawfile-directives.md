# Clawfile Directives

The Clawfile extends the Dockerfile with directives that `claw build` translates into standard Dockerfile primitives (`LABEL`, `ENV`, `RUN`). The output is a plain OCI image. Any valid Dockerfile is a valid Clawfile.

## Directive Reference

| Directive | Purpose |
|-----------|---------|
| `CLAW_TYPE` | Select the runtime driver |
| `AGENT` | Name the behavioral contract file |
| `PERSONA` | Import a persona workspace |
| `MODEL` | Bind named model slots to providers |
| `CLLAMA` | Declare governance proxy type(s) |
| `HANDLE` | Declare platform identity |
| `INVOKE` | Schedule cron-based invocations |
| `SURFACE` | Declare available surfaces (volumes, services, channels) |
| `SKILL` | Mount operator policy files read-only |
| `INCLUDE` | Compose contracts at pod level |
| `CONFIGURE` | Apply runner-specific config mutations |
| `TRACK` | Wrap package managers to log mutations |
| `PRIVILEGE` | Drop container privileges |

## CLAW_TYPE

```dockerfile
CLAW_TYPE openclaw
```

Selects the runtime driver that governs how the agent container is configured and validated. Available types: `openclaw`, `hermes`, `nanoclaw`, `nanobot`, `picoclaw`, `nullclaw`, `microclaw`.

The type determines which base image to use, what config format is generated, which platforms are supported, and how health probes work.

## AGENT

```dockerfile
AGENT AGENTS.md
```

Names the behavioral contract file. This file is bind-mounted read-only into the container at runtime -- it survives full container compromise. The contract defines the agent's purpose, rules, and constraints.

## PERSONA

```dockerfile
PERSONA ./persona
PERSONA oci://ghcr.io/myorg/analyst-persona:v1
```

Imports a persona workspace containing memory, history, style, and knowledge files. Accepts a local path or an OCI artifact reference. When present, `CLAW_PERSONA_DIR` is set in the container environment.

Local references are copied with traversal and symlink hardening. Non-local references are pulled as OCI artifacts.

## MODEL

```dockerfile
MODEL primary openrouter/anthropic/claude-sonnet-4
MODEL fallback anthropic/claude-haiku-3-5
```

Binds named model slots to provider-qualified model identifiers. The `primary` slot is the default model used by the runner. Additional slots like `fallback` provide alternatives for cost or capability routing.

When cllama is enabled, the agent never contacts providers directly -- the proxy resolves model slots to real API calls.

## CLLAMA

```dockerfile
CLLAMA passthrough
```

Declares the governance proxy type. The `passthrough` reference implementation provides credential starvation, identity resolution, cost tracking, and audit logging without modifying prompts or responses.

Future proxy types (e.g. `cllama-policy`) will add bidirectional interception for prompt decoration and response amendment.

## HANDLE

```dockerfile
HANDLE discord
HANDLE telegram
HANDLE slack
```

Declares platform identity for the agent. Clawdapus broadcasts every agent's handles as environment variables into every service in the pod, enabling bot-to-bot discovery and routing.

The driver automatically wires runner config for the declared platform: mention patterns, bot allowlists, guild routing, and peer discovery.

Handle details (bot ID, username, guild IDs) are specified in the pod YAML under `x-claw.handles`, not in the Clawfile.

## INVOKE

```dockerfile
INVOKE 15 8 * * 1-5  pre-market
```

Schedules cron-based invocations. The format is a standard cron expression followed by a name. Invocation details (message content, target channel) are specified in the pod YAML under `x-claw.invoke`.

## SURFACE

```dockerfile
SURFACE service://trading-api
SURFACE volume://shared-research read-write
SURFACE channel://discord
```

Declares the agent's access to external resources. Surfaces come in three types:

- **`service://`** -- access to a service endpoint. Service skills are auto-discovered from `claw.describe` labels.
- **`volume://`** -- access to a shared volume with an access mode (`read-only` or `read-write`).
- **`channel://`** -- routing policy for platform channels (map-form supports DM policy, guild allowlists, mention requirements).

Surfaces are typically declared at pod level via `x-claw.surfaces` or `surfaces-defaults` rather than in the Clawfile.

## SKILL

```dockerfile
SKILL policy/risk-limits.md
```

Mounts operator policy files read-only into the runner. Skills appear in the agent's `CLAWDAPUS.md` context document and are available as reference material during reasoning.

## INCLUDE

```dockerfile
INCLUDE enforce ./compliance/trading-rules.md
INCLUDE guide ./style/house-voice.md
INCLUDE reference ./docs/api-reference.md
```

Composes contracts at pod level with three inclusion modes:

- **`enforce`** -- inlined into the generated `AGENTS.md` as hard rules.
- **`guide`** -- inlined as soft guidance.
- **`reference`** -- mounted as read-only skill material, not inlined.

## CONFIGURE

```dockerfile
CONFIGURE nullclaw config set channels.discord.accounts.main.guild_id "123456789"
CONFIGURE nullclaw config set channels.discord.accounts.main.require_mention true
```

Applies runner-specific config mutations at init time. Runs after defaults, so it overrides what `HANDLE` and other directives generated. Values are parsed as JSON when possible (booleans, numbers, arrays, objects are unquoted; strings are quoted).

## TRACK

```dockerfile
TRACK pip install tiktoken
TRACK apt install jq
```

Wraps package manager commands to log mutations. Tracked changes can later be promoted to permanent image layers via recipe promotion.

## PRIVILEGE

```dockerfile
PRIVILEGE drop net_raw
```

Drops container privileges to reduce the attack surface. Maps to standard Linux capability drops in the generated container configuration.

## Example Clawfile

```dockerfile
FROM openclaw:latest

CLAW_TYPE openclaw
AGENT AGENTS.md

MODEL primary openrouter/anthropic/claude-sonnet-4
MODEL fallback anthropic/claude-haiku-3-5

CLLAMA passthrough

HANDLE discord
INVOKE 15 8 * * 1-5  pre-market

SURFACE service://trading-api
SURFACE volume://shared-research read-write

SKILL policy/risk-limits.md
```
