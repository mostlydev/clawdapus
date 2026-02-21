# Phase 2: Runtime Driver + Pod Orchestration — Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** `claw compose up` launches an OpenClaw pod from `claw-pod.yml` with driver-enforced config injection, contract verification, and security-hardened compose output.

**Architecture:** Parse `claw-pod.yml` with `x-claw` extensions. For each Claw service, inspect the image for `claw.*` labels, resolve a driver by `CLAW_TYPE`, run driver validation/materialization, emit `compose.generated.yml` with read-only root filesystem and bounded restart policy, shell to `docker compose`, then run post-apply verification. Fail-closed at every gate.

**Tech Stack:** Go 1.23, Cobra CLI, `gopkg.in/yaml.v3`, Docker SDK (read-only), `encoding/json` for config emission, `docker compose` for lifecycle.

**Key reference docs:**
- `docs/plans/2026-02-18-clawdapus-architecture.md` — Phase 2 architecture (driver framework, pod runtime, volume surfaces)
- `docs/plans/2026-02-20-vertical-spike-clawfile-build.md` — Spike 1 completion + Phase 2 outline + retrospective
- `gemini-notes.md` — Gemini's implementation guidance (JSON5, stderr, schema)

---

## Design Decisions (read before implementing)

### JSON5 vs JSON for config generation
OpenClaw reads JSON5 config. JSON is valid JSON5. For Phase 2, we GENERATE configs from scratch (not patching user configs), so `encoding/json` is sufficient. Add a JSON5 parser later only if we need to read existing OpenClaw configs. YAGNI.

### CONFIGURE directives at runtime
CONFIGURE directives (e.g., `openclaw config set agents.defaults.heartbeat.every 30m`) are currently emitted as a build-time shell script (`configure.sh`). The runtime driver also needs these values to generate mounted config. **Solution:** Add `claw.configure.*` labels in the emitter so the driver can read them from image metadata. The build-time script remains as a fallback for plain `docker run` (non-claw-compose usage).

### Read-only filesystem guard
Every Claw service gets `read_only: true` in generated compose. This prevents binary swaps on container restart. The driver declares required `tmpfs` paths (e.g., `/tmp`) for writable scratch space. Combined with bounded `restart: on-failure`, this prevents both filesystem mutation and infinite crash loops.

### Image references
- **Clawfile builds:** `FROM node:24-bookworm-slim` + `npm install -g openclaw@2026.2.9` (version-pinned at build time). This is the production path.
- **Integration tests:** Use locally-built images from `claw build`. No external image registry dependency for tests.

---

## Task 1: Add CONFIGURE Labels to Emitter

**Files:**
- Modify: `internal/clawfile/emit.go`
- Modify: `internal/clawfile/emit_test.go`

**Why:** Runtime driver needs to read CONFIGURE directives from image labels. Currently they're only emitted as a shell script.

**Step 1: Write the failing test**

Add to `internal/clawfile/emit_test.go` inside `TestEmitProducesValidDockerfile`:

```go
// Verify CONFIGURE labels are emitted
if !strings.Contains(output, `LABEL claw.configure.0=`) {
    t.Error("expected claw.configure.0 label")
}
if !strings.Contains(output, `LABEL claw.configure.1=`) {
    t.Error("expected claw.configure.1 label")
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./internal/clawfile/ -run TestEmitProducesValidDockerfile -v`
Expected: FAIL — configure labels not emitted yet.

**Step 3: Add CONFIGURE labels to `buildLabelLines`**

In `internal/clawfile/emit.go`, add after the TRACK label loop in `buildLabelLines`:

```go
for i, configure := range config.Configures {
    lines = append(lines, formatLabel(fmt.Sprintf("claw.configure.%d", i), configure))
}
```

**Step 4: Run tests to verify they pass**

Run: `go test ./internal/clawfile/ -v`
Expected: ALL PASS (including determinism test — configures are ordered, so deterministic).

**Step 5: Update inspect to parse configure labels**

In `internal/inspect/inspect.go`, add a `Configures []string` field to `ClawInfo`:

```go
type ClawInfo struct {
    // ... existing fields ...
    Configures []string
}
```

In `ParseLabels`, after the surface parsing block, add indexed label extraction for `claw.configure.*` (same pattern as surfaces — extract index, sort by index):

```go
type indexedConfigure struct {
    index int
    value string
}
// ... same indexed sort pattern as surfaces ...
```

Initialize `Configures: make([]string, 0)` in `ParseLabels`.

**Step 6: Add inspect test for configure labels**

In `internal/inspect/inspect_test.go`, add configure labels to the test input map and assert they're parsed in order.

**Step 7: Run all tests**

Run: `go test ./...`
Expected: ALL PASS.

**Step 8: Rebuild and verify round-trip**

```bash
go build -o bin/claw ./cmd/claw
./bin/claw build -t claw-openclaw-example examples/openclaw
./bin/claw inspect claw-openclaw-example
```

Verify `claw.configure.*` labels appear in inspect output.

**Step 9: Commit**

```bash
git add internal/clawfile/emit.go internal/clawfile/emit_test.go internal/inspect/inspect.go internal/inspect/inspect_test.go
git commit -m "feat: emit CONFIGURE directives as image labels for runtime driver access"
```

---

## Task 2: Add yaml.v3 Dependency

**Step 1: Add dependency**

```bash
go get gopkg.in/yaml.v3
```

**Step 2: Verify**

```bash
go test ./...
```

Expected: ALL PASS, go.mod now includes `gopkg.in/yaml.v3`.

**Step 3: Commit**

```bash
git add go.mod go.sum
git commit -m "deps: add gopkg.in/yaml.v3 for pod YAML parsing"
```

---

## Task 3: Driver Types and Registry

**Files:**
- Create: `internal/driver/types.go`
- Create: `internal/driver/registry.go`
- Create: `internal/driver/types_test.go`
- Create: `internal/driver/registry_test.go`

**Step 1: Write the failing test for registry**

Create `internal/driver/registry_test.go`:

```go
package driver

import "testing"

func TestLookupUnknownTypeReturnsError(t *testing.T) {
    _, err := Lookup("nonexistent")
    if err == nil {
        t.Fatal("expected error for unknown driver type")
    }
}

func TestRegisterAndLookup(t *testing.T) {
    Register("test-driver", &stubDriver{})
    d, err := Lookup("test-driver")
    if err != nil {
        t.Fatalf("unexpected error: %v", err)
    }
    if d == nil {
        t.Fatal("expected non-nil driver")
    }
}

type stubDriver struct{}

func (s *stubDriver) Validate(rc *ResolvedClaw) error                                   { return nil }
func (s *stubDriver) Materialize(rc *ResolvedClaw, opts MaterializeOpts) (*MaterializeResult, error) { return &MaterializeResult{}, nil }
func (s *stubDriver) PostApply(rc *ResolvedClaw, opts PostApplyOpts) error               { return nil }
func (s *stubDriver) HealthProbe(ref ContainerRef) (*Health, error)                      { return &Health{OK: true}, nil }
```

**Step 2: Run test to verify it fails**

Run: `go test ./internal/driver/ -v`
Expected: FAIL — types and functions don't exist yet.

**Step 3: Implement types.go**

Create `internal/driver/types.go`:

