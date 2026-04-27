# ADR-024: Runner Base Refresh from Upstream Sources

**Date:** 2026-04-09
**Status:** Draft
**Amends:** ADR-022 §3 (build-time runner base freshness moves from `claw build` to `claw pull`, narrowly)
**Depends on:** ADR-010 (CLI Surface Simplification), ADR-022 (Infrastructure Image Lifecycle and the Four-Verb Operator Surface)
**Implementation:** `docs/plans/2026-04-09-128-runner-update-from-upstream.md`

## Context

ADR-022 fixed runtime infra freshness with pinned, published images on `ghcr.io/mostlydev`. That model does not extend to built-in runner bases (`openclaw`, `microclaw`, `nullclaw`, `nanobot`, `picoclaw`, `nanoclaw-orchestrator`):

- Each driver's `BaseImage()` (`internal/driver/<driver>/baseimage.go`) returns an inline Dockerfile and a synthetic local tag like `openclaw:latest`.
- `internal/build/build.go:ensureBaseImage` only auto-builds the base image when it is missing locally (`build.go:63`). Once built, the local tag is treated as authoritative indefinitely.
- `claw pull` skips `build:` services entirely (ADR-022 §3, "without exception"), so it never refreshes runner bases.
- `claw build` runs `docker build` without `--pull` or `--no-cache` (`build.go:131`), so even rebuilding a Clawfile reuses any stale runner base layer that already exists.
- `docker pull openclaw:latest` fails with `repository does not exist` because `openclaw:latest` is not a registry artifact.

The operator question — *"how do I get the latest OpenClaw onto Tiverton?"* — has no good answer in current `claw`. This already bit the live trading pod.

An alternative direction would close the gap by introducing an explicit `claw runners update` top-level verb. That direction is internally defensible (runner refresh is mechanically a local build, not a network pull, so the verb should match the operation). This ADR proposes a different direction.

## The trust argument against publishing runner bases

ADR-022's published infra surface (`cllama`, `claw-api`, `clawdash`, `claw-wall`, `hermes-base`) consists of *mostlydev source code* compiled into images. Trusting `ghcr.io/mostlydev/cllama` is the same trust decision as trusting the Go source in this repo — there is no third-party software involved that the operator might want to verify independently.

Built-in runner bases are different. Each one packages an upstream third-party harness:

| Driver | Upstream package |
|---|---|
| openclaw | `https://openclaw.ai/install.sh` |
| microclaw | upstream microclaw distribution |
| nullclaw | upstream nullclaw distribution |
| nanobot | upstream nanobot distribution |
| picoclaw | `docker.io/sipeed/picoclaw` |
| nanoclaw-orchestrator | `https://github.com/qwibitai/nanoclaw.git` |

If mostlydev publishes `ghcr.io/mostlydev/openclaw-base:v0.5.2`, an operator who installs OpenClaw via that path is trusting *mostlydev's repackaging* of OpenClaw. They have to take it on faith that the bytes inside the published image are exactly what the upstream installer would produce. Some operators will not want to grant that trust — and they should not have to, because the recipe is short, fully described in `baseimage.go`, and trivially reproducible from upstream sources on the operator's own machine.

The trust model that fits this surface is **the Homebrew model**, not the registry model:

- Mostlydev ships the *recipe* (the inline Dockerfile in `baseimage.go`).
- The operator's machine builds the runner base locally from upstream sources.
- The result is provably "what upstream said today, plus a thin mostlydev integration shim that the operator can audit in the recipe."

Hermes-base remains pinned and published per ADR-022 §3, deliberately. Hermes-base is a mostlydev compatibility-shim image (`patch-hermes-runtime.py` in `dockerfiles/hermes-base/` is mostlydev source code), so trusting our published image is identical to trusting our Go source. The trust argument above does not apply.

## Relationship to ADR-022

This ADR **amends ADR-022 §3 narrowly**. ADR-022 §3 says:

> Build-time base images stay with `claw build`. ... Services with `build:` blocks are skipped by `claw pull`, without exception.

That rule was justified by an anti-collision concern: preventing a registry `docker pull` from silently overwriting a locally built service image that happens to share a tag. **The anti-collision rationale is preserved intact** by this ADR, because runner-base refresh is a `docker build` of a recipe producing a local tag that never exists in any registry — there is no collision surface to begin with.

