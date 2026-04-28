# Base Image Auto-Resolution for All Drivers

**Date:** 2026-03-21
**Status:** Approved

## Problem

Most Clawfiles in-tree use repo-local driver tags such as `openclaw:latest`, `hermes:latest`, `microclaw:latest`, and `nullclaw:latest`, but not all of them. Current exceptions include:

- `examples/picoclaw/Clawfile` using `FROM docker.io/sipeed/picoclaw:latest`
- nanoclaw examples using `FROM nanoclaw-orchestrator:latest`

Today, only the openclaw driver implements `BaseImageProvider`, so only it auto-builds when the local base image is missing. Users of the other six drivers must manually build or pull base images before `claw build` works.

Maintaining Clawdapus-owned pre-built images for every runtime is the wrong burden. Some upstreams already publish official images, some publish installers or packages, and nanoclaw currently needs a Clawdapus-compatible orchestrator wrapper. The implementation needs to follow the packaging reality of each driver instead of forcing one recipe style onto all of them.

## Solution

Each driver ships a `baseimage.go` file implementing `BaseImageProvider`. When `claw build` encounters a missing `FROM` image, the existing `ensureBaseImage()` in `internal/build/build.go` already handles resolution — no changes needed to the build pipeline. We just need to fill in the missing implementations.

Each `baseimage.go` produces a self-contained Dockerfile string. The rule is:

- prefer the real upstream packaging path
- use a local compatibility alias over an official upstream image when that is the upstream distribution model
- use a source-build wrapper only when no stable published base fits the current Clawdapus runtime contract

That keeps the images real, but avoids rebuilding tooling that upstream already ships.

## Per-Driver Dockerfile Recipes

### openclaw (already exists — no changes)

- Base: `node:22-slim`
- Install: `curl -fsSL https://openclaw.ai/install.sh | bash -s -- --no-prompt --no-onboard --method npm`
- Entrypoint: `openclaw gateway --port 18789 --bind loopback`
- Extras: build-essential, python3, cmake (native deps for npm packages)

### hermes

- Tag: `hermes:latest`
- Base: `ghcr.io/astral-sh/uv:python3.11-bookworm-slim`
- Install: clone `https://github.com/NousResearch/hermes-agent` and `uv pip install --system "/opt/hermes-agent[messaging,cron]"`
- Entrypoint: `hermes gateway run`
- Extras: `bash`, `ca-certificates`, `curl`, `git`, `jq`, `procps`, `tini`
- Notes: use the real Hermes repo and install the packaged CLI entrypoint instead of a stub shell script

### nanobot

- Tag: `nanobot:latest`
- Base: `ghcr.io/astral-sh/uv:python3.12-bookworm-slim`
- Install: `uv pip install --system --no-cache nanobot-ai`
- Entrypoint: `nanobot gateway`
- Extras: `ca-certificates`, `curl`, `git`, `jq`, `procps`, `tini`

### picoclaw

- Tag: `picoclaw:latest`
- Base: `docker.io/sipeed/picoclaw:latest`
- Install: none; local base image is a compatibility alias over the upstream image
- Entrypoint: `picoclaw gateway`
- Extras: inherit upstream runtime as-is
- Notes: this keeps the local `picoclaw:latest` tag usable for auto-resolution without forking the upstream container recipe

### nullclaw

- Tag: `nullclaw:latest`
- Base: `ghcr.io/nullclaw/nullclaw:latest`
- Install: none; local base image is a compatibility alias over the upstream image
- Entrypoint: inherited upstream `nullclaw gateway --port 3000 --host ::`
- Extras: inherit upstream runtime as-is
- Notes: this avoids reimplementing the upstream Zig build and preserves the current HOME/config behavior the driver already targets

### microclaw

- Tag: `microclaw:latest`
- Base: `ghcr.io/microclaw/microclaw:latest`
- Install: add `procps` and create `/app/config` for the file mount used by the driver
- Entrypoint: `microclaw start`
- Extras: inherit upstream runtime plus `procps`
- Notes: the upstream image is the real runtime; the only local change is making the image satisfy Clawdapus healthcheck and mount assumptions

### nanoclaw

- Tag: `nanoclaw-orchestrator:latest`
- Builder: `node:22-bookworm-slim` + clone `https://github.com/qwibitai/nanoclaw.git` + `npm ci && npm run build`
- Runtime: `node:22-bookworm-slim` + Docker CLI copied from `docker:27-cli`
- Entrypoint: `node /workspace/dist/index.js`
- Extras: `ca-certificates`, `git`, `procps`, `python3`, `make`, `g++`, `tini`, global `@anthropic-ai/claude-code`
- Notes: nanoclaw is the one driver that still needs a source-build compatibility image because Clawdapus expects an orchestrator container with Docker CLI access and Claude Code SDK wiring

## File Layout

### New files (one per driver)

- `internal/driver/hermes/baseimage.go`
- `internal/driver/nanobot/baseimage.go`
- `internal/driver/nanoclaw/baseimage.go`
- `internal/driver/nullclaw/baseimage.go`
- `internal/driver/microclaw/baseimage.go`
- `internal/driver/picoclaw/baseimage.go`

### New test files (one per driver)

- `internal/driver/<name>/baseimage_test.go` — same pattern as openclaw: assert interface satisfaction, non-empty tag/dockerfile

### No changes needed

- `internal/build/build.go` — `ensureBaseImage()` already handles the resolution
- `internal/driver/types.go` — `BaseImageProvider` interface already exists
- `internal/driver/openclaw/baseimage.go` — already correct
- Rollcall `Dockerfile.*-base` files — stay as spike test fixtures

### Adjacent doc cleanup

- fix stale Hermes upstream references to point at `https://github.com/NousResearch/hermes-agent`

## Follow-up

- optional cleanup: decide later whether to standardize `examples/picoclaw/Clawfile` on local `picoclaw:latest`; the current fully qualified upstream image still works via Docker's normal pull path
