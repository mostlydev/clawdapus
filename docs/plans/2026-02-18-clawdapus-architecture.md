# Clawdapus Architecture Plan

**Date:** 2026-02-18
**Status:** v3 — post-deliberation consensus
**Source of truth:** `MANIFESTO.md`
**Reviews:** Grok (structural critique), Codex (architecture + suggestions), operator (cllama clarification)
**Deliberation:** 3-agent talking stick (alpha/Codex, beta/Claude, gamma/Grok) — arch-review room, 3 rounds, consensus reached

---

## What We're Building

`claw` — a Go CLI that is to agent containers what `docker compose` is to service containers. It governs, deploys, and monitors fleets of AI agent bots as managed, untrusted workloads.

Clawdapus is not an agent framework. It is the infrastructure layer beneath agent frameworks — where deployment meets governance.

---

## Invariants

Invariants are stated as goals from the start. Each is promoted from SHOULD to MUST when its enforcement mechanism ships in the corresponding phase. Until enforcement exists, the invariant is documented but not claimed as a guarantee.

| Invariant | Enforcement mechanism | Promoted to MUST in |
|-----------|----------------------|---------------------|
| No contract → no start | File existence check on `agent:` host path before compose emit | Phase 2A |
| No cllama decision on egress → deny | Sidecar exists and proxies all LLM traffic; no direct egress allowed | Phase 2B |
| Missing required policy module → deny service call | Sidecar checks policy modules before routing tool calls to services | Phase 3 |
| Purpose is immutable from inside | Contract bind mount is always `:ro` | Phase 2A |
| One lifecycle authority | `docker compose` is sole lifecycle authority; Docker SDK is read-only | Phase 2A |

See also: [ADR-001: cllama Transport](../decisions/001-cllama-transport.md), [ADR-002: Runtime Authority](../decisions/002-runtime-authority.md)

---

## Key Architecture Decisions

### 1. Hybrid Build / Runtime Split

**Build phase** (`claw build`)
- Reads a `Clawfile` (extended Dockerfile syntax)
- Translates Claw-specific directives into standard Dockerfile primitives
- Emits an inspectable `Dockerfile.generated` as a build artifact
- Calls `docker build` on the generated file
- Output is a standard OCI image — runnable on any Docker host without `claw`

**Runtime phase** (`claw up`, `claw ps`, `claw down`, etc.)
- Reads a `claw-pod.yml` (extended docker-compose syntax)
- Parses `x-claw` extension blocks
- Wires surfaces, assembles skill maps, injects cllama sidecars
- Emits a clean `compose.generated.yml` (no `x-claw` keys)
- Shells out to `docker compose` for **all** container lifecycle operations
- Uses Docker SDK **only** for read operations: inspect, logs, events

**Rationale:** Single lifecycle authority eliminates state drift between compose and SDK. Generated files are build artifacts — inspectable, debuggable, but not hand-edited.

### 2. Language: Go

- Single binary distribution — `claw` installs like `kubectl`, not `npm install -g`
- Docker's Go SDK (`github.com/docker/docker/client`) is first-party
- Dockerfile parsing via `github.com/moby/buildkit` — battle-tested parser we extend
- Entire container ecosystem tooling (compose, kubectl, helm, containerd) is Go
- No runtime dependency, no cold start latency

### 3. Compose Strategy

`claw-pod.yml` is a valid `docker-compose.yml`. The `x-claw` extension namespace is already ignored by Docker natively. Clawdapus:
1. Parses `claw-pod.yml` using a Go YAML library
2. Processes `x-claw` blocks (surfaces, cllama config, count scaling, skill maps)
3. Injects cllama sidecar containers for each Claw service
4. Emits a clean compose file without `x-claw` keys
5. Shells out to `docker compose` with the generated file

### 4. cllama as Bidirectional LLM Proxy

**This is the core insight and the hardest design problem.** cllama is not just an output filter — it is a bidirectional proxy that sits between the runner and the LLM provider.

