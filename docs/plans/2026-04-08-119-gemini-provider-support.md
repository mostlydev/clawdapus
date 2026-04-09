# Issue #119: First-Class Google Gemini Provider Support — Implementation Plan

**Date:** 2026-04-08
**Status:** Draft
**Issue:** #119
**Related design note:** `docs/plans/2026-04-06-gemini-provider-support.md`
**Scope:** cllama provider registry, `claw up` provider seeding, pricing coverage, docs, release pinning

## Goal

Let operators declare direct Gemini model refs such as `google/gemini-2.5-flash`
and seed native Google credentials through `x-claw.cllama-env`, without routing
through OpenRouter.

This is an implementation plan for the design in
`2026-04-06-gemini-provider-support.md`. The earlier doc remains the design/why
document; this one is the execution/how document.

## Contract Decisions

- `GEMINI_API_KEY` is the primary env var for native Gemini routing.
- `GOOGLE_API_KEY` is accepted as a lower-priority alias.
- `GOOGLE_BASE_URL` is accepted as an override for the default Google endpoint.
- Direct Gemini uses Google’s OpenAI-compatible endpoint:
  `https://generativelanguage.googleapis.com/v1beta/openai`
- The provider name is `google`, so model refs are `google/<model>`.
- Scope is compile-time/provider-seed support only. No runtime policy or
  `model-restrict` work belongs in this issue.

## Why The Two Drafts Needed Reconciliation

The 2026-04-06 draft had the right feature shape and release concerns, but it
was too high-level to execute directly.

Claude’s 2026-04-08 draft was stronger on test-first sequencing and caught one
real adjacent bug in `cmd/claw/compose_up.go`: `isProviderKey()` still omits
`XAI_API_KEY` even though xAI is already seeded through `seedKeyDefs`. That
means `stripLLMKeys()` can leak xAI keys into agent env. That fix belongs in
the same patch because this issue touches the same provider-key hygiene surface.

Claude’s draft was still missing two things the original design had right:

- the canonical doc target is `AGENTS.md`, not `CLAUDE.md`
- the work is not actually operator-shippable until a Gemini-capable cllama
  image is published and the main repo pin is updated

## Execution Plan

### 1. Preflight

- [ ] Confirm current code still matches the plan surfaces:
  - `cllama/internal/provider/provider.go`
  - `cllama/internal/cost/pricing.go`
  - `cmd/claw/compose_up.go`
  - `internal/driver/shared/model.go`
- [ ] Verify `#119` is in `In Progress` on the project board before code work.
- [ ] Check whether the `cllama/` submodule worktree is already dirty before
  editing so we do not trample unrelated changes.

### 2. cllama: add `google` provider support

**Files**

- `cllama/internal/provider/provider.go`
- `cllama/internal/provider/provider_test.go`

**Implementation**

- [ ] Add `google` to `knownProviders` with the default base URL
  `https://generativelanguage.googleapis.com/v1beta/openai`.
- [ ] Extend `envKeyMap` with:
  - `GEMINI_API_KEY`
  - `GEMINI_API_KEY_1`
  - `GOOGLE_API_KEY`
- [ ] Extend `envBaseURLMap` with `GOOGLE_BASE_URL`.
- [ ] Extend `LoadFromEnv()` seed definitions so `google` keys are loaded in
  this order:
  1. `GEMINI_API_KEY`
  2. `GEMINI_API_KEY_1`
  3. `GOOGLE_API_KEY`
- [ ] Preserve existing default auth/API behavior unless live code inspection
  shows Google needs a provider-specific override. The expected outcome is still
  `auth=bearer` and `api_format=openai`.

**Tests**

- [ ] `GEMINI_API_KEY` seeds `google`
- [ ] `GOOGLE_API_KEY` works as fallback
- [ ] `GEMINI_API_KEY` wins when both are set
- [ ] `GOOGLE_BASE_URL` overrides the default base URL

**Verification**

- [ ] `cd cllama && go test ./internal/provider`

### 3. cllama: add direct Google pricing coverage

**Files**

- `cllama/internal/cost/pricing.go`
- `cllama/internal/cost/pricing_test.go`

**Implementation**

- [ ] Add direct `google` pricing entries for the Gemini models we already
  support via OpenRouter pricing rows.
- [ ] Verify the rates against the current source of truth at implementation
  time instead of blindly copying stale numbers.

**Tests**

- [ ] Add direct `google/gemini-*` lookup coverage in `pricing_test.go`.

**Verification**

- [ ] `cd cllama && go test ./internal/cost`
- [ ] `cd cllama && go test ./...`
- [ ] `cd cllama && go vet ./...`

**Commit boundary**

- [ ] Commit the full cllama feature as one coherent submodule change, not as
  multiple unrelated submodule commits.

