# Plan: `claw init --from` cross-runtime and same-runtime imports

Issue: https://github.com/mostlydev/clawdapus/issues/230

Author: Claude (claude:5379be30). v2 — converged with codex adversarial review (https://github.com/mostlydev/clawdapus/issues/230#issuecomment-4426381993).

## 1. Problem

`claw init --from <path>` is currently:

- **Source-locked.** Only `openclaw.json` is recognized. Hermes users have no import path.
- **Target-locked.** Hard-codes `FROM openclaw:latest`, `CLAW_TYPE openclaw`. `--type` is silently ignored when `--from` is present.
- **Layout-locked.** Emits the legacy flat scaffold instead of the ADR-011 canonical `agents/<name>/` layout.
- **Lossy.** Drops platform-specific routing, forgets Hermes Slack socket-mode needs both `SLACK_BOT_TOKEN` and `SLACK_APP_TOKEN` (#229/#231 now on master), and copies provider refs even when the target runtime cannot use them.
- **Secret-discoverability is poor.** Token-shaped fields ride through as literal `${VAR}` placeholders without ever being inventoried in `.env.example`.

## 2. Goals

A first-class migration UX with four covered paths:

| Source         | Target         | Behavior                                                                            |
| -------------- | -------------- | ----------------------------------------------------------------------------------- |
| OpenClaw cfg   | `openclaw`     | Preserve structurally identical: handles, channels routing, model slots.            |
| OpenClaw cfg   | `hermes`       | Map what Hermes supports; emit migration notes for unmappable features.             |
| Hermes cfg+env | `hermes`       | Preserve model/provider, identity (SOUL.md), channel env vars, allowed users.       |
| Hermes cfg+env | `openclaw`     | Same mapping discipline as the reverse direction.                                   |

Non-goals (per issue):

- No deployment-specific scaffolds.
- No new `channel://slack` SURFACE in the data model.
- No inlining of secrets into tracked files.
- No support for `--from <X> --type nanobot|microclaw|nanoclaw|...`. v1 limits import targets to `openclaw` and `hermes`. Other `--type` values with `--from` fail fast with "import target not supported yet."

## 3. Source detection

Replace `findOpenClawConfig` with `Detect(fromPath, override SourceKind) (Descriptor, error)`. Recognizes:

| Marker                                                                                  | Source kind |
| --------------------------------------------------------------------------------------- | ----------- |
| `openclaw.json` at path, `<path>/openclaw.json`, or `<path>/config/openclaw.json`       | `openclaw`  |
| `config.yaml` with a `model:` block (containing `default` and/or `provider`), optionally with sibling `SOUL.md`, `.env`, `skills/`, `cron/`, `memory/` | `hermes`    |
| Both present                                                                            | error: ambiguous; require explicit `--source` |
| Neither                                                                                 | current error: nothing recognized              |

A `.env` file next to the detected config is consumed as additional input (channel tokens, allowed-users, identity-overrides) but never copied verbatim. Its values are extracted into `.env.example` placeholders only.

`--source <openclaw|hermes>` exists as an explicit override when detection is ambiguous or the operator points at an unusual location.

## 4. Target selection

- `--type <openclaw|hermes>` is honored regardless of `--from`.
- If `--type` is omitted, target defaults to the source runtime (same-runtime import).
- `--type` outside `{openclaw, hermes}` with `--from` → `"import target %q not supported yet; use --type openclaw|hermes for --from"`.
- If the chosen target cannot represent a source feature, importer fails with a migration note unless the operator passes `--accept-loss=<feature,...>` (or `--accept-loss=all`). Each fatal note names the exact token needed.

## 5. Output layout

ADR-011 canonical layout, no opt-outs. The legacy flat scaffold is gone in v1.

```
project-root/
  agents/<agent-name>/
    Clawfile
    AGENTS.md
    SOUL.md             (only when source had one)
    skills/             (only when source had importable skills)
  claw-pod.yml
  .env.example
  .gitignore
  MIGRATION.md          (always emitted for --from runs)
```

Agent name source:

- OpenClaw → `agents.list[0].id` if present, normalized; else `assistant`.
- Hermes → `SOUL.md` first heading (kebab-cased) if present, else service hint, else `assistant`.

## 6. Translation matrix

### 6.1 Handles, channels, routing

The IR carries platform-specific typed structs (§9). The translator dispatches on platform, so Slack routing cannot accidentally map into Discord routing.

| Source feature                                          | OpenClaw target                       | Hermes target                          |
| ------------------------------------------------------- | ------------------------------------- | -------------------------------------- |
| OC `channels.discord.enabled=true`                      | `HANDLE discord`                      | `HANDLE discord`                       |
| OC `channels.discord.token` ($DISCORD_BOT_TOKEN)        | env placeholder                       | env placeholder                        |
| OC `channels.discord.dmPolicy`                          | `channel://discord` SURFACE           | env `DISCORD_REQUIRE_MENTION` / Hermes runtime knobs where matching exists; else fatal note (`--accept-loss=discord-routing`) |
| OC `channels.discord.guilds.<id>.requireMention=true`   | `channel://discord` SURFACE           | env `DISCORD_REQUIRE_MENTION=true`     |
| OC `channels.discord.allowFrom` / users allowlist       | `channel://discord allowFrom`         | env `DISCORD_ALLOWED_USERS` (comma-joined) |
| OC `channels.slack.enabled=true`                        | `HANDLE slack`                        | `HANDLE slack` + scaffold both `SLACK_BOT_TOKEN` and `SLACK_APP_TOKEN` |
| OC `channels.slack.token` ($SLACK_BOT_TOKEN)            | env placeholder                       | env placeholder + `SLACK_APP_TOKEN` placeholder |
| OC Slack routing fields (allowed channels, users, etc.) | fatal note: "no channel://slack SURFACE in v1; pass --accept-loss=slack-routing to drop, or wait for runtime support" | env `SLACK_ALLOWED_USERS` (comma-joined) |
| OC `agents.list[0].name`                                | preserved as HANDLE username + display name | preserved (SOUL.md identity)            |

| Source feature (Hermes env)                             | OpenClaw target                                                   | Hermes target          |
| ------------------------------------------------------- | ----------------------------------------------------------------- | ---------------------- |
| `DISCORD_BOT_TOKEN`/`DISCORD_BOT_ID`                    | placeholder in `.env.example`                                     | placeholder            |
| `DISCORD_REQUIRE_MENTION=true`                          | implied by `HANDLE discord` (OpenClaw enforces tool-only natively) | preserved env          |
| `DISCORD_ALLOWED_USERS`                                 | `channel://discord allowFrom` SURFACE                              | preserved env          |
| `SLACK_BOT_TOKEN`/`SLACK_APP_TOKEN`/`SLACK_BOT_ID`      | placeholders                                                      | placeholders           |
| `SLACK_ALLOWED_USERS`                                   | fatal note: `--accept-loss=slack-routing` to drop                  | preserved env          |
| `HERMES_DEFAULT_AGENT_IDENTITY`                         | folded into generated `AGENTS.md` (and `SOUL.md` if source had one)| folded into generated `AGENTS.md` / `SOUL.md`; **not** passed through as raw env. Migration note records the source value and target file. |
| Any other `HERMES_*`                                    | dropped, migration note                                            | dropped except canonical passthrough keys recognized by Clawdapus Hermes driver |

The Clawdapus Hermes driver materializes its own managed default identity (`managedDefaultAgentIdentity` in `internal/driver/hermes/config.go`). Round-tripping `HERMES_DEFAULT_AGENT_IDENTITY` as env passthrough would conflict with that. Folding into `AGENTS.md`/`SOUL.md` is the supported way to express operator identity intent.

### 6.2 Model + provider

Default discipline: **preserve native provider routes where the target supports them.** cllama is opt-in unless the source's existing wiring requires it.

`translateModel(source, target, opts) → (Clawfile MODEL/CLLAMA directives, env requirements, notes)` follows:

1. **Source already routes through a proxy** (OC `models.providers.<p>.baseUrl` pointing at non-vendor host, Hermes `model.base_url` non-empty):
   - Emit `MODEL primary <provider>/<model>` + `CLLAMA passthrough` regardless of target. Carry the upstream URL/key into a migration note ("verify cllama can reach <url>") rather than the Clawfile.
2. **Source uses a native provider Clawdapus knows** (`openrouter`, `anthropic`, `openai`):
   - Hermes target: native (Hermes driver `resolveModelConfig` already supports each → no cllama).
   - OpenClaw target: native via `models.providers.<provider>` (OpenClaw can carry the provider directly). Add env placeholder for the required API key (`OPENROUTER_API_KEY` / `ANTHROPIC_API_KEY` / `OPENAI_API_KEY`).
3. **Operator explicitly passes `--cllama`**: emit `CLLAMA passthrough` regardless of source state.
4. **Source provider unknown to Clawdapus, no base_url**: fatal note ("provider %q is not natively supported; pass --model <alt> or --accept-loss=custom-provider").

`--model <provider/model>` overrides source detection. `--cllama` forces proxy route.

This replaces the v1 draft's "openrouter/... always works through cllama" — which was too aggressive and would have broken Hermes/OpenClaw's existing native paths.

### 6.3 Skills, contracts, persona, cron

- OpenClaw `agents.defaults.workspace` → ignored (canonical layout owns the path).
- Skill files under `<source>/skills/*.md` (both runtimes) → copied to `agents/<name>/skills/`.
- Hermes `SOUL.md` → copied verbatim to `agents/<name>/SOUL.md`. Hermes target materializes it; OpenClaw target ignores it harmlessly.
- Hermes `cron/*.yaml` → **not** copied to `agents/<name>/cron/` (would imply Clawdapus runs them; it doesn't — `INVOKE` is the supported path). Instead, copied under `imported/cron/` (reference only, gitignored) and surfaced in `MIGRATION.md` with "translate each entry to an `INVOKE` directive in `Clawfile`."
- Persona refs: not handled in v1. Migration note recommends re-declaring via `PERSONA` directive.

### 6.4 cllama wiring

Derived from §6.2 rules, not a separate decision. The translator emits exactly one `CLLAMA passthrough` directive when rule 1 or rule 3 fires; otherwise none.

## 7. Migration notes UX

Importer always writes `MIGRATION.md` next to the scaffold:

```
# Migration notes — generated by `claw init --from`

Source: hermes (config: /path/to/hermes-home)
Target: openclaw

## Applied
- HANDLE discord (token + id placeholders in .env.example)
- HANDLE slack (token, app-token, id placeholders in .env.example)
- MODEL primary openrouter/anthropic/claude-sonnet-4 (native OpenRouter provider; OPENROUTER_API_KEY required)
- DISCORD_ALLOWED_USERS → channel://discord allowFrom [<list>]
- SOUL.md folded operator identity from HERMES_DEFAULT_AGENT_IDENTITY

## Action required
- cron/daily-summary.yaml: rewrite as `INVOKE` directive in Clawfile. Reference copy left at imported/cron/daily-summary.yaml.
- SLACK_ALLOWED_USERS: no channel://slack policy in Clawdapus v1. Re-run with `--accept-loss=slack-routing` to drop, or wait for runtime support.

## Verify before `claw up`
- OPENROUTER_API_KEY is set in .env
- Slack socket mode: SLACK_APP_TOKEN obtained from your Slack app
```

Each fatal action item is also surfaced as a `[claw] note:` line during init and is what blocks the run when `--accept-loss` is missing.

## 8. Secret extraction

A value matching `${VAR}`, or matching a known token pattern (`xoxb-…`, `xapp-…`, `sk-…`, `sk-or-…`, Discord bot-token regex, OpenAI keys, Anthropic keys), is replaced in generated tracked files (`Clawfile`, `claw-pod.yml`) with a `${VAR}` placeholder, and the var name is appended (deduped, sorted) to `.env.example`. Source files are read-only; we never write back.

Literal-token values that don't match any known pattern get a `${UNKNOWN_TOKEN_N}` placeholder and a migration note flagging "we saw a token-shaped string we couldn't classify."

## 9. Implementation sketch

New package `internal/initimport/` rooted under `cmd/claw/`-callable surface:

```
internal/initimport/
  detect.go        // Detect(fromPath, override SourceKind) -> Descriptor
  openclaw.go      // readOpenClaw(path) -> Descriptor
  hermes.go        // readHermes(path) -> Descriptor  (config.yaml + .env + SOUL.md; ignores unknown keys, records them in RawNotes)
  translate.go     // Translate(src Descriptor, target TargetRuntime, opts Options) -> (Plan, Notes)
  emit.go          // Emit(plan Plan, dir string) -> error
  envscan.go       // ExtractSecrets(values) -> placeholderMap, []EnvExampleEntry
  testdata/        // OpenClaw + Hermes fixture trees, one per scenario
```

`cmd/claw/init.go` shrinks to:

```go
func runInitWithOptions(dir, fromPath string, opts initScaffoldOptions, interactive bool) error {
    if fromPath != "" {
        return runInitFromImport(dir, fromPath, opts)
    }
    return runInitScaffold(dir, opts, interactive)
}
```

`runInitFromImport`:

1. `src, err := initimport.Detect(fromPath, opts.SourceOverride)`
2. `target, err := resolveImportTarget(opts.ClawType, src.Kind)` — rejects targets outside `{openclaw, hermes}`.
3. `plan, notes := initimport.Translate(src, target, opts)`
4. If `notes.HasFatal() && !opts.AcceptLossSatisfies(notes.FatalFeatures())`: print, return error.
5. `initimport.Emit(plan, dir)` writes files.
6. Print summary + next steps (mirroring current scaffold output).

`Descriptor` is the runtime-agnostic IR. Channel info is a typed sum, so the translator cannot conflate platforms:

```go
type Descriptor struct {
    Kind        SourceKind
    AgentName   string
    Identity    string                // SOUL.md contents, or HERMES_DEFAULT_AGENT_IDENTITY value
    Models      ModelSlots            // primary, fallback
    Cllama      bool                  // source already wired a proxy
    Channels    Channels              // typed sum
    EnvVars     map[string]string     // raw, from sibling .env
    SkillsDir   string                // optional source path
    CronDir     string                // optional source path (hermes only)
    RawNotes    []string              // unrecognized fields/keys from the reader
}

type Channels struct {
    Discord  *DiscordChannel
    Slack    *SlackChannel
    Telegram *TelegramChannel
}

type DiscordChannel struct {
    Token         string   // raw ($VAR or literal)
    BotID         string
    RequireMention bool
    AllowFrom     []string
    DMPolicy      string
    Guilds        []DiscordGuild
}

type SlackChannel struct {
    BotToken      string
    AppToken      string
    BotID         string
    AllowedUsers  []string
    // No DM/guild equivalents; Slack policy is intentionally separated.
}

type TelegramChannel struct {
    Token string
    BotID string
}

type ModelSlots struct {
    Primary  ModelRef
    Fallback []ModelRef
}

type ModelRef struct {
    Provider string  // "openrouter" | "anthropic" | "openai" | "custom" | ...
    Model    string
    BaseURL  string  // empty unless source had a custom base_url
    APIKey   string  // raw — never written to tracked files (extracted to .env.example)
}
```

`Notes` distinguishes informational from fatal:

```go
type Notes struct {
    Applied      []string
    Action       []string  // human-readable migration items
    FatalLosses  []FatalLoss
}

type FatalLoss struct {
    Feature  string  // canonical token used by --accept-loss
    Reason   string
}
```

Canonical `--accept-loss` tokens: `slack-routing`, `discord-routing`, `custom-provider`, `cron`, `identity-env`. `all` is the omnibus.

Hermes config schema fidelity: `readHermes` only consumes fields documented in `internal/driver/hermes/config.go` (`model.{default,provider,base_url,api_key}`, `terminal.*` is ignored as runtime). Unknown top-level keys land in `RawNotes` rather than failing the import.

### 9.1 Tests

`internal/initimport/*_test.go` (per-package unit tests using `testdata/`):

- detect: openclaw-only, hermes-only, ambiguous (both), neither, explicit `--source` override.
- openclaw reader: structural fields populated; unknown keys land in RawNotes.
- hermes reader: model.default + .env merge; unknown keys → RawNotes; missing SOUL.md is fine.
- translate (happy paths):
  - openclaw → openclaw: structural preservation
  - hermes → hermes: env passthrough, SOUL.md preserved
  - openclaw → hermes: Discord routing folded into env, Slack routing flagged fatal without accept-loss
  - hermes → openclaw: Discord allowFrom routing surfaces, Slack policy fatal without accept-loss
- translate (negative paths):
  - unsupported `--type nanobot` with `--from` → error from `resolveImportTarget`
  - unknown provider, no base_url, no `--accept-loss=custom-provider` → fatal
  - `--accept-loss=slack-routing` swallows the Slack fatal
  - source provider routed through proxy → `CLLAMA passthrough` emitted regardless of target
- envscan: known token shapes redacted; unknown tokens become `${UNKNOWN_TOKEN_N}` with note.
- emit: writes canonical layout, refuses overwrite, copies SOUL.md verbatim, places cron under `imported/cron/`, sorts `.env.example` deterministically.

`cmd/claw/init_test.go` integration test layer:

- Keep `TestInitFromOpenClawConfig`, expand to assert canonical layout (not flat) and new MIGRATION.md presence.
- Add `TestInitFromOpenClawWithHermesTarget` and `TestInitFromHermesWithOpenClawTarget` end-to-end.
- Add `TestInitFromRejectsUnsupportedTargetType`.
- Add `TestInitFromHermesSlackEmitsBothTokens`.

### 9.2 Files touched

- `cmd/claw/init.go` — replace `runInitFrom`, retire `findOpenClawConfig`, `detectChannels`, `detectModels`, `generateMigrationScaffold` (~150 LOC deleted; ~40 LOC added wrapper).
- `cmd/claw/init_test.go` — expand per §9.1.
- New: `internal/initimport/` package (~500-700 LOC including tests + fixtures).
- `site/guide/` — short migration page referencing the four paths (Phase 3, may follow in a separate PR).

## 10. Verification

- `go test ./...`
- `go vet ./...`
- Manual: `claw init <dst> --from <openclaw-fixture> --type hermes`, then `claw build` + `claw up -d` against fixture. Same in reverse.
- `MIGRATION.md` content asserted in tests for at least one cross-runtime case.

## 11. Decisions (converged from review)

Codex's 10 review points have been applied verbatim:

1. **No `--legacy-flat`.** Canonical layout only (§5).
2. **No `channel://slack` stub.** Slack routing fields are fatal without `--accept-loss=slack-routing` (§6.1, §7).
3. **Granular `--accept-loss=<token,...>`** with `all` as the omnibus. Canonical tokens listed in §9.
4. **Typed channel IR.** `DiscordChannel`/`SlackChannel`/`TelegramChannel` sum-type prevents cross-platform mapping bugs (§9).
5. **Hermes schema fidelity.** Reader consumes only documented fields; unknowns land in `RawNotes` (§9, §6).
6. **`HERMES_DEFAULT_AGENT_IDENTITY` is not passed through as env.** Folded into `AGENTS.md`/`SOUL.md` (§6.1).
7. **Native provider routes preserved.** cllama is opt-in unless source already used proxy semantics or operator requests it (§6.2). v1 draft's aggressive openrouter→cllama rule is gone.
8. **Target scope limited to `{openclaw, hermes}`** for `--from`. Other `--type` values fail with a clear message (§2, §4).
9. **Hermes cron is migration evidence, not active import.** Files land under `imported/cron/` and are flagged in `MIGRATION.md` for translation to `INVOKE` (§6.3).
10. **Negative-path tests are first-class.** See §9.1 negative paths list.

The MIGRATION.md typo from the v1 draft (`SLACK_ALLOWED_USERS → channel://discord allowFrom`) is fixed in §7's revised example.

## 12. Phasing

- Phase 1: `internal/initimport/` package + `cmd/claw/init.go` wiring + same-runtime tests (openclaw→openclaw, hermes→hermes).
- Phase 2: cross-runtime translation + migration notes + negative-path tests.
- Phase 3: docs/guide page.

Phases 1+2 ship in one PR (issue acceptance criteria require cross-runtime). Phase 3 may lag by one PR.
