# Surfaces, Skills & CLAWDAPUS.md

Clawdapus solves a fundamental problem: an agent is useless if it does not know what it can do or who it is. The context and discovery layer ensures every Claw knows its identity, its boundaries, and the capabilities available to it -- all resolved at compile time.

## Surfaces

Surfaces define the infrastructure boundaries an agent can reach. They are declared under `x-claw.surfaces` in the pod YAML and serve two audiences: operators get topology visibility, agents get capability discovery.

### Surface Types

| Type | Syntax | Purpose |
|------|--------|---------|
| **Volume** | `volume://shared-cache read-write` | Shared filesystem between services. Access mode (`read-only` or `read-write`) is declared and enforced. |
| **Host** | `host://./data/models read-only` | Bind mount from the host filesystem into the container. |
| **Service** | `service://trading-api` | Pod-internal API or MCP server. The agent can call this service over the pod network. |
| **Channel** | `channel://discord`, `channel://telegram`, `channel://slack` | Chat platform presence. Declares which platforms the agent operates on. |

### Declaring Surfaces

Surfaces are declared per-service or inherited from pod-level defaults:

```yaml
x-claw:
  pod: trading-desk
  surfaces-defaults:
    - "service://trading-api"
    - "volume://shared-research read-write"

services:
  analyst:
    x-claw:
      surfaces:
        - ...                              # inherit pod defaults
        - "volume://analyst-scratch read-write"  # add service-specific surface
```

::: tip Pod-Level Defaults
Use `surfaces-defaults` at the pod level for surfaces shared across most services. Individual services inherit by default, replace by declaring their own list, or extend using the `...` spread token. See [Compilation Principles](./compilation-principles) for the full override semantics.
:::

### Channel Surfaces

Channel surfaces go beyond simple declaration. They support map-form configuration for routing policy:

```yaml
surfaces:
  - channel://discord:
      dm:
        policy: allowlist
        allowFrom: ["123456789"]
      guilds:
        - id: "${GUILD_ID}"
          requireMention: true
          users: [allow_from_handles]
```

Channel surface settings are applied after handle defaults. The `allow_from_handles` token automatically expands to include every peer bot in the pod, enabling controlled bot-to-bot messaging within a guild.

## CLAWDAPUS.md -- The Infrastructure's Letter to the Agent

Every Claw receives a generated `CLAWDAPUS.md` file injected into its workspace. This is the single context document that tells the agent everything it needs to know about its operational environment.

### What CLAWDAPUS.md Contains

- **Agent identity** -- name, pod membership, role
- **Allowed surfaces** -- every surface the agent can access, with mount paths and access modes
- **Service descriptions** -- capabilities of each `service://` surface, inlined from `claw.describe` descriptors
- **Peer handles** -- other agents in the pod, their platform identities, how to mention them
- **Available feeds** -- data feeds the agent is subscribed to
- **Skill index** -- paths to all mounted skill files

::: info Generated, Not Authored
`CLAWDAPUS.md` is a generated artifact, produced fresh on every `claw up`. Do not hand-edit it. The source of truth is the pod YAML and the service descriptors. When you add a service to the pod, the skill map updates automatically. No code changes needed.
:::

### How Service Descriptions Get There

When a service image carries a `claw.describe` label, the compiler extracts the descriptor during `claw up` and inlines the service's description directly into the CLAWDAPUS.md surface sections. This means workflow-critical API documentation is always in prompt context without extra pod YAML authoring.

```
## Surfaces

### service://trading-api
Real-time and historical market data API.

**Available operations:**
- `get_price` -- Current and historical token price data
- `get_whale_activity` -- Large wallet movements in last N hours

**Authentication:** Bearer token via `TRADING_API_TOKEN`

### volume://shared-research
Mount path: /mnt/shared-research (read-write)
```

Operator-authored skills (policy files, includes with `mode: reference`) remain as separate mounted files in the runner's skill directory. Only generated context is collapsed into CLAWDAPUS.md.

## Pod Skills vs. Claw Skills

Clawdapus treats compile-time skill material and runtime-authored skills as different state classes:

| Class | Owner | Source | Host path | Container path |
|-------|-------|--------|-----------|----------------|
| **Pod skills** | Operator / service | `SKILL`, `x-claw.skills`, `x-claw.include` reference material, and service descriptors | Generated or source files under the pod checkout / `.claw-runtime` | Read-only file mounts inside the runner skill directory |
| **Claw skills** | Running claw | Skills the agent creates in its own runner skill directory | `.claw-skills/<claw-id>/skills/` | Writable mount at the driver-reported skill directory |

