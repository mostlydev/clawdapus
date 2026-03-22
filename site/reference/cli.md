# CLI Commands

The `claw` CLI is the single entry point for building, deploying, and managing governed agent pods.

## Global Flags

| Flag | Short | Description |
|------|-------|-------------|
| `--file <path>` | `-f` | Path to `claw-pod.yml`. Locates `compose.generated.yml` next to it. |
| `--version` | | Print the CLI version and exit. |

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

**Examples:**

```bash
claw build -t trading-desk-analyst:latest ./agents/analyst
claw build -t my-bot:latest .
claw build --context ./project ./agents/bot/Clawfile
```

## claw up

Parse the pod YAML, inspect images, enforce driver constraints, generate per-agent configs, wire the cllama proxy, and call `docker compose up`. This is the main compilation and deployment command.

```bash
claw up [pod-file]
```

The pod file can be specified as a positional argument or via `-f`. Defaults to `claw-pod.yml` in the current directory.

| Flag | Description |
|------|-------------|
| `-d` | Detached mode. Required when the pod contains managed `x-claw` services. |
| `-f, --file <path>` | Path to `claw-pod.yml`. |

`claw up` writes `compose.generated.yml` next to the pod file. It resolves `${...}` placeholders inside `x-claw` metadata from your shell environment and any pod-local `.env` file.

**Examples:**

```bash
claw up -d
claw up -f claw-pod.yml -d
claw up ./examples/quickstart/claw-pod.yml -d
```

## claw down

Tear down a running pod. Exempt from the staleness check -- you can always shut down a stale pod.

```bash
claw down
```

Runs `docker compose down` against the generated compose file.

## claw ps

Show the status of running containers in the pod.

```bash
claw ps
```

Wraps `docker compose ps` against `compose.generated.yml`.

## claw logs

Stream logs from pod services.

```bash
claw logs [service]
```

| Flag | Description |
|------|-------------|
| `--follow` | Follow log output in real time. |

**Examples:**

```bash
claw logs
claw logs analyst
claw logs --follow cllama-passthrough
```

## claw health

Show health status of all containers in the pod. Uses driver-specific health probes when available, falling back to native Docker healthchecks.

```bash
claw health
```

Output columns: `SERVICE`, `STATUS`, `DETAIL`.

## claw inspect

Parse and display claw metadata labels from a built image.

```bash
claw inspect <image>
```

Shows the claw type, agent contract, cllama configuration, model bindings, surfaces, and privilege settings baked into the image.

**Example:**

```bash
claw inspect trading-desk-analyst:latest
```

## claw doctor

Check system prerequisites: Docker CLI, buildx, and compose plugin availability.

```bash
claw doctor
```

Run this after installing to verify your environment is ready.

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
claw init my-project
claw init --project trading-desk --type openclaw --platform discord
claw init --from ~/existing-openclaw-config
```

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
claw agent add researcher
claw agent add --type hermes --platform telegram analyst
```

## claw compose

Pass any subcommand through to `docker compose -f compose.generated.yml`. Use this for compose operations not covered by the named shortcuts.

```bash
claw compose <subcommand> [args...]
```

**Examples:**

```bash
claw compose exec analyst bash
claw compose restart cllama-passthrough
claw compose top
```

## claw audit

Summarize normalized cllama telemetry for the current pod.

```bash
claw audit
```

| Flag | Description |
|------|-------------|
| `--since <duration\|timestamp>` | Only include events since this duration (e.g. `1h`) or RFC3339 timestamp. |
| `--claw <id>` | Filter events to one claw_id. |
| `--type <type>` | Filter to one event type (request, response, error, intervention). |
| `--json` | Emit machine-readable JSON output. |

**Example:**

```bash
claw audit --since 24h --claw analyst-0
```

## Staleness Guard

The lifecycle commands `ps`, `logs`, `health`, and `compose` refuse to run if `claw-pod.yml` is newer than `compose.generated.yml`. This prevents operating against stale configuration. Run `claw up` to regenerate.

`claw down` is exempt from this check -- you can always tear down a pod regardless of staleness.
