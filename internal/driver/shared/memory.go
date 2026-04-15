package shared

import (
	"fmt"
	"os"
	"path/filepath"
)

// PreparePortableMemory creates the canonical cross-driver memory directory for
// one service runtime and opportunistically imports memory from older layouts.
func PreparePortableMemory(stateDir string, extraImportRoots ...string) (string, error) {
	memoryDir := filepath.Join(stateDir, "memory")
	if err := os.MkdirAll(memoryDir, 0o777); err != nil {
		return "", fmt.Errorf("create portable memory dir: %w", err)
	}

	for _, root := range uniqueImportRoots(stateDir, extraImportRoots...) {
		for _, srcDir := range legacyPortableMemoryDirs(root) {
			if err := MergeTree(memoryDir, srcDir); err != nil {
				return "", err
			}
		}
		for _, srcFile := range legacyPortableMemoryFiles(root) {
			if err := importPortableMemoryFile(memoryDir, srcFile); err != nil {
				return "", err
			}
		}
	}

	for _, name := range []string{"MEMORY.md", "USER.md"} {
		path := filepath.Join(memoryDir, name)
		if _, err := os.Stat(path); err == nil {
			continue
		} else if !os.IsNotExist(err) {
			return "", fmt.Errorf("stat portable memory file %q: %w", path, err)
		}
		if err := os.WriteFile(path, nil, 0o666); err != nil {
			return "", fmt.Errorf("seed portable memory file %q: %w", path, err)
		}
	}
	if err := normalizePortableMemoryPermissions(memoryDir); err != nil {
		return "", err
	}

	return memoryDir, nil
}

func normalizePortableMemoryPermissions(memoryDir string) error {
	return filepath.Walk(memoryDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return fmt.Errorf("walk portable memory %q: %w", path, err)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return nil
		}
		var target os.FileMode
		var kind string
		switch {
		case info.IsDir():
			target = 0o777
			kind = "dir"
		case info.Mode().IsRegular():
			target = 0o666
			kind = "file"
		default:
			return nil
		}
		if info.Mode().Perm() == target {
			return nil
		}
		if err := os.Chmod(path, target); err != nil {
			return fmt.Errorf("chmod portable memory %s %q: %w", kind, path, err)
		}
		return nil
	})
}

func legacyPortableMemoryDirs(runtimeDir string) []string {
	return []string{
		filepath.Join(runtimeDir, "hermes-home", "memories"),
		filepath.Join(runtimeDir, "workspace", "memory"),
		filepath.Join(runtimeDir, "nanobot-home", "workspace", "memory"),
		filepath.Join(runtimeDir, "picoclaw-home", "workspace", "memory"),
		filepath.Join(runtimeDir, "data", "working_dir", "memory"),
	}
}

func legacyPortableMemoryFiles(runtimeDir string) []string {
	workspaceRoots := []string{
		filepath.Join(runtimeDir, "workspace"),
		filepath.Join(runtimeDir, "nanobot-home", "workspace"),
		filepath.Join(runtimeDir, "picoclaw-home", "workspace"),
		filepath.Join(runtimeDir, "data", "working_dir"),
	}

	files := make([]string, 0, len(workspaceRoots)*2)
	for _, root := range workspaceRoots {
		files = append(files,
			filepath.Join(root, "MEMORY.md"),
			filepath.Join(root, "USER.md"),
		)
	}
	return files
}

func importPortableMemoryFile(dstDir, srcPath string) error {
	info, err := os.Stat(srcPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("stat legacy memory file %q: %w", srcPath, err)
	}
	if info.IsDir() || !info.Mode().IsRegular() {
		return nil
	}

	dstPath := filepath.Join(dstDir, filepath.Base(srcPath))
	return copyMissingFile(dstPath, srcPath, info.Mode().Perm())
}
