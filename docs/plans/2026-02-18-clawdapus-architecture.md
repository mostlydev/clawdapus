# Clawdapus Architecture Plan

**Date:** 2026-02-18
**Status:** Draft — under review
**Source of truth:** `MANIFESTO.md`

---

## What We're Building

`claw` — a Go CLI that is to agent containers what `docker compose` is to service containers. It governs, deploys, and monitors fleets of AI agent bots as managed, untrusted workloads.

Clawdapus is not an agent framework. It is the infrastructure layer beneath agent frameworks — where deployment meets governance.

---

## Key Architecture Decisions

### 1. Hybrid Build / Runtime Split

Clawdapus operates in two distinct phases:

**Build phase** (`claw build`)
- Reads a `Clawfile` (extended Dockerfile syntax)
- Translates Claw-specific directives into standard Dockerfile primitives
- Emits an inspectable `Dockerfile.generated` as a build artifact
- Calls `docker build` on the generated file
- Output is a standard OCI image — runnable on any Docker host without `claw`

**Runtime phase** (`claw up`, `claw ps`, `claw down`, etc.)
- Reads a `claw-pod.yml` (extended docker-compose syntax)
- Parses `x-claw` extension blocks
- Wires surfaces, assembles skill maps, enforces cllama requirements
- Emits a clean `compose.generated.yml` (no `x-claw` keys)
- Shells out to `docker compose` for container lifecycle
- Maintains its own state layer on top for fleet governance

**Rationale:** Generated files (Dockerfile, compose) are build artifacts — inspectable, debuggable, but not hand-edited. Like compiled output, not source. This gives us full runtime control without forking Docker or Compose.

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
3. Emits a clean compose file without `x-claw` keys
4. Shells out to `docker compose` with the generated file

### 4. Docker SDK Strategy

- **Docker Go SDK** for runtime operations: start/stop containers, inspect state, stream logs, handle events
- **Shell out to `docker` CLI** for build operations (`docker build`, `docker buildx`) where SDK coverage lags
- No forking of Docker or Compose

---

## Repository Structure

Current `openclaw/` runtime code will be archived to `archive/openclaw-runtime/`. The repo then becomes the home of the `claw` CLI.

```
clawdapus/
├── MANIFESTO.md                  # Source of truth for vision and principles
├── docs/
│   └── plans/                    # Architecture and feature plans
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
│   │   └── emit.go               # Emit clean compose.yml
│   │
│   ├── surface/                  # Surface wiring and skill map assembly
│   │   ├── resolver.go           # Match Claw surface declarations to service expose blocks
│   │   ├── skillmap.go           # Assemble capability inventory per Claw
│   │   └── mcp.go                # Query MCP servers for tool listings
│   │
│   ├── runtime/                  # Docker SDK wrapper
│   │   ├── client.go             # Docker client initialization
│   │   ├── lifecycle.go          # Start, stop, restart containers
│   │   └── inspect.go            # Status, logs, events
│   │
│   └── build/                    # Build orchestration
│       ├── build.go              # Coordinate Clawfile → Dockerfile → docker build
│       └── worker.go             # Worker mode: spin up, snapshot
│
├── pkg/
│   └── types/                    # Shared public types (if needed for plugins/extensions)
│
├── examples/
│   ├── crypto-ops/               # Example claw-pod.yml + Clawfiles
│   └── simple/                   # Minimal single-Claw example
│
└── go.mod
```

### Repo Strategy: Single Repo (for now)

Keep everything in one repo until natural seams emerge. Candidates for future split:
- Base images (`FROM openclaw:latest`, `FROM nanobot:latest`) → separate `clawdapus-images` repo
- cllama module ecosystem → separate when third parties start publishing modules
- Persona registry client → separate when persona marketplace exists

---

## The Clawfile Directive Set

All directives translate to standard Dockerfile primitives at build time.

| Directive | Translates to | Purpose |
|-----------|--------------|---------|
| `CLAW_TYPE` | `LABEL claw.type=...` | Declares runner type |
| `AGENT` | `LABEL claw.agent.file=...` | Contract filename convention |
| `MODEL` | `LABEL claw.model.<slot>=...` | Named model slot bindings |
| `CLLAMA` | `LABEL claw.cllama=...` | Default judgment stack |
| `PERSONA` | `RUN claw-persona-fetch ...` | Fetch persona from registry into workspace |
| `CONFIGURE` | Injected into entrypoint | Init-time shell mutations |
| `INVOKE` | `RUN echo "..." >> /etc/cron.d/claw` | Cron schedule entries |
| `TRACK` | `RUN claw-track-install apt pip npm` | Install package manager wrappers |
| `ACT` | `RUN ...` (worker mode only) | Worker-mode setup commands |
| `SURFACE` | `LABEL claw.surface.<n>=...` | Consumed surface declarations |
| `PRIVILEGE` | `LABEL claw.privilege.<mode>=...` | Per-mode privilege config |

