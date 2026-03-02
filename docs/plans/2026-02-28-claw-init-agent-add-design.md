# Design: `claw init` + `claw agent add` — Interactive Scaffolding

**Date:** 2026-02-28
**Status:** Implemented (core shipped) with follow-ups
**Scope:** New interactive scaffolding workflow for `claw init` and `claw agent add`

## Summary

Transform `claw init` from a static template dumper into an interactive scaffolder that produces a fully working single-agent project. Add `claw agent add` as a context-aware command that adds agents to an existing project, creating layout-appropriate agent files and auto-updating the pod file.

**Philosophy:** The scaffolder is opinionated, the runtime is not. `claw init` produces the canonical layout. `claw agent add` preserves the existing project layout by default (`--layout auto`) while still allowing explicit `--layout canonical|flat`. `claw build` and `claw up` follow whatever the pod file points at — flat, nested, or anything else.

## Implementation Status (2026-03-01)

### Progress Update (2026-03-01, later pass)

- ✅ Implemented layout-preserving `claw agent add` with auto-detection and explicit `--layout` override.
- ✅ Added regression tests for flat-layout preservation and `--layout` validation/override.
- ✅ Implemented `--type generic` base image parameterization in scaffolds.
- ✅ Implemented env-prefix collision/reuse warnings for `.env.example` additions.
- ✅ Reconciled README + quickstart + ADR wording with canonical-by-default and non-forcing behavior.
- ✅ Fixed quickstart/runtime startup defaults: scaffolded services now inject both platform token + ID env vars; Discord default DM policy now uses `pairing` (with legacy `denylist` normalized to `open` + wildcard allowFrom where needed) so startup is valid out-of-the-box.

### Completed

- ✅ `claw init` implemented as canonical scaffold generator:
  - creates `agents/<name>/Clawfile`, `agents/<name>/AGENTS.md`, `claw-pod.yml`, `.env.example`
  - emits both `image:` and `build.context:` in pod service
  - appends required `.gitignore` entries (`.env`, `*.generated.*`) instead of refusing
  - supports interactive prompts with flag overrides (`--project`, `--agent`, `--type`, `--model`, `--cllama`, `--platform`, `--volume`)
- ✅ `claw init --from` preserved as legacy flat migration output intentionally (non-forcing transition)
- ✅ `claw agent add` command added:
  - creates layout-appropriate Clawfile + contract files (canonical or flat)
  - updates `claw-pod.yml` by YAML AST mutation
  - appends prefixed env vars to `.env.example`
  - supports `--dry-run` and `--yes`
  - supports `--layout auto|canonical|flat` (default `auto`)
  - supports contract reuse and shared profile creation flow
  - preserves existing project layout style: canonical projects stay canonical; flat projects get flat additions (`Clawfile.<agent>`, `AGENTS-<agent>.md`, `build.context: .` + `build.dockerfile`)
- ✅ Mutation safety behavior implemented:
  - no implicit deletes
  - planned change summary printed before write
  - shared-profile rewiring requires explicit opt-in
- ✅ Contract safety hardening implemented:
  - absolute `--contract` paths are rejected (pod-root-relative only)
- ✅ Generic claw scaffold base image parameterized:
  - `--type generic` emits `FROM alpine:3.20` instead of `FROM openclaw:latest`
- ✅ Env prefix collision warnings implemented:
  - warns on service-prefix collisions (`my-bot` vs `my_bot`)
  - warns when target `.env.example` keys already exist and will be reused
- ✅ ADR-011 added at `docs/decisions/011-canonical-project-layout.md`
- ✅ README + quickstart reconciled to canonical defaults and layout-preserving `agent add`
- ✅ Test coverage added for init + agent add flows; full suite passing (`go test ./...`)
- ✅ Added doc smoke test (`spike` tag) that extracts quickstart shell blocks from root `README.md` + `examples/quickstart/README.md` and executes them in a fresh Docker CLI container against the local repo, asserting a working claw startup flow

