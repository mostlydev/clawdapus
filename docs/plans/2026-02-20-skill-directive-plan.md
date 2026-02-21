# SKILL Directive Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Operators can mount skill files (markdown reference material) into a Claw's runner skill directory via `SKILL` in Clawfile and `skills:` in claw-pod.yml, with each file individually bind-mounted read-only so the agent's own skill files remain writable.

**Architecture:** Skills flow through four layers: Clawfile parser (compile to labels) → image inspect (extract labels) → pod parser (merge pod-level skills) → compose_up (resolve paths, validate, mount). The driver declares where skills go via `SkillDir` on `MaterializeResult`. CLAWDAPUS.md lists all skills in its Skills section.

**Tech Stack:** Go 1.23, `github.com/moby/buildkit` (Dockerfile parser), `gopkg.in/yaml.v3`

**Design doc:** `docs/plans/2026-02-20-skill-directive-design.md`

---

### Task 1: Add SKILL to Clawfile parser

**Files:**
- Modify: `internal/clawfile/directives.go`
- Modify: `internal/clawfile/parser.go`
- Modify: `internal/clawfile/parser_test.go`

**Step 1: Add Skills field to ClawConfig**

In `internal/clawfile/directives.go`, add a `Skills []string` field to `ClawConfig` and initialize it in `NewClawConfig`:

```go
type ClawConfig struct {
	ClawType    string
	Agent       string
	Models      map[string]string
	Cllama      string
	Persona     string
	Surfaces    []Surface
	Skills      []string          // NEW
	Invocations []Invocation
	Privileges  map[string]string
	Configures  []string
	Tracks      []string
}
```

```go
func NewClawConfig() *ClawConfig {
	return &ClawConfig{
		Models:      make(map[string]string),
		Surfaces:    make([]Surface, 0),
		Skills:      make([]string, 0),  // NEW
		Invocations: make([]Invocation, 0),
		Privileges:  make(map[string]string),
		Configures:  make([]string, 0),
		Tracks:      make([]string, 0),
	}
}
```

**Step 2: Add `"skill"` to knownDirectives and parse it**

In `internal/clawfile/parser.go`, add `"skill": true` to the `knownDirectives` map.

Add a case in the switch:

```go
		case "skill":
			if len(args) < 1 {
				return nil, fmt.Errorf("line %d: SKILL requires a file path", node.StartLine)
			}
			config.Skills = append(config.Skills, args[0])
```

**Step 3: Write the test**

Add to `internal/clawfile/parser_test.go`:

```go
func TestParseExtractsSkills(t *testing.T) {
	input := `FROM alpine
CLAW_TYPE openclaw
SKILL ./skills/custom-workflow.md
SKILL ./skills/team-conventions.md
`
	result, err := Parse(strings.NewReader(input))
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Config.Skills) != 2 {
		t.Fatalf("expected 2 skills, got %d", len(result.Config.Skills))
	}
	if result.Config.Skills[0] != "./skills/custom-workflow.md" {
		t.Errorf("expected first skill path, got %q", result.Config.Skills[0])
	}
	if result.Config.Skills[1] != "./skills/team-conventions.md" {
		t.Errorf("expected second skill path, got %q", result.Config.Skills[1])
	}
}

func TestParseRejectsEmptySkill(t *testing.T) {
	_, err := Parse(strings.NewReader("FROM alpine\nCLAW_TYPE openclaw\nSKILL\n"))
	if err == nil {
		t.Fatal("expected SKILL with no argument to fail")
	}
}
```

**Step 4: Run tests**

Run: `go test ./internal/clawfile/ -v`
Expected: ALL PASS

**Step 5: Commit**

```bash
git add internal/clawfile/directives.go internal/clawfile/parser.go internal/clawfile/parser_test.go
git commit -m "feat: add SKILL directive to Clawfile parser"
```

---

### Task 2: Emit SKILL labels in Clawfile emitter

**Files:**
- Modify: `internal/clawfile/emit.go`
- Modify: `internal/clawfile/emit_test.go`
- Modify: `internal/clawfile/parser_test.go` (update testClawfile)

**Step 1: Add skill labels to buildLabelLines**

