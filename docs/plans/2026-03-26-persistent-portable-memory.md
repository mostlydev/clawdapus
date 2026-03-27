# Persistent Portable Agent Memory Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Move portable agent memory out of `.claw-runtime/` (wiped on every `claw up`) into `.claw-memory/` (a new persistent sibling), so agent notes survive redeployments and driver migrations.

**Architecture:** `preMigrateMemory` runs before `resetRuntimeDir` to copy any existing `.claw-runtime/<service>/memory/` content into `.claw-memory/<service>/memory/` (one-time auto-migration, idempotent). `compose_up` then creates `.claw-memory/<service>/` as a persistent directory and passes it as `MaterializeOpts.StateDir`. All 7 drivers already call `shared.ResolveStateDir(opts.RuntimeDir, opts.StateDir)` — they just need to pass it to `PreparePortableMemory` instead of `opts.RuntimeDir` directly.

**Tech Stack:** Go, `internal/driver/shared.MergeTree`, `shared.ResolveStateDir`, `ensurePersistentCllamaDir` (already in `compose_up.go`)

---

## Background: Why This Matters

Current layout (broken):

```
<pod-dir>/
  .claw-runtime/         ← os.RemoveAll on every claw up
    analyst/
      memory/            ← MEMORY.md, USER.md, dated notes — LOST on claw up
      config/            ← regenerated (fine)
  .claw-auth/            ← persistent (tokens)
  .claw-session-history/ ← persistent (proxy-written turn log)
```

Target layout:

```
<pod-dir>/
  .claw-runtime/         ← wiped (ephemeral artifacts only)
    analyst/
      config/
      hermes-home/       ← etc.
  .claw-memory/          ← NEW: persistent, never wiped
    analyst/
      memory/
        MEMORY.md        ← survives claw up, survives driver migration
        USER.md
  .claw-auth/
  .claw-session-history/
```

The `StateDir string` field on `MaterializeOpts` was added for exactly this purpose and is already defined but never set. This plan wires it up end-to-end.

---

## Key Helpers (read before starting)

- `internal/driver/shared/state.go:ResolveStateDir(runtimeDir, stateDir string) string` — returns `stateDir` if non-empty, else `runtimeDir`. Used in all driver changes.
- `internal/driver/shared/state.go:MergeTree(dstDir, srcDir string) error` — recursive copy, never overwrites. Used in `preMigrateMemory`.
- `cmd/claw/compose_up.go:ensurePersistentCllamaDir(podDir, name string) (string, error)` — creates `<podDir>/<name>` with `0o777`, returns absolute path. Reused for `.claw-memory/<service>`.

---

## Task 1: Write failing tests for `preMigrateMemory`

**Files:**
- Modify: `cmd/claw/compose_up_test.go`

Add these three tests. They will fail (compile error) until Task 2.

**Step 1: Add the three tests**

```go
func TestPreMigrateMemoryCopiesRuntimeMemoryToClawMemory(t *testing.T) {
	podDir := t.TempDir()

	runtimeMemDir := filepath.Join(podDir, ".claw-runtime", "analyst", "memory")
	if err := os.MkdirAll(runtimeMemDir, 0o777); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(runtimeMemDir, "MEMORY.md"), []byte("agent notes"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(runtimeMemDir, "2026-03-26.md"), []byte("dated note"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := preMigrateMemory(podDir); err != nil {
		t.Fatalf("preMigrateMemory returned error: %v", err)
	}

	dstDir := filepath.Join(podDir, ".claw-memory", "analyst", "memory")
	for name, want := range map[string]string{
		"MEMORY.md":     "agent notes",
		"2026-03-26.md": "dated note",
	} {
		data, err := os.ReadFile(filepath.Join(dstDir, name))
		if err != nil {
			t.Fatalf("read migrated %s: %v", name, err)
		}
		if string(data) != want {
			t.Fatalf("%s: expected %q, got %q", name, want, string(data))
		}
	}
}

func TestPreMigrateMemoryIsIdempotent(t *testing.T) {
	podDir := t.TempDir()

	// Pre-existing canonical memory — must not be overwritten
	dstDir := filepath.Join(podDir, ".claw-memory", "analyst", "memory")
	if err := os.MkdirAll(dstDir, 0o777); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dstDir, "MEMORY.md"), []byte("canonical"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Old runtime content
	runtimeMemDir := filepath.Join(podDir, ".claw-runtime", "analyst", "memory")
	if err := os.MkdirAll(runtimeMemDir, 0o777); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(runtimeMemDir, "MEMORY.md"), []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := preMigrateMemory(podDir); err != nil {
		t.Fatalf("preMigrateMemory returned error: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(dstDir, "MEMORY.md"))
	if err != nil {
		t.Fatalf("read canonical: %v", err)
	}
	if string(data) != "canonical" {
		t.Fatalf("expected canonical to win, got %q", string(data))
	}
}

func TestPreMigrateMemoryNoopsWhenRuntimeAbsent(t *testing.T) {
	podDir := t.TempDir() // fresh dir, no .claw-runtime
	if err := preMigrateMemory(podDir); err != nil {
		t.Fatalf("preMigrateMemory should be a no-op on first run, got: %v", err)
	}
}
```

