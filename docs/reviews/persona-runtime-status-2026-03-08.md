# PERSONA Runtime Status

Date: 2026-03-08

## What PERSONA Actually Does Today

`PERSONA` is now a runtime materialization feature, but it is narrower than the manifesto language implies.

Current behavior:

- `PERSONA <ref>` in a Clawfile is still emitted as `LABEL claw.persona.default=<ref>`.
- `x-claw.persona` in a pod overrides the image-level default.
- During `claw up`, Clawdapus resolves the effective persona ref and materializes it into the service runtime directory at `.claw-runtime/<service>/persona/`.
- Local refs are supported:
  - relative directory paths like `./personas/allen`
  - absolute paths
  - `file://` paths
- Local persona directories are copied into the runtime directory with path-traversal checks and symlink rejection.
- Non-local refs are treated as OCI artifact references and pulled via `oras-go` into the runtime persona directory using Docker credentials when available.
- Drivers mount the resulting persona directory into the runner as a writable workspace and expose its path with `CLAW_PERSONA_DIR` when a persona is actually present.
- Generated `CLAWDAPUS.md` now includes the persona ref and the persona mount location.

What PERSONA does not do today:

- It does not merge persona content into `AGENTS.md`.
- It does not inject persona files into the prompt automatically.
- It does not define or enforce any specific file layout inside the persona directory.
- It does not restore memory/history into runner-native state stores.
- It does not provide snapshotting, syncing back to a registry, or promotion workflows.
- It does not currently have end-to-end tests against a live OCI registry pull path; current automated coverage is local-path based.

## Compared With The Manifesto

The manifesto describes persona as:

- a portable identity layer
- a package containing memory, history, style, and knowledge
- independently swappable from both the runner and the contract
- downloadable from a registry

The implementation now satisfies part of that:

- independent from the contract: yes
- independently swappable at deploy time: yes
- registry-backed in the runtime model: yes, via OCI pull support
- actual identity semantics: no, not by itself

In practice, the code implements persona as a mounted writable directory, not as a fully realized identity system.

## Compared With The Architecture Plan

The architecture plan said:

- build time stores a default persona ref as image metadata
- runtime resolves the ref
- runtime fetches the artifact via `oras`
- runtime bind-mounts it into the container

That is now materially true, with one extension:

- local directory refs are also supported for development and testing, in addition to OCI refs

The gap versus the plan is not fetch/mount anymore. The gap is higher-level lifecycle and runner integration.

## Usefulness

`PERSONA` is useful now, but mostly as infrastructure plumbing rather than as a finished product feature.

Useful today:

- separating mutable identity/workspace content from the immutable behavioral contract
- swapping persona content without rebuilding the image
- sharing a reusable directory of memory/style/reference artifacts across runners
- giving runners and tools a stable filesystem path (`CLAW_PERSONA_DIR`) for persona-scoped state

Not especially useful yet:

- if the runner does not know to read or write that directory
- if operators expect persona alone to change model behavior without any runner-side consumption
- if they expect persistence or registry round-trips beyond initial materialization

Bottom line:

`PERSONA` is now a real runtime mount mechanism. It is useful as a deployment primitive. It is not yet a complete “identity system.”

## Validation Performed

Code paths reviewed:

- `cmd/claw/compose_up.go`
- `internal/persona/materialize.go`
- `internal/driver/openclaw/driver.go`
- `internal/driver/nanoclaw/driver.go`
- `internal/driver/shared/clawdapus_md.go`

Automated tests run:

- `go test ./internal/persona ./internal/driver/openclaw ./internal/driver/nanoclaw ./internal/driver/shared ./cmd/claw`
- `go test ./...`

Important covered cases:

- local persona directories are copied into the runtime directory
- escaping local paths are rejected
- openclaw mounts persona at `/claw/persona`
- nanoclaw mounts persona at `/workspace/container/persona`
- `CLAWDAPUS.md` advertises persona only when mounted

## Recommendation

Docs should describe `PERSONA` as:

- a deploy-time materialized persona workspace
- mounted writable into the runner
- independently swappable from contract and image

Docs should not currently claim:

- automatic memory restoration
- automatic prompt injection
- complete portable identity semantics
- finished registry lifecycle tooling