In `internal/clawfile/emit.go`, add after the tracks loop (around line 78) and before the configures loop:

```go
	for i, skill := range config.Skills {
		lines = append(lines, formatLabel(fmt.Sprintf("claw.skill.%d", i), skill))
	}
```

**Step 2: Update testClawfile to include SKILL directives**

In `internal/clawfile/parser_test.go`, add two SKILL lines to `testClawfile` (after the SURFACE lines):

```
SKILL ./skills/custom-workflow.md
SKILL ./skills/team-conventions.md
```

**Step 3: Update TestParseExtractsLists**

The skills count check already exists if we added skills to testClawfile. Update the existing `TestParseExtractsLists` to check `Skills`:

```go
	if len(result.Config.Skills) != 2 {
		t.Fatalf("expected 2 skills, got %d", len(result.Config.Skills))
	}
```

**Step 4: Update TestEmitProducesValidDockerfile**

In `internal/clawfile/emit_test.go`, add assertions:

```go
	if !strings.Contains(output, `LABEL claw.skill.0="./skills/custom-workflow.md"`) {
		t.Fatal("missing claw.skill.0 label")
	}
	if !strings.Contains(output, `LABEL claw.skill.1="./skills/team-conventions.md"`) {
		t.Fatal("missing claw.skill.1 label")
	}
```

And add `"SKILL "` to the raw directive leak check list.

**Step 5: Run tests**

Run: `go test ./internal/clawfile/ -v`
Expected: ALL PASS

**Step 6: Commit**

```bash
git add internal/clawfile/emit.go internal/clawfile/emit_test.go internal/clawfile/parser_test.go
git commit -m "feat: emit SKILL labels in Clawfile emitter"
```

---

### Task 3: Extract skill labels in image inspect

**Files:**
- Modify: `internal/inspect/inspect.go`
- Modify: `internal/inspect/inspect_test.go`

**Step 1: Add Skills field to ClawInfo**

In `internal/inspect/inspect.go`, add `Skills []string` to `ClawInfo` and initialize it:

```go
type ClawInfo struct {
	ClawType   string
	Agent      string
	Models     map[string]string
	Cllama     string
	Persona    string
	Surfaces   []string
	Skills     []string          // NEW
	Privileges map[string]string
	Configures []string
}
```

Initialize in `ParseLabels`:
```go
	info := &ClawInfo{
		Models:     make(map[string]string),
		Surfaces:   make([]string, 0),
		Skills:     make([]string, 0),  // NEW
		Privileges: make(map[string]string),
		Configures: make([]string, 0),
	}
```

**Step 2: Parse claw.skill.* labels**

Add a `skills` accumulator (same pattern as `surfaces` and `configures`):

```go
	skills := make([]indexedEntry, 0)
```

Add a case in the switch:

```go
		case strings.HasPrefix(key, "claw.skill."):
			index := maxInt()
			suffix := strings.TrimPrefix(key, "claw.skill.")
			if parsed, err := strconv.Atoi(suffix); err == nil {
				index = parsed
			}
			skills = append(skills, indexedEntry{
				Index: index,
				Key:   key,
				Value: value,
			})
```

Add sort + collect after the configures block:

```go
	sort.Slice(skills, func(i int, j int) bool {
		if skills[i].Index == skills[j].Index {
			return skills[i].Key < skills[j].Key
		}
		return skills[i].Index < skills[j].Index
	})

	for _, skill := range skills {
		info.Skills = append(info.Skills, skill.Value)
	}
```

**Step 3: Update the test**

In `internal/inspect/inspect_test.go`, add skill labels to `TestParseLabelsExtractsClawLabels`:

```go
		"claw.skill.0":          "./skills/custom-workflow.md",
		"claw.skill.1":          "./skills/team-conventions.md",
```

Add assertions:

```go
	if len(info.Skills) != 2 {
		t.Fatalf("expected 2 skills, got %d", len(info.Skills))
	}
	if info.Skills[0] != "./skills/custom-workflow.md" {
		t.Fatalf("expected skill[0] to be custom-workflow, got %q", info.Skills[0])
	}
	if info.Skills[1] != "./skills/team-conventions.md" {
		t.Fatalf("expected skill[1] to be team-conventions, got %q", info.Skills[1])
	}
```