```go
package driver

// Driver translates Clawfile intent into runner-specific enforcement.
// Fail-closed: Validate runs before compose up, PostApply runs after.
type Driver interface {
    Validate(rc *ResolvedClaw) error
    Materialize(rc *ResolvedClaw, opts MaterializeOpts) (*MaterializeResult, error)
    PostApply(rc *ResolvedClaw, opts PostApplyOpts) error
    HealthProbe(ref ContainerRef) (*Health, error)
}

// ResolvedClaw combines image-level claw labels with pod-level x-claw overrides.
type ResolvedClaw struct {
    ServiceName string
    ImageRef    string
    ClawType    string
    Agent       string            // filename from image labels (e.g., "AGENTS.md")
    AgentHostPath string          // resolved host path for bind mount
    Models      map[string]string // slot -> provider/model
    Surfaces    []ResolvedSurface
    Privileges  map[string]string
    Configures  []string          // openclaw config set commands from labels
    Count       int               // from pod x-claw (default 1)
    Environment map[string]string // from pod environment block
}

type ResolvedSurface struct {
    Scheme     string // channel, service, volume, host, egress
    Target     string // discord, fleet-master, shared-cache, etc.
    AccessMode string // read-only, read-write (for volume/host surfaces)
}

type MaterializeOpts struct {
    RuntimeDir string // host directory for generated artifacts
}

// MaterializeResult describes what the compose generator must add to the service.
type MaterializeResult struct {
    Mounts      []Mount
    Tmpfs       []string          // paths needing tmpfs (for read_only: true)
    Environment map[string]string // additional env vars
    Healthcheck *Healthcheck
    ReadOnly    bool              // default: true
    Restart     string            // default: "on-failure"
}

type Mount struct {
    HostPath      string
    ContainerPath string
    ReadOnly      bool
}

type Healthcheck struct {
    Test     []string
    Interval string
    Timeout  string
    Retries  int
}

type PostApplyOpts struct {
    ContainerID string
}

type ContainerRef struct {
    ContainerID string
    ServiceName string
}

type Health struct {
    OK     bool
    Detail string
}
```

**Step 4: Implement registry.go**

Create `internal/driver/registry.go`:

```go
package driver

import (
    "fmt"
    "sync"
)

var (
    mu       sync.RWMutex
    drivers  = make(map[string]Driver)
)

func Register(name string, d Driver) {
    mu.Lock()
    defer mu.Unlock()
    drivers[name] = d
}

func Lookup(name string) (Driver, error) {
    mu.RLock()
    defer mu.RUnlock()
    d, ok := drivers[name]
    if !ok {
        return nil, fmt.Errorf("unknown CLAW_TYPE %q: no registered driver", name)
    }
    return d, nil
}
```

**Step 5: Run tests**

Run: `go test ./internal/driver/ -v`
Expected: ALL PASS.

**Step 6: Commit**

```bash
git add internal/driver/types.go internal/driver/registry.go internal/driver/registry_test.go
git commit -m "feat: driver types and registry for CLAW_TYPE -> enforcement mapping"
```

---

## Task 4: Contract Enforcement

**Files:**
- Create: `internal/runtime/contract.go`
- Create: `internal/runtime/contract_test.go`

**Step 1: Write the failing test**

Create `internal/runtime/contract_test.go`:

```go
package runtime

import (
    "os"
    "path/filepath"
    "testing"
)

func TestResolveContractMissingFileErrors(t *testing.T) {
    _, err := ResolveContract("/nonexistent/path", "AGENTS.md")
    if err == nil {
        t.Fatal("expected error for missing agent file")
    }
}

func TestResolveContractExistingFileReturns(t *testing.T) {
    dir := t.TempDir()
    agentFile := filepath.Join(dir, "AGENTS.md")
    if err := os.WriteFile(agentFile, []byte("# Contract"), 0644); err != nil {
        t.Fatal(err)
    }

    mount, err := ResolveContract(dir, "AGENTS.md")
    if err != nil {
        t.Fatalf("unexpected error: %v", err)
    }
    if mount.HostPath != agentFile {
        t.Errorf("expected host path %q, got %q", agentFile, mount.HostPath)
    }
    if mount.ContainerPath != "/claw/AGENTS.md" {
        t.Errorf("expected container path /claw/AGENTS.md, got %q", mount.ContainerPath)
    }
    if !mount.ReadOnly {
        t.Error("expected read-only mount")
    }
}

func TestResolveContractEmptyFilenameErrors(t *testing.T) {
    _, err := ResolveContract("/some/dir", "")
    if err == nil {
        t.Fatal("expected error for empty agent filename")
    }
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./internal/runtime/ -v`
Expected: FAIL — package doesn't exist.

**Step 3: Implement contract.go**

Create `internal/runtime/contract.go`:

```go
package runtime

import (
    "fmt"
    "os"
    "path/filepath"
)

// ContractMount represents a verified agent contract bind mount.
type ContractMount struct {
    HostPath      string
    ContainerPath string
    ReadOnly      bool
}

// ResolveContract validates that the agent file exists and returns the mount spec.
// Fail-closed: missing or empty filename is a hard error.
func ResolveContract(baseDir string, agentFilename string) (*ContractMount, error) {
    if agentFilename == "" {
        return nil, fmt.Errorf("contract enforcement: AGENT filename is empty (no contract, no start)")
    }

    hostPath := filepath.Join(baseDir, agentFilename)
    if _, err := os.Stat(hostPath); err != nil {
        return nil, fmt.Errorf("contract enforcement: agent file %q not found: %w (no contract, no start)", hostPath, err)
    }

    return &ContractMount{
        HostPath:      hostPath,
        ContainerPath: filepath.Join("/claw", agentFilename),
        ReadOnly:      true,
    }, nil
}
```

**Step 4: Run tests**

Run: `go test ./internal/runtime/ -v`
Expected: ALL PASS.

**Step 5: Commit**

```bash
git add internal/runtime/contract.go internal/runtime/contract_test.go
git commit -m "feat: fail-closed contract enforcement for AGENT directive"
```

---

## Task 5: OpenClaw Config Generation

**Files:**
- Create: `internal/driver/openclaw/config.go`
- Create: `internal/driver/openclaw/config_test.go`

This generates an OpenClaw `openclaw.json` config from resolved Claw directives. Emits standard JSON (valid JSON5). No external JSON5 library needed.

**Step 1: Write the failing test**

Create `internal/driver/openclaw/config_test.go`:

```go
package openclaw

import (
    "encoding/json"
    "testing"

    "github.com/mostlydev/clawdapus/internal/driver"
)

func TestGenerateConfigSetsModelPrimary(t *testing.T) {
    rc := &driver.ResolvedClaw{
        Models: map[string]string{
            "primary": "openrouter/anthropic/claude-sonnet-4",
        },
        Configures: make([]string, 0),
    }
    data, err := GenerateConfig(rc)
    if err != nil {
        t.Fatalf("unexpected error: %v", err)
    }

    var config map[string]interface{}
    if err := json.Unmarshal(data, &config); err != nil {
        t.Fatalf("invalid JSON: %v", err)
    }

    agents := config["agents"].(map[string]interface{})
    defaults := agents["defaults"].(map[string]interface{})
    model := defaults["model"].(map[string]interface{})
    if model["primary"] != "openrouter/anthropic/claude-sonnet-4" {
        t.Errorf("expected model primary, got %v", model["primary"])
    }
}

func TestGenerateConfigAppliesConfigureDirectives(t *testing.T) {
    rc := &driver.ResolvedClaw{
        Models: make(map[string]string),
        Configures: []string{
            "openclaw config set agents.defaults.heartbeat.every 30m",
            "openclaw config set agents.defaults.heartbeat.target none",
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

    agents := config["agents"].(map[string]interface{})
    defaults := agents["defaults"].(map[string]interface{})
    heartbeat := defaults["heartbeat"].(map[string]interface{})
    if heartbeat["every"] != "30m" {
        t.Errorf("expected heartbeat.every=30m, got %v", heartbeat["every"])
    }
    if heartbeat["target"] != "none" {
        t.Errorf("expected heartbeat.target=none, got %v", heartbeat["target"])
    }
}

func TestGenerateConfigIsDeterministic(t *testing.T) {
    rc := &driver.ResolvedClaw{
        Models: map[string]string{
            "primary":   "anthropic/claude-sonnet-4",
            "fallback":  "openai/gpt-4o",
        },
        Configures: []string{
            "openclaw config set agents.defaults.heartbeat.every 30m",
        },
    }
    first, _ := GenerateConfig(rc)
    second, _ := GenerateConfig(rc)
    if string(first) != string(second) {
        t.Error("config generation is not deterministic")
    }
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./internal/driver/openclaw/ -v`
Expected: FAIL — package doesn't exist.

