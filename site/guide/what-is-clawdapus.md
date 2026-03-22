# What is Clawdapus?

Every agent framework answers the same question: *how do I make agents collaborate on tasks?* Swarm coordinates handoffs. CrewAI assigns roles. LangGraph builds execution graphs. They are all application-layer orchestration systems built on a shared assumption: **the agent is a trusted process.**

That assumption holds for autonomous assistants. It breaks the moment you deploy bots that operate publicly -- posting to feeds, replying on Discord, executing trades, burning provider tokens -- **as a persistent presence with a persistent identity.**

Clawdapus starts from the opposite premise. **The agent is an untrusted workload.** It is a container that can think, and like any container, it must be reproducible, inspectable, diffable, and killable. Its purpose is not its own to define. Its schedule is not its own to set. But within those boundaries, it is alive -- it can install tools, build scripts, modify its workspace, and adapt to its environment.

> Swarm is for agents that work *for* you. Clawdapus is for bots that work *as* you. Different trust model. Different stack.

## The Docker Analogy

Clawdapus is infrastructure for bots the way Docker is infrastructure for applications. The `Clawfile` extends the `Dockerfile`. The `claw-pod.yml` extends `docker-compose.yml`. Extended directives live in namespaces Docker already ignores. If you decide to eject, you still have working OCI images and a working compose file.

The `claw` CLI maps directly to what you already know:

| Clawdapus | Docker equivalent | Purpose |
|-----------|------------------|---------|
| `claw init` | `docker init` + project templating | Scaffold a canonical-by-default project layout |
| `claw agent add` | _(none)_ | Add agents while preserving existing layout |
| `Clawfile` | `Dockerfile` | Build an immutable agent image |
| `claw-pod.yml` | `docker-compose.yml` | Run a governed agent fleet |
| `claw build` | `docker build` | Transpile + build OCI image |
| `claw up` | `docker compose up` | Enforce + deploy |

Any valid Dockerfile is a valid Clawfile. Any valid `docker-compose.yml` is a valid `claw-pod.yml`. You are always one `docker compose` command away from running your stack without Clawdapus.

## What It Is NOT

Clawdapus is **not an agent framework**. It does not define how agents reason, plan, or execute code. It supports seven different runner types today -- OpenClaw, Hermes, NanoClaw, Nanobot, PicoClaw, NullClaw, MicroClaw -- and treats them all the same way.

Clawdapus is **not a bot-building tool**. It helps you deploy, govern, monitor, and evolve bots that already exist. You bring the agent; Clawdapus brings the infrastructure that makes it safe to run in production.

It is the layer below the framework. The layer above the operating system.

## Core Principles

1. **Purpose is sacred** -- the behavioral contract is bind-mounted read-only and survives full container compromise.
2. **The workspace is alive** -- bots install and adapt; mutations are tracked and promotable through a human gate.
3. **Configuration is code** -- every deviation from defaults is diffable.
4. **Drift is an open metric** -- behavioral drift is scored independently via the governance proxy, not self-reported.
5. **Surfaces are declared** -- topology for operators, capability discovery for bots. The proxy enforces cognitive boundaries.
6. **Claws are users** -- standard credentials; the proxy governs intent, the service's own auth governs execution.
7. **Compute is a privilege** -- the operator assigns models and schedules; the proxy enforces budgets and rate limits.
8. **Think twice, act once** -- a reasoning model cannot be its own judge. Governance runs in a separate process.

## Current Status

**v0.3.2 released** -- [download](https://github.com/mostlydev/clawdapus/releases/tag/v0.3.2)

| Phase | Status |
|-------|--------|
| Phase 1 -- Clawfile parser + build | Done |
| Phase 2 -- Driver framework + pod runtime + OpenClaw + volume surfaces | Done |
| Phase 3 -- Surface manifests, service skills, CLAWDAPUS.md | Done |
| Phase 3.5 -- HANDLE directive + social topology (Discord, Telegram, Slack) | Done |
| Phase 3.6 -- INVOKE scheduling + Discord config wiring | Done |
| Phase 3.7 -- Social topology: mentionPatterns, allowBots, peer handles | Done |
| Phase 3.8 -- Channel surface bindings | Done |
| Phase 4 -- Governance proxy integration + credential starvation | Done |
| Phase 4.5 -- Interactive `claw init` and `claw agent add` | Done |
| Phase 4.7 -- Nanobot + PicoClaw + NullClaw + MicroClaw drivers | Done |
| Phase 4.8 -- Hermes driver + shared helper extraction | Done |
| Phase 4.9 -- Peer handles, mention safety, healthcheck passthrough | Done |
| Phase 5 -- Fleet governance: Master Claw, telemetry, context feeds | Design |
| Phase 6 -- Recipe promotion + worker mode | Planned |

## Next Steps

Read the full vision in the [Manifesto](/manifesto) or jump straight to the [Quickstart](/guide/quickstart) to get a pod running in five minutes.
