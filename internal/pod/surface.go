package pod

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/mostlydev/clawdapus/internal/driver"
)

// ParseSurface parses a raw surface string like "volume://research-cache read-only"
// into a ResolvedSurface.
func ParseSurface(raw string) (driver.ResolvedSurface, error) {
	parts := strings.Fields(raw)
	if len(parts) == 0 {
		return driver.ResolvedSurface{}, fmt.Errorf("empty surface declaration")
	}

	parsed, err := url.Parse(parts[0])
	if err != nil {
		return driver.ResolvedSurface{}, fmt.Errorf("invalid surface URI %q: %w", parts[0], err)
	}

	scheme := parsed.Scheme
	target := parsed.Host
	if target == "" {
		target = parsed.Opaque
	}

	if scheme == "" || target == "" {
		return driver.ResolvedSurface{}, fmt.Errorf("surface URI %q must have scheme and target", parts[0])
	}

	accessMode := ""
	if len(parts) > 1 {
		accessMode = parts[1]
	}

	return driver.ResolvedSurface{
		Scheme:     scheme,
		Target:     target,
		AccessMode: accessMode,
	}, nil
}