What changes: `claw pull` now refreshes the *build-time runner base* of a pod's `build:` services, while still leaving the service images themselves to `claw build`. The split becomes:

- **`claw pull`** — pinned runtime infra (unchanged) + runner base refresh (new, narrow amendment)
- **`claw build`** — pod service image compilation (unchanged)
- **`claw up`** — authority on staleness (unchanged semantics, enriched drift signal)

The alternative would be introducing a new top-level verb (`claw runners update`), which preserves ADR-022 §3 verbatim but breaks ADR-022 §2's explicit "no new top-level verbs are introduced" promise. This ADR chooses to amend §3 narrowly rather than expand §2. Both are amendments; the choice is between amending §2 (verb surface) or §3 (pull scope). Amending §3 keeps the operator-visible surface smaller, so we choose it.

## Decision

### 1. Runner bases are built locally from upstream, never pinned by Clawdapus

The six synthetic-tag drivers (`openclaw`, `microclaw`, `nullclaw`, `nanobot`, `picoclaw`, `nanoclaw-orchestrator`) keep their inline `BaseImage()` Dockerfiles. Clawdapus does not publish runner base images for these drivers and does not pin upstream runner versions in the binary's release manifest.

`hermes-base` continues to be pinned and published per ADR-022 §3, as the deliberate exception.

### 2. `claw pull` becomes the runner base refresh verb, with an explicit mode matrix

`claw pull` is the sole verb for runner base freshness. Its input modes are:

| Invocation | Behavior |
|---|---|
| `claw pull --file <pod>` or `claw pull <pod>` (positional) | **Pod mode**: pull pinned infra + refresh runner bases needed by the pod's `build:` services |
| `claw pull --no-runners --file <pod>` or `claw pull --no-runners <pod>` | **Pod mode without runner refresh**: pull pinned infra and registry service images only |
| `claw pull` with `claw-pod.yml` in cwd (no args) | **Pod mode via auto-resolution** — unchanged from current behavior (`cmd/claw/image_lifecycle.go:137`) |
| `claw pull <clawfile-path-or-dir>` | **Single-Clawfile mode**: resolve the input with the same single-Clawfile rules as `claw build <path>`, then refresh that driver's runner base |
| `claw pull` with no args and no pod in cwd | **Bare mode**: core infra pull (unchanged) + refresh any *locally-tagged* managed runner aliases |
| `claw pull --no-runners` with no args and no pod in cwd | **Bare mode without runner refresh**: core infra pull only |

Disambiguation preserves the repo's existing authoring behavior rather than narrowing it. `--file` remains pod-only. Positional `.yml`/`.yaml` inputs stay pod mode. Any other positional input is resolved using the same single-Clawfile rules that `claw build <path>` already uses: directories resolve to `<dir>/Clawfile`, filenames starting with `Clawfile` (including flat-layout names like `Clawfile.westin` and example files like `Clawfile.nanoclaw`) are treated as Clawfiles, and other custom paths continue to work if they already pass Clawfile detection. This avoids breaking existing flat-layout projects and custom-named Clawfiles just to support runner refresh.

The bare mode's "refresh locally-tagged managed aliases" is deliberately lazy: on a fresh machine with no runner aliases locally, it is a no-op for runner bases (you only refresh what you're already using). This keeps `claw pull` cheap as a sanity-check command and makes single-Clawfile authoring workflows smooth: after refreshing once with `claw pull my.Clawfile`, subsequent `claw pull` invocations from any directory keep that alias current.

Driver names and local Docker aliases are related but not identical. Most map one-to-one (`openclaw` → `openclaw:latest`), but the `nanoclaw` driver maps to the `nanoclaw-orchestrator:latest` alias via `RunnerAlias()`. Operator-facing command selection follows pod/Clawfile driver selection; provenance labels record both the driver name and the local alias.

For each runner driver that `claw pull` refreshes, it:

1. Builds the inline `BaseImage()` Dockerfile into a temporary local tag with `docker build --pull --no-cache`. The `--pull` flag forces Docker to fetch the upstream FROM image (e.g., `node:22-slim`) fresh from Docker Hub. The `--no-cache` flag forces every `RUN` instruction to re-execute, so `curl https://openclaw.ai/install.sh | bash` actually re-runs against the current upstream installer.
2. Runs the driver's *version probe* inside the freshly built image to extract the upstream runner version (e.g., `openclaw 0.5.2` → `0.5.2`).
3. Computes the image ID (`docker inspect --format '{{.Id}}'`) and the recipe SHA (sha256 of the inline Dockerfile content).
4. Tags the temporary result as **both** `<alias>:v<version>` and `<alias>:latest` in the local Docker daemon, in that order.
5. Prints a one-line operator-visible upgrade message: `openclaw: installed v0.5.2 (was v0.5.0)`.

`<alias>:latest` is not overwritten until the build, inspect, and version-probe steps succeed. If the refresh fails halfway through, the previously usable local alias stays intact. Picoclaw is mechanically a wrapper around `FROM docker.io/sipeed/picoclaw:latest`, so its refresh is effectively a fresh upstream Docker pull through the same local-build pipeline.

For drivers that do not implement the version probe, the fallback tag is `<alias>:built-YYYYMMDD-<imageid12>` — the build date plus the first 12 characters of the image ID. The image-ID suffix prevents same-day collisions between multiple refreshes.

### 3. `claw build` rewrites `FROM` and stamps three provenance labels

When `clawfile.Emit` produces `Dockerfile.generated`, it replaces `FROM <alias>:latest` with `FROM <alias>:v<version>` where `<alias>` is a known runner driver alias. The version is resolved from the local `<alias>:latest` tag's sibling version-prefixed tag in `RepoTags`.

`claw build` also injects three labels into the generated Dockerfile:

```dockerfile
LABEL claw.runner.built-against="openclaw:v0.5.2"
LABEL claw.runner.image-id="sha256:abc123def456..."
LABEL claw.runner.recipe-sha="sha256:789abc..."
```

- **`built-against`** — human-readable upstream version tag. Used in operator-facing hint messages. This is what the operator sees when they ask "what version of OpenClaw is this service built against?"
- **`image-id`** — the runner base image's Docker image ID at build time. This is the **strong drift fingerprint**. Two refreshes that produce the same version string but different image IDs (e.g., upstream re-released `0.5.2` with patches, or the same-day fallback built twice) are detected as drift. Drift comparison uses *this label*, not the version string.
- **`recipe-sha`** — sha256 of the inline `BaseImage()` Dockerfile at the moment the base was built. Detects when mostlydev edits the recipe itself (e.g., switches base image from `node:22-slim` to `node:24-slim`). Tertiary metadata; not used for default drift detection, but available to `claw inspect` and future tooling.

The generated artifact is self-describing: `cat Dockerfile.generated` tells the operator exactly which upstream OpenClaw version the service image was built against, and inspecting the service image gives a cryptographically strong provenance trail.

If the local runner base is missing or the version cannot be resolved, `claw build` fails closed with a remediation message that matches the caller's invocation shape:

- Called from a pod context: `run: claw pull -f <pod-file>`
- Called as `claw build <path>`: `run: claw pull <same-path>`

Both of these commands are honored by the mode matrix in §2, so the remediation always leads to a command that can fix the problem.

### 4. `claw up` surfaces drift as a soft hint, using image IDs

When `claw up` validates pod service images, it inspects each one for the `claw.runner.image-id` label and compares it to the current local `<alias>:latest` image ID. If the IDs differ, `claw up` prints:

```
analyst: built against openclaw v0.5.0 (image abc123def456), current is v0.5.2 (image 789abcd...) — consider running: claw build
```

This is informational, not fail-closed. The older runner base is still functionally valid — the operator may have intentionally avoided rebuilding. Service images without any `claw.runner.*` labels (built by older `claw` binaries) produce no drift hint; default `claw up` treats them as not-yet-migrated. `claw up --fix` *will* rebuild them to pick up provenance labels.