**Outbound (runner → LLM):** Intercepts prompts before they reach the LLM. Prevents the model from seeing content that violates policy. Gates what the runner is allowed to *ask* — role gating, thought gating, tool-use gating.

**Inbound (LLM → runner):** Intercepts model responses before they reach the runner. Adjusts, rewrites, or drops responses that violate policy, drift from purpose, or fail tone requirements. Can engage in its own conversation with the LLM to arrive at a compliant response before passing it to the runner's stream of thought.

**The runner never knows.** It thinks it's talking directly to the LLM. cllama is transparent — same API shape in, same API shape out.

**Transport: sidecar per Claw (default mode).**
- Each Claw gets a `cllama-sidecar` container, injected automatically by `claw up`
- Sidecar exposes an OpenAI-compatible API endpoint on a private network
- Runner's LLM base URL is rewritten to point at the sidecar (via ENV injection)
- Sidecar applies the cllama pipeline: purpose → policy → tone → obfuscation
- Sidecar holds real API keys — runner never sees provider keys
- Sidecar also intercepts tool calls and enforces `require_cllama` constraints

**Dual modes (deliberation consensus):**
- **Proxy mode (default):** HTTP sidecar. Works with any runner that makes HTTP calls to an LLM endpoint. No runner integration needed. Built first.
- **Adapter mode (future):** SDK-level hook for runners using local models or embedded clients that don't go through an HTTP base URL. Requires runner-specific integration. Documented as a known gap; not built until a concrete runner needs it.

See [ADR-001: cllama Transport](../decisions/001-cllama-transport.md) for full decision record.

### 5. Persona as Runtime Mount (not baked into image)

- At build time: `PERSONA` compiles to `LABEL claw.persona.default=<registry-ref>` — declares the default, does not fetch
- At runtime: `claw up` resolves the persona ref, fetches the OCI artifact via `oras`, bind-mounts into container
- Swap a persona without rebuilding the image

**Rationale:** Persona is content, not infrastructure. Independent layer versioning.

### 6. Security Defaults

Every Claw container gets these by default (overridable via `PRIVILEGE`):

- `--read-only` rootfs except `/workspace` and `/claw/tmp`
- User namespace with `claw-user:1000`
- Runner's LLM keys are never real provider keys — always sidecar-local endpoints
- Network policy: only cllama sidecar outbound unless explicitly allowed via surfaces

---

## Repository Structure

```
clawdapus/
├── MANIFESTO.md                  # Source of truth for vision and principles
├── docs/
│   ├── plans/                    # Architecture and feature plans
│   └── decisions/                # Architecture Decision Records (ADRs)
├── archive/
│   └── openclaw-runtime/         # Former openclaw Docker runtime (reference)
│
├── cmd/
│   └── claw/
│       └── main.go               # CLI entrypoint
│
├── internal/
│   ├── clawfile/                 # Clawfile parser and Dockerfile emitter
│   │   ├── parser.go             # Parse Clawfile, identify Claw directives
│   │   ├── directives.go         # Directive types: CLAW_TYPE, AGENT, CLLAMA, etc.
│   │   └── emit.go               # Emit standard Dockerfile from parsed AST
│   │
│   ├── pod/                      # claw-pod.yml parser and compose emitter
│   │   ├── parser.go             # Parse claw-pod.yml, extract x-claw blocks
│   │   ├── types.go              # Pod, Service, Surface, ClawConfig types
│   │   ├── identity.go           # Claw naming, ordinal identity, rescale semantics
│   │   └── emit.go               # Emit clean compose.yml (with cllama sidecars injected)
│   │
│   ├── cllama/                   # cllama sidecar orchestration
│   │   ├── sidecar.go            # Sidecar container config generation
│   │   ├── policy.go             # Policy module resolution and layering
│   │   └── proxy.go              # LLM API proxy logic (bidirectional interception)
│   │
│   ├── surface/                  # Surface wiring and skill map assembly
│   │   ├── resolver.go           # Match Claw surface declarations to service expose blocks
│   │   ├── skillmap.go           # Assemble capability inventory per Claw
│   │   └── mcp.go                # Query MCP servers for tool listings
│   │
│   ├── persona/                  # Persona fetching and mounting
│   │   ├── registry.go           # OCI artifact resolution via oras
│   │   └── mount.go              # Bind mount generation for compose
│   │
│   ├── runtime/                  # Docker SDK wrapper (read-only operations only)
│   │   ├── client.go             # Docker client initialization
│   │   └── inspect.go            # Status, logs, events (never lifecycle)
│   │
│   └── build/                    # Build orchestration
│       ├── build.go              # Coordinate Clawfile → Dockerfile → docker build
│       └── worker.go             # Worker mode: spin up, snapshot
│
├── examples/
│   ├── crypto-ops/               # Full pod example: Clawfiles + claw-pod.yml
│   ├── simple/                   # Minimal single-Claw example
│   └── one-shot/                 # Minimal Claude Code one-shot Clawfile
│
└── go.mod
```

