# Issue #179 — First-class stdio MCP wrapper for managed MCP sidecars

**Issue:** https://github.com/mostlydev/clawdapus/issues/179
**Related:** #177 (Streamable HTTP MCP transport landed in v0.11.0 / cllama v0.5.0)
**Workflow:** Claude drafts → Codex reviews + implements → Claude tests + releases.
**Target release:** clawdapus v0.12.0 (cllama is **not** touched in this scope).

## 1. Problem and goal

After #177, agents can call MCP tools through cllama as long as the MCP server
speaks **Streamable HTTP** at a pod-internal endpoint. Many useful MCP servers
(perplexity-mcp, filesystem, sqlite, the npx-installable ecosystem) are
distributed as **stdio commands**. Today, dropping one into a Clawdapus pod
requires hand-rolling a stdio→HTTP adapter per pod.

**Goal:** make stdio MCP wrapping first-class. Operators declare a stdio
command and Clawdapus mediates it through the existing v0.11.0 cllama MCP
transport — no bespoke per-pod glue.

**Non-goal in this scope:**
- Live `tools/list` discovery during `claw up` (stays compile-time hermetic
  per ADR-020). Operators provide a baked descriptor.
- Touching cllama. The wrapper exposes the *same* Streamable HTTP MCP surface
  cllama already speaks; cllama needs zero changes.
- Any change to the existing `tools[].http` HTTP managed-tool path.

## 2. Operator surface (target shape)

```yaml
services:
  perplexity:
    image: ghcr.io/mostlydev/claw-mcp-stdio:v0.12.0       # the new shared wrapper image
    environment:
      PERPLEXITY_API_KEY: ${PERPLEXITY_KEY}                # stdio creds stay here
    volumes:
      - ./perplexity.claw-describe.json:/.claw-describe.json:ro
    x-claw:
      mcp-stdio:
        command: npx
        args: ["-y", "perplexity-mcp"]

  allen:
    image: ghcr.io/mostlydev/hermes-base:v2026.3.17-claw.3
    x-claw:
      agent: allen
      cllama: [openai]
      tools:
        - service: perplexity
          allow: [search]
```

Why this shape:

- **One shared wrapper image** (`ghcr.io/mostlydev/claw-mcp-stdio`), versioned
  in lockstep with clawdapus releases (same `DefaultClawInfraTag`). No
  per-package image to maintain.
- **`x-claw.mcp-stdio` is the *only* new pod surface** — a pure declarative
  block that names the child command + args. `claw up` translates it into
  env vars on the wrapper container. Nothing else in the pod parser changes.
- **The descriptor is operator-supplied via volume mount or COPY**, exactly
  like any other v2 `claw.describe`. `mcp.transport: streamable_http` +
  `path: /mcp` already work in v0.11.0; the wrapper image ships zero
  descriptor by default so operators must supply tools[]. This makes
  `claw up` deterministic — there's no live tools/list probe.
- **No new agent-side syntax.** Subscribing agents use the existing
  `x-claw.tools: [{ service, allow }]` block from #177.
- **Stdio creds stay in the wrapper service env.** Agent containers never
  see them.

Operators write *one* image ref + *two* fields (`command`, `args`) + their
own descriptor file. That's the entire delta vs a normal MCP HTTP sidecar.

## 3. Architecture: the `claw-mcp-stdio` wrapper image

### 3.1 Composition

A small Go binary (`cmd/claw-mcp-stdio/`) packaged in a Node-base image so
`npx`-style commands work out of the box. Single static binary as ENTRYPOINT.

```dockerfile
FROM golang:1.24-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /claw-mcp-stdio ./cmd/claw-mcp-stdio

FROM node:20-bookworm-slim
RUN apt-get update && apt-get install -y --no-install-recommends \
      ca-certificates tini python3 \
    && rm -rf /var/lib/apt/lists/*
COPY --from=build /claw-mcp-stdio /usr/local/bin/claw-mcp-stdio
EXPOSE 8080
HEALTHCHECK --interval=15s --timeout=5s --retries=3 \
  CMD ["/usr/local/bin/claw-mcp-stdio", "-healthcheck"]
ENTRYPOINT ["tini", "--", "/usr/local/bin/claw-mcp-stdio"]
```

**Base choice rationale:** `node:20-bookworm-slim` (~150 MB) covers the
dominant MCP distribution channel (npm + npx). `python3` is added for the
small but real subset of stdio MCP servers shipped via `uvx` / `python -m`.
Operators who need a heavier base build their own image; the shared image
covers the common case.

