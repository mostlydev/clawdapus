# Pod-Level Model Slots and Hot-Swappable Model Overrides

**Date:** 2026-04-06
**Status:** Draft
**Issue:** #118
**Depends on:** ADR-017, ADR-019
**Scope:** pod parser, `claw up` compilation, cllama model policy, claw-api write plane

## Problem

Issue #118 is really two different feature requests that are currently tangled together:

1. **Compile-time pod overrides.** Operators need to change model slots in `claw-pod.yml` without rebuilding a shared image.
2. **True runtime retargeting.** The existing `claw-api` write plane exposes `POST /fleet/model/restrict`, but cllama does not consume that state at runtime.

The first problem is blocking real operator workflows today. The second is a separate live-governance feature and should not be coupled to the parser/compiler change.

## Current Behavior

Today model selection is effectively image-bound:

```text
Clawfile MODEL directives
  -> image labels claw.model.<slot>
    -> inspect.ParseLabels -> ClawInfo.Models
      -> compose_up.go copies info.Models into ResolvedClaw.Models
        -> drivers + cllama compile runner config / model_policy
```

Concrete code evidence in the current tree:

- `internal/inspect/inspect.go` parses `claw.model.*` labels into `ClawInfo.Models`
- `internal/pod/types.go` has no `ClawBlock.Models`
- `internal/pod/parser.go` has no `x-claw.models` parsing path
- `cmd/claw/compose_up.go` builds `ResolvedClaw.Models` from `info.Models` only
- `internal/cllama/modelpolicy.go` already compiles whatever lands in `ResolvedClaw.Models`
- `cmd/claw-api/handler.go` can write model-governance state, but cllama does not read it

That means switching a trader from one model to another still requires editing the shared base `Clawfile`, rebuilding, and redeploying the image.

## Plan Shape

This should land in two phases:

1. **Phase 1: pod-level model slots at compile time**
   This is the shippable operator-facing fix. `claw up` should compile image labels plus pod YAML into one resolved model map.

2. **Phase 2: real runtime model override state**
   This is a cllama/governance feature. It should follow only after the compile-time path is in place and clearly tested.

The key decision is to keep Phase 1 narrow and non-ambiguous. Pod YAML changes should affect only the next `claw up`, not live traffic inside an already-running proxy.

## Phase 1: Compile-Time Pod Model Slots

### User Contract

Add service-level `x-claw.models` and pod-level `x-claw.models-defaults`.

Example:

```yaml
x-claw:
  pod: trading-desk
  models-defaults:
    primary: openrouter/anthropic/claude-sonnet-4
    fallback: anthropic/claude-haiku-4-5

services:
  weston:
    image: trader:latest
    x-claw:
      agent: ./AGENTS.md
      models:
        primary: openrouter/google/gemini-2.5-flash

  sentinel:
    image: trader:latest
    x-claw:
      agent: ./AGENTS.md
      models: {}
```

Expected meaning:

- `weston` inherits `fallback` from `models-defaults` but overrides `primary`
- `sentinel` explicitly declines pod-level model defaults and falls back to image labels only

### Precedence

Phase 1 precedence should be:

```text
service x-claw.models
  > pod x-claw.models-defaults
  > image claw.model.* labels
```

Important constraint: this phase is **override/add only**. It does not add a subtraction language for removing an image-declared slot. Image-only slots that are not overridden remain present.

### Merge Semantics

`models` is a map field, so it should follow the same family of semantics as `cllama-defaults.env`, not list spread semantics:

- omitted `models:` key on a service -> inherit `models-defaults`
- `models: {}` or `models: null` -> suppress pod defaults for that service
- service entries override same-name keys from `models-defaults`
- there is no `...` token for models

This is the critical correction to the current draft: using `applyRawObjectDefault()` would not support the documented "inherit fallback, override primary" behavior. Models need additive key merge, not copy-only object default behavior.

### Implementation

#### Step 1: Add Parsed Model Fields

**Files**

- `internal/pod/parser.go`
- `internal/pod/types.go`

Add `Models map[string]string` to:

- `rawClawBlock`
- `ClawBlock`

Then copy parsed values into `service.Claw` during pod parsing.

#### Step 2: Add `models-defaults` to Raw Pod Defaults Expansion

**File**

- `internal/pod/parser.go`

Extend `podDefaults` with:

```go
ModelsDefaults map[string]interface{}
```

Read it from `rawXClaw["models-defaults"]` in `expandPodDefaults()`.

Do **not** use `applyRawObjectDefault(raw, "models", defaults.ModelsDefaults)`.

Instead add a dedicated helper, analogous to `applyRawCllamaDefaults()`:

- if the service omits `models`, deep-copy `models-defaults`
- if the service declares `models: {}` or `models: null`, preserve that explicit suppression
- if the service declares entries, merge default keys first and service keys second

