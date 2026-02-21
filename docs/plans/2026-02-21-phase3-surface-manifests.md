# Phase 3 Slice: Surface Manifests + Multi-Claw Example

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Two OpenClaw services in one pod, each with their own agent contract, sharing a volume with different access modes, and each receiving a generated SURFACES.md injected into their OpenClaw context via the `bootstrap-extra-files` hook.

**Architecture:** Parse pod-level `surfaces` from `x-claw` blocks into `ResolvedSurface` structs. Pass them to the OpenClaw driver during materialization. The driver generates a `SURFACES.md` markdown file describing available surfaces (name, type, access, mount path) and adds the `bootstrap-extra-files` hook to the OpenClaw config JSON so that OpenClaw automatically injects the surface descriptions into the agent's context on every session and heartbeat. The compose emitter already handles volume mounts with correct `:ro`/`:rw` modes.

**Tech Stack:** Go 1.23, `encoding/json`, `gopkg.in/yaml.v3`, OpenClaw `bootstrap-extra-files` hook

**Key reference files:**
- `internal/driver/types.go` — `ResolvedClaw.Surfaces` field already exists (type `[]ResolvedSurface`)
- `internal/driver/openclaw/driver.go` — `Materialize` method to modify
- `internal/driver/openclaw/config.go` — `GenerateConfig` and `setPath` helper
- `cmd/claw/compose_up.go` — populates `ResolvedClaw` but doesn't set `Surfaces` yet
- `internal/pod/compose_emit.go` — already parses volume surfaces from `x-claw` for compose mounts
- OpenClaw source: `bootstrap-extra-files` hook reads `hooks.bootstrap-extra-files.paths` from config

---

## Task 1: Wire surfaces from pod parser into ResolvedClaw in compose_up

**Files:**
- Modify: `cmd/claw/compose_up.go:88-99`
- Test: manual — no unit test file for `cmd/claw` compose_up (CLI integration)

Currently `compose_up.go` builds `ResolvedClaw` but never sets `Surfaces`. The pod's `x-claw.surfaces` are parsed into `svc.Claw.Surfaces []string` (e.g., `"volume://research-cache read-write"`). We need to parse these into `[]driver.ResolvedSurface` and set them on `rc`.

**Step 1: Write a helper to parse surface strings**

Create `internal/pod/surface.go`:

```go
package pod

import (
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

**Step 2: Write the failing test**

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

**Step 3: Run tests to verify they fail**

Run: `go test ./internal/pod/ -run TestParseSurface -v`
Expected: FAIL — `ParseSurface` not defined yet.

**Step 4: Implement ParseSurface**

Write `internal/pod/surface.go` with the code from Step 1.

**Step 5: Run tests to verify they pass**

Run: `go test ./internal/pod/ -run TestParseSurface -v`
Expected: PASS

**Step 6: Wire into compose_up.go**

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

**Step 7: Verify build**

Run: `go build -o bin/claw ./cmd/claw`
Expected: compiles without errors.

**Step 8: Commit**

```bash
git add internal/pod/surface.go internal/pod/surface_test.go cmd/claw/compose_up.go
git commit -m "feat: parse pod surfaces into ResolvedSurface, wire into compose_up"
```

---

## Task 2: Generate SURFACES.md in OpenClaw driver

**Files:**
- Create: `internal/driver/openclaw/surfaces.go`
- Create: `internal/driver/openclaw/surfaces_test.go`
- Modify: `internal/driver/openclaw/driver.go:33-71` (Materialize method)

The driver receives `ResolvedClaw.Surfaces` and generates a markdown file describing each surface. This file will be mounted into the OpenClaw workspace and injected into the agent's context via the `bootstrap-extra-files` hook.

**Step 1: Write the failing test**

Create `internal/driver/openclaw/surfaces_test.go`:

```go
package openclaw

import (
	"strings"
	"testing"

	"github.com/mostlydev/clawdapus/internal/driver"
)

