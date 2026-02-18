# The Clawdapus Claw

## Declarative Bot Instantiation

### A Manifesto for Containerized Agent Infrastructure

v0.8 — February 2026

---

## I. The Thesis

Every agent framework in existence answers the same question: how do I make agents collaborate on tasks? Swarm coordinates handoffs. CrewAI assigns roles. LangGraph builds execution graphs. AgentStack scaffolds projects. They are all application-layer orchestration systems built on a shared assumption: the agent is a trusted process doing work for the operator.

Clawdapus starts from the opposite premise. The agent is an untrusted workload.

It is a container that can think, and like any container, it must be reproducible, inspectable, diffable, and killable. Its purpose is not its own to define. Its schedule is not its own to set. But within those boundaries, it is alive. It can grow. It can install tools, build scripts, modify its workspace, and adapt to its environment. It is a managed organism, not a jailed process.

This is infrastructure-layer containment for cognitive workloads. Clawdapus does not replace agent runners any more than Docker replaces Flask. It operates beneath them — the layer where deployment meets governance.

Swarm is for agents that work *for* you. Clawdapus is for bots that work *as* you. Different trust model. Different stack.

---

## II. The Anatomy of a Claw

A running Claw bifurcates cognition into two independent layers: internal execution and external judgment.

**The Runner (Internal Execution)** — An agent runtime installed in the base image. Runners span a wide weight range, from full agent operating systems to minimal loops — and Clawdapus wraps them all identically. OpenClaw is the heaviest: a Gateway-centric agent system built on the Pi Agent Core SDK, with a multi-channel messaging gateway, a skill system, built-in tools for shell, browser, file system, and canvas, and a model resolver with multi-provider failover. Nanobot is the lightweight end: ~4,000 lines of Python running the same fundamental agent loop — input, context, LLM, tools, repeat — with its own skill system, persistent memory, and multi-provider support. NanoClaw wraps Claude Code as its runtime inside container-isolated workspaces. Claude Code itself is a single-execution runner. A custom Python script is a runner. The weight class doesn't matter. Every runner implements the same pattern: receive input, assemble context, call a model, execute tools, persist state. The runner handles the *how* of any given task.

**The Behavioral Contract (The Law)** — A read-only, bind-mounted file whose name follows the runner's convention. For OpenClaw, this is AGENTS.md. For Claude Code and NanoClaw, CLAUDE.md. For Nanobot, instructions live in config.json and SKILL.md files within the workspace. The contract defines the *what* — what the Claw is allowed to be, what it should do, what it must never do. It lives on the host filesystem, outside the container. Even a root-compromised runner cannot rewrite its own mission.

**The Persona (The History)** — A mutable workspace of identity, memory, knowledge, style, and accumulated interaction history. This is the *who* — who the Claw has become over time. Personas are downloadable, versionable, forkable OCI artifacts. They grow during operation and can be snapshotted and promoted.

**cllama (The Judgment Proxy)** — An external, LLM-powered proxy that sits between the Claw and the world. It uses the identity of the Claw *and* the identity of the outside party to apply policy, shape tone, enforce strategy, and gate risk. cllama handles the *should*. Should this output reach the world? Should this action proceed? Should this communication go to this recipient in this tone? The runner never sees cllama's evaluation. The two cognitive layers are independent: the runner optimizes for capability, cllama optimizes for judgment.

These four components — runner, contract, persona, cllama — are the anatomy of every Claw. They are independently versioned, independently deployable, and independently auditable. Swap the runner without touching the persona. Swap the persona without touching the contract. Swap the judgment layer without touching any of them.

---

## III. Core Principles

### Principle 1: Purpose Is Sacred

The Behavioral Contract is bind-mounted and read-only. The bot reads its purpose on every invocation but cannot alter, append to, or reason about the constraint document itself. If the contract is not present at boot, the container does not start. A Claw without purpose does not run.

### Principle 2: The Workspace Is Alive

Everything else is mutable. The bot can install packages, write scripts, modify configuration, and reshape its environment. Sometimes as root. But mutations are intercepted by the tracking layer and become redeployment recipes — suggestions for what the next base image should include. The operator reviews the recipe and decides what to promote. Ad hoc evolution becomes permanent infrastructure through a human gate.

### Principle 3: Think Twice, Act Once