**Step 2: Run to verify they fail**

```bash
go test ./cmd/claw/... -run TestPreMigrateMemory
```

Expected: compilation error — `preMigrateMemory` undefined.

---

## Task 2: Implement `preMigrateMemory` and wire `StateDir` in `compose_up.go`

**Files:**
- Modify: `cmd/claw/compose_up.go`

### Step 1: Add `preMigrateMemory` function

Add near `resetRuntimeDir` (around line 706). Note: `shared` is already imported.

```go
// preMigrateMemory performs a one-time migration of portable memory from the
// ephemeral runtime layout (.claw-runtime/<service>/memory/) to the persistent
// layout (.claw-memory/<service>/memory/). Must run BEFORE resetRuntimeDir.
// Uses MergeTree — never overwrites files that already exist in the destination.
// Idempotent: safe to run on every claw up.
func preMigrateMemory(podDir string) error {
	runtimeDir := filepath.Join(podDir, ".claw-runtime")
	entries, err := os.ReadDir(runtimeDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil // first ever run, nothing to migrate
		}
		return fmt.Errorf("pre-migrate memory: read runtime dir: %w", err)
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		srcMemDir := filepath.Join(runtimeDir, entry.Name(), "memory")
		if _, statErr := os.Stat(srcMemDir); statErr != nil {
			continue
		}
		dstMemDir := filepath.Join(podDir, ".claw-memory", entry.Name(), "memory")
		if err := shared.MergeTree(dstMemDir, srcMemDir); err != nil {
			return fmt.Errorf("pre-migrate memory for %q: %w", entry.Name(), err)
		}
	}
	return nil
}
```

### Step 2: Call `preMigrateMemory` before `resetRuntimeDir`

Find (around line 102):
```go
runtimeDir := filepath.Join(podDir, ".claw-runtime")
if err := resetRuntimeDir(runtimeDir); err != nil {
    return fmt.Errorf("reset runtime dir: %w", err)
}
```

Change to:
```go
runtimeDir := filepath.Join(podDir, ".claw-runtime")
if err := preMigrateMemory(podDir); err != nil {
    return fmt.Errorf("pre-migrate memory: %w", err)
}
if err := resetRuntimeDir(runtimeDir); err != nil {
    return fmt.Errorf("reset runtime dir: %w", err)
}
```

### Step 3: Declare `serviceStateDirs` map

Find the block of map declarations (around line 132, near `serviceRuntimeDirs`):
```go
serviceRuntimeDirs := make(map[string]string)
```

Add immediately after:
```go
serviceStateDirs := make(map[string]string)
```

### Step 4: Populate `serviceStateDirs` in pass 1

Find (around line 327):
```go
serviceRuntimeDirs[name] = svcRuntimeDir
```

Add immediately after:
```go
svcStateDir, err := ensurePersistentCllamaDir(podDir, filepath.Join(".claw-memory", name))
if err != nil {
    return fmt.Errorf("service %q: create memory state dir: %w", name, err)
}
serviceStateDirs[name] = svcStateDir
```

