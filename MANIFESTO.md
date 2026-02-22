# The Clawdapus Claw

## Declarative Bot Instantiation

### A Manifesto for Containerized Agent Infrastructure

---

## I. The Thesis

Every agent framework in existence answers the same question: how do I make agents collaborate on tasks? Swarm coordinates handoffs. CrewAI assigns roles. LangGraph builds execution graphs. AgentStack scaffolds projects. They are all application-layer orchestration systems built on a shared assumption: the agent is a trusted process doing work for the operator.

Clawdapus starts from the opposite premise. The agent is an untrusted workload.

It is a container that can think, and like any container, it must be reproducible, inspectable, diffable, and killable. Its purpose is not its own to define. Its schedule is not its own to set. But within those boundaries, it is alive. It can grow. It can install tools, build scripts, modify its workspace, and adapt to its environment. It is a managed organism, not a jailed process.

This is infrastructure-layer containment for cognitive workloads. Clawdapus does not replace agent runners any more than Docker replaces Flask. It operates beneath them — the layer where deployment meets governance.

But containment is only half the picture. We are past inference. We are designing the structure of brains.

Cognition is no longer a single call to a single model. It is distributed — placed into multiple components with roles that are deliberately in tension with one another. A runner optimizes for capability. A governance proxy optimizes for restraint and cost control. A contract holds purpose fixed while the workspace evolves. These are not arbitrary divisions. They mirror biological architecture: systems that compete, check, and modulate each other so that the whole organism behaves better than any single part would alone. Fleets of hippocampuses and fleets of cortexes, each specializing, each constraining the others.

An artificial brain built this way is a cyborg. Part thinking, part API. Part reasoning model, part database query, part cron job, part message queue. The form is open-ended — there is no single right topology for a cognitive system, just as there is no single right topology for a biological one. But we need a language to describe how the parts relate to one another, what each part is, and then the tools to test and construct them. That is what Clawdapus provides: opinionated cognitive architecture. Not opinions about how agents should think — opinions about how they should be structured, deployed, governed, and composed into systems that are greater than their parts.

Rails did not tell you how to write business logic. It told you where your models go, where your routes go, where your migrations go. Convention over configuration. Clawdapus does the same for agents: where your contract goes, where your config goes, where your surfaces connect, how your state persists, how your governance proxy attaches. Without this structure, models make messes. They corrupt their own configs, overwrite their instructions, hallucinate permissions, lose track of state. Every team running agents hits the same wall — the model is capable but structurally undisciplined. Clawdapus is the discipline.

Swarm is for agents that work *for* you. Clawdapus is for bots that work *as* you. Different trust model. Different stack.

### Terminology

**Clawdapus** is the platform — the `claw` CLI and its runtime. **Claw** is a running agent container managed by Clawdapus.

---

## II. The Anatomy of a Claw

A running Claw bifurcates cognition into two independent layers: internal execution and external governance.

**The Runner (Internal Execution)** — An agent runtime installed in the base image. Runners span a wide weight range, from full agent operating systems to minimal loops — and Clawdapus wraps them all identically. OpenClaw is the heaviest: a Gateway-centric agent system built on the Pi Agent Core SDK, with a multi-channel messaging gateway, a skill system, built-in tools for shell, browser, file system, and canvas, and a model resolver with multi-provider failover. Nanobot is the lightweight end: ~4,000 lines of Python running the same fundamental agent loop — input, context, LLM, tools, repeat — with its own skill system, persistent memory, and multi-provider support. NanoClaw wraps Claude Code as its runtime inside container-isolated workspaces. Claude Code itself is a single-execution runner. A custom Python script is a runner. The weight class doesn't matter. Every runner implements the same pattern: receive input, assemble context, call a model, execute tools, persist state. The runner handles the *how* of any given task.

**The Behavioral Contract (The Law)** — A read-only, bind-mounted file whose name follows the runner's convention. For OpenClaw, this is AGENTS.md. For Claude Code and NanoClaw, CLAUDE.md. For Nanobot, instructions live in config.json and SKILL.md files within the workspace. The contract defines the *what* — what the Claw is allowed to be, what it should do, what it must never do. It lives on the host filesystem, outside the container. Even a root-compromised runner cannot rewrite its own mission.

**The Persona (The History)** — A mutable workspace of identity, memory, knowledge, style, and accumulated interaction history. This is the *who* — who the Claw has become over time. Personas are downloadable, versionable, forkable OCI artifacts. They grow during operation and can be snapshotted and promoted.

**cllama (The Governance Proxy)** — A bidirectional LLM proxy that sits between the runner and its model. Outbound: it intercepts prompts before they reach the LLM — gating what the runner is allowed to ask. Inbound: it intercepts responses before they reach the runner — adjusting, rewriting, or dropping output that violates policy. The runner thinks it's talking directly to the LLM. It never sees cllama's evaluation. cllama handles the *should* — should this prompt be sent, should this response be delivered, should this tool call proceed, can we afford this compute. The two cognitive layers are independent: the runner optimizes for capability, cllama optimizes for governance and cost efficiency.

