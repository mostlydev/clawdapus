# Phase 3 Slice: CLAWDAPUS.md Context Injection + Multi-Claw Example

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Two OpenClaw services in one pod, each with their own agent contract, sharing a volume with different access modes, and each receiving a generated `CLAWDAPUS.md` — a single bootstrapped context file describing the agent's infrastructure environment — injected into their context via the runner's context mechanism.

**Architecture:** Parse pod-level `surfaces` from `x-claw` blocks into `ResolvedSurface` structs. Pass them to the OpenClaw driver during materialization. The driver generates `CLAWDAPUS.md` — the infrastructure layer's letter to the agent. One file, one injection point, containing:

- **Surfaces** — what's available: name, type, access mode, connection details (mount path, host, port, credential env vars). For complex surfaces, references to skill files.
- **Skills index** — what skill files are available and what they describe.
- **Identity** — pod name, service name, claw type.

Skills live as separate files in `skills/` for detailed on-demand reference (later slices). CLAWDAPUS.md is the always-visible index. The driver knows how to inject it per runner type (OpenClaw: `bootstrap-extra-files` hook; other runners: their own context mechanism).

The compose emitter already handles volume mounts with correct `:ro`/`:rw` modes.

### Addendum (Service-emitted surface skills — updated direction)

Scope for this slice now also includes service self-description:

- Service containers may declare a label `claw.skill.emit` pointing to a markdown skill file inside the service image.
- On `service://<name>` resolution, Clawdapus attempts to extract that file during compose-up.
- If present, it is mounted for the consumer as `skills/surface-<name>.md`.
- If absent, Clawdapus emits a fallback skill with known hostname/ports so the claw still has a documented entry point.
- Precedence remains operator override friendly:
  1) extracted service skill for `surface-<name>.md` (default),
  2) operator `SKILL`/`x-claw.skills` for the same `surface-<name>.md` basename,
  3) generated fallback skill.

This avoids baking service-specific API docs into Clawdapus while still keeping all service access documented and discoverable.

**Tech Stack:** Go 1.23, `encoding/json`, `gopkg.in/yaml.v3`, OpenClaw `bootstrap-extra-files` hook

**Key reference files:**
- `internal/driver/types.go` — `ResolvedClaw.Surfaces` field already exists (type `[]ResolvedSurface`)
- `internal/driver/openclaw/driver.go` — `Materialize` method to modify
- `internal/driver/openclaw/config.go` — `GenerateConfig` and `setPath` helper
- `cmd/claw/compose_up.go` — populates `ResolvedClaw` but doesn't set `Surfaces` yet
- `internal/pod/compose_emit.go` — already parses volume surfaces from `x-claw` for compose mounts
- OpenClaw source: `bootstrap-extra-files` hook reads `hooks.bootstrap-extra-files.paths` from config, used to inject `CLAWDAPUS.md`

---

## Task 1: Wire surfaces from pod parser into ResolvedClaw in compose_up

**Files:**
- Modify: `cmd/claw/compose_up.go:88-99`
- Test: manual — no unit test file for `cmd/claw` compose_up (CLI integration)

Currently `compose_up.go` builds `ResolvedClaw` but never sets `Surfaces`. The pod's `x-claw.surfaces` are parsed into `svc.Claw.Surfaces []string` (e.g., `"volume://research-cache read-write"`). We need to parse these into `[]driver.ResolvedSurface` and set them on `rc`.

**Step 1: Write the failing test**

Create `internal/pod/surface_test.go`:

```go
package pod

import (
	"testing"
)

func TestParseSurfaceVolumeReadWrite(t *testing.T) {
	s, err := ParseSurface("volume://research-cache read-write")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if s.Scheme != "volume" {
		t.Errorf("expected scheme=volume, got %q", s.Scheme)
	}
	if s.Target != "research-cache" {
		t.Errorf("expected target=research-cache, got %q", s.Target)
	}
	if s.AccessMode != "read-write" {
		t.Errorf("expected access=read-write, got %q", s.AccessMode)
	}
}

func TestParseSurfaceVolumeReadOnly(t *testing.T) {
	s, err := ParseSurface("volume://research-cache read-only")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if s.AccessMode != "read-only" {
		t.Errorf("expected access=read-only, got %q", s.AccessMode)
	}
}

func TestParseSurfaceChannelNoAccess(t *testing.T) {
	s, err := ParseSurface("channel://discord")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if s.Scheme != "channel" {
		t.Errorf("expected scheme=channel, got %q", s.Scheme)
	}
	if s.Target != "discord" {
		t.Errorf("expected target=discord, got %q", s.Target)
	}
	if s.AccessMode != "" {
		t.Errorf("expected empty access mode, got %q", s.AccessMode)
	}
}

func TestParseSurfaceOpaqueURI(t *testing.T) {
	s, err := ParseSurface("volume:shared-cache read-write")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if s.Target != "shared-cache" {
		t.Errorf("expected target=shared-cache, got %q", s.Target)
	}
}
```

**Step 2: Run tests to verify they fail**

Run: `go test ./internal/pod/ -run TestParseSurface -v`
Expected: FAIL — `ParseSurface` not defined yet.

**Step 3: Implement ParseSurface**

Create `internal/pod/surface.go`:

```go
package pod

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/mostlydev/clawdapus/internal/driver"
)

// ParseSurface parses a raw surface string like "volume://research-cache read-only"
// into a ResolvedSurface.
func ParseSurface(raw string) (driver.ResolvedSurface, error) {
	parts := strings.Fields(raw)
	if len(parts) == 0 {
		return driver.ResolvedSurface{}, fmt.Errorf("empty surface declaration")
	}

	parsed, err := url.Parse(parts[0])
	if err != nil {
		return driver.ResolvedSurface{}, fmt.Errorf("invalid surface URI %q: %w", parts[0], err)
	}

	scheme := parsed.Scheme
	target := parsed.Host
	if target == "" {
		target = parsed.Opaque
	}

	if scheme == "" || target == "" {
		return driver.ResolvedSurface{}, fmt.Errorf("surface URI %q must have scheme and target", parts[0])
	}

	accessMode := ""
	if len(parts) > 1 {
		accessMode = parts[1]
	}

	return driver.ResolvedSurface{
		Scheme:     scheme,
		Target:     target,
		AccessMode: accessMode,
	}, nil
}
```

**Step 4: Run tests to verify they pass**

Run: `go test ./internal/pod/ -run TestParseSurface -v`
Expected: PASS

**Step 5: Wire into compose_up.go**

Modify `cmd/claw/compose_up.go` in the `ResolvedClaw` construction block (around line 88). After setting `Environment`, add:

```go
		// Parse pod-level surfaces into ResolvedSurface structs
		var surfaces []driver.ResolvedSurface
		if svc.Claw != nil {
			for _, raw := range svc.Claw.Surfaces {
				s, err := pod.ParseSurface(raw)
				if err != nil {
					return fmt.Errorf("service %q: %w", name, err)
				}
				surfaces = append(surfaces, s)
			}
		}
```

And set `Surfaces: surfaces` on the `rc` struct.

**Step 6: Verify build**

Run: `go build -o bin/claw ./cmd/claw`
Expected: compiles without errors.

**Step 7: Commit**

```bash
git add internal/pod/surface.go internal/pod/surface_test.go cmd/claw/compose_up.go
git commit -m "feat: parse pod surfaces into ResolvedSurface, wire into compose_up"
```

---

## Task 2: Generate CLAWDAPUS.md in OpenClaw driver

**Files:**
- Create: `internal/driver/openclaw/clawdapus_md.go`
- Create: `internal/driver/openclaw/clawdapus_md_test.go`
- Modify: `internal/driver/openclaw/driver.go:33-71` (Materialize method)

The driver receives `ResolvedClaw` (including `Surfaces`, `ServiceName`, pod name) and generates `CLAWDAPUS.md` — the infrastructure layer's context file for the agent. This is mounted read-only and injected into the agent's context via the `bootstrap-extra-files` hook.

**Step 1: Write the failing test**

Create `internal/driver/openclaw/clawdapus_md_test.go`:

```go
package openclaw

import (
	"strings"
	"testing"

	"github.com/mostlydev/clawdapus/internal/driver"
)

func TestGenerateClawdapusMD(t *testing.T) {
	rc := &driver.ResolvedClaw{
		ServiceName: "researcher",
		ClawType:    "openclaw",
		Surfaces: []driver.ResolvedSurface{
			{Scheme: "volume", Target: "research-cache", AccessMode: "read-write"},
			{Scheme: "channel", Target: "discord", AccessMode: ""},
			{Scheme: "service", Target: "fleet-master", AccessMode: ""},
		},
	}

	md := GenerateClawdapusMD(rc, "research-pod")

	if !strings.Contains(md, "# CLAWDAPUS.md") {
		t.Error("expected CLAWDAPUS.md header")
	}
	// Identity section
	if !strings.Contains(md, "research-pod") {
		t.Error("expected pod name")
	}
	if !strings.Contains(md, "researcher") {
		t.Error("expected service name")
	}
	if !strings.Contains(md, "openclaw") {
		t.Error("expected claw type")
	}
	// Surfaces
	if !strings.Contains(md, "research-cache") {
		t.Error("expected research-cache surface")
	}
	if !strings.Contains(md, "/mnt/research-cache") {
		t.Error("expected mount path for volume surface")
	}
	if !strings.Contains(md, "read-write") {
		t.Error("expected read-write access mode")
	}
	if !strings.Contains(md, "discord") {
		t.Error("expected discord channel surface")
	}
	// Service and channel surfaces reference skills
	if !strings.Contains(md, "skills/surface-fleet-master.md") {
		t.Error("expected skill reference for service surface")
	}
	if !strings.Contains(md, "skills/surface-discord.md") {
		t.Error("expected skill reference for channel surface")
	}
	// Volume surfaces should NOT reference skills
	if strings.Contains(md, "skills/surface-research-cache.md") {
		t.Error("volume surface should not have skill reference")
	}
}

func TestGenerateClawdapusMDNoSurfaces(t *testing.T) {
	rc := &driver.ResolvedClaw{
		ServiceName: "worker",
		ClawType:    "openclaw",
	}

	md := GenerateClawdapusMD(rc, "test-pod")

	if !strings.Contains(md, "# CLAWDAPUS.md") {
		t.Error("expected header")
	}
	if !strings.Contains(md, "No surfaces") {
		t.Error("expected 'No surfaces' message")
	}
	if !strings.Contains(md, "worker") {
		t.Error("expected service name in identity")
	}
}

func TestGenerateClawdapusMDVolumeReadOnly(t *testing.T) {
	rc := &driver.ResolvedClaw{
		ServiceName: "analyst",
		ClawType:    "openclaw",
		Surfaces: []driver.ResolvedSurface{
			{Scheme: "volume", Target: "shared-data", AccessMode: "read-only"},
		},
	}

	md := GenerateClawdapusMD(rc, "research-pod")

	if !strings.Contains(md, "read-only") {
		t.Error("expected read-only access mode")
	}
	if !strings.Contains(md, "/mnt/shared-data") {
		t.Error("expected mount path")
	}
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./internal/driver/openclaw/ -run TestGenerateClawdapusMD -v`
Expected: FAIL — `GenerateClawdapusMD` not defined.

**Step 3: Implement GenerateClawdapusMD**

Create `internal/driver/openclaw/clawdapus_md.go`:

```go
package openclaw

import (
	"fmt"
	"strings"

	"github.com/mostlydev/clawdapus/internal/driver"
)

// GenerateClawdapusMD builds the CLAWDAPUS.md context file — the infrastructure
// layer's letter to the agent. Contains identity, surfaces, and skill index.
// Injected into the agent's context via bootstrap-extra-files hook.
func GenerateClawdapusMD(rc *driver.ResolvedClaw, podName string) string {
	var b strings.Builder

	b.WriteString("# CLAWDAPUS.md\n\n")
	b.WriteString("This file is generated by Clawdapus. It describes your infrastructure environment.\n\n")

	// Identity
	b.WriteString("## Identity\n\n")
	b.WriteString(fmt.Sprintf("- **Pod:** %s\n", podName))
	b.WriteString(fmt.Sprintf("- **Service:** %s\n", rc.ServiceName))
	b.WriteString(fmt.Sprintf("- **Type:** %s\n", rc.ClawType))
	b.WriteString("\n")

	// Surfaces
	b.WriteString("## Surfaces\n\n")
	if len(rc.Surfaces) == 0 {
		b.WriteString("No surfaces declared for this service.\n\n")
	} else {
		for _, s := range rc.Surfaces {
			b.WriteString(fmt.Sprintf("### %s (%s)\n", s.Target, s.Scheme))
			if s.AccessMode != "" {
				b.WriteString(fmt.Sprintf("- **Access:** %s\n", s.AccessMode))
			}
			switch s.Scheme {
			case "volume":
				b.WriteString(fmt.Sprintf("- **Mount path:** /mnt/%s\n", s.Target))
			case "service":
				b.WriteString(fmt.Sprintf("- **Host:** %s\n", s.Target))
				b.WriteString(fmt.Sprintf("- **Skill:** `skills/surface-%s.md`\n", s.Target))
			case "channel":
				b.WriteString(fmt.Sprintf("- **Skill:** `skills/surface-%s.md`\n", s.Target))
			}
			b.WriteString("\n")
		}
	}

	// Skills index (placeholder for future slices — SKILL directive + surface-generated skills)
	b.WriteString("## Skills\n\n")
	var skills []string
	for _, s := range rc.Surfaces {
		if s.Scheme == "service" || s.Scheme == "channel" {
			skills = append(skills, fmt.Sprintf("- `skills/surface-%s.md` — %s %s surface", s.Target, s.Target, s.Scheme))
		}
	}
	if len(skills) == 0 {
		b.WriteString("No skills available.\n")
	} else {
		for _, sk := range skills {
			b.WriteString(sk + "\n")
		}
	}

	return b.String()
}
```

**Step 4: Run tests to verify they pass**

Run: `go test ./internal/driver/openclaw/ -run TestGenerateSurfaces -v`
Expected: PASS

**Step 5: Wire into Materialize**

Modify `internal/driver/openclaw/driver.go` Materialize method. After writing `openclaw.json` and before building the return struct, always generate CLAWDAPUS.md (it contains identity even without surfaces):

```go
	// Generate CLAWDAPUS.md — infrastructure context for the agent
	clawdapusMd := GenerateClawdapusMD(rc, rc.ServiceName) // TODO: pass pod name when available
	clawdapusPath := filepath.Join(opts.RuntimeDir, "CLAWDAPUS.md")
	if err := os.WriteFile(clawdapusPath, []byte(clawdapusMd), 0644); err != nil {
		return nil, fmt.Errorf("openclaw driver: failed to write CLAWDAPUS.md: %w", err)
	}

	mounts = append(mounts, driver.Mount{
		HostPath:      clawdapusPath,
		ContainerPath: "/claw/CLAWDAPUS.md",
		ReadOnly:      true,
	})
```

> **Note on mount path vs hook path:** The file is mounted at `/claw/CLAWDAPUS.md`. The hook config uses the relative path `CLAWDAPUS.md`. This works because AGENTS.md is already mounted at `/claw/AGENTS.md` and loaded by OpenClaw's workspace bootstrap from the same `/claw` directory. The `bootstrap-extra-files` hook resolves paths relative to the workspace dir, which is `/claw`.

**Step 6: Run all tests**

