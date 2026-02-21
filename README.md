# Clawdapus

**Infrastructure-layer governance for AI agent containers.**

`claw` is to agent fleets what `docker compose` is to service fleets — the layer below the framework, where deployment meets governance. Agents are untrusted workloads. Clawdapus enforces that.

---

## The Problem

Every agent framework answers the same question: how do I make agents collaborate? Swarm, CrewAI, LangGraph — all application-layer orchestration built on a shared assumption: **the agent is a trusted process.**

That assumption breaks the moment you deploy bots that operate publicly — on Discord, posting to feeds, managing customer data, executing trades. You need:

- **Reproducible builds** — what's running in prod, exactly?
- **Immutable purpose** — the bot can't rewrite its own instructions, even if fully compromised
- **Auditable configuration** — what changed, when, and why
- **Independent judgment** — a second cognitive layer that evaluates what the bot wants to do before it does it
- **Observable drift** — is the bot still behaving as intended?

Clawdapus provides the infrastructure primitives for all of this — without replacing the runner.

> Swarm is for agents that work *for* you. Clawdapus is for bots that work *as* you.

---

## How It Works

Clawdapus extends two formats you already know:

| Clawdapus | Docker equivalent | Purpose |
|-----------|------------------|---------|
| `Clawfile` | `Dockerfile` | Build an immutable agent image |
| `claw-pod.yml` | `docker-compose.yml` | Run a governed agent fleet |
| `claw build` | `docker build` | Transpile + build OCI image |
| `claw compose up` | `docker compose up` | Enforce + deploy |

Any valid Dockerfile is a valid Clawfile. Any valid `docker-compose.yml` is a valid `claw-pod.yml`. Extended directives live in namespaces Docker already ignores. Eject from Clawdapus anytime — you still have a working OCI image and a working compose file.

---

## A Clawfile

```dockerfile
FROM openclaw:latest

CLAW_TYPE openclaw
AGENT AGENTS.md                   # behavioral contract — bind-mounted read-only

# Identity
PERSONA registry.claw.io/personas/crypto-crusher:v2

# Models the operator assigns — bot doesn't choose its own
MODEL primary   gpt-4o
MODEL summarizer llama-3.2-3b

# Optional: bidirectional LLM judgment proxy
CLLAMA cllama-org-policy/xai/grok-4.1-fast \
  tone/sardonic-but-informed \
  obfuscation/humanize-v2 \
  purpose/engagement-farming

# Track every package install — becomes a redeployment recipe
TRACK apt pip npm

# Schedule (system cron, not bot-modifiable)
INVOKE 0 9 * * *     tweet-cycle
INVOKE 0,30 * * * *  engagement-sweep
INVOKE 0 */4 * * *   drift-check

# Public identity on platforms
HANDLE discord

# What this bot can talk to
SURFACE channel://discord
SURFACE volume://shared-cache       read-write
SURFACE service://market-scanner

# Operator-provided usage guides for surfaces
SKILL skills/crypto-feeds.md
```

`claw build` transpiles this to a standard Dockerfile. `docker build` does the rest. The output is a plain OCI image — runnable anywhere without `claw`.

---

## The Anatomy of a Claw

Every running Claw has four independent layers:

```
┌─────────────────────────────────────────────────┐
│  Behavioral Contract  (read-only bind mount)     │
│  AGENTS.md / CLAUDE.md — purpose, on the host   │
│  Survives full container compromise              │
├─────────────────────────────────────────────────┤
│  Runner (internal execution)                    │
│  OpenClaw · Nanobot · NanoClaw · Claude Code    │
│  Any script or framework                        │
├─────────────────────────────────────────────────┤
│  Persona (identity workspace)                   │
│  Memory · history · style · knowledge           │
│  Mutable, snapshotable, forkable OCI artifact   │
├─────────────────────────────────────────────────┤
│  cllama (optional judgment proxy)               │
│  Intercepts prompts outbound + responses inbound│
│  Runner never knows it's there                  │
└─────────────────────────────────────────────────┘
```

**The contract is immutable from inside the container.** If the container is fully compromised — root access, total workspace control — the behavioral contract is still untouchable because it lives on the host.

**The runner is independent from the persona.** Swap runners without touching identity. Fork a persona without changing purpose. Add or remove the judgment layer without rebuilding anything.

---

## cllama: The Judgment Proxy

When a reasoning model tries to govern itself, the guardrails are part of the same cognitive process they're trying to constrain. The model can reason around them.

`cllama` is a bidirectional LLM proxy — a **separate process, running a separate model** — that sits between the runner and the LLM provider:

- **Outbound:** intercepts prompts before the LLM sees them. Gates what the runner is allowed to *ask*.
- **Inbound:** intercepts responses before the runner sees them. Rewrites, adjusts, or drops output that violates policy.

The runner thinks it's talking directly to the LLM. It never sees `cllama`. The real provider API keys live in the sidecar — the runner never sees those either.

