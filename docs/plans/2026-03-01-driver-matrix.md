# Driver Matrix & Integration Plan

## Feature Support Matrix

| CLAW_TYPE | Runtime | Config | HANDLE (Channels) | INVOKE (Scheduling) | CLLAMA (Proxy) | Container | Health |
|-----------|---------|--------|-------------------|---------------------|----------------|-----------|--------|
| `openclaw` | Node/TS | JSON5 | Discord | `jobs.json` | Yes | Read-only | `openclaw health --json` |
| `nullclaw` | Node/TS | JSON | Discord, Telegram, Slack | CLI cron | Yes | Read-only | HTTP `:3000/health` |
| `nanobot` | Python | JSON | Telegram, Discord, WhatsApp, Feishu, DingTalk, Slack, Email, QQ, Matrix | Built-in / TBD | Yes (via base URL) | Writable workspace | HTTP Gateway / `pgrep python` |
| `picoclaw` | Go | JSON | Telegram, Discord, Slack, WhatsApp, Feishu, LINE, WeCom, QQ, DingTalk, OneBot | Built-in / TBD | Yes (via base URL) | Read-only | HTTP Health Endpoint |

## Integration Plan

### 1. `nanobot` Integration
`nanobot` is an ultra-lightweight Python-based assistant (~4k LOC). It relies heavily on a `config.json` file and a local workspace.

*   **Config:** We will write a driver `internal/driver/nanobot` that generates the `~/.nanobot/config.json`.
*   **HANDLE:** Map `HANDLE telegram`, `HANDLE discord`, `HANDLE slack`, etc., directly into the `channels` object of the JSON config. It supports many more channels than our current drivers, so we should expand our allowed platforms in `types.go`.
*   **INVOKE:** Research how `nanobot` natively stores scheduled tasks. If it uses a local database or JSON file, we will pre-populate it during the `Materialize` step. Otherwise, we can leverage an external cron to hit its gateway.
*   **CLLAMA:** `nanobot` supports API Base URL overrides. We'll configure `providers.openrouter.base_url` or inject `ANTHROPIC_BASE_URL` to route through the `cllama` proxy.
*   **Health:** `nanobot` spins up a gateway. We can use `curl -fsS http://localhost:<port>/health` or fallback to `pgrep python`.

### 2. `picoclaw` Integration
`picoclaw` is a Go-based assistant that runs in <10MB RAM. It uses a single binary and a `config.json` structure.

*   **Config:** Implement `internal/driver/picoclaw` that marshals a corresponding `config.json`.
*   **HANDLE:** Map `HANDLE` directives to `Channels` in `picoclaw`'s config (which includes Telegram, Discord, WhatsApp, Feishu, LINE, WeCom, QQ, DingTalk).
*   **INVOKE:** Like `nanobot`, we need to determine its scheduling mechanism. We might need to implement a sidecar or use an API endpoint if it supports one.
*   **CLLAMA:** `picoclaw` has a `ProvidersConfig` where we can specify the `APIBase`. We will inject the proxy URL here.
*   **Container:** The container can be extremely minimal (scratch or alpine) and mostly read-only, except for its data directory.
*   **Health:** `picoclaw` explicitly registers a health endpoint in its channel manager, making it easy to probe via HTTP.

## Next Steps
1. Create a GitHub Issue detailing this plan.
2. Extend `ResolvedClaw` and `HandleInfo` validation to support the new extended list of channels (WhatsApp, Feishu, LINE, WeCom, QQ, DingTalk, Email, Matrix).
3. Implement `internal/driver/nanobot` package.
4. Implement `internal/driver/picoclaw` package.
