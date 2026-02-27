# Updating Clawdapus Documentation

When implementation changes — new directives, new driver behavior, new decisions — update these locations in order.

## Checklist

### 1. CLAUDE.md (root)
**Update when:** Any implementation phase completes, new settled decisions are made, package structure changes.
- Implementation status table — mark phase DONE
- Important Implementation Decisions — add any new settled patterns
- Key Package Structure — if new packages added

### 2. docs/plans/phase2-progress.md
**Update when:** A task or phase completes.
- Add a new section for the completed slice/phase
- Record commit hash, design decisions, and any notes for future resumption
- Mark tasks DONE with commit hashes

### 3. MANIFESTO.md (root)
**Update when:** Core architectural principles change, new concepts are introduced (e.g. new directive types, new runtime behaviors), or a phase is completed that touches the fundamental design.
- This is the source of truth for vision and architecture
- Should not need frequent updates — only when the model changes

### 4. README.md (root)
**Update when:** A user-facing feature is complete and usable.
- Feature list / capabilities section
- Status table (if present)
- Quickstart commands (if CLI surface changes)

### 5. docs/plans/2026-02-18-clawdapus-architecture.md
**Update when:** A phase completes or a design decision supersedes what's written.
- Mark phases as DONE
- Update the decisions section if settled patterns change

### 6. Example pod files (`examples/*/claw-pod.yml`, `Clawfile`, etc.)
**Update when:** New directives are available, existing directive syntax changes, or the example should demonstrate new capabilities.
- Keep examples current with implemented features
- Examples are also integration test fixtures — keep them buildable

### 7. Example env files (`examples/*/.env.example`)
**Update when:** A new env var is required (webhook URLs, credentials, etc.).
- Add placeholder entries with descriptive comments

### 8. Planning files (`docs/plans/`)
**Lifecycle:**
- Create a plan before starting a phase/slice
- Track progress in `phase2-progress.md` (or create a new tracker for future phases)
- **Delete the plan file when the phase is fully implemented and verified** (don't leave stale docs)
- Plans for PENDING/INCOMPLETE work stay until done

### 9. ADRs (`docs/decisions/`)
**Update when:** A significant architectural decision is made that warrants a formal record.
- Create a new `NNN-title.md` with status, context, decision, rationale, consequences
- Mark existing ADRs as superseded if a decision changes

### 10. Review docs (`docs/reviews/`)
**Lifecycle:**
- Create when a review identifies issues
- Mark findings as resolved in the doc as fixes land
- **Delete when all findings are closed**

---

## What "complete" means for a planning file

A planning file can be deleted when:
1. All tasks in it are marked DONE
2. The feature is verified (`go test ./...` passes, spike test passes)
3. The decisions are captured in `CLAUDE.md` and/or `phase2-progress.md`

---

## Current planning file status

| File | Status | Action |
|------|--------|--------|
| `2026-02-18-clawdapus-architecture.md` | Reference — all phases listed | Keep; update phase statuses |
| `phase2-progress.md` | Active tracker | Keep; update as phases complete |
