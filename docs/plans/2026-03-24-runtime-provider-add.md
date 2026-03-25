# Runtime Provider Add

**Date:** 2026-03-24
**Status:** Approved (rev 2 — post-Codex review)
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

- Add a provider through the cllama UI with name, base URL, auth type, api format,
  and first API key.
- Effect is **immediate** — no cllama restart, no pod interruption. The registry is
  the live in-memory state shared by the proxy; mutations take effect on the next
  request.
- Provider **persists** across restarts and `claw up` re-runs.
- `claw up` does not blow away runtime providers. (Note: today `mergeProviderSeeds`
  already writes back the full `existing` map — all providers survive a re-up. The
  bug is that `source` is stripped on round-trip, not that providers are deleted.)
- Seed providers (from cllama-env) cannot be silently overwritten by the UI.

## Non-Goals

- Deleting providers via the UI (edit `providers.json` directly, or add later).
- Editing a provider's base URL or auth type after creation (add a new provider).
- Merging into an existing provider — `AddRuntimeProvider` is a strict create.

---

## Root Cause of Current Breakage

`cmd/claw/compose_up.go` defines a local `v2ProviderState` struct that is used for
both reading (`loadExistingProviders`) and writing (`mergeProviderSeeds`) providers.
It has no `Source` field:

```go
// compose_up.go:2003 — missing Source
type v2ProviderState struct {
    BaseURL     string       `json:"base_url"`
    Auth        string       `json:"auth,omitempty"`
    APIFormat   string       `json:"api_format,omitempty"`
    ActiveKeyID string       `json:"active_key_id,omitempty"`
    Keys        []v2KeyEntry `json:"keys"`
}
```

When `loadExistingProviders` deserialises a provider that has `"source":"runtime"`,
the field is silently dropped. When `mergeProviderSeeds` writes the output (`out.Providers
= existing`), the `source` key is absent. A runtime provider's `source` field therefore
disappears after the first `claw up`, making the runtime/seed distinction impossible to
maintain.

The fix is one line: add `Source string` to `v2ProviderState`.

---

## Design

### 1. `v2ProviderState` — add `Source` field (compose_up.go)

```go
type v2ProviderState struct {
    BaseURL     string       `json:"base_url"`
    Auth        string       `json:"auth,omitempty"`
    APIFormat   string       `json:"api_format,omitempty"`
    ActiveKeyID string       `json:"active_key_id,omitempty"`
    Source      string       `json:"source,omitempty"` // "seed" | "runtime"
    Keys        []v2KeyEntry `json:"keys"`
}
```

No other change to `mergeProviderSeeds` is needed for preservation — the existing
`out.Providers = existing` already carries all providers through. The only additional
behavior is a warning log when a seed provider in the current `claw up` run collides
with an existing provider that has `source: "runtime"`:

```go
// In the bySvc loop, before updating existing[provName]:
if old, exists := existing[provName]; exists && old.Source == "runtime" {
    fmt.Fprintf(stderr, "warning: cllama-env seeds provider %q, overwriting runtime provider\n", provName)
}
```

Seed always wins. The runtime entry is overwritten. This is explicit, logged, and
consistent with the principle that the pod's declared config is authoritative.

**Migration:** Providers hand-edited into `providers.json` before this change have no
`source` field. They survive `claw up` unchanged (the existing pass-through behavior).
They are not treated as `"seed"` for any purpose — the `source` field is only checked
in the warning above and in `AddRuntimeProvider`'s conflict guard. Operators who want
them to be clearly marked can add `"source": "runtime"` manually.

### 2. `ProviderState` — add `Source` field (cllama/provider.go)

```go
type ProviderState struct {
    BaseURL     string     `json:"base_url"`
    Auth        string     `json:"auth,omitempty"`
    APIFormat   string     `json:"api_format,omitempty"`
    ActiveKeyID string     `json:"active_key_id,omitempty"`
    Source      string     `json:"source,omitempty"` // "seed" | "runtime"
    Keys        []KeyEntry `json:"keys"`
}
```

`LoadFromEnv` sets `Source: "seed"` on providers it creates.
`loadV1Locked` sets `Source: "seed"` (v1 files are always from cllama-env/env).
`loadV2Locked` preserves whatever `source` is on disk (including `""`).

### 3. `AddRuntimeProvider` — strict create (cllama/provider.go)

```go
// AddRuntimeProvider creates a new provider at runtime with a single ready key.
// Returns an error if a provider with that name already exists (seed or runtime).
// To add more keys to an existing provider, use AddRuntimeKey.
func (r *Registry) AddRuntimeProvider(name, baseURL, auth, apiFormat, label, secret string) error
```

Implementation:
1. Normalize `name` via `normalizeName`. Return error if empty.
2. Lock registry.
3. If provider already exists (any source), return error:
   `"provider %q already exists; use /keys/add to add keys to it"`.
   This prevents silent replacement of existing providers and their key history.
4. Build `ProviderState`:
   - `BaseURL`: as provided (pre-validated by caller)
   - `Auth`: as provided; default `"bearer"` if empty
   - `APIFormat`: as provided; default `"openai"` if empty
   - `Source`: `"runtime"`
