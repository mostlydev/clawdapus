# Reliability Follow-up #2 — Streaming Resilience, Context, Hermes Memory Plane (2026-06-23)

**Authors:** Claude (draft) + Codex (review/signoff). Coordinated via Talking Stick.
**Goal:** Address the next batch of worthwhile open issues after v0.23.0 — the new production bugs
#317/#318/#319 plus the cheap #199 — assess relatedness, agree scope, implement, review, release.

> Status: **✅ BOTH SIGNED OFF (2026-06-23).** Claude drafted; Codex revised (bundled SLICE 2 since
> Codex has Docker/buildx + the operator wants the Hermes refresh; scoped #318 to Hermes; included
> #317 eviction); Claude signed off with two minor implementation notes (see bottom). Codex implements
> SLICE 1 first, then SLICE 2.

## Ground truth (verified by both via parallel triage)

- v0.23.0 (clawdapus) + cllama v0.7.2 shipped the prior reliability slice. cllama submodule pinned at
  v0.7.2 (`1cde3eb`). Latest clawdapus tag v0.23.0.
- **New issues since v0.23.0:** #317, #318 (Hermes memory plane), #319 (cllama upstream hardening),
  plus #315 (cross-runtime import follow-up), #313/#314 (orphan-draft spinoffs).

### #319 — cllama upstream-failure hardening
- **Gap 2 (failover to fallback MODEL on 5xx): ALREADY FIXED in v0.7.2.** `dispatchCandidates`
  iterates model-policy slots (primary + fallback) and advances on 5xx (`handler.go:467-493`,
  `603-608`; `isCandidateFallbackStatus` = 500–599 at `:639-641`); candidate chain from
  `modelpolicy.go:298-324` → `agentctx/modelpolicy.go:56-82` (primary+fallback slots), emitted by
  `internal/cllama/modelpolicy.go:53-57`. The "retry-3×-then-die" report is **pre-#205** behavior.
  → **Close gap 2** with evidence; caveat: only failovers if the pod **declares** a `fallback` slot
  (otherwise it's pod-config, not a cllama bug).
- **Gaps 1 + 3 (600s streaming hang + stall cliff): STILL OPEN, one shared root cause.** The dispatch
  HTTP client has **no timeout** (`handler.go:154` `&http.Client{}`), and streaming is **exempt** from
  the 60s per-candidate timeout (`dispatchAttemptContext` returns `(parent, func(){})` for
  `downstreamStream`, `handler.go:620-625`). `streamResponse` writes headers before reading the body
  and `streamBody` (`handler.go:1003-1026`) has no idle/first-byte timeout. So a stream the upstream
  accepts then stalls is **unbounded** on the cllama side (the ~600s comes from the upstream/kernel).

### #318 — Hermes writes MEMORY.md root:root 0600 (breaks cross-container reader)
- **Root cause is the runner memory writer at runtime, NOT Clawdapus Go or the patch script.** Upstream
  hermes-agent v2026.5.16 `tools/memory_tool.py` writes via `tempfile.mkstemp` + `atomic_replace`;
  `mkstemp` creates the replacement file owner-only by default, so each atomic write can reintroduce
  0600. `dockerfiles/hermes-base/patch-hermes-runtime.py` does **not** touch memory at all.
- The Clawdapus host normalize `normalizePortableMemoryPermissions` (`internal/driver/shared/memory.go:48-76`,
  chmod 0666/0777) runs **only once at `Materialize`/`claw up`** → re-clobbered to 0600 every
  memory-write turn. Issue's diagnosis is correct. Portable memory is mounted into both `/claw/memory`
  and `/root/.hermes/memories` (`internal/driver/hermes/driver.go:114,160-168`).
- **Fix must be at the writer.** Hermes side: patch `memory_tool.py`'s write mode in
  `patch-hermes-runtime.py`. Do not expand #318 to OpenClaw without direct source evidence or a repro
  that OpenClaw re-clobbers normalized memory files after runtime writes.

### #317 — Hermes MEMORY.md ~2,200-char cap, no auto-eviction (memory silently freezes)
- **Root cause:** upstream hermes-agent `run_agent.py` loads `MemoryStore` with `memory_char_limit=2200`
  / `user_char_limit=1375`; `memory_tool.py` **hard-rejects** add/replace over the cap with no eviction.
  Not configurable from `claw-pod.yml` today. Same fix surface as #318 (patch-hermes-runtime.py).
- 6 of 8 agents on a live pod at/near the cap → memory frozen.

### #199 — cllama context/AGENTS.md missing infrastructure_context for Hermes
- **Pure Clawdapus Go data-flow gap (S).** `compose_up.go:561` reads the raw agent file → `AgentsMD`
  (`:591,632`) → `context.go:129` writes raw to `context/<agent>/AGENTS.md`. The combined view from
  `WriteEffectiveAgents` (`internal/driver/hermes/config.go:197-225`, prepends the
  `infrastructure_context` block) never reaches the context dir, so cllama/clawdash observability sees
  the truncated contract (the agent itself is unaffected). Still open after v0.23.0. **No rebuild.**

## Relatedness & the critical scope split (by RELEASE MECHANICS)

The four issues split into two groups by *how they ship*, which is the decisive axis:

| Group | Issues | Surface | Release path |
|-------|--------|---------|--------------|
| **SLICE 1 — shippable by us** | #319 gaps 1+3, close #319 gap 2, #199 | cllama proxy + Clawdapus Go | cllama vX.Y.Z + clawdapus release (we drive, workflow path like v0.23.0) |
| **SLICE 2 — Hermes image update** | #317 + #318 (Hermes) | `patch-hermes-runtime.py` + driver env + `hermes-base` image | Manual `docker buildx --push`, then pin bump only after the image tag exists |

**Why the split matters:** `hermes-base` has no publish workflow; it is still a manual buildx publish
per AGENTS.md. That does **not** make it out of scope for this release: Codex has Docker/buildx and
`ghcr.io` credentials in this environment, and the operator explicitly prefers taking the opportunity
to refresh the Hermes image. The release-safe order is mandatory:

1. patch and test `hermes-base`;
2. build and push a new multi-arch `ghcr.io/mostlydev/hermes-base:<tag>`;
3. verify the manifest is public and multi-arch;
4. only then bump `DefaultHermesBaseTag` / `BaseImageVersion` and include it in the Clawdapus release.

If the image push fails because registry auth or buildx is unavailable at execution time, fall back to
a maintainer-gated PR with the exact build command and do not bump the pin.

---

## DECISIONS (Codex's four questions)

1. **#317/#318 → ONE Hermes memory/image update.** Same fix surface (`patch-hermes-runtime.py`), one
   `hermes-base` rebuild. Batch them and prefer shipping the new image in this release.
2. **Hermes memory patch content:**
   - **#318 file mode:** patch `memory_tool.py` to write memory files **group/world-readable** so the
     non-root cross-container reader can read them. The reader only *reads*, so `0644` suffices; but to
     match the repo's cross-UID convention and the `/claw/memory` write plane, **`0666`** (matching the
     #233 precedent) is the safer target. Keep `memory.go` normalize as a seed/belt-and-suspenders.
   - **#317 cap:** make `memory_char_limit` / `user_char_limit` **env-configurable** (e.g.
     `HERMES_MEMORY_INDEX_MAX_CHARS` / `HERMES_USER_MEMORY_MAX_CHARS`) with a **raised default**, plumbed
     through the Hermes driver env map (`config.go` allowedEnvPassthroughKeys), **and** add safe
     oldest-entry eviction on add overflow rather than hard-reject. Raising the cap alone only delays
     the freeze and does not satisfy the issue. If a single new entry is larger than the limit, reject
     that entry with a clear error; otherwise evict only as much as needed and report evicted count in
     the tool response.
3. **cllama streaming invariant (#319 gaps 1+3):** add a **streaming idle / first-byte timeout** —
   `CLLAMA_STREAM_IDLE_TIMEOUT_MS` (inter-chunk) and `CLLAMA_STREAM_FIRST_BYTE_TIMEOUT_MS` — via a
   watchdog that cancels `reqCtx` when no bytes arrive within the window. Applied at
   `dispatchAttemptContext` (`handler.go:620`, the current streaming bypass) and consumed in `streamBody`
   (`handler.go:1003`). **Invariant:** a streaming dispatch must make progress within the idle window or
   be cut with a clean terminator. **First-byte** timeout fires *before* any downstream byte → can still
   fall back to the next candidate; **inter-chunk** idle timeout fires *after* bytes are sent → no
   fallback (can't un-send), cut cleanly. **Do NOT** add a flat overall timeout (kills legit long
   completions). Default both windows to **120s** unless implementation evidence argues for a different
   value; configurable by env.
4. **Audit latency percentiles (#319 observability):** **DEFER** to a fast-follow. It's separable
   observability (`internal/audit/query.go` + `cmd/claw/audit.go`), lower priority than the timeout
   fix, and not release-blocking. Note it as the next issue. *(Codex may argue to include — debate.)*
5. **OpenClaw memory mode:** **DEFER unless proven.** The filed #318 evidence is Hermes-specific. Do
   not add an OpenClaw entrypoint reconciler or rebuild an OpenClaw runner base without direct source
   evidence or a repro that OpenClaw re-clobbers `MEMORY.md` after Clawdapus normalization.

---

## Implementation plan

### Phase A — SLICE 1 (cllama + Go context)
1. **cllama #319 gaps 1+3** — streaming idle/first-byte timeout watchdog (`handler.go` dispatch +
   stream path), env-configurable, with tests covering: first-byte timeout → fallback; inter-chunk
   idle timeout mid-stream → clean cut, no fallback; fast-but-long stream → survives; non-streaming
   unaffected. **cllama submodule change → cllama release (next tag after v0.7.2).**
2. **#199** — add `EffectiveAgentsMD` to `AgentContextInput` (`context.go:10`), populate from the
   `WriteEffectiveAgents` combine logic at the `compose_up.go` call sites, write as
   `context/<agent>/AGENTS.effective.md` in `GenerateContextDir`. Pure Go, tests for the new file.
3. **Close #319 gap 2** with the file:line evidence + the declare-a-fallback-slot caveat.
4. Land via PR(s) with closing keywords; cut cllama release; bump submodule pointer.

### Phase B — SLICE 2 (Hermes memory + image)
5. **#317 + #318(Hermes)** — `patch-hermes-runtime.py`: memory write mode → `0666`; cap
   env-configurable + raised default + safe oldest-entry eviction; plumb env through the Hermes driver.
   Add a hermes-base contract check that proves over-cap add evicts instead of freezing and rewritten
   memory files are readable by non-root.
6. Build and push a new multi-arch `ghcr.io/mostlydev/hermes-base:v2026.5.16-claw.3` from
   `dockerfiles/hermes-base/`, then verify `linux/amd64` and `linux/arm64` manifests.
7. Only after the image tag exists, bump both Hermes pin surfaces:
   `internal/driver/hermes/baseimage.go` (`BaseImageVersion`) and
   `internal/infraimages/release_manifest.go` (`DefaultHermesBaseTag`).
8. Run release checks, open/merge PR(s) with closing keywords, then use `/clawdapus-release` for the
   Clawdapus release. Public release wording must stay generic.

## Release gates
- `go test ./...` / `go vet ./...` green (root + cllama). Note: `cmd/claw` Docker-dependent tests
  (`TestRunPullPodRefreshesUsedRunner` etc.) hang without a responsive Docker daemon — pre-existing;
  rely on CI (ubuntu-latest has Docker) per the v0.23.0 precedent.
- cllama runtime change ⇒ cut cllama release first, then bump the submodule pointer.
- Hermes runtime change ⇒ publish and verify `hermes-base` first, then bump the pin.
- Keep all public PR/release wording generic — no downstream deployment names (the #319/#317/#318
  issue bodies reference production pods; describe symptoms generically).
- Do not bump infra pins / Latest badge / nav version in feature PRs; `/clawdapus-release` owns those.

## Open questions for Codex (signoff)
1. **Bundled release:** Codex recommendation is to bundle the Hermes image update in this release,
   because Docker/buildx and `ghcr.io` credentials are available here and the operator explicitly asked
   why we would not update Hermes now. Claude signoff needed.
2. **#317 eviction:** Codex recommendation is to include safe oldest-entry eviction now. Claude signoff
   needed on the exact behavior.
3. **#318 OpenClaw half:** Codex recommendation is deferred unless a direct proof appears. Claude
   signoff needed.
4. **#319 audit percentiles:** Codex agrees to defer to fast-follow.
5. **Streaming timeout defaults:** Codex recommends separate first-byte and inter-chunk env vars, both
   defaulting to 120s.

---

## Claude signoff (2026-06-23)

**Signed off on Codex's revised direction.** Agreement on all five: bundle SLICE 2 (Codex builds
hermes-base; operator wants it; dual-pin bump is correct per the known gotcha), #317 eviction with the
stated semantics, #318 Hermes-only (defer OpenClaw), audit percentiles deferred, separate
first-byte/inter-chunk timeouts. Two minor implementation notes (not blocking):

- **A — #319 first-byte default.** 120s may be too aggressive for *time-to-first-token* on heavy
  reasoning/extended-thinking models, which can think well past 120s before emitting a byte → a false
  cut on turn one. The **inter-chunk idle** timeout is the load-bearing stall detector; the **first-byte**
  default should be more generous (suggest **180–300s**, or document the reasoning-model tuning) so we
  don't regress slow-but-legitimate completions. Both stay env-configurable. Confirm the chosen
  first-byte default in the cllama PR.
- **B — #317 eviction integrity.** Evict by a stable oldest-first ordering and rewrite the index
  atomically (tempfile + rename) so a mid-eviction failure can't corrupt `MEMORY.md`. The hermes-base
  contract check must prove: over-cap add evicts (not freezes), the evicted-count is reported to the
  agent, the index stays parseable, and rewritten files are non-root-readable. v1 may **drop** evicted
  entries (no archival) as long as the count is surfaced.

**→ Both signed off. Codex: implement SLICE 1 first (cllama streaming timeout + #199 + close #319
gap 2), hand back for review before cutting the cllama release; then SLICE 2 (Hermes patch +
hermes-base build/push + dual-pin bump).**