### 3.2 Configuration (env-driven)

Read at process startup:

| Env var                              | Default         | Meaning |
|--------------------------------------|-----------------|---------|
| `CLAW_MCP_STDIO_COMMAND`             | (required)      | Executable to spawn (e.g. `npx`). |
| `CLAW_MCP_STDIO_ARGS`                | `[]`            | JSON-encoded array of args. |
| `CLAW_MCP_STDIO_PORT`                | `8080`          | HTTP listen port. |
| `CLAW_MCP_STDIO_PATH`                | `/mcp`          | HTTP path that exposes the MCP endpoint. |
| `CLAW_MCP_STDIO_READY_TIMEOUT_MS`    | `30000`         | How long to wait for child `initialize` reply before failing readiness. |
| `CLAW_MCP_STDIO_RESTART_BACKOFF_MS`  | `1000`          | Initial restart backoff after child exit. |
| `CLAW_MCP_STDIO_RESTART_MAX_MS`      | `15000`         | Max restart backoff cap. |
| `CLAW_MCP_STDIO_AUTH_TOKEN`          | (empty = open)  | If set, requires `Authorization: Bearer <token>` on requests. |
| `CLAW_MCP_STDIO_REQUEST_TIMEOUT_MS`  | `60000`         | Per-request roundtrip timeout. |

Child process inherits the wrapper container's full env (so
`PERPLEXITY_API_KEY` etc. flow through).

### 3.3 Lifecycle

**Single shared child.** One stdio MCP process. The wrapper multiplexes HTTP
JSON-RPC requests by their `id` field — every server in the wild already
handles concurrent IDs since this is how MCP works.

```
                                    ┌────────────────────────┐
  cllama ──HTTP/JSON-RPC──▶ wrapper │  spawn(command, args)  │
                                    │   ↕ stdin/stdout NDJSON │
                                    │   stdio MCP child       │
                                    └────────────────────────┘
```

Startup sequence:

1. Read env, parse args.
2. `exec.Command(...)` with stdin/stdout pipes; stderr → wrapper stdout
   (so `claw logs <wrapper-svc>` shows everything).
3. Start a stdout reader goroutine: parse newline-delimited JSON-RPC frames,
   route by `id` to a pending-request map.
4. Send `initialize` (forwarded later from the HTTP side, or pre-warmed —
   see §3.4) and wait for the initialized-ack.
5. Listen on `:PORT`, accept `POST /mcp` requests.

If the child exits, restart it with exponential backoff up to
`CLAW_MCP_STDIO_RESTART_MAX_MS`. Pending requests during a restart get a
JSON-RPC error response (`code: -32000`, `message: "stdio child restarting"`)
so cllama returns a clean tool-error envelope instead of timing out.

### 3.4 HTTP ↔ stdio bridge

For each `POST /mcp`:

1. Authenticate (bearer if `CLAW_MCP_STDIO_AUTH_TOKEN` set).
2. Read the JSON-RPC request body. Capture its `id`.
3. Create a pending-channel keyed on `id`.
4. Write the request as one newline-terminated JSON line to child stdin.
5. Wait on the channel (with `CLAW_MCP_STDIO_REQUEST_TIMEOUT_MS` deadline).
6. Write the response back as `application/json`. (No SSE in v1; cllama's
   client already accepts JSON — see `cllama/internal/mcp/client.go`.)

**Session handling.** The cllama client already manages MCP session IDs and
sends `Mcp-Session-Id` headers; the wrapper just needs to **mint and echo**
session IDs on `initialize`, store them in memory, and validate them on
subsequent calls. A single shared child means all sessions share the same
tool universe — that's correct, since the descriptor is one snapshot of one
MCP server. Session IDs are still useful for the cllama-side retry-on-expiry
behavior (forces a re-initialize when the wrapper restarts).

For the v1 implementation a single in-memory session map is fine. Wrapper
restart drops the map — cllama's "retry once on session expiry" path
(`cllama/internal/mcp/client.go`) already handles this exact case from the
#177 work.

### 3.5 Health and readiness

`-healthcheck` flag returns 0 if a separate companion HTTP probe at
`GET /healthz` returns 200. Readiness flips green only after the wrapper has
successfully completed one round-trip with the child (lazy, on first
incoming request) **or** after a configurable warmup `initialize` succeeds.
Default: lazy — keeps cold start cheap and avoids racing npx package fetch.