```
CLLAMA cllama-org-policy/xai/grok-4.1-fast \
  purpose/engagement-farming \     ← does this serve the goal?
  policy/no-financial-advice \     ← hard rails
  tone/sardonic-but-informed \     ← voice consistency
  obfuscation/humanize-v2          ← behavioral variance
```

Pipeline: `raw cognition → purpose → policy → tone → obfuscation → world`.

Each stage is independently versioned, independently swappable, rollback-capable. Update your corporate voice policy once — realign an entire fleet without touching a single behavioral contract.

`cllama` is **optional**. Claws run with config-injection-only enforcement when no `CLLAMA` directive is present.

---

## A Fleet: claw-pod.yml

```yaml
# claw-pod.yml — crypto-ops

x-claw:
  pod: crypto-ops
  master: fleet-master

volumes:
  shared-cache:
    x-claw:
      access:
        - crypto-crusher-*: read-write
        - dashboard: read-only

services:

  crypto-crusher:
    build:
      context: ./crusher
      dockerfile: Clawfile
    x-claw:
      agent: ./agents/crusher-agents.md
      cllama: cllama-org-policy/xai/grok-4.1-fast tone/sardonic obfuscation/humanize-v2
      count: 3                         # generates crypto-crusher-0, -1, -2
      handles:
        discord:
          id: "123456789"
          username: "crypto-crusher-bot"
          guilds:
            - id: "111222333"
              name: "Crypto Ops HQ"
              channels:
                - id: "987654321"
                  name: "bot-commands"
      surfaces:
        - volume://shared-cache: read-write
        - channel://discord
        - service://market-scanner
    environment:
      DISCORD_TOKEN: ${DISCORD_TOKEN}

  # Plain Docker container — not a Claw, but a full pod member
  market-scanner:
    image: custom/market-scanner:latest
    x-claw:
      expose:
        protocol: rest
        port: 8080
        discover: auto               # generates skill file automatically
```

`claw compose up` parses `x-claw` blocks, enforces config, assembles skill maps, emits a clean `compose.generated.yml`, and hands off to `docker compose`. No custom runtime. No proprietary scheduler.

---

## Social Topology: The HANDLE Directive

A trading bot needs human approval before executing a large position. A Rails API in the pod monitors positions. How does it know which Discord user to ping?

```bash
# Available in every pod service automatically:
CLAW_HANDLE_CRYPTO_CRUSHER_DISCORD_ID=123456789
CLAW_HANDLE_CRYPTO_CRUSHER_DISCORD_GUILDS=111222333
CLAW_HANDLE_CRYPTO_CRUSHER_DISCORD_JSON={"id":"123456789",...}
```

Clawdapus aggregates agent platform identities and broadcasts them as environment variables into every service in the pod. No hardcoding. No service discovery. Just env vars injected at startup.

---

## Surfaces and Skill Maps

Every Claw receives a `CLAWDAPUS.md` — always bootstrapped into its context — describing its environment:

```markdown
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
- **Host:** market-scanner:8080
- **Skill:** skills/surface-market-scanner.md

## Skills
- skills/surface-market-scanner.md — Market Scanner API (discovered via OpenAPI)
- skills/surface-discord.md — Discord channel constraints
- skills/handle-discord.md — Discord identity and guild membership
- skills/crypto-feeds.md — Crypto data feed tools (operator-provided)
```

Skills come from three sources, merged automatically:
1. **SKILL directives** — operator-provided guides, mounted read-only
2. **Service-generated** — queried at startup from MCP listings, OpenAPI specs, or static `describe` blocks
3. **Fallback stubs** — generated when no service description exists

Add a service to the pod: skill map updates automatically. No code changes.

```bash
$ claw skillmap crypto-crusher-0

  FROM market-scanner (service://market-scanner):
    get_price            Current and historical token price data
    get_whale_activity   Large wallet movements in last N hours
    [discovered via OpenAPI → skills/surface-market-scanner.md]

  FROM shared-cache (volume://shared-cache):
    read-write mount at /mnt/shared-cache

  OPERATOR SKILLS:
    skills/crypto-feeds.md — Crypto data feed tools
```

---

## Fleet Visibility

```bash
$ claw ps

TENTACLE          STATUS    CLLAMA    DRIFT
crypto-crusher-0  running   healthy   0.02
crypto-crusher-1  running   healthy   0.04
crypto-crusher-2  running   WARNING   0.31
fleet-master      running   healthy   0.00

$ claw audit crypto-crusher-2 --last 24h

14:32  tweet-cycle       OUTPUT MODIFIED by cllama:policy  (financial advice detected)
18:01  engagement-sweep  OUTPUT DROPPED by cllama:purpose  (off-strategy)
22:15  tweet-cycle       OUTPUT MODIFIED by cllama:tone    (voice inconsistency)
```

Drift is independently scored — not self-reported. A separate process audits outputs against the contract and cllama policy. High drift triggers quarantine and operator escalation.

