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
