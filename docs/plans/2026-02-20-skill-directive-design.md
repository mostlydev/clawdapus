# SKILL Directive Design

**Date:** 2026-02-20
**Status:** Approved
**Prerequisite for:** Phase 3 Slice 2 (service surfaces + skill generation)

## Summary

The SKILL directive lets operators mount skill files into a Claw's runner skill directory. Skills are markdown files that provide on-demand reference material — usage guides, API docs, workflow instructions — that agents look up through the runner's standard skill mechanism.

## Two Sources of Skills

1. **Explicit** — operator provides skill files via `SKILL` in Clawfile or `skills:` in x-claw
2. **Generated** — Slice 2/3 will generate skill files for service and channel surfaces (future, but this lays the foundation)

## Clawfile Syntax

```dockerfile
SKILL ./skills/custom-workflow.md
SKILL ./skills/team-conventions.md
```

Compiles to labels: `LABEL claw.skill.0="./skills/custom-workflow.md"`. Files are not baked into the image — resolved and mounted at runtime (same pattern as AGENT).

## claw-pod.yml Syntax

```yaml
x-claw:
  skills:
    - ./skills/custom-workflow.md
    - ./skills/team-conventions.md
```

Pod-level skills are additive to image-level skills. If same basename appears in both, pod-level wins (same override pattern as agent).

## Individual File Mounts (Not Directory Mounts)

Each skill is mounted as an individual file, not a directory mount. The runner's skill directory is a working directory where the agent reads and writes its own skills. A directory bind-mount would mask the agent's own files.

```
./skills/custom-workflow.md:/claw/skills/custom-workflow.md:ro
./skills/team-conventions.md:/claw/skills/team-conventions.md:ro
```

Operator-injected files are read-only overlays. The agent can see them but not modify them.

## Data Flow

```
Clawfile SKILL → claw.skill.N labels → image inspect → ResolvedClaw.Skills
claw-pod.yml skills: → pod parser → ClawBlock.Skills → ResolvedClaw.Skills (merged)
```

Both sources merge into `ResolvedClaw.Skills []ResolvedSkill` with `Name` (basename) and `HostPath` (resolved absolute path).

## Driver Integration

`MaterializeResult` gets a `SkillDir string` field. The driver sets it per runner type (OpenClaw: `"/claw/skills"`). `compose_up.go` uses it to build per-file mount paths. If `SkillDir` is empty, skill mounts are skipped.

Skill mounting is universal — `compose_up.go` handles it, not the driver. The driver only declares where skills go.

## CLAWDAPUS.md Integration

`GenerateClawdapusMD` lists all skills (explicit + surface-generated) in the Skills section. The agent sees the full inventory.

## Error Handling

- Missing skill file at compose_up time → hard error, pod doesn't start
- Duplicate basenames → hard error
- Path traversal → hard error
- Empty skills list → fine, no skill mounts
- Driver returns empty SkillDir → skills silently skipped

## Not Building Yet

- `claw skillmap` CLI command (future)
- Surface-generated skills (Slice 2/3)
- Skill content validation
