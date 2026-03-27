# ADR-017: Pod-Level Defaults and Service Self-Description

**Date:** 2026-03-22
**Status:** Accepted
**Depends on:** ADR-004 (Service Surface Skills), ADR-013 (Context Feeds)
**Implementation:** Milestones 1-3 complete (pod defaults with spread expansion, service descriptor contract, feed resolution pipeline). Milestone 5 (unified CLAWDAPUS.md) complete — surface/handle metadata inlined, separate skill files removed. Milestone 4 (RailsTrail bridge) and Milestone 6 (driver helpers) deferred. Plan: docs/plans/2026-03-22-pod-defaults-and-service-self-description.md

## Context

Clawdapus treats agents as untrusted workloads governed by operator-authored pod contracts. The pod file (`claw-pod.yml`) is the deployment source of truth — inspectable, diffable, deterministic. `claw up` compiles it into runtime artifacts.

Two problems have emerged as pods grow:

1. **Operator repetition.** Every claw in a pod repeats the same `cllama`, `cllama-env`, `surfaces`, `feeds`, and `skills` blocks even when they're identical. Tiverton-house (5 claws, 1 infra service, 1 governor) has four services sharing identical cllama, feeds, and surfaces stanzas. YAML anchors mitigate visual noise but not structural duplication.

2. **Service knowledge is in the wrong place.** Feeds are declared by consumers, not providers. A claw that wants market data must know the trading API's endpoint path, TTL, and auth scheme. Service skills are either a single extracted markdown file (`claw.skill.emit`) or a generic hostname+ports stub. Services cannot advertise their capabilities in a structured way that the pod compiler can consume.

Both problems share a root cause: the pod surface lacks inheritance and the compilation pipeline lacks a service descriptor contract.

## Decision

### 1. Pod-Level Defaults

Pod-level `x-claw` gains four new default fields alongside the existing `handles-defaults`:

- `cllama-defaults` — proxy type and provider env keys
- `surfaces-defaults` — surface list
- `feeds-defaults` — feed list
- `skills-defaults` — skill file list

Every claw-managed service inherits these unless it declares its own value for that field.

### 2. Replace-on-Declare with Spread

Override semantics follow one rule: **if a service declares a list field, it replaces the defaults entirely.**

To extend defaults rather than replace them, the service uses a `...` spread token in the list:

```yaml
skills:
  - ...                    # defaults expand here
  - ./policy/escalation.md # then this is appended
```

- No `...` → full replacement
- `...` present → defaults splice at that position
- At most one `...` per list

This is more expressive than any standard YAML merge convention while remaining unambiguous. `cllama-defaults.env` is a map and merges additively (service keys win on collision), matching the existing `handles-defaults` pattern.

### 3. Service Self-Description (`claw.describe`)

Services declare a structured JSON descriptor via image label:

```dockerfile
LABEL claw.describe=/app/.claw-describe.json
```

The descriptor advertises feeds provided, auth requirements, a human-readable description, and an optional skill file path. `claw up` extracts it from the image (same mechanism as `claw.skill.emit`) and compiles it into the pod manifest.

The descriptor does not contain a service name — deployment identity comes from the pod YAML, not the image. One image can back multiple services.

### 4. Provider-Owned Feeds with Consumer Subscription

Feeds move from consumer-declared to provider-declared. A service's descriptor advertises its feeds. Consumers subscribe by name:

```yaml
feeds: [market-context]
```

`claw up` resolves the name against a feed registry built from service descriptors. Explicit feed declarations (source + path + ttl) bypass the registry and work as before.

Resolution happens in `claw up` after image inspection, not in the parser. The parser stores unresolved feed names; `claw up` resolves them once the registry exists.

### 5. Unified Context Document

Generated surface and handle skill files are collapsed into CLAWDAPUS.md. One generated context document per agent instead of N files. Service descriptions retain their contractual weight — they're still injected into `AGENTS.generated.md` as guide content, just sourced from CLAWDAPUS.md sections rather than separate files.

Operator-authored skills (policy files, includes with `mode: reference`) remain as separate mounted files.

### 6. Compile-Time Only

All registration and description happens during `claw up`. No runtime self-registration endpoints. The generated compose file and runtime artifacts remain the single source of truth for what's deployed. This preserves the inspectable, diffable deployment model that is Clawdapus's core value proposition.

### 7. Dockerfile-Label Inspection for Build-Services

For services using local `build:` blocks without an existing image, `claw up` now inspects the configured Dockerfile for `claw.*` labels. This ensures that services sharing a single build context (e.g., a Rails app and its Sidekiq worker) can self-describe independently by pointing to different Dockerfiles, even before the images are built. This closes a compiler gap where multiple services sharing one context directory would collide on a default `.claw-describe.json`.

## Rationale

**Why not runtime self-registration?** Clawdapus's value is deterministic, auditable deployment. If services register at boot, the running state diverges from the pod file. The right version is image self-description compiled by `claw up`.

**Why replace-on-declare instead of always-merge?** List merging is inherently ambiguous (append? prepend? deduplicate by what?). Every system that attempts it (Helm, Kustomize, Ansible) ends up with surprising edge cases. Replace is the simplest default. The `...` spread provides controlled extension when needed.

**Why no `name` in the descriptor?** A single image can back multiple compose services. Tiverton-house uses one hermes base image for all traders. Binding the descriptor to a service name would break image reuse.

**Why two-phase feed resolution?** The parser has no image knowledge. Descriptors are extracted from images during `claw up`. Trying to resolve feed names in the parser would require passing image inspection state into the YAML parser, coupling two independent phases.

## Consequences

**Positive:**
- Pod files shrink dramatically. Tiverton-house's per-service `x-claw` blocks reduce to identity + overrides only.
- Services self-describe their feeds, auth, and capabilities. Consumers subscribe by name.
- One generated context document per agent instead of N redundant files.
- The `...` spread convention is simple and more expressive than any standard YAML merge.
- RailsTrail (and future framework adapters) can generate descriptors from introspection, closing the loop between app code and pod contracts.

**Negative:**
- Breaking change to pod YAML surface. Tiverton-house pod must be rewritten. (Acceptable — it's the only production pod.)
- Two-phase feed resolution adds a step to `claw up`. Unresolved feeds are a new error category.
- `claw.skill.emit` becomes redundant once `claw.describe` with `skill` field is live. Deprecation timeline TBD.
- The `...` spread is a custom convention. Operators must learn it. (Mitigated by simplicity — one token, one rule.)
