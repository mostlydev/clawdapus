# Data Policy

Every model request a claw makes goes through cllama, so cllama is where Clawdapus enforces where the request is processed and whether the provider may train on it or keep it. The policy is part of the pod, compiled by `claw up` into each agent's cllama context. It is checked on every request, including every fallback candidate.

The default is the most conservative policy:

| Setting | Default | Meaning |
|---|---|---|
| `regions` | `[us, eu]` | The request may only be processed in the US or the EU. |
| `training` | `none` | The provider may not train on request or response content. |
| `retention` | `none` | The provider may not retain request or response content. |
| `local-retention` | `none` | cllama keeps no request or response content locally. |

A pod that declares nothing gets these defaults.

## Declared policy vs. provider guarantees

A policy states what you require. cllama only lets a request through when the requirement can be established for the actual provider endpoint it is about to call:

- **Enforced:** the provider offers a per-request control, and cllama applies it. Examples are a regional endpoint or parameter, a no-store flag, and routing preferences that exclude providers that collect data.
- **Attested:** the guarantee exists only at the account level, such as a zero-data-retention agreement or a data-residency project. You attest it for that provider endpoint in the pod.
- **Unestablished:** the request is refused with a client error. An audit intervention names the requirement that could not be met. Nothing is silently downgraded.

An attestation cannot override a known contradiction. If a model or feature is documented to retain or train on data, attesting the opposite does not make the request pass.

## What each provider can establish