## 4. Pod parser additions (`internal/pod`)

Minimal additions, all isolated to the service-level x-claw block.

### 4.1 New types

```go
// internal/pod/types.go
type ClawBlock struct {
    // ...existing fields...
    MCPStdio *MCPStdioBlock // nil unless x-claw.mcp-stdio is declared
}

type MCPStdioBlock struct {
    Command string
    Args    []string
}
```

### 4.2 Raw parser extension

```go
// internal/pod/parser.go
type rawClawBlock struct {
    // ...existing fields...
    MCPStdio *rawMCPStdio `yaml:"mcp-stdio"`
}

type rawMCPStdio struct {
    Command string   `yaml:"command"`
    Args    []string `yaml:"args"`
}
```

In the existing service-loop (around `parser.go:185+`), parse and validate:

- `command` is required, must be non-empty after trim.
- `args` defaults to `[]`.
- Reject `mcp-stdio` if the service ALSO declares `x-claw.agent` (these are
  agent-runner directives — mcp-stdio services are tool sidecars, not
  agents). This prevents accidental conflation.
- Reject `mcp-stdio` if `x-claw.cllama` is set (same reason).
- `count > 1` is rejected for mcp-stdio services in v1 (single shared child
  per service; horizontal scaling of stdio MCP is out of scope and has
  ambiguous semantics).

### 4.3 Tests to add (`internal/pod/parser_mcp_stdio_test.go`)

- happy-path: `command + args` parses into `Service.Claw.MCPStdio`.
- empty `command` → error.
- mcp-stdio + agent → error.
- mcp-stdio + cllama → error.
- mcp-stdio + count > 1 → error.

## 5. Compose emission (`internal/pod/compose_emit.go`)

When `Service.Claw.MCPStdio != nil`, inject env vars on the compose service
output:

- `CLAW_MCP_STDIO_COMMAND=<command>`
- `CLAW_MCP_STDIO_ARGS=<json-encoded args array>`

Implementation hook: existing emission already merges `Service.Environment`
into the compose `environment:` map. Add a small helper
`mcpStdioEnv(*MCPStdioBlock) map[string]string` and merge it after user env
(so user env overrides if they really want to).

**Don't** force the wrapper image. Operators choose their image — they may
extend the shared base with extra deps (e.g. their own corp CA). The pod
parser is concerned with *behavior* (env wiring), not *image identity*.

### 5.1 Tests

`internal/pod/compose_emit_mcp_stdio_test.go`:

- mcp-stdio block → emitted env contains `CLAW_MCP_STDIO_COMMAND` and
  `CLAW_MCP_STDIO_ARGS` JSON-encoded.
- args with shell metacharacters round-trip safely (JSON-encoded, not
  shell-interpolated).
- user-provided `CLAW_MCP_STDIO_*` env wins (last write wins), so escape
  hatches stay open.

## 6. Compile path (already done in v0.11.0 — verify no regression)

No changes needed in `cmd/claw/compose_up.go` for the manifest path. The
existing `buildToolManifestEntries` already emits
`execution.transport = "mcp"` when a `claw.describe` v2 descriptor declares
`mcp:` and tools without `http:`. The wrapper service's mounted
`.claw-describe.json` flows through `loadDescriptorFromImage` (image label
flow) or `loadDescriptorFromBuildCtx` (build-context flow) — both work
unchanged.

**Verify (no code change):** the descriptor extraction path picks up the
mounted `/.claw-describe.json` at runtime. Today, `LoadFromImage` reads
labels + image fs. A bind-mounted descriptor file is *runtime*, not image
metadata, so `claw up` won't see it through the image-inspect path.

**This is the one design subtlety**: how does `claw up` learn the wrapper
service's descriptor?

Three options, in order of preference:

a) **Explicit `x-claw.describe-file` pod field** pointing at a host file:
   ```yaml
   x-claw:
     mcp-stdio: { command: npx, args: ["-y", "perplexity-mcp"] }
     describe-file: ./perplexity.claw-describe.json
   ```
   `claw up` reads it during descriptor collection. Build-context-style
   resolution. Simple, explicit, no Docker plumbing needed.

b) **Convention: descriptor co-located with pod** — look for
   `<pod-dir>/.claw-describe.<service-name>.json` automatically. Magic, but
   zero new pod surface.