### Deferred / Follow-ups

- ⏳ Optional: add canonical variants of `examples/multi-claw` and `examples/trading-desk` (current examples intentionally remain hand-authored layouts)
- ⏳ Replace current simple interactive prompts with a TUI prompt library (`survey`/`huh`) if desired

## Codex Review Findings (addressed)

1. **build.context blocker** — Generated pods emit both `image:` (for inspect) and `build:` (for build). Standard docker-compose behavior.
2. **x-claw.agent path** — `x-claw.agent` is resolved relative to pod root, not build context. Generated path: `./agents/<name>/AGENTS.md`.
3. **shared-profile path** — Shared profiles are referenced via `x-claw.agent` in the pod file (pod-root-relative: `./shared/<profile>/AGENTS.md`), not via Clawfile `AGENT` directive. The Clawfile `AGENT AGENTS.md` remains as a baked image fallback; the pod override takes precedence.
4. **canonical rule conflict** — Softened: agent directories always have a Clawfile; AGENTS.md is required for unique contracts and optional when a shared profile is used (it may remain as an unused local fallback).
5. **env var mapping** — Multi-agent pods use explicit mapping: `.env` has `WESTIN_DISCORD_BOT_TOKEN` + `WESTIN_DISCORD_BOT_ID`, pod file maps canonical runtime keys per service (`DISCORD_BOT_TOKEN`, `DISCORD_BOT_ID`).
6. **conflict policy** — `.gitignore` is appended to if it exists (adds missing entries). Core files (Clawfile, claw-pod.yml, AGENTS.md) still refuse to overwrite.
7. **flag naming** — `--project` for project name, `--agent` for agent name. Unambiguous.

## ADR-011: Canonical Project Layout

**Decision:** `claw init` scaffolds a predictable canonical directory layout by default. `claw agent add` preserves the existing project layout by default (`--layout auto`) and supports explicit override (`--layout canonical|flat`).

**Motivation:** "Docker on Rails" — you should be able to `cd` into any clawdapus project and immediately know where everything is.

**The layout:**

```
my-project/
├── claw-pod.yml                # pod manifest (shared config, volumes, services)
├── .env.example                # env var template (committed to git)
├── .env                        # actual secrets (gitignored)
├── .gitignore                  # generated, includes .env + *.generated.*
├── agents/
│   ├── assistant/
│   │   ├── Clawfile            # image definition + governance directives
│   │   ├── AGENTS.md           # behavioral contract
│   │   └── skills/             # agent-specific skills (optional)
│   └── researcher/
│       ├── Clawfile
│       ├── AGENTS.md
│       └── skills/
└── shared/
    ├── trader/
    │   └── AGENTS.md           # shared contract (reused by multiple agents)
    └── skills/
        └── runbook.md          # shared skills (mounted to all agents)
```

**Rules:**

- One agent = one directory under `agents/`
- Each agent directory always contains a `Clawfile`
- Agent directories contain `AGENTS.md` for unique contracts; when using a shared profile, the runtime contract lives in `shared/<profile>/AGENTS.md` and is referenced via `x-claw.agent` in the pod file (local `agents/<name>/AGENTS.md` may remain and is not auto-deleted)
- Agent-specific skills go in `agents/<name>/skills/`
- Shared contracts go in `shared/<profile>/AGENTS.md`
- Shared skills go in `shared/skills/`
- `claw-pod.yml` references agents via `image:` (for runtime inspect) and `build: { context: agents/<name> }` (for building)
- `x-claw.agent` paths are always relative to the pod file's directory (project root), e.g., `./agents/<name>/AGENTS.md` or `./shared/<profile>/AGENTS.md`
- Generated artifacts (`Dockerfile.generated`, `compose.generated.yml`) appear next to their source and are gitignored
- The `shared/` directory is only created when something shared exists

