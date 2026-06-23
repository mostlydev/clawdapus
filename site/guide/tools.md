# Managed Tools

Services declare callable tools in their `claw.describe` descriptor using MCP-shaped schemas. `claw up` compiles a per-agent `tools.json` manifest from the declared and policy-filtered tool catalog. cllama injects those tools into upstream LLM requests and mediates execution transparently.

Unlike [surfaces and skills](/guide/surfaces-and-skills), managed tools provide a callable interface rather than documented reference material. The LLM invokes a tool by name; cllama executes it.

## Declaring Tools in a Service

A service advertises callable tools in its `claw.describe` descriptor (version 2):

```json
{
  "version": 2,
  "description": "Trading Desk API",
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
  "auth": { "type": "bearer", "env": "TRADING_API_TOKEN" }
}
```

Tool schemas use MCP's vocabulary (`name`, `description`, `inputSchema`, `annotations`). The `http` field is Clawdapus-only execution metadata — the LLM never sees it.

The `readOnly` annotation distinguishes safe queries from side-effecting operations in the compiled schema. It is available to future tool policy and custom proxies. Today `claw audit` reports managed tool calls and failures, not a per-call `readOnly` column.

## Subscribing in Pod YAML

Agents subscribe to tools with a `tools:` block in their `x-claw` section. No tools are exposed unless explicitly declared — deny by default.

```yaml
services:
  analyst:
    image: analyst:latest
    x-claw:
      agent: ./AGENTS.md
      cllama: passthrough
      surfaces:
        - service://trading-api
      tools:
        - service: trading-api
          allow:
            - get_market_context   # read-only access

  executor:
    image: executor:latest
    x-claw:
      agent: ./agents/executor/AGENTS.md
      cllama: passthrough
      surfaces:
        - service://trading-api
      tools:
        - service: trading-api
          allow: all               # full access

  trading-api:
    image: trading-api:latest
    expose:
      - "4000"
```

`surfaces:` grants network reachability. `tools:` grants verb authority. Both are required for full tool access.

### Pod-Level Defaults

Use `tools-defaults:` at pod level to share tool access across multiple agents, then extend or restrict at the service level:

```yaml
x-claw:
  pod: trading-desk
  tools-defaults:
    - service: trading-api
      allow:
        - get_market_context

services:
  analyst:
    x-claw:
      cllama: passthrough
      tools:
        - ...                        # inherit pod defaults
        - service: trading-api
          allow:
            - execute_trade          # add executor-only tool
```

### Hard Error: Tools Without cllama

Declaring `x-claw.tools` on a service that does not have `cllama: passthrough` (or another cllama type) is a hard error at `claw up` time. Tools require a proxy.

## What claw up Compiles

`claw up` writes `tools.json` to each subscribing agent's context directory:

```text
.claw-runtime/context/
└── analyst/
    ├── AGENTS.md
    ├── CLAWDAPUS.md
    ├── metadata.json
    ├── feeds.json
    ├── tools.json
    └── memory.json
```

The manifest contains the resolved tool schemas, execution metadata, auth, and mediation policy:

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

Tool names are namespaced as `<service>.<tool>` to prevent collisions across services. Agents do not read this manifest — `cllama` does.

`CLAWDAPUS.md` gains a `## Tools` section listing available tool names and descriptions so the agent's behavioral contract reflects what it can call.

## Wrapping Stdio MCP Servers

Many MCP servers are distributed as stdio commands instead of HTTP services. Use the shared `ghcr.io/mostlydev/claw-mcp-stdio` image to expose those commands as a pod-internal Streamable HTTP MCP endpoint while keeping their credentials on the sidecar.

```yaml
services:
  perplexity:
    image: ghcr.io/mostlydev/claw-mcp-stdio:v0.23.1
    environment:
      PERPLEXITY_API_KEY: ${PERPLEXITY_KEY}
    expose:
      - "8080"
    x-claw:
      mcp-stdio:
        command: npx
        args: ["-y", "perplexity-mcp"]

  analyst:
    image: analyst:latest
    x-claw:
      cllama: passthrough
      tools:
        - service: perplexity
          allow: [search]
```

Run discovery once after the sidecar command and credentials are configured:

```bash
claw discover perplexity
```

`claw discover` starts the wrapper in a short-lived local container, performs MCP `initialize` and `tools/list`, then writes `.claw-discovered/perplexity.claw-describe.json`. That snapshot is the compile-time source of truth for `claw up`, so deploys remain deterministic and do not depend on live MCP discovery.

The generated descriptor uses the same v2 shape as hand-authored MCP descriptors:

```json
{
  "version": 2,
  "mcp": { "transport": "streamable_http", "path": "/mcp" },
  "tools": [
    {
      "name": "search",
      "description": "Search the web with Perplexity.",
      "inputSchema": {
        "type": "object",
        "properties": { "query": { "type": "string" } },
        "required": ["query"]
      }
    }
  ]
}
```

`x-claw.mcp-stdio` only configures the child process; the wrapper exposes `/mcp`, handles MCP session initialization, restarts the child on exit, and logs stderr through the sidecar container logs. If live discovery is unavailable, `x-claw.describe-file` is still supported as an explicit descriptor override.

## Runtime Behavior

### Tool Injection

When `tools.json` is loaded, cllama appends the compiled managed tool schemas to whatever `tools[]` the runner already declared on the outgoing LLM request, so runner-native tools (e.g. OpenClaw built-ins) and managed tools coexist on the same request surface. Tools from `tools.json` are namespaced as `<service>.<tool>` to keep them distinguishable from runner-native tool names. Both OpenAI-compatible and Anthropic formats are supported.

