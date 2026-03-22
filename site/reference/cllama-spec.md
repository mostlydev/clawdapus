# cllama Specification

`cllama` is an open standard for a context-aware, bidirectional LLM governance proxy. It runs as a shared pod-level service managed by Clawdapus, serving multiple autonomous agents within the same pod.

This page summarizes the key concepts. For the full specification, see [CLLAMA_SPEC.md on GitHub](https://github.com/mostlydev/clawdapus/blob/master/docs/CLLAMA_SPEC.md).

## Core Principles

- **Bidirectional interception** -- intercepts outbound prompts (agent to provider) and inbound responses (provider to agent).
- **Multi-agent identity** -- a single proxy serves all agents in a pod. Identity is established via unique per-agent bearer tokens.
- **Credential starvation** -- agent containers receive dummy tokens. The proxy holds the real provider API keys. No credentials, no bypass.
- **Context-aware authorization** -- the proxy uses bearer tokens to load agent identity, active rules, and available tools for dynamic allow/deny/amend decisions.

## Transport Model

The proxy exposes an HTTP API compatible with the OpenAI Chat Completions API.

| Property | Value |
|----------|-------|
| Endpoint | `POST /v1/chat/completions` |
| Listen port | `0.0.0.0:8080` |
| Base URL (as seen by runner) | `http://cllama-<type>:8080/v1` |
| Auth header | `Authorization: Bearer <agent-id>:<secure-secret>` |

Clawdapus configures each agent's runner to use the proxy URL as its LLM base URL. The runner thinks it is talking directly to the model provider. Two distinct code paths handle OpenAI format (`messages[]`) and Anthropic format (top-level `system` field).

::: info Proxy chaining
The wire protocol supports chained proxies, but runtime currently allows only one proxy type per pod. Declaring multiple proxy types fails fast until chain execution is implemented.
:::

## Identity Resolution

Every agent in the pod receives a unique bearer token during `claw up`. The token format is:

```
<agent-id>:<secure-secret>
```

The `agent-id` portion maps directly to a subdirectory under `CLAW_CONTEXT_ROOT`. The `secure-secret` is validated against the principals stored in `metadata.json` for that agent.

When a request arrives, the proxy:

1. Extracts the `<agent-id>` from the bearer token.
2. Loads the agent's context from `CLAW_CONTEXT_ROOT/<agent-id>/`.
3. Validates the `<secure-secret>` against `metadata.json` principals.
4. Checks the requested model against the agent's allowed models.

For `count > 1` services, each ordinal gets its own identity. A service named `analyst` with `count: 3` produces `analyst-0`, `analyst-1`, and `analyst-2`, each with independent bearer tokens and context directories. Tokens and context are per ordinal, not per base service name.

## Context Mount Layout

Clawdapus bind-mounts a shared context directory into the proxy container. Each agent gets a subdirectory containing its compiled governance context.

```
/claw/context/
  analyst-0/
    AGENTS.md          # Compiled contract (includes, enforce, guide)
    CLAWDAPUS.md       # Infrastructure map (surfaces, skills, peers)
    metadata.json      # Identity, handles, active policy modules
  analyst-1/
    ...
```

| File | Purpose |
|------|---------|
| `AGENTS.md` | The agent's compiled behavioral contract, including inlined `enforce` and `guide` content from `INCLUDE` directives. |
| `CLAWDAPUS.md` | Infrastructure context: surfaces, mount paths, peer handles, feeds, and available skills. |
| `metadata.json` | Machine-readable identity (handles, allowed models, bearer token auth). |

Host-side path: `.claw-runtime/context/<agent-id>/`. Mounted into the cllama container at `/claw/context/<agent-id>/`. The `context/` directory segment is required in both host and container paths.

## Environment Variables

The proxy receives global pod context via environment variables.

| Variable | Description |
|----------|-------------|
| `CLAW_POD` | The pod name. |
| `PROVIDER_API_KEY_*` | Real provider keys (e.g. `OPENAI_API_KEY`, `ANTHROPIC_API_KEY`). |
| `CLAW_CONTEXT_ROOT` | Path to the shared context directory (defaults to `/claw/context`). |

Provider API keys belong in `x-claw.cllama-env` at the service level, not in regular agent `environment:` blocks.

## Request Lifecycle

### Pre-flight

Identity resolution, token validation, and model authorization.

### Outbound interception

The outbound interception phase (spec section 4.B) is where the proxy enforces governance before the request reaches the LLM provider. The proxy may perform any combination of the following:

#### Context aggregation

The proxy parses the `enforce` rules from the agent-specific `AGENTS.md`. These rules form the behavioral contract that governs what the agent is allowed to do.

#### Tool scoping

When the agent's request contains a `tools` array, the proxy evaluates each tool against the agent's identity and active policy modules. Tools not authorized for the agent's contracted role can be silently dropped from the request before it reaches the provider. This prevents agents from invoking capabilities outside their designated scope, even if the underlying runner exposes them.

#### Prompt decoration (pre-prompting)

The proxy may modify the outbound `messages` array to inject operator-defined rules, priorities, or warnings. This decoration happens transparently -- the agent has no visibility into what was added. Use cases include injecting safety guidelines, organizational policies, or situational context that the agent's base contract does not cover.

#### Policy blocking

If the outbound prompt violates a loaded policy module or `enforce` rule, the proxy may short-circuit the request entirely. Instead of forwarding to the provider, it returns an error or a mock response. This is the hard enforcement boundary -- requests that cannot be made safe through decoration are rejected outright.

#### Forced model routing and rate limiting

Even if the agent requests a specific model (e.g., `gpt-4o`), the proxy may seamlessly rewrite the `model` field to use a different, operator-approved model (e.g., `claude-3-haiku`). The agent never knows its model was downgraded. Combined with rate limiting via `429 Too Many Requests` responses, this allows operators to enforce strict compute budgets, meter usage across the fleet, and prevent runaway agents from burning tokens.

### Provider execution

The proxy strips the dummy token, attaches the real provider API key, and forwards the request upstream.

### Inbound interception

The inbound interception phase (spec section 4.D) evaluates the provider's response before it reaches the agent. The proxy may perform:

#### Response amendment

The proxy evaluates the provider's response against the `enforce` rules in the agent's contract (`/claw/context/<agent-id>/AGENTS.md`) and the active policy modules. If the response violates the tone, instructions, or restrictions defined in the contract, the proxy may rewrite the content before the agent sees it. This is the response-side counterpart to prompt decoration.

#### PII leakage blocking

The proxy can detect and redact personally identifiable information in outbound responses. If the provider's response contains data that should not flow back to the agent (customer names, account numbers, internal identifiers), the proxy strips or masks it. This is especially relevant for agents operating on sensitive datasets where the LLM may inadvertently surface restricted information.

#### Drift scoring

The proxy quantifies how far the provider's raw response drifted from the agent's ideal behavior as defined in its contract. The drift score is a numeric metric emitted in telemetry. The scoring methodology is organization-specific and not defined by the cllama standard -- operators implement their own scoring logic based on their governance requirements.

### Egress

The (potentially amended) response is returned to the agent.

## Telemetry Format

The proxy emits structured JSON logs to stdout, one line per event. Clawdapus collects these for the `claw audit` command.

### Fields

| Field | Description |
|-------|-------------|
| `timestamp` | ISO-8601 timestamp. |
| `claw_id` | The calling agent's identifier. |
| `type` | Event type: `request`, `response`, `error`, `intervention`. |
| `intervention` | Why the proxy modified a prompt, dropped a tool, or amended a response. References the specific policy module or rule. |
| `model` | The model used for the request. |
| `tokens_in` | Input token count. |
| `tokens_out` | Output token count. |
| `cost` | Estimated cost for the request/response pair. |
| `latency` | Request duration. |

### Spec divergences

The reference implementation has a few known divergences from the spec document:

- The `intervention` field is typed as `*string` with no `omitempty` tag. Every event emits `"intervention": null`, even when no intervention occurred.
- The implementation emits four `type` values: `request`, `response`, `error`, and `intervention`. The spec (section 5) omits `error` from its type enum and lists `drift_score` instead.
- The spec uses the field name `intervention_reason` where the reference logger uses `intervention`.

These divergences are documented here as practical guidance. The reference implementation is the source of truth for runtime behavior.

## Ecosystem Implementations

### Passthrough reference

The reference image (`ghcr.io/mostlydev/cllama`) implements the v1 API contract as a pure transparent proxy:

- Bearer-token identity resolution and validation.
- Environment validation (`CLAW_POD`, `CLAW_CONTEXT_ROOT`, provider credentials).
- Structured audit logging of all traffic.
- No prompt decoration, no response amendment.

This image is used for testing and serves as the starting point for building custom policy engines.

### Future: cllama-policy

The next planned implementation is `cllama-policy`, which adds bidirectional interception -- prompt decoration, tool scoping, response amendment, and drift scoring. The passthrough reference establishes the transport and identity contract; `cllama-policy` builds the governance logic on top.

### Third-party engines

Any OpenAI-compatible proxy that consumes the Clawdapus context mount layout can act as a governance layer. The spec defines the contract, not the implementation. Operators can build proprietary engines incorporating advanced DLP, RAG-based context injection, or conversational configuration.

### ClawRouter

[ClawRouter](https://github.com/BlockRunAI/ClawRouter) is a specialized cllama implementation focused on forced model routing, rate limiting, and compute metering. It intercepts model requests, evaluates them against organizational budgets or provider availability, and dynamically routes, downgrades, or rate-limits requests to contain costs across a fleet of untrusted agents.

## Implementation Notes

These notes reflect the current state of the reference implementation (`cllama/` submodule) and are useful for debugging or extending.

### Proxy handler

The proxy handler (`cllama/internal/proxy/handler.go`) is pure passthrough. It rewrites the `model` field in the request body and forwards everything else unchanged. There is no prompt decoration, no system message injection, and no middleware hook system. Two distinct code paths handle:

- **OpenAI format:** Standard `messages[]` array.
- **Anthropic format:** Top-level `system` field alongside `messages[]`.

### Context mount contents

The `agentctx` struct currently holds only three fields: `AgentsMD`, `ClawdapusMD`, and `Metadata` (used for bearer token auth). There are no outbound service credentials, no feed manifests, and no decoration config in the context mount today.

### Operator dashboard

The cllama proxy includes a real-time web dashboard for operator visibility.

| Property | Value |
|----------|-------|
| Host port | `8181` (default) |
| Container port | `8081` |

The dashboard shows live agent activity, provider status, token usage, and cost breakdown across the pod.

### Build and publish

The cllama image supports multi-architecture builds:

```bash
docker buildx build \
  --platform linux/amd64,linux/arm64 \
  -t ghcr.io/mostlydev/cllama:latest \
  --push cllama/
```

The `cllama/` directory is a git submodule pointing to a private SSH repo. Fresh clones leave it empty. The published image on `ghcr.io` is public, so end users pull the pre-built image rather than building from source.

### Logger internals

The logger (`cllama/internal/logging/logger.go`) writes one JSON object per line to stdout. Key implementation details:

- The `intervention` field is declared as `*string` (pointer to string). Because there is no `omitempty` struct tag, Go's JSON marshaler emits `"intervention": null` on every event, even when no intervention occurred. This is intentional -- it ensures log parsers can rely on the field always being present.
- Every request/response pair produces two log events: one with `type: "request"` on ingress and one with `type: "response"` on egress. Error events use `type: "error"`. Intervention events use `type: "intervention"`.
- Token counts (`tokens_in`, `tokens_out`) and cost estimates are extracted from the provider's response headers or body and attached to the response event.

### Image resolution

When `claw up` encounters a cllama proxy declaration, it resolves the image through the standard `ensureImage()` fallback chain:

1. Check if the image exists locally.
2. Attempt `docker pull` from the registry.
3. Attempt a local Dockerfile build.
4. Attempt a git URL build.

For the public `ghcr.io/mostlydev/cllama` image, step 2 succeeds on most systems. The git URL fallback does not work for cllama because the Docker builder cannot access the private submodule repo.

## Pod Configuration

### Declaring a cllama proxy

The proxy is declared in `claw-pod.yml` via the `cllama` field on a service's `x-claw` block:

```yaml
services:
  analyst:
    x-claw:
      agent: analyst
      cllama: passthrough
      cllama-env:
        OPENAI_API_KEY: ${OPENAI_API_KEY}
        ANTHROPIC_API_KEY: ${ANTHROPIC_API_KEY}
```

The `cllama` value specifies the proxy type. Currently only `passthrough` ships as a reference implementation.

### Provider keys

Provider API keys are declared in `x-claw.cllama-env`, not in the service's regular `environment:` block. This separation ensures that real credentials flow only to the proxy container, not to the agent container.

For pods with multiple services using the same provider keys, use YAML anchors:

```yaml
x-claw-env: &cllama-keys
  OPENAI_API_KEY: ${OPENAI_API_KEY}
  ANTHROPIC_API_KEY: ${ANTHROPIC_API_KEY}

services:
  analyst:
    x-claw:
      agent: analyst
      cllama: passthrough
      cllama-env: *cllama-keys
  researcher:
    x-claw:
      agent: researcher
      cllama: passthrough
      cllama-env: *cllama-keys
```

### Count expansion with cllama

When a service declares both `cllama` and `count > 1`, each ordinal gets its own bearer token and context directory. The proxy authenticates each ordinal independently:

```yaml
services:
  analyst:
    x-claw:
      agent: analyst
      cllama: passthrough
      count: 3
```

This produces `analyst-0`, `analyst-1`, and `analyst-2`, each with:
- A unique bearer token in format `analyst-N:<secret>`
- A context directory at `/claw/context/analyst-N/`
- Independent telemetry tagged with `claw_id: analyst-N`

## Security Model

### Credential isolation

The proxy enforces a strict credential boundary. Agent containers never see real provider API keys. The flow is:

1. `claw up` generates a dummy bearer token for each agent.
2. The agent's runner is configured with the proxy URL and dummy token.
3. The proxy receives the dummy token, validates it, strips it, and attaches the real provider key.
4. The agent cannot extract the real key because it only communicates with the proxy, never directly with the provider.

### Network isolation

Within the pod's Docker network, agents can reach the proxy at `http://cllama-<type>:8080`. They cannot reach the provider directly because no provider credentials exist in their environment. Even if an agent attempted to call the provider API directly, it would lack authentication.

### Token validation

Bearer tokens are validated against the `principals` field in each agent's `metadata.json`. A request with an invalid or missing token is rejected before any provider call is made. This is fail-closed: unknown tokens are denied, not passed through.
