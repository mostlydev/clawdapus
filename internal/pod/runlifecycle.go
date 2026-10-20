package pod

import (
	"fmt"
	"strings"
	"time"
)

type rawRunLimits struct {
	MaxRuntime     string `yaml:"max-runtime"`
	MaxAuthority   string `yaml:"max-authority"`
	MaxTaskBytes   int    `yaml:"max-task-bytes"`
	MaxResultBytes int    `yaml:"max-result-bytes"`
	Memory         string `yaml:"memory"`
	Pids           int    `yaml:"pids"`
}

// Defaults for per-run templates. max-authority bounds how far ahead an
// orchestrator may set authority_expires_at for a single launch or renewal.
const (
	DefaultRunMaxRuntime     = 30 * time.Minute
	DefaultRunMaxAuthority   = 2 * time.Hour
	DefaultRunMaxTaskBytes   = 32 << 10
	DefaultRunMaxResultBytes = 256 << 10
	DefaultRunMemory         = "1g"
	DefaultRunPids           = 256

	maxRunTaskBytesCeiling   = 100 << 10
	maxRunResultBytesCeiling = 4 << 20
)

func parseRunLifecycle(rawLifecycle string, raw *rawRunLimits) (string, *RunLimits, error) {
	lifecycle := strings.TrimSpace(rawLifecycle)
	switch lifecycle {
	case "", "service":
		if raw != nil {
			return "", nil, fmt.Errorf("x-claw.run requires x-claw.lifecycle: %s", LifecyclePerRun)
		}
		return "", nil, nil
	case LifecyclePerRun:
	default:
		return "", nil, fmt.Errorf("x-claw.lifecycle: unknown value %q (expected service or %s)", rawLifecycle, LifecyclePerRun)
	}

	limits := &RunLimits{
		MaxRuntime:     DefaultRunMaxRuntime,
		MaxAuthority:   DefaultRunMaxAuthority,
		MaxTaskBytes:   DefaultRunMaxTaskBytes,
		MaxResultBytes: DefaultRunMaxResultBytes,
		Memory:         DefaultRunMemory,
		Pids:           DefaultRunPids,
	}
	if raw == nil {
		return lifecycle, limits, nil
	}
	if strings.TrimSpace(raw.MaxRuntime) != "" {
		d, err := parsePositiveDuration("x-claw.run.max-runtime", raw.MaxRuntime)
		if err != nil {
			return "", nil, err
		}
		limits.MaxRuntime = d
	}
	if strings.TrimSpace(raw.MaxAuthority) != "" {
		d, err := parsePositiveDuration("x-claw.run.max-authority", raw.MaxAuthority)
		if err != nil {
			return "", nil, err
		}
		limits.MaxAuthority = d
	}
	if raw.MaxTaskBytes != 0 {
		if raw.MaxTaskBytes < 0 || raw.MaxTaskBytes > maxRunTaskBytesCeiling {
			return "", nil, fmt.Errorf("x-claw.run.max-task-bytes must be between 1 and %d", maxRunTaskBytesCeiling)
		}
		limits.MaxTaskBytes = raw.MaxTaskBytes
	}
	if raw.MaxResultBytes != 0 {
		if raw.MaxResultBytes < 0 || raw.MaxResultBytes > maxRunResultBytesCeiling {
			return "", nil, fmt.Errorf("x-claw.run.max-result-bytes must be between 1 and %d", maxRunResultBytesCeiling)
		}
		limits.MaxResultBytes = raw.MaxResultBytes
	}
	if strings.TrimSpace(raw.Memory) != "" {
		limits.Memory = strings.TrimSpace(raw.Memory)
	}
	if raw.Pids != 0 {
		if raw.Pids < 0 {
			return "", nil, fmt.Errorf("x-claw.run.pids must be positive")
		}
		limits.Pids = raw.Pids
	}
	return lifecycle, limits, nil
}

func parsePositiveDuration(field, raw string) (time.Duration, error) {
	d, err := time.ParseDuration(strings.TrimSpace(raw))
	if err != nil || d <= 0 {
		return 0, fmt.Errorf("%s must be a positive duration (e.g. 30m), got %q", field, raw)
	}
	return d, nil
}

// validatePerRunService rejects declarations a one-shot per-run worker cannot
// honour. Workers have no channel identity, schedule, mounts, persona or
// persistent memory; every capability reaches them through cllama.
func validatePerRunService(svc *Service) error {
	if !svc.IsPerRun() {
		return nil
	}
	c := svc.Claw
	switch {
	case c.MCPStdio != nil:
		return fmt.Errorf("x-claw.lifecycle: %s cannot be combined with mcp-stdio", LifecyclePerRun)
	case c.Count > 1:
		return fmt.Errorf("x-claw.lifecycle: %s does not support count; each run is its own worker", LifecyclePerRun)
	case len(c.Handles) > 0:
		return fmt.Errorf("x-claw.lifecycle: %s does not support handles", LifecyclePerRun)
	case len(c.Invoke) > 0:
		return fmt.Errorf("x-claw.lifecycle: %s does not support invoke; runs are launched by the run supervisor", LifecyclePerRun)
	case c.Memory != nil:
		return fmt.Errorf("x-claw.lifecycle: %s does not support memory (set memory: null to drop pod defaults)", LifecyclePerRun)
	case len(c.Surfaces) > 0:
		return fmt.Errorf("x-claw.lifecycle: %s does not support surfaces; tools are reached through cllama (set surfaces: [] to drop pod defaults)", LifecyclePerRun)
	case len(c.Skills) > 0:
		return fmt.Errorf("x-claw.lifecycle: %s does not support skills yet", LifecyclePerRun)
	case strings.TrimSpace(c.Persona) != "":
		return fmt.Errorf("x-claw.lifecycle: %s does not support persona", LifecyclePerRun)
	case c.ClawAPIMode != "":
		return fmt.Errorf("x-claw.lifecycle: %s does not support claw-api", LifecyclePerRun)
	}
	for _, key := range []string{"volumes", "ports", "container_name", "privileged", "pid", "network_mode", "devices", "cap_add"} {
		if _, ok := svc.Compose[key]; ok {
			return fmt.Errorf("x-claw.lifecycle: %s does not allow compose key %q; the worker container is generated", LifecyclePerRun, key)
		}
	}
	return nil
}