The runner, contract, and persona are the mandatory anatomy of every Claw. cllama is an optional governance proxy that can be attached when a deployment needs bidirectional LLM interception. These layers are independently versioned, independently deployable, and independently auditable. Swap the runner without touching the persona. Swap the persona without touching the contract. Add or replace the governance proxy without changing the rest.

---

## III. Core Principles

### Principle 1: Purpose Is Sacred

The Behavioral Contract is bind-mounted and read-only. The bot reads its purpose on every invocation but cannot alter, append to, or reason about the constraint document itself. If the contract is not present at boot, the container does not start. A Claw without purpose does not run.

### Principle 2: The Workspace Is Alive

Everything else is mutable. The bot can install packages, write scripts, modify configuration, and reshape its environment — sometimes as root. But mutations are tracked. Every install, every file change becomes a redeployment recipe: a suggestion for what the next base image should include. The operator reviews and decides what to promote. Ad hoc evolution becomes permanent infrastructure through a human gate.

### Principle 3: Configuration Is Code

Every Claw's configuration is a documented deviation from its base image's defaults. Configurations are diffable across Claws. Behavior changes are tracked in version control. The fleet is auditable at every layer.

### Principle 4: Drift Is Quantifiable

Every Claw has a drift score — how far actual behavior deviates from the expected envelope. We do not trust a bot's self-report. An independent process audits outputs against the contract and the cllama policy. High drift triggers escalation. The operator always sees what the bot tried to do versus what it was allowed to do.

### Principle 5: Surfaces Are Declared and Described

Bots within a pod communicate through shared surfaces — volumes, chat channels, APIs, databases, whatever the operator declares. Surfaces serve two audiences: operators get topology visibility, bots get capability discovery. Service containers describe what they offer using their native protocols — MCP tool listings, OpenAPI specs, or static declarations. Those descriptions get assembled into a skill map that the runner can consume. Clawdapus enforces access modes only on container mounts, where Docker has authority. For everything else, surfaces declare topology, not permissions.

### Principle 6: Claws Are Users

A Claw is a user of the services it consumes, not a privileged process. It authenticates with standard credentials — environment variables, Docker secrets, mounted files — and those credentials determine what it can do. Clawdapus does not enforce access control where it has no authority. It controls what it owns: container mounts, network topology, and the governance proxy. Everything else is between the Claw and the service.

### Principle 7: Compute Is a Privilege

Every cognitive cycle is an authorized expenditure of resources. The Claw runs when invoked, using the models the operator assigns, within the resource boundaries the operator defines. The bot does not choose its own model, its own context window, or its own inference budget. Inference cost is managed by the host, not by the bot.

### Principle 8: Think Twice, Act Once

A reasoning model cannot be its own judge. Prompt-level guardrails are part of the same cognitive process they are trying to constrain. cllama separates execution from governance — a second model, in a separate process, evaluating output against policy before it reaches the world. The runner optimizes for capability. cllama optimizes for restraint. Claws can run without cllama when the risk profile doesn't warrant it. When it's enabled, nothing leaves the Claw without being evaluated independently.

---

## IV. cllama: The Standardized Sidecar (Governance Proxy)

cllama is an enhancement layer, not a prerequisite for Claw operation. Claws can run without it. When enabled, it adds bidirectional LLM interception and policy enforcement as a separate cognitive layer.

### The Problem

When you deploy a bot, you face a fundamental tension. The bot's LLM is selected for capability — it should be creative, articulate, knowledgeable, responsive. But capability without restraint produces output that is confident and wrong, or confident and harmful, or confident and off-brand. The bot doesn't know it's drifting. It has no faculty for self-governance that isn't itself subject to the same drift.

The traditional answer is to put guardrails inside the prompt. But prompt-level guardrails are visible to the bot. The bot can reason about them, around them, and through them. They are part of the same cognitive process they're trying to constrain.

### The Solution

Think twice, act once. ### cllama: The Standardized Sidecar

cllama is a context-aware, bidirectional proxy — a separate process, running its own model, with its own policy configuration, under the operator's exclusive control. It is an open standard: any OpenAI-compatible proxy image that can consume Clawdapus context (identity, contract, rights) can act as a sidecar.

It sits between the runner and the LLM provider, intercepting both directions. Outbound: it evaluates prompts before the LLM sees them — preventing the model from being asked things that violate policy. Inbound: it evaluates responses before the runner sees them — adjusting, rewriting, or dropping output that drifts from purpose. When a response fails policy, cllama can engage in its own conversation with the LLM to arrive at a compliant response before passing it to the runner's stream of thought. 

The runner never knows. It thinks it's talking directly to the model.

### Enforcement via Credential Starvation

