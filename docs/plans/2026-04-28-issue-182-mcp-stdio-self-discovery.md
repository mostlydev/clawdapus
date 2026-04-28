# Issue #182 — Self-discovery for stdio MCP sidecars

**Issue:** https://github.com/mostlydev/clawdapus/issues/182
**Related:** #179 (stdio wrapper landed in v0.12.0), #177 (Streamable HTTP MCP transport in v0.11.0/cllama v0.5.0), [ADR-020](../decisions/020-cllama-compiled-tool-mediation.md).
**Workflow:** Claude drafts → Codex reviews + implements → Claude tests + releases.
**Target release:** clawdapus v0.13.0. cllama is **not** touched.

## 1. Problem

v0.12.0 made `x-claw.describe-file` the primary way to teach `claw up` what tools a stdio MCP sidecar exposes. That's wrong on two axes:

- **MCP self-describes through `tools/list`.** Forcing operators to hand-author the schemas defeats the protocol.
- **It's a UX trap.** Operators forget the field; when they do remember, the snapshot drifts silently as the upstream package updates.

We need `x-claw.mcp-stdio: {command, args}` to be sufficient — *with* a deterministic, reviewable snapshot for compile-time hermeticity per ADR-020.

## 2. Goal

Operators write:

```yaml
services:
  perplexity:
    image: ghcr.io/mostlydev/claw-mcp-stdio:v0.13.0
    environment:
      PERPLEXITY_API_KEY: ${PERPLEXITY_KEY}
    x-claw:
      mcp-stdio:
        command: npx
        args: ["-y", "perplexity-mcp"]
```

…and run a one-shot:

```
$ claw discover
discovered: perplexity (5 tools) → .claw-discovered/perplexity.claw-describe.json
```

`claw up` then compiles `tools.json` from that snapshot. The snapshot is a normal v2 `.claw-describe.json`, tracked in git, diffable on PRs.

