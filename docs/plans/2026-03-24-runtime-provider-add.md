# Runtime Provider Add

**Date:** 2026-03-24
**Status:** Approved
**Scope:** cllama submodule + clawdapus main repo

## Problem

The cllama UI can manage keys within existing providers, but has no way to add a
completely new provider (e.g. `mistral` at `https://api.mistral.ai/v1`, or a local
Ollama instance) at runtime. Providers currently come from two sources only:

1. Well-known env vars (`OPENAI_API_KEY`, etc.) loaded at startup via `LoadFromEnv`
2. `cllama-env` vars compiled into `.claw-auth/providers.json` by `claw up`

Adding a new provider requires either editing `providers.json` by hand or modifying
the pod and running `claw up` again — both require operator access and the latter
interrupts the pod.

## Goals

- Add a provider through the cllama UI with a name, base URL, auth type, api format,
  and first API key.
- Effect is **immediate** — no cllama restart required, no pod interruption.
- Provider **persists** across restarts and `claw up` re-runs.
- `claw up` does not blow away runtime providers when recompiling seed providers.
- Seed providers (from cllama-env / env vars) cannot be silently overwritten by the UI.

## Non-Goals

- Deleting providers via the UI (out of scope; edit providers.json directly).
- Editing a provider's base URL or auth type after creation (out of scope for now).
- Adding providers without at least one API key (always requires a key at creation).

---

## Design

### Data Model

Add a `Source` field to `ProviderState` in `cllama/internal/provider/provider.go`:

```go
type ProviderState struct {
    BaseURL     string     `json:"base_url"`
    Auth        string     `json:"auth,omitempty"`
    APIFormat   string     `json:"api_format,omitempty"`
    ActiveKeyID string     `json:"active_key_id,omitempty"`
    Source      string     `json:"source,omitempty"` // "seed" | "runtime"; omitted for legacy entries
    Keys        []KeyEntry `json:"keys"`
}
```

**Source semantics:**
- `""` (omitted) — loaded from env or legacy file; treated as seed by `mergeProviderSeeds`
- `"seed"` — compiled from pod cllama-env by `claw up`
- `"runtime"` — created via the cllama UI; never touched by `claw up`

`LoadFromEnv` sets `Source: "seed"` on providers it creates.
`loadV2Locked` preserves whatever `source` is on disk.
`AddRuntimeProvider` sets `Source: "runtime"`.

### New Registry Method

```go
// AddRuntimeProvider creates a new provider at runtime with a single ready key.
// Returns an error if the name is already present as a seed provider (source != "runtime").
func (r *Registry) AddRuntimeProvider(name, baseURL, auth, apiFormat, label, secret string) error
```

Implementation:
1. Normalize `name` with `normalizeName`.
2. Lock registry.
3. If provider already exists AND `state.Source != "runtime"`, return error:
   `"provider %q is managed by claw up; use cllama-env to change its keys"`.
4. Build `ProviderState`:
   - `BaseURL`: as provided (caller must validate)
   - `Auth`: as provided, default `"bearer"` if empty
   - `APIFormat`: as provided, default `"openai"` if empty
   - `Source`: `"runtime"`
5. Build `KeyEntry`:
   - `ID`: `"runtime:" + name + ":" + hex(rand 4 bytes)` — unique even if same provider is re-created
   - `Label`: `label` (default `"primary"` if empty)
   - `Secret`: `secret`
   - `Source`: `"runtime"`
   - `State`: `KeyStateReady`
   - `AddedAt`: now UTC RFC3339
6. Set `state.ActiveKeyID = keyEntry.ID`.
7. Write `state` into `r.providers[name]`.
8. Unlock.
9. Call `r.SaveToFile()` and return its error.

### Persistence — `mergeProviderSeeds` (clawdapus)

Current: `mergeProviderSeeds` builds an output map by iterating over `seedKeyDefs`
(providers present in cllama-env). Providers not in `seedKeyDefs` are implicitly
dropped from the output.

Change: after building the seed output, copy any existing provider with
`source: "runtime"` unchanged into the output map. Seed providers that share a
name with a runtime provider overwrite the runtime entry — log a warning when this
happens so the operator knows.

```go
// After building `existing` and processing seed providers:
for name, state := range existing {
    if state.Source == "runtime" {
        if _, conflict := out.Providers[name]; conflict {
            // Seed wins — log and skip.
            log.Printf("warning: cllama-env provider %q overwrites runtime provider", name)
            continue
        }
        out.Providers[name] = state
    }
}
```

