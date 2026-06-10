# Ollama Quickstart — Zero Credentials

A governed LLM agent with **no API keys, no Discord, no secrets**. One agent, one local model, and the full Clawdapus governance plane: every turn routes through the cllama proxy and lands in the audit log.

This is the fastest way to see what `claw up` actually compiles and what governance looks like on turn one.

## Prerequisites

- Docker with Compose
- The `claw` CLI ([install](https://clawdapus.dev/guide/quickstart#install))

That's the whole list. The pod includes an Ollama sidecar that pulls a ~400MB model on first start. No `.env` file is needed.

## How it's wired

The agent declares `MODEL primary openai/qwen2.5:0.5b` and the pod points the proxy's `openai` provider at the Ollama sidecar:

```yaml
cllama-env:
  OPENAI_API_KEY: "local-ollama"   # not a secret — Ollama ignores auth,
                                   # but the proxy's key pool needs an entry
  OPENAI_BASE_URL: "http://ollama:11434/v1"
surfaces:
  - service://ollama               # grants the pod-internal network path
```

The agent itself never sees a base URL or a key — it only knows the proxy. Swap `OPENAI_BASE_URL` for any OpenAI-compatible endpoint later and nothing else changes.

## Run

```bash
claw pull        # pinned infra images + runner bases
claw build       # compile Clawfile -> agent image
claw up -d       # compile pod -> compose, launch, verify
```

First start pulls `qwen2.5:0.5b` into `./.ollama-models/` (one-time, ~400MB).

## See a governed turn

The pod schedules a reflection prompt every 5 minutes. Fire it immediately instead of waiting:

```bash
claw api schedule list
claw api schedule fire <id>
```

Then look at the governance plane:

```bash
claw audit            # the turn: model, latency, tokens — through the proxy
claw logs assistant   # the agent's actual output
```

That `claw audit` line is the point of this example: the agent never held a credential, never talked to a model server directly, and every inference is accounted for.

## Using a host Ollama instead

If you already run Ollama on your machine, delete the `ollama` service and the `surfaces:` block from `claw-pod.yml` and point the proxy at the host:

```yaml
      cllama-env:
        OPENAI_API_KEY: "local-ollama"
        OPENAI_BASE_URL: "http://host.docker.internal:11434/v1"
```

Any model you've pulled works — change `MODEL primary openai/<model>` in the `Clawfile` and run `claw build && claw up -d` again.

## Next steps

- [Quickstart](https://clawdapus.dev/guide/quickstart) — the Discord-connected path
- [Hermes quickstart](https://clawdapus.dev/guide/hermes) — the Hermes runner
- [Managed tools](https://clawdapus.dev/guide/tools) — give agents governed, schema-validated tools
