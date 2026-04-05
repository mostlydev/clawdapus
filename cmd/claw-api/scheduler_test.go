package main

import (
	"testing"
	"time"
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
