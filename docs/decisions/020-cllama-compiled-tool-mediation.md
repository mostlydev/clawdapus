# ADR-020: Compiled Tool Plane with Native and Mediated Execution Modes

**Date:** 2026-03-29
**Status:** Implemented (mediated path — Phase 3 + Phase 4 complete, including additive native-tool preservation and managed→native handoff; see implementation notes below)
**Depends on:** ADR-007 (Credential Starvation), ADR-017 (Service Self-Description), ADR-019 (Model Policy Authority)
**Amends:** ADR-018 (Session History) — extends the recording contract to include tool execution traces and failed requests
**Evolves:** ADR-004 (Service Surface Skills) — skills remain as behavioral guidance; tools add a callable interface

## Context

Clawdapus services self-describe via `claw.describe` (ADR-017). The descriptor declares feeds, endpoints, auth, and skill file paths. `claw up` compiles these into per-agent context: CLAWDAPUS.md documents available services, Anthropic-format skill files explain how to use them, and feeds deliver live data. The agent reads the documentation and constructs HTTP calls manually.

This is fragile. Agents hallucinate endpoint paths, forget auth headers, confuse HTTP methods, and misformat request bodies. Documentation tells the agent *about* the service — it does not give the agent a *callable interface* to it.

Three industry standards converge on this problem:

- **MCP** (Model Context Protocol) defines structured tool schemas (`tools/list`, `tools/call`) with JSON-RPC execution — the mechanical interface standard. MCP's tool schema shape (`name`, `description`, `inputSchema`, `annotations`) is a clean, provider-agnostic way to describe callable capabilities.
- **Anthropic Skills** (markdown with YAML frontmatter) describe when and why to use tools — the behavioral guidance standard. Already implemented in Clawdapus via `internal/skillmd/format.go`.
- **Docker/OCI** provides image labels, container networking, and compose services — the deployment standard.

Clawdapus should bridge these standards, not replace any of them. The descriptor is the Rosetta Stone. This ADR adopts MCP's tool schema shape as the capability description format and supports two execution models:

- **Native execution.** Clawdapus governs which tools are presented, but the runner executes them using whatever authentication and network identity the included surface already supports.
- **Mediated execution.** Clawdapus governs both presentation and execution, but only for Clawdapus-compatible services or services that explicitly trust a delegated Clawdapus broker/assertion path.

This is not MCP runtime adoption in v1 — there is no required JSON-RPC transport, no mandatory `tools/call`, and no universal MCP client. It is a transitional compatibility layer that uses MCP's schema vocabulary while delivering capabilities through the mechanisms runners and services can realistically support today.

The central architectural distinction is:

- **Governance identity** answers "which policy applies to this agent?"
- **Execution identity** answers "what credential or network identity does the backend actually authorize?"

Clawdapus always owns governance identity. It does **not** automatically own execution identity. Presentation governance is universal. Rights projection is conditional on the execution path.

### Why not make runner-native MCP the only v1 path?

**Runner agnosticism.** Seven runners exist and they do not yet share a universal path for consuming pod-shared MCP/client configuration. Adding bespoke pod-shared capability loading to each scales linearly with the driver count.

**Execution identity is surface-specific.** On customer infrastructure, runners may legitimately need to execute against AD, mTLS, OAuth, or service-account protected surfaces without Clawdapus brokering those credentials. That is a valid native model, but it does not eliminate the need for a compatibility path when runners cannot yet host pod-shared tools natively.

**Compile-time determinism.** MCP discovery is runtime. Clawdapus requires all wiring resolved during `claw up`.

## Positions Evaluated

Three architectures were evaluated across four rounds by three independent reviewers. The consistent conclusion was:

- `native` execution is the cleaner steady-state architecture when runners can host pod-shared tools and the backend's own auth model should remain authoritative.
- `mediated` execution is the only zero-runner-change compatibility path for governed pod-shared tools across the current runner set.

### Position A: cllama owns tool mediation (selected compatibility mode)

`claw.describe` v2 adds MCP-shaped tool schemas alongside the existing Anthropic skill. `claw up` compiles per-agent `tools.json` (filtered by tool policy) next to `feeds.json`. cllama injects tools into LLM requests, intercepts tool_calls, executes them against services, and loops until terminal text. Runners are unchanged.

**For:** Zero runner changes. Gives Clawdapus an auditable compatibility path for governed tools. Follows the feed injection pattern exactly. Aligns with Manifesto Principle 7 (governance in a separate process).

**Against:** cllama becomes stateful within a request lifecycle. Streaming requires non-streaming upstream when tools are injected. Adds complexity to the proxy.

**Evaluation:** The statefulness is bounded (single HTTP request lifecycle, with only a narrow continuity exception across turns). The streaming trade-off is acceptable for a compatibility mode. The complexity follows the existing pattern — feeds already turn cllama from a passthrough into a context-aware proxy. Additive composition of runner-local and pod-shared tools remains the preferred steady state, but that requires the runner to own the combined tool loop.

### Position B: MCP broker via claw-api (rejected)

**Fatal objection:** Universal runner support for pod-shared MCP/client configuration would still be required. Conflates fleet governance with capability delivery.