### Step 5: Pass `StateDir` in pass 2

Find the `Materialize` call (around line 590):
```go
result, err := d.Materialize(rc, driver.MaterializeOpts{RuntimeDir: svcRuntimeDir, PodName: p.Name})
```

Change to:
```go
result, err := d.Materialize(rc, driver.MaterializeOpts{
    RuntimeDir: svcRuntimeDir,
    StateDir:   serviceStateDirs[name],
    PodName:    p.Name,
})
```

**Step 6: Run tests**

```bash
go test ./cmd/claw/... -run TestPreMigrateMemory
```

Expected: all 3 `TestPreMigrateMemory*` tests PASS.

```bash
go test ./...
```

Expected: full suite still green (driver tests pass because `StateDir` is empty in existing driver tests — backward compat is preserved).

**Step 7: Commit**

```bash
git add cmd/claw/compose_up.go
git commit -m "feat(compose-up): pre-migrate memory and wire StateDir into Materialize"
```

---

## Task 3: Write failing test for `StateDir` in openclaw driver

**Files:**
- Modify: `internal/driver/openclaw/driver_test.go`

**Step 1: Add the test**

```go
func TestMaterializeMemoryLandsInStateDir(t *testing.T) {
	dir := t.TempDir()
	stateDir := t.TempDir()

	agentFile := filepath.Join(dir, "AGENTS.md")
	if err := os.WriteFile(agentFile, []byte("# Contract"), 0o644); err != nil {
		t.Fatal(err)
	}

	d := &Driver{}
	rc := &driver.ResolvedClaw{
		ClawType:      "openclaw",
		AgentHostPath: agentFile,
		Models:        map[string]string{"primary": "anthropic/claude-sonnet-4"},
	}
	result, err := d.Materialize(rc, driver.MaterializeOpts{
		RuntimeDir: dir,
		StateDir:   stateDir,
		PodName:    "test",
	})
	if err != nil {
		t.Fatalf("Materialize failed: %v", err)
	}

	// memory/ must be inside stateDir
	expectedMemDir := filepath.Join(stateDir, "memory")
	if _, err := os.Stat(expectedMemDir); err != nil {
		t.Fatalf("expected memory dir at stateDir/memory: %v", err)
	}

	// memory/ must NOT be inside runtimeDir
	if _, err := os.Stat(filepath.Join(dir, "memory")); !os.IsNotExist(err) {
		t.Fatal("expected no memory dir inside runtimeDir when StateDir is set")
	}

	// the mount must point at stateDir/memory
	for _, mount := range result.Mounts {
		if mount.ContainerPath == shared.PortableMemoryDir {
			if mount.HostPath != expectedMemDir {
				t.Fatalf("memory mount HostPath: expected %q, got %q", expectedMemDir, mount.HostPath)
			}
			return
		}
	}
	t.Fatal("expected portable memory mount at " + shared.PortableMemoryDir)
}
```

**Step 2: Run to verify it fails**

```bash
go test ./internal/driver/openclaw/... -run TestMaterializeMemoryLandsInStateDir
```

Expected: FAIL — memory still lands in `dir/memory` not `stateDir/memory`.

---

## Task 4: Update `openclaw` driver

**Files:**
- Modify: `internal/driver/openclaw/driver.go` (around line 41)

**Step 1: Change the `PreparePortableMemory` call**

Find:
```go
memoryDir, err := shared.PreparePortableMemory(opts.RuntimeDir)
if err != nil {
    return nil, fmt.Errorf("openclaw driver: prepare portable memory: %w", err)
}
```

Change to:
```go
memoryDir, err := shared.PreparePortableMemory(shared.ResolveStateDir(opts.RuntimeDir, opts.StateDir))
if err != nil {
    return nil, fmt.Errorf("openclaw driver: prepare portable memory: %w", err)
}
```

**Step 2: Run tests**

```bash
go test ./internal/driver/openclaw/... -run TestMaterializeMemoryLandsInStateDir
```

Expected: PASS.

```bash
go test ./internal/driver/openclaw/...
```