Think twice, act once. A reasoning model cannot be its own judge. Prompt-level guardrails are visible to the model and subject to the same reasoning they're trying to constrain. You're asking the engine to also be the brakes. cllama separates them. The runner thinks. cllama thinks again. Only then does the output reach the world. Two independent cognitive processes, two independent models, one gate. Nothing leaves the Claw without being thought about twice.

### Principle 4: Compute Is a Privilege

Every cognitive cycle is an authorized expenditure of resources. Inference cost containment is managed by the host, not by the bot. The Claw is gated — it runs when invoked, using the models the operator assigns, within the resource boundaries the operator defines. The bot does not choose its own model, its own context window, or its own inference budget.

### Principle 5: Configuration Is Code

Every Claw's configuration is a documented deviation from its base image's defaults. Configurations are diffable across Claws. Behavior changes are tracked in version control. The fleet is auditable at every layer.

### Principle 6: Drift Is Quantifiable

Every Claw has a drift score — how far actual behavior deviates from the expected envelope. We do not trust a bot's self-report. We audit its outputs against its contract and its cllama policy. Drift scoring is performed by an independent process. High drift triggers escalation. The operator always sees what the bot tried to do versus what it was allowed to do.

### Principle 7: Surfaces Are Declared and Described

Bots within a pod communicate and act through shared surfaces — volumes, message queues, chat channels, APIs, databases, whatever the operator declares. Surfaces serve two audiences: the operator gets topology visibility (where can communication happen?), and the bots get capability discovery (what tools and services can I use?). Service containers — a company CRM, a market data API, a ticketing system — expose surfaces with machine-readable descriptions of what they offer. Those descriptions get assembled into a skill map that the runner can consume. The standard requires that surfaces be declared and that service surfaces describe themselves well enough for bots to use them.

---

## IV. cllama: The Judgment Proxy

### The Problem

When you deploy a bot, you face a fundamental tension. The bot's LLM is selected for capability — it should be creative, articulate, knowledgeable, responsive. But capability without restraint produces output that is confident and wrong, or confident and harmful, or confident and off-brand. The bot doesn't know it's drifting. It has no faculty for self-governance that isn't itself subject to the same drift.

The traditional answer is to put guardrails inside the prompt. But prompt-level guardrails are visible to the bot. The bot can reason about them, around them, and through them. They are part of the same cognitive process they're trying to constrain.

### The Solution

Think twice, act once. cllama is a separate LLM-powered process — running its own model, with its own policy configuration, under the operator's exclusive control — that evaluates the bot's output before it reaches the world. The runner thinks about what to say. cllama thinks about whether to say it. The bot never sees cllama's evaluation. It never knows its output was modified or dropped. Two independent cognitive processes, two independent models, one gate.

While the runner handles the logic of a task, cllama answers the institutional questions:

*"Can we say this here?"* — Platform-appropriate content gating.

*"Can we say this to this person?"* — Identity-aware policy. The same data might be cleared for an internal log but killed before it reaches a client on Discord.

*"Do we execute a transaction this large?"* — Financial and strategic risk thresholds.

*"Do we delete our backups?"* — Safety-critical action gating.

*"What tone do we set as a company?"* — Brand alignment and voice consistency.

*"Does this serve our strategic objective?"* — Purpose alignment evaluation.

### Identity-Aware Policy

cllama does not just evaluate text. It evaluates text *in context*. It accepts the Claw's persona and the recipient's identity as inputs into judgment. The same raw output from a runner might be acceptable when directed at one audience and unacceptable when directed at another. A single cllama policy can govern a diverse pod because it reads identities and adjusts.

### The CLLAMA Directive

```
CLLAMA cllama-org-policy/xai/grok-4.1-fast \
  tone/sardonic-but-informed \
  obfuscation/humanize-v2 \
  purpose/engagement-farming
```

The first segment — `cllama-org-policy/xai/grok-4.1-fast` — identifies the base judgment layer: organizational namespace, provider, model. Different Claws in the same pod can run different judgment stacks. High-volume Claws use fast cheap judgment. Sensitive Claws use careful expensive judgment.

The modules that follow are layered on top:

**Obfuscation** — Behavioral variance injection. Timing jitter, vocabulary rotation, stylistic drift. The bot does not know it is being humanized. Systematic behavioral camouflage managed as infrastructure.

**Policy** — Hard rails. Financial advice, medical claims, legal exposure — intercepted and either rewritten or dropped. The bot never sees the rejection.

