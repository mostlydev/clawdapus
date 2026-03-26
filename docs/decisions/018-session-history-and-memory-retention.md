# ADR-018: Session History and Persistent Memory Surfaces

**Date:** 2026-03-26
**Status:** Accepted
**Depends on:** ADR-008 (cllama Sidecar Standard), ADR-014 (Telemetry Normalization)
**Implementation:** Phase 1 complete — durable session history recorder, proxy wiring, compose mount, persistent host directory. Phase 2 (scoped read API) and Phase 3 (derived retrieval) deferred. Plan: docs/plans/2026-03-26-cllama-session-history.md

## Context

Clawdapus deploys bots as persistent presences. A crypto analyst running for months, a trading desk bot executing strategies across sessions, a Discord representative that users know by name — these are not ephemeral task workers. They accumulate context, develop patterns, and carry forward the consequences of prior decisions.

But until this ADR, every runner held conversational context purely in process memory. Container restart meant amnesia. `claw up` wiped `.claw-runtime/` on every run. A re-deploy was a full reset.

This is structurally wrong for the Clawdapus model. The contract survives because it is infra-owned. The persona survives because it is a persistent bind mount. But the agent's actual interaction history — its most operationally relevant context — was being silently discarded.

The problem has two distinct parts that require separate solutions:

1. **Session history** — the record of what the agent said, asked, and was told by the LLM. This is a byproduct of inference, not something the agent writes. It belongs to the operator, not the runner.
2. **Portable memory** — the agent's own scratch and note-taking space. This is agent-authored and already exists as a runner-facing bind mount at `/claw/memory`. This ADR does not change it.

Conflating these two surfaces causes mistakes. If session history is stored with runner memory, it can be overwritten by the agent or lost when the agent's memory format changes. If portable memory is written by the proxy, it violates the runner's authority over its own scratch space.

## Decision

### 1. Two distinct surfaces, two distinct owners

| Surface | Owner | Written by | Path inside container | Host path | Survived by |
|---------|-------|------------|-----------------------|-----------|-------------|
| Session history | Infrastructure | cllama proxy | `/claw/session-history` | `<pod-dir>/.claw-session-history/` | `claw up`, container restart, `claw down` |
| Portable memory | Runner | Agent | `/claw/memory` | `<pod-dir>/.claw-runtime/<agent-id>/memory/` (or persona dir) | Container restart only |

These surfaces must never be merged, renamed, or crossed. The proxy does not write to runner memory. The runner does not write to session history.

### 2. Session history is infra-owned and proxy-written

cllama captures successful LLM turns at the proxy boundary and writes normalized JSONL entries to `<base-dir>/<agent-id>/history.jsonl`. One entry per successful 2xx completion.

Capture is unconditional: it happens for every claw in the pod regardless of whether the agent ever asks for its history. The agent does not need to do anything to enable it.

The entry schema (see `cllama/internal/sessionhistory/recorder.go`):

```
Version            int
TS                 string (RFC 3339)
ClawID             string
Path               string
RequestedModel     string
EffectiveProvider  string
EffectiveModel     string
StatusCode         int
Stream             bool
RequestOriginal    json.RawMessage
RequestEffective   json.RawMessage
Response           Payload { Format, JSON, Text }
Usage              { PromptTokens, CompletionTokens, ReportedCostUSD }
```

SSE streams are stored as `Response.Format = "sse", Response.Text = <captured text>` to avoid invalid JSONL from raw event bytes.

### 3. The persistent host directory is a sibling of `.claw-auth`

Session history must not live under `.claw-runtime/` because `claw up` wipes that directory on every run.

The canonical pattern for persistent cllama state is the sibling directory alongside `.claw-auth/`. Session history follows the same model:

```
<pod-dir>/
├── .claw-runtime/        ← wiped on every claw up
├── .claw-auth/           ← persistent: tokens and keys
└── .claw-session-history/ ← persistent: session log per agent
    ├── analyst-0/
    │   └── history.jsonl
    └── researcher/
        └── history.jsonl
```

`claw up` creates `.claw-session-history/` alongside `.claw-auth/` before launching compose. The `ensurePersistentCllamaDir` helper encapsulates both.

### 4. Recording rules

- Record only when the upstream response is 2xx. Non-2xx responses belong to the structured audit log (ADR-014), not session history.
- Preserve both `RequestOriginal` (what the claw sent) and `RequestEffective` (what cllama forwarded after any modifications). This distinction matters for future policy analysis.
- Append-only. Never delete or truncate entries. History grows until the operator explicitly removes it.
- The env var `CLAW_SESSION_HISTORY_DIR` controls the base directory. Empty string means no-op — the recorder is disabled and no disk I/O occurs.

### 5. Phase model

**Phase 1 (this ADR): Retention**
Capture and persist. No read API. No prompt decoration. The agent gains no new capabilities from session history in Phase 1 — this phase is purely about ensuring the data is not lost.

**Phase 2: Scoped read API**
Add a self-scoped read endpoint to cllama so a claw can query its own recent session history using its bearer token. At that point, instructions can meaningfully tell claws they may retrieve prior context from cllama.

**Phase 3: Derived retrieval**
Build retrieval on top of retained history: rolling summaries, recency windows, time-decayed ranking. This is where cllama starts behaving like a memory-aware retrieval layer rather than only a recorder.

## Rationale

**Why proxy-owned rather than runner-owned?**

Runners differ. OpenClaw, Nanobot, PicoClaw, Hermes — each manages context in its own way. Some persist conversation windows to disk; some don't. Some formats are runner-specific and not portable. Relying on the runner to preserve session history means every runner would need to implement the same retention contract, producing N incompatible implementations.

The proxy is the one thing all cllama-enabled claws have in common. Every LLM turn passes through it. Session history captured at the proxy boundary is runner-agnostic, format-normalized, and always present when cllama is in use.

**Why keep portable memory runner-owned?**

Portable memory at `/claw/memory` is the agent's active scratchpad — a place to write notes, drafts, and knowledge it is building. The format, naming convention, and lifecycle of that content is under the agent's control. If the proxy wrote to runner memory, agents would need to be careful not to overwrite infrastructure-generated files. That couples the runner and the proxy in the wrong direction.

**Why JSONL over a database?**

JSONL is inspectable without tooling, portable, appendable without locking issues at rest, and trivially grep-able. A SQLite database would be a stronger choice for Phase 3 retrieval but JSONL is the right substrate for Phase 1 retention — readable on day one, no migration needed to get data out.

## Consequences

**Positive:**
- Bots accumulate session context across restarts and redeployments without any runner changes
- History is operator-visible and directly inspectable (`cat .claw-session-history/<agent-id>/history.jsonl`)
- Captured without runner cooperation — works with all 7 current driver types
- Provides a substrate for Phase 2 and 3 features without constraining their design
- Consistent with the existing persistent-sibling-dir pattern established by `.claw-auth/`

**Negative:**
- Phase 1 is pure write — agents gain no active benefit until Phase 2 lands
- JSONL grows without bound; operators need manual housekeeping or an explicit rotation policy (future work)
- Session history and portable memory are now two distinct persistence surfaces; operators must understand the ownership boundary

## Relationship to Other ADRs

- **ADR-008** established cllama as the canonical interception point. Session history is a natural extension of that role — from governing inference to recording it.
- **ADR-014** owns structured telemetry for audit and cost accounting. Session history is distinct: it preserves full turn content for continuity, not just event metadata for audit.
- **ADR-012 (Master Claw)** may eventually consume session history as an additional signal for fleet governance, but that integration is deferred beyond Phase 3.
