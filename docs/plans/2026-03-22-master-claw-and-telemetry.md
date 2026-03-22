# Master Claw & Telemetry Closure Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Close ADR-014 (telemetry normalization) and bring the Master Claw pattern (ADR-012) to a working end-to-end demo with both a standalone example and tiverton-house production integration.

**Architecture:** The audit pipeline (`internal/audit/`) already handles normalization, filtering, and summarization. `claw-api` already serves `/fleet/{status,logs,metrics,alerts}` with bearer auth. `x-claw.master` auto-injects `claw-api` into compose. The remaining work is: (1) surface `feed_fetch` events in audit, (2) add configurable alert thresholds to `claw-api`, (3) create a minimal standalone master-claw example, and (4) wire Sentinel into the tiverton-house production pod.

**Tech Stack:** Go 1.22+, Docker Compose, claw CLI, claw-api HTTP service

**Dependency:** The cllama submodule (`cllama/`) contains the producer side for `feed_fetch` telemetry events. `LogFeedFetch()` is already implemented in `cllama/internal/logging/logger.go` and called from the feed fetcher in `cllama/internal/feeds/fetcher.go`. The submodule is a private SSH repo and appears empty in fresh clones/worktrees. Tasks 1-2 build the **consumer** side in `internal/audit/` — they normalize events that cllama already emits. No cllama changes are needed.

---

## Phase 1: Telemetry Polish (ADR-014 Closure)

### Task 1: Surface `feed_fetch` events in audit Event struct

The cllama logger already emits `feed_fetch` events with `feed_name` and `feed_url` fields (see `cllama/internal/logging/logger.go:LogFeedFetch`), but the audit `Event` struct on the consumer side drops those fields during normalization. The Master Claw needs to see feed health.

**Files:**
- Modify: `internal/audit/event.go:5-18`
- Modify: `internal/audit/normalize.go:37-66`
- Test: `internal/audit/normalize_test.go`

**Step 1: Write the failing test**

Add to `internal/audit/normalize_test.go`:

```go
func TestNormalizeLineParseFeedFetchEvent(t *testing.T) {
	line := `{"ts":"2026-03-22T10:00:00Z","claw_id":"weston","type":"feed_fetch","feed_name":"market-context","feed_url":"http://trading-api:4000/api/v1/market_context/weston","status_code":200,"latency_ms":45}`
	event, err := NormalizeLine([]byte(line))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if event.Type != "feed_fetch" {
		t.Fatalf("expected type feed_fetch, got %q", event.Type)
	}
	if event.FeedName != "market-context" {
		t.Fatalf("expected feed_name market-context, got %q", event.FeedName)
	}
	if event.FeedURL != "http://trading-api:4000/api/v1/market_context/weston" {
		t.Fatalf("expected feed_url, got %q", event.FeedURL)
	}
	if event.StatusCode == nil || *event.StatusCode != 200 {
		t.Fatalf("expected status_code 200, got %v", event.StatusCode)
	}
}
```

**Step 2: Run test to verify it fails**

Run: `cd /Users/wojtek/dev/ai/clawdapus/.worktrees/master-claw && go test ./internal/audit/ -run TestNormalizeLineParseFeedFetchEvent -v`
Expected: FAIL — `event.FeedName undefined`

**Step 3: Add fields to Event struct and normalization**

In `internal/audit/event.go`, add to `Event` struct after `Error`:

```go
	FeedName string `json:"feed_name,omitempty"`
	FeedURL  string `json:"feed_url,omitempty"`
```

In `internal/audit/normalize.go`, add to `NormalizeLine` after the `Error` field assignment (line 42):

```go
	FeedName:      strings.TrimSpace(stringField(raw, "feed_name")),
	FeedURL:       strings.TrimSpace(stringField(raw, "feed_url")),
```

**Step 4: Run test to verify it passes**

Run: `cd /Users/wojtek/dev/ai/clawdapus/.worktrees/master-claw && go test ./internal/audit/ -v`
Expected: ALL PASS

**Step 5: Commit**

