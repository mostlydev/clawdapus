---
outline: deep
---

# How It Fits Together

[Anatomy of a Claw](/guide/anatomy) zooms into a single agent — runner, contract, persona,
proxy. This page zooms out: how a whole pod is built, compiled, run, and governed, and how the
pieces relate. It is the map; the linked guide pages are the territory.

Everything below describes what ships today. Genuinely future work is called out explicitly in
[Roadmap](#what-ships-today-vs-roadmap). The authoritative shipped-vs-planned reconciliation —
capability by capability, with the code and ADR behind each — lives in
[`docs/PROJECT_STATE.md`](https://github.com/mostlydev/clawdapus/blob/master/docs/PROJECT_STATE.md).

## The lifecycle

Clawdapus is a compiler with a four-verb operator surface. A pod moves through the same path
every time:

```
Clawfile  ──build──►  OCI image
claw-pod.yml ─┐
              ├── claw up (compile) ──►  compose.generated.yml ──►  running, governed pod
infra images ─┘
                          claw pull / claw build / claw up / claw down
```

- **`claw pull`** fetches pinned runtime infra, registry-backed pod services, and refreshes
  built-in runner bases.
- **`claw build`** transpiles each Clawfile to a `Dockerfile.generated` and builds the OCI image.
- **`claw up`** is the compiler: it parses the pod, inspects images, resolves all wiring (feeds,
  skills, identity, surfaces, models, memory), and emits `compose.generated.yml` — the single
  source of truth that `docker compose` then runs.
- **`claw down`** tears the pod back down.

The compiler's invariants — compile-time not runtime, provider-owns/consumer-subscribes,
pod-level defaults with service overrides, one canonical descriptor, services self-describe —
are detailed in [Compilation Principles](/guide/compilation-principles) ([ADR-017](https://github.com/mostlydev/clawdapus/blob/master/docs/decisions/017-pod-defaults-and-service-self-description.md)).

## The planes

A Clawdapus pod is organized into planes that compose at compile time.

### Image plane — the Clawfile

An extended Dockerfile. Directives (`CLAW_TYPE`, `AGENT`, `MODEL`, `CLLAMA`, `HANDLE`, `SURFACE`,
`INVOKE`, `CONFIGURE`, `PERSONA`, `SKILL`) compile to standard Dockerfile primitives via image
labels. Any valid Dockerfile is a valid Clawfile. → [Clawfile](/guide/clawfile)

### Deployment plane — claw-pod.yml

An extended `docker-compose.yml`. Services declare `x-claw` blocks; shared config is declared
once under pod-level defaults (`*-defaults`) and inherited, with per-service overrides and `...`
spread to extend. Non-cognitive services (a Rails app, Postgres) are first-class pod members.
→ [Pod YAML](/guide/pod-yaml)

### Governance plane — cllama

An independent, intercepting proxy under the operator's exclusive control. It enforces
governance through **credential starvation**: the proxy holds the real provider API keys; each
agent gets only a bearer token, so every LLM call *must* pass through it
([ADR-007](https://github.com/mostlydev/clawdapus/blob/master/docs/decisions/007-llm-isolation-credential-starvation.md)).
Beyond isolation, cllama:

- **mediates compiled tools** — services declare callable tools in `claw.describe`; `claw up`
  compiles a deny-by-default per-agent catalog; cllama injects the schemas, executes
  `tool_call`s against the service, and loops until terminal text, transparently to the runner
  ([ADR-020](https://github.com/mostlydev/clawdapus/blob/master/docs/decisions/020-cllama-compiled-tool-mediation.md)) → [Managed Tools](/guide/tools);
- **emits normalized telemetry** — every request, response, intervention, and tool round is
  recorded, surfaced by **`claw audit`**
  ([ADR-014](https://github.com/mostlydev/clawdapus/blob/master/docs/decisions/014-telemetry-normalization-and-audit.md)).

→ [cllama Governance Proxy](/guide/cllama)

### Memory plane

Two durable surfaces plus an ambient recall layer, all surviving container restarts and runner
swaps ([ADR-018](https://github.com/mostlydev/clawdapus/blob/master/docs/decisions/018-session-history-and-memory-retention.md),
[ADR-021](https://github.com/mostlydev/clawdapus/blob/master/docs/decisions/021-memory-plane-and-pluggable-recall.md)):

- **Session history** — infrastructure-owned; cllama captures every successful turn to a durable
  ledger outside the runtime tree, with no runner cooperation.
- **Portable memory** — runner-owned scratch/notes at `/claw/memory`.
- **Ambient memory plane** — pluggable memory services (declared via `claw.describe`, compiled by
  `claw up`) derive durable state from the ledger; cllama recalls query-relevant context into
  each inference turn automatically. `claw memory backfill` / `claw memory forget` manage the
  ledger replay.

→ [Memory Plane](/guide/memory)

### Context & discovery plane

When an agent declares a `service://` surface, `claw up` reads that service's `claw.describe`
descriptor and projects its feeds, endpoints, auth, and skills into the agent's generated
`CLAWDAPUS.md` and feed manifests — deny-by-default, subscribe-by-name. Add a service to the
pod and the agent automatically receives the manual for it
([ADR-004](https://github.com/mostlydev/clawdapus/blob/master/docs/decisions/004-service-surface-skills.md),
[ADR-013](https://github.com/mostlydev/clawdapus/blob/master/docs/decisions/013-context-feeds.md)).
→ [Surfaces & Skills](/guide/surfaces-and-skills)

### Social plane — HANDLE

`HANDLE` declares an agent's chat-platform identity and broadcasts its handle ID to every
service in the pod as an environment variable, so a service can `@mention` a specific bot
without hardcoding IDs ([ADR-003](https://github.com/mostlydev/clawdapus/blob/master/docs/decisions/003-topology-simplification.md),
[ADR-016](https://github.com/mostlydev/clawdapus/blob/master/docs/decisions/016-canonical-social-identity-and-conformance-spikes.md)).
→ [Social Topology](/guide/social-topology)

### Fleet plane — claw-api & Master Claw

`x-claw.master` auto-injects a `claw-api` service and wires the designated agent with a scoped
bearer token and `CLAW_API_URL`, so an in-pod **Master Claw** can read fleet telemetry and act
through the API's authenticated, scope-checked surface
([ADR-012](https://github.com/mostlydev/clawdapus/blob/master/docs/decisions/012-master-claw-fleet-governance.md),
[ADR-015](https://github.com/mostlydev/clawdapus/blob/master/docs/decisions/015-claw-api-authentication-and-scoping.md)).
The proxy is the sensory organ (telemetry + hard limits); the Master Claw is the brain that
decides. How an organization defines drift and what the Master Claw does with the telemetry is
operator policy, not built-in behavior — see [Roadmap](#what-ships-today-vs-roadmap).

## What ships today vs. roadmap

| Capability | State |
|------------|-------|
| Four-verb operator surface; `claw up` compiler | Shipped |
| 7 runner drivers (OpenClaw, Hermes, NanoClaw, Nanobot, PicoClaw, MicroClaw, NullClaw) | Shipped → [Drivers](/guide/drivers) |
| cllama: credential starvation, compiled tool mediation, normalized telemetry, `claw audit` | Shipped |
| Memory plane: session history, portable memory, ambient recall | Shipped |
| Context feeds, channel-awareness + channel-memory retrieval | Shipped |
| Social topology / `HANDLE` broadcast | Shipped |
| `claw-api` + Master Claw wiring (`x-claw.master`) | Shipped |
| Persona materialization, contract composition (`INCLUDE`) | Shipped |
| **Drift scoring** (a numeric behavioral-drift metric) | **Not built in.** cllama emits the raw telemetry; computing a drift score is left to a swappable proxy implementation or an organization's Master Claw policy. There is no `drift_score` field in the reference proxy and no `DRIFT` column in `claw audit`. |
| **`TRACK` / `claw recipe` / `claw bake`** (recipe promotion) | **Planned.** Tracked mutation → human-gated promotion to the base image is designed but not yet implemented. |

## Where the decisions live

- **[Architecture Decision Records](https://github.com/mostlydev/clawdapus/tree/master/docs/decisions)** —
  the rationale behind each capability above (referenced inline by number).
- **[`docs/PROJECT_STATE.md`](https://github.com/mostlydev/clawdapus/blob/master/docs/PROJECT_STATE.md)** —
  the canonical shipped-vs-planned reconciliation and ADR status index.
- **[Changelog](/changelog)** — the release-by-release history.
- **[Manifesto](/manifesto)** — the philosophy and full vision.