**Tone** — Voice shaping. Raw output in, persona-voiced output out. Swappable without changing the behavioral contract. A/B testable across Claws.

**Purpose** — Strategic objective evaluation. Does this output serve the operator's goal? Evaluated relative to who this bot is supposed to be.

Pipeline: raw cognition → purpose → policy → tone → obfuscation → world. Each layer independent, versioned, swappable, rollback-capable.

### Procedural and Conversational Configuration

cllama can be configured procedurally — rigid code for high-security rails, hard policy gates, financial thresholds — or conversationally — natural language for nuanced tone management, brand philosophy, relationship guidelines. This allows an organization to update its corporate communication philosophy by updating a cllama module, instantly realigning an entire fleet without touching a single behavioral contract.

### API Keys

API keys for LLMs, platform APIs, and external services are exported as environment variables. Standard Docker practice. cllama has its own keys, configured separately — the bot's keys and cllama's keys are independent. Optional hardened mode routes all bot outbound calls through cllama so the bot never holds keys in process memory, but that's not the default.

---

## V. The Clawfile: An Extended Dockerfile

The Clawfile is not a separate configuration format. It is a Dockerfile with extended directives. It inherits all standard Dockerfile syntax — FROM, RUN, COPY, ENV, ENTRYPOINT — and adds directives for the behavioral, configurational, and cognitive layers of managed bot infrastructure.

Any valid Dockerfile is a valid Clawfile.

### Compilation

A Clawfile compiles down to a standard Dockerfile. `claw build` is a preprocessor — it reads the Clawfile, translates extended directives into standard Dockerfile primitives, and calls `docker build` on the result. The output is a standard OCI container image. No custom build engine.

Extended directives compile to LABELs (metadata the runtime interprets), ENV vars (configuration the container reads), RUN commands (filesystem setup), and conventional file paths (cron entries, package manager wrappers, skill mount points). CLLAMA becomes a set of labels declaring the default judgment stack. AGENT becomes a label declaring the expected bind mount path. INVOKE becomes cron entries written into the image. TRACK becomes wrapper scripts around package managers. SURFACE becomes topology metadata labels.

This means Clawdapus inherits the entire Docker ecosystem: registries, layer caching, multi-stage builds, BuildKit, CI/CD pipelines, and every tool that understands OCI images. A compiled Clawfile is just a Dockerfile. A built Claw is just a Docker image. Anyone can inspect the compiled output, debug with standard tools, or eject from Clawdapus entirely and still have a working container.

### Claw Types: The Base Image

A claw type is a base image with an agent runner installed in a predictable location. That's it.

An OpenClaw claw type is a Linux image with OpenClaw installed — its Gateway control plane, its channel adapters, its cron system, its skill registry, the Pi Agent Core runtime, its built-in tools, and its multi-model routing with provider failover. It is a substantial runtime — a full agent operating system. It expects AGENTS.md. Heavyweight. 430,000+ lines. 15+ channel adapters.

A Nanobot claw type is a Linux image with Nanobot installed — its agent loop, its skill system, its memory module, its provider routing, and its channel adapters. It is the same fundamental architecture as OpenClaw stripped to essentials. ~4,000 lines of Python. Sub-second cold start. 45MB memory footprint. MCP-native for modular tool composition. It reads config.json and workspace skills. Lightweight runners are not lesser claw types. They are different weight classes for different problems.

A NanoClaw claw type wraps Claude Code as its runtime, running each agent group in its own container sandbox with isolated filesystems and per-group CLAUDE.md memory. NanoClaw already does its own container isolation — Clawdapus wrapping NanoClaw is not redundant sandboxing, it is additive governance: purpose contracts, judgment proxies, lifecycle management, surface topology, and fleet coordination that NanoClaw doesn't provide.

A Claude Code claw type is simpler still — a Linux environment with Claude Code as a single-execution runner. It handles one task at a time. It expects CLAUDE.md.

Any agent runner can be a claw type. The requirement is that the runner is installed at a known path and reads a behavioral contract whose filename is part of the claw type's convention. Clawdapus wraps them all identically. The operator picks the weight class that fits the problem: OpenClaw for persistent multi-channel assistants, Nanobot for lean MCP-driven agents, NanoClaw for Claude Code with isolation, Claude Code for one-shot tasks, or a custom script for anything else.

