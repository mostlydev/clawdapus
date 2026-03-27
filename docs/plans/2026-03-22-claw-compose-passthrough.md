# `claw compose` Passthrough & Staleness Detection — Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Add `claw compose <subcommand> [args...]` as a generic escape hatch to docker compose, and block commands when `compose.generated.yml` is stale relative to the pod file.

**Architecture:** One new cobra command with `DisableFlagParsing: true` passes everything through to `docker compose -f <generated>`. A staleness check in `resolveComposeGeneratedPath()` compares mtimes of pod file vs generated file and returns an error when stale. `claw down` bypasses the staleness check.

**Tech Stack:** Go, cobra, os.Stat for mtime comparison

---

### Task 1: Staleness check — failing tests

**Files:**
- Modify: `cmd/claw/compose_test.go`

**Step 1: Write failing tests for staleness detection**

Add these tests to `cmd/claw/compose_test.go`:

```go
func TestResolveComposeGeneratedPathStaleWithPodFile(t *testing.T) {
	dir := t.TempDir()
	generatedPath := filepath.Join(dir, "compose.generated.yml")
	podPath := filepath.Join(dir, "claw-pod.yml")

	// Create generated file first (older)
	os.WriteFile(generatedPath, []byte("services: {}"), 0644)

	// Sleep to guarantee mtime difference
	time.Sleep(50 * time.Millisecond)

	// Create pod file second (newer → stale)
	os.WriteFile(podPath, []byte("services: {}"), 0644)

	composePodFile = podPath
	defer func() { composePodFile = "" }()

	_, err := resolveComposeGeneratedPath()
	if err == nil {
		t.Fatal("expected staleness error when pod file is newer than generated")
	}
	if !strings.Contains(err.Error(), "claw up") {
		t.Errorf("expected error to mention 'claw up', got: %s", err.Error())
	}
}

func TestResolveComposeGeneratedPathStaleDefaultDir(t *testing.T) {
	orig, _ := os.Getwd()
	dir := t.TempDir()
	os.Chdir(dir)
	defer os.Chdir(orig)

	// Create generated file first (older)
	os.WriteFile(filepath.Join(dir, "compose.generated.yml"), []byte("services: {}"), 0644)

	time.Sleep(50 * time.Millisecond)

	// Create pod file second (newer → stale)
	os.WriteFile(filepath.Join(dir, "claw-pod.yml"), []byte("services: {}"), 0644)

	composePodFile = ""

	_, err := resolveComposeGeneratedPath()
	if err == nil {
		t.Fatal("expected staleness error when pod file is newer than generated")
	}
	if !strings.Contains(err.Error(), "claw up") {
		t.Errorf("expected error to mention 'claw up', got: %s", err.Error())
	}
}

func TestResolveComposeGeneratedPathFreshNotStale(t *testing.T) {
	dir := t.TempDir()
	podPath := filepath.Join(dir, "claw-pod.yml")

	// Create pod file first (older)
	os.WriteFile(podPath, []byte("services: {}"), 0644)

	time.Sleep(50 * time.Millisecond)

	// Create generated file second (newer → fresh)
	os.WriteFile(filepath.Join(dir, "compose.generated.yml"), []byte("services: {}"), 0644)

	composePodFile = podPath
	defer func() { composePodFile = "" }()

	path, err := resolveComposeGeneratedPath()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.HasSuffix(path, "compose.generated.yml") {
		t.Errorf("expected compose.generated.yml path, got %q", path)
	}
}
```

**Step 2: Run tests to verify they fail**

Run: `go test ./cmd/claw/ -run 'TestResolveComposeGeneratedPathStale|TestResolveComposeGeneratedPathFreshNotStale' -v`
Expected: `TestResolveComposeGeneratedPathStaleWithPodFile` and `TestResolveComposeGeneratedPathStaleDefaultDir` FAIL (no staleness check exists yet). `TestResolveComposeGeneratedPathFreshNotStale` PASS (fresh file already works).

**Step 3: Commit**