### Repo Strategy: Single Repo (for now)

Keep everything in one repo until natural seams emerge. Candidates for future split:
- Base images → `clawdapus-images`
- cllama sidecar image → `cllama` (when it stabilizes)
- Persona registry client → when marketplace exists

---

## The Clawfile Directive Set

All directives translate to standard Dockerfile primitives at build time.

| Directive | Translates to | Purpose |
|-----------|--------------|---------|
| `CLAW_TYPE` | `LABEL claw.type=...` | Declares runner type |
| `AGENT` | `LABEL claw.agent.file=...` | Contract filename convention |
| `MODEL` | `LABEL claw.model.<slot>=...` | Named model slot bindings |
| `CLLAMA` | `LABEL claw.cllama.default=...` | Default judgment stack (overridable at deploy) |
| `PERSONA` | `LABEL claw.persona.default=...` | Default persona ref (fetched and mounted at runtime, not baked) |
| `CONFIGURE` | Entrypoint wrapper script (`/claw/configure.sh`) | Shell mutations run at container init before the runner starts |
| `INVOKE` | `RUN echo "..." >> /etc/cron.d/claw` | Cron schedule entries |
| `TRACK` | `RUN claw-track-install apt pip npm` | Install package manager wrappers |
| `ACT` | `RUN ...` (worker mode only) | Worker-mode setup commands |
| `SURFACE` | `LABEL claw.surface.<n>=...` | Consumed surface declarations |
| `PRIVILEGE` | `LABEL claw.privilege.<mode>=...` | Per-mode privilege config |

Standard Dockerfile directives (`FROM`, `RUN`, `COPY`, `ENV`, `ENTRYPOINT`, etc.) pass through unchanged.

### CONFIGURE clarification

`CONFIGURE` directives are **not** build-time `RUN` commands. They compile into an entrypoint wrapper script (`/claw/configure.sh`) that runs at container startup, before the runner starts. This is for init-time mutations against the base image defaults — things that need to happen fresh on every boot, not baked into the image.

### Build-time vs deploy-time overridability

| Directive | Baked at build | Overridable in claw-pod.yml |
|-----------|:-:|:-:|
| `CLAW_TYPE` | yes | no |
| `AGENT` | yes (default filename) | yes (`agent:` path) |
| `MODEL` | yes (default slots) | yes |
| `CLLAMA` | yes (default stack) | yes |
| `PERSONA` | yes (default ref) | yes |
| `INVOKE` | yes (default schedule) | yes |
| `CONFIGURE` | yes | no (runs from image) |
| `TRACK` | yes | no |
| `ACT` | yes | no |
| `SURFACE` | yes (default declarations) | yes (additive) |
| `PRIVILEGE` | yes (default modes) | yes |

---

## The claw-pod.yml Extension Schema

All Claw-specific config lives under `x-claw` at the appropriate scope.

**Pod-level:**
```yaml
x-claw:
  pod: <name>
  master: <service-name>
```

**Service-level (Claw):**
```yaml
x-claw:
  agent: <path-to-contract-file>     # host path, bind-mounted read-only
  persona: <registry-ref>            # override Clawfile default
  cllama: <stack-spec>               # override Clawfile default
  count: <n>                         # scale to N identical containers
  surfaces:                          # consumed surfaces
    - <uri>: <access-mode>
  describe:
    role: <string>
    inputs: [...]
    outputs: [...]
```

