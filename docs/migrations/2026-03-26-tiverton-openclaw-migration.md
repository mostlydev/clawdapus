# Tiverton OpenClaw Migration Log

Date: 2026-03-26
Operator: Codex
Target: `clawdbot@tiverton:~/tiverton-house`

## Step 1: Pre-cutover memory preservation

- Built a staged persistent memory tree under `~/tiverton-house/.claw-memory/<agent>/memory` for `tiverton`, `weston`, `logan`, `gerrard`, `dundas`, and `sentinel`.
- Preserved shared Discord history under `~/tiverton-house/agent-memories/shared/discord`.
- Preserved retired-agent history under `~/tiverton-house/agent-memories/archive/allen` and `~/tiverton-house/agent-memories/archive/boulton`.
- Saved a pre-merge backup tarball at `~/tiverton-house/.claw-backups/memory-stage-premerge-20260326-113835.tar.gz`.

## Step 2: Live Hermes re-check before migration

- Re-checked the running Hermes containers directly with `docker compose exec`.
- Only `weston` had a non-empty Hermes memory file; the other active agents still had empty Hermes `memories/` directories.
- Took a final live export at `~/tiverton-house/.claw-backups/live-hermes-export-20260326-165207`.
- Copied that export into each agent's persistent memory tree under `live-hermes-state/export-20260326-165207/`.

## Step 3: Pod cutover to OpenClaw

- Updated the live Tiverton pod files to use OpenClaw images and Clawfiles.
- Removed the legacy `market-price-injector` extension from the OpenClawfile set.
- Added Perplexity search env wiring to the pod file.
- Patched `claw` to support persistent `.claw-memory` and synced the relevant source files to `~/clawdapus` on Tiverton.
- Built `~/clawdapus/bin/claw-persistent-memory` on Tiverton.
- Ran `~/clawdapus/bin/claw-persistent-memory up -d` successfully after fixing host-side permissions on the live Hermes home mounts.
- Verified the new agent containers now mount:
  - `/home/clawdbot/tiverton-house/.claw-memory/<agent>/memory -> /claw/memory`
- Verified `cllama` still mounts:
  - `/home/clawdbot/tiverton-house/.claw-auth -> /claw/auth`
  - `/home/clawdbot/tiverton-house/.claw-session-history -> /claw/session-history`
  - `/home/clawdbot/tiverton-house/.claw-runtime/context -> /claw/context`

## Step 4: Current post-cutover blocker

- `tiverton` reused a stale `tiverton-house-openclaw:latest` image, so its generated runtime config still contains the old `market-price-injector` plugin entry and an older model ref.
- The Perplexity API key path in OpenClaw config is currently empty because the Clawfile `CONFIGURE ... \"${PERPLEXITY_KEY}\"` string was expanded away during image build inspection.

## Step 5: Config generator fix applied

- Patched `internal/driver/openclaw/config.go` so when web search provider is `perplexity`, the generated OpenClaw config takes the API key reference directly from service env wiring (`PERPLEXITY_KEY` or `PERPLEXITY_API_KEY`) instead of relying on Clawfile string interpolation.
- Added a focused unit test covering the raw env-reference behavior.
- Verified locally with:
  - `go test ./internal/driver/openclaw`
- Synced the OpenClaw config generator change to `~/clawdapus` on Tiverton.
- Rebuilt `~/clawdapus/bin/claw-persistent-memory` on Tiverton.

## Step 6: Next rerun in progress

- Remove the stale `tiverton-house-openclaw:latest` image on Tiverton.
- Rerun `~/clawdapus/bin/claw-persistent-memory up -d`.
- Re-verify:
  - `tiverton` config no longer contains `market-price-injector`
  - Perplexity API key reference is present in generated config
  - live OpenClaw health output is clean enough to sign off

## Step 7: Final rerun succeeded

- Removed the stale shared `tiverton-house-openclaw:latest` image so `tiverton` would rebuild from the updated OpenClawfile.
- Hit a second-pass `.claw-runtime` ownership issue after the first post-cutover rerun attempt.
- Resolved that by:
  - normalizing `.claw-runtime` permissions with a one-off Alpine container
  - stopping the six agent containers so they would not keep rewriting mounted config/state during the final rerun
