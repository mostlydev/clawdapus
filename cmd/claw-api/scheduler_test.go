package main

import (
	"testing"
	"time"

	"github.com/docker/docker/api/types"
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