**Step 3: Implement config.go**

Create `internal/driver/openclaw/config.go`:

```go
package openclaw

import (
    "encoding/json"
    "fmt"
    "sort"
    "strings"

    "github.com/mostlydev/clawdapus/internal/driver"
)

// GenerateConfig builds an OpenClaw JSON config from resolved Claw directives.
// Emits standard JSON (valid JSON5). Deterministic output via sorted keys.
func GenerateConfig(rc *driver.ResolvedClaw) ([]byte, error) {
    config := make(map[string]interface{})

    // Apply MODEL directives
    for slot, model := range rc.Models {
        setPath(config, "agents.defaults.model."+slot, model)
    }

    // Apply CONFIGURE directives (parse "openclaw config set <path> <value>")
    for _, cmd := range rc.Configures {
        path, value, err := parseConfigSetCommand(cmd)
        if err != nil {
            return nil, fmt.Errorf("config generation: %w", err)
        }
        setPath(config, path, value)
    }

    return marshalSorted(config)
}

// parseConfigSetCommand extracts dotted path and value from
// "openclaw config set <dotted.path> <value>".
func parseConfigSetCommand(cmd string) (string, string, error) {
    parts := strings.Fields(cmd)
    // Expected: "openclaw" "config" "set" "<path>" "<value>"
    if len(parts) < 5 || parts[0] != "openclaw" || parts[1] != "config" || parts[2] != "set" {
        return "", "", fmt.Errorf("unrecognized CONFIGURE command: %q (expected 'openclaw config set <path> <value>')", cmd)
    }
    path := parts[3]
    value := strings.Join(parts[4:], " ")
    return path, value, nil
}

// setPath sets a nested value in a map using a dotted path.
func setPath(obj map[string]interface{}, path string, value interface{}) {
    parts := strings.Split(path, ".")
    current := obj
    for i, part := range parts {
        if i == len(parts)-1 {
            current[part] = value
            return
        }
        next, ok := current[part].(map[string]interface{})
        if !ok {
            next = make(map[string]interface{})
            current[part] = next
        }
        current = next
    }
}

// marshalSorted produces deterministic JSON with sorted keys.
func marshalSorted(v interface{}) ([]byte, error) {
    // encoding/json sorts map keys by default in Go 1.12+
    return json.MarshalIndent(v, "", "  ")
}

// sortedKeys returns alphabetically sorted keys from a map.
func sortedKeys(m map[string]interface{}) []string {
    keys := make([]string, 0, len(m))
    for k := range m {
        keys = append(keys, k)
    }
    sort.Strings(keys)
    return keys
}
```

**Step 4: Run tests**

Run: `go test ./internal/driver/openclaw/ -v`
Expected: ALL PASS.

**Step 5: Commit**

```bash
git add internal/driver/openclaw/config.go internal/driver/openclaw/config_test.go
git commit -m "feat: OpenClaw config generation from Claw directives (JSON, deterministic)"
```

---

## Task 6: OpenClaw Driver

**Files:**
- Create: `internal/driver/openclaw/driver.go`
- Create: `internal/driver/openclaw/driver_test.go`

**Step 1: Write the failing test**

Create `internal/driver/openclaw/driver_test.go`:

```go
package openclaw

import (
    "os"
    "path/filepath"
    "testing"

    "github.com/mostlydev/clawdapus/internal/driver"
)

func TestValidateMissingAgentErrors(t *testing.T) {
    d := &Driver{}
    rc := &driver.ResolvedClaw{
        ClawType:      "openclaw",
        Agent:         "AGENTS.md",
        AgentHostPath: "/nonexistent/AGENTS.md",
        Models:        make(map[string]string),
        Configures:    make([]string, 0),
    }
    if err := d.Validate(rc); err == nil {
        t.Fatal("expected error for missing agent file")
    }
}

func TestValidatePassesWithAgent(t *testing.T) {
    dir := t.TempDir()
    agentFile := filepath.Join(dir, "AGENTS.md")
    os.WriteFile(agentFile, []byte("# Contract"), 0644)

    d := &Driver{}
    rc := &driver.ResolvedClaw{
        ClawType:      "openclaw",
        Agent:         "AGENTS.md",
        AgentHostPath: agentFile,
        Models:        make(map[string]string),
        Configures:    make([]string, 0),
    }
    if err := d.Validate(rc); err != nil {
        t.Fatalf("unexpected error: %v", err)
    }
}

func TestMaterializeWritesConfigAndReturnsResult(t *testing.T) {
    dir := t.TempDir()
    agentFile := filepath.Join(dir, "AGENTS.md")
    os.WriteFile(agentFile, []byte("# Contract"), 0644)

    d := &Driver{}
    rc := &driver.ResolvedClaw{
        ClawType:      "openclaw",
        Agent:         "AGENTS.md",
        AgentHostPath: agentFile,
        Models:        map[string]string{"primary": "anthropic/claude-sonnet-4"},
        Configures:    []string{"openclaw config set agents.defaults.heartbeat.every 30m"},
    }
    result, err := d.Materialize(rc, driver.MaterializeOpts{RuntimeDir: dir})
    if err != nil {
        t.Fatalf("unexpected error: %v", err)
    }

    // Config file should exist
    configPath := filepath.Join(dir, "openclaw.json")
    if _, err := os.Stat(configPath); err != nil {
        t.Fatalf("config file not written: %v", err)
    }

    // Result should include config mount (read-only) and agent mount (read-only)
    if len(result.Mounts) < 2 {
        t.Fatalf("expected at least 2 mounts (config + agent), got %d", len(result.Mounts))
    }

    // Result should enable read-only root
    if !result.ReadOnly {
        t.Error("expected ReadOnly=true")
    }

    // Result should include tmpfs for writable paths
    if len(result.Tmpfs) == 0 {
        t.Error("expected at least one tmpfs mount")
    }

    // Result should have bounded restart policy
    if result.Restart != "on-failure" {
        t.Errorf("expected restart=on-failure, got %q", result.Restart)
    }
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./internal/driver/openclaw/ -v`
Expected: FAIL — Driver type doesn't exist.

**Step 3: Implement driver.go**

Create `internal/driver/openclaw/driver.go`:

