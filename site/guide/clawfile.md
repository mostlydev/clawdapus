# The Clawfile

The Clawfile is an extended Dockerfile. Any valid Dockerfile is a valid Clawfile. `claw build` is a preprocessor -- it translates extended directives into standard Dockerfile primitives (`LABEL`, `ENV`, `RUN`) and calls `docker build` on the result. The output is a standard OCI container image.

No custom build engine. No proprietary image format. Eject from Clawdapus anytime -- you still have a working OCI image.

## A Complete Example

Here is a Clawfile for a trading desk agent:

```dockerfile
FROM openclaw:latest

CLAW_TYPE openclaw
AGENT AGENTS.md                   # behavioral contract — bind-mounted read-only

MODEL primary openrouter/anthropic/claude-sonnet-4
MODEL fallback anthropic/claude-haiku-3-5

CLLAMA passthrough                # governance proxy — credential starvation + cost tracking

HANDLE discord                    # platform identity — mention patterns, peer discovery
INVOKE 15 8 * * 1-5  pre-market   # scheduled invocation — cron, managed by operator

SURFACE service://trading-api     # declared capabilities — auto-discovered, skill-mapped
SURFACE volume://shared-research read-write

SKILL policy/risk-limits.md       # operator policy — mounted read-only into runner
```

This declares an OpenClaw agent that:
- Has a read-only behavioral contract (`AGENTS.md`)
- Uses Claude Sonnet as its primary model with Haiku as fallback
- Routes all LLM calls through the cllama governance proxy
- Has a Discord identity and responds to mentions
- Runs a pre-market task every weekday at 8:15 AM
- Can access a trading API and a shared research volume
- Has operator-defined risk limits mounted as a read-only skill

## Directive Reference

`claw build` translates these directives into standard Dockerfile primitives. Everything becomes labels, environment variables, or `RUN` commands in the generated image.

| Directive | Purpose |
|-----------|---------|
| `CLAW_TYPE` | Selects the runtime driver (`openclaw`, `hermes`, `nanobot`, `picoclaw`, `nanoclaw`, `microclaw`, `nullclaw`) |
| `AGENT` | Names the behavioral contract file to be bind-mounted read-only |
| `PERSONA` | Imports a persona workspace -- local path or OCI artifact ref |
| `MODEL` | Binds named model slots (e.g., `primary`, `fallback`) to providers |
| `CLLAMA` | Declares governance proxy type(s) |
| `HANDLE` | Declares platform identity (`discord`, `telegram`, `slack`, and others per driver) |
| `INVOKE` | Scheduled invocations via cron expression |
| `SURFACE` | Declared capabilities -- volumes, services, channels |
| `SKILL` | Operator policy files mounted read-only into the runner |
| `INCLUDE` | Contract composition -- `enforce`, `guide`, or `reference` mode |
| `CONFIGURE` | Runner-specific config mutations at container init |
| `TRACK` | Wraps package managers to log mutations for recipe promotion |
| `PRIVILEGE` | Drops container privileges |

## How Directives Become Docker

`claw build` is purely a transpiler. Each directive maps to standard Dockerfile constructs:

::: info Transpilation, Not Magic
The Clawfile is not interpreted at runtime. `claw build` produces a standard Dockerfile, and `docker build` produces a standard OCI image. The extended directives become image labels that `claw up` reads at deployment time.
:::

For example, `CLAW_TYPE openclaw` becomes a label on the image. `MODEL primary openrouter/anthropic/claude-sonnet-4` becomes a label encoding the model binding. `claw up` reads these labels when composing the pod and generates the appropriate runtime configuration for the selected driver.

## CLAW_TYPE and Drivers

The `CLAW_TYPE` directive selects which runtime driver handles the agent. All drivers support `MODEL`, `AGENT`, `CLLAMA`, and `CONFIGURE`. Platform support varies:

| Capability | `openclaw` | `hermes` | `nanoclaw` | `nanobot` | `picoclaw` | `nullclaw` | `microclaw` |
|---|:---:|:---:|:---:|:---:|:---:|:---:|:---:|
| HANDLE: Discord | yes | yes | -- | yes | yes | yes | yes |
| HANDLE: Telegram | -- | yes | -- | yes | yes | yes | yes |
| HANDLE: Slack | -- | yes | -- | yes | yes | yes | yes |
| INVOKE (cron) | yes | yes | -- | yes | yes | yes | -- |
| Structured health | yes | yes | -- | -- | yes | yes | -- |
| Read-only rootfs | yes | yes | -- | yes | yes | yes | -- |

## MODEL Slots

The `MODEL` directive binds named slots to provider/model pairs:

```dockerfile
MODEL primary openrouter/anthropic/claude-sonnet-4
MODEL fallback anthropic/claude-haiku-3-5
MODEL summarizer openrouter/google/gemini-flash-2.0
```

When cllama is enabled, the proxy can silently downgrade a requested model (e.g., from a primary to a fallback) or apply rate limits without the agent knowing its compute was throttled.

## CONFIGURE for Runtime Overrides

`CONFIGURE` runs after driver defaults are generated, allowing fine-grained mutations:

```dockerfile
HANDLE discord

# Pin to one guild
CONFIGURE nullclaw config set channels.discord.accounts.main.guild_id "123456789012345678"

# Require mention in group chats
CONFIGURE nullclaw config set channels.discord.accounts.main.require_mention true
```

::: tip Defaults First, Then Override
`HANDLE` generates sensible driver defaults. `CONFIGURE` overrides specific fields after those defaults are applied. Use `HANDLE` for identity, `CONFIGURE` for policy details.
:::

## Building

```bash
# Build from a Clawfile
claw build -t my-agent:latest ./agents/my-agent

# Separate build context
claw build -t my-agent:latest --context ./build-context ./agents/my-agent

# On first run, claw build auto-builds the local base image if missing
```

The generated `Dockerfile.generated` is a standard Dockerfile. Inspect it to see exactly what `claw build` produced -- but do not hand-edit it, as it is regenerated on every build.
