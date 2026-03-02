# Issue: Implement Support for `nanobot` and `picoclaw` Drivers

**Title:** Integrate `nanobot` and `picoclaw` as Supported Claw Types

**Description:**
The current Claw Type support table reveals uneven support across different runtimes (`openclaw`, `nanoclaw`, `microclaw`, `nullclaw`). To expand the ecosystem and provide ultra-lightweight options, we need to integrate support for two new engines:
1. [HKUDS/nanobot](https://github.com/HKUDS/nanobot) (Python, ~4K LOC, highly modular)
2. [sipeed/picoclaw](https://github.com/sipeed/picoclaw) (Go, <10MB RAM, extremely fast)

Both runtimes follow a similar paradigm (JSON-based configuration, multi-channel support) but offer distinct advantages in terms of footprint and channel integrations.

### Matrix of Features to Address

| CLAW_TYPE | Runtime | Config | HANDLE (Channels) | INVOKE (Scheduling) | CLLAMA (Proxy) | Container | Health |
|-----------|---------|--------|-------------------|---------------------|----------------|-----------|--------|
| `nanobot` | Python | JSON | Telegram, Discord, WhatsApp, Feishu, DingTalk, Slack, Email, QQ, Matrix | TBD (Native schedule/Cron) | Yes (via Config Base URL) | Writable | HTTP Gateway or `pgrep` |
| `picoclaw` | Go | JSON | Telegram, Discord, Slack, WhatsApp, Feishu, LINE, WeCom, QQ, DingTalk | TBD (Native schedule/Cron) | Yes (via Config Base URL) | Read-only | HTTP Health Endpoint |

### Implementation Plan

1. **Config Generation:**
   - Create `internal/driver/nanobot/driver.go` to generate `~/.nanobot/config.json`.
   - Create `internal/driver/picoclaw/driver.go` to generate the expected `config.json`.

2. **HANDLE Support Expansion:**
   - Both runtimes support a much wider variety of channels (WhatsApp, Feishu, QQ, DingTalk, LINE, WeCom, etc.).
   - Update `internal/driver/types.go` and validation logic to allow these new channels across all drivers (or ignore them gracefully if the driver doesn't support them).

3. **INVOKE Scheduling:**
   - Investigate how `nanobot` and `picoclaw` handle scheduled tasks natively.
   - If native support exists, map our `INVOKE` directive to their format. Otherwise, provide a sidecar mechanism or use standard `cron` inside their containers if appropriate.

4. **CLLAMA Integration:**
   - Ensure both engines correctly route their LLM traffic through the `cllama` governance proxy by overriding their base URLs in the generated configurations.

5. **Health Probes:**
   - Implement HTTP-based health probes for both drivers, as both expose gateways/health endpoints.

**Acceptance Criteria:**
- `claw build` and `claw up` work seamlessly with `CLAW_TYPE nanobot` and `CLAW_TYPE picoclaw`.
- Both drivers successfully route traffic through `cllama`.
- At least two new channels (e.g., WhatsApp, Feishu) are successfully mapped to configuration.
- A functional example for both `nanobot` and `picoclaw` is added to the `examples/` directory.

---
*See [docs/plans/2026-03-01-driver-matrix.md](docs/plans/2026-03-01-driver-matrix.md) for full context.*