```go
package openclaw

import (
    "fmt"
    "os"
    "path/filepath"

    "github.com/mostlydev/clawdapus/internal/driver"
)

// Driver implements the Driver interface for OpenClaw runner containers.
type Driver struct{}

func init() {
    driver.Register("openclaw", &Driver{})
}

func (d *Driver) Validate(rc *driver.ResolvedClaw) error {
    // Fail-closed: agent file must exist
    if rc.AgentHostPath == "" {
        return fmt.Errorf("openclaw driver: no agent host path specified (no contract, no start)")
    }
    if _, err := os.Stat(rc.AgentHostPath); err != nil {
        return fmt.Errorf("openclaw driver: agent file %q not found: %w (no contract, no start)", rc.AgentHostPath, err)
    }
    return nil
}

func (d *Driver) Materialize(rc *driver.ResolvedClaw, opts driver.MaterializeOpts) (*driver.MaterializeResult, error) {
    // Generate OpenClaw config
    configData, err := GenerateConfig(rc)
    if err != nil {
        return nil, fmt.Errorf("openclaw driver: config generation failed: %w", err)
    }

    // Write config to runtime directory
    configPath := filepath.Join(opts.RuntimeDir, "openclaw.json")
    if err := os.WriteFile(configPath, configData, 0644); err != nil {
        return nil, fmt.Errorf("openclaw driver: failed to write config: %w", err)
    }

    mounts := []driver.Mount{
        {
            HostPath:      configPath,
            ContainerPath: "/app/config/openclaw.json",
            ReadOnly:      true,
        },
        {
            HostPath:      rc.AgentHostPath,
            ContainerPath: "/claw/" + rc.Agent,
            ReadOnly:      true,
        },
    }

    return &driver.MaterializeResult{
        Mounts:  mounts,
        Tmpfs:   []string{"/tmp", "/app/data"},
        ReadOnly: true,
        Restart:  "on-failure",
        Healthcheck: &driver.Healthcheck{
            Test:     []string{"CMD", "openclaw", "health", "--json"},
            Interval: "30s",
            Timeout:  "10s",
            Retries:  3,
        },
        Environment: map[string]string{
            "CLAW_MANAGED": "true",
        },
    }, nil
}

func (d *Driver) PostApply(rc *driver.ResolvedClaw, opts driver.PostApplyOpts) error {
    // Phase 2: verify container is running and healthy.
    // For now, check container exists via Docker SDK (read-only).
    // Full verification (config actually loaded, agent mounted) deferred to health probe.
    if opts.ContainerID == "" {
        return fmt.Errorf("openclaw driver: post-apply check failed: no container ID")
    }
    return nil
}

func (d *Driver) HealthProbe(ref driver.ContainerRef) (*driver.Health, error) {
    // Stub: will be implemented in Task 10 (health probe with stderr separation)
    return &driver.Health{OK: true, Detail: "stub"}, nil
}
```

**Step 4: Run tests**

Run: `go test ./internal/driver/openclaw/ -v`
Expected: ALL PASS.

**Step 5: Run all tests to check no regressions**

Run: `go test ./...`
Expected: ALL PASS.

**Step 6: Commit**

```bash
git add internal/driver/openclaw/driver.go internal/driver/openclaw/driver_test.go
git commit -m "feat: OpenClaw driver with validation, materialization, and security hardening"
```

---

## Task 7: Pod Parser

**Files:**
- Create: `internal/pod/parser.go`
- Create: `internal/pod/types.go`
- Create: `internal/pod/parser_test.go`

**Step 1: Write the failing test**

Create `internal/pod/parser_test.go`:

```go
package pod

import (
    "strings"
    "testing"
)

const testPodYAML = `
x-claw:
  pod: test-fleet

services:
  coordinator:
    image: claw-openclaw-example
    x-claw:
      agent: ./AGENTS.md
      surfaces:
        - "channel://discord"
        - "service://fleet-master"
    environment:
      DISCORD_TOKEN: "${DISCORD_TOKEN}"

  worker:
    image: claw-openclaw-example
    x-claw:
      agent: ./AGENTS.md
      count: 2
      surfaces:
        - "volume://shared-cache read-write"
`

func TestParsePodExtractsPodName(t *testing.T) {
    pod, err := Parse(strings.NewReader(testPodYAML))
    if err != nil {
        t.Fatalf("unexpected error: %v", err)
    }
    if pod.Name != "test-fleet" {
        t.Errorf("expected pod name %q, got %q", "test-fleet", pod.Name)
    }
}

func TestParsePodExtractsServices(t *testing.T) {
    pod, err := Parse(strings.NewReader(testPodYAML))
    if err != nil {
        t.Fatalf("unexpected error: %v", err)
    }
    if len(pod.Services) != 2 {
        t.Fatalf("expected 2 services, got %d", len(pod.Services))
    }
}

func TestParsePodExtractsClawBlock(t *testing.T) {
    pod, err := Parse(strings.NewReader(testPodYAML))
    if err != nil {
        t.Fatalf("unexpected error: %v", err)
    }
    coord := pod.Services["coordinator"]
    if coord == nil {
        t.Fatal("expected coordinator service")
    }
    if coord.Claw == nil {
        t.Fatal("expected x-claw block on coordinator")
    }
    if coord.Claw.Agent != "./AGENTS.md" {
        t.Errorf("expected agent ./AGENTS.md, got %q", coord.Claw.Agent)
    }
    if len(coord.Claw.Surfaces) != 2 {
        t.Errorf("expected 2 surfaces, got %d", len(coord.Claw.Surfaces))
    }
}

func TestParsePodExtractsCount(t *testing.T) {
    pod, err := Parse(strings.NewReader(testPodYAML))
    if err != nil {
        t.Fatalf("unexpected error: %v", err)
    }
    worker := pod.Services["worker"]
    if worker == nil {
        t.Fatal("expected worker service")
    }
    if worker.Claw.Count != 2 {
        t.Errorf("expected count=2, got %d", worker.Claw.Count)
    }
}

func TestParsePodExtractsEnvironment(t *testing.T) {
    pod, err := Parse(strings.NewReader(testPodYAML))
    if err != nil {
        t.Fatalf("unexpected error: %v", err)
    }
    coord := pod.Services["coordinator"]
    if coord.Environment["DISCORD_TOKEN"] != "${DISCORD_TOKEN}" {
        t.Errorf("expected DISCORD_TOKEN env var")
    }
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./internal/pod/ -v`
Expected: FAIL — package doesn't exist.

**Step 3: Implement types.go**

Create `internal/pod/types.go`:

```go
package pod

// Pod represents a parsed claw-pod.yml.
type Pod struct {
    Name     string
    Services map[string]*Service
}

// Service represents a service in a claw-pod.yml.
type Service struct {
    Image       string
    Claw        *ClawBlock
    Environment map[string]string
}

// ClawBlock represents the x-claw extension on a service.
type ClawBlock struct {
    Agent    string
    Persona  string
    Cllama   string
    Count    int
    Surfaces []string
}
```

**Step 4: Implement parser.go**

Create `internal/pod/parser.go`:

```go
package pod

import (
    "fmt"
    "io"

    "gopkg.in/yaml.v3"
)

// rawPod is the YAML deserialization target.
type rawPod struct {
    XClaw    rawPodClaw            `yaml:"x-claw"`
    Services map[string]rawService `yaml:"services"`
}

type rawPodClaw struct {
    Pod    string `yaml:"pod"`
    Master string `yaml:"master"`
}

type rawService struct {
    Image       string            `yaml:"image"`
    XClaw       *rawClawBlock     `yaml:"x-claw"`
    Environment map[string]string `yaml:"environment"`
}

type rawClawBlock struct {
    Agent    string   `yaml:"agent"`
    Persona  string   `yaml:"persona"`
    Cllama   string   `yaml:"cllama"`
    Count    int      `yaml:"count"`
    Surfaces []string `yaml:"surfaces"`
}

// Parse reads a claw-pod.yml from the given reader.
func Parse(r io.Reader) (*Pod, error) {
    var raw rawPod
    decoder := yaml.NewDecoder(r)
    if err := decoder.Decode(&raw); err != nil {
        return nil, fmt.Errorf("parse claw-pod.yml: %w", err)
    }

    pod := &Pod{
        Name:     raw.XClaw.Pod,
        Services: make(map[string]*Service, len(raw.Services)),
    }

    for name, svc := range raw.Services {
        service := &Service{
            Image:       svc.Image,
            Environment: svc.Environment,
        }
        if svc.XClaw != nil {
            count := svc.XClaw.Count
            if count < 1 {
                count = 1
            }
            service.Claw = &ClawBlock{
                Agent:    svc.XClaw.Agent,
                Persona:  svc.XClaw.Persona,
                Cllama:   svc.XClaw.Cllama,
                Count:    count,
                Surfaces: svc.XClaw.Surfaces,
            }
        }
        pod.Services[name] = service
    }

    return pod, nil
}
```

**Step 5: Run tests**

Run: `go test ./internal/pod/ -v`
Expected: ALL PASS.

**Step 6: Commit**

```bash
git add internal/pod/types.go internal/pod/parser.go internal/pod/parser_test.go
git commit -m "feat: claw-pod.yml parser with x-claw extension support"
```