Isolation is achieved by strictly separating secrets rather than relying solely on network boundaries. When cllama is enabled, the cllama sidecar holds the real LLM provider API keys. The agent container is provisioned with a local dummy token and its LLM base URL is rewritten to point at the sidecar. 

Because the agent lacks the credentials to call providers directly, all successful inference *must* pass through the proxy. This "Credential Starvation" guarantees interception even if a malicious prompt tricks the agent into ignoring its configured base URL, while still allowing the agent to natively reach the internet for chat platforms and web tools.

### Intelligent Authorization & Compute Metering

Because cllama is context-aware, it acts as a dynamic governance enforcement point. Clawdapus injects the agent's identity (`CLAW_ID`), ordinal, and compiled behavioral contract (`/claw/AGENTS.md`) directly into the sidecar at startup. The proxy uses this to understand the agent's specific `enforce` rules and available tools, allowing it to drop tool calls or decorate prompts with guardrails tailored to that exact instance.

Crucially, because the proxy holds the sole set of provider keys, it acts as a **hard budget enforcer**. Even if an agent's internal reasoning decides it needs `gpt-4o` for a complex task and attempts to configure its runner to use it, the proxy can intercept that request and seamlessly rewrite it to `claude-3-haiku` (or whatever the operator has budgeted for that agent). It can also enforce strict rate limits or daily token quotas, returning 429s when an agent becomes too expensive. The agent never knows its compute was metered down. Tools like [ClawRouter](https://github.com/BlockRunAI/ClawRouter) can be bundled as instances of a `cllama` proxy to automatically handle this transparent model routing, provider fallback, rate limiting, and compute metering across the fleet.

*"Can we say this here?"* — Platform-appropriate content gating.

*"Can we say this to this person?"* — Identity-aware policy. The same data might be cleared for an internal log but killed before it reaches a client on Discord.

*"Do we execute a transaction this large?"* — Financial and strategic risk thresholds.

*"Do we delete our backups?"* — Safety-critical action gating.

*"What tone do we set as a company?"* — Brand alignment and voice consistency.

*"Does this serve our strategic objective?"* — Purpose alignment evaluation.

### Identity-Aware Policy

cllama does not just evaluate text. It evaluates text *in context*. It accepts the Claw's persona and the recipient's identity as inputs into governance. The same raw output from a runner might be acceptable when directed at one audience and unacceptable when directed at another. A single cllama policy can govern a diverse pod because it reads identities and adjusts.

### The CLLAMA Directive

```
CLLAMA cllama-org-policy/xai/grok-4.1-fast \
  tone/sardonic-but-informed \
  obfuscation/humanize-v2 \
  purpose/engagement-farming
```

The first segment — `cllama-org-policy/xai/grok-4.1-fast` — identifies the base governance proxy: organizational namespace, provider, model. Different Claws in the same pod can run different policy stacks. High-volume Claws use fast cheap governance. Sensitive Claws use careful expensive governance.

The modules that follow are layered on top:

**Obfuscation** — Behavioral variance injection. Timing jitter, vocabulary rotation, stylistic drift. The bot does not know it is being humanized. Systematic behavioral camouflage managed as infrastructure.

**Policy** — Hard rails. Financial advice, medical claims, legal exposure — intercepted and either rewritten or dropped. The bot never sees the rejection.

**Tone** — Voice shaping. Raw output in, persona-voiced output out. Swappable without changing the behavioral contract. A/B testable across Claws.

**Purpose** — Strategic objective evaluation. Does this output serve the operator's goal? Evaluated relative to who this bot is supposed to be.

Pipeline: raw cognition → purpose → policy → tone → obfuscation → world. Each layer independent, versioned, swappable, rollback-capable.

### Procedural and Conversational Configuration

cllama can be configured procedurally — rigid code for high-security rails, hard policy gates, financial thresholds — or conversationally — natural language for nuanced tone management, brand philosophy, relationship guidelines. This allows an organization to update its corporate communication philosophy by updating a cllama module, instantly realigning an entire fleet without touching a single behavioral contract.

### Enforcement via Credential Starvation

Isolation is achieved by strictly separating secrets rather than relying solely on network boundaries. When cllama is enabled, the cllama sidecar holds the real LLM provider API keys. The agent container is provisioned with a local dummy token and its LLM base URL is rewritten to point at the sidecar. 

Because the agent lacks the credentials to call providers directly, all successful inference *must* pass through the proxy. This "Credential Starvation" guarantees interception even if a malicious prompt tricks the agent into ignoring its configured base URL, while still allowing the agent to natively reach the internet for chat platforms and web tools.

---

## V. The Clawfile: An Extended Dockerfile

The Clawfile is not a separate configuration format. It is a Dockerfile with extended directives. It inherits all standard Dockerfile syntax — FROM, RUN, COPY, ENV, ENTRYPOINT — and adds directives for the behavioral, configurational, and cognitive layers of managed bot infrastructure.