### Position C: Separate claw-mcp gateway (rejected)

**Fatal objection:** Another infrastructure service per pod. Universal runner support for pod-shared MCP/client configuration would still be required. Runtime discovery violates compile-time determinism.

Both reviewers who originally proposed B and C reversed their positions after examining the handler.go code paths and the runner agnosticism constraint.

## Decision

### 0. One capability IR, two delivery modes

Clawdapus should have one canonical capability IR and two delivery modes:

| Mode | What Clawdapus governs | Tool-loop owner | How pod-shared tools are delivered | Backend auth path |
|---|---|---|---|---|
| `native` | Presentation, policy, audit hooks | Runner | Runner loads compiled pod-shared tool/MCP/client config additively with local tools | Whatever auth and network identity the included surface already supports |
| `mediated` | Presentation, policy, execution | cllama or a Clawdapus-owned broker | Provider-native `tools[]` injection from compiled `tools.json` | Delegated service credential or explicit trust in a Clawdapus-compatible broker/assertion path |

The larger picture is additive composition. Pod-shared tools should sit alongside runner-local tools, not replace them. That is naturally the runner in `native` mode, where one client/executor owns the whole tool loop and backend authorization remains the backend's problem.

This ADR therefore does two things:

- defines the canonical capability IR (`tools[]`, `feeds[]`, `skill`, `endpoints[]`)
- defines two projections of that IR: native presentation/execution and mediated presentation/execution

Three rules follow from this split:

- **Presentation governance is universal.** Clawdapus decides which tools an agent can see.
- **Execution mediation is optional.** Native execution is valid when the runner should execute directly against the included surface.
- **Rights projection is conditional.** Clawdapus only projects backend rights when the execution path actually carries delegated credentials or a trusted Clawdapus assertion.

`native` mode is the default execution model and the intended steady state once runners can consume pod-shared MCP/client configuration **and** audit parity exists for pod-shared tool usage. `mediated` mode is the compatibility layer for runners that cannot yet host pod-shared tools natively, and for Clawdapus-compatible services where full mediated execution is desirable.

### 1. Canonical capability IR via `claw.describe` v2

The descriptor defines four capability types. Each serves a distinct purpose and maps to an industry standard:

| Descriptor field | Industry standard | Clawdapus role | Compiled projection |
|---|---|---|---|
| `tools[]` | MCP tool schema shape | LLM-callable interface | `tools.json` (`mediated`) or runner-side tool/client config (`native`) |
| `skill` | Anthropic skill format | Behavioral guidance | `skills/<service>.md` |
| `feeds[]` | (Clawdapus-native) | Live data delivery | `feeds.json` |
| `endpoints[]` | OpenAPI-adjacent | Operator documentation | CLAWDAPUS.md (when no tools) |

**`tools[]` are the LLM-callable interface.** The tool schema uses MCP's visible shape (`name`, `description`, `inputSchema`, `annotations`). The only non-MCP addition is hidden execution metadata (`http`) for Clawdapus compilation — the LLM never sees it.

**`endpoints[]` are operator documentation.** They describe the service's HTTP surface for human operators, `claw inspect`, and manual debugging. They are NOT used for LLM tool calling. When a service declares `tools[]`, its endpoint details are omitted from agent-facing `CLAWDAPUS.md` entirely. Agents interact through governed tools or not at all. Endpoint details remain available to operators through `claw inspect`, descriptor inspection, and other operator surfaces. Services that declare only `endpoints[]` and no `tools[]` continue to use the current manual-documentation path.

This separation is critical: `tools[]` are the governed, model-callable interface. `endpoints[]` are the ungoverned, human-readable reference. They may describe the same HTTP operations but serve different audiences and different trust models.

This all-or-nothing suppression is intentional. Partial tool coverage is not an invitation for agents to fall back to manual HTTP on the remaining endpoints. If an operation should be agent-usable, it should be exposed as a governed tool. If it should remain human-only, it stays in operator-facing endpoint documentation.

**Descriptor v2 example:**

```json
{
  "version": 2,
  "description": "Trading Desk API — broker connectivity, trade execution, and market context.",
  "tools": [
    {
      "name": "get_market_context",
      "description": "Retrieve agent-scoped market context: positions, balance, buying power",
      "inputSchema": {
        "type": "object",
        "properties": {
          "claw_id": { "type": "string", "description": "Agent identifier" }
        },
        "required": ["claw_id"]
      },
      "http": { "method": "GET", "path": "/api/v1/market_context/{claw_id}" },
      "annotations": { "readOnly": true }
    },
    {
      "name": "execute_trade",
      "description": "Execute a market order",
      "inputSchema": {
        "type": "object",
        "properties": {
          "symbol": { "type": "string" },
          "side": { "type": "string", "enum": ["buy", "sell"] },
          "quantity": { "type": "number" }
        },
        "required": ["symbol", "side", "quantity"]
      },
      "http": { "method": "POST", "path": "/api/v1/trades", "body": "json" },
      "annotations": { "readOnly": false }
    }
  ],
  "feeds": [
    { "name": "market-context", "path": "/api/v1/market_context/{claw_id}", "ttl": 30 }
  ],
  "skill": "skills/trading-policy.md",
  "auth": { "type": "bearer", "env": "TRADING_API_TOKEN" }
}
```

