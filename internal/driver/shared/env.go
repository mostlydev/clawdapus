package shared

import (
	"fmt"
	"os"
	"regexp"
	"strings"
)

var envPlaceholderPattern = regexp.MustCompile(`\$\{([^}]+)\}`)

// ResolveEnvTokenFromMapWithRuntimeEnv resolves a service environment value
// using the same interpolation context claw up passes to Compose: pod .env
// values plus process environment overrides.
func ResolveEnvTokenFromMapWithRuntimeEnv(env map[string]string, key string, runtimeEnv map[string]string) (string, error) {
	if env == nil {
		return "", nil
	}
	return ResolveEnvTokenWithRuntimeEnv(env[key], runtimeEnv)
}

func ResolveProviderAPIKeyWithRuntimeEnv(provider string, env map[string]string, runtimeEnv map[string]string) (string, error) {
	for _, key := range ExpectedProviderKeys(provider) {
		token, err := ResolveEnvTokenFromMapWithRuntimeEnv(env, key, runtimeEnv)
		if err != nil {
			return "", fmt.Errorf("%s: %w", key, err)
		}
		if token != "" {
			return token, nil
		}
	}
	return "", nil
}

func ResolveEnvTokenWithRuntimeEnv(raw string, runtimeEnv map[string]string) (string, error) {
	v := strings.TrimSpace(raw)
	if v == "" {
		return "", nil
	}

	if strings.HasPrefix(v, "${") && strings.HasSuffix(v, "}") {
		return expandEnvRefs(v, runtimeEnv)
	}
	if strings.HasPrefix(v, "$") {
		name := strings.TrimSpace(strings.TrimPrefix(v, "$"))
		if isEnvVarName(name) {
			value, ok := lookupRuntimeEnv(name, runtimeEnv)
			if !ok {
				return "", fmt.Errorf("unresolved environment variable %q", name)
			}
			return strings.TrimSpace(value), nil
		}
	}
	if strings.Contains(v, "${") {
		return expandEnvRefs(v, runtimeEnv)
	}
	return v, nil
}

func expandEnvRefs(value string, runtimeEnv map[string]string) (string, error) {
	var expandErr error
	expanded := envPlaceholderPattern.ReplaceAllStringFunc(value, func(match string) string {
		if expandErr != nil {
			return match
		}
		resolved, err := resolveEnvPlaceholder(match, runtimeEnv)
		if err != nil {
			expandErr = err
			return match
		}
		return resolved
	})
	if expandErr != nil {
		return "", expandErr
	}
	if unresolved := envPlaceholderPattern.FindString(expanded); unresolved != "" {
		return "", fmt.Errorf("unresolved environment placeholder %q", unresolved)
	}
	return strings.TrimSpace(expanded), nil
}

func resolveEnvPlaceholder(match string, runtimeEnv map[string]string) (string, error) {
	expr := strings.TrimSpace(match[2 : len(match)-1])
	name, op, operand, err := parseEnvPlaceholderExpr(expr)
	if err != nil {
		return "", err
	}

	value, isSet := lookupRuntimeEnv(name, runtimeEnv)
	nonEmpty := strings.TrimSpace(value) != ""

	resolveOperand := func() (string, error) {
		return expandEnvRefs(operand, runtimeEnv)
	}

	switch op {
	case "":
		if !isSet {
			return "", fmt.Errorf("unresolved environment variable %q", name)
		}
		return value, nil
	case ":-":
		if !isSet || !nonEmpty {
			return resolveOperand()
		}
		return value, nil
	case "-":
		if !isSet {
			return resolveOperand()
		}
		return value, nil
	case ":?":
		if !isSet || !nonEmpty {
			msg := strings.TrimSpace(operand)
			if msg == "" {
				msg = fmt.Sprintf("%s is required", name)
			}
			return "", fmt.Errorf("%s", msg)
		}
		return value, nil
	case "?":
		if !isSet {
			msg := strings.TrimSpace(operand)
			if msg == "" {
				msg = fmt.Sprintf("%s is required", name)
			}
			return "", fmt.Errorf("%s", msg)
		}
		return value, nil
	case ":+":
		if isSet && nonEmpty {
			return resolveOperand()
		}
		return "", nil
	case "+":
		if isSet {
			return resolveOperand()
		}
		return "", nil
	default:
		return "", fmt.Errorf("unsupported environment placeholder operator %q", op)
	}
}

func parseEnvPlaceholderExpr(expr string) (name, op, operand string, err error) {
	expr = strings.TrimSpace(expr)
	if expr == "" {
		return "", "", "", fmt.Errorf("empty environment placeholder")
	}

	for _, candidate := range []string{":-", ":?", ":+", "-", "?", "+"} {
		if idx := strings.Index(expr, candidate); idx > 0 {
			name = strings.TrimSpace(expr[:idx])
			op = candidate
			operand = expr[idx+len(candidate):]
			break
		}
	}
	if name == "" {
		name = expr
	}
	if !isEnvVarName(name) {
		return "", "", "", fmt.Errorf("invalid environment placeholder %q", expr)
	}
	return name, op, operand, nil
}

func lookupRuntimeEnv(name string, runtimeEnv map[string]string) (string, bool) {
	if runtimeEnv != nil {
		value, ok := runtimeEnv[name]
		return value, ok
	}
	return os.LookupEnv(name)
}