---

## Task 8: Compose Emitter

**Files:**
- Create: `internal/pod/compose_emit.go`
- Create: `internal/pod/compose_emit_test.go`

This generates `compose.generated.yml` from a parsed pod definition + driver materialization results. Includes security hardening: `read_only`, `tmpfs`, bounded `restart`.

**Step 1: Write the failing test**

Create `internal/pod/compose_emit_test.go`:

```go
package pod

import (
    "strings"
    "testing"

    "github.com/mostlydev/clawdapus/internal/driver"
)

func TestEmitComposeBasicService(t *testing.T) {
    pod := &Pod{
        Name: "test-pod",
        Services: map[string]*Service{
            "agent": {
                Image: "claw-example:latest",
                Claw: &ClawBlock{
                    Agent: "./AGENTS.md",
                    Count: 1,
                },
                Environment: map[string]string{"FOO": "bar"},
            },
        },
    }

    results := map[string]*driver.MaterializeResult{
        "agent": {
            ReadOnly: true,
            Restart:  "on-failure",
            Tmpfs:    []string{"/tmp"},
            Mounts: []driver.Mount{
                {HostPath: "/runtime/openclaw.json", ContainerPath: "/app/config/openclaw.json", ReadOnly: true},
                {HostPath: "/project/AGENTS.md", ContainerPath: "/claw/AGENTS.md", ReadOnly: true},
            },
            Environment: map[string]string{"CLAW_MANAGED": "true"},
            Healthcheck: &driver.Healthcheck{
                Test: []string{"CMD", "openclaw", "health", "--json"},
                Interval: "30s",
                Timeout:  "10s",
                Retries:  3,
            },
        },
    }

    output, err := EmitCompose(pod, results)
    if err != nil {
        t.Fatalf("unexpected error: %v", err)
    }

    // Check read_only
    if !strings.Contains(output, "read_only: true") {
        t.Error("expected read_only: true in output")
    }

    // Check tmpfs
    if !strings.Contains(output, "/tmp") {
        t.Error("expected /tmp tmpfs in output")
    }

    // Check restart policy
    if !strings.Contains(output, "on-failure") {
        t.Error("expected restart: on-failure in output")
    }

    // Check healthcheck
    if !strings.Contains(output, "openclaw") {
        t.Error("expected healthcheck command in output")
    }

    // Check environment merge
    if !strings.Contains(output, "CLAW_MANAGED") {
        t.Error("expected CLAW_MANAGED env var")
    }
    if !strings.Contains(output, "FOO") {
        t.Error("expected FOO env var from pod definition")
    }
}

func TestEmitComposeExpandsCount(t *testing.T) {
    pod := &Pod{
        Name: "test-pod",
        Services: map[string]*Service{
            "worker": {
                Image: "claw-example:latest",
                Claw: &ClawBlock{
                    Agent: "./AGENTS.md",
                    Count: 3,
                },
            },
        },
    }

    results := map[string]*driver.MaterializeResult{
        "worker": {
            ReadOnly: true,
            Restart:  "on-failure",
            Tmpfs:    []string{"/tmp"},
            Mounts:   make([]driver.Mount, 0),
        },
    }

    output, err := EmitCompose(pod, results)
    if err != nil {
        t.Fatalf("unexpected error: %v", err)
    }

    // Should expand to worker-0, worker-1, worker-2
    if !strings.Contains(output, "worker-0:") {
        t.Error("expected worker-0 service")
    }
    if !strings.Contains(output, "worker-1:") {
        t.Error("expected worker-1 service")
    }
    if !strings.Contains(output, "worker-2:") {
        t.Error("expected worker-2 service")
    }
    // Should NOT contain bare "worker:" (only expanded ordinals)
    lines := strings.Split(output, "\n")
    for _, line := range lines {
        trimmed := strings.TrimSpace(line)
        if trimmed == "worker:" {
            t.Error("bare 'worker:' should not appear; only ordinal-expanded names")
        }
    }
}

func TestEmitComposeVolumeSurface(t *testing.T) {
    pod := &Pod{
        Name: "test-pod",
        Services: map[string]*Service{
            "agent": {
                Image: "claw-example:latest",
                Claw: &ClawBlock{
                    Agent:    "./AGENTS.md",
                    Count:    1,
                    Surfaces: []string{"volume://shared-cache read-write"},
                },
            },
        },
    }

    results := map[string]*driver.MaterializeResult{
        "agent": {
            ReadOnly: true,
            Restart:  "on-failure",
            Mounts:   make([]driver.Mount, 0),
        },
    }

    output, err := EmitCompose(pod, results)
    if err != nil {
        t.Fatalf("unexpected error: %v", err)
    }

    // Should have top-level volumes section
    if !strings.Contains(output, "volumes:") {
        t.Error("expected volumes section")
    }
    if !strings.Contains(output, "shared-cache") {
        t.Error("expected shared-cache volume")
    }
}

func TestEmitComposeIsDeterministic(t *testing.T) {
    pod := &Pod{
        Name: "test-pod",
        Services: map[string]*Service{
            "alpha": {Image: "img:1", Claw: &ClawBlock{Agent: "A.md", Count: 1}},
            "beta":  {Image: "img:2", Claw: &ClawBlock{Agent: "B.md", Count: 2}},
        },
    }
    results := map[string]*driver.MaterializeResult{
        "alpha": {ReadOnly: true, Restart: "on-failure", Mounts: make([]driver.Mount, 0)},
        "beta":  {ReadOnly: true, Restart: "on-failure", Mounts: make([]driver.Mount, 0)},
    }

    first, _ := EmitCompose(pod, results)
    second, _ := EmitCompose(pod, results)
    if first != second {
        t.Error("compose emission is not deterministic")
    }
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./internal/pod/ -run TestEmitCompose -v`
Expected: FAIL — `EmitCompose` doesn't exist.

**Step 3: Implement compose_emit.go**

Create `internal/pod/compose_emit.go`:

```go
package pod

import (
    "fmt"
    "net/url"
    "sort"
    "strings"

    "github.com/mostlydev/clawdapus/internal/driver"
    "gopkg.in/yaml.v3"
)

// composeFile is the YAML serialization target for compose.generated.yml.
type composeFile struct {
    Services map[string]*composeService `yaml:"services"`
    Volumes  map[string]interface{}     `yaml:"volumes,omitempty"`
    Networks map[string]interface{}     `yaml:"networks,omitempty"`
}

type composeService struct {
    Image       string              `yaml:"image"`
    ReadOnly    bool                `yaml:"read_only,omitempty"`
    Tmpfs       []string            `yaml:"tmpfs,omitempty"`
    Volumes     []string            `yaml:"volumes,omitempty"`
    Environment map[string]string   `yaml:"environment,omitempty"`
    Restart     string              `yaml:"restart,omitempty"`
    Healthcheck *composeHealthcheck `yaml:"healthcheck,omitempty"`
    Labels      map[string]string   `yaml:"labels,omitempty"`
}

type composeHealthcheck struct {
    Test     []string `yaml:"test"`
    Interval string   `yaml:"interval"`
    Timeout  string   `yaml:"timeout"`
    Retries  int      `yaml:"retries"`
}

// EmitCompose generates a compose.generated.yml string from pod definition and
// driver materialization results. Output is deterministic (sorted service names).
func EmitCompose(p *Pod, results map[string]*driver.MaterializeResult) (string, error) {
    cf := &composeFile{
        Services: make(map[string]*composeService),
        Volumes:  make(map[string]interface{}),
    }

    // Sort service names for deterministic output
    serviceNames := sortedServiceNames(p.Services)

    for _, name := range serviceNames {
        svc := p.Services[name]
        result := results[name]
        if result == nil {
            result = &driver.MaterializeResult{}
        }

        count := 1
        if svc.Claw != nil && svc.Claw.Count > 0 {
            count = svc.Claw.Count
        }

        // Collect volume surfaces for this service
        var volumeMounts []string
        if svc.Claw != nil {
            for _, raw := range svc.Claw.Surfaces {
                parts := strings.Fields(raw)
                if len(parts) == 0 {
                    continue
                }
                parsed, err := url.Parse(parts[0])
                if err != nil || parsed.Scheme != "volume" {
                    continue
                }
                volName := parsed.Host
                if volName == "" {
                    volName = parsed.Opaque
                }
                accessMode := "rw"
                if len(parts) > 1 && strings.Contains(parts[1], "read-only") {
                    accessMode = "ro"
                }
                cf.Volumes[volName] = nil // top-level volume declaration
                volumeMounts = append(volumeMounts, fmt.Sprintf("%s:/mnt/%s:%s", volName, volName, accessMode))
            }
        }

        // Expand count into ordinal-named services
        for ordinal := 0; ordinal < count; ordinal++ {
            serviceName := name
            if count > 1 {
                serviceName = fmt.Sprintf("%s-%d", name, ordinal)
            }

            cs := &composeService{
                Image:    svc.Image,
                ReadOnly: result.ReadOnly,
                Restart:  result.Restart,
                Labels: map[string]string{
                    "claw.pod":     p.Name,
                    "claw.service": name,
                },
            }

            if count > 1 {
                cs.Labels["claw.ordinal"] = fmt.Sprintf("%d", ordinal)
            }

            // Tmpfs
            if len(result.Tmpfs) > 0 {
                cs.Tmpfs = make([]string, len(result.Tmpfs))
                copy(cs.Tmpfs, result.Tmpfs)
            }

            // Mounts from driver
            var mounts []string
            for _, m := range result.Mounts {
                mode := "rw"
                if m.ReadOnly {
                    mode = "ro"
                }
                mounts = append(mounts, fmt.Sprintf("%s:%s:%s", m.HostPath, m.ContainerPath, mode))
            }
            mounts = append(mounts, volumeMounts...)
            if len(mounts) > 0 {
                cs.Volumes = mounts
            }

            // Environment: merge pod env + driver env (driver wins on conflict)
            env := make(map[string]string)
            for k, v := range svc.Environment {
                env[k] = v
            }
            for k, v := range result.Environment {
                env[k] = v
            }
            if len(env) > 0 {
                cs.Environment = env
            }

            // Healthcheck
            if result.Healthcheck != nil {
                cs.Healthcheck = &composeHealthcheck{
                    Test:     result.Healthcheck.Test,
                    Interval: result.Healthcheck.Interval,
                    Timeout:  result.Healthcheck.Timeout,
                    Retries:  result.Healthcheck.Retries,
                }
            }

            cf.Services[serviceName] = cs
        }
    }

    // Remove empty volumes map
    if len(cf.Volumes) == 0 {
        cf.Volumes = nil
    }

    data, err := yaml.Marshal(cf)
    if err != nil {
        return "", fmt.Errorf("emit compose: %w", err)
    }

    return string(data), nil
}

func sortedServiceNames(services map[string]*Service) []string {
    names := make([]string, 0, len(services))
    for name := range services {
        names = append(names, name)
    }
    sort.Strings(names)
    return names
}
```

**Step 4: Run tests**

Run: `go test ./internal/pod/ -v`
Expected: ALL PASS.

**Step 5: Run all project tests**

Run: `go test ./...`
Expected: ALL PASS.

**Step 6: Commit**

```bash
git add internal/pod/compose_emit.go internal/pod/compose_emit_test.go
git commit -m "feat: compose emitter with read_only, tmpfs, ordinal scaling, volume surfaces"
```

---

## Task 9: CLI — `claw compose` Commands

**Files:**
- Create: `cmd/claw/compose.go`
- Create: `cmd/claw/compose_up.go`
- Create: `cmd/claw/compose_down.go`
- Create: `cmd/claw/compose_ps.go`
- Create: `cmd/claw/compose_logs.go`

**Step 1: Create the parent compose command**

Create `cmd/claw/compose.go`:

```go
package main

import "github.com/spf13/cobra"

var composeCmd = &cobra.Command{
    Use:   "compose",
    Short: "Pod lifecycle commands (up, down, ps, logs)",
}

func init() {
    rootCmd.AddCommand(composeCmd)
}
```

**Step 2: Create compose_up.go**

Create `cmd/claw/compose_up.go`:

```go
package main

import (
    "fmt"
    "os"
    "os/exec"
    "path/filepath"

    "github.com/spf13/cobra"

    "github.com/mostlydev/clawdapus/internal/driver"
    _ "github.com/mostlydev/clawdapus/internal/driver/openclaw" // register driver
    "github.com/mostlydev/clawdapus/internal/inspect"
    "github.com/mostlydev/clawdapus/internal/pod"
    "github.com/mostlydev/clawdapus/internal/runtime"
)

var composeUpDetach bool

var composeUpCmd = &cobra.Command{
    Use:   "up",
    Short: "Launch a Claw pod from claw-pod.yml",
    RunE: func(cmd *cobra.Command, args []string) error {
        podFile := "claw-pod.yml"
        if len(args) > 0 {
            podFile = args[0]
        }
        return runComposeUp(podFile)
    },
}

func runComposeUp(podFile string) error {
    // 1. Parse pod file
    f, err := os.Open(podFile)
    if err != nil {
        return fmt.Errorf("open pod file: %w", err)
    }
    defer f.Close()

    p, err := pod.Parse(f)
    if err != nil {
        return err
    }

    podDir, _ := filepath.Abs(filepath.Dir(podFile))
    runtimeDir := filepath.Join(podDir, ".claw-runtime")
    if err := os.MkdirAll(runtimeDir, 0755); err != nil {
        return fmt.Errorf("create runtime dir: %w", err)
    }

    results := make(map[string]*driver.MaterializeResult)

    // 2. For each Claw service: inspect, resolve, validate, materialize
    for name, svc := range p.Services {
        if svc.Claw == nil {
            continue
        }

        // Inspect image to get claw.* labels
        info, err := inspect.Inspect(svc.Image)
        if err != nil {
            return fmt.Errorf("inspect image %q for service %q: %w", svc.Image, name, err)
        }

        if info.ClawType == "" {
            return fmt.Errorf("service %q: image %q has no claw.type label", name, svc.Image)
        }

        // Resolve agent contract
        agentHostPath := ""
        agentFile := info.Agent
        if svc.Claw.Agent != "" {
            // Pod-level agent path (host-relative)
            contract, err := runtime.ResolveContract(podDir, svc.Claw.Agent)
            if err != nil {
                return fmt.Errorf("service %q: %w", name, err)
            }
            agentHostPath = contract.HostPath
        } else if agentFile != "" {
            // Agent from image labels — look in pod directory
            contract, err := runtime.ResolveContract(podDir, agentFile)
            if err != nil {
                return fmt.Errorf("service %q: %w", name, err)
            }
            agentHostPath = contract.HostPath
        }

        // Build ResolvedClaw
        rc := &driver.ResolvedClaw{
            ServiceName:   name,
            ImageRef:      svc.Image,
            ClawType:      info.ClawType,
            Agent:         agentFile,
            AgentHostPath: agentHostPath,
            Models:        info.Models,
            Configures:    info.Configures,
            Privileges:    info.Privileges,
            Count:         svc.Claw.Count,
            Environment:   svc.Environment,
        }

        // Lookup driver
        d, err := driver.Lookup(rc.ClawType)
        if err != nil {
            return fmt.Errorf("service %q: %w", name, err)
        }

        // Validate (fail-closed preflight)
        if err := d.Validate(rc); err != nil {
            return fmt.Errorf("service %q: validation failed: %w", name, err)
        }

        // Materialize
        svcRuntimeDir := filepath.Join(runtimeDir, name)
        if err := os.MkdirAll(svcRuntimeDir, 0755); err != nil {
            return fmt.Errorf("create service runtime dir: %w", err)
        }

        result, err := d.Materialize(rc, driver.MaterializeOpts{RuntimeDir: svcRuntimeDir})
        if err != nil {
            return fmt.Errorf("service %q: materialization failed: %w", name, err)
        }

        results[name] = result
        fmt.Printf("[claw] %s: validated and materialized (%s driver)\n", name, rc.ClawType)
    }

    // 3. Emit compose.generated.yml
    output, err := pod.EmitCompose(p, results)
    if err != nil {
        return err
    }

    generatedPath := filepath.Join(podDir, "compose.generated.yml")
    if err := os.WriteFile(generatedPath, []byte(output), 0644); err != nil {
        return fmt.Errorf("write compose.generated.yml: %w", err)
    }
    fmt.Printf("[claw] wrote %s\n", generatedPath)

    // 4. Shell to docker compose
    composeArgs := []string{"compose", "-f", generatedPath, "up"}
    if composeUpDetach {
        composeArgs = append(composeArgs, "-d")
    }

    dockerCmd := exec.Command("docker", composeArgs...)
    dockerCmd.Stdout = os.Stdout
    dockerCmd.Stderr = os.Stderr
    if err := dockerCmd.Run(); err != nil {
        return fmt.Errorf("docker compose up failed: %w", err)
    }

    // 5. Post-apply verification (for now, success if compose didn't error)
    fmt.Println("[claw] pod is up")
    return nil
}

func init() {
    composeUpCmd.Flags().BoolVarP(&composeUpDetach, "detach", "d", false, "Run in background")
    composeCmd.AddCommand(composeUpCmd)
}
```

