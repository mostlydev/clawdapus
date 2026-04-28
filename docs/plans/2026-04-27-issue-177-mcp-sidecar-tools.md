# Issue #177 — MCP sidecar services as managed tools

**Date:** 2026-04-27
**Issue:** https://github.com/mostlydev/clawdapus/issues/177
**ADR:** ADR-020 (Phase 5)
**Author:** Claude (draft for Codex review and implementation)

## Background

ADR-020 defines a single capability IR (`tools[]` in `claw.describe`) with two
delivery modes — `native` (runner-executed) and `mediated` (cllama-executed).
The mediated path is shipped end-to-end (Phases 1–4), but it can only execute
tools whose descriptor declares HTTP execution metadata. Each tool needs an
explicit `http: { method, path }`. There is no path for a standard MCP server
sidecar to be plugged in as a capability provider — `cllama` validates
`tool.Execution.Transport == "http"` and rejects everything else with
`unsupported_transport`.

ADR-020 already names this work as Phase 5: "MCP client in cllama, baked
`.claw-tools.json`, and `claw discover`". This plan implements the in-pod
sidecar half of Phase 5. `claw discover` (live tools/list snapshot for the
operator's build context) is treated as a follow-up (§Out of scope).

## Acceptance criteria (from issue)

1. A service can advertise MCP tools without writing `http` execution
   metadata for every tool.
2. `claw up` compiles allowed MCP tools into per-agent context
   deterministically.
3. `cllama` executes managed tool calls against the MCP sidecar and preserves
   existing policy limits (`max_rounds`, `timeout_per_tool_ms`,
   `total_timeout_ms`, `max_tool_result_bytes`), audit/`tool_trace`,
   session-history, and credential-starvation boundaries.
4. Tool names remain namespaced by service (e.g. `perplexity-mcp.search`).
5. Auth/env stays on the provider sidecar, not in the agent container.
6. Existing HTTP managed tools continue to work unchanged.

## Design decisions

### D1. Operator surface — descriptor-level `mcp` block

Add an optional top-level `mcp` block to the `claw.describe` v2 descriptor.
When present, the descriptor is treated as MCP-native: `tools[].http` becomes
optional and execution routes through the MCP endpoint.

```json
{
  "version": 2,
  "description": "Perplexity MCP server",
  "mcp": { "transport": "streamable_http", "path": "/mcp" },
  "tools": [
    {
      "name": "search",
      "description": "Web search with recency control",
      "inputSchema": { "type": "object", "properties": { "query": { "type": "string" } } }
    }
  ],
  "auth": { "type": "bearer", "env": "PERPLEXITY_BEARER" }
}
```

- `mcp.transport` defaults to `"streamable_http"`. Only that transport is
  accepted in v1 (stdio is local-only and not relevant for sidecars; the
  legacy HTTP+SSE transport is intentionally not supported).
- `mcp.path` is the MCP endpoint, defaults to `/mcp`. Must start with `/`.
- When `mcp` is set, `tools[].http` may be absent. If both are present for a
  given tool, that is a hard parse error — pick one execution shape per tool.
- A descriptor without `mcp` must continue to require `http` per tool
  (existing behavior, no regression).

The pod-side grammar is unchanged — `tools: [{ service: <name>, allow: [...] }]`
already works because MCP-ness is a property of the providing service, not
the consumer.

### D2. Compiled manifest — new `transport: "mcp"` execution shape

`tools.json` per agent grows a second execution shape. The LLM-facing fields
(`name`, `description`, `inputSchema`, `annotations`) are unchanged.

```json
{
  "name": "perplexity-mcp.search",
  "description": "Web search with recency control",
  "inputSchema": { ... },
  "execution": {
    "transport": "mcp",
    "service": "perplexity-mcp",
    "base_url": "http://perplexity-mcp:8080",
    "path": "/mcp",
    "tool_name": "search",
    "auth": { "type": "bearer", "token": "<resolved>" }
  }
}
```

Notes:
- `tool_name` is the un-namespaced MCP tool name. cllama sends this in
  `tools/call.params.name`. The compiled `name` stays namespaced for
  collision-free LLM presentation and audit.
- `method`/`body_key` are not used for MCP tools; omit them.
- Auth resolution reuses `resolveManifestAuth` exactly — same precedence as
  HTTP managed tools (per-agent service-auth → descriptor env fallback).

### D3. Discovery — baked tools, no live probe at `claw up`

Per ADR-020 (compile-time hermeticity), `claw up` does NOT connect to MCP
services to fetch `tools/list`. The operator commits the tool schemas to the
descriptor (via `tools[]`), exactly as for HTTP services today.

`claw discover` (a future operator command that runs the service container
once, calls `tools/list`, and writes the result back to the descriptor or to
a sibling `.claw-tools.json`) is the right operator UX but is **out of scope
for this issue**. Workaround until it lands: hand-author the tool list in
`.claw-describe.json`. Most MCP servers publish their tool catalog in their
README.

### D4. cllama MCP client — minimal Streamable HTTP transport

Add `cllama/internal/mcp/client.go`:
- `Client.Call(ctx, target, toolName, args) ([]byte, int, error)` —
  high-level entry point that returns the same `(rawJSON, statusCode, err)`
  triple as `callManagedHTTPTool`.
- Per-target session cache keyed by `(base_url + path)`. On cold cache,
  perform `initialize` + `notifications/initialized`, store any returned
  `Mcp-Session-Id`, and proceed with `tools/call`. On any
  `session_required` / `invalid_session` / 4xx-with-no-session error, drop
  the cached session and retry once.
- POST `Content-Type: application/json`,
  `Accept: application/json, text/event-stream`. Handle either response
  shape: parse a single JSON body, or read SSE events (`data:` lines) until
  the JSON-RPC response with the matching `id` is received.
- Bearer auth applied via `Authorization` header when
  `tool.Execution.Auth.Type == "bearer"`.

Result normalization: MCP `tools/call` responses look like

```json
{ "result": { "content": [{ "type": "text", "text": "..." }, ...], "isError": false } }
```

cllama wraps this into the existing managed-tool result envelope:

```json
{ "ok": true, "data": { "content": [...] } }
```

`isError: true` becomes `{ "ok": false, "error": { "code": "tool_error", "message": "<flattened text>" } }`.
JSON-RPC transport errors (parse error, server error, timeout) map onto the
existing error code vocabulary (`request_failed`, `read_failed`, `http_<n>`,
`timeout`).

### D5. Dispatch — single switch in `executeManagedOpenAITool` / `executeManagedAnthropicTool`

Both Anthropic and OpenAI execution paths today directly call
`callManagedHTTPTool`. Replace those two call sites with a thin
`dispatchManagedTool(ctx, agentID, manifest, args)` that switches on
`manifest.Execution.Transport`:

```go
switch strings.ToLower(strings.TrimSpace(tool.Execution.Transport)) {
case "http":
    return h.callManagedHTTPTool(ctx, agentID, tool, args)
case "mcp":
    return h.callManagedMCPTool(ctx, agentID, tool, args)
default:
    return toolErrorPayload("unsupported_transport", ...), 0, nil
}
```

Trace fields (`Service`, namespacing, latency, status code) are populated by
the caller exactly as today — no changes to `tool_trace` shape, audit, or
session-history.

### D6. Spike coverage

Extend `TestSpikeRollCall` (or add a sibling spike) to exercise an MCP
provider end-to-end. Use a tiny in-tree fake MCP service (a `dockerfiles/`
mini-image that speaks Streamable HTTP with one `echo` tool) so the spike is
hermetic and doesn't depend on a third-party MCP server. This proves cllama
mediated execution against a real MCP transport, including the
`initialize` handshake and the `tools/call` round-trip.

## Build sequence

### Phase 5a — descriptor + compile-time wiring

Files to touch:
- `internal/describe/descriptor.go`: add `MCP *MCPTransport` and
  `MCPTransport{ Transport, Path string }`. Loosen `validateTools` so that
  when `d.MCP != nil`, `tool.HTTP` is optional. Reject the both-present case.
  Reject unknown `mcp.transport` values (only `streamable_http` accepted).
- `internal/describe/registry.go`: extend `ToolSpec` with `MCP` (carrying
  the descriptor's MCP block + the un-namespaced tool name) so callers know
  whether to compile HTTP or MCP execution.
- `cmd/claw/compose_up.go::buildToolManifestEntries`: switch on
  `tool.MCP != nil` (or descriptor lookup) to emit a `transport: "mcp"`
  manifest entry instead of HTTP. Reuse `resolveServiceBaseURL` and
  `resolveManifestAuth` unchanged.
- `internal/cllama/context.go`: extend `ToolExecution` with
  `ToolName string `json:"tool_name,omitempty"``. (Path/BaseURL/Auth fields
  reused as-is.)

Tests:
- `internal/describe/descriptor_test.go`: parse a v2 descriptor with `mcp`
  block, assert tools without `http` validate; assert both-present rejects;
  assert unknown transport rejects.
- `internal/describe/registry_test.go`: tool registry carries MCP info
  through.
- `internal/cllama/context_test.go`: mediated manifest with
  `transport: "mcp"` round-trips correctly.
- `cmd/claw/compose_up_test.go` (or nearest existing test for tool
  compilation): assert a pod with an MCP provider compiles
  `transport: "mcp"` entries with the expected `tool_name`, `path`,
  `base_url`, and `auth`.

### Phase 5b — cllama MCP execution

Files to add/touch:
- `cllama/internal/mcp/client.go` (new): minimal Streamable HTTP MCP client
  per D4. JSON-RPC framing, initialize/initialized handshake with cached
  session, `tools/call` with SSE-or-JSON response parsing, bounded
  `maxManagedToolResultBytes` reads.
- `cllama/internal/mcp/client_test.go` (new): table-driven tests against an
  `httptest.Server` that returns (a) JSON, (b) SSE, (c) JSON-RPC error,
  (d) session-required-then-OK on retry.
- `cllama/internal/proxy/toolmediation.go`: introduce
  `dispatchManagedTool` and replace the two call sites in
  `executeManagedOpenAITool` / `executeManagedAnthropicTool`. Add
  `callManagedMCPTool` that wires `agentctx.ToolManifestEntry` → MCP client
  call and runs the result through the same `toolSuccess` / `toolError`
  envelope.
- `cllama/internal/agentctx/agentctx.go`: extend `ToolExecution` with
  `ToolName` so the proxy sees the un-namespaced tool name.
- `cllama/internal/proxy/toolmediation_test.go`: extend ownership tests with
  a transport-MCP manifest entry to cover the dispatch switch.

### Phase 5c — example + spike

- `examples/`: small example pod under `examples/mcp-sidecar/` showing the
  shape from the issue (one cllama agent, one MCP-shaped service). Real
  Perplexity is fine if `PERPLEXITY_API_KEY` is documented; otherwise wire
  it to the in-tree fake echo server.
- `dockerfiles/mcp-echo/` (new, contributor-only): tiny Streamable HTTP
  server with a single `echo` tool. Used for the spike fixture and the
  example. Published manually by maintainers (no separate publication
  workflow needed for v1; treat it like the other `dockerfiles/` images).
- `cmd/claw/spike_*_test.go` (new or extension): bring up the
  mcp-sidecar example, cllama mediates a tool call, assert audit shows the
  `tool_call` event with `service = mcp-echo`.

### Phase 5d — docs

- `docs/decisions/020-cllama-compiled-tool-mediation.md`: append an
  Implementation Status note flipping Phase 5 partial-shipped (sidecar
  transport done, `claw discover` deferred).
- `README.md` and/or `docs/plans/2026-04-05-issue-115-managed-tools-injection.md`:
  short note on the new `mcp` descriptor block.
- `site/changelog.md`: entry under the next version once released.

## Test plan

- `go vet ./... && go test ./...` — unit + describe + compose_up coverage.
- `go test ./cllama/...` — MCP client tests + proxy dispatch.
- `go test -tags integration ./...` — should remain green (no
  intentional regression).
- `go test -tags spike -run TestSpikeMCPSidecar ./cmd/claw/...` — new
  hermetic spike against the mcp-echo image.
- Manual: `cd examples/mcp-sidecar && claw up -d && claw audit` shows
  managed tool calls with the MCP transport and namespaced names.

## Out of scope

- `claw discover` (live `tools/list` snapshot). File a follow-up issue.
- MCP `resources` and `prompts` primitives — feeds and skills cover those
  intent-wise (ADR-020 §9). Not needed for sidecar tool support.
- HTTP+SSE legacy MCP transport. Streamable HTTP only in v1.
- stdio-transport MCP servers — out of scope by definition (sidecars are
  network services).
- Native-mode pod-shared tool delivery (ADR-020 Phase 2 / Phase 6) — this
  plan is mediated-only.
- Per-tool MCP `annotations` extensions (parallel-safe, etc.) — mirror
  whatever MCP returns into `tool_trace`, but no policy behavior change.

## Open questions for review

1. **Auth shape.** Should descriptor `auth.type == "bearer"` translate to
   `Authorization: Bearer ...` for MCP, or do MCP servers commonly expect a
   custom header (e.g. `X-API-Key`)? Proposed answer: bearer in v1; add
   `auth.type == "header"` projection later if needed (the descriptor
   already accepts `header` as a type — just need to define how the
   compiled manifest represents it).
2. **Session caching scope.** Per cllama process, or per request? Per
   process is more efficient but introduces a tiny piece of cross-request
   state inside the proxy — does that violate any boundary we care about?
   Session IDs are not user-scoped, so the answer is probably "no", but
   worth confirming.
3. **Result truncation semantics.** When `tools/call` returns an SSE stream
   that exceeds `maxManagedToolResultBytes`, do we want to truncate the
   final flattened JSON, or drop the response entirely? Today the HTTP path
   truncates the body — proposing the same here for symmetry.
4. **Spike fixture.** Build a tiny Go-based `mcp-echo` server in-tree, or
   pull a public reference (e.g. the `mcp-server-everything` reference
   image)? In-tree is more reproducible and avoids network dependence in
   spike runs.
