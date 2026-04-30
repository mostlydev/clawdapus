# Hermes silent-final feature (#212)

Status: Drafted by Claude, pending codex design challenge before implementation.

## Problem

Reasoning models routed via cllama (deepseek-v4-pro, GPT-5.4, Claude thinking, etc.) may legitimately decide to think and stay silent — particularly in `mention_only` Discord channels. Upstream Hermes treats an empty-after-`<think>` final response as a generation failure, retries up to 3 times, and surfaces a Discord error message:

> `Gerrard: ⚠️ Model generated only think blocks with no actual response after 3 retries`

Observed live on tiverton-house with `vercel/deepseek/deepseek-v4-pro` after #210 landed.

## Upstream code path (verified against hermes-base v2026.3.17-claw.4 in tiverton-house-tiverton-1)

- `/opt/hermes-agent/run_agent.py:5953` — final-response branch.
- `_has_content_after_think_block(content)` (line 884) — strips `<think>...</think>` and returns True iff non-whitespace remains.
- `5953` if False, falls into a 3-retry loop ending at line 6038 with the user-visible `error` payload.

## Scope

### 1. hermes-base patch (`dockerfiles/hermes-base/patch-hermes-runtime.py`)

Add a `replace_once` patch that wraps the empty-after-think branch with an `HERMES_ALLOW_SILENT_FINAL` env check. Pseudo-shape:

```python
text = replace_once(
    text,
    "                    if not self._has_content_after_think_block(final_response):\n",
    "                    if not self._has_content_after_think_block(final_response):\n"
    "                        if os.getenv(\"HERMES_ALLOW_SILENT_FINAL\") == \"1\":\n"
    "                            self._cleanup_task_resources(effective_task_id)\n"
    "                            self._persist_session(messages, conversation_history)\n"
    "                            return {\n"
    "                                \"final_response\": None,\n"
    "                                \"messages\": messages,\n"
    "                                \"api_calls\": api_call_count,\n"
    "                                \"completed\": True,\n"
    "                                \"partial\": False,\n"
    "                            }\n",
    "run_agent silent-final opt-out",
)
```

