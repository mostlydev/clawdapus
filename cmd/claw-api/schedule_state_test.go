package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	schedulepkg "github.com/mostlydev/clawdapus/internal/schedule"
)

func TestScheduleStateStorePersistsAndDropsStaleInvocations(t *testing.T) {
	dir := t.TempDir()
	manifest := &schedulepkg.Manifest{
		Version: 1,
		Pod:     "ops",
		Invocations: []schedulepkg.ManifestInvocation{
			{ID: "westin-open", Service: "westin"},
			{ID: "analyst-open", Service: "analyst"},
		},
	}
	store, err := newScheduleStateStore(dir, manifest)
	if err != nil {
		t.Fatalf("newScheduleStateStore: %v", err)
	}
	if err := store.Update(func(file *schedulepkg.StateFile) {
		state := file.Invocations["westin-open"]
		state.Paused = true
		state.LastStatus = "skipped"
		file.Invocations["westin-open"] = state
	}); err != nil {
		t.Fatalf("Update: %v", err)
	}

	reloaded, err := newScheduleStateStore(dir, &schedulepkg.Manifest{
		Version: 1,
		Pod:     "ops",
		Invocations: []schedulepkg.ManifestInvocation{
			{ID: "westin-open", Service: "westin"},
		},
	})
	if err != nil {
		t.Fatalf("reload store: %v", err)
	}
	snapshot := reloaded.Snapshot()
	if snapshot.Pod != "ops" {
		t.Fatalf("expected pod ops, got %q", snapshot.Pod)
	}
	if len(snapshot.Invocations) != 1 {
		t.Fatalf("expected 1 invocation after manifest sync, got %d", len(snapshot.Invocations))
	}
	state, ok := snapshot.Invocations["westin-open"]
	if !ok {
		t.Fatal("expected westin-open state to persist")
	}
	if !state.Paused || state.LastStatus != "skipped" {
		t.Fatalf("expected persisted state, got %+v", state)
	}
	if _, ok := snapshot.Invocations["analyst-open"]; ok {
		t.Fatal("expected stale invocation to be removed")
	}
	if _, ok := reloaded.Invocation("westin-open"); !ok {
		t.Fatal("expected westin-open lookup to succeed")
	}
	if _, ok := reloaded.Invocation("analyst-open"); ok {
		t.Fatal("expected analyst-open lookup to fail after manifest sync")
	}
	if _, err := os.Stat(filepath.Join(dir, "schedule-state.json")); err != nil {
		t.Fatalf("expected persisted state file: %v", err)
	}
}

func TestScheduleStateStoreNormalizesZeroTimePointers(t *testing.T) {
	dir := t.TempDir()
	manifest := &schedulepkg.Manifest{
		Version: 1,
		Pod:     "ops",
		Invocations: []schedulepkg.ManifestInvocation{
			{ID: "never", Service: "westin"},
		},
	}
	store, err := newScheduleStateStore(dir, manifest)
	if err != nil {
		t.Fatalf("newScheduleStateStore: %v", err)
	}
	zero := time.Time{}
	if err := store.Update(func(file *schedulepkg.StateFile) {
		state := file.Invocations["never"]
		state.PausedUntil = &zero
		state.LastEvaluatedAt = &zero
		state.LastAttemptedAt = &zero
		state.LastFiredAt = &zero
		state.LastSkippedAt = &zero
		state.NextFireAt = &zero
		file.Invocations["never"] = state
	}); err != nil {
		t.Fatalf("Update: %v", err)
	}

	state := store.Snapshot().Invocations["never"]
	if state.PausedUntil != nil ||
		state.LastEvaluatedAt != nil ||
		state.LastAttemptedAt != nil ||
		state.LastFiredAt != nil ||
		state.LastSkippedAt != nil ||
		state.NextFireAt != nil {
		t.Fatalf("expected zero time pointers to be nil, got %+v", state)
	}
}