This is the only change to `compose_up.go`.

### UI Route — `/providers/add`

**Handler:** `handleProviderAdd(w http.ResponseWriter, r *http.Request)`

Form fields (all `application/x-www-form-urlencoded`):

| Field | Description | Validation |
|---|---|---|
| `name` | Provider slug | Non-empty, `[a-z0-9-]+` |
| `base_url` | Base URL incl. `/v1` | Parses as `http://` or `https://`, non-empty host |
| `auth` | `bearer` / `x-api-key` / `none` | One of the three values |
| `api_format` | `openai` / `anthropic` | One of the two values |
| `key_label` | Label for first key | Default `"primary"` if empty |
| `secret` | API key secret | Non-empty |

Validation errors return HTTP 400 with a plain-text error body (same pattern as
existing key routes). On success: `http.Redirect(w, r, "/", http.StatusSeeOther)`.

The handler calls `r.registry.AddRuntimeProvider(name, baseURL, auth, apiFormat,
keyLabel, secret)`. If that returns an error (e.g. seed conflict), returns 400 with
the error.

### Dashboard Template

Add a "Add Provider" section below the existing providers panel in `dashboard.html`.

Form (POST to `/providers/add`):
```
Name:        [text input, placeholder "mistral"]
Base URL:    [text input, placeholder "https://api.mistral.ai/v1"]
Auth:        [select: bearer | x-api-key | none]
API Format:  [select: openai | anthropic]
Key Label:   [text input, placeholder "primary"]
API Key:     [password input]
             [Add Provider button]
```

Runtime providers display identically to seed providers in the provider table —
same pool badge counts, same per-key rows, same activate/disable/delete key actions.
No visual distinction needed (the `source` field is internal).

---

## File Inventory

### cllama submodule

| File | Change |
|---|---|
| `internal/provider/provider.go` | Add `Source` to `ProviderState`; set `Source: "seed"` in `LoadFromEnv`; add `AddRuntimeProvider` method |
| `internal/ui/handler.go` | Add `handleProviderAdd`; register `/providers/add` route |
| `internal/ui/templates/dashboard.html` | Add "Add Provider" form below providers panel |
| `internal/provider/provider_test.go` | Tests for `AddRuntimeProvider` (success, seed conflict, SaveToFile call) |
| `internal/ui/handler_test.go` | Tests for POST `/providers/add` (valid, bad name, bad URL, seed conflict) |

### clawdapus main repo

| File | Change |
|---|---|
| `cmd/claw/compose_up.go` | `mergeProviderSeeds`: copy runtime providers into output; warn on seed conflict |
| `cmd/claw/compose_up_test.go` | Tests for runtime provider preservation and seed-wins-conflict |

---

## Implementation Tasks

1. **`provider.go`** — Add `Source` to `ProviderState`; set `Source: "seed"` in
   `LoadFromEnv` and `loadV1Locked`; implement `AddRuntimeProvider`.

2. **`provider_test.go`** — `TestAddRuntimeProviderCreatesProvider`,
   `TestAddRuntimeProviderRejectsSeedConflict`,
   `TestAddRuntimeProviderSavesToFile`.

3. **`ui/handler.go`** — Register `/providers/add` route; implement
   `handleProviderAdd` with field validation and registry call.

4. **`ui/handler_test.go`** — `TestHandleProviderAddCreatesProvider`,
   `TestHandleProviderAddRejectsBadURL`, `TestHandleProviderAddRejectsSeedConflict`.

5. **`dashboard.html`** — Add "Add Provider" form.

6. **`compose_up.go`** — Runtime provider preservation in `mergeProviderSeeds`.

7. **`compose_up_test.go`** — `TestMergeProviderSeedsPreservesRuntimeProviders`,
   `TestMergeProviderSeedsWarnOnSeedConflict`.

---

## Acceptance Criteria

- A provider added via the UI is immediately usable by the proxy (no restart).
- Running `claw up` again does not remove the runtime provider from `providers.json`.
- Adding a provider with a name already managed by cllama-env returns an error in
  the UI (not a silent overwrite).
- If cllama-env later adds a provider with the same name as a runtime provider,
  `claw up` logs a warning and the seed entry wins.
- All new code has unit test coverage.