func TestGenerateSurfacesMarkdown(t *testing.T) {
	surfaces := []driver.ResolvedSurface{
		{Scheme: "volume", Target: "research-cache", AccessMode: "read-write"},
		{Scheme: "channel", Target: "discord", AccessMode: ""},
	}

	md := GenerateSurfacesMarkdown(surfaces)

	if !strings.Contains(md, "# Available Surfaces") {
		t.Error("expected header")
	}
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
}

func TestGenerateSurfacesMarkdownEmpty(t *testing.T) {
	md := GenerateSurfacesMarkdown(nil)
	if !strings.Contains(md, "No surfaces") {
		t.Error("expected 'No surfaces' message for empty list")
	}
}

func TestGenerateSurfacesMarkdownVolumeReadOnly(t *testing.T) {
	surfaces := []driver.ResolvedSurface{
		{Scheme: "volume", Target: "shared-data", AccessMode: "read-only"},
	}

	md := GenerateSurfacesMarkdown(surfaces)

	if !strings.Contains(md, "read-only") {
		t.Error("expected read-only access mode")
	}
	if !strings.Contains(md, "/mnt/shared-data") {
		t.Error("expected mount path")
	}
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./internal/driver/openclaw/ -run TestGenerateSurfaces -v`
Expected: FAIL — `GenerateSurfacesMarkdown` not defined.

**Step 3: Implement GenerateSurfacesMarkdown**

Create `internal/driver/openclaw/surfaces.go`:

```go
package openclaw

import (
	"fmt"
	"strings"

	"github.com/mostlydev/clawdapus/internal/driver"
)

// GenerateSurfacesMarkdown builds a markdown description of available surfaces.
// This is mounted read-only and injected into the agent's context via
// OpenClaw's bootstrap-extra-files hook.
func GenerateSurfacesMarkdown(surfaces []driver.ResolvedSurface) string {
	if len(surfaces) == 0 {
		return "# Available Surfaces\n\nNo surfaces declared for this service.\n"
	}

	var b strings.Builder
	b.WriteString("# Available Surfaces\n\n")

	for _, s := range surfaces {
		b.WriteString(fmt.Sprintf("## %s (%s)\n", s.Target, s.Scheme))

		if s.AccessMode != "" {
			b.WriteString(fmt.Sprintf("- **Access:** %s\n", s.AccessMode))
		}

		if s.Scheme == "volume" {
			b.WriteString(fmt.Sprintf("- **Mount path:** /mnt/%s\n", s.Target))
		}

		b.WriteString("\n")
	}

	return b.String()
}
```

**Step 4: Run tests to verify they pass**

Run: `go test ./internal/driver/openclaw/ -run TestGenerateSurfaces -v`
Expected: PASS

**Step 5: Wire into Materialize**

Modify `internal/driver/openclaw/driver.go` Materialize method. After writing `openclaw.json` and before building the return struct:

```go
	// Generate SURFACES.md if surfaces are declared
	if len(rc.Surfaces) > 0 {
		surfacesMd := GenerateSurfacesMarkdown(rc.Surfaces)
		surfacesPath := filepath.Join(opts.RuntimeDir, "SURFACES.md")
		if err := os.WriteFile(surfacesPath, []byte(surfacesMd), 0644); err != nil {
			return nil, fmt.Errorf("openclaw driver: failed to write SURFACES.md: %w", err)
		}

		mounts = append(mounts, driver.Mount{
			HostPath:      surfacesPath,
			ContainerPath: "/claw/SURFACES.md",
			ReadOnly:      true,
		})
	}
```

**Step 6: Run all tests**

Run: `go test ./internal/driver/openclaw/ -v`
Expected: PASS (existing tests unaffected — they don't set Surfaces)

**Step 7: Commit**

```bash
git add internal/driver/openclaw/surfaces.go internal/driver/openclaw/surfaces_test.go internal/driver/openclaw/driver.go
git commit -m "feat: generate SURFACES.md in openclaw driver from resolved surfaces"
```

---

## Task 3: Add bootstrap-extra-files hook to OpenClaw config

**Files:**
- Modify: `internal/driver/openclaw/config.go:13-31` (GenerateConfig)
- Modify: `internal/driver/openclaw/config_test.go`

When surfaces are present, `GenerateConfig` must add the `hooks.bootstrap-extra-files` config so OpenClaw automatically injects SURFACES.md into the agent's context.

**Step 1: Write the failing test**

Add to `internal/driver/openclaw/config_test.go`:

```go
func TestGenerateConfigAddsSurfaceHookWhenSurfacesPresent(t *testing.T) {
	rc := &driver.ResolvedClaw{
		Models:     map[string]string{"primary": "test/model"},
		Configures: []string{},
		Surfaces: []driver.ResolvedSurface{
			{Scheme: "volume", Target: "cache", AccessMode: "read-write"},
		},
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
		t.Fatal("expected paths array with SURFACES.md")
	}
	if paths[0] != "SURFACES.md" {
		t.Errorf("expected paths[0]=SURFACES.md, got %v", paths[0])
	}
}

func TestGenerateConfigNoHookWhenNoSurfaces(t *testing.T) {
	rc := &driver.ResolvedClaw{
		Models:     map[string]string{"primary": "test/model"},
		Configures: []string{},
		Surfaces:   nil,
	}

	data, err := GenerateConfig(rc)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var config map[string]interface{}
	json.Unmarshal(data, &config)

	if _, ok := config["hooks"]; ok {
		t.Error("expected no hooks key when no surfaces declared")
	}
}
```

**Step 2: Run tests to verify they fail**

Run: `go test ./internal/driver/openclaw/ -run TestGenerateConfig.*Surface -v`
Expected: FAIL

**Step 3: Implement**

Modify `GenerateConfig` in `internal/driver/openclaw/config.go`. After the CONFIGURE loop and before `json.MarshalIndent`, add:

```go
	// Add bootstrap-extra-files hook if surfaces are declared
	if len(rc.Surfaces) > 0 {
		setPath(config, "hooks.bootstrap-extra-files.enabled", true)
		setPath(config, "hooks.bootstrap-extra-files.paths", []string{"SURFACES.md"})
	}
```

**Step 4: Run tests**

Run: `go test ./internal/driver/openclaw/ -v`
Expected: ALL PASS

**Step 5: Commit**

```bash
git add internal/driver/openclaw/config.go internal/driver/openclaw/config_test.go
git commit -m "feat: add bootstrap-extra-files hook to openclaw config when surfaces present"
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

Check /claw/SURFACES.md for your available surfaces and their access modes.
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

Check /claw/SURFACES.md for your available surfaces and their access modes.
Read research data from the volume surface mounted at the path described there.

## Rules

- Read data from the shared volume (read-only access)
- Produce analysis summaries
- Do not attempt to write to the shared volume
```

**Step 4: Verify parse + emit works**

Run:
```bash
go build -o bin/claw ./cmd/claw
bin/claw compose -f examples/multi-claw/claw-pod.yml up -d
```

Expected: Both services start. Check `examples/multi-claw/compose.generated.yml` for:
- Two services (researcher, analyst)
- `research-cache:/mnt/research-cache:rw` on researcher
- `research-cache:/mnt/research-cache:ro` on analyst
- Top-level `volumes: { research-cache: }` declaration
- Both on `claw-internal` network
- Both have `SURFACES.md` mount in volumes list

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

	// Shared volume declared once at top level
	if strings.Count(out, "research-cache:") < 1 {
		t.Error("expected research-cache volume declaration")
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
- SURFACES.md mount on both services
- `bootstrap-extra-files` hook in both runtime configs
- `claw-internal` network with `internal: true`

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
| `internal/driver/openclaw/surfaces.go` | Create | GenerateSurfacesMarkdown |
| `internal/driver/openclaw/surfaces_test.go` | Create | Surface markdown tests |
| `internal/driver/openclaw/driver.go` | Modify | Write SURFACES.md, add mount |
| `internal/driver/openclaw/config.go` | Modify | Add bootstrap-extra-files hook |
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
- Both services have `/claw/SURFACES.md:ro` mount
- Both OpenClaw configs include `hooks.bootstrap-extra-files.paths: ["SURFACES.md"]`
- Both services on `claw-internal` network
