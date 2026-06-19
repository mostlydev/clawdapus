# Plan: auto descriptor refresh during `claw up --fix`

Issue: [#224](https://github.com/mostlydev/clawdapus/issues/224)
Builds on: PR #223 (`claw compose build` accepts stale generated compose)
Status: draft for adversarial review

## Goal

`claw up --fix -d` is the single command that handles a build-backed service whose `.claw-describe.json` is emitted at runtime (Rails introspection writing `/rails/.claw-describe.json` on boot, etc.). The operator should never have to know the internal `claw compose build` → `docker compose up -d` → `docker compose cp` → `claw up -d` sequence.

## Operator contract (deliberately small)

- `claw up --fix -d` — does everything; the new auto-refresh runs only in this mode.
- `claw up -d` — stays strict. On a missing/stale runtime-emitted descriptor it errors with a direct `claw up --fix -d` remediation hint.
- No new flags. No new pod-level fields. The internal mechanism is implementation detail.
- `x-claw.describe-file: <host-path>` keeps its current meaning (explicit host snapshot, opt-in).

## Image contract (no new labels)

The existing `LABEL claw.describe=/path/in/image` already declares "this image's descriptor lives at this in-image path." Today the operator is expected to `COPY` the descriptor into the image at build time so a stopped container can read it back via `docker cp` (`extractFileFromImage`).

Reinterpretation under `--fix`:

- If the labeled path is **present** in the just-built image → today's behavior. Build-time baked.
- If the labeled path is **missing** from the just-built image (`extractFileFromImage` returns `ErrNotExist`) → treat as runtime-emitted. Under `--fix`, run the refresh dance (described below) and write a snapshot to `.claw-discovered/<service>.claw-describe.json`. Under strict mode, error with a `claw up --fix -d` hint.
- If the image carries no `claw.describe` label at all → no descriptor expected; current behavior (no auto-refresh).

This keeps the contract one-dimensional: the `claw.describe` label is the single declaration that "this service has a descriptor"; whether it's baked or emitted is a property of the image filesystem at build time, not a separate signal the operator has to declare.

## Where the new step slots into `claw up`

`runComposeUp` in `cmd/claw/compose_up.go` today (relevant phases):

```
loadPodDefinition
planPodServiceImages
  --fix:  pullRegistryServiceImages / buildPlannedServiceImages
  strict: firstMissingPullPlan / firstMissingBuildPlan → remediationErrorf
warnRunnerBaseDrift
resolveRuntimePlaceholders
discoverMCPStdioServices              ← existing stdio-only refresh path
materialize loop (per service)
collectServiceDescriptors             ← failure today: missing runtime descriptor
BuildFeedRegistry / BuildToolRegistry
resolveFeedSubscriptions
resolveToolSubscriptions              ← failure today: tool policy refs unknown tool
resolveMemorySubscriptions
attachCapabilityProvidersToInternalNetwork
prepareClawAPIRuntime
EmitCompose → write compose.generated.yml
ensureRequiredInfraImagesAvailable / ensureInfraImages
docker compose up -d
PostApply per generated service
```

The new step is `refreshRuntimeDescriptors`, sibling of `discoverMCPStdioServices`, inserted between `discoverMCPStdioServices` and the materialize loop. It runs only when `composeUpFix` is true.

## `refreshRuntimeDescriptors` — the dance

For each service `svc` in pod:

1. **Filter.** Skip unless all of:
   - `svc.Compose["build"]` is set (build-backed);
   - `loadDescriptorFromImage(imageRef, info.DescribePath)` returns `ErrNotExist` for the labeled path (or descriptor extracted is empty per a "looks like a stub" check — see open question 1);
   - Snapshot is missing or stale at `.claw-discovered/<service>.claw-describe.json` (mtime older than image build time per `claw.runner.image-id` comparison, or absent).
2. **Source the dependency context.** Use the existing `compose.generated.yml` if present (previous `claw up`), otherwise emit a minimal phase-1 compose containing the provider service plus the transitive closure of its `depends_on`, written to `.claw-discovered/phase1.compose.yml`. (See open question 3.)
3. **Boot the provider with its deps.** `docker compose -f <chosen-compose> up -d <provider-svc>`. This brings up postgres, redis, etc., the same way the manual workflow does.
4. **Wait for the descriptor file.** Poll `docker compose -f <chosen-compose> exec <provider-svc> test -f <claw.describe-path>` with `discoverTimeout` (45s default). On timeout, capture and surface `docker compose logs <provider-svc>` so the operator can see *why* the app didn't boot.
5. **Extract.** `docker compose -f <chosen-compose> cp <provider-svc>:<claw.describe-path> .claw-discovered/<service>.claw-describe.json`.
6. **Validate the snapshot parses.** Reject malformed descriptors before they poison the compile pipeline.
7. **Tear down.** `docker compose -f <chosen-compose> down <provider-svc>` (or only the ephemerally-started services if the compose file had others already running). The subsequent `compose up -d` in the main flow will re-create cleanly via `--force-recreate` for runtime consumers.

If any step fails, surface logs and abort `--fix` with a non-zero exit. Do not partially succeed.

## Generalizing `discoveredDescribeFile`

`resolveServiceMetadata` currently only consults `.claw-discovered/<svc>.claw-describe.json` for MCP-stdio sidecars (`discoveredDescribeFile` line 3540 gates on `IsMCPStdioSidecar`). Relax this: a `.claw-discovered/<svc>.claw-describe.json` snapshot is consulted for any service when present, sitting between `explicitDescribeFile` (path 3) and `loadDescriptorFromImage` (path 5). The MCP-stdio "no snapshot, no descriptor" hard error stays scoped to stdio sidecars only.

## Strict-mode error wording

When `composeUpFix == false` and `collectServiceDescriptors` produces nil for a build-backed service whose image declares `claw.describe`, fail before continuing into `resolveToolSubscriptions`:

```
service "trading-api": descriptor is missing or stale.
  this service emits its descriptor at runtime.
  rerun with 'claw up --fix -d' to refresh and apply.
```

When `resolveToolSubscriptions` fails with "references unknown tool" because the tool's provider service yielded nil descriptor in strict mode, wrap the error with the same hint pointing at `claw up --fix -d`.

## Tests

Required by acceptance criteria:

1. **Unit (compose_up_test.go).** `refreshRuntimeDescriptors` correctly:
   - Skips services without `compose.build`.
   - Skips services without `claw.describe` label.
   - Skips services whose image already contains the labeled descriptor file.
   - Skips services with a fresh `.claw-discovered/<svc>.claw-describe.json` snapshot.
   - Invokes the docker dance through a faked `runDescriptorRefreshDockerCommand` (mirror `runDiscoveryDockerCommand` indirection in discover.go).
   - Writes the descriptor snapshot to the expected path.
   - Surfaces logs on extraction timeout.

2. **Unit (compose_up_test.go).** Strict-mode error paths:
   - `collectServiceDescriptors` returns the `claw up --fix -d` hint when a build-backed service yields nil descriptor.
   - `resolveToolSubscriptions` wraps "unknown tool" errors with the `--fix -d` hint when the provider service is build-backed and has no descriptor.

3. **Spike (`cmd/claw/spike_runtime_descriptor_test.go`, build tag `spike`).** New end-to-end:
   - Build a Rails-shaped image that writes `/app/.claw-describe.json` only on container start (Python script with a 2-second sleep, then writes a real descriptor).
   - Pod with `x-claw.tools: [{ service: provider, allow: [tool_added_in_this_update] }]`.
   - Expect plain `claw up -d` to fail with the `--fix -d` hint.
   - Expect `claw up --fix -d` to succeed and the resulting `.claw-discovered/provider.claw-describe.json` to contain `tool_added_in_this_update`.

4. **Spike (existing `TestSpikeRollCall`).** Should remain green — the new code path is gated on the runtime-emitted signal and rollcall services bake their descriptors at build time.

## Out of scope

- Replacing the existing MCP-stdio `discoverMCPStdioService` flow. The new path is a sibling — both write to `.claw-discovered/`. Eventual unification is a refactor for later, after this lands.
- Auto-refresh for non-build-backed services (image services pulled from a registry). They cannot emit their descriptor between pull and start in any sensible way; if the image lacks the labeled file, that's a build-side bug.
- Generalizing `--fix` to refresh descriptors for services that already have a fresh-enough snapshot. Refresh is gated on staleness; the operator can `rm .claw-discovered/<svc>.claw-describe.json` to force a refresh on the next `--fix`.
- Touching `claw up -d` semantics beyond improving the error message. Strict mode stays strict.
- Documenting `docker compose cp` as a workflow anywhere. The README, `site/guide/cli.md`, and changelog should describe `claw up --fix -d` as the only path.

## Open questions for adversarial review

1. **Stub descriptor vs missing file.** Some Rails patterns ship a one-line stub `.claw-describe.json` in the build context (so `loadDescriptorFromImage` succeeds with empty data) instead of letting it be missing. Should `refreshRuntimeDescriptors` also fire when the image-extracted descriptor parses as "empty" (zero feeds, zero tools, zero memory, no skill)? Risk: false positives (a service that legitimately has nothing to declare gets re-extracted forever). Conservative answer: treat only `ErrNotExist` as the trigger; require operators with stub-shipping patterns to adopt the no-`COPY` pattern.

2. **Staleness oracle.** "Snapshot is older than image build time" requires correlating the snapshot's recorded `WrapperImageID` (per `discover.go`'s `DiscoveryMetadata`) with the current `docker inspect` image ID. We'd extend `describe.DiscoveryMetadata` (or a new `RuntimeMetadata` field) to record the image ID at extraction time. Alternative cheaper proxies: snapshot mtime vs image creation time, snapshot mtime vs `claw-pod.yml` mtime. The image-ID comparison is most accurate; mtime is fragile across `git checkout`. Recommend image-ID.

