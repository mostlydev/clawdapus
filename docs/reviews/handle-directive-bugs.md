# Handle Directive / Social Topology Review Notes

Scope reviewed: Phase 3.5 Handle directive implementation excluding deferred Task 7–12 items.

## Findings

### 1) Inconsistent handle-platform casing between Clawfile and x-claw handles
- **Location:** `internal/pod/parser.go:109` (in `parseHandles` loop)
- **Issue:** `x-claw` platform keys are not normalized (lowercased/trimming) when parsed.
- **Impact:** Platform keys from `handles:` may be case-sensitive and diverge from `HANDLE <platform>` behavior (which lowercases), causing env var generation/platform enablement mismatches.
- **Suggested fix:** normalize platform keys in `parseHandles` with `strings.TrimSpace` + `strings.ToLower` (or reject invalid/empty values).

### 2) Duplicate HANDLE directives generate duplicate `claw.handle.*` labels
- **Location:** `internal/clawfile/parser.go:102-108` + `internal/clawfile/emit.go:67`
- **Issue:** `HANDLE discord` can be repeated and current pipeline preserves duplicates, producing repeated labels in emitted Dockerfile.
- **Impact:** Redundant metadata; unstable/needless label duplication and potential merge ambiguity for downstream consumers.
- **Suggested fix:** deduplicate in parser, or fail on duplicate platform in `HANDLE` directives.

### 3) Handle label parsing ignores label value semantics
- **Location:** `internal/inspect/inspect.go:65-67`
- **Issue:** Any `claw.handle.<platform>` key is treated as enabled regardless of value.
- **Impact:** Invalid/falsey values are incorrectly interpreted as enabled handles.
- **Suggested fix:** accept only expected truthy values (e.g., `true` or `1`) or validate explicit `true`.

## Notes
- Not addressed per request: Task 7–12 items from the plan (env broadcast, compose injection, OpenClaw handle platform config, CLAWDAPUS handle section, handle skill generation, openclaw example, verification).

## Status update
- Re-checked all three findings in current working tree. They appear addressed in code:
  - `internal/pod/parser.go` now normalizes and validates `x-claw` handle platform keys.
  - `internal/clawfile/parser.go` now rejects duplicate `HANDLE` directives.
  - `internal/inspect/inspect.go` now requires handle labels to be `true` or `1`.
