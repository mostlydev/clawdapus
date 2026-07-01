package main

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mostlydev/clawdapus/internal/driver"
)

func TestBuildRulesManifestEntriesFromResolvedIncludes(t *testing.T) {
	dir := t.TempDir()
	enforcePath := filepath.Join(dir, "risk-limits.md")
	guidePath := filepath.Join(dir, "workflow.md")
	referencePath := filepath.Join(dir, "reference.md")
	if err := os.WriteFile(enforcePath, []byte("No irreversible action without approval.\n"), 0o644); err != nil {
		t.Fatalf("write enforce include: %v", err)
	}
	if err := os.WriteFile(guidePath, []byte("Prefer concise rationale.\n\n"), 0o644); err != nil {
		t.Fatalf("write guide include: %v", err)
	}
	if err := os.WriteFile(referencePath, []byte("Background only.\n"), 0o644); err != nil {
		t.Fatalf("write reference include: %v", err)
	}

	got, err := buildRulesManifestEntries([]driver.ResolvedInclude{
		{ID: "risk_limits", Mode: "enforce", HostPath: enforcePath},
		{ID: "workflow", Mode: "guide", HostPath: guidePath},
		{ID: "background", Mode: "reference", HostPath: referencePath},
	})
	if err != nil {
		t.Fatalf("buildRulesManifestEntries: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected enforce+guide rules only, got %+v", got)
	}

	if got[0].ID != "include.risk_limits" || got[0].Mode != "enforce" || got[0].Source != "include:risk_limits" {
		t.Fatalf("unexpected enforce rule metadata: %+v", got[0])
	}
	if got[0].Text != "No irreversible action without approval." {
		t.Fatalf("unexpected enforce text: %q", got[0].Text)
	}
	if got[0].ContentSHA256 != sha256Hex(got[0].Text) {
		t.Fatalf("unexpected enforce digest: %q", got[0].ContentSHA256)
	}

	if got[1].ID != "include.workflow" || got[1].Mode != "guide" || got[1].Source != "include:workflow" {
		t.Fatalf("unexpected guide rule metadata: %+v", got[1])
	}
	if got[1].Text != "Prefer concise rationale." {
		t.Fatalf("unexpected guide text: %q", got[1].Text)
	}
	if got[1].ContentSHA256 != sha256Hex(got[1].Text) {
		t.Fatalf("unexpected guide digest: %q", got[1].ContentSHA256)
	}
}

func TestBuildRulesManifestEntriesRejectsOversizedRule(t *testing.T) {
	dir := t.TempDir()
	enforcePath := filepath.Join(dir, "too-large.md")
	if err := os.WriteFile(enforcePath, []byte(strings.Repeat("x", rulesManifestRuleMaxChars+1)), 0o644); err != nil {
		t.Fatalf("write enforce include: %v", err)
	}

	_, err := buildRulesManifestEntries([]driver.ResolvedInclude{{
		ID:       "large_policy",
		Mode:     "enforce",
		HostPath: enforcePath,
	}})
	if err == nil || !strings.Contains(err.Error(), "rule text length must be <=") {
		t.Fatalf("expected oversized rule error, got %v", err)
	}
}

func sha256Hex(text string) string {
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:])
}
