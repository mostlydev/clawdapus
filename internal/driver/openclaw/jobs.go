package openclaw

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/mostlydev/clawdapus/internal/driver"
)

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
	jobs := make([]job, 0, len(rc.Invocations))
	for _, inv := range rc.Invocations {
		name := inv.Name
		if name == "" {
			name = truncate(inv.Message, 60)
		}
		j := job{
			ID:            deterministicJobID(rc.ServiceName, inv.Schedule, inv.Message),
			AgentID:       "main",
			Name:          name,
			Enabled:       true,
			CreatedAtMs:   now,
			UpdatedAtMs:   now,
			Schedule:      jobSchedule{Expr: inv.Schedule, TZ: "UTC", Kind: "cron"},
			SessionTarget: "isolated",
			WakeMode:      "now",
			Payload:       jobPayload{Kind: "agentTurn", Message: inv.Message, TimeoutSeconds: 300},
			Delivery:      jobDelivery{Mode: "announce", BestEffort: true, To: inv.To},
			State:         jobState{},
		}
		jobs = append(jobs, j)
	}
	return json.MarshalIndent(jobs, "", "  ")
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
