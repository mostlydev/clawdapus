package main

import (
	"context"
	"testing"
	"time"

	"github.com/docker/docker/api/types"
	schedulepkg "github.com/mostlydev/clawdapus/internal/schedule"
)

func TestNextSchedulerDelayAlignsToMinuteBoundary(t *testing.T) {
	now := time.Date(2026, time.April, 5, 12, 34, 45, 0, time.UTC)
	if got := nextSchedulerDelay(now); got != 15*time.Second {
		t.Fatalf("expected 15s delay, got %v", got)
	}
}

func TestNextSchedulerDelayReturnsMinuteAtBoundary(t *testing.T) {
	now := time.Date(2026, time.April, 5, 12, 34, 0, 0, time.UTC)
	if got := nextSchedulerDelay(now); got != time.Minute {
		t.Fatalf("expected 1m delay at boundary, got %v", got)
	}
}

func TestShouldAttemptDegradedThrottlesToRoughlyTenPercent(t *testing.T) {
	base := time.Date(2026, time.April, 6, 9, 0, 0, 0, time.UTC)
	allowed := 0
	total := 1000
	for i := 0; i < total; i++ {
		if shouldAttemptDegraded("westin-open", base.Add(time.Duration(i)*time.Minute)) {
			allowed++
		}
	}
	if allowed < 70 || allowed > 130 {
		t.Fatalf("expected roughly 10%% allowed, got %d/%d", allowed, total)
	}
}

func TestWakeExecTimeoutUsesOpenClawBudget(t *testing.T) {
	if got := wakeExecTimeout("openclaw-exec"); got != openclawWakeExecTimeout {
		t.Fatalf("expected openclaw wake timeout %v, got %v", openclawWakeExecTimeout, got)
	}
	if got := wakeExecTimeout("hermes-exec"); got != defaultWakeExecTimeout {
		t.Fatalf("expected default wake timeout %v, got %v", defaultWakeExecTimeout, got)
	}
}

func TestDeferWakeForHealthStatusRequiresHealthyOpenClawTarget(t *testing.T) {
	t.Run("healthy openclaw proceeds", func(t *testing.T) {
		if detail, skip := deferWakeForHealthStatus("openclaw-exec", &types.ContainerState{
			Health: &types.Health{Status: "healthy"},
		}); skip || detail != "" {
			t.Fatalf("expected healthy openclaw target to proceed, got detail=%q skip=%v", detail, skip)
		}
	})

	t.Run("starting openclaw defers", func(t *testing.T) {
		detail, skip := deferWakeForHealthStatus("openclaw-exec", &types.ContainerState{
			Health: &types.Health{Status: "starting"},
		})
		if !skip || detail != "target-health-starting" {
			t.Fatalf("expected starting openclaw target to defer, got detail=%q skip=%v", detail, skip)
		}
	})

	t.Run("unhealthy openclaw defers", func(t *testing.T) {
		detail, skip := deferWakeForHealthStatus("openclaw-exec", &types.ContainerState{
			Health: &types.Health{Status: "unhealthy"},
		})
		if !skip || detail != "target-health-unhealthy" {
			t.Fatalf("expected unhealthy openclaw target to defer, got detail=%q skip=%v", detail, skip)
		}
	})

	t.Run("non-openclaw adapter ignores health", func(t *testing.T) {
		if detail, skip := deferWakeForHealthStatus("hermes-exec", &types.ContainerState{
			Health: &types.Health{Status: "starting"},
		}); skip || detail != "" {
			t.Fatalf("expected non-openclaw adapter to ignore health deferral, got detail=%q skip=%v", detail, skip)
		}
	})
}

func TestSchedulerDoesNotDispatchExhaustedSchedule(t *testing.T) {
	manifest := &schedulepkg.Manifest{
		Version: 1,
		Pod:     "ops",
		Invocations: []schedulepkg.ManifestInvocation{{
			ID:       "never",
			Service:  "westin",
			AgentID:  "westin",
			Schedule: "0 5 31 2 *",
			Timezone: "America/New_York",
			Name:     "Disabled job",
			Wake:     schedulepkg.Wake{Adapter: "hermes-exec", Target: "westin", Command: []string{"hermes", "cron", "run", "never"}},
		}},
	}
	state := newTestScheduleStateStore(t, manifest)
	scheduler, err := newScheduler(manifest, nil, state, nil)
	if err != nil {
		t.Fatalf("newScheduler: %v", err)
	}
	if len(scheduler.entries) != 1 {
		t.Fatalf("expected one scheduler entry, got %d", len(scheduler.entries))
	}
	if !scheduler.entries[0].nextFireUTC.IsZero() {
		t.Fatalf("expected exhausted schedule next fire to be zero, got %s", scheduler.entries[0].nextFireUTC)
	}

	scheduler.tick(context.Background(), time.Date(2026, time.July, 2, 14, 45, 0, 0, time.UTC))

	invocation := state.Snapshot().Invocations["never"]
	if invocation.LastStatus != "schedule-exhausted" {
		t.Fatalf("expected schedule-exhausted state, got %+v", invocation)
	}
	if invocation.LastAttemptedAt != nil || invocation.LastFiredAt != nil || invocation.ConsecutiveFailures != 0 {
		t.Fatalf("exhausted schedule should not dispatch, got %+v", invocation)
	}
	if invocation.NextFireAt != nil {
		t.Fatalf("exhausted schedule should not publish next_fire_at, got %+v", invocation.NextFireAt)
	}
}