**Service-level (plain container):**
```yaml
x-claw:
  expose:
    protocol: mcp | rest | grpc
    port: <n>
  require_cllama:
    - <policy-module>
  describe:
    role: <string>
    capabilities: [...]
```

**Network/volume-level:**
```yaml
networks:
  internal:
    x-claw:
      visibility: pod-only | egress-only | public

volumes:
  shared-cache:
    x-claw:
      access:
        - <service-pattern>: <access-mode>
```

---

## Claw Identity and Scaling

When `count: N` is specified, Clawdapus generates N named instances with stable ordinal identity:

```
crypto-crusher-0, crypto-crusher-1, crypto-crusher-2
```

**Naming:** `<service>-<ordinal>`, zero-indexed. Deterministic and stable across restarts.

**Rescaling up:** New ordinals are appended. Existing containers are untouched. `count: 3 → count: 5` adds `-3` and `-4`.

**Rescaling down:** Highest ordinals are removed first. `count: 5 → count: 3` removes `-4` and `-3`.

**Drift history:** Drift scores and audit logs are keyed by ordinal name. Removing and re-adding an ordinal does not inherit prior history — it starts fresh.

**Command targeting:** CLI commands accept either service name (applies to all) or ordinal name (applies to one):
```
claw logs crypto-crusher        # all 3
claw logs crypto-crusher-2      # just one
claw audit crypto-crusher-2     # just one
```

---

## CLI Commands

```
claw build [path]              # Clawfile → Dockerfile → docker build
claw inspect <image>           # Show resolved Claw labels from built image
claw up [pod]                  # claw-pod.yml → compose.yml → docker compose up
claw down [pod]                # Stop and remove pod containers
claw ps [pod]                  # Status with drift scores and cllama health
claw logs <claw>               # Stream logs from a running Claw
claw skillmap <claw>           # Show assembled capability inventory
claw audit <claw> [--last Xh]  # cllama intervention history and drift events
claw recipe <claw> [--since Xd] # Suggested recipe from mutation log
claw bake <claw> --from-recipe  # Apply recipe to rebuild image
claw snapshot <claw> --as <ref> # Snapshot running Claw as new image
claw doctor                    # Check Docker version, BuildKit, compose plugin, etc.
```

---

## Implementation Phases

### Phase 1 — Clawfile Parser + Build

**Goal:** `claw build`, `claw inspect`, and `claw doctor` work end-to-end.

**Invariants gated:** None (build-only phase, no runtime invariants yet).

1. `claw doctor` — check Docker, BuildKit, compose plugin versions
2. Parse a Clawfile using buildkit's Dockerfile parser as base
3. Identify and extract Claw-specific directives
4. Translate directives to Dockerfile primitives (LABELs, ENV, entrypoint wrapper for CONFIGURE)
5. Emit `Dockerfile.generated`
6. Shell out to `docker build`
7. `claw inspect` reads labels from built image and displays resolved config

**Validation:** Create four test Clawfiles before writing the parser:
- OpenClaw (heavyweight, AGENTS.md contract)
- Nanobot (lightweight, config.json contract)
- Claude Code (one-shot, CLAUDE.md contract)
- Raw Python script (custom runner, custom contract)

Verify every directive translates cleanly for all four. Especially:
- `INVOKE` semantics differ for internal-scheduling vs external-cron runners
- `AGENT` bind-mount path varies per `CLAW_TYPE`
- `CONFIGURE` entrypoint wrapper must not conflict with runner's own entrypoint

**Success criteria:** All four test Clawfiles build to runnable OCI images. `claw inspect` shows correct labels. Generated Dockerfile is valid and inspectable.

### Phase 2A — Pod Runtime (parallel with 2B)

**Goal:** `claw up` / `claw down` / `claw ps` work with basic container lifecycle.

**Invariants promoted to MUST:**
- No contract → no start (file existence check)
- Purpose is immutable from inside (`:ro` bind mount)
- One lifecycle authority (compose-only)

