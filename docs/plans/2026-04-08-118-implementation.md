# Implementation Plan: Issue #118 — Pod-Level Model Slots

**Date:** 2026-04-08
**Status:** Ready to execute
**Issue:** #118
**Design doc:** `docs/plans/2026-04-06-pod-level-model-slots.md` (authoritative for *what*; this plan is *how*)
**Scope:** Phase 1 only — compile-time pod model slots merged into `ResolvedClaw.Models` at `claw up` time

## Verification (drift from design doc)

| Design claim | Reality |
|---|---|
| `compose_up.go` ~305 sets `Models: info.Models` | Actual line is `cmd/claw/compose_up.go:302`. Single `ResolvedClaw{}` literal in the file — only one construction site. |
| `inspect.go` ~70-72 parses `claw.model.*` | Confirmed; `info.Models` is `map[string]string` initialized non-nil at `internal/inspect/inspect.go:39`. |
| `ClawBlock` / `rawClawBlock` shapes | Confirmed at `internal/pod/types.go:42-58` and `internal/pod/parser.go:60-75`. |
| Follow `memory-defaults` precedent | The *registration shape* matches `MemoryDefaults` (struct field, `deepCopyMapOrNil`, `applyRawPodDefaults` dispatch), but **merge semantics must follow `applyRawCllamaDefaults`'s `cllama-env` branch** at `parser.go:732-741`, which uses `mergeStringMap` (`parser.go:896`). `applyRawObjectDefault` would be wrong — that's full-replace, not additive per-key. |
| `cmd/claw/inspect.go` rendering | Already iterates `info.Models` at `inspect.go:36-39`. Design says leave it image-scoped. **No change.** |
| `cloneStringMap` already exists | Yes, at `cmd/claw/compose_manifest.go:130` in `package main`. Reuse it inside the new merge helper. |

No example fixtures or testdata files need pre-updating — no current fixture references `models:` at the pod level.

## Ordered task breakdown

1. [ ] Add `Models map[string]string` to `ClawBlock` and `rawClawBlock`
2. [ ] Wire `svc.XClaw.Models` into `service.Claw` literal in `Parse`
3. [ ] Add `ModelsDefaults` to `podDefaults`, populate in `expandPodDefaults`, dispatch from `applyRawPodDefaults`, implement `applyRawModelsDefaults` (additive merge, omitted inherits, `{}` or `null` suppress pod defaults only)
4. [ ] Add `mergeModelSlots` helper in `compose_up.go` and use at the `ResolvedClaw` literal (line 302)
5. [ ] Parser tests
6. [ ] `mergeModelSlots` unit tests
7. [ ] Docs (`AGENTS.md`, `site/guide/pod-yaml.md`, `examples/quickstart/claw-pod.yml`)

## Per-step details

### Step 1 — type fields
- `internal/pod/types.go:42-58`: add `Models map[string]string` to `ClawBlock` (group near `Cllama`/`CllamaEnv`).
- `internal/pod/parser.go:60-75`: add `Models map[string]string \`yaml:"models"\`` to `rawClawBlock`.

### Step 2 — parser wiring
- `internal/pod/parser.go:257-272`: in the `service.Claw = &ClawBlock{...}` literal, add `Models: svc.XClaw.Models,`. Defaults expansion runs before unmarshal, so the map is already merged by this point.

### Step 3 — pod defaults
- `internal/pod/parser.go:682-689`: add `ModelsDefaults map[string]interface{}` to `podDefaults`.
- `internal/pod/parser.go:640-647` (`expandPodDefaults`): `ModelsDefaults: deepCopyMapOrNil(rawXClaw["models-defaults"])`.
- `internal/pod/parser.go:691-711` (`applyRawPodDefaults`): call new `applyRawModelsDefaults(raw, defaults.ModelsDefaults)`.
- Preflight checks already verified:
  - `mergeStringMap` is `func mergeStringMap(base, override map[string]interface{}) map[string]interface{}` at `internal/pod/parser.go:896`, so it is type-compatible with raw defaults merging.
  - `mapStringAny(nil)` returns `(nil, nil)` in `internal/pod/compose_preserve.go:40`.
  - unmarshalling `models: null` into `map[string]string` yields a nil map without error under `gopkg.in/yaml.v3`.
- New helper near `applyRawCllamaDefaults` (~`parser.go:723-741`):

```go
func applyRawModelsDefaults(raw map[string]interface{}, defaults map[string]interface{}) error {
    serviceVal, present := raw["models"]
    if !present {
        if len(defaults) > 0 { raw["models"] = deepCopyMap(defaults) }
        return nil
    }
    serviceMap, err := mapStringAny(serviceVal)
    if err != nil { return fmt.Errorf("models: %w", err) }
    if serviceMap == nil { // explicit null == suppress pod defaults
        return nil
    }
    if len(serviceMap) == 0 { // {} == suppress pod defaults
        raw["models"] = map[string]interface{}{}
        return nil
    }
    if len(defaults) == 0 { return nil }
    raw["models"] = mergeStringMap(defaults, serviceMap) // additive, service wins per key
    return nil
}
```

Type validation (string values) happens automatically when YAML re-unmarshals into `rawClawBlock.Models map[string]string` at `parser.go:124`.