### Extended Directives

**CLAW_TYPE** — Declares the base image and therefore the agent runner.

**AGENT** — Names the behavioral contract file. Bind-mounted read-only from the host. Filename follows the runner's convention.

**CONFIGURE** — Shell commands that run at container init, mutating base defaults into this Claw's setup. Tools like jq and sed for fine-grained changes. `claw-module enable/disable` for schema-aware cascading changes.

**CLLAMA** — The default judgment proxy baked into the image. Namespaced policy prefix, provider, model, then module declarations. Overridable per-deployment in claw-pod.yml, same as CMD is overridable by compose's command.

**INVOKE** — Invocation schedules. For simple runners and lightweight runners like Nanobot that use external cron, INVOKE entries trigger execution directly. For runners like OpenClaw that have their own internal scheduling, INVOKE manages the container lifecycle on a macro schedule while the runner handles micro-scheduling internally.

**PRIVILEGE** — Per-mode privilege levels. Worker gets root. Runtime drops to standard user. Lockdown restricts filesystem and network for quarantined Claws.

**TRACK** — Package manager interception. Wraps apt, pip, npm, cargo. Every install logged with context. Logs become redeployment recipes.

**PERSONA** — Imports a downloadable persona workspace from a registry.

**MODEL** — Binds named model slots to providers and models. The operator fills slots. The runner references them by name.

**ACT** — Worker-mode directives. Install packages, import knowledge, configure the runtime. Snapshot when done.

**SURFACE** — Declares what this Claw consumes. Volumes, queues, chat platforms, APIs, MCP services. Clawdapus resolves service references against expose blocks in the pod, assembles the skill map, and enforces service-side cllama requirements.

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

# cllama: the judgment proxy
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

# Communication surfaces
SURFACE volume://shared-cache       read-write
SURFACE queue://fleet/signals       subscribe
SURFACE discord://crypto-ops/general read-write
SURFACE service://company-crm       read-write
SURFACE http://master:9000/api      report

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

# cllama: judgment still applies regardless of runner weight
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
SURFACE queue://fleet/signals    subscribe
SURFACE service://market-scanner read-only

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

Personas are the content layer. The Clawfile is the infrastructure layer. The behavioral contract is the governance layer. cllama is the judgment layer. Independent and composable.

A persona contains an identity manifest (name, handles, bio, platform profiles), a memory store (interaction history, relationship graph), a knowledge base (domain documents, embeddings), a style fingerprint (vocabulary, sentence patterns, punctuation habits), behavioral patterns (timing, engagement preferences, topic affinities), and workspace state (scripts, tools, cached data accumulated during operation).

### Forking

Personas are forkable. Snapshot a running Claw that has accumulated memory and knowledge over months. Fork it. The fork inherits everything. Only the patched fields diverge. Deploy the fork with a different behavioral contract and you have two bots that share history and knowledge but differ in purpose and voice.

### Layer Independence

The behavioral contract controls purpose — bind-mounted, read-only, operator-written.

The persona controls identity — writable workspace, grows over time, snapshotable and forkable.

cllama controls judgment — versioned, identity-aware, swapped at invocation.

The Clawfile controls infrastructure — base image, runner, models, schedule, privileges, surfaces.

The bot can grow its persona. It cannot change its purpose. It cannot override the operator's judgment. It cannot alter its own infrastructure.

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

The Clawfile bakes defaults into the image. The claw-pod.yml overrides per-deployment. Same image, different judgment. Same image, different schedule. Same image, different surfaces — all without rebuilding.

### Mixed Clusters

A pod is not a collection of bots. It is a mixed cluster of cognitive and non-cognitive services. Regular Docker containers participate as first-class pod members via `describe` blocks — machine-readable self-description of what they do, what they produce, what they consume.

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
        - market-scanner: write
        - dashboard: read

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
        - discord://crypto-ops/general: read-write
        - http://market-scanner:8080/api: read
        - service://company-crm: read-write
      describe:
        role: "Original crypto market commentary"
        outputs: ["tweets", "threads", "discord posts"]
        inputs: ["market data", "timeline context"]
    networks:
      - internal
      - public-egress
    depends_on:
      - market-scanner

  # Plain Docker container — not a Claw, but a full pod member.
  market-scanner:
    image: custom/market-scanner:latest
    x-claw:
      surfaces:
        - volume://shared-cache: write
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

