# ADR-011: Canonical Scaffold Layout

**Date:** 2026-02-28
**Status:** Accepted

## Context

`claw init` originally generated a flat project (`Clawfile`, `AGENTS.md`, `claw-pod.yml` at repo root). That works for one agent, but multi-agent projects become inconsistent quickly: contract paths diverge, build contexts vary, and examples stop being interchangeable.

The goal is "Docker on Rails": scaffold conventions should be predictable, inspectable, and easy to extend with `claw agent add`, while still allowing manual edits.

## Decision

`claw init` and `claw agent add` now scaffold a canonical layout:

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

1. One agent per `agents/<name>/`.
2. Pod service definitions emit both `image:` and `build.context: ./agents/<name>`.
3. Runtime contract path in pod YAML is pod-root-relative (`./agents/<name>/AGENTS.md` or shared path).
4. `AGENTS.md` remains the standard contract filename.
5. Shared contracts may live under `shared/<profile>/AGENTS.md`; local agent contracts are never auto-deleted.
6. `.gitignore` is append-only for required scaffold entries (`.env`, `*.generated.*`).

## Consequences

**Positive:**
- Predictable project structure across examples and generated projects
- Clean multi-agent extension path via `claw agent add`
- Better separation of per-agent build context vs pod-level shared config
- Safer mutation model (append-only updates, no implicit deletions)

**Negative:**
- Existing flat projects remain valid but look different from generated projects
- Additional path depth for single-agent use cases

## Alternatives Considered

1. Keep flat root files forever (`Clawfile.<name>`, `AGENTS-<name>.md`) — rejected: weaker structure and harder scaling.
2. Enforce managed-only files (no manual edits) — rejected: conflicts with inspectable, hand-editable philosophy.
3. Auto-migrate all existing examples/projects — rejected for now; runtime stays layout-agnostic.