**Step 4: Run tests**

Run: `go test ./internal/inspect/ -v`
Expected: ALL PASS

**Step 5: Commit**

```bash
git add internal/inspect/inspect.go internal/inspect/inspect_test.go
git commit -m "feat: extract claw.skill labels in image inspect"
```

---

### Task 4: Parse skills in pod parser

**Files:**
- Modify: `internal/pod/types.go`
- Modify: `internal/pod/parser.go`
- Modify: `internal/pod/parser_test.go`

**Step 1: Add Skills to ClawBlock and rawClawBlock**

In `internal/pod/types.go`:

```go
type ClawBlock struct {
	Agent    string
	Persona  string
	Cllama   string
	Count    int
	Surfaces []string
	Skills   []string   // NEW
}
```

In `internal/pod/parser.go`, add to `rawClawBlock`:

```go
type rawClawBlock struct {
	Agent    string   `yaml:"agent"`
	Persona  string   `yaml:"persona"`
	Cllama   string   `yaml:"cllama"`
	Count    int      `yaml:"count"`
	Surfaces []string `yaml:"surfaces"`
	Skills   []string `yaml:"skills"`   // NEW
}
```

**Step 2: Wire Skills in Parse()**

In `internal/pod/parser.go`, in the `Parse` function where the `ClawBlock` is built (around line 62), add after `Surfaces`:

```go
			skills := svc.XClaw.Skills
			if skills == nil {
				skills = make([]string, 0)
			}
```

And add `Skills: skills` to the `ClawBlock` literal.

**Step 3: Write the test**

Add to `internal/pod/parser_test.go`:

```go
const testPodWithSkillsYAML = `
x-claw:
  pod: skill-pod

services:
  worker:
    image: claw-openclaw-example
    x-claw:
      agent: ./AGENTS.md
      skills:
        - ./skills/custom-workflow.md
        - ./skills/team-conventions.md
`

func TestParsePodExtractsSkills(t *testing.T) {
	pod, err := Parse(strings.NewReader(testPodWithSkillsYAML))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	worker := pod.Services["worker"]
	if worker == nil {
		t.Fatal("expected worker service")
	}
	if len(worker.Claw.Skills) != 2 {
		t.Fatalf("expected 2 skills, got %d", len(worker.Claw.Skills))
	}
	if worker.Claw.Skills[0] != "./skills/custom-workflow.md" {
		t.Errorf("expected first skill, got %q", worker.Claw.Skills[0])
	}
}

func TestParsePodDefaultsEmptySkills(t *testing.T) {
	pod, err := Parse(strings.NewReader(testPodYAML))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	coord := pod.Services["coordinator"]
	if coord.Claw.Skills == nil {
		t.Error("expected non-nil skills slice (empty, not nil)")
	}
	if len(coord.Claw.Skills) != 0 {
		t.Errorf("expected 0 skills, got %d", len(coord.Claw.Skills))
	}
}
```

**Step 4: Run tests**

Run: `go test ./internal/pod/ -run TestParsePod -v`
Expected: ALL PASS

**Step 5: Commit**

```bash
git add internal/pod/types.go internal/pod/parser.go internal/pod/parser_test.go
git commit -m "feat: parse skills in claw-pod.yml"
```

---

### Task 5: Add ResolvedSkill type and SkillDir to driver types

**Files:**
- Modify: `internal/driver/types.go`

**Step 1: Add types**

In `internal/driver/types.go`, add `ResolvedSkill` struct and `Skills` field on `ResolvedClaw`:

```go
type ResolvedSkill struct {
	Name     string // basename (e.g., "custom-workflow.md")
	HostPath string // resolved absolute host path
}
```

Add to `ResolvedClaw`:
```go
	Skills        []ResolvedSkill
```

Add `SkillDir` to `MaterializeResult`:
```go
	SkillDir    string            // container path for skills (e.g., "/claw/skills")
```

**Step 2: Run all tests to verify no breakage**

Run: `go test ./...`
Expected: ALL PASS (no existing code uses these new fields yet)

**Step 3: Commit**

```bash
git add internal/driver/types.go
git commit -m "feat: add ResolvedSkill type and SkillDir to driver types"
```

