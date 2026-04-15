package main

import (
	"context"
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

	mu      sync.RWMutex
	entries []*scheduledInvocation
}

type scheduledInvocation struct {
	manifest    schedulepkg.ManifestInvocation
	location    *time.Location
	schedule    cron.Schedule
	nextFireUTC time.Time
	lastFireUTC time.Time
	lastStatus  string
	lastDetail  string
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
}

type dispatchOptions struct {
	manual         bool
	bypassPause    bool
	bypassWhen     bool
	ignoreSkipNext bool
	ignoreDegraded bool
}

const defaultWakeExecTimeout = 30 * time.Second
const openclawWakeExecTimeout = 2 * time.Minute

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
			nextFireUTC: compiled.Next(now.In(location)).UTC(),
			lastStatus:  "scheduled",
		})
	}
	s := &scheduler{
		manifest: manifest,
		docker:   docker,
		log:      log,
		state:    state,
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
	s.logf("scheduler started with %d invocation(s)", len(s.entries))
	timer := time.NewTimer(nextSchedulerDelay(time.Now().UTC()))
	defer timer.Stop()

	s.tick(ctx, time.Now().UTC())

	for {
		select {
		case <-ctx.Done():
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
	entries := append([]*scheduledInvocation(nil), s.entries...)
	s.mu.Unlock()

	for _, entry := range entries {
		if entry == nil || now.Before(entry.nextFireUTC) {
			continue
		}
		fireAt := entry.nextFireUTC
		result := s.dispatch(ctx, entry, fireAt)

		s.mu.Lock()
		entry.lastFireUTC = fireAt
		entry.lastStatus = result.status
		entry.lastDetail = result.detail
		entry.nextFireUTC = entry.schedule.Next(now.In(entry.location)).UTC()
		s.mu.Unlock()

		s.persistDispatchResult(entry, fireAt, entry.nextFireUTC, result)
	}
}

func (s *scheduler) dispatch(ctx context.Context, entry *scheduledInvocation, fireAt time.Time) dispatchResult {
	return s.dispatchWithOptions(ctx, entry, fireAt, dispatchOptions{})
}

func (s *scheduler) dispatchWithOptions(ctx context.Context, entry *scheduledInvocation, fireAt time.Time, opts dispatchOptions) dispatchResult {
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
	fireAt := s.now()
	result := s.dispatchWithOptions(ctx, entry, fireAt, dispatchOptions{
		manual:         true,
		bypassPause:    bypassPause,
		bypassWhen:     bypassWhen,
		ignoreSkipNext: true,
		ignoreDegraded: true,
	})

	s.mu.Lock()
	entry.lastFireUTC = fireAt
	entry.lastStatus = result.status
	entry.lastDetail = result.detail
	nextFire := entry.nextFireUTC
	s.mu.Unlock()

	if nextFire.IsZero() {
		nextFire = fireAt
	}
	s.persistDispatchResult(entry, fireAt, nextFire, result)
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
			state.NextFireAt = &nextFire
			file.Invocations[entry.manifest.ID] = state
		}
	})
}

func (s *scheduler) persistDispatchResult(entry *scheduledInvocation, fireAt, nextFire time.Time, result dispatchResult) {
	if s == nil || s.state == nil {
		return
	}
	if err := s.state.Update(func(file *schedulepkg.StateFile) {
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
		next := nextFire.UTC()
		state.LastEvaluatedAt = &evaluatedAt
		state.NextFireAt = &next
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
	}); err != nil {
		s.logf("schedule %s: persist state failed: %v", entry.manifest.ID, err)
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
