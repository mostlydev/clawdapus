package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mostlydev/clawdapus/internal/driver"
)

func TestComposeUpRejectsFileFlagAndPositionalTogether(t *testing.T) {
	prev := composePodFile
	composePodFile = "a.yml"
	defer func() { composePodFile = prev }()

	err := composeUpCmd.RunE(composeUpCmd, []string{"b.yml"})
	if err == nil {
		t.Fatal("expected conflict error when both --file and positional pod file are set")
	}
	if !strings.Contains(err.Error(), "pod file specified twice") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestMergeResolvedSkills(t *testing.T) {
	imageSkills := []driver.ResolvedSkill{
		{Name: "agent.md", HostPath: "/host/image/agent.md"},
		{Name: "shared.md", HostPath: "/host/image/shared.md"},
	}
	podSkills := []driver.ResolvedSkill{
		{Name: "shared.md", HostPath: "/host/pod/shared.md"},
		{Name: "pod.md", HostPath: "/host/pod/pod.md"},
	}

	merged := mergeResolvedSkills(imageSkills, podSkills)
	if len(merged) != 3 {
		t.Fatalf("expected 3 skills, got %d", len(merged))
	}
	if merged[1].HostPath != "/host/pod/shared.md" {
		t.Fatalf("expected pod override for shared.md, got %q", merged[1].HostPath)
	}
	if merged[2].Name != "pod.md" {
		t.Fatalf("expected pod-level-only skill to be appended, got %q", merged[2].Name)
	}
}

func TestResolveSkillEmitWritesFile(t *testing.T) {
	tmpDir := t.TempDir()

	prevExtractor := extractServiceSkillFromImage
	prevWriter := writeRuntimeFile
	extractServiceSkillFromImage = func(_, _ string) ([]byte, error) {
		return []byte("# emitted\n"), nil
	}
	writeRuntimeFile = func(path string, data []byte, perm os.FileMode) error {
		return prevWriter(path, data, perm)
	}
	defer func() {
		extractServiceSkillFromImage = prevExtractor
		writeRuntimeFile = prevWriter
	}()

	skill, err := resolveSkillEmit("gateway", tmpDir, "claw/openclaw:latest", "/app/SKILL.md")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if skill == nil {
		t.Fatal("expected emitted skill resolution")
	}
	if skill.Name != "SKILL.md" {
		t.Errorf("expected SKILL.md, got %q", skill.Name)
	}
	if !strings.HasSuffix(skill.HostPath, filepath.Join("skills", "SKILL.md")) {
		t.Errorf("expected emitted skill in skills dir, got %q", skill.HostPath)
	}

	got, err := os.ReadFile(skill.HostPath)
	if err != nil {
		t.Fatalf("read emitted skill file: %v", err)
	}
	if string(got) != "# emitted\n" {
		t.Errorf("unexpected emitted skill content: %q", string(got))
	}
}

func TestResolveSkillEmitRejectsInvalidPath(t *testing.T) {
	tmpDir := t.TempDir()

	_, err := resolveSkillEmit("gateway", tmpDir, "claw/openclaw:latest", "/")
	if err == nil {
		t.Fatal("expected invalid emitted skill path error")
	}
}

func TestResolveSkillEmitFallsBackOnExtractionError(t *testing.T) {
	tmpDir := t.TempDir()

	prevExtractor := extractServiceSkillFromImage
	extractServiceSkillFromImage = func(_, _ string) ([]byte, error) {
		return nil, fmt.Errorf("image not found")
	}
	defer func() { extractServiceSkillFromImage = prevExtractor }()

	// Should return nil, nil — pod startup continues with fallback skill
	skill, err := resolveSkillEmit("gateway", tmpDir, "claw/openclaw:latest", "/app/SKILL.md")
	if err != nil {
		t.Fatalf("expected warn+fallback (nil error), got: %v", err)
	}
	if skill != nil {
		t.Errorf("expected nil skill on extraction failure, got %+v", skill)
	}
}

func TestMergedPortsDeduplication(t *testing.T) {
	expose := []string{"80", "443"}
	ports := []string{"443", "8080"}

	merged := mergedPorts(expose, ports)
	if len(merged) != 3 {
		t.Fatalf("expected 3 merged ports, got %d: %v", len(merged), merged)
	}
	seen := map[string]bool{}
	for _, p := range merged {
		if seen[p] {
			t.Errorf("duplicate port %q in merged result", p)
		}
		seen[p] = true
	}
}

func TestMergedPortsExposeOnly(t *testing.T) {
	merged := mergedPorts([]string{"80"}, nil)
	if len(merged) != 1 || merged[0] != "80" {
		t.Errorf("expected [80], got %v", merged)
	}
}

func TestMergedPortsPortsOnly(t *testing.T) {
	merged := mergedPorts(nil, []string{"443"})
	if len(merged) != 1 || merged[0] != "443" {
		t.Errorf("expected [443], got %v", merged)
	}
}

func TestResolveServiceGeneratedSkills(t *testing.T) {
	tmpDir := t.TempDir()
	surfaces := []driver.ResolvedSurface{
		{
			Scheme: "service",
			Target: "api-server",
			Ports:  []string{"8080"},
		},
		{
			Scheme: "service",
			Target: "db",
		},
		{
			Scheme: "channel",
			Target: "discord",
		},
	}

	skills, err := resolveServiceGeneratedSkills(tmpDir, surfaces)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(skills) != 2 {
		t.Fatalf("expected 2 generated skills, got %d", len(skills))
	}

	if skills[0].Name != "surface-api-server.md" && skills[1].Name != "surface-api-server.md" {
		t.Fatalf("expected generated skill for api-server, got %v", []string{skills[0].Name, skills[1].Name})
	}
	if skills[0].Name != "surface-db.md" && skills[1].Name != "surface-db.md" {
		t.Fatalf("expected generated skill for db, got %v", []string{skills[0].Name, skills[1].Name})
	}
}