```bash
git add cmd/claw/compose_test.go
git commit -m "test: add staleness detection tests for resolveComposeGeneratedPath"
```

---

### Task 2: Staleness check — implementation

**Files:**
- Modify: `cmd/claw/compose.go`

**Step 1: Implement staleness check**

Replace the body of `resolveComposeGeneratedPath()` in `cmd/claw/compose.go` with:

```go
var skipStalenessCheck bool

func resolveComposeGeneratedPath() (string, error) {
	var podDir string
	var podFile string

	if composePodFile != "" {
		absPodFile, err := filepath.Abs(composePodFile)
		if err != nil {
			return "", fmt.Errorf("resolve pod file path %q: %w", composePodFile, err)
		}
		podDir = filepath.Dir(absPodFile)
		podFile = absPodFile
	} else {
		cwd, err := os.Getwd()
		if err != nil {
			return "", fmt.Errorf("resolve current directory: %w", err)
		}
		podDir = cwd
		podFile = filepath.Join(cwd, "claw-pod.yml")
	}

	generatedPath := filepath.Join(podDir, "compose.generated.yml")
	genInfo, err := os.Stat(generatedPath)
	if err != nil {
		if composePodFile != "" {
			return "", fmt.Errorf("no compose.generated.yml found next to %q (run 'claw up %s' first)", composePodFile, composePodFile)
		}
		return "", fmt.Errorf("no compose.generated.yml found in %q (rerun from pod directory or pass --file <path-to-claw-pod.yml>)", podDir)
	}

	if !skipStalenessCheck {
		if podInfo, err := os.Stat(podFile); err == nil {
			if podInfo.ModTime().After(genInfo.ModTime()) {
				return "", fmt.Errorf("claw-pod.yml is newer than compose.generated.yml — run 'claw up' to regenerate")
			}
		}
	}

	return generatedPath, nil
}
```

The `skipStalenessCheck` package-level var defaults to `false`. `claw down` sets it to `true` before calling.

**Step 2: Wire `claw down` to skip staleness**

In `cmd/claw/compose_down.go`, add `skipStalenessCheck = true` at the top of the `RunE` func and `defer func() { skipStalenessCheck = false }()` right after:

```go
RunE: func(cmd *cobra.Command, args []string) error {
    skipStalenessCheck = true
    defer func() { skipStalenessCheck = false }()

    generatedPath, err := resolveComposeGeneratedPath()
    // ... rest unchanged
```

**Step 3: Run tests to verify they pass**

Run: `go test ./cmd/claw/ -run 'TestResolveComposeGeneratedPath' -v`
Expected: ALL pass, including the two new staleness tests.

**Step 4: Commit**

```bash
git add cmd/claw/compose.go cmd/claw/compose_down.go
git commit -m "feat: block lifecycle commands when compose.generated.yml is stale"
```

---

### Task 3: `claw compose` passthrough — failing test

**Files:**
- Create: `cmd/claw/compose_passthrough_test.go`

**Step 1: Write a test that the command is registered**

```go
package main

import (
	"testing"
)

func TestComposePassthroughRegistered(t *testing.T) {
	for _, cmd := range rootCmd.Commands() {
		if cmd.Use == "compose [subcommand] [args...]" {
			return
		}
	}
	t.Fatal("expected 'compose' command to be registered on rootCmd")
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./cmd/claw/ -run TestComposePassthroughRegistered -v`
Expected: FAIL — no compose command registered.

**Step 3: Commit**

```bash
git add cmd/claw/compose_passthrough_test.go
git commit -m "test: add registration test for claw compose passthrough"
```

---

### Task 4: `claw compose` passthrough — implementation

**Files:**
- Create: `cmd/claw/compose_passthrough.go`

**Step 1: Implement the passthrough command**

