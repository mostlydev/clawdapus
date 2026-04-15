package openclaw

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/mostlydev/clawdapus/internal/driver"
)

const defaultInvocationTimeoutSeconds = 300

type job struct {
	ID            string      `json:"id"`
	AgentID       string      `json:"agentId"`
	Name          string      `json:"name"`
	Enabled       bool        `json:"enabled"`
	CreatedAtMs   int64       `json:"createdAtMs"`
	UpdatedAtMs   int64       `json:"updatedAtMs"`
	Schedule      jobSchedule `json:"schedule"`
	SessionTarget string      `json:"sessionTarget"`
	WakeMode      string      `json:"wakeMode"`
	Payload       jobPayload  `json:"payload"`
	Delivery      jobDelivery `json:"delivery"`
	State         jobState    `json:"state"`
}

type jobStore struct {
	Version int   `json:"version"`
	Jobs    []job `json:"jobs"`
}

type jobSchedule struct {
	Expr string `json:"expr"`
	TZ   string `json:"tz"`
	Kind string `json:"kind"`
}

type jobPayload struct {
	Kind           string `json:"kind"`
	Message        string `json:"message"`
	TimeoutSeconds int    `json:"timeoutSeconds"`
}

type jobDelivery struct {
	Mode       string `json:"mode"`
	BestEffort bool   `json:"bestEffort"`
	To         string `json:"to,omitempty"` // omit when empty → openclaw uses last channel
}

type jobState struct {
	NextRunAtMs       int64  `json:"nextRunAtMs"`
	LastRunAtMs       int64  `json:"lastRunAtMs"`
	LastStatus        string `json:"lastStatus"`
	LastDurationMs    int64  `json:"lastDurationMs"`
	ConsecutiveErrors int    `json:"consecutiveErrors"`
}

// GenerateJobsJSON produces the openclaw cron/jobs.json content for rc.Invocations.
// IDs are deterministic: same service + schedule + message always produces the same ID,
// so re-running claw up is idempotent.
func GenerateJobsJSON(rc *driver.ResolvedClaw) ([]byte, error) {
	now := time.Now().UnixMilli()
	timeoutSeconds, err := resolvedInvocationTimeoutSeconds(rc)
	if err != nil {
		return nil, err
	}
	jobs := make([]job, 0, len(rc.Invocations))
	timezone := strings.TrimSpace(rc.Timezone)
	if timezone == "" {
		timezone = "UTC"
	}
	for _, inv := range rc.Invocations {
		name := inv.Name
		if name == "" {
			name = truncate(inv.Message, 60)
		}
		id := strings.TrimSpace(inv.ID)
		if id == "" {
			id = deterministicJobID(rc.ServiceName, inv.Schedule, inv.Message)
		}
		j := job{
			ID:            id,
			AgentID:       "main",
			Name:          name,
			Enabled:       inv.Origin != driver.OriginPod,
			CreatedAtMs:   now,
			UpdatedAtMs:   now,
			Schedule:      jobSchedule{Expr: inv.Schedule, TZ: timezone, Kind: "cron"},
			SessionTarget: "isolated",
			WakeMode:      "now",
			Payload:       jobPayload{Kind: "agentTurn", Message: inv.Message, TimeoutSeconds: timeoutSeconds},
			Delivery:      jobDelivery{Mode: "announce", BestEffort: true, To: inv.To},
			State:         jobState{},
		}
		jobs = append(jobs, j)
	}
	return json.MarshalIndent(jobStore{
		Version: 1,
		Jobs:    jobs,
	}, "", "  ")
}

func resolvedInvocationTimeoutSeconds(rc *driver.ResolvedClaw) (int, error) {
	timeoutSeconds := defaultInvocationTimeoutSeconds
	if rc == nil {
		return timeoutSeconds, nil
	}
	for _, cmd := range rc.Configures {
		path, value, err := parseConfigSetCommand(cmd)
		if err != nil {
			return 0, fmt.Errorf("jobs generation: %w", err)
		}
		if path != "agents.defaults.timeoutSeconds" {
			continue
		}
		timeoutSeconds, err = positiveIntConfigValue(path, value)
		if err != nil {
			return 0, fmt.Errorf("jobs generation: %w", err)
		}
	}
	return timeoutSeconds, nil
}

func positiveIntConfigValue(path string, value any) (int, error) {
	switch v := value.(type) {
	case float64:
		parsed := int(v)
		if float64(parsed) != v || parsed <= 0 {
			return 0, fmt.Errorf("%s must be a positive integer, got %v", path, value)
		}
		return parsed, nil
	case string:
		parsed, err := strconv.Atoi(strings.TrimSpace(v))
		if err != nil || parsed <= 0 {
			return 0, fmt.Errorf("%s must be a positive integer, got %q", path, v)
		}
		return parsed, nil
	default:
		return 0, fmt.Errorf("%s must be a positive integer, got %T", path, value)
	}
}

func deterministicJobID(parts ...string) string {
	h := sha256.Sum256([]byte(strings.Join(parts, "|")))
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		h[0:4], h[4:6], h[6:8], h[8:10], h[10:16])
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