---

### Task 6: OpenClaw driver returns SkillDir

**Files:**
- Modify: `internal/driver/openclaw/driver.go`
- Modify: `internal/driver/openclaw/clawdapus_md.go`
- Create: `internal/driver/openclaw/skill_test.go`

**Step 1: Write the test**

Create `internal/driver/openclaw/skill_test.go`:

```go
package openclaw

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/mostlydev/clawdapus/internal/driver"
)

func TestMaterializeReturnsSkillDir(t *testing.T) {
	tmpDir := t.TempDir()
	agentPath := filepath.Join(tmpDir, "AGENTS.md")
	if err := os.WriteFile(agentPath, []byte("# Test"), 0644); err != nil {
		t.Fatal(err)
	}

	d := &Driver{}
	rc := &driver.ResolvedClaw{
		ServiceName:   "test-svc",
		ClawType:      "openclaw",
		Agent:         "AGENTS.md",
		AgentHostPath: agentPath,
		Models:        map[string]string{},
		Configures:    []string{},
	}

	runtimeDir := filepath.Join(tmpDir, "runtime")
	if err := os.MkdirAll(runtimeDir, 0700); err != nil {
		t.Fatal(err)
	}

	result, err := d.Materialize(rc, driver.MaterializeOpts{RuntimeDir: runtimeDir, PodName: "test-pod"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.SkillDir != "/claw/skills" {
		t.Errorf("expected SkillDir=/claw/skills, got %q", result.SkillDir)
	}
}
```

**Step 2: Set SkillDir in Materialize**

In `internal/driver/openclaw/driver.go`, add `SkillDir` to the returned `MaterializeResult` (around line 76):

```go
	return &driver.MaterializeResult{
		Mounts:   mounts,
		Tmpfs:    []string{"/tmp", "/run", "/app/data", "/root/.openclaw"},
		ReadOnly: true,
		Restart:  "on-failure",
		SkillDir: "/claw/skills",    // NEW
		Healthcheck: &driver.Healthcheck{
			// ... existing ...
		},
		Environment: map[string]string{
			"CLAW_MANAGED": "true",
		},
	}, nil
```

**Step 3: Update CLAWDAPUS.md to list explicit skills**

In `internal/driver/openclaw/clawdapus_md.go`, update the Skills section to also list explicit skills from `rc.Skills`:

```go
	// Skills index
	b.WriteString("## Skills\n\n")
	var skillEntries []string

	// Surface-generated skills
	for _, s := range rc.Surfaces {
		if s.Scheme == "service" || s.Scheme == "channel" {
			skillEntries = append(skillEntries, fmt.Sprintf("- `skills/surface-%s.md` — %s %s surface", s.Target, s.Target, s.Scheme))
		}
	}

	// Explicit operator skills
	for _, sk := range rc.Skills {
		skillEntries = append(skillEntries, fmt.Sprintf("- `skills/%s` — operator-provided skill", sk.Name))
	}

	if len(skillEntries) == 0 {
		b.WriteString("No skills available.\n")
	} else {
		for _, entry := range skillEntries {
			b.WriteString(entry + "\n")
		}
	}
```

**Step 4: Add test for skills in CLAWDAPUS.md**

Add to `internal/driver/openclaw/clawdapus_md_test.go`:

```go
func TestGenerateClawdapusMDListsExplicitSkills(t *testing.T) {
	rc := &driver.ResolvedClaw{
		ServiceName: "worker",
		ClawType:    "openclaw",
		Skills: []driver.ResolvedSkill{
			{Name: "custom-workflow.md", HostPath: "/tmp/skills/custom-workflow.md"},
			{Name: "team-conventions.md", HostPath: "/tmp/skills/team-conventions.md"},
		},
	}

	md := GenerateClawdapusMD(rc, "test-pod")

	if !strings.Contains(md, "skills/custom-workflow.md") {
		t.Error("expected custom-workflow.md in skills section")
	}
	if !strings.Contains(md, "skills/team-conventions.md") {
		t.Error("expected team-conventions.md in skills section")
	}
	if strings.Contains(md, "No skills available") {
		t.Error("should not say no skills when explicit skills are present")
	}
}
```

