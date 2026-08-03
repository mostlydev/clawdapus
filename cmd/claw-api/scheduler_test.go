package main

import (
	"bytes"
	"context"
	"errors"
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

func TestSchedulerDispatchesUnrelatedTargetsConcurrently(t *testing.T) {
	now := time.Date(2026, time.July, 2, 14, 45, 0, 0, time.UTC)
	scheduler, state := newDueTestScheduler(t, now,
		testScheduleEntry{id: "analyst-open", target: "analyst"},
		testScheduleEntry{id: "trader-open", target: "trader"},
	)
	started := make(chan string, 2)
	release := make(chan struct{})
	scheduler.dispatchFn = func(_ context.Context, entry *scheduledInvocation, _ time.Time, _ dispatchOptions) dispatchResult {
		scheduler.logf("test dispatch %s", entry.manifest.ID)
		started <- entry.manifest.ID
		<-release
		return dispatchResult{status: "fired", attempted: true, fired: true}
	}
	scheduler.log = &bytes.Buffer{}

	scheduler.tick(context.Background(), now)
	first := awaitDispatchStart(t, started)
	second := awaitDispatchStart(t, started)
	if first == second {
		t.Fatalf("expected two different invocations to start, got %q twice", first)
	}
	close(release)
	waitForInvocationIdle(t, scheduler, "analyst-open")
	waitForInvocationIdle(t, scheduler, "trader-open")

	for _, id := range []string{"analyst-open", "trader-open"} {
		invocation := state.Snapshot().Invocations[id]
		if invocation.LastStatus != "fired" || invocation.LastFiredAt == nil || !invocation.LastFiredAt.Equal(now) {
			t.Fatalf("expected %s to persist fired state at %s, got %+v", id, now, invocation)
		}
		wantNext := now.Add(time.Minute)
		if invocation.NextFireAt == nil || !invocation.NextFireAt.Equal(wantNext) {
			t.Fatalf("expected %s next fire at %s, got %+v", id, wantNext, invocation.NextFireAt)
		}
	}
}

func TestSchedulerSerializesInvocationsForSameTarget(t *testing.T) {
	now := time.Date(2026, time.July, 2, 14, 45, 0, 0, time.UTC)
	scheduler, state := newDueTestScheduler(t, now,
		testScheduleEntry{id: "analyst-open", target: "analyst"},
		testScheduleEntry{id: "analyst-risk", target: "analyst"},
	)
	started := make(chan string, 2)
	release := make(chan struct{})
	scheduler.dispatchFn = func(_ context.Context, entry *scheduledInvocation, _ time.Time, _ dispatchOptions) dispatchResult {
		started <- entry.manifest.ID
		<-release
		return dispatchResult{status: "fired", attempted: true, fired: true}
	}

	scheduler.tick(context.Background(), now)
	if first := awaitDispatchStart(t, started); first != "analyst-open" {
		t.Fatalf("expected manifest-first invocation to start first, got %s", first)
	}
	assertNoDispatchStart(t, started)
	close(release)
	if second := awaitDispatchStart(t, started); second != "analyst-risk" {
		t.Fatalf("expected second manifest invocation after release, got %s", second)
	}
	firstState := stateForInvocation(t, state, "analyst-open")
	if firstState.LastStatus != "fired" || firstState.LastFiredAt == nil {
		t.Fatalf("expected first invocation persisted before second started, got %+v", firstState)
	}
	waitForInvocationIdle(t, scheduler, "analyst-open")
	waitForInvocationIdle(t, scheduler, "analyst-risk")
}

func TestSchedulerPreservesSameTargetOrderAcrossTicksAndManualFire(t *testing.T) {
	now := time.Date(2026, time.July, 2, 14, 45, 0, 0, time.UTC)
	scheduler, state := newDueTestScheduler(t, now,
		testScheduleEntry{id: "holder", target: "analyst"},
		testScheduleEntry{id: "batch-first", target: "analyst"},
		testScheduleEntry{id: "batch-second", target: "analyst"},
		testScheduleEntry{id: "later-tick", target: "analyst"},
		testScheduleEntry{id: "manual", target: "analyst"},
	)
	setTestNextFire(t, scheduler, state, "holder", now.Add(time.Hour))
	setTestNextFire(t, scheduler, state, "batch-first", now)
	setTestNextFire(t, scheduler, state, "batch-second", now)
	setTestNextFire(t, scheduler, state, "later-tick", now.Add(time.Minute))
	setTestNextFire(t, scheduler, state, "manual", now.Add(time.Hour))

	started := make(chan string, 5)
	releaseHolder := make(chan struct{})
	scheduler.dispatchFn = func(_ context.Context, entry *scheduledInvocation, _ time.Time, _ dispatchOptions) dispatchResult {
		started <- entry.manifest.ID
		if entry.manifest.ID == "holder" {
			<-releaseHolder
		}
		return dispatchResult{status: "fired", attempted: true, fired: true}
	}
	holderDone := make(chan error, 1)
	go func() {
		_, err := scheduler.FireNow(context.Background(), "holder", false, false)
		holderDone <- err
	}()
	if first := awaitDispatchStart(t, started); first != "holder" {
		t.Fatalf("expected holder to acquire target first, got %s", first)
	}

	scheduler.tick(context.Background(), now)
	scheduler.tick(context.Background(), now.Add(time.Minute))
	manualDone := make(chan error, 1)
	go func() {
		_, err := scheduler.FireNow(context.Background(), "manual", false, false)
		manualDone <- err
	}()
	waitForInvocationInFlight(t, scheduler, "manual")
	close(releaseHolder)

	for _, want := range []string{"batch-first", "batch-second", "later-tick", "manual"} {
		if got := awaitDispatchStart(t, started); got != want {
			t.Fatalf("expected dispatch order %v; wanted %s, got %s", []string{"batch-first", "batch-second", "later-tick", "manual"}, want, got)
		}
	}
	if err := <-holderDone; err != nil {
		t.Fatalf("holder manual fire failed: %v", err)
	}
	if err := <-manualDone; err != nil {
		t.Fatalf("queued manual fire failed: %v", err)
	}
	for _, id := range []string{"batch-first", "batch-second", "later-tick", "manual"} {
		waitForInvocationIdle(t, scheduler, id)
	}
}

func TestTargetQueueCancellationRemovesMiddleReservation(t *testing.T) {
	queue := &targetQueue{}
	holder := queue.reserve()
	canceled := queue.reserve()
	successor := queue.reserve()
	if !holder.acquire(context.Background()) {
		t.Fatal("holder did not acquire target queue")
	}
	canceledCtx, cancel := context.WithCancel(context.Background())
	cancel()
	if canceled.acquire(canceledCtx) {
		t.Fatal("canceled middle reservation acquired target queue")
	}
	holder.release()
	acquireCtx, acquireCancel := context.WithTimeout(context.Background(), time.Second)
	defer acquireCancel()
	if !successor.acquire(acquireCtx) {
		t.Fatal("successor did not acquire after middle reservation cancellation")
	}
	successor.release()
}

func TestSchedulerDoesNotOverlapSameInvocation(t *testing.T) {
	now := time.Date(2026, time.July, 2, 14, 45, 0, 0, time.UTC)
	scheduler, state := newDueTestScheduler(t, now, testScheduleEntry{id: "analyst-open", target: "analyst"})
	started := make(chan string, 2)
	release := make(chan struct{})
	scheduler.dispatchFn = func(_ context.Context, entry *scheduledInvocation, _ time.Time, _ dispatchOptions) dispatchResult {
		started <- entry.manifest.ID
		<-release
		return dispatchResult{status: "fired", attempted: true, fired: true}
	}

	scheduler.tick(context.Background(), now)
	awaitDispatchStart(t, started)
	if _, err := scheduler.state.UpdateInvocation("analyst-open", func(invocation *schedulepkg.InvocationState) error {
		invocation.SkipNext = true
		return nil
	}); err != nil {
		close(release)
		t.Fatalf("arm skip-next: %v", err)
	}
	scheduler.tick(context.Background(), now.Add(time.Minute))
	assertNoDispatchStart(t, started)
	wantNext := now.Add(2 * time.Minute)
	invocation := stateForInvocation(t, state, "analyst-open")
	if invocation.NextFireAt == nil || !invocation.NextFireAt.Equal(wantNext) {
		t.Fatalf("expected overlapping slot to advance next fire to %s, got %+v", wantNext, invocation.NextFireAt)
	}
	close(release)
	waitForInvocationIdle(t, scheduler, "analyst-open")
	invocation = stateForInvocation(t, state, "analyst-open")
	if invocation.NextFireAt == nil || !invocation.NextFireAt.Equal(wantNext) {
		t.Fatalf("completion regressed next fire; expected %s, got %+v", wantNext, invocation.NextFireAt)
	}
	if !invocation.SkipNext {
		t.Fatal("overlap suppression consumed skip-next before a dispatchable slot")
	}
}

func TestSchedulerRecordsSuppressedSlotsInState(t *testing.T) {
	now := time.Date(2026, time.July, 2, 14, 45, 0, 0, time.UTC)
	scheduler, state := newDueTestScheduler(t, now, testScheduleEntry{id: "analyst-open", target: "analyst"})
	started := make(chan string, 2)
	release := make(chan struct{})
	scheduler.dispatchFn = func(_ context.Context, entry *scheduledInvocation, _ time.Time, _ dispatchOptions) dispatchResult {
		started <- entry.manifest.ID
		<-release
		return dispatchResult{status: "fired", attempted: true, fired: true}
	}

	scheduler.tick(context.Background(), now)
	awaitDispatchStart(t, started)

	// Two further slots come due while the first wake is still running. A wake
	// budget longer than the schedule cadence makes this ordinary, not exotic.
	scheduler.tick(context.Background(), now.Add(time.Minute))
	scheduler.tick(context.Background(), now.Add(2*time.Minute))

	invocation := stateForInvocation(t, state, "analyst-open")
	if invocation.SuppressedSlots != 2 {
		t.Fatalf("expected 2 suppressed slots recorded, got %d", invocation.SuppressedSlots)
	}
	wantSuppressed := now.Add(2 * time.Minute)
	if invocation.LastSuppressedAt == nil || !invocation.LastSuppressedAt.Equal(wantSuppressed) {
		t.Fatalf("expected last suppressed slot %s, got %+v", wantSuppressed, invocation.LastSuppressedAt)
	}

	close(release)
	waitForInvocationIdle(t, scheduler, "analyst-open")

	// Completing the in-flight wake must not erase the audit record of the
	// slots that were dropped while it ran.
	invocation = stateForInvocation(t, state, "analyst-open")
	if invocation.LastStatus != "fired" {
		t.Fatalf("expected completed wake to persist fired, got %q", invocation.LastStatus)
	}
	if invocation.SuppressedSlots != 2 {
		t.Fatalf("completion erased suppressed-slot count, got %d", invocation.SuppressedSlots)
	}
	if invocation.LastSuppressedAt == nil || !invocation.LastSuppressedAt.Equal(wantSuppressed) {
		t.Fatalf("completion erased last suppressed slot, got %+v", invocation.LastSuppressedAt)
	}
}

func TestSchedulerLeavesSuppressedSlotsUntouchedWithoutOverlap(t *testing.T) {
	now := time.Date(2026, time.July, 2, 14, 45, 0, 0, time.UTC)
	scheduler, state := newDueTestScheduler(t, now, testScheduleEntry{id: "analyst-open", target: "analyst"})
	scheduler.dispatchFn = func(_ context.Context, _ *scheduledInvocation, _ time.Time, _ dispatchOptions) dispatchResult {
		return dispatchResult{status: "fired", attempted: true, fired: true}
	}

	scheduler.tick(context.Background(), now)
	waitForInvocationIdle(t, scheduler, "analyst-open")

	invocation := stateForInvocation(t, state, "analyst-open")
	if invocation.SuppressedSlots != 0 {
		t.Fatalf("expected no suppressed slots for a clean dispatch, got %d", invocation.SuppressedSlots)
	}
	if invocation.LastSuppressedAt != nil {
		t.Fatalf("expected no suppressed timestamp for a clean dispatch, got %+v", invocation.LastSuppressedAt)
	}
}

func TestSchedulerFireNowRejectsInvocationAlreadyInFlight(t *testing.T) {
	now := time.Date(2026, time.July, 2, 14, 45, 0, 0, time.UTC)
	scheduler, _ := newDueTestScheduler(t, now, testScheduleEntry{id: "analyst-open", target: "analyst"})
	started := make(chan string, 1)
	release := make(chan struct{})
	scheduler.dispatchFn = func(_ context.Context, entry *scheduledInvocation, _ time.Time, _ dispatchOptions) dispatchResult {
		started <- entry.manifest.ID
		<-release
		return dispatchResult{status: "fired", attempted: true, fired: true}
	}

	scheduler.tick(context.Background(), now)
	awaitDispatchStart(t, started)
	if _, err := scheduler.FireNow(context.Background(), "analyst-open", false, false); !errors.Is(err, errScheduleInvocationInFlight) {
		close(release)
		t.Fatalf("expected in-flight error, got %v", err)
	}
	close(release)
	waitForInvocationIdle(t, scheduler, "analyst-open")
}

func TestSchedulerFireNowRejectsConcurrentManualFire(t *testing.T) {
	now := time.Date(2026, time.July, 2, 14, 45, 0, 0, time.UTC)
	scheduler, state := newDueTestScheduler(t, now, testScheduleEntry{id: "analyst-open", target: "analyst"})
	started := make(chan string, 1)
	release := make(chan struct{})
	scheduler.dispatchFn = func(_ context.Context, entry *scheduledInvocation, _ time.Time, _ dispatchOptions) dispatchResult {
		started <- entry.manifest.ID
		<-release
		return dispatchResult{status: "manual-fire", attempted: true, fired: true}
	}
	firstDone := make(chan error, 1)
	go func() {
		_, err := scheduler.FireNow(context.Background(), "analyst-open", false, false)
		firstDone <- err
	}()
	awaitDispatchStart(t, started)

	if _, err := scheduler.FireNow(context.Background(), "analyst-open", false, false); !errors.Is(err, errScheduleInvocationInFlight) {
		close(release)
		t.Fatalf("expected concurrent manual fire conflict, got %v", err)
	}
	close(release)
	select {
	case err := <-firstDone:
		if err != nil {
			t.Fatalf("first manual fire failed: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for first manual fire")
	}
	invocation := stateForInvocation(t, state, "analyst-open")
	if invocation.NextFireAt == nil || !invocation.NextFireAt.Equal(now) {
		t.Fatalf("manual fire changed scheduled next fire; expected %s, got %+v", now, invocation.NextFireAt)
	}
}

func TestSchedulerFireNowCancellationWhileTargetBusyIsNeutral(t *testing.T) {
	now := time.Date(2026, time.July, 2, 14, 45, 0, 0, time.UTC)
	scheduler, state := newDueTestScheduler(t, now,
		testScheduleEntry{id: "analyst-open", target: "analyst"},
		testScheduleEntry{id: "analyst-risk", target: "analyst"},
	)
	riskEntry := scheduler.lookupEntry("analyst-risk")
	scheduler.mu.Lock()
	riskEntry.nextFireUTC = now.Add(time.Hour)
	scheduler.mu.Unlock()
	if _, err := state.UpdateInvocation("analyst-risk", func(invocation *schedulepkg.InvocationState) error {
		next := now.Add(time.Hour)
		invocation.NextFireAt = &next
		return nil
	}); err != nil {
		t.Fatalf("seed later fire: %v", err)
	}
	started := make(chan string, 1)
	release := make(chan struct{})
	scheduler.dispatchFn = func(_ context.Context, entry *scheduledInvocation, _ time.Time, _ dispatchOptions) dispatchResult {
		started <- entry.manifest.ID
		<-release
		return dispatchResult{status: "fired", attempted: true, fired: true}
	}

	scheduler.tick(context.Background(), now)
	awaitDispatchStart(t, started)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, err := scheduler.FireNow(ctx, "analyst-risk", false, false); !errors.Is(err, context.DeadlineExceeded) {
		close(release)
		t.Fatalf("expected deadline while waiting for busy target, got %v", err)
	}
	invocation := stateForInvocation(t, state, "analyst-risk")
	if invocation.LastAttemptedAt != nil || invocation.LastFiredAt != nil || invocation.ConsecutiveFailures != 0 || invocation.Degraded {
		close(release)
		t.Fatalf("canceled target wait mutated dispatch state: %+v", invocation)
	}
	entry := scheduler.lookupEntry("analyst-risk")
	scheduler.mu.RLock()
	inFlight := entry.inFlight
	scheduler.mu.RUnlock()
	if inFlight {
		close(release)
		t.Fatal("canceled target wait leaked invocation claim")
	}
	close(release)
	waitForInvocationIdle(t, scheduler, "analyst-open")
}

func TestSchedulerPersistenceFailureDoesNotLeakInvocationClaim(t *testing.T) {
	now := time.Date(2026, time.July, 2, 14, 45, 0, 0, time.UTC)
	scheduler, state := newDueTestScheduler(t, now, testScheduleEntry{id: "analyst-open", target: "analyst"})
	state.path = t.TempDir()
	started := make(chan string, 2)
	scheduler.dispatchFn = func(_ context.Context, entry *scheduledInvocation, _ time.Time, _ dispatchOptions) dispatchResult {
		started <- entry.manifest.ID
		return dispatchResult{status: "fired", attempted: true, fired: true}
	}

	scheduler.tick(context.Background(), now)
	awaitDispatchStart(t, started)
	waitForInvocationIdle(t, scheduler, "analyst-open")
	scheduler.tick(context.Background(), now.Add(time.Minute))
	awaitDispatchStart(t, started)
	waitForInvocationIdle(t, scheduler, "analyst-open")
}

func TestSchedulerRunShutdownCancelsLookupAndWaitDrains(t *testing.T) {
	now := time.Now().UTC().Add(-time.Minute)
	scheduler, state := newDueTestScheduler(t, now, testScheduleEntry{id: "analyst-open", target: "analyst"})
	started := make(chan struct{}, 1)
	ctx, cancel := context.WithCancel(context.Background())
	scheduler.lookupFn = func(ctx context.Context, _ string) (types.Container, error) {
		started <- struct{}{}
		<-ctx.Done()
		return types.Container{}, ctx.Err()
	}

	go scheduler.Run(ctx)
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for target lookup")
	}
	cancel()
	waitCtx, waitCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer waitCancel()
	if err := scheduler.Wait(waitCtx); err != nil {
		t.Fatalf("wait for scheduler shutdown: %v", err)
	}
	waitForInvocationIdle(t, scheduler, "analyst-open")
	invocation := stateForInvocation(t, state, "analyst-open")
	if invocation.LastAttemptedAt != nil || invocation.LastFiredAt != nil || invocation.ConsecutiveFailures != 0 || invocation.Degraded {
		t.Fatalf("shutdown cancellation counted as wake failure: %+v", invocation)
	}
}

func TestSchedulerManualCancellationDuringLookupIsNeutral(t *testing.T) {
	now := time.Now().UTC().Add(time.Hour)
	scheduler, state := newDueTestScheduler(t, now, testScheduleEntry{id: "analyst-open", target: "analyst"})
	started := make(chan struct{}, 1)
	scheduler.lookupFn = func(ctx context.Context, _ string) (types.Container, error) {
		started <- struct{}{}
		<-ctx.Done()
		return types.Container{}, ctx.Err()
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := scheduler.FireNow(ctx, "analyst-open", false, false)
		done <- err
	}()
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for manual target lookup")
	}
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("expected canceled manual fire, got %v", err)
	}
	waitForInvocationIdle(t, scheduler, "analyst-open")
	invocation := stateForInvocation(t, state, "analyst-open")
	if invocation.LastAttemptedAt != nil || invocation.LastFiredAt != nil || invocation.ConsecutiveFailures != 0 || invocation.Degraded {
		t.Fatalf("manual lookup cancellation mutated failure state: %+v", invocation)
	}
}

