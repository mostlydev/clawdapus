# Hermes Quickstart

[Hermes](https://github.com/NousResearch/hermes-agent) is a Python agent runtime from Nous Research with first-class Discord support and a tool ecosystem. It is a production-proven Clawdapus driver — this guide takes you from nothing to a governed Hermes agent replying on Discord.

If you're coming from a hand-rolled Python Discord bot, this is the migration path: Hermes gives you the agent loop, Clawdapus gives you reproducible builds, credential isolation, and a governance proxy in front of every model call.

## Prerequisites

- Docker with Compose
- The `claw` CLI — see [Quickstart § Install](/guide/quickstart#install)
- An Anthropic API key (or any [supported provider](/guide/cllama))
- A Discord bot:
  1. Create an application at [discord.com/developers/applications](https://discord.com/developers/applications)
  2. Add a bot and copy its **token** and **application ID**
  3. Under *Privileged Gateway Intents*, enable **MESSAGE CONTENT** — without it the bot connects but never receives messages
  4. Invite it to a server you control

## Clone the Example

```bash
git clone https://github.com/mostlydev/clawdapus.git
cd clawdapus/examples/hermes-quickstart
cp .env.example .env
# fill in ANTHROPIC_API_KEY, DISCORD_BOT_TOKEN, DISCORD_BOT_ID, DISCORD_GUILD_ID
```

The example is four files:

```text
hermes-quickstart/
├── Clawfile       # CLAW_TYPE hermes, model slot, governance directive
├── claw-pod.yml   # one service, one Discord handle
├── AGENTS.md      # the agent's behavioral contract
└── .env.example
```

The `Clawfile` is the whole image recipe:

```dockerfile
FROM ghcr.io/mostlydev/hermes-base:v2026.5.16-claw.3

CLAW_TYPE hermes
AGENT AGENTS.md

MODEL primary anthropic/claude-haiku-4-5

CLLAMA passthrough

HANDLE discord
```

`hermes-base` is upstream `hermes-agent` plus Clawdapus compatibility patches (reply-mention suppression, identity override support, non-blocking slash-command sync). You never run raw upstream Hermes in a pod.

## Run the Operator Loop

```bash
claw pull        # pinned infra (cllama, hermes-base)
claw build       # Clawfile -> Dockerfile.generated -> agent image
claw up -d       # pod -> compose.generated.yml, launch, fail-closed verification
```

`claw up -d` is required (not optional) for managed services: post-apply verification is fail-closed, so a misconfigured pod stops instead of half-starting.

## Verify

Mention the bot:

> @hermes-assistant hello

The reply is a governed turn — it routed through cllama. Check both planes:

```bash
claw audit            # model, latency, tokens, cost for the turn
claw logs assistant   # the runner's own logs
```

## Hermes-Specific Gotchas

These are the five things Hermes operators actually hit. Bookmark this section.

### 1. Identity layering: who the agent thinks it is

Hermes seeds a default `SOUL.md` ("You are Hermes, made by Nous Research") on first boot. Clawdapus overrides this in layers, last writer wins:

1. Upstream default SOUL.md
2. The driver writes the contracted identity from your `AGENT` file
3. A configured [persona](/guide/anatomy) SOUL.md takes priority over both

If your agent introduces itself as "Hermes by Nous Research", your `AGENTS.md` contract never made it into the identity layer — rerun `claw up` and check `claw compose exec assistant cat /root/.hermes/SOUL.md`.

### 2. Env passthrough: compose `environment:` does not reach tools

Hermes tool execution reads a `.env` file, not the container environment. Only variables in the driver's passthrough allowlist (`allowedEnvPassthroughKeys()` in `internal/driver/hermes/config.go`) cross over. If your agent's tool needs an env var and it isn't on the allowlist, it will be invisible at tool time even though `docker inspect` shows it on the container.

### 3. Memory caps and cross-UID files

Hermes writes runner-owned memory into Clawdapus' portable memory surface. The managed `hermes-base` image defaults to a 12000-character memory index cap and a 6000-character user-memory cap; override them per service with `HERMES_MEMORY_INDEX_MAX_CHARS` and `HERMES_USER_MEMORY_MAX_CHARS` when a pod needs a larger scratchpad. When a new memory entry would exceed the cap, Hermes evicts oldest entries if that makes the write fit and reports how many entries it evicted.

`MEMORY.md` is rewritten as mode `0666` after atomic saves so host operators and non-root diagnostics can keep reading it through bind mounts. An individual entry larger than the cap is rejected instead of erasing existing memory.

### 4. Tool-only mode and silent finals

On Discord, Hermes prefers emitting `send_message` tool calls over plain text finals. Clawdapus configures silent-final handling so the agent doesn't double-post. If your agent "thinks but never replies", check whether it produced a final with no `send_message` — `claw logs assistant` shows the turn; the [troubleshooting guide](/guide/troubleshooting) has the full decision table.

### 5. Runtime status is not channel content

Managed Hermes chat services keep lifecycle, retry/fallback, provider-failure,
and background-review status out of content channels by default. `claw logs
assistant` and `/root/.hermes/logs/gateway.log` remain the diagnostic surfaces.
For an interactive/debug service, opt visible status back in with
`HERMES_CHAT_STATUS_DELIVERY=on`; opt background-review summaries back in with
`hermes config set display.memory_notifications on`. Unset
`HERMES_CHAT_STATUS_DELIVERY` preserves upstream Hermes behavior; Clawdapus
sets it to `off` for managed chat services.

### 6. Native tool policy

Hermes ships native tools in its own toolsets. Restrict those tools at compile
time in the service's `x-claw.hermes` block:

```yaml
services:
  assistant:
    x-claw:
      hermes:
        disable-tools: [skill_manage, session_search]
```

The driver writes the resolved list into both the container environment and the
Hermes `.env`; no cllama rule or image patch is needed. `allow-tools` subtracts
from the disabled set and wins if a tool appears in both lists:

```yaml
      hermes:
        disable-tools: [skill_manage, session_search]
        allow-tools: [session_search]
```

This leaves `skill_manage` disabled and enables `session_search`. Explicit
`disable-tools` entries apply even when the service has no Discord or Slack
handle.

### 7. gateway.log: the first diagnostic surface

```bash
claw compose exec assistant cat /root/.hermes/logs/gateway.log
```

Every Discord event Hermes receives is logged here. **Zero entries after startup means the bot is connected but not receiving** — almost always the MESSAGE CONTENT intent (see prerequisites) or a stale gateway session. This one command distinguishes "broken bot" from "deaf bot" instantly.

## Next Steps

- [Pod YAML](/guide/pod-yaml) — multi-agent pods, pod-level defaults, tool policy
- [Managed Tools](/guide/tools) — give the agent schema-validated, governed tools
- [Driver Support Matrix](/guide/drivers) — what Hermes supports vs the other six drivers
- [No credentials yet?](/guide/quickstart) — the [Ollama quickstart](https://github.com/mostlydev/clawdapus/tree/master/examples/ollama-quickstart) runs governed turns with zero secrets