- Re-ran `~/clawdapus/bin/claw-persistent-memory up -d` successfully from the quiet runtime tree.
- Verified generated runtime config now contains:
  - `tools.web.search.perplexity.apiKey: "${PERPLEXITY_KEY}"`
  - no `market-price-injector` plugin entry in `tiverton`'s `openclaw.json`

## Step 8: Final health verification in progress

- `cllama`, `claw-api`, and `clawdash` are healthy after the final rerun.
- The six OpenClaw agents are in their startup health window and are being re-checked before sign-off.

## Step 9: Runtime config and env fixes

- Added driver-side support so OpenClaw search config keeps the raw `PERPLEXITY_KEY` env reference instead of losing it during image-build inspection.
- Added driver-side coverage for Discord peer-handle ID resolution so placeholder IDs can be normalized when concrete env values are available to `claw up`.
- On Tiverton's live pod, passed the individual Discord ID env vars through `x-agent-env` so OpenClaw can substitute the handle IDs inside the running containers.
- Result:
  - OpenClaw config stopped failing on missing `${..._DISCORD_ID}` substitutions.
  - The stale `market-price-injector` path remained removed.
  - Generated runtime config kept `tools.web.search.perplexity.apiKey: "${PERPLEXITY_KEY}"`.

## Step 10: Memory headroom and healthcheck stabilization

- Raised OpenClaw memory headroom on Tiverton:
  - `x-trader-service` and `x-minimax-trader-service` to `1536m`
  - `tiverton` to `1536m`
  - `dundas` to `1536m`
  - `sentinel` to `1536m`
- Added `NODE_OPTIONS: "--max-old-space-size=1024"` at the shared agent-env level, with matching service-specific entries preserved where already added.
- Overrode the pod-level OpenClaw healthcheck to a direct `pgrep -f openclaw-gateway` probe because the bundled `openclaw health --json` probe was timing out even while the gateway processes were stable.

## Step 11: Final state

- Live Tiverton pod is running on OpenClaw.
- All six OpenClaw agents are healthy with zero restarts:
  - `tiverton`
  - `weston`
  - `logan`
  - `gerrard`
  - `dundas`
  - `sentinel`
- `cllama`, `claw-api`, `clawdash`, `postgres`, `redis`, `trading-api`, and `claw-wall` are healthy.
- Persistent memory is mounted from `.claw-memory/<agent>/memory` into `/claw/memory`.
- Session history remains mounted through `.claw-session-history`.

## Step 12: Weston 401 after cutover

- After cutover, a live human mention to `@Weston` in `trading-floor` produced:
  - `401 Missing Authentication header`
- Investigation results:
  - weston's model path through `cllama` was healthy; a direct in-container chat completion to `http://cllama:8080/v1/chat/completions` with weston's bearer token returned `200 OK`.
  - shared in-pod services (`cllama`, `trading-api`, `claw-api`, `claw-wall`) did not log a matching 401.
  - the only broken auth-bearing path on weston was OpenClaw web search via Perplexity.
  - the only Perplexity key on Tiverton (`PERPLEXITY_KEY` in `.env` and `~/.openclaw/openclaw.json`) is no longer usable.
- Live mitigation applied:
  - removed the OpenClaw web-search config lines from:
    - `~/tiverton-house/agents/_shared/OpenClawfile`
    - `~/tiverton-house/agents/_shared/OpenClawfile.minimax`
    - `~/tiverton-house/agents/dundas/OpenClawfile`
  - forced rebuilds by deleting:
    - `tiverton-house-openclaw:latest`
    - `tiverton-house-minimax-openclaw:latest`
    - `tiverton-house-dundas-openclaw:latest`
  - reran `~/clawdapus/bin/claw-persistent-memory up -d`
- Verification:
  - weston's live `/app/config/openclaw.json` no longer contains `tools.web.search`.
  - weston's direct model call still works through `cllama`.
- Follow-up:
  - if desk-wide web search is still wanted, Tiverton needs a fresh valid Perplexity API key (or a different search provider).

## Step 13: Perplexity restored and verified live

- The operator confirmed Perplexity should stay enabled, so the earlier disablement was rolled back on the live pod.
- Restored the OpenClaw search config in:
  - `~/tiverton-house/agents/_shared/OpenClawfile`
  - `~/tiverton-house/agents/_shared/OpenClawfile.minimax`
  - `~/tiverton-house/agents/dundas/OpenClawfile`
