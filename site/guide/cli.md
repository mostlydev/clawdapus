# CLI Commands

The `claw` CLI is the single entry point for building, deploying, and managing governed agent pods. The everyday operator loop is `claw pull`, `claw build`, `claw up`, `claw down`: pull pinned/runtime images and runner bases, build local services, compile and launch, then tear the pod back down.

## Global Flags

| Flag | Short | Description |
|------|-------|-------------|
| `--file <path>` | `-f` | Path to `claw-pod.yml`. Locates `compose.generated.yml` next to it. |
| `--version` | | Print the CLI version and exit. |

The `-f` flag is a persistent flag on the root command, so lifecycle commands inherit it. When omitted, commands look for `compose.generated.yml` in the current directory.

---

## claw pull

Fetch pinned infra images, pod registry images, and built-in local runner bases.

```bash
claw pull [pod-file-or-clawfile] [--no-runners]
```

Without a pod file, `claw pull` fetches the binary's pinned runtime infra images and refreshes managed runner aliases already tagged locally. With a pod file, it narrows to the runtime infra that pod actually needs, pulls every service image that does **not** declare `build:`, and refreshes runner bases used by `build:` services. With a Clawfile path, it refreshes the runner base required by that Clawfile.

| Flag | Description |
|------|-------------|
| `--no-runners` | Skip local runner base refresh. Use this for a fast pinned-infra and registry-image pull. |

**What it does:**

1. Checks the binary's pinned infra image refs.
2. Pulls missing infra images.
3. If a pod is present, pulls registry-backed service images.
4. Refreshes built-in local runner bases with `docker build --pull --no-cache`, unless `--no-runners` is set.
5. Leaves pod service images with `build:` to `claw build`.

**Examples:**

```bash
# Pull without refreshing runner bases
claw pull --no-runners

# Pull infra, registry services, and runner bases for another pod
claw pull ./examples/quickstart/claw-pod.yml

# Refresh the runner required by one Clawfile
claw pull ./agents/assistant/Clawfile
```

---

## claw build

Build local sources.

```bash
claw build [path-or-clawfile]
```

With a positional argument, `claw build` keeps the single-image form: transpile one Clawfile, write `Dockerfile.generated`, and run `docker build`. With no positional argument and a `claw-pod.yml` in scope, it becomes pod-aware and builds every service that declares `build:`.

| Flag | Description |
|------|-------------|
| `-t, --tag <name>` | Tag for the built image in single-image mode. |
| `--context <dir>` | Docker build context directory in single-image mode. Defaults to the Clawfile's parent directory. |

For built-in local runner bases, `claw build` consumes an already-refreshed alias. If a required runner alias is missing or lacks provenance, it tells you to run `claw pull`. Non-runner base-image remediation still follows its existing path.

**Examples:**

```bash
# Build from an agent directory
claw build -t trading-desk-analyst:latest ./agents/analyst

# Build every build-owned service in the current pod
claw build
```

---

## claw up

Parse the pod YAML, inspect images, enforce driver constraints, generate per-agent configs, wire the cllama proxy, and call `docker compose up`. This is the main compilation and deployment command.

```bash
claw up [pod-file]
```

The pod file can be specified as a positional argument or via `-f`. Defaults to `claw-pod.yml` in the current directory. Specifying both the `-f` flag and a positional argument is an error.

| Flag | Description |
|------|-------------|
| `-d` | Detached mode. Required when the pod contains managed `x-claw` services. |
| `--fix` | Pull/build missing images and refresh stale runtime-emitted descriptors before starting. |
| `--discover-tools` | Discover missing or stale stdio MCP tool snapshots before compiling. |
| `-f, --file <path>` | Path to `claw-pod.yml`. |

**What it does:**

