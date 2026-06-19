# Issue #137 — picoclaw upstream gateway port=0 breaks spike test

Status: Drafted by Claude, pending codex design challenge before implementation.

## Problem

`TestSpikeRollCall/picoclaw` fails because the picoclaw container crash-loops on:

```
FTL gateway src/pkg/gateway/gateway.go:139 > config pre-check failed: invalid gateway port: 0, port must be between 1 and 65535
```

The clawdapus picoclaw driver (`internal/driver/picoclaw/config.go`) writes `config.json` without a `gateway` block. The picoclaw binary loads it, `Gateway.Port` falls to the Go zero value (`0`), and `preCheckConfig` (upstream `pkg/gateway/gateway.go`) rejects it.

This is masked by two things:
1. The subtest is gated on a real provider key, so it only runs when `ANTHROPIC_API_KEY` (etc.) is present.
2. `Dockerfile.picoclaw-base` does `git clone --depth 1 https://github.com/sipeed/picoclaw.git`, pulling upstream HEAD — when the binary's compiled-in defaults stopped covering missing config sections, the regression became visible.

## Upstream context (verified against `sipeed/picoclaw@v0.2.7`)

- `pkg/config/gateway.go` declares `GatewayConfig{Host string, Port int, HotReload bool, LogLevel string}`.
- `pkg/config/defaults.go` `DefaultConfig()` returns `Gateway{Host: "localhost", Port: 18790, ...}`. This default only applies when the entire config file is absent or empty (`LoadConfig` falls back to `DefaultConfig()`); it does **not** fill missing fields when parsing a partially-populated JSON.
- `pkg/gateway/gateway.go` `preCheckConfig` rejects `Port <= 0 || Port > 65535`.
- `PICOCLAW_GATEWAY_PORT` / `PICOCLAW_GATEWAY_HOST` env overrides exist but require explicit env-var injection — currently neither the driver nor the Dockerfile sets them.

Conclusion: the long-term fix is for our driver to emit `gateway.host` and `gateway.port` explicitly, matching how `internal/driver/openclaw` writes its own port config. The driver should not rely on undocumented upstream defaults.

## Scope (v1)

### 1. Driver writes a `gateway` block

In `internal/driver/picoclaw/config.go::GenerateConfig`, after the `agents.defaults.*` writes and before `model_list`, insert:

```go
if err := shared.SetPath(config, "gateway.host", picoclawGatewayHost); err != nil {
    return nil, fmt.Errorf("config generation: %w", err)
}
if err := shared.SetPath(config, "gateway.port", picoclawGatewayPort); err != nil {
    return nil, fmt.Errorf("config generation: %w", err)
}
```

New constants colocated at the top of `config.go`:

```go
const (
    picoclawGatewayHost = "localhost"
    picoclawGatewayPort = 18790
)
```

(18790 is the historical port already referenced by `internal/driver/picoclaw/driver.go::healthURL`/`readyURL` and `EXPOSE 18790` in the Dockerfile, so this is consistent with everything that already assumes the binary listens there.)

`CONFIGURE picoclaw config set gateway.port <n>` already works (the existing CONFIGURE pass runs after this write and overlays operator paths via `shared.SetPath`), so operators can still override per service without touching driver code.

### 2. Dockerfile pin

Change `examples/rollcall/Dockerfile.picoclaw-base`:

```dockerfile
RUN git clone --depth 1 --branch v0.2.7 https://github.com/sipeed/picoclaw.git .
```

`v0.2.7` is the latest upstream tag (2026-04-22). Pinning is belt-and-braces — the driver fix above is the actual root cause fix; the pin protects the spike test from future upstream regressions on unrelated knobs.

### 3. Unit tests

In `internal/driver/picoclaw/config_test.go`, add cases asserting:

- **TestGenerateConfigEmitsGatewayBlock**: A minimal valid `ResolvedClaw` produces JSON with `gateway.host == "localhost"` and `gateway.port == 18790`.
- **TestGenerateConfigGatewayOverridableViaConfigure**: Adding `Configures: []string{"picoclaw config set gateway.port 19000"}` to the same input produces `gateway.port == 19000` in the output (i.e. CONFIGURE wins because it runs last).