`x-claw.describe-file` survives as an explicit override (for operators who want to hand-pin a snapshot the auto-discovery couldn't produce, or for air-gapped builds).

## 3. Snapshot artifact

### 3.1 Location and naming

`<pod-dir>/.claw-discovered/<service>.claw-describe.json`

- One file per stdio sidecar service (clean diffs, easy ownership).
- Hidden directory keeps pod dirs uncluttered.
- Tracked in git by default. If operators want to ignore them, they add to `.gitignore` themselves — but tracking them is the right default because that's how schema drift becomes reviewable.

### 3.2 Shape

A normal v2 service descriptor (same shape `LoadFromFile` already understands), plus a small metadata block at the top so we can detect drift:

```json
{
  "version": 2,
  "x-claw-discovery": {
    "command": "npx",
    "args": ["-y", "perplexity-mcp"],
    "wrapper_image": "ghcr.io/mostlydev/claw-mcp-stdio:v0.13.0",
    "wrapper_image_digest": "sha256:…",
    "discovered_at": "2026-04-28T17:00:00Z",
    "mcp_protocol_version": "2025-11-25",
    "tool_count": 5
  },
  "mcp": {
    "transport": "streamable_http",
    "path": "/mcp"
  },
  "tools": [
    {
      "name": "search",
      "description": "…",
      "inputSchema": { … }
    }
  ]
}
```

`x-claw-discovery` is an `x-`-prefixed key, ignored by the existing descriptor parser (it has no schema for it, so `json.Unmarshal` drops it onto a blank field unless we add it). To be safe, we'll add a typed `XClawDiscovery *DiscoveryMetadata` field to `ServiceDescriptor` with `json:"x-claw-discovery,omitempty"`. The validator ignores it on parse.

### 3.3 Drift detection

Every `claw up`:

- For each stdio sidecar with both pod-yml `mcp-stdio` and a discovered snapshot, compare `command + args` and the wrapper image ref.
- If they differ → soft warning printed during `claw up` and a suggested fix:
  ```
  warning: perplexity: mcp-stdio command changed since discovery. Run `claw discover perplexity` to refresh.
  ```
- This is a warning, not an error. Operators may have legitimate reasons (e.g. they pinned a known-good snapshot; the upstream package added cosmetic changes they don't want to absorb yet).

## 4. Discovery mechanics

### 4.1 `claw discover [<service>...]` — explicit one-shot

```
$ claw discover                  # discover all stdio sidecars in claw-pod.yml
$ claw discover perplexity       # discover one
$ claw discover --pod path/to/claw-pod.yml --refresh   # force re-run even if snapshot exists
```

For each target service:

1. Resolve the wrapper image ref (from `svc.Image`); pull if missing.
2. Resolve env: shallow-merge process env + pod-dir `.env` + the service's `environment:` map (with `${VAR}` expansion).
3. Synthesize the wrapper env: `CLAW_MCP_STDIO_COMMAND`, `CLAW_MCP_STDIO_ARGS` (JSON-encoded), and a freshly-minted `CLAW_MCP_STDIO_AUTH_TOKEN`.
4. Spawn an ephemeral container:
   ```
   docker run --rm -d \
     --name claw-discover-perplexity-<rand> \
     -p 127.0.0.1:<rand-port>:8080 \
     -e CLAW_MCP_STDIO_COMMAND=npx \
     -e CLAW_MCP_STDIO_ARGS='["-y","perplexity-mcp"]' \
     -e CLAW_MCP_STDIO_AUTH_TOKEN=<token> \
     -e PERPLEXITY_API_KEY=… \
     ghcr.io/mostlydev/claw-mcp-stdio:v0.13.0
   ```
5. Poll `GET /healthz` until 200 (with a configurable readiness timeout, default 60s — covers npx package fetch).
6. POST `initialize` → POST `notifications/initialized` → POST `tools/list` against `/mcp`. Capture `MCP-Session-Id` per the spec.
7. Convert MCP `tools/list` response into v2 `.claw-describe.json`: each entry's `name`, `description`, `inputSchema` flow through 1:1; the descriptor gets `mcp: { transport: streamable_http, path: /mcp }` and the `x-claw-discovery` metadata block.
8. Write `<pod-dir>/.claw-discovered/<service>.claw-describe.json` (creating the directory if absent). Atomic rename via tmpfile.
9. Tear down the container; report what changed.

If discovery fails (image unavailable, child crashes, `tools/list` errors), print the wrapper container's stderr (we already capture it) and exit nonzero. Don't write a partial snapshot.

### 4.2 Discovery client

A small new package `internal/mcpdiscover/` with:

- `Client` — thin Streamable HTTP MCP client. Just `initialize` + `notifications/initialized` + `tools/list`. ~150 lines.
- `Convert(toolsListResponse) (*describe.ServiceDescriptor, error)` — translates MCP shape into v2 descriptor.
- `Wait(targetURL, timeout)` — readiness poll for `/healthz`.

We don't reuse `cllama/internal/mcp/client.go` because it's in a submodule and importing across the boundary is awkward. The discovery surface is small enough that 100% duplication is fine.

### 4.3 `claw up --discover-tools` — opt-in inline

If `--discover-tools` is set, `claw up` runs `claw discover` for any stdio sidecar that has no snapshot (or whose snapshot is stale per §3.3), *before* descriptor extraction. After discovery the normal compile path runs.

Off by default. Discovery touches the network and spawns containers — keeping it opt-in preserves the principle that `claw up` is hermetic by default.

### 4.4 Resolution order in `claw up`

When resolving a service descriptor (§ `cmd/claw/compose_up.go:resolveServiceMetadata`), check sources in this order, **first match wins**:

1. `x-claw.describe-file` (explicit override) — unchanged from v0.12.0.
2. `<pod-dir>/.claw-discovered/<service>.claw-describe.json` (auto-detected snapshot) — **NEW**.
3. Image label / image filesystem `.claw-describe.json` — unchanged.
4. Build-context filesystem fallback — unchanged.

For stdio sidecars (`svc.IsMCPStdioSidecar()`), if none of (1)–(4) exist, error with:
```
error: service "perplexity": no descriptor found.
  Run `claw discover perplexity` to generate one,
  or set x-claw.describe-file to a hand-authored snapshot.
```

This is the failure-mode improvement: instead of silently treating the service as having zero tools, we direct the operator to the right command.

## 5. CLI surface

New `cmd/claw/discover.go` (keep it small — most logic lives in `internal/mcpdiscover` and a new helper in `cmd/claw/`).

```
claw discover [flags] [service ...]

  Discover MCP tool descriptors for stdio sidecars in a pod and write them
  to .claw-discovered/<service>.claw-describe.json.

Flags:
  --pod <path>            Pod file (default: ./claw-pod.yml)
  --refresh               Re-discover even when a snapshot already exists
  --timeout <duration>    Per-service readiness + discovery timeout (default 90s)
  --json                  Emit a JSON summary of changes
  --keep-container        Don't tear down on success (debug)

Exit codes:
  0  success (or no-op when all snapshots are current)
  1  one or more services failed discovery
  2  pod parse error / no stdio sidecars found
```

`claw up` gains one new flag:

```
  --discover-tools        Run `claw discover` for any stdio sidecar missing
                          a snapshot before compiling.
```

Both update `site/guide/cli.md` and `skills/clawdapus/SKILL.md` (+ regenerated mirror).

## 6. Pod parser changes

Minimal:

- **`x-claw.describe-file`**: stays. Documented as override / pin path. No behavior change.
- **No new pod-yml fields.** The convention-based snapshot path is purely a filesystem convention.

## 7. Compose path

No change. `claw up` still emits the same compose for stdio sidecars (`CLAW_MCP_STDIO_COMMAND/ARGS` env, no read-only fs, on-failure restart). The only delta is *which descriptor source* `resolveServiceMetadata` picks.

## 8. Hermeticity model

Compile-time hermeticity is preserved:

- Default `claw up` reads from cached snapshots only. Zero network round-trips, zero container spawns.
- `claw discover` is the explicit network-touching step. Operators run it intentionally.
- `claw up --discover-tools` is opt-in.
- Snapshots are tracked artifacts; PR review covers tool-schema changes the same way it covers code changes.

This matches the model ADR-020 already specifies for compiled tool mediation.

## 9. Spike test

`cmd/claw/mcp_stdio_discover_spike_test.go` (`-tags spike -run TestSpikeMCPStdioDiscover`):

1. Reuse the harmless `examples/mcp-stdio/echo-server/` fixture from v0.12.0.
2. Build the wrapper image locally (or reuse if present).
3. Run `claw discover` against a pod containing the echo service.
4. Assert `.claw-discovered/echo.claw-describe.json` exists, has the expected `tools[].name == "echo"`, and includes the `x-claw-discovery` metadata block.
5. Run `claw up -d`, drive an agent tool call, assert the audit shows the discovered tool was used.
6. Modify the pod-yml `args` to a different value; run `claw up` and assert the drift warning fires (non-fatal).
7. `claw down`; cleanup.

Hermetic, no creds needed (the echo server doesn't depend on anything).

Manual real-world test: re-run the existing `examples/perplexity-stdio/` example *without* the hand-authored `perplexity.claw-describe.json` (delete it after running `claw discover perplexity`).

## 10. Documentation

- **`site/guide/tools.md`**: rewrite the stdio MCP section. Lead with `x-claw.mcp-stdio` + `claw discover`; demote `describe-file` to "override" subsection.
- **`site/guide/cli.md`**: new `claw discover` reference; new `--discover-tools` flag on `claw up`.
- **`skills/clawdapus/SKILL.md`** + embedded mirror: add `claw discover` to the command table; demote `describe-file` documentation.
- **`README.md`**: one-line callout in the feature list.
- **`docs/decisions/020-cllama-compiled-tool-mediation.md`**: add a Phase 5c status block ("compile-time MCP self-discovery, 2026-04-?, issue #182"). Note the snapshot artifact convention.
- **Update `examples/mcp-stdio/`** to delete the hand-authored descriptor and instead document running `claw discover`. Keep the file as a reference but linked from a "advanced: pinning a snapshot" callout.

## 11. Release impact (v0.13.0)

Minor release. Surface:

- New `claw discover` subcommand.
- New `--discover-tools` flag on `claw up`.
- New `.claw-discovered/` convention (operators don't have to do anything; discovery creates it).
- No new infra image, no Dockerfile changes, no pod-yml field changes.
- No cllama changes; `DefaultCllamaTag` stays at v0.5.0.
- Bump `DefaultClawInfraTag` to v0.13.0 in lockstep with the release-prep commit (clawdash/claw-api/claw-wall/claw-mcp-stdio republish at the new tag for the verifier).

## 12. Build sequence (for codex)

Sliced for review:

**Step 1 — discovery client** (`internal/mcpdiscover/`):
- `client.go`: minimal Streamable HTTP MCP client; `Initialize`, `Initialized`, `ListTools`.
- `convert.go`: `tools/list` → v2 descriptor.
- Unit tests against an `httptest.Server` that simulates the wrapper.

**Step 2 — descriptor metadata field** (`internal/describe/descriptor.go`):
- Add `XClawDiscovery *DiscoveryMetadata` field with `json:"x-claw-discovery,omitempty"`.
- Validator unchanged (the field is informational).
- Tests round-trip a descriptor with the metadata block.

**Step 3 — `claw discover` subcommand** (`cmd/claw/discover.go`, `cmd/claw/discover_helpers.go`):
- Pod parse → enumerate stdio sidecars.
- Per-service: pull image, spawn ephemeral container, wait for `/healthz`, run discovery via `internal/mcpdiscover`, write snapshot atomically.
- Env resolution: pod-dir `.env` + service `environment:` + `${VAR}` expansion.
- Cobra registration.
- Unit tests with a fake docker runner (existing pattern in the repo) and a fake MCP server.

**Step 4 — `claw up` resolution order + drift detection** (`cmd/claw/compose_up.go`):
- New helper `discoveredSnapshotPath(podDir, serviceName)` returns the convention path.
- In `resolveServiceMetadata`, after `explicitDescribeFile` check, fall through to `loadDescriptorFromFile(discoveredSnapshotPath(...))` if it exists.
- Drift check: if pod-yml `mcp-stdio` differs from snapshot's `x-claw-discovery`, print warning.
- Hard-error message when stdio sidecar has no descriptor source.

**Step 5 — `claw up --discover-tools` flag** (`cmd/claw/compose_up.go`):
- New flag; before descriptor collection, run `discoverServices(pod, predicate=missing-or-stale)`.

**Step 6 — example update + spike**:
- Convert `examples/mcp-stdio/` to use auto-discovery (delete the hand-authored descriptor; update README to run `claw discover` first).
- Add `cmd/claw/mcp_stdio_discover_spike_test.go`.

**Step 7 — docs sweep** (per §10) — last.

## 13. Test matrix

```bash
unset GOROOT
go vet ./...
go vet -tags spike ./...
go test ./...
go test -tags integration ./...
go test -tags spike -run TestSpikeMCPStdioDiscover ./cmd/claw/...
go test -tags spike -run TestSpikeMCPStdio ./cmd/claw/...   # regression for #179
```

## 14. Open questions for codex review

1. **Snapshot directory name.** Plan says `.claw-discovered/`. Alternatives: `.claw-tools/`, `claw-discover/` (un-hidden). Hidden + descriptive feels right but flag if you'd prefer otherwise.
2. **Tracked or ignored by default?** Plan says tracked (so PR review covers schema drift). Operators with cred-bearing snapshots can `.gitignore` themselves. Push back if you think the default should flip.
3. **Discovery container env injection.** For wrappers like perplexity-mcp the child needs creds (`PERPLEXITY_API_KEY`). Plan resolves env from pod-dir `.env` + the service's `environment:` map. Is there a simpler primitive already in the repo for this?
4. **Drift detection severity.** Warning vs error when snapshot's `command+args` no longer matches pod-yml. Plan says warning; argue if you'd rather force re-run.
5. **`--discover-tools` implicit on first `claw up`.** Should we be helpful and auto-discover on the very first `claw up` (when no snapshot exists), or always require the explicit `--discover-tools` / `claw discover` step? Plan says explicit; argue if the cost-of-confusion is worse than the cost-of-network-on-up.
6. **Snapshot validation.** Should `claw discover` fail if the new snapshot would lose tools that the existing snapshot listed (potential bug in upstream package)? Plan says no — diffs in PRs catch this.

## 15. Acceptance checklist (against issue #182)

- [ ] A stdio MCP wrapper service can become a managed tool provider without `x-claw.describe-file`.
- [ ] `claw discover` obtains a v2 descriptor snapshot from the MCP server's `tools/list`.
- [ ] `claw up` remains deterministic after discovery; compiled `tools.json` still uses `execution.transport = "mcp"`.
- [ ] Existing #177 Streamable HTTP MCP and #179 explicit `describe-file` flows continue to work.
- [ ] Spike fixture demonstrates discovery from `examples/mcp-stdio/`.

## 16. Out of scope

- Live `tools/list` snapshot at every `claw up` (violates ADR-020).
- HTTP MCP discovery (HTTP MCPs already use `claw.describe`; that path is fine).
- Cllama or wire-level MCP transport changes.
- Discovery for non-MCP descriptor capabilities (feeds, memory, endpoints).
- Snapshot signing / provenance attestation (could be a follow-up if tool-schema integrity becomes a real concern).
