package shared

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

const (
	PortableMemoryDir  = "/claw/memory"
	PortableMemoryEnv  = "CLAW_MEMORY_DIR"
	PortableHistoryDir = "/claw/history"
	PortableHistoryEnv = "CLAW_HISTORY_DIR"
)

// ResolveStateDir picks the durable host state directory when one is provided,
// otherwise it falls back to the runtime directory for call sites that have not
// yet been wired for persistence-aware tests.
func ResolveStateDir(runtimeDir, stateDir string) string {
	if trimmed := strings.TrimSpace(stateDir); trimmed != "" {
		return trimmed
	}
	return runtimeDir
}

// MergeTree recursively copies regular files from srcDir into dstDir without
// overwriting files that already exist in dstDir. Symlinks and special files are
// ignored.
func MergeTree(dstDir, srcDir string) error {
	entries, err := os.ReadDir(srcDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("read source dir %q: %w", srcDir, err)
	}
	if err := os.MkdirAll(dstDir, 0o777); err != nil {
		return fmt.Errorf("create destination dir %q: %w", dstDir, err)
	}

	for _, entry := range entries {
		srcPath := filepath.Join(srcDir, entry.Name())
		dstPath := filepath.Join(dstDir, entry.Name())

		info, err := entry.Info()
		if err != nil {
			return fmt.Errorf("stat source entry %q: %w", srcPath, err)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			continue
		}
		if info.IsDir() {
			if err := MergeTree(dstPath, srcPath); err != nil {
				return err
			}
			continue
		}
		if !info.Mode().IsRegular() {
			continue
		}
		if err := copyMissingFile(dstPath, srcPath, info.Mode().Perm()); err != nil {
			return err
		}
	}

	return nil
}

func uniqueImportRoots(primary string, extras ...string) []string {
	roots := make([]string, 0, 1+len(extras))
	seen := make(map[string]struct{}, 1+len(extras))

	for _, root := range append([]string{primary}, extras...) {
		root = strings.TrimSpace(root)
		if root == "" {
			continue
		}
		cleaned := filepath.Clean(root)
		if _, ok := seen[cleaned]; ok {
			continue
		}
		seen[cleaned] = struct{}{}
		roots = append(roots, cleaned)
	}

	return roots
}

func copyMissingFile(dstPath, srcPath string, mode os.FileMode) error {
	if _, err := os.Stat(dstPath); err == nil {
		return nil
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("stat destination file %q: %w", dstPath, err)
	}

	src, err := os.Open(srcPath)
	if err != nil {
		return fmt.Errorf("open source file %q: %w", srcPath, err)
	}
	defer src.Close()

	if err := os.MkdirAll(filepath.Dir(dstPath), 0o777); err != nil {
		return fmt.Errorf("create destination parent for %q: %w", dstPath, err)
	}

	dst, err := os.OpenFile(dstPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return fmt.Errorf("create destination file %q: %w", dstPath, err)
	}

	if _, err := io.Copy(dst, src); err != nil {
		dst.Close()
		return fmt.Errorf("copy source file %q: %w", srcPath, err)
	}
	if err := dst.Close(); err != nil {
		return fmt.Errorf("close destination file %q: %w", dstPath, err)
	}

	return nil
}