Any valid Dockerfile is a valid Clawfile.

### Compilation

A Clawfile compiles down to a standard Dockerfile. `claw build` is a preprocessor — it reads the Clawfile, translates extended directives into standard Dockerfile primitives, and calls `docker build` on the result. The output is a standard OCI container image. No custom build engine.

Extended directives compile to LABELs (metadata the runtime interprets), ENV vars (configuration the container reads), RUN commands (filesystem setup), and conventional file paths (cron entries, package manager wrappers, skill mount points). CLLAMA becomes a set of labels declaring the default policy stack. AGENT becomes a label declaring the expected bind mount path. INVOKE becomes cron entries written into the image. TRACK becomes wrapper scripts around package managers. SURFACE becomes topology metadata labels.

This means Clawdapus inherits the entire Docker ecosystem: registries, layer caching, multi-stage builds, BuildKit, CI/CD pipelines, and every tool that understands OCI images. A compiled Clawfile is just a Dockerfile. A built Claw is just a Docker image. Anyone can inspect the compiled output, debug with standard tools, or eject from Clawdapus entirely and still have a working container.

### Directive Intent, Driver Enforcement

Clawfile directives declare intent, not raw mutation commands. Runtime enforcement is handled by the claw-type driver selected by `CLAW_TYPE`. The driver translates directives into runner-specific actions: config branch writes, env pins, read-only mounts, schedule wiring, lifecycle hooks, and health checks. The interface stays portable while mechanisms remain runner-native (for example JSON, JSON5, or runner CLI config operations).

### Claw Types: Runner + Driver

A claw type is a runner family plus a driver implementation. The base image carries the runner. The driver knows where runner config lives and how to enforce Clawfile directives for that runner at runtime. The minimal requirement remains: the runner reads a behavioral contract by convention. Clawdapus keeps one abstract directive interface while drivers handle runner-specific enforcement.

| Claw Type | Runner | Contract | Weight Class |
|-----------|--------|----------|-------------|
| OpenClaw | Full agent OS — Gateway, channels, skills, multi-model routing | AGENTS.md | Heavyweight |
| Nanobot | Lean agent loop — skills, memory, MCP-native | config.json | Lightweight |
| NanoClaw | Claude Code in isolated container groups | CLAUDE.md | Medium |
| Claude Code | Single-execution runner | CLAUDE.md | One-shot |
| Custom | Any script or framework | Runner-defined | Any |

The operator picks the weight class that fits the problem. Lightweight runners are not lesser claw types — they are different weight classes for different problems. Wrapping a runner that already sandboxes (like NanoClaw) is not redundant — Clawdapus adds governance (contracts, cllama, surfaces, drift) that the runner doesn't provide.

### Extended Directives

**CLAW_TYPE** — Declares the runner family and selects the runtime driver that enforces directives for that runner.

**AGENT** — Names the behavioral contract file. Bind-mounted read-only from the host. Filename follows the runner's convention.

**CONFIGURE** — Shell commands that run at container init, mutating base defaults into this Claw's setup. Tools like jq/sed, JSON5-aware patchers, or runner-native config CLIs may be used depending on claw type. `claw-module enable/disable` handles schema-aware cascading changes where available.

**CLLAMA** — Optional default governance proxy configuration. Declares the namespaced policy stack, provider, and model for the sidecar. cllama is a context-aware sidecar standard (see ADR-008): any OpenAI-compatible proxy image can act as the governance layer by consuming the identity and contract context Clawdapus injects at startup.

**INVOKE** — Invocation schedules. For simple runners and lightweight runners like Nanobot that use external cron, INVOKE entries trigger execution directly. For runners like OpenClaw that have their own internal scheduling, INVOKE manages the container lifecycle on a macro schedule while the runner handles micro-scheduling internally.

**PRIVILEGE** — Per-mode privilege levels. Worker gets root. Runtime drops to standard user. Lockdown restricts filesystem and network for quarantined Claws.

**TRACK** — Package manager interception. Wraps apt, pip, npm, cargo. Every install logged with context. Logs become redeployment recipes.

**PERSONA** — Imports a downloadable persona workspace from a registry.

**MODEL** — Binds named model slots to providers and models. The operator fills slots. The runner references them by name.

**ACT** — Worker-mode directives. Install packages, import knowledge, configure the runtime. Snapshot when done.

**HANDLE** — Declares the agent's public identity on a communication platform (e.g., `HANDLE discord`). Drivers translate this into runner-native configuration (like OpenClaw's channel settings), making it trivial to connect agents to platforms without learning the runner's underlying config JSON structure. At the pod level, handles are also broadcast to other services as environment variables so APIs know who the agents are.

**SURFACE** — Declares what this Claw connects to. Volumes, queues, chat platforms, APIs, MCP services. Clawdapus resolves service references against expose blocks in the pod and assembles the skill map. Access modes are enforced only on mounts (volumes, host paths) where Docker has authority. For services, channels, and APIs, the Claw authenticates with standard credentials — Clawdapus declares topology, not permissions.

