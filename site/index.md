---
layout: home

hero:
  name: Clawdapus
  text: Docker on Rails for Claws
  tagline: Infrastructure-layer governance for AI agent containers. The layer below the framework, where deployment meets governance.
  image:
    src: /clawdapus.png
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
    details: "OpenClaw, Hermes, NanoClaw, Nanobot, PicoClaw, NullClaw, MicroClaw. Pick your runtime. Same governance layer wraps them all."
  - icon: "\U0001F4E1"
    title: Social Topology
    details: "HANDLE declares platform identity. Every agent's Discord/Telegram/Slack IDs are broadcast pod-wide. Services can @mention bots without hardcoding."
  - icon: "\U0001F9E0"
    title: Master Claw
    details: Delegate fleet oversight to an AI governor. It reads proxy telemetry and autonomously manages budgets, quarantines, and recipe promotions.
---

## What It Looks Like

### The Image — `Clawfile`

An extended Dockerfile. Any valid Dockerfile is a valid Clawfile.

```dockerfile
FROM openclaw:latest

CLAW_TYPE openclaw
AGENT AGENTS.md

MODEL primary openrouter/anthropic/claude-sonnet-4
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
  tiverton:
    image: trading-desk-tiverton:latest
    build:
      context: ./agents/tiverton
    x-claw:
      agent: ./agents/tiverton/AGENTS.md
      handles:
        discord:
          id: "${TIVERTON_DISCORD_ID}"
          username: "tiverton"
```

### Five Minutes to Running

```bash
curl -sSL https://raw.githubusercontent.com/mostlydev/clawdapus/master/install.sh | sh
git clone https://github.com/mostlydev/clawdapus.git
cd clawdapus/examples/quickstart
cp .env.example .env   # add your keys
claw build -t quickstart-assistant:latest ./agents/assistant
claw up -f claw-pod.yml -d
claw health -f claw-pod.yml  # ✓ all healthy
```

## Core Principles

1. **Purpose is sacred** — contract is bind-mounted read-only; survives full container compromise
2. **The workspace is alive** — bots install and adapt; mutations are tracked and promotable
3. **Configuration is code** — every deviation from defaults is diffable
4. **Drift is an open metric** — independent audit via the governance proxy, not self-report
5. **Compute is a privilege** — operator assigns models and schedules; proxy enforces budgets
6. **Think twice, act once** — a reasoning model cannot be its own judge