**Epistemic boundary.** Clawdapus **cannot honestly know** that the local runner alias is older than upstream latest without an explicit refresh. `claw up` only compares image IDs that are already present locally. If an operator has not run `claw pull` recently, `claw up` will report "no drift" even if upstream has shipped a new release in the meantime. Upstream freshness is an explicit operator action, always gated by `claw pull`. This boundary is deliberate: we do not want `claw up` to probe upstream sources on every pod start, and we do not want it to claim knowledge it does not have.

If the runner base is *missing entirely* (not just drifted), that is a hard failure with the same remediation as ADR-022's strict mode: `run: claw pull`.

### 5. The `BaseImageProvider` interface gains optional siblings

```go
// RunnerBaseProvider is optionally implemented by drivers whose base image is
// built from upstream sources rather than pulled from a pinned registry tag.
// Implementing this interface signals that claw pull should refresh the base
// against fresh upstream sources, and that claw build should rewrite
// FROM <alias>:latest to FROM <alias>:v<version> at emit time.
type RunnerBaseProvider interface {
    BaseImageProvider
    RunnerAlias() string
}

// RunnerVersionProber is optionally implemented by RunnerBaseProvider drivers
// that can report the installed upstream runner version. Drivers that do not
// implement this interface fall back to a build-date-plus-image-ID tag.
type RunnerVersionProber interface {
    RunnerVersionProbe() []string
}
```

Hermes implements neither interface (its base image is pinned per ADR-022). The five synthetic-tag drivers implement `RunnerBaseProvider`, with `RunnerVersionProber` opted in per-driver after the probe is verified against the driver's installed toolchain (see plan §2).

### 6. Contributor-only repackaging must include a versioned local tag

Contributors hacking on `baseimage.go` can still rebuild a runner base manually with `docker build`, but `claw build` now requires a versioned sibling tag so generated Dockerfiles do not keep pointing at mutable `:latest`. A manual escape hatch therefore needs the same shape as `claw pull`: tag the rebuilt image as both `<alias>:latest` and `<alias>:<version-or-built-tag>`, or run `claw pull` after editing the recipe. An alias with only `<alias>:latest` is treated as requiring refresh.

## Consequences

**Positive:**

- Operators get an intuitive refresh verb (`claw pull`) without Clawdapus assuming the role of upstream packager for third-party harnesses.
- The trust boundary is honest: the operator trusts the recipe in `baseimage.go` (auditable Go source) and the upstream installer URL (third-party trust they already accept). Mostlydev never sits between.
- Pod images carry both human-readable version strings and a strong image-ID provenance stamp — the operator can answer "what version?" *and* drift detection survives upstream version-string instability.
- Drift detection works even when upstream re-releases the same version string with different bytes, because comparison is by image ID.
- Same-day fallback tags do not collide, because the fallback tag includes the image-ID suffix.
- No new publishing infrastructure, no new ghcr.io packages, no new manifest entries.
- The four-verb operator surface is preserved (ADR-022 §2). The amendment is entirely contained in `claw pull`'s internals; no new top-level verb.
- The single-Clawfile authoring path works end-to-end: `claw pull my.Clawfile` → `claw build my.Clawfile` → container runs.
- Operators who need a fast infra-only refresh have an explicit escape hatch: `claw pull --no-runners`.

**Negative:**

- `claw pull` becomes slower for pods with `build:` services. A clean OpenClaw rebuild downloads `node:22-slim` and runs the install script — minutes, not seconds. `claw pull --no-runners` preserves the fast pinned-infra path.
- Reproducibility *across* `claw pull` runs is limited by upstream source stability. Two operators running `claw pull` on different days may get different versions. This is the explicit cost of not pinning.
- Runner refresh depends on upstream availability. If `openclaw.ai` is down, `claw pull` fails — but no worse than today, just more visible.
- `clawfile.Emit` couples (loosely) to local Docker state, since it needs the provenance info for FROM rewriting and label injection. Mitigated by passing the resolved provenance as an explicit parameter — the function stays pure if called with nil provenance.

**Amends ADR-022 §3:**

- `claw pull` no longer skips build-time runner bases "without exception" — it refreshes them. The anti-collision rationale of §3 is preserved (no registry collision surface exists for locally built bases) but the letter of the rule is narrowed.

**Breaking / behavioral changes:**

