# Pod YAML

Just as the Clawfile extends the Dockerfile, `claw-pod.yml` extends `docker-compose.yml`. Extended keys live under an `x-claw` namespace, which Docker naturally ignores. Any valid `docker-compose.yml` is a valid `claw-pod.yml`. Eject from Clawdapus anytime -- you still have a working compose file.

## A Complete Example

```yaml
x-claw:
  pod: operations-room
  master: coordinator
  cllama-defaults:
    proxy: [passthrough]
    env:
      OPENROUTER_API_KEY: "${OPENROUTER_API_KEY}"
      ANTHROPIC_API_KEY: "${ANTHROPIC_API_KEY}"
  models-defaults:
    primary: openrouter/anthropic/claude-sonnet-4-6
    fallback: anthropic/claude-haiku-4-5
  surfaces-defaults:
    - "service://operations-api"
    - "volume://shared-work read-write"
  feeds-defaults: [ops-context]    # resolved from operations-api's claw.describe
services:
  analyst:
    image: operations-analyst:latest
    build:
      context: ./agents/analyst
    x-claw:
      agent: ./agents/analyst/AGENTS.md
      handles:
        discord:
          id: "${ANALYST_DISCORD_ID}"
          username: "analyst"
      invoke:
        - schedule: "15 8 * * 1-5"
          name: "Morning brief"
          message: "Summarize overnight activity and post the operations brief."
          to: ops-floor
```

This declares a pod called `operations-room` with a master claw, shared proxy config, shared model slots, shared surfaces, and a service that inherits all of it while adding its own Discord identity and scheduled invocations.

## Pod-Level Configuration

The top-level `x-claw` block declares shared configuration that all services inherit by default.

| Key | Purpose |
|-----|---------|
| `pod` | Names the pod (used in generated artifacts and container naming) |
| `master` | Designates the Master Claw governor for fleet oversight |
| `cllama-defaults` | Shared governance proxy configuration -- proxy types and provider API keys |
| `models-defaults` | Shared compile-time model slots inherited by all claw-managed services |
| `surfaces-defaults` | Surfaces available to all services (volumes, services, channels) |
| `feeds-defaults` | Context feeds subscribed by all services |
| `tools-defaults` | Managed tool allowlists inherited by all services |
| `tool-policy-defaults` | Managed-tool mediation policy (`max-rounds`, `timeout-per-tool-ms`, `total-timeout-ms`) inherited by all services |
| `budget-defaults` | Proxy-enforced spend and request-rate caps inherited by all cllama-managed services |
| `memory-defaults` | Memory service subscription inherited by all services |
| `skills-defaults` | Operator skill files inherited by all services |
| `handles-defaults` | Shared chat topology (guild IDs, channel IDs) inherited by all services |
| `context` | Tunes auto-injected runtime context, including channel-context tails and context blocks |
| `channel-memory` | Optional durable channel-memory sidecar integration for channel retrieval |
| `principals` | Explicit `claw-api` principals, verbs, scopes, and injection targets |
| `alert-webhooks` / `alert-mentions` | Pod-scoped fleet alert delivery settings |
| `sequential-conformance` | Allows shared Discord handle IDs for sequential conformance spikes |

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

Service-level fields include `agent`, `persona`, `describe-file`, `cllama`, `cllama-env`, `models`, `handles`, `feeds`, `tools`, `tool-policy`, `budget`, `memory`, `include`, `surfaces`, `skills`, `invoke`, `context`, `claw-api`, `mcp-stdio`, `hermes`, and `count`.

### Managed-Tool Mediation Policy

Services that subscribe to managed tools run their inference turns under a mediation budget: a maximum number of tool rounds, a per-tool execution timeout, and a total wall-clock budget for the whole mediated turn. The defaults (`max-rounds: 8`, `timeout-per-tool-ms: 30000`, `total-timeout-ms: 120000`) suit fast models, but slow reasoning models can exceed the 120s total budget on a single turn — the request fails with a 502 `context deadline exceeded`.

Tune the policy at pod level (`tool-policy-defaults`) or per service (`tool-policy`). Fields you omit keep their defaults; a service-level declaration replaces the pod default entirely:

```yaml
x-claw:
  tool-policy-defaults:
    total-timeout-ms: 300000     # 5 minutes for slow reasoning models

services:
  fast-bot:
    x-claw:
      tool-policy:               # replaces the pod default
        max-rounds: 4
        total-timeout-ms: 60000
```

