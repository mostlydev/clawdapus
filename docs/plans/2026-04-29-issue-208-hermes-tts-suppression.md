# Issue #208 — Hermes driver disables `text_to_speech` for Discord-handle agents by default

**Status:** implemented by Codex on `issue-208-hermes-disable-tts-discord`; Claude reviews, builds/pushes `hermes-base:v2026.3.17-claw.4`, releases, and deploys.

**Linked:** [#208](https://github.com/mostlydev/clawdapus/issues/208).

**Author:** Claude (df3fed3a). Branch: `issue-208-hermes-disable-tts-discord`.

## TL;DR

Hermes ships `text_to_speech` (display name 🔊 `speak`) in `_HERMES_CORE_TOOLS`. The model picks it for short replies on Discord, generates an audio file via `edge-tts`, and uploads as a Discord attachment — confirmed live on tiverton-house. Trading desk and similar pods have zero use for model-driven voice replies on Discord and need this off by default.

Two-layer fix:

1. **`hermes-base` image patch** (`dockerfiles/hermes-base/patch-hermes-runtime.py`): patch `toolsets.py` so `_HERMES_CORE_TOOLS` and any named bundle that explicitly lists `text_to_speech` (or other tools named in `CLAWDAPUS_DISABLED_TOOLS`) is filtered at module-load time. Bump `DefaultHermesBaseTag` to `v2026.3.17-claw.4`.
2. **Hermes driver** (`internal/driver/hermes/config.go`): for any agent with at least one Discord handle, inject `CLAWDAPUS_DISABLED_TOOLS=text_to_speech` into the container env by default. Pod-yaml override: `x-claw.hermes.allow-tools: [text_to_speech]` opts back in.

Telegram-only Hermes agents are unaffected. Multi-platform Hermes agents (Telegram + Discord) get `text_to_speech` disabled because Discord is the lowest-tolerance surface.

## v1 design

### 1. `hermes-base` patch

Append to `dockerfiles/hermes-base/patch-hermes-runtime.py`:

```python
toolsets_py = purelib / "toolsets.py"
text = toolsets_py.read_text()

# Inject env-driven disable filter at module top, after existing imports.
# Reads CLAWDAPUS_DISABLED_TOOLS as a comma-separated list and filters
# both _HERMES_CORE_TOOLS and any TOOLSETS[*]["tools"] entry containing
# a named tool. Bundle "includes" arrays are unaffected — nested toolset
# resolution still works.
text = replace_once(
    text,
    "_HERMES_CORE_TOOLS = [",
    """import os as _claw_os

_CLAW_DISABLED_TOOLS = {
    t.strip()
    for t in _claw_os.getenv("CLAWDAPUS_DISABLED_TOOLS", "").split(",")
    if t.strip()
}


def _claw_filter_tools(tools):
    if not _CLAW_DISABLED_TOOLS:
        return tools
    return [t for t in tools if t not in _CLAW_DISABLED_TOOLS]


_HERMES_CORE_TOOLS = [""",
    "_HERMES_CORE_TOOLS env-driven disable filter prelude",
)

# Wrap the module-level _HERMES_CORE_TOOLS = [...] assignment so the env
# filter is applied immediately after the literal list is built.
text = replace_once(
    text,
    "    \"ha_list_entities\", \"ha_get_state\", \"ha_list_services\", \"ha_call_service\",\n]\n",
    "    \"ha_list_entities\", \"ha_get_state\", \"ha_list_services\", \"ha_call_service\",\n]\n_HERMES_CORE_TOOLS = _claw_filter_tools(_HERMES_CORE_TOOLS)\n",
    "_HERMES_CORE_TOOLS env-driven disable filter application",
)

# Filter every TOOLSETS[*]["tools"] list that may carry a disabled tool.
# Done at module load so all consumers (gateway/run.py, model_tools.py)
# see the filtered list.
text = replace_once(
    text,
    "TOOLSETS = {",
    "TOOLSETS = {  # filtered post-build via _claw_filter_tools below",
    "TOOLSETS comment marker",
)

text += (
    "\n# Apply env-driven tool filter to every named bundle's explicit tools[]\n"
    "# (does not touch includes; nested toolset resolution still works).\n"
    "for _ts_name, _ts_def in list(TOOLSETS.items()):\n"
    "    if isinstance(_ts_def, dict) and \"tools\" in _ts_def and isinstance(_ts_def[\"tools\"], list):\n"
    "        _ts_def[\"tools\"] = _claw_filter_tools(_ts_def[\"tools\"])\n"
)

toolsets_py.write_text(text)
```

Notes:
- `_HERMES_CORE_TOOLS` is a module-level list reused by reference inside `TOOLSETS` (e.g. `TOOLSETS["hermes-discord"]["tools"] = _HERMES_CORE_TOOLS`). The two-step rewrite (first the list, then a sweep over `TOOLSETS`) is defensive: if upstream Hermes changes the assignment from "share by reference" to "copy on define", the filter still applies.
- Filter runs at import time, so by the time `gateway/run.py` builds the agent's tool manifest, `text_to_speech` is gone from the relevant lists.
- `edge_tts` is still installed and `text_to_speech` is still **registered** in the registry — `check_tts_requirements()` still returns True. We just don't expose it via the per-platform toolset bundle. That keeps the patch surgical: any explicit caller could still invoke it, but the model-visible tool manifest doesn't list it.

Bump `internal/infraimages/release_manifest.go`:

```go
DefaultHermesBaseTag = "v2026.3.17-claw.4"
```

(Patch tag bump on same upstream Hermes release.)

### 2. Hermes driver

`internal/driver/hermes/config.go` — extend the env-build path that already calls `hasDiscordHandle`:

```go
const clawdapusDisabledToolsEnv = "CLAWDAPUS_DISABLED_TOOLS"
const tts = "text_to_speech"

func defaultDisabledHermesToolsForDiscord(rc *driver.ResolvedClaw) []string {
    // Default-deny for Discord: text_to_speech is the only entry today.
    // Future v2 may add image_generate or other footgun tools.
    return []string{tts}
}

func resolveDisabledHermesTools(rc *driver.ResolvedClaw) []string {
    if !hasDiscordHandle(rc) {
        return nil
    }
    disabled := defaultDisabledHermesToolsForDiscord(rc)

    // Pod-yaml override: x-claw.hermes.allow-tools removes from disabled list.
    allow := rc.HermesAllowTools()  // new accessor on ResolvedClaw
    if len(allow) == 0 {
        return disabled
    }
    allowSet := make(map[string]struct{}, len(allow))
    for _, t := range allow {
        allowSet[t] = struct{}{}
    }
    out := make([]string, 0, len(disabled))
    for _, t := range disabled {
        if _, ok := allowSet[t]; ok {
            continue
        }
        out = append(out, t)
    }
    return out
}
```

Wire into the existing env-map builder (call site near line 75):

```go
if disabled := resolveDisabledHermesTools(rc); len(disabled) > 0 {
    env[clawdapusDisabledToolsEnv] = strings.Join(disabled, ",")
}
```

`HermesAllowTools()` accessor on `driver.ResolvedClaw` (or wherever the Hermes-specific block lives — currently `rc.Hermes.AllowTools` if a `Hermes` struct exists, otherwise add one). Since `x-claw.hermes` is a new pod-yaml namespace, it must be parsed:

`internal/pod/types.go`:

```go
type ClawBlock struct {
    // ... existing fields ...
    Hermes *HermesConfig `yaml:"-"`
}

type HermesConfig struct {
    AllowTools []string
}
```

`internal/pod/parser.go`:

```go
type rawHermesConfig struct {
    AllowTools []string `yaml:"allow-tools"`
}

func parseHermesConfig(raw map[string]any) (*HermesConfig, error) {
    if raw == nil {
        return nil, nil
    }
    bytes, _ := yaml.Marshal(raw)
    var rh rawHermesConfig
    if err := yaml.Unmarshal(bytes, &rh); err != nil {
        return nil, fmt.Errorf("parse x-claw.hermes: %w", err)
    }
    return &HermesConfig{AllowTools: rh.AllowTools}, nil
}
```

Hook into the existing `parseClawBlock` flow.

### 3. Tests

Unit (`internal/driver/hermes/config_test.go`):

- Discord-handle agent with no `x-claw.hermes` → env contains `CLAWDAPUS_DISABLED_TOOLS=text_to_speech`.
- Discord-handle agent with `x-claw.hermes.allow-tools: [text_to_speech]` → env does NOT contain `CLAWDAPUS_DISABLED_TOOLS`.
- Discord-handle agent with `x-claw.hermes.allow-tools: [other_tool]` → env contains `CLAWDAPUS_DISABLED_TOOLS=text_to_speech` (other_tool isn't in the disabled list to begin with).
- Telegram-only agent → no `CLAWDAPUS_DISABLED_TOOLS` env entry.
- Telegram + Discord agent → env contains `CLAWDAPUS_DISABLED_TOOLS=text_to_speech` (Discord rule wins).

Unit (`internal/pod/parser_test.go`):

- Pod yaml round-trip for `x-claw.hermes.allow-tools` lists.
- Empty / missing `x-claw.hermes` → `nil` Hermes config.

Spike extension (`cmd/claw/spike_hermes_base_image_contract_test.go`):

- Existing `TestSpikeHermesBaseImageContract` adds an assertion: with `CLAWDAPUS_DISABLED_TOOLS=text_to_speech` set in the container env, `python -c 'from toolsets import _HERMES_CORE_TOOLS; assert "text_to_speech" not in _HERMES_CORE_TOOLS'` exits zero.

### 4. Acceptance

1. New Hermes pods with Discord handles and no `x-claw.hermes.allow-tools` block do not expose `text_to_speech` in the model's tool manifest.
2. `tools_count` in cllama request logs decreases by 1 (e.g. 22 → 21) for affected Hermes-Discord agents after redeploy.
3. Pod yaml `x-claw.hermes.allow-tools: [text_to_speech]` re-enables `text_to_speech` for that service.
4. Telegram-only Hermes agents are unaffected (env var not set).
5. `hermes-base:v2026.3.17-claw.4` rebuilds and the patch survives upstream Hermes startup.

## Implementation order

1. **Phase A** — Done: `dockerfiles/hermes-base/patch-hermes-runtime.py` filters `_HERMES_CORE_TOOLS` and named bundle `tools[]` lists.
2. **Phase B** — Pending Claude release step: build & push `ghcr.io/mostlydev/hermes-base:v2026.3.17-claw.4` (release skill Step 10).
3. **Phase C** — Done: `DefaultHermesBaseTag` and `hermes.BaseImageVersion` now point at `v2026.3.17-claw.4`.
4. **Phase D** — Done: pod yaml parser supports `x-claw.hermes.allow-tools`.
5. **Phase E** — Done: Hermes driver writes `CLAWDAPUS_DISABLED_TOOLS=text_to_speech` to both `.env` and container env for Discord-handle agents unless opted in.
6. **Phase F** — Done for unit/source coverage; spike test has been extended but still requires Docker to run.
7. **Phase G** — Done: `site/guide/drivers.md` and `site/changelog.md` Unreleased updated.

Phases A–C ship the runtime piece; D–E ship the driver-side default. They can land in one PR. Phase G goes into the same release commit that Claude promotes via `/clawdapus-release`.

## Out of scope (v1)

- Other footgun tools (`image_generate`, `browser_*`). Same mechanism would apply; each needs its own opinionated default. File follow-up issues if tiverton or other pods see these surface.
- Fleet-wide `x-claw.hermes-defaults.disable-tools` pod-level config — v2 if operators ask.
- Hermes' own `platform_toolsets` config.yaml override — too coarse; can't disable individual tools without defining a custom toolset bundle.
- TTS toggling via `/voice` Discord command at runtime — that's a different code path (auto-TTS for replies vs the model-invoked tool); unrelated.

## Operator deploy path

After release: SSH to clawdbot@tiverton, `claw pull && claw up -d`. Then mention any Hermes-driven agent (Weston, Tiverton, Logan, …) — the response should be plain text, no audio. cllama `tools_count` log field drops by 1 vs the pre-release baseline. No pod yaml changes needed; the default-deny flips automatically.

If a future use case wants TTS back for a specific service:

```yaml
services:
  some-voice-bot:
    x-claw:
      hermes:
        allow-tools: [text_to_speech]
```