The writable claw-skill directory is mounted first. Declared pod skills and service manuals are then mounted read-only at their exact paths, so operator-owned skills override name conflicts while agent-authored skills survive `claw up`, image rebuilds, and driver migrations.

## Skills Discovery

Skills discovery is the mechanism by which agents learn what pod services can do. It is entirely compile-time -- no runtime querying, no service registration endpoints.

### The Discovery Pipeline

1. **Service carries a descriptor.** The image includes a `claw.describe` label pointing to a structured JSON file that advertises endpoints, feeds, auth requirements, and an optional skill file.

2. **`claw up` extracts the descriptor.** During image inspection, the compiler reads the label and parses the descriptor.

3. **Descriptor is projected into CLAWDAPUS.md.** Service descriptions, endpoint lists, and auth requirements are inlined into the agent's context document.

4. **Skill files are mounted.** If the descriptor references a skill file, it is extracted from the image and mounted read-only into the agent's skill directory.

### Framework Adapters

Services do not have to manually author descriptors. Framework adapters generate them from code introspection:

- **RailsTrail** -- Introspects Rails routes, state machines, and manual actions to produce a `claw.describe` descriptor automatically. The developer writes Rails code; the adapter produces the descriptor; `claw up` compiles it into the pod.

Future adapters can follow the same pattern for any framework that exposes its capabilities through introspectable metadata.

### `claw skillmap`

The `claw skillmap` command gives operators a complete view of what skills an agent has access to and where they came from:

```bash
$ claw skillmap crypto-crusher-0

  FROM market-scanner (service://market-scanner):
    get_price            Current and historical token price data
    get_whale_activity   Large wallet movements in last N hours
    [discovered via OpenAPI → skills/surface-market-scanner.md]

  FROM shared-cache (volume://shared-cache):
    read-write at /mnt/shared-cache
```

Every entry traces back to its source -- a service descriptor, a volume declaration, or an operator-authored skill file. Add a service to the pod, and `claw skillmap` reflects the change after the next `claw up`.

## Feed Manifests

Feeds are live data injected into agent context. They follow the same provider-owns, consumer-subscribes model as the rest of the compilation pipeline.

### How Feeds Work

A service declares the feeds it provides via its `claw.describe` descriptor:

```json
{
  "feeds": [
    { "name": "market-context", "path": "/feeds/market", "ttl": 300 }
  ]
}
```

Agents subscribe by name in their `x-claw` block or inherit from `feeds-defaults`:

```yaml
x-claw:
  feeds-defaults: [market-context]
```

During `claw up`, the compiler resolves feed names against a registry built from all service descriptors in the pod. The result is a feed manifest injected into the cllama context, telling the proxy where to fetch data and how often to refresh it.

::: warning Feeds Are Provider-Owned
Feeds are declared by the service that provides the data, not by the agent that consumes it. The consumer subscribes by name and never needs to know the provider's endpoint path, TTL, or auth scheme. See [ADR-017](https://github.com/mostlydev/clawdapus/blob/master/docs/decisions/017-pod-defaults-and-service-self-description.md) for the design rationale.
:::

### Feed Resolution

Feed resolution is a two-phase process:

1. **Parse phase** -- The YAML parser stores unresolved feed names. It has no image knowledge at this point.
2. **Compile phase** -- After image inspection, `claw up` builds the feed registry from extracted descriptors and resolves each feed name. Unresolved names are a compile-time error.

Explicit feed declarations (with source, path, and TTL) bypass the registry and work directly, providing an escape hatch when needed.

## Putting It Together

The context and discovery layer forms a closed loop:

1. **Operator declares surfaces** in the pod YAML
2. **Services self-describe** via `claw.describe` in their images
3. **`claw up` compiles** descriptors into CLAWDAPUS.md, feed manifests, and skill mounts
4. **Agent receives CLAWDAPUS.md** and knows exactly what it can do, who its peers are, and how to reach every service

Add a service to the pod. The next `claw up` extracts its descriptor, updates every agent's CLAWDAPUS.md, and refreshes the skill map. Remove a service, and the references disappear. The agent's view of the world is always consistent with what is actually deployed.

This is the compile-time guarantee: the agent's context matches reality because both are generated from the same source.