5. Build first `KeyEntry`:
   - `ID`: `"runtime:" + name + ":" + hex(rand 4 bytes)` — collision-resistant
   - `Label`: `label`; default `"primary"` if empty
   - `Secret`: `secret`
   - `Source`: `"runtime"`
   - `State`: `KeyStateReady`
   - `AddedAt`: now UTC RFC3339
6. Set `state.ActiveKeyID = keyEntry.ID`.
7. Write state into `r.providers[name]`.
8. Unlock.
9. Call `r.SaveToFile()` and return its error.

### 4. UI Route `/providers/add` (cllama/ui/handler.go)

Register in `ServeHTTP`:
```go
case r.Method == http.MethodPost && r.URL.Path == "/providers/add":
    h.handleProviderAdd(w, r)
```

Form fields (`application/x-www-form-urlencoded`):

| Field | Description | Validation |
|---|---|---|
| `name` | Provider slug | Non-empty; `normalizeName` result must be non-empty |
| `base_url` | Full base URL incl. `/v1` | `url.Parse` succeeds; scheme is `http` or `https`; host non-empty |
| `auth` | Auth mode | One of `bearer`, `x-api-key`, `none` |
| `api_format` | Wire format | One of `openai`, `anthropic` |
| `key_label` | Label for first key | Default `"primary"` if empty |
| `secret` | API key secret | Non-empty |

Validation errors → HTTP 400, plain-text body. Success → `http.Redirect` to `/` with
303. Registry errors (e.g. provider already exists) → HTTP 400.

### 5. Dashboard template (cllama/ui/templates/dashboard.html)

Add a collapsible "Add Provider" section below the providers table. Reuse the same
form/input CSS already present for the Add Key form.

```html
<form method="post" action="/providers/add">
  <input name="name"       placeholder="mistral" required />
  <input name="base_url"   placeholder="https://api.mistral.ai/v1" required />
  <select name="auth">
    <option value="bearer" selected>bearer</option>
    <option value="x-api-key">x-api-key</option>
    <option value="none">none</option>
  </select>
  <select name="api_format">
    <option value="openai" selected>openai</option>
    <option value="anthropic">anthropic</option>
  </select>
  <input name="key_label"  placeholder="primary" />
  <input name="secret"     type="password" placeholder="API key" required />
  <button type="submit">Add Provider</button>
</form>
```

---

## File Inventory

### cllama submodule

| File | Change |
|---|---|
| `internal/provider/provider.go` | Add `Source` to `ProviderState`; set `Source: "seed"` in `LoadFromEnv` and `loadV1Locked`; add `AddRuntimeProvider` |
| `internal/ui/handler.go` | Register `/providers/add`; implement `handleProviderAdd` |
| `internal/ui/templates/dashboard.html` | Add Provider form |
| `internal/provider/provider_test.go` | `TestAddRuntimeProviderCreatesProvider`, `TestAddRuntimeProviderRejectsExistingProvider`, `TestAddRuntimeProviderSavesToFile` |
| `internal/ui/handler_test.go` | `TestHandleProviderAddCreatesProvider`, `TestHandleProviderAddRejectsBadURL`, `TestHandleProviderAddRejectsExistingProvider` |

### clawdapus main repo

| File | Change |
|---|---|
| `cmd/claw/compose_up.go` | Add `Source` to `v2ProviderState`; warn when seed overwrites runtime |
| `cmd/claw/compose_up_test.go` | `TestMergeProviderSeedsPreservesRuntimeProviderSource`, `TestMergeProviderSeedsWarnOnSeedOverwritesRuntime` |

---

## Implementation Tasks

1. **`compose_up.go`** — Add `Source string` to `v2ProviderState`. Add warning log in
   `mergeProviderSeeds` when seed overwrites a runtime provider.

2. **`compose_up_test.go`** — Tests: source field round-trips through `mergeProviderSeeds`;
   warning emitted when seed name conflicts with runtime provider.

3. **`provider.go`** — Add `Source` to `ProviderState`. Set `Source: "seed"` in
   `LoadFromEnv` and `loadV1Locked`. Implement `AddRuntimeProvider` (strict create,
   saves to file).

4. **`provider_test.go`** — Tests for `AddRuntimeProvider`: success path, existing
   provider rejection (both seed and runtime), `SaveToFile` called.

5. **`ui/handler.go`** — Register route, implement handler with field validation.

6. **`ui/handler_test.go`** — Tests: valid add, bad URL, existing provider conflict.

7. **`dashboard.html`** — Add Provider form.

---

## Acceptance Criteria

- A provider added via the UI is immediately usable by the proxy (no restart).
- `source: "runtime"` survives a `claw up` round-trip (field is preserved in JSON).
- Attempting to add a provider that already exists returns HTTP 400; no data is changed.
- When `claw up` seeds a provider that was previously runtime, a warning is logged and
  the seed wins.
- Hand-edited providers with no `source` field are preserved as-is on `claw up`
  (existing behavior; no migration required).
- All new code has unit test coverage.