Both reuse the existing `getPath` helper already in `config_test.go`.

### 4. Spike verification (no code change)

Spike test `TestSpikeRollCall/picoclaw` is the existing acceptance gate. No change to the spike body itself. Manual run on a machine with `ANTHROPIC_API_KEY`:

```bash
go test -tags spike -run 'TestSpikeRollCall/picoclaw' ./cmd/claw/...
```

## Non-goals

- **Promoting picoclaw to lockstep release pinning** (similar to how cllama is pinned in `release_manifest.go`). Picoclaw upstream is not our infra image — it's a downstream of a third-party tool. The submodule pattern doesn't apply. A Dockerfile `git clone --branch` pin is sufficient.
- **Adding a port field to the driver's `ResolvedClaw` surface.** Not justified by any current pod-yaml use case. Operators who genuinely need a non-default port can use `CONFIGURE picoclaw config set gateway.port`.
- **Bumping `examples/rollcall/Dockerfile.picoclaw-base` past v0.2.7 to track HEAD.** Out of scope; that's a separate "follow upstream" decision the maintainer should make explicitly.
- **Touching the host validation.** Upstream accepts `localhost` (their default) and our driver only ever runs the binary inside the container, so `localhost` matches `EXPOSE 18790` plus the `healthURL` + `readyURL` curls done from inside the container. No host wiring change needed.

## Risks

- **Operator overriding host.** If someone CONFIGUREs `gateway.host = 0.0.0.0` to expose to other containers in the same network, our healthcheck `curl http://localhost:18790/health` still works (loopback), but external reachability becomes their responsibility. No driver change needed.
- **Future upstream rename of `gateway.port`.** Mitigated by the v0.2.7 pin. If we ever need to track upstream further, we re-evaluate at bump time.
- **Multiple picoclaw services in one pod.** Each container has its own network namespace, so port 18790 inside each container is fine. No port collision.

## Test matrix

| Layer | Test | Expectation |
|-------|------|-------------|
| Unit | `TestGenerateConfig*` (new) | `gateway.host=localhost`, `gateway.port=18790` baseline |
| Unit | `TestGenerateConfig*Override` (new) | CONFIGURE override wins |
| Unit | existing picoclaw config_test.go | unchanged, still green |
| Vet | `go vet ./...` | green |
| Spike (manual) | `TestSpikeRollCall/picoclaw` | passes against fresh `Dockerfile.picoclaw-base` build |

## Open questions for codex

1. Should the Dockerfile `--branch v0.2.7` pin be done in the same PR as the driver fix, or split? (Proposal: same PR; both are part of the bug.)
2. Worth lifting the constants `picoclawGatewayHost`/`picoclawGatewayPort` to package-public `Default*` so external callers (tests, future driver introspection) can reference them without grepping the source? (Proposal: keep them unexported; only the driver and its tests need them.)
3. Should we emit a brief CLAUDE.md note about "drivers must write all required config sections, not rely on upstream defaults"? (Proposal: no — the driver-level gotchas section in CLAUDE.md is for repo-specific gotchas with cross-driver implications. This is a single-driver fix and the test is the durable contract.)

## Workflow

- Plan drafted by Claude (this doc).
- Codex: design challenge in a talking-stick note. If consensus, proceed to implementation.
- Codex implements: edit `config.go`, `config_test.go`, `Dockerfile.picoclaw-base`. Runs `go test ./internal/driver/picoclaw/...` and `go vet ./...` locally. Commits and passes stick back.
- Claude: runs full unit suite, decides on release. This is a behavior fix in a non-shipped path (spike-only fixture + driver fix that only matters when picoclaw is used). It's a candidate for v0.14.2 patch, but the lockstep release calculus depends on whether anything else is pending.

## Acceptance

- `go test ./internal/driver/picoclaw/...` green with new test cases.
- `go vet ./...` green.
- `Dockerfile.picoclaw-base` pinned to `v0.2.7` (or whatever upstream tag the maintainer accepts).
- `TestSpikeRollCall/picoclaw` passes when the operator runs it with provider credentials.
- Issue #137 closes via PR body keyword.