Run: `go test ./internal/driver/openclaw/ -v`
Expected: PASS (existing tests unaffected — they don't set Surfaces)

**Step 7: Commit**

```bash
git add internal/driver/openclaw/clawdapus_md.go internal/driver/openclaw/clawdapus_md_test.go internal/driver/openclaw/driver.go
git commit -m "feat: generate CLAWDAPUS.md in openclaw driver with identity and surfaces"
```

---

## Task 3: Add bootstrap-extra-files hook to OpenClaw config

**Files:**
- Modify: `internal/driver/openclaw/config.go:13-31` (GenerateConfig)
- Modify: `internal/driver/openclaw/config_test.go`

`GenerateConfig` must always add the `hooks.bootstrap-extra-files` config so OpenClaw automatically injects CLAWDAPUS.md into the agent's context. This is always enabled (CLAWDAPUS.md contains identity info even without surfaces).

**Step 1: Write the failing test**

Add to `internal/driver/openclaw/config_test.go`:

```go
func TestGenerateConfigAlwaysAddsBootstrapHook(t *testing.T) {
	rc := &driver.ResolvedClaw{
		Models:     map[string]string{"primary": "test/model"},
		Configures: []string{},
	}

	data, err := GenerateConfig(rc)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var config map[string]interface{}
	if err := json.Unmarshal(data, &config); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	hooks, ok := config["hooks"].(map[string]interface{})
	if !ok {
		t.Fatal("expected hooks key in config")
	}
	bef, ok := hooks["bootstrap-extra-files"].(map[string]interface{})
	if !ok {
		t.Fatal("expected hooks.bootstrap-extra-files in config")
	}
	if bef["enabled"] != true {
		t.Error("expected enabled=true")
	}
	paths, ok := bef["paths"].([]interface{})
	if !ok || len(paths) == 0 {
		t.Fatal("expected paths array with CLAWDAPUS.md")
	}
	if paths[0] != "CLAWDAPUS.md" {
		t.Errorf("expected paths[0]=CLAWDAPUS.md, got %v", paths[0])
	}
}
```

**Step 2: Run tests to verify they fail**

Run: `go test ./internal/driver/openclaw/ -run TestGenerateConfigAlwaysAddsBootstrapHook -v`
Expected: FAIL

**Step 3: Implement**

Modify `GenerateConfig` in `internal/driver/openclaw/config.go`. After the CONFIGURE loop and before `json.MarshalIndent`, add:

```go
	// Always add bootstrap-extra-files hook for CLAWDAPUS.md injection
	setPath(config, "hooks.bootstrap-extra-files.enabled", true)
	setPath(config, "hooks.bootstrap-extra-files.paths", []string{"CLAWDAPUS.md"})
```

**Step 4: Run tests**

Run: `go test ./internal/driver/openclaw/ -v`
Expected: ALL PASS

**Step 5: Commit**

```bash
git add internal/driver/openclaw/config.go internal/driver/openclaw/config_test.go
git commit -m "feat: always add bootstrap-extra-files hook for CLAWDAPUS.md injection"
```

---

## Task 4: Create multi-claw example

**Files:**
- Create: `examples/multi-claw/claw-pod.yml`
- Create: `examples/multi-claw/agents/RESEARCHER.md`
- Create: `examples/multi-claw/agents/ANALYST.md`

**Step 1: Create the pod file**

Create `examples/multi-claw/claw-pod.yml`:

```yaml
x-claw:
  pod: research-pod

services:
  researcher:
    image: claw-openclaw-example
    x-claw:
      agent: ./agents/RESEARCHER.md
      surfaces:
        - "volume://research-cache read-write"

  analyst:
    image: claw-openclaw-example
    x-claw:
      agent: ./agents/ANALYST.md
      surfaces:
        - "volume://research-cache read-only"
```

**Step 2: Create the researcher agent contract**

Create `examples/multi-claw/agents/RESEARCHER.md`:

```markdown
# Researcher Agent Contract

You are a research agent. Your job is to gather and organize information.

## Workspace

Check /claw/CLAWDAPUS.md for your available surfaces and their access modes.
Write research output to the volume surface mounted at the path described there.

## Rules

- Write structured data (JSON, markdown) to the shared volume
- Organize output into dated directories
- Do not modify or delete other agents' output
```

**Step 3: Create the analyst agent contract**

Create `examples/multi-claw/agents/ANALYST.md`:

```markdown
# Analyst Agent Contract

You are an analyst agent. Your job is to read research data and produce insights.

## Workspace

Check /claw/CLAWDAPUS.md for your available surfaces and their access modes.
Read research data from the volume surface mounted at the path described there.

## Rules

- Read data from the shared volume (read-only access)
- Produce analysis summaries
- Do not attempt to write to the shared volume
```

**Step 4: Verify parse + emit works**

Prerequisite: the example image must exist. Build it first if not already built:
```bash
go build -o bin/claw ./cmd/claw
bin/claw build -t claw-openclaw-example examples/openclaw
```

Then launch:
```bash
bin/claw compose -f examples/multi-claw/claw-pod.yml up -d
```

Expected: Both services start. Check `examples/multi-claw/compose.generated.yml` for:
- Two services (researcher, analyst)
- `research-cache:/mnt/research-cache:rw` on researcher
- `research-cache:/mnt/research-cache:ro` on analyst
- Top-level `volumes: { research-cache: }` declaration
- Both on `claw-internal` network
- Both have `CLAWDAPUS.md` mount in volumes list

Then tear down:
```bash
bin/claw compose -f examples/multi-claw/claw-pod.yml down
```

**Step 5: Commit**

```bash
git add examples/multi-claw/
git commit -m "feat: add multi-claw example with researcher + analyst sharing a volume"
```

---

## Task 5: Add unit test for multi-service compose emit with shared volume

**Files:**
- Modify: `internal/pod/compose_emit_test.go`

**Step 1: Write the test**

Add to `internal/pod/compose_emit_test.go`:

```go
func TestEmitComposeMultiServiceSharedVolume(t *testing.T) {
	p := &Pod{
		Name: "research-pod",
		Services: map[string]*Service{
			"analyst": {
				Image: "claw-openclaw-example",
				Claw: &ClawBlock{
					Surfaces: []string{"volume://research-cache read-only"},
				},
			},
			"researcher": {
				Image: "claw-openclaw-example",
				Claw: &ClawBlock{
					Surfaces: []string{"volume://research-cache read-write"},
				},
			},
		},
	}

	results := map[string]*driver.MaterializeResult{
		"researcher": {
			ReadOnly: true,
			Restart:  "on-failure",
			Tmpfs:    []string{"/tmp"},
		},
		"analyst": {
			ReadOnly: true,
			Restart:  "on-failure",
			Tmpfs:    []string{"/tmp"},
		},
	}

	out, err := EmitCompose(p, results)
	if err != nil {
		t.Fatalf("EmitCompose returned error: %v", err)
	}

	// Both services present
	if !strings.Contains(out, "researcher:") {
		t.Error("expected researcher service")
	}
	if !strings.Contains(out, "analyst:") {
		t.Error("expected analyst service")
	}

	// Shared volume declared at top level — parse YAML to verify structure
	var composed map[string]interface{}
	if err := yaml.Unmarshal([]byte(out), &composed); err != nil {
		t.Fatalf("generated compose is not valid YAML: %v", err)
	}
	volumes, _ := composed["volumes"].(map[string]interface{})
	if _, ok := volumes["research-cache"]; !ok {
		t.Error("expected research-cache in top-level volumes")
	}

	// Researcher gets rw, analyst gets ro
	if !strings.Contains(out, "research-cache:/mnt/research-cache:rw") {
		t.Error("expected researcher to have rw mount")
	}
	if !strings.Contains(out, "research-cache:/mnt/research-cache:ro") {
		t.Error("expected analyst to have ro mount")
	}

	// Both on claw-internal network
	if !strings.Contains(out, "claw-internal") {
		t.Error("expected claw-internal network")
	}
}
```

**Step 2: Run test**

Run: `go test ./internal/pod/ -run TestEmitComposeMultiServiceSharedVolume -v`
Expected: PASS (compose emitter already handles this — this test confirms it)

**Step 3: Commit**

```bash
git add internal/pod/compose_emit_test.go
git commit -m "test: add multi-service shared volume compose emit test"
```

---

## Task 6: Verify and update progress

**Step 1: Run all tests**

```bash
go test ./...
go build -o bin/claw ./cmd/claw
go vet ./...
```

Expected: all pass, binary compiles, no vet warnings.

**Step 2: Run integration tests (if Docker available)**

```bash
go test -tags=integration ./internal/pod/ -v -run TestE2E
```

Expected: existing e2e tests still pass.

**Step 3: Smoke test the multi-claw example**

```bash
bin/claw compose -f examples/multi-claw/claw-pod.yml up -d
bin/claw compose -f examples/multi-claw/claw-pod.yml ps
bin/claw compose -f examples/multi-claw/claw-pod.yml health
bin/claw compose -f examples/multi-claw/claw-pod.yml down
```

Inspect `examples/multi-claw/compose.generated.yml` and verify:
- Two services with correct volume access modes
- CLAWDAPUS.md mount on both services
- `claw-internal` network with `internal: true`

Inspect generated runtime configs to verify hook injection:
```bash
cat examples/multi-claw/.claw-runtime/researcher/openclaw.json | python3 -c "import sys,json; d=json.load(sys.stdin); assert d['hooks']['bootstrap-extra-files']['paths'] == ['CLAWDAPUS.md'], 'hook missing'"
cat examples/multi-claw/.claw-runtime/analyst/openclaw.json | python3 -c "import sys,json; d=json.load(sys.stdin); assert d['hooks']['bootstrap-extra-files']['paths'] == ['CLAWDAPUS.md'], 'hook missing'"
```

Inspect generated CLAWDAPUS.md files:
```bash
cat examples/multi-claw/.claw-runtime/researcher/CLAWDAPUS.md
cat examples/multi-claw/.claw-runtime/analyst/CLAWDAPUS.md
```
Both should contain `# CLAWDAPUS.md` with identity, surfaces (`research-cache`), and skills sections.

**Step 4: Commit any remaining changes**

```bash
git add -A
git commit -m "feat: phase 3 slice — surface manifests, multi-claw example"
```

---

## Summary of Changes

| File | Action | Purpose |
|------|--------|---------|
| `internal/pod/surface.go` | Create | ParseSurface helper |
| `internal/pod/surface_test.go` | Create | Surface parsing tests |
| `cmd/claw/compose_up.go` | Modify | Wire pod surfaces into ResolvedClaw |
| `internal/driver/openclaw/clawdapus_md.go` | Create | GenerateClawdapusMD |
| `internal/driver/openclaw/clawdapus_md_test.go` | Create | CLAWDAPUS.md generation tests |
| `internal/driver/openclaw/driver.go` | Modify | Write CLAWDAPUS.md, add mount |
| `internal/driver/openclaw/config.go` | Modify | Always add bootstrap-extra-files hook |
| `internal/driver/openclaw/config_test.go` | Modify | Hook config tests |
| `internal/pod/compose_emit_test.go` | Modify | Multi-service shared volume test |
| `examples/multi-claw/claw-pod.yml` | Create | Two-service example pod |
| `examples/multi-claw/agents/RESEARCHER.md` | Create | Researcher agent contract |
| `examples/multi-claw/agents/ANALYST.md` | Create | Analyst agent contract |

## Exit Criteria

- `go test ./...` — all pass
- `go build -o bin/claw ./cmd/claw` — compiles
- Multi-claw example starts with `claw compose -f examples/multi-claw/claw-pod.yml up -d`
- Researcher has `research-cache:/mnt/research-cache:rw` mount
- Analyst has `research-cache:/mnt/research-cache:ro` mount
- Both services have `/claw/CLAWDAPUS.md:ro` mount
- Both OpenClaw configs include `hooks.bootstrap-extra-files.paths: ["CLAWDAPUS.md"]`
- Both CLAWDAPUS.md files contain identity, surfaces, and skills sections
- Both services on `claw-internal` network

---

## Codex Review Disposition

Review from Codex identified 10 items. Resolution:

| # | Issue | Disposition |
|---|-------|-------------|
| 1 | TDD order wrong in Task 1 | **Fixed** — steps reordered: test first, then implement |
| 2 | Missing `fmt` import | **Fixed** — added to ParseSurface snippet |
| 3 | CLI syntax drift (`-f` stale) | **Rejected** — `-f` is a persistent flag on `compose` cmd, syntax is correct |
| 4 | Mount vs hook path mismatch | **Clarified** — added note explaining `/claw` workspace resolution |
| 5 | Parser lacks validation | **Partially fixed** — added scheme+target non-empty check. Enum validation is YAGNI. |
| 6 | Invalid/unsafe targets | **Deferred** — volume names are Docker-controlled, not filesystem paths |
| 7 | Example reproducibility gap | **Fixed** — added prerequisite build step in Task 4 |
| 8 | Weak test assertions | **Fixed** — Task 5 now parses YAML structure instead of string counting |
| 9 | Exit criteria stale command | **Rejected** — same as #3, `-f` syntax is correct |
| 10 | No runtime config inspection | **Fixed** — added concrete JSON inspection steps in Task 6 |
