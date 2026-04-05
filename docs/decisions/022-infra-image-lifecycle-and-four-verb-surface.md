# ADR-022: Infrastructure Image Lifecycle and the Four-Verb Operator Surface

**Date:** 2026-04-05
**Status:** Draft
**Related:** ADR-010 (CLI Surface Simplification)

## Context

Operator workflow currently leaks raw Docker commands for first-party infrastructure image maintenance. Examples from live gotchas:

- `docker build -t ghcr.io/mostlydev/claw-api:latest -f dockerfiles/claw-api/Dockerfile .`
- `docker pull ghcr.io/mostlydev/cllama:latest && docker compose -f compose.generated.yml up -d cllama`
- Manual `docker buildx build --platform ... --push` incantations for multi-arch infra images.

These commands appear in CLAUDE.md as tribal knowledge rather than in the operator's mental model of `claw`. An operator who does not know them silently runs against stale infra images with no diagnostic signal.

The root cause is in `cmd/claw/compose_up.go` (`ensureImage()`). The current fallback chain is:

1. local image exists → use it
2. else `docker pull`
3. else build from local Dockerfile
4. else build from git URL

This guarantees an image is **present**. It does not guarantee an image is **correct** relative to the `claw` binary's expectations. The binary and its infra images drift independently because:

- First-party infra images (`claw-api`, `clawdash`, `cllama`, `claw-wall`, `hermes-base`) are published to `ghcr.io` with `:latest` tags
- `ensureImage()` treats any locally present image as authoritative
- No metadata ties a running infra image to a specific `claw` binary release
- `claw doctor` is host-scoped (`docker`, `buildx`, `compose`) and does not inspect infra freshness

Operators are therefore forced to reason about infra image state from outside `claw` — by reading release notes, running raw `docker` commands, or hitting incidents and backtracking.

This violates the project's approachability goal. The operator's mental model should be: **a small, memorable set of verbs, with `claw up` as the authoritative voice on what is stale and what to do about it**. No investigation. No guessing. The flow should flow.

## Decision

### 1. Pin first-party infra images in the `claw` binary

Every `claw` binary is built with a compiled-in manifest of expected first-party infra image tags. **Each image has its own tag namespace — tags do not need to match `claw`'s own semver.** What matters is that the binary knows exactly which tag it was built against.

Example (values are illustrative; the actual manifest is frozen at `claw` build time):

```
claw-api      ghcr.io/mostlydev/claw-api:v0.4.2
clawdash      ghcr.io/mostlydev/clawdash:v0.4.2
cllama        ghcr.io/mostlydev/cllama:v0.2.2
claw-wall     ghcr.io/mostlydev/claw-wall:v0.4.2
hermes-base   ghcr.io/mostlydev/hermes-base:v2026.3.17
```

The binary no longer uses `:latest` for any managed infra image. Cutting a `claw` release freezes the manifest at whatever infra tags are current at that moment; infra images do not have to release in lockstep with `claw`, they just have to have a stable tag the binary can target.

**Freshness policy: tag comparison only.** "Is this image current?" is a deterministic string comparison between the binary's expected tag and what is present locally. If the local image carries the expected tag, it is considered current. No revision inspection, no dirty-flag checks, no heuristics.

### 2. The operator surface is four verbs

```
claw down      tear down
claw pull      fetch pinned infra + pod services' registry images
claw build     compile pod services' local sources (Clawfiles + build: blocks)
claw up        assemble the pod; authoritative on what is stale
```

These four verbs are the complete operator flow. They are memorable, they mirror `docker compose` vocabulary where the analogy is honest, and they map to genuinely distinct operations:

- **down** — symmetric teardown
- **pull** — "get things from the network"
- **build** — "compile my sources"
- **up** — "assemble and start"

No new top-level verbs are introduced. No `claw infra status` / `claw infra sync` subtree. No `doctor` expansion.

### 3. `claw pull` is the sole infra freshness verb

