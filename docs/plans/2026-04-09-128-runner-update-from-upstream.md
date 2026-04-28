# Implementation Plan: Issue #128 — Runner Refresh from Upstream

**Date:** 2026-04-09
**Status:** Draft
**Issue:** #128
**ADR:** `docs/decisions/024-runner-base-refresh-from-upstream.md`
**Alternative considered:** an explicit `claw runners update` top-level verb

## Goal

Make `claw pull` the operator-facing refresh verb for built-in runner bases, building each base from clean upstream sources on the local machine, surfacing the resolved upstream version explicitly, rewriting `Dockerfile.generated` to reference the explicit version tag, and stamping service images with human-readable + image-ID + recipe-SHA provenance.

No mostlydev-published runner images. No release manifest entries. No new publishing workflows.

## Shipping target

This is a single focused implementation session **after** a per-driver probe verification pass. The surface is small (~9 files, ~500 lines net), but the probe verification is a blocker — each driver's `RunnerVersionProbe` must be validated against the driver's actual installed toolchain before the probe table in §2 is final. "Lands in one PR" is realistic; "ships without per-driver verification" is not.

## Current-state verification

- `internal/driver/openclaw/baseimage.go:3` — synthetic tag `openclaw:latest`
- `internal/driver/microclaw/baseimage.go` — synthetic tag `microclaw:latest`
- `internal/driver/nullclaw/baseimage.go` — synthetic tag `nullclaw:latest`
- `internal/driver/nanobot/baseimage.go` — synthetic tag `nanobot:latest`
- `internal/driver/picoclaw/baseimage.go:3` — synthetic tag `picoclaw:latest`, base is `docker.io/sipeed/picoclaw:latest`
- `internal/driver/nanoclaw/baseimage.go:3` — synthetic tag `nanoclaw-orchestrator:latest`; recipe installs `ca-certificates git python3 make g++` in builder and `ca-certificates git procps tini` in runtime (**note: no jq**)
- `internal/driver/hermes/baseimage.go:5` — already pinned to `ghcr.io/mostlydev/hermes-base:v2026.3.17-claw.2` (out of scope)
- `internal/build/build.go:57-84` — `ensureBaseImage` only auto-builds when missing locally; never refreshes
- `internal/build/build.go:131` — `BuildFromDockerfileContent` runs plain `docker build`, no `--pull --no-cache`
- `internal/clawfile/emit.go:10` — `Emit(result *ParseResult) (string, error)` is pure-Go, copies FROM lines verbatim
- `cmd/claw/pull.go:9-27` — `runPull` has three code paths: no pod (`pullCoreInfraImages`), pod mode (infra + registry service pulls). Auto-resolves `claw-pod.yml` in cwd via `resolveOptionalPodFile`.
- `cmd/claw/image_lifecycle.go:137` — `resolveOptionalPodFile` returns `(path, true, nil)` when `claw-pod.yml` exists in cwd, `("", false, nil)` otherwise.
- `cmd/claw/image_lifecycle.go:281-348` — `requiredPodPullInfraSpecs` computes which infra a pod needs

The current behavior: a machine that has built `openclaw:latest` once will use that stale image forever, with no `claw` verb that updates it.

## Target operator flow

Pod mode:

```
$ claw pull
[claw] pulling pinned infra (claw-api v0.8.0, cllama v0.3.3, ...)
[claw] refreshing runner bases for pod (1 driver: openclaw)
[claw] openclaw: building base from upstream (this may take a few minutes)
[claw]   FROM node:22-slim (pulled fresh)
[claw]   curl https://openclaw.ai/install.sh ...
[claw] openclaw: installed v0.5.2 (was v0.5.0)
[claw]   tagged: openclaw:v0.5.2, openclaw:latest
[claw] pull complete
```

Fast infra-only mode:

```
$ claw pull --no-runners
[claw] pulling pinned infra (...)
[claw] runner base refresh skipped (--no-runners)
[claw] pull complete
```

Single-Clawfile authoring mode:

```
$ claw pull my.Clawfile
[claw] refreshing runner base for clawfile (driver: openclaw)
[claw] openclaw: building base from upstream ...
[claw] openclaw: installed v0.5.2
[claw] pull complete
```

Bare mode (refresh locally-tagged only):

