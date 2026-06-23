# Next Slice: Hermes Image Refresh + Governance Enforcement (2026-06-23)

**Coordination:** Talking Stick room `79e89703-8009-4c52-8663-1ae577a3dfcb`.
**Status:** Hermes image refresh and #257/#259 quiet-channel slice implemented; broader policy-plane work remains future work.

## Current Ground Truth

- Work is on branch `issue-257-259-hermes-status-noise`.
- Latest Clawdapus release before this slice is `v0.23.1`; it pins:
  - `hermes-base:v2026.5.16-claw.3`
  - `cllama:v0.7.3`
  - first-party infra images at `v0.23.1`
- The three newly filed production reliability issues are closed:
  - #317 Hermes `MEMORY.md` cap/eviction
  - #318 Hermes memory file mode
  - #319 cllama upstream failure handling
- Open Hermes work remains in #257/#259: runtime, retry, scheduler, auxiliary, and background-review status must not post into content channels by default.
- Upstream Hermes moved beyond the previous base:
  - previous Clawdapus upstream pin: `v2026.5.16`
  - latest upstream Hermes tag observed: `v2026.6.19`
- The patch ledger has been rebased onto `v2026.6.19`:
  - Discord moved from `gateway/platforms/discord.py` to `plugins/platforms/discord/adapter.py`
  - OpenAI client construction moved to `agent/agent_runtime_helpers.py`
  - memory initialization moved to `agent/agent_init.py`
  - silent-final handling moved to `agent/conversation_loop.py`
  - tool-only kwargs handling moved to `agent/chat_completion_helpers.py`
- `hermes-base:v2026.6.19-claw.2` has been published as a multi-arch image.
- Project-board hygiene is now current: the previously missing open issues (#258, #259, #260, #261,
  #312, #313, #314, #315) were added to the Clawdapus project and set to `Backlog`.

## Recommendation

Take the Hermes image opportunity, but treat it as a guarded Hermes train, not a blind pin bump.

The right next release shape is:

1. **Hermes train (#257/#259):**
   - Rebase `dockerfiles/hermes-base/patch-hermes-runtime.py` against upstream `v2026.6.19`.
   - Preserve existing Clawdapus patches: identity, voice intent trimming, tool-only final delivery, disabled tool filtering, memory cap/eviction/file mode, cron transient-failure suppression, silent-final behavior, cllama consumer session epoch.
   - Add the #257/#259 default policy: runtime/retry/fallback/auxiliary/background-review status goes to logs/telemetry by default, not content channels; keep an opt-in debug path.
   - Done in this slice with `HERMES_CHAT_STATUS_DELIVERY=off` by default and `display.memory_notifications: off`.
2. **Policy plane design (#306):**
   - Write ADR-025 and the `docs/CLLAMA_SPEC.md` contract update before implementing generalized policy hooks.
   - Keep #306 as the convergence gate for #307/#308.
3. **Budget enforcement (#310):**
   - Implement core hard caps in cllama independently of the generalized policy plane if #306 converges.
   - Prove with a full spike: exceed cap -> 429/intervention -> `fleet.budget.set` raises cap -> traffic resumes.
4. **Fleet governance demo (#309):**
   - Conditional on #310 landing cleanly. It should demonstrate a real closed loop, not only a doc walkthrough.
5. **Docs/site freshness:**
   - Update `docs/PROJECT_STATE.md`, `docs/CLLAMA_SPEC.md`, Hermes driver docs, the public site guide pages, and changelog `Unreleased`.
   - Re-promote claims that were softened for #304 only where #310 makes enforcement real.
6. **Board hygiene:**
   - Done for missing open issues.
   - Move only the chosen slice to `In Progress`; do not reshuffle backlog priority without maintainer signoff.

## Hermes Release Gates

The Hermes pin must not move until the image exists.

Required order:

1. Rebase/patch `hermes-base`. Done.
2. Run the local canary build against `v2026.6.19`. Done.
3. Run `go test -tags spike -run TestSpikeHermesBaseImageContract ./cmd/claw`. Done.
4. Build and push a multi-arch image, for example:

   ```sh
   docker buildx build \
     --platform linux/amd64,linux/arm64 \
     -t ghcr.io/mostlydev/hermes-base:v2026.6.19-claw.1 \
     --push dockerfiles/hermes-base/
   ```

5. Verify the pushed manifest includes `linux/amd64` and `linux/arm64`. Done for `v2026.6.19-claw.2`.
6. Only then bump:
   - `internal/driver/hermes/baseimage.go`
   - `internal/infraimages/release_manifest.go`
7. Run release verification. Done with `go run ./scripts/check-release-infra-tags --release-tag v0.23.1`.
8. Cut the Clawdapus release through the normal release workflow after review.

## Open Decisions

- **Priority:** Hermes train ships before #306/#310; #306 ADR remains a separate convergence gate.
- **Upstream bump fallback:** Not needed; `v2026.6.19` rebase succeeded.
- **Default status policy:** Managed Hermes chat handles suppress runtime status by default; operators opt in with `HERMES_CHAT_STATUS_DELIVERY=on`.
- **Live spike:** If credentials are available, run a Discord/Hermes smoke after the image spike to prove provider-outage/status messages stay out of the content channel while real assistant replies still deliver.