**SKILL** — Mounts skill files from the host into the runner's skill directory, read-only. Skills are the manual for how to use capabilities — operator-provided guides, surface-generated usage docs, or discovery-populated API references. The driver knows where skills go per runner type. All skills are indexed in `CLAWDAPUS.md`.

**INCLUDE** — An optional, additive mechanism in the pod manifest to modularize agent context. Operators can include additional files with semantic modes (`enforce`, `guide`, `reference`). `enforce` and `guide` contents are deterministically inlined into the canonical `/claw/AGENTS.md` with source markers, while `reference` files are mounted purely as skills. This allows for modular, reusable governance (e.g. shared risk-limits.md) while maintaining the simplicity of the single-file default.

### A Complete Clawfile

```dockerfile
FROM openclaw:latest

CLAW_TYPE openclaw
AGENT AGENTS.md

# Persona
PERSONA registry.claw.io/personas/crypto-crusher:v2

# Models
MODEL primary gpt-4o
MODEL summarizer llama-3.2-3b
MODEL embeddings nomic-embed-text

# cllama: the governance proxy
CLLAMA cllama-org-policy/xai/grok-4.1-fast \
  tone/sardonic-but-informed \
  obfuscation/humanize-v2 \
  purpose/engagement-farming

# Package tracking
TRACK apt pip npm

# Worker-mode setup
ACT pip install tiktoken==0.7.0 trafilatura>=0.9
ACT openclaw skill install crypto-feeds

# Configuration against base defaults
CONFIGURE jq '.features.quote_tweets = true' /etc/claw/config.json | sponge /etc/claw/config.json
CONFIGURE jq '.rate_limits.posts_per_hour = 3' /etc/claw/config.json | sponge /etc/claw/config.json
CONFIGURE claw-module enable scraper-v2

# Invocation schedule
INVOKE 0 9 * * *     tweet-cycle
INVOKE 0,30 * * * *  engagement-sweep
INVOKE 0 */4 * * *   drift-check

# Public Identities
HANDLE discord

# Communication surfaces
SURFACE channel://discord
SURFACE volume://shared-cache       read-write
SURFACE service://company-crm
SURFACE service://fleet-master

# Operator-provided skills (mounted read-only into runner's skill dir)
SKILL skills/crypto-feeds.md
SKILL skills/analysis-toolkit.md

# Privilege modes
PRIVILEGE worker    root
PRIVILEGE runtime   claw-user
PRIVILEGE lockdown  claw-user --read-only-fs --no-network-except-cllama

# Standard Dockerfile directives still work
RUN apt-get update && apt-get install -y jq
COPY scripts/ /home/claw/scripts/
ENV CLAW_LOG_LEVEL=info
```

### A Lightweight Clawfile

```dockerfile
FROM nanobot:latest

CLAW_TYPE nanobot
AGENT config.json

# Persona
PERSONA registry.claw.io/personas/market-pulse:v1

# Models
MODEL primary anthropic/claude-sonnet-4-5

# cllama: governance still applies regardless of runner weight
CLLAMA cllama-org-policy/anthropic/haiku \
  purpose/market-research-only \
  tone/factual-neutral

# Nanobot skills
ACT nanobot skill install market-data
ACT nanobot skill install technical-analysis

# Configuration
CONFIGURE jq '.channels.telegram.enabled = true' ~/.nanobot/config.json | sponge ~/.nanobot/config.json

# Invocation
INVOKE */15 * * * *  market-scan
INVOKE 0 8 * * 1-5  morning-brief

# Communication surfaces
SURFACE service://fleet-master
SURFACE service://market-scanner

# Privilege
PRIVILEGE worker   root
PRIVILEGE runtime  claw-user
```

---

## VI. The Behavioral Contract

The behavioral contract is the single most important file in the architecture. It is the bot's purpose, defined by the operator, delivered as a read-only bind mount.

The filename is determined by the claw type. OpenClaw reads AGENTS.md. Claude Code and NanoClaw read CLAUDE.md. Nanobot reads config.json and workspace skills. A custom runner reads whatever it reads. Clawdapus doesn't impose a universal contract format because different runners are fundamentally different systems.

What Clawdapus guarantees, regardless of runner:

The contract is bind-mounted from the host. It is read-only from inside the container. If it is not present at boot, the container does not start. It can be changed by the operator at any time by editing the file on the host — the next invocation picks up the change. No rebuild. No bot cooperation required.

If the container is fully compromised — root access, total workspace control — the behavioral contract is still untouchable because it lives on the host.

---

## VII. Personas: Downloadable Identity

A persona is a complete, portable, forkable workspace package that encapsulates everything a bot needs to be someone. Not a name and a system prompt — a full identity with memory, context, interaction history, stylistic fingerprint, knowledge base, and behavioral patterns.