**Step 3: Create compose_down.go**

Create `cmd/claw/compose_down.go`:

```go
package main

import (
    "fmt"
    "os"
    "os/exec"
    "path/filepath"

    "github.com/spf13/cobra"
)

var composeDownCmd = &cobra.Command{
    Use:   "down",
    Short: "Stop and remove a Claw pod",
    RunE: func(cmd *cobra.Command, args []string) error {
        podDir := "."
        generatedPath := filepath.Join(podDir, "compose.generated.yml")

        if _, err := os.Stat(generatedPath); err != nil {
            return fmt.Errorf("no compose.generated.yml found (run 'claw compose up' first)")
        }

        dockerCmd := exec.Command("docker", "compose", "-f", generatedPath, "down")
        dockerCmd.Stdout = os.Stdout
        dockerCmd.Stderr = os.Stderr
        return dockerCmd.Run()
    },
}

func init() {
    composeCmd.AddCommand(composeDownCmd)
}
```

**Step 4: Create compose_ps.go**

Create `cmd/claw/compose_ps.go`:

```go
package main

import (
    "fmt"
    "os"
    "os/exec"
    "path/filepath"

    "github.com/spf13/cobra"
)

var composePsCmd = &cobra.Command{
    Use:   "ps",
    Short: "Show status of Claw pod containers",
    RunE: func(cmd *cobra.Command, args []string) error {
        podDir := "."
        generatedPath := filepath.Join(podDir, "compose.generated.yml")

        if _, err := os.Stat(generatedPath); err != nil {
            return fmt.Errorf("no compose.generated.yml found (run 'claw compose up' first)")
        }

        dockerCmd := exec.Command("docker", "compose", "-f", generatedPath, "ps")
        dockerCmd.Stdout = os.Stdout
        dockerCmd.Stderr = os.Stderr
        return dockerCmd.Run()
    },
}

func init() {
    composeCmd.AddCommand(composePsCmd)
}
```

**Step 5: Create compose_logs.go**

Create `cmd/claw/compose_logs.go`:

```go
package main

import (
    "fmt"
    "os"
    "os/exec"
    "path/filepath"

    "github.com/spf13/cobra"
)

var composeLogsFollow bool

var composeLogsCmd = &cobra.Command{
    Use:   "logs [service]",
    Short: "Stream logs from a Claw pod",
    RunE: func(cmd *cobra.Command, args []string) error {
        podDir := "."
        generatedPath := filepath.Join(podDir, "compose.generated.yml")

        if _, err := os.Stat(generatedPath); err != nil {
            return fmt.Errorf("no compose.generated.yml found (run 'claw compose up' first)")
        }

        composeArgs := []string{"compose", "-f", generatedPath, "logs"}
        if composeLogsFollow {
            composeArgs = append(composeArgs, "-f")
        }
        composeArgs = append(composeArgs, args...)

        dockerCmd := exec.Command("docker", composeArgs...)
        dockerCmd.Stdout = os.Stdout
        dockerCmd.Stderr = os.Stderr
        return dockerCmd.Run()
    },
}

func init() {
    composeLogsCmd.Flags().BoolVarP(&composeLogsFollow, "follow", "f", false, "Follow log output")
    composeCmd.AddCommand(composeLogsCmd)
}
```

**Step 6: Verify compilation**

```bash
go build -o bin/claw ./cmd/claw
./bin/claw compose --help
./bin/claw compose up --help
```

Expected: Help text for compose subcommands.

**Step 7: Run all tests**

Run: `go test ./...`
Expected: ALL PASS.

**Step 8: Commit**

```bash
git add cmd/claw/compose.go cmd/claw/compose_up.go cmd/claw/compose_down.go cmd/claw/compose_ps.go cmd/claw/compose_logs.go
git commit -m "feat: claw compose up/down/ps/logs CLI commands"
```

---

## Task 10: Health Probe with Stderr Separation

**Files:**
- Create: `internal/health/openclaw.go`
- Create: `internal/health/openclaw_test.go`

The critical lesson from Gemini and the spike retrospective: `openclaw health --json` emits warnings to stderr. The health probe MUST use stdout-only parsing and find the first `{` to avoid parsing stderr bleed.

**Step 1: Write the failing test**

Create `internal/health/openclaw_test.go`:

```go
package health

import "testing"

func TestParseHealthJSONClean(t *testing.T) {
    stdout := `{"status":"ok","version":"2026.2.9"}`
    result, err := ParseHealthJSON([]byte(stdout))
    if err != nil {
        t.Fatalf("unexpected error: %v", err)
    }
    if !result.OK {
        t.Error("expected OK=true")
    }
}

func TestParseHealthJSONWithLeadingNoise(t *testing.T) {
    // Simulates stderr bleed or banner text before JSON
    stdout := `WARNING: some plugin loaded
{"status":"ok","version":"2026.2.9"}`
    result, err := ParseHealthJSON([]byte(stdout))
    if err != nil {
        t.Fatalf("unexpected error: %v", err)
    }
    if !result.OK {
        t.Error("expected OK=true despite leading noise")
    }
}

func TestParseHealthJSONUnhealthy(t *testing.T) {
    stdout := `{"status":"error","detail":"gateway unreachable"}`
    result, err := ParseHealthJSON([]byte(stdout))
    if err != nil {
        t.Fatalf("unexpected error: %v", err)
    }
    if result.OK {
        t.Error("expected OK=false for error status")
    }
    if result.Detail != "gateway unreachable" {
        t.Errorf("expected detail, got %q", result.Detail)
    }
}

func TestParseHealthJSONGarbage(t *testing.T) {
    stdout := `not json at all`
    _, err := ParseHealthJSON([]byte(stdout))
    if err == nil {
        t.Fatal("expected error for non-JSON output")
    }
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./internal/health/ -v`
Expected: FAIL — package doesn't exist.

**Step 3: Implement openclaw.go**

Create `internal/health/openclaw.go`:

```go
package health

import (
    "encoding/json"
    "fmt"
    "strings"
)

// HealthResult is the parsed output from a health probe.
type HealthResult struct {
    OK     bool
    Detail string
}

// openclawHealth is the JSON structure from `openclaw health --json`.
type openclawHealth struct {
    Status  string `json:"status"`
    Detail  string `json:"detail"`
    Version string `json:"version"`
}

// ParseHealthJSON extracts health status from stdout bytes.
// Handles leading noise by scanning for the first '{' character.
// This is critical: openclaw health --json may emit plugin warnings to
// stdout even when stderr is separated.
func ParseHealthJSON(stdout []byte) (*HealthResult, error) {
    s := string(stdout)

    // Find first JSON object boundary
    idx := strings.Index(s, "{")
    if idx < 0 {
        return nil, fmt.Errorf("health probe: no JSON object found in output")
    }

    jsonStr := s[idx:]
    var h openclawHealth
    if err := json.Unmarshal([]byte(jsonStr), &h); err != nil {
        return nil, fmt.Errorf("health probe: failed to parse JSON: %w", err)
    }

    return &HealthResult{
        OK:     h.Status == "ok",
        Detail: h.Detail,
    }, nil
}
```

**Step 4: Run tests**

Run: `go test ./internal/health/ -v`
Expected: ALL PASS.

**Step 5: Wire into OpenClaw driver HealthProbe**

Update `internal/driver/openclaw/driver.go` — replace the stub `HealthProbe`:

```go
func (d *Driver) HealthProbe(ref driver.ContainerRef) (*driver.Health, error) {
    // Use cmd.Output() (stdout only) — never CombinedOutput for JSON responses.
    // Full container exec implementation requires Docker SDK; for now, return stub
    // indicating the probe path is wired. Integration tests will exercise the real path.
    return &driver.Health{OK: true, Detail: "probe wired, awaiting container exec"}, nil
}
```

(The actual `docker exec` call for health probes requires Docker SDK container exec, which is integration-test territory. The unit-testable part — JSON parsing with noise handling — is covered above.)

**Step 6: Run all tests**

Run: `go test ./...`
Expected: ALL PASS.

**Step 7: Commit**

```bash
git add internal/health/openclaw.go internal/health/openclaw_test.go internal/driver/openclaw/driver.go
git commit -m "feat: health probe with stderr noise handling for openclaw health --json"
```

---

## Task 11: Example claw-pod.yml + Integration Smoke

**Files:**
- Create: `examples/openclaw/claw-pod.yml`
- Create: `internal/pod/integration_test.go` (behind `//go:build integration`)

**Step 1: Create example claw-pod.yml**

Create `examples/openclaw/claw-pod.yml`:

```yaml
x-claw:
  pod: openclaw-example

services:
  gateway:
    image: claw-openclaw-example
    x-claw:
      agent: ./AGENTS.md
      surfaces:
        - "channel://discord"
        - "service://fleet-master"
    environment:
      OPENCLAW_PORT: "18789"
```

**Step 2: Write integration smoke test**

Create `internal/pod/integration_test.go`:

```go
//go:build integration

package pod

import (
    "os"
    "path/filepath"
    "testing"

    "github.com/mostlydev/clawdapus/internal/driver"
    _ "github.com/mostlydev/clawdapus/internal/driver/openclaw"
    "github.com/mostlydev/clawdapus/internal/inspect"
    "github.com/mostlydev/clawdapus/internal/runtime"
)

func TestComposeUpSmoke(t *testing.T) {
    // Requires: claw build -t claw-openclaw-example examples/openclaw
    // Run: go test -tags=integration ./internal/pod/ -run TestComposeUpSmoke -v

    imageRef := "claw-openclaw-example"

    // Inspect image
    info, err := inspect.Inspect(imageRef)
    if err != nil {
        t.Skipf("image %q not available: %v (run 'claw build' first)", imageRef, err)
    }
    if info.ClawType == "" {
        t.Fatalf("image %q has no claw.type label", imageRef)
    }

    // Resolve contract
    exampleDir, _ := filepath.Abs("../../examples/openclaw")
    contract, err := runtime.ResolveContract(exampleDir, info.Agent)
    if err != nil {
        t.Fatalf("contract resolution failed: %v", err)
    }

    // Resolve driver
    d, err := driver.Lookup(info.ClawType)
    if err != nil {
        t.Fatalf("driver lookup failed: %v", err)
    }

    // Validate
    rc := &driver.ResolvedClaw{
        ServiceName:   "gateway",
        ImageRef:      imageRef,
        ClawType:      info.ClawType,
        Agent:         info.Agent,
        AgentHostPath: contract.HostPath,
        Models:        info.Models,
        Configures:    info.Configures,
        Privileges:    info.Privileges,
        Count:         1,
    }
    if err := d.Validate(rc); err != nil {
        t.Fatalf("validation failed: %v", err)
    }

    // Materialize
    runtimeDir := t.TempDir()
    result, err := d.Materialize(rc, driver.MaterializeOpts{RuntimeDir: runtimeDir})
    if err != nil {
        t.Fatalf("materialization failed: %v", err)
    }

    // Verify materialization result
    if !result.ReadOnly {
        t.Error("expected ReadOnly=true")
    }
    if result.Restart != "on-failure" {
        t.Errorf("expected restart=on-failure, got %q", result.Restart)
    }

    // Check config file was written
    configPath := filepath.Join(runtimeDir, "openclaw.json")
    if _, err := os.Stat(configPath); err != nil {
        t.Fatalf("config file not written: %v", err)
    }

    // Emit compose
    p := &Pod{
        Name: "smoke-test",
        Services: map[string]*Service{
            "gateway": {
                Image: imageRef,
                Claw: &ClawBlock{
                    Agent: "./AGENTS.md",
                    Count: 1,
                },
            },
        },
    }
    results := map[string]*driver.MaterializeResult{"gateway": result}
    output, err := EmitCompose(p, results)
    if err != nil {
        t.Fatalf("compose emission failed: %v", err)
    }

    // Verify compose output
    if output == "" {
        t.Fatal("empty compose output")
    }
    t.Logf("Generated compose:\n%s", output)
}
```

**Step 3: Run unit tests (no Docker needed)**

Run: `go test ./...`
Expected: ALL PASS (integration test skipped without tag).

**Step 4: Build and run integration smoke (Docker required)**

```bash
go build -o bin/claw ./cmd/claw
./bin/claw build -t claw-openclaw-example examples/openclaw
go test -tags=integration ./internal/pod/ -run TestComposeUpSmoke -v
```

Expected: Smoke test passes — image inspected, driver resolved, config materialized, compose emitted.

**Step 5: Test full CLI flow**

```bash
cd examples/openclaw
../../bin/claw compose up -d
../../bin/claw compose ps
../../bin/claw compose logs
../../bin/claw compose down
```

**Step 6: Commit**

```bash
git add examples/openclaw/claw-pod.yml internal/pod/integration_test.go
git commit -m "feat: example claw-pod.yml and integration smoke test for Phase 2 pipeline"
```

---

## Exit Criteria Checklist

After all tasks complete, verify:

- [ ] `go test ./...` — all unit tests pass
- [ ] `go test -tags=integration ./...` — integration tests pass with Docker
- [ ] `go build -o bin/claw ./cmd/claw` — binary compiles
- [ ] `claw compose up -d` launches pod from `claw-pod.yml`
- [ ] `compose.generated.yml` contains `read_only: true` and `tmpfs` mounts
- [ ] `compose.generated.yml` contains bounded `restart: on-failure`
- [ ] Driver enforces AGENT file existence (fail-closed)
- [ ] Generated OpenClaw config contains model and configure directive values
- [ ] Compose output is deterministic (byte-identical across runs)
- [ ] `count: N` expands to ordinal-named services (`service-0` through `service-N-1`)
- [ ] Volume surfaces appear as top-level compose volumes with access modes
- [ ] `claw compose ps` and `claw compose logs` work against running pod
- [ ] `claw compose down` cleanly stops the pod