- `Dockerfile.generated` now contains `FROM openclaw:v0.5.2` instead of `FROM openclaw:latest`. Tests in `internal/clawfile/emit_test.go` that assert on literal `:latest` need updating.
- Pod images built before the upgrade lack the three provenance labels. On the next `claw up --fix`, they'll rebuild because the labels are missing. Default `claw up` prints nothing special for unlabeled images (treated as not-yet-migrated) and continues.

**Risks:**

- A driver's version probe could break silently if upstream changes `--version` output format. Mitigated by: (a) the per-driver probe verification step (see plan §2), which locks the parser against a captured sample; (b) the image-ID drift check, which is format-independent and catches changes even if the version parser silently degrades to the fallback tag.
- An operator who runs `claw pull` without `claw build` afterwards leaves their pod images stale relative to the refreshed runner base. The soft hint in `claw up` mitigates this but does not prevent it.

## Migration

There is no publishing pipeline to set up. The migration is entirely in-tree code:

1. Add the `RunnerBaseProvider` and `RunnerVersionProber` interfaces in `internal/driver/types.go`.
2. Implement them in each of the six runner driver `baseimage.go` files, **verifying each probe against the driver's installed toolchain** (plan §2).
3. Add `RefreshRunnerBase` to `internal/build/build.go`.
4. Extend `cmd/claw/pull.go` with the three-mode dispatch (pod / Clawfile / bare) and `--no-runners`.
5. Extend `internal/clawfile/emit.go` to take a runner-provenance struct and rewrite FROM lines + inject three labels.
6. Update `internal/build/build.go:Generate` to resolve provenance from local `docker image inspect` and pass it to emit.
7. Extend `cmd/claw/compose_up.go` to read `claw.runner.image-id` labels and emit the soft drift hint.
8. Update tests: `clawfile/emit_test.go`, `cmd/claw/pull_test.go`, `internal/build/build_test.go`.
9. Update operator-facing docs: `AGENTS.md`, `README.md`, `site/guide/cli.md`, regenerate `cmd/claw/skill_data/SKILL.md`.

The implementation plan in `docs/plans/2026-04-09-128-runner-update-from-upstream.md` walks each step.

## Alternatives Considered

1. **An explicit `claw runners update` verb.** Preserves ADR-022 §3's "pull skips build:" rule verbatim, at the cost of introducing a new top-level verb that breaks ADR-022 §2's "no new top-level verbs" promise. **Rejected** because issue #128 is an operator expectation failure: `claw pull` sounds like the command that should make the platform bits current before `claw build`. A separate command would document that mismatch instead of fixing it.

2. **Publish and pin runner base images.** Internally consistent with ADR-022. **Rejected on the trust argument** — making mostlydev the implicit packaging authority for upstream third-party harnesses is the wrong relationship.

3. **Add a new top-level verb `claw runners refresh`** without subcommand structure. **Same rejection as the explicit `claw runners update` verb above.**

4. **Force `claw build` to always run with `--pull --no-cache`.** Simplest possible change. **Rejected** because it conflates pod-image compilation (fast, frequent) with runner-base refresh (slow, rare).

5. **Single-label provenance via version string only.** Considered in an earlier draft of this ADR. **Rejected** because semver is not content-stable: upstream can re-release the same version with patches, and the same-day fallback tag collides across multiple refreshes. Image-ID comparison is format-independent and collision-free, so we adopt a three-label scheme where the version string is operator-facing and the image ID is the drift fingerprint.

6. **Cache the resolved version in a local state file** (`~/.claw/runner-versions.json`). **Rejected** because Docker already manages the local image state; a parallel state file creates divergence risk.

7. **Stamp the version label inside the Dockerfile during build with `--label`.** **Rejected** because the version is not known until the install completes — `--label` requires the value at `docker build` invocation time. The probe-then-tag-then-relabel-at-emit approach is the correct ordering.

8. **Make `claw pull` with no args refresh *all* managed runner aliases unconditionally.** Considered as a simpler bare mode. **Rejected** because it would force 30+ minutes of refresh on a fresh machine for users who only care about one driver. The "refresh locally-tagged aliases only" bare mode is cheap on fresh machines and honest about what the operator is already using.
