package cllama

import (
	"sort"
	"strings"
)

type IngressSurface string

const (
	IngressSurfaceOpenAIChatCompletions IngressSurface = "openai-chat-completions"
	IngressSurfaceAnthropicMessages     IngressSurface = "anthropic-messages"
)

// NormalizeProviderID canonicalizes provider aliases that Clawdapus accepts in
// operator-facing model refs before they are compiled to cllama-facing config.
func NormalizeProviderID(provider string) string {
	normalized := strings.ToLower(strings.TrimSpace(provider))
	switch normalized {
	case "z.ai", "z-ai":
		return "zai"
	case "opencode-zen":
		return "opencode"
	case "qwen":
		return "qwen-portal"
	case "kimi-code":
		return "kimi-coding"
	case "bytedance", "doubao":
		return "volcengine"
	default:
		return normalized
	}
}

// SplitProviderModelRef splits a provider/model ref and normalizes the provider
// to the canonical ID Clawdapus uses for cllama wiring.
func SplitProviderModelRef(ref string) (string, string, bool) {
	trimmed := strings.TrimSpace(ref)
	if trimmed == "" {
		return "", "", false
	}

	parts := strings.SplitN(trimmed, "/", 2)
	provider := "anthropic"
	modelID := trimmed
	if len(parts) == 2 {
		provider = parts[0]
		modelID = parts[1]
	}

	provider = NormalizeProviderID(provider)
	modelID = strings.TrimSpace(modelID)
	if provider == "" || modelID == "" {
		return "", "", false
	}
	return provider, modelID, true
}

// ProviderQualifiedModelRef returns the normalized provider plus a canonical
// provider-prefixed model ref for use in cllama-facing runner config.
func ProviderQualifiedModelRef(ref string) (string, string, bool) {
	provider, modelID, ok := SplitProviderModelRef(ref)
	if !ok {
		return "", "", false
	}
	return provider, provider + "/" + modelID, true
}

// CollectProviderModels groups declared model refs by normalized provider and
// emits deterministic provider-prefixed model IDs.
func CollectProviderModels(models map[string]string) map[string][]string {
	byProvider := make(map[string]map[string]struct{})
	for _, rawRef := range models {
		provider, modelRef, ok := ProviderQualifiedModelRef(rawRef)
		if !ok {
			continue
		}
		if _, exists := byProvider[provider]; !exists {
			byProvider[provider] = make(map[string]struct{})
		}
		byProvider[provider][modelRef] = struct{}{}
	}

	out := make(map[string][]string, len(byProvider))
	for provider, ids := range byProvider {
		modelIDs := make([]string, 0, len(ids))
		for id := range ids {
			modelIDs = append(modelIDs, id)
		}
		sort.Strings(modelIDs)
		out[provider] = modelIDs
	}
	return out
}

// IngressSurfaceForProvider returns the canonical cllama ingress surface a
// runner should target for the given provider when cllama is enabled.
func IngressSurfaceForProvider(provider string) IngressSurface {
	switch NormalizeProviderID(provider) {
	case "anthropic", "synthetic", "minimax-portal", "kimi-coding", "cloudflare-ai-gateway", "xiaomi":
		return IngressSurfaceAnthropicMessages
	default:
		return IngressSurfaceOpenAIChatCompletions
	}
}

// RequestPath returns the canonical HTTP path for the ingress surface.
func (surface IngressSurface) RequestPath() string {
	switch surface {
	case IngressSurfaceAnthropicMessages:
		return "/v1/messages"
	default:
		return "/v1/chat/completions"
	}
}
