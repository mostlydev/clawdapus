---
layout: home

hero:
  name: Clawdapus
  text: Docker on Rails for Claws
  tagline: Infrastructure-layer governance for AI agent containers. The layer below the framework, where deployment meets governance.
  image:
    src: /clawdapus-glyph.png
    alt: Clawdapus
  actions:
    - theme: brand
      text: Get Started
      link: /guide/quickstart
    - theme: alt
      text: View on GitHub
      link: https://github.com/mostlydev/clawdapus

features:
  - icon: "\U0001F419"
    title: Untrusted by Design
    details: Every agent is a container — reproducible, inspectable, diffable, and killable. Purpose is bind-mounted read-only. Survives full container compromise.
  - icon: "\U0001F512"
    title: Credential Starvation
    details: The cllama governance proxy holds the real API keys. Agents get bearer tokens. No credentials means no bypass — every LLM call flows through the proxy.
  - icon: "\U0001F433"
    title: Extends Docker
    details: Clawfile extends Dockerfile. claw-pod.yml extends docker-compose.yml. Eject anytime — you still have working OCI images and compose files.
  - icon: "\U0001F3AD"
    title: 7 Runner Drivers
    details: "OpenClaw, Hermes, Nanobot, PicoClaw. Pick your runtime. Same governance layer wraps them all."
  - icon: "\U0001F4E1"
    title: Social Topology
    details: "HANDLE declares platform identity. Every agent's Discord/Telegram/Slack IDs are broadcast pod-wide. Services can @mention bots without hardcoding."
  - icon: "\U0001F9E0"
    title: Master Claw
    details: "Delegate fleet oversight to an in-pod AI governor. x-claw.master auto-wires a claw-api service and a scoped bearer token, so the governor reads proxy telemetry and acts through an authenticated, scope-checked API."
---

## What It Looks Like

### The Image — `Clawfile`

An extended Dockerfile. Any valid Dockerfile is a valid Clawfile.

```dockerfile
FROM openclaw:latest

CLAW_TYPE openclaw
AGENT AGENTS.md

MODEL primary openrouter/anthropic/claude-sonnet-5
CLLAMA passthrough

HANDLE discord
INVOKE 15 8 * * 1-5  pre-market

SURFACE service://trading-api
SURFACE volume://shared-research read-write
```

### The Deployment — `claw-pod.yml`

An extended docker-compose.yml. Services inherit pod-level defaults.

```yaml
x-claw:
  pod: trading-desk
  master: octopus
  cllama-defaults:
    proxy: [passthrough]
    env:
      OPENROUTER_API_KEY: "${OPENROUTER_API_KEY}"
  surfaces-defaults:
    - "service://trading-api"
    - "volume://shared-research read-write"

services:
  analyst:
    image: trading-desk-analyst:latest
    build:
      context: ./agents/analyst
    x-claw:
      agent: ./agents/analyst/AGENTS.md
      handles:
        discord:
          id: "${ANALYST_DISCORD_ID}"
          username: "analyst"
```

### Five Minutes to Running

```bash
curl -sSL https://raw.githubusercontent.com/mostlydev/clawdapus/master/install.sh | sh
git clone https://github.com/mostlydev/clawdapus.git
cd clawdapus/examples/quickstart
cp .env.example .env   # add your keys
claw pull      # pinned runtime infra + registry services
claw build     # local build: services
claw up -d     # compile + launch
claw health    # ✓ all healthy
claw down      # tear down when you're done
```

The everyday operator loop is `claw pull`, `claw build`, `claw up`, `claw down`.

## Core Principles

1. **Purpose is sacred** — contract is bind-mounted read-only; survives full container compromise
2. **The workspace is alive** — bots install and adapt; mutations are tracked and promotable
3. **Configuration is code** — every deviation from defaults is diffable
4. **Drift is an open metric** — independent audit via the governance proxy, not self-report
5. **Surfaces are declared** — topology for operators; capability discovery for bots. The proxy enforces cognitive boundaries.
6. **Claws are users** — standard credentials; the proxy governs intent, the service's own auth governs execution
7. **Compute is a privilege** — operator assigns models and schedules; proxy meters usage
8. **Think twice, act once** — a reasoning model cannot be its own judge
9. **Memory survives the container (and the runner)** — session history is captured at the proxy boundary and persisted outside the runtime directory. Swap the runtime without losing the mind. The **ambient memory plane** is live: pluggable memory services declared via `claw.describe`, compiled by `claw up`, and orchestrated by cllama. Infrastructure recalls derived context before each inference turn and retains after each response — automatically, without the agent asking.