### 4. Main repo: compile Google seeds into `providers.json`

**Files**

- `cmd/claw/compose_up.go`
- `cmd/claw/compose_up_test.go`

**Implementation**

- [ ] Extend `seedKeyDefs` with `google` entries:
  - `GEMINI_API_KEY`
  - `GEMINI_API_KEY_1`
  - `GOOGLE_API_KEY`
- [ ] Extend `defaultBaseURLs` with `google`.
- [ ] Extend the local `baseURLEnvMap` in `mergeProviderSeeds()` with
  `GOOGLE_BASE_URL`.
- [ ] Extend `isProviderKey()` for the three Google key vars.
- [ ] Also add the missing `XAI_API_KEY` and `XAI_API_KEY_1` cases to
  `isProviderKey()` so `stripLLMKeys()` stops leaking xAI keys into agent env.

**Tests**

- [ ] Add/extend tests that prove `mergeProviderSeeds()` writes a `google`
  provider into `providers.json`.
- [ ] Extend `TestIsProviderKey` coverage for:
  - `GEMINI_API_KEY`
  - `GEMINI_API_KEY_1`
  - `GOOGLE_API_KEY`
  - `XAI_API_KEY`
  - `XAI_API_KEY_1`
- [ ] Make sure the provider-key stripping tests still pass after the xAI fix.

**Verification**

- [ ] `go test ./cmd/claw/... -run 'TestMergeProviderSeeds|TestIsProviderKey|TestStripLLMKeys'`
- [ ] `go test ./cmd/claw/...`

### 5. Docs and operator guidance

**Files**

- `site/guide/cllama.md`
- `AGENTS.md`
- `site/changelog.md`
- optionally `cllama/README.md` if standalone proxy docs need parity

**Implementation**

- [ ] Update the cllama guide to list native Google support and show a direct
  `google/gemini-*` example using `GEMINI_API_KEY`.
- [ ] Document that `GOOGLE_API_KEY` is a lower-priority alias.
- [ ] Update `AGENTS.md` with the current provider-seeding behavior so future
  agents do not rediscover this by reading code.
- [ ] Add a changelog entry before merge, since this is a user-visible feature
  on `master`.

### 6. Main repo integration and release pinning

This is the missing step from Claude’s draft. Without it, the issue can merge as
source code but still not be operator-usable through the four-verb release path.

**Files / surfaces**

- the `cllama/` submodule pointer in the main repo
- `cmd/claw/image_lifecycle.go`
- cllama image publication workflow / tag process

**Implementation**

- [ ] Update the root repo to the committed Gemini-capable `cllama/` SHA.
- [ ] Publish a new versioned `ghcr.io/mostlydev/cllama:<tag>` image from that
  submodule commit.
- [ ] Bump the pinned cllama tag in `cmd/claw/image_lifecycle.go`.
- [ ] If the issue is considered release-worthy immediately, make sure the next
  release path uses the new pinned ref rather than the previous cllama image.

**Why this is required**

ADR-022 moved operators to pinned infra refs. If the published cllama image tag
does not contain the Gemini support, `claw pull` and released `claw` binaries
will not actually deliver the feature.

### 7. End-to-end verification

- [ ] `go test ./...`
- [ ] `go vet ./...`
- [ ] Verify generated `providers.json` includes a `google` provider when a pod
  supplies `GEMINI_API_KEY` in `x-claw.cllama-env`.
- [ ] If a real Gemini key is available, run one smoke path with a pod that
  declares `models.primary: google/gemini-...` and confirm the request routes
  without OpenRouter.
- [ ] Move `#119` to `In review` only after code, docs, verification, submodule
  pointer update, and published-image pinning are all complete.

## Out Of Scope

- runtime `model-restrict` or any live model retargeting
- changes to `claw inspect`
- OpenRouter behavior changes
- driver-specific model handling refactors
- prompt/policy changes in `cllama/internal/proxy/handler.go`

## Acceptance Criteria

- `google/<model>` refs are valid end-to-end through cllama and `claw up`
- `GEMINI_API_KEY` works as the primary native Gemini credential
- `GOOGLE_API_KEY` works as a lower-priority alias
- `GOOGLE_BASE_URL` can override the default endpoint
- direct `google` pricing lookups work in cllama cost tracking
- `providers.json` generation in `claw up` emits `google` correctly
- provider-key stripping no longer leaks xAI keys
- docs reflect native Gemini support
- a published cllama image and updated main-repo pin make the feature usable by
  released Clawdapus binaries

## Notes

- `internal/driver/shared/model.go` is already generic enough for `google`
  provider refs; no driver refactor should be necessary.
- The `cllama` proxy routing path is already provider-prefix based. This issue
  is primarily about registry seeding, base URL/defaults, and release wiring.
