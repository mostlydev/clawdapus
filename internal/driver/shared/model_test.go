package shared

import (
	"reflect"
	"testing"
)

func TestSplitModelRef(t *testing.T) {
	tests := []struct {
		ref          string
		wantProvider string
		wantModel    string
		wantOK       bool
	}{
		{ref: "anthropic/claude-sonnet-4", wantProvider: "anthropic", wantModel: "claude-sonnet-4", wantOK: true},
		{ref: "gpt-4.1", wantProvider: "anthropic", wantModel: "gpt-4.1", wantOK: true},
		{ref: " OpenRouter / moonshotai/kimi-k2.5 ", wantProvider: "openrouter", wantModel: "moonshotai/kimi-k2.5", wantOK: true},
		{ref: "", wantOK: false},
		{ref: "provider/", wantOK: false},
		{ref: "/model", wantOK: false},
	}

	for _, tt := range tests {
		gotProvider, gotModel, gotOK := SplitModelRef(tt.ref)
		if gotProvider != tt.wantProvider || gotModel != tt.wantModel || gotOK != tt.wantOK {
			t.Fatalf("SplitModelRef(%q) = (%q, %q, %v), want (%q, %q, %v)", tt.ref, gotProvider, gotModel, gotOK, tt.wantProvider, tt.wantModel, tt.wantOK)
		}
	}
}

func TestCollectProviders(t *testing.T) {
	got := CollectProviders(map[string]string{
		"primary":  "openrouter/anthropic/claude-sonnet-4",
		"fallback": "anthropic/claude-3-5-haiku",
		"extra":    "ollama/qwen2.5:14b",
		"ignored":  "provider/",
	})
	want := []string{"anthropic", "ollama", "openrouter"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("CollectProviders() = %v, want %v", got, want)
	}
}

func TestExpectedProviderKeys(t *testing.T) {
	if got := ExpectedProviderKeys("anthropic"); !reflect.DeepEqual(got, []string{"ANTHROPIC_API_KEY", "PROVIDER_API_KEY_ANTHROPIC", "PROVIDER_API_KEY"}) {
		t.Fatalf("ExpectedProviderKeys(anthropic) = %v", got)
	}
	if got := ExpectedProviderKeys("openrouter"); !reflect.DeepEqual(got, []string{"OPENROUTER_API_KEY", "PROVIDER_API_KEY_OPENROUTER", "PROVIDER_API_KEY", "OPENAI_API_KEY"}) {
		t.Fatalf("ExpectedProviderKeys(openrouter) = %v", got)
	}
}

func TestResolveProviderAPIKey(t *testing.T) {
	env := map[string]string{
		"OPENAI_API_KEY":              "openai-key",
		"PROVIDER_API_KEY":            "fallback-key",
		"PROVIDER_API_KEY_OPENROUTER": "provider-openrouter-key",
	}
	if got := ResolveProviderAPIKey("openrouter", env); got != "provider-openrouter-key" {
		t.Fatalf("ResolveProviderAPIKey(openrouter) = %q", got)
	}
	if got := ResolveProviderAPIKey("anthropic", env); got != "fallback-key" {
		t.Fatalf("ResolveProviderAPIKey(anthropic) = %q", got)
	}
}

func TestResolveEnvToken(t *testing.T) {
	t.Setenv("TOKEN_A", "token-a")
	t.Setenv("TOKEN_B", "token-b")

	tests := []struct {
		raw  string
		want string
	}{
		{raw: "literal", want: "literal"},
		{raw: "$TOKEN_A", want: "token-a"},
		{raw: "${TOKEN_B}", want: "token-b"},
		{raw: "$BAD-NAME", want: "$BAD-NAME"},
		{raw: "${}", want: ""},
		{raw: "   ", want: ""},
	}

	for _, tt := range tests {
		if got := ResolveEnvToken(tt.raw); got != tt.want {
			t.Fatalf("ResolveEnvToken(%q) = %q, want %q", tt.raw, got, tt.want)
		}
	}
}
