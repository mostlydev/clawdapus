package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

const skillMarkerFile = "skill-installed"

type skillMarker struct {
	Version     string    `json:"version"`
	InstalledAt time.Time `json:"installed_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

func skillMarkerPath(home string) string {
	return filepath.Join(home, ".claw", skillMarkerFile)
}

func readSkillMarker(home string) *skillMarker {
	data, err := os.ReadFile(skillMarkerPath(home))
	if err != nil {
		return nil
	}
	var m skillMarker
	if err := json.Unmarshal(data, &m); err != nil {
		return nil
	}
	return &m
}

func writeSkillMarker(home string) error {
	path := skillMarkerPath(home)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}

	existing := readSkillMarker(home)
	m := skillMarker{
		Version:     version,
		InstalledAt: time.Now(),
		UpdatedAt:   time.Now(),
	}
	if existing != nil {
		m.InstalledAt = existing.InstalledAt
	}

	data, err := json.Marshal(m)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

// maybeSyncSkill silently updates installed skill files when the binary
// version is newer than the installed version. Called on every claw invocation.
func maybeSyncSkill() {
	if version == "dev" {
		return
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return
	}

	marker := readSkillMarker(home)
	if marker == nil {
		return // not installed, nothing to sync
	}

	if marker.Version == version {
		return // already up to date
	}

	content, err := embeddedSkillContent()
	if err != nil {
		return
	}

	for _, dir := range skillTargetDirs(home) {
		_ = writeSkillFile(dir, content)
	}
	_ = writeSkillMarker(home)
}
