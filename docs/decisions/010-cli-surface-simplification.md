# ADR-010: CLI Surface Simplification

**Date:** 2026-02-27
**Status:** Accepted
**Supersedes:** Decision in phase2-progress.md ("CLI commands are `claw compose up/down/ps/logs` (not `claw up`)")

## Context

The original CLI design used `claw compose up/down/ps/logs/health` — a parent `compose` subcommand with children. The rationale was Docker familiarity: users who know `docker compose up` would recognize the pattern.

After building through Phase 4, this prefix is dead weight. Docker needs the `compose` qualifier because `docker` is a multi-purpose tool (`docker build`, `docker run`, `docker compose up`, `docker image ls` — compose is one of many namespaces). `claw` is single-purpose: it governs agent pods. There is no ambiguity about what `claw up` does.

The project is pre-release with no external users. This is the last good opportunity to simplify the CLI surface before publishing.

## Decision

**Flatten all `claw compose *` subcommands to top-level `claw *` commands.**

| Before | After |
|--------|-------|
| `claw compose up [-f pod.yml] [-d]` | `claw up [-f pod.yml] [-d]` |
| `claw compose down [-f pod.yml]` | `claw down [-f pod.yml]` |
| `claw compose ps [-f pod.yml]` | `claw ps [-f pod.yml]` |
| `claw compose logs [-f pod.yml] [svc]` | `claw logs [-f pod.yml] [svc]` |
| `claw compose health [-f pod.yml]` | `claw health [-f pod.yml]` |

The full CLI surface becomes:

```
claw build       Build an agent image from a Clawfile
claw up          Launch a governed agent pod
claw down        Tear down a pod
claw ps          Show pod container status
claw logs        Stream pod logs
claw health      Driver health probes
claw inspect     Show claw.* labels from a built image
claw doctor      Verify Docker prerequisites
claw init        Scaffold a new Claw project
```

Every command is a top-level verb. No nesting. No namespaces.

The `-f` flag (path to `claw-pod.yml`) is shared across all lifecycle commands via a persistent flag on the root command.

No backwards compatibility shim — pre-release, zero external users.

## Consequences

**Positive:**
- Shorter commands for the most common operations
- Every command is a verb — discoverable via `claw --help`
- Consistent with the project's positioning: `claw` is the single tool for governed agent deployment, not a general-purpose container tool
- Matches the README/docs language that already says "claw up" informally

**Negative:**
- Loses the explicit `docker compose` analogy for new users coming from Docker
- If Clawdapus ever adds non-pod commands that could collide (e.g., `claw logs` for build logs vs. pod logs), the flat namespace could become ambiguous (unlikely — pod lifecycle is the primary concern)

**Risks:**
- None meaningful. Pre-release change, no users to break.

## Alternatives Considered

1. **Keep `claw compose *`** — maintains Docker analogy but adds friction to the most common commands. The analogy is already imperfect (`claw build` was never `claw compose build`). Rejected: consistency over analogy.

2. **Support both** — `claw up` as alias for `claw compose up`. Adds maintenance burden and confusion about which is canonical. Rejected: pick one.

3. **Different verb names** — e.g., `claw deploy`, `claw teardown`. Rejected: `up`/`down`/`ps`/`logs` are already universally understood from Docker and need no explanation.
