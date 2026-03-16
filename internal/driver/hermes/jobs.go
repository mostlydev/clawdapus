package hermes

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/mostlydev/clawdapus/internal/driver"
	"github.com/mostlydev/clawdapus/internal/driver/shared"
	"github.com/robfig/cron/v3"
)

type jobsFile struct {
	Jobs      []job  `json:"jobs"`
	UpdatedAt string `json:"updated_at"`
}

type job struct {
	ID              string      `json:"id"`
	Name            string      `json:"name"`
	Prompt          string      `json:"prompt"`
	Skills          []string    `json:"skills"`
	Skill           *string     `json:"skill"`
	Model           *string     `json:"model"`
	Provider        *string     `json:"provider"`
	BaseURL         *string     `json:"base_url"`
	Schedule        schedule    `json:"schedule"`
	ScheduleDisplay string      `json:"schedule_display"`
	Repeat          repeatState `json:"repeat"`
	Enabled         bool        `json:"enabled"`
	State           string      `json:"state"`
	PausedAt        *string     `json:"paused_at"`
	PausedReason    *string     `json:"paused_reason"`
	CreatedAt       string      `json:"created_at"`
	NextRunAt       string      `json:"next_run_at"`
	LastRunAt       *string     `json:"last_run_at"`
	LastStatus      *string     `json:"last_status"`
	LastError       *string     `json:"last_error"`
	Deliver         string      `json:"deliver"`
	Origin          any         `json:"origin"`
}

type schedule struct {
	Kind    string `json:"kind"`
	Expr    string `json:"expr"`
	Display string `json:"display"`
}

type repeatState struct {
	Times     *int `json:"times"`
	Completed int  `json:"completed"`
}

func GenerateJobsJSON(rc *driver.ResolvedClaw) ([]byte, error) {
	now := time.Now().UTC()
	file := jobsFile{
		Jobs:      make([]job, 0, len(rc.Invocations)),
		UpdatedAt: now.Format(time.RFC3339),
	}

	for i, inv := range rc.Invocations {
		expr := strings.TrimSpace(inv.Schedule)
		if !shared.IsFiveFieldCron(expr) {
			return nil, fmt.Errorf("invocation %d has invalid cron expression %q (expected 5 fields)", i+1, inv.Schedule)
		}

		message := strings.TrimSpace(inv.Message)
		if message == "" {
			return nil, fmt.Errorf("invocation %d has empty message", i+1)
		}

		nextRunAt, err := nextRunAt(expr, now)
		if err != nil {
			return nil, fmt.Errorf("invocation %d next run: %w", i+1, err)
		}

		name := strings.TrimSpace(inv.Name)
		if name == "" {
			name = fmt.Sprintf("invoke-%02d", i+1)
		}

		deliver := deliverTarget(rc.Handles, strings.TrimSpace(inv.To))
		file.Jobs = append(file.Jobs, job{
			ID:              deterministicJobID(rc.ServiceName, expr, message, deliver),
			Name:            name,
			Prompt:          message,
			Skills:          []string{},
			Schedule:        schedule{Kind: "cron", Expr: expr, Display: expr},
			ScheduleDisplay: expr,
			Repeat:          repeatState{Times: nil, Completed: 0},
			Enabled:         true,
			State:           "scheduled",
			CreatedAt:       now.Format(time.RFC3339),
			NextRunAt:       nextRunAt,
			Deliver:         deliver,
			Origin:          nil,
		})
	}

	return json.MarshalIndent(file, "", "  ")
}

func deliverTarget(handles map[string]*driver.HandleInfo, to string) string {
	if to == "" {
		return "local"
	}

	platform := ""
	for _, candidate := range supportedPlatforms {
		if _, ok := handles[candidate]; !ok {
			continue
		}
		if platform != "" {
			return "local"
		}
		platform = candidate
	}
	if platform == "" {
		return "local"
	}
	return platform + ":" + to
}

func nextRunAt(expr string, now time.Time) (string, error) {
	schedule, err := cron.ParseStandard(expr)
	if err != nil {
		return "", fmt.Errorf("parse cron %q: %w", expr, err)
	}
	return schedule.Next(now).UTC().Format(time.RFC3339), nil
}

func deterministicJobID(parts ...string) string {
	h := sha256.Sum256([]byte(strings.Join(parts, "|")))
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		h[0:4], h[4:6], h[6:8], h[8:10], h[10:16])
}