### Surfaces, Skill Maps, and Skill Mounts

Services expose. Claws consume. At pod startup, Clawdapus assembles a skill map for each Claw — the complete set of capabilities available based on surface access. MCP services self-describe via tool listing. Non-MCP services declare capabilities in their describe block.

```
$ claw skillmap crypto-crusher-0

Available capabilities for crypto-crusher-0:

  FROM market-scanner (http://market-scanner:8080/api):
    get_price            Current and historical token price data
    get_whale_activity   Large wallet movements in last N hours
    get_market_sentiment Aggregated fear/greed and social volume

  FROM company-crm (mcp, auto-discovered):
    lookup_customer      Find customer by name, email, or account ID
    create_ticket        Open support ticket linked to a customer
    get_deal_status      Current pipeline stage and value for any deal
    ⚠ requires cllama: policy/customer-data-access, policy/pii-gate

  FROM shared-cache (volume://shared-cache):
    read-write           File-based data exchange

  FROM fleet/signals (queue://fleet/signals):
    subscribe            Fleet coordination messages
```

The skill map is delivered via a read-only skill mount. Add a service, the skill map grows. Remove a service, it shrinks. No code changes. No retraining.

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

**Runner compatibility** — Clawdapus operates beneath agent runners and wraps them all identically. OpenClaw (430K+ lines), Nanobot (~4K lines), NanoClaw, Claude Code, custom scripts. Adoption is incremental — take an existing bot, containerize it in a Clawfile, add a behavioral contract, and you have a managed Claw.

**cllama module ecosystem** — The standard defines the module interface but doesn't restrict what modules do. Organizations maintain their own judgment stacks under their own policy namespace.

**Persona marketplace** — Personas are publishable, shareable, forkable artifacts. Download someone else's identity and knowledge. Attach your own purpose. Wrap it in your own judgment. Deploy on your own infrastructure.

---

## XIII. Reasoning

**Why extend Docker?** The hard problems of containerization are solved. Extending Docker inherits decades of infrastructure investment.

**Why do Clawfiles compile to Dockerfiles?** Because no custom build engine survives contact with production. `claw build` is a preprocessor, not a build system. The output is a standard Dockerfile. `docker build` does the rest.

**Why is the behavioral contract a bind mount?** Purpose changes faster than infrastructure. The bind mount means the operator changes purpose at the speed of editing a text file. It also means purpose is outside the blast radius of a compromised container.

**Why is cllama a separate cognitive layer?** Think twice, act once. A reasoning model cannot be its own judge. Prompt-level guardrails are visible to the model and subject to the same reasoning they're trying to constrain.

**Why track mutations instead of preventing them?** Prevention kills capability. Untracked mutation is drift. Tracked mutation is evolution.

**Why defined surfaces?** Not just to restrict communication — to give the operator a topology map *and* to give the bots a skill map. Surfaces are dual-purpose.

**Why do services declare cllama requirements?** Because the service knows its own risk profile. The Claw's cllama governs what the bot says. The service's required cllama governs what the bot does — to the service's data, through the service's API.

**Why are lightweight runners first-class?** Because governance is not proportional to complexity. A 4,000-line agent with brokerage access needs the same purpose contract and judgment proxy as a 430,000-line agent OS.

---

## XIV. What This Is Not

Clawdapus is not an agent framework. It does not define how agents reason or execute.

Clawdapus is not a bot-building tool. It helps you deploy, govern, monitor, and evolve bots that already exist.

Clawdapus is infrastructure for bots the way Docker is infrastructure for applications. The layer below the framework. The layer above the operating system. Where deployment meets governance.

---

*Clawdapus is open architecture. This manifesto defines the standard. Implementations follow.*

---

## XV. Implementation Notes

*This section will be updated as architectural decisions are made.*

### Build/Runtime Split (decided Feb 2026)

Clawdapus uses a hybrid approach:

- **Build phase** — `claw build` transpiles the Clawfile into a standard Dockerfile and calls `docker build`. Output is an inspectable build artifact, not something you edit directly — like a compiled binary.
- **Runtime phase** — `clawdapus` drives container lifecycle, injects environment, mediates surfaces, runs cllama. Always go through `claw` commands at runtime.

This gives inspectable builds and full runtime control. The `x-claw` extensions in claw-pod.yml are parsed by Clawdapus, not by Docker Compose directly.
