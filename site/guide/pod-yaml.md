# Pod YAML

Just as the Clawfile extends the Dockerfile, `claw-pod.yml` extends `docker-compose.yml`. Extended keys live under an `x-claw` namespace, which Docker naturally ignores. Any valid `docker-compose.yml` is a valid `claw-pod.yml`. Eject from Clawdapus anytime -- you still have a working compose file.

## A Complete Example

```yaml
x-claw:
  pod: trading-desk
  master: octopus
  cllama-defaults:
    proxy: [passthrough]
    env:
      OPENROUTER_API_KEY: "${OPENROUTER_API_KEY}"
      ANTHROPIC_API_KEY: "${ANTHROPIC_API_KEY}"
  surfaces-defaults:
    - "service://trading-api"
    - "volume://shared-research read-write"
  feeds-defaults: [market-context]    # resolved from trading-api's claw.describe
services:
  tiverton:
    image: trading-desk-tiverton:latest
    build:
      context: ./agents/tiverton
    x-claw:
      agent: ./agents/tiverton/AGENTS.md
      handles:
        discord:
          id: "${TIVERTON_DISCORD_ID}"
          username: "tiverton"
      invoke:
        - schedule: "15 8 * * 1-5"
          name: "Pre-market synthesis"
          message: "Run pre-market synthesis and post the floor briefing."
          to: trading-floor
```

This declares a pod called `trading-desk` with a master claw, shared proxy config, shared surfaces, and a service that inherits all of it while adding its own Discord identity and scheduled invocations.

## Pod-Level Configuration

The top-level `x-claw` block declares shared configuration that all services inherit by default.

| Key | Purpose |
|-----|---------|
| `pod` | Names the pod (used in generated artifacts and container naming) |
| `master` | Designates the Master Claw governor for fleet oversight |
| `cllama-defaults` | Shared governance proxy configuration -- proxy types and provider API keys |
| `surfaces-defaults` | Surfaces available to all services (volumes, services, channels) |
| `feeds-defaults` | Context feeds subscribed by all services |
| `handles-defaults` | Shared chat topology (guild IDs, channel IDs) inherited by all services |

::: warning Provider Keys Are Pod-Level
Provider API keys for cllama-managed services belong in `x-claw.cllama-defaults.env`, not in individual service `environment:` blocks. Use YAML anchors if you need keys at the service level too.
:::

## Service-Level `x-claw`

Each service under `services:` can have its own `x-claw` block with service-specific configuration:

```yaml
services:
  analyst:
    image: trading-desk-analyst:latest
    x-claw:
      agent: ./agents/analyst/AGENTS.md
      persona: ./personas/analyst
      cllama:
        proxy: [passthrough]
      handles:
        discord:
          id: "${ANALYST_DISCORD_ID}"
          username: "analyst"
      surfaces:
        - "service://trading-api"
        - "volume://shared-research read-only"
      skills:
        - ./policy/risk-limits.md
      invoke:
        - schedule: "0 9 * * 1-5"
          name: "Morning analysis"
          message: "Run morning market analysis."
```

Service-level fields include `agent`, `persona`, `cllama`, `cllama-env`, `handles`, `surfaces`, `skills`, `invoke`, `include`, and `count`.

## Defaults and Overrides

The Clawfile bakes defaults into the image. The pod YAML overrides them per-deployment. This follows a consistent pattern:

- **Inherit by default.** Services receive pod-level `cllama-defaults`, `surfaces-defaults`, `feeds-defaults`, and `handles-defaults` automatically.
- **Override to replace.** Setting a field at the service level replaces the pod default entirely.
- **Spread to extend.** Use `...` to inherit the pod default and add to it:

```yaml
    x-claw:
      skills:
        - ...                          # inherit pod defaults
        - ./policy/escalation.md       # add coordinator-only skill
```

::: tip The Spread Operator
The `...` entry in a list means "include everything from the pod-level default here." This lets you extend shared config without duplicating it.
:::

## Mixed Cognitive and Non-Cognitive Services

A pod is a mixed cluster. Regular API containers, databases, and message queues participate as first-class pod members alongside agents. Non-cognitive services do not need `x-claw` blocks but still benefit from the pod:

- They receive `CLAW_HANDLE_*` environment variables for every agent in the pod
- They can be declared as `service://` surfaces that agents consume
- They can carry `claw.describe` labels for automatic skill discovery

```yaml
services:
  # A regular Rails API -- no x-claw block needed
  trading-api:
    image: trading-api:latest
    ports:
      - "3000:3000"

  # An AI agent that consumes the API
  trader:
    image: trading-desk-trader:latest
    x-claw:
      agent: ./agents/trader/AGENTS.md
      surfaces:
        - "service://trading-api"
```

## Environment Variable Resolution

`claw up` resolves `${...}` placeholders inside `x-claw` metadata from your shell environment and the pod-local `.env` file before generating runtime config. You do not need to duplicate handle IDs, guild IDs, or channel IDs into service `environment:` blocks.

```yaml
x-claw:
  handles-defaults:
    discord:
      guilds: ["${DISCORD_GUILD_ID}"]
      channels:
        trading-floor: "${TRADING_FLOOR_CHANNEL_ID}"
```

## Scaling with `count`

Use `count` to deploy multiple instances of the same agent:

```yaml
services:
  crusher:
    image: crypto-crusher:latest
    x-claw:
      agent: ./agents/crusher/AGENTS.md
      count: 3
```

This expands into ordinal-named compose services: `crusher-0`, `crusher-1`, `crusher-2`. Each instance gets its own bearer token and cllama context when the governance proxy is enabled.

## Generated Output

`claw up` reads the pod YAML, inspects images, runs driver enforcement, generates per-agent configs, wires the cllama proxy, and calls `docker compose`. The output is `compose.generated.yml` -- a standard compose file written next to the pod file. Inspect it freely, but do not hand-edit it; it is regenerated on every `claw up`.