**Step 5: Run tests**

Run: `go test ./internal/driver/openclaw/ -v`
Expected: ALL PASS

**Step 6: Commit**

```bash
git add internal/driver/openclaw/driver.go internal/driver/openclaw/clawdapus_md.go internal/driver/openclaw/clawdapus_md_test.go internal/driver/openclaw/skill_test.go
git commit -m "feat: OpenClaw driver returns SkillDir, CLAWDAPUS.md lists skills"
```

---

### Task 7: Skill resolution helper

**Files:**
- Create: `internal/runtime/skill.go`
- Create: `internal/runtime/skill_test.go`

This mirrors the existing `runtime.ResolveContract()` pattern — validates file exists, checks path traversal, returns resolved path.

**Step 1: Write the test**

Create `internal/runtime/skill_test.go`:

```go
package runtime

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolveSkillsBasicCase(t *testing.T) {
	tmpDir := t.TempDir()
	skillDir := filepath.Join(tmpDir, "skills")
	if err := os.MkdirAll(skillDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "workflow.md"), []byte("# Workflow"), 0644); err != nil {
		t.Fatal(err)
	}

	skills, err := ResolveSkills(tmpDir, []string{"./skills/workflow.md"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(skills) != 1 {
		t.Fatalf("expected 1 skill, got %d", len(skills))
	}
	if skills[0].Name != "workflow.md" {
		t.Errorf("expected name=workflow.md, got %q", skills[0].Name)
	}
	if !filepath.IsAbs(skills[0].HostPath) {
		t.Errorf("expected absolute host path, got %q", skills[0].HostPath)
	}
}

func TestResolveSkillsRejectsMissingFile(t *testing.T) {
	tmpDir := t.TempDir()
	_, err := ResolveSkills(tmpDir, []string{"./skills/nonexistent.md"})
	if err == nil {
		t.Fatal("expected error for missing skill file")
	}
}

func TestResolveSkillsRejectsPathTraversal(t *testing.T) {
	tmpDir := t.TempDir()
	_, err := ResolveSkills(tmpDir, []string{"../../etc/passwd"})
	if err == nil {
		t.Fatal("expected error for path traversal")
	}
	if !strings.Contains(err.Error(), "escapes") {
		t.Fatalf("expected escapes error, got: %v", err)
	}
}

func TestResolveSkillsRejectsDuplicateBasenames(t *testing.T) {
	tmpDir := t.TempDir()
	dirA := filepath.Join(tmpDir, "a")
	dirB := filepath.Join(tmpDir, "b")
	os.MkdirAll(dirA, 0755)
	os.MkdirAll(dirB, 0755)
	os.WriteFile(filepath.Join(dirA, "same.md"), []byte("# A"), 0644)
	os.WriteFile(filepath.Join(dirB, "same.md"), []byte("# B"), 0644)

	_, err := ResolveSkills(tmpDir, []string{"./a/same.md", "./b/same.md"})
	if err == nil {
		t.Fatal("expected error for duplicate basenames")
	}
	if !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("expected duplicate error, got: %v", err)
	}
}

func TestResolveSkillsEmptyList(t *testing.T) {
	tmpDir := t.TempDir()
	skills, err := ResolveSkills(tmpDir, []string{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(skills) != 0 {
		t.Errorf("expected 0 skills, got %d", len(skills))
	}
}
```

**Step 2: Run tests to verify they fail**

Run: `go test ./internal/runtime/ -run TestResolveSkill -v`
Expected: FAIL — `ResolveSkills` not defined.

**Step 3: Implement ResolveSkills**

Create `internal/runtime/skill.go`:

```go
package runtime

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/mostlydev/clawdapus/internal/driver"
)

// ResolveSkills validates that all skill files exist, checks for path traversal,
// detects duplicate basenames, and returns resolved skills. Fail-closed.
func ResolveSkills(baseDir string, paths []string) ([]driver.ResolvedSkill, error) {
	if len(paths) == 0 {
		return nil, nil
	}

	absBase, err := filepath.Abs(baseDir)
	if err != nil {
		return nil, fmt.Errorf("skill resolution: cannot resolve base dir %q: %w", baseDir, err)
	}

	seen := make(map[string]string) // basename -> original path (for error messages)
	skills := make([]driver.ResolvedSkill, 0, len(paths))

	for _, p := range paths {
		hostPath, err := filepath.Abs(filepath.Join(baseDir, p))
		if err != nil {
			return nil, fmt.Errorf("skill resolution: cannot resolve path %q: %w", p, err)
		}

		// Path traversal guard
		if !strings.HasPrefix(hostPath, absBase+string(filepath.Separator)) && hostPath != absBase {
			return nil, fmt.Errorf("skill resolution: path %q escapes base directory %q", p, baseDir)
		}

		// File existence check
		if _, err := os.Stat(hostPath); err != nil {
			return nil, fmt.Errorf("skill resolution: file %q not found: %w", hostPath, err)
		}

		// Duplicate basename check
		name := filepath.Base(hostPath)
		if prev, exists := seen[name]; exists {
			return nil, fmt.Errorf("skill resolution: duplicate basename %q (from %q and %q)", name, prev, p)
		}
		seen[name] = p

		skills = append(skills, driver.ResolvedSkill{
			Name:     name,
			HostPath: hostPath,
		})
	}

	return skills, nil
}
```

**Step 4: Run tests**

Run: `go test ./internal/runtime/ -v`
Expected: ALL PASS

**Step 5: Commit**

```bash
git add internal/runtime/skill.go internal/runtime/skill_test.go
git commit -m "feat: add skill resolution with path traversal and duplicate detection"
```

---

### Task 8: Wire skills into compose_up

**Files:**
- Modify: `cmd/claw/compose_up.go`

**Step 1: Merge image-level and pod-level skills**

In `cmd/claw/compose_up.go`, after the surface parsing loop (around line 109) and before building `rc`, add skill resolution:

```go
		// Merge skills: image-level (from labels) + pod-level (from x-claw)
		var skillPaths []string
		skillPaths = append(skillPaths, info.Skills...)
		if svc.Claw != nil {
			skillPaths = append(skillPaths, svc.Claw.Skills...)
		}
		skills, err := runtime.ResolveSkills(podDir, skillPaths)
		if err != nil {
			return fmt.Errorf("service %q: %w", name, err)
		}
```

Add `Skills: skills` to the `rc` struct literal.

**Step 2: Add skill mounts after Materialize**

After `d.Materialize()` returns (around line 142), add skill mount generation:

```go
		// Mount individual skill files into the driver's skill directory
		if result.SkillDir != "" && len(rc.Skills) > 0 {
			for _, sk := range rc.Skills {
				result.Mounts = append(result.Mounts, driver.Mount{
					HostPath:      sk.HostPath,
					ContainerPath: filepath.Join(result.SkillDir, sk.Name),
					ReadOnly:      true,
				})
			}
		}
```

**Step 3: Verify build**

Run: `go build -o bin/claw ./cmd/claw`
Expected: compiles

**Step 4: Run all tests**

Run: `go test ./...`
Expected: ALL PASS

**Step 5: Commit**

```bash
git add cmd/claw/compose_up.go
git commit -m "feat: wire skill resolution and mounting into compose_up"
```

---

### Task 9: Example and verification

**Files:**
- Modify: `examples/multi-claw/claw-pod.yml`
- Create: `examples/multi-claw/skills/research-methodology.md`

**Step 1: Create a skill file**

Create `examples/multi-claw/skills/research-methodology.md`:

```markdown
# Research Methodology

## Data Collection
- Use structured formats (JSON, markdown)
- Date-stamp all output directories
- Include source attribution

## Analysis Standards
- Cross-reference multiple sources
- Flag confidence levels (high/medium/low)
- Separate observations from conclusions
```

**Step 2: Add skills to the pod file**

In `examples/multi-claw/claw-pod.yml`, add `skills:` to the researcher service:

```yaml
services:
  researcher:
    image: claw-openclaw-example
    x-claw:
      agent: ./agents/RESEARCHER.md
      skills:
        - ./skills/research-methodology.md
      surfaces:
        - "volume://research-cache read-write"
```

**Step 3: Run all tests**

Run: `go test ./...`
Expected: ALL PASS