1. Parses `claw-pod.yml`, resolving `${...}` placeholders from your shell environment and any pod-local `.env` file.
2. Inspects each service image for claw metadata labels (via `claw inspect` internally).
3. Loads `claw.describe` descriptors from explicit overrides, `.claw-discovered/` snapshots, images, or build contexts.
4. Runs driver-specific validation and materialization (config generation, persona resolution, contract composition).
5. Wires cllama proxy services — generates bearer tokens, context files, and proxy config.
6. Generates per-agent runtime artifacts: `AGENTS.generated.md`, `CLAWDAPUS.md`, `metadata.json`, runner configs.
7. Emits `compose.generated.yml` next to the pod file.
8. Calls `docker compose up` against the generated file.
9. For managed services (`-d` mode), runs post-apply health verification.

**Generated artifacts** are written to `.claw-runtime/` next to the pod file. Per-agent context lives at `.claw-runtime/context/<agent-id>/`. These are generated outputs — inspect them for debugging, but do not hand-edit them. When `claw-api` is present, Clawdapus Dash also exposes this context under its Agents view, including redacted runtime manifests and the latest live cllama context snapshot.

**Dashboard URL:** When the pod declares a `clawdash` surface, `claw up` prints the dashboard URL on success (for example `[claw] dashboard:  http://localhost:8082`) so you can jump straight into the Agents / Topology / Fleet views without re-checking the compose output.

`claw up` is strict by default. If an infra image is missing, it tells you to run `claw pull`. If a local runner base needs refresh, the build path tells you to run `claw pull`. If a pod service image is not built, it tells you to run `claw build`. If a build-backed service descriptor is stale and an allowlist references a new tool, it tells you to run `claw up --fix -d`. `claw up --fix` can pull/build missing infra and service images and refresh runtime-emitted descriptor snapshots, but runner refresh still happens through `claw pull`.

**Examples:**

```bash
# Launch the pod in detached mode (required for managed services)
claw up -d

# First-run shortcut
claw up --fix -d

# Discover stdio MCP tools while compiling
claw up --discover-tools -d

# Explicit pod file
claw up -f claw-pod.yml -d

# Launch an example pod
claw up ./examples/quickstart/claw-pod.yml -d
```

**Relationship with other commands:** All lifecycle commands (`ps`, `logs`, `health`, `audit`, `compose`) depend on the `compose.generated.yml` that `claw up` produces. Run `claw up` first.

---

## claw discover

Discover tool schemas from stdio MCP sidecars and write deterministic descriptor snapshots.

```bash
claw discover [service...]
```

Without service names, `claw discover` discovers every `x-claw.mcp-stdio` service in the pod. For each service it starts the configured wrapper image as a short-lived local container, preserves the service environment and bind mounts, runs MCP `initialize` plus `tools/list`, and writes `.claw-discovered/<service>.claw-describe.json`.

`claw up` consumes that snapshot during compilation. It does not perform live discovery unless `--discover-tools` is set.

| Flag | Description |
|------|-------------|
| `--timeout <duration>` | Maximum time to wait for each discovery sidecar. Defaults to `45s`. |

**Examples:**

```bash
# Discover all stdio MCP sidecars in the current pod
claw discover

# Discover one provider
claw discover perplexity
```

---

## claw down

Tear down a running pod.

```bash
claw down
```

Runs `docker compose down` against the generated compose file.