```bash
cd /Users/wojtek/dev/ai/clawdapus/.worktrees/master-claw
git add internal/audit/event.go internal/audit/normalize.go internal/audit/normalize_test.go
git commit -m "feat(audit): surface feed_fetch events with feed_name and feed_url fields"
```

---

### Task 2: Count `feed_fetch` events in agent summary

Feed fetch failures should appear in summaries so the Master Claw can detect stale feeds.

**Files:**
- Modify: `internal/audit/event.go:20-32` (AgentSummary)
- Modify: `internal/audit/query.go:71-84` (Summarize switch)
- Test: `internal/audit/normalize_test.go`

**Step 1: Write the failing test**

Add to `internal/audit/normalize_test.go`:

```go
func TestSummarizeCountsFeedFetches(t *testing.T) {
	events := []Event{
		{ClawID: "weston", Type: "request"},
		{ClawID: "weston", Type: "feed_fetch", FeedName: "market-context", StatusCode: ptrInt(200)},
		{ClawID: "weston", Type: "feed_fetch", FeedName: "market-context", StatusCode: ptrInt(500)},
	}
	summary := Summarize(events)
	if len(summary.Agents) != 1 {
		t.Fatalf("expected 1 agent, got %d", len(summary.Agents))
	}
	agent := summary.Agents[0]
	if agent.FeedFetches != 2 {
		t.Fatalf("expected 2 feed fetches, got %d", agent.FeedFetches)
	}
	if agent.FeedErrors != 1 {
		t.Fatalf("expected 1 feed error, got %d", agent.FeedErrors)
	}
}
```

**Step 2: Run test to verify it fails**

Run: `cd /Users/wojtek/dev/ai/clawdapus/.worktrees/master-claw && go test ./internal/audit/ -run TestSummarizeCountsFeedFetches -v`
Expected: FAIL — `agent.FeedFetches undefined`

**Step 3: Add FeedFetches/FeedErrors to AgentSummary and Summarize**

In `internal/audit/event.go`, add to `AgentSummary` struct after `ModelUsage`:

```go
	FeedFetches int `json:"feed_fetches"`
	FeedErrors  int `json:"feed_errors"`
```

In `internal/audit/query.go`, add a case to the switch in `Summarize`:

```go
	case "feed_fetch":
		agent.FeedFetches++
		if event.StatusCode != nil && *event.StatusCode >= 400 {
			agent.FeedErrors++
		}
		if event.Error != "" {
			agent.FeedErrors++
		}
```

**Step 4: Run tests**

Run: `cd /Users/wojtek/dev/ai/clawdapus/.worktrees/master-claw && go test ./internal/audit/ -v`
Expected: ALL PASS

**Step 5: Commit**

```bash
cd /Users/wojtek/dev/ai/clawdapus/.worktrees/master-claw
git add internal/audit/event.go internal/audit/query.go internal/audit/normalize_test.go
git commit -m "feat(audit): count feed_fetch events and feed errors in agent summary"
```

---

### Task 3: Add configurable alert thresholds to claw-api

Currently `/fleet/alerts` fires on any `errors > 0`. Real pods need thresholds like "error rate > 5%" or "cost > $X/hour."

**Files:**
- Create: `internal/clawapi/thresholds.go`
- Create: `internal/clawapi/thresholds_test.go`
- Modify: `cmd/claw-api/handler.go:343-383` (collectAlerts)
- Modify: `cmd/claw-api/main.go` (load thresholds config)

**Step 1: Write the failing test for threshold evaluation**

Create `internal/clawapi/thresholds_test.go`:

```go
package clawapi

import (
	"testing"

	"github.com/mostlydev/clawdapus/internal/audit"
)

func TestDefaultThresholdsFlagErrors(t *testing.T) {
	th := DefaultThresholds()
	agent := audit.AgentSummary{
		ClawID:   "weston",
		Requests: 100,
		Errors:   6,
	}
	alerts := th.Evaluate(agent)
	if len(alerts) == 0 {
		t.Fatal("expected error rate alert")
	}
	found := false
	for _, a := range alerts {
		if a.Type == "error_rate" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected error_rate alert, got %v", alerts)
	}
}

func TestDefaultThresholdsNoAlertWhenHealthy(t *testing.T) {
	th := DefaultThresholds()
	agent := audit.AgentSummary{
		ClawID:   "weston",
		Requests: 100,
		Errors:   1,
		CostUSD:  0.50,
	}
	alerts := th.Evaluate(agent)
	if len(alerts) != 0 {
		t.Fatalf("expected no alerts for healthy agent, got %v", alerts)
	}
}

func TestThresholdsFlagFeedErrors(t *testing.T) {
	th := DefaultThresholds()
	agent := audit.AgentSummary{
		ClawID:      "weston",
		Requests:    10,
		FeedFetches: 10,
		FeedErrors:  4,
	}
	alerts := th.Evaluate(agent)
	found := false
	for _, a := range alerts {
		if a.Type == "feed_error_rate" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected feed_error_rate alert, got %v", alerts)
	}
}
```

**Step 2: Run test to verify it fails**

Run: `cd /Users/wojtek/dev/ai/clawdapus/.worktrees/master-claw && go test ./internal/clawapi/ -run TestDefaultThresholds -v`
Expected: FAIL — compilation error, types don't exist

**Step 3: Implement thresholds**

Create `internal/clawapi/thresholds.go`:

```go
package clawapi

import (
	"fmt"

	"github.com/mostlydev/clawdapus/internal/audit"
)

type ThresholdAlert struct {
	Type     string `json:"type"`
	Severity string `json:"severity"`
	Summary  string `json:"summary"`
}

type Thresholds struct {
	ErrorRatePercent     float64 `json:"error_rate_percent"`
	CostPerHourUSD       float64 `json:"cost_per_hour_usd"`
	FeedErrorRatePercent float64 `json:"feed_error_rate_percent"`
	InterventionCount    int     `json:"intervention_count"`
}

func DefaultThresholds() Thresholds {
	return Thresholds{
		ErrorRatePercent:     5.0,
		CostPerHourUSD:       10.0,
		FeedErrorRatePercent: 20.0,
		InterventionCount:    5,
	}
}

func (th Thresholds) Evaluate(agent audit.AgentSummary) []ThresholdAlert {
	var alerts []ThresholdAlert

	if agent.Requests > 0 {
		errorRate := float64(agent.Errors) / float64(agent.Requests) * 100
		if errorRate > th.ErrorRatePercent {
			alerts = append(alerts, ThresholdAlert{
				Type:     "error_rate",
				Severity: "warning",
				Summary:  fmt.Sprintf("%s error rate %.1f%% exceeds %.1f%% threshold (%d errors / %d requests)", agent.ClawID, errorRate, th.ErrorRatePercent, agent.Errors, agent.Requests),
			})
		}
	}

	if th.CostPerHourUSD > 0 && agent.CostUSD > th.CostPerHourUSD {
		alerts = append(alerts, ThresholdAlert{
			Type:     "cost",
			Severity: "warning",
			Summary:  fmt.Sprintf("%s cost $%.2f exceeds $%.2f threshold in window", agent.ClawID, agent.CostUSD, th.CostPerHourUSD),
		})
	}

	if agent.FeedFetches > 0 {
		feedErrorRate := float64(agent.FeedErrors) / float64(agent.FeedFetches) * 100
		if feedErrorRate > th.FeedErrorRatePercent {
			alerts = append(alerts, ThresholdAlert{
				Type:     "feed_error_rate",
				Severity: "warning",
				Summary:  fmt.Sprintf("%s feed error rate %.1f%% exceeds %.1f%% threshold (%d errors / %d fetches)", agent.ClawID, feedErrorRate, th.FeedErrorRatePercent, agent.FeedErrors, agent.FeedFetches),
			})
		}
	}

	if th.InterventionCount > 0 && agent.Interventions >= th.InterventionCount {
		alerts = append(alerts, ThresholdAlert{
			Type:     "interventions",
			Severity: "warning",
			Summary:  fmt.Sprintf("%s recorded %d intervention(s), threshold is %d", agent.ClawID, agent.Interventions, th.InterventionCount),
		})
	}

	return alerts
}
```

