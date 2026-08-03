package main

import (
	"context"
	"errors"
	"fmt"
	"hash/fnv"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/docker/docker/api/types"
	containerapi "github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/client"
	"github.com/mostlydev/clawdapus/internal/driver/shared"
	schedulepkg "github.com/mostlydev/clawdapus/internal/schedule"
	"github.com/robfig/cron/v3"
)

type scheduler struct {
	manifest *schedulepkg.Manifest
	docker   *client.Client
	log      io.Writer
	state    *scheduleStateStore
	now      func() time.Time

	mu           sync.RWMutex
	entries      []*scheduledInvocation
	targetQueues map[string]*targetQueue
	stopping     bool
	stopCtx      context.Context
	stopCancel   context.CancelFunc
	dispatchWG   sync.WaitGroup
	done         chan struct{}
	logMu        sync.Mutex
	dispatchFn   func(context.Context, *scheduledInvocation, time.Time, dispatchOptions) dispatchResult
	lookupFn     func(context.Context, string) (types.Container, error)
}

type scheduledInvocation struct {
	manifest    schedulepkg.ManifestInvocation
	location    *time.Location
	schedule    cron.Schedule
	nextFireUTC time.Time
	lastFireUTC time.Time
	lastStatus  string
	lastDetail  string
	inFlight    bool
}

type dispatchResult struct {
	status        string
	detail        string
	attempted     bool
	fired         bool
	skipped       bool
	failure       bool
	clearPause    bool
	clearSkipNext bool
	canceled      bool
}

type dispatchOptions struct {
	manual         bool
	bypassPause    bool
	bypassWhen     bool
	ignoreSkipNext bool
	ignoreDegraded bool
}

const defaultWakeExecTimeout = 30 * time.Second
const openclawWakeExecTimeout = schedulepkg.MaxWakeExecTimeout

var errScheduleInvocationInFlight = errors.New("schedule invocation already in flight")

func newScheduler(manifest *schedulepkg.Manifest, docker *client.Client, state *scheduleStateStore, log io.Writer) (*scheduler, error) {
	if manifest == nil || len(manifest.Invocations) == 0 {
		return nil, nil
	}
	parser := cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)
	entries := make([]*scheduledInvocation, 0, len(manifest.Invocations))
	now := time.Now().UTC()
	for _, inv := range manifest.Invocations {
		locationName := strings.TrimSpace(inv.Timezone)
		if locationName == "" {
			locationName = "UTC"
		}
		location, err := time.LoadLocation(locationName)
		if err != nil {
			return nil, fmt.Errorf("invocation %q: load timezone %q: %w", inv.ID, locationName, err)
		}
		compiled, err := parser.Parse(strings.TrimSpace(inv.Schedule))
		if err != nil {
			return nil, fmt.Errorf("invocation %q: parse schedule %q: %w", inv.ID, inv.Schedule, err)
		}
		entries = append(entries, &scheduledInvocation{
			manifest:    inv,
			location:    location,
			schedule:    compiled,
			nextFireUTC: nextScheduledFireUTC(compiled, now, location),
			lastStatus:  "scheduled",
		})
	}
	stopCtx, stopCancel := context.WithCancel(context.Background())
	s := &scheduler{
		manifest:     manifest,
		docker:       docker,
		log:          log,
		state:        state,
		targetQueues: make(map[string]*targetQueue),
		stopCtx:      stopCtx,
		stopCancel:   stopCancel,
		done:         make(chan struct{}),
		now: func() time.Time {
			return time.Now().UTC()
		},
		entries: entries,
	}
	if err := s.syncInitialState(); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *scheduler) Run(ctx context.Context) {
	if s == nil {
		return
	}
	defer close(s.done)
	s.logf("scheduler started with %d invocation(s)", len(s.entries))
	timer := time.NewTimer(nextSchedulerDelay(time.Now().UTC()))
	defer timer.Stop()

	s.tick(ctx, time.Now().UTC())

	for {
		select {
		case <-ctx.Done():
			s.stopScheduledDispatches()
			s.dispatchWG.Wait()
			s.logf("scheduler stopped")
			return
		case <-timer.C:
			s.tick(ctx, time.Now().UTC())
			timer.Reset(nextSchedulerDelay(time.Now().UTC()))
		}
	}
}

