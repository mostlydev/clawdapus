# ADR-011: Canonical-By-Default Scaffold Layout

**Date:** 2026-02-28
**Status:** Accepted

## Context

`claw init` originally generated a flat project (`Clawfile`, `AGENTS.md`, `claw-pod.yml` at repo root). That works for one agent, but multi-agent projects become inconsistent quickly: contract paths diverge and build contexts vary.

The goal is "Docker on Rails": scaffold conventions should be predictable, inspectable, and easy to extend with `claw agent add`, while still allowing manual edits.

## Decision

`claw init` scaffolds the canonical layout by default:

```text
<project>/
├── claw-pod.yml
├── .env.example
├── .gitignore
├── agents/
│   └── <agent>/
│       ├── Clawfile
│       ├── AGENTS.md
│       └── skills/
└── shared/                # optional, created only when needed
```

Key rules:

1. `claw init` creates one agent per `agents/<name>/`.
2. Pod service definitions emit both `image:` and `build.context: ./agents/<name>`.
3. Runtime contract path in pod YAML is pod-root-relative (`./agents/<name>/AGENTS.md` or shared path).
4. `AGENTS.md` remains the standard contract filename.
5. Shared contracts may live under `shared/<profile>/AGENTS.md`; local agent contracts are never auto-deleted.
6. `.gitignore` is append-only for required scaffold entries (`.env`, `*.generated.*`).
7. `claw agent add` preserves existing project layout by default:
`--layout auto` detects canonical vs flat from current pod/build/contract paths and scaffolds in-kind.
8. `claw agent add --layout canonical|flat` can override auto-detection.

## Consequences

**Positive:**
- Predictable default project structure from `claw init`
- Clean multi-agent extension path via `claw agent add` without forced migration
- Better separation of per-agent build context vs pod-level shared config
- Safer mutation model (append-only updates, no implicit deletions)

**Negative:**
- Tooling logic is slightly more complex because both canonical and flat layouts are supported
- Additional path depth for single-agent use cases

## Alternatives Considered

1. Keep flat root files forever (`Clawfile.<name>`, `AGENTS-<name>.md`) as the only layout — rejected: weaker structure and harder scaling for new scaffolds.
2. Enforce canonical-only for every project mutation — rejected: forces migration and conflicts with inspectable, hand-editable philosophy.
3. Enforce managed-only files (no manual edits) — rejected: conflicts with inspectable, hand-editable philosophy.