Standard Dockerfile directives (`FROM`, `RUN`, `COPY`, `ENV`, `ENTRYPOINT`, etc.) pass through unchanged.

---

## The claw-pod.yml Extension Schema

All Claw-specific config lives under `x-claw` at the appropriate scope.

**Pod-level (`x-claw` at top):**
```yaml
x-claw:
  pod: <name>
  master: <service-name>
```

**Service-level (`x-claw` under a service):**
```yaml
x-claw:
  agent: <path-to-contract-file>     # host path, bind-mounted read-only
  persona: <registry-ref>            # override Clawfile PERSONA at deploy time
  cllama: <stack-spec>               # override Clawfile CLLAMA at deploy time
  count: <n>                         # scale to N identical containers
  surfaces:                          # consumed surfaces (Claw side)
    - <uri>: <access-mode>
  expose:                            # declared surface (service side)
    protocol: mcp | rest | grpc
    port: <n>
  require_cllama:                    # mandatory judgment for any Claw calling this service
    - <policy-module>
  describe:                          # machine-readable self-description
    role: <string>
    inputs: [...]
    outputs: [...]
    capabilities: [...]              # for non-MCP services
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

## CLI Commands (Phase 1 scope)

```
claw build [path]              # Clawfile → Dockerfile → docker build
claw up [pod]                  # claw-pod.yml → compose.yml → docker compose up
claw down [pod]                # Stop and remove pod containers
claw ps [pod]                  # Status of all Claws in pod (with drift, cllama health)
claw logs <claw>               # Stream logs from a running Claw
claw skillmap <claw>           # Show assembled capability inventory
claw audit <claw> [--last Xh]  # Show cllama intervention history and drift events
claw recipe <claw> [--since Xd] # Show suggested recipe from mutation log
claw snapshot <claw> --as <ref> # Snapshot running Claw as new image
```

---

## Implementation Phases

### Phase 1 — Clawfile Parser + Build (start here)

**Goal:** `claw build` works end-to-end.

1. Parse a Clawfile using buildkit's Dockerfile parser as base
2. Identify and extract Claw-specific directives (unknown to standard parser)
3. Translate directives to Dockerfile primitives (LABELs, ENV, RUN)
4. Emit `Dockerfile.generated`
5. Shell out to `docker build`

**Success criteria:** A Clawfile with all directives builds to a runnable OCI image. Generated Dockerfile is valid and inspectable.

### Phase 2 — claw-pod.yml Parser + Runtime

**Goal:** `claw up` / `claw down` / `claw ps` work.

1. Parse `claw-pod.yml`, extract `x-claw` blocks
2. Handle `count` scaling (emit N services with deterministic names)
3. Emit clean `compose.generated.yml`
4. Wire agent bind mounts from `agent:` declarations
5. Shell out to `docker compose up`

**Success criteria:** A claw-pod.yml with mixed Claws and plain services starts correctly. Agent contracts are bind-mounted read-only.

### Phase 3 — Surfaces + Skill Maps

**Goal:** `claw skillmap` works. MCP services self-describe.

1. Resolve surface declarations against expose blocks
2. Query MCP servers for tool listings at pod init
3. Assemble per-Claw skill maps
4. Write skill maps to read-only skill mount inside each Claw container
5. Enforce `require_cllama` constraints at the surface boundary

**Success criteria:** `claw skillmap <claw>` shows correct capability inventory. Adding a service updates the skill map without code changes.

### Phase 4 — cllama Proxy

**Goal:** Outputs from Claws pass through cllama before reaching the world.

*Design TBD — requires decisions on proxy transport (HTTP interceptor? sidecar? SDK hook?).*

### Phase 5 — Drift Scoring + Master Claw

**Goal:** `claw ps` shows drift scores. `claw audit` shows intervention history.

*Design TBD — depends on cllama proxy implementation.*

### Phase 6 — Recipe Promotion + Worker Mode

**Goal:** `claw recipe` and `claw snapshot` work.

*Design TBD.*

---

## Open Questions

1. **cllama transport** — How does cllama intercept runner output? Options: HTTP proxy sidecar per Claw, SDK-level hook in the runner, network policy + transparent proxy. This is the hardest design problem in the system.

2. **Skill mount format** — What format do skill maps use? JSON schema? OpenAPI fragments? MCP tool definitions? Needs to be runner-agnostic.

3. **Persona registry** — Where do personas live? OCI registry (treat as image layers)? Custom registry? Git-based? The manifesto implies OCI but doesn't specify.

4. **Drift scoring** — Who runs the independent audit process? A sidecar per Claw? The master Claw? A separate Clawdapus daemon?

5. **claw-pod.yml vs Clawfile relationship** — The manifesto says claw-pod.yml overrides Clawfile defaults the same way compose `command:` overrides Dockerfile `CMD`. We need to specify exactly which directives are overridable at deploy time vs baked at build time.