func (s *scheduler) tick(ctx context.Context, now time.Time) {
	s.mu.Lock()
	if s.stopping || ctx.Err() != nil {
		s.mu.Unlock()
		return
	}
	batches := make([]scheduledDispatchBatch, 0, len(s.entries))
	batchByTarget := make(map[string]int)
	nextFireUpdates := make([]nextFireUpdate, 0, len(s.entries))
	suppressed := make([]suppressedSlot, 0)
	for _, entry := range s.entries {
		if entry == nil || entry.nextFireUTC.IsZero() || now.Before(entry.nextFireUTC) {
			continue
		}
		fireAt := entry.nextFireUTC
		entry.nextFireUTC = nextScheduledFireUTC(entry.schedule, now, entry.location)
		update := nextFireUpdate{id: entry.manifest.ID, nextFire: entry.nextFireUTC}
		if entry.inFlight {
			// The previous wake for this invocation is still running, so this
			// slot is dropped rather than queued. Record it in schedule state:
			// the log line alone is invisible to clawdash and claw api schedule.
			slot := fireAt
			update.suppressedSlot = &slot
			nextFireUpdates = append(nextFireUpdates, update)
			suppressed = append(suppressed, suppressedSlot{id: entry.manifest.ID, slot: fireAt})
			continue
		}
		nextFireUpdates = append(nextFireUpdates, update)
		entry.inFlight = true
		target := strings.TrimSpace(entry.manifest.Wake.Target)
		batchIndex, ok := batchByTarget[target]
		if !ok {
			batchIndex = len(batches)
			batchByTarget[target] = batchIndex
			queue := s.targetQueueLocked(target)
			// Reserve synchronously so goroutine scheduling cannot reorder batches
			// submitted by later ticks or manual fires.
			batches = append(batches, scheduledDispatchBatch{
				reservation: queue.reserve(),
			})
		}
		batches[batchIndex].invocations = append(batches[batchIndex].invocations, scheduledDispatch{entry: entry, fireAt: fireAt})
	}
	s.dispatchWG.Add(len(batches))
	s.mu.Unlock()

	if err := s.persistNextFireUpdates(nextFireUpdates); err != nil {
		s.logf("persist next-fire updates failed: %v", err)
	}
	for _, entry := range suppressed {
		s.logf("schedule %s: overlap-suppressed slot %s", entry.id, entry.slot.UTC().Format(time.RFC3339))
	}
	for _, batch := range batches {
		go s.runScheduledBatch(ctx, batch)
	}
}

type scheduledDispatch struct {
	entry  *scheduledInvocation
	fireAt time.Time
}

type scheduledDispatchBatch struct {
	reservation *targetReservation
	invocations []scheduledDispatch
}

type targetQueue struct {
	mu      sync.Mutex
	active  bool
	waiters []*targetReservation
}

// A targetReservation is a FIFO ticket. One scheduled batch holds one ticket,
// which prevents later work from interleaving between its manifest-ordered wakes.

type targetReservation struct {
	queue *targetQueue
	ready chan struct{}
	state targetReservationState
}

type targetReservationState uint8

const (
	reservationWaiting targetReservationState = iota
	reservationGranted
	reservationReleased
	reservationCanceled
)

func (q *targetQueue) reserve() *targetReservation {
	q.mu.Lock()
	defer q.mu.Unlock()
	reservation := &targetReservation{
		queue: q,
		ready: make(chan struct{}),
		state: reservationWaiting,
	}
	if !q.active {
		q.active = true
		reservation.state = reservationGranted
		close(reservation.ready)
		return reservation
	}
	q.waiters = append(q.waiters, reservation)
	return reservation
}

func (r *targetReservation) acquire(ctx context.Context) bool {
	if r == nil {
		return false
	}
	if ctx == nil {
		r.cancel()
		return false
	}
	if ctx.Err() != nil {
		r.cancel()
		return false
	}
	select {
	case <-r.ready:
		r.queue.mu.Lock()
		granted := r.state == reservationGranted
		r.queue.mu.Unlock()
		if !granted || ctx.Err() != nil {
			r.cancel()
			return false
		}
		return true
	case <-ctx.Done():
		r.cancel()
		return false
	}
}

func (r *targetReservation) cancel() {
	if r == nil || r.queue == nil {
		return
	}
	q := r.queue
	q.mu.Lock()
	defer q.mu.Unlock()
	switch r.state {
	case reservationWaiting:
		for index, waiter := range q.waiters {
			if waiter == r {
				q.waiters = append(q.waiters[:index], q.waiters[index+1:]...)
				break
			}
		}
		r.state = reservationCanceled
		close(r.ready)
	case reservationGranted:
		r.state = reservationCanceled
		q.grantNextLocked()
	}
}

