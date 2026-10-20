package pod

import (
	"fmt"
	"regexp"
	"sort"
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
	// Zero byte and pid limits mean "use the default"; negatives are refused.
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
		mem := strings.TrimSpace(raw.Memory)
		if !dockerMemoryRe.MatchString(mem) {
			return "", nil, fmt.Errorf("x-claw.run.memory must be a positive Docker memory size (e.g. 512m, 1g), got %q", raw.Memory)
		}
		limits.Memory = mem
	}
	if raw.Pids != 0 {
		if raw.Pids < 0 {
			return "", nil, fmt.Errorf("x-claw.run.pids must be positive")
		}
		limits.Pids = raw.Pids
	}
	return lifecycle, limits, nil
}

// parsePositiveDuration accepts whole-second durations of at least 1s, so the
// compiled template (which stores seconds) never truncates a limit to zero.
func parsePositiveDuration(field, raw string) (time.Duration, error) {
	d, err := time.ParseDuration(strings.TrimSpace(raw))
	if err != nil || d < time.Second || d%time.Second != 0 {
		return 0, fmt.Errorf("%s must be a whole number of seconds, at least 1s (e.g. 30m), got %q", field, raw)
	}
	return d, nil
}

var dockerMemoryRe = regexp.MustCompile(`^[1-9][0-9]*[bkmgBKMG]?$`)

var perRunComposeKeys = map[string]struct{}{"image": {}, "build": {}}

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
	// The worker container is generated from a fixed shape; any compose key
	// the renderer would not honour is refused rather than silently dropped.
	keys := make([]string, 0, len(svc.Compose))
	for key := range svc.Compose {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		if _, ok := perRunComposeKeys[key]; !ok {
			return fmt.Errorf("x-claw.lifecycle: %s does not allow compose key %q; the worker container is generated (allowed: image, build)", LifecyclePerRun, key)
		}
	}
	return nil
}