Exact indent and shape will need verification against the actual upstream block — codex should confirm during implementation. The point is: when env is set, return a clean "completed silently" result that downstream code already handles (the Discord adapter already has a "no final response → nothing to send" path; we don't need to add one).

`replace_once` already raises `SystemExit` on miss, so an upstream Hermes bump that moves this block fails the docker build loud.

### 2. Hermes driver wiring (`internal/driver/hermes/config.go`, `internal/driver/types.go`)

Extend the existing `HermesConfig` block (already used for `AllowTools`):

```go
// internal/driver/types.go
type HermesConfig struct {
    AllowTools  []string
    AllowSilent bool   // NEW
}
```

Pod parser additions in `internal/pod/parser_hermes.go` (mirrors the existing `allow-tools` parsing). Pod YAML:

```yaml
x-claw:
  agent: gerrard
  hermes:
    allow-silent: true
```

Driver wiring in `config.go`: if `rc.Hermes != nil && rc.Hermes.AllowSilent`, set `HERMES_ALLOW_SILENT_FINAL=1` in the rendered env. Add the env name to `allowedEnvPassthroughKeys()` so it reaches the agent runtime via the `.env` file.

### 3. hermes-base image bump

New tag: `v2026.3.17-claw.5`. Build + multi-arch push:

```bash
docker buildx build \
  --platform linux/amd64,linux/arm64 \
  -t ghcr.io/mostlydev/hermes-base:v2026.3.17-claw.5 \
  --push dockerfiles/hermes-base/
```

Bump `DefaultHermesBaseTag` in `internal/infraimages/release_manifest.go`.

### 4. Tests

- **Unit (driver)**: `TestMaterializeWritesAllowSilentEnv` — `HermesConfig{AllowSilent: true}` produces `HERMES_ALLOW_SILENT_FINAL=1` in compose env. Mirrors `TestMaterializeHonorsHermesAllowToolsOptIn`.
- **Unit (parser)**: `TestParseHermesAllowSilent` — pod YAML `hermes.allow-silent: true` parses into `HermesConfig.AllowSilent`. Mirrors `TestParseHermesAllowTools`.
- **Spike contract**: extend `TestSpikeHermesBaseImageContract` to `grep -F` the env check inside the patched image's `run_agent.py`. This ensures the patch actually applied — fail loud if upstream drift breaks the string match.

### 5. Docs

- `site/changelog.md` v0.14.4 entry (single bullet).
- `site/guide/pod-yaml.md` and `site/guide/clawfile.md` if they list `x-claw.hermes` fields. Otherwise minimal addition only where the structure is documented.
- CLAUDE.md gotcha: optional one-liner mentioning the new env passthrough key. Codex's call.

## Out of scope

- **Default-on behavior.** This is opt-in. Existing pods see no change.
- **Cllama-layer silence detection.** Violates the "cllama is pure passthrough" gotcha. Hermes-side patch is correct.
- **Other runners' empty-response handling.** OpenClaw, nanoclaw, etc. have their own runtime models; out of scope here.
- **Per-channel silence policy.** All-or-nothing per service. Operators wanting nuance can use multiple service identities.

## Upstream PR

Open in parallel: PR to hermes-agent that adds `HERMES_ALLOW_SILENT_FINAL` upstream (same env name). When merged + released, we delete the `replace_once` patch. Until then, we carry it.

## Risks

- **Indent/whitespace mismatch.** `replace_once` is exact. Codex must `cat -A` the upstream snippet to confirm trailing whitespace before writing the patch. We've been bitten by this before (slash-command sync patch noted "16 spaces of trailing whitespace").
- **Wrong return shape.** The empty-final branch lower in the file (lines 6021-6041) shows the canonical "completed=False, partial=True, error=..." shape used today. We want `completed=True, partial=False, final_response=None`. Verify Hermes Discord adapter doesn't barf on `final_response: None`. Quick read of `cleanup`/`persist` paths should confirm.
- **Persistence side effects.** `_persist_session` is called in the existing failure path. We should call it on the success-silent path too so session history reflects the silent turn. Codex confirms.

## Test matrix

| Layer | Test | Expectation |
|-------|------|-------------|
| Unit | `TestParseHermesAllowSilent` (new) | parser populates `HermesConfig.AllowSilent` |
| Unit | `TestMaterializeWritesAllowSilentEnv` (new) | driver emits `HERMES_ALLOW_SILENT_FINAL=1` |
| Unit | existing hermes parser/driver tests | unchanged, still green |
| Vet | `go vet ./...` | green |
| Spike | `TestSpikeHermesBaseImageContract` (extended) | image contains the env check string |
| Manual | tiverton-house: gerrard with `allow-silent: true` and a reasoning model in `mention_only` channel | non-mentioned messages produce no Discord error |

## Release coordination

This is a hermes-base + clawdapus release.

1. Build + push `ghcr.io/mostlydev/hermes-base:v2026.3.17-claw.5` from `dockerfiles/hermes-base/`. Verify with `TestSpikeHermesBaseImageContract`.
2. Bump `DefaultHermesBaseTag` in `internal/infraimages/release_manifest.go`.
3. Cut clawdapus patch release (e.g. `v0.14.4`). Lockstep images (claw-api/clawdash/claw-wall/claw-mcp-stdio) can be retagged from v0.14.3 via `docker buildx imagetools create` since no infra code changed — same pattern as v0.14.3.
4. Apply on tiverton-house: add `hermes: { allow-silent: true }` to the relevant services (tiverton, gerrard, dundas, weston, allen — wherever the operator wants silence semantics), `claw up -d`.

## Open questions for codex

1. **`HERMES_ALLOW_SILENT_FINAL` semantics**: only `=1`, or accept truthy strings (`true`, `yes`)? Proposal: only `=1` — simpler, matches `os.getenv(...) == "1"` pattern already used in our patches.
2. **Per-pod default vs per-service only**: should pod-level `x-claw.hermes-defaults: { allow-silent: true }` work? Proposal: yes, follows the existing defaults+override pattern, but we don't need the defaults wiring to land in this issue if the parser can be extended later. v1 = service-level only is fine.
3. **Logging**: should the patch emit a `[silent-final]` debug log line when it kicks in? Proposal: yes, single `logger.debug` so operators can confirm via `docker logs`.
4. **Spike-test method**: for `TestSpikeHermesBaseImageContract`, do we copy the patched file out via `docker cp` and grep, or `docker run --rm <image> python -c 'import inspect; ...'`? Proposal: `docker cp` is simpler and matches existing spike test patterns.
5. **Upstream PR timing**: open before or after merging our patch? Proposal: after — we know our patch works first, then upstream PR with prose explaining the use case (multi-agent rooms, mention-only policies, reasoning models).

## Workflow

- Plan drafted by Claude (this doc).
- Codex: design challenge in a talking-stick note. If consensus, proceed to implementation.
- Codex implements: `patch-hermes-runtime.py`, `internal/driver/hermes/config.go`, `internal/driver/types.go`, `internal/pod/parser_hermes.go`, tests, docker build. Commits on a branch, opens PR.
- Claude: tests, merges PR, builds + pushes hermes-base image, bumps `DefaultHermesBaseTag`, cuts clawdapus release, applies on tiverton-house.

## Acceptance

- `go test ./internal/driver/hermes/...` and `go test ./internal/pod/...` green with new tests.
- `go vet ./...` green.
- Spike `TestSpikeHermesBaseImageContract` green against the new image.
- hermes-base v2026.3.17-claw.5 published; `DefaultHermesBaseTag` bumped; clawdapus release cut.
- Tiverton manual test: silent agents in mention_only channel produce no Discord error.
- Issue #212 closes via PR body keyword.
