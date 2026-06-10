# Contributing to Clawdapus

Thanks for considering a contribution. This guide covers the dev setup, the workflow, and the few hard rules that keep releases shippable.

## Dev Setup

- Go (see `go.mod` for the required version)
- Docker with Compose (for integration and spike tests)

```bash
git clone https://github.com/mostlydev/clawdapus.git
cd clawdapus
go build -o bin/claw ./cmd/claw
```

The `cllama/` directory is a git submodule with its own repository. A fresh clone leaves it empty; populate it with:

```bash
git submodule update --init
```

End users never need the submodule — infra images are published to ghcr.io.

## Test Tiers

| Tier | Command | Needs |
|------|---------|-------|
| Unit | `go test ./...` | nothing |
| Vet | `go vet ./...` | nothing |
| Integration | `go test -tags integration ./...` | Docker |
| Spike (end-to-end) | `go test -tags spike -run TestSpikeRollCall ./cmd/claw/...` | Docker + real Discord/provider credentials |

Unit and vet must pass on every PR. Run the integration tier when you touch compile/runtime wiring. Spike tests are the heavy validation path — see [TESTING.md](./TESTING.md).

## Issue-First Workflow

All non-trivial work starts with a GitHub issue, prioritized on the [project board](https://github.com/users/mostlydev/projects/2).

1. Find or create the issue. It must carry enough context for someone to pick it up cold: motivation, constraints, key decisions.
2. Move it to **In Progress** when you start.
3. Work on a branch tied to the issue (`issue-123-short-name`).
4. **Put a closing keyword in the PR body** — `Closes #123` / `Fixes #123`. Board automation only links PRs to issues through closing keywords; branch names are not enough. A PR without one strands the issue in the wrong column after merge.

## PR Conventions

- One logical change per PR; group related issues when they ship together (`Closes #1, #2`).
- Update tests in the same area as behavior changes.
- `compose.generated.yml` and `Dockerfile.generated` are build artifacts — never hand-edit them as source.
- Match the surrounding code's style; when fixing a driver bug, check whether the same bug exists in all seven drivers under `internal/driver/`.

## Hands Off: Release Artifacts

Releases are tag-driven and cut by the maintainer. These must **never** change in a feature PR — each one rides a coordinated lockstep across image builds, the `cllama` submodule, GitHub releases, and the published site:

- **Pins in `internal/infraimages/release_manifest.go`** (`DefaultClawInfraTag`, `DefaultCllamaTag`, `DefaultHermesBaseTag`) — these move only with a real release; otherwise the release verifier fails or, worse, ships pins pointing at images that don't match the source tree.
- **The `<Badge type="tip" text="Latest" />` badge or a new version section in `site/changelog.md`** — the site deploys on every master push, so this would publicly claim a release that doesn't exist. Add your notes under `## Unreleased` instead.
- **The version dropdown in `site/.vitepress/config.mts`** — same reason.
- **The `cllama` submodule pointer beyond the latest cllama tag** — if your fix needs new cllama code, a cllama release happens first; the pointer bump rides release prep.

If a fix genuinely requires one of these to move, say so explicitly in the PR body so the maintainer can sequence the release — don't bake it silently into the diff.

One more rule for all public artifacts (changelogs, release notes, issue comments, PR bodies): describe downstream deployments generically ("production deployments", "Hermes runners"), never by name.

## Where to Start

- The [project board](https://github.com/users/mostlydev/projects/2) — column order is priority order.
- `AGENTS.md` — the repo's working knowledge: entry points, gotchas, current behavior.
- [clawdapus.dev](https://clawdapus.dev) — user-facing docs.
