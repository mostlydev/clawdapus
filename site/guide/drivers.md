# Driver Support Matrix

Clawdapus drivers adapt the generic governance model to specific agent runtimes. Pick a driver based on what your agent needs. All drivers support `MODEL`, `AGENT`, `CLLAMA`, and `CONFIGURE`.

New to Hermes? Start with the dedicated [Hermes quickstart](/guide/hermes).

## Feature Matrix

| | `openclaw` | `hermes` | `nanobot` | `picoclaw` |
|---|:---:|:---:|:---:|:---:|
| **Runtime** | [OpenClaw](https://openclaw.ai) | [Hermes](https://github.com/NousResearch/hermes-agent) | [Nanobot](https://github.com/HKUDS/nanobot) | [PicoClaw](https://github.com/sipeed/picoclaw) |
| `claw init` scaffold | yes | yes | yes | yes |
| HANDLE: Discord | yes | yes | yes | yes |
| HANDLE: Telegram | yes | yes | yes | yes |
| HANDLE: Slack | yes | yes | yes | yes |
| HANDLE: long-tail | -- | -- | -- | yes |
| INVOKE (cron) | yes | yes | yes | yes |
| Structured health | yes | yes | yes | yes |
| Read-only rootfs | yes | yes | yes | yes |
| Non-root container | -- | -- | -- | yes |

::: info Retired drivers
`nanoclaw`, `microclaw`, and `nullclaw` were retired in
[ADR-026](https://github.com/mostlydev/clawdapus/blob/master/docs/decisions/026-runner-adoption-and-retirement.md).
Their `CLAW_TYPE`s fail `claw up` with a migration message pointing to Hermes.
:::

**PicoClaw long-tail platforms:** WhatsApp, Feishu, LINE, QQ, DingTalk, OneBot, WeCom, WeCom App, Pico, MaixCam.

`claw init` also scaffolds a `generic` type (alpine:3.20, no driver enforcement) for custom runtimes.

## Driver Behavior

### All Drivers

- Chat-capable drivers that configure Discord handles set `mention_only` (or the driver's equivalent) for Discord channels to prevent feedback loops in multi-agent pods.
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

#### Pinned runtime and durable state

The built-in runner pins OpenClaw **2026.10.5** and Node **24.22.0**. The Node base is pinned by multi-platform digest and the npm tarball is checked against its published SHA512. Run `claw pull`, `claw build`, then `claw up -d` to refresh runner-derived service images. Custom bases need rebuilding for `agents.entries.main` and the compiled wake helper.

Discord and Slack are now separate official OpenClaw packages. Their matching, integrity-checked versions are baked into the runner under `/opt/claw-openclaw-plugins/`; `claw up` compiles loader paths for declared, enabled channels. Telegram remains bundled. Custom bases must provide those plugin paths. An operator plugin allowlist, denylist or disabled entry that excludes a required channel fails compilation. Telegram and Slack use `botToken`; Discord retains `token`. Slack also compiles `appToken` when `SLACK_APP_TOKEN` is provided.

Managed providers select the built-in OpenClaw runtime explicitly. Behavioral `CONFIGURE` settings still apply, but conflicting provider routing, credentials, catalogs or runtime selection fail compilation. Main, image, subagent and heartbeat model selections must resolve to declared `MODEL` slots or aliases of those models. Provider credential and routing variables in `CONFIGURE env` are rejected. Request-level proxies, alternate authentication strategies and authentication-header replacements are also rejected; ordinary timeouts, TLS settings and diagnostic headers remain configurable. Disable cllama explicitly for direct provider routing. Tool denials, pairing, mention requirements and upstream approval/sandbox safeguards remain in effect; the refresh does not grant Full Access or a broader tool profile.

SQLite state and main-agent transcripts persist per compose identity under `.claw-memory/<service>/openclaw-state/<compose-service>/{state,sessions}`. The driver sets `session.store` to `/root/.openclaw/agents/main/sessions/openclaw-agent.sqlite`: current upstream otherwise stores the live database beside model and auth catalogs, while `sessions/` holds archives. Generated config remains a separate rotating mount. Plugins, model catalogs, auth profiles and caches stay temporary, so writable extensions from an earlier image do not survive recreation. Parent tmpfs mounts keep the layout writable for non-root users. The entrypoint forwards shutdown and drains the Gateway so SQLite ownership is released before recreation. These state directories use existing writable runner permissions: host UIDs are trusted and transcripts are not isolated from other host users. A custom `session.store` outside the mounted directories needs its own persistent mount.

#### Compiled schedules

Both image `INVOKE` and pod `invoke` entries compile into `schedule.json`, with `claw-api` owning their timing. Image-only schedules therefore add that sidecar even without a master. The retired OpenClaw `cron/jobs.json` is no longer generated. Each wake starts a Gateway-backed turn with a fresh session ID. The helper accepts positional arguments and passes the task unchanged through a private temporary file to the native CLI; the file is removed when the helper exits. The existing 300-second wake default is preserved. An explicitly configured `agents.defaults.timeoutSeconds` must be 1–900 for every OpenClaw service; zero no longer selects an unbounded timeout. The process deadline allows five seconds beyond the native deadline for its structured result, then ten seconds to terminate an unresponsive CLI. The scheduler adds twenty seconds for process overhead, and manual-fire transports cover the maximum supported wake.

An explicit `to` carries its resolved platform. Unknown Discord channel names and ambiguous platforms fail compilation; raw numeric Discord IDs remain supported. Without a target, the helper does not guess a channel or automatically deliver final text: the task can send through its configured messaging tool. Explicit final delivery uses OpenClaw's native delivery path.

Frequent schedules create frequent durable sessions. Configure native `session.maintenance` for your retention policy; for example, `mode: enforce`, `pruneAfter: 30d` and `maxEntries: 1000`. Clawdapus keeps upstream maintenance defaults rather than silently deleting conversation history.

Previously generated `.claw-runtime/<service>/cron/jobs.json` files do not block a normal recompile; image and pod declarations regenerate their schedules. Preserve separately managed native cron state and migrate it through OpenClaw **2026.9.7** Doctor before upgrading: current upstream no longer imports that retired JSON store. The driver rejects legacy `cron/jobs.json` if an operator has copied it into the new durable state root; it does not migrate arbitrary old runtime edits. See [upstream migration guidance](https://github.com/openclaw/openclaw/blob/v2026.10.5/docs/automation/cron-jobs/managing-jobs.md#legacy-store-migration).

### hermes

The Hermes driver emits `HERMES_DEFAULT_AGENT_IDENTITY` so the Hermes runner starts its system prompt with a Clawdapus-managed identity instead of the upstream generic `You are Hermes Agent` block. It also writes a service-specific `SOUL.md`; when a persona is configured, the persona's SOUL.md takes priority.

Container env vars from compose `environment:` are not available in Hermes agent tool execution. Only vars listed in `allowedEnvPassthroughKeys()` reach the tool runtime via the `.env` file. New env vars that agents need must be added to this allowlist.

Hermes runner memory defaults to a 12000-character memory index cap and a
6000-character user-memory cap. Services can override those caps with
`HERMES_MEMORY_INDEX_MAX_CHARS` and `HERMES_USER_MEMORY_MAX_CHARS`; the managed
base image evicts oldest entries on overflow when the new entry can fit after
eviction and rewrites `MEMORY.md` as mode `0666` so host-side diagnostics remain
readable across writes.

For Discord handles, the Hermes driver disables the upstream `text_to_speech` tool by default so agents reply in text instead of model-selected voice attachments. A service can opt back in with `x-claw.hermes.allow-tools: [text_to_speech]`.

For Discord, Slack, and Telegram handles, the driver compiles explicit Hermes
`platform_toolsets` so the runner's native web, terminal, and file tools are an
inspectable part of the generated config instead of an implicit upstream
fallback. `FIRECRAWL_API_KEY` and `FIRECRAWL_API_URL` are passed through to the
Hermes `.env` when set, so configured Firecrawl access reaches tool execution.
Hermes also defaults `NO_PROXY`/`no_proxy` to `localhost,127.0.0.1,cllama` so a
Docker-injected host proxy cannot break local cllama traffic; explicit operator
`NO_PROXY` overrides are preserved.

Hermes Discord and Slack services default to silent-final handling because the
visible reply often goes through the `send_message` tool. Empty visible final
responses after tool calls complete as no-op turns instead of surfacing retry
or nudge warnings to the channel. Operators can disable this per service with
`HERMES_ALLOW_SILENT_FINAL=0`, or enable it explicitly for other Hermes
surfaces with `x-claw.hermes.allow-silent: true`. Transient cron upstream
failures are logged and recorded as failed runs without user-channel delivery
by default; set `HERMES_CRON_DELIVER_TRANSIENT_FAILURES=1` to restore the old
delivery behavior.

Managed Hermes chat services also set `HERMES_CHAT_STATUS_DELIVERY=off` and
`display.memory_notifications: off` by default. Retry/fallback/lifecycle status
callbacks and background-review summaries stay in logs instead of being posted
as channel prose; set `HERMES_CHAT_STATUS_DELIVERY=on` or configure
`display.memory_notifications` when an interactive/debug channel should show
that runtime telemetry. Unset `HERMES_CHAT_STATUS_DELIVERY` preserves upstream
Hermes behavior; the quiet default is applied by the Clawdapus driver.

### nanobot

Nanobot driver with generated config and Discord/Telegram/Slack handle wiring. Supports INVOKE scheduling and read-only rootfs.

### picoclaw

PicoClaw is the most platform-diverse driver, supporting the long-tail of chat platforms beyond Discord/Telegram/Slack. Supports model-list config, non-root containers, and structured health probes.

## Choosing a Driver

- **Need Discord routing controls?** `openclaw` has the richest Discord config support.
- **Need Telegram or Slack?** `openclaw`, `hermes`, `nanobot`, or `picoclaw`.
- **Need WhatsApp, LINE, or other platforms?** `picoclaw` is the only option.
- **Need non-root containers?** `picoclaw`.
- **Need fine-grained runtime config?** every driver supports `CONFIGURE <runner> config set <path> <value>`.
- **Just need a governed container?** `generic` type gives you an alpine base with no driver enforcement.