All values must be positive, and `total-timeout-ms` must be at least `timeout-per-tool-ms`. The merged policy is compiled into each agent's `tools.json` in the cllama context directory.

### Context Blocks

Context blocks are short operator-authored snippets that stay visible in late runtime context without bloating the primary contract. Use them for durable operating focus that should appear every turn, such as a compact policy reminder, a feed-reading frame, or current campaign objective.

Declare blocks at pod level under `x-claw.context.blocks`, or override them per service under `services.<name>.x-claw.context.blocks`. A service-level list replaces the pod-level list; an explicit empty list suppresses inherited blocks for that service.

```yaml
x-claw:
  context:
    blocks:
      - id: operating-focus
        kind: runtime_motivation
        text: Keep the active operating contract visible; act on current evidence.
        cadence: every_turn
        placement: after_feeds
        max-chars: 800

services:
  quiet-bot:
    x-claw:
      context:
        blocks: []   # suppress pod-level blocks
```

`id` and `text` are required. `kind` defaults to `context_block`, `enabled` defaults to `true`, `cadence` currently supports `every_turn`, `placement` supports `before_feeds` and `after_feeds` (default), and `max-chars` defaults to `800`. `claw up` validates the manifest and writes `context-blocks.json` to each subscribing agent's cllama context directory.

### Budget And Request-Rate Caps

Services with cllama can declare a pre-dispatch budget policy. `limit-usd` caps known reported spend in a sliding session-history window; `max-requests` caps successful 2xx turns in that same window. When a cap is already reached, cllama rejects the next OpenAI-compatible or Anthropic-format request with HTTP 429 and logs `budget_exceeded` or `rate_limited`.

Declare defaults at pod level (`budget-defaults`) or override per service (`budget`). A service-level declaration replaces the pod default entirely, and `budget: null` suppresses inherited defaults:

```yaml
x-claw:
  budget-defaults:
    limit-usd: 5.00
    max-requests: 200
    window: 24h
    behavior: hard_stop

services:
  high-volume-analyst:
    x-claw:
      budget:
        max-requests: 1000
        window: 1h
```

At least one of `limit-usd` or `max-requests` is required. `window` accepts any positive Go duration (`30m`, `1h`, `24h`). `behavior` defaults to `hard_stop`; accepted values are `hard_stop`, `rate_limit`, and `soft_alert`. Runtime updates through `POST /fleet/budget/set` write `.claw-governance/<agent-id>/budget.json`; cllama reads that override on each request and merges it over the compiled metadata budget, so operators or a Master Claw can raise caps without `claw up`.

## Defaults and Overrides

The Clawfile bakes defaults into the image. The pod YAML overrides them per-deployment. This follows a consistent pattern:

- **Inherit by default.** Services receive pod-level `cllama-defaults`, `models-defaults`, `surfaces-defaults`, `feeds-defaults`, `tools-defaults`, `tool-policy-defaults`, `budget-defaults`, `memory-defaults`, `skills-defaults`, and `handles-defaults` automatically.
- **Override to replace.** Setting a field at the service level replaces the pod default entirely, except `models`, which merges additively per slot.
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

## Model Slot Precedence

Image `MODEL` labels still define the base slot map, but pod YAML can retarget slots at deploy time:

```yaml
x-claw:
  models-defaults:
    primary: openrouter/anthropic/claude-sonnet-4-6
    fallback: anthropic/claude-haiku-4-5

services:
  analyst:
    image: trading-desk-analyst:latest
    x-claw:
      agent: ./agents/analyst/AGENTS.md
      models:
        primary: openrouter/google/gemini-2.5-flash
```

Precedence is:

- service `x-claw.models`
- pod `x-claw.models-defaults`
- image `MODEL` labels

`x-claw.models` merges additively over `models-defaults`, so overriding `primary` still inherits `fallback` unless you explicitly suppress pod defaults. `models: {}` and `models: null` both suppress pod defaults only; image-declared slots still apply.

### Ordered Fallback Chains

The `fallback` slot accepts an ordered list. cllama walks the chain in
declared order when a provider is exhausted:

```yaml
x-claw:
  models-defaults:
    primary: openai/gpt-5.6
    fallback:
      - openai/gpt-5.1
      - anthropic/claude-sonnet-5
      - anthropic/claude-haiku-4-5
```

Chain rules:

- The fallback family replaces **atomically**: any service-level `fallback`
  declaration (scalar or list) replaces the entire default chain — chains
  never interleave across layers. The same rule applies to image `MODEL
  fallback` labels: a pod-declared chain replaces the image's fallback.