| Provider | Processing region | No training | No retention |
|---|---|---|---|
| OpenAI | Recognized US/EU regional processing endpoints; otherwise a `processing:<region>` attestation. A regional storage endpoint (e.g. Japan's) is storage residency, not in-region inference. | `no-training` attestation for the account. | `store: false` is applied but does not establish zero retention: a `zero-data-retention` attestation is required, and stateful or non-eligible features are refused. |
| Anthropic | On supported Messages models, cllama sets `inference_geo: us` when the US is allowed. Other regions and older models need a `processing:<region>` attestation. | `no-training` attestation. | `zero-data-retention` attestation, eligible features only. Model families whose standard offering mandates retention are refused under `retention: none`. |
| Google (Gemini API) | The global Developer API endpoint establishes no region; a regional deployment needs a `processing:<region>` attestation. | `no-training` attestation (an API key does not prove the paid tier). | Paid no-training is not zero retention; an eligible deployment must be attested. |
| OpenRouter | Its US/EU regional origins constrain shared endpoints, but your bring-your-own-key deployments and observability destinations stay your responsibility. Even on a regional origin, a `processing:<region>` attestation is required, and the global origin is never used as a fallback. | cllama sets `provider.data_collection: "deny"`. | cllama sets `provider.zdr: true`; a `zero-data-retention` attestation must also cover any observability destinations you configured. A request's own `provider` preferences may narrow routing but never weaken these. |
| Vercel AI Gateway | No region is inferred from the global URL; `processing:<region>` attestation. | `no-training` attestation. | cllama sets `providerOptions.gateway.zeroDataRetention: true`, and a `zero-data-retention` attestation is also required: bring-your-own-key eligibility and your own destinations cannot be inferred. Other `providerOptions` are refused. |
| xAI | The documented US endpoint keeps processing in the US; others need an attestation. | `no-training` attestation. | Team-level `zero-data-retention` attestation before any content is sent. |
| Meta, custom endpoints, Ollama | Nothing is inferred from a provider name or hostname (an Ollama URL may be remote): `processing:<region>` attestation for that endpoint. | `no-training` attestation. | `zero-data-retention` attestation. |

Sources: [OpenAI data controls](https://developers.openai.com/api/docs/guides/your-data), [OpenAI prompt caching](https://developers.openai.com/api/docs/guides/prompt-caching), [Anthropic data residency](https://platform.claude.com/docs/en/manage-claude/data-residency), [Anthropic retention](https://platform.claude.com/docs/en/manage-claude/api-and-data-retention), [Gemini API ZDR](https://ai.google.dev/gemini-api/docs/zdr), [OpenRouter provider routing](https://openrouter.ai/docs/guides/routing/provider-selection), [OpenRouter in-region routing](https://openrouter.ai/docs/guides/features/in-region-routing), [Vercel AI Gateway ZDR](https://vercel.com/docs/ai-gateway/security-and-compliance/zdr), [xAI security](https://docs.x.ai/developers/faq/security).

An attestation is your assertion about an account or deployment agreement. cllama cannot discover it from an API key; it records which attestation allowed each request.

## Declaring a policy

Pod-level `x-claw.data-policy-defaults` applies to every service. A service-level `x-claw.data-policy` replaces it entirely; the two are not merged.

```yaml
x-claw:
  pod: support
  data-policy-defaults:
    regions: [us, eu]
    training: none
    retention: none
    local-retention: none
    attestations:
      # Each guarantee must be true of your account; cllama cannot verify it.
      - provider: openai
        guarantees: [no-training, processing:us, zero-data-retention]
      - provider: anthropic                # cllama sets inference_geo: us itself
        guarantees: [no-training, zero-data-retention]
```

Fields:

- `regions`: lowercase region codes (`us`, `eu`, `jp`, `uk`, `ca`, `au`, `in`, `sg`, `kr`, ...), or `global` on its own to allow any region.
- `training`: `none` or `allowed`.
- `retention`: provider retention, `none` or `provider-default`.
- `local-retention`: cllama's own copy of request and response content, `none` or `allowed`.
- `attestations`: account-level guarantees you vouch for. Each entry has:
  - `provider`;
  - an optional `endpoint`;
  - `guarantees`, from `zero-data-retention`, `no-training`, `processing:<region>` and `storage:<region>`.

  Without `endpoint`, an attestation covers only the provider's official endpoints, never a base URL overridden with `*_BASE_URL`. To vouch for a custom, self-hosted or regional endpoint, name it: an `https` URL, or `http` for a local or in-pod host.

Unknown keys are rejected, so a misspelled field fails `claw up` instead of quietly keeping a default.

## Expanding to Japan and other regions

Adding a region allows it; it does not route traffic there by itself. cllama then accepts providers that can show processing in any allowed region.

Allow Japan alongside the US and EU:

```yaml
x-claw:
  data-policy-defaults:
    regions: [us, eu, jp]
```

Allowing a region grants permission; it does not create provider support. Japan therefore also needs an endpoint you have set up for in-region processing and attest. Here a service must stay in Japan:

```yaml
services:
  jp-support:
    x-claw:
      data-policy:
        regions: [jp]
        attestations:
          - provider: openai
            endpoint: https://<your-jp-endpoint>/v1
            guarantees: [no-training, processing:jp, storage:jp, zero-data-retention]
      cllama-env:
        OPENAI_BASE_URL: https://<your-jp-endpoint>/v1
```

Allow any region, for example for a development pod:

```yaml
x-claw:
  data-policy-defaults:
    regions: [global]
```

## Training and retention

Opting in is explicit and per service:

```yaml
services:
  research:
    x-claw:
      data-policy:
        training: allowed          # e.g. a free tier that trains on content
        retention: provider-default
```

## Local retention

With `local-retention: none`, cllama still records metadata for each finished completion and managed failure: ids, model, token usage, cost, status and timestamps. A refused request is logged as an audit intervention, not a history entry. Budgets, cost reporting and `claw audit` keep working. Request, response and tool content, context snapshots, and automatically retained memory are not written. Channel cursor ledgers hold only message IDs and are kept. Existing files are never deleted. `claw history` and memory features that replay past content need `local-retention: allowed`.

The [trace journal](/guide/cllama#trace-journal) is a separate explicit opt-in (`CLLAMA_TRACE_JOURNAL: "on"`). It keeps journal content for verifiable run traces even when `local-retention` is `none`, independent of the provider's retention. It does not re-enable normal history or snapshots.

This policy covers model-provider data and proxy observability. It does not delete application or business records, or mounted contracts.

An OpenRouter `provider.only` pin such as `google-vertex/global` selects an upstream endpoint; it does not establish where processing happens, and does not satisfy a regional policy on its own.

`eu` always means your EU requirement. OpenRouter's EU origin covers a broader European area (EEA, UK, Switzerland), so on its own it cannot prove EU-only processing. Establish a matching EU deployment, or explicitly allow the additional countries.

## Claws outside cllama

The policy is enforced at cllama. A claw service that does not route model requests through cllama is not covered, and `claw up` warns about each one.

## Upgrading

Data policy is enforced by default from cllama v0.14.0. Pods that relied on a provider without per-request controls start getting refused requests after upgrading. Fix this either by attesting the guarantees your provider accounts actually have, or by relaxing the policy explicitly. `claw audit` shows which requirement was unmet.
