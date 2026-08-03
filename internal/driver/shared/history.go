package shared

import (
	"fmt"
	"os"
	"path/filepath"
)

type portableHistoryDirMapping struct {
	srcPath string
	dstRel  string
}

type portableHistoryFileMapping struct {
	srcPath string
	dstRel  string
}

// PreparePortableHistory creates a read-oriented cross-driver history archive
// for one service and opportunistically imports native runner session stores.
// It is intentionally additive: existing files win, and imported history keeps
// runner-specific formats under subdirectories.
func PreparePortableHistory(stateDir string, extraImportRoots ...string) (string, error) {
	historyDir := filepath.Join(stateDir, "history")
	if err := os.MkdirAll(historyDir, 0o777); err != nil {
		return "", fmt.Errorf("create portable history dir: %w", err)
	}

	for _, root := range uniqueImportRoots(stateDir, extraImportRoots...) {
		for _, mapping := range legacyPortableHistoryDirs(root) {
			if err := MergeTree(filepath.Join(historyDir, mapping.dstRel), mapping.srcPath); err != nil {
				return "", err
			}
		}
		for _, mapping := range legacyPortableHistoryFiles(root) {
			if err := importPortableHistoryFile(historyDir, mapping); err != nil {
				return "", err
			}
		}
	}

	return historyDir, nil
}

func legacyPortableHistoryDirs(root string) []portableHistoryDirMapping {
	return []portableHistoryDirMapping{
		{srcPath: filepath.Join(root, "hermes-home", "sessions"), dstRel: filepath.Join("hermes", "sessions")},
		{srcPath: filepath.Join(root, "state", "sessions"), dstRel: filepath.Join("openclaw", "sessions")},
		{srcPath: filepath.Join(root, "state", "agents"), dstRel: filepath.Join("openclaw", "agents")},
		{srcPath: filepath.Join(root, "openclaw-state", "sessions"), dstRel: filepath.Join("openclaw", "sessions")},
		{srcPath: filepath.Join(root, "openclaw-state", "agents"), dstRel: filepath.Join("openclaw", "agents")},
		{srcPath: filepath.Join(root, "nanobot-home", "workspace", "sessions"), dstRel: filepath.Join("nanobot", "sessions")},
		{srcPath: filepath.Join(root, "nanobot-home", "sessions"), dstRel: filepath.Join("nanobot", "sessions")},
		{srcPath: filepath.Join(root, "picoclaw-home", "workspace", "sessions"), dstRel: filepath.Join("picoclaw", "sessions")},
		{srcPath: filepath.Join(root, "picoclaw-home", "sessions"), dstRel: filepath.Join("picoclaw", "sessions")},
	}
}

func legacyPortableHistoryFiles(root string) []portableHistoryFileMapping {
	return []portableHistoryFileMapping{
		{srcPath: filepath.Join(root, "hermes-home", "state.db"), dstRel: filepath.Join("hermes", "state.db")},
		{srcPath: filepath.Join(root, "hermes-home", "state.db-shm"), dstRel: filepath.Join("hermes", "state.db-shm")},
		{srcPath: filepath.Join(root, "hermes-home", "state.db-wal"), dstRel: filepath.Join("hermes", "state.db-wal")},
		{srcPath: filepath.Join(root, "hermes-home", "gateway_state.json"), dstRel: filepath.Join("hermes", "gateway_state.json")},
		{srcPath: filepath.Join(root, "hermes-home", "channel_directory.json"), dstRel: filepath.Join("hermes", "channel_directory.json")},
	}
}

func importPortableHistoryFile(historyDir string, mapping portableHistoryFileMapping) error {
	info, err := os.Stat(mapping.srcPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("stat legacy history file %q: %w", mapping.srcPath, err)
	}
	if info.IsDir() || !info.Mode().IsRegular() {
		return nil
	}

	return copyMissingFile(filepath.Join(historyDir, mapping.dstRel), mapping.srcPath, info.Mode().Perm())
}