**Step 4: Run tests**

Run: `cd /Users/wojtek/dev/ai/clawdapus/.worktrees/master-claw && go test ./internal/clawapi/ -v`
Expected: ALL PASS

**Step 5: Commit**

```bash
cd /Users/wojtek/dev/ai/clawdapus/.worktrees/master-claw
git add internal/clawapi/thresholds.go internal/clawapi/thresholds_test.go
git commit -m "feat(clawapi): configurable alert thresholds for error rate, cost, feeds, interventions"
```

---

### Task 4: Wire thresholds into claw-api alert handler

Replace the binary `errors > 0` logic in `collectAlerts` with threshold evaluation.

**Files:**
- Modify: `cmd/claw-api/handler.go:35-41` (apiHandler struct)
- Modify: `cmd/claw-api/handler.go:343-383` (collectAlerts)
- Modify: `cmd/claw-api/main.go` (load thresholds)
- Test: `cmd/claw-api/handler_test.go` (if it exists; otherwise add inline verification)

**Step 1: Add thresholds field to apiHandler**

In `cmd/claw-api/handler.go`, add to `apiHandler` struct:

```go
	thresholds clawapi.Thresholds
```

Update `newHandler` signature to accept thresholds:

```go
func newHandler(manifest *manifestpkg.PodManifest, store *clawapi.Store, docker *client.Client, auditWriter io.Writer, thresholds clawapi.Thresholds) http.Handler {
```

And set `thresholds: thresholds` in the return.

**Step 2: Replace collectAlerts metric logic**

Replace the metric alert generation in `collectAlerts` (lines 363-381) with:

```go
	for _, agent := range summary.Agents {
		if !principal.AllowsClawID(h.manifest.PodName, agent.ClawID) {
			continue
		}
		for _, ta := range h.thresholds.Evaluate(agent) {
			alerts = append(alerts, alert{
				Severity: ta.Severity,
				ClawID:   agent.ClawID,
				Summary:  ta.Summary,
			})
		}
	}
```

**Step 3: Add env-based threshold overrides and wire into main.go**

Add to `internal/clawapi/thresholds.go`:

```go
func ThresholdsFromEnv() Thresholds {
	th := DefaultThresholds()
	if v := os.Getenv("CLAW_ALERT_ERROR_RATE_PERCENT"); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			th.ErrorRatePercent = f
		}
	}
	if v := os.Getenv("CLAW_ALERT_COST_PER_HOUR_USD"); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			th.CostPerHourUSD = f
		}
	}
	if v := os.Getenv("CLAW_ALERT_FEED_ERROR_RATE_PERCENT"); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			th.FeedErrorRatePercent = f
		}
	}
	if v := os.Getenv("CLAW_ALERT_INTERVENTION_COUNT"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			th.InterventionCount = n
		}
	}
	return th
}
```

Add imports `"os"` and `"strconv"` to thresholds.go.

In `cmd/claw-api/main.go`, where `newHandler` is called, pass `clawapi.ThresholdsFromEnv()`.

Operators configure thresholds via env vars on the `claw-api` container. The pod parser's `Service.Compose` passthrough preserves `environment:` blocks on non-claw services, so operators can set these in the pod YAML on a manual `claw-api` service, or via `.env` when using auto-injected `claw-api`.

**Step 4: Run tests**

Run: `cd /Users/wojtek/dev/ai/clawdapus/.worktrees/master-claw && go test ./cmd/claw-api/ -v && go test ./... 2>&1 | tail -5`
Expected: ALL PASS

**Step 5: Commit**

```bash
cd /Users/wojtek/dev/ai/clawdapus/.worktrees/master-claw
git add cmd/claw-api/handler.go cmd/claw-api/main.go
git commit -m "feat(claw-api): wire threshold-based alerts into /fleet/alerts endpoint"
```