Expected: all openclaw tests PASS.

**Step 3: Commit**

```bash
git add internal/driver/openclaw/driver.go internal/driver/openclaw/driver_test.go
git commit -m "feat(driver/openclaw): use StateDir for portable memory"
```

---

## Task 5: Update `hermes` driver

**Files:**
- Modify: `internal/driver/hermes/driver.go` (around line 106)
- Modify: `internal/driver/hermes/driver_test.go`

**Step 1: Write the failing test** (same pattern as openclaw, but hermes uses `newTestRC`)

```go
func TestMaterializeMemoryLandsInStateDir(t *testing.T) {
	rc, tmp := newTestRC(t)
	runtimeDir := filepath.Join(tmp, "runtime")
	stateDir := filepath.Join(tmp, "state")
	for _, d := range []string{runtimeDir, stateDir} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}

	result, err := (&Driver{}).Materialize(rc, driver.MaterializeOpts{
		RuntimeDir: runtimeDir,
		StateDir:   stateDir,
		PodName:    "test",
	})
	if err != nil {
		t.Fatalf("Materialize failed: %v", err)
	}

	expectedMemDir := filepath.Join(stateDir, "memory")
	if _, err := os.Stat(expectedMemDir); err != nil {
		t.Fatalf("expected memory dir at stateDir/memory: %v", err)
	}
	if _, err := os.Stat(filepath.Join(runtimeDir, "memory")); !os.IsNotExist(err) {
		t.Fatal("expected no memory dir inside runtimeDir when StateDir is set")
	}
	for _, mount := range result.Mounts {
		if mount.ContainerPath == shared.PortableMemoryDir {
			if mount.HostPath != expectedMemDir {
				t.Fatalf("memory mount HostPath: expected %q, got %q", expectedMemDir, mount.HostPath)
			}
			return
		}
	}
	t.Fatal("expected portable memory mount at " + shared.PortableMemoryDir)
}
```

**Step 2: Run to verify it fails**

```bash
go test ./internal/driver/hermes/... -run TestMaterializeMemoryLandsInStateDir
```

Expected: FAIL.

**Step 3: Apply the driver change**

Find (around line 106):
```go
memoryDir, err := shared.PreparePortableMemory(opts.RuntimeDir)
```

Change to:
```go
memoryDir, err := shared.PreparePortableMemory(shared.ResolveStateDir(opts.RuntimeDir, opts.StateDir))
```

**Step 4: Run tests**

```bash
go test ./internal/driver/hermes/...
```

Expected: all PASS.

**Step 5: Commit**

```bash
git add internal/driver/hermes/driver.go internal/driver/hermes/driver_test.go
git commit -m "feat(driver/hermes): use StateDir for portable memory"
```

---

## Task 6: Update `nanobot` driver

**Files:**
- Modify: `internal/driver/nanobot/driver.go` (around line 83)
- Modify: `internal/driver/nanobot/driver_test.go`

Same TDD pattern as Tasks 3–4. Find the `newTestRC` helper in `nanobot/driver_test.go` first.

**Step 1:** Add `TestMaterializeMemoryLandsInStateDir` (same body as hermes version).

**Step 2:** Run to confirm FAIL.

**Step 3:** Change `PreparePortableMemory(opts.RuntimeDir)` → `PreparePortableMemory(shared.ResolveStateDir(opts.RuntimeDir, opts.StateDir))`

**Step 4:** Run `go test ./internal/driver/nanobot/...` — all PASS.

**Step 5: Commit**

```bash
git add internal/driver/nanobot/driver.go internal/driver/nanobot/driver_test.go
git commit -m "feat(driver/nanobot): use StateDir for portable memory"
```

---

## Task 7: Update `nanoclaw` driver

**Files:**
- Modify: `internal/driver/nanoclaw/driver.go` (around line 45)
- Modify: `internal/driver/nanoclaw/driver_test.go`

Same TDD pattern. Find `newTestRC` in `nanoclaw/driver_test.go`.

**Step 1:** Add `TestMaterializeMemoryLandsInStateDir` (same body).

**Step 2:** Run to confirm FAIL.