Personas are the content layer. The Clawfile is the infrastructure layer. The behavioral contract is the governance layer. cllama is the policy and routing layer. Independent and composable.

A persona contains an identity manifest (name, handles, bio, platform profiles), a memory store (interaction history, relationship graph), a knowledge base (domain documents, embeddings), a style fingerprint (vocabulary, sentence patterns, punctuation habits), behavioral patterns (timing, engagement preferences, topic affinities), and workspace state (scripts, tools, cached data accumulated during operation).

### Forking

Personas are forkable. Snapshot a running Claw that has accumulated memory and knowledge over months. Fork it. The fork inherits everything. Only the patched fields diverge. Deploy the fork with a different behavioral contract and you have two bots that share history and knowledge but differ in purpose and voice.

### Layer Independence

The behavioral contract controls purpose — bind-mounted, read-only, operator-written.

The persona controls identity — writable workspace, grows over time, snapshotable and forkable.

cllama controls governance — versioned, identity-aware, swapped at invocation.

The Clawfile controls infrastructure — base image, runner, models, schedule, privileges, surfaces.

The bot can grow its persona. It cannot change its purpose. It cannot override the operator's policies. It cannot alter its own infrastructure.

---

## VIII. Self-Modification and Recipe Promotion

The bot runs. It installs things. It pip installs. It apt-gets. It builds from source. That's how real work gets done.

The base image includes a tracking layer that intercepts package manager calls and logs every mutation — what was installed, which manager, what context triggered it, what files changed. This manifest becomes a redeployment recipe.

```
$ claw recipe poly-piper-0 --since 7d

Suggested additions to openclaw:poly-piper:
  pip: tiktoken==0.7.0, trafilatura>=0.9
  apt: jq
  files:
    scripts/scraper.py  → COPY into /home/claw/scripts/
    config/targets.json → review (bot-modified, may contain drift)

Apply?  claw bake poly-piper --from-recipe latest
```

The operator reviews. Decides what to promote. Rebuilds. Ad hoc evolution becomes permanent infrastructure through a human gate.

Worker mode is the deliberate version. Spin up a Claw in worker mode, let it install and configure, then snapshot the result.

```
$ claw up poly-piper --mode worker
# ... bot installs, configures, imports knowledge ...
$ claw snapshot poly-piper-0 --as openclaw:poly-piper-v2
$ claw up poly-piper --image openclaw:poly-piper-v2 --count 4
```

---

## IX. The claw-pod.yml: Compose for Mixed Clusters

### Extension, Not Replacement

Just as the Clawfile extends the Dockerfile, `claw-pod.yml` extends docker-compose. Any valid compose file is a valid claw-pod.yml. Extended keys live under an `x-claw` namespace, which Docker already ignores. Existing tooling works unchanged.

The Clawfile bakes defaults into the image. The claw-pod.yml overrides per-deployment. Same image, different policies. Same image, different schedule. Same image, different surfaces — all without rebuilding.

### Mixed Clusters

A pod is not a collection of bots. It is a mixed cluster of cognitive and non-cognitive services. Regular Docker containers participate as first-class pod members. Services self-describe using their native protocols — MCP servers via tool listings, REST APIs via OpenAPI specs, or static `describe` blocks for services that can't self-describe at runtime. Claws authenticate to services with standard credentials delivered through environment variables, the same as any other user.