Run: `go build -o bin/claw ./cmd/claw`
Expected: compiles

**Step 4: Commit**

```bash
git add examples/multi-claw/
git commit -m "feat: add skill file to multi-claw example"
```

---

### Task 10: Final verification and progress update

**Step 1: Run all tests clean**

```bash
go test -count=1 ./...
go build -o bin/claw ./cmd/claw
go vet ./...
```

Expected: all pass, binary compiles, no vet warnings.

**Step 2: Update progress doc**

Add a new section to `docs/plans/phase2-progress.md` for the SKILL directive:

```markdown
## SKILL Directive

**Plan:** `docs/plans/2026-02-20-skill-directive-plan.md`
**Started:** 2026-02-20

| # | Task | Status | Commit | Notes |
|---|------|--------|--------|-------|
| 1 | SKILL in Clawfile parser | DONE | — | Parse + validation |
| 2 | SKILL label emission | DONE | — | claw.skill.N labels |
| 3 | SKILL in image inspect | DONE | — | claw.skill.* extraction |
| 4 | Skills in pod parser | DONE | — | skills: in x-claw |
| 5 | Driver types | DONE | — | ResolvedSkill, SkillDir |
| 6 | OpenClaw SkillDir + CLAWDAPUS.md | DONE | — | /claw/skills, skill index |
| 7 | Skill resolution helper | DONE | — | Path traversal, dedup |
| 8 | Wire into compose_up | DONE | — | Merge + mount |
| 9 | Example | DONE | — | research-methodology.md |
| 10 | Final verification | DONE | — | All tests pass |
```

**Step 3: Commit**

```bash
git add docs/plans/phase2-progress.md
git commit -m "docs: update progress with SKILL directive completion"
```

---

## Summary of Changes

| File | Action | Purpose |
|------|--------|---------|
| `internal/clawfile/directives.go` | Modify | Add Skills field to ClawConfig |
| `internal/clawfile/parser.go` | Modify | Parse SKILL directive |
| `internal/clawfile/parser_test.go` | Modify | SKILL parse tests + update testClawfile |
| `internal/clawfile/emit.go` | Modify | Emit claw.skill.N labels |
| `internal/clawfile/emit_test.go` | Modify | Label emission assertions |
| `internal/inspect/inspect.go` | Modify | Extract claw.skill.* labels |
| `internal/inspect/inspect_test.go` | Modify | Skill label extraction test |
| `internal/pod/types.go` | Modify | Add Skills to ClawBlock |
| `internal/pod/parser.go` | Modify | Parse skills: in x-claw |
| `internal/pod/parser_test.go` | Modify | Pod skill parsing tests |
| `internal/driver/types.go` | Modify | Add ResolvedSkill, SkillDir |
| `internal/driver/openclaw/driver.go` | Modify | Set SkillDir="/claw/skills" |
| `internal/driver/openclaw/clawdapus_md.go` | Modify | List explicit skills |
| `internal/driver/openclaw/clawdapus_md_test.go` | Modify | Skill listing test |
| `internal/driver/openclaw/skill_test.go` | Create | SkillDir test |
| `internal/runtime/skill.go` | Create | ResolveSkills helper |
| `internal/runtime/skill_test.go` | Create | Resolution tests |
| `cmd/claw/compose_up.go` | Modify | Wire skill merge + mount |
| `examples/multi-claw/claw-pod.yml` | Modify | Add skills to researcher |
| `examples/multi-claw/skills/research-methodology.md` | Create | Example skill file |
| `docs/plans/phase2-progress.md` | Modify | Progress update |

## Exit Criteria

- `go test ./...` — all pass
- `go build -o bin/claw ./cmd/claw` — compiles
- `SKILL ./path` in Clawfile → `claw.skill.0` label in built image
- `skills:` in claw-pod.yml → parsed into ClawBlock.Skills
- Image-level and pod-level skills merge in compose_up
- Missing skill file → hard error (fail-closed)
- Path traversal → hard error
- Duplicate basenames → hard error
- Skills mounted as individual files at driver's SkillDir
- CLAWDAPUS.md lists all skills in Skills section
- OpenClaw driver SkillDir = "/claw/skills"
