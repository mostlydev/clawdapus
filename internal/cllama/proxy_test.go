package cllama

import "testing"

func TestProxyServiceName(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{in: "", want: "cllama"},
		{in: "passthrough", want: "cllama"},
		{in: "PASSTHROUGH", want: "cllama"},
		{in: "policy", want: "cllama-policy"},
	}
	for _, tc := range tests {
		if got := ProxyServiceName(tc.in); got != tc.want {
			t.Fatalf("ProxyServiceName(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestProxyImageRef(t *testing.T) {
	if got := ProxyImageRef("passthrough"); got != "ghcr.io/mostlydev/cllama:latest" {
		t.Fatalf("passthrough image = %q", got)
	}
	if got := ProxyImageRef("policy"); got != "ghcr.io/mostlydev/cllama-policy:latest" {
		t.Fatalf("policy image = %q", got)
	}
}

func TestProxyHealthcheckBinary(t *testing.T) {
	if got := ProxyHealthcheckBinary("passthrough"); got != "/cllama" {
		t.Fatalf("passthrough binary = %q", got)
	}
	if got := ProxyHealthcheckBinary("policy"); got != "/cllama-policy" {
		t.Fatalf("policy binary = %q", got)
	}
}

func TestProxyBaseURL(t *testing.T) {
	if got := ProxyBaseURL("passthrough"); got != "http://cllama:8080/v1" {
		t.Fatalf("passthrough base URL = %q", got)
	}
	if got := ProxyBaseURL("policy"); got != "http://cllama-policy:8080/v1" {
		t.Fatalf("policy base URL = %q", got)
	}
}
