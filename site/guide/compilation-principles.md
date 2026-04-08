# Compilation Principles

`claw up` is a compiler. It reads the pod file, inspects images, extracts service descriptors, and emits deterministic runtime artifacts. The generated compose file is the single source of truth for what is deployed. These five principles govern the compilation pipeline and must not be violated by new features.

## 1. Compile-Time, Not Runtime

**All wiring -- feeds, skills, identity, surfaces -- is resolved during `claw up`. No runtime self-registration. The generated compose file is the single source of truth.**

### Why it matters

Clawdapus's core value proposition is deterministic, auditable deployment. The operator can `diff` two compose files and know exactly what changed. If services could register capabilities at boot, the running state would diverge from the pod file, and the operator would lose the ability to inspect what is actually deployed.

### How it works in practice

When you run `claw up`, the compiler:

1. Parses `claw-pod.yml` and resolves pod-level defaults
2. Inspects every service image, extracting `claw.describe` labels
3. Builds a feed registry from service descriptors
4. Resolves feed subscriptions against the registry
5. Generates per-agent context files (`CLAWDAPUS.md`, `AGENTS.generated.md`)
6. Emits `compose.generated.yml`

Every artifact is written to disk before `docker compose up` runs. Nothing is deferred to container boot.

### What it prevents

Runtime self-registration leads to ordering bugs (service A boots before service B has registered), ghost state (a crashed service's registration lingers), and untraceable drift between what the pod file says and what is actually running.

## 2. Provider-Owns, Consumer-Subscribes

**Services declare what they offer (feeds, endpoints, auth). Agents subscribe by name. The consumer should never need to know a service's URL path or TTL.**

### Why it matters

When consumers declare their own feed sources, they must know implementation details of the provider: its endpoint path, refresh interval, auth scheme. This couples consumers to providers and means every consumer repeats the same fragile configuration.

### How it works in practice

A trading API image carries a `claw.describe` descriptor that advertises its feeds:

```json
{
  "description": "Real-time and historical market data API",
  "feeds": [
    { "name": "market-context", "path": "/feeds/market", "ttl": 300 }
  ]
}
```

A claw subscribes by name alone:

```yaml
x-claw:
  feeds: [market-context]
```

`claw up` resolves `market-context` against the feed registry built from all service descriptors in the pod. The consumer never touches a URL or TTL.

### What it prevents

Consumer-declared feeds scatter provider knowledge across every service block. When the provider changes an endpoint path, every consumer must update. Provider-owned feeds centralize that knowledge in the one place that should own it.

## 3. Pod-Level Defaults, Service-Level Overrides

**Shared config is declared once at pod level. Services inherit by default, override or extend (`...` spread) as needed.**

### Why it matters

Without defaults, every service in a five-agent pod repeats the same cllama config, the same surfaces, the same feeds. YAML anchors reduce visual noise but not structural duplication. Pod-level defaults eliminate the duplication entirely.

### How it works in practice

```yaml
x-claw:
  pod: trading-desk
  cllama-defaults:
    proxy: [passthrough]
    env:
      OPENROUTER_API_KEY: "${OPENROUTER_API_KEY}"
  models-defaults:
    primary: openrouter/anthropic/claude-sonnet-4
    fallback: anthropic/claude-haiku-4-5
  surfaces-defaults:
    - "service://trading-api"
    - "volume://shared-research read-write"
  feeds-defaults: [market-context]
```

Every claw-managed service inherits these. To replace a field, declare it at the service level. To extend it, use the `...` spread token:

```yaml
x-claw:
  skills:
    - ...                          # pod defaults splice here
    - ./policy/escalation.md       # then this is appended
```

::: tip Spread Rules
- No `...` in the list -- full replacement of defaults
- `...` present -- defaults splice at that position
- At most one `...` per list
- Map fields (`cllama-defaults.env`, `models-defaults`) merge additively; service keys win on collision
:::

### What it prevents

Structural duplication across services. When shared config changes, you update one place instead of N. The spread convention avoids the ambiguity of implicit list merging (append? prepend? deduplicate by what?) that plagues Helm, Kustomize, and Ansible.

## 4. One Canonical Descriptor

**A service's capabilities are declared once (via `claw.describe` in the image) and projected into whatever artifacts need them.**

### Why it matters

Without a single source, the same service capability information gets manually duplicated across pod YAML, skill files, feed declarations, and CLAWDAPUS.md sections. When the service changes, some copies get updated and others do not.

### How it works in practice

A service image carries one descriptor:

```dockerfile
LABEL claw.describe=/app/.claw-describe.json
```

During `claw up`, the compiler extracts this descriptor and projects it into:

- **CLAWDAPUS.md** -- service descriptions inlined into surface sections
- **Feed manifests** -- feed name, path, TTL, auth requirements
- **Effective agent contracts** -- guide content in `AGENTS.generated.md`
- **Skill map** -- the `claw skillmap` output for operator visibility

One declaration, multiple projections. Add a service to the pod, and every downstream artifact updates automatically.

### What it prevents

Documentation drift between what a service actually provides and what agents are told it provides. Manual synchronization of capability information across multiple files.

## 5. Services Self-Describe

**Images carry structured descriptors (`LABEL claw.describe=...`). `claw up` extracts and compiles them. Framework adapters generate descriptors from code introspection.**

### Why it matters

The operator should not have to manually author skill files for every API in the pod. The service knows what it does. The infrastructure should ask the service, not the operator.

### How it works in practice

The `claw.describe` label points to a JSON file inside the image:

```json
{
  "description": "Real-time and historical market data API",
  "feeds": [
    { "name": "market-context", "path": "/feeds/market", "ttl": 300 }
  ],
  "auth": { "type": "bearer", "env": "TRADING_API_TOKEN" },
  "skill": "/app/skills/trading-api.md"
}
```

The descriptor does not contain a service name -- deployment identity comes from the pod YAML, not the image. One image can back multiple compose services.

Framework adapters like RailsTrail can generate descriptors from code introspection: routes become endpoints, state machines become documented workflows, manual actions become skill entries. The developer writes Rails code; the adapter produces the descriptor; `claw up` compiles it into the pod.

### What it prevents

Orphaned skill files that describe capabilities the service no longer has. Operators manually writing and maintaining skill markdown for every API. Services that join a pod as capability black boxes.

## The Two-Pass `claw up` Flow

The compilation pipeline runs in two phases because some artifacts depend on information that is only available after image inspection:

**Pass 1 -- Parse and Inspect:**
- Parse `claw-pod.yml` and resolve pod-level defaults
- Inspect service images and extract `claw.describe` descriptors
- Build the feed registry from descriptors
- Resolve cllama wiring (bearer tokens, proxy config, provider keys)

**Pass 2 -- Materialize:**
- Resolve feed subscriptions against the registry
- Generate per-agent `CLAWDAPUS.md` with inlined service descriptions
- Run driver `Materialize()` for each service (configs, personas, skill files)
- Emit `compose.generated.yml`

cllama wiring is resolved before materialization because drivers need the bearer token and proxy URL during config generation. Feed resolution happens after image inspection because the parser has no image knowledge -- descriptors only exist once images are inspected.

::: info Further Reading
These principles were formalized in [ADR-017: Pod-Level Defaults and Service Self-Description](https://github.com/mostlydev/clawdapus/blob/master/docs/decisions/017-pod-defaults-and-service-self-description.md). The ADR covers the full rationale, override semantics, and migration consequences.
:::