That keeps pod defaults and service overrides consistent with the examples and with the repo's "pod-level defaults, service-level overrides" rule.

#### Step 3: Merge Pod Models Over Image Models During `claw up`

**File**

- `cmd/claw/compose_up.go`

Where `ResolvedClaw` is constructed, replace the direct image assignment:

```go
Models: info.Models,
```

with a merge helper:

```go
Models: mergeModelSlots(info.Models, svc.Claw.Models),
```

Helper behavior:

- clone the image map so callers do not share mutable state
- overlay pod-declared slots by key
- return image-only slots unchanged when pod has no override

This is the point where compile-time precedence becomes real. Drivers and cllama should continue consuming `ResolvedClaw.Models` exactly as they do today.

#### Step 4: Keep Downstream Consumers Unchanged

No driver-specific changes should be required for Phase 1.

Existing downstream paths already consume `ResolvedClaw.Models`:

- `internal/driver/shared/PrimaryModelRef`
- `internal/driver/shared/CollectProviders`
- runner config generation in each driver
- `internal/cllama/InjectCompiledModelPolicy`

The entire point of this phase is to fix the data entering `ResolvedClaw`, not to add another parallel model path later in the pipeline.

#### Step 5: Tests

**Files**

- `internal/pod/parser_defaults_test.go`
- `internal/pod/parser_test.go`
- `cmd/claw/compose_up_test.go`

Add coverage for:

1. service-level `x-claw.models` parsing
2. `models-defaults` inheritance when the service omits `models`
3. additive merge when the service overrides one slot and inherits another
4. `models: {}` and `models: null` suppressing pod defaults
5. `mergeModelSlots(image, pod)` preserving image-only slots and overlaying pod keys

No new cllama model-policy tests are needed for the merge itself. That package already validates ordering and de-duplication of the model map it is given.

### Acceptance Criteria

Phase 1 is done when all of the following are true:

1. A service can declare `x-claw.models.primary` in `claw-pod.yml` and see that value flow into `ResolvedClaw.Models`.
2. Pod `models-defaults` can provide a fallback slot inherited by services that do not override it.
3. `models: {}` or `models: null` suppress pod defaults without affecting image labels.
4. `claw up` compiles the merged model set into runner configs and cllama `model_policy` without any driver-specific special cases.
5. Existing pods that declare no pod-level models continue to behave exactly as they do today.

### Documentation

Update:

- `AGENTS.md` for the current parser/runtime model
- `site/guide/clawfile.md` or the pod guide for `x-claw.models`
- at least one example pod showing `models-defaults` plus a service override

### Out of Scope for Phase 1

- live model changes inside an already-running cllama proxy
- any change to `claw inspect`
- deletion/tombstone semantics for image-declared model slots

`claw inspect` should stay image-scoped in this phase. It reports labels from an image, not the compiled state of a specific pod deployment. If resolved inspection is needed later, it should be a pod-aware command, not a semantic overload of `claw inspect <image>`.

## Phase 2: Runtime Model Override State

Phase 2 should be tracked as a follow-up under issue #118 or a child issue. It is a different feature from pod-level compilation and crosses the cllama submodule boundary.

### Goal

Allow governance to retarget or restrict models for running services without requiring `claw up`.

### Why This Is Separate

The current `POST /fleet/model/restrict` path is not wrong because of its file write. It is incomplete because the proxy does not consume the file. That means the missing work is in runtime state semantics and enforcement, not in the pod parser.

### Required Design Work

Before implementation, decide:

1. Is `model-restrict` truly a restriction-only API, or should runtime retargeting be a separate endpoint and file format?
2. Is runtime targeting scoped by base service, `claw_id`, or compose service name?
3. Does runtime state name a slot (`primary`, `fallback`) or a direct provider/model ref?
4. How does runtime state compose with the compile-time model policy from Phase 1?

My recommendation is to avoid overloading "restrict" and instead define one explicit runtime policy file with versioned schema and clear precedence over compile-time slots.

### Likely Implementation Shape

At a high level, Phase 2 will need:

1. a versioned governance file written by `claw-api`
2. that file mounted into or already visible from the cllama runtime
3. request-time cllama enforcement that combines compile-time allowed models with runtime override/restriction state
4. integration or spike coverage proving a live model change is actually honored without rebuilding or redeploying the image

This phase will require coordinated changes in:

- `cmd/claw-api/`
- `cmd/claw/compose_up.go` if new mounts or metadata are needed
- `cllama/` runtime policy loading and enforcement

## Recommended Landing Order

1. Land Phase 1 by itself with parser/compiler/tests/docs.
2. Validate that pod-level model overrides solve the shared-image operator problem.
3. Then design and land Phase 2 as a separate runtime-governance change.

That gives us a clean, testable improvement now without pretending the current write-plane is already a live override mechanism.