**Step 3:** Change `PreparePortableMemory(opts.RuntimeDir)` → `PreparePortableMemory(shared.ResolveStateDir(opts.RuntimeDir, opts.StateDir))`

**Step 4:** `go test ./internal/driver/nanoclaw/...` — all PASS.

**Step 5: Commit**

```bash
git add internal/driver/nanoclaw/driver.go internal/driver/nanoclaw/driver_test.go
git commit -m "feat(driver/nanoclaw): use StateDir for portable memory"
```

---

## Task 8: Update `picoclaw` driver

**Files:**
- Modify: `internal/driver/picoclaw/driver.go` (around line 91)
- Modify: `internal/driver/picoclaw/driver_test.go`

Same TDD pattern. Find `newTestRC` in `picoclaw/driver_test.go`.

**Step 1:** Add `TestMaterializeMemoryLandsInStateDir` (same body).

**Step 2:** Run to confirm FAIL.

**Step 3:** Change `PreparePortableMemory(opts.RuntimeDir)` → `PreparePortableMemory(shared.ResolveStateDir(opts.RuntimeDir, opts.StateDir))`

**Step 4:** `go test ./internal/driver/picoclaw/...` — all PASS.

**Step 5: Commit**

```bash
git add internal/driver/picoclaw/driver.go internal/driver/picoclaw/driver_test.go
git commit -m "feat(driver/picoclaw): use StateDir for portable memory"
```

---

## Task 9: Update `microclaw` driver

**Files:**
- Modify: `internal/driver/microclaw/driver.go` (around line 91)
- Modify: `internal/driver/microclaw/driver_test.go`

Same TDD pattern. Find `newTestRC` in `microclaw/driver_test.go`.

**Step 1:** Add `TestMaterializeMemoryLandsInStateDir` (same body).

**Step 2:** Run to confirm FAIL.

**Step 3:** Change `PreparePortableMemory(opts.RuntimeDir)` → `PreparePortableMemory(shared.ResolveStateDir(opts.RuntimeDir, opts.StateDir))`

**Step 4:** `go test ./internal/driver/microclaw/...` — all PASS.

**Step 5: Commit**

```bash
git add internal/driver/microclaw/driver.go internal/driver/microclaw/driver_test.go
git commit -m "feat(driver/microclaw): use StateDir for portable memory"
```

---

## Task 10: Update `nullclaw` driver

**Files:**
- Modify: `internal/driver/nullclaw/driver.go` (around line 63)
- Modify: `internal/driver/nullclaw/driver_test.go`

Same TDD pattern. Find `newTestRC` in `nullclaw/driver_test.go`.

**Step 1:** Add `TestMaterializeMemoryLandsInStateDir` (same body).

**Step 2:** Run to confirm FAIL.

**Step 3:** Change `PreparePortableMemory(opts.RuntimeDir)` → `PreparePortableMemory(shared.ResolveStateDir(opts.RuntimeDir, opts.StateDir))`

**Step 4:** `go test ./internal/driver/nullclaw/...` — all PASS.

**Step 5: Commit**

```bash
git add internal/driver/nullclaw/driver.go internal/driver/nullclaw/driver_test.go
git commit -m "feat(driver/nullclaw): use StateDir for portable memory"
```

---

## Task 11: Add compose_up boundary test

**Files:**
- Modify: `cmd/claw/compose_up_test.go`

**Step 1: Add the test**

