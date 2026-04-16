package shared

import (
	"encoding/json"
	"fmt"
	"strings"
)

func SetPath(obj map[string]any, path string, value any) error {
	path = strings.TrimSpace(path)
	if path == "" {
		return fmt.Errorf("invalid empty config path")
	}

	parts := strings.Split(path, ".")
	current := obj
	for i, part := range parts {
		if part == "" {
			return fmt.Errorf("invalid config path %q", path)
		}

		if i == len(parts)-1 {
			if existing, exists := current[part]; exists {
				if _, isMap := existing.(map[string]any); isMap {
					return fmt.Errorf("path conflict at %q: cannot overwrite object with value", strings.Join(parts[:i+1], "."))
				}
			}
			current[part] = value
			return nil
		}

		nextRaw, exists := current[part]
		if !exists {
			next := make(map[string]any)
			current[part] = next
			current = next
			continue
		}

		next, ok := nextRaw.(map[string]any)
		if !ok {
			return fmt.Errorf("path conflict at %q: expected object, found %T", strings.Join(parts[:i+1], "."), nextRaw)
		}
		current = next
	}

	return nil
}

func ParseConfigSetCommand(line, driverPrefix string) (path string, value any, err error) {
	parts := strings.Fields(line)
	if len(parts) < 5 || parts[0] != driverPrefix || parts[1] != "config" || parts[2] != "set" {
		return "", nil, fmt.Errorf(
			"unrecognized CONFIGURE command: %q (expected '%s config set <path> <value>')",
			line,
			driverPrefix,
		)
	}

	args := parts[3:]
	for len(args) > 0 && isConfigSetFlag(args[0]) {
		args = args[1:]
	}
	for len(args) > 0 && isConfigSetFlag(args[len(args)-1]) {
		args = args[:len(args)-1]
	}
	if len(args) < 2 {
		return "", nil, fmt.Errorf("unrecognized CONFIGURE command: %q (expected '<path> <value>')", line)
	}

	path = strings.TrimSpace(args[0])
	if path == "" {
		return "", nil, fmt.Errorf("unrecognized CONFIGURE command: %q (expected non-empty path)", line)
	}

	valueText := strings.TrimSpace(strings.Join(args[1:], " "))
	if valueText == "" {
		return "", nil, fmt.Errorf("unrecognized CONFIGURE command: %q (expected non-empty value)", line)
	}

	var typed any
	if err := json.Unmarshal([]byte(valueText), &typed); err == nil {
		return path, typed, nil
	}

	return path, valueText, nil
}

func isConfigSetFlag(token string) bool {
	switch token {
	case "--json", "--strict-json":
		return true
	default:
		return false
	}
}

func PrimaryModelRef(models map[string]string) (string, error) {
	if models == nil {
		return "", fmt.Errorf("missing MODEL primary (set `MODEL primary <provider/model>` in Clawfile)")
	}
	if primary := strings.TrimSpace(models["primary"]); primary != "" {
		return primary, nil
	}
	return "", fmt.Errorf("missing MODEL primary (set `MODEL primary <provider/model>` in Clawfile)")
}