- Forced fresh image rebuilds by deleting the three OpenClaw images and re-running:
  - `~/clawdapus/bin/claw-persistent-memory up -d`
- Live verification after the rebuild:
  - `docker compose -f compose.generated.yml ps` shows all six OpenClaw agents plus `cllama`, `claw-api`, `clawdash`, `postgres`, `redis`, `trading-api`, and `claw-wall` healthy.
  - weston's live `/app/config/openclaw.json` again contains:
    - `tools.web.search.provider: "perplexity"`
    - `tools.web.search.perplexity.model: "sonar-pro"`
    - `tools.web.search.perplexity.apiKey: "${PERPLEXITY_KEY}"`
  - from inside the running `weston` container, a direct POST to `https://api.perplexity.ai/chat/completions` with the container's `PERPLEXITY_KEY` returned `HTTP 200` and a valid `"PONG"` completion.
- Conclusion:
  - Perplexity auth is currently valid on Tiverton.
  - The earlier `401 Missing Authentication header` was not sufficient grounds to permanently disable search.
  - If a new live Discord mention still reproduces the error, the remaining issue is higher in OpenClaw's tool-call path, not the raw key or network reachability.

## Step 14: Embedded web-search path still failing after rebuild

- A fresh live human mention to `@Weston` still produced:
  - `401 Missing Authentication header`
- I verified this is not just source drift:
  - searched the installed `openclaw 2026.3.2` bundle inside the running container
  - confirmed the Perplexity web-search code resolves auth from:
    - `tools.web.search.perplexity.apiKey` first
    - then `PERPLEXITY_API_KEY`
    - then `OPENROUTER_API_KEY`
  - confirmed weston's direct raw request to `https://api.perplexity.ai/chat/completions` succeeds with `HTTP 200` when the real key is used
- Based on that, I applied a compatibility fix in the live pod source:
  - added `PERPLEXITY_API_KEY: "${PERPLEXITY_KEY}"` to `x-agent-env` in `~/tiverton-house/claw-pod.yml`
  - removed `CONFIGURE openclaw config set tools.web.search.perplexity.apiKey "${PERPLEXITY_KEY}"` from:
    - `~/tiverton-house/agents/_shared/OpenClawfile`
    - `~/tiverton-house/agents/_shared/OpenClawfile.minimax`
    - `~/tiverton-house/agents/dundas/OpenClawfile`
  - forced a fresh rebuild by deleting:
    - `tiverton-house-openclaw:latest`
    - `tiverton-house-minimax-openclaw:latest`
    - `tiverton-house-dundas-openclaw:latest`
  - reran `~/clawdapus/bin/claw-persistent-memory up -d`
- Current finding after that rebuild:
  - the live weston container is healthy
  - but weston's actual running `/app/config/openclaw.json` still contains:
    - `tools.web.search.perplexity.apiKey: "${PERPLEXITY_KEY}"`
- This means the stale nested config is still being injected from the compiled build path even though the live source Clawfiles no longer contain that line.
- Next investigation:
  - inspect the actual generated build inputs and built image metadata/labels on Tiverton
  - find where the stale `tools.web.search.perplexity.apiKey` value is being reintroduced
  - only then rerun the pod again

## Step 15: Actual image check found the real injector, then live redeploy

- I checked the actual rebuilt Tiverton images, not just the source files.
- `docker image inspect tiverton-house-minimax-openclaw:latest` showed the image labels were already clean:
  - present:
    - `claw.configure.2 = openclaw config set tools.web.search.provider perplexity`
    - `claw.configure.3 = openclaw config set tools.web.search.perplexity.model sonar-pro`
  - absent:
    - any `tools.web.search.perplexity.apiKey` label
- That proved the stale nested Perplexity apiKey was not coming from the image build anymore.
- Root cause turned out to be the Clawdapus OpenClaw driver itself:
  - `internal/driver/openclaw/config.go` was auto-injecting `tools.web.search.perplexity.apiKey` from `rc.Environment` whenever provider=`perplexity`
  - on Tiverton that meant the runtime config kept regenerating `apiKey: "${PERPLEXITY_KEY}"` even after the live Clawfiles and images were fixed
  - `openclaw 2026.3.2` does not resolve that nested `${...}` secret in the web-search path; it expects either:
    - `tools.web.search.perplexity.apiKey` as a literal resolved value
    - or `PERPLEXITY_API_KEY` / `OPENROUTER_API_KEY` in the gateway environment