```
$ claw pull
[claw] pulling pinned infra (...)
[claw] refreshing locally-tagged managed runner aliases (1: openclaw)
[claw] openclaw: already at v0.5.2
[claw] pull complete
```

After a fresh `claw pull`, if the operator forgot to rebuild:

```
$ claw up -d
[claw] analyst: built against openclaw v0.5.0 (image abc123def456), current is v0.5.2 (image 789abcd...) — consider running: claw build
[claw] pod up
```

## Ordered work breakdown

### 1. Add the optional driver interfaces

**File:** `internal/driver/types.go`

Append after `BaseImageProvider`:

```go
// RunnerBaseProvider is optionally implemented by drivers whose base image is
// built from upstream sources rather than pulled from a pinned registry tag.
// Implementing this interface signals to claw pull that the base should be
// refreshed against fresh upstream sources, and to claw build that
// FROM <alias>:latest should be rewritten to FROM <alias>:v<version> at emit time.
type RunnerBaseProvider interface {
    BaseImageProvider
    RunnerAlias() string
}

// RunnerVersionProber is optionally implemented by RunnerBaseProvider drivers
// that can report the installed upstream runner version. The returned command
// is run inside the freshly built base image; stdout is parsed as the version
// string. Drivers that do not implement this interface fall back to a
// build-date-plus-image-ID tag like "built-20260409-abc123def456".
type RunnerVersionProber interface {
    RunnerVersionProbe() []string
}
```

No changes to the existing `Driver` interface or `BaseImageProvider`.

### 2. Per-driver probe verification (BLOCKING prerequisite)

Each driver that opts into `RunnerVersionProber` must have its probe command verified against the driver's actual recipe before the probe is committed. Verification procedure:

1. Build the driver's current `BaseImage()` Dockerfile manually: `docker build --pull --no-cache -t <alias>:verify -f <tmpfile> .`
2. Run the candidate probe: `docker run --rm <alias>:verify <probe-cmd>`
3. Capture stdout and confirm it parses into a clean version string.
4. Add the captured sample to `internal/driver/<driver>/baseimage_test.go` as a golden fixture for the parser.

Initial probe table — **each row must be verified in step §2.1 before implementation lands**:

| Driver | Recipe installs | Candidate probe | Verified? |
|---|---|---|---|
| openclaw | node, npm, openclaw via install.sh | `openclaw --version \| awk '{print $NF}'` | ☐ |
| microclaw | (verify recipe) | `microclaw --version \| awk '{print $NF}'` | ☐ |
| nullclaw | (verify recipe) | `nullclaw --version \| awk '{print $NF}'` | ☐ |
| nanobot | python + nanobot package | `python -c "import nanobot; print(nanobot.__version__)"` | ☐ |
| nanoclaw | **node in builder + runtime; NO jq** | `node -e 'console.log(require("/workspace/package.json").version)'` | ☐ |
| picoclaw | `docker.io/sipeed/picoclaw:latest`, no tools added | **omit `RunnerVersionProber`** — falls back to `built-YYYYMMDD-<imageid12>` | N/A |

**Explicit correction from the prior draft:** the nanoclaw probe is `node`-based, not `jq`-based. The nanoclaw recipe (`internal/driver/nanoclaw/baseimage.go:21`) does not install `jq`, and using `jq` would silently fall back to the build-date tag on every refresh. `node` is guaranteed present (the recipe is `FROM node:22-bookworm-slim`), and `/workspace/package.json` is guaranteed present (copied from the cloned repo in the builder stage).

Drivers whose probe cannot be verified against their current recipe in a single verification pass are shipped with `RunnerVersionProber` **unimplemented**. They use the fallback tag scheme and can gain a probe later in a follow-up PR without touching the core mechanism.

### 3. Implement `RunnerBaseProvider` (and optional probe) in each driver

**Files:** `internal/driver/{openclaw,microclaw,nullclaw,nanobot,picoclaw,nanoclaw}/baseimage.go`

For openclaw:

```go
func (d *Driver) RunnerAlias() string {
    return "openclaw"
}

func (d *Driver) RunnerVersionProbe() []string {
    return []string{"sh", "-c", "openclaw --version 2>/dev/null | awk '{print $NF}'"}
}
```

