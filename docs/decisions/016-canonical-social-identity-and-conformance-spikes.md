# ADR-016: Canonical Social Identity and Conformance Spikes

**Date:** 2026-03-19
**Status:** Proposed
**Depends on:** ADR-003 (Topology Simplification and the HANDLE Directive)

## Context

Clawdapus already has the right architectural split for social topology:

- `HANDLE` is the canonical declaration of platform identity
- `SURFACE` is infrastructure-only
- drivers translate handle data into runner-native mention, routing, and platform configuration

That split only works if a declared social identity actually means something stable. Today the Discord rollcall spike violates that rule for convenience: it boots multiple services concurrently while every service declares the same Discord bot identity. This is acceptable as a rough runtime smoke test, but it is not a valid topology fixture.

When multiple active services advertise the same platform identity, the system loses the exact semantics the project needs:

- wrong bot IDs can still appear to work
- mentions become ambiguous
- peer discovery becomes non-canonical
- generated allowlists and contact variables stop being trustworthy

At the same time, the project still needs a practical spike that can answer a simpler question:

- can a given runtime boot, receive a native Discord mention, and produce the expected response pattern?

Those are different goals. If they stay conflated, the repo will either keep hiding topology bugs or overfit tests around scarce Discord credentials.

## Decision

### 1. `HANDLE` identity is canonical and exclusive while active

For a given social platform, one concurrently active service owns one declared identity.

Multiple concurrently active services must not advertise the same canonical platform identity if the fixture is intended to exercise social topology, mentions, peer routing, or inter-agent conversation semantics.

### 2. Shared platform identities are allowed only for sequential conformance testing

A single real Discord bot identity may be reused across multiple runtime conformance checks if those runtimes are exercised one at a time.

These sequential conformance spikes may assert:

- the runtime starts successfully
- the bot receives a native mention trigger
- the bot responds after the trigger
- the response matches an expected runtime-specific pattern

These spikes do **not** establish topology semantics. They are runtime/mention conformance checks, not social-topology fixtures.

### 3. Concurrent topology and conversation tests require unique identities

Any spike or example that claims to validate:

- bot-to-bot conversation
- peer mentions
- handle discovery
- generated `CLAW_HANDLE_*` contact data
- platform allowlists derived from social topology

must use unique platform identities for each concurrently active service on that platform.

If unique identities are not available, the test must narrow its claim and run sequentially instead of pretending to validate topology.

### 4. Drivers remain responsible for topology realization

This ADR does not introduce a second alias registry or a test-only topology language.

Operators still declare social topology through `handles`, and later through shared handle defaults where appropriate. Drivers remain responsible for realizing that canonical handle graph into runner-native behavior such as:

- mention formatting
- peer/bot allowlists
- guild and channel routing defaults
- exported contact metadata for non-claw services

### 5. Test names and examples must reflect what they prove

Repo fixtures should distinguish between:

- **runtime conformance spikes**: sequential, one active service per reused identity
- **social topology spikes**: concurrent, unique identities required

The current rollcall-style fixture belongs in the first category unless and until it is backed by distinct Discord identities.

## Implementation Sequence

### Milestone 1: Clarify fixture scope
1. Recast the existing rollcall spike as a runtime conformance fixture rather than a topology fixture
2. Ensure it runs one runtime at a time when reusing a single Discord identity
3. Keep its assertions focused on mention trigger and response pattern

### Milestone 2: Preserve topology semantics
4. Reserve concurrent multi-agent Discord spikes for fixtures with unique identities
5. Make topology-oriented examples and docs state that concurrent identity reuse is not a valid social-topology test

### Milestone 3: Tighten driver-facing identity guarantees
6. Use the canonical handle graph as the source for mention synthesis and peer discovery
7. Fail closed on identity mismatches or ambiguous concurrent ownership when topology semantics matter

## Rationale

This keeps the big picture intact.

The operator model stays simple: declare handles once and let drivers do the platform work. The testing model also becomes honest: a shared Discord bot can still prove runtime behavior, but only a unique-identity fixture can prove social topology.

That avoids papering over the exact bugs the project cares about most, especially wrong IDs and flaky mentions, without forcing every runtime smoke test to provision a full fleet of real Discord bots.

## Consequences

**Positive:**
- Preserves ADR-003's rule that `HANDLE` is the canonical social identity layer
- Keeps operator ergonomics aligned with driver-level topology realization
- Allows practical mention/response spike coverage even when only one Discord bot identity is available
- Makes topology claims trustworthy by requiring unique identities for concurrent conversation tests

**Negative:**
- The current rollcall spike cannot keep claiming concurrent multi-agent topology coverage if it reuses one identity
- Real conversation/topology spikes become more operationally expensive because they need unique platform credentials
- Some current examples may need to be renamed or narrowed so their stated guarantees match reality
