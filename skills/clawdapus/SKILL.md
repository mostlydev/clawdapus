---
name: clawdapus
description: Use when working with the claw CLI, Clawfiles, claw-pod.yml, or deploying AI agent containers with Clawdapus. Use when you see CLAW_TYPE, AGENT, MODEL, CONFIGURE, INVOKE, SURFACE, TRACK, or PRIVILEGE directives.
---

# Clawdapus — `claw` CLI Reference

Infrastructure-layer governance for AI agent containers. `claw` treats agents as untrusted workloads — reproducible, inspectable, diffable, killable.

## Quick Reference

### Install and verify

```bash
go install ./cmd/claw        # from repo checkout
claw doctor                   # check Docker, buildx, compose
```

### Build and inspect

```bash
claw build -t my-image path/to/dir    # Clawfile -> Dockerfile.generated -> docker build
claw inspect my-image                  # show claw.* labels from image
```

### Pod lifecycle (mirrors docker compose UX)

```bash
claw compose -f path/to/claw-pod.yml up -d     # generate compose + launch
claw compose -f path/to/claw-pod.yml ps         # status
claw compose -f path/to/claw-pod.yml logs svc   # stream logs
claw compose -f path/to/claw-pod.yml health     # driver health probe
claw compose -f path/to/claw-pod.yml down       # tear down
```

`-f` locates `compose.generated.yml` next to the pod file. Without `-f`, looks in current directory.

## Clawfile Directives

A Clawfile is an extended Dockerfile. Standard Dockerfile directives pass through unchanged. Claw directives compile to LABELs + generated scripts.

| Directive | What it does | Build vs Runtime |
|-----------|-------------|-----------------|
| `CLAW_TYPE <type>` | Selects runtime driver (e.g. `openclaw`). Not just a label — determines HOW enforcement happens. | Build: label. Runtime: driver selection. |
| `AGENT <file>` | Behavioral contract. **Must exist on host** or startup fails (fail-closed). Mounted read-only. | Build: label. Runtime: `:ro` bind mount. |
| `MODEL <slot> <provider/model>` | Named model slot. Multiple allowed (primary, fallback). | Build: label. Runtime: driver injects into runner config. |
| `SKILL <file>` | Mounts reference markdown into the runner skill directory. | Build: label. Runtime: host path validation + read-only file mount. |
| `CONFIGURE <cmd>` | **Runs at container startup**, NOT build time. Compiled into `/claw/configure.sh` entrypoint wrapper. For init-time mutations against base image defaults. | Build: generates script. Runtime: executes on boot. |
| `INVOKE <cron> <cmd>` | System-level cron in `/etc/cron.d/claw`. Bot-unmodifiable. | Build: writes cron file. |
| `TRACK <pkg-managers>` | Installs package manager wrappers for mutation tracking (apt, pip, npm). | Build: wrapper install. |
| `SURFACE <scheme>://<target>` | Declares infrastructure connections (volumes, services). See Surface Taxonomy below. | Build: label. Runtime: compose wiring. |
| `PRIVILEGE <mode> <user>` | Maps privilege modes (worker, runtime) to user specs. | Build: label. Runtime: Docker user/security. |

## Surface Taxonomy

Surfaces strictly define infrastructure boundaries enforced by Clawdapus via compose generation.

| Scheme | Enforcement |
|--------|------------|
| `volume://<name>` | Compose volume mount with `:ro`/`:rw` |
| `host://<path>` | Compose bind mount with access mode |
| `service://<name>` | Compose networking — pod-internal reachability |

For `service://<name>`, Clawdapus also mounts a service skill when available:

- Primary source: `claw.skill.emit` label in the target service image (path inside image).
- Fallback: generated `skills/surface-<name>.md` with hostname + discovered port hints.

> **Note on Application Integrations:** External services (like Discord, Slack, external APIs) are *not* Surfaces. They are configured natively by the runner via `CONFIGURE` directives and standard Docker `environment` variables.

## Skill Mounting Semantics

- `SKILL` directives in Clawfile become `claw.skill.N` labels on the image.
- `x-claw.skills` in `claw-pod.yml` merges with image skills during compose-up.
- Image-level skills and pod-level skills are merged by basename:
  - pod-level same-basename entry replaces image-level entry
  - duplicate basenames across either layer fail validation before startup
- Each file is bound individually into the runner's skill directory, read-only, so runner-owned skill files remain writable.
- `service://` targets can contribute one auto-mounted skill path: `skills/surface-<name>.md`.
- If a service image includes `claw.skill.emit`, that extracted skill is used for `surface-<name>.md`.
- If no emitted file is available, Clawdapus writes a minimal fallback stub so the target is discoverable from CLAWDAPUS and the skills index.
- Operator-provided `surface-<name>.md` entries replace service-emitted content for the same path when present.

## claw-pod.yml

Extended docker-compose. Claw config lives under `x-claw:` (ignored by plain docker compose).

```yaml
x-claw:
  pod: my-pod                    # pod-level

services:
  my-agent:
    image: my-claw-image
    x-claw:                      # service-level
      agent: ./AGENTS.md         # host path, mounted :ro
      surfaces:
        - "service://fleet-master"
    environment:                  # standard compose, NOT x-claw
      DISCORD_TOKEN: ${DISCORD_TOKEN}
```

Credentials go in standard compose blocks (`environment:`, `secrets:`), never in `x-claw`.

## Fail-Closed Semantics

Clawdapus refuses to start containers when:
- `AGENT` file doesn't exist on host
- Driver preflight fails (config can't be applied)
- Driver post-apply fails (enforcement can't be verified)
- Unsupported surface scheme for the driver

This is by design. If enforcement can't be confirmed, the container doesn't run.

## Architecture Key Points

- **`claw build`** transpiles Clawfile to standard Dockerfile, calls `docker build`. Output is standard OCI image.
- **`claw compose up`** parses `claw-pod.yml`, runs driver enforcement, emits `compose.generated.yml`, calls `docker compose`.
- **docker compose** is the sole lifecycle authority. Docker SDK is read-only (inspect, logs, events).
- **Drivers** translate WHAT (Clawfile declares) into HOW (runner-specific enforcement). OpenClaw driver uses Go-native JSON5 patching, not repeated CLI shellouts.
- **Generated files** (`Dockerfile.generated`, `compose.generated.yml`) are build artifacts — inspectable, auditable, not hand-edited.
