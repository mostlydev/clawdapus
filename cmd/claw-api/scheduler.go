package main

import (
	"context"
	"fmt"
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

func newScheduler(manifest *schedulepkg.Manifest, docker *client.Client, log io.Writer) (*scheduler, error) {
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
	return &scheduler{
		manifest: manifest,
		docker:   docker,
		log:      log,
		entries:  entries,
	}, nil
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
		status, detail := s.dispatch(ctx, entry, fireAt)

		s.mu.Lock()
		entry.lastFireUTC = fireAt
		entry.lastStatus = status
		entry.lastDetail = detail
		entry.nextFireUTC = entry.schedule.Next(now.In(entry.location)).UTC()
		s.mu.Unlock()
	}
}

func (s *scheduler) dispatch(ctx context.Context, entry *scheduledInvocation, fireAt time.Time) (string, string) {
	when := entry.manifest.When
	if when != nil {
		cal, err := schedulepkg.LookupCalendar(when.Calendar)
		if err != nil {
			detail := err.Error()
			s.logf("schedule %s: wake skipped: %s", entry.manifest.ID, detail)
			return "calendar-error", detail
		}
		state := cal.SessionAt(fireAt)
		if !state.Matches(when.SessionOrDefault()) {
			detail := state.Reason
			if detail == "" {
				detail = "calendar-closed"
			}
			s.logf("schedule %s: skipped (%s)", entry.manifest.ID, detail)
			return "skipped", detail
		}
	}

	target, err := s.lookupTargetContainer(ctx, entry.manifest.Wake.Target)
	if err != nil {
		detail := err.Error()
		s.logf("schedule %s: wake target error: %s", entry.manifest.ID, detail)
		return "wake-target-error", detail
	}

	execCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	stdout, stderr, exitCode, err := shared.ExecInContainer(execCtx, s.docker, target.ID, entry.manifest.Wake.Command)
	if err != nil {
		detail := err.Error()
		s.logf("schedule %s: wake failed: %s", entry.manifest.ID, detail)
		return "wake-error", detail
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
		return "wake-exit-nonzero", detail
	}

	s.logf("schedule %s: fired via %s -> %s", entry.manifest.ID, entry.manifest.Wake.Adapter, composeServiceName(target))
	return "fired", strings.TrimSpace(stdout)
}

func (s *scheduler) lookupTargetContainer(ctx context.Context, target string) (types.Container, error) {
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

func (s *scheduler) logf(format string, args ...any) {
	if s == nil || s.log == nil {
		return
	}
	fmt.Fprintf(s.log, "claw-api scheduler: "+format+"\n", args...)
}

func nextSchedulerDelay(now time.Time) time.Duration {
	next := now.UTC().Truncate(time.Minute).Add(time.Minute)
	delay := next.Sub(now.UTC())
	if delay <= 0 {
		return time.Minute
	}
	return delay
}