Repeat for microclaw, nullclaw, nanobot, nanoclaw using the verified probes from §2. Picoclaw implements only `RunnerAlias()`.

### 4. Add `RefreshRunnerBase` in `internal/build/build.go`

Add a new exported function:

```go
// RefreshResult captures everything claw pull learned about a freshly built
// runner base. All three fields are passed to claw build later so service
// images can be stamped with full provenance.
type RefreshResult struct {
    DriverName  string // "openclaw" driver name
    Alias       string // "openclaw" Docker alias, or "nanoclaw-orchestrator"
    ImageRef    string // "<alias>:latest"
    BuiltRef    string // "<alias>:v0.5.2" or "<alias>:built-..."
    VersionTag  string // "v0.5.2" or "built-..."
    PreviousRef string // previous versioned ref, if one was discoverable
    PreviousTag string // previous version tag, if one was discoverable
    ImageID     string // "sha256:abc..." — strong drift fingerprint
    RecipeSHA   string // "sha256:def..." — recipe content hash
}

// RefreshRunnerBase rebuilds the driver's base image against fresh upstream
// sources, probes the resulting image for its upstream runner version, tags
// the result, and returns the provenance needed by the caller.
func RefreshRunnerBase(driverName string, d driver.RunnerBaseProvider) (*RefreshResult, error)
```

Implementation shape:

1. `tag, dockerfile := d.BaseImage(); alias := d.RunnerAlias()`.
2. Record the previous versioned ref (if any) via local Docker inspection.
3. Compute `recipeSHA := sha256.Sum256([]byte(dockerfile))`.
4. Generate a unique interim tag: `interim := alias + ":refreshing-" + shortRand()`.
5. Run `docker build --pull --no-cache -t <interim> <tmpdir>`.
6. Query the image ID: `docker inspect --format '{{.Id}}' <interim>`.
7. Resolve the version via `RunnerVersionProber` if implemented; otherwise fall back to `built-YYYYMMDD-<imageid12>`.
8. Compute versioned tag: `versioned := alias + ":v" + version` (or `alias + ":" + fallbackTag` for non-probe drivers).
9. `docker tag <interim> <versioned>` and `docker tag <interim> <alias>:latest`.
10. `docker rmi <interim>` (best-effort; not a hard error if it fails).
11. Return the `RefreshResult`.

`<alias>:latest` is not retagged until build, inspect, and probe have all succeeded. A failed refresh should leave the previous usable alias in place.

Helpers `dockerTag`, `dockerRmiQuiet`, `shortRand`, and `lookupLocalRunnerVersion` wrap shell invocations of `docker` — the file already shells out to docker (see `build.go:131`), so this is a continuation of the existing pattern.

### 5. Three-mode dispatch in `cmd/claw/pull.go`

The current `pullCmd` uses `resolveOptionalPodFile` (`image_lifecycle.go:137`), which handles both explicit `--file` and positional-arg cases plus the `claw-pod.yml`-in-cwd auto-resolution. Extend that logic to also recognize the full set of single-Clawfile inputs that `claw build <path>` already accepts, rather than inventing a narrower filename-only classifier.

Change `pullCmd` definition in `cmd/claw/pull.go`:

```go
var pullCmd = &cobra.Command{
    Use:   "pull [pod-file-or-clawfile]",
    Short: "Fetch pinned infra, refresh runner bases, and pull pod registry images",
    Args:  cobra.MaximumNArgs(1),
    RunE: func(cmd *cobra.Command, args []string) error {
        target, err := resolvePullTarget(composePodFile, args)
        if err != nil {
            return err
        }
        switch target.Kind {
        case pullTargetPod:
            return runPullPod(target.Path)
        case pullTargetClawfile:
            return runPullClawfile(target.Path)
        case pullTargetBare:
            return runPullBare()
        }
        return fmt.Errorf("unreachable")
    },
}
```

Add `--no-runners` to skip the runner-refresh phase while preserving the existing pinned-infra and registry-service pull behavior.

New helper in `cmd/claw/image_lifecycle.go` (next to `resolveOptionalPodFile`):

