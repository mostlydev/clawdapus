package main

import (
	"strings"
	"testing"
)

func TestParseClawTypeAcceptsSupportedValues(t *testing.T) {
	tests := []string{
		"openclaw",
		"nanoclaw",
		"microclaw",
		"nullclaw",
		"nanobot",
		"picoclaw",
		"generic",
	}

	for _, tt := range tests {
		got, err := parseClawType(tt)
		if err != nil {
			t.Fatalf("parseClawType(%q) returned error: %v", tt, err)
		}
		if got != tt {
			t.Fatalf("parseClawType(%q)=%q, want %q", tt, got, tt)
		}
	}
}

func TestParseClawTypeRejectsUnknownValue(t *testing.T) {
	_, err := parseClawType("unknown-runner")
	if err == nil {
		t.Fatal("expected parseClawType to reject unknown runner")
	}
	if !strings.Contains(err.Error(), "invalid claw type") {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(err.Error(), "nanobot") || !strings.Contains(err.Error(), "picoclaw") {
		t.Fatalf("expected error to list nanobot/picoclaw, got: %v", err)
	}
}

func TestDefaultBaseImageForClawType(t *testing.T) {
	tests := []struct {
		name     string
		clawType string
		want     string
	}{
		{name: "openclaw", clawType: "openclaw", want: "openclaw:latest"},
		{name: "nanoclaw", clawType: "nanoclaw", want: "node:22-slim"},
		{name: "microclaw", clawType: "microclaw", want: "node:22-slim"},
		{name: "nullclaw", clawType: "nullclaw", want: "node:22-slim"},
		{name: "nanobot", clawType: "nanobot", want: "nanobot:latest"},
		{name: "picoclaw", clawType: "picoclaw", want: "docker.io/sipeed/picoclaw:latest"},
		{name: "generic", clawType: "generic", want: "alpine:3.20"},
		{name: "unknown", clawType: "something-else", want: "alpine:3.20"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := defaultBaseImageForClawType(tt.clawType)
			if got != tt.want {
				t.Fatalf("defaultBaseImageForClawType(%q)=%q, want %q", tt.clawType, got, tt.want)
			}
		})
	}
}