```go
func TestMemoryStateDirSurvivesRuntimeReset(t *testing.T) {
	podDir := t.TempDir()

	// Simulate what runComposeUp does for one service
	svcStateDir, err := ensurePersistentCllamaDir(podDir, filepath.Join(".claw-memory", "analyst"))
	if err != nil {
		t.Fatalf("ensurePersistentCllamaDir: %v", err)
	}

	// State dir must NOT be inside .claw-runtime
	runtimeDir := filepath.Join(podDir, ".claw-runtime")
	if strings.HasPrefix(svcStateDir, runtimeDir+string(filepath.Separator)) {
		t.Fatalf("state dir %q must not be inside runtimeDir %q", svcStateDir, runtimeDir)
	}

	// Write a file to simulate agent memory
	memDir := filepath.Join(svcStateDir, "memory")
	if err := os.MkdirAll(memDir, 0o777); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(memDir, "MEMORY.md"), []byte("important"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Simulate claw up resetting the runtime dir
	if err := os.MkdirAll(runtimeDir, 0o777); err != nil {
		t.Fatal(err)
	}
	if err := resetRuntimeDir(runtimeDir); err != nil {
		t.Fatalf("resetRuntimeDir: %v", err)
	}

	// Memory must survive
	data, err := os.ReadFile(filepath.Join(memDir, "MEMORY.md"))
	if err != nil {
		t.Fatalf("memory did not survive resetRuntimeDir: %v", err)
	}
	if string(data) != "important" {
		t.Fatalf("memory corrupted: got %q", string(data))
	}
}
```

Note: this test needs `"strings"` imported. Check whether it's already in compose_up_test.go imports; add it if not.

**Step 2: Run**

```bash
go test ./cmd/claw/... -run TestMemoryStateDirSurvivesRuntimeReset
```

Expected: PASS immediately (tests the helper wiring we did in Task 2, no new code needed).

**Step 3: Commit**

```bash
git add cmd/claw/compose_up_test.go
git commit -m "test(compose-up): assert memory state dir survives runtime reset"
```

---

## Task 12: Full suite verification and update CLAUDE.md

**Step 1: Run full test suite**

```bash
go test ./...
go vet ./...
```

All tests must PASS.

**Step 2: Update CLAUDE.md gotcha**

Find the existing gotcha about cllama session history (search for `.claw-session-history`). Add a companion line for `.claw-memory`:

In `CLAUDE.md` (which is a symlink to `AGENTS.md`), find the gotcha:
```
- cllama session history: `claw up` bind-mounts `.claw-session-history/` → `/claw/session-history` in the cllama container...
```

Add after it:
```
- Portable memory: `claw up` now stores agent memory in `.claw-memory/<service>/memory/` (persistent sibling of `.claw-auth/`, `.claw-session-history/`). This directory survives `claw up` resets. Driver migration (e.g. hermes → openclaw) automatically inherits the same memory dir. The old `.claw-runtime/<service>/memory/` layout is auto-migrated on the first `claw up` after upgrade.
```

**Step 3: Commit everything**

```bash
git add AGENTS.md
git commit -m "docs(agents): document persistent .claw-memory layout"
```

---

## Verification After All Tasks

### Functional check (using any existing pod)

```bash
# 1. Run claw up
claw up -d examples/quickstart/claw-pod.yml

# 2. Verify .claw-memory/ exists alongside .claw-runtime/
ls examples/quickstart/
# Expected: .claw-auth  .claw-memory  .claw-runtime  .claw-session-history

# 3. Verify per-agent memory dir was created
ls examples/quickstart/.claw-memory/
# Expected: <agent-service-names>/

# 4. Write a test file
echo "survives" > examples/quickstart/.claw-memory/<service>/memory/test.md

# 5. Run claw up again
claw up -d examples/quickstart/claw-pod.yml

# 6. File must still exist
cat examples/quickstart/.claw-memory/<service>/memory/test.md
# Expected: survives

# 7. .claw-runtime/<service>/memory/ must NOT exist
ls examples/quickstart/.claw-runtime/<service>/
# Expected: no memory/ directory here
```

### Migration check (pods with existing runtime memory)

If a pod has pre-existing `.claw-runtime/<service>/memory/MEMORY.md` with real content:

```bash
# Run claw up — migration is automatic
claw up -d <pod-dir>/claw-pod.yml

# Memory must appear in .claw-memory/
cat <pod-dir>/.claw-memory/<service>/memory/MEMORY.md
# Expected: original content preserved
```

### All new tests

```bash
go test ./... -run "TestPreMigrateMemory|TestMemoryStateDirSurvivesRuntimeReset|TestMaterializeMemoryLandsInStateDir"
```

Expected: 11 new tests all PASS (3 in cmd/claw, 1 per driver × 7, 1 boundary in cmd/claw).
