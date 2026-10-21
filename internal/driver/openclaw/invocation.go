package openclaw

import (
	"fmt"
	"github.com/mostlydev/clawdapus/internal/driver"
	"strconv"
	"strings"
)

const defaultInvocationTimeoutSeconds = 300

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

// InvocationTimeoutSeconds preserves the configured native-turn budget when
// Clawdapus owns timing rather than relying on the retired native cron store.
func InvocationTimeoutSeconds(rc *driver.ResolvedClaw) (int, error) {
	n, err := resolvedInvocationTimeoutSeconds(rc)
	if err != nil {
		return 0, err
	}
	if n > 900 {
		return 0, fmt.Errorf("OpenClaw invocation timeout must not exceed 900 seconds")
	}
	return n, nil
}