`claw pull` fetches every pinned first-party infra image the binary expects, plus registry image refs for pod services that do not carry a local source. It is pod-aware when a `claw-pod.yml` is present in the cwd, and still useful without one (it can pull the binary's infra manifest on a fresh machine).

**Services with `build:` blocks are skipped by `claw pull`**, without exception. This is critical: today's pod parser (`cmd/claw/compose_up.go:3650`) allows a service to carry both `build:` and `image:` simultaneously, where the `image:` value is the local build target (either user-declared or auto-generated via `managedServiceImageRef()` as `claw-local/<pod>-<svc>:latest`). Pulling those refs would either fail against the registry or — worse — silently overwrite a just-built local image with something from a registry that happens to share the tag. `claw pull` therefore treats the presence of `build:` as an unambiguous signal that the image is owned by `claw build`, and leaves it alone.

The split is clean:

- **`build:` present** → `claw build` owns this image
- **`build:` absent, `image:` present** → `claw pull` owns this image
- **both absent** → pod parse error (existing behavior)

`claw pull` is idempotent and explicit: if everything in its scope is already at the expected tags, it prints a one-line summary and exits. It never silently substitutes a newer tag.

### 4. `claw build` becomes pod-aware

`claw build` today (`cmd/claw/build.go:16`) takes a single optional path and an explicit `-t` tag, compiling one Clawfile at a time. The pod-scoped build machinery lives privately inside `cmd/claw/compose_up.go` (`resolveManagedServiceImage` at line 3650), using auto-generated tags via `managedServiceImageRef()`. That contract split is too narrow to be the remediation for "one of this pod's service images isn't built" — pointing an operator at bare `claw build` would force them to discover which Clawfile, which tag, which context, and how many times to run it.

The contract therefore extends:

- **`claw build`** (no args, with a `claw-pod.yml` in cwd) — scans the pod, builds every service that has a `build:` block (whether the dockerfile is a plain Dockerfile or a Clawfile that compiles down to one), using the pod-scoped tags generated by `managedServiceImageRef()`. This is the form `claw up` points operators at.
- **`claw build <path>`** — today's behavior, unchanged. Compiles a single Clawfile with an optional explicit `-t` tag. Remains the manual authoring tool.

`claw build` still does not touch first-party infra images in either form. Infra images come from `claw pull`; repo-dev rebuilds live in separate dev tooling (out of scope).

Rationale: pod-aware `claw build` is an additive change (every existing invocation keeps working) and preserves the four-verb mental model. The alternative — pointing strict-mode remediation at `claw up --fix` — would force operators to memorize a flag for the common case of "my service image isn't built yet," contradicting the approachability goal.

### 5. `claw up` is authoritative and strict

`claw up` owns the decision tree and emits exactly one prescriptive remediation line when anything is stale or missing:

```
claw-api:v0.4.2 missing locally                  → run: claw pull
cllama:v0.2.2 present but tag mismatch           → run: claw pull
service "analyst" image not built                → run: claw build
```

It never silently pulls, never silently builds, never silently substitutes tags. Read the line, run the suggested command, run `claw up` again. The loop is predictable.

### 6. `claw up --fix` is the escape hatch

`claw up --fix` does whatever pull/build is needed, then starts the pod. This exists for:

- First-run onboarding (where strict mode would otherwise require two manual cycles)
- Operators who consciously opt out of read-and-run

`--fix` is never the default. It is an explicit affordance.

### 7. First-party infra images carry diagnostic-only metadata

Every first-party infra image is stamped at build time with OCI labels:

```
claw.component=<claw-api|clawdash|cllama|claw-wall|hermes-base>
org.opencontainers.image.revision=<git sha>
claw.source=registry|local-checkout
claw.dirty=<true|false>
```

These labels are **diagnostic-only**. `claw up`'s freshness decision is strictly tag comparison (see §1); it does not inspect revisions or dirty flags to override a tag match. The labels exist to support future tooling (e.g. a deeper `claw inspect` view, repo-dev drift warnings) without expanding the operator-visible surface today.

This separation is deliberate: end-user strictness (tag match) and any future repo-dev strictness (revision match) are different modes and should not be conflated. This ADR defines only the end-user policy.

### 8. `claw doctor` stays host-only

`claw doctor` retains its scope: `docker`, `buildx`, `compose` availability on the host. It does not become cwd-sensitive. It does not inspect infra images. It remains the "can this box run claw?" check, runnable on a fresh machine with no pod in sight.

Infra image correctness is `claw up`'s responsibility. Pod coherence is `claw up`'s responsibility. Host sanity is `claw doctor`'s responsibility. Three scopes, no overlap.

## Migration

Current state (verified against `cmd/claw/compose_up.go:3970-4002` and `AGENTS.md:183-189`) uses `:latest` for every managed infra image, and `claw-api` has no registry presence. Transitioning to pinned tags requires:

| Image | Current state | Target state | Blocker |
|---|---|---|---|
| `cllama` | `ghcr.io/mostlydev/cllama:latest`, multi-arch, published from submodule repo | pinned tag from cllama's own version namespace (e.g. `:v0.2.2`) | none — tag namespace already exists |
| `hermes-base` | `ghcr.io/mostlydev/hermes-base:v2026.3.17` (dated), multi-arch | pinned dated tag (already satisfies contract) | none |
| `claw-api` | publication workflow added (`.github/workflows/claw-api-image.yml`), multi-arch, published on master + tags | pinned tag per `claw` release | none — workflow in place, awaiting first tag cut |
| `clawdash` | `ghcr.io/mostlydev/clawdash:latest` | pinned tag per `claw` release | needs versioned-tag publication |
| `claw-wall` | `ghcr.io/mostlydev/claw-wall:latest`, multi-arch | pinned tag per `claw` release | needs versioned-tag publication |

Migration steps:

1. ~~Add `claw-api` publication to the release workflow~~ **Done** — `.github/workflows/claw-api-image.yml` publishes multi-arch images on master pushes and version tags.
2. Add versioned-tag publication for `claw-wall` alongside existing `:latest` (keep `:latest` during transition for existing operators). `clawdash` already emits `type=ref,event=tag` and `type=sha` alongside `:latest`.
3. Introduce the compiled-in infra manifest in the `claw` binary, initially shadowing the `:latest` fallback.
4. Flip `ensureInfraImages` to consult the manifest. Hard-fail only after operators have had a full release cycle to update.
5. Drop `:latest` references and raw `docker build`/`docker pull`/`docker buildx` incantations from operator-facing documentation. The sweep must cover at minimum:
   - **Top-level entry points:** `README.md` quickstart, `site/index.md` hero/CTA flow, `site/guide/quickstart.md`, `site/guide/cli.md` (command surface), `site/guide/what-is-clawdapus.md`.
   - **Adjacent guide pages** that currently lean on raw docker: `site/guide/cllama.md`, `site/guide/clawfile.md`, `site/manifesto.md` (if it cites any), `site/changelog.md` entry for this change.
   - **Example READMEs** that narrate an operator flow: `examples/quickstart/README.md`, `examples/trading-desk/README.md`, `examples/master-claw/README.md`, and any `examples/*/README.md` that walks through pulls/builds.
   - **Agent/contributor context:** `AGENTS.md` (and its `CLAUDE.md` symlink) — remove the `docker pull ghcr.io/mostlydev/cllama:latest` and `docker buildx build ... --push` gotchas, replacing them with the four-verb flow. Mirror into `skills/clawdapus/SKILL.md` and the embedded `cmd/claw/skill_data/SKILL.md` (regenerate via `go generate ./cmd/claw/...`).
   - **Tests that assert on doc content:** `cmd/claw/docs_quickstart_spike_test.go` extracts shell blocks from `README.md` and runs them in a fresh container — the updated quickstart must keep that test green (or the test's extraction targets must move with it).
6. Update the `TESTING.md` operator flow if it cites raw docker commands, and audit `docs/plans/` entries that are still live references (historical plan docs can stay as written).

Until step 4 lands, `:latest` remains the effective default. This ADR describes the target state.

## Consequences

**Positive:**

- Operators learn four verbs and `claw up`'s error messages. Nothing else.
- Raw `docker build`/`docker pull` commands disappear from operator documentation and onboarding.
- "Is this pod running against stale infra?" becomes a question `claw up` can answer deterministically.
- The clawdapus release process pins infra versioning at release time: cutting a `claw` release freezes whatever infra tags are current, without forcing infra images into a shared semver.
- The mental model matches `docker compose` where the analogy is sound (`up`/`down`/`pull`/`build`) and diverges where it should (no `claw run`, no `claw exec` — `claw compose` already covers passthrough).

**Negative:**

- First-run UX requires multiple cycles in strict mode: `claw up` → "run claw pull" → `claw pull` → `claw up` → "run claw build" → `claw build` → `claw up`. Mitigated by documenting `claw up --fix` prominently in onboarding.
- Release process requires freezing an infra manifest per `claw` release. Each infra image must have a stable, published tag at that moment. This is net-positive for correctness but imposes process discipline.
- Repo-dev workflow for contributors hacking on infra images is not addressed by this ADR. Contributors will need dev tooling (out of scope here) to build and test infra images from local Dockerfiles.
- `claw build` gains a pod-aware form. `resolveManagedServiceImage`'s private pod-scoped build logic must either be extracted and called from `claw build`, or `claw build` must call into that same path — either way, the private/public boundary moves.

**Breaking:**

- `ghcr.io/mostlydev/*:latest` tags stop being the reference. Any external docs, compose files, or CI pipelines pinning `:latest` will need to track versioned tags.
- `ensureImage()`'s fallback chain changes semantics: a locally present image with the wrong tag no longer satisfies the binary's expectations.

**Risks:**

- If the infra manifest references tags that haven't been published, `claw pull` will hard-fail for every operator on that binary. Mitigation: CI must verify all manifest tags exist in the registry before publishing a `claw` release.
- `claw-api` publication is a prerequisite for the full model — without it, the migration cannot complete. Step 1 of the migration plan is a hard blocker.
- Operators with air-gapped or restricted registry access may need to mirror the full pinned tag set rather than `:latest`. Acceptable tradeoff.

## Alternatives Considered

1. **Add `claw infra status` and `claw infra sync` subcommands** — introduces new vocabulary outside the four-verb surface. Rejected: adds commands operators have to remember, and `claw up` can be authoritative without a separate diagnostic verb.

2. **Overload `claw build` to also rebuild infra from local Dockerfiles** — would surprise end users and conflate "compile my Clawfile(s)" with "rebuild project infrastructure." Rejected: the repo-dev use case is a minority workflow and can live behind separate dev tooling.

   (Note: `claw build` does extend to become pod-aware for user service images — see §4. That extension is within the "compile my sources" contract; it does not cross into first-party infra.)

3. **Auto-remediate silently in `claw up`** — simplest UX but violates the "no guessing" principle. Operators would lose visibility into what the tool changed on their behalf. Rejected: predictability beats convenience for infra lifecycle.

4. **Expand `claw doctor` to become pod- and infra-aware** — implicitly changes `doctor`'s scope based on cwd, which is exactly the kind of hidden context that makes `ensureImage()` confusing today. Rejected: keep scopes distinct.

5. **Keep `:latest` tags but add a separate freshness manifest file** — preserves the current publishing model at the cost of a parallel versioning system. Rejected: pinning by tag is simpler and more standard.

6. **Make `--fix` the default behavior** — best first-run UX but trains operators to ignore what `claw` is doing on their behalf. Rejected: strict-by-default with an explicit opt-in to auto-remediation matches the project's operator-transparency values.