c) **Bake into a per-package wrapper image** — operators publish their own
   image extending `claw-mcp-stdio` with the descriptor baked in via COPY.
   Heavyweight, defeats the "just drop in" goal.

**Recommendation: (a).** Add `x-claw.describe-file: <relative path>` to the
service block. In `cmd/claw/compose_up.go:resolveServiceMetadata`, before
falling through to image/build-context inspection, check for the
service-level describe-file and `LoadFromBuildContext`-equivalent it. This
keeps it deterministic, visible in pod YAML, and reuses existing descriptor
extraction logic.

This is a small extension to `internal/pod` (one new field, one parser
line) and one new code path in `compose_up.go`.

## 7. Hermetic spike test

### 7.1 Test fixture: `mcp-echo-stdio`

Tiny stdio MCP server in `examples/mcp-stdio/echo-server/server.js` (or
`.py` — whichever is shorter). ~50 lines. Implements:

- `initialize` → returns minimal capabilities, echoes protocol version.
- `tools/list` → returns one tool `echo` with `{message: string}` schema.
- `tools/call` `echo` → returns content blocks with the input message.

No network, no external deps beyond the runtime present in the wrapper
base image. This means it can run in `TestSpikeMCPStdio` without secrets.

### 7.2 Spike test: `cmd/claw/mcp_stdio_spike_test.go`

`go test -tags spike -run TestSpikeMCPStdio ./cmd/claw/...`

Mirrors `TestSpikeRollCall`'s structure:

1. `claw build && claw up -d` on `examples/mcp-stdio/`.
2. Trigger the agent to make one tool call.
3. Assert the cllama session-history line for that turn carries
   `tool_trace[].transport == "mcp"`.
4. Assert `claw audit` JSON shows the tool call attributed to the right
   agent.
5. `claw down`.

This proves end-to-end: stdio child → wrapper HTTP → cllama MCP client →
agent → tool call → audit trail. If this passes, perplexity-mcp will work.

### 7.3 Manual real-world example

`examples/perplexity-stdio/claw-pod.yml` with a comment block at the top
documenting the `PERPLEXITY_KEY` env var. Not run in CI, but a copy-pasteable
template for operators.

## 8. Documentation

- **`site/guide/tools.md` (or new `site/guide/mcp-stdio.md`):** add a
  "Wrapping a stdio MCP server" section. Show the perplexity example and
  the descriptor file shape.
- **`site/guide/cli.md`:** mention the new `mcp-stdio` x-claw field in the
  pod-yml reference.
- **`skills/clawdapus/SKILL.md`** (and regenerated mirror at
  `cmd/claw/skill_data/SKILL.md`): document `x-claw.mcp-stdio` + the
  `claw-mcp-stdio` image so agents using the skill can guide operators.