```go
type pullTargetKind int

const (
    pullTargetBare pullTargetKind = iota
    pullTargetPod
    pullTargetClawfile
)

type pullTarget struct {
    Kind pullTargetKind
    Path string
}

// resolvePullTarget implements the ADR-024 §2 mode matrix:
//   - explicit -f <pod>                                   → pullTargetPod
//   - positional <pod> (.yml/.yaml)                       → pullTargetPod
//   - positional <path> accepted by claw build's
//     single-Clawfile resolution rules                    → pullTargetClawfile
//   - no args, claw-pod.yml in cwd                        → pullTargetPod (auto)
//   - no args, no pod in cwd                              → pullTargetBare
//   - unclassifiable positional input                     → hard error
func resolvePullTarget(explicit string, args []string) (pullTarget, error) {
    if explicit != "" && len(args) > 0 {
        return pullTarget{}, fmt.Errorf("pull target specified twice: use either '--file %s' or positional '%s', not both", explicit, args[0])
    }
    if explicit != "" {
        return pullTarget{Kind: pullTargetPod, Path: explicit}, nil
    }
    if len(args) > 0 {
        return classifyPullArg(args[0])
    }
    if _, err := os.Stat("claw-pod.yml"); err == nil {
        return pullTarget{Kind: pullTargetPod, Path: "claw-pod.yml"}, nil
    } else if !errors.Is(err, os.ErrNotExist) {
        return pullTarget{}, fmt.Errorf("stat claw-pod.yml: %w", err)
    }
    return pullTarget{Kind: pullTargetBare}, nil
}

func classifyPullArg(path string) (pullTarget, error) {
    ext := strings.ToLower(filepath.Ext(path))
    if ext == ".yml" || ext == ".yaml" {
        return pullTarget{Kind: pullTargetPod, Path: path}, nil
    }

    resolved, err := resolveClawfilePath(path)
    if err == nil && isClawBuildFile(resolved) {
        return pullTarget{Kind: pullTargetClawfile, Path: resolved}, nil
    }

    return pullTarget{}, fmt.Errorf("cannot classify pull target %q: expected a pod file (*.yml/*.yaml) or any path accepted by 'claw build <path>'", path)
}
```

This deliberately reuses existing Clawfile behavior instead of narrowing it. Flat-layout filenames like `Clawfile.westin`, example files like `Clawfile.nanoclaw`, directories containing a `Clawfile`, and other custom paths that already satisfy `isClawBuildFile` continue to work in `claw pull`. We should not make runner refresh less capable than `claw build`, because the remediation path in §8 must accept the same input the user just built from.

The `runPullBare` function preserves current behavior (`pullCoreInfraImages()`) and additionally refreshes any managed runner aliases already locally tagged. The `runPullPod` function is the existing logic plus a call to `refreshPodRunnerBases(p)`. The `runPullClawfile` function parses the Clawfile and refreshes just that driver's base.

Backward compatibility: `resolveOptionalPodFile` at `image_lifecycle.go:137` is kept intact for other callers (`compose_up.go`, etc.). Only `pullCmd` switches to `resolvePullTarget`.

### 6. Runner driver discovery helpers in `cmd/claw/image_lifecycle.go`

```go
// requiredRunnerDriversForPod returns the unique RunnerBaseProvider drivers
// used by the pod's build: services.
func requiredRunnerDriversForPod(podDir string, p *pod.Pod, plans []plannedServiceImage) ([]driver.RunnerBaseProvider, error)

// runnerDriverForClawfile returns the RunnerBaseProvider for a single clawfile
// path, or nil if the driver does not implement RunnerBaseProvider.
func runnerDriverForClawfile(clawfilePath string) (driver.RunnerBaseProvider, error)

// locallyTaggedRunnerDrivers returns runner drivers whose <alias>:latest is
// already present in the local Docker daemon. Used by the bare claw pull mode.
func locallyTaggedRunnerDrivers() []driver.RunnerBaseProvider
```

Each helper calls out to `driver.Lookup` / `driver.All` (whichever is the current registry API) and filters by `RunnerBaseProvider` assertion.

The refresh driver:

```go
func refreshRunnerBases(drivers []driver.RunnerBaseProvider) (map[string]*build.RefreshResult, error) {
    results := make(map[string]*build.RefreshResult)
    for _, d := range drivers {
        alias := d.RunnerAlias()
        fmt.Printf("[claw] %s: building base from upstream (this may take a few minutes)\n", alias)
        res, err := build.RefreshRunnerBase(d)
        if err != nil {
            return nil, fmt.Errorf("refresh runner base %s: %w", alias, err)
        }
        switch {
        case res.PreviousRef == "":
            fmt.Printf("[claw] %s: installed %s\n", alias, res.BuiltRef)
        case res.PreviousRef == res.BuiltRef:
            fmt.Printf("[claw] %s: refreshed %s\n", alias, res.BuiltRef)
        default:
            fmt.Printf("[claw] %s: installed %s (was %s)\n", alias, res.BuiltRef, res.PreviousRef)
        }
        results[alias] = res
    }
    return results, nil
}
```

### 7. FROM rewriting and label injection in `internal/clawfile/emit.go`

Change the signature:

```go
// RunnerProvenance describes the resolved runner base for a single driver
// alias. Passed to Emit so it can rewrite FROM lines and inject provenance
// labels into Dockerfile.generated.
type RunnerProvenance struct {
    Alias       string // "openclaw"
    Version     string // "0.5.2" or "built-20260409-abc123def456"
    ImageID     string // "sha256:..."
    RecipeSHA   string // "sha256:..."
}

// Emit renders the generated Dockerfile. If runner is non-nil, FROM <alias>:latest
// is rewritten to FROM <alias>:v<version> (or FROM <alias>:<fallbackTag>) and
// three provenance labels are injected. If runner is nil, Emit preserves the
// current literal-copy behavior.
func Emit(result *ParseResult, runner *RunnerProvenance) (string, error)
```

Inside `Emit`, when walking `result.DockerNodes`:

- If `node.Value` is `"from"` (case-insensitive) and `runner != nil` and the FROM image matches `runner.Alias + ":latest"`, rewrite the line to `FROM <alias>:<tag>` where `<tag>` is `"v" + runner.Version` if `runner.Version` starts with a semver-looking string, otherwise `runner.Version` itself (for the fallback tag that already includes its full form).

In `buildGeneratedLines`, append three lines when `runner != nil`:

```go
if runner != nil {
    lines = append(lines,
        formatLabel("claw.runner.built-against", fmt.Sprintf("%s:%s", runner.Alias, runnerTagFor(runner.Version))),
        formatLabel("claw.runner.image-id", runner.ImageID),
        formatLabel("claw.runner.recipe-sha", runner.RecipeSHA),
    )
}
```

Where `runnerTagFor("0.5.2") = "v0.5.2"` and `runnerTagFor("built-20260409-abc...") = "built-20260409-abc..."`.

Existing tests in `emit_test.go` pass `nil` for the new parameter and continue to pass.

### 8. Populate provenance in `build.Generate`

**File:** `internal/build/build.go`

Modify `Generate`:

```go
func Generate(clawfilePath string) (string, error) {
    // existing parse + driver lookup ...

    var provenance *clawfile.RunnerProvenance
    if rbp, ok := d.(driver.RunnerBaseProvider); ok {
        p, err := resolveLocalRunnerProvenance(rbp)
        if err != nil {
            return "", remediationErrorf("claw pull", "%w", err)
        }
        provenance = p
    }

    rendered, err := clawfile.Emit(parsed, provenance)
    if err != nil {
        return "", fmt.Errorf("emit dockerfile: %w", err)
    }
    // ...
}
```

`resolveLocalRunnerProvenance` looks up `<alias>:latest` in local Docker, extracts its image ID and the version-or-fallback tag from `RepoTags`, recomputes the recipe SHA from the driver's current `BaseImage()` Dockerfile content, and returns the provenance struct. Returns a remediation error if `<alias>:latest` is missing locally.

