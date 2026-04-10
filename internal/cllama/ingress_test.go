package cllama

import (
	"reflect"
	"testing"
)

func TestNormalizeProviderID(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{in: "google", want: "google"},
		{in: "z.ai", want: "zai"},
		{in: "Z-AI", want: "zai"},
		{in: "qwen", want: "qwen-portal"},
		{in: "kimi-code", want: "kimi-coding"},
		{in: "doubao", want: "volcengine"},
	}

	for _, tc := range tests {
		if got := NormalizeProviderID(tc.in); got != tc.want {
			t.Fatalf("NormalizeProviderID(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestSplitProviderModelRefDefaultsBareModelsToAnthropic(t *testing.T) {
	provider, modelID, ok := SplitProviderModelRef("claude-sonnet-4")
	if !ok {
		t.Fatal("expected bare model ref to parse")
	}
	if provider != "anthropic" || modelID != "claude-sonnet-4" {
		t.Fatalf("got provider=%q model=%q", provider, modelID)
	}
}

func TestProviderQualifiedModelRefNormalizesProviderAliases(t *testing.T) {
	provider, modelRef, ok := ProviderQualifiedModelRef("qwen/qwen3-235b-a22b")
	if !ok {
		t.Fatal("expected aliased provider ref to parse")
	}
	if provider != "qwen-portal" {
		t.Fatalf("provider = %q, want qwen-portal", provider)
	}
	if modelRef != "qwen-portal/qwen3-235b-a22b" {
		t.Fatalf("modelRef = %q, want qwen-portal/qwen3-235b-a22b", modelRef)
	}
}

func TestCollectProviderModelsDeduplicatesAndSorts(t *testing.T) {
	models := map[string]string{
		"primary":   "google/gemini-2.5-flash",
		"fallback":  "anthropic/claude-sonnet-4",
		"secondary": "google/gemini-2.5-pro",
		"cheap":     "google/gemini-2.5-flash",
	}

	got, err := CollectProviderModels(models)
	if err != nil {
		t.Fatalf("CollectProviderModels() unexpected error: %v", err)
	}
	want := map[string][]string{
		"anthropic": {"anthropic/claude-sonnet-4"},
		"google":    {"google/gemini-2.5-flash", "google/gemini-2.5-pro"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("CollectProviderModels() = %#v, want %#v", got, want)
	}
}

func TestSplitProviderModelRefRejectsSyntheticIngressProviderPrefix(t *testing.T) {
	if provider, modelID, ok := SplitProviderModelRef("cllama/google/gemini-3-flash-preview"); ok {
		t.Fatalf("expected synthetic ingress provider prefix to be rejected, got provider=%q model=%q", provider, modelID)
	}
}

func TestCollectProviderModelsRejectsSyntheticIngressProviderPrefix(t *testing.T) {
	_, err := CollectProviderModels(map[string]string{
		"primary": "cllama/google/gemini-3-flash-preview",
	})
	if err == nil {
		t.Fatal("expected synthetic ingress provider prefix to fail")
	}
	if err.Error() != `invalid cllama provider/model ref for slot "primary": "cllama/google/gemini-3-flash-preview"` {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestIngressSurfaceForProvider(t *testing.T) {
	tests := []struct {
		provider string
		want     IngressSurface
		path     string
	}{
		{provider: "google", want: IngressSurfaceOpenAIChatCompletions, path: "/v1/chat/completions"},
		{provider: "openrouter", want: IngressSurfaceOpenAIChatCompletions, path: "/v1/chat/completions"},
		{provider: "anthropic", want: IngressSurfaceAnthropicMessages, path: "/v1/messages"},
		{provider: "kimi-code", want: IngressSurfaceAnthropicMessages, path: "/v1/messages"},
	}

	for _, tc := range tests {
		got := IngressSurfaceForProvider(tc.provider)
		if got != tc.want {
			t.Fatalf("IngressSurfaceForProvider(%q) = %q, want %q", tc.provider, got, tc.want)
		}
		if got.RequestPath() != tc.path {
			t.Fatalf("IngressSurfaceForProvider(%q).RequestPath() = %q, want %q", tc.provider, got.RequestPath(), tc.path)
		}
	}
}