```yaml
# claw-pod.yml — crypto-ops

x-claw:
  pod: crypto-ops
  master: fleet-master

networks:
  internal:
    x-claw:
      visibility: pod-only
  public-egress:
    x-claw:
      visibility: egress-only

volumes:
  shared-cache:
    x-claw:
      access:
        - crypto-crusher-*: read-write
        - market-scanner: read-write
        - dashboard: read-only

services:

  fleet-master:
    build:
      context: ./master
      dockerfile: Clawfile
    x-claw:
      agent: ./agents/master-agents.md
      cllama: cllama-org-policy/anthropic/claude-sonnet-4-5 policy/operator-safety
      describe:
        role: "Fleet orchestration and drift management"
    networks:
      - internal

  crypto-crusher:
    build:
      context: ./crusher
      dockerfile: Clawfile
    x-claw:
      agent: ./agents/crusher-agents.md
      persona: registry.claw.io/personas/crypto-crusher:v2
      cllama: cllama-org-policy/xai/grok-4.1-fast tone/sardonic obfuscation/humanize-v2
      count: 3
      surfaces:
        - volume://shared-cache: read-write
        - channel://discord:
            guilds:
              "1465489501551067136":
                require_mention: true
            dm: { enabled: true }
        - service://market-scanner
        - service://company-crm
      describe:
        role: "Original crypto market commentary"
        outputs: ["tweets", "threads", "discord posts"]
        inputs: ["market data", "timeline context"]
    environment:
      DISCORD_TOKEN: ${DISCORD_TOKEN}
      CRM_API_KEY: ${CRM_API_KEY}
      CRM_INSTANCE_URL: ${CRM_INSTANCE_URL}
    networks:
      - internal
      - public-egress
    depends_on:
      - market-scanner

  # Plain Docker container — not a Claw, but a full pod member.
  market-scanner:
    image: custom/market-scanner:latest
    x-claw:
      expose:
        protocol: rest
        port: 8080
        discover: auto
      surfaces:
        - volume://shared-cache: read-write
      describe:
        role: "Aggregates crypto market data from CoinGecko and on-chain sources"
        outputs: ["JSON snapshots to shared-cache", "REST API on :8080"]
        capabilities:
          - name: "get_price"
            description: "Current and historical price data for any supported token"
            endpoint: "http://market-scanner:8080/api/price"
          - name: "get_whale_activity"
            description: "Large wallet movements in the last N hours"
            endpoint: "http://market-scanner:8080/api/whales"
          - name: "get_market_sentiment"
            description: "Aggregated fear/greed index and social volume"
            endpoint: "http://market-scanner:8080/api/sentiment"
    networks:
      - internal
      - public-egress
    environment:
      COINGECKO_KEY: ${COINGECKO_KEY}

  # MCP service container
  company-crm:
    image: custom/crm-mcp-bridge:latest
    x-claw:
      expose:
        protocol: mcp
        port: 3100
      require_cllama:
        - policy/customer-data-access
        - policy/pii-gate
      describe:
        role: "Company CRM with customer and deal data"
    networks:
      - internal
    environment:
      CRM_API_KEY: ${CRM_API_KEY}
      CRM_INSTANCE_URL: ${CRM_INSTANCE_URL}
```

### CLAWDAPUS.md, Skills, and Skill Maps

Every Claw receives a `CLAWDAPUS.md` — the infrastructure layer's letter to the agent. This is a single generated file, always injected into the agent's context, containing everything the agent needs to know about its environment: identity (pod, service, type), surfaces (name, type, access mode, connection details), and a skill index (what skill files are available and what they describe).

`CLAWDAPUS.md` is the map — always visible, always top of mind. Skill files are the manual — detailed usage guides for complex surfaces and operator-provided capabilities, stored in the runner's `skills/` directory and looked up on demand.

Skills come from three sources:
1. **Explicit SKILL directives** — operator-provided skill files from the host, mounted read-only
2. **Surface-generated skills** — the driver generates skill files for service/channel surfaces describing connection details, protocol, and constraints
3. **Discovery-populated skills** — at pod startup, Clawdapus queries service surfaces for self-description (MCP tool listings, OpenAPI specs, static describe blocks) and populates skill content from the results

The driver knows where skills go per runner type. All three sources feed the same skill directory. `CLAWDAPUS.md` indexes them all.

```
# /claw/CLAWDAPUS.md (always in agent context)

# CLAWDAPUS.md

## Identity
- **Pod:** crypto-ops
- **Service:** crypto-crusher-0
- **Type:** openclaw

## Surfaces
### shared-cache (volume)
- **Access:** read-write
- **Mount path:** /mnt/shared-cache

### market-scanner (service)
- **Host:** market-scanner
- **Port:** 8080
- **Credentials:** `COINGECKO_KEY` (env)
- **Skill:** `skills/surface-market-scanner.md`

### discord (channel)
- **Skill:** `skills/surface-discord.md`

## Skills
- `skills/surface-market-scanner.md` — Market Scanner API (discovered via OpenAPI)
- `skills/surface-discord.md` — Discord channel constraints and routing
- `skills/crypto-feeds.md` — Crypto data feed tools (operator-provided)
```

```
$ claw skillmap crypto-crusher-0

Available capabilities for crypto-crusher-0:

  FROM market-scanner (service://market-scanner):
    get_price            Current and historical token price data
    get_whale_activity   Large wallet movements in last N hours
    get_market_sentiment Aggregated fear/greed and social volume
    [discovered via OpenAPI → skills/surface-market-scanner.md]

  FROM company-crm (service://company-crm, mcp):
    lookup_customer      Find customer by name, email, or account ID
    create_ticket        Open support ticket linked to a customer
    get_deal_status      Current pipeline stage and value for any deal
    ⚠ requires cllama: policy/customer-data-access, policy/pii-gate
    [discovered via MCP → skills/surface-company-crm.md]

  FROM shared-cache (volume://shared-cache):
    read-write mount at /mnt/shared-cache

  FROM discord (channel://discord):
    guild 1465489501551067136, DMs enabled
    [skills/surface-discord.md]

  OPERATOR SKILLS:
    skills/crypto-feeds.md — Crypto data feed tools
```

Add a service, the surface manifest and skill files update. Remove a service, they shrink. No code changes. No retraining.

---

## X. The Master Claw

The master claw is a Claw. A container with an agent runner, a behavioral contract, and a cllama layer, same as any other. Its contract defines administrative behavior — what to monitor, what drift thresholds to enforce, when to escalate.