---

## Recipe Promotion

Bots install things. That's how real work gets done. Clawdapus tracks every mutation:

```bash
$ claw recipe crypto-crusher-0 --since 7d

Suggested additions to openclaw:crypto-crusher:
  pip: tiktoken==0.7.0, trafilatura>=0.9
  apt: jq
  files:
    scripts/scraper.py  → COPY into /home/claw/scripts/

Apply?  claw bake crypto-crusher --from-recipe latest
```

Ad hoc evolution becomes permanent infrastructure through a human gate.

---

## Runner Support

Clawdapus wraps all runners identically:

| Claw Type | Runner | Contract | Weight |
|-----------|--------|----------|--------|
| `openclaw` | Full agent OS — gateway, channels, skills, multi-model | AGENTS.md | Heavy |
| `nanobot` | Lean agent loop — MCP-native, skills, persistent memory | config.json | Light |
| `nanoclaw` | Claude Code in isolated container groups | CLAUDE.md | Medium |
| `claude-code` | Single-execution runner | CLAUDE.md | One-shot |
| `custom` | Any script or framework | Runner-defined | Any |

Governance is not proportional to complexity. A 4,000-line agent with brokerage access needs the same purpose contract and judgment proxy as a 430,000-line agent OS.

---

## Core Principles

1. **Purpose is sacred** — behavioral contract is bind-mounted read-only; survives full container compromise
2. **The workspace is alive** — bots can install, build, and adapt; mutations are tracked and promotable
3. **Configuration is code** — every deviation from base defaults is documented and diffable
4. **Drift is quantifiable** — independent audit, not self-report
5. **Surfaces are declared** — topology visibility for operators; capability discovery for bots
6. **Claws are users** — standard credentials, service's own auth governs access
7. **Compute is a privilege** — operator assigns models, budgets, and schedules; bot doesn't choose
8. **Think twice, act once** — a reasoning model cannot be its own judge

---

## Status

**Active development — pre-release.**

| Phase | Status |
|-------|--------|
| Phase 1 — Clawfile parser + build (`claw build`, `claw inspect`) | **Done** |
| Phase 2 — Driver framework + pod runtime + OpenClaw + volume surfaces | **Done** |
| Phase 3 — Surface manifests, service skills, CLAWDAPUS.md | **Done** |
| Phase 3.5 — HANDLE directive + social topology projection | **Done** |
| Phase 3 — Channel surface bindings (`channel://discord`) | In progress |
| Phase 4 — cllama sidecar + policy pipeline | Planned |
| Phase 5 — Drift scoring + fleet governance | Planned |
| Phase 6 — Recipe promotion + worker mode | Planned |

---

## Examples

| Example | What it shows |
|---------|---------------|
| [`examples/openclaw/`](./examples/openclaw/) | Single OpenClaw agent with Discord handle, skill emit, and service surface |
| [`examples/multi-claw/`](./examples/multi-claw/) | Two agents sharing a volume surface with different access modes |
| [`examples/trading-desk/`](./examples/trading-desk/) | Seven isolated agents — coordinator, four traders, pump trader, systems monitor — sharing a research volume and coordinating via Discord. Each agent runs its own OpenClaw instance in its own container. Self-describing API service via `claw.skill.emit`. |

---

## Quickstart

```bash
# Prerequisites: Docker, docker compose, Go toolchain

go install ./cmd/claw

claw doctor                                              # verify environment
claw build -t my-agent examples/openclaw               # build an agent image
claw inspect my-agent                                  # inspect resolved config
claw compose -f examples/openclaw/claw-pod.yml up -d   # run a governed fleet
claw compose -f examples/openclaw/claw-pod.yml ps      # status + health
claw compose -f examples/openclaw/claw-pod.yml logs gateway
claw compose -f examples/openclaw/claw-pod.yml down
```

`claw compose` emits `compose.generated.yml` next to the pod file — inspectable, debuggable, not hand-edited.

---

## Documentation

- [`MANIFESTO.md`](./MANIFESTO.md) — vision, principles, full architecture (source of truth)
- [`docs/plans/2026-02-18-clawdapus-architecture.md`](./docs/plans/2026-02-18-clawdapus-architecture.md) — implementation plan, invariants, CLI surface
- [`docs/decisions/001-cllama-transport.md`](./docs/decisions/001-cllama-transport.md) — ADR: cllama as sidecar HTTP proxy
- [`docs/decisions/002-runtime-authority.md`](./docs/decisions/002-runtime-authority.md) — ADR: compose-only lifecycle authority

## AI Agent Guidance

The repository includes a skill guide at [`skills/clawdapus/SKILL.md`](./skills/clawdapus/SKILL.md).

Install for Claude Code:

```bash
cp -r skills/clawdapus ~/.claude/skills/
```

## Contributing

Start with [`MANIFESTO.md`](./MANIFESTO.md) and align with the plan files before contributing.
