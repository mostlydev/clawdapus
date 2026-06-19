# Plan: `claw init --from` same-runtime imports

Issue #230 originally explored both same-runtime imports and cross-runtime
translation. After implementation review, the cross-runtime path was cut from
scope: automated OpenClaw -> Hermes or Hermes -> OpenClaw translation is too
lossy to be a trustworthy operator workflow. Operators switching runtimes should
start from a fresh scaffold and hand-port the contract deliberately.

## Scope

Keep:

- OpenClaw config -> claw-managed OpenClaw.
- Hermes config + `.env` -> claw-managed Hermes.
- Canonical `agents/<name>/` layout.
- Source autodetection for `openclaw.json` or Hermes `config.yaml`.
- `--source openclaw|hermes` for ambiguous source directories.
- Secret extraction into `.env.example`.
- A slim `MIGRATION.md` with applied mappings, action notes, secret placeholders,
  and pre-`claw up` checks.

Drop:

- OpenClaw -> Hermes translation.
- Hermes -> OpenClaw translation.
- `--accept-loss`.
- `--type` target selection during `--from` imports. With `--from`, `--type`
  is an error; imports preserve the detected source runtime.
- Cross-runtime routing conversions and loss-token machinery.

## Source Detection

`claw init --from <path>` accepts:

- `<path>/openclaw.json`
- `<path>/config/openclaw.json`
- `<path>/config.yaml` or `<path>/config.yml` that looks like a Hermes config
  with a `model:` block

If both source markers are present, the command fails and asks for
`--source openclaw` or `--source hermes`.

## Translation Rules

OpenClaw imports preserve the same runtime shape:

- model primary and first runtime-effective fallback
- cllama when the source provider used a custom `baseUrl`
- Discord, Slack, and Telegram handles
- Discord routing that maps to current `channel://discord`
- source `skills/*.md`

Hermes imports preserve the same runtime shape:

- model provider/default/base_url/api_key where supported
- cllama when the source used `model.base_url`
- Discord, Slack, and Telegram handle env vars
- Hermes `SOUL.md` and source identity intent in generated agent material
- source `skills/*.md`
- `cron/` files as gitignored references under `imported/cron/`, with a note to
  translate them to `INVOKE`

Unsupported same-runtime details are not hidden. Non-critical drops become
`MIGRATION.md` action notes. Provider choices that would create a known-broken
scaffold fail early with a `--model <provider/model>` recovery hint.

## Tests

- OpenClaw import emits canonical layout, handles, `.env.example`, and no legacy
  root `Clawfile`.
- Hermes import emits canonical Hermes layout and preserves required handle env.
- `--type` with `--from` fails clearly.
- Ambiguous source detection requires `--source`.
- Custom unsupported providers without `base_url` fail with a `--model` hint.
- Migration notes cover copied cron references and non-preserved same-runtime
  routing details.
