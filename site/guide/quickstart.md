# Quickstart

Get a governed agent pod running in five minutes.

## Prerequisites

- **Docker Desktop** -- Clawdapus uses Docker for building images and running containers.
- **OpenRouter API key** -- sign up at [openrouter.ai](https://openrouter.ai/) if you don't have one.
- **Discord bot token + guild ID** -- you'll need a Discord bot application. See below if you don't have one yet.

::: details How to create a Discord bot and get your IDs
1. Go to the [Discord Developer Portal](https://discord.com/developers/applications) and click **New Application**. Name it whatever you want.
2. Go to **Bot** in the left sidebar. Click **Reset Token** and copy it — this is your `DISCORD_BOT_TOKEN`.
3. Under **Privileged Gateway Intents**, enable **Message Content Intent**. The bot needs this to read messages.
4. Go to **OAuth2 → URL Generator**. Select scopes: `bot`. Select permissions: `Send Messages`, `Read Message History`. Copy the generated URL and open it to invite the bot to your server.
5. To get your `DISCORD_BOT_ID`: on the **General Information** page, copy the **Application ID**.
6. To get your `DISCORD_GUILD_ID`: in Discord, enable Developer Mode (Settings → Advanced → Developer Mode), then right-click your server name and **Copy Server ID**.
:::

## Install

```bash
curl -sSL https://raw.githubusercontent.com/mostlydev/clawdapus/master/install.sh | sh
```

Verify your environment is ready:

```bash
claw doctor
```

::: tip Building from source
If you prefer to build from source:
```bash
go build -o bin/claw ./cmd/claw
```
:::

## Clone the Quickstart Example

```bash
git clone https://github.com/mostlydev/clawdapus.git
cd clawdapus/examples/quickstart
```

## Configure

Copy the example environment file and fill in your credentials:

```bash
cp .env.example .env
```

Edit `.env` and set:
- `OPENROUTER_API_KEY` -- your OpenRouter API key
- `DISCORD_BOT_TOKEN` -- your Discord bot token
- `DISCORD_BOT_ID` -- your Discord bot's application ID
- `DISCORD_GUILD_ID` -- the Discord server (guild) ID

## Run the Operator Loop

```bash
source .env

# 1. Pull pinned runtime infra, registry-backed pod services, and runner bases
claw pull

# 2. Build this pod's local build: services
claw build

# 3. Compile the pod and launch it
claw up -d
```

Clawdapus keeps the operator loop explicit:

- `claw pull` fetches pinned runtime infra, registry-backed pod services, and refreshes built-in local runner bases such as `openclaw:latest`
- `claw build` builds pod services that declare `build:`
- `claw up` compiles the pod and launches it
- `claw down` tears the pod back down

`claw up` stays strict by default and tells you which command to run when something is missing. `claw up --fix -d` can pull/build missing infra and service images, but runner refresh still happens through `claw pull`. Use `claw pull --no-runners` when you only want the fast pinned-infra and registry-image path.

`claw up` resolves `${...}` placeholders inside `x-claw` metadata from your shell environment and the pod-local `.env` file, so you do not need to duplicate handle IDs or guild IDs into service `environment:` blocks.

## Verify

Check that everything is running:

```bash
claw ps        # assistant + cllama both running
claw health    # both healthy
```

Message `@quickstart-bot` in your Discord server. The bot responds through the governance proxy -- it has no direct API access. The dashboards update live.

When you're done:

```bash
claw down
```

## Dashboards

- **cllama governance proxy** -- port **8181**. Every LLM call in real time: which agent, which model, token counts, cost.
- **Clawdapus Dash** -- port **8082**. Live service health, topology wiring, per-service drill-down, and agent context inspection. The Agents view shows compiled contracts, redacted runtime manifests, and the latest live context snapshot captured by cllama.

## Alternative: Scaffold from Scratch

If you want to start a fresh project instead of cloning the quickstart:

```bash
claw init my-pod
cd my-pod
cp .env.example .env
# Edit .env with your credentials
source .env
claw pull
claw build
claw up -d
```

That scaffolded project follows the same operator loop: `claw pull`, `claw build`, `claw up`, and `claw down`.

Add more agents later:

```bash
claw agent add researcher
```

`claw agent add` preserves your project's existing layout by default. Use `--layout canonical` or `--layout flat` to override auto-detection.

## Install AI Skill

Give your coding agent full operational knowledge of Clawdapus -- the `claw` CLI, Clawfile syntax, claw-pod.yml structure, cllama proxy wiring, driver semantics, and troubleshooting patterns.

```bash
# Recommended: installs to ~/.claude/skills/ and ~/.agents/skills/
# Auto-updates whenever you update the claw binary.
claw skill install
```

Or install manually:

```bash
SKILL_URL="https://raw.githubusercontent.com/mostlydev/clawdapus/master/skills/clawdapus/SKILL.md"

# Claude Code / OpenCode
mkdir -p ~/.claude/skills/clawdapus-cli
curl -sSL "$SKILL_URL" -o ~/.claude/skills/clawdapus-cli/SKILL.md

# Codex CLI / Gemini CLI / OpenCode (shared .agents/skills/ convention)
mkdir -p .agents/skills/clawdapus-cli
curl -sSL "$SKILL_URL" -o .agents/skills/clawdapus-cli/SKILL.md

# Cursor / Windsurf / other .cursorrules-based agents
curl -sSL "$SKILL_URL" >> .cursorrules
```

This gives your coding agent full context on Clawfiles, pod configurations, and the full `claw` CLI surface. When installed via `claw skill install`, the skill auto-updates on every `claw` invocation.

## No credentials yet?

The [Ollama quickstart](https://github.com/mostlydev/clawdapus/tree/master/examples/ollama-quickstart) runs a governed agent against a local model with **zero secrets** — no provider key, no Discord bot. Docker plus the `claw` CLI is the entire prerequisite list, and `claw audit` shows the governance plane working on the first turn.

## Next Steps

- [Hermes Quickstart](/guide/hermes) -- the same loop on the Hermes runner, with its Discord-native tooling.
- [Anatomy of a Claw](/guide/anatomy) -- understand the four layers: runner, contract, persona, and governance proxy.
- [Clawfile Reference](/guide/clawfile) -- the full directive reference for building agent images.
- [cllama Governance Proxy](/guide/cllama) -- credential starvation, cost tracking, and the audit pipeline.
