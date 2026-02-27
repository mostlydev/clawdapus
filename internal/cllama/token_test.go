package cllama

import (
	"strings"
	"testing"
)

func TestGenerateTokenFormat(t *testing.T) {
	tok := GenerateToken("tiverton")
	parts := strings.SplitN(tok, ":", 2)
	if len(parts) != 2 {
		t.Fatalf("expected token with 2 parts, got %q", tok)
	}
	if parts[0] != "tiverton" {
		t.Errorf("expected agent-id prefix, got %q", parts[0])
	}
	if len(parts[1]) != 48 {
		t.Errorf("expected 48-char hex secret, got %d", len(parts[1]))
	}
}

func TestGenerateTokenUnique(t *testing.T) {
	a := GenerateToken("bot")
	b := GenerateToken("bot")
	if a == b {
		t.Error("tokens should be unique")
	}
}
