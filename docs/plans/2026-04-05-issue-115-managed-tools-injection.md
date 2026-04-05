# Investigation: managed tools compiled but not injected at runtime (#115)

**Status:** Confirmed in source tree; observability fix landed
**Issue:** [#115](https://github.com/mostlydev/clawdapus/issues/115)
**Reporter environment:** Tiverton House pod, `claw` v0.5.1, `ghcr.io/mostlydev/cllama:latest`, OpenClaw `2026.3.24`

## Symptom

Operator runs `claw up -d` on a pod with a v2 descriptor service (`trading-api`) that
declares `tools[]`, and an OpenClaw agent (`weston`) subscribed via `x-claw.tools`.

- `.claw-runtime/context/weston/tools.json` is compiled correctly and lists
  `trading-api.propose_trade`, `trading-api.get_market_context`, etc.
- Live `cllama` logs show `feed_fetch` events but **zero** evidence of tool loading,
  injection, or mediation for Weston.
- Weston reports in Discord that he has no managed trading tools in his effective
  toolset and falls back to mounted shell scripts.

## What the source already implements

Managed tool mediation is fully wired in `cllama`:

- `cllama/internal/agentctx/agentctx.go` — `loadToolsManifest()` reads
  `tools.json` and populates `AgentContext.Tools` on every agent-context load.
- `cllama/internal/proxy/handler.go` — both the OpenAI path (`handleOpenAI`) and
  the Anthropic path (`handleAnthropicMessages`) call `hasManagedTools()` and
  `injectManagedOpenAITools()` / `injectManagedAnthropicTools()` before
  dispatching upstream, and call `handleManagedOpenAI` / `handleManagedAnthropic`
  for tool-aware response handling.
- `cllama/internal/proxy/toolmediation.go` — executes tool calls, logs
  `tool_call` events via the session-history writer, and restreams synthetic SSE.

So the "no injection" symptom cannot be explained by missing code on `master`.

## Hypotheses to verify

### H1 — Published `cllama:latest` lags the source tree

If the image tag the operator pulled predates commit `7c5aeb2`
("Load and inject managed tool manifests"), the running container has no tool
mediation regardless of source state. Verification:

```bash
docker image inspect ghcr.io/mostlydev/cllama:latest \
  --format '{{index .Config.Labels "org.opencontainers.image.revision"}}'
docker run --rm ghcr.io/mostlydev/cllama:latest --version
```

Compare the commit against the submodule pointer in this repo. Fix: pin infra
image tags per ADR-022 so the operator cannot silently run stale mediation code.

### H2 — `tools.json` is present on the host but not mounted into cllama

`tools.json` lives under `.claw-runtime/context/<agent-id>/`. cllama's bind
mount maps that directory tree under `/claw/context/`. If a subtle path bug
(missing `context/` segment, wrong agent-id casing) prevents cllama from seeing
it, `loadToolsManifest` returns `nil` silently (no tools.json → no error), and
`hasManagedTools` returns `false` for the entire request lifecycle. Verification:

```bash
docker compose exec cllama ls -la /claw/context/weston/
docker compose exec cllama cat /claw/context/weston/tools.json | jq '.tools | length'
```

### H3 — Agent-id mismatch between handler auth and context directory

`loadContext(agentID)` resolves the context dir from the bearer-token-parsed
agent id. If the minted bearer embeds a slightly different id than the context
directory name (e.g. due to a recent rename, or an ordinal suffix not present on
the bearer side), context loads still succeed via `weston` but tools resolve
against a different dir. Verification: compare the `aud` / subject in the
minted bearer against `.claw-runtime/context/*/`.

### H4 — Tool injection silently dropped when request uses a non-standard path

`ServeHTTP` routes `/v1/messages` → Anthropic flow, everything else → OpenAI
flow. If OpenClaw sends requests to an unrecognized path (e.g. provider-native
prefix), the OpenAI flow still runs and injection should still happen — but
worth confirming from `cllama` access logs.

### H5 — Tool injection happens but the upstream provider drops `tools[]`

openrouter and some format bridges can silently drop unsupported fields. If
injection is working but upstream is stripping, the agent reports no tools even
though cllama did its job. Verification: tail the cllama structured log and
confirm `tool_call` / `tool_trace` events appear (or don't) for Weston during a
live turn.

## Conclusion

The current `master` tree already contains the managed tool mediation path. In
particular:

- `tools.json` is compiled into `.claw-runtime/context/<agent-id>/`
- cllama loads that manifest from `/claw/context/<agent-id>/tools.json`
- both OpenAI and Anthropic request paths inject provider-native tool schemas
  and route into the mediation loop when managed tools are present

The main repo-local gap behind the Tiverton report was **observability**: the
structured logs emitted `feed_fetch`, `request`, and `response`, but nothing
that told an operator whether cllama had actually loaded a tool manifest for a
given turn. This branch closes that gap with a `tool_manifest_loaded` event
containing `manifest_present` and `tools_count`.

## Remaining live checks

1. Run H1 + H2 verifications on `tiverton` — these are fastest to rule out.
2. If H1 confirms stale image, pin the tag (ADR-022, #116) and republish
   `cllama:latest` from current `master`; no code change needed in clawdapus.
3. If H2/H3 confirms a path/id mismatch, fix in clawdapus context materializer.
4. Use the new `tool_manifest_loaded` event to confirm what the live pod sees on
   each request before assuming a compile-time or runtime wiring bug.

## Fix landed here

- `cllama` now logs `tool_manifest_loaded` on every chat request with:
  - `manifest_present`
  - `tools_count`
- parent-repo audit normalization now preserves those fields so the event
  remains visible in operator tooling

This does not rule out **H1** in Tiverton specifically; a stale published image
is still plausible. It does rule out the simpler theory that the current source
tree is missing managed tool mediation entirely.
