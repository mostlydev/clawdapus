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
| 012 | Master Claw & Fleet Governance | Partial | `x-claw.master` auto-injects `claw-api` + bearer/feeds wiring (shipped). Autonomous budget/quarantine/recipe *decisions* are agent behavior + external policy, not enforced infra. Drift scoring **not** shipped (see 014). |
| 013 | Context Feeds | Shipped | feeds registry; `channel-context`, `channel-awareness` |
| 014 | Telemetry Normalization & `claw audit` | Partial | `claw audit` shipped (`cmd/claw/audit.go`; columns CLAW/REQ/RESP/ERR/INT/TOOLS/TOOL_ERR/TOK_IN/TOK_OUT/COST_USD/MODELS). **No `drift_score`** anywhere in `cmd/`, `internal/`, `cllama/internal/` — drift scoring is External/Planned, not a shipped column. |
| 015 | `claw-api` Auth & Scoping | Partial | principals/scopes shipped; some write verbs (`fleet.restart` etc.) defined but handlers not implemented (per CLAUDE.md). VERIFY (Codex) current write-plane state. |
| 016 | Canonical Social Identity & Conformance Spikes | Shipped | `sequential-conformance`; `TestSpikeRollCall` |
| 017 | Pod Defaults & Service Self-Description | Shipped | pod defaults; `claw.describe`; feed registry |
| 018 | Session History & Persistent Memory Surfaces | Shipped | `.claw-session-history/`, `.claw-memory/`; `claw history` |
| 019 | Model Policy Authority & Declared Failover | Partial | `models`/`models-defaults` merge + image `MODEL` overlay shipped. VERIFY (Codex) whether declared *failover* execution is implemented or design-only. |
| 020 | Compiled Tool Plane (native + mediated) | Shipped | managed tools via `claw.describe`; cllama mediated execution (v0.6.x schema) |
| 021 | Memory Plane as Compiled Capability | Shipped | pluggable memory services via `claw.describe`; channel-memory; ambient recall/retain |
| 022 | Infra Image Lifecycle & Four-Verb Surface | Shipped | `claw pull/build/up/down`; `internal/infraimages/release_manifest.go` |
| 023 | Explicit cllama Ingress Surface Matrix | Shipped | VERIFY (Codex) exact current matrix vs ADR |
| 024 | Runner Base Refresh from Upstream | Shipped | `claw pull` runner refresh; `built-against`/`image-id`/`recipe-sha` labels |

*(ADR-005 does not exist.)*

---

## Capability → documentation map

| Capability | Status | Public doc home | Orphaned? |
|------------|--------|-----------------|-----------|
| Four-verb operator surface (`pull`/`build`/`up`/`down`) | Shipped | quickstart, cli, what-is | No |
| `claw up` as compiler | Shipped | compilation-principles, anatomy | No |
| Clawfile → Dockerfile | Shipped | clawfile | No |
| claw-pod.yml + pod defaults / spread | Shipped | pod-yaml | Thin on defaults/spread |
| cllama governance proxy | Shipped | cllama | No (but drift over-claim) |
| Credential starvation | Shipped | cllama, what-is, index | No |
| Compiled/managed tool mediation (ADR-020) | Shipped | tools | Partial — mediated vs native modes thin |
| Memory plane / pluggable recall (ADR-021) | Shipped | memory | No |
| Session history retention (ADR-018) | Shipped | memory | `claw history` command under-documented |
| `claw audit` + telemetry (ADR-014) | Shipped | cli, cllama | **Mislabeled** "Phase 5"; drift column fabricated |
| channel-memory + awareness digests | Shipped | (changelog only) | **Orphaned** — no guide page |
| claw-api + Master Claw (ADR-012/015) | Partial | (manifesto, what-is) | Master Claw over-claims autonomy/drift |
| Social topology / HANDLE (ADR-003/016) | Shipped | social-topology | Leak + thin |
| Persona materialization | Shipped | (anatomy?) | VERIFY doc home |
| 7 runner drivers | Shipped | drivers | No |
| Context feeds (ADR-013) | Shipped | surfaces-and-skills | VERIFY coverage |
| `TRACK` / recipe / bake | **Planned** | what-is ("Phase 6") | Correctly future, label it clearly |
| Drift scoring | **External/Planned** | what-is, cllama, cli, README, manifesto | **Over-claimed as shipped** |

---

## Known drift inventory (fix sites)

Confirmed against code. Historical changelog entries are **excluded** (incident record — leave
alone): `site/changelog.md:123` (Logan), `:187` (tiverton-house/weston) stay untouched.

**Named-deployment leaks (scrub → generic):**
- `site/index.md:79-88` — `tiverton` / `TIVERTON_DISCORD_ID`
- `README.md:213-222` — same
- `site/guide/social-topology.md:71-77` — same
- `site/guide/pod-yaml.md:24-25` — same *(found beyond Codex's list)*

**Drift-score / fabricated-output over-claims (→ describe telemetry/audit as shipped; drift as external/planned):**
- `site/guide/what-is-clawdapus.md` — fabricated `claw ps` DRIFT column + `claw audit` "Phase 5"
- `README.md:559-575` — drift claims
- `site/guide/cllama.md:123`, `:359`
- `site/guide/cli.md:210`, `:491`
- `MANIFESTO.md:152`, `:161`

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
| site/guide/cllama.md | D | Codex | todo | drift @123/@356-363; spec-vs-reference split; telemetry fields; "private SSH" line |
| site/guide/cli.md | D | Codex | todo | audit columns (add TOOLS/TOOL_ERR) + invalid `--last` example; missing commands (discover/history export/skill install/update); remove DRIFT `claw ps` sample; `memory forget --agent` required; skillmap/recipe/bake not registered |
| site/guide/social-topology.md | D | Codex | todo | leak @71-77; stale platform matrix (omits nanoclaw; OpenClaw has discord/telegram/slack) |
| site/guide/pod-yaml.md | C | Codex | todo | leak @24-33; incomplete service-field list (feeds/tools/memory/context/mcp-stdio/describe-file/claw-api self); channel-context tuning wording |
| site/guide/anatomy.md | C | Codex | todo | verify vs current compile flow |
| site/guide/clawfile.md | C | Codex | todo | verify directives |
| site/guide/tools.md | C | Codex | todo | native vs mediated modes |
| site/guide/memory.md | C | Codex | todo | session history + recall + `claw history`/`claw memory` |
| site/guide/surfaces-and-skills.md | C | Codex | todo | feeds coverage |
| site/guide/compilation-principles.md | C | Codex | todo | verify vs ADR-017 |
| site/guide/drivers.md | C | Codex | todo | 7-driver matrix + per-driver platform support vs `internal/driver/*` |

**Out-of-scope leak found (needs its own decision):** `examples/trading-desk/` itself uses named
claws as service names (`tiverton`, `westin`, `allen`, `logan`, `micro`) and agent files
(`TIVERTON.md`, …) — a public-artifact leak larger than the doc pages. Renaming ripples into
agent paths, env vars, and possibly spike fixtures, so it is **not** included in this docs PR;
flag to the maintainer as a follow-up issue.

**Release guardrails:** no changes to `release_manifest.go` pins, changelog `Latest` badge, nav
version dropdown, or cllama submodule pointer. A `## Unreleased` changelog note is allowed.