**Non-requirement:** The canonical layout is the default scaffold output from `claw init`. The runtime (`claw build`, `claw up`) does not enforce layout. `claw agent add` defaults to preserving the existing layout (`--layout auto`), so a flat project remains flat unless explicitly overridden.

## Command: `claw init`

### Purpose

Bootstraps a new clawdapus project with a single working agent. Interactive prompts with flag overrides.

### Interactive Flow

```
$ claw init .

🐙 Initializing Clawdapus project

Project name (default: my-project): trading-desk

Agent name: tiverton

Claw type:
  ❯ openclaw
    generic

Model (e.g. openrouter/anthropic/claude-sonnet-4): openrouter/anthropic/claude-sonnet-4

Use cllama proxy? (recommended)
  ❯ yes — passthrough
    no

Platform:
  ❯ discord
    slack
    telegram
    none

Create a shared volume? (y/n): y
Volume name (default: shared): shared
Access mode:
  ❯ read-write
    read-only

✔ Created agents/tiverton/Clawfile
✔ Created agents/tiverton/AGENTS.md
✔ Created claw-pod.yml
✔ Created .env.example
✔ Created .gitignore

Next steps:
  cp .env.example .env
  # Fill in your credentials
  source .env
  claw build -t tiverton agents/tiverton
  claw up -d
```

### Prompts

| Prompt | Type | Default | Notes |
|--------|------|---------|-------|
| Project name | text input | directory name | Used as pod name in `x-claw.pod` |
| Agent name | text input | `assistant` | Directory name under `agents/`, service name in pod file |
| Claw type | select | `openclaw` | Determines driver; only `openclaw` and `generic` available now |
| Model | text input | `openrouter/anthropic/claude-sonnet-4` | Full provider/model path |
| cllama | select | `yes — passthrough` | Whether to inject cllama sidecar |
| Platform | select | `discord` | Which platform handle to configure; `none` skips |
| Shared volume | yes/no | `n` | Creates a named Docker volume and adds surface |
| Volume name | text input | `shared` | Only if shared volume = yes |
| Volume access | select | `read-write` | Only if shared volume = yes |

### Flag Overrides

Any prompt can be skipped via flag: `--project`, `--agent`, `--type`, `--model`, `--cllama`, `--platform`, `--volume`. If all flags are provided, no interactive prompts are shown.

Special flag: `--from <path>` preserves existing migration behavior (detect OpenClaw config, extract channels/models).

### Generated Files

**`agents/<name>/Clawfile`:**
```dockerfile
FROM openclaw:latest

CLAW_TYPE openclaw
AGENT AGENTS.md

MODEL primary openrouter/anthropic/claude-sonnet-4

CLLAMA passthrough

HANDLE discord
```

**`agents/<name>/AGENTS.md`:**
```markdown
# Agent Contract

You are a helpful assistant. Follow these rules:

1. Be concise and direct
2. Stay on topic
3. Ask for clarification when instructions are ambiguous
```

**`claw-pod.yml`:**
```yaml
x-claw:
  pod: trading-desk

services:
  tiverton:
    image: trading-desk-tiverton:latest
    build:
      context: agents/tiverton
    x-claw:
      agent: ./agents/tiverton/AGENTS.md
      cllama: passthrough
      cllama-env:
        OPENROUTER_API_KEY: "${OPENROUTER_API_KEY}"
      handles:
        discord:
          id: "${DISCORD_BOT_ID}"
          username: "tiverton"
      surfaces:
        - "volume://shared read-write"
    environment:
      DISCORD_BOT_TOKEN: "${DISCORD_BOT_TOKEN}"
      DISCORD_BOT_ID: "${DISCORD_BOT_ID}"

volumes:
  shared: {}
```

**Note:** Both `image:` and `build:` are emitted. `image:` names the tag for `docker inspect` (required by current runtime). `build:` provides the build context. `claw build` tags to the `image:` value; `claw up` inspects via `image:`. This is standard docker-compose behavior.

