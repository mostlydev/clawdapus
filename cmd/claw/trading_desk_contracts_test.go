package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTradingDeskContractsDoNotContainInstructionPlaceholders(t *testing.T) {
	agentDir := filepath.Join("..", "..", "examples", "trading-desk", "agents")
	files := []string{
		"SYSTEMS-MONITOR.md",
		"SOCIAL-TRADER.md",
		"NEWS-ROUTER.md",
		"MACRO-TRADER.md",
		"VALUE-TRADER.md",
		"DESK-MANAGER.md",
		"MOMENTUM-TRADER.md",
	}

	for _, name := range files {
		path := filepath.Join(agentDir, name)
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		text := string(data)
		if strings.Contains(text, "Your operational instructions go here") {
			t.Fatalf("%s still contains placeholder instructions", path)
		}
		if strings.Contains(text, "<!--") {
			t.Fatalf("%s still contains unresolved template comments", path)
		}
	}
}