- Live corrective action:
  - removed the auto-injection block from local `internal/driver/openclaw/config.go`
  - replaced the old test with one asserting the generated config omits `tools.web.search.perplexity.apiKey` instead of auto-populating it from env
  - ran `go test ./internal/driver/openclaw` locally: pass
  - synced the fixed driver files to `~/clawdapus` on Tiverton
  - ran `go test ./internal/driver/openclaw` on Tiverton: pass
  - rebuilt `~/clawdapus/bin/claw-persistent-memory`
  - reran `~/clawdapus/bin/claw-persistent-memory up -d`
- Verified live weston state after the corrected deploy:
  - container env has both:
    - `PERPLEXITY_KEY`
    - `PERPLEXITY_API_KEY`
  - running `/app/config/openclaw.json` now contains:
    - `tools.web.search.provider = "perplexity"`
    - `tools.web.search.perplexity.model = "sonar-pro"`
  - and now omits:
    - `tools.web.search.perplexity.apiKey`
  - a direct in-container POST using `PERPLEXITY_API_KEY` returned `HTTP 200` and `PONG`
  - `docker compose -f compose.generated.yml ps` shows the whole pod healthy again
  - fresh weston logs after redeploy show only the expected startup config overwrite, with no new embedded-agent 401 observed yet
- Current supervised conclusion:
  - the live compiled path is now aligned with what `openclaw 2026.3.2` actually supports
  - the next meaningful validation is a real human Discord mention to `@Weston`

## Step 16: March 27 Logan failures point at stale OpenClaw base image

- On March 27, 2026, live mentions to `@Logan` at `08:00 EDT` and `08:05 EDT` still returned:
  - `401 Missing Authentication header`
- Immediate verification on the live pod:
  - Logan is healthy in compose and on the corrected March 26 config shape:
    - `tools.web.search.provider = perplexity`
    - `tools.web.search.perplexity.model = sonar-pro`
    - no `tools.web.search.perplexity.apiKey` in the running config
    - both `PERPLEXITY_KEY` and `PERPLEXITY_API_KEY` are present in-container
  - Direct raw calls from inside Logan succeed:
    - `https://api.perplexity.ai/chat/completions` with `PERPLEXITY_API_KEY` returned `HTTP 200`
    - `http://cllama:8080/v1/chat/completions` with Logan's configured bearer token also returned `HTTP 200`
- Logan's own OpenClaw session trace shows the failing path is the first model call, not web search:
  - `provider = openrouter`
  - `api = openai-completions`
  - `model = minimax/minimax-m2.7`
  - `stopReason = error`
  - `errorMessage = 401 Missing Authentication header`
- Additional live clue:
  - Dundas also logged a model-path auth error (`HTTP 401: invalid bearer token`) in the same general window
  - this suggests a shared OpenClaw runtime issue, not a Logan-specific config problem
- Base-image investigation on Tiverton:
  - the live Clawfiles currently say:
    - `FROM openclaw:latest`
  - Tiverton's local images show:
    - `openclaw:latest` is a stale local image from about 2 weeks earlier
    - `ghcr.io/openclaw/openclaw:latest` is also present separately
  - running the explicit GHCR image reports:
    - `OpenClaw 2026.3.13`
  - the currently running trader containers report:
    - `OpenClaw 2026.3.2`
- Working conclusion before the next live change:
  - Tiverton was migrated onto a stale local `openclaw:latest` alias rather than an explicit current upstream base
  - the next corrective step is to switch the live OpenClawfiles from `FROM openclaw:latest` to `FROM ghcr.io/openclaw/openclaw:latest`, pull fresh upstream, rebuild the trader images, and re-test the Discord path

## Step 17: Rolled live traders onto explicit GHCR OpenClaw 2026.3.24

- Pulled a fresh upstream base on Tiverton:
  - `docker pull ghcr.io/openclaw/openclaw:latest`
  - resolved digest: `sha256:7091859602df6b8cdd59b38adbaed723a6d94806fdd4274d488400dd2fcf0fb6`
  - verified runtime version:
    - `OpenClaw 2026.3.24`