1. Parse `claw-pod.yml`, extract `x-claw` blocks
2. Handle `count` scaling with ordinal identity
3. Validate contract paths exist on host (**fail-closed: no contract → refuse to emit compose**)
4. Wire agent bind mounts as read-only
5. Emit clean `compose.generated.yml`
6. Shell out to `docker compose up`

**Success criteria:** A claw-pod.yml starts correctly. Missing contract blocks startup with a clear error. `claw ps` shows container status.

### Phase 2B — cllama Pass-Through Sidecar (parallel with 2A)

**Goal:** Every Claw's LLM traffic routes through a transparent sidecar.

**Invariants promoted to MUST:**
- No cllama decision on egress → deny (sidecar exists and proxies)

1. Build a minimal cllama sidecar image (Go binary, OpenAI-compatible API proxy)
2. For each Claw service, inject a `<name>-cllama` sidecar into the generated compose
3. Rewrite runner's LLM base URL env vars to point at sidecar
4. Sidecar holds real provider API keys; runner never sees them
5. **Pass-through mode only** — no policy evaluation, just transparent proxy + request/response logging

**Success criteria:** Each Claw's LLM calls route through its sidecar. Sidecar logs all requests. Direct LLM egress is blocked.

### Phase 3 — Surfaces + Skill Maps

**Goal:** `claw skillmap` works. `require_cllama` is enforced.

**Invariants promoted to MUST:**
- Missing required policy module → deny service call

1. Resolve surface declarations against expose blocks
2. Query MCP servers for tool listings at pod init
3. Assemble per-Claw skill maps
4. Write skill maps to read-only skill mount at `/claw/skillmap.json`
5. Enforce `require_cllama` — sidecar checks policy modules before routing tool calls to services

**Success criteria:** `claw skillmap <claw>` shows correct capability inventory. `require_cllama` blocks tool calls without the right policy. Adding a service updates the skill map.

### Phase 4 — Full cllama Policy Pipeline

**Goal:** Complete bidirectional interception with all pipeline stages.

1. Purpose evaluation — does this prompt/response serve the operator's goal?
2. Policy enforcement — hard rails (financial advice, PII, legal exposure)
3. Tone shaping — voice consistency on responses
4. Obfuscation — timing jitter, vocabulary rotation on outputs
5. cllama can engage in its own conversation with the LLM to rework non-compliant responses before passing them to the runner
6. Tool call interception — gate dangerous tool invocations

**Success criteria:** `claw audit` shows intervention history. Pipeline stages are independently configurable and swappable.

### Phase 5 — Drift Scoring + Fleet Governance

**Goal:** `claw ps` shows drift scores. Master Claw operates.

1. Independent drift scoring process (sidecar or Clawdapus daemon)
2. Drift thresholds → capability restriction → quarantine escalation
3. Master Claw contract and lifecycle management
4. `claw audit` shows full intervention + drift history

### Phase 6 — Recipe Promotion + Worker Mode

**Goal:** `claw recipe`, `claw bake`, `claw snapshot` work.

1. TRACK wrapper scripts log mutations inside running containers
2. `claw recipe` reads mutation logs and generates promotion suggestions
3. `claw bake` applies recipe to rebuild image
4. Worker mode: `claw up --mode worker` → interact → `claw snapshot`

---

## Open Questions

1. **Skill mount format** — JSON with a runner-agnostic schema? MCP tool definition format? OpenAPI fragments? Leaning toward a simple JSON format at `/claw/skillmap.json` that each runner adapter knows how to read.

2. **Persona registry** — OCI artifacts via `oras` is the leading option. Need to define the artifact structure (manifest, memory, knowledge, style fingerprint as separate layers? or single tarball?).

3. **cllama sidecar image** — What base image? What LLM client library? Needs to be extremely lightweight since every Claw gets one. Likely a small Go binary.

4. **Versioned schemas** — Clawfile directives, x-claw schema, and skill-map format should be independently versioned with compatibility rules. When to formalize this?