func (r *targetReservation) release() {
	if r == nil || r.queue == nil {
		return
	}
	q := r.queue
	q.mu.Lock()
	defer q.mu.Unlock()
	if r.state != reservationGranted {
		return
	}
	r.state = reservationReleased
	q.grantNextLocked()
}

func (q *targetQueue) grantNextLocked() {
	for len(q.waiters) > 0 {
		next := q.waiters[0]
		q.waiters = q.waiters[1:]
		if next.state != reservationWaiting {
			continue
		}
		next.state = reservationGranted
		close(next.ready)
		return
	}
	q.active = false
}

type nextFireUpdate struct {
	id       string
	nextFire time.Time
	// suppressedSlot is set when this tick found the invocation still in
	// flight, so the due slot was dropped instead of dispatched.
	suppressedSlot *time.Time
}

type suppressedSlot struct {
	id   string
	slot time.Time
}

func (s *scheduler) runScheduledBatch(ctx context.Context, batch scheduledDispatchBatch) {
	defer s.dispatchWG.Done()
	if !batch.reservation.acquire(ctx) {
		for _, invocation := range batch.invocations {
			s.markInvocationIdle(invocation.entry)
		}
		return
	}
	defer batch.reservation.release()

	for index, invocation := range batch.invocations {
		if ctx.Err() != nil {
			for _, pending := range batch.invocations[index:] {
				s.markInvocationIdle(pending.entry)
			}
			break
		}
		result := s.executeDispatch(ctx, invocation.entry, invocation.fireAt, dispatchOptions{})
		if result.canceled {
			s.logf("schedule %s: wake canceled", invocation.entry.manifest.ID)
			for _, pending := range batch.invocations[index:] {
				s.markInvocationIdle(pending.entry)
			}
			break
		}
		s.completeScheduledDispatch(invocation.entry, invocation.fireAt, result)
		if ctx.Err() != nil {
			for _, pending := range batch.invocations[index+1:] {
				s.markInvocationIdle(pending.entry)
			}
			break
		}
	}
}

func (s *scheduler) completeScheduledDispatch(entry *scheduledInvocation, fireAt time.Time, result dispatchResult) {
	defer s.markInvocationIdle(entry)
	s.mu.Lock()
	entry.lastFireUTC = fireAt
	entry.lastStatus = result.status
	entry.lastDetail = result.detail
	nextFire := entry.nextFireUTC
	s.mu.Unlock()

	if err := s.persistDispatchResult(entry, fireAt, nextFire, result); err != nil {
		s.logf("schedule %s: persist state failed: %v", entry.manifest.ID, err)
	}
}

func (s *scheduler) executeDispatch(ctx context.Context, entry *scheduledInvocation, fireAt time.Time, opts dispatchOptions) dispatchResult {
	if s.dispatchFn != nil {
		return s.dispatchFn(ctx, entry, fireAt, opts)
	}
	return s.dispatchWithOptions(ctx, entry, fireAt, opts)
}

func (s *scheduler) targetQueueLocked(target string) *targetQueue {
	key := strings.TrimSpace(target)
	if s.targetQueues == nil {
		s.targetQueues = make(map[string]*targetQueue)
	}
	if s.targetQueues[key] == nil {
		s.targetQueues[key] = &targetQueue{}
	}
	return s.targetQueues[key]
}

func (s *scheduler) claimManualReservation(entry *scheduledInvocation) (*targetReservation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stopping {
		return nil, context.Canceled
	}
	if entry == nil || entry.inFlight {
		return nil, errScheduleInvocationInFlight
	}
	entry.inFlight = true
	return s.targetQueueLocked(entry.manifest.Wake.Target).reserve(), nil
}

func (s *scheduler) markInvocationIdle(entry *scheduledInvocation) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if entry != nil {
		entry.inFlight = false
	}
}

func (s *scheduler) stopScheduledDispatches() {
	s.mu.Lock()
	if !s.stopping {
		s.stopping = true
		s.stopCancel()
	}
	s.mu.Unlock()
}

