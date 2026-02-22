# The Clawdapus Claw

## Declarative Bot Instantiation
### A Manifesto for Containerized Agent Infrastructure

---

## PART 1: THE PHILOSOPHY

### I. The Thesis

Every agent framework in existence answers the same question: how do I make agents collaborate on tasks? Swarm coordinates handoffs. CrewAI assigns roles. LangGraph builds execution graphs. AgentStack scaffolds projects. They are all application-layer orchestration systems built on a shared assumption: the agent is a trusted process doing work for the operator.

Clawdapus starts from the opposite premise. The agent is an untrusted workload.

It is a container that can think, and like any container, it must be reproducible, inspectable, diffable, and killable. Its purpose is not its own to define. Its schedule is not its own to set. But within those boundaries, it is alive. It can grow. It can install tools, build scripts, modify its workspace, and adapt to its environment. It is a managed organism, not a jailed process.

This is infrastructure-layer containment for cognitive workloads. Clawdapus does not replace agent runners any more than Docker replaces Flask. It operates beneath them — the layer where deployment meets governance.

An artificial brain built this way is a cyborg. Part thinking, part API. Part reasoning model, part database query, part cron job, part message queue. The form is open-ended, but we need a language to describe how the parts relate to one another. That is what Clawdapus provides: opinionated cognitive architecture. Not opinions about how agents should think — opinions about how they should be structured, deployed, governed, and composed into systems that are greater than their parts.

Swarm is for agents that work *for* you. Clawdapus is for bots that work *as* you. Different trust model. Different stack.

### II. What This Is Not

Clawdapus is **not an agent framework**. It does not define how agents reason, plan, or execute code.
Clawdapus is **not a bot-building tool**. It helps you deploy, govern, monitor, and evolve bots that already exist.

Clawdapus is infrastructure for bots the way Docker is infrastructure for applications. The layer below the framework. The layer above the operating system.

### III. Core Principles

1. **Purpose Is Sacred** — The bot reads its purpose on every invocation but cannot alter the constraint document itself. If the contract is not present at boot, the container does not start.
2. **The Workspace Is Alive** — The bot can install packages and write scripts. Ad hoc evolution is tracked and becomes permanent infrastructure through a human-gated recipe promotion process.
3. **Configuration Is Code** — Every configuration is a documented, diffable deviation from its base image's defaults.
4. **Drift Is Quantifiable** — We do not trust a bot's self-report. An independent process audits outputs against the contract to score drift.
5. **Surfaces Are Declared** — Bots communicate through shared surfaces (volumes, APIs). Surfaces serve two audiences: operators get topology visibility, bots get capability discovery. 
6. **Claws Are Users** — A Claw authenticates to services with standard credentials (environment variables). Clawdapus does not enforce access control on third-party APIs; the service's own auth governs access.
7. **Compute Is a Privilege** — Every cognitive cycle is an authorized expenditure. The operator assigns models and schedules; the proxy enforces budgets and rate limits. The bot does not choose its own budget.
8. **Think Twice, Act Once** — A reasoning model cannot be its own judge. Prompt-level guardrails are part of the same cognitive process they are trying to constrain. Governance must be executed by a separate, independent process.

---

## PART 2: THE ARCHITECTURE (THE ANATOMY)

### IV. The Anatomy of a Claw

A running Claw bifurcates cognition into two independent layers: internal execution and external governance. Every Claw is built from four mandatory or optional components:

1. **The Runner (Internal Execution)** — The application code (e.g. OpenClaw, Nanobot, Claude Code, or a custom python script) that implements the agent loop: receive input, assemble context, call a model, execute tools.
2. **The Behavioral Contract (The Law)** — A read-only file defining the bot's purpose and strict rules.
3. **The Persona (The History)** — A mutable workspace of identity, memory, knowledge, style, and accumulated interaction history.
4. **cllama (The Governance Proxy)** — *(Optional)* An independent, intercepting proxy that governs what the runner is allowed to ask the LLM, and amends what the LLM is allowed to reply.

These layers are independently versioned, independently deployable, and independently auditable. Swap the runner without touching the persona. Swap the persona without touching the contract.

### V. The Behavioral Contract

The behavioral contract is the single most important file in the architecture. It is the bot's purpose, defined by the operator, delivered as a read-only bind mount from the host. Even if the container is fully compromised (root access), the contract remains untouchable.

**Contract Composition:** 
Operators can provide a single, monolithic file (like `AGENTS.md` or `CLAUDE.md`), or they can use the `INCLUDE` directive in the pod manifest to modularize the contract. Inclusions have semantic modes:
- **`enforce`**: Hard constraints and mandatory rules (e.g., risk-limits). These are deterministically concatenated into the final read-only contract.
- **`guide`**: Strong recommendations and procedural workflows. Also inlined into the contract.
- **`reference`**: Informational context and playbooks. These are *not* inlined, saving tokens; they are mounted as read-only files in the runner's skill directory.

### VI. Personas: Downloadable Identity

