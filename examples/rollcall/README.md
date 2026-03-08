# Rollcall

End-to-end driver parity fixture for Clawdapus.

This example boots six different runtime families in one pod, wires them through
`cllama` passthrough, exposes `clawdash`, posts a Discord roll-call prompt, and
verifies that each runtime replies identifying itself.

## What It Covers

- `openclaw`
- `nullclaw`
- `microclaw`
- `nanoclaw`
- `nanobot`
- `picoclaw`

Each service shares the same Discord bot token. The distinction between agents
comes from their `AGENTS.md` contracts and per-service runtime configuration.

## Files

- `claw-pod.yml`: six agent services plus shared proxy/dashboard wiring
- `agents/*/Clawfile`: one Clawfile per runtime
- `agents/*/AGENTS.md`: minimal runtime-specific self-identification contract
- `Dockerfile.*-base`: local base images used by the spike test
- `discord-responder.sh`: helper script used by the stub runtimes

## Setup

```bash
cp .env.example .env
# Edit .env with real values
```

Required variables:

- `DISCORD_BOT_TOKEN`
- `DISCORD_BOT_ID`
- `DISCORD_GUILD_ID`
- `ROLLCALL_CHANNEL_ID`
- `DISCORD_WEBHOOK_URL`
- `OPENROUTER_API_KEY` or `ANTHROPIC_API_KEY`

`DISCORD_WEBHOOK_URL` is required because the spike posts the trigger message via
webhook rather than as a bot user. That avoids agents ignoring the message as
self-authored bot traffic.

## Run

From the repo root:

```bash
go test -tags spike -v -run TestSpikeRollCall ./cmd/claw/...
```

Or from this directory:

```bash
go test -tags spike -v -run TestSpikeRollCall ../../cmd/claw/...
```

## Expected Result

The test should:

1. Build the base images for each runtime family if needed.
2. Build the six rollcall agent images.
3. Run `claw up` on this pod.
4. Wait for each container to become healthy or running.
5. Post a Discord roll-call message through the webhook.
6. Observe six AI-generated replies mentioning:
   - `openclaw`
   - `nullclaw`
   - `microclaw`
   - `nanoclaw` (or `Claude Agent SDK`)
   - `nanobot`
   - `picoclaw`
7. Confirm `cllama` cost data is reachable.

## Notes

- This is a live spike test, not a CI-safe example.
- Real Discord and model-provider credentials are required.
- Cleanup is automatic on normal completion and on Ctrl-C.