type testScheduleEntry struct {
	id     string
	target string
}

func newDueTestScheduler(t *testing.T, now time.Time, definitions ...testScheduleEntry) (*scheduler, *scheduleStateStore) {
	t.Helper()
	invocations := make([]schedulepkg.ManifestInvocation, 0, len(definitions))
	for _, definition := range definitions {
		invocations = append(invocations, schedulepkg.ManifestInvocation{
			ID:       definition.id,
			Service:  definition.target,
			AgentID:  definition.target,
			Schedule: "* * * * *",
			Timezone: "UTC",
			Name:     definition.id,
			Wake: schedulepkg.Wake{
				Adapter: "hermes-exec",
				Target:  definition.target,
				Command: []string{"hermes", "cron", "run", definition.id},
			},
		})
	}
	manifest := &schedulepkg.Manifest{Version: 1, Pod: "ops", Invocations: invocations}
	state := newTestScheduleStateStore(t, manifest)
	scheduler, err := newScheduler(manifest, nil, state, nil)
	if err != nil {
		t.Fatalf("newScheduler: %v", err)
	}
	scheduler.mu.Lock()
	for _, entry := range scheduler.entries {
		entry.nextFireUTC = now
	}
	scheduler.mu.Unlock()
	for _, definition := range definitions {
		if _, err := state.UpdateInvocation(definition.id, func(invocation *schedulepkg.InvocationState) error {
			next := now
			invocation.NextFireAt = &next
			return nil
		}); err != nil {
			t.Fatalf("seed next fire for %s: %v", definition.id, err)
		}
	}
	return scheduler, state
}