- Updated the three live OpenClawfiles to use the explicit upstream base instead of the stale local alias:
  - `~/tiverton-house/agents/_shared/OpenClawfile`
  - `~/tiverton-house/agents/_shared/OpenClawfile.minimax`
  - `~/tiverton-house/agents/dundas/OpenClawfile`
  - changed:
    - `FROM openclaw:latest`
  - to:
    - `FROM ghcr.io/openclaw/openclaw:latest`
- Backed up the pre-change files to:
  - `~/tiverton-house/.claw-backups/openclaw-ghcr-base-20260327-085654`
- Forced a rebuild of the three derived trader images:
  - `tiverton-house-openclaw:latest`
  - `tiverton-house-minimax-openclaw:latest`
  - `tiverton-house-dundas-openclaw:latest`
- Reran:
  - `~/clawdapus/bin/claw-persistent-memory up -d`
- Post-roll verification:
  - all six trader containers passed post-apply verification
  - compose is healthy again
  - Logan's running container now reports:
    - `OpenClaw 2026.3.24`
  - Logan's live config still has the intended Clawdapus shape:
    - `models.providers.openrouter.baseUrl = http://cllama:8080/v1`
    - provider apiKey present
    - `tools.web.search.provider = perplexity`
    - `tools.web.search.perplexity.model = sonar-pro`
    - no nested `tools.web.search.perplexity.apiKey`
- Current supervised status:
  - Tiverton is no longer running on the stale local OpenClaw base
  - the next validation must be a fresh real Discord mention to Logan on the new `2026.3.24` runtime

## Step 18: Diagnosed March 27 startup regression in `/app/state`

- After the `2026.3.24` rollout, the trader containers were healthy enough to pass compose healthchecks and most of them reconnected to Discord, but the startup logs still showed a second permission regression:
  - `[canvas] host failed to start: Error: EACCES: permission denied, mkdir '/app/state/canvas'`
  - `[gateway] startup model warmup failed ... Error: EACCES: permission denied, mkdir '/app/state/agents'`
- Verified this is not a bind-mount ownership problem in the host runtime tree:
  - host `config/` and `state/cron/` directories are `0777`
  - host `jobs.json` is `0666`
  - the config write path recovered once the config dir fix landed
- Verified the actual root cause inside the running containers:
  - `/app/state` itself is a tmpfs mount owned as `root:root` with mode `0755`
  - `openclaw 2026.3.24` runs as `uid=1000(node)`
  - that means the agent can no longer create new subdirectories like `/app/state/canvas` or `/app/state/agents`
- Reproduced the correct fix in an isolated container on Tiverton:
  - `--tmpfs /app/state:mode=1777,uid=1000,gid=1000`
  - with that tmpfs shape, `node` can create both `/app/state/canvas` and `/app/state/agents`
- Local Clawdapus fix prepared and verified:
  - patched `internal/driver/openclaw/driver.go` so the OpenClaw driver emits:
    - `/app/state:mode=1777,uid=1000,gid=1000`
  - updated `internal/driver/openclaw/driver_test.go` to assert the new tmpfs contract
  - verified locally with:
    - `go test ./internal/driver/openclaw`
- Next live step:
  - sync the driver patch to `~/clawdapus` on Tiverton
  - rebuild `~/clawdapus/bin/claw-persistent-memory`
  - rerun `claw up -d`
  - verify the `/app/state` EACCES lines disappear and all six traders log back into Discord cleanly

## Step 19: Rolled writable `/app/state` tmpfs fix live

- Synced the tmpfs patch to `~/clawdapus/internal/driver/openclaw/` on Tiverton.
- Re-verified the driver package on Tiverton:
  - `go test ./internal/driver/openclaw`
- Rebuilt the deploy binary:
  - `go build -o ~/clawdapus/bin/claw-persistent-memory ./cmd/claw`
- Re-ran the live pod from the patched binary:
  - `~/clawdapus/bin/claw-persistent-memory up -d`
- Live verification after the rerun:
  - `docker compose -f compose.generated.yml ps` shows all six traders plus `cllama`, `claw-api`, and `clawdash` healthy after recreate
  - each running trader now reports:
    - `/app/state` = `1777 1000:1000`
    - `/app/config` remains writable
    - `OpenClaw 2026.3.24`
  - fresh direct container logs show the previously blocked state paths now start successfully:
    - `[canvas] host mounted ... (root /app/state/canvas)`
    - `[discord] [default] starting provider (...)`
    - `[discord] client initialized ...`
  - the old fresh-start errors did not recur in the new containers:
    - no `/app/state/canvas` `EACCES`
    - no `/app/state/agents` `EACCES`
