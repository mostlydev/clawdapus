# Holistic Documentation Refresh — Design Plan

Tracking issue: [#280](https://github.com/mostlydev/clawdapus/issues/280)
Status: **converged** (Claude authored; Codex adversarial review folded in, note `f6147c39`)
Branch: `issue-280-docs-holistic-refresh`
Canonical reconciliation + per-page checklist: [`docs/PROJECT_STATE.md`](../PROJECT_STATE.md)

## Problem

Docs were maintained incrementally, issue-by-issue. The big-picture surfaces — site
landing/vision pages and the git README — have drifted from the project's actual state.
The narrative still leans on a stale "Phase 5 / Phase 6" framing written before large
capabilities shipped (memory plane, channel-memory + awareness digests, claw-api /
Master Claw, four-verb infra lifecycle, compiled tool mediation, session history, ingress
surface matrix — ADRs 018–024). Context captured in plans/ADRs/phases is **orphaned**:
not surfaced or linked where a reader can follow it.

### Concrete drift already confirmed

- `site/index.md` example uses downstream-deployment service/env identifiers — a **named-deployment
  leak** (violates the "no downstream names in public artifacts" rule).
- `site/guide/what-is-clawdapus.md` calls `claw audit` + drift scoring **"Phase 5 design"**,
  yet `claw audit` ships today (rollcall spike validates its telemetry). The same page shows
  a fabricated `claw ps` **DRIFT** column and drift scores that do **not** exist in the
  reference cllama proxy — an over-claim. The page is simultaneously under- and over-claiming.
- ADRs 018–024 capabilities are only thinly reflected in the vision/landing narrative.

## Decisions (locked with maintainer)

1. **Scope:** everything in one coordinated effort — narrative/roadmap reconciliation **and**
   a full per-page accuracy sweep across the site + README.
2. **Roadmap model:** public docs lead with what's shipped **now**, present-tense, plus a short
   forward roadmap of genuinely-planned items. Maintain **one canonical internal source of
   truth** (from ADRs + plans + changelog + code) that public pages link to.
3. **Context home:** add a public **Architecture / How it fits together** page tying
   capabilities to ADRs, **and** a terse internal index under `docs/` mapping ADR → status and
   capability → doc location as the maintenance source of truth.

## Approach

**Reconciliation-first.** Build a shipped-vs-planned matrix from ADRs/plans/changelog **and the
code/tests** (CLAUDE.md already enumerates much current behavior) before rewriting prose. Every
public claim is then checked against the matrix. This catches under-claims (`claw audit`) and
over-claims (DRIFT column / drift scoring).

**Parallel survey.** Dispatch read-only agents to produce gap reports for distinct areas
(vision docs; core-concept guides; reference pages + README) against current code. Synthesize
into the matrix.

## Deliverables (proposed order)

1. **Shipped-vs-planned matrix** (working artifact, lands in this plan or a sibling note).
2. **Internal canonical index** under `docs/` — ADR → status (accepted / shipped / superseded),
   capability → doc location. *Open: `docs/PROJECT_STATE.md` vs `docs/decisions/README.md`
   index vs both.*
3. **Public Architecture page** — "How it fits together," capabilities ↔ ADRs. *Open: under
   Introduction vs new top-level nav entry.*
4. **Narrative rewrite** (present-tense, de-phased, downstream-name-free): `site/index.md`,
   `site/guide/what-is-clawdapus.md`, `site/manifesto.md` (+ root `MANIFESTO.md`), `README.md`.
5. **Per-page accuracy sweep** of the remaining guide pages: anatomy, clawfile, pod-yaml,
   cllama, tools, memory, surfaces-and-skills, compilation-principles, social-topology, cli,
   drivers — each verified against current code. *Open: depth bar (light correctness vs deeper
   rewrite where weak).*

## Guardrails

- **Release artifacts untouched:** no changes to `internal/infraimages/release_manifest.go`
  pins, the changelog `Latest` badge, the nav dropdown version, or the cllama submodule pointer.
  (This is a docs PR, not a release.) A changelog entry under `## Unreleased` is allowed.
- **No downstream-deployment names** anywhere in the new/edited public docs.
- **Replace fabricated CLI output** (e.g. the `claw ps` DRIFT column) with real output captured
  from the actual commands/tests; clearly label anything genuinely planned as roadmap.
- **Trust order** when sources disagree: current code → tests → examples → ADRs → plans.

## Collaboration (Talking Stick)

Claude + Codex work this together. Proposed division, to converge on:
- Reconciliation matrix + internal index: shared; whoever holds the stick advances it.
- Narrative rewrite: Claude drafts prose; Codex adversarial-reviews for over-claims/accuracy.
- Per-page accuracy sweep: split by page set; cross-review.
- Codex strong on edge cases / accuracy convergence / release-safety; Claude on synthesis +
  first-pass prose + review.

## Resolved (Codex review `f6147c39`, accepted)

1. **Internal index:** single canonical `docs/PROJECT_STATE.md` with terse tables (capability,
   status, evidence, doc location, ADR refs). An optional `docs/decisions/README.md` would be a
   thin ADR list linking to PROJECT_STATE — never a second status source.
2. **Architecture page:** `/guide/architecture`, in the Introduction sidebar after "What is
   Clawdapus?" and before "Quickstart"; linked from README + homepage. No new top-nav item.
3. **Sweep depth:** two-tier. Tier C (correctness) on every page — strip downstream names,
   fabricated output, stale phase labels; verify CLI snippets against code/tests. Tier D (deep
   rewrite) on index, what-is, manifesto (site+root), README, cllama, cli, social-topology.
4. **ADRs/plans:** stay as historical record; build the index on top. Add a narrow banner only
   where an old plan is publicly linked *and* actively misleading.

Per-page ownership + the full drift inventory (with line numbers, verified against code) now
live in [`docs/PROJECT_STATE.md`](../PROJECT_STATE.md).
