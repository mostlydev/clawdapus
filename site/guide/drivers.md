# Driver Support Matrix

Clawdapus drivers adapt the generic governance model to specific agent runtimes. Pick a driver based on what your agent needs. All drivers support `MODEL`, `AGENT`, `CLLAMA`, and `CONFIGURE`.

## Feature Matrix

| | `openclaw` | `hermes` | `nanoclaw` | `nanobot` | `picoclaw` | `nullclaw` | `microclaw` |
|---|:---:|:---:|:---:|:---:|:---:|:---:|:---:|
| **Runtime** | [OpenClaw](https://openclaw.ai) | [Hermes](https://github.com/NousResearch/hermes-agent) | [Claude Agent SDK](https://github.com/anthropics/claude-code) | [Nanobot](https://github.com/HKUDS/nanobot) | [PicoClaw](https://github.com/sipeed/picoclaw) | [NullClaw](https://github.com/nullclaw/nullclaw) | [MicroClaw](https://github.com/microclaw/microclaw) |
| `claw init` scaffold | yes | yes | yes | yes | yes | yes | yes |
| HANDLE: Discord | yes | yes | -- | yes | yes | yes | yes |
| HANDLE: Telegram | -- | yes | -- | yes | yes | yes | yes |
| HANDLE: Slack | -- | yes | -- | yes | yes | yes | yes |
| HANDLE: long-tail | -- | -- | -- | -- | yes | -- | -- |
| INVOKE (cron) | yes | yes | -- | yes | yes | yes | -- |
| Structured health | yes | yes | -- | -- | yes | yes | -- |
| Read-only rootfs | yes | yes | -- | yes | yes | yes | -- |
| Non-root container | -- | -- | -- | -- | yes | -- | -- |

**PicoClaw long-tail platforms:** WhatsApp, Feishu, LINE, QQ, DingTalk, OneBot, WeCom, WeCom App, Pico, MaixCam.

`claw init` also scaffolds a `generic` type (alpine:3.20, no driver enforcement) for custom runtimes.

## Driver Behavior

### All Drivers

- All drivers set `mention_only` (or the driver's equivalent) for Discord channels to prevent feedback loops in multi-agent pods.
- All drivers explicitly set `HOME` in the container env to match their config mount path.
- Runtime directories use `0o777` permissions so container users with different UIDs can write.

### openclaw

The OpenClaw driver generates `openclaw.json` configuration and supports the full set of Discord routing controls:

| `channel://discord` setting | Supported |
|---|:---:|
| DM `policy` (pairing, allowlist, open, disabled) | yes |
| DM `allowFrom` | yes |
| Guild `requireMention` | yes |
| Guild `users[]` allowlist | yes |
| `allow_from_handles: true` (expands into guild users) | yes |
| `allow_from_services: [svc...]` (derives Discord IDs) | yes |
| Guild `policy` | no |

Guild-level `policy` is rejected at config generation time rather than producing a config the runtime rejects at boot.

cllama wiring for OpenClaw uses `models.providers.<provider>.{baseUrl, apiKey, api, models}` -- not `agents.defaults.model.baseURL/apiKey`.

OpenClaw state uses its canonical home directory inside the container: `~/.openclaw` (`/root/.openclaw`). Clawdapus mounts the generated config at `/root/.openclaw/config/openclaw.json`. The runtime keeps writable tmpfs mounts at both `/root` and `/root/.openclaw` (mode 1777): the parent `/root` overlay guarantees non-root users can traverse into the home directory, and the nested `~/.openclaw` overlay guarantees Docker does not leave the state root behind as `0755 root:root` when it creates the config bind-mount target.

### hermes

The Hermes driver emits `HERMES_DEFAULT_AGENT_IDENTITY` so the Hermes runner starts its system prompt with a Clawdapus-managed identity instead of the upstream generic `You are Hermes Agent` block. It also writes a service-specific `SOUL.md`; when a persona is configured, the persona's SOUL.md takes priority.

Container env vars from compose `environment:` are not available in Hermes agent tool execution. Only vars listed in `allowedEnvPassthroughKeys()` reach the tool runtime via the `.env` file. New env vars that agents need must be added to this allowlist.

### nanoclaw

Claude Agent SDK-based driver. Does not currently support HANDLE, INVOKE, or structured health probes.

### nanobot

Nanobot driver with generated config and Discord/Telegram/Slack handle wiring. Supports INVOKE scheduling and read-only rootfs.

### picoclaw

PicoClaw is the most platform-diverse driver, supporting the long-tail of chat platforms beyond Discord/Telegram/Slack. Supports model-list config, non-root containers, and structured health probes.

### nullclaw

NullClaw supports CONFIGURE for fine-grained runtime config mutations. Use `CONFIGURE nullclaw config set <path> <value>` to pin guild IDs, set mention requirements, configure Telegram allowlists, or select Slack transport modes.

### microclaw

Minimal driver supporting Discord, Telegram, and Slack handles. Does not support INVOKE scheduling or structured health probes.

## Choosing a Driver

- **Need Discord routing controls?** `openclaw` has the richest Discord config support.
- **Need Telegram or Slack?** `hermes`, `nanobot`, `picoclaw`, `nullclaw`, or `microclaw`.
- **Need WhatsApp, LINE, or other platforms?** `picoclaw` is the only option.
- **Need non-root containers?** `picoclaw`.
- **Need fine-grained runtime config?** `nullclaw` with `CONFIGURE`.
- **Just need a governed container?** `generic` type gives you an alpine base with no driver enforcement.
