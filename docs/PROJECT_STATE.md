# Project State — Canonical Reconciliation

**This is the single source of truth for "what ships today vs. what is planned."** Public
docs (site + README) lead with present-tense capability and link back to the picture captured
here. ADRs and `docs/plans/` remain the historical record; this file is the index built on top
of them — do not duplicate their content, point to them.

Maintenance: when a capability lands or a release cuts, update the relevant row here in the
same change. Trust order when sources disagree: current code → tests → examples → ADRs → plans.
Last reconciled: 2026-05-31 (against `master` @ v0.21.0, issue #280).

Status legend: **Shipped** (implemented and in use) · **Partial** (some shipped; gap noted) ·
**Planned** (designed, not implemented) · **External** (depends on an out-of-tree component,
e.g. an org's Master Claw policy).

---

## ADR status

| ADR | Title | Status | Evidence / notes |
|-----|-------|--------|------------------|
| 001 | cllama Transport | Shipped | `cllama/`, `internal/cllama/` |
| 002 | Runtime Authority | Shipped | `docker compose` is sole lifecycle writer; Docker SDK read-only |
| 003 | Topology Simplification & HANDLE | Shipped | `HANDLE` directive; `x-claw.handles` |
| 004 | Service Surface Skills | Shipped | `internal/describe/`, `SURFACE`, mounted skills |
| 006 | INVOKE Scheduling | Shipped | `INVOKE` directive; `claw api schedule` |
| 007 | LLM Isolation via Credential Starvation | Shipped | cllama holds keys; agents get bearer tokens |
| 008 | cllama as Standardized Sidecar | Shipped | auto-injected proxy sidecar |
| 009 | Contract Composition & Policy | Shipped | `x-claw.include` enforce/guide/reference |
| 010 | CLI Surface Simplification | Shipped | four-verb surface; ADR-022 extends |
| 011 | Canonical-By-Default Scaffold | Shipped | `claw init`, `claw agent add` |
| 012 | Master Claw & Fleet Governance | Partial | `x-claw.master` auto-injects `claw-api` + bearer/feeds wiring (shipped). Budget caps are proxy-enforced when declared or updated through `fleet.budget.set`; autonomous budget/quarantine/recipe *decisions* are agent behavior + external policy. Drift scoring **not** shipped (see 014). |
| 013 | Context Feeds | Shipped | feeds registry; `channel-context`, `channel-awareness` |
| 014 | Telemetry Normalization & `claw audit` | Partial | `claw audit` shipped (`cmd/claw/audit.go`; columns CLAW/REQ/RESP/ERR/INT/TOOLS/TOOL_ERR/TOK_IN/TOK_OUT/COST_USD/MODELS). **No `drift_score`** anywhere in `cmd/`, `internal/`, `cllama/internal/` — drift scoring is External/Planned, not a shipped column. |
| 015 | `claw-api` Auth & Scoping | Partial | principals/scopes and write handlers shipped: `POST /fleet/restart`, `/fleet/quarantine`, `/fleet/budget/set`, `/fleet/model/restrict`, and schedule controls (`pause`, `resume`, `skip-next`, `clear-skip-next`, `fire`) route in `cmd/claw-api/handler.go`; master principals get read/write verbs in `internal/clawapi/principal.go`. `fleet.budget.set` writes governance JSON consumed by cllama for live budget/rate enforcement; model-restrict governance JSON is not yet enforced by the reference proxy. |
| 016 | Canonical Social Identity & Conformance Spikes | Shipped | `sequential-conformance`; `TestSpikeRollCall` |
| 017 | Pod Defaults & Service Self-Description | Shipped | pod defaults; `claw.describe`; feed registry |
| 018 | Session History & Persistent Memory Surfaces | Shipped | `.claw-session-history/`, `.claw-memory/`; `claw history` |
| 019 | Model Policy Authority & Declared Failover | Shipped | `models`/`models-defaults` merge + image `MODEL` overlay shipped. Declared failover execution is implemented for key/provider exhaustion via `internal/cllama/modelpolicy.go`, `cllama/internal/agentctx/modelpolicy.go`, `cllama/internal/proxy/modelpolicy.go`, and `dispatchCandidates` in `cllama/internal/proxy/handler.go`; tests cover 429/cooldown fallback and no fallback on upstream 500. |
| 020 | Compiled Tool Plane (native + mediated) | Shipped | managed tools via `claw.describe`; cllama mediated execution (v0.6.x schema) |
| 021 | Memory Plane as Compiled Capability | Shipped | pluggable memory services via `claw.describe`; channel-memory; ambient recall/retain |
| 022 | Infra Image Lifecycle & Four-Verb Surface | Shipped | `claw pull/build/up/down`; `internal/infraimages/release_manifest.go` |
| 023 | Explicit cllama Ingress Surface Matrix | Shipped | `internal/cllama/ingress.go` compiles OpenAI Chat Completions at `/v1/chat/completions` and Anthropic Messages at `/v1/messages`. Anthropic-surface providers: `anthropic`, `synthetic`, `minimax-portal`, `kimi-coding`, `cloudflare-ai-gateway`, `xiaomi`; other providers default to OpenAI chat. Runtime dispatch treats `/v1/messages` as Anthropic and other POST paths as OpenAI. |
| 024 | Runner Base Refresh from Upstream | Shipped | `claw pull` runner refresh; `built-against`/`image-id`/`recipe-sha` labels |

*(ADR-005 does not exist.)*

---

## Capability → documentation map

| Capability | Status | Public doc home | Orphaned? |
|------------|--------|-----------------|-----------|
| Four-verb operator surface (`pull`/`build`/`up`/`down`) | Shipped | quickstart, cli, what-is | No |
| `claw up` as compiler | Shipped | compilation-principles, anatomy | No |
| Clawfile → Dockerfile | Shipped | clawfile | No |
| claw-pod.yml + pod defaults / spread | Shipped | pod-yaml | No |
| cllama governance proxy | Shipped | cllama | No |
| Credential starvation | Shipped | cllama, what-is, index | No |
| Compiled/managed tool mediation (ADR-020) | Shipped | tools | No |
| Memory plane / pluggable recall (ADR-021) | Shipped | memory | No |
| Session history retention (ADR-018) | Shipped | memory, cli | No |
| `claw audit` + telemetry (ADR-014) | Shipped | cli, cllama | No |
| channel-memory + awareness digests | Shipped | memory, social-topology | No |
| claw-api + Master Claw (ADR-012/015) | Partial | manifesto, what-is, architecture | Budget caps enforced by cllama; policy decisions external; model-restrict enforcement partial |
| Social topology / HANDLE (ADR-003/016) | Shipped | social-topology | No |
| Persona materialization | Shipped | anatomy | No |
| 7 runner drivers | Shipped | drivers | No |
| Context feeds (ADR-013) | Shipped | surfaces-and-skills, cllama, social-topology | No |
| `TRACK` / recipe / bake | **Planned** | what-is roadmap, architecture | Correctly future, label it clearly |
| Drift scoring | **External/Planned** | what-is, cllama, cli, README, manifesto, architecture | No built-in score; docs mark it external/planned |

---

## Known drift inventory (fix sites)

Confirmed against code. Historical changelog entries are **excluded** (incident record — leave
alone): the downstream-deployment references in older `site/changelog.md` entries (e.g.
`:123`, `:187`) stay untouched.

**Named-deployment leaks (scrub → generic):**
- `site/index.md:79-88` — fixed by Claude in #280 lane
- `README.md:213-222` — fixed by Claude in #280 lane
- `site/guide/social-topology.md:71-77` — fixed by Codex in #280 lane
- `site/guide/pod-yaml.md:24-33` — fixed by Codex in #280 lane

**Drift-score / fabricated-output over-claims (→ describe telemetry/audit as shipped; drift as external/planned):**
- `site/guide/what-is-clawdapus.md` — fixed by Claude in #280 lane
- `README.md:559-575` — fixed by Claude; Codex corrected the audit totals example
- `site/guide/cllama.md:123`, `:359` — fixed by Codex in #280 lane
- `site/guide/cli.md:210`, `:491` — fixed by Codex in #280 lane
- `MANIFESTO.md:152`, `:161` — fixed by Claude in #280 lane

**Stale phase framing:** every "Phase N" mention → reframe present-tense or move to roadmap.

---

## Per-page checklist (issue #280)

Tier C = correctness pass (scrub names/fabricated output/stale phases, verify CLI snippets).
Tier D = deep rewrite (mental model stale/weak).

| Page | Tier | Owner | Status | Notes |
|------|------|-------|--------|-------|
| docs/PROJECT_STATE.md | — | Claude | **done** | this file (living) |
| site/guide/architecture.md (new) | D | Claude | **done** | capabilities ↔ ADRs; commit 71227a6 |
| site/index.md | D | Claude | **done** | leak + Master Claw card; commit ada159b |
| site/guide/what-is-clawdapus.md | D | Claude | **done** | real audit output, drift honest, de-phased; ada159b |
| site/manifesto.md | D | Claude | **done** | recipe→roadmap, drift clarifier, link fix; 45cb05c |
| MANIFESTO.md (root) | D | Claude | **done** | same as site manifesto; 45cb05c |
| README.md | D | Claude | **done** | leak + audit + retired v0.3.2/phase table; b4080d6 |
| site/.vitepress/config.mts | C | Claude | **done** | architecture in Introduction sidebar; 71227a6 |
| site/guide/cllama.md | D | Codex | **done** | spec-vs-reference split; context mount; telemetry; no DRIFT/private-SSH claims |
| site/guide/cli.md | D | Codex | **done** | real audit columns/totals; no invalid `--last`; history/discover/skill/update refs; memory forget requires `--agent`; planned commands marked unregistered |
| site/guide/social-topology.md | D | Codex | **done** | leak scrubbed; platform matrix refreshed |
| site/guide/pod-yaml.md | C | Codex | **done** | leak scrubbed; service/default fields refreshed; channel-context tuning clarified |
| site/guide/anatomy.md | C | Codex | **done** | proxy wording, NanoClaw runtime, durable state split |
| site/guide/clawfile.md | C | Codex | **done** | TRACK/recipe/bake marked planned; PRIVILEGE syntax fixed; matrix refreshed |
| site/guide/tools.md | C | Codex | **done** | readOnly/audit claim fixed; current stdio image tag |
| site/guide/memory.md | C | Codex | **done** | `claw history export`, required `--agent`, channel-memory/awareness coverage |
| site/guide/surfaces-and-skills.md | C | Codex | **done** | egress surface, planned skillmap, RailsTrail as planned adapter pattern |
| site/guide/compilation-principles.md | C | Codex | **done** | actual projections/manifests; RailsTrail framed as planned pattern |
| site/guide/drivers.md | C | Codex | **done** | 7-driver matrix refreshed against `internal/driver/*` |

**Out-of-scope leak found (needs its own decision):** `examples/trading-desk/` itself uses
named-deployment service names and agent files — a public-artifact leak larger than the doc
pages. Renaming ripples into
agent paths, env vars, and possibly spike fixtures, so it is **not** included in this docs PR;
flag to the maintainer as a follow-up issue.

**Release guardrails:** no changes to `release_manifest.go` pins, changelog `Latest` badge, nav
version dropdown, or cllama submodule pointer. A `## Unreleased` changelog note is allowed.