The remediation error message adapts to invocation context: if `Generate` was called from `claw build <path>`, the remediation is `run: claw pull <same-path>`; if from `claw build` pod mode, `run: claw pull`. (Wiring this requires threading the caller's invocation context into the error; alternately, the error carries a hint that the caller formats.)

### 9. Drift hint in `claw up`

**File:** `cmd/claw/compose_up.go`

In the pod service image validation phase, for each service image that has a `claw.runner.image-id` label:

1. Read `claw.runner.built-against` (e.g., `openclaw:v0.5.0`) to extract the alias.
2. Read `claw.runner.image-id` (e.g., `sha256:abc...`).
3. Query the current local `<alias>:latest` image ID via `docker inspect`.
4. If different, print:
   ```
   [claw] <service>: built against <alias> <built-against-version> (image <old-short>), current is <new-version> (image <new-short>) — consider running: claw build
   ```
5. If `--fix` is set, automatically trigger a rebuild of the affected services.

Service images without any `claw.runner.*` labels (built by older `claw` binaries) are silently treated as not-yet-migrated. `--fix` rebuilds them to pick up the labels.

**Epistemic boundary (matches ADR-024 §4):** this check compares local image IDs only. `claw up` does not probe upstream sources, does not assert that the local alias is "latest," and does not refresh the runner base itself. Runner refresh is always an explicit `claw pull`.

### 10. Tests

**Unit tests:**

- `internal/clawfile/emit_test.go`:
  - Existing cases pass `nil` for `runner` and continue to pass (no regression).
  - New case: pass a populated `RunnerProvenance`, assert FROM rewriting from `openclaw:latest` to `openclaw:v0.5.2` and presence of the three labels.
  - Edge case: FROM with explicit non-`:latest` tag (e.g., `FROM openclaw:pinned`) should be left alone.
  - Edge case: multi-stage Dockerfile with `FROM node:22 AS builder` followed by `FROM openclaw:latest` — only the runner FROM is rewritten.

- `internal/build/build_test.go`:
  - `RefreshRunnerBase` against a fake `RunnerBaseProvider` that returns a trivial Dockerfile (`FROM busybox\nRUN echo 0.0.1 > /version\nRUN chmod +x /bin/true`) and a probe that reads `/version`. Verify the interim build, probe, tagging, and image ID capture flow.
  - Fake driver without `RunnerVersionProber` → fallback tag format `built-YYYYMMDD-<imageid12>`.
  - Two consecutive refreshes of the same fake driver in the same test (simulating same-day) produce different image IDs and different fallback tags. **This is the regression test against codex's same-day collision finding.**

- `cmd/claw/image_lifecycle_test.go` (or new `pull_test.go`):
  - `resolvePullTarget` for all mode-matrix cases:
    - explicit `--file foo.pod.yml` → pod mode
    - positional `foo.pod.yml` → pod mode
    - positional `Clawfile.nanoclaw` → clawfile mode
    - positional `Clawfile` (basename, no extension) → clawfile mode
    - positional directory containing `Clawfile` → clawfile mode
    - positional custom file accepted by `isClawBuildFile` → clawfile mode
    - positional `unknown.txt` → error
    - no args, `claw-pod.yml` in cwd → pod mode (auto)
    - no args, no pod in cwd → bare mode
    - both `--file` and positional → error
    - `--no-runners` skips runner refresh while still pulling infra / registry images
  - `locallyTaggedRunnerDrivers` returns drivers whose alias tag exists in a mocked docker state.

- `cmd/claw/compose_up_test.go`:
  - Drift detection: service image labeled `claw.runner.image-id=sha256:abc`, current `openclaw:latest` has image ID `sha256:def` → soft hint printed, `claw up` continues.
  - **Same-version-different-id regression test:** service image labeled with version `openclaw:v0.5.2` and image-id `sha256:abc`, current `openclaw:latest` is also tagged `openclaw:v0.5.2` but has image-id `sha256:def` → drift detected (this is the exact case codex flagged as the semver-not-content-stable hole).
  - Service image without `claw.runner.*` labels → no hint, `claw up` continues.

**Integration tests:**

- `cmd/claw/` integration test that exercises `claw pull` against a fixture pod with one OpenClaw `build:` service, using a stub driver registered with `RunnerBaseProvider`. Verifies the full pull → rebuild → tag → build → up flow without hitting real upstream installers.

**Spike tests:**

- Update `TestSpikeRollCall` / `TestSpikeComposeUp` assertions that touch `Dockerfile.generated` content — the FROM line is no longer literal `:latest`.

**Full test sweep:**

```
go vet ./...
go test ./...
go test -tags integration ./...
```

Spike tests (live docker):

```
go test -tags spike -run TestSpikeRollCall ./cmd/claw/...
```

### 11. Documentation sweep

- `AGENTS.md` (and the `CLAUDE.md` symlink): remove the OpenClaw refresh footgun from "Repo-Specific Gotchas." Add a note that `claw pull` refreshes runner bases for the pod's `build:` services (or for a single Clawfile) and that this can take minutes. Document the three-mode `claw pull` matrix.
- `README.md`: extend the four-verb explanation to mention runner refresh under `claw pull`, including the single-Clawfile mode and `--no-runners` escape hatch.
- `site/guide/cli.md`: same.
- `site/guide/quickstart.md`: confirm the quickstart still works end-to-end.
- `cmd/claw/skill_data/SKILL.md` and `skills/clawdapus/SKILL.md`: regenerate via `go generate ./cmd/claw/...`.
- `site/changelog.md`: add an entry under the next version describing the runner refresh path, the FROM-rewrite change, and the three-label provenance.

## Manual smoke test

1. `docker rmi openclaw:latest openclaw:v* openclaw:built-* 2>/dev/null || true`
2. `cd examples/quickstart && claw up -d` — expect `claw build` failure with `run: claw pull -f claw-pod.yml` remediation.
3. `claw pull` — expect openclaw rebuild with version output.
4. `cat compose.generated.yml` and `cat <build-ctx>/Dockerfile.generated` — verify `FROM openclaw:v<version>` and three `LABEL claw.runner.*` lines.
5. `docker image inspect openclaw:latest` — verify both `openclaw:v<version>` and `openclaw:latest` in `RepoTags`.
6. `claw build && claw up -d` — pod starts.
7. **Single-Clawfile mode:** `claw pull examples/trading-desk/Clawfile.nanoclaw` and `claw pull examples/quickstart` — expect runner refresh to work for both a custom-named Clawfile and a directory input, using the same resolution rules as `claw build`.
8. **Same-version drift simulation:** `docker tag openclaw:v<version> openclaw:v<version>-spoof && docker rmi openclaw:latest && docker tag busybox openclaw:latest` → `claw up` should print the soft drift hint because image IDs differ.
9. **Fast infra-only smoke:** `claw pull --no-runners -f claw-pod.yml` should pull pinned infra and registry service images without touching local runner aliases.
10. **Live Tiverton smoke:** on the trading pod host, run `claw pull -f claw-pod.yml`, then `claw build -f claw-pod.yml`, then `claw up -d -f claw-pod.yml`, then `claw compose exec analyst openclaw --version` to confirm the new runner version.

## Open questions to settle during implementation

1. **Probe output stability per driver.** Resolved per-driver in §2. Non-blocking for the core mechanism because graceful fallback is built in, but blocking for the probe table being final.
2. **Picoclaw versioning.** Confirmed: no probe, always uses the fallback tag. Drift detection still works via image-id comparison.
3. **Multi-stage Dockerfile FROM rewriting.** Covered by a test case in §10; implementation must walk `DockerNodes` and only rewrite the FROM whose image matches a known alias, leaving intermediate stages alone.
4. **Concurrent `claw pull` runs on the same machine.** Handled by the unique interim tag (`alias:refreshing-<rand>`) in `RefreshRunnerBase`. Worth a note in the plan for future reviewers.
5. **Network failures during refresh.** If `curl install.sh` fails mid-build, the operator gets a docker-build error. First implementation surfaces the raw error; friendlier wrapping can be a follow-up.
6. **Error-message adaptation for remediation.** `build.Generate`'s remediation error needs to know whether it was called from pod mode or single-Clawfile mode so the `run: claw pull <same-path>` hint matches the exact `claw build <path>` input. Plan: thread an invocation context through `Generate` or return a structured error the caller formats.

## Non-goals for this work

- Publishing any runner base images to ghcr.io.
- Changing `internal/infraimages/release_manifest.go` or its tests.
- Touching hermes-base (remains pinned per ADR-022).
- Adding a new top-level CLI verb.
- Per-driver configuration of which upstream version to install (operators get whatever the upstream installer picks at refresh time).
- Air-gapped operator support (this model assumes network access to upstream installers).
- Pinning runner versions in any artifact other than the local Docker daemon's tag list.
- Stronger upstream freshness checks (`claw up` comparing local alias against upstream latest). ADR-024 §4 explicitly rejects this as an epistemic over-claim.
