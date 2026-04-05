# CLI Commands

The `claw` CLI is the single entry point for building, deploying, and managing governed agent pods.

## Global Flags

| Flag | Short | Description |
|------|-------|-------------|
| `--file <path>` | `-f` | Path to `claw-pod.yml`. Locates `compose.generated.yml` next to it. |
| `--version` | | Print the CLI version and exit. |

The `-f` flag is a persistent flag on the root command, so all lifecycle commands (`ps`, `logs`, `health`, `down`, `audit`, `compose`) inherit it. When omitted, commands look for `compose.generated.yml` in the current directory.

---

## claw build

Transpile a Clawfile into a standard Dockerfile and build the OCI image.

```bash
claw build [path-or-clawfile]
```

If `path-or-clawfile` is a directory, `claw build` looks for a `Clawfile` inside it. If omitted, the current directory is used.

| Flag | Description |
|------|-------------|
| `-t, --tag <name>` | Tag for the built image (e.g. `my-agent:latest`). |
| `--context <dir>` | Docker build context directory. Defaults to the Clawfile's parent directory. |

On the first run, if the driver's base image (e.g. `openclaw:latest`) is missing locally, `claw build` auto-builds it.

**What it does:**

1. Reads the Clawfile and translates extended directives (`CLAW_TYPE`, `MODEL`, `CLLAMA`, etc.) into standard Dockerfile primitives (`LABEL`, `ENV`, `RUN`).
2. Writes `Dockerfile.generated` next to the Clawfile.
3. Calls `docker build` on the generated Dockerfile.

The output is a standard OCI image. You can `docker run` it directly, or use it in a `claw-pod.yml`.

**Examples:**

```bash
# Build from an agent directory
claw build -t trading-desk-analyst:latest ./agents/analyst

# Build from the current directory
claw build -t my-bot:latest .

# Separate build context from Clawfile location
claw build --context ./project ./agents/bot/Clawfile
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
| `-f, --file <path>` | Path to `claw-pod.yml`. |

**What it does:**

1. Parses `claw-pod.yml`, resolving `${...}` placeholders from your shell environment and any pod-local `.env` file.
2. Inspects each service image for claw metadata labels (via `claw inspect` internally).
3. Loads `claw.describe` descriptors from images for services that self-describe.
4. Runs driver-specific validation and materialization (config generation, persona resolution, contract composition).
5. Wires cllama proxy services — generates bearer tokens, context files, and proxy config.
6. Generates per-agent runtime artifacts: `AGENTS.generated.md`, `CLAWDAPUS.md`, `metadata.json`, runner configs.
7. Emits `compose.generated.yml` next to the pod file.
8. Calls `docker compose up` against the generated file.
9. For managed services (`-d` mode), runs post-apply health verification.

**Generated artifacts** are written to `.claw-runtime/` next to the pod file. Per-agent context lives at `.claw-runtime/context/<agent-id>/`. These are generated outputs — inspect them for debugging, but do not hand-edit them.

**Image resolution** follows a 3-step fallback: check local images, then `docker pull`, then build from local Dockerfile or git URL.

**Examples:**

```bash
# Launch the pod in detached mode (required for managed services)
claw up -d

# Explicit pod file
claw up -f claw-pod.yml -d

# Launch an example pod
claw up ./examples/quickstart/claw-pod.yml -d
```

**Relationship with other commands:** All lifecycle commands (`ps`, `logs`, `health`, `audit`, `compose`) depend on the `compose.generated.yml` that `claw up` produces. Run `claw up` first.

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
NAME                    IMAGE                           COMMAND   SERVICE               CREATED        STATUS
trading-desk-analyst-1  trading-desk-analyst:latest      ...      analyst               2 hours ago    Up 2 hours (healthy)
trading-desk-cllama-1   ghcr.io/mostlydev/cllama:latest  ...      cllama-passthrough    2 hours ago    Up 2 hours (healthy)
```

**Future (Design -- Phase 5):** `claw ps` will include drift scoring from the governance proxy:

```
TENTACLE          STATUS    CLLAMA    DRIFT
crypto-crusher-0  running   healthy   0.02
crypto-crusher-1  running   healthy   0.04
crypto-crusher-2  running   WARNING   0.31
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
claw logs --follow cllama-passthrough
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
cllama-passthrough   healthy    native docker healthcheck
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
| `--from <path>` | Migrate from an existing OpenClaw config directory. |
| `--project <name>` | Project name (used for `x-claw.pod` and image prefix). |
| `--agent <name>` | Primary agent name. |
| `--type <type>` | Claw type (openclaw, hermes, nanoclaw, nanobot, picoclaw, nullclaw, microclaw, generic). |
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

# Migrate from existing OpenClaw setup
claw init --from ~/existing-openclaw-config
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
claw compose restart cllama-passthrough

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
| `--type <type>` | Filter to one event type: `request`, `response`, `error`, or `intervention`. |
| `--json` | Emit machine-readable JSON output. |

**Example -- tabular output:**

```bash
$ claw audit --since 24h

Pod: trading-desk
Events: 847
CLAW       REQ  RESP  ERR  INT  TOK_IN   TOK_OUT  COST_USD  MODELS
analyst    312  310   2    0    482101   89402    1.2340    claude-sonnet-4(312)
scanner    535  535   0    3    201440   45200    0.5120    claude-haiku-3-5(535)

Totals: req=847 resp=845 err=2 int=3 tokens=683541/134602 cost=$1.7460
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

**Future (Design -- Phase 5):** `claw audit` will include intervention detail with policy attribution:

```
$ claw audit crypto-crusher-2 --last 24h

14:32  tweet-cycle       OUTPUT MODIFIED by cllama:policy  (financial advice detected)
18:01  engagement-sweep  OUTPUT DROPPED by cllama:purpose  (off-strategy)
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
claw memory forget <service> --entry-id <id>
```

| Flag | Description |
|------|-------------|
| `--entry-id <id>` | Stable session-history entry ID to tombstone (required) |
| `--reason <text>` | Human-readable reason for audit records |

Forget does not mutate the immutable `history.jsonl` ledger.

**Example:**

```bash
claw memory forget mem-svc --entry-id hist1_abc123 --reason "operator request"
```

---

## claw skillmap (Planned)

Display the compiled skill map for a specific agent -- showing which surfaces provide which capabilities, how they were discovered, and where the generated skill files live.

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

This command surfaces the same information that `claw up` compiles into each agent's `CLAWDAPUS.md`, but in a human-readable diagnostic format. Useful for verifying that service self-description (`claw.describe` labels) and surface declarations are wiring correctly.

---

## Recipe Promotion (Planned -- Phase 6)

Two commands for the tracked mutation workflow. Agents install packages, write scripts, and adapt their environments at runtime. The `TRACK` directive logs these mutations. Recipe promotion turns tracked mutations into permanent image changes through a human gate.

### claw recipe

Show tracked mutations for a running agent since a given time.

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

Promote a recipe to a permanent image layer. Takes the tracked mutations and bakes them into the agent's base image, so the next `claw build` includes them by default.

```bash
claw bake <agent> --from-recipe <version>
```

Tracked mutation is evolution. Untracked mutation is drift. This command is the human gate between the two.

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

**Exemptions:** `claw down` skips the staleness check. You can always tear down a pod regardless of whether the definition has changed -- you should never be blocked from stopping containers.

---

## Command Quick Reference

| Command | Staleness guard | Requires `claw up` first | Notes |
|---------|:-:|:-:|-------|
| `claw build` | -- | No | Standalone image build |
| `claw up` | -- | No | Generates `compose.generated.yml` |
| `claw down` | Exempt | Yes | Always allowed |
| `claw ps` | Yes | Yes | |
| `claw logs` | Yes | Yes | |
| `claw health` | Yes | Yes | |
| `claw audit` | Yes | Yes | Reads pod manifest from `.claw-runtime/` |
| `claw compose` | Yes | Yes | Passthrough to `docker compose` |
| `claw inspect` | -- | No | Reads image labels directly |
| `claw doctor` | -- | No | System prerequisite check |
| `claw init` | -- | No | Project scaffolding |
| `claw agent add` | -- | No | Modifies project files |
| `claw memory backfill` | -- | Yes | Replays session ledger into memory service |
| `claw memory forget` | -- | Yes | Tombstones a retained entry |
