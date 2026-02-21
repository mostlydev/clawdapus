package runtime

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolveSkillsBasicCase(t *testing.T) {
	tmpDir := t.TempDir()
	skillDir := filepath.Join(tmpDir, "skills")
	if err := os.MkdirAll(skillDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "workflow.md"), []byte("# Workflow"), 0644); err != nil {
		t.Fatal(err)
	}

	skills, err := ResolveSkills(tmpDir, []string{"./skills/workflow.md"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(skills) != 1 {
		t.Fatalf("expected 1 skill, got %d", len(skills))
	}
	if skills[0].Name != "workflow.md" {
		t.Errorf("expected name=workflow.md, got %q", skills[0].Name)
	}
	if !filepath.IsAbs(skills[0].HostPath) {
		t.Errorf("expected absolute host path, got %q", skills[0].HostPath)
	}
}

func TestResolveSkillsRejectsMissingFile(t *testing.T) {
	tmpDir := t.TempDir()
	_, err := ResolveSkills(tmpDir, []string{"./skills/nonexistent.md"})
	if err == nil {
		t.Fatal("expected error for missing skill file")
	}
}

func TestResolveSkillsRejectsPathTraversal(t *testing.T) {
	tmpDir := t.TempDir()
	_, err := ResolveSkills(tmpDir, []string{"../../etc/passwd"})
	if err == nil {
		t.Fatal("expected error for path traversal")
	}
	if !strings.Contains(err.Error(), "escapes") {
		t.Fatalf("expected escapes error, got: %v", err)
	}
}

func TestResolveSkillsRejectsDuplicateBasenames(t *testing.T) {
	tmpDir := t.TempDir()
	dirA := filepath.Join(tmpDir, "a")
	dirB := filepath.Join(tmpDir, "b")
	os.MkdirAll(dirA, 0755)
	os.MkdirAll(dirB, 0755)
	os.WriteFile(filepath.Join(dirA, "same.md"), []byte("# A"), 0644)
	os.WriteFile(filepath.Join(dirB, "same.md"), []byte("# B"), 0644)

	_, err := ResolveSkills(tmpDir, []string{"./a/same.md", "./b/same.md"})
	if err == nil {
		t.Fatal("expected error for duplicate basenames")
	}
	if !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("expected duplicate error, got: %v", err)
	}
}

func TestResolveSkillsRejectsDirectory(t *testing.T) {
	tmpDir := t.TempDir()
	skillDir := filepath.Join(tmpDir, "skills")
	if err := os.MkdirAll(skillDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(skillDir, "not-a-file"), 0755); err != nil {
		t.Fatal(err)
	}

	_, err := ResolveSkills(tmpDir, []string{"./skills/not-a-file"})
	if err == nil {
		t.Fatal("expected error for skill path that is a directory")
	}
	if !strings.Contains(err.Error(), "not a regular file") {
		t.Fatalf("expected directory error, got: %v", err)
	}
}

func TestResolveSkillsRejectsSymlinkOutsideBase(t *testing.T) {
	tmpDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(tmpDir, "skills"), 0755); err != nil {
		t.Fatal(err)
	}

	outsideDir := t.TempDir()
	outside := filepath.Join(outsideDir, "outside-skill.md")
	if err := os.WriteFile(outside, []byte("# outside"), 0644); err != nil {
		t.Fatal(err)
	}

	linkPath := filepath.Join(tmpDir, "skills", "escaped.md")
	if err := os.Symlink(outside, linkPath); err != nil {
		t.Skipf("symlink not supported: %v", err)
	}

	_, err := ResolveSkills(tmpDir, []string{"./skills/escaped.md"})
	if err == nil {
		t.Fatal("expected error for symlink that escapes base directory")
	}
	if !strings.Contains(err.Error(), "escapes base directory") {
		t.Fatalf("expected escapes error, got: %v", err)
	}
}

func TestResolveSkillsEmptyList(t *testing.T) {
	tmpDir := t.TempDir()
	skills, err := ResolveSkills(tmpDir, []string{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if skills == nil {
		t.Fatal("expected non-nil empty slice, got nil")
	}
	if len(skills) != 0 {
		t.Errorf("expected 0 skills, got %d", len(skills))
	}
}