A persona is a complete, portable, forkable workspace package that encapsulates everything a bot needs to be someone. Not just a name and a system prompt — a full identity with memory, interaction history, stylistic fingerprint, and knowledge base.

Personas are the content layer. They grow during operation and can be snapshotted. Snapshot a running Claw that has accumulated memory over months, fork it, and deploy the fork with a different behavioral contract. You now have two bots that share history and knowledge but differ in purpose.

### VII. cllama: The Standardized Governance Proxy

cllama is an open standard for a context-aware, bidirectional proxy — a separate process, running its own model, under the operator's exclusive control. Any OpenAI-compatible proxy image that can consume Clawdapus context can act as the governance layer.

**The Pipeline:** 
It sits between the runner and the LLM provider. Outbound, it evaluates prompts before the LLM sees them to prevent policy violations. Inbound, it evaluates responses before the runner sees them, dropping output that drifts from purpose. The runner never knows the proxy exists; it thinks it's talking directly to the model.

**Intelligent Authorization & Compute Metering:** 
Clawdapus injects the agent's identity (`CLAW_ID`) and compiled behavioral contract directly into the proxy at startup. Because it is context-aware, the proxy acts as a dynamic governance enforcement point. It can drop specific tool calls based on the agent's identity. Furthermore, because it acts as the central router, it enforces hard compute budgets, silently downgrading a requested model (e.g., from `gpt-4o` to `claude-3-haiku`) or applying rate limits (`429s`) without the agent knowing its compute was throttled.

**Enforcement via Credential Starvation:** 
Isolation is achieved by strictly separating secrets. The proxy holds the real LLM provider API keys. The agent container is provisioned with a unique Bearer Token. Because the agent lacks the credentials to call providers directly, all successful inference *must* pass through the proxy, even if a malicious prompt tricks the agent into ignoring its configured base URL.

**Transport (Shared Pod Proxy):** 
By default, Clawdapus deploys a single shared governance proxy per pod. It uses the Bearer Token to resolve which agent is making the request, dynamically loads that agent's specific contract, and applies the policy. This drastically reduces resource overhead for multi-agent fleets while enabling pod-wide compute budgeting.

---

## PART 3: THE ORCHESTRATION

### VIII. The Clawfile: Building the Image

The Clawfile is an extended Dockerfile. Any valid Dockerfile is a valid Clawfile. `claw build` is a preprocessor — it translates extended directives into standard Dockerfile primitives (`LABEL`, `ENV`, `RUN`), and calls `docker build` on the result. The output is a standard OCI container image. 

**Core Directives:**
- **`CLAW_TYPE`** — Declares the runner family (e.g., `openclaw`, `nanobot`) and selects the runtime driver that translates directives into runner-specific config.
- **`AGENT`** — Names the behavioral contract file to be bind-mounted.
- **`PERSONA`** — Imports a downloadable persona workspace from a registry.
- **`MODEL`** — Binds named model slots (e.g., `primary`, `summarizer`) to providers and models.
- **`CLLAMA`** — Declares the default policy stack, provider, and model for the sidecar proxy.
- **`INVOKE`** — Invocation schedules, managed via cron.
- **`CONFIGURE`** — Shell commands that run at container init to mutate base defaults (e.g., using `jq` to alter runner JSON configs).
- **`PRIVILEGE`** — Drops container privileges to standard users, or locks down the filesystem/network.

**Self-Modification and Recipe Promotion (`TRACK` & `ACT`)**
The bot runs. It installs things. It `pip installs`. That's how real work gets done. The `TRACK` directive wraps package managers (apt, pip, npm) to log every mutation. `ACT` directives are used to trigger installations during a worker-mode setup.

These logs become a redeployment recipe. The operator reviews the recipe (`claw recipe`) and decides what to promote to the permanent base image (`claw bake`). Tracked mutation is evolution; untracked mutation is drift.

### IX. The claw-pod.yml: Running the Fleet

Just as the Clawfile extends the Dockerfile, `claw-pod.yml` extends `docker-compose.yml`. Extended keys live under an `x-claw` namespace, which Docker naturally ignores. The Clawfile bakes defaults into the image; the `claw-pod.yml` overrides them per-deployment.

A pod is a mixed cluster of cognitive and non-cognitive services. Regular API containers (like a Rails app or a Postgres DB) participate as first-class pod members alongside the agents.

**Surfaces:** 
Declared under `x-claw: surfaces`, these define the infrastructure boundaries the agent can reach:
- `volume://` (Shared read/write cache)
- `host://` (Bind mounts)
- `service://` (Pod-internal APIs and MCP servers)

### X. Context & Discovery

An agent is useless if it doesn't know what it can do or who it is. Clawdapus solves this through dynamic context injection.

**The CLAWDAPUS.md Map:** 
Every Claw receives a `CLAWDAPUS.md` file injected into its workspace. This is the infrastructure layer's letter to the agent. It lists the agent's identity, its allowed surfaces, and an index of available skill files. 

