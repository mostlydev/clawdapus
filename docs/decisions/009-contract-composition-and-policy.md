# ADR-009: Contract Composition and Policy Inclusion

**Date:** 2026-02-21
**Status:** Accepted

## Context

Initially, the agent's behavioral contract was defined by a single file (e.g., `AGENTS.md` or `CLAUDE.md`), mapped via the `AGENT` directive or the `x-claw.agent` pod configuration. 

As deployments mature (e.g., the `trading-desk` example), operators need to inject additional organizational context, risk limits, approval workflows, and reference materials. Blindly concatenating all these documents into a single `AGENTS.md` file degrades token efficiency, blurs the line between hard rules and reference material, and removes the semantic boundaries needed for rigorous auditability and policy enforcement (especially by the `cllama` sidecar).

## Decision

We are introducing a deterministic **Contract Composition** system as an optional extension to the base agent definition. The simple, intuitive approach of providing a single contract file remains the default and is fully supported.

1. **Simple Mode (Default):**
   Operators provide a single contract file via the `AGENT` directive (Clawfile) or `agent: <path>` (pod manifest). Clawdapus mounts this file as the canonical `/claw/AGENTS.md`. No composition or concatenation occurs.

2. **Composition Mode (Optional Extension):**
   Operators can add an `include` array to the `x-claw` block to modularize their context. This is additive to the base agent file.

Documents in the `include` array can be declared with specific semantic `mode`s:

- **`enforce`**: Hard constraints and mandatory rules.
- **`guide`**: Strong recommendations and procedural workflows.
- **`reference`**: Informational context, playbooks, and background data.

### Implementation Mechanics

1. **Declared Inclusion:** 
   ```yaml
   x-claw:
     agent: ./agents/base.md
     include:
       - id: risk_limits
         file: ./governance/risk-limits.md
         mode: enforce
       - id: approval_flow
         file: ./ops/approval-workflow.md
         mode: guide
       - id: strategy_notes
         file: ./research/strategy-playbook.md
         mode: reference
   ```

2. **Compile Step:** At runtime (`claw up`), Clawdapus executes a deterministic compile step.
   - It reads the base agent file.
   - It concatenates the contents of `enforce` and `guide` inclusions in declared order, wrapping them in source markers (e.g., `--- BEGIN: risk_limits (enforce) ---`).
   - This combined content is written to a transient artifact (e.g., `AGENTS.generated.md`) and bind-mounted into the container as the canonical `/claw/AGENTS.md`.

3. **Skill Mounting:** `reference` documents are *not* inlined. They are mounted directly into the runner's skill directory as read-only files (e.g., `/claw/skills/include-strategy_notes.md`).

4. **Context Bootstrapping:** `CLAWDAPUS.md` is updated with a generated index listing all included documents, their IDs, modes, descriptions, and mount paths, ensuring the agent knows exactly what context is available and how binding it is.

## Rationale

This multi-tiered inclusion model preserves the semantic weight of different documents. It allows operators to maintain modular governance files (like a shared `risk-limits.md` across a whole fleet) without manual copy-pasting. 

Crucially, this structured metadata (`enforce` vs `reference`) becomes a foundational input for the `cllama` sidecar (see ADR-008). The sidecar can parse the generated contract boundaries and apply strict, programmatic validation specifically against the `enforce` blocks, while ignoring the noise of `reference` materials during its drift scoring and compliance checks.

## Consequences

**Positive:**
- Modular, reusable governance and context files.
- Better token efficiency (reference materials are loaded on-demand via skills, not crammed into the system prompt).
- Provides structured, semantic inputs (`enforce` rules) for the `cllama` policy engine.
- High auditability (source markers trace generated contract text back to specific governance files).

**Negative:**
- Adds complexity to the `claw up` compose generation phase (requires reading, concatenating, and hashing files on the host before starting containers).
