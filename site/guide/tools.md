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

The `readOnly` annotation distinguishes safe queries from side-effecting operations. This distinction surfaces in `claw audit` output and can be used by future tool policy.

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

## Runtime Behavior

### Tool Injection

When `tools.json` is loaded, cllama replaces the outgoing LLM request's `tools[]` with the compiled managed tool schemas. Both OpenAI-compatible and Anthropic formats are supported.

When managed tools are injected, cllama forces `stream: false` on the upstream request. If the runner originally requested streaming, cllama re-streams the final text response as synthetic SSE chunks after the tool chain completes. During long mediated loops, SSE keepalive comments are emitted to prevent the runner from timing out.

Requests where no managed tools are compiled pass through unchanged, including streaming.

### Mediation Loop

When the LLM returns a `tool_call`:

1. cllama validates the call against the manifest — unknown tools are rejected
2. cllama executes the tool via HTTP against the declared service
3. cllama constructs a follow-up LLM request with the tool result appended
4. This loop repeats until the LLM returns terminal text
5. cllama returns the final response to the runner

The runner never sees the intermediate tool rounds. Only the terminal text is returned.

**Budget limits** (configurable in pod YAML, compiled into `tools.json`):

| Limit | Default |
|-------|---------|
| `max_rounds` | 8 |
| `timeout_per_tool_ms` | 30,000ms |
| `total_timeout_ms` | 120,000ms |
| `max_tool_result_bytes` | 16,384 bytes |

Tool results exceeding `max_tool_result_bytes` are truncated with an explicit `"truncated": true` flag so the LLM does not reason over partial data as complete.

### Cross-Turn Continuity

Hidden tool rounds are preserved across turns. When the runner sends its next request, cllama reinjects the hidden assistant/tool transcript so the LLM sees the full coherent history that produced each terminal response.

### Error Handling

Tool execution errors are fed back to the LLM as structured results inside the mediated loop — the LLM decides how to communicate the failure to the runner. If cllama itself encounters a fatal error (budget exhaustion, internal failure), it returns `502` to the runner.

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

`claw audit` merges session-history `tool_call` events with proxy log events, so managed tool activity and failures are visible without manual ledger inspection.

## Skills and Tools: Complementary

A service can declare both a `skill` (Anthropic skill format markdown) and `tools[]` in its descriptor. They serve different audiences:

| Concept | Format | Audience | Purpose |
|---------|--------|----------|---------|
| Tool | MCP schema | LLM function calling | Callable interface |
| Skill | Anthropic skill markdown | Agent context | When and why to use the service |
| Feed | JSON manifest | cllama injection | Live ambient data |

When a service declares tools, its endpoint details are omitted from agent-facing `CLAWDAPUS.md`. Agents interact through governed tools or not at all. Operator-facing endpoint documentation remains available via `claw inspect`.