**`.env.example`:**
```bash
# LLM Provider (required — used by cllama proxy, never by agent directly)
OPENROUTER_API_KEY=sk-or-...

# Discord credentials
DISCORD_BOT_TOKEN=
DISCORD_BOT_ID=
DISCORD_GUILD_ID=
```

**`.gitignore`:**
```
.env
*.generated.*
```

### Conflict Handling

Core files (`claw-pod.yml`, Clawfile, AGENTS.md): if any target already exists, the command refuses and tells you which file. No `--force`, no merge logic. Delete first or use a new directory.

`.gitignore`: if it already exists, the command appends missing entries (`.env`, `*.generated.*`) rather than refusing. Idempotent — won't duplicate entries.

## Command: `claw agent add`

### Purpose

Adds an agent to an existing clawdapus project. Reads the current `claw-pod.yml` to understand project context (existing volumes, cllama config, platforms). Creates layout-appropriate agent files (canonical or flat) and auto-updates the pod file.

### Prerequisite

A valid `claw-pod.yml` must exist in the current directory (or specified via `-f`). If not, the command fails with a message suggesting `claw init` first.

### Interactive Flow

```
$ claw agent add westin

Reading claw-pod.yml...

Claw type:
  ❯ openclaw
    generic

Model (e.g. openrouter/anthropic/claude-sonnet-4): openrouter/anthropic/claude-sonnet-4

Use cllama proxy? (default: inherit pod setting passthrough)
  ❯ yes — passthrough
    no

Platform:
  ❯ discord (same as tiverton)
    slack
    telegram
    none

AGENTS.md:
  ❯ Create new (agents/westin/AGENTS.md)
    Reuse: agents/tiverton/AGENTS.md
    Create shared profile

Shared volumes:
  shared access:
    ❯ none
      read-only
      read-write

✔ Created agents/westin/Clawfile
✔ Created agents/westin/AGENTS.md
✔ Updated claw-pod.yml (added service: westin)
✔ Updated .env.example (added WESTIN_DISCORD_BOT_TOKEN, WESTIN_DISCORD_BOT_ID)
```

Note: the example above shows canonical layout output. In flat projects, `claw agent add` (default `--layout auto`) emits flat additions (`Clawfile.<name>`, `AGENTS-<name>.md`) and uses `build.context: .` + `build.dockerfile`.

### Context-Aware Behavior

| Concern | Behavior |
|---------|----------|
| **cllama** | If the pod already uses cllama, prompt defaults to inherit (`yes`). Operator can still choose `no`. |
| **Platform** | If existing agents use discord, offers "discord (same as tiverton)" — pre-fills guild config, only needs new bot credentials. |
| **AGENTS.md** | Offers: create new, reuse an existing agent's contract, or create a shared profile (copies to `shared/<profile>/AGENTS.md`; optional rewiring of existing agents only with explicit confirmation). |
| **Layout** | Default `--layout auto` detects current project style and preserves it: canonical projects add `agents/<name>/...`; flat projects add `Clawfile.<name>` + `AGENTS-<name>.md` with `build.context: .` + `build.dockerfile`. |
| **Shared volumes** | Lists existing volumes from the pod file with access mode selection per volume. Default is `none` (no implicit access grant). |
| **.env.example** | Appends new vars prefixed with agent name: `WESTIN_DISCORD_BOT_TOKEN`, `WESTIN_DISCORD_BOT_ID`. Pod file maps these to canonical names per service (`DISCORD_BOT_TOKEN`, `DISCORD_BOT_ID`). Append-only: no deletion/renaming of existing keys. |

### "Create shared profile" Flow

