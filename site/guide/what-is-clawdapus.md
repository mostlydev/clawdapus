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
| `claw pull` | `docker compose pull` + `docker build --pull --no-cache` | Fetch pinned infra, pod registry images, and built-in local runner base aliases |
| `claw build` | `docker build` | Transpile + build OCI image, or build all pod `build:` services |
| `claw up` | `docker compose up` | Enforce + deploy; authoritative on missing images |

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
9. **Memory survives the container (and the runner)** -- session history is captured at the proxy boundary and persisted outside the runtime directory. Swap the runtime without losing the mind. The **ambient memory plane** is live: pluggable memory services declared via `claw.describe`, compiled by `claw up`, and orchestrated by cllama. Infrastructure recalls derived context before each inference turn and retains after each response. The agent does not manage its own long-term memory. Infrastructure does.

## Master Claw

Clawdapus is designed for autonomous fleet governance. The operator writes the Clawfile and sets the budgets, but day-to-day oversight can be delegated to a **Master Claw** -- the "Top Octopus."

The Master Claw is an AI governor running inside the pod, tasked with reading proxy telemetry and making executive decisions. The `cllama` governance proxy is its sensory organ -- a passive firewall that enforces hard rules and emits structured telemetry. The Master Claw is the brain that reads that telemetry and autonomously manages the fleet: shifting budgets, promoting recipes, or quarantining drifting agents.

In enterprise deployments, this forms a **Hub-and-Spoke Governance Model**. Multiple pods across different zones run their own local `cllama` proxies as firewalls, while a single Master Claw ingests telemetry from all of them to autonomously manage the entire neural fleet.

Read the full design in the [Manifesto](/manifesto).

## Fleet Visibility

Behavioral drift is scored independently -- not self-reported. The structured logs from `cllama` provide raw telemetry today; the `claw audit` command and drift scoring are Phase 5 design.

```bash
$ claw ps

TENTACLE          STATUS    CLLAMA    DRIFT
crypto-crusher-0  running   healthy   0.02
crypto-crusher-1  running   healthy   0.04
crypto-crusher-2  running   WARNING   0.31

$ claw audit crypto-crusher-2 --last 24h

14:32  tweet-cycle       OUTPUT MODIFIED by cllama:policy  (financial advice detected)
18:01  engagement-sweep  OUTPUT DROPPED by cllama:purpose  (off-strategy)
```

The `DRIFT` column in `claw ps` and the intervention history in `claw audit` give operators a verifiable record of exactly what the bot tried to do versus what it was allowed to do. The drift score is computed by the proxy implementation, not by the agent itself.

## Recipe Promotion

Bots install things. That is how real work gets done. The `TRACK` directive wraps package managers (apt, pip, npm) to log every mutation. `claw recipe` reviews those mutations. `claw bake` promotes them to the permanent base image.

```bash
$ claw recipe crypto-crusher-0 --since 7d

  pip: tiktoken==0.7.0, trafilatura>=0.9
  apt: jq
  files: scripts/scraper.py

Apply?  claw bake crypto-crusher --from-recipe latest
```

Tracked mutation is evolution. Untracked mutation is drift. Ad hoc capability-building becomes permanent infrastructure through a human gate. This is Phase 6, planned.

## Current Status

See the [Changelog](/changelog) for release notes and the full phase roadmap.

## Next Steps

Read the full vision in the [Manifesto](/manifesto) or jump straight to the [Quickstart](/guide/quickstart) to get a pod running in five minutes.
