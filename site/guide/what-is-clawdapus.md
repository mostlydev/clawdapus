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
4. **Drift is an open metric** -- the governance proxy emits independent telemetry rather than trusting a bot's self-report. What counts as "drift" is operator-defined and computed outside the agent; Clawdapus ships the verifiable record, not a built-in score.
5. **Surfaces are declared** -- topology for operators, capability discovery for bots. The proxy enforces cognitive boundaries.
6. **Claws are users** -- standard credentials; the proxy governs intent, the service's own auth governs execution.
7. **Compute is a privilege** -- the operator assigns models and schedules; the proxy enforces budgets and rate limits.
8. **Think twice, act once** -- a reasoning model cannot be its own judge. Governance runs in a separate process.
9. **Memory survives the container (and the runner)** -- session history is captured at the proxy boundary and persisted outside the runtime directory. Swap the runtime without losing the mind. The **ambient memory plane** is live: pluggable memory services declared via `claw.describe`, compiled by `claw up`, and orchestrated by cllama. Infrastructure recalls derived context before each inference turn and retains after each response. The agent does not manage its own long-term memory. Infrastructure does.

## Master Claw

Clawdapus is designed for autonomous fleet governance. The operator writes the Clawfile and sets the budgets, but day-to-day oversight can be delegated to a **Master Claw** -- the "Top Octopus."

The Master Claw is an AI governor running inside the pod, tasked with reading proxy telemetry and making executive decisions. `x-claw.master` auto-wires the plumbing today: it injects a `claw-api` service and hands the designated governor a scoped bearer token and `CLAW_API_URL`, so it can read fleet telemetry and act through an authenticated, scope-checked API. The `cllama` governance proxy is its sensory organ -- a credential firewall and telemetry choke point for compiled model, context, memory, and tool mediation. What the governor *does* with that telemetry -- shifting budgets, quarantining agents, promoting recipes -- is operator-defined policy (and recipe promotion is still on the roadmap), not built-in enforcement.

In enterprise deployments, this forms a **Hub-and-Spoke Governance Model**. Multiple pods across different zones run their own local `cllama` proxies as firewalls, while a single Master Claw ingests telemetry from all of them to autonomously manage the entire neural fleet.

Read the full design in the [Manifesto](/manifesto).

## Fleet Visibility

cllama emits normalized telemetry for every turn it proxies -- requests, responses, interventions, and managed tool rounds. **`claw audit`** aggregates that ledger into a per-claw summary for the current pod, an independent record of what each bot did, what it cost, and where the proxy intervened. Because the provider keys live with the proxy and every call flows through it, this record is authoritative, not self-reported.

```bash
$ claw audit --since 24h

Pod: research-pod
Events: 460
CLAW        REQ  RESP  ERR  INT  TOOLS  TOOL_ERR  TOK_IN  TOK_OUT  COST_USD  MODELS
analyst     142  142   0    3    18     0         284011  39402    1.8742    anthropic/claude-sonnet-4
researcher  88   87    1    0    5      0         151233  20118    0.9931    anthropic/claude-sonnet-4

Totals: req=230 resp=229 err=1 int=3 tools=23/23 tokens=435244/59520 cost=$2.8673
```

The `INT` column counts interventions -- turns where the proxy modified or dropped output. Filter with `--claw <id>`, `--type <event>`, or `--since <duration>`, and add `--json` for machine-readable output.

**Drift scoring is deliberately not built in.** Defining behavioral drift is organization-specific, so Clawdapus emits the raw telemetry and leaves the scoring to a swappable proxy implementation or a Master Claw policy. There is no drift score in the reference proxy and no `DRIFT` column in `claw audit` -- the telemetry is the open metric.

## Recipe Promotion (roadmap)

Bots install things -- that is how real work gets done. The planned **recipe promotion** loop turns ad hoc mutation into permanent infrastructure through a human gate: a `TRACK` directive wraps package managers (apt, pip, npm) to log every mutation, `claw recipe` reviews the accumulated changes, and `claw bake` promotes the approved ones into the base image. Tracked mutation is evolution; untracked mutation is drift.

This loop is **designed but not yet implemented** -- the `TRACK`/`recipe`/`bake` surface does not ship today. See [How It Fits Together](/guide/architecture#what-ships-today-vs-roadmap) for the current shipped-vs-roadmap picture.

## Current Status

[How It Fits Together](/guide/architecture) maps every capability to what ships today versus what is on the roadmap, and the [Changelog](/changelog) has the release-by-release history. The canonical reconciliation lives in [`docs/PROJECT_STATE.md`](https://github.com/mostlydev/clawdapus/blob/master/docs/PROJECT_STATE.md).

## Next Steps

Read the full vision in the [Manifesto](/manifesto) or jump straight to the [Quickstart](/guide/quickstart) to get a pod running in five minutes.