`claw down` is exempt from the [staleness guard](#staleness-guard) -- you can always shut down a pod even if `claw-pod.yml` has been modified since the last `claw up`.

**Examples:**

```bash
# Tear down from the pod directory
claw down

# Tear down using an explicit pod file path
claw down -f ./examples/quickstart/claw-pod.yml
```

---

## claw ps

Show the status of running containers in the pod.

```bash
claw ps
```

Wraps `docker compose ps` against `compose.generated.yml`. Subject to the [staleness guard](#staleness-guard).

**Example output:**

```
NAME                    IMAGE                           COMMAND   SERVICE     CREATED        STATUS
trading-desk-analyst-1  trading-desk-analyst:latest      ...      analyst     2 hours ago    Up 2 hours (healthy)
trading-desk-cllama-1   ghcr.io/mostlydev/cllama:v0.6.8  ...      cllama      2 hours ago    Up 2 hours (healthy)
```

---

## claw logs

Stream logs from pod services.

```bash
claw logs [service]
```

| Flag | Description |
|------|-------------|
| `--follow` | Follow log output in real time. |

Without a service name, shows logs from all services in the pod. Subject to the [staleness guard](#staleness-guard).

**Examples:**

```bash
# All pod logs
claw logs

# Logs from a specific service
claw logs analyst

# Follow cllama proxy logs in real time (useful for watching LLM traffic)
claw logs --follow cllama
```

---

## claw health

Show health status of all containers in the pod. Subject to the [staleness guard](#staleness-guard).

```bash
claw health
```

Output columns: `SERVICE`, `STATUS`, `DETAIL`.

For claw services (those with a `claw.type` label), `claw health` uses the driver-specific `HealthProbe` implementation. For non-claw services (databases, APIs, etc.), it falls back to native Docker healthcheck status or running state.

User-defined `healthcheck:` blocks in `claw-pod.yml` take precedence over driver defaults.

**Example output:**

```
SERVICE              STATUS     DETAIL
analyst              healthy    openclaw health --json: ok
cllama               healthy    native docker healthcheck
trading-api          running    native (no claw driver)
```

---

## claw inspect

Parse and display claw metadata labels from a built image.

```bash
claw inspect <image>
```

Shows the claw type, agent contract, cllama configuration, model bindings, surfaces, persona, and privilege settings baked into the image. Returns "Not a Claw image" if the image has no `claw.type` label.

**Example:**

```bash
$ claw inspect trading-desk-analyst:latest

Claw Type: openclaw
Agent:     AGENTS.md
Cllama:    passthrough
Model[primary]: openrouter/anthropic/claude-sonnet-4
Model[fallback]: anthropic/claude-haiku-3-5
Surface:   service://trading-api
Surface:   volume://shared-research read-write
```

---

## claw doctor

Check system prerequisites: Docker CLI, buildx, and compose plugin availability.

```bash
claw doctor
```

Run this after installing to verify your environment is ready.

**Example output:**

```
docker     OK   Docker version 27.5.1, build 9f9e405
buildx     OK   github.com/docker/buildx v0.21.1
compose    OK   Docker Compose version v2.35.1
```

If any check fails, the command exits with a non-zero status and marks the failing component as `FAIL`.

---

## claw init

Scaffold a new Clawdapus project with canonical layout.

```bash
claw init [directory]
```

When run interactively (no flags), prompts for project name, agent name, claw type, model, cllama proxy, and platform. Creates `agents/<name>/Clawfile`, `agents/<name>/AGENTS.md`, `claw-pod.yml`, and `.env.example`.

| Flag | Description |
|------|-------------|
| `--from <path>` | Import an existing OpenClaw or Hermes config into the same runtime. |
| `--source <openclaw\|hermes>` | Source runtime override when `--from` autodetection is ambiguous. |
| `--project <name>` | Project name (used for `x-claw.pod` and image prefix). |
| `--agent <name>` | Primary agent name. |
| `--type <type>` | Claw type (openclaw, hermes, nanobot, picoclaw, generic). |
| `--model <provider/model>` | Primary model. |
| `--cllama <yes\|no>` | Enable cllama proxy. |
| `--platform <name>` | Platform handle (discord, slack, telegram, none). |
| `--volume <spec>` | Shared volume (`<name>` or `<name>:<mode>`). |

**Examples:**

```bash
# Interactive scaffolding (prompts for all options)
claw init my-project

# Non-interactive with all flags
claw init --project trading-desk --type openclaw --platform discord

# Import an existing OpenClaw or Hermes setup into claw management
claw init --from ~/existing-config
```

After scaffolding:

```bash
cd my-project
cp .env.example .env
# Edit .env with your API keys and bot tokens
source .env
claw build -t my-project-assistant:latest ./agents/assistant
claw up -d
```

---

## claw agent add

Add an agent to an existing project. Detects the project layout automatically and preserves it.

```bash
claw agent add [name]
```

- **Canonical layout:** adds `agents/<name>/Clawfile` + `agents/<name>/AGENTS.md`
- **Flat layout:** adds `Clawfile.<name>` + `AGENTS-<name>.md`

| Flag | Description |
|------|-------------|
| `--layout <auto\|canonical\|flat>` | Override layout detection. |
| `--type <type>` | Claw type for the new agent. |
| `--model <provider/model>` | Primary model. |
| `--cllama <yes\|no\|inherit>` | Cllama proxy setting (`inherit` reuses pod config). |
| `--platform <name>` | Platform handle. |
| `--contract <path>` | Path to an existing contract file to reference. |
| `--volume <spec>` | Volume surface specs (repeatable). |
| `--dry-run` | Print what would be created without writing files. |
| `-y, --yes` | Skip confirmation prompts. |

**Examples:**

```bash
# Interactive — prompts for type, model, platform
claw agent add researcher

# Non-interactive with flags
claw agent add --type hermes --platform telegram analyst

# Preview without writing files
claw agent add --dry-run scanner
```

---

## claw compose

Pass any subcommand through to `docker compose -f compose.generated.yml`. Use this for compose operations not covered by the named shortcuts (`ps`, `logs`, `health`, `down`).

```bash
claw compose <subcommand> [args...]
```

Flag parsing is disabled for this command -- everything after `compose` is forwarded directly to `docker compose`. Subject to the [staleness guard](#staleness-guard).

**Examples:**

```bash
# Shell into a running agent container
claw compose exec analyst bash

# Restart the cllama proxy without tearing down the whole pod
claw compose restart cllama

# View resource usage across the pod
claw compose top

# View container events
claw compose events

# Rebuild and restart a single service
claw compose up -d --build analyst

# Pull updated images
claw compose pull
```

Any `docker compose` subcommand works. The full list is available via `docker compose --help`.

---

## claw audit

Summarize normalized cllama telemetry for the current pod.

```bash
claw audit
```

Collects structured JSON logs from cllama proxy containers, normalizes them, and presents per-agent summaries. Requires a running (or recently stopped) pod with cllama proxy containers that have emitted telemetry.

| Flag | Description |
|------|-------------|
| `--since <duration\|timestamp>` | Only include events since this duration (e.g. `1h`, `24h`) or RFC3339 timestamp (e.g. `2026-03-22T00:00:00Z`). |
| `--claw <id>` | Filter events to one claw_id. |
| `--type <type>` | Filter to one event type, for example `request`, `response`, `error`, `intervention`, `feed_fetch`, `channel_context_op`, `provider_pool`, or `tool_call`. |
| `--json` | Emit machine-readable JSON output. |

**Example -- tabular output:**

```bash
$ claw audit --since 24h

Pod: trading-desk
Events: 847
CLAW       REQ  RESP  ERR  INT  TOOLS  TOOL_ERR  TOK_IN  TOK_OUT  COST_USD  MODELS
analyst    312  310   2    0    18     1         482101  89402    1.2340    claude-sonnet-4(312)
scanner    535  535   0    3    0      0         201440  45200    0.5120    claude-haiku-3-5(535)

Totals: req=847 resp=845 err=2 int=3 tools=18/1 tokens=683541/134602 cost=$1.7460
```

**Example -- filtered to one agent:**

```bash
$ claw audit --since 24h --claw analyst-0
```

**Example -- JSON output for scripting:**

```bash
$ claw audit --since 1h --json | jq '.summary.cost_usd'
```

The JSON output includes the pod name, skipped malformed lines count, per-agent summary, and the full list of matched events.

Each event in the JSON output carries `manifest_present` (bool) and `tools_count` (int) fields when the upstream request was mediated by a compiled managed-tool manifest. These fields let operators verify at runtime whether `tools.json` was actually loaded by cllama for a given agent — distinct from checking whether the file exists on disk. Events without managed tools omit both fields.

`claw audit` reports the normalized telemetry that exists today. The reference proxy does not emit a built-in drift score and there is no `DRIFT` column; policy attribution beyond the emitted `intervention` reason belongs to a swappable proxy or Master Claw policy.

---

## claw history

Inspect persistent agent session history written by cllama.

### claw history export

Export one agent's session history as NDJSON:

```bash
claw history export <agent-id>
```

| Flag | Description |
|------|-------------|
| `--after <RFC3339>` | Emit only entries after this timestamp |
| `--limit <n>` | Maximum entries to emit (default 100, capped at 1000) |

**Example:**

```bash
claw history export analyst-0 --limit 200
```

---

## claw memory

Operator commands for the memory plane.

### claw memory backfill

Replay the durable session ledger into a memory service's `retain` endpoint.

```bash
claw memory backfill <service>
```

| Flag | Description |
|------|-------------|
| `--after <RFC3339>` | Replay only entries after this timestamp |
| `--limit <n>` | Maximum entries to replay per agent (0 means all) |
| `--agent <id>` | Restrict replay to one or more agent IDs |
| `--url <url>` | Override the retain endpoint URL |
| `--auth-token <token>` | Override the bearer token for retain requests |

Backfill uses the same stable session-history `entry.id` values as live retain, honors local forget tombstones, and uses an indexed `history.index.json` sidecar for `--after` seeks — no full ledger rescan.

**Examples:**

```bash
# Replay entire ledger for all subscribed agents
claw memory backfill mem-svc

# Replay only entries after a given timestamp
claw memory backfill mem-svc --after 2026-03-01T00:00:00Z

# Replay for a single agent using an explicit retain URL
claw memory backfill mem-svc --agent analyst-0 --url http://localhost:9090/retain
```

### claw memory forget

Tombstone a retained memory entry. Dispatches the service's `forget` endpoint (when declared) and writes infra-owned tombstones locally under `.claw-memory-tombstones/`. Later backfill runs honor tombstones and skip re-retain for forgotten entry IDs.

```bash
claw memory forget <service> --agent <agent-id> --entry-id <id>
```

| Flag | Description |
|------|-------------|
| `--agent <id>` | Agent whose retained entry should be forgotten (required; repeatable) |
| `--entry-id <id>` | Stable session-history entry ID to tombstone (required; repeatable) |
| `--url <url>` | Override the forget endpoint URL |
| `--auth-token <token>` | Override the bearer token for forget requests |
| `--reason <text>` | Human-readable reason for audit records |

Forget does not mutate the immutable `history.jsonl` ledger.

**Example:**

```bash
claw memory forget mem-svc --agent analyst-0 --entry-id hist1_abc123 --reason "operator request"
```

---

## Planned: Skill Map Diagnostics

The `claw skillmap` command is not registered today. It is a planned diagnostic for displaying the compiled skill map for a specific agent -- showing which surfaces provide which capabilities, how they were discovered, and where the generated skill files live.

```bash
claw skillmap <agent-id>
```

**Planned output:**

```bash
$ claw skillmap crypto-crusher-0

  FROM market-scanner (service://market-scanner):
    get_price            Current and historical token price data
    get_whale_activity   Large wallet movements in last N hours
    [discovered via OpenAPI → skills/surface-market-scanner.md]

  FROM shared-cache (volume://shared-cache):
    read-write at /mnt/shared-cache
```

The planned command would surface the same information that `claw up` compiles into each agent's `CLAWDAPUS.md`, but in a human-readable diagnostic format. It would be useful for verifying that service self-description (`claw.describe` labels) and surface declarations are wiring correctly.

---

## Recipe Promotion (Planned)

The `claw recipe` and `claw bake` commands are not registered today. They are planned commands for the tracked mutation workflow, where agents install packages, write scripts, and adapt their environments at runtime. The `TRACK` directive is parsed into image metadata; runtime package wrapping and recipe promotion remain roadmap work.

### claw recipe

Planned behavior: show tracked mutations for a running agent since a given time.

```bash
claw recipe <agent> --since <duration>
```

**Planned output:**

```bash
$ claw recipe crypto-crusher-0 --since 7d

  pip: tiktoken==0.7.0, trafilatura>=0.9
  apt: jq
  files: scripts/scraper.py

Apply?  claw bake crypto-crusher --from-recipe latest
```

### claw bake

Planned behavior: promote a recipe to a permanent image layer. It would take tracked mutations and bake them into the agent's base image, so the next `claw build` includes them by default.

```bash
claw bake <agent> --from-recipe <version>
```

Tracked mutation is evolution. Untracked mutation is drift. The planned command is the human gate between the two.

---

## Staleness Guard

The lifecycle commands `ps`, `logs`, `health`, `compose`, and `audit` refuse to run if `claw-pod.yml` is newer than `compose.generated.yml`.

**Why this exists:** The generated compose file is the single source of truth for what is deployed. If you edit `claw-pod.yml` (adding a service, changing a model binding, modifying surfaces), the running pod no longer matches the pod definition. Running `claw ps` against a stale generated file would show you the state of an outdated deployment. Running `claw health` might check containers that no longer match the intended topology.

The staleness guard prevents this class of confusion by failing early with a clear message:

```
Error: claw-pod.yml is newer than compose.generated.yml — run 'claw up' to regenerate
```

**How it works:** The guard compares file modification times. If the pod file's mtime is newer than the generated file's mtime, the command is refused.

**Resolution:** Run `claw up` to regenerate `compose.generated.yml` from the updated pod definition.

**Exemptions:**

- `claw down` skips the staleness check. You can always tear down a pod regardless of whether the definition has changed -- you should never be blocked from stopping containers.
- `claw compose build` downgrades the staleness check to a warning. Building a service image does not depend on the generated compose matching the pod file -- it only needs the existing `services.<name>.build` block. The warning reminds you to run `claw up` afterward to apply any pod-level changes. This carve-out applies only to the literal `build` subcommand; `claw compose --progress=plain build foo` (with global docker compose flags before the subcommand) still trips the strict guard. If you need that shape, invoke `docker compose -f compose.generated.yml ...` directly.

---

## Command Quick Reference

| Command | Staleness guard | Requires `claw up` first | Notes |
|---------|:-:|:-:|-------|
| `claw pull` | -- | No | Pinned infra + pod registry images + runner bases |
| `claw build` | -- | No | Single-image or pod-aware build |
| `claw up` | -- | No | Generates `compose.generated.yml`; strict by default |
| `claw discover` | -- | No | Writes deterministic stdio MCP descriptor snapshots |
| `claw down` | Exempt | Yes | Always allowed |
| `claw ps` | Yes | Yes | |
| `claw logs` | Yes | Yes | |
| `claw health` | Yes | Yes | |
| `claw audit` | Yes | Yes | Reads pod manifest from `.claw-runtime/` |
| `claw compose` | Yes¹ | Yes | Passthrough to `docker compose` (¹ `claw compose build` warns instead of erroring; see [Staleness Guard](#staleness-guard)) |
| `claw api schedule` | Yes | Yes | Tunnels schedule list/get/pause/resume/skip/fire through `claw-api` |
| `claw inspect` | -- | No | Reads image labels directly |
| `claw doctor` | -- | No | System prerequisite check |
| `claw init` | -- | No | Project scaffolding |
| `claw agent add` | -- | No | Modifies project files |
| `claw history export` | -- | Yes | Streams one agent's `history.jsonl` as NDJSON |
| `claw memory backfill` | -- | Yes | Replays session ledger into memory service |
| `claw memory forget` | -- | Yes | Tombstones a retained entry for one or more `--agent` IDs |
| `claw skill install` | -- | No | Installs the bundled Clawdapus CLI skill for coding agents |
| `claw update` | -- | No | Re-runs the latest-release installer |