---

### Task 5: Update ADR-014 status

**Files:**
- Modify: `docs/decisions/014-telemetry-normalization-and-audit.md:1-6`

**Step 1: Update ADR status**

Change line 3 from:
```
**Status:** Proposed
```
to:
```
**Status:** Accepted
```

Add after the Depends-on line:
```
**Implementation:** Milestones 1-3 complete. `claw audit` CLI, normalization pipeline, and `claw-api` read operations all implemented. Alert thresholds configurable via `Thresholds` type.
```

**Step 2: Commit**

```bash
cd /Users/wojtek/dev/ai/clawdapus/.worktrees/master-claw
git add docs/decisions/014-telemetry-normalization-and-audit.md
git commit -m "docs: update ADR-014 status to Accepted — telemetry pipeline implemented"
```

---

## Phase 2: Master Claw Standalone Example

### Task 6: Create `examples/master-claw/` scaffold

A minimal pod: two workers + one governor. All OpenClaw for simplicity.

**Files:**
- Create: `examples/master-claw/claw-pod.yml`
- Create: `examples/master-claw/Clawfile`
- Create: `examples/master-claw/agents/GOVERNOR.md`
- Create: `examples/master-claw/agents/WORKER.md`
- Create: `examples/master-claw/README.md`

**Step 1: Create Clawfile**

Create `examples/master-claw/Clawfile`:

```dockerfile
FROM ghcr.io/mostlydev/openclaw:latest

CLAW_TYPE openclaw

MODEL primary openrouter/anthropic/claude-sonnet-4

HANDLE discord
```

Note: This is a Clawfile, not a raw Dockerfile. `claw up` transpiles it via
`internal/clawfile/` into a Dockerfile with proper `LABEL` directives. The
`CLAW_TYPE` directive is required (see `internal/clawfile/parser_test.go`).

**Step 2: Create governor contract**

Create `examples/master-claw/agents/GOVERNOR.md`:

```markdown
# Governor

You are the fleet governor for this pod.

## Responsibilities
- Monitor agent health, cost, and error rates via the alerts feed injected into your context
- When the fleet is nominal, report briefly and stop
- When alerts appear, investigate using claw-api tools before making claims

## Tools Available
Use the `claw-api` service surface:
- `GET /fleet/status` — health and uptime for all services
- `GET /fleet/metrics?claw_id=<id>&since=<window>` — detailed telemetry for one agent
- `GET /fleet/logs?service=<name>&lines=<n>` — recent logs for a service
- `GET /fleet/alerts` — current anomalies (also injected as a feed)

## Periodic Review
Every 5 minutes you receive a review prompt. Check the alerts in your context.
If nominal, say so. If anomalies appear, investigate and report findings.

## Style
- Short, operator-style notes
- Distinguish symptoms from root causes
- When evidence is incomplete, say what you checked and what is still missing
```

**Step 3: Create worker contract**

Create `examples/master-claw/agents/WORKER.md`:

```markdown
# Worker

You are a general-purpose assistant in a managed pod.
Respond to user messages on Discord. Keep responses concise and helpful.
```

**Step 4: Create pod YAML**

Create `examples/master-claw/claw-pod.yml`:

```yaml
x-claw:
  pod: master-claw-demo
  master: governor

# Pod-level cllama and cllama-env are NOT parsed — the pod parser only accepts
# pod, master, and handles-defaults at pod level. Provider keys must be declared
# at service level using YAML anchors to stay DRY.
x-master-claw-demo:
  cllama_agent: &cllama_agent
    cllama: [passthrough]
  proxy_env: &proxy_env
    OPENROUTER_API_KEY: "${OPENROUTER_API_KEY}"

services:
  worker-a:
    image: master-claw-demo:latest
    build:
      context: .
      dockerfile: Clawfile
    x-claw:
      <<: *cllama_agent
      agent: ./agents/WORKER.md
      cllama-env: *proxy_env
      handles:
        discord:
          id: "${WORKER_A_DISCORD_ID}"
          username: "worker-a"
    environment:
      DISCORD_BOT_TOKEN: "${WORKER_A_BOT_TOKEN}"

  worker-b:
    image: master-claw-demo:latest
    build:
      context: .
      dockerfile: Clawfile
    x-claw:
      <<: *cllama_agent
      agent: ./agents/WORKER.md
      cllama-env: *proxy_env
      handles:
        discord:
          id: "${WORKER_B_DISCORD_ID}"
          username: "worker-b"
    environment:
      DISCORD_BOT_TOKEN: "${WORKER_B_BOT_TOKEN}"

  governor:
    image: master-claw-demo:latest
    build:
      context: .
      dockerfile: Clawfile
    x-claw:
      <<: *cllama_agent
      agent: ./agents/GOVERNOR.md
      cllama-env: *proxy_env
      feeds:
        - source: claw-api
          path: /fleet/alerts
          ttl: 30
      surfaces:
        - "service://claw-api"
      handles:
        discord:
          id: "${GOVERNOR_DISCORD_ID}"
          username: "governor"
      invoke:
        - schedule: "*/5 * * * *"
          name: "Fleet review"
          message: "Run your periodic fleet review. Check the alerts in your context and report."
    environment:
      DISCORD_BOT_TOKEN: "${GOVERNOR_BOT_TOKEN}"
```

**Step 5: Create README**

Create `examples/master-claw/README.md`:

```markdown
# Master Claw Example

Demonstrates fleet governance using a Master Claw (ADR-012).

## What's in the pod

- **worker-a** and **worker-b**: Simple OpenClaw agents on Discord
- **governor**: Fleet governor that monitors worker health via `claw-api`
- **cllama**: Shared proxy (auto-injected) routing all LLM calls
- **claw-api**: Governance API (auto-injected when `master:` is set)

## How it works

1. `x-claw.master: governor` tells `claw up` to auto-inject `claw-api`
2. Governor gets a bearer token for `claw-api` via `CLAW_API_URL`
3. `/fleet/alerts` feed is injected into governor's context every turn
4. Governor has an INVOKE schedule that fires every 5 minutes for periodic review

## Running

```bash
cp .env.example .env
# Fill in Discord bot tokens and OPENROUTER_API_KEY
claw up -d
claw ps
claw logs governor
claw audit
```
```

**Step 6: Create .env.example**

Create `examples/master-claw/.env.example`:

```bash
# Provider API key (used by cllama proxy, not by agents directly)
OPENROUTER_API_KEY=

# Discord bot tokens (one per agent — each needs a unique Discord application)
GOVERNOR_BOT_TOKEN=
GOVERNOR_DISCORD_ID=
WORKER_A_BOT_TOKEN=
WORKER_A_DISCORD_ID=
WORKER_B_BOT_TOKEN=
WORKER_B_DISCORD_ID=
```

**Step 7: Commit**

```bash
cd /Users/wojtek/dev/ai/clawdapus/.worktrees/master-claw
git add examples/master-claw/
git commit -m "feat: add master-claw example pod with fleet governor pattern"
```

---

### Task 7: Verify master-claw example parses cleanly

The pod should parse and emit compose without errors, even without Docker running.

**Files:**
- Test: `cmd/claw/` (use existing compose_up test patterns)

**Step 1: Write a test that parses the example pod**

Add to an appropriate test file (or create `cmd/claw/master_claw_example_test.go`):