func (s *scheduler) Wait(ctx context.Context) error {
	if s == nil {
		return nil
	}
	select {
	case <-s.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *scheduler) dispatchWithOptions(ctx context.Context, entry *scheduledInvocation, fireAt time.Time, opts dispatchOptions) dispatchResult {
	if err := ctx.Err(); err != nil {
		return dispatchResult{status: "wake-canceled", detail: err.Error(), canceled: true}
	}
	var state schedulepkg.InvocationState
	if s.state != nil {
		state, _ = s.state.Invocation(entry.manifest.ID)
	}
	result := dispatchResult{}
	bypassedPause := false
	bypassedWhen := false
	if state.Paused {
		if state.PausedUntil != nil && !fireAt.Before(state.PausedUntil.UTC()) {
			result.clearPause = true
			state.Paused = false
			state.PausedUntil = nil
			state.PauseReason = ""
		} else if opts.bypassPause {
			bypassedPause = true
		} else {
			result.status = "skipped"
			result.detail = "paused-by-operator"
			result.skipped = true
			s.logf("schedule %s: skipped (%s)", entry.manifest.ID, result.detail)
			return result
		}
	}
	if state.SkipNext && !opts.ignoreSkipNext {
		result.status = "skipped"
		result.detail = "skip-next"
		result.skipped = true
		result.clearSkipNext = true
		s.logf("schedule %s: skipped (%s)", entry.manifest.ID, result.detail)
		return result
	}

	when := entry.manifest.When
	if when != nil {
		cal, err := schedulepkg.LookupCalendar(when.Calendar)
		if err != nil {
			detail := err.Error()
			s.logf("schedule %s: wake skipped: %s", entry.manifest.ID, detail)
			result.status = "calendar-error"
			result.detail = detail
			return result
		}
		state := cal.SessionAt(fireAt)
		if !state.Matches(when.SessionOrDefault()) {
			if opts.bypassWhen {
				bypassedWhen = true
			} else {
				detail := state.Reason
				if detail == "" {
					detail = "calendar-closed"
				}
				s.logf("schedule %s: skipped (%s)", entry.manifest.ID, detail)
				result.status = "skipped"
				result.detail = detail
				result.skipped = true
				return result
			}
		}
	}
	if state.Degraded && !opts.ignoreDegraded && !shouldAttemptDegraded(entry.manifest.ID, fireAt) {
		result.status = "skipped"
		result.detail = "degraded-throttled"
		result.skipped = true
		s.logf("schedule %s: skipped (%s)", entry.manifest.ID, result.detail)
		return result
	}

	target, err := s.lookupTargetContainer(ctx, entry.manifest.Wake.Target)
	if err != nil {
		detail := err.Error()
		if ctx.Err() != nil {
			result.status = "wake-canceled"
			result.detail = detail
			result.canceled = true
			return result
		}
		s.logf("schedule %s: wake target error: %s", entry.manifest.ID, detail)
		result.status = "wake-target-error"
		result.detail = detail
		result.failure = true
		return result
	}

	if detail, skip := s.deferWakeForHealth(ctx, target.ID, entry.manifest.Wake.Adapter); skip {
		result.status = "skipped"
		result.detail = detail
		result.skipped = true
		s.logf("schedule %s: skipped (%s)", entry.manifest.ID, result.detail)
		return result
	}

	execCtx, cancel := context.WithTimeout(ctx, wakeExecTimeout(entry.manifest.Wake.Adapter))
	defer cancel()
	stdout, stderr, exitCode, err := shared.ExecInContainer(execCtx, s.docker, target.ID, entry.manifest.Wake.Command)
	if err != nil {
		detail := err.Error()
		if ctx.Err() != nil {
			result.status = "wake-canceled"
			result.detail = detail
			result.canceled = true
			return result
		}
		s.logf("schedule %s: wake failed: %s", entry.manifest.ID, detail)
		result.status = "wake-error"
		result.detail = detail
		result.attempted = true
		result.failure = true
		return result
	}
	if exitCode != 0 {
		detail := strings.TrimSpace(stderr)
		if detail == "" {
			detail = strings.TrimSpace(stdout)
		}
		if detail == "" {
			detail = fmt.Sprintf("exit code %d", exitCode)
		}
		s.logf("schedule %s: wake exited %d: %s", entry.manifest.ID, exitCode, detail)
		result.status = "wake-exit-nonzero"
		result.detail = detail
		result.attempted = true
		result.failure = true
		return result
	}

	s.logf("schedule %s: fired via %s -> %s", entry.manifest.ID, entry.manifest.Wake.Adapter, composeServiceName(target))
	result.status = "fired"
	if opts.manual {
		switch {
		case bypassedWhen:
			result.status = "manual-fire-bypass"
		case bypassedPause:
			result.status = "manual-fire-paused"
		default:
			result.status = "manual-fire"
		}
	}
	result.detail = strings.TrimSpace(stdout)
	result.attempted = true
	result.fired = true
	return result
}

func wakeExecTimeout(adapter string) time.Duration {
	switch strings.TrimSpace(adapter) {
	case "openclaw-exec":
		return openclawWakeExecTimeout
	default:
		return defaultWakeExecTimeout
	}
}

func nextScheduledFireUTC(schedule cron.Schedule, now time.Time, location *time.Location) time.Time {
	if schedule == nil {
		return time.Time{}
	}
	if location != nil {
		now = now.In(location)
	}
	next := schedule.Next(now)
	if next.IsZero() {
		return time.Time{}
	}
	return next.UTC()
}

func deferWakeForHealthStatus(adapter string, state *types.ContainerState) (string, bool) {
	if strings.TrimSpace(adapter) != "openclaw-exec" || state == nil || state.Health == nil {
		return "", false
	}
	status := strings.ToLower(strings.TrimSpace(state.Health.Status))
	switch status {
	case "", "healthy":
		return "", false
	case "starting":
		return "target-health-starting", true
	default:
		return "target-health-" + status, true
	}
}

func (s *scheduler) deferWakeForHealth(ctx context.Context, containerID, adapter string) (string, bool) {
	if s == nil || s.docker == nil {
		return "", false
	}
	info, err := s.docker.ContainerInspect(ctx, containerID)
	if err != nil {
		return "", false
	}
	return deferWakeForHealthStatus(adapter, info.State)
}

func (s *scheduler) lookupTargetContainer(ctx context.Context, target string) (types.Container, error) {
	if s != nil && s.lookupFn != nil {
		return s.lookupFn(ctx, target)
	}
	if s == nil || s.docker == nil {
		return types.Container{}, fmt.Errorf("docker client unavailable")
	}
	args := filters.NewArgs(filters.Arg("label", "claw.pod="+s.manifest.Pod))
	containers, err := s.docker.ContainerList(ctx, containerapi.ListOptions{All: true, Filters: args})
	if err != nil {
		return types.Container{}, err
	}
	matches := matchServiceContainers(containers, target)
	if len(matches) == 0 {
		return types.Container{}, fmt.Errorf("no container found for target %q", target)
	}
	for _, ctr := range matches {
		if strings.EqualFold(ctr.State, "running") {
			return ctr, nil
		}
	}
	return matches[0], nil
}

func (s *scheduler) FireNow(ctx context.Context, id string, bypassWhen, bypassPause bool) (dispatchResult, error) {
	if s == nil {
		return dispatchResult{}, fmt.Errorf("scheduler unavailable")
	}
	entry := s.lookupEntry(id)
	if entry == nil {
		return dispatchResult{}, fmt.Errorf("schedule %q not found", id)
	}
	reservation, err := s.claimManualReservation(entry)
	if err != nil {
		if errors.Is(err, errScheduleInvocationInFlight) {
			return dispatchResult{}, fmt.Errorf("%w: %s", err, id)
		}
		return dispatchResult{}, err
	}
	defer s.markInvocationIdle(entry)
	dispatchCtx, cancel := context.WithCancel(ctx)
	stopCancel := context.AfterFunc(s.stopCtx, cancel)
	defer func() {
		stopCancel()
		cancel()
	}()
	if !reservation.acquire(dispatchCtx) {
		if err := dispatchCtx.Err(); err != nil {
			return dispatchResult{}, err
		}
		return dispatchResult{}, context.Canceled
	}
	defer reservation.release()

	fireAt := s.now()
	result := s.executeDispatch(dispatchCtx, entry, fireAt, dispatchOptions{
		manual:         true,
		bypassPause:    bypassPause,
		bypassWhen:     bypassWhen,
		ignoreSkipNext: true,
		ignoreDegraded: true,
	})
	if result.canceled {
		if err := dispatchCtx.Err(); err != nil {
			return dispatchResult{}, err
		}
		return dispatchResult{}, context.Canceled
	}

	s.mu.Lock()
	entry.lastFireUTC = fireAt
	entry.lastStatus = result.status
	entry.lastDetail = result.detail
	nextFire := entry.nextFireUTC
	s.mu.Unlock()

	if err := s.persistDispatchResult(entry, fireAt, nextFire, result); err != nil {
		s.logf("schedule %s: persist state failed: %v", entry.manifest.ID, err)
	}
	return result, nil
}

func (s *scheduler) lookupEntry(id string) *scheduledInvocation {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, entry := range s.entries {
		if entry != nil && entry.manifest.ID == id {
			return entry
		}
	}
	return nil
}

func (s *scheduler) logf(format string, args ...any) {
	if s == nil || s.log == nil {
		return
	}
	s.logMu.Lock()
	defer s.logMu.Unlock()
	fmt.Fprintf(s.log, "claw-api scheduler: "+format+"\n", args...)
}

func (s *scheduler) syncInitialState() error {
	if s == nil || s.state == nil {
		return nil
	}
	return s.state.Update(func(file *schedulepkg.StateFile) {
		for _, entry := range s.entries {
			state := file.Invocations[entry.manifest.ID]
			if state.LastStatus == "" {
				state.LastStatus = "scheduled"
			}
			nextFire := entry.nextFireUTC
			if nextFire.IsZero() {
				state.NextFireAt = nil
				if state.LastStatus == "scheduled" {
					state.LastStatus = "schedule-exhausted"
					state.LastDetail = "schedule has no future fire time"
				}
			} else {
				state.NextFireAt = &nextFire
			}
			file.Invocations[entry.manifest.ID] = state
		}
	})
}

func (s *scheduler) persistNextFireUpdates(updates []nextFireUpdate) error {
	if s == nil || s.state == nil || len(updates) == 0 {
		return nil
	}
	return s.state.Update(func(file *schedulepkg.StateFile) {
		for _, update := range updates {
			state := file.Invocations[update.id]
			setNextFireMonotonic(&state, update.nextFire)
			if update.suppressedSlot != nil {
				state.SuppressedSlots++
				slot := update.suppressedSlot.UTC()
				state.LastSuppressedAt = &slot
			}
			file.Invocations[update.id] = state
		}
	})
}

func (s *scheduler) persistDispatchResult(entry *scheduledInvocation, fireAt, nextFire time.Time, result dispatchResult) error {
	if s == nil || s.state == nil {
		return nil
	}
	return s.state.Update(func(file *schedulepkg.StateFile) {
		state := file.Invocations[entry.manifest.ID]
		if result.clearPause {
			state.Paused = false
			state.PausedUntil = nil
			state.PauseReason = ""
		}
		if result.clearSkipNext {
			state.SkipNext = false
		}
		evaluatedAt := fireAt.UTC()
		state.LastEvaluatedAt = &evaluatedAt
		setNextFireMonotonic(&state, nextFire)
		state.LastStatus = result.status
		state.LastDetail = result.detail
		if result.skipped {
			skippedAt := fireAt.UTC()
			state.LastSkippedAt = &skippedAt
		}
		if result.attempted {
			attemptedAt := fireAt.UTC()
			state.LastAttemptedAt = &attemptedAt
		}
		if result.fired {
			firedAt := fireAt.UTC()
			state.LastFiredAt = &firedAt
			state.ConsecutiveFailures = 0
			state.Degraded = false
		} else if result.failure {
			state.ConsecutiveFailures++
			if state.ConsecutiveFailures >= 3 {
				state.Degraded = true
			}
		}
		file.Invocations[entry.manifest.ID] = state
	})
}

func setNextFireMonotonic(state *schedulepkg.InvocationState, nextFire time.Time) {
	if nextFire.IsZero() {
		state.NextFireAt = nil
		return
	}
	next := nextFire.UTC()
	if state.NextFireAt == nil || next.After(state.NextFireAt.UTC()) {
		state.NextFireAt = &next
	}
}

func nextSchedulerDelay(now time.Time) time.Duration {
	next := now.UTC().Truncate(time.Minute).Add(time.Minute)
	delay := next.Sub(now.UTC())
	if delay <= 0 {
		return time.Minute
	}
	return delay
}

func shouldAttemptDegraded(invocationID string, fireAt time.Time) bool {
	slot := fireAt.UTC().Unix() / 60
	hasher := fnv.New32a()
	_, _ = hasher.Write([]byte(invocationID))
	_, _ = hasher.Write([]byte{':'})
	_, _ = hasher.Write([]byte(fmt.Sprintf("%d", slot)))
	return hasher.Sum32()%10 == 0
}