3. **Phase-1 compose vs reusing existing.** First-time deploys have no `compose.generated.yml` to reuse. Options:
   - **(a) Emit a minimal phase-1 compose** containing only the provider service plus its `depends_on` transitive closure. New emit code path but symmetric for first/subsequent deploys.
   - **(b) Reuse existing `compose.generated.yml` when present, otherwise fail with "first deploy needs a baked descriptor."** Simpler but punts the first-deploy ergonomics.
   - **(c) Emit the full new `compose.generated.yml` first with empty descriptors, bring up the providers from it, extract, then re-emit and re-apply.** Two-pass compile; risks emitting a compose that fails its own validation, which the test suite would have to special-case.
   Recommend (a) — symmetric, no first-deploy rough edge, the phase-1 file is short-lived under `.claw-discovered/`.

4. **Tearing down between extract and apply.** If we leave the provider running after extraction, the subsequent `docker compose up -d` will recreate it (because compose detects matching service definitions and the apply uses `--force-recreate` for runtime consumers anyway). If we tear it down, we incur an extra cold start but get a cleaner state. Recommend: leave it up; let the apply step handle it. Cuts ~5-15s per provider on the happy path.

5. **What if the provider service is also one of the runtime consumers (e.g. agent-managed)?** Currently providers we'd refresh are agent-managed-no (Rails app, not a claw-managed agent). The filter should explicitly require `!svc.IsAgentManaged()` — agent-managed services are descriptor consumers, not providers. Worth asserting in the filter to prevent confused setups.

