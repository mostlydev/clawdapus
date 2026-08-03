# ADR-026: Runner Adoption Measurement and Driver Retirement Policy

**Date:** 2026-08-03
**Status:** Proposed
**Depends on:** ADR-024 (Runner Base Refresh From Upstream)
**Tracks:** #353 (audit adoption and retire stagnant runner integrations)
**Evidence snapshot:** 2026-08-03T00:00Z — see `scripts/runner-adoption-snapshot/` and `docs/evidence/2026-08-03-runner-adoption.json`

## Context

Clawdapus carries seven runner drivers: OpenClaw, Hermes, Nanobot, NanoClaw, PicoClaw, MicroClaw,
and NullClaw. Each one is a standing cost — compatibility with an upstream we do not control, image
refresh, documentation, fixtures, a conformance shape in `examples/rollcall/`, and a share of every
driver-generic bug fix. `CLAUDE.md` records the multiplier plainly: "Bug fixes in one driver often
apply to all 7."

The request that opened #353 was to retire runners "not seeing an increase in adoption over time."
This ADR exists because **that rule cannot be applied as stated**, and discovering why changed the
decision.

## The measurement problem

Three candidate evidence sources were tried. Two do not survive scrutiny.

**Stargazer timestamps — unavailable.** #353 specified these as the adoption time series. The
`/repos/{owner}/{repo}/stargazers` endpoint with `Accept: application/vnd.github.star+json` returns
404 for every external upstream from our environment. This is not rate limiting (4846/5000 core
requests remaining when tested) and not a token scope problem — the identical call succeeds against
`mostlydev/clawdapus` and returns real `starred_at` values, and plain `repos/{owner}/{repo}` succeeds
for every upstream. Only the subresource is blocked. Recorded here as a permanent limitation so a
future collaborator does not waste the same afternoon.

**Per-release downloads normalized by release age — confounded, rejected.** Downloads concentrate in
the days after publication and taper. Age-normalization therefore flatters whichever release is
newest, and in our data every project with asset-bearing releases shows its most recent release with
the highest downloads/day, *including the ones visibly winding down*. It measures recency, not growth.
Release data is retained in the evidence artifact for absolute distribution scale and cadence only.

**Fork creation timestamps bucketed into 30-day windows — adopted as the primary metric.** Unlike
stars, total downloads, and fork *counts*, a bucketed fork rate is not cumulative: it can fall. It is
a weak proxy for users, but it is the only non-cumulative adoption series obtainable for every
runner, and it is reproducible from a public endpoint.

Two secondary sources corroborate: PyPI download history (available for `nanobot-ai` only, and the
one true adoption time series in the set) and Docker Hub `pull_count` (available for PicoClaw only,
because ghcr.io publishes no public pull counts — an asymmetry that makes OpenClaw and Hermes images
non-comparable on that axis).

One identification note, since it cost time: PyPI publishes no project URLs for `nanobot-ai`, so
Nanobot's upstream is not discoverable from the package page. It was confirmed as `HKUDS/nanobot` by
matching that repository's `pyproject.toml` — `name = "nanobot-ai"`, `version = "0.3.0"` — against
the published package. The collector records the mapping so the next audit does not have to redo it.

**Commit velocity is explicitly not an adoption metric.** It measures maintainer output. It is
recorded because upstream viability is a separate concern, and it must never decide adoption. The
evidence below contains a case that proves the point.

## Evidence

New forks per 30-day window, newest first, snapshot 2026-08-03T00:00Z:

| runner | 0-30d | 30-60d | 60-90d | 90-120d | newest/oldest | recent60/prior60 | commits QoQ | latest release |
|---|---|---|---|---|---|---|---|---|
| Hermes | 6040 | 7598 | 10437 | 16301 | 0.37 | 0.51 | **+76%** | 2026-07-30 |
| OpenClaw | 1804 | 2187 | 3758 | 6995 | 0.26 | 0.37 | **+8%** | 2026-08-02 |
| Nanobot | 366 | 316 | 482 | 811 | 0.45 | 0.53 | -35% | 2026-07-25 |
| PicoClaw | 315 | 102 | 156 | 305 | **1.03** | **0.90** | **-75%** | 2026-07-02 (`nightly`) |
| NanoClaw | 167 | 223 | 412 | 2355 | 0.07 | 0.14 | -29% | 2026-08-01 |
| NullClaw | 32 | 22 | 44 | 44 | 0.73 | 0.61 | -89% | **2026-05-29** |
| MicroClaw | **0** | 3 | 10 | 8 | **0.00** | 0.17 | -91% | 2026-08-01 |

Supporting figures: stars — OpenClaw 384956, Hermes 224323, Nanobot 46521, NanoClaw 30410,
PicoClaw 29799, NullClaw 7987, MicroClaw 730. Forks — OpenClaw 80901, Hermes 43378, NanoClaw 12871,
PicoClaw 4381, NullClaw 929, MicroClaw 132. Distribution scale —
OpenClaw ~123k downloads/release, PicoClaw ~33k/release plus 210714 Docker Hub pulls, Hermes and
NullClaw ~2k/release, MicroClaw ~30-95/release, NanoClaw not asset-distributed. `nanobot-ai` PyPI
monthly downloads — Feb 50395, Mar 79326, Apr 53246, May 33854, Jun 33446, Jul 68201. MicroClaw's
public Events API retains 169 events from 2026-07-04 including 11 `WatchEvent`s, so it is receiving
new stars and is not literally inert.

The table above is the committed collector's output. Those figures were also collected independently
by a second agent using separately written tooling, which agreed exactly on five of seven runners
(Nanobot 366, NanoClaw 167, PicoClaw 315, NullClaw 32, MicroClaw 0) and to within two forks on
OpenClaw and Hermes, the difference being live drift between snapshots taken minutes apart. Two
collectors reaching the same answer is a materially stronger basis for deleting a driver than either
run alone, and it is the reason the retirement below is stated without hedging.

### Two findings that determine the policy

**Every runner in the cohort has a declining fork rate.** Including OpenClaw (0.37) and Hermes
(0.51), which are unambiguous keeps on every other axis and are the two runners the project is built
around. Post-launch decay is universal here. A rule of the form "retire what is not increasing"
therefore selects all seven drivers, including the two the product depends on. The rule must be a
**floor**, not a **derivative**.

**PicoClaw has the best adoption retention in the cohort and the worst maintenance trend.** Its
fork rate is the only one flat-to-rising (1.03 newest/oldest, 0.90 on 60-day windows) while its
commit volume fell 75%. The two metrics return opposite verdicts, and the adoption metric is the one
that answers the question actually being asked. Any policy that had used commit velocity as the
deciding signal would have deleted the healthiest-retaining runner in the set. This is the concrete
reason the policy below separates the two rules and never collapses them.

## Decision

**This ADR retires drivers on adoption grounds only.** A maintenance-viability rule is deliberately
*not* adopted here — see "Deferred" below for why.

**Adoption floor.** A runner fails when new forks in the trailing 30-day window are not materially
distinguishable from zero relative to the cohort, corroborated by at least one secondary distribution
metric. Threshold for this snapshot: fewer than 10 new forks in the trailing 30 days, where the
next-lowest surviving runner has 32. The wording is "no material adoption growth", not "no growth" —
MicroClaw's 11 recent `WatchEvent`s mean the stronger claim would be false.

**Outcome:** retire **MicroClaw** — 0 new forks in the trailing 30 days, 0.00 retention, 730 stars,
and ~30-95 downloads per release, three orders of magnitude below PicoClaw. It is the only runner
that fails the floor, and it is worth stating that it is *actively maintained* (five releases in ten
days). MicroClaw has a maintainer and no users; that is the honest reading, and it is exactly what an
adoption rule is supposed to catch and a maintenance rule would have missed.

Retain OpenClaw, Hermes, Nanobot, NanoClaw, PicoClaw, and NullClaw. Flag PicoClaw and NanoClaw for
revisit at the next audit — PicoClaw for its maintenance trend, NanoClaw for the steepest retention
decay in the set (0.07) despite healthy absolute numbers.