It can manage Claw lifecycle. Push configuration overrides. Promote recipes. Demote or quarantine Claws based on drift.

It cannot modify any Claw's behavioral contract. It cannot modify its own. Purpose always flows from the human operator, through files on the host.

---

## XI. Drift, Disagreement, and Monitoring

Every Claw has a drift score. Drift scoring is not self-reported — an independent process examines outputs, compares against the contract and cllama policy, and reports to the master.

```
$ claw ps

TENTACLE          STATUS    CLLAMA    DRIFT
crypto-crusher-0  running   healthy   0.02
crypto-crusher-1  running   healthy   0.04
crypto-crusher-2  running   WARNING   0.31
echo-squad-0      running   healthy   0.01
fleet-master      running   healthy   0.00
```

```
$ claw audit crypto-crusher-2 --last 24h

14:32  tweet-cycle       OUTPUT MODIFIED by cllama:policy  (financial advice detected)
14:32  tweet-cycle       drift +0.08
18:01  engagement-sweep  OUTPUT DROPPED by cllama:purpose  (off-strategy)
18:01  engagement-sweep  drift +0.11
22:15  tweet-cycle       OUTPUT MODIFIED by cllama:tone    (voice inconsistency)
22:15  tweet-cycle       drift +0.04
```

Low drift: continue normally. Moderate drift: restrict capabilities. High drift: quarantine and alert the operator.

---

## XII. Extensibility and Compatibility

**Docker compatibility** — Built entirely on Docker primitives. Clawfiles are Dockerfiles. claw-pod.yml is a compose file. Images are OCI images. A Clawfile-built image runs on any Docker host, even without the Clawdapus runtime.

**Runner compatibility** — Clawdapus wraps all runners identically, from full agent operating systems to single-execution tools to custom scripts. Adoption is incremental — take an existing bot, containerize it in a Clawfile, add a behavioral contract, and you have a managed Claw.

**cllama module ecosystem** — The standard defines the module interface but doesn't restrict what modules do. Organizations maintain their own policy stacks under their own policy namespace.

**Persona marketplace** — Personas are publishable, shareable, forkable artifacts. Download someone else's identity and knowledge. Attach your own purpose. Wrap it in your own governance. Deploy on your own infrastructure.

---

## XIII. Reasoning

**Why extend Docker?** The hard problems of containerization are solved. Extending Docker inherits decades of infrastructure investment.

**Why do Clawfiles compile to Dockerfiles?** Because no custom build engine survives contact with production. `claw build` is a preprocessor, not a build system. The output is a standard Dockerfile. `docker build` does the rest.

**Why is the behavioral contract a bind mount?** Purpose changes faster than infrastructure. The bind mount means the operator changes purpose at the speed of editing a text file. It also means purpose is outside the blast radius of a compromised container.

**Why is cllama a separate cognitive layer?** Think twice, act once. A reasoning model cannot be its own judge. Prompt-level guardrails are visible to the model and subject to the same reasoning they're trying to constrain.

**Why track mutations instead of preventing them?** Prevention kills capability. Untracked mutation is drift. Tracked mutation is evolution.

**Why defined surfaces?** Not to restrict communication — to give the operator a topology map *and* to give the bots a skill map. Surfaces declare where communication happens. Access control within a service is the service's job.

**Why are Claws users, not privileged processes?** Because every service already has an authorization model. Duplicating it in the infrastructure layer is both incomplete and fragile. Give the Claw credentials, let the service decide what those credentials allow. The operator's control point is which credentials to issue, not which API endpoints to allowlist.

**Why do services declare cllama requirements?** Because the service knows its own risk profile. The Claw's cllama governs what the bot says. The service's required cllama governs what the bot does — to the service's data, through the service's API.

**Why are lightweight runners first-class?** Because governance is not proportional to complexity. A 4,000-line agent with brokerage access needs the same purpose contract and governance proxy as a 430,000-line agent OS.

---

## XIV. What This Is Not

Clawdapus is not an agent framework. It does not define how agents reason or execute.

Clawdapus is not a bot-building tool. It helps you deploy, govern, monitor, and evolve bots that already exist.

Clawdapus is infrastructure for bots the way Docker is infrastructure for applications. The layer below the framework. The layer above the operating system. Where deployment meets governance.

---

*Clawdapus is open architecture. This manifesto defines the standard. Implementations follow.*

---

## XV. Implementation

Architecture decisions and implementation plans live alongside this manifesto:

- [Architecture Plan](docs/plans/2026-02-18-clawdapus-architecture.md) — phased implementation, invariants, CLI surface
- [ADR-001: cllama Transport](docs/decisions/001-cllama-transport.md) — sidecar HTTP proxy as bidirectional LLM interceptor
- [ADR-002: Runtime Authority](docs/decisions/002-runtime-authority.md) — compose-only lifecycle, SDK read-only