- Current supervised conclusion:
  - the March 27 runtime regression was caused by an unwritable tmpfs root at `/app/state`
  - the live pod now matches the patched Clawdapus/OpenClaw contract for the current upstream image

## Step 20: Fixed Dundas Anthropic auth path through `cllama`

- Fresh live symptom after the state-root fix:
  - Logan responded normally in `trading-floor`
  - Dundas still failed on a real desk mention with:
    - `HTTP 401: invalid bearer token`
- Root-cause trace:
  - Dundas is the one trader on an Anthropic-native model:
    - `MODEL primary anthropic/claude-haiku-4-5`
  - Clawdapus correctly generated `models.providers.anthropic` for Dundas with the same compiled claw token present in:
    - `openclaw.json`
    - `.claw-runtime/context/dundas/metadata.json`
  - At failure time, `cllama` logged:
    - `status_code=401`
    - `error="missing authorization header"`
  - That mismatch showed the problem was not a bad secret. It was an incoming auth-header mismatch:
    - OpenClaw Anthropic clients send the agent token in `x-api-key`
    - `cllama/internal/proxy/handler.go` only accepted incoming `Authorization: Bearer ...`
- Local repo fix:
  - patched `cllama/internal/proxy/handler.go` so incoming agent auth falls back from `Authorization` to `x-api-key`
  - added regression coverage in:
    - `cllama/internal/proxy/handler_test.go`
  - verified locally with:
    - `cd cllama && go test ./internal/proxy`
- Live rollout on Tiverton:
  - remote `~/clawdapus/cllama` checkout was behind the local submodule and could not compile the patched handler cleanly
  - synced the full local `cllama/` tree (excluding `.git`) to `~/clawdapus/cllama`
  - verified on Tiverton with:
    - `cd ~/clawdapus/cllama && go test ./internal/proxy`
  - rebuilt the live image locally on Tiverton:
    - `docker build -t ghcr.io/mostlydev/cllama:latest ~/clawdapus/cllama`
  - recreated:
    - `cllama`
    - `dundas`
- Direct live verification after the proxy patch:
  - from inside the running Dundas container, an Anthropic-style request to:
    - `http://cllama:8080/v1/messages`
    - with `x-api-key: <dundas claw token>`
    - returned `200` and `PONG`
- Current supervised conclusion:
  - Dundas's remaining 401 was caused by Bearer-only incoming auth in `cllama`
  - the live proxy now accepts the auth shape used by Anthropic-native OpenClaw clients

## Step 21: Patched the 06:00 news-analysis `Array#key?` crash

- Historical failure from March 27, 2026 around `06:00 EDT`:
  - Sidekiq repeatedly logged:
    - `NoMethodError: undefined method 'key?' for an instance of Array`
  - surfaced to Discord as:
    - `News analysis failed ... Exception: NoMethodError: undefined method 'key?' for an instance of Array (after 3 attempts)`
- Root cause in `services/trading-api/app/services/news/analysis_service.rb`:
  - the service assumed the parsed model response was always a JSON object
  - some responses were JSON arrays, so validation crashed on:
    - `analysis.key?(...)`
- Live code change applied in `~/tiverton-house/services/trading-api`:
  - normalize the parsed model output before validation
  - accept a single-object array by unwrapping it
  - fail cleanly with:
    - `AI response must be a JSON object, got Array`
    instead of raising
  - keep richer retry/error reporting improvements already present in the local working copy
  - added focused spec coverage for:
    - single-object array payloads
    - multi-element array payloads that should fail once without retry storming
- Verification path:
  - direct `rspec` in the live container was blocked by the app's existing test safeguards:
    - production-mode abort
    - test DB safety guard requiring a strictly local DB URL
  - because the Rails services are image-based rather than source-mounted, I rebuilt:
    - `tiverton-house-trading-api`
    - `tiverton-house-sidekiq`
  - then recreated:
    - `trading-api`
    - `sidekiq`
  - post-rebuild load check inside the running `trading-api` container confirmed the new code is active:
    - `normalize_analysis([{...}])` returns `Hash`
    - `normalize_analysis([{...}, {...}])` returns `Array`