When managed tools are injected, cllama forces `stream: false` on the upstream request. If the runner originally requested streaming, cllama re-streams the final text response as synthetic SSE chunks after the tool chain completes. During long mediated loops, SSE keepalive comments are emitted to prevent the runner from timing out.

Requests where no managed tools are compiled pass through unchanged, including streaming.

### Mediation Loop

When the LLM returns a response containing tool calls, cllama dispatches based on which tools were called:

1. **Managed tool calls only** — cllama validates each call against the manifest, executes the tools via HTTP against the declared services, and constructs a follow-up LLM request with the tool results appended. This loop repeats until the LLM returns terminal text. The runner never sees the intermediate managed rounds — only the terminal text is returned.
2. **Runner-native tool calls only** — cllama passes the response back to the runner unchanged. The runner executes its own tools and continues the conversation normally.
3. **Managed first, native later in the same response** — cllama serializes the round instead of hard-failing. It occludes the runner-native suffix, executes the managed prefix internally, appends the managed results into the hidden transcript, and asks the model to continue from there. If the model then emits runner-native tool calls only, cllama returns that response to the runner.
4. **Managed first, native later in the same overall turn** — once cllama has hidden managed rounds and a later model response contains only runner-native tool calls, cllama returns that native tool-call response to the runner and stores a one-shot continuity handoff. On the runner's follow-up request with the native tool result, cllama reinjects the hidden managed assistant/tool transcript immediately before the native tool-call message so the upstream model still sees a coherent history.
5. **Runner-native first, managed later in the same response** — cllama still refuses to reorder the plan, but it no longer leaks a proxy error back to the runner. Instead it keeps the mixed batch hidden, feeds the model synthetic rejected-tool results explaining that managed service tools must come first, and lets the model replan on an internal follow-up turn.

Unknown managed tool names are rejected at validation time.

Within a mediated turn, cllama also detects repeated managed tool calls with the same canonical tool name and arguments. The first call executes normally; later duplicates are **not re-executed** against the providing service. By default (`CLLAMA_MANAGED_DUPLICATE_POLICY=replay`) the model-facing result of the first call is replayed for the duplicate so the model receives the data it asked for and moves on; setting `reject` restores the legacy behavior of returning a data-less `duplicate_tool_call` 409. Either way, `tool_trace` records the original round, duplicate count, streak, and policy. If a model keeps re-issuing the identical call even after the replay, a streak cutoff (`CLLAMA_MANAGED_DUPLICATE_STREAK_CUTOFF`, default 3) disables tools and forces a final answer before the round budget is exhausted. This keeps retry loops from burning MCP sidecar time — or the whole turn — on the same query.

**Budget limits** (compiled into `tools.json`):

| Limit | Default |
|-------|---------|
| `max_rounds` | 8 |
| `timeout_per_tool_ms` | 30,000ms |
| `total_timeout_ms` | 120,000ms |
| `max_tool_result_bytes` | 16,384 bytes |

The first three are configurable from the pod YAML via `x-claw.tool-policy` (service level) or `x-claw.tool-policy-defaults` (pod level). Omitted fields keep their defaults. Slow reasoning models often need a larger `total-timeout-ms` — the whole mediated turn (every inference round plus every tool execution) shares that one budget, and a turn that exceeds it fails with a 502:

```yaml
x-claw:
  tool-policy-defaults:
    total-timeout-ms: 300000

services:
  fast-bot:
    x-claw:
      tool-policy:            # replaces the pod default entirely
        max-rounds: 4
        total-timeout-ms: 60000
```

See [Pod YAML — Managed-Tool Mediation Policy](./pod-yaml#managed-tool-mediation-policy).

Tool results exceeding `max_tool_result_bytes` are truncated with an explicit `"truncated": true` flag so the LLM does not reason over partial data as complete.

### Cross-Turn Continuity

Hidden tool rounds are preserved across turns. When the runner sends its next request, cllama reinjects the hidden assistant/tool transcript so the LLM sees the full coherent history that produced each terminal response.

### Error Handling

Tool execution errors are fed back to the LLM as structured results inside the mediated loop — the LLM decides how to communicate the failure to the runner. Duplicate managed tool calls are served from the cached first-call result (or, under `reject` policy, returned as a structured duplicate error) rather than re-executed against the sidecar. If managed-tool mediation itself encounters a fatal timeout or internal failure, cllama returns `502` to the runner.

## Telemetry and Audit

Mediated requests write a `tool_trace` to session history:

```json
{
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

`claw audit` merges session-history `tool_call` events with proxy log events, so managed tool activity and failures are visible without manual ledger inspection. Every proxied request also carries `manifest_present` and `tools_count` fields in the JSON output — use them to confirm at runtime that a compiled tool manifest actually reached cllama for a given agent.

## Skills and Tools: Complementary

A service can declare both a `skill` (Anthropic skill format markdown) and `tools[]` in its descriptor. They serve different audiences:

| Concept | Format | Audience | Purpose |
|---------|--------|----------|---------|
| Tool | MCP schema | LLM function calling | Callable interface |
| Skill | Anthropic skill markdown | Agent context | When and why to use the service |
| Feed | JSON manifest | cllama injection | Live ambient data |

When a service declares tools, its endpoint details are omitted from agent-facing `CLAWDAPUS.md`. Agents interact through governed tools or not at all. Operator-facing endpoint documentation remains available via `claw inspect`.