**Deferred: upstream maintenance viability.** NullClaw is the obvious candidate for a
maintenance-based rule — no release since 2026-05-29 and commit volume down 89%. It is retained here
anyway, because 32 new forks in the trailing 30 days, ~2k downloads per release, and 7987 stars are
real use. Retiring a runner people demonstrably use, under an issue whose stated basis is adoption,
would mean deciding on one rule and justifying with another. If carrying an unmaintained upstream
becomes a real cost, that deserves its own issue, its own thresholds, and its own argument — with
NullClaw as its first candidate. The related open question, whether NullClaw is a user-facing runner
at all or an internal null adapter that should be judged on architectural utility as a test seam,
belongs to that issue too. This ADR does not close it from data.

**Removed `CLAW_TYPE` values fail closed for one release** with a validation error naming the removed
runner and its migration target, then are deleted. A `claw up` that fails with a clear message is
compile-time behavior consistent with the compiler contract; silently changing behavior is not.

**Revisit policy:** re-run the collector and re-evaluate at each minor release, or whenever a
retained runner's upstream goes 60+ days without a release.

## Consequences

**The cost side is not free, and it is not the deleted lines.** Driver LOC: openclaw 3523, hermes
2784, picoclaw 1252, nullclaw 1125, nanobot 974, microclaw 847, nanoclaw 597. Retiring MicroClaw
reclaims 847 lines. What it actually costs is **a conformance shape**. `examples/rollcall/` is a
seven-driver pod and `CLAUDE.md` requires it remain a full spike test; `TestSpikeRollCall` is the
primary proof that cllama proxy enforcement holds across *heterogeneous* runner shapes — distinct
config formats, `HOME` handling, mention-only semantics, cron paths. Every shape removed is a shape
that can no longer catch a driver-generic regression, and `CLAUDE.md` records that "bug fixes in one
driver often apply to all 7". The rollcall fixture must be **revised to keep full conformance
coverage across all six retained drivers**, not merely have a service deleted from it. Confining this
ADR to a single removal keeps that cost to one shape, which is part of why the narrower outcome is
the right one.

**Historical references are not support surface, and must not be edited.** A retired runner's name
survives in three distinct kinds of place, and they get different treatment:

- **Active support surface — update.** `internal/driver/`, the driver registry, build recipes and
  runner aliases, `examples/`, `README.md`, `site/guide/`, the embedded skill text under
  `cmd/claw/skill_data/`, and generated-artifact test expectations. These describe what Clawdapus
  supports today; leaving a retired runner in them is a false claim.
- **Historical record — leave alone.** `docs/plans/`, superseded ADRs, and existing `site/changelog.md`
  entries. These are dated accounts of what was true when written. `CLAUDE.md` already forbids
  rewriting historical changelog entries; the same reasoning covers plans and prior ADRs. A plan
  from 2026-03 that describes bringing MicroClaw to driver parity remains a true statement about
  2026-03. Editing it to erase the runner falsifies the record and destroys the context a future
  reader needs to understand why the driver existed.
- **Migration guidance — add.** The validation error for a retired `CLAW_TYPE`, the new changelog
  entry, and this ADR. These are the only places that should describe the removal itself.

The practical test: if a reader would be misled about what `claw up` will do *today*, update it. If
they would only be reading about the past, leave it.

**The central limitation, stated plainly:** we have no telemetry on which drivers *Clawdapus users*
actually run. Every metric in this ADR is upstream popularity. It answers "would anyone out there
miss this runner", not "do our users depend on this driver". The entire decision rests on that
substitution, and if driver-level usage telemetry ever exists it supersedes this evidence model.

**Reproducibility.** `scripts/runner-adoption-snapshot/` re-collects every surviving metric and
writes a dated JSON artifact under `docs/evidence/`. The classification thresholds live in this ADR,
not in the collector, so a future audit can re-run the data without inheriting today's judgement.
