# ADR-007: LLM Isolation via Credential Starvation

**Date:** 2026-02-21
**Status:** Accepted

## Context

When the `CLLAMA` directive is used, we must guarantee that all of an agent's LLM inference traffic routes through the `cllama` sidecar proxy for policy enforcement and drift scoring.

A strict infrastructure approach to guarantee this interception would be **network isolation**: placing the agent container on an internal Docker network where its only allowed egress is the sidecar. However, this severely degrades agent utility. An agent completely cut off from the internet cannot natively communicate with chat platforms (Discord, Slack), fetch external web pages, or use API-based tools unless we build a complex, generalized egress proxy for all HTTP traffic. 

We need a way to strictly isolate LLM traffic to the sidecar without restricting general internet access.

## Decision

We enforce LLM interception through **Credential Starvation** combined with **Driver-Level Configuration Injection**, rather than network isolation.

1. **Configuration Injection:** When `CLLAMA` is active, the driver configures the runner's LLM provider settings (e.g., `baseURL`) to point to the local `cllama` sidecar endpoint.
2. **Credential Starvation:** The real LLM provider API keys (e.g., `ANTHROPIC_API_KEY`) are *never* provided to the agent container. They are securely mounted only into the `cllama` sidecar. The agent container is provisioned with a local, dummy token (e.g., `CLLAMA_TOKEN_...`).

## Rationale

This approach achieves strict isolation without breaking general egress:

- **Security Guarantee:** Even if a malicious prompt or compromised runner bypasses the injected `baseURL` configuration and attempts to `curl` `api.anthropic.com` directly, the request will fail (`401 Unauthorized`) because the agent does not possess the real API keys.
- **Utility Preservation:** The agent container remains on a standard Docker network with internet egress, allowing it to function normally on Discord, Slack, and external APIs using its own platform-specific credentials.
- **Simplicity:** It avoids the massive scope creep of building a universal HTTP egress proxy or managing complex iptables/network policies.

## Consequences

**Positive:**
- Perfect guarantee that all successful LLM inference passes through the policy sidecar.
- Agent utility (web browsing, tool use, chat platform connections) is entirely preserved.
- Architecture remains simple; no custom network layers required.
- **Compute Metering & Cost Control:** Because the proxy is the sole bearer of provider keys, it can seamlessly rewrite outbound requests to force a specific model (e.g., downgrading an agent's request from `gpt-4o` to `claude-3-haiku`) regardless of what the agent runner attempts to configure. This guarantees hard budget constraints on untrusted agents.

**Negative:**
- We rely on the operator to correctly supply the real API keys to the sidecar environment and *not* to the agent environment during pod configuration. (Clawdapus pod generation tooling should help enforce this separation).