Known limitation: if `models-defaults` contains a non-string value, the eventual typed unmarshal error will likely be reported against an inheriting service rather than the pod defaults block. Do not expand Phase 1 to add bespoke defaults-side validation unless this proves confusing in practice.

### Step 4 — compose_up merge
- `cmd/claw/compose_up.go:302`: replace `Models: info.Models,` with `Models: mergeModelSlots(info.Models, svc.Claw.Models),`.
- New helper (place in `compose_up.go` near other small helpers, same `package main` so `cloneStringMap` is directly callable):

```go
// mergeModelSlots overlays pod-declared model slots onto image-declared slots.
// Image-only slots are preserved; pod entries replace image entries by key.
// Empty or nil pod maps suppress pod defaults only; image labels still apply.
// Always returns a fresh map.
func mergeModelSlots(image, pod map[string]string) map[string]string {
    out := cloneStringMap(image)
    if out == nil { out = make(map[string]string, len(pod)) }
    for k, v := range pod { out[k] = v }
    return out
}
```

### Step 5 — parser tests

**`internal/pod/parser_test.go`** — add `testPodWithModelsYAML` and:
- `TestParsePodServiceLevelModels` — `weston.Claw.Models["primary"]` and `["fallback"]` populated.
- `TestParsePodNoModelsBlock` — service without `models:` produces nil/empty `Claw.Models`, no error.

**`internal/pod/parser_defaults_test.go`** — add `testPodWithModelDefaultsYAML` covering all merge cases in one fixture (services: `inheritor`, `override`, `suppressor`, `null_suppressor`, `adder`):
- `TestParseModelsDefaultsInherited` — inheritor gets both `primary` + `fallback`.
- `TestParseModelsDefaultsOverrideMergeIsAdditive` — **the regression-guard test** — override has overridden `primary` AND inherited `fallback`. Fails if anyone swaps to `applyRawObjectDefault`.
- `TestParseModelsDefaultsEmptyMapSuppresses` — `models: {}` clears defaults.
- `TestParseModelsDefaultsNullSuppresses` — `models: null` also clears defaults.
- `TestParseModelsDefaultsAdditiveNewSlot` — adder gets `primary`, `fallback`, and `tertiary`.
- `TestParseModelsNoDefaultsNoServiceModels` — no defaults + no service models → nil, no error.

### Step 6 — `mergeModelSlots` unit tests

**`cmd/claw/compose_up_test.go`** — `TestMergeModelSlots` table-driven:
- pod overrides image per key
- image-only key preserved
- nil pod returns clone of image
- nil image, non-nil pod
- both nil → empty/nil result
- mutating return value does NOT affect input image map (immutability check)
- parser-to-merge semantic check: `models: {}` and `models: null` suppress pod defaults, but `mergeModelSlots(image, parsedModels)` still yields the image-declared slots

**No changes** to `internal/cllama/modelpolicy_test.go` — downstream unchanged.

### Step 7 — docs
- `AGENTS.md` — short subsection: `x-claw.models`, `models-defaults`, precedence (service > pod > image), additive merge, and the crucial limitation that `{}` / `null` suppress pod defaults only, not image labels.
- `site/guide/pod-yaml.md` — document the same precedence and suppression semantics explicitly.
- `examples/quickstart/claw-pod.yml` — add a minimal `models-defaults` block plus one service override as the worked example. Keep `examples/trading-desk/` focused on the real desk topology.

## Risks / gotchas surfaced

1. **Behavior delta on zero-models case**: today `rc.Models` is the inspect empty (non-nil) map; after the change, with empty inputs `cloneStringMap` returns nil. Verified all downstream consumers (`compose_manifest.go:55`, `compose_up.go:552,589`, drivers' `rc.Models[slot]` reads) tolerate nil. Note in commit message.
2. **Single `ResolvedClaw` literal**: only `compose_up.go:295`. `count > 1` reuses the same `rc`, so all ordinals see the merged map automatically.
3. **No mutation of `ResolvedClaw.Models` downstream** — verified via grep. Safe to hand callers a fresh map.
4. **`models: null` vs `models: {}`** — both are explicit suppression of pod defaults. Neither removes image-declared slots.
5. **`mapStringAny` nil handling** — already handles `(nil, nil)` for yaml-null per `applyRawCllamaDefaults` precedent.
6. **`cloneStringMap` is `package main` in `cmd/claw/`** — same package as `compose_up.go`, no import gymnastics.
7. **`cmd/claw/inspect.go` stays untouched** — it correctly shows image-scoped models, which matches the design's intent.

## Out of scope (Phase 2, NOT in this work)

- Runtime `model-restrict.json` consumption inside cllama
- Live retargeting of running services
- Any change to `cllama/` policy loading
- Any new `claw-api` endpoint shape
- Subtraction/tombstone semantics for removing image-declared slots

## Critical files

- `internal/pod/types.go`
- `internal/pod/parser.go`
- `internal/pod/parser_test.go`
- `internal/pod/parser_defaults_test.go`
- `cmd/claw/compose_up.go`
- `cmd/claw/compose_up_test.go`
- `AGENTS.md`
- `site/guide/pod-yaml.md`
- `examples/quickstart/claw-pod.yml`
