package initimport

import (
	"fmt"
	"regexp"
	"strings"
)

var envRefPattern = regexp.MustCompile(`^\$\{([A-Za-z_][A-Za-z0-9_]*)\}$`)

type envBuilder struct {
	values       map[string]string
	exampleKeys  map[string]struct{}
	secretNotes  []string
	unknownIndex int
}

func newEnvBuilder() *envBuilder {
	return &envBuilder{values: map[string]string{}, exampleKeys: map[string]struct{}{}}
}

func (b *envBuilder) addPlaceholder(key string) {
	key = strings.TrimSpace(key)
	if key == "" {
		return
	}
	b.values[key] = "${" + key + "}"
	b.exampleKeys[key] = struct{}{}
}

func (b *envBuilder) addLiteral(key, value string) {
	key = strings.TrimSpace(key)
	value = strings.TrimSpace(value)
	if key == "" || value == "" {
		return
	}
	b.values[key] = value
	b.exampleKeys[key] = struct{}{}
}

func (b *envBuilder) addSecret(preferredKey, raw, label string) string {
	raw = strings.TrimSpace(raw)
	preferredKey = strings.TrimSpace(preferredKey)
	if raw == "" {
		if preferredKey != "" {
			b.addPlaceholder(preferredKey)
			return preferredKey
		}
		return ""
	}
	if match := envRefPattern.FindStringSubmatch(raw); match != nil {
		b.addPlaceholder(match[1])
		return match[1]
	}
	key := preferredKey
	if key == "" {
		b.unknownIndex++
		key = fmt.Sprintf("UNKNOWN_TOKEN_%d", b.unknownIndex)
	}
	b.addPlaceholder(key)
	b.secretNotes = append(b.secretNotes, fmt.Sprintf("%s contained a literal secret; generated ${%s} placeholder", label, key))
	return key
}

func providerEnvKey(provider string) string {
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case "openrouter":
		return "OPENROUTER_API_KEY"
	case "anthropic":
		return "ANTHROPIC_API_KEY"
	case "openai":
		return "OPENAI_API_KEY"
	default:
		return ""
	}
}

func bestEffortProviderEnvKey(provider string) string {
	var b strings.Builder
	lastUnderscore := false
	for _, r := range strings.TrimSpace(provider) {
		switch {
		case r >= 'a' && r <= 'z':
			b.WriteRune(r - ('a' - 'A'))
			lastUnderscore = false
		case r >= 'A' && r <= 'Z' || r >= '0' && r <= '9':
			b.WriteRune(r)
			lastUnderscore = false
		default:
			if !lastUnderscore && b.Len() > 0 {
				b.WriteRune('_')
				lastUnderscore = true
			}
		}
	}
	name := strings.Trim(b.String(), "_")
	if name == "" {
		name = "CUSTOM_PROVIDER"
	}
	return name + "_API_KEY"
}