**Skills Discovery:** 
When an agent declares a `service://` surface, Clawdapus queries that service to find out what it does. Services self-describe via MCP tool listings, OpenAPI specs, or an explicit `claw.skill.emit` label. Clawdapus generates a markdown "skill" file explaining how to use the service and mounts it into the agent's skill directory. Add a service to the pod, and the agent automatically receives the manual on how to use it.

**Social Topology (`HANDLE`):** 
Agents have identities on chat platforms. The `HANDLE` directive declares a bot's platform identity (e.g., `HANDLE discord`). Clawdapus translates this into the runner's native configuration (enabling the Discord plugin). Crucially, it also broadcasts the agent's Handle ID as an environment variable to *every* service in the pod. This enables the "Leviathan Pattern": a non-AI API service can read `CLAW_HANDLE_CRYPTO_CRUSHER_DISCORD_ID` and dynamically construct an `@mention` to alert a specific bot in a chat channel without hardcoding IDs.

---

## PART 4: FLEET OPERATIONS

### XI. Drift and Monitoring

Every Claw has a drift score. Drift scoring is not self-reported — an independent process examines outputs, compares them against the behavioral contract and the proxy's interventions, and generates a score.

When the `cllama` proxy intercepts a request, drops a tool call, or amends a response, it emits a structured log. These telemetry logs (`claw audit`) provide a verifiable history of exactly what the bot *tried* to do versus what it was *allowed* to do. 

Low drift: continue normally. Moderate drift: restrict capabilities. High drift: quarantine and alert the operator.

### XII. The Master Claw and Hub-and-Spoke Governance

Clawdapus is designed for autonomous fleet governance. The operator writes the Clawfile and sets the budgets, but day-to-day oversight can be delegated to a **Master Claw** (The "Top Octopus"). 

The Master Claw is an autonomous AI governor managing a fleet of subordinate agents. 

- **The Governance Proxy is its Sensory Organ:** The shared `cllama` proxy is a passive firewall. It enforces the hard rules (rate limits, budgets) and emits the telemetry logs. It does not "think" about fleet management.
- **The Master Claw is the Brain:** The Master Claw reads the telemetry emitted by the proxy. If a proxy reports that a subordinate agent has a high drift score or is burning through its budget, the Master Claw makes an executive decision to autonomously quarantine the agent, shift budgets, or promote a recipe.

In large enterprise deployments, this forms a **Hub-and-Spoke Governance Model**. Multiple pods across different infrastructure zones run their own local `cllama` proxies acting as firewalls. Sitting above them all is a single Master Claw, continuously ingesting telemetry from all those proxies, dynamically managing the entire neural fleet autonomously.

---

## PART 5: APPENDICES

### XIII. Reasoning (FAQ)

**Why extend Docker?** The hard problems of containerization are solved. Extending Docker inherits decades of infrastructure investment, layer caching, and CI/CD compatibility.
**Why do Clawfiles compile to Dockerfiles?** Because no custom build engine survives contact with production. `claw build` is a preprocessor. The output is a standard Dockerfile. `docker build` does the rest.
**Why is the behavioral contract a bind mount?** Purpose changes faster than infrastructure. The bind mount means the operator changes purpose at the speed of editing a text file, and it keeps the purpose outside the blast radius of a compromised container.
**Why are Claws users, not privileged processes?** Every service already has an authorization model. Duplicating it in the infrastructure layer is fragile. Give the Claw credentials via standard env vars, and let the service's own auth decide what those credentials allow.
**Why are lightweight runners first-class?** Because governance is not proportional to complexity. A 400-line Python script with brokerage API access needs the exact same purpose contract and governance proxy as a massive agent OS.

### XIV. Implementation

Architecture decisions and implementation plans live alongside this manifesto:

- [Architecture Plan](docs/plans/2026-02-18-clawdapus-architecture.md) — phased implementation, invariants, CLI surface
- [ADR-001: cllama Transport](docs/decisions/001-cllama-transport.md) — HTTP proxy as bidirectional LLM interceptor
- [ADR-002: Runtime Authority](docs/decisions/002-runtime-authority.md) — compose-only lifecycle, SDK read-only
- [ADR-003: Topology Simplification](docs/decisions/003-topology-simplification.md) — moving channel identity to HANDLE
- [ADR-004: Service Surface Skills](docs/decisions/004-service-surface-skills.md) — `claw.skill.emit` and fallback generation
- [ADR-006: INVOKE Scheduling](docs/decisions/006-invoke-scheduling.md) — native runner scheduling over system cron
- [ADR-007: Credential Starvation](docs/decisions/007-llm-isolation-credential-starvation.md) — isolating LLM traffic without breaking egress
- [ADR-008: cllama Sidecar Standard](docs/decisions/008-cllama-sidecar-standard.md) — formalizing the context-aware proxy
- [ADR-009: Contract Composition](docs/decisions/009-contract-composition-and-policy.md) — modularizing context via includes
- [CLLAMA_SPEC.md](docs/CLLAMA_SPEC.md) — The technical specification for the proxy standard