```go
package main

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/mostlydev/clawdapus/internal/pod"
)

func TestMasterClawExampleParsesCleanly(t *testing.T) {
	exampleDir := filepath.Join(testRepoRoot(t), "examples", "master-claw")
	podPath := filepath.Join(exampleDir, "claw-pod.yml")

	p, err := pod.ParseFile(podPath)
	if err != nil {
		t.Fatalf("failed to parse master-claw pod: %v", err)
	}
	if p.Master != "governor" {
		t.Fatalf("expected master=governor, got %q", p.Master)
	}
	if _, ok := p.Services["governor"]; !ok {
		t.Fatal("expected governor service in parsed pod")
	}
	svc := p.Services["governor"]
	if svc.Claw == nil {
		t.Fatal("expected governor to be a claw-managed service")
	}
	if len(svc.Claw.Feeds) == 0 {
		t.Fatal("expected governor to have feeds declared")
	}

	// Verify cllama-env is set at service level (not pod level)
	if len(svc.Claw.CllamaEnv) == 0 {
		t.Fatal("expected governor to have cllama-env (provider keys)")
	}

	// Verify all 3 services have cllama enabled
	for _, name := range []string{"worker-a", "worker-b", "governor"} {
		s := p.Services[name]
		if s == nil || s.Claw == nil {
			t.Fatalf("expected %s to be a claw-managed service", name)
		}
		if len(s.Claw.Cllama) == 0 {
			t.Fatalf("expected %s to have cllama enabled", name)
		}
	}

	// Verify compose emission succeeds (catches Clawfile/surface/config errors)
	// Use empty results map — EmitCompose validates structure without materialization
	compose, err := pod.EmitCompose(p, map[string]*driver.MaterializeResult{})
	if err != nil {
		t.Fatalf("EmitCompose failed: %v", err)
	}
	if !strings.Contains(compose, "claw-api") {
		t.Fatal("expected auto-injected claw-api service in compose output")
	}
}
```

Note: Check how `testRepoRoot` is implemented in existing tests — it may be a helper
that walks up to find `go.mod`. Also add the `driver` import and any blank driver imports
needed for the test file. The test must NOT skip — the example file is committed alongside it.

**Step 2: Run test**

Run: `cd /Users/wojtek/dev/ai/clawdapus/.worktrees/master-claw && go test ./cmd/claw/ -run TestMasterClawExampleParsesCleanly -v`
Expected: PASS

**Step 3: Commit**

```bash
cd /Users/wojtek/dev/ai/clawdapus/.worktrees/master-claw
git add cmd/claw/master_claw_example_test.go
git commit -m "test: verify master-claw example pod parses with master, feeds, and surfaces"
```

---

### Task 8: Update ADR-012 status

**Files:**
- Modify: `docs/decisions/012-master-claw-fleet-governance.md:1-6`

**Step 1: Update status**

Change `**Status:** Proposed` to `**Status:** Accepted`.

Add after Depends-on:
```
**Implementation:** Milestones 1-2 complete (telemetry + claw-api read plane). Milestone 3 (Master Claw example) in `examples/master-claw/` and `examples/trading-desk/`. Write plane (Milestone 4) and hub-and-spoke (Milestone 5) deferred.
```

**Step 2: Commit**

```bash
cd /Users/wojtek/dev/ai/clawdapus/.worktrees/master-claw
git add docs/decisions/012-master-claw-fleet-governance.md
git commit -m "docs: update ADR-012 status to Accepted — read plane and example implemented"
```

---

## Phase 3: Tiverton-House Integration

> These tasks are performed on the remote tiverton-house repo, not in the clawdapus worktree.

### Task 9: Add Sentinel service to tiverton-house pod

**Context:** Tiverton-house already has `weston`, `coordinator`, `dundas` with cllama + feeds. Add `sentinel` as the fleet governor.

**Step 1: Add sentinel to claw-pod.yml**

Add `master: sentinel` to the pod-level `x-claw` block.

Add a new service block for sentinel:

```yaml
  sentinel:
    image: tiverton-openclaw:latest
    build:
      context: .
      dockerfile: Clawfile
    x-claw:
      <<: *cllama_agent
      agent: ./agents/SENTINEL.md
      cllama-env: *cllama_env
      feeds:
        - source: claw-api
          path: /fleet/alerts
          ttl: 30
      surfaces:
        - "service://claw-api"
      handles:
        discord:
          id: "${SENTINEL_DISCORD_ID}"
          username: "sentinel"
      invoke:
        - schedule: "*/10 * * * *"
          name: "Fleet review"
          message: "Review fleet alerts. Check trading agent health, feed freshness, and cost. Report anomalies."
    environment:
      DISCORD_BOT_TOKEN: "${SENTINEL_DISCORD_BOT_TOKEN}"
```

