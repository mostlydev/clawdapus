package shared

import (
	"os"
	"sort"
	"strings"
)

// SplitModelRef splits provider/model refs and applies the default anthropic provider
// for bare model IDs.
func SplitModelRef(ref string) (string, string, bool) {
	trimmed := strings.TrimSpace(ref)
	if trimmed == "" {
		return "", "", false
	}

	parts := strings.SplitN(trimmed, "/", 2)
	if len(parts) == 1 {
		return "anthropic", parts[0], true
	}

	provider := strings.ToLower(strings.TrimSpace(parts[0]))
	model := strings.TrimSpace(parts[1])
	if provider == "" || model == "" {
		return "", "", false
	}
	return provider, model, true
}

// CollectProviders returns a sorted unique provider list from MODEL refs.
func CollectProviders(models map[string]string) []string {
	seen := make(map[string]struct{})
	for _, ref := range models {
		provider, _, ok := SplitModelRef(ref)
		if !ok {
			continue
		}
		seen[provider] = struct{}{}
	}

	out := make([]string, 0, len(seen))
	for provider := range seen {
		out = append(out, provider)
	}
	sort.Strings(out)
	return out
}

func NormalizeProvider(provider string) string {
	normalized := strings.ToLower(strings.TrimSpace(provider))
	if normalized == "" {
		return "openai"
	}
	return normalized
}

func ProviderAllowsEmptyAPIKey(provider string) bool {
	switch NormalizeProvider(provider) {
	case "ollama":
		return true
	default:
		return false
	}
}

func ExpectedProviderKeys(provider string) []string {
	p := NormalizeProvider(provider)
	sanitized := sanitizeProviderEnvSuffix(p)

	out := []string{sanitized + "_API_KEY"}
	out = append(out, "PROVIDER_API_KEY_"+sanitized)
	out = append(out, "PROVIDER_API_KEY")
	if p != "anthropic" {
		out = append(out, "OPENAI_API_KEY")
	}
	return dedupStrings(out)
}

func ResolveProviderAPIKey(provider string, env map[string]string) string {
	for _, key := range ExpectedProviderKeys(provider) {
		if token := ResolveEnvTokenFromMap(env, key); token != "" {
			return token
		}
	}
	return ""
}

func ResolveEnvTokenFromMap(env map[string]string, key string) string {
	if env == nil {
		return ""
	}
	return ResolveEnvToken(env[key])
}

func ResolveEnvToken(raw string) string {
	v := strings.TrimSpace(raw)
	if v == "" {
		return ""
	}

	if strings.HasPrefix(v, "${") && strings.HasSuffix(v, "}") {
		name := strings.TrimSpace(v[2 : len(v)-1])
		if name == "" {
			return ""
		}
		return strings.TrimSpace(os.Getenv(name))
	}
	if strings.HasPrefix(v, "$") {
		name := strings.TrimSpace(strings.TrimPrefix(v, "$"))
		if isEnvVarName(name) {
			return strings.TrimSpace(os.Getenv(name))
		}
	}
	return v
}

func isEnvVarName(s string) bool {
	if s == "" {
		return false
	}
	for i, r := range s {
		if i == 0 && !(r == '_' || (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z')) {
			return false
		}
		if !(r == '_' || (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9')) {
			return false
		}
	}
	return true
}

func sanitizeProviderEnvSuffix(provider string) string {
	if provider == "" {
		return "OPENAI"
	}
	s := strings.ToUpper(provider)
	s = strings.ReplaceAll(s, "-", "_")
	s = strings.ReplaceAll(s, ".", "_")
	s = strings.ReplaceAll(s, "/", "_")
	return s
}

func dedupStrings(in []string) []string {
	seen := make(map[string]struct{}, len(in))
	out := make([]string, 0, len(in))
	for _, v := range in {
		if _, ok := seen[v]; ok {
			continue
		}
		seen[v] = struct{}{}
		out = append(out, v)
	}
	return out
}