func awaitDispatchStart(t *testing.T, started <-chan string) string {
	t.Helper()
	select {
	case id := <-started:
		return id
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for dispatch to start")
		return ""
	}
}

func assertNoDispatchStart(t *testing.T, started <-chan string) {
	t.Helper()
	select {
	case id := <-started:
		t.Fatalf("unexpected concurrent dispatch for %s", id)
	case <-time.After(150 * time.Millisecond):
	}
}

func waitForInvocationIdle(t *testing.T, scheduler *scheduler, id string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		entry := scheduler.lookupEntry(id)
		scheduler.mu.RLock()
		inFlight := entry != nil && entry.inFlight
		scheduler.mu.RUnlock()
		if !inFlight {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s dispatch to finish", id)
}

func waitForInvocationInFlight(t *testing.T, scheduler *scheduler, id string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		entry := scheduler.lookupEntry(id)
		scheduler.mu.RLock()
		inFlight := entry != nil && entry.inFlight
		scheduler.mu.RUnlock()
		if inFlight {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s dispatch to be claimed", id)
}

func setTestNextFire(t *testing.T, scheduler *scheduler, state *scheduleStateStore, id string, next time.Time) {
	t.Helper()
	entry := scheduler.lookupEntry(id)
	if entry == nil {
		t.Fatalf("missing scheduler entry %s", id)
	}
	scheduler.mu.Lock()
	entry.nextFireUTC = next
	scheduler.mu.Unlock()
	if _, err := state.UpdateInvocation(id, func(invocation *schedulepkg.InvocationState) error {
		invocation.NextFireAt = &next
		return nil
	}); err != nil {
		t.Fatalf("set next fire for %s: %v", id, err)
	}
}

func stateForInvocation(t *testing.T, state *scheduleStateStore, id string) schedulepkg.InvocationState {
	t.Helper()
	invocation, ok := state.Invocation(id)
	if !ok {
		t.Fatalf("missing schedule state for %s", id)
	}
	return invocation
}