```go
package main

import (
	"fmt"
	"os"
	"os/exec"

	"github.com/spf13/cobra"
)

var composePassthroughCmd = &cobra.Command{
	Use:                "compose [subcommand] [args...]",
	Short:              "Run any docker compose command against the generated compose file",
	DisableFlagParsing: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		if len(args) == 0 {
			return fmt.Errorf("usage: claw compose <subcommand> [args...]")
		}

		generatedPath, err := resolveComposeGeneratedPath()
		if err != nil {
			return err
		}

		composeArgs := append([]string{"compose", "-f", generatedPath}, args...)
		dockerCmd := exec.Command("docker", composeArgs...)
		dockerCmd.Stdin = os.Stdin
		dockerCmd.Stdout = os.Stdout
		dockerCmd.Stderr = os.Stderr
		if err := dockerCmd.Run(); err != nil {
			return fmt.Errorf("docker compose %s failed: %w", args[0], err)
		}
		return nil
	},
}

func init() {
	rootCmd.AddCommand(composePassthroughCmd)
}
```

Note: `Stdin` is connected so interactive commands like `claw compose exec analyst bash` work.

Note: `DisableFlagParsing: true` means `-f` from the root persistent flags won't be parsed for this subcommand. This is acceptable — operators use `claw compose` from the pod directory. If `-f` support is needed later, it can be wired by manually parsing the first args.

**Step 2: Run tests to verify they pass**

Run: `go test ./cmd/claw/ -run TestComposePassthroughRegistered -v`
Expected: PASS

**Step 3: Also run full test suite to check for regressions**

Run: `go test ./cmd/claw/ -v`
Expected: ALL pass. Pay attention to the existing `TestResolveComposeGeneratedPath*` tests — the staleness check must not break them. The existing `TestResolveComposeGeneratedPathDefaultExists` creates only `compose.generated.yml` without a pod file, so the staleness check's `os.Stat(podFile)` will return an error and the `err == nil` guard skips the check. Same for `TestResolveComposeGeneratedPathWithPodFile` which creates both files at roughly the same mtime (generated is same or newer). Both should still pass.

**Step 4: Commit**

```bash
git add cmd/claw/compose_passthrough.go
git commit -m "feat: add claw compose passthrough command"
```

---

### Task 5: Update docs

**Files:**
- Modify: `AGENTS.md` (lines listing CLI surface, ~line with "Current top-level commands")
- Modify: `README.md` (quickstart section showing claw commands)
- Modify: `skills/clawdapus/SKILL.md` (CLI Commands section, ~line 14-29)

**Step 1: Update AGENTS.md**

In the "Actual CLI Surface" section, add `claw compose` to the command list:

```
- `claw compose`
```

Add a note in the "Current Behavior Worth Knowing" section:

```
- Lifecycle commands (`ps`, `logs`, `health`, `compose`) refuse to run if `claw-pod.yml` is newer than `compose.generated.yml`. `claw down` is exempt — you can always tear down a stale pod. Run `claw up` to regenerate.
```

**Step 2: Update skills/clawdapus/SKILL.md**

In the CLI Commands section (~line 14-29), add after the `claw health` line:

```bash
claw compose <cmd> [args]    # passthrough: any docker compose subcommand
```

Add a note about staleness:

```
Lifecycle commands block if `claw-pod.yml` is newer than `compose.generated.yml` — run `claw up` to regenerate. `claw down` is exempt.
```

**Step 3: Update README.md**

In the quickstart section, add an example after the verify block showing the passthrough:

```bash
# Run any docker compose command
claw compose exec assistant bash
claw compose restart cllama-passthrough
claw compose top
```

**Step 4: Commit**

```bash
git add AGENTS.md README.md skills/clawdapus/SKILL.md
git commit -m "docs: add claw compose command and staleness behavior to CLI reference"
```

---

### Task 6: Final verification

**Step 1: Run full test suite**

Run: `go test ./cmd/claw/ -v`
Expected: ALL pass.

**Step 2: Run vet**

Run: `go vet ./cmd/claw/...`
Expected: Clean.

**Step 3: Manual smoke test (if Docker available)**

From an example directory with an existing `compose.generated.yml`:
- `claw compose ps` — should work like `claw ps`
- Touch `claw-pod.yml` to make it newer — `claw compose ps` should error with staleness message
- `claw down` should still work despite staleness
