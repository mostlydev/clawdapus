# Rollcall

End-to-end driver parity fixture for Clawdapus.

This fixture reuses one Discord bot identity across seven runtime families, so
the spike now materializes one runtime at a time rather than pretending to be a
concurrent social-topology test. Each subtest wires one runtime through
`cllama` passthrough, exposes `clawdash`, posts a Discord mention, and verifies
that the runtime replies identifying itself.

## What It Covers

- `openclaw`
- `nullclaw`
- `microclaw`
- `nanoclaw`
- `nanobot`
- `picoclaw`
- `hermes`

Each service shares the same Discord bot token. The distinction between agents
comes from their `AGENTS.md` contracts and per-service runtime configuration.
Because the identity is shared, this fixture is for sequential runtime
conformance only. It is not a valid concurrent topology example.

## Files

- `claw-pod.yml`: spike template containing the seven runtime service definitions
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
- `XAI_API_KEY` or `OPENROUTER_API_KEY` or `ANTHROPIC_API_KEY`

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

For the integrated capability-wave path on `oc-roll` only:

```bash
go test -tags spike -v -run TestSpikeCapabilityWaveLive ./cmd/claw/...
```

## Expected Result

The test should:

1. Build the base images for each runtime family if needed.
2. Build the seven rollcall agent images.
3. Materialize and run one single-service pod per runtime.
4. Wait for each runtime container to become healthy or running.
5. Post a Discord mention through the webhook for that runtime.
6. Observe seven AI-generated replies across the full test run mentioning:
   - `openclaw`
   - `nullclaw`
   - `microclaw`
   - `nanoclaw` (or `Claude Agent SDK`)
   - `nanobot`
   - `picoclaw`
   - `hermes`
7. Confirm `claw audit` telemetry appears for each runtime's cllama-backed turn.

The dedicated capability-wave spike additionally confirms that `oc-roll` can:

1. receive a real Discord trigger
2. execute a managed service tool through cllama mediation
3. record `tool_trace` in session history
4. emit `memory_op` recall and retain telemetry in the same turn

## Notes

- This is a live spike test, not a CI-safe example.
- Real Discord and model-provider credentials are required.
- Cleanup is automatic on normal completion and on Ctrl-C.
- `claw-pod.yml` is a spike template for the sequential test flow, not a
  directly runnable concurrent shared-identity topology pod.