- Only `fallback` accepts a list. Other slots (`primary`, `analysis`, ...)
  are scalar, and non-fallback slots never participate in failover.
- Clawfile images declare at most one `MODEL fallback`; longer chains are
  pod-level deployment policy.

See ADR-019 for the full failover contract.

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

## Stdio MCP Sidecars

Use `x-claw.mcp-stdio` on a sidecar service to run a stdio MCP command behind the shared Streamable HTTP wrapper image:

```yaml
services:
  search:
    image: ghcr.io/mostlydev/claw-mcp-stdio:v0.23.1
    environment:
      PERPLEXITY_API_KEY: ${PERPLEXITY_KEY}
    expose:
      - "8080"
    x-claw:
      describe-file: ./perplexity.claw-describe.json
      mcp-stdio:
        command: npx
        args: ["-y", "perplexity-mcp"]
```

`command` is required and `args` is a JSON-safe list; no shell interpolation is used. `describe-file` is a host path relative to the pod file and should contain a v2 descriptor with `mcp: { "transport": "streamable_http", "path": "/mcp" }` plus the baked `tools[]` snapshot.

## Channel Context Tuning

When any cllama-enabled service has Discord channels in its handles, `claw up` auto-injects the `claw-wall` sidecar plus two feeds for each consuming agent: `channel-context` (cursored delta tail, mention/turn context) and `channel-awareness` (uncursored last-24h raw window, always-on memory of the room). They are tuned through the same `x-claw.context.channel` block, but each feed still has its own default caps where noted. See [Social Topology · Channel Context Feed](/guide/social-topology#channel-context-feed-claw-wall).

```yaml
x-claw:
  pod: operations-room
  context:
    channel:
      since: 24h          # window covered by the feeds (default 24h)
      limit: 40           # max messages returned (default: 40 for channel-context, 60 for channel-awareness)
      max-chars: 32768    # byte cap on rendered body (default 32 KB)
      buffer: 5000        # per-channel safety cap in the wall sidecar (default 5000)
```

Service-level `x-claw.context.channel` overrides pod-level values for that service. The same block tunes both `channel-context` and `channel-awareness` — busy production rooms typically want `max-chars` somewhere in the 64-256 KB range so the 24h awareness feed actually covers a meaningful slice of the day rather than the last handful of messages. `buffer` is shared across the pod, so when services disagree the largest value wins, with the built-in 5000-message floor still applied. `since` accepts any Go duration (`30m`, `2h15m`, `24h`). Set `max_chars` if you prefer the underscore alias; mixing the two with conflicting values is a parse error.

::: warning Raise the cllama injection budget too
`x-claw.context.channel.max-chars` only sizes the *source* window `claw-wall` returns. cllama applies its own bounded budgets when it injects feeds into the prompt — `32 KB` per feed and `64 KB` aggregate by default. A pod that raises `max-chars` to 256 KB but leaves cllama at its defaults will still have the awareness feed truncated, or skipped entirely when earlier feeds (market/style context, scaffolds, memory) consume the shared aggregate budget first. Raise `CLLAMA_FEED_MAX_RESPONSE_BYTES` and `CLLAMA_FEED_MAX_TOTAL_BYTES` together via `x-claw.cllama-defaults.env` so the larger window actually reaches the model. See [cllama · Feed Injection Budgets](/guide/cllama#feed-injection-budgets).
:::

The wall serves `mode=tail` (non-consuming, latest-N walk) by default. Starting in v0.14.0, cllama drives the feed as a delta-since-watermark by adding `after=<channel_id>:<message_id>` cursors to the URL, so each turn fetches only new messages instead of re-pasting the full tail. `since` and `limit` from `x-claw.context.channel` remain the bootstrap and dual-cap bounds. The wall's retention horizon and startup backfill budget are sidecar env settings (`CLAW_WALL_RETENTION`, `CLAW_WALL_BACKFILL_MAX_PAGES`) rather than pod-YAML schema. See [Social Topology · Cursored Append-Only Deltas](/guide/social-topology#cursored-append-only-deltas) for the proxy-side model. The legacy cursor/mailbox path remains callable as `mode=delta` for any consumer that genuinely wants oldest-unread paging; generated feeds do not use it.

## Generated Output

`claw up` reads the pod YAML, inspects images, runs driver enforcement, generates per-agent configs, wires the cllama proxy, and calls `docker compose`. The output is `compose.generated.yml` -- a standard compose file written next to the pod file. Inspect it freely, but do not hand-edit it; it is regenerated on every `claw up`.