**Step 2: Write sentinel contract**

Create `agents/SENTINEL.md` in tiverton-house:

```markdown
# Sentinel — Fleet Governor

You are Sentinel, the fleet governor for the Tiverton trading desk.

## Your Fleet
- **weston**: Lead trader (momentum, tech stocks)
- **coordinator**: Orchestration and compliance
- **dundas**: Research and analysis

## Responsibilities
- Monitor trader health, cost, and error rates
- Watch for stale market context feeds — a failing feed means a trader is flying blind
- Report anomalies to your Discord channel
- When the fleet is nominal, keep it brief

## Tools
Use `claw-api` (URL in your environment as CLAW_API_URL):
- `GET /fleet/status` — service health
- `GET /fleet/metrics?claw_id=<id>&since=<window>` — per-agent telemetry
- `GET /fleet/logs?service=<name>&lines=<n>` — recent logs
- `GET /fleet/alerts` — anomalies (also in your context feed)

## Trading-Specific Alerts
- Feed errors on market-context feeds are CRITICAL — trader is blind
- High error rate on weston = potential failed trades
- Cost spikes may indicate runaway inference loops

## Style
- Terse, operator notes
- Facts first, interpretation second
- If unsure, say what you checked and what's missing
```

**Step 3: Add bot token to .env**

Add to `.env`:
```
SENTINEL_DISCORD_BOT_TOKEN=<bot token from Discord developer portal>
SENTINEL_DISCORD_ID=<bot application ID from Discord developer portal>
```

**Step 4: Deploy**

```bash
export $(grep -v "^#" .env | xargs)
claw up -d
claw ps
claw logs sentinel --tail 20
```

**Step 5: Verify**

- `claw ps` shows sentinel running
- `claw audit` shows sentinel in telemetry
- `claw logs sentinel` shows periodic fleet reviews
- Sentinel's Discord channel shows governance notes

---

### Task 10: Close resolved GitHub issues

Review open issues #72–#84 and close any that are now implemented:

- **#76** (End-to-end feed injection example): Close if feed injection is working in trading-desk and tiverton
- **#74** (claw-api structured audit logging): Close — `logDecision` already logs all auth decisions
- **#72** (claw-api read endpoint handlers): Close — all 4 read endpoints implemented
- **#73** (claw-api read-path scope filtering): Close — scope filtering implemented in `authorize()`
- **#75** (Master Claw example pod): Close — `examples/master-claw/` and `examples/trading-desk/octopus`
- **#77** (Anomaly threshold configuration): Close — `Thresholds` type with configurable defaults

Review remaining issues and update descriptions if scope has changed.

```bash
gh issue close 72 -c "Implemented: all 4 read endpoints in cmd/claw-api/handler.go"
gh issue close 73 -c "Implemented: scope filtering via Principal.AllowsService/AllowsClawID"
gh issue close 74 -c "Implemented: logDecision logs all auth decisions as structured JSON"
gh issue close 75 -c "Implemented: examples/master-claw/ and examples/trading-desk/ both have master claw"
gh issue close 76 -c "Implemented: feed injection working in tiverton-house production"
gh issue close 77 -c "Implemented: clawapi.Thresholds with configurable error_rate, cost, feed_error_rate, intervention_count"
```

---

## Verification Checklist

After all tasks:

- [ ] `go test ./...` passes in the worktree (all 25 packages)
- [ ] `go vet ./...` clean
- [ ] `claw audit` shows `feed_fetch` events in output
- [ ] `/fleet/alerts` returns threshold-based alerts (not binary)
- [ ] `examples/master-claw/claw-pod.yml` parses cleanly
- [ ] ADR-012 status: Accepted
- [ ] ADR-014 status: Accepted
- [ ] Tiverton sentinel running and posting fleet reviews
- [ ] Relevant GitHub issues closed
