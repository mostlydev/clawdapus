# First-Class Google Gemini Provider Support

**Date:** 2026-04-06
**Status:** Draft
**Issue:** #119
**Execution plan:** `docs/plans/2026-04-08-119-gemini-provider-support.md`
**Scope:** cllama provider registry, compose_up env seeding, cost tracking

## Problem

Clawdapus has no native `google` provider. Gemini models work today only through OpenRouter (`openrouter/google/gemini-2.5-flash`). Operators who want direct Gemini API access — for cost separation, latency, or independence from OpenRouter — have no supported path.

Three code locations need the provider added:

1. **cllama provider registry** (`cllama/internal/provider/provider.go`) — `knownProviders`, `envKeyMap`, `envBaseURLMap`, `defaultAuth`, `defaultAPIFormat`, `LoadFromEnv`
2. **compose_up env seeding** (`cmd/claw/compose_up.go`) — `seedKeyDefs`, `isProviderKey`, base URL map
3. **cllama cost tracking** (`cllama/internal/cost/pricing.go`) — already has OpenRouter Google pricing, needs direct `google` provider entries

## Google Gemini API Compatibility

Google provides an OpenAI-compatible endpoint at `https://generativelanguage.googleapis.com/v1beta/openai/`. This means:
- Auth: `bearer` (standard `Authorization: Bearer <key>` header)
- API format: `openai` (standard chat completions)
- Model refs: `google/gemini-2.5-flash`, `google/gemini-2.5-pro`, etc.

The cllama proxy's `splitModel` function already handles the `provider/model` split correctly. A request for `google/gemini-2.5-flash` would split to provider=`google`, upstream model=`gemini-2.5-flash`, and route to the Google base URL.

## Implementation Steps

### Step 1: cllama Provider Registry

**File:** `cllama/internal/provider/provider.go`

Add `google` to `knownProviders`:
```go
var knownProviders = map[string]string{
    "openai":     "https://api.openai.com/v1",
    "xai":        "https://api.x.ai/v1",
    "anthropic":  "https://api.anthropic.com/v1",
    "openrouter": "https://openrouter.ai/api/v1",
    "ollama":     "http://ollama:11434/v1",
    "google":     "https://generativelanguage.googleapis.com/v1beta/openai",
}
```

Add env key mappings to `envKeyMap`:
```go
"GEMINI_API_KEY":     "google",
"GEMINI_API_KEY_1":   "google",
"GOOGLE_API_KEY":     "google",
```

Add base URL mapping to `envBaseURLMap`:
```go
"GOOGLE_BASE_URL": "google",
```

Add Google to `LoadFromEnv` key definitions:
```go
"google": {
    {"GEMINI_API_KEY", "seed:GEMINI_API_KEY", "primary"},
    {"GEMINI_API_KEY_1", "seed:GEMINI_API_KEY_1", "backup-1"},
    {"GOOGLE_API_KEY", "seed:GOOGLE_API_KEY", "backup-2"},
},
```

`GEMINI_API_KEY` takes priority over `GOOGLE_API_KEY` because it's more specific. `GOOGLE_API_KEY` is accepted as an alias since some tooling uses that name.

Auth and API format defaults: `google` uses `bearer` auth and `openai` format — both are the defaults in `defaultAuth` and `defaultAPIFormat`, so no changes needed there.

### Step 2: compose_up Env Seeding

**File:** `cmd/claw/compose_up.go`

Add to `seedKeyDefs`:
```go
{"GEMINI_API_KEY", "google", "seed:GEMINI_API_KEY", "primary"},
{"GEMINI_API_KEY_1", "google", "seed:GEMINI_API_KEY_1", "backup-1"},
{"GOOGLE_API_KEY", "google", "seed:GOOGLE_API_KEY", "backup-2"},
```

Add to `isProviderKey`:
```go
case "OPENAI_API_KEY", "OPENAI_API_KEY_1", "OPENAI_API_KEY_2",
    "ANTHROPIC_API_KEY", "ANTHROPIC_API_KEY_1",
    "OPENROUTER_API_KEY", "OPENROUTER_API_KEY_1",
    "GEMINI_API_KEY", "GEMINI_API_KEY_1", "GOOGLE_API_KEY":
    return true
```

Add to the base URL env map (around line 2748):
```go
"GOOGLE_BASE_URL": "google",
```

### Step 3: Cost Tracking

**File:** `cllama/internal/cost/pricing.go`

Add direct `google` provider pricing alongside the existing OpenRouter Google entries:

```go
"google": {
    "gemini-2.5-pro":   {InputPerMTok: 1.25, OutputPerMTok: 10.0},
    "gemini-2.5-flash": {InputPerMTok: 0.15, OutputPerMTok: 0.60},
},
```

These match the OpenRouter pass-through rates already in-tree (lines 72-73).

### Step 4: Tests

**File:** `cllama/internal/provider/provider_test.go`

- Test `LoadFromEnv` picks up `GEMINI_API_KEY` and registers a `google` provider with correct base URL
- Test `GOOGLE_API_KEY` fallback when `GEMINI_API_KEY` is absent
- Test that `google` provider has `bearer` auth and `openai` api_format
- Test `Get("google")` returns correct provider after env loading

**File:** `cmd/claw/compose_up_test.go`

- Test `isProviderKey` returns true for `GEMINI_API_KEY`, `GEMINI_API_KEY_1`, `GOOGLE_API_KEY`
- Test that `seedKeyDefs` includes google entries (if tested directly)

**File:** `cllama/internal/cost/pricing_test.go` (if exists)

- Test direct `google/gemini-2.5-flash` pricing lookup returns expected rates

### Step 5: Documentation

- Update `site/guide/cllama.md` to list `google` as a supported provider
- Add example showing direct Gemini configuration:

```yaml
x-claw:
  cllama-defaults:
    env:
      GEMINI_API_KEY: "${GEMINI_API_KEY}"

services:
  analyst:
    x-claw:
      models:
        primary: google/gemini-2.5-flash
```

- Update `AGENTS.md` gotchas to note that both `GEMINI_API_KEY` and `GOOGLE_API_KEY` are recognized

## What This Does NOT Change

- Proxy handler (`cllama/internal/proxy/handler.go`) — `splitModel` already handles `google/model` correctly
- Model policy (`cllama/internal/proxy/modelpolicy.go`) — provider-agnostic, works with any provider prefix
- Driver configs — all drivers use `shared.CollectProviders(rc.Models)` which extracts the provider from model refs; `google` will be collected automatically
- OpenRouter routing — `openrouter/google/gemini-*` continues to work as before (routed to OpenRouter, not to Google directly)

## Risks

- **Gemini API compatibility**: Google's OpenAI-compatible endpoint is in `v1beta`. If the endpoint path changes, only `knownProviders["google"]` needs updating. The `GOOGLE_BASE_URL` env override provides an escape hatch.
- **Key naming collision**: `GOOGLE_API_KEY` is a common env var name used by other Google services (Maps, Cloud, etc.). Making `GEMINI_API_KEY` the primary and `GOOGLE_API_KEY` a lower-priority alias mitigates accidental key leakage into the wrong service.
- **cllama submodule boundary**: Steps 1, 3, and 4 (provider tests) touch the cllama submodule. This requires a commit inside `cllama/`, then updating the submodule pointer in the main repo.