Note that `market-context` appears as both a feed and a tool. This is intentional: the feed delivers periodic context injection into the system prompt (the agent always has fresh market data), while the tool provides on-demand invocation (the agent explicitly requests context when it needs it for a specific decision). They complement each other — the feed ensures ambient awareness, the tool enables deliberate action.
```

**Tool annotations** use MCP's `annotations` field. `readOnly` distinguishes safe queries from side-effecting operations. This metadata is used by tool policy (below) and visible in `claw audit` output. Future annotations (`idempotent`, `confirmationRequired`) extend this without schema changes.

**MCP-native services** bake their tool schemas into the image as a `.claw-tools.json` artifact (a snapshot of `tools/list` output). `claw up` reads this from the image like any other descriptor artifact — no live MCP connection during compilation. Live MCP discovery is a future `claw discover` command that updates baked schemas against a running pod.

**Why baked, not live?** `claw up` resolves descriptors before containers start (`compose_up.go:344`). It cannot connect to services that don't exist yet. Requiring baked schemas maintains compile-time hermeticity and avoids bootstrap circular dependencies.

### 2. Authority and identity model

Service access has four independent dimensions. The first three are declared in pod YAML; the fourth depends on the execution mode:

| Layer | Declaration / source | What it controls | Default |
|---|---|---|---|
| **Topology** | `surfaces: [service://X]` | Network reachability between containers | No access |
| **Verb authority** | `tools: [{ service: X, allow: ... }]` | Which operations the LLM can invoke | No tools |
| **Governance identity** | Agent bearer + context metadata | Which Clawdapus policy applies | Authenticated caller only |
| **Execution identity** | Runner-native backend auth (`native`) or projected credential / trusted broker (`mediated`) | What the backend actually authorizes | Surface-specific auth model |

Declaring `service://X` grants network reachability. Tool access requires explicit `tools:` declaration. These are distinct: topology is transport, tools are verb authority, governance identity decides what Clawdapus presents, and execution identity decides what the backend accepts.

```yaml
# claw-pod.yml
services:
  analyst:
    x-claw:
      agent: agents/analyst
      surfaces:
        - service://trading-api       # reachability
      tools:
        - service: trading-api         # verb authority
          allow:
            - get_market_context       # read-only access

  executor:
    x-claw:
      agent: agents/executor
      surfaces:
        - service://trading-api
      tools:
        - service: trading-api
          allow: all                   # full access (explicit)
```

**No tools by default.** If `tools:` is omitted, no tools are compiled — even if the surface's descriptor declares them. This matches ADR-015's deny-by-default scoping model and prevents accidental exposure of destructive tools.

This is compiled MGL-style policy applied at infrastructure time: the pod author declares which capabilities each agent role may access, and the compilation pipeline enforces it by emitting only the permitted tools into each agent's manifest.

**Native mode keeps backend auth native.** In `native` mode, Clawdapus does not rewrite or terminate backend authentication. The runner executes against the included surface using whatever execution identity that surface already supports: Active Directory, mTLS, OAuth, service accounts, customer-specific credentials, or any other existing scheme. Clawdapus governs visibility and intent; the backend still enforces rights.

**Mediated mode requires a real trust path.** In `mediated` mode, Clawdapus may execute the tool on the agent's behalf, but only when the surface is Clawdapus-compatible or explicitly trusts delegated credentials or brokered identity assertions. An `X-Claw-ID` header or authenticated caller identity is not enough by itself. If the backend does not trust a projected Clawdapus path, then `mediated` mode can govern presentation but not honestly claim end-to-end rights projection.

`tools:` is intentionally list-shaped so it composes cleanly with pod defaults and `...` spread. Each entry has:

- `service`: the providing compose service
- `allow`: either `all` or a list of tool names

After pod-default expansion, grants are normalized by service name:

- `allow: all` wins for that service
- otherwise tool names are unioned

**Pod defaults and spread.** `tools-defaults:` at pod level uses the same list shape. Service-level `tools:` follows the standard replace-on-declare rule, and `...` splices pod defaults into the service list before normalization. This keeps the external grammar aligned with the existing defaults model while still yielding a service-keyed compiled policy.

### 3. Compiled projections from one IR

The tool compilation pipeline mirrors the feed pipeline at the registry and policy layers, then forks into mode-specific projections:

| Step | Feeds (existing) | Tools (`native`) | Tools (`mediated`) |
|---|---|---|---|
| Descriptor declares | `feeds[]` with name, path, TTL | `tools[]` with name, inputSchema, http | `tools[]` with name, inputSchema, http |
| Registry built | `BuildFeedRegistry()` from descriptors | `BuildToolRegistry()` from descriptors | `BuildToolRegistry()` from descriptors |
| Policy filters | Feed subscription in pod YAML | `tools:` declaration in pod YAML | `tools:` declaration in pod YAML |
| Artifact written | `feeds.json` in context dir | Runner-side tool/MCP/client config | `tools.json` in context dir |
| Auth handling | Feed auth manifest | Surface-native auth remains external | Projected auth may be inlined for trusted mediated execution |
| Runtime consumer | cllama feed fetcher | Runner tool host / MCP client / native loader | cllama mediator |

The canonical IR is shared. What changes by mode is the execution projection.

In `native` mode, Clawdapus compiles the allowed tool catalog into whatever runner-side configuration is needed to present pod-shared tools alongside local tools. Clawdapus does not need to inline bearer tokens or terminate backend auth in that path. The runner executes against the included surface using its native execution identity, and the surface's own auth scheme remains authoritative.

In `mediated` mode, Clawdapus writes `/claw/context/<agent-id>/tools.json` as the execution manifest for cllama or a Clawdapus-owned broker. This is the mode that mirrors `feeds.json` most closely because the proxy is both the runtime consumer and the execution point.

Auth is only inlined into `tools.json` for `mediated` execution, using the same resolution order as `feeds.json`: per-agent service-auth projection (ADR-015 principal scoping) takes precedence, falling back to descriptor-level auth from service environment when that fallback is actually valid for the target surface. For `claw-api` tools, cllama first authenticates the caller using the agent bearer token, then executes the tool using the projected `claw-api` principal credential from `service-auth/`. The ingress bearer token and the downstream service principal remain distinct.

`claw-api` follows this ADR as a normal self-describing service. Its tools are declared through the same `tools[]` IR, gated by the same `tools:` policy, and authenticated through the same projected service-principal path. Existing `claw-api: self` wiring remains a credential-projection convenience, not a grant of verb authority. Write-plane verbs remain subject to both tool allowlisting and ADR-015 principal scope.

**`mediated` manifest at `/claw/context/<agent-id>/tools.json`:**

```json
{
  "version": 1,
  "tools": [
    {
      "name": "trading-api.get_market_context",
      "description": "Retrieve agent-scoped market context",
      "inputSchema": { "..." : "..." },
      "annotations": { "readOnly": true },
      "execution": {
        "transport": "http",
        "service": "trading-api",
        "base_url": "http://trading-api:4000",
        "method": "GET",
        "path": "/api/v1/market_context/{claw_id}",
        "auth": { "type": "bearer", "token": "resolved-token-value" }
      }
    }
  ],
  "policy": {
    "max_rounds": 8,
    "timeout_per_tool_ms": 30000,
    "total_timeout_ms": 120000
  }
}
```

The mediated manifest separates LLM-facing schema (name, description, inputSchema) from execution metadata (transport, URL, auth). The LLM sees only the schema. cllama uses the execution metadata to make HTTP calls. This path hides service URL and credential details from the agent because Clawdapus is the executor.

**Namespacing** is mandatory. The compiled manifest prefixes tool names with the service name (`trading-api.get_market_context`). The descriptor stays service-agnostic; namespacing is applied at compile time. This prevents collisions when multiple services expose tools with the same base name.

**Path placeholders.** `{claw_id}` in HTTP paths is substituted at execution time using the authenticated agent identity for mediated calls, or by the runner-side tool host in native mode. Other placeholders (`{param}`) are substituted from the tool call's `arguments` object.

### 4. Native mode: presentation-governed, runner-executed

`native` mode is the default execution model. Clawdapus compiles and filters the pod-shared tool catalog, but the runner owns the tool loop and executes tools additively with its own local tools.

This is the right model when:

- the surface already has its own enterprise auth model
- the runner should act under a customer-managed execution identity
- Clawdapus should govern cognition and presentation without becoming an auth broker

Examples include Active Directory, mTLS, OAuth, service accounts, and other customer-specific infrastructure auth schemes. In this mode, Clawdapus does not claim end-to-end rights projection. It governs what the model can see and ask for; the backend still governs what actually runs.

Audit in `native` mode is required for graduation but may be indirect at first. A runner can load pod-shared tools natively before audit parity exists, but Clawdapus should not treat that path as governance-complete until tool execution remains observable through a broker, proxy, or equivalent telemetry path.

### 5. Mediated mode: cllama injection, interception, execution

This section defines `mediated` mode only. `Mediated` execution is the compatibility path for unchanged runners and the full-governance path for Clawdapus-compatible services.

Here, **Clawdapus-compatible** means the backend either accepts projected service principals generated by `claw up` or explicitly trusts a Clawdapus broker/assertion path for execution authorization.

In `mediated` mode, cllama gains the ability to inject tools into LLM requests and execute tool_calls transparently. This extends the existing pattern:

| Capability | Declaration | Compiled artifact | Runtime enforcement |
|---|---|---|---|
| LLM access | API keys in `.env` | `providers.json` | cllama key pool (ADR-007) |
| Model selection | `MODEL` in Clawfile | `model_policy` in metadata | cllama policy enforcement (ADR-019) |
| Data context | `feeds` in descriptor | `feeds.json` | cllama fetcher + injection |
| **Service tools** | **`tools` in descriptor** | **`tools.json`** | **cllama injection + execution** |

#### Tool injection

When `tools.json` is loaded for an agent in `mediated` mode, cllama appends managed tools to any runner-native tool definitions already present on the outbound request. Managed tools are namespaced as `<service>.<tool>` (e.g., `trading-api.get_market_context`), which distinguishes them from runner-native tools when logs or transcripts are inspected.

For OpenAI-compatible requests, legacy `functions[]` are normalized into `tools[]` before merge so additive composition preserves older runner tool clients as well. Existing `tool_choice` intent is preserved when safe; if it targets a managed tool by canonical name, cllama rewrites the name to the provider-safe presented alias.

#### Streaming behavior

When cllama injects managed tools, it forces `stream: false` on the upstream LLM request. This prevents partial text from being flushed to the runner before a tool_call is detected. If the runner originally requested streaming, cllama re-streams the final text response as synthetic SSE chunks after the tool chain completes.

If the downstream client requested streaming, cllama SHOULD keep the downstream HTTP stream alive during mediation with harmless SSE keepalive or progress comments. These are transport-level liveness signals, not synthetic assistant tokens. The goal is to prevent the runner UI from appearing hung while cllama executes hidden tool rounds.

Requests where cllama has NO managed tools to inject are unaffected — streaming passes through normally.

**Why not speculative streaming?** Detecting tool_calls mid-stream requires parsing provider-specific SSE chunk formats, buffering partial JSON, and handling edge cases where tool_calls arrive late. The complexity couples cllama to provider serialization details. Forcing non-streaming is simple, correct, and provider-agnostic. The latency cost (no token streaming during tool-augmented requests) is acceptable for chat agents, which are the primary tool consumers.

#### Response handling: ownership-partitioned executor

A fundamental constraint: when the LLM returns tool_calls, the protocol requires results for ALL calls before it will continue. Two independent executors (cllama + runner) cannot both fulfill a single response's tool_calls without one fabricating results for the other's tools. Fabricated results let the LLM reason over output that never happened.

`mediated` mode therefore partitions by response ownership rather than pretending both executors can satisfy the same tool round.

**Current rule:** runner-native and managed tools can coexist on the same request surface, but cllama preserves a monotonic execution boundary inside each mediated chain:
- If a response contains managed tool calls only, cllama owns that round and executes them internally.
- If a response contains runner-native tool calls only, cllama passes the response back to the runner unchanged. If the downstream client originally requested streaming, cllama synthesizes an equivalent SSE stream so the runner still receives its expected protocol shape.
- If a single response contains a managed prefix followed by a runner-native suffix, cllama occludes the runner-native suffix, executes the managed prefix internally, appends the managed results into the hidden transcript, and asks the model to continue from that state. If the model later emits runner-native tool calls only, cllama hands that response back to the runner and stores the usual one-shot continuity handoff so the hidden managed transcript is reinserted before the runner's follow-up tool-result request.
- If a single response contains runner-native calls before later managed calls, or otherwise interleaves ownership, cllama still refuses to reorder the plan, but it keeps that batch hidden and feeds the model synthetic rejected-tool results so it can retry internally without leaking proxy errors to the runner surface.

**If the response contains managed tool_calls only:**
1. cllama validates each call against the manifest (reject unknown tools — fail closed)
2. Executes managed tools sequentially against target services
3. Constructs a follow-up LLM request with tool results appended
4. Repeats until the LLM returns terminal text
5. Returns the final response to the runner

**If the response contains runner-native tool_calls only before any hidden managed round:**
- Return the response to the runner so its native tool loop can continue normally.

**If the response contains a managed prefix and a runner-native suffix in one model response:**
- Serialize the round. cllama executes the managed prefix first, feeds those results back upstream, and waits for the model to re-emit any runner-native step cleanly in a later response.

**If the response contains runner-native calls before later managed calls, or otherwise interleaves ownership:**
- Do not reorder the model's plan.
- Preserve the mixed batch in hidden continuity, append synthetic rejected-tool results that explain the ordering constraint, and let the model replan on an internal follow-up turn.

**If the response contains only text:**
- Return directly (or re-stream if the runner requested streaming).

This monotonic-executor model handles the common cases cleanly:
- Service-only tool chains: cllama handles transparently, runner sees text
- Runner-only tool chains in mediated requests: cllama preserves them, runner remains the executor
- Managed-first mixed batches: cllama serializes the managed prefix before letting the runner resume
- Native additive tool chains: runner handles both local and pod-shared tools in `native` mode
- Native-first or interleaved mixed batches in `mediated` mode: refuse execution, feed errors back

**Future:** `native` mode is the preferred additive path. Any later two-phase mediated execution would require an explicit runner-side protocol extension and is not the architectural target.

#### Transcript continuity across turns

`mediated` mode creates a hidden tool loop. Returning only terminal text to the runner is not enough, because the runner's local conversation history will not include the intermediate assistant/tool turns that produced that text. On the next user turn, the runner may send an incomplete transcript back to cllama.

This mode therefore requires a continuity shim. Session history alone is not sufficient because it is an audit record, not part of the live prompt path. `mediated` mode MUST preserve effective tool-round context across turns using one of these strategies:

1. **Transcript reflection.** If the runner/protocol can accept it, cllama returns the effective assistant/tool transcript in provider-native form so the runner stores the mediated turns locally. This is the preferred v1 strategy because it keeps continuity in the runner's own transcript.
2. **Continuity summary.** Otherwise, cllama persists a compact summary of the mediated tool rounds and injects that summary into the next request before forwarding upstream.

The exact mechanism is an implementation choice, but the requirement is architectural: hidden tool rounds must not disappear between user turns.

#### Error handling

Tool execution errors are fed back to the LLM as structured results, not returned to the runner:
```json
{
  "role": "tool",
  "tool_call_id": "call_abc",
  "content": "{\"ok\": false, \"error\": {\"code\": \"timeout\", \"message\": \"Service did not respond within 30s\"}}"
}
```
The LLM decides how to communicate the failure. If cllama itself fails (internal error, budget exhaustion), it returns `502` to the runner. No partial text is sent because non-streaming prevents premature flushing.

#### Budget and timeouts

- `max_rounds` (default 8): Maximum tool loop iterations per request. Prevents infinite loops.
- `timeout_per_tool_ms` (default 30,000): Per-tool execution timeout.
- `total_timeout_ms` (default 120,000): Total chain timeout including all LLM calls and tool executions.
- `max_tool_result_bytes` (default 16,384): Tool results exceeding this are truncated with a notice, preventing context window exhaustion.

Truncation MUST be explicit in the structured result so the model does not reason over partial data as if it were complete. Minimum shape:

```json
{
  "ok": true,
  "data": "...first bytes...",
  "truncated": true,
  "original_bytes": 52000
}
```

All configurable at pod level, compiled into `tools.json`.

### 6. Boundary: what cllama does and does not do

**cllama manages in `mediated` mode:** Tools from `tools.json` — injection, interception, execution, delegated auth where supported, and audit.

**cllama does NOT manage:**
- **Runner-native tools.** Shell, file ops, send_message, browser remain runner-owned. Additive composition with pod-shared tools happens in `native` mode, not inside the `mediated` tool loop.
- **Dynamic discovery.** The manifest is static. No runtime `tools/list`. No tool registration.
- **Cognitive decisions.** The LLM chooses which tools to call. cllama is a mechanical executor.
- **General cross-request state.** Each request gets a fresh tool loop. The only exception is bounded continuity state required to preserve mediated tool-round context across turns.

cllama is a **tool mediator**, not an agent framework. It extends the proxy's existing intercept-enforce-forward pattern to a new dimension.

### 7. Skills and tools: complementary, not competing

Skills and tools are sibling concepts from the same descriptor. A service emits both:

| Concept | Standard | Format | Audience | Example |
|---|---|---|---|---|
| Tool | MCP tool schema | JSON Schema | LLM function calling | `execute_trade(symbol, side, qty)` |
| Skill | Anthropic skill | Markdown + YAML frontmatter | Agent context | "Check risk limits before trading" |
| Feed | Clawdapus-native | JSON manifest | cllama system prompt injection | Market data every 30s |

The skill says *when and why*. The tool provides *how*. The feed delivers *what's happening now*.

For v1, one skill file per service (not per tool). If a service exposes five tools, the skill can have five sections. `claw up` continues to mount skills at `/claw/skills/` and reference them in CLAWDAPUS.md. CLAWDAPUS.md gains a `## Tools` section listing available tool names and descriptions.

### 8. Session history and audit (amends ADR-018)

ADR-018 defines session history as successful 2xx completions recorded to `history.jsonl`. This ADR extends that contract in two ways: (1) tool-mediated requests record a `tool_trace` capturing each execution round, and (2) failed tool executions are also recorded, since tool failures are the most important events to audit. The recorder gains a `status` field (`"ok"` or `"error"`) to distinguish successful from failed entries.

For `native` mode, equivalent visibility is still required before the path can be treated as governance-complete. That visibility may come from a Clawdapus-owned proxy/broker, runner-reported telemetry, or another auditable transport, but the architecture requires parity in observable tool usage even when execution identity remains native to the surface.

Session history expands with a `tool_trace` field:

```json
{
  "agent_id": "analyst-0",
  "timestamp": "2026-03-29T14:30:00Z",
  "model": "anthropic/claude-sonnet-4",
  "request": { "messages": ["..."] },
  "response": { "content": "Your portfolio shows..." },
  "usage": { "prompt_tokens": 2000, "completion_tokens": 400, "total_rounds": 2 },
  "tool_trace": [
    {
      "round": 1,
      "tool_calls": [
        {
          "name": "trading-api.get_market_context",
          "arguments": { "claw_id": "analyst-0" },
          "result": { "ok": true, "data": { "balance": 50000 } },
          "latency_ms": 120,
          "service": "trading-api"
        }
      ],
      "round_usage": { "prompt_tokens": 800, "completion_tokens": 200 }
    }
  ]
}
```

`usage` aggregates ALL LLM calls in the chain (the runner's bill). `tool_trace` captures each round for audit.

### 9. MCP primitives: analogous projection, not equivalence

MCP defines three primitives. Each has an analogous Clawdapus concept, but the semantics differ:

| MCP primitive | MCP semantics | Clawdapus analogue | Difference |
|---|---|---|---|
| Tools | Model-invoked, JSON-RPC execution | `tools[]` → mediated manifest or native runner config | Same intent. Clawdapus uses provider-native tool calling or runner-native hosting, not required JSON-RPC in v1. |
| Resources | Application-controlled, URI-addressed data | `feeds[]` → `feeds.json` | Feeds are auto-injected context with TTL. MCP resources are on-demand and client-fetched. |
| Prompts | User-invoked templates with arguments | `skill` → mounted skill files | Skills are ambient behavioral guidance. MCP prompts are explicit user actions. |

These are analogous projections, not semantic equivalents. Clawdapus covers similar ground through its own mechanisms, optimized for compile-time determinism and proxy-mediated delivery. This ADR adds the last capability type (callable tools) using MCP's schema vocabulary, so that future MCP interop is a transport change, not a schema rewrite.

## Future Extensions

**Graduation path.** `native` mode is the preferred steady state only if audit parity exists. When a runner can consume pod-shared MCP/client configuration, `claw up` generates that config and the runner merges pod-shared tools additively with its local tool set. Native mode does not graduate on runner capability alone; pod-shared tool execution must remain auditable. The preferred audit strategy is a Clawdapus-owned proxy or MCP broker for pod-shared tools, so runners gain additive composition without giving up observable service-tool traffic. `Mediated` mode remains a supported long-term path for Clawdapus-compatible services that want brokered execution and rights projection.

**Dynamic tool context.** Context-sensitive filtering: time-of-day policies, alert-driven restriction, session-scoped escalation. Reads from cllama's existing context loader.

**Parallel tool execution.** `parallel_safe: true` annotation on tools. Concurrent execution via goroutine pool.

**Live MCP discovery.** `claw discover` command connects to a running pod's MCP services and updates baked tool schemas. Development-time convenience, not a compilation dependency.

## Consequences

**Positive:**
- Agents gain reliable, structured service interaction via one MCP-shaped capability IR that can be projected into either native runner execution or mediated execution.
- `native` mode cleanly fits enterprise and customer infrastructure where backend auth should remain authoritative and runner execution identity is real.
- `mediated` mode extends credential starvation to service tools for Clawdapus-compatible surfaces and other trusted brokered paths. Agent-facing endpoint details are omitted for services that declare managed tools.
- Uses MCP schema vocabulary while preserving Clawdapus's compile-time determinism.
- The mediated projection follows the same declare → compile → inject pattern as feeds.

**Negative:**
- Two execution modes add conceptual complexity and require clear operator guidance about when Clawdapus is only governing presentation versus fully brokering execution.
- cllama gains complexity: tool injection, execution loop, and response coordination.
- Non-streaming upstream for tool-augmented requests adds latency.
- `mediated` mode cannot transparently mix runner-local and pod-shared tools in one upstream tool round.
- `native` mode remains contingent on an auditable pod-shared transport path or telemetry path; runner capability alone is insufficient for governance-complete execution.

**Neutral:**
- `claw.describe` v1 descriptors work unchanged. The `version: 2` field gates new behavior.
- Existing skills continue to function with their role clarified as behavioral guidance.

## Implementation

### Phase 1: Compile-time IR and policy plumbing
- Add `Tools []ToolDescriptor` and `Annotations` to `internal/describe/descriptor.go`
- Add tool registry/materialization alongside feed registry/materialization in `compose_up.go`
- Reuse URL synthesis (`compose_up.go:996`) and bearer auth projection (`compose_up.go:977`)
- Add list-shaped `tools:` / `tools-defaults:` policy parsing with explicit opt-in semantics
- Write per-agent mediated `tools.json` to context directory (`internal/cllama/context.go`)
- Project tool names into CLAWDAPUS.md `## Tools` section
- Unit tests alongside existing `compose_up_test.go` and `context_test.go`

### Phase 2: Native projection and runner integration

- Generate runner-side tool/MCP/client config from the compiled tool catalog
- Load pod-shared tools additively alongside runner-local tools where a runner supports it
- Preserve backend auth as runner/surface responsibility in this path
- Define the first audit-capable native transport or telemetry strategy

### Phase 3: cllama tool injection + non-streaming mediation (OpenAI)

This is the critical path for the mediated compatibility layer. Consider subdividing into 3a (injection + single-round execution) and 3b (multi-round loop + continuity + re-streaming).

- Load `tools.json` in cllama agent context loader (`agentctx`)
- Append managed tools to the outgoing request's `tools[]` alongside runner-native tools in `handleOpenAI`
- Force `stream: false` when managed tools are injected
- Detect `tool_calls` in response, execute managed tools via HTTP
- Implement tool execution loop with budget, timeouts, and result truncation
- Return final text to runner (re-stream if needed)
- Implement transcript continuity (prefer transcript reflection for v1)
- Record `tool_trace` in session history

### Phase 4: Anthropic parity and audit polish
- Implement Anthropic-format tool handling (`tool_use` / `tool_result` content blocks)
- Unified tool execution path for both API formats
- `claw audit` tool usage reporting

### Phase 5: MCP transport and discovery
- Implement MCP client in cllama for downstream `tools/call` execution
- Support baked `.claw-tools.json` from MCP-native images
- `claw discover` command for live MCP schema updates

### Phase 6: Graduation and advanced policy
- `parallel_safe` annotation and concurrent tool execution
- Dynamic tool filtering (time-based, alert-driven)
- Expand runner-side MCP/client config coverage across drivers
- Graduate `native` mode per runner only when audit parity exists

## Implementation Status (2026-04-03)

The capability-evolution wave (this ADR + ADR-021) landed together. Current status:

**Shipped (mediated path, Phases 1–4):**
- `claw.describe` version 2 with `tools[]` parsing
- `x-claw.tools` / `tools-defaults:` pod grammar with deny-by-default semantics
- `tools.json` compiled into each subscribing agent's cllama context directory
- `CLAWDAPUS.md` `## Tools` section listing managed tool names and descriptions
- Hard error at `claw up` time when non-cllama services declare `x-claw.tools` or `x-claw.memory`
- cllama loads `tools.json` and injects managed tools into OpenAI-compatible upstream requests
- cllama loads `tools.json` and injects managed tools into Anthropic-format upstream requests
- cllama preserves runner-native tools additively on the same request surface and keeps mixed-order recovery inside the mediated model loop instead of surfacing proxy errors back to the runner
- Bounded mediation loop with `max_rounds`, per-tool timeout, total timeout, and `max_tool_result_bytes` truncation
- Structured tool error feedback within the mediated loop
- Synthetic SSE re-streaming when the runner requested streaming
- SSE keepalive/progress comments during long mediated loops
- Cross-turn continuity: hidden tool rounds reinjected into subsequent upstream requests (both formats)
- One-shot managed→native handoff continuity: if hidden managed rounds are followed by a native-only tool response, the hidden transcript is reinserted before the runner's follow-up tool-result request
- Session history `tool_trace`, `status`, and `usage.total_rounds` extensions
- `claw audit` merges session-history `tool_call` events with proxy log events

**Shipped (Phase 5 — MCP sidecar transport, 2026-04-27, issue #177):**
- `claw.describe` v2 accepts a top-level `mcp` block (`transport: streamable_http`, `path: /mcp`); when present, `tools[].http` becomes optional
- `claw up` compiles allowed MCP tools into per-agent `tools.json` with `transport: "mcp"` execution metadata, including the un-namespaced `tool_name` and reusing existing per-agent service-auth resolution
- cllama gains a Streamable HTTP MCP client (`internal/mcp`) that runs the `initialize` + `notifications/initialized` handshake, caches sessions per target, parses both JSON and `text/event-stream` JSON-RPC responses, and retries once on session-expired (HTTP 400/404)
- Managed-tool dispatch in cllama switches by transport (`http` → existing path, `mcp` → new MCP client) inside `executeManaged{OpenAI,Anthropic}Tool`; `tool_trace`, namespacing, audit, session-history, policy budgets, and credential-starvation boundaries are unchanged
- Existing HTTP managed tools and descriptors continue to work without modification; descriptors without `mcp:` still require `tools[].http`

**Shipped (Phase 5b — stdio MCP wrapper, 2026-04-28, issue #179):**
- `claw-mcp-stdio` wraps stdio MCP commands behind the same Streamable HTTP `/mcp` surface that cllama v0.5.0 already mediates
- `x-claw.mcp-stdio` wires wrapper child command/args into the sidecar environment without turning the sidecar into an agent-managed runner
- `x-claw.describe-file` lets operators provide a deterministic host-side v2 descriptor snapshot for wrapper services
- No cllama transport changes are required; compiled `tools.json` still uses `execution.transport = "mcp"`

**Shipped (Phase 5c — stdio MCP discovery, 2026-04-28, issue #182):**
- `claw discover [service...]` starts stdio MCP wrapper services ephemerally, preserves service environment and bind mounts, runs MCP `initialize` + `tools/list`, and writes `.claw-discovered/<service>.claw-describe.json`
- `claw up` loads `.claw-discovered/` snapshots for stdio sidecars after explicit `describe-file` overrides and before image/build descriptor fallback
- Discovery snapshots carry `x-claw-discovery` metadata so `claw up` can warn when the checked-in snapshot no longer matches the declared command, args, or wrapper image
- `claw up --discover-tools` is an explicit convenience for refreshing missing or stale stdio snapshots without making normal `claw up` depend on live MCP discovery

**Not yet shipped:**
- Phase 2: native projection / runner-side MCP config generation
- Phase 5 follow-up: baked `.claw-tools.json` artifact support for MCP-native images
- Phase 6: `parallel_safe` annotation, dynamic filtering, native mode graduation

See `docs/plans/2026-03-30-memory-plane-and-pluggable-recall.md` for the companion implementation-status document covering both ADR-020 and ADR-021.

## Amendment (2026-08-03): Terminal-on-Success Annotation Validation

cllama's managed mediation now supports terminal-on-success tools: a tool
annotated `"x-claw.terminalOnSuccess": true` in its descriptor ends the
mediated turn after a successful call instead of requesting another model
round. The annotation flows through the existing generic annotations
projection (descriptor → tool registry → generated `tools.json`) untouched.

Clawdapus adds compile-time validation only: cllama ignores and logs non-bool
values at runtime, so `claw up` fails closed when a descriptor declares the
key with any non-boolean value. Unknown annotation keys continue to pass
through unvalidated — the namespace remains open for service authors.