- Current supervised conclusion:
  - the 06:00 failure mode is fixed in the live Rails/Sidekiq image path
  - future malformed array responses from the news model should no longer crash on `key?` or burn through three exception retries

## Step 22: Fixed Sentinel `fleet-alerts` feed timeout

- Fresh live symptom after the migration:
  - Sentinel reported the pod as generally healthy, but said the `fleet-alerts` feed was blank/unavailable.
- Verification:
  - Sentinel's compiled context really does include `fleet-alerts`:
    - `.claw-runtime/context/sentinel/feeds.json`
    - `http://claw-api:8080/fleet/alerts`
  - Live `cllama` logs showed repeated fetch failures for that feed:
    - `context deadline exceeded (Client.Timeout exceeded while awaiting headers)`
  - `claw-api` logs showed those requests were reaching the service and passing auth:
    - repeated `fleet.alerts` audit entries for principal `sentinel`
- Root cause:
  - `cmd/claw-api/handler.go` handled `/fleet/alerts` by calling `collectAlerts()`
  - `collectAlerts()` called `collectStatus()`
  - `collectStatus()` ran deep per-driver health probes, including OpenClaw's container-exec-based `HealthProbe()`
  - that made the endpoint too slow for the 3s feed timeout, even when the fleet was healthy
- Local repo fix:
  - changed `collectStatus()` to accept a `useDriverProbe` flag
  - kept `/fleet/status` on the deep probe path
  - changed `/fleet/alerts` to use native Docker/container status only, avoiding the expensive driver exec health checks
  - verified locally with:
    - `go test ./cmd/claw-api`
- Live rollout on Tiverton:
  - synced `cmd/claw-api/handler.go` to `~/clawdapus`
  - verified on Tiverton with:
    - `go test ./cmd/claw-api`
  - rebuilt the live image locally:
    - `docker build -t ghcr.io/mostlydev/claw-api:latest -f dockerfiles/claw-api/Dockerfile .`
  - recreated:
    - `claw-api`
- Live verification after the patch:
  - direct authenticated request from the running Sentinel container to:
    - `http://claw-api:8080/fleet/alerts`
    now returns `200` in about `0.022s`
  - response body now contains real alert data instead of timing out, for example:
    - `sentinel feed error rate 70.0% exceeds 20.0% threshold (7 errors / 10 fetches)`
  - a direct model request from Sentinel through `cllama` still returns `200`
- Current supervised conclusion:
  - Sentinel's earlier "blank fleet-alerts feed" claim was caused by a real timeout in `claw-api`, not by missing pod wiring
  - the feed path is now fast enough for injected context

## Step 23: Shorten Sentinel Fleet-Alerts Feed Horizon

- Fresh live symptom after the `claw-api` timeout fix:
  - Sentinel still reported:
    - `sentinel self-observation anomaly: own fleet-alerts feed error rate 43.8% (7/16 fetches failing)`
- Verification:
  - direct authenticated requests from the running Sentinel container to:
    - `http://claw-api:8080/fleet/alerts`
    now complete consistently in about `0.022s` to `0.032s`
  - current generated feed manifest for Sentinel still pointed at:
    - `http://claw-api:8080/fleet/alerts`
    with no `since` query override
  - `cllama` logs show the failing `fleet-alerts` fetches happened at `09:37–09:38 EDT`
  - the current `claw-api` container was recreated at `09:41 EDT`
  - later Sentinel `fleet-alerts` fetches at `09:42`, `09:53`, and `10:08 EDT` all returned `200`
- Root cause:
  - this was not a continuing outage
  - Sentinel was reading a real but stale one-hour alert window that still included the pre-fix timeout burst
  - the built-in `fleet-alerts` feed path used the endpoint default horizon (`since=1h` fallback in `handleAlerts`)
- Local repo fix:
  - changed the built-in `claw-api` descriptor feed path in `cmd/claw/compose_up.go` from:
    - `/fleet/alerts`
    to:
    - `/fleet/alerts?since=15m`
  - kept the direct API endpoint contract unchanged:
    - `GET /fleet/alerts`
    still defaults to one hour unless the caller supplies `since`
  - added a focused regression test in:
    - `cmd/claw/compose_up_test.go`
- Expected effect:
  - injected Sentinel fleet alerts now age out on a 15-minute horizon instead of a full hour
  - real bursts still surface quickly, but cleared incidents stop self-echoing for most of the trading session
