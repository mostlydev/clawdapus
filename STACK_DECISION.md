# Stack Decision: eClaw vs OpenClaw (Single Agent)

## Requirement

Single agent with:

- heartbeat scheduling
- writable working memory
- command execution

## Result

Use **OpenClaw mode** (`openclaw/`) for this requirement.

## Evidence

### eClaw limits for this exact use case

- eClaw explicitly marks heartbeat wake as no-op: `src/index.ts:271`.
- eClaw command execution path is tied to vibecoding and shells out to `claude` CLI (`src/agent/vibecoding-tool.ts:171`, `src/agent/vibecoding-tool.ts:190`).

### OpenClaw fits directly

- Heartbeat is first-class (`docs/gateway/heartbeat.md:13`, `docs/gateway/configuration.md:2026`).
- Working memory is workspace-native markdown (`docs/concepts/memory.md:10`, `docs/concepts/memory.md:20`).
- Runtime command tools are first-class (`tools.exec`, runtime groups include `exec`/`process`) (`docs/gateway/configuration.md:2047`, `docs/gateway/configuration.md:2220`).

## Practical note

- The generic loop runner (`compose.yml` + `runner/`) can run any shell command (`BOT_COMMAND`) but does not provide OpenClaw-style heartbeat/memory/tooling semantics by itself.
- The OpenClaw runtime lives in `openclaw/` and runs the npm `openclaw` CLI inside its container.