When selected, this:
1. Prompts for profile name (e.g., `trader`)
2. Creates `shared/trader/AGENTS.md` (copies content from selected source)
3. Sets the new agent's `x-claw.agent` in `claw-pod.yml` to `./shared/trader/AGENTS.md`
4. Optionally offers to rewire other agents currently using the source contract (default: no). Only selected agents are updated.
5. Never deletes `agents/<name>/AGENTS.md` automatically. Prints a cleanup hint if a file becomes unused.
6. Clawfiles keep `AGENT AGENTS.md` as a baked image fallback (harmless — pod override takes precedence at runtime)

**Path resolution:** Shared contracts are always referenced via `x-claw.agent` in the pod file using pod-root-relative paths (`./shared/trader/AGENTS.md`). The Clawfile `AGENT` directive is not rewritten — it serves as a fallback baked into the image label and does not need to resolve to the shared path.

### Pod File Update

The CLI parses the existing `claw-pod.yml`, adds the new service block, and writes it back. Uses Go's `gopkg.in/yaml.v3` AST node API to preserve existing formatting and comments as much as possible.

### Mutation Safety Rules

- No implicit destructive actions: no automatic deletion of contracts, env keys, or service blocks.
- Existing-agent rewires (for shared profiles) require explicit confirmation in interactive mode.
- For commands that modify existing files (`claw-pod.yml`, `.env.example`, `.gitignore`), show a planned change summary before writing.
- Add `--dry-run` to print planned file edits without writing.
- Add `--yes` to skip confirmations in scripted usage.

### Flag Overrides

Same pattern as `claw init`: `--type`, `--model`, `--platform`, `--contract <path>`, `--volume <name>:<mode>`, `--layout <auto|canonical|flat>`, plus safety flags `--dry-run` and `--yes`.

## Scope

### In Scope

- ✅ Evolve `claw init` from static scaffolder to interactive (preserve `--from` migration)
- ✅ New `claw agent add` command
- ✅ ADR-011: Canonical Project Layout
- ✅ Reconcile examples policy with non-forcing layout support
  - quickstart: canonical scaffold example
  - multi-claw/trading-desk: intentionally left as hand-authored layouts (no forced migration)
- ✅ Verify `claw build` and `claw up` work with `agents/<name>/` build contexts

### Out of Scope (Future Work)

- `claw agent remove` — deleting agents from the pod file
- `claw agent list` — listing agents in the project
- `claw surface add` — interactive surface scaffolding
- Migration tool for existing flat-layout projects to canonical layout
- Template/starter packs (e.g., `claw init --template trading-desk`)

## Implementation Notes

### Interactive Prompts

Use a Go terminal UI library for interactive prompts. Candidates:
- `github.com/AlecAivazis/survey/v2` — mature, well-known, supports select/input/confirm
- `github.com/charmbracelet/huh` — newer, prettier, from the Charm ecosystem

### YAML Preservation

When `claw agent add` modifies `claw-pod.yml`, it must preserve existing comments and formatting. `gopkg.in/yaml.v3` supports this via node-level manipulation. This is the trickiest part of the implementation.

### Path Resolution

Two distinct path contexts:

- **Clawfile `AGENT` directive** — relative to build context. Canonical example: `AGENT AGENTS.md` in `agents/tiverton/Clawfile` resolves to `agents/tiverton/AGENTS.md`. Flat example: `AGENT AGENTS-westin.md` in `Clawfile.westin` resolves to `AGENTS-westin.md` at pod root. Baked into image labels at build time. Serves as fallback.
- **`x-claw.agent` in pod file** — relative to pod file directory (project root). `./agents/tiverton/AGENTS.md` or `./shared/trader/AGENTS.md`. This is the runtime source of truth and overrides the image label.

For canonical and flat layouts, scaffolded defaults keep these paths aligned. For shared profiles, only `x-claw.agent` is updated; the Clawfile `AGENT` directive remains as a stale-but-harmless image label.

### Source of Truth for Contract Path

`x-claw.agent` in the pod file is the runtime source of truth. The Clawfile `AGENT` directive is a build-time default baked into the image. The pod override always wins. This is consistent with how other x-claw fields (handles, surfaces, etc.) override image labels.
