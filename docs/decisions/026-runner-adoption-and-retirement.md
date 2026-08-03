# ADR-026: Runner Adoption Measurement and Driver Retirement Policy

**Date:** 2026-08-03
**Status:** Accepted
**Depends on:** ADR-024 (Runner Base Refresh From Upstream)
**Tracks:** #353 (audit adoption and retire stagnant runner integrations)
**Evidence snapshot:** 2026-08-03T01:58Z — see `scripts/runner-adoption-snapshot/` and `docs/evidence/2026-08-03-runner-adoption.json`

## Context

Clawdapus carries seven runner drivers: OpenClaw, Hermes, Nanobot, NanoClaw, PicoClaw, MicroClaw,
and NullClaw. Each one is a standing cost — compatibility with an upstream we do not control, image
refresh, documentation, fixtures, a conformance shape in `examples/rollcall/`, and a share of every
driver-generic bug fix. `AGENTS.md` records the multiplier plainly: "Bug fixes in one driver often
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

Secondary sources corroborate where available: PyPI download history (`nanobot-ai`), Docker Hub
`pull_count` (PicoClaw), and cumulative GitHub release-asset downloads. ghcr.io publishes no public
pull counts, so container pulls are not comparable across the cohort.

One identification note, since it cost time: PyPI publishes no project URLs for `nanobot-ai`, so
Nanobot's upstream is not discoverable from the package page. It was confirmed as `HKUDS/nanobot` by
matching that repository's `pyproject.toml` — `name = "nanobot-ai"`, `version = "0.3.0"` — against
the published package. The collector records the mapping so the next audit does not have to redo it.

**Commit velocity is explicitly not an adoption metric.** It measures maintainer output. It is
recorded because upstream viability is a separate concern, and it must never decide adoption. The
evidence below contains a case that proves the point.

## Evidence

New forks per 30-day window, newest first, snapshot 2026-08-03T01:58Z:

| runner | 0-30d | 30-60d | 60-90d | 90-120d | newest/oldest | recent60/prior60 | commits QoQ | latest release |
|---|---|---|---|---|---|---|---|---|
| Hermes | 6036 | 7587 | 10442 | 16307 | 0.37 | 0.51 | **+86%** | 2026-07-30 |
| OpenClaw | 1807 | 2182 | 3756 | 6980 | 0.26 | 0.37 | **+6%** | 2026-08-02 |
| Nanobot | 366 | 314 | 480 | 813 | 0.45 | 0.53 | -33% | 2026-07-25 |
| PicoClaw | 315 | 102 | 154 | 306 | **1.03** | **0.91** | **-76%** | 2026-07-02 (`nightly`) |
| NanoClaw | 167 | 222 | 413 | 2346 | **0.07** | 0.14 | -32% | 2026-08-01 |
| NullClaw | 32 | 22 | 44 | 44 | 0.73 | 0.61 | -89% | **2026-05-29** |
| MicroClaw | **0** | 3 | 10 | 8 | **0.00** | 0.17 | -91% | 2026-08-01 |

Supporting figures: stars — OpenClaw 384960, Hermes 224342, Nanobot 46522, NanoClaw 30411,
PicoClaw 29799, NullClaw 7987, MicroClaw 730. Forks — OpenClaw 80906, Hermes 43387, Nanobot 8231,
NanoClaw 12871, PicoClaw 4381, NullClaw 929, MicroClaw 132. Cumulative GitHub release-asset
downloads — OpenClaw 1825617, Hermes 25561, PicoClaw 227049, NullClaw 13229, MicroClaw 12161,
and none for Nanobot or NanoClaw. PicoClaw also has 210807 Docker Hub pulls. `nanobot-ai` PyPI
monthly downloads — Mar 79326, Apr 53246, May 33854, Jun 33446, Jul 68201.

The table above is the committed collector's output. Those figures were also collected independently
by a second agent using separately written tooling, which agreed exactly on the low-volume boundary
(MicroClaw 0 and NullClaw 32) and within live drift on the larger projects. Two
collectors reaching the same answer is a materially stronger basis for deleting a driver than either
run alone. It establishes that the fork counts are not artifacts of one implementation; it does not
turn those proxy counts into Clawdapus usage telemetry.

### Two findings that determine the policy

**A derivative is not a useful retirement boundary.** Six runners have declining fork rates,
including OpenClaw (0.26) and Hermes (0.37), which are unambiguous keeps on every other axis.
PicoClaw is the sole flat-to-rising exception. A literal rule of “retire what is not increasing”
would therefore delete six of seven drivers, including the two the product is built around. Recent
absolute activity and corroborating scale matter more than the sign of the derivative.

**PicoClaw has the best adoption retention in the cohort and the worst maintenance trend.** Its
fork rate is the only one flat-to-rising (1.03 newest/oldest, 0.90 on 60-day windows) while its
commit volume fell 75%. The two metrics return opposite verdicts, and the adoption metric is the one
that answers the question actually being asked. Any policy that had used commit velocity as the
deciding signal would have deleted the healthiest-retaining runner in the set.

## Decision

The operator's instruction was to stop carrying the ambiguous runners: *"Just remove all the
ambiguous ones. I don't need to maintain them."* The basis for this ADR is therefore **maintenance
cost**, with the adoption evidence used to decide which runners are defensibly worth that cost. It is
worth being explicit that this is a broader basis than adoption alone, rather than pretending a pure
adoption rule produced this set.

The evidence is a review boundary, not an automatic three-clause deletion formula. Applying a
conjunction after seeing the rows would be post-hoc: NanoClaw's lack of release assets is missing
corroboration, not evidence of missing users, while NullClaw has measurable adoption despite its
maintenance signal. The decision is therefore recorded at its real level of judgment:

1. **Keep a runner when the public case for carrying it is clear.** Absolute recent fork activity is
   the common floor; package, image, or release distribution is corroboration where available.
2. **Treat missing corroboration and maintenance uncertainty as ambiguity, not as negative adoption
   evidence.** Those signals can justify declining the integration's maintenance cost, but must not
   be rewritten as “no users.”
3. **Record each retirement independently.** A future audit re-runs the evidence; it does not
   mechanically delete a runner because one proxy crossed a tuned threshold.

### Verdicts

| runner | public evidence | uncertainty | decision |
|---|---|---|---|
| OpenClaw | 1807 forks/30d; 1.8m release-asset downloads | none material | **retain** |
| Hermes | 6036 forks/30d; 25.5k release-asset downloads | none material | **retain** |
| Nanobot | 366 forks/30d; PyPI 68201/month in July | download series is choppy | **retain** |
| PicoClaw | 315 forks/30d; 210807 Docker pulls; best retention | commits down 76% | **retain** |
| NanoClaw | 167 forks/30d; 30411 stars | no independent distribution signal; 0.07 retention | **retire** |
| NullClaw | 32 forks/30d; 13.2k release-asset downloads | no release in 64 days; commits down 89% | **retire** |
| MicroClaw | 0 forks/30d; 730 stars | minimal public adoption despite active releases | **retire** |

**MicroClaw has minimal public adoption.** Zero new forks in the trailing 30 days, 0.00 retention, 730
stars, and 12161 cumulative release-asset downloads — orders of magnitude below PicoClaw. It is
*actively maintained*, four releases in the ten days before the snapshot, which is precisely the
point: maintenance activity does not establish adoption. The evidence supports “minimal public
adoption,” not “no users.”

**NullClaw's maintenance signal is weak.** No release since 2026-05-29 and commit volume down 89%. Its
adoption is real (7987 stars, 13229 cumulative release-asset downloads, 32 new forks), so this is a
maintenance-cost decision under uncertainty, not a claim that nobody uses it or that the upstream
has stopped entirely.

**NanoClaw remains ambiguous.** No independent public distribution metric was found, so its 30411
stars cannot be checked against package downloads, image pulls, or release-asset downloads. That
absence is ambiguity, not negative evidence. NanoClaw also carries the steepest adoption decay in
the cohort by a wide margin — 0.07 newest/oldest, 0.14 across 60-day windows, against a next-worst of
0.26. Without a corroborating signal, the operator declined the cost of maintaining the integration.

**PicoClaw is retained, and this is the load-bearing case.** On maintenance it looks like the worst
runner in the set: commits down 76%, and its most recent
tag is a `nightly`. On adoption it is the *best* — the only runner whose fork rate is flat-to-rising
(1.03 newest/oldest, 0.91 across 60-day windows), plus 210807 Docker Hub pulls and 227049 cumulative
release-asset downloads. Two metrics, opposite verdicts. Any rule that let maintenance activity
decide would have deleted the healthiest-retaining runner in the cohort while keeping weaker ones.

**Removed `CLAW_TYPE` values fail closed for one release** with a validation error naming the retired
runner, ADR-026, and Hermes as the supported migration target (`internal/driver/registry.go`). A
`claw up` that fails with a clear message is compile-time behavior consistent with the compiler
contract; silently changing behavior is not.

**Revisit policy:** re-run the collector and re-evaluate at each minor release, or whenever a
retained runner's upstream goes 60+ days without a release.

## Consequences

**The cost side is not free, and it is not the deleted lines.** Driver LOC: openclaw 3523, hermes
2784, picoclaw 1252, nullclaw 1125, nanobot 974, microclaw 847, nanoclaw 597. Retiring NanoClaw,
MicroClaw, and NullClaw reclaims ~2,600 lines of driver code plus their examples, fixtures, and
docs. What it actually costs is **three conformance shapes**. `examples/rollcall/` was a seven-driver
pod and `AGENTS.md` requires it remain a full spike test; `TestSpikeRollCall` is the primary proof
that cllama proxy enforcement holds across *heterogeneous* runner shapes — distinct config formats,
`HOME` handling, mention-only semantics, cron paths. Every shape removed is a shape that can no
longer catch a driver-generic regression; the pre-retirement guide recorded that bug fixes commonly
applied across all seven. The rollcall fixture must be **revised to keep full conformance coverage across all
four retained drivers**, not merely have three services deleted from it. The offsetting gain is the
operator's stated one: every driver-generic fix now has four targets instead of seven, and none of
the remaining four is a runner we cannot corroborate real use for.

**Historical references are not support surface, and must not be edited.** A retired runner's name
survives in three distinct kinds of place, and they get different treatment:

- **Active support surface — update.** `internal/driver/`, the driver registry, build recipes and
  runner aliases, `examples/`, `README.md`, `site/guide/`, the embedded skill text under
  `cmd/claw/skill_data/`, and generated-artifact test expectations. These describe what Clawdapus
  supports today; leaving a retired runner in them is a false claim.
- **Historical record — leave alone.** `docs/plans/`, superseded ADRs, and existing `site/changelog.md`
  entries. These are dated accounts of what was true when written. `AGENTS.md` already forbids
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
writes a dated JSON artifact under `docs/evidence/`. The collector captures fork windows, commit
windows, release cadence and asset downloads, Docker Hub pulls where public, and PyPI history where
available. A future audit can refresh the evidence without inheriting today's judgment as code.