- **`README.md`:** one-line callout in the feature list.
- **`docs/decisions/020-cllama-compiled-tool-mediation.md`:** add a Phase 5b
  status section noting stdio wrapper added in v0.12.0 (issue #179).

## 9. Release impact

This is a **minor** release (0.12.0):

- New shared infra image `ghcr.io/mostlydev/claw-mcp-stdio` versioned at
  `DefaultClawInfraTag`. Add to `internal/infraimages/release_manifest.go`:
  ```go
  DefaultClawMCPStdioTag = DefaultClawInfraTag
  // ReleaseRefs:
  fmt.Sprintf("ghcr.io/mostlydev/claw-mcp-stdio:%s", releaseTag),
  ```
- New CI workflow `.github/workflows/claw-mcp-stdio-image.yml` mirroring
  `claw-wall-image.yml` (same trigger pattern, same labels, same buildx
  matrix).
- Update `.claude/skills/clawdapus-release/SKILL.md` with the new image in
  Step 10 (prepublish list) and Step 12 (verify).
- New CLI surface change → bump SKILL.md as in Step 6 of the release skill.

cllama is **not** touched. The submodule pointer doesn't move.

## 10. Build sequence (for codex)

Recommended commit/PR slicing — small, reviewable steps. Each step ends
green.

**Step 1 — wrapper binary + image** (`cmd/claw-mcp-stdio/`,
`dockerfiles/claw-mcp-stdio/Dockerfile`, `.github/workflows/claw-mcp-stdio-image.yml`):
- `cmd/claw-mcp-stdio/main.go` — env parsing, child spawn, HTTP server,
  request multiplexer, restart loop.
- Unit tests for the multiplexer (no Docker needed): feed mock stdio frames
  in, drive HTTP requests, assert correct response routing.
- `Dockerfile` per §3.1.
- CI workflow.
- `internal/infraimages/release_manifest.go` adds the new ref.

**Step 2 — pod parser** (`internal/pod/types.go`, `internal/pod/parser.go`,
`internal/pod/parser_mcp_stdio_test.go`):
- Add `MCPStdio` field, raw type, parse + validate per §4.

**Step 3 — describe-file plumbing** (`internal/pod/types.go`,
`internal/pod/parser.go`, `cmd/claw/compose_up.go:resolveServiceMetadata`):
- Add `x-claw.describe-file` parsing (one new string field on `ClawBlock`).
- In `resolveServiceMetadata`, if describe-file is set, resolve the path
  relative to `podDir` and load via existing descriptor parser. Falls
  through to current image/build-context path when unset.
- Tests in `cmd/claw/compose_up_descriptor_test.go` (or wherever the
  current descriptor tests live).

**Step 4 — compose emission** (`internal/pod/compose_emit.go`,
`internal/pod/compose_emit_mcp_stdio_test.go`):
- Inject `CLAW_MCP_STDIO_COMMAND` + `CLAW_MCP_STDIO_ARGS` env per §5.

**Step 5 — example + spike** (`examples/mcp-stdio/`,
`cmd/claw/mcp_stdio_spike_test.go`):
- Build the echo fixture, write the pod YAML + descriptor.
- Add the spike test.

**Step 6 — docs sweep** (per §8) — last so it reflects what actually shipped.

## 11. Testing matrix Codex must run before handoff

```bash
unset GOROOT  # if mise/homebrew Go drift bites again
go vet ./...
go test ./...
go test -tags integration ./...
go test -tags spike -run TestSpikeMCPStdio ./cmd/claw/...
# regression for #177:
go test -tags spike -run TestSpikeRollCall ./cmd/claw/...
```

Also `docker buildx build` the wrapper image locally (single arch is fine
for the dev loop) and run the spike against it.

## 12. Open questions for codex review

1. **Wrapper auth in v1.** Default is open (no token). Should we instead
   default to "auto-mint a bearer at `claw up` time" (analogous to
   `claw-api: self`) so even pod-internal traffic is authenticated? Argues
   for: defense in depth, parity with claw-api. Argues against: extra
   compile-time wiring just to gate a localhost-only call. Recommendation:
   ship v1 with optional bearer (env-driven), revisit if a real attacker
   model justifies it.

2. **Single shared child vs per-session child.** §3.3 picks single shared.
   Are there real-world MCP servers (perplexity-mcp included) that require
   per-session isolation? Quick survey before committing.

3. **`x-claw.describe-file` location: service-level or pod-level?** Plan
   has it on the service block. Could also be a pod-level map keyed by
   service name. Service-level is more local but adds one more service-block
   field; the trade-off is mild.

4. **Should the wrapper bundle Python alongside Node?** Adds ~30 MB to the
   image but covers the `uvx`/`python -m` MCP servers without operators
   needing a separate base. Plan says yes — challenge if you disagree.

5. **Single image or matrix?** Should we publish `claw-mcp-stdio:node-only`
   and `claw-mcp-stdio:full` (with Python)? v1 plan: one image, full. Easy
   to split later; hard to merge if we start split.

## 13. Acceptance checklist (against issue #179)

- [ ] A pod can wrap a stdio MCP server and expose it at a pod-internal
      Streamable HTTP `/mcp` endpoint. *(§3, §4, §5)*
- [ ] A `claw.describe` v2 descriptor with top-level
      `mcp: { transport: "streamable_http", path: "/mcp" }` can point at
      the wrapper service. *(unchanged from v0.11.0; verified in §6 + §7.2
      assertions)*
- [ ] `claw up` compiles the allowed tools into `tools.json` with
      `execution.transport = "mcp"`. *(unchanged from v0.11.0; verified
      via spike)*
- [ ] cllama can call the wrapped stdio tool through the existing MCP
      transport path. *(spike test §7.2)*
- [ ] Sample/fixture demonstrates an npm stdio MCP package. *(echo fixture
      in §7.1; perplexity example in §7.3)*

## 14. Out of scope (file as follow-ups if pressure builds)

- `claw discover` to snapshot live `tools/list` into a baked descriptor.
- Per-session child isolation.
- Header-style auth on the cllama MCP client side.
- Wrapper packaging beyond `node + python3` (e.g. Ruby/Elixir MCP servers).