6. **Should `claw discover` learn this too?** The existing `claw discover` command refreshes MCP-stdio snapshots. A symmetric `claw discover trading-api` for runtime-emitted services would be useful when the operator wants to refresh without going through `--fix`. Out of scope for #224 but a natural follow-up — flag for later issue?

## Build sequence

1. Add `refreshRuntimeDescriptors` (new file `cmd/claw/refresh_runtime_descriptors.go` mirroring `cmd/claw/discover.go`'s structure: ephemeral container helper + main loop).
2. Add `runRuntimeDescriptorRefreshCommand` indirection (mirroring `runDiscoveryDockerCommand`) for testability.
3. Generalize `discoveredDescribeFile` to non-stdio services.
4. Wire `refreshRuntimeDescriptors` into `runComposeUp` after `discoverMCPStdioServices`, gated on `composeUpFix`.
5. Add strict-mode error wrapping in `collectServiceDescriptors` and `resolveToolSubscriptions`.
6. Unit tests in `compose_up_test.go` (new test functions) using the faked docker indirection.
7. Spike test in new `cmd/claw/spike_runtime_descriptor_test.go`.
8. Update `site/guide/cli.md` "Staleness Guard" section to mention `claw up --fix -d` as the canonical refresh path; remove any `docker compose cp` references in the docs (none expected outside of the strict-mode error string).
9. CHANGELOG entry under `## Unreleased`.

Each step is small enough to verify independently. Build/test gates: `go build ./...`, `go vet ./...`, `go test ./...`, then the spike test.